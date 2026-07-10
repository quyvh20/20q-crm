package ai

import (
	"context"
	"encoding/json"
	"net/url"
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
	ro := toolNames(AllowedToolsWithSchema(true, false, nil, nil))
	if ro["create_deal"] || ro["update_deal"] || ro["create_task"] {
		t.Fatal("read-only tool set must not include write tools")
	}
	if !ro["search_deals"] || !ro["get_analytics"] {
		t.Fatal("read-only tool set must keep the read tools")
	}
	full := toolNames(AllowedToolsWithSchema(false, false, nil, nil))
	if !full["create_deal"] || !full["update_deal"] {
		t.Fatal("writer tool set must include the write tools")
	}
}

// A7.4: create_workflow/update_workflow are gated by workflows.manage, independent
// of the records read/write split.
func TestAllowedToolsWithSchema_WorkflowToolsGatedByCapability(t *testing.T) {
	// A writer WITHOUT workflows.manage must not see the authoring tools.
	writerNoWf := toolNames(AllowedToolsWithSchema(false, false, nil, nil))
	if writerNoWf["create_workflow"] || writerNoWf["update_workflow"] {
		t.Fatal("workflow tools must be hidden without workflows.manage")
	}
	// With workflows.manage they appear.
	writerWf := toolNames(AllowedToolsWithSchema(false, true, nil, nil))
	if !writerWf["create_workflow"] || !writerWf["update_workflow"] {
		t.Fatal("workflow tools must appear with workflows.manage")
	}
	// They appear even for a caller that is read-only on records (the gate is
	// workflows.manage, not records.write), and such a caller still gets no record
	// write tools.
	readerWf := toolNames(AllowedToolsWithSchema(true, true, nil, nil))
	if !readerWf["create_workflow"] || !readerWf["update_workflow"] {
		t.Fatal("workflow tools must appear for a read-only caller that has workflows.manage")
	}
	if readerWf["create_deal"] || readerWf["update_deal"] {
		t.Fatal("read-only caller must still not receive record write tools")
	}
}

// A7.4: emitWorkflowHandoff builds the navigate to the builder's copilot with the
// prompt in the `ai` query param (create → /workflows/new, update → /workflows/:id).
func TestEmitWorkflowHandoff(t *testing.T) {
	cc := &CommandCenter{}

	// create_workflow → /workflows/new?ai=<description>
	events := make(chan CommandEvent, 4)
	cc.emitWorkflowHandoff(events, ToolCall{
		Name:   "create_workflow",
		Params: json.RawMessage(`{"description":"when a deal is won, notify the owner"}`),
	})
	nav := <-events
	if nav.Type != "navigate" {
		t.Fatalf("expected navigate event, got %q", nav.Type)
	}
	var navData struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(nav.Data, &navData); err != nil {
		t.Fatalf("navdata: %v", err)
	}
	if !strings.HasPrefix(navData.Path, "/workflows/new?ai=") {
		t.Fatalf("create should navigate to /workflows/new with the prompt, got %q", navData.Path)
	}
	if !strings.Contains(navData.Path, url.QueryEscape("when a deal is won, notify the owner")) {
		t.Fatalf("create path must carry the escaped description, got %q", navData.Path)
	}
	if ack := <-events; ack.Type != "response" || ack.Message == "" {
		t.Fatalf("expected a text ack after navigate, got %+v", ack)
	}

	// update_workflow → /workflows/<id>?ai=<instruction>
	events2 := make(chan CommandEvent, 4)
	cc.emitWorkflowHandoff(events2, ToolCall{
		Name:   "update_workflow",
		Params: json.RawMessage(`{"workflow_id":"wf-123","instruction":"also send a Slack message"}`),
	})
	nav2 := <-events2
	var navData2 struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(nav2.Data, &navData2)
	if !strings.HasPrefix(navData2.Path, "/workflows/wf-123?ai=") {
		t.Fatalf("update should navigate to the workflow's builder, got %q", navData2.Path)
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
