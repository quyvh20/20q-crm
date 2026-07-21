package integrations

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// The form-embed capture endpoint: POST /api/capture/forms/:public_token.
//
// Everything here is shaped by one fact — there is NO credential. The token is in
// the page source, so every request is anonymous and every failure is forgeable.
// That changes three things relative to the other capture routes:
//
//   - What the caller may write is bounded by the form's DECLARED field list, not
//     by a key. On the other routes the credential is what stops a stranger
//     stuffing arbitrary keys into the ledger; here the declaration is.
//   - Nothing on this route may feed the source's error badge. Every failure is
//     something a stranger can cause at will, so counting them would let anyone who
//     read the page source flip a healthy form into an alarm state.
//   - Wire fields that decide side effects — the idempotency key, any test marker —
//     are ignored rather than read, because "the caller cannot be trusted" is the
//     whole threat model rather than an edge case.

// formSubmission is the wire shape the generated snippet posts.
type formSubmission struct {
	Fields  map[string]any `json:"fields"`
	Context map[string]any `json:"context"`
	Consent map[string]any `json:"consent"`
	// Turnstile carries the widget's token when the source has Turnstile on. Named
	// for our wire rather than Cloudflare's `cf-turnstile-response` because the
	// snippet builds this JSON itself.
	Turnstile string `json:"turnstile_token"`
}

// maxFormKeys bounds how many keys one submission may carry before we stop reading
// it. Ported from the batch endpoint's per-item bound and for the same reason: a
// payload of ten thousand junk keys is stored twice (raw_payload and
// quarantined_fields) and then scanned by the mapping UI's observed-keys query.
const maxFormKeys = 200

// formError is the one shape the snippet's error hook reads.
func formError(c *gin.Context, status int, msg string) {
	c.AbortWithStatusJSON(status, gin.H{"error": msg})
}

// formAccepted is THE response for every submission we accept — a real one, a
// honeypot catch, a Turnstile rejection, an over-cap store. One constant shape,
// with no field that varies by outcome.
//
// That constancy is the honeypot's entire value, and it is easy to lose by
// accident: an earlier version added `consent_recorded` on the success path only,
// which let a bot detect a catch by that key's ABSENCE and adapt. Anything an
// anonymous caller could correlate with the outcome belongs on the ledger, which is
// where every one of these paths already writes it.
func formAccepted(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"data": gin.H{"ok": true}})
}

