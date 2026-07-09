package automation

// Durable delay support (overhaul phase A1).
//
// A delay step no longer sleeps inside a worker goroutine. Instead the engine
// persists the absolute deadline in a `waiting` action log plus run.wake_at,
// flips the run to StatusWaiting, and unwinds — freeing the worker within
// milliseconds. The retry sweeper (scheduler.sweepRetries) flips due waiting
// runs back to pending; on resume the parked log completes and execution
// continues after the delay step. A restart mid-wait changes nothing: the
// deadline was persisted before parking, so elapsed time is never lost.

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// stepsExecState carries a run's resume state through the recursive steps walk.
// Keys are both step IDs and structural step paths (BuildStepPath) because
// action logs are keyed by path while workflow definitions reference IDs.
type stepsExecState struct {
	completed map[string]bool               // steps that finished successfully
	waiting   map[string]*WorkflowActionLog // parked delay logs by ActionPath
	started   map[string]bool               // completed ∪ waiting — pins condition branches
}

func newStepsExecState() *stepsExecState {
	return &stepsExecState{
		completed: make(map[string]bool),
		waiting:   make(map[string]*WorkflowActionLog),
		started:   make(map[string]bool),
	}
}

func (s *stepsExecState) markCompleted(id, path string) {
	if id != "" {
		s.completed[id] = true
		s.started[id] = true
	}
	if path != "" {
		s.completed[path] = true
		s.started[path] = true
	}
}

// syncRunCompleted mirrors the completed-set into run.CompletedActions so
// crash recovery and follow-up attempts skip finished steps.
func syncRunCompleted(run *WorkflowRun, state *stepsExecState) {
	var completedList []string
	for k := range state.completed {
		completedList = append(completedList, k)
	}
	completedJSON, _ := json.Marshal(completedList)
	run.CompletedActions = datatypes.JSON(completedJSON)
}

// stepPathIndex maps every step's structural path to its step ID so logs
// (path-keyed) can be aliased back to definition IDs on resume.
func stepPathIndex(steps []StepSpec, parentPath string, branch string) map[string]string {
	idx := make(map[string]string)
	var walk func(steps []StepSpec, parentPath string, branch string)
	walk = func(steps []StepSpec, parentPath string, branch string) {
		for i, s := range steps {
			path := BuildStepPath(parentPath, branch, i)
			if s.ID != "" {
				idx[path] = s.ID
			}
			if s.Type == "condition" {
				walk(s.YesSteps, path, "yes")
				walk(s.NoSteps, path, "no")
			}
		}
	}
	walk(steps, parentPath, branch)
	return idx
}

// hasAnyStepStarted reports whether any step in the subtree has a success or
// waiting log, matched by step ID or structural path. Used to pin a condition
// to the branch it already entered — including a branch whose only progress is
// a parked delay, which must not flip sides when the run resumes.
func hasAnyStepStarted(steps []StepSpec, started map[string]bool, parentPath string, branch string) bool {
	for i, s := range steps {
		path := BuildStepPath(parentPath, branch, i)
		switch s.Type {
		case "action", "delay":
			if started[s.ID] || started[path] {
				return true
			}
		case "condition":
			if hasAnyStepStarted(s.YesSteps, started, path, "yes") || hasAnyStepStarted(s.NoSteps, started, path, "no") {
				return true
			}
		}
	}
	return false
}

// wakeAtFromLog extracts the persisted deadline from a waiting log's output.
// A zero time (missing/corrupt output) reads as "due now" so a damaged log
// can never wedge a run forever.
func wakeAtFromLog(lg *WorkflowActionLog) time.Time {
	if lg == nil || len(lg.Output) == 0 {
		return time.Time{}
	}
	var out struct {
		WakeAt time.Time `json:"wake_at"`
	}
	if err := json.Unmarshal(lg.Output, &out); err != nil {
		return time.Time{}
	}
	return out.WakeAt
}

