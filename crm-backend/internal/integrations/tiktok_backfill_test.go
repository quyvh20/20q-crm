package integrations

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"crm-backend/internal/integrations/envelope"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// The export CSV, in the shape TikTok's docs publish it: ten fixed metadata columns,
// then the form's OWN question labels.
const tiktokExportCSV = `lead id,created_time,ad_id,ad_name,adgroup_id,adgroup_name,campaign_id,campaign_name,form_id,form_name,Name,Phone number,Email,Please select the service you are interested in?
7012345678901234567,2026-06-01 09:00:00,9012,Spring Ad,5678,Spring Group,1234,Spring,1700000000000001,Spring Form,Jane Doe,15088888888,jane@example.com,Roofing
7012345678901234568,2026-06-02 10:00:00,9012,Spring Ad,5678,Spring Group,1234,Spring,1700000000000001,Spring Form,John Roe,15099999999,john@example.com,Siding
`

// fakeTikTokExport serves the task + download endpoints. pollsBeforeSucceed models a
// task that is still building, which is the normal case for a real export.
type fakeTikTokExport struct {
	server *httptest.Server

	pollsBeforeSucceed int
	polls              int
	csv                string
	failRegion         string // this region's task reports FAILED
	downloadJSONError  bool
	hugeBody           bool

	sawRegions   []string // x-lead-region on each task call ("" for none)
	sawDownloads []string
}

func newFakeTikTokExport(t *testing.T) *fakeTikTokExport {
	t.Helper()
	f := &fakeTikTokExport{csv: tiktokExportCSV}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		region := r.Header.Get("x-lead-region")
		switch {
		case strings.HasSuffix(r.URL.Path, "/page/lead/task/download/"):
			f.sawDownloads = append(f.sawDownloads, region)
			if f.downloadJSONError {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":40000,"message":"failed to download.","data":{}}`))
				return
			}
			w.Header().Set("Content-Type", "text/csv")
			if f.hugeBody {
				_, _ = w.Write([]byte(strings.Repeat("x", tiktokExportLimit+1024)))
				return
			}
			_, _ = w.Write([]byte(f.csv))
		case strings.HasSuffix(r.URL.Path, "/page/lead/task/"):
			f.sawRegions = append(f.sawRegions, region)
			status := "SUCCEED"
			if region == f.failRegion && f.failRegion != "" {
				status = "FAILED"
			} else if f.polls < f.pollsBeforeSucceed {
				status = "RUNNING"
				f.polls++
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0, "message": "OK",
				"data": map[string]any{"status": status, "task_id": "task-" + region},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeTikTokExport) provider() *TikTokProvider {
	p := NewTikTokProvider("app1", "secret", "https://auth.example", "https://crm.example/hook", NewHTTPClient(nil))
	p.baseURL = f.server.URL + "/open_api/v1.3"
	return p
}

// drainBackfill runs the executor's loop the way runBackfill does, and reports the
// regions it walked.
func drainBackfill(t *testing.T, p *TikTokProvider, conn *IntegrationConnection) []RawLead {
	t.Helper()
	var all []RawLead
	cursor := ""
	for i := 0; i < 40; i++ {
		leads, next, err := p.Backfill(context.Background(), conn, Credentials{AccessToken: "t"}, "form1", cursor)
		if err != nil {
			t.Fatalf("Backfill: %v", err)
		}
		all = append(all, leads...)
		if next == "" {
			return all
		}
		cursor = next
	}
	t.Fatal("backfill did not terminate within the executor's page budget")
	return nil
}

// Leads live in three separate silos and one request reaches exactly one. A backfill
// that asked once would import nothing for an advertiser targeting the other two AND
// REPORT SUCCESS, because the wrong header returns an empty export rather than error.
func TestTikTokBackfill_WalksEveryDataResidencyRegion(t *testing.T) {
	f := newFakeTikTokExport(t)
	p := f.provider()

	leads := drainBackfill(t, p, &IntegrationConnection{ExternalAccountID: "adv1"})

	if len(f.sawDownloads) != 3 {
		t.Fatalf("expected one download per silo, got %d (%v)", len(f.sawDownloads), f.sawDownloads)
	}
	for _, want := range tiktokExportRegions {
		found := false
		for _, got := range f.sawDownloads {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("region %q was never asked; its leads would be silently missing", want)
		}
	}
	// Two rows per silo, three silos.
	if len(leads) != 6 {
		t.Errorf("expected 6 leads across three silos, got %d", len(leads))
	}
}

// The export is a background task, so the state machine has to poll — and it must do
// it through the executor's loop rather than sleeping inside a provider call.
func TestTikTokBackfill_PollsUntilTheTaskSucceeds(t *testing.T) {
	f := newFakeTikTokExport(t)
	f.pollsBeforeSucceed = 3
	p := f.provider()

	leads := drainBackfill(t, p, &IntegrationConnection{ExternalAccountID: "adv1"})
	if len(leads) == 0 {
		t.Fatal("a task that takes a few polls must still deliver its leads")
	}
	// The first region polled 3 times then succeeded, so it was asked 4 times.
	if len(f.sawRegions) < 4 {
		t.Errorf("expected the task to be polled, saw %d calls", len(f.sawRegions))
	}
}

// One silo failing must not abandon the other two — they may hold most of the history.
func TestTikTokBackfill_AFailedRegionDoesNotAbortTheRest(t *testing.T) {
	f := newFakeTikTokExport(t)
	f.failRegion = "us"
	p := f.provider()

	leads := drainBackfill(t, p, &IntegrationConnection{ExternalAccountID: "adv1"})
	if len(f.sawDownloads) != 2 {
		t.Errorf("the two healthy silos must still be downloaded, got %v", f.sawDownloads)
	}
	if len(leads) != 4 {
		t.Errorf("expected the surviving silos' leads, got %d", len(leads))
	}
}

// A truncated export parses perfectly and simply contains fewer people, so importing
// one would drop history while reporting success. It must be refused.
func TestTikTokBackfill_RefusesATruncatedExport(t *testing.T) {
	f := newFakeTikTokExport(t)
	f.hugeBody = true
	p := f.provider()

	_, _, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "adv1"},
		Credentials{AccessToken: "t"}, "form1", "")
	if err == nil {
		t.Fatal("an export cut at the body cap must be refused, not imported")
	}
	if !strings.Contains(err.Error(), "cut short") {
		t.Errorf("the error should say why: %v", err)
	}
}

