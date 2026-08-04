package usecase

import (
	"strings"
	"testing"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"github.com/google/uuid"
)

// ---------- activation policy ----------

func TestShouldActivate(t *testing.T) {
	auto := domain.TemplateWorkflow{
		Activation: domain.TemplateActivationAuto,
		Trigger:    domain.TemplateTrigger{Type: "contact_created"},
	}

	t.Run("internal-only actions activate", func(t *testing.T) {
		on, _ := shouldActivate(auto, []automation.ActionSpec{
			{Type: "create_task"}, {Type: "log_activity"}, {Type: "update_record"},
		})
		if !on {
			t.Error("internal-only workflow should be switched on")
		}
	})

	t.Run("manual activation is honoured", func(t *testing.T) {
		w := auto
		w.Activation = domain.TemplateActivationManual
		if on, reason := shouldActivate(w, []automation.ActionSpec{{Type: "create_task"}}); on || reason == "" {
			t.Error("manual template must stay off, with a reason")
		}
	})

	// The whole point of the policy: a freshly created workspace must not start
	// emailing the customer's real contacts.
	t.Run("outbound actions block activation", func(t *testing.T) {
		for _, actionType := range []string{"send_email", "send_webhook", "ai_generate", "enroll_records", "notify_user"} {
			on, reason := shouldActivate(auto, []automation.ActionSpec{
				{Type: "create_task"}, {Type: actionType},
			})
			if on {
				t.Errorf("%s must block activation", actionType)
			}
			if reason == "" {
				t.Errorf("%s should explain why it stayed off", actionType)
			}
		}
	})

	// A schedule/date_field workflow's timers are armed by the HTTP handler, not by
	// anything the apply engine can reach — activating one produces a workflow that
	// looks live and never fires.
	t.Run("timer-backed triggers never auto-activate", func(t *testing.T) {
		for _, trig := range []string{"schedule", "date_field"} {
			w := auto
			w.Trigger = domain.TemplateTrigger{Type: trig}
			if on, _ := shouldActivate(w, []automation.ActionSpec{{Type: "create_task"}}); on {
				t.Errorf("%s trigger must not auto-activate: its timers are never armed", trig)
			}
		}
	})
}

// TestShouldActivate_SeesActionsInsideBranches is the regression guard for the ONE
// thing R5 deploy 1 was told to delete and must not have.
//
// applyWorkflows feeds shouldActivate the output of automation.FlattenStepsToActions.
// The R5 teardown plan listed that function for removal alongside the deprecated
// `actions` column, on the reasoning that flattening only ever existed to fill it. It
// did not: the flatten is what makes the activation allow-list see into If/Else
// branches. Replace it with nil, or with a walk over top-level steps only, and a
// template whose send_email sits inside a branch is auto-activated — a workspace that
// starts emailing the customer's real contacts seconds after it is created.
//
// The tests above all hand shouldActivate a pre-built slice, so every one of them
// passes with the flatten broken. This one starts from the steps tree.
func TestShouldActivate_SeesActionsInsideBranches(t *testing.T) {
	auto := domain.TemplateWorkflow{
		Activation: domain.TemplateActivationAuto,
		Trigger:    domain.TemplateTrigger{Type: "contact_created"},
	}

	// A tree whose ONLY outbound action is two levels down, in a no-branch.
	buried := []automation.StepSpec{
		{Type: "action", ID: "t1", Action: &automation.ActionSpec{ID: "t1", Type: "create_task"}},
		{
			Type: "condition", ID: "c1",
			Condition: &automation.ConditionGroup{Field: "contact.tier", Operator: "eq", Value: "gold"},
			YesSteps: []automation.StepSpec{
				{Type: "action", ID: "l1", Action: &automation.ActionSpec{ID: "l1", Type: "log_activity"}},
			},
			NoSteps: []automation.StepSpec{
				{Type: "action", ID: "e1", Action: &automation.ActionSpec{ID: "e1", Type: "send_email"}},
			},
		},
	}

	flat := automation.FlattenStepsToActions(buried)
	if len(flat) != 3 {
		t.Fatalf("the flatten must inline BOTH branches, got %d actions: %+v", len(flat), flat)
	}

	on, reason := shouldActivate(auto, flat)
	if on {
		t.Fatal("a send_email buried in a no-branch must block auto-activation")
	}
	if reason == "" || !strings.Contains(reason, "send_email") {
		t.Errorf("the reason must name the offending action, got %q", reason)
	}

	// And the same tree WITHOUT the outbound action still activates, so the guard is
	// discriminating rather than just always-off.
	safe := []automation.StepSpec{buried[0], {
		Type: "condition", ID: "c1",
		Condition: buried[1].Condition,
		YesSteps:  buried[1].YesSteps,
	}}
	if on, reason := shouldActivate(auto, automation.FlattenStepsToActions(safe)); !on {
		t.Errorf("an internal-only tree must still auto-activate, got reason %q", reason)
	}
}

