package marketing

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// ── fakes for the campaign usecase's consumer-side interfaces ──────────────────

type fakeCampStore struct {
	campaigns    map[uuid.UUID]*domain.Campaign
	createCalled bool
	updateCalled bool
	cleared      bool
	snapshotN    int
	estimateN    int
	profile      *OrgMarketingProfile
	content      *CampaignContent
}

func newFakeCampStore() *fakeCampStore {
	return &fakeCampStore{campaigns: map[uuid.UUID]*domain.Campaign{}}
}

func (f *fakeCampStore) CreateCampaign(_ context.Context, c *domain.Campaign) error {
	f.createCalled = true
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	f.campaigns[c.ID] = c
	return nil
}
func (f *fakeCampStore) GetCampaignByID(_ context.Context, _ uuid.UUID, id uuid.UUID) (*domain.Campaign, error) {
	return f.campaigns[id], nil
}
func (f *fakeCampStore) ListCampaignsByOrg(_ context.Context, _ uuid.UUID) ([]domain.Campaign, error) {
	out := make([]domain.Campaign, 0, len(f.campaigns))
	for _, c := range f.campaigns {
		out = append(out, *c)
	}
	return out, nil
}
func (f *fakeCampStore) UpdateCampaign(_ context.Context, c *domain.Campaign) error {
	f.updateCalled = true
	f.campaigns[c.ID] = c
	return nil
}
func (f *fakeCampStore) SoftDeleteCampaign(_ context.Context, _ uuid.UUID, id uuid.UUID) (bool, error) {
	if _, ok := f.campaigns[id]; !ok {
		return false, nil
	}
	delete(f.campaigns, id)
	return true, nil
}
func (f *fakeCampStore) ClearRoster(_ context.Context, _ uuid.UUID) error { f.cleared = true; return nil }
func (f *fakeCampStore) SnapshotRoster(_ context.Context, _, _ uuid.UUID, _ domain.AudienceQuery) (int, error) {
	return f.snapshotN, nil
}
func (f *fakeCampStore) EstimateAudience(_ context.Context, _ domain.AudienceQuery) (int, error) {
	return f.estimateN, nil
}
func (f *fakeCampStore) GetProfile(_ context.Context, _ uuid.UUID) (*OrgMarketingProfile, error) {
	return f.profile, nil
}
func (f *fakeCampStore) GetContentByID(_ context.Context, _ uuid.UUID, _ uuid.UUID) (*CampaignContent, error) {
	return f.content, nil
}

type fakeAudience struct {
	unknown map[uuid.UUID]bool // ids Get should reject
}

func (f *fakeAudience) AudienceQueryForSegments(_ context.Context, _ uuid.UUID, _, _ []uuid.UUID) (*domain.AudienceQuery, error) {
	return &domain.AudienceQuery{SelectSQL: "SELECT NULL::uuid, NULL::text WHERE false"}, nil
}
func (f *fakeAudience) Get(_ context.Context, _ uuid.UUID, id uuid.UUID) (*domain.Segment, error) {
	if f.unknown[id] {
		return nil, domain.NewAppError(http.StatusNotFound, "segment not found")
	}
	return &domain.Segment{ID: id, Type: domain.SegmentTypeDynamic}, nil
}

type fakeDomainGate struct {
	ok     bool
	reason string
}

func (f *fakeDomainGate) CanBulkSend(_ context.Context, _ uuid.UUID) (bool, string, error) {
	return f.ok, f.reason, nil
}

type fakeTokenGate struct{ ok bool }

func (f *fakeTokenGate) Configured() bool { return f.ok }

// ── env ────────────────────────────────────────────────────────────────────────

type campEnv struct {
	uc      *CampaignUseCase
	store   *fakeCampStore
	aud     *fakeAudience
	domains *fakeDomainGate
	tokens  *fakeTokenGate
	orgID   uuid.UUID
}

func newCampEnv() *campEnv {
	store := newFakeCampStore()
	aud := &fakeAudience{unknown: map[uuid.UUID]bool{}}
	dg := &fakeDomainGate{ok: true}
	tg := &fakeTokenGate{ok: true}
	return &campEnv{
		uc:      NewCampaignUseCase(store, aud, dg, tg, nil),
		store:   store,
		aud:     aud,
		domains: dg,
		tokens:  tg,
		orgID:   uuid.New(),
	}
}

func campErrCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	appErr, ok := err.(*domain.AppError)
	if !ok {
		t.Fatalf("expected AppError, got %T: %v", err, err)
	}
	return appErr.Code
}

// ── CRUD validation ─────────────────────────────────────────────────────────────

func TestCampaign_CreateValidation(t *testing.T) {
	env := newCampEnv()
	ctx := context.Background()

	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(), domain.CampaignInput{Name: "  "}))); code != http.StatusBadRequest {
		t.Fatalf("blank name code=%d want 400", code)
	}
	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(), domain.CampaignInput{Name: strings.Repeat("x", 201)}))); code != http.StatusBadRequest {
		t.Fatalf("long name code=%d want 400", code)
	}
	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(), domain.CampaignInput{Name: "C", SendLane: "rocket"}))); code != http.StatusBadRequest {
		t.Fatalf("bad lane code=%d want 400", code)
	}
	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(), domain.CampaignInput{Name: "C", RecipientLockMode: "whenever"}))); code != http.StatusBadRequest {
		t.Fatalf("bad lock code=%d want 400", code)
	}
	if env.store.createCalled {
		t.Fatal("no invalid input should have been persisted")
	}

	// Unknown segment id rejected.
	bad := uuid.New()
	env.aud.unknown[bad] = true
	if code := campErrCode(t, mustErr(env.uc.Create(ctx, env.orgID, uuid.New(), domain.CampaignInput{Name: "C", SegmentIDs: []uuid.UUID{bad}}))); code != http.StatusBadRequest {
		t.Fatalf("unknown segment code=%d want 400", code)
	}

	// Valid → created with defaults.
	c, err := env.uc.Create(ctx, env.orgID, uuid.New(), domain.CampaignInput{Name: "Launch", SegmentIDs: []uuid.UUID{uuid.New()}})
	if err != nil {
		t.Fatalf("valid create: %v", err)
	}
	if c.Status != domain.CampaignStatusDraft || c.SendLane != domain.CampaignLaneSingle || c.RecipientLockMode != domain.RecipientLockOnSchedule {
		t.Fatalf("defaults not applied: %+v", c)
	}
}

