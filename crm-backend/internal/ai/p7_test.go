package ai

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// fakeAuthz is a configurable aiAuthorizer for the P7/P8 convergence tests.
type fakeAuthz struct {
	caps   []string         // capabilities the caller holds
	allow  map[string]bool  // "slug:action" → allowed (OLS)
	mask   domain.FieldMask // FLS mask returned for any object
	audits []domain.AuditEntry
}

func (f *fakeAuthz) Authorize(_ context.Context, _ uuid.UUID, slug string, action domain.RecordAction) error {
	if f.allow[slug+":"+string(action)] {
		return nil
	}
	return domain.NewAppError(403, "OLS denied")
}
func (f *fakeAuthz) FieldMask(_ context.Context, _ uuid.UUID, _ string) domain.FieldMask {
	return f.mask
}
func (f *fakeAuthz) HasCapability(_ context.Context, _ uuid.UUID, capability string) error {
	for _, c := range f.caps {
		if c == capability {
			return nil
		}
	}
	return domain.NewAppError(403, "missing "+capability)
}
func (f *fakeAuthz) CallerCapabilities(_ context.Context, _ uuid.UUID) []string { return f.caps }
func (f *fakeAuthz) Audit(_ context.Context, e domain.AuditEntry)               { f.audits = append(f.audits, e) }

func toolNames(tools []Tool) map[string]bool {
	m := make(map[string]bool, len(tools))
	for _, t := range tools {
		m[t.Name] = true
	}
	return m
}

// ── Tool list derives from can-write, not role name ──────────────────────────

func TestAllowedToolsWithSchema_ReadOnlyDropsWriteTools(t *testing.T) {
	ro := toolNames(AllowedToolsWithSchema(true, nil, nil))
	if ro["create_deal"] || ro["update_deal"] || ro["create_task"] {
		t.Fatal("read-only tool set must not include write tools")
	}
	if !ro["search_deals"] || !ro["get_analytics"] {
		t.Fatal("read-only tool set must keep the read tools")
	}
	full := toolNames(AllowedToolsWithSchema(false, nil, nil))
	if !full["create_deal"] || !full["update_deal"] {
		t.Fatal("writer tool set must include the write tools")
	}
}

// ── effectiveScope: DataScope wins; sales_rep name is the bridge fallback ─────

func TestEffectiveScope(t *testing.T) {
	if got := effectiveScope(domain.Caller{DataScope: domain.DataScopeOwn}); got != domain.DataScopeOwn {
		t.Fatalf("explicit own scope: got %q", got)
	}
	if got := effectiveScope(domain.Caller{Role: domain.RoleSales}); got != domain.DataScopeOwn {
		t.Fatalf("sales_rep name fallback should be own, got %q", got)
	}
	if got := effectiveScope(domain.Caller{Role: "custom"}); got != domain.DataScopeAll {
		t.Fatalf("unknown role with no scope should default all, got %q", got)
	}
}

// ── callerCanWrite: capability/OLS derived, owner + nil-authz bridge ──────────

func TestCallerCanWrite(t *testing.T) {
	cc := newTestCC(nil)
	ctx := context.Background()
	org := uuid.New()

	// nil authz → legacy bridge: any non-viewer can write.
	if !cc.callerCanWrite(ctx, org, domain.Caller{Role: domain.RoleManager}) {
		t.Fatal("bridge: non-viewer should be able to write")
	}
	if cc.callerCanWrite(ctx, org, domain.Caller{Role: domain.RoleViewer}) {
		t.Fatal("bridge: viewer should not be able to write")
	}

	// owner bypasses regardless of authz grants.
	cc.authz = &fakeAuthz{}
	if !cc.callerCanWrite(ctx, org, domain.Caller{IsOwner: true}) {
		t.Fatal("owner should always be able to write")
	}
	// records.write capability grants write.
	cc.authz = &fakeAuthz{caps: []string{domain.CapRecordsWrite}}
	if !cc.callerCanWrite(ctx, org, domain.Caller{Role: "custom"}) {
		t.Fatal("records.write should grant write")
	}
	// OLS create on contact grants write even without records.write.
	cc.authz = &fakeAuthz{allow: map[string]bool{"contact:create": true}}
	if !cc.callerCanWrite(ctx, org, domain.Caller{Role: "custom"}) {
		t.Fatal("OLS create(contact) should grant write")
	}
	// nothing → cannot write (the custom-role narrowing P7 fixes).
	cc.authz = &fakeAuthz{}
	if cc.callerCanWrite(ctx, org, domain.Caller{Role: "custom"}) {
		t.Fatal("a capability-less custom role must not get write tools")
	}
}

// ── forecast gates on analytics.view, not role name ──────────────────────────

func TestToolGetAnalytics_ForecastGatedOnAnalyticsView(t *testing.T) {
	cc := newTestCC(nil)
	org, uid := uuid.New(), uuid.New()
	// Caller can read deals (OLS) but lacks analytics.view → forecast denied.
	cc.authz = &fakeAuthz{allow: map[string]bool{"deal:read": true}}
	out := cc.toolGetAnalytics(context.Background(), org, uid, "custom", map[string]interface{}{"metric": "forecast"})
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	if resp["error"] == nil {
		t.Fatalf("forecast without analytics.view must be denied, got %s", string(out))
	}
	if resp["forecast"] != nil {
		t.Fatal("no forecast data should leak on denial")
	}
}

// ── OLS read denial short-circuits a data tool ───────────────────────────────

func TestToolSearchDeals_OLSReadDenied(t *testing.T) {
	cc := newTestCC(nil)
	org, uid := uuid.New(), uuid.New()
	cc.authz = &fakeAuthz{} // deny everything
	out := cc.toolSearchDeals(context.Background(), org, uid, "custom", map[string]interface{}{"query": "acme"})
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	if resp["error"] == nil {
		t.Fatalf("a role with no OLS read on deals must be denied, got %s", string(out))
	}
}

// ── persona is templated from data_scope + capabilities, not the role name ───

func TestBuildRolePrompt_FromScopeAndCapabilities(t *testing.T) {
	cc := newTestCC(nil)
	org := uuid.New()
	ctx := context.Background()

	// Own-scope, no write, no analytics → OWN RECORDS ONLY + READ-ONLY.
	cc.authz = &fakeAuthz{}
	own := cc.buildRolePrompt(ctx, org, CommandRequest{UserRole: "custom"}, domain.Caller{Role: "custom", DataScope: domain.DataScopeOwn})
	if !strings.Contains(own, "OWN RECORDS ONLY") {
		t.Error("own-scope persona should say OWN RECORDS ONLY")
	}
	if !strings.Contains(own, "READ-ONLY") {
		t.Error("no-write persona should say READ-ONLY")
	}

	// All-scope writer with analytics → FULL RECORD ACCESS + analytics mention.
	cc.authz = &fakeAuthz{caps: []string{domain.CapRecordsWrite, domain.CapAnalyticsView}}
	full := cc.buildRolePrompt(ctx, org, CommandRequest{UserRole: "custom"}, domain.Caller{Role: "custom", DataScope: domain.DataScopeAll})
	if !strings.Contains(full, "FULL RECORD ACCESS") {
		t.Error("all-scope persona should say FULL RECORD ACCESS")
	}
	if strings.Contains(full, "READ-ONLY") {
		t.Error("a writer persona must not be marked READ-ONLY")
	}
	if !strings.Contains(strings.ToLower(full), "forecast") {
		t.Error("an analytics-capable persona should mention forecasts")
	}
}
