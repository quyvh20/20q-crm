package marketing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// CampaignUseCase is the M7 bulk-send orchestration surface (route-gated by
// marketing.manage). M7.1 covers CRUD + the audience fan-out + the pre-send launch
// checklist — it sends NO email (that is the M7.2 send worker). It reuses M5 for the
// audience (domain.SegmentUseCase), M2 for the verified-domain gate (DomainService),
// M3/M4/M6 for the remaining launch gates, and its own campaign Repository.
// The usecase depends on small consumer-side interfaces (not the concrete marketing
// services) so it is unit-testable with fakes; *Repository, domain.SegmentUseCase,
// *DomainService and *TokenService satisfy them structurally, so main.go wiring is
// unchanged.
type campaignStore interface {
	CreateCampaign(ctx context.Context, c *domain.Campaign) error
	GetCampaignByID(ctx context.Context, orgID, id uuid.UUID) (*domain.Campaign, error)
	ListCampaignsByOrg(ctx context.Context, orgID uuid.UUID) ([]domain.Campaign, error)
	UpdateCampaign(ctx context.Context, c *domain.Campaign) (bool, error)
	SoftDeleteCampaign(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	BeginLaunch(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	FinishLaunch(ctx context.Context, orgID, id uuid.UUID) (bool, error)
	SetCampaignStatus(ctx context.Context, orgID, id uuid.UUID, status string, from []string) (bool, error)
	CountRosterByStatus(ctx context.Context, campaignID uuid.UUID) (map[string]int, error)
	ClearRoster(ctx context.Context, campaignID uuid.UUID) error
	SnapshotRoster(ctx context.Context, campaignID, orgID uuid.UUID, aud domain.AudienceQuery) (int, error)
	AssignABTestCells(ctx context.Context, campaignID uuid.UUID, testPct int) (int, error)
	EstimateAudience(ctx context.Context, aud domain.AudienceQuery) (int, error)
	GetProfile(ctx context.Context, orgID uuid.UUID) (*OrgMarketingProfile, error)
	GetContentByID(ctx context.Context, orgID, id uuid.UUID) (*CampaignContent, error)
}

type audienceResolver interface {
	AudienceQueryForSegments(ctx context.Context, orgID uuid.UUID, includeIDs, excludeIDs []uuid.UUID) (*domain.AudienceQuery, error)
	Get(ctx context.Context, orgID, id uuid.UUID) (*domain.Segment, error)
}

type domainGate interface {
	CanBulkSend(ctx context.Context, orgID uuid.UUID) (bool, string, error)
}

type tokenGate interface {
	Configured() bool
}

type CampaignUseCase struct {
	repo     campaignStore
	segments audienceResolver
	domains  domainGate
	tokens   tokenGate
	logger   *slog.Logger
}

// NewCampaignUseCase builds the usecase. tokens may be nil (unsub key unset) — the
// launch checklist then reports the unsubscribe gate as not-ready rather than panic.
func NewCampaignUseCase(repo campaignStore, segments audienceResolver, domains domainGate, tokens tokenGate, logger *slog.Logger) *CampaignUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &CampaignUseCase{repo: repo, segments: segments, domains: domains, tokens: tokens, logger: logger}
}

const maxCampaignNameLen = 200

// gmailClipBytes: Gmail clips a message body at ~102 KB; M6 flags content at/above
// this, and the launch gate refuses to send it.
const gmailClipBytes = 102 * 1024

func (uc *CampaignUseCase) List(ctx context.Context, orgID uuid.UUID) ([]domain.Campaign, error) {
	return uc.repo.ListCampaignsByOrg(ctx, orgID)
}

func (uc *CampaignUseCase) Get(ctx context.Context, orgID, id uuid.UUID) (*domain.Campaign, error) {
	c, err := uc.repo.GetCampaignByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "campaign not found")
	}
	return c, nil
}