func TestCampaign_UpdateBlockedAfterLaunch(t *testing.T) {
	env := newCampEnv()
	c := &domain.Campaign{ID: uuid.New(), OrgID: env.orgID, Name: "C", Status: domain.CampaignStatusSending, SegmentIDs: datatypes.JSON("[]"), ExcludeSegmentIDs: datatypes.JSON("[]")}
	env.store.campaigns[c.ID] = c
	if code := campErrCode(t, mustErr(env.uc.Update(context.Background(), env.orgID, c.ID, domain.CampaignInput{Name: "New"}))); code != http.StatusConflict {
		t.Fatalf("editing a sending campaign code=%d want 409", code)
	}
}

// ── launch readiness gates ───────────────────────────────────────────────────

func readyCampaign(env *campEnv) *domain.Campaign {
	cid := uuid.New()
	c := &domain.Campaign{
		ID: cid, OrgID: env.orgID, Name: "C", Status: domain.CampaignStatusDraft,
		ContentID:         ptrUUID(uuid.New()),
		SegmentIDs:        marshalUUIDList([]uuid.UUID{uuid.New()}),
		ExcludeSegmentIDs: datatypes.JSON("[]"),
	}
	env.store.campaigns[cid] = c
	env.store.content = &CampaignContent{BodyHTMLCompiled: "<html>hi</html>", CompiledSizeBytes: 2000}
	env.store.profile = &OrgMarketingProfile{FromName: "Acme", PhysicalPostalAddress: "1 Main St", MarketingPaused: false}
	env.store.estimateN = 42
	env.domains.ok = true
	env.tokens.ok = true
	return c
}

func ptrUUID(id uuid.UUID) *uuid.UUID { return &id }

func checkByKey(r *domain.LaunchReadiness, key string) *domain.LaunchCheck {
	for i := range r.Checks {
		if r.Checks[i].Key == key {
			return &r.Checks[i]
		}
	}
	return nil
}

func TestCampaign_ReadinessAllGreen(t *testing.T) {
	env := newCampEnv()
	c := readyCampaign(env)
	r, err := env.uc.Readiness(context.Background(), env.orgID, c.ID)
	if err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if !r.Ready {
		t.Fatalf("expected ready, got checks=%+v", r.Checks)
	}
	if r.AudienceCount != 42 {
		t.Fatalf("audience count=%d want 42", r.AudienceCount)
	}
}

func TestCampaign_ReadinessGatesBlock(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(env *campEnv, c *domain.Campaign)
		wantKey string
	}{
		{"no content", func(env *campEnv, c *domain.Campaign) { c.ContentID = nil }, "content"},
		{"oversized content", func(env *campEnv, c *domain.Campaign) { env.store.content.CompiledSizeBytes = 200 * 1024 }, "content"},
		{"uncompiled content", func(env *campEnv, c *domain.Campaign) { env.store.content.BodyHTMLCompiled = "" }, "content"},
		{"no audience", func(env *campEnv, c *domain.Campaign) { env.store.estimateN = 0 }, "audience"},
		{"unverified domain", func(env *campEnv, c *domain.Campaign) { env.domains.ok = false; env.domains.reason = "dkim_unverified" }, "domain"},
		{"paused sender", func(env *campEnv, c *domain.Campaign) { env.store.profile.MarketingPaused = true }, "sender"},
		{"no postal address", func(env *campEnv, c *domain.Campaign) { env.store.profile.PhysicalPostalAddress = "" }, "sender"},
		{"unsub not configured", func(env *campEnv, c *domain.Campaign) { env.tokens.ok = false }, "unsubscribe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newCampEnv()
			c := readyCampaign(env)
			tc.mutate(env, c)
			r, err := env.uc.Readiness(context.Background(), env.orgID, c.ID)
			if err != nil {
				t.Fatalf("readiness: %v", err)
			}
			if r.Ready {
				t.Fatalf("expected NOT ready when %s", tc.name)
			}
			ck := checkByKey(r, tc.wantKey)
			if ck == nil || ck.OK {
				t.Fatalf("expected check %q to be failing, got %+v", tc.wantKey, r.Checks)
			}
		})
	}
}

func TestCampaign_SnapshotBlockedWhileSending(t *testing.T) {
	env := newCampEnv()
	c := &domain.Campaign{ID: uuid.New(), OrgID: env.orgID, Name: "C", Status: domain.CampaignStatusSending, SegmentIDs: datatypes.JSON("[]"), ExcludeSegmentIDs: datatypes.JSON("[]")}
	env.store.campaigns[c.ID] = c
	if code := campErrCode(t, mustErrSnapshot(env.uc.Snapshot(context.Background(), env.orgID, c.ID))); code != http.StatusConflict {
		t.Fatalf("snapshot while sending code=%d want 409", code)
	}
}

func mustErr(_ *domain.Campaign, err error) error                    { return err }
func mustErrSnapshot(_ *domain.SnapshotResult, err error) error      { return err }