// CaptureForm accepts one submission from a customer's own website.
//
// formCORS has already run: it charged the rate limiters, resolved the source to
// check the origin, and set the CORS headers that make every response below —
// including the errors — readable by the page.
func (h *Handler) CaptureForm(c *gin.Context) {
	token := strings.TrimSpace(c.Param("public_token"))

	// +1 on the limit so a body AT the cap is distinguishable from one over it.
	// Without it an oversized submission truncates mid-JSON and reports a confusing
	// 400 — and a long textarea on a public form is exactly how that happens.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, captureBodyLimit+1))
	if err != nil {
		formError(c, http.StatusBadRequest, "could not read the submission")
		return
	}
	if len(body) > captureBodyLimit {
		formError(c, http.StatusRequestEntityTooLarge, "that submission is too large")
		return
	}

	source, err := h.repo.FindFormSourceByPublicToken(c.Request.Context(), token)
	if err != nil {
		h.logger.Error("integrations: form source lookup failed", "error", err)
		formError(c, http.StatusServiceUnavailable, "could not accept the submission; try again")
		return
	}
	// Unknown, revoked, disabled and wrong-kind collapse into one 404 — which of
	// them it is, is not an anonymous caller's business. `error` status is
	// deliberately still live: it is a badge, not a gate.
	if source == nil || !source.IsLive() || source.Kind != KindFormEmbed {
		formError(c, http.StatusNotFound, "this form is no longer accepting submissions")
		return
	}

	var req formSubmission
	if jerr := json.Unmarshal(body, &req); jerr != nil {
		// A ledger row for a parse failure, because otherwise the admin has ZERO
		// evidence: this 400 lands in a fetch() on someone else's website that nobody
		// is watching. For a snippet running across every browser, a malformed
		// submission is exactly the thing you need to be able to see.
		h.recordFormRejection(c, source, body, "the submission was not valid JSON")
		formError(c, http.StatusBadRequest, "the submission was not valid JSON")
		return
	}
	if len(req.Fields) == 0 {
		formError(c, http.StatusUnprocessableEntity, "the submission carried no fields")
		return
	}
	if len(req.Fields) > maxFormKeys {
		formError(c, http.StatusUnprocessableEntity, "that submission carries more fields than a form plausibly has")
		return
	}

	cfg := ParseFormConfig(source.Config)

	// ── The honeypot ─────────────────────────────────────────────────────────
	// A field no human can see, so a value in it is a bot. Answered with the SAME
	// 200 body a real success gets: a bot that learns it was caught simply adapts,
	// and the whole value of a honeypot is that it looks like it worked.
	if h.honeypotTripped(cfg, req.Fields) {
		h.recordFormRejection(c, source, body, "honeypot field was filled — recorded as spam, no contact written")
		formAccepted(c)
		return
	}

	// ── Turnstile ────────────────────────────────────────────────────────────
	if verdict := h.verifyTurnstile(c, source, req.Turnstile); !verdict.OK {
		if verdict.TellVisitor {
			// A stale or already-spent token is usually a REAL person who left the tab
			// open. Telling them lets the widget re-challenge and the submission
			// succeed; a silent 200 would give them a thank-you page and lose the lead.
			h.recordFormRejection(c, source, body, verdict.LedgerNote)
			formError(c, http.StatusUnprocessableEntity, "please complete the verification and submit again")
			return
		}
		// Everything else — no token at all, a mis-keyed secret, Cloudflare
		// unreachable — is stored and answered 200. A bot must not learn anything,
		// and a real person must not lose their lead to our configuration.
		h.recordFormRejection(c, source, body, verdict.LedgerNote)
		formAccepted(c)
		return
	}

	// ── What the caller is allowed to have said ──────────────────────────────
	fields := declaredFieldsOnly(cfg, req.Fields)
	if len(fields) == 0 {
		formError(c, http.StatusUnprocessableEntity, "the submission carried none of this form's fields")
		return
	}

	lead := RawLead{
		Fields: fields,
		// Context is REBUILT from the two keys we read, never passed through: a
		// caller-supplied context map would otherwise be a way to write arbitrary
		// JSON onto the ledger row.
		Context: formContext(req.Context),
		Consent: req.Consent,
		// ProviderEventID is deliberately EMPTY. The other routes take it from an
		// Idempotency-Key header, but on an anonymous route a caller could PRE-CLAIM
		// keys — and the replay switch re-runs failed and quarantined rows in place,
		// so a pre-claimed row is a way to bind someone else's later lead to a
		// stranger's payload. No wire key, no claim.
		//
		// TestOrigin is deliberately absent for the same reason: it is never read off
		// a payload except on the Google route, where the caller at least holds a key.
	}

	if !h.formCapAllows(c, source, &lead, body) {
		return // already answered 200 with a quarantined row
	}

	res, err := h.ingest.Ingest(c.Request.Context(), &source.LeadSource, lead)
	if err != nil {
		if appErr, ok := err.(*domain.AppError); ok {
			if appErr.Code >= http.StatusInternalServerError {
				h.countFormFailure(c, source)
			}
			formError(c, appErr.Code, appErr.Message)
			return
		}
		h.logger.Error("integrations: form ingest failed", "error", err, "source_id", source.ID.String())
		h.countFormFailure(c, source)
		formError(c, http.StatusInternalServerError, "could not accept the submission; try again")
		return
	}
	if res.RecordID == uuid.Nil {
		h.logger.Error("integrations: form ingest returned no record", "source_id", source.ID.String())
		h.countFormFailure(c, source)
		formError(c, http.StatusInternalServerError, "could not accept the submission; try again")
		return
	}

	// The same constant shape every accepted submission gets. captureResponse
	// carries record ids, quarantined key names and warnings — internal ids and
	// echoes of caller-chosen strings, none of which an anonymous page should
	// receive, and any of which would vary by outcome. The ledger is where
	// diagnostics live; res is already recorded there.
	_ = res
	formAccepted(c)
}

// honeypotTripped reports whether the invisible field carries a value.
func (h *Handler) honeypotTripped(cfg FormConfig, fields map[string]any) bool {
	name := strings.TrimSpace(cfg.Honeypot)
	if name == "" {
		return false
	}
	v, ok := fields[name]
	return ok && strings.TrimSpace(stringOf(v)) != ""
}

// declaredFieldsOnly keeps the keys the form says it collects, and drops the rest.
//
// The declared list does the job the credential does on every other capture route.
// Undeclared keys are dropped rather than quarantined ON PURPOSE: quarantined keys
// are stored verbatim and then sampled by the mapping UI's observed-keys query to
// suggest new mappings, so keeping them would let anyone who viewed the page source
// inject arbitrary strings into the admin's picker. There is no legitimate sender
// of an undeclared key here — the snippet posts exactly what the form declares.
func declaredFieldsOnly(cfg FormConfig, in map[string]any) map[string]any {
	declared := cfg.DeclaredFields()
	out := make(map[string]any, len(in))
	for k, v := range in {
		if declared[k] {
			out[k] = v
		}
	}
	return out
}