func (uc *CampaignUseCase) Create(ctx context.Context, orgID, userID uuid.UUID, in domain.CampaignInput) (*domain.Campaign, error) {
	v, err := uc.validateInput(ctx, orgID, in)
	if err != nil {
		return nil, err
	}
	c := &domain.Campaign{
		OrgID:             orgID,
		Name:              v.name,
		ContentID:         in.ContentID,
		SegmentIDs:        marshalUUIDList(in.SegmentIDs),
		ExcludeSegmentIDs: marshalUUIDList(in.ExcludeSegmentIDs),
		TopicID:           in.TopicID,
		Status:            domain.CampaignStatusDraft,
		SendLane:          v.lane,
		ScheduledAt:       in.ScheduledAt,
		RecipientLockMode: v.lock,
		ABTestPct:         v.abTestPct,
		ABSubjectB:        v.abSubjectB,
		ABTestWindowHours: v.abTestWindowHours,
	}
	if userID != uuid.Nil {
		c.CreatedBy = &userID
	}
	if err := uc.repo.CreateCampaign(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (uc *CampaignUseCase) Update(ctx context.Context, orgID, id uuid.UUID, in domain.CampaignInput) (*domain.Campaign, error) {
	c, err := uc.repo.GetCampaignByID(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "campaign not found")
	}
	// A campaign that has left draft/scheduled is in-flight or done — its audience &
	// content are locked. Editing it would desync the roster already being sent.
	if c.Status != domain.CampaignStatusDraft && c.Status != domain.CampaignStatusScheduled {
		return nil, domain.NewAppError(http.StatusConflict, "campaign can only be edited while in draft or scheduled")
	}
	// validateInput still validates lane/lock (rejects bad values) but the composer
	// does not send them, so we do NOT overwrite send_lane / scheduled_at /
	// recipient_lock_mode here — `c` already holds their persisted values and
	// UpdateCampaign writes those back unchanged (else editing a scheduled campaign
	// would silently null scheduled_at and reset the lane/lock to defaults).
	v, err := uc.validateInput(ctx, orgID, in)
	if err != nil {
		return nil, err
	}
	c.Name = v.name
	c.ContentID = in.ContentID
	c.SegmentIDs = marshalUUIDList(in.SegmentIDs)
	c.ExcludeSegmentIDs = marshalUUIDList(in.ExcludeSegmentIDs)
	c.TopicID = in.TopicID
	// A/B config is composer-editable (unlike lane/lock/scheduled_at). UpdateCampaign
	// already writes these columns; never touch ab_winner_variant/ab_decided_at (the
	// decider owns those).
	c.ABTestPct = v.abTestPct
	c.ABSubjectB = v.abSubjectB
	c.ABTestWindowHours = v.abTestWindowHours
	// Guarded partial UPDATE: 0 rows ⇒ the campaign left draft/scheduled (a concurrent
	// Launch) between our read and write — surface a 409 rather than clobber it.
	ok, err := uc.repo.UpdateCampaign(ctx, c)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.NewAppError(http.StatusConflict, "campaign can only be edited while in draft or scheduled")
	}
	return c, nil
}

func (uc *CampaignUseCase) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	removed, err := uc.repo.SoftDeleteCampaign(ctx, orgID, id)
	if err != nil {
		return err
	}
	if !removed {
		return domain.NewAppError(http.StatusNotFound, "campaign not found")
	}
	// A soft-deleted campaign can never send again (the claim query requires
	// deleted_at IS NULL) nor be re-snapshotted (Get 404s), so reclaim its roster now
	// — otherwise those rows are orphaned forever (the pruner only touches sent/
	// canceled campaigns). Best-effort: a clear failure must not fail the delete.
	_ = uc.repo.ClearRoster(ctx, id)
	return nil
}

// EstimateForSegments returns the live DISTINCT-email audience size for a set of
// segments (the composer's count, on unsaved selections too).
func (uc *CampaignUseCase) EstimateForSegments(ctx context.Context, orgID uuid.UUID, includeIDs, excludeIDs []uuid.UUID) (int, error) {
	if len(includeIDs) == 0 {
		return 0, nil
	}
	aud, err := uc.segments.AudienceQueryForSegments(ctx, orgID, includeIDs, excludeIDs)
	if err != nil {
		return 0, err
	}
	return uc.repo.EstimateAudience(ctx, *aud)
}

