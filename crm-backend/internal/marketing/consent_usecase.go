package marketing

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// maxGrantContactIDs bounds the explicit-list path. Segments are the intended
// mechanism for anything large; a caller sending more than this almost certainly
// wants a segment, and an unbounded IN list is a denial-of-service on ourselves.
const maxGrantContactIDs = 200

type consentStore interface {
	GrantLawfulBasis(ctx context.Context, orgID uuid.UUID, scope GrantScope, in GrantInput) (int64, error)
	PreviewLawfulBasisGrant(ctx context.Context, orgID uuid.UUID, scope GrantScope) (GrantCounts, error)
}

// ConsentUseCase records an org's declaration that it has a lawful basis to market
// to a population of its own contacts.
//
// This is a legally significant write, so it is deliberately strict in three ways
// that a normal CRUD usecase would not be: it refuses bases that would have no
// effect, it refuses callers who cannot see the whole org, and it reports only what
// the database actually wrote.
type ConsentUseCase struct {
	repo     consentStore
	segments audienceResolver
	audit    domain.AuthEventWriter
	logger   *slog.Logger
	now      func() time.Time
}

func NewConsentUseCase(repo consentStore, segments audienceResolver, audit domain.AuthEventWriter, logger *slog.Logger) *ConsentUseCase {
	return &ConsentUseCase{repo: repo, segments: segments, audit: audit, logger: logger, now: time.Now}
}

// ConsentGrantInput is the validated request.
type ConsentGrantInput struct {
	Basis            string
	Source           string
	SegmentIDs       []uuid.UUID
	ExcludeSegmentIDs []uuid.UUID
	ContactIDs       []uuid.UUID
	CASLExpiresAt    *time.Time
}

func badRequest(msg string) error { return domain.NewAppError(http.StatusBadRequest, msg) }

