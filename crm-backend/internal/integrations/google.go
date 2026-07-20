package integrations

import (
	"crypto/hmac"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// The Google Ads lead-form webhook (plan L3): the advertiser pastes a URL and a
// key into Google's form editor, and Google POSTs each submission here. No OAuth,
// no app review — the URL token names the source, the key corroborates it.
//
// Everything on this route is shaped by Google's documented retry contract:
// 200 = done; 4xx = NEVER retried; 5xx = retried (count and window undocumented).
// So a 4xx here is a decision to lose the lead unless it is genuinely permanent,
// and every transient condition — rate limit, dedupe race, DB blip — must answer
// 5xx, whatever the equivalent bearer route returns.

// googleColumn is one entry of user_column_data.
type googleColumn struct {
	// ColumnID is populated for all field types and is the stable key
	// ("EMAIL", "FULL_NAME", custom-question ids). ColumnName is documented as
	// deprecated and "might not always be populated" — a fallback only.
	ColumnID   string `json:"column_id"`
	ColumnName string `json:"column_name"`
	Value      string `json:"string_value"`
}

// googlePayload is the webhook envelope.
//
// Decoded with encoding/json deliberately: Google mandates parsers ignore unknown
// fields, and Go's field matching is case-insensitive as a fallback — which is
// load-bearing, because Google's own docs spell the key field both "google_key"
// (production sample) and "Google_key" (test samples). An exact-case map lookup
// would 401 every advertiser's test.
type googlePayload struct {
	LeadID         string         `json:"lead_id"`
	UserColumnData []googleColumn `json:"user_column_data"`
	APIVersion     string         `json:"api_version"`
	FormID         int64          `json:"form_id"`
	CampaignID     int64          `json:"campaign_id"`
	GoogleKey      string         `json:"google_key"`
	IsTest         bool           `json:"is_test"` // absent or false = production lead
	GclID          string         `json:"gcl_id"`
	AdgroupID      int64          `json:"adgroup_id"`  // video/discovery only; 0 in tests
	CreativeID     int64          `json:"creative_id"` // same
	AssetGroupID   int64          `json:"asset_group_id"`
	LeadSubmitTime string         `json:"lead_submit_time"`
}

// googleKeyMismatchError is the fixed ledger text for a failed key check. Fixed
// and server-authored — the received key must never be echoed into a field admins
// view, and a constant string is what lets the row be recognized later.
const googleKeyMismatchError = "the webhook key Google sent did not match this source's key — " +
	"real leads are being rejected until the key in Google's form editor matches. " +
	"After fixing it, resend missed leads through the batch endpoint (each lead_id is its idempotency key)."

// googleCapQuarantineError is the fixed ledger text for a lead accepted past the
// daily cap. It documents its own recovery because the ledger row IS the
// notification surface until L6 builds alerting.
const googleCapQuarantineError = "daily capture limit reached — this lead was stored but NOT written as a contact. " +
	"Resend it through the batch endpoint tomorrow (idempotency_key = this lead_id) to write it; " +
	"note batch replays do not start workflows unless the source's bulk-delivery toggle is on."

// googleError writes Google's documented error shape, {"message": ...}.
// Not captureError's {"error": ...}: response shapes are local per public path
// (the house rule stated on captureError), and this is the one the platform docs
// name.
func googleError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"message": msg})
}

// googleAllow applies one rate-limit key with GOOGLE semantics: over-limit answers
// 503, never 429 — a 429 is a 4xx, and Google would drop the lead permanently for
// what is by definition a transient condition.
func (h *Handler) googleAllow(c *gin.Context, key string) bool {
	lim := h.limiter
	if strings.HasPrefix(key, "ip:") {
		lim = h.ipLimiter
	}
	ok, retry := lim.AllowN(c.Request.Context(), key, 1)
	if ok {
		return true
	}
	secs := int(retry.Seconds())
	if secs < 1 {
		secs = 1
	}
	c.Header("Retry-After", strconv.Itoa(secs))
	googleError(c, http.StatusServiceUnavailable, "rate limited; retry")
	return false
}

// flattenGoogleColumns turns user_column_data into the flat map the L2 mapping
// engine consumes, keyed by column_id with column_name as the documented-
// deprecated fallback. TOP-level keys are load-bearing: raw_payload stores this
// map verbatim, and the mapping UI's "observed keys" reads its top-level keys —
// nest it and a custom question could never be one-click mapped.
func flattenGoogleColumns(cols []googleColumn) map[string]any {
	out := make(map[string]any, len(cols))
	for _, col := range cols {
		key := strings.TrimSpace(col.ColumnID)
		if key == "" {
			key = strings.TrimSpace(col.ColumnName)
		}
		if key == "" {
			continue
		}
		out[key] = col.Value
	}
	return out
}