// Readiness runs the pre-send checklist. A campaign may launch only when Ready.
func (uc *CampaignUseCase) Readiness(ctx context.Context, orgID, id uuid.UUID) (*domain.LaunchReadiness, error) {
	c, err := uc.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	includes := parseUUIDList(c.SegmentIDs)
	excludes := parseUUIDList(c.ExcludeSegmentIDs)

	var checks []domain.LaunchCheck
	add := func(key, label string, ok bool, detail string) {
		checks = append(checks, domain.LaunchCheck{Key: key, Label: label, OK: ok, Detail: detail})
	}

	// Audience
	count, audErr := uc.EstimateForSegments(ctx, orgID, includes, excludes)
	if audErr != nil {
		add("audience", "Audience resolved", false, "could not resolve the audience")
	} else {
		add("audience", "Audience has recipients", count > 0, audienceDetail(len(includes), count))
	}

	// Content compiled + not oversized (M6)
	uc.addContentCheck(ctx, orgID, c, add)

	// Verified sending domain + aligned Return-Path (M2/B1)
	domOK, domReason, derr := uc.domains.CanBulkSend(ctx, orgID)
	if derr != nil {
		add("domain", "Verified sending domain", false, "could not check the sending domain")
	} else {
		add("domain", "Verified sending domain", domOK, domainReasonDetail(domReason))
	}

	// Sender profile: from-name + postal address + org not marketing-paused (M3/M4)
	profile, perr := uc.repo.GetProfile(ctx, orgID)
	if perr != nil {
		add("sender", "Sender profile complete", false, "could not load the sender profile")
	} else {
		ok, reason := SenderProfileSendable(profile)
		add("sender", "Sender profile complete", ok, senderReasonDetail(reason))
	}

	// One-click unsubscribe minting configured (M3/B3)
	add("unsubscribe", "One-click unsubscribe configured", uc.tokens != nil && uc.tokens.Configured(),
		"the marketing unsubscribe signing key is not set on this deployment")

	ready := true
	for _, ck := range checks {
		if !ck.OK {
			ready = false
			break
		}
	}
	return &domain.LaunchReadiness{Ready: ready, Checks: checks, AudienceCount: count}, nil
}

// Snapshot materializes the roster from the campaign's segments (M7.1: sends
// nothing — it only builds the recipient set). Re-snapshots idempotently by clearing
// first. The actual launch/send is M7.2.
func (uc *CampaignUseCase) Snapshot(ctx context.Context, orgID, id uuid.UUID) (*domain.SnapshotResult, error) {
	c, err := uc.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	// The public snapshot (a preview/rebuild) is allowed ONLY before sending. Once a
	// campaign is snapshotting/sending/paused/sent/canceled, rebuilding the roster
	// would wipe partially-sent send-state and mass-double-send on resume.
	if c.Status != domain.CampaignStatusDraft && c.Status != domain.CampaignStatusScheduled {
		return nil, domain.NewAppError(http.StatusConflict, "the roster can only be built while the campaign is draft or scheduled")
	}
	return uc.materializeRoster(ctx, orgID, id, c)
}

// materializeRoster clears + rebuilds the roster from the campaign's segments. Callers
// own the status guard (Snapshot restricts to draft/scheduled; Launch runs it inside
// the exclusive 'snapshotting' state where the worker can't claim).
func (uc *CampaignUseCase) materializeRoster(ctx context.Context, orgID, id uuid.UUID, c *domain.Campaign) (*domain.SnapshotResult, error) {
	includes := parseUUIDList(c.SegmentIDs)
	excludes := parseUUIDList(c.ExcludeSegmentIDs)
	aud, err := uc.segments.AudienceQueryForSegments(ctx, orgID, includes, excludes)
	if err != nil {
		return nil, err
	}
	if err := uc.repo.ClearRoster(ctx, id); err != nil {
		return nil, err
	}
	total, err := uc.repo.SnapshotRoster(ctx, id, orgID, *aud)
	if err != nil {
		return nil, err
	}
	return &domain.SnapshotResult{Total: total}, nil
}

