package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ActionExecutor interface that all action executors must implement.
type ActionExecutor interface {
	Execute(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (output any, err error)
}

// executeAction dispatches an action to the appropriate executor.
func (e *Engine) executeAction(ctx context.Context, run *WorkflowRun, action ActionSpec, evalCtx EvalContext) (any, error) {
	executor, ok := e.executors[action.Type]
	if !ok {
		return nil, fmt.Errorf("unknown action type: %s", action.Type)
	}
	return executor.Execute(ctx, run, action, evalCtx)
}

// getStringParam extracts a string param with template interpolation.
func getStringParam(params map[string]any, key string, evalCtx EvalContext) string {
	val, ok := params[key]
	if !ok {
		return ""
	}
	str, ok := val.(string)
	if !ok {
		return fmt.Sprintf("%v", val)
	}
	return InterpolateTemplate(str, evalCtx)
}

// getStringSliceParam extracts a []string param with template interpolation.
// Accepts []string, []any, or a comma-separated string (e.g. "a@x.com, b@x.com").
func getStringSliceParam(params map[string]any, key string, evalCtx EvalContext) []string {
	val, ok := params[key]
	if !ok {
		return nil
	}
	switch v := val.(type) {
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, InterpolateTemplate(s, evalCtx))
			}
		}
		return result
	case []string:
		result := make([]string, 0, len(v))
		for _, s := range v {
			result = append(result, InterpolateTemplate(s, evalCtx))
		}
		return result
	case string:
		// Support comma-separated string (e.g. from TemplateInput)
		interpolated := InterpolateTemplate(v, evalCtx)
		if interpolated == "" {
			return nil
		}
		parts := strings.Split(interpolated, ",")
		result := make([]string, 0, len(parts))
		for _, p := range parts {
			trimmed := strings.TrimSpace(p)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		if len(result) == 0 {
			return nil
		}
		return result
	default:
		return nil
	}
}

// getIntParam extracts an integer param.
func getIntParam(params map[string]any, key string) int {
	val, ok := params[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

// getMapParam extracts a map[string]string param with template interpolation.
func getMapParam(params map[string]any, key string, evalCtx EvalContext) map[string]string {
	val, ok := params[key]
	if !ok {
		return nil
	}
	m, ok := val.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			result[k] = InterpolateTemplate(s, evalCtx)
		} else {
			result[k] = fmt.Sprintf("%v", v)
		}
	}
	return result
}