// googleLeadContext builds the delivery context from the envelope's ad metadata.
// Zero-valued ids are absent, not zero — Google's test posts send 0 for the ids
// the test cannot know, and a stored "0" would read as a real id in the ledger.
func googleLeadContext(p *googlePayload) map[string]any {
	ctx := map[string]any{}
	if v := strings.TrimSpace(p.GclID); v != "" {
		ctx["gcl_id"] = v
	}
	for k, v := range map[string]int64{
		"campaign_id":    p.CampaignID,
		"adgroup_id":     p.AdgroupID,
		"creative_id":    p.CreativeID,
		"asset_group_id": p.AssetGroupID,
		"form_id":        p.FormID,
	} {
		if v != 0 {
			ctx[k] = strconv.FormatInt(v, 10)
		}
	}
	if v := strings.TrimSpace(p.LeadSubmitTime); v != "" {
		ctx["lead_submit_time"] = v
	}
	return ctx
}

// redactedEnvelope is what a PRE-AUTH ledger row stores as raw_payload: the
// envelope Google sent, key redacted, columns nested verbatim.
//
// The nesting is deliberate, not laziness. The mapping UI's observed-keys
// suggestion samples raw_payload's TOP-LEVEL keys across all rows — and a
// key-mismatch row is the first place an UNAUTHENTICATED request writes
// raw_payload at all, so flattened attacker-chosen column names would seed the
// admin's mapping picker from outside the credential. Nested, the top level is
// this fixed envelope vocabulary and nothing else. Post-auth rows flatten (their
// caller held the key; their keys are the suggestion feature working as designed).
func redactedEnvelope(p *googlePayload) map[string]any {
	return map[string]any{
		"lead_id":          p.LeadID,
		"user_column_data": p.UserColumnData,
		"form_id":          p.FormID,
		"campaign_id":      p.CampaignID,
		"is_test":          p.IsTest,
		"google_key":       "(redacted)",
	}
}

// googleSeedFieldMap is the mapping a new google_ads source starts with: Google's
// standard column_ids onto contact fields. Custom questions arrive under their own
// ids, quarantine, surface as observed keys, and get mapped by the admin — the L2
// flow, unchanged. Note FULL_NAME carries a non-empty target_key even though
// split_name ignores it at runtime: Apply rejects an empty target BEFORE the
// transform switch, so an empty one would save clean and then fail on every lead.
// Only Google columns with a NATIVE contact counterpart are seeded. COMPANY_NAME
// and the rest of the B2B set are deliberately absent: `company` is
// allowlist-blacklisted (the adapter would demand a UUID for what Google sends as
// a name), and the others have no standing field — they quarantine, appear as
// observed keys, and the admin one-click-maps them to custom fields, which is the
// L2 flow working as designed rather than a gap.
func googleSeedFieldMap() FieldMap {
	return FieldMap{
		"EMAIL":        {TargetKey: "email"},
		"WORK_EMAIL":   {TargetKey: "email"},
		"FULL_NAME":    {TargetKey: "first_name", Transform: TransformSplitName},
		"FIRST_NAME":   {TargetKey: "first_name"},
		"LAST_NAME":    {TargetKey: "last_name"},
		"PHONE_NUMBER": {TargetKey: "phone"},
		"WORK_PHONE":   {TargetKey: "phone"},
	}
}