// handleDelayStep processes one delay step. Returns proceed=true when the
// delay is satisfied and the walk should continue to the next step, or
// proceed=false when the run has been parked (StatusWaiting) and the walk
// must unwind without completing or failing the run.
//
// Two delay shapes share this path (A4.4): a fixed duration and a wait-until that
// resolves its deadline from a record date field on evalCtx. Both persist an
// absolute wake_at, so resume/crash-recovery is identical (the deadline is read
// back from the parked log, never recomputed).
func (e *Engine) handleDelayStep(step StepSpec, stepPath string, run *WorkflowRun, state *stepsExecState, evalCtx *EvalContext) (bool, error) {
	now := time.Now()

	// Resume path: this step already parked the run once.
	if wlog := state.waiting[stepPath]; wlog != nil {
		wakeAt := wakeAtFromLog(wlog)
		if wakeAt.After(now) {
			// Woken early (duplicate queue push, manual requeue, crash
			// recovery): park again with the original deadline.
			run.Status = StatusWaiting
			run.WakeAt = &wakeAt
			run.NextRetryAt = nil
			if e.repo != nil {
				if err := e.repo.UpdateRunStandalone(e.ctx, run); err != nil {
					e.logger.Error("automation: re-park waiting run failed", "error", err, "run_id", run.ID.String())
				}
			}
			e.logger.Info("automation: delay not due, re-parked run", "run_id", run.ID.String(), "step_id", step.ID, "wake_at", wakeAt)
			return false, nil
		}

		// Deadline reached — complete the parked log and continue.
		wlog.Status = LogStatusSuccess
		wlog.DurationMs = now.Sub(wlog.CreatedAt).Milliseconds()
		output := delayOutputFields(step, wakeAt, true)
		outputJSON, _ := json.Marshal(output)
		wlog.Output = datatypes.JSON(outputJSON)

		state.markCompleted(step.ID, stepPath)
		syncRunCompleted(run, state)
		run.RetryCount = 0
		run.LastError = ""
		run.NextRetryAt = nil
		run.WakeAt = nil

		if e.repo != nil {
			if err := e.commitActionAndRun(wlog, run); err != nil {
				e.logger.Error("automation: commit delay completion failed", "error", err, "run_id", run.ID.String())
				return false, err
			}
		}
		if evalCtx.Actions == nil {
			evalCtx.Actions = make(map[string]any)
		}
		evalCtx.Actions[step.ID] = output
		e.logger.Info("automation: delay elapsed, run resumed", "run_id", run.ID.String(), "step_id", step.ID)
		return true, nil
	}

	// First encounter: resolve the deadline and park (or proceed if already due).
	wakeAt, resolved := resolveDelayWakeAt(step, evalCtx, now)
	shouldPark := resolved && wakeAt.After(now)

	actionLog := &WorkflowActionLog{
		ID:         uuid.New(),
		RunID:      run.ID,
		ActionPath: stepPath,
		ActionType: ActionDelay,
		Status:     "running",
		AttemptNo:  run.RetryCount + 1,
		CreatedAt:  now,
	}
	inputJSON, _ := json.Marshal(delayInputFields(step))
	actionLog.Input = datatypes.JSON(inputJSON)

	// Proceed immediately when there's no durable repo (pure tree-walk tests), or the
	// deadline is already due / unresolvable (zero duration, a wait-until whose date
	// field is empty or already past). A wait "until" a moment that has passed is
	// trivially satisfied.
	if e.repo == nil || !shouldPark {
		state.markCompleted(step.ID, stepPath)
		if evalCtx.Actions == nil {
			evalCtx.Actions = make(map[string]any)
		}
		output := delayOutputFields(step, wakeAt, false)
		evalCtx.Actions[step.ID] = output
		if e.repo != nil {
			actionLog.Status = LogStatusSuccess
			outputJSON, _ := json.Marshal(output)
			actionLog.Output = datatypes.JSON(outputJSON)
			syncRunCompleted(run, state)
			if err := e.repo.CreateActionLogStandalone(e.ctx, actionLog); err != nil {
				e.logger.Error("automation: create zero-delay log failed", "error", err, "run_id", run.ID.String())
			}
			if err := e.repo.UpdateRunStandalone(e.ctx, run); err != nil {
				e.logger.Error("automation: update run after zero-delay failed", "error", err, "run_id", run.ID.String())
			}
		}
		return true, nil
	}

	if err := e.repo.CreateActionLogStandalone(e.ctx, actionLog); err != nil {
		e.logger.Error("automation: create delay log failed", "error", err, "run_id", run.ID.String())
	}

	actionLog.Status = LogStatusWaiting
	outputJSON, _ := json.Marshal(delayOutputFields(step, wakeAt, false))
	actionLog.Output = datatypes.JSON(outputJSON)

	run.Status = StatusWaiting
	run.WakeAt = &wakeAt
	run.NextRetryAt = nil
	run.LastError = ""

	if err := e.commitActionAndRun(actionLog, run); err != nil {
		e.logger.Error("automation: commit delay park failed", "error", err, "run_id", run.ID.String())
		return false, err
	}
	state.waiting[stepPath] = actionLog
	state.started[stepPath] = true
	if step.ID != "" {
		state.started[step.ID] = true
	}
	e.logger.Info("automation: run parked on delay", "run_id", run.ID.String(), "step_id", step.ID, "wake_at", wakeAt)
	return false, nil
}

// resolveDelayWakeAt computes the absolute wake time for a delay step. A wait-until
// delay resolves the record date field from the eval context (same path resolution
// as conditions/templates) and applies offset_days / at_time / timezone via the
// shared date_field math; a fixed delay is now + duration. The bool is false only
// when a wait-until field can't be resolved to a date — the caller then proceeds
// immediately, since there is no future moment to wait for.
func resolveDelayWakeAt(step StepSpec, evalCtx *EvalContext, now time.Time) (time.Time, bool) {
	d := step.Delay
	if d == nil {
		return now, true
	}
	if d.IsWaitUntil() {
		val := resolvePath(d.UntilField, *evalCtx)
		fireAt, ok := computeDateFieldFireAt(val, dateFieldParams{OffsetDays: d.OffsetDays, AtTime: d.AtTime, Timezone: d.Timezone}, now)
		if !ok {
			return time.Time{}, false
		}
		return fireAt, true
	}
	if d.DurationSec <= 0 {
		return now, true
	}
	return now.Add(time.Duration(d.DurationSec) * time.Second), true
}

// delayInputFields describes a delay step's configuration for its action log Input.
func delayInputFields(step StepSpec) map[string]any {
	if d := step.Delay; d != nil && d.IsWaitUntil() {
		return map[string]any{
			"until_field": d.UntilField,
			"offset_days": d.OffsetDays,
			"at_time":     d.AtTime,
			"timezone":    d.Timezone,
		}
	}
	return map[string]any{"duration_sec": delayDurationSec(step)}
}

// delayOutputFields is the delay step's action-log Output. wake_at is the durable
// deadline read back on resume (wakeAtFromLog), so it must always be present.
func delayOutputFields(step StepSpec, wakeAt time.Time, resumed bool) map[string]any {
	out := map[string]any{"wake_at": wakeAt, "resumed": resumed}
	if d := step.Delay; d != nil && d.IsWaitUntil() {
		out["until_field"] = d.UntilField
		out["offset_days"] = d.OffsetDays
	} else {
		out["duration_sec"] = delayDurationSec(step)
	}
	return out
}

func delayDurationSec(step StepSpec) int {
	if step.Delay == nil {
		return 0
	}
	return step.Delay.DurationSec
}
