package usecase

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// leadScoreStaleness is how old an org's scores may get before the sweep
// reclaims it. Paired with a 15-minute tick in main.go: the tick decides how
// often we LOOK, this decides how often an org is actually re-swept.
const leadScoreStaleness = time.Hour

// leadScoreCatalogSource is the narrow slice of the registry this usecase needs
// to build the field catalog a rule's condition is validated and compiled
// against. It is the SAME catalog the report builder and the M5 segment
// builder use — that identity is the feature, not an optimisation: a rule and a
// segment that name lead_score must mean the same column, or an org's
// "score >= 50" audience would disagree with the score it filters on.
type leadScoreCatalogSource interface {
	EnsureSystemObjects(ctx context.Context, orgID uuid.UUID) error
	GetDefBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*domain.ObjectDef, error)
	ListFields(ctx context.Context, defID uuid.UUID) ([]domain.ObjectField, error)
}

// leadScoreEngine is the repository slice that actually rescores. Declared
// consumer-side so the usecase can be unit-tested without a database.
type leadScoreEngine interface {
	domain.LeadScoringRepository
	WithLeadScoreLock(ctx context.Context, fn func() error) (bool, error)
}

// segmentASTValidator is the M5 compile-check, reached through the same port
// the segment usecase uses. Reusing it — rather than reimplementing the checks
// — is what guarantees a condition that SAVES as a scoring rule is a condition
// that COMPILES in the engine.
type segmentASTValidator interface {
	ValidateDefinition(catalog []domain.ReportField, ast domain.SegmentFilter) error
}

type leadScoringUseCase struct {
	repo      leadScoreEngine
	registry  leadScoreCatalogSource
	segments  segmentASTValidator
	authz     domain.RecordAuthorizer
	logger    *slog.Logger
}

func NewLeadScoringUseCase(repo leadScoreEngine, registry leadScoreCatalogSource, segments segmentASTValidator, authz domain.RecordAuthorizer, logger *slog.Logger) domain.LeadScoringUseCase {
	if logger == nil {
		logger = slog.Default()
	}
	return &leadScoringUseCase{repo: repo, registry: registry, segments: segments, authz: authz, logger: logger}
}