// Launch validates readiness, then atomically claims the campaign into 'snapshotting'
// (only one concurrent Launch wins), builds the roster while the worker cannot see it,
// and flips to 'sending'. Launch sends no email synchronously — the worker drains.
// Refuses a not-ready campaign (422) and a non-launchable state (409). On a snapshot
// failure it reverts to 'draft' so the campaign stays editable/retryable rather than
// stranded in 'snapshotting'.
func (uc *CampaignUseCase) Launch(ctx context.Context, orgID, id uuid.UUID) (*domain.Campaign, error) {
	r, err := uc.Readiness(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if !r.Ready {
		return nil, domain.NewAppError(http.StatusUnprocessableEntity, "campaign is not ready to send — resolve the checklist first")
	}
	c, err := uc.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	began, err := uc.repo.BeginLaunch(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	if !began {
		return nil, domain.NewAppError(http.StatusConflict, "campaign is not in a launchable state (already launching or sent)")
	}
	if _, err := uc.materializeRoster(ctx, orgID, id, c); err != nil {
		// Roll back the exclusive claim so the campaign is editable/retryable again.
		_, _ = uc.repo.SetCampaignStatus(ctx, orgID, id, domain.CampaignStatusDraft, []string{domain.CampaignStatusSnapshotting})
		return nil, err
	}
	// A/B (M9 Part B): split a test fraction into A/B cells + hold the remainder for the
	// winner, inside the exclusive snapshotting window (the worker can't claim mid-split).
	// A subject test needs a variant-B subject; without one this is a no-op (normal send).
	if c.ABTestPct > 0 && c.ABSubjectB != "" {
		if _, err := uc.repo.AssignABTestCells(ctx, id, c.ABTestPct); err != nil {
			_, _ = uc.repo.SetCampaignStatus(ctx, orgID, id, domain.CampaignStatusDraft, []string{domain.CampaignStatusSnapshotting})
			return nil, err
		}
	}
	if _, err := uc.repo.FinishLaunch(ctx, orgID, id); err != nil {
		return nil, err
	}
	return uc.Get(ctx, orgID, id)
}

// Pause halts an in-flight send (the claim query stops seeing its recipients). Only a
// 'sending' campaign can pause.
func (uc *CampaignUseCase) Pause(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.transition(ctx, orgID, id, domain.CampaignStatusPaused, map[string]bool{domain.CampaignStatusSending: true})
}

// Resume returns a paused campaign to 'sending'.
func (uc *CampaignUseCase) Resume(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.transition(ctx, orgID, id, domain.CampaignStatusSending, map[string]bool{domain.CampaignStatusPaused: true})
}

// Cancel permanently stops a send (from sending, paused, or scheduled). A sent or
// already-canceled campaign cannot be canceled.
func (uc *CampaignUseCase) Cancel(ctx context.Context, orgID, id uuid.UUID) error {
	return uc.transition(ctx, orgID, id, domain.CampaignStatusCanceled, map[string]bool{
		domain.CampaignStatusSending: true, domain.CampaignStatusPaused: true, domain.CampaignStatusScheduled: true,
	})
}

func (uc *CampaignUseCase) transition(ctx context.Context, orgID, id uuid.UUID, to string, from map[string]bool) error {
	// Existence check (404 vs 409). The actual flip is a single guarded UPDATE so a
	// concurrent transition can't win a lost-update race.
	if _, err := uc.Get(ctx, orgID, id); err != nil {
		return err
	}
	fromList := make([]string, 0, len(from))
	for s := range from {
		fromList = append(fromList, s)
	}
	ok, err := uc.repo.SetCampaignStatus(ctx, orgID, id, to, fromList)
	if err != nil {
		return err
	}
	if !ok {
		return domain.NewAppError(http.StatusConflict, "campaign cannot move to "+to+" from its current state")
	}
	return nil
}

// Progress returns the campaign status + roster counts by status (the polling/SSE
// progress source).
func (uc *CampaignUseCase) Progress(ctx context.Context, orgID, id uuid.UUID) (map[string]any, error) {
	c, err := uc.Get(ctx, orgID, id)
	if err != nil {
		return nil, err
	}
	counts, err := uc.repo.CountRosterByStatus(ctx, id)
	if err != nil {
		return nil, err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	// After the roster prune (or any time the live roster is gone) fall back to the
	// per-status counts snapshotted at the terminal transition, so a finished campaign
	// keeps showing its real totals instead of 0 / 0.
	if total == 0 && len(c.SnapshotCounts) > 0 {
		var snap map[string]int
		if json.Unmarshal(c.SnapshotCounts, &snap) == nil && len(snap) > 0 {
			counts = snap
			for _, n := range snap {
				total += n
			}
		}
	}
	return map[string]any{"status": c.Status, "counts": counts, "total": total}, nil
}

// ── internals ────────────────────────────────────────────────────────────────

// validatedInput is validateInput's normalized output.
type validatedInput struct {
	name              string
	lane              string
	lock              string
	abTestPct         int
	abSubjectB        string
	abTestWindowHours int
}

// validateInput checks the name + enums, the A/B config, and that every referenced
// segment exists in the org (uc.segments.Get is org-scoped and returns 404 for a
// foreign/missing id).
func (uc *CampaignUseCase) validateInput(ctx context.Context, orgID uuid.UUID, in domain.CampaignInput) (validatedInput, error) {
	var v validatedInput
	v.name = strings.TrimSpace(in.Name)
	if v.name == "" {
		return v, domain.NewAppError(http.StatusBadRequest, "name is required")
	}
	if utf8.RuneCountInString(v.name) > maxCampaignNameLen {
		return v, domain.NewAppError(http.StatusBadRequest, "name must be 200 characters or fewer")
	}
	v.lane = strings.TrimSpace(in.SendLane)
	if v.lane == "" {
		v.lane = domain.CampaignLaneSingle
	}
	if v.lane != domain.CampaignLaneSingle && v.lane != domain.CampaignLaneBatch {
		return v, domain.NewAppError(http.StatusBadRequest, "invalid send lane")
	}
	v.lock = strings.TrimSpace(in.RecipientLockMode)
	if v.lock == "" {
		v.lock = domain.RecipientLockOnSchedule
	}
	if v.lock != domain.RecipientLockOnSchedule && v.lock != domain.RecipientLockAtSend {
		return v, domain.NewAppError(http.StatusBadRequest, "invalid recipient lock mode")
	}
	// A/B (M9 Part B): pct>0 enables a subject test and REQUIRES a variant-B subject; pct=0
	// clears the config. Window defaults to 4h, capped at 7 days.
	if in.ABTestPct < 0 || in.ABTestPct > 100 {
		return v, domain.NewAppError(http.StatusBadRequest, "A/B test percentage must be between 0 and 100")
	}
	v.abTestPct = in.ABTestPct
	if v.abTestPct > 0 {
		v.abSubjectB = strings.TrimSpace(in.ABSubjectB)
		if v.abSubjectB == "" {
			return v, domain.NewAppError(http.StatusBadRequest, "an A/B test needs a variant B subject line")
		}
		if utf8.RuneCountInString(v.abSubjectB) > 998 {
			return v, domain.NewAppError(http.StatusBadRequest, "variant B subject must be 998 characters or fewer")
		}
		v.abTestWindowHours = in.ABTestWindowHours
		if v.abTestWindowHours <= 0 {
			v.abTestWindowHours = 4
		}
		if v.abTestWindowHours > 168 {
			v.abTestWindowHours = 168
		}
	} else {
		v.abTestWindowHours = 4
	}
	for _, id := range append(append([]uuid.UUID{}, in.SegmentIDs...), in.ExcludeSegmentIDs...) {
		if _, gerr := uc.segments.Get(ctx, orgID, id); gerr != nil {
			return v, domain.NewAppError(http.StatusBadRequest, "unknown segment: "+id.String())
		}
	}
	return v, nil
}

func (uc *CampaignUseCase) addContentCheck(ctx context.Context, orgID uuid.UUID, c *domain.Campaign, add func(key, label string, ok bool, detail string)) {
	if c.ContentID == nil {
		add("content", "Email content selected & compiled", false, "no content selected")
		return
	}
	content, err := uc.repo.GetContentByID(ctx, orgID, *c.ContentID)
	if err != nil || content == nil {
		add("content", "Email content selected & compiled", false, "the selected content no longer exists")
		return
	}
	if strings.TrimSpace(content.BodyHTMLCompiled) == "" {
		add("content", "Email content selected & compiled", false, "the content has not compiled yet")
		return
	}
	if content.CompiledSizeBytes >= gmailClipBytes {
		add("content", "Email content selected & compiled", false, "the content is ≥100 KB and Gmail will clip it")
		return
	}
	add("content", "Email content selected & compiled", true, "")
}

func audienceDetail(numSegments, count int) string {
	if numSegments == 0 {
		return "no audience selected"
	}
	if count == 0 {
		return "the selected audience currently matches no mailable contacts"
	}
	return ""
}

func domainReasonDetail(reason string) string {
	switch reason {
	case "", "ok":
		return ""
	case "no_domain":
		return "no verified sending domain — add one under Sending domains"
	case "spf_unverified":
		return "SPF is not yet verified"
	case "dkim_unverified":
		return "DKIM is not yet verified"
	case "dmarc_missing":
		return "DMARC is not published"
	default:
		return reason
	}
}

func senderReasonDetail(reason string) string {
	switch reason {
	case "":
		return ""
	case "no_profile", "no_from_name":
		return "set a From name in the sender profile"
	case "no_postal_address":
		return "add a physical postal address (required by CAN-SPAM)"
	case "marketing_paused":
		return "marketing is paused for this workspace"
	default:
		return reason
	}
}

func parseUUIDList(j datatypes.JSON) []uuid.UUID {
	if len(j) == 0 {
		return nil
	}
	var raw []string
	if err := json.Unmarshal(j, &raw); err != nil {
		return nil
	}
	out := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		if id, err := uuid.Parse(s); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func marshalUUIDList(ids []uuid.UUID) datatypes.JSON {
	if len(ids) == 0 {
		return datatypes.JSON("[]")
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return datatypes.JSON("[]")
	}
	return datatypes.JSON(b)
}