// The docs say a download error "may return a response in JSON format". A one-row CSV
// and a JSON error must not be confused.
func TestTikTokBackfill_JSONErrorOnDownloadIsAFailure(t *testing.T) {
	f := newFakeTikTokExport(t)
	f.downloadJSONError = true
	p := f.provider()

	_, _, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "adv1"},
		Credentials{AccessToken: "t"}, "form1", "")
	if err == nil {
		t.Fatal("a JSON error body must not be parsed as an export")
	}
}

// The lead id is what deduplicates a backfilled lead against one the webhook already
// delivered. Without it a re-run imports every person a second time.
func TestTikTokBackfill_CarriesTheLeadIDForDedupe(t *testing.T) {
	leads, err := parseTikTokLeadCSV([]byte(tiktokExportCSV), "adv1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(leads) != 2 {
		t.Fatalf("expected two leads, got %d", len(leads))
	}
	if leads[0].ProviderEventID != "7012345678901234567" {
		t.Errorf("lead id must survive at full precision, got %q", leads[0].ProviderEventID)
	}
}

// An export whose rows cannot be deduplicated must be refused outright rather than
// imported — a re-run would duplicate every contact in it.
func TestTikTokBackfill_RefusesAnExportWithNoLeadIDColumn(t *testing.T) {
	_, err := parseTikTokLeadCSV([]byte("created_time,Email\n2026-01-01,a@example.com\n"), "adv1")
	if err == nil {
		t.Error("without a lead id column the export cannot be deduplicated and must be refused")
	}
}

