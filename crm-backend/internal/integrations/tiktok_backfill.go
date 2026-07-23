package integrations

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// TikTok's historical import: a task, not a page.
//
// The framework's Backfill contract is "return one page plus a cursor, and the
// executor loops with a delay". TikTok has no pages. You create an export TASK, poll
// it until it succeeds, then download a CSV that EXPIRES TEN MINUTES after it is
// generated — and the leads live in three separate data-residency silos that must
// each be asked separately.
//
// All of that maps onto the same loop: each Backfill call advances a state machine one
// step and hands back where it got to in the cursor. The executor's throttle, its page
// budget and its cancellation keep working unchanged, and no provider gets to invent
// its own sleep loop inside a request.

const (
	// tiktokExportLimit caps the download. TikTok switches to a zip only ABOVE 10MB of
	// CSV, so the client's default 1MB would silently cut a real advertiser's history
	// — and a truncated CSV parses perfectly, it just contains fewer people.
	tiktokExportLimit = 16 << 20

	tiktokLeadIDColumn  = "lead id"
	tiktokFormIDColumn  = "form_id"
	tiktokCreatedColumn = "created_time"
)

// tiktokExportRegions are the three data-residency silos, in the order a backfill
// walks them.
//
// Not a preference — a requirement. TikTok stores leads in three separate places and
// one request reaches exactly one of them: "" (rest of world), "us", and "eu" (the
// EEA, Switzerland and the UK). The docs state that a call without the header "will
// only receive leads from other countries/regions", so the WRONG header returns an
// empty export rather than an error. A backfill that asked once would import nothing
// for an advertiser targeting the other two and report success.
var tiktokExportRegions = []string{"", "us", "eu"}

// Backfill advances the export state machine one step.
//
// Cursor grammar: "<regionIndex>|<taskID>". Empty means "start at region 0".
func (p *TikTokProvider) Backfill(ctx context.Context, conn *IntegrationConnection, creds Credentials, formID, cursor string) ([]RawLead, string, error) {
	if formID == "" {
		return nil, "", errors.New("tiktok: cannot backfill without a form id")
	}
	region, taskID, done := parseTikTokCursor(cursor)
	if done {
		return nil, "", nil
	}

	status, id, err := p.leadExportTask(ctx, conn, creds, formID, tiktokExportRegions[region], taskID)
	if err != nil {
		return nil, "", err
	}
	switch status {
	case "SUCCEED":
		leads, derr := p.downloadLeadExport(ctx, conn, creds, tiktokExportRegions[region], id)
		if derr != nil {
			return nil, "", derr
		}
		return leads, tiktokCursor(region+1, ""), nil
	case "FAILED":
		// One silo's export failed. Move on rather than abandoning the whole backfill:
		// the other two may hold most of this form's history, and a re-run resumes
		// safely because dedupe skips everything already imported.
		return nil, tiktokCursor(region+1, ""), nil
	default:
		// CREATED or RUNNING — hand the same task back. The executor waits a second and
		// asks again, which is the documented poll loop without a sleep in here.
		return nil, tiktokCursor(region, id), nil
	}
}

// leadExportTask creates the export task, or polls it when taskID is set. One
// endpoint does both, discriminated by whether task_id is sent — TikTok's design.
func (p *TikTokProvider) leadExportTask(ctx context.Context, conn *IntegrationConnection, creds Credentials, formID, region, taskID string) (status, id string, err error) {
	payload := map[string]any{
		"advertiser_id": conn.ExternalAccountID,
		"page_id":       formID,
	}
	if taskID != "" {
		payload["task_id"] = taskID
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", "", err
	}
	var out struct {
		Status string          `json:"status"`
		TaskID json.RawMessage `json:"task_id"`
	}
	if err := p.callRegion(ctx, http.MethodPost, p.baseURL+"/page/lead/task/", creds.AccessToken, region, body, &out); err != nil {
		return "", "", err
	}
	id = tiktokID(out.TaskID)
	if id == "" {
		id = taskID
	}
	if id == "" {
		return "", "", errors.New("tiktok: the export task returned no task id")
	}
	return strings.ToUpper(strings.TrimSpace(out.Status)), id, nil
}

