package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// steps_required_test.go covers the two decisions R5 deploy 1 had to make explicitly
// rather than inherit:
//
//  1. "REQUIRE STEPS." The A8 deprecation note said "after this date, remove this
//     fallback and require Steps." Removing the fallback is not the same as requiring
//     steps: with validateActions simply deleted, a body carrying no steps would have
//     had nothing validate it at all. So the validator now rejects an empty tree
//     outright, and these tests assert it at the HTTP boundary, which is where the
//     defect was reachable (handlers.go's hasSteps("[]") was false, so `steps: []` fell
//     through to the flat validator and created a live workflow that ran nothing).
//
//  2. "KEEP DelayExecutor." R5 listed it as a flat-path artifact. It is not. See
//     TestDelayAsActionStep_IsReachable.

// ── 1. A workflow needs steps ─────────────────────────────────────────────────

// stepsReqHandler builds a Handler for the rejection paths only. repo / db / engine are
// deliberately nil: CreateWorkflow must reject an unusable body BEFORE it touches any of
// them, so a regression that started persisting first would panic here rather than
// quietly writing a workflow that executes nothing.
func stepsReqHandler() *Handler {
	return &Handler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func stepsReqRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/api/workflows", func(c *gin.Context) {
		c.Set("org_id", uuid.New())
		c.Set("user_id", uuid.New())
	}, h.CreateWorkflow)
	return r
}

// stepsReqErrorBody is the decoded error envelope, including the per-field details the
// builder surfaces to the author.
type stepsReqErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func stepsReqPost(t *testing.T, body string) (int, stepsReqErrorBody) {
	t.Helper()
	h := stepsReqHandler()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/workflows", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	stepsReqRouter(h).ServeHTTP(w, req)

	var out stepsReqErrorBody
	if w.Body.Len() > 0 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w.Code, out
}