func (uc *leadScoringUseCase) catalog(ctx context.Context, orgID uuid.UUID) ([]domain.ReportField, error) {
	if err := uc.registry.EnsureSystemObjects(ctx, orgID); err != nil {
		return nil, err
	}
	def, err := uc.registry.GetDefBySlug(ctx, orgID, "contact")
	if err != nil {
		return nil, err
	}
	if def == nil {
		return nil, domain.NewAppError(http.StatusNotFound, "contact object not found")
	}
	fields, err := uc.registry.ListFields(ctx, def.ID)
	if err != nil {
		return nil, err
	}
	// lead_score is REMOVED from the catalog scoring rules compile against.
	//
	// It stays in the report and segment catalogs — "contacts scoring 50+" is
	// exactly the audience this feature exists to enable. But a scoring RULE
	// that tests lead_score would make the engine's UPDATE read the column it
	// is writing in the same statement. Postgres would evaluate it against the
	// pre-UPDATE value, so the score would flip-flop between passes and never
	// settle, with no error to explain why.
	catalog := reportCatalogForDef(def, fields)
	out := make([]domain.ReportField, 0, len(catalog))
	for _, f := range catalog {
		if f.Key == "lead_score" {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// ============================================================
// Rule CRUD
// ============================================================

func (uc *leadScoringUseCase) ListRules(ctx context.Context, orgID uuid.UUID) ([]domain.LeadScoringRule, error) {
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionRead); err != nil {
		return nil, err
	}
	out, err := uc.repo.ListRules(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	// Never nil: a Go nil slice marshals as JSON null, and the settings page
	// would white-screen on `.map` rather than render an empty state.
	if out == nil {
		out = []domain.LeadScoringRule{}
	}
	return out, nil
}

func (uc *leadScoringUseCase) CreateRule(ctx context.Context, orgID uuid.UUID, in domain.CreateLeadScoringRuleInput) (*domain.LeadScoringRule, error) {
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		return nil, err
	}
	n, err := uc.repo.CountRules(ctx, orgID)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if n >= domain.MaxLeadScoringRules {
		return nil, domain.NewAppError(http.StatusBadRequest, "this workspace already has the maximum number of scoring rules")
	}
	if err := uc.validateRule(ctx, orgID, in.Kind, in.Condition); err != nil {
		return nil, err
	}

	rule := &domain.LeadScoringRule{
		ID:        uuid.New(),
		OrgID:     orgID,
		Name:      strings.TrimSpace(in.Name),
		Kind:      in.Kind,
		Points:    in.Points,
		Condition: in.Condition,
		Enabled:   true,
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if in.Position != nil {
		rule.Position = *in.Position
	}
	if err := uc.repo.CreateRule(ctx, rule); err != nil {
		return nil, domain.ErrInternal
	}
	uc.rescoreInBackground(orgID)
	return rule, nil
}

func (uc *leadScoringUseCase) UpdateRule(ctx context.Context, orgID, id uuid.UUID, in domain.UpdateLeadScoringRuleInput) (*domain.LeadScoringRule, error) {
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		return nil, err
	}
	rule, err := uc.repo.GetRule(ctx, orgID, id)
	if err != nil {
		return nil, domain.ErrInternal
	}
	if rule == nil {
		return nil, domain.ErrLeadScoringRuleNotFound
	}

	if in.Name != nil {
		rule.Name = strings.TrimSpace(*in.Name)
	}
	if in.Points != nil {
		rule.Points = *in.Points
	}
	if in.Enabled != nil {
		rule.Enabled = *in.Enabled
	}
	if in.Position != nil {
		rule.Position = *in.Position
	}
	if in.Condition != nil {
		// Kind is immutable: the condition shape is a function of it, so a kind
		// change is a different rule. Editing the condition re-validates against
		// the EXISTING kind.
		if err := uc.validateRule(ctx, orgID, rule.Kind, *in.Condition); err != nil {
			return nil, err
		}
		rule.Condition = *in.Condition
	}
	if err := uc.repo.UpdateRule(ctx, rule); err != nil {
		return nil, domain.ErrInternal
	}
	uc.rescoreInBackground(orgID)
	return rule, nil
}

func (uc *leadScoringUseCase) DeleteRule(ctx context.Context, orgID, id uuid.UUID) error {
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		return err
	}
	rule, err := uc.repo.GetRule(ctx, orgID, id)
	if err != nil {
		return domain.ErrInternal
	}
	if rule == nil {
		return domain.ErrLeadScoringRuleNotFound
	}
	if err := uc.repo.DeleteRule(ctx, orgID, id); err != nil {
		return domain.ErrInternal
	}
	// Deleting the LAST rule still needs a pass, to zero the scores the old
	// rules left standing — which is why OrgsDueForRescore is driven from
	// organizations rather than from the rules table.
	uc.rescoreInBackground(orgID)
	return nil
}

// validateRule checks a condition compiles before it can ever reach the engine.
//
// Rejecting an EMPTY condition is the load-bearing half. An empty segment AST
// legitimately compiles to "" meaning "everyone" — correct for an audience,
// catastrophic for a score, where it would silently award the rule's points to
// every contact in the workspace. The compiler skips empty predicates as a
// second line of defence; this is the first.
func (uc *leadScoringUseCase) validateRule(ctx context.Context, orgID uuid.UUID, kind string, cond domain.JSON) error {
	if len(cond) == 0 || string(cond) == "null" || strings.TrimSpace(string(cond)) == "{}" {
		return domain.NewAppError(http.StatusBadRequest, "a scoring rule needs a condition")
	}
	switch kind {
	case domain.LeadRuleKindField:
		var ast domain.SegmentFilter
		if err := json.Unmarshal(cond, &ast); err != nil {
			return domain.NewAppError(http.StatusBadRequest, "the rule's condition is not valid JSON")
		}
		if !ast.IsFieldLeaf() && !ast.IsTagLeaf() && len(ast.Rules) == 0 {
			return domain.NewAppError(http.StatusBadRequest, "a field rule needs at least one condition")
		}
		catalog, err := uc.catalog(ctx, orgID)
		if err != nil {
			return err
		}
		// The SAME validator segments use, so a rule that saves here is a rule
		// that compiles there.
		if err := uc.segments.ValidateDefinition(catalog, ast); err != nil {
			return domain.NewAppError(http.StatusBadRequest, err.Error())
		}
	case domain.LeadRuleKindEngagement:
		var c domain.LeadEngagementCondition
		if err := json.Unmarshal(cond, &c); err != nil {
			return domain.NewAppError(http.StatusBadRequest, "the rule's condition is not valid JSON")
		}
		if !domain.ValidLeadEvents[c.Event] {
			return domain.NewAppError(http.StatusBadRequest, "that engagement event cannot be scored")
		}
		if c.WindowDays <= 0 || c.WindowDays > 3650 {
			return domain.NewAppError(http.StatusBadRequest, "the engagement window must be between 1 and 3650 days")
		}
		if c.MinCount < 0 {
			return domain.NewAppError(http.StatusBadRequest, "the minimum event count cannot be negative")
		}
	default:
		return domain.NewAppError(http.StatusBadRequest, "unknown rule kind")
	}
	return nil
}

// ============================================================
// Rescore
// ============================================================

func (uc *leadScoringUseCase) RecomputeOrgNow(ctx context.Context, orgID uuid.UUID) (int64, error) {
	if err := uc.authz.Authorize(ctx, orgID, "contact", domain.ActionEdit); err != nil {
		return 0, err
	}
	return uc.rescoreOrg(ctx, orgID)
}

func (uc *leadScoringUseCase) rescoreOrg(ctx context.Context, orgID uuid.UUID) (int64, error) {
	catalog, err := uc.catalog(ctx, orgID)
	if err != nil {
		return 0, err
	}
	rules, err := uc.repo.ListRules(ctx, orgID)
	if err != nil {
		return 0, domain.ErrInternal
	}
	n, skipped, err := uc.repo.RecomputeOrg(ctx, orgID, catalog, rules)
	// A rule that will not compile is SKIPPED, not fatal (see the compiler), so
	// one broken rule degrades itself rather than freezing the whole workspace's
	// scores. It must still be loud: nothing else would ever tell the admin.
	for _, s := range skipped {
		uc.logger.Error("lead scoring: rule skipped — it no longer compiles, so it contributes no points until it is fixed or deleted",
			"org_id", orgID.String(), "reason", s)
	}
	// Stamp the watermark on success AND on truncation — both mean "we looked",
	// and leaving it unset would pin this org at the head of the NULLS FIRST
	// queue. A hard failure deliberately does NOT stamp, so a broken org is
	// retried on the next tick instead of being parked for the staleness window.
	if err == nil || domain.IsLeadScoreTruncated(err) {
		if markErr := uc.repo.MarkOrgRescored(ctx, orgID); markErr != nil {
			uc.logger.Warn("lead scoring: could not stamp the rescore watermark", "org_id", orgID.String(), "error", markErr)
		}
	}
	return n, err
}

// rescoreInBackground rescores one org off the request path, after a rule
// changed, so the user sees new scores without waiting for the next sweep.
//
// context.Background(), never the request's: gin cancels the request context
// the instant the handler returns, so a goroutine holding it would be killed
// mid-UPDATE. Its own recover() because a panic in a bare goroutine takes the
// whole server down. Bounded by a timeout so a pathological org cannot leave a
// goroutine running indefinitely.
func (uc *leadScoringUseCase) rescoreInBackground(orgID uuid.UUID) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				uc.logger.Error("lead scoring: rescore panicked", "org_id", orgID.String(), "panic", r)
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if _, err := uc.rescoreOrg(ctx, orgID); err != nil {
			uc.logger.Warn("lead scoring: post-edit rescore failed; the sweep will retry", "org_id", orgID.String(), "error", err)
		}
	}()
}

// RunRescorePass is the sweep. Fleet-singleton via the advisory lock: the work
// is idempotent, so concurrent replicas would converge anyway, but they would
// each pay for the same scan.
func (uc *leadScoringUseCase) RunRescorePass(ctx context.Context) (int, error) {
	var swept int
	ran, err := uc.repo.WithLeadScoreLock(ctx, func() error {
		orgs, err := uc.repo.OrgsDueForRescore(ctx, leadScoreStaleness, 0)
		if err != nil {
			return err
		}
		for _, orgID := range orgs {
			n, err := uc.rescoreOrg(ctx, orgID)
			if err != nil {
				// Truncation is not a failure of the rows that WERE written, but
				// it must be visible: the next pass starts over from the top, so
				// a permanently truncating org needs a human.
				uc.logger.Warn("lead scoring: org rescore incomplete",
					"org_id", orgID.String(), "contacts", n, "error", err)
				continue
			}
			swept++
		}
		return nil
	})
	if err != nil {
		return swept, err
	}
	if !ran {
		// Another replica holds the lock. Not an error.
		return 0, nil
	}
	return swept, nil
}