// downloadLeadExport fetches the finished export and parses it.
//
// The response is a CSV (or a zip above 10MB), NOT the usual JSON envelope, so this is
// the one call that bypasses decodeTikTok. It runs on a client with a raised body cap
// and REFUSES a truncated body: a short CSV parses perfectly and simply contains fewer
// people, so importing one would drop history while reporting success.
//
// The file expires ten minutes after generation, which is why the download happens in
// the same step that observed SUCCEED rather than being queued for later.
func (p *TikTokProvider) downloadLeadExport(ctx context.Context, conn *IntegrationConnection, creds Credentials, region, taskID string) ([]RawLead, error) {
	q := url.Values{}
	q.Set("advertiser_id", conn.ExternalAccountID)
	q.Set("task_id", taskID)

	req := OutboundRequest{
		Method:    http.MethodGet,
		URL:       p.baseURL + "/page/lead/task/download/?" + q.Encode(),
		Header:    http.Header{},
		BodyLimit: tiktokExportLimit,
	}
	req.Header.Set("Access-Token", creds.AccessToken)
	if region != "" {
		req.Header.Set("x-lead-region", region)
	}
	resp, err := p.http.Do(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Truncated {
		return nil, fmt.Errorf("tiktok: the lead export exceeded %d bytes and was cut short; importing it would silently drop the remainder", tiktokExportLimit)
	}
	// The docs say an error here "may return a response in JSON format or output an
	// error message in the returned CSV file". A body that decodes as an envelope is
	// therefore a failure, never a one-row export.
	if looksLikeTikTokEnvelope(resp.Body) {
		if derr := decodeTikTok(resp.Body, nil); derr != nil {
			return nil, derr
		}
		return nil, errors.New("tiktok: the export download returned a JSON body instead of a CSV")
	}
	// Above 10MB of CSV TikTok returns a ZIP, not raw CSV. Undetected, the zip parses
	// as garbage and yields zero leads — so the biggest advertisers, the ones a
	// backfill matters most for, would import nothing and see success. Inflate it in
	// memory and read the single CSV member.
	body := resp.Body
	if isZip(body) {
		csvBytes, zerr := csvFromZip(body)
		if zerr != nil {
			return nil, zerr
		}
		body = csvBytes
	}
	return parseTikTokLeadCSV(body, conn.ExternalAccountID)
}

// isZip reports whether a body is a PKZIP archive by its magic number.
func isZip(body []byte) bool {
	return len(body) >= 4 && body[0] == 'P' && body[1] == 'K' && body[2] == 0x03 && body[3] == 0x04
}

// csvFromZip reads the single CSV member out of TikTok's zipped export.
func csvFromZip(body []byte) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return nil, fmt.Errorf("tiktok: the export zip could not be opened: %w", err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".csv") {
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, fmt.Errorf("tiktok: the export csv could not be read from the zip: %w", oerr)
		}
		// Bounded read: the inflated CSV must also not blow memory. A zip bomb aside, a
		// legitimate 10MB+ export inflates to a bounded size we cap the same way as the
		// download.
		out, rerr := io.ReadAll(io.LimitReader(rc, tiktokExportLimit))
		rc.Close()
		if rerr != nil {
			return nil, fmt.Errorf("tiktok: the export csv could not be read from the zip: %w", rerr)
		}
		return out, nil
	}
	return nil, errors.New("tiktok: the export zip contained no csv")
}

// looksLikeTikTokEnvelope reports whether a download response is a JSON error rather
// than the export itself.
func looksLikeTikTokEnvelope(body []byte) bool {
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var probe struct {
		Code json.RawMessage `json:"code"`
	}
	return json.Unmarshal([]byte(trimmed), &probe) == nil && len(probe.Code) > 0
}