// formContext rebuilds the delivery context from the two keys the snippet sends.
//
// Rebuilt rather than passed through: `context` lands verbatim on the ledger row,
// so accepting the caller's whole map would let an anonymous poster write arbitrary
// JSON into it — and the ledger's value is that it says what actually happened.
func formContext(in map[string]any) map[string]any {
	out := map[string]any{}
	if v := strings.TrimSpace(stringOf(in["page_url"])); v != "" {
		out["page_url"] = truncate(v, 2048)
	}
	if v := strings.TrimSpace(stringOf(in["referrer"])); v != "" {
		out["referrer"] = truncate(v, 2048)
	}
	return out
}

// formCapAllows enforces the daily cap, storing an over-cap submission rather than
// refusing it — the shape L3 established. Returns false when it has already
// answered.
func (h *Handler) formCapAllows(c *gin.Context, source *FormSource, lead *RawLead, body []byte) bool {
	if source.DailyCap <= 0 {
		return true
	}
	n, err := h.repo.CountCreatedToday(c.Request.Context(), source.ID, time.Now())
	if err != nil {
		h.logger.Error("integrations: form daily cap check failed", "error", err, "source_id", source.ID.String())
		formError(c, http.StatusServiceUnavailable, "could not accept the submission; try again")
		return false
	}
	if n < int64(source.DailyCap) {
		return true
	}
	// Stored, not refused. The visitor filled in a form and pressed send; telling
	// them "no" because the workspace hit a quota loses a real person's enquiry for
	// a reason that is nothing to do with them.
	h.recordFormRejection(c, source, body,
		"daily capture limit reached — this submission was stored but NOT written as a contact. "+
			"Raise the limit, then resend it through the batch endpoint to write it.")
	formAccepted(c)
	return false
}

// countFormFailure feeds the source's failure badge — but only from the two states
// a stranger cannot manufacture: an Ingest 5xx and the no-record guard.
//
// Every OTHER failure on this route is forgeable, because the token is public. If
// 4xx, honeypot, Turnstile, cap or rate-limit outcomes counted, anyone who viewed
// the page source could flip a healthy form into `error` at will. The consequence
// worth writing down: the badge will rarely trip for this kind, so L6 alerting must
// not lean on it for form embeds.
func (h *Handler) countFormFailure(c *gin.Context, source *FormSource) {
	h.countSourceFailure(c.Request.Context(), &source.LeadSource)
}

// recordFormRejection writes the ledger row for a submission we accepted but did
// not turn into a contact.
//
// The row is the ONLY evidence of these: the visitor saw a thank-you, the snippet
// logged nothing, and there is no integrator watching a response. Best-effort, and
// deliberately conflict-tolerant.
//
// The payload is stored NESTED under a fixed key rather than flattened. Every row
// on this route is written by an unauthenticated caller, and a flat shape would put
// caller-chosen key names at the top level of raw_payload — which the mapping UI
// samples to suggest mappings to the admin.
func (h *Handler) recordFormRejection(c *gin.Context, source *FormSource, body []byte, note string) {
	raw, _ := json.Marshal(map[string]any{"submission": json.RawMessage(sanitizeFormBody(body))})
	event := &IntegrationEvent{
		OrgID:      source.OrgID,
		SourceID:   &source.ID,
		Status:     EventStatusQuarantined,
		Attempts:   1,
		RawPayload: datatypes.JSON(raw),
		Error:      note,
	}
	if _, err := h.repo.InsertEventDeduped(c.Request.Context(), event); err != nil {
		h.logger.Error("integrations: could not record form rejection", "error", err, "source_id", source.ID.String())
	}
}

// sanitizeFormBody returns a body safe to embed in a JSONB document, or a JSON
// string describing why it could not be. An unparseable body is exactly the case
// this is most often called for, so it must not itself break the write.
func sanitizeFormBody(body []byte) []byte {
	if len(body) == 0 || !json.Valid(body) {
		out, _ := json.Marshal(string(sanitizeJSONText(body)))
		return out
	}
	return body
}

// sanitizeJSONText strips what Postgres will not accept inside jsonb: Go tolerates
// a NUL byte in a string and Postgres rejects it, so one byte from a public form
// would fail the write and take the evidence with it.
func sanitizeJSONText(b []byte) []byte {
	out := make([]byte, 0, len(b))
	for _, ch := range b {
		if ch != 0x00 {
			out = append(out, ch)
		}
	}
	if len(out) > 4096 {
		out = out[:4096]
	}
	return out
}