// validate enforces every rule that keeps a grant from being silently ineffective.
func (uc *ConsentUseCase) validate(ctx context.Context, in *ConsentGrantInput) error {
	// A row-scoped caller must not do this. Consent is keyed per NORMALIZED EMAIL,
	// and several contacts can share one address, so a "scoped" grant would still
	// write the row governing a colleague's contact. Half-scoping is incoherent
	// here; refusing is the honest answer. Mirrors segment_usecase.orgWideCaller,
	// where callerless (trusted in-process) counts as org-wide.
	if caller, ok := domain.CallerFromContext(ctx); ok {
		if !caller.IsOwner && caller.DataScope != domain.DataScopeAll {
			return domain.NewAppError(http.StatusForbidden,
				"declaring a lawful basis requires org-wide access: consent is keyed per email address, which may be shared by contacts outside your scope")
		}
	}

	in.Basis = strings.TrimSpace(in.Basis)
	if in.Basis == "" {
		return badRequest("basis is required")
	}
	if !IsValidConsentBasis(in.Basis) {
		return badRequest("unknown consent basis: " + in.Basis)
	}
	// The narrower gate. express/double_opt_in are valid bases but HasLawfulBasis
	// rejects them at status 'pending', so granting one would return 200 and change
	// nothing observable. Refuse rather than lie.
	if !IsGrantableConsentBasis(in.Basis) {
		return badRequest("basis " + in.Basis + " cannot be declared by an administrator: it is an affirmative opt-in that must come from the subscriber, and recording it here would not make anyone mailable")
	}

	in.Source = strings.TrimSpace(in.Source)
	if in.Source == "" {
		return badRequest("source is required: record where this basis comes from, e.g. \"2024 customer import\"")
	}
	// Counted in RUNES, not bytes: consent_source is VARCHAR(64), and Postgres
	// measures that in characters. A byte check would let a 64-character
	// multi-byte source through to a 22001 at insert time — a 500 where a 400
	// belongs.
	if utf8.RuneCountInString(in.Source) > 64 {
		return badRequest("source must be 64 characters or fewer")
	}

	// CASL implied bases expire; the standing ones do not. Requiring the expiry
	// here stops an org recording an implied basis that never lapses.
	switch {
	case CASLImpliedBasis(in.Basis):
		if in.CASLExpiresAt == nil {
			return badRequest("casl_expires_at is required for " + in.Basis + ": CASL implied consent expires")
		}
		if !in.CASLExpiresAt.After(uc.now()) {
			return badRequest("casl_expires_at must be in the future")
		}
	case in.CASLExpiresAt != nil:
		return badRequest("casl_expires_at applies only to the CASL implied bases")
	}

	hasSegments := len(in.SegmentIDs) > 0
	hasContacts := len(in.ContactIDs) > 0
	switch {
	case hasSegments && hasContacts:
		return badRequest("choose either segments or an explicit contact list, not both")
	case !hasSegments && !hasContacts:
		return badRequest("one of segment_ids or contact_ids is required")
	case hasContacts && len(in.ContactIDs) > maxGrantContactIDs:
		return badRequest("too many contact_ids: use a segment for more than " + itoa(maxGrantContactIDs))
	case hasContacts && len(in.ExcludeSegmentIDs) > 0:
		return badRequest("exclude_segment_ids applies only to the segment path")
	}
	return nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// scopeFor resolves the request's targeting into the query the repository runs.
func (uc *ConsentUseCase) scopeFor(ctx context.Context, orgID uuid.UUID, in ConsentGrantInput) (GrantScope, error) {
	if len(in.ContactIDs) > 0 {
		return GrantScope{ContactIDs: in.ContactIDs}, nil
	}

	// Every named segment must resolve, and this check has to happen HERE rather
	// than being inferred downstream. AudienceQueryForSegments silently skips ids
	// it cannot find and always returns a non-nil query — an entirely unresolvable
	// list compiles to a zero-row "WHERE false" SELECT. Without this, deleting a
	// segment between page load and submit (or one wrong digit in a uuid, which
	// parses fine) yields 200 with granted:0 and an audit event recording a grant
	// that targeted nothing. The mixed case is worse still: one valid id and one
	// stale id silently narrows the population with no signal at all.
	for _, id := range append(append([]uuid.UUID{}, in.SegmentIDs...), in.ExcludeSegmentIDs...) {
		seg, err := uc.segments.Get(ctx, orgID, id)
		if err != nil {
			return GrantScope{}, err
		}
		if seg == nil {
			return GrantScope{}, domain.NewAppError(http.StatusNotFound, "audience not found: "+id.String())
		}
	}

	aud, err := uc.segments.AudienceQueryForSegments(ctx, orgID, in.SegmentIDs, in.ExcludeSegmentIDs)
	if err != nil {
		return GrantScope{}, err
	}
	if aud == nil {
		return GrantScope{}, badRequest("those segments resolve to no contacts")
	}
	return GrantScope{Audience: aud}, nil
}

// Preview reports what a grant would do without writing anything.
func (uc *ConsentUseCase) Preview(ctx context.Context, orgID uuid.UUID, in ConsentGrantInput) (*GrantCounts, error) {
	if err := uc.validate(ctx, &in); err != nil {
		return nil, err
	}
	scope, err := uc.scopeFor(ctx, orgID, in)
	if err != nil {
		return nil, err
	}
	counts, err := uc.repo.PreviewLawfulBasisGrant(ctx, orgID, scope)
	if err != nil {
		return nil, err
	}
	return &counts, nil
}

// Grant records the declaration and returns what the database actually wrote.
func (uc *ConsentUseCase) Grant(ctx context.Context, orgID, userID uuid.UUID, in ConsentGrantInput) (*GrantCounts, error) {
	if err := uc.validate(ctx, &in); err != nil {
		return nil, err
	}
	scope, err := uc.scopeFor(ctx, orgID, in)
	if err != nil {
		return nil, err
	}

	// Preview first for the denominators; the write supplies the only honest
	// numerator.
	counts, err := uc.repo.PreviewLawfulBasisGrant(ctx, orgID, scope)
	if err != nil {
		return nil, err
	}

	now := uc.now()
	granted, err := uc.repo.GrantLawfulBasis(ctx, orgID, scope, GrantInput{
		Basis:         in.Basis,
		Source:        in.Source,
		GrantedBy:     userID,
		CASLExpiresAt: in.CASLExpiresAt,
		Now:           now,
	})
	if err != nil {
		return nil, err
	}

	// Granted comes from RowsAffected, never from the candidate count. Skipped is
	// then the difference — the addresses the conflict arm's WHERE left alone
	// because they are already unsubscribed or cleaned.
	counts.Granted = granted
	counts.Skipped = counts.Candidates - granted
	if counts.Skipped < 0 {
		counts.Skipped = 0
	}

	uc.recordGrant(ctx, orgID, map[string]any{
		"basis":       in.Basis,
		"source":      in.Source,
		"granted":     granted,
		"candidates":  counts.Candidates,
		"skipped":     counts.Skipped,
		"suppressed":  counts.Suppressed,
		"segment_ids": uuidsToStrings(in.SegmentIDs),
		"contact_ids": len(in.ContactIDs),
	})
	return &counts, nil
}

func uuidsToStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// recordGrant writes ONE admin audit event per request, never one per contact —
// mirroring the bulk field-permission precedent, which records a count rather than
// a row each. The consent row itself carries the durable per-address provenance;
// this event is the batch-level operational trail.
//
// usecase.recordAdminEvent is unexported and in another package, so the shape is
// replicated here rather than reused.
func (uc *ConsentUseCase) recordGrant(ctx context.Context, orgID uuid.UUID, metadata map[string]any) {
	if uc.audit == nil {
		return
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		raw = []byte("{}")
	}
	e := &domain.AuthEvent{
		Category:  "admin",
		EventType: "marketing.consent.granted",
		Metadata:  domain.JSON(raw),
	}
	if orgID != uuid.Nil {
		o := orgID
		e.OrgID = &o
	}
	if caller, ok := domain.CallerFromContext(ctx); ok && caller.UserID != uuid.Nil {
		a := caller.UserID
		e.ActorID = &a
	}
	if meta, ok := domain.RequestMetaFromContext(ctx); ok {
		if meta.IP != "" {
			ip := meta.IP
			e.IP = &ip
		}
		if meta.UserAgent != "" {
			ua := meta.UserAgent
			e.UserAgent = &ua
		}
	}
	if err := uc.audit.WriteAuthEvent(ctx, e); err != nil && uc.logger != nil {
		uc.logger.Error("marketing: failed to audit consent grant", "error", err, "org_id", orgID.String())
	}
}