// parseTikTokLeadCSV turns the export into RawLeads.
//
// The fixed prefix is metadata; every column after form_name is one of the form's own
// question LABELS, in the form's own language. That is the sharp edge of this feature
// and it is surfaced rather than guessed at — see tiktokAnswerKey.
func parseTikTokLeadCSV(body []byte, advertiserID string) ([]RawLead, error) {
	r := csv.NewReader(bytes.NewReader(body))
	// The export is not rectangular across forms — a custom question adds a column —
	// so per-record variance must be legal rather than an error.
	r.FieldsPerRecord = -1

	header, err := r.Read()
	if errors.Is(err, io.EOF) {
		// An empty export is a legitimate answer (a form with no history, or a silo
		// this advertiser does not target), not a failure.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("tiktok: could not read the export header: %w", err)
	}
	names := make([]string, len(header))
	leadCol := -1
	for i, h := range header {
		name := strings.ToLower(strings.TrimSpace(h))
		if i == 0 {
			// A UTF-8 BOM rides the first cell of a Windows-generated CSV and would make
			// the first column name "lead id", which then matches nothing — so the
			// dedupe key silently disappears and the whole export is refused, or worse a
			// later same-normalizing column is mistaken for it. Strip it here.
			name = strings.TrimPrefix(name, "\ufeff")
		}
		names[i] = name
		// FIRST match only, so a custom question a user happened to label "Lead id"
		// cannot hijack the dedupe key from the real metadata column. TikTok's real one
		// is always in the fixed prefix, before form_name.
		if leadCol < 0 && name == tiktokLeadIDColumn {
			leadCol = i
		}
	}
	if leadCol < 0 {
		// Without the provider's own id nothing can be deduplicated against the
		// webhook, so a re-run would import every person a second time.
		return nil, errors.New("tiktok: the export has no lead id column, so its rows could not be deduplicated")
	}

	var leads []RawLead
	for {
		row, rerr := r.Read()
		if errors.Is(rerr, io.EOF) {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("tiktok: could not read an export row: %w", rerr)
		}
		if leadCol >= len(row) {
			continue
		}
		leadID := strings.TrimSpace(row[leadCol])
		if leadID == "" {
			continue
		}
		fields := map[string]any{}
		leadCtx := map[string]any{"advertiser_id": advertiserID}
		for i, cell := range row {
			if i >= len(names) || i == leadCol {
				continue
			}
			value := strings.TrimSpace(cell)
			if value == "" {
				continue
			}
			if key, meta := tiktokMetaColumn(names[i]); meta {
				if key != "" {
					leadCtx[key] = value
				}
				continue
			}
			// FIRST value wins on a key collision, and the loser is preserved under its
			// raw column label rather than silently overwriting. Two columns can
			// normalize to one key — a form with both "Phone" and "Phone number", say —
			// and dropping one would lose an answer. The kept-under-raw-label copy
			// quarantines and shows up in the mapping UI for the admin to resolve, the
			// same path any custom question takes.
			key := tiktokAnswerKey(names[i])
			if _, taken := fields[key]; taken && key != names[i] {
				if _, rawTaken := fields[names[i]]; !rawTaken {
					fields[names[i]] = value
				}
				continue
			}
			if _, taken := fields[key]; !taken {
				fields[key] = value
			}
		}
		if len(fields) == 0 {
			continue
		}
		leads = append(leads, RawLead{Fields: fields, Context: leadCtx, ProviderEventID: leadID})
	}
	return leads, nil
}

// tiktokMetaColumn classifies a fixed prefix column: whether it is metadata, and
// which delivery-context key it becomes (empty for the ones not worth carrying).
func tiktokMetaColumn(name string) (string, bool) {
	switch name {
	case tiktokCreatedColumn, tiktokFormIDColumn, "ad_id", "adgroup_id", "campaign_id":
		return name, true
	case "ad_name", "adgroup_name", "campaign_name", "form_name":
		// Names rather than ids: useful in TikTok's own reporting, noise on every
		// delivery row here.
		return "", true
	}
	return "", false
}

// tiktokAnswerKey maps an export column back onto the field name the WEBHOOK uses, so
// a backfilled lead and a live one meet the same field mapping.
//
// This is the one place the two paths genuinely disagree. The webhook sends
// `phone_number`; the export sends the form's own label — "Phone number" on an English
// form, something else entirely on a Vietnamese one. The standard English labels are
// translated here. Anything else passes through under its own name, quarantines, and
// appears in the mapping UI's observed keys for the admin to map once, which is the
// path a custom question already takes. Guessing at translations would put a wrong
// value in a real contact field, which is worse than leaving the key visible.
func tiktokAnswerKey(label string) string {
	switch label {
	case "email":
		return "email"
	case "phone number", "phone_number", "phone":
		return "phone_number"
	case "name", "full name":
		return "name"
	case "first name":
		return "first_name"
	case "surname", "last name":
		return "last_name"
	}
	return label
}

func tiktokCursor(region int, taskID string) string {
	if region >= len(tiktokExportRegions) {
		return "" // every silo walked; the executor stops
	}
	return strconv.Itoa(region) + "|" + taskID
}

func parseTikTokCursor(cursor string) (region int, taskID string, done bool) {
	if cursor == "" {
		return 0, "", false
	}
	parts := strings.SplitN(cursor, "|", 2)
	n, err := strconv.Atoi(parts[0])
	if err != nil || n < 0 || n >= len(tiktokExportRegions) {
		return 0, "", true
	}
	if len(parts) == 2 {
		taskID = parts[1]
	}
	return n, taskID, false
}

// callRegion is call() plus the data-residency header.
//
// Separate from call() on purpose: getting this header wrong returns an EMPTY export
// rather than an error, so the calls that need it should not share a code path with
// the ones that must never send it.
func (p *TikTokProvider) callRegion(ctx context.Context, method, endpoint, token, region string, body []byte, out any) error {
	req := OutboundRequest{Method: method, URL: endpoint, Header: http.Header{}}
	if token != "" {
		req.Header.Set("Access-Token", token)
	}
	if region != "" {
		req.Header.Set("x-lead-region", region)
	}
	if body != nil {
		req.Body = body
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.http.Do(ctx, req)
	if err != nil {
		return err
	}
	return decodeTikTok(resp.Body, out)
}