// CaptureGoogleAds is the public webhook: POST /api/capture/google-ads/:public_token.
func (h *Handler) CaptureGoogleAds(c *gin.Context) {
	token := strings.TrimSpace(c.Param("public_token"))
	if token == "" {
		googleError(c, http.StatusUnauthorized, "unknown webhook URL")
		return
	}

	// Throttle before the DB probe (the CaptureLead ordering, for the same
	// amplification reason — and the mismatch path below costs a WRITE, which is
	// strictly more than the read that rule was invented to protect).
	if !h.googleAllow(c, "gt:"+token) || !h.googleAllow(c, "ip:"+c.ClientIP()) {
		return
	}

	body, err := io.ReadAll(io.LimitReader(c.Request.Body, captureBodyLimit))
	if err != nil {
		googleError(c, http.StatusBadRequest, "could not read body")
		return
	}
	var p googlePayload
	if err := json.Unmarshal(body, &p); err != nil {
		// A retry resends the same bytes; 400 is honest.
		googleError(c, http.StatusBadRequest, "body must be a JSON object")
		return
	}

	source, err := h.repo.FindSourceByPublicToken(c.Request.Context(), token)
	if err != nil {
		h.logger.Error("integrations: google source lookup failed", "error", err)
		googleError(c, http.StatusServiceUnavailable, "lookup failed; retry")
		return
	}
	// Unknown, revoked and disabled collapse into one 401 — which of the three it
	// is, is not the caller's business. `error` status is deliberately NOT here: it
	// is a badge, and refusing traffic while flagged would drop leads unledgered
	// during the exact window someone is fixing the source.
	if source == nil || !source.IsLive() {
		googleError(c, http.StatusUnauthorized, "unknown webhook URL")
		return
	}

	// Key check BEFORE the dedupe switch, without exception: the replay arm answers
	// with a prior delivery's record id and outcome, so reached pre-auth it would
	// be an unauthenticated oracle for what any guessed lead_id produced.
	if source.GoogleKeyHash == "" ||
		!hmac.Equal([]byte(HashLeadKey(strings.TrimSpace(p.GoogleKey))), []byte(source.GoogleKeyHash)) {
		h.recordGoogleMismatch(c, source, &p)
		// 401, not 5xx: a wrong key is a permanent configuration error fixed in
		// Google's editor, and the advertiser's "Send test data" shows red on any
		// non-200 — which is exactly how the mistake surfaces during setup. Google
		// echoes the CONFIGURED key on test posts, so a correct setup tests green.
		googleError(c, http.StatusUnauthorized, "webhook key mismatch")
		return
	}

	fields := flattenGoogleColumns(p.UserColumnData)
	if len(fields) == 0 {
		googleError(c, http.StatusUnprocessableEntity, "user_column_data is required")
		return
	}

	origin := TestOriginNone
	if p.IsTest {
		// The one payload-derived origin the pipeline permits. Forging it buys an
		// attacker (who must already hold the key) nothing that is not bounded:
		// the identity is coerced synthetic, the write converges on one flagged
		// contact per source, it is silenced, makes no deal, and still spends cap.
		origin = TestOriginProvider
	}

	lead := RawLead{
		Fields:          fields,
		Context:         googleLeadContext(&p),
		ProviderEventID: strings.TrimSpace(p.LeadID),
		TestOrigin:      origin,
	}

	// The daily cap: accept-and-quarantine, never a 4xx and never a blind 5xx. A
	// 429 makes Google drop the lead permanently for a condition that resets at
	// UTC midnight; a 503 bets the lead on an undocumented retry budget lasting
	// that long. Storing it costs nothing the cap exists to bound — no contact, no
	// workflow, no email — and the quarantined row replays through the batch
	// endpoint, which enforces the cap for real on the day it runs.
	//
	// Tests BYPASS the gate rather than quarantine: TestOrigin is not persisted on
	// the ledger, so a quarantined test replayed tomorrow would run as a REAL lead
	// — writing a deal against the synthetic test contact into the forecast. The
	// bypass is bounded by coercion: the create branch fires at most once per
	// source, ever (one cap slot), and every later test is an update.
	if !p.IsTest && source.DailyCap > 0 {
		n, cerr := h.repo.CountCreatedToday(c.Request.Context(), source.ID, time.Now())
		if cerr != nil {
			h.logger.Error("integrations: daily cap check failed", "error", cerr, "source_id", source.ID.String())
			c.Header("Retry-After", "60")
			googleError(c, http.StatusServiceUnavailable, "could not verify capture limit; retry")
			return
		}
		if n >= int64(source.DailyCap) {
			h.quarantineCappedGoogleLead(c, source, &lead)
			return
		}
	}

	res, err := h.ingest.Ingest(c.Request.Context(), &source.LeadSource, lead)
	if err != nil {
		h.respondGoogleIngestError(c, source, err)
		return
	}
	if res.RecordID == uuid.Nil {
		// The no-silent-loss guard, with the counter: a 200 with nothing written is
		// the shape that turns any bug here into invisible lead loss.
		h.logger.Error("integrations: google ingest returned no record", "source_id", source.ID.String())
		h.countGoogleFailure(c, source)
		googleError(c, http.StatusInternalServerError, "lead was not written; retry")
		return
	}
	// 200 with an empty body — the documented contract. The ledger, not the
	// response, is where diagnostics live: Google is not going to read them.
	c.JSON(http.StatusOK, gin.H{})
}