// TestShouldActivate_DelayAllowListEntryIsTruthful pins the coupling between
// domain.autoActivatableActions["delay"] and the flattener.
//
// That allow-list entry is only meaningful because FlattenStepsToActions synthesises an
// ActionSpec{Type: "delay"} for every delay STEP — delays are not actions in the steps
// tree, so if the flattener stopped emitting them the map entry would quietly describe
// something that never occurs, and a reader trimming "dead" entries would remove it.
func TestShouldActivate_DelayAllowListEntryIsTruthful(t *testing.T) {
	flat := automation.FlattenStepsToActions([]automation.StepSpec{
		{Type: "delay", ID: "d1", Delay: &automation.DelayParams{DurationSec: 3600}},
	})
	if len(flat) != 1 || flat[0].Type != "delay" {
		t.Fatalf("a delay step must flatten to one action of type 'delay', got %+v", flat)
	}
	if !domain.IsAutoActivatableAction(flat[0].Type) {
		t.Fatal("the allow-list must accept the type the flattener actually emits for a delay")
	}
}

// ---------- pipeline replace-vs-append ----------

func stage(name string) domain.PipelineStage { return domain.PipelineStage{Name: name} }

func TestIsUntouchedSeedPipeline(t *testing.T) {
	seeded := []domain.PipelineStage{
		stage("Lead In"), stage("Qualified"), stage("Proposal"), stage("Negotiation"), stage("Closed Won"),
	}
	if !isUntouchedSeedPipeline(seeded) {
		t.Error("the factory pipeline should read as untouched")
	}

	// An empty pipeline is not "the untouched seed" — there is nothing to replace.
	if isUntouchedSeedPipeline(nil) {
		t.Error("empty pipeline must not be treated as replaceable seed")
	}

	renamed := append([]domain.PipelineStage(nil), seeded...)
	renamed[2] = stage("Pitch")
	if isUntouchedSeedPipeline(renamed) {
		t.Error("a renamed stage means the customer has customised the pipeline")
	}

	extra := append(append([]domain.PipelineStage(nil), seeded...), stage("Onboarding"))
	if isUntouchedSeedPipeline(extra) {
		t.Error("an added stage means the pipeline is customised")
	}

	reordered := []domain.PipelineStage{
		stage("Qualified"), stage("Lead In"), stage("Proposal"), stage("Negotiation"), stage("Closed Won"),
	}
	if isUntouchedSeedPipeline(reordered) {
		t.Error("a reordered pipeline is customised")
	}

	// Whitespace should not defeat the comparison.
	padded := []domain.PipelineStage{
		stage(" Lead In "), stage("Qualified"), stage("Proposal"), stage("Negotiation"), stage("Closed Won"),
	}
	if !isUntouchedSeedPipeline(padded) {
		t.Error("surrounding whitespace should not count as customisation")
	}
}

// ---------- trigger stage resolution ----------