// TestCreateWorkflow_RequiresSteps is R5's stated "done when": an Actions-only or
// empty-steps POST to /api/workflows is rejected with a useful message.
//
// "Useful" is asserted, not assumed. The rejection has to name the `steps` field (the
// builder highlights by field path) and has to tell the author what to do about it —
// a bare "invalid payload" would be indistinguishable from the 400 they got before,
// when an actions-only body was accepted and produced a workflow that never ran.
func TestCreateWorkflow_RequiresSteps(t *testing.T) {
	cases := map[string]string{
		// What a pre-R5 client sends: the flat action list, no steps. `actions` is not
		// even a request field any more, so it is ignored and the body has no steps.
		"actions-only body": `{"name":"legacy client","trigger":{"type":"contact_created"},
			"actions":[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]}`,
		// The shape that used to slip through hasSteps() and reach the flat validator.
		"explicit empty steps": `{"name":"empty tree","trigger":{"type":"contact_created"},"steps":[]}`,
		// jsonb null, which the engine treats identically to absent.
		"null steps": `{"name":"null tree","trigger":{"type":"contact_created"},"steps":null}`,
		"no steps key at all": `{"name":"nothing","trigger":{"type":"contact_created"}}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			code, resp := stepsReqPost(t, body)

			require.Equal(t, http.StatusBadRequest, code, "body: %s", body)
			assert.Equal(t, "VALIDATION_FAILED", resp.Error.Code)

			var stepsErr string
			for _, d := range resp.Error.Details {
				if d.Field == "steps" {
					stepsErr = d.Message
				}
			}
			require.NotEmpty(t, stepsErr,
				"the rejection must name the `steps` field so the builder can highlight it; got %+v", resp.Error)
			assert.Contains(t, stepsErr, "at least one step",
				"the message must tell the author what to add")
			assert.Contains(t, strings.ToLower(stepsErr), "actions",
				"a client still sending `actions` needs to be told that is why it failed")
		})
	}
}

// A body WITH a real steps tree still passes validation — the guard rejects emptiness,
// not everything. (It gets past the validator and then nil-panics on the repo, which is
// exactly the seam this handler is built at; recovered so the assertion is the panic
// site, not a failed request.)
func TestCreateWorkflow_AcceptsARealStepsTree(t *testing.T) {
	h := stepsReqHandler()
	body := `{"name":"ok","trigger":{"type":"contact_created"},
		"steps":[{"type":"action","id":"a1","action":{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}}]}`

	reachedRepo := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				reachedRepo = true // validation passed; the nil repo is what blew up
			}
		}()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/workflows", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		stepsReqRouter(h).ServeHTTP(w, req)
		if w.Code == http.StatusBadRequest {
			t.Errorf("a valid steps tree must not be rejected: %s", w.Body.String())
		}
	}()
	assert.True(t, reachedRepo, "a valid body must get past validation to the persistence layer")
}

// ── 2. DelayExecutor is NOT a flat-path artifact ──────────────────────────────

// stepsReqSpyExecutor records every action it is handed.
type stepsReqSpyExecutor struct {
	calls []ActionSpec
	inner ActionExecutor
}

func (s *stepsReqSpyExecutor) Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	s.calls = append(s.calls, action)
	if s.inner != nil {
		return s.inner.Execute(ctx, run, action, evalCtx)
	}
	return map[string]any{"spied": true}, nil
}

// TestDelayAsActionStep_IsReachable is the evidence for KEEPING DelayExecutor and its
// default registration in Engine.Start, which the R5 plan listed for removal as a
// "flat-path artifact".
//
// It is not one. There are TWO ways to spell a wait, and only one of them is the
// durable path:
//
//	{"type":"delay",  "id":"d","delay": {"duration_sec":60}}            -> handleDelayStep
//	{"type":"action", "id":"d","action":{"type":"delay","params":{…}}}  -> DelayExecutor
//
// The second shape survives the teardown intact. ValidActionTypes contains "delay", so
// validateStepsRecursive accepts it and validateActionParams checks its params; the
// executor walker switches on step.Type, sees "action", and dispatches through
// executeAction into the executors map. The frontend permits it too — 'delay' is a
// member of both the ActionSpec type union and the zod actionTypes enum — and the AI
// copilot emits raw step trees, so nothing structurally prevents one being authored.
//
// Delete the executor or its registration and every such workflow starts failing with
// "unknown action type: delay". This test asserts BOTH halves of the routing: the
// action shape reaches the executors map, and the delay-step shape provably does not
// (so the durable wake_at path is untouched by keeping this).
func TestDelayAsActionStep_IsReachable(t *testing.T) {
	// The registry entry that makes the validator accept it in the first place.
	require.True(t, ValidActionTypes[ActionDelay],
		"if 'delay' ever leaves ValidActionTypes, the action shape stops validating and this changes")

	t.Run("the validator accepts a delay authored as an action step", func(t *testing.T) {
		steps := []byte(`[{"type":"action","id":"d1","action":{"type":"delay","id":"d1","params":{"duration_sec":60}}}]`)
		res := ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, steps)
		assert.True(t, res.Valid, "an action-shaped delay is a saveable workflow: %+v", res.Errors)

		// …and its params are genuinely checked, through validateActionParams.
		bad := []byte(`[{"type":"action","id":"d1","action":{"type":"delay","id":"d1","params":{"duration_sec":0}}}]`)
		res = ValidateWorkflowPayload([]byte(`{"type":"contact_created"}`), nil, bad)
		assert.False(t, res.Valid, "duration_sec=0 must still be rejected on the action shape")
	})

	t.Run("an action-shaped delay dispatches into the executors map", func(t *testing.T) {
		spy := &stepsReqSpyExecutor{inner: NewDelayExecutor()}
		e := &Engine{
			ctx:       context.Background(),
			logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			executors: map[string]ActionExecutor{ActionDelay: spy},
		}
		run := &WorkflowRun{ID: uuid.New()}
		evalCtx := EvalContext{Actions: map[string]any{}}

		steps := []StepSpec{actionStep(ActionSpec{
			ID: "d1", Type: ActionDelay, Params: map[string]any{"duration_sec": 1},
		})}
		completed, err := e.executeStepsRecursive(context.Background(), steps, run, nil, &evalCtx, "", "")
		require.NoError(t, err)
		assert.True(t, completed)

		require.Len(t, spy.calls, 1, "the action-shaped delay MUST reach the delay executor")
		assert.Equal(t, ActionDelay, spy.calls[0].Type)
		// The real DelayExecutor ran and reported its wait, so this is not just routing —
		// removing it would change behaviour, not merely delete an unused type.
		assert.Equal(t, map[string]any{"delayed_sec": 1}, evalCtx.Actions["d1"])
	})

	t.Run("a delay STEP never touches the executors map", func(t *testing.T) {
		spy := &stepsReqSpyExecutor{}
		e := &Engine{
			ctx:       context.Background(),
			logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
			executors: map[string]ActionExecutor{ActionDelay: spy},
		}
		run := &WorkflowRun{ID: uuid.New()}
		evalCtx := EvalContext{Actions: map[string]any{}}

		steps := []StepSpec{{Type: "delay", ID: "d1", Delay: &DelayParams{DurationSec: 1}}}
		_, err := e.executeStepsRecursive(context.Background(), steps, run, nil, &evalCtx, "", "")
		require.NoError(t, err)

		assert.Empty(t, spy.calls,
			"a delay step is the DURABLE wake_at path — routing it through DelayExecutor would "+
				"turn a month-long wait into a blocked worker goroutine")
	})
}