// respondGoogleIngestError maps Ingest's error taxonomy onto Google's retry
// contract and feeds the failure counter.
func (h *Handler) respondGoogleIngestError(c *gin.Context, source *GoogleSource, err error) {
	if appErr, ok := err.(*domain.AppError); ok {
		switch {
		case appErr.Code == http.StatusConflict:
			// The dedupe switch's "in flight / incomplete" answers. Transient in
			// fact (a stranded row is one reaper sweep from resolution), 4xx on the
			// wire — and Google abandoning a delivery that is minutes from
			// succeeding is exactly the loss this route exists to prevent. Not a
			// failure of the source either, so it never feeds the counter.
			c.Header("Retry-After", "300")
			googleError(c, http.StatusServiceUnavailable, "delivery in flight; retry")
		case appErr.Code >= http.StatusInternalServerError:
			// Source-wide breakage (unreadable field map, allowlist failure). The
			// counter is FOR this; retrying is harmless (deduped) and recovers the
			// lead once the admin fixes the source.
			h.countGoogleFailure(c, source)
			googleError(c, appErr.Code, appErr.Message)
		default:
			// A payload-shaped 4xx (e.g. 422 no-email on a source an admin edited
			// back to email-only matching). Permanent for THIS lead — Google will
			// not retry, and the failed ledger row is the recovery copy. Not
			// counted: the source may be fine for every other lead.
			googleError(c, appErr.Code, appErr.Message)
		}
		return
	}
	h.logger.Error("integrations: google ingest failed", "error", err, "source_id", source.ID.String())
	h.countGoogleFailure(c, source)
	googleError(c, http.StatusInternalServerError, "could not process lead")
}

// countGoogleFailure feeds the consecutive-failure counter and logs a flip.
//
// Called ONLY on post-key-verification processing failures — never pre-auth
// (unknown token, bad key, rate limit, cap), which are attacker-forgeable and
// must not be able to flip a healthy source's badge. The flip gates nothing;
// it exists so L6 has something to alert on and the settings page shows red.
func (h *Handler) countGoogleFailure(c *gin.Context, source *GoogleSource) {
	flipped, err := h.repo.IncrementSourceFailure(c.Request.Context(), source.ID)
	if err != nil {
		h.logger.Error("integrations: could not count source failure", "error", err, "source_id", source.ID.String())
		return
	}
	if flipped {
		h.logger.Error("integrations: source flipped to error after consecutive failures",
			"source_id", source.ID.String(), "org_id", source.OrgID.String())
	}
}

// recordGoogleMismatch writes the failed ledger row for a key mismatch.
// Best-effort — the 401 is the answer; this row is the evidence, and it is what
// makes a mis-pasted key diagnosable from OUR side (the advertiser's side just
// shows a red test). Conflict-tolerant: Google redelivers, and a second mismatch
// of the same lead_id must not error into a retry wall.
func (h *Handler) recordGoogleMismatch(c *gin.Context, source *GoogleSource, p *googlePayload) {
	raw, _ := json.Marshal(redactedEnvelope(p))
	var providerID *string
	if id := strings.TrimSpace(p.LeadID); id != "" {
		providerID = &id
	}
	event := &IntegrationEvent{
		OrgID:           source.OrgID,
		SourceID:        &source.ID,
		ProviderEventID: providerID,
		Status:          EventStatusFailed, // failed, so a batch resend re-runs it in place once the key is fixed
		Attempts:        1,
		RawPayload:      datatypes.JSON(raw),
		Error:           googleKeyMismatchError,
	}
	if _, err := h.repo.InsertEventDeduped(c.Request.Context(), event); err != nil {
		h.logger.Error("integrations: could not record key mismatch", "error", err, "source_id", source.ID.String())
	}
}

// quarantineCappedGoogleLead stores a post-cap lead and answers 200.
//
// Dedupe-aware: a redelivery of a lead_id that already reached a terminal success
// must answer 200 for THAT delivery rather than writing a second row, and a prior
// failed/quarantined row is already the recovery copy.
func (h *Handler) quarantineCappedGoogleLead(c *gin.Context, source *GoogleSource, lead *RawLead) {
	raw, _ := json.Marshal(lead.Fields)
	ctxJSON, _ := json.Marshal(lead.Context)
	var providerID *string
	if lead.ProviderEventID != "" {
		id := lead.ProviderEventID
		providerID = &id
	}
	event := &IntegrationEvent{
		OrgID:           source.OrgID,
		SourceID:        &source.ID,
		ProviderEventID: providerID,
		Status:          EventStatusQuarantined,
		Attempts:        1,
		RawPayload:      datatypes.JSON(raw),
		Context:         datatypes.JSON(ctxJSON),
		Error:           googleCapQuarantineError,
	}
	inserted, err := h.repo.InsertEventDeduped(c.Request.Context(), event)
	if err != nil {
		h.logger.Error("integrations: could not quarantine capped lead", "error", err, "source_id", source.ID.String())
		// The one path where a 5xx is the only honest answer: we could neither
		// write the lead nor store it for later, so Google's retry is the only
		// copy that still exists.
		googleError(c, http.StatusServiceUnavailable, "could not store lead; retry")
		return
	}
	if !inserted {
		// The lead_id already has a row — a prior delivery beat this one. Whatever
		// its state (success, failed, quarantined), the recovery copy exists;
		// nothing more to store.
		h.logger.Info("integrations: capped lead already ledgered", "source_id", source.ID.String())
	}
	c.JSON(http.StatusOK, gin.H{})
}