func TestResolveTriggerParams(t *testing.T) {
	stageID := uuid.New().String()
	stages := map[string]string{"closed won": stageID}

	t.Run("stage name resolves to id", func(t *testing.T) {
		out, err := resolveTriggerParams(domain.TemplateTrigger{
			Type: "deal_stage_changed", Params: domain.JSON(`{"to_stage":"Closed Won"}`),
		}, stages)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out["to_stage"] != stageID {
			t.Errorf("expected %s, got %v", stageID, out["to_stage"])
		}
	})

	t.Run("wildcard is left alone", func(t *testing.T) {
		out, err := resolveTriggerParams(domain.TemplateTrigger{
			Type: "deal_stage_changed", Params: domain.JSON(`{"to_stage":"*"}`),
		}, stages)
		if err != nil || out["to_stage"] != "*" {
			t.Errorf("wildcard must pass through unchanged, got %v (%v)", out["to_stage"], err)
		}
	})

	t.Run("existing id is left alone", func(t *testing.T) {
		other := uuid.New().String()
		out, err := resolveTriggerParams(domain.TemplateTrigger{
			Type: "deal_stage_changed", Params: domain.JSON(`{"to_stage":"` + other + `"}`),
		}, stages)
		if err != nil || out["to_stage"] != other {
			t.Errorf("a uuid must pass through unchanged, got %v (%v)", out["to_stage"], err)
		}
	})

	// Failing loudly beats installing an automation that can never match anything.
	t.Run("unknown stage name is an error", func(t *testing.T) {
		if _, err := resolveTriggerParams(domain.TemplateTrigger{
			Type: "deal_stage_changed", Params: domain.JSON(`{"to_stage":"Nonexistent"}`),
		}, stages); err == nil {
			t.Error("an unresolvable stage name must fail the workflow")
		}
	})

	t.Run("absent params yield an empty object", func(t *testing.T) {
		out, err := resolveTriggerParams(domain.TemplateTrigger{Type: "contact_created"}, stages)
		if err != nil || out == nil || len(out) != 0 {
			t.Errorf("expected empty params, got %v (%v)", out, err)
		}
	})

	t.Run("null params yield an empty object", func(t *testing.T) {
		out, err := resolveTriggerParams(domain.TemplateTrigger{
			Type: "contact_created", Params: domain.JSON(`null`),
		}, stages)
		if err != nil || len(out) != 0 {
			t.Errorf("expected empty params, got %v (%v)", out, err)
		}
	})
}

// ---------- JSONB decoding ----------

func TestNonEmptyJSONGuardsNullAndAbsent(t *testing.T) {
	// A column added after the row exists reads back nil; a SQL NULL reads back the
	// literal "null". Both would break json.Unmarshal into a slice.
	if got := string(nonEmptyJSON(nil, "[]")); got != "[]" {
		t.Errorf("nil should fall back, got %s", got)
	}
	if got := string(nonEmptyJSON(domain.JSON("null"), "[]")); got != "[]" {
		t.Errorf("SQL null should fall back, got %s", got)
	}
	if got := string(nonEmptyJSON(domain.JSON(`[{"a":1}]`), "[]")); got != `[{"a":1}]` {
		t.Errorf("real content should pass through, got %s", got)
	}
}

func TestDecodeSpecToleratesLegacyPayloads(t *testing.T) {
	// The 2022 rows stored pipeline_stages as bare strings. Decoding must degrade to
	// "no stages" rather than erroring, so one stale row cannot break the catalog.
	row := &domain.SystemTemplate{
		Slug:           "legacy",
		PipelineStages: domain.JSON(`["New Lead","Viewing Scheduled"]`),
	}
	spec, err := decodeSpec(row)
	if err != nil {
		t.Fatalf("decodeSpec should not error on legacy content: %v", err)
	}
	if len(spec.stages) != 0 {
		t.Errorf("legacy string stages should decode to nothing, got %d", len(spec.stages))
	}
	if spec.kb == nil {
		t.Error("kb map must never be nil")
	}
}