// The standard English labels must land on the same keys the WEBHOOK uses, or a
// backfilled lead misses the seed field map that a live one hits.
func TestTikTokBackfill_MapsStandardLabelsOntoWebhookFieldNames(t *testing.T) {
	leads, err := parseTikTokLeadCSV([]byte(tiktokExportCSV), "adv1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f := leads[0].Fields
	if f["email"] != "jane@example.com" {
		t.Errorf("Email must map to `email` (the webhook's name), got %v", f)
	}
	if f["phone_number"] != "15088888888" {
		t.Errorf("Phone number must map to `phone_number`, got %v", f)
	}
	if f["name"] != "Jane Doe" {
		t.Errorf("Name must map to `name`, got %v", f)
	}
	// A custom question keeps its own label and will quarantine, showing up in the
	// mapping UI for the admin. Guessing at a target would put a wrong value in a real
	// contact field.
	if f["please select the service you are interested in?"] != "Roofing" {
		t.Errorf("a custom question must survive under its own name, got %v", f)
	}
	// Metadata belongs on the delivery, not on the contact.
	for _, meta := range []string{"ad_id", "campaign_id", "form_id", "created_time"} {
		if _, leaked := f[meta]; leaked {
			t.Errorf("%s is delivery metadata and must not be written as a contact field", meta)
		}
	}
	if leads[0].Context["form_id"] != "1700000000000001" {
		t.Errorf("form_id must ride the delivery context, got %v", leads[0].Context)
	}
}

// An empty export is a legitimate answer — a form with no history, or a silo this
// advertiser does not target — and must not read as a failure.
func TestTikTokBackfill_EmptyExportIsNotAnError(t *testing.T) {
	leads, err := parseTikTokLeadCSV(nil, "adv1")
	if err != nil {
		t.Fatalf("an empty export must not error: %v", err)
	}
	if len(leads) != 0 {
		t.Errorf("expected no leads, got %d", len(leads))
	}
}

// The cursor is the executor's only state. A malformed one must terminate the walk
// rather than restarting it, or a corrupted value would loop the import forever.
func TestTikTokBackfill_MalformedCursorTerminates(t *testing.T) {
	f := newFakeTikTokExport(t)
	p := f.provider()
	for _, bad := range []string{"nonsense", "9|task", "-1|task"} {
		leads, next, err := p.Backfill(context.Background(), &IntegrationConnection{ExternalAccountID: "adv1"},
			Credentials{AccessToken: "t"}, "form1", bad)
		if err != nil || next != "" || len(leads) != 0 {
			t.Errorf("cursor %q should end the walk, got leads=%d next=%q err=%v", bad, len(leads), next, err)
		}
	}
}

// A zip is TikTok's documented format above 10MB — exactly the biggest advertisers,
// the ones a backfill matters most for. Undetected it parses as garbage CSV and
// yields zero leads while reporting success.
func TestTikTokBackfill_InflatesAZippedExport(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("leads.csv")
	_, _ = w.Write([]byte(tiktokExportCSV))
	_ = zw.Close()

	if !isZip(buf.Bytes()) {
		t.Fatal("the fixture must be recognised as a zip")
	}
	csvBytes, err := csvFromZip(buf.Bytes())
	if err != nil {
		t.Fatalf("csvFromZip: %v", err)
	}
	leads, err := parseTikTokLeadCSV(csvBytes, "adv1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(leads) != 2 {
		t.Errorf("a zipped export must yield the same leads as a raw one, got %d", len(leads))
	}
}

// A UTF-8 BOM on the first cell would make the lead-id column name unmatchable, so
// the whole export is refused for a formatting artifact Windows adds routinely.
func TestTikTokBackfill_StripsALeadingBOM(t *testing.T) {
	withBOM := "\ufeff" + tiktokExportCSV
	leads, err := parseTikTokLeadCSV([]byte(withBOM), "adv1")
	if err != nil {
		t.Fatalf("a BOM must not make the export unparseable: %v", err)
	}
	if len(leads) != 2 || leads[0].ProviderEventID == "" {
		t.Errorf("the lead id column must still be found past a BOM, got %d leads", len(leads))
	}
}

// A custom question a user labelled "Lead id" must not hijack the dedupe key from the
// real metadata column, which is always in the fixed prefix.
func TestTikTokBackfill_LeadIDColumnIsTheFirstMatch(t *testing.T) {
	csv := "lead id,Email,Lead id\n" +
		"7012345678901234567,jane@example.com,not-the-real-id\n"
	leads, err := parseTikTokLeadCSV([]byte(csv), "adv1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(leads) != 1 || leads[0].ProviderEventID != "7012345678901234567" {
		t.Errorf("the FIRST lead id column must win, got %+v", leads)
	}
}

// Two columns that normalize to one key must not silently overwrite: the second is
// preserved under its raw label so its answer survives into the mapping UI.
func TestTikTokBackfill_CollidingColumnsBothSurvive(t *testing.T) {
	csv := "lead id,Phone number,Phone\n" +
		"7012345678901234567,15088888888,15099999999\n"
	leads, err := parseTikTokLeadCSV([]byte(csv), "adv1")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f := leads[0].Fields
	if f["phone_number"] != "15088888888" {
		t.Errorf("the first colliding column must win the canonical key, got %v", f["phone_number"])
	}
	if f["phone"] != "15099999999" {
		t.Errorf("the loser must survive under its raw label rather than being dropped, got %v", f)
	}
}

// ── runBackfill over TikTok: the executor's two HIGH review findings ────────

// tiktokBackfillStack wires a source Handler to a ConnectionService whose registry
// holds a TikTok provider pointed at a fake export server, so runBackfill (the
// unexported unit under test) can resolve creds and walk the state machine.
func tiktokBackfillStack(t *testing.T, db *gorm.DB, exportSrv *httptest.Server, pollsPerRegion int, fakeExport *fakeTikTokExport) (*Handler, *LeadSource, uuid.UUID) {
	t.Helper()
	repo := NewRepository(db)
	ring, err := envelope.ParseKeyring(testKey)
	require.NoError(t, err)
	codec := envelope.NewCodec(ring)

	prov := NewTikTokProvider("app1", "secret", "https://auth.example", "https://crm.example/hook", NewHTTPClient(nil))
	prov.baseURL = exportSrv.URL + "/open_api/v1.3"
	reg := NewRegistry()
	reg.Register(prov)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	conn := NewConnectionService(repo, codec, reg, "https://api.example", "https://app.example", logger)
	ingest := NewLeadIngestService(repo, &recordingWriter{}, &stubMatcher{}, contactSchema(), noFieldDefs{}, stubMembers{}, nil, logger)
	h := NewHandler(repo, ingest, allowingAuthorizer{}, stubMembers{}, contactSchema(), nil,
		NewRateLimiter(nil, 0, 0), NewRateLimiter(nil, 0, 0), logger).WithConnections(conn)

	orgID := seedOrg(t, db)
	connID := uuid.New()
	sealed, kv, err := conn.sealCredentials(orgID, connID, Credentials{AccessToken: "tt-token"})
	require.NoError(t, err)
	require.NoError(t, repo.InsertConnection(context.Background(), &IntegrationConnection{
		ID: connID, OrgID: orgID, Provider: ProviderKeyTikTok, ExternalAccountID: "adv1",
		ExternalAccountLabel: "Acme", EncryptedCredentials: sealed, KeyVersion: kv, Status: ConnStatusConnected,
	}))

	cfg, _ := json.Marshal(map[string]any{"tiktok": map[string]any{"form_id": "form1"}})
	src := &LeadSource{
		OrgID: orgID, Kind: KindTikTokForm, Name: "TikTok form", TargetSlug: "contact",
		UpdatePolicy: UpdatePolicyFillBlankOnly, Status: SourceStatusActive,
		MatchFields: datatypes.JSON(`["email"]`),
		FieldMap:    datatypes.JSON(`{"email":{"target_key":"email"}}`),
		Config:      datatypes.JSON(cfg),
	}
	require.NoError(t, repo.CreateConnectionSource(context.Background(), src))
	require.NoError(t, repo.SetSourceConnection(context.Background(), orgID, src.ID, connID))
	return h, src, orgID
}

// The headline HIGH: for TikTok a loop iteration is mostly a POLL, so charging polls
// against the 100-page budget means a slow export (more than ~33 polls per silo)
// exhausts it before the leads arrive — importing nothing and logging "finished".
// With polls not counted, an export that polls far past the page budget still lands.
func TestRunBackfill_PollsDoNotExhaustThePageBudget(t *testing.T) {
	db, cleanup := newIntegrationsTestDB(t)
	defer cleanup()
	prev := backfillPageDelay
	backfillPageDelay = 0 // the poll cadence, zeroed so 300+ polls run instantly
	defer func() { backfillPageDelay = prev }()

	// 120 polls per region — more than backfillMaxPages=100 — before each SUCCEEDs.
	fake := newFakeTikTokExport(t)
	fake.pollsBeforeSucceed = 120
	h, src, orgID := tiktokBackfillStack(t, db, fake.server, 120, fake)

	h.runBackfill(context.Background(), src, false)

	var written int64
	require.NoError(t, db.Raw("SELECT COUNT(*) FROM integration_events WHERE org_id = ? AND status = ? AND outcome = ?",
		orgID, EventStatusProcessed, OutcomeCreated).Scan(&written).Error)
	require.Greater(t, written, int64(0),
		"a slow export must still import its leads — polls must not spend the page budget")
}
