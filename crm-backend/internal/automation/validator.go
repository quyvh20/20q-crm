package automation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// ValidationError represents a structured validation error.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationResult contains the outcome of workflow validation.
type ValidationResult struct {
	Valid    bool              `json:"valid"`
	Errors   []ValidationError `json:"errors,omitempty"`
	Warnings []string          `json:"warnings,omitempty"`
}

// ValidateWorkflowPayload validates trigger, conditions, and actions/steps payloads.
func ValidateWorkflowPayload(triggerJSON, conditionsJSON, actionsJSON []byte, stepsJSON ...[]byte) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// Validate trigger
	validateTrigger(triggerJSON, result)

	// Validate conditions (nullable)
	if conditionsJSON != nil && len(conditionsJSON) > 0 && string(conditionsJSON) != "null" {
		validateConditions(conditionsJSON, result)
	}

	// Validate steps (recursive tree) or legacy actions (flat array)
	var steps []byte
	if len(stepsJSON) > 0 {
		steps = stepsJSON[0]
	}

	if len(steps) > 0 && string(steps) != "null" && string(steps) != "[]" {
		validateSteps(steps, result)
	} else {
		validateActions(actionsJSON, result)
	}

	return result
}

func validateSteps(data []byte, result *ValidationResult) {
	var steps []StepSpec
	if err := json.Unmarshal(data, &steps); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "steps",
			Message: fmt.Sprintf("invalid steps JSON: %s", err.Error()),
		})
		return
	}

	idSet := make(map[string]bool)
	validateStepsRecursive(steps, "steps", 0, idSet, result)
}

// MaxStepTreeDepth is the maximum allowed nesting depth for the steps tree.
// Depth 0 = top-level steps, depth 1 = inside a condition branch, etc.
const MaxStepTreeDepth = 5

func validateStepsRecursive(steps []StepSpec, path string, depth int, idSet map[string]bool, result *ValidationResult) {
	if depth > MaxStepTreeDepth {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   path,
			Message: fmt.Sprintf("step tree depth %d exceeds maximum of %d", depth, MaxStepTreeDepth),
		})
		return
	}
	for i, step := range steps {
		stepPath := fmt.Sprintf("%s[%d]", path, i)

		if step.ID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   stepPath + ".id",
				Message: "step id is required",
			})
		} else if idSet[step.ID] {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   stepPath + ".id",
				Message: fmt.Sprintf("duplicate step id: '%s'", step.ID),
			})
		} else {
			idSet[step.ID] = true
		}

		switch step.Type {
		case "action":
			if step.Action == nil {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   stepPath + ".action",
					Message: "action property is required for step type 'action'",
				})
				continue
			}
			if step.Action.ID == "" {
				step.Action.ID = step.ID
			}
			if step.Action.ID != step.ID {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   stepPath + ".action.id",
					Message: fmt.Sprintf("action id '%s' must match step id '%s'", step.Action.ID, step.ID),
				})
			}
			if !ValidActionTypes[step.Action.Type] {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   stepPath + ".action.type",
					Message: fmt.Sprintf("unknown action type: '%s'", step.Action.Type),
				})
			}
			validateActionParams(*step.Action, stepPath+".action", result)

		case "condition":
			if step.Condition == nil {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   stepPath + ".condition",
					Message: "condition property is required for step type 'condition'",
				})
			} else {
				if depth := getConditionDepth(*step.Condition); depth > 3 {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   stepPath + ".condition",
						Message: fmt.Sprintf("condition nesting depth %d exceeds maximum of 3", depth),
					})
				}
				validateConditionRules(step.Condition.Rules, stepPath+".condition", result)
			}
			validateStepsRecursive(step.YesSteps, stepPath+".yes_steps", depth+1, idSet, result)
			validateStepsRecursive(step.NoSteps, stepPath+".no_steps", depth+1, idSet, result)

		case "delay":
			if step.Delay == nil {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   stepPath + ".delay",
					Message: "delay requires 'delay' with 'duration_sec'",
				})
			} else {
				dummyAction := ActionSpec{
					Type: ActionDelay,
					ID:   step.ID,
					Params: map[string]any{
						"duration_sec": step.Delay.DurationSec,
					},
				}
				validateActionParams(dummyAction, stepPath, result)
			}

		default:
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   stepPath + ".type",
				Message: fmt.Sprintf("unknown step type: '%s'", step.Type),
			})
		}
	}
}

func validateTrigger(data []byte, result *ValidationResult) {
	if len(data) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "trigger",
			Message: "trigger is required",
		})
		return
	}

	var trigger TriggerSpec
	if err := json.Unmarshal(data, &trigger); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "trigger",
			Message: fmt.Sprintf("invalid trigger JSON: %s", err.Error()),
		})
		return
	}

	if !IsValidTriggerType(trigger.Type) {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "trigger.type",
			Message: fmt.Sprintf("unknown trigger type: '%s'. Valid built-in types: %s. Custom object triggers use '{slug}_created' or '{slug}_updated'.", trigger.Type, validTriggerTypesList()),
		})
	}

	// Validate trigger-specific params
	switch trigger.Type {
	case TriggerDealStageChanged:
		if trigger.Params == nil {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   "trigger.params",
				Message: "deal_stage_changed requires params with 'to_stage'",
			})
		} else {
			toStage, ok := trigger.Params["to_stage"]
			if !ok {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   "trigger.params.to_stage",
					Message: "deal_stage_changed requires 'to_stage' parameter",
				})
			} else if toStr, isStr := toStage.(string); isStr && toStr == "" {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   "trigger.params.to_stage",
					Message: "'to_stage' must not be empty — select a specific pipeline stage",
				})
			}
			// from_stage is optional — validate it's a non-empty string if present
			if fromStage, ok := trigger.Params["from_stage"]; ok {
				if fromStr, isStr := fromStage.(string); isStr && fromStr == "" {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   "trigger.params.from_stage",
						Message: "'from_stage' must not be empty (use '*' for any stage, or omit entirely)",
					})
				}
			}
		}
	case TriggerNoActivityDays:
		if trigger.Params == nil {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   "trigger.params",
				Message: "no_activity_days requires params with 'days' and 'entity'",
			})
		} else {
			if _, ok := trigger.Params["days"]; !ok {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   "trigger.params.days",
					Message: "'days' parameter is required",
				})
			}
			if entity, ok := trigger.Params["entity"]; ok {
				if e, ok := entity.(string); ok && e != "contact" && e != "deal" {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   "trigger.params.entity",
						Message: "entity must be 'contact' or 'deal'",
					})
				}
			} else {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   "trigger.params.entity",
					Message: "'entity' parameter is required",
				})
			}
		}
	default:
		// All *_updated triggers (contact_updated, subscription_updated, etc.)
		// support optional watch_field / watch_value for field-level filtering.
		if strings.HasSuffix(trigger.Type, "_updated") && trigger.Params != nil {
			if wf, ok := trigger.Params["watch_field"]; ok {
				wfStr, isStr := wf.(string)
				if !isStr || wfStr == "" {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   "trigger.params.watch_field",
						Message: "watch_field must be a non-empty string (e.g. 'contact.owner_user_id')",
					})
				}
			}
			if _, hasValue := trigger.Params["watch_value"]; hasValue {
				if _, hasField := trigger.Params["watch_field"]; !hasField {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   "trigger.params.watch_value",
						Message: "watch_value requires watch_field to be set",
					})
				}
			}
		}
	}
}

func validateConditions(data []byte, result *ValidationResult) {
	var group ConditionGroup
	if err := json.Unmarshal(data, &group); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "conditions",
			Message: fmt.Sprintf("invalid conditions JSON: %s", err.Error()),
		})
		return
	}

	// Check depth
	if depth := getConditionDepth(group); depth > 3 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "conditions",
			Message: fmt.Sprintf("condition nesting depth %d exceeds maximum of 3", depth),
		})
	}

	// Validate operators in rules
	validateConditionRules(group.Rules, "conditions", result)
}

func getConditionDepth(group ConditionGroup) int {
	if group.Op == "" {
		return 0
	}
	maxChildDepth := 0
	for _, rule := range group.Rules {
		if rule.IsGroup() {
			childGroup := ConditionGroup{Op: rule.Op, Rules: rule.Rules}
			childDepth := getConditionDepth(childGroup)
			if childDepth > maxChildDepth {
				maxChildDepth = childDepth
			}
		}
	}
	return 1 + maxChildDepth
}

func validateConditionRules(rules []ConditionRule, path string, result *ValidationResult) {
	for i, rule := range rules {
		rulePath := fmt.Sprintf("%s.rules[%d]", path, i)
		if rule.IsGroup() {
			if rule.Op != "AND" && rule.Op != "OR" {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   rulePath + ".op",
					Message: fmt.Sprintf("operator must be 'AND' or 'OR', got '%s'", rule.Op),
				})
			}
			validateConditionRules(rule.Rules, rulePath, result)
		} else {
			if rule.Field == "" {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   rulePath + ".field",
					Message: "field is required for leaf rules",
				})
			}
			if rule.Operator != "" && !ValidOperators[rule.Operator] {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   rulePath + ".operator",
					Message: fmt.Sprintf("unknown operator: '%s'", rule.Operator),
				})
			}
		}
	}
}

func validateActions(data []byte, result *ValidationResult) {
	if len(data) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "actions",
			Message: "actions array is required",
		})
		return
	}

	var actions []ActionSpec
	if err := json.Unmarshal(data, &actions); err != nil {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "actions",
			Message: fmt.Sprintf("invalid actions JSON: %s", err.Error()),
		})
		return
	}

	if len(actions) == 0 {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "actions",
			Message: "at least one action is required",
		})
		return
	}

	// Check for duplicate action IDs
	idSet := make(map[string]bool)
	for i, action := range actions {
		actionPath := fmt.Sprintf("actions[%d]", i)

		if action.ID == "" {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   actionPath + ".id",
				Message: "action id is required",
			})
		} else if idSet[action.ID] {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   actionPath + ".id",
				Message: fmt.Sprintf("duplicate action id: '%s'", action.ID),
			})
		} else {
			idSet[action.ID] = true
		}

		if !ValidActionTypes[action.Type] {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   actionPath + ".type",
				Message: fmt.Sprintf("unknown action type: '%s'", action.Type),
			})
		}

		// Type-specific validation
		validateActionParams(action, actionPath, result)
	}
}

func validateActionParams(action ActionSpec, path string, result *ValidationResult) {
	switch action.Type {
	case ActionSendEmail:
		if _, ok := action.Params["to"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.to",
				Message: "send_email requires 'to' parameter",
			})
		} else if toStr, ok := action.Params["to"].(string); ok {
			// Validate "to" is a valid email or template variable
			toStr = strings.TrimSpace(toStr)
			if toStr != "" && !isEmailOrTemplate(toStr) {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.to",
					Message: fmt.Sprintf("invalid email address: '%s' (must be email or {{template}})", toStr),
				})
			}
		}
		// Validate "cc" — comma-separated, each part must be email or template
		if ccVal, ok := action.Params["cc"]; ok {
			if ccStr, ok := ccVal.(string); ok {
				ccStr = strings.TrimSpace(ccStr)
				if ccStr != "" {
					for _, part := range strings.Split(ccStr, ",") {
						part = strings.TrimSpace(part)
						if part != "" && !isEmailOrTemplate(part) {
							result.Valid = false
							result.Errors = append(result.Errors, ValidationError{
								Field:   path + ".params.cc",
								Message: fmt.Sprintf("invalid CC address: '%s' (must be email or {{template}})", part),
							})
						}
					}
				}
			}
		}
	case ActionCreateTask:
		if _, ok := action.Params["title"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.title",
				Message: "create_task requires 'title' parameter",
			})
		}
	case ActionAssignUser:
		if _, ok := action.Params["entity"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.entity",
				Message: "assign_user requires 'entity' parameter",
			})
		}
		if strategy, ok := action.Params["strategy"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.strategy",
				Message: "assign_user requires 'strategy' parameter",
			})
		} else {
			strategyStr, _ := strategy.(string)
			validStrategies := map[string]bool{"specific": true, "round_robin": true, "least_loaded": true}
			if !validStrategies[strategyStr] {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.strategy",
					Message: fmt.Sprintf("invalid strategy: '%s'. Valid: specific, round_robin, least_loaded", strategyStr),
				})
			}
			if strategyStr == "specific" {
				if _, hasUID := action.Params["user_id"]; !hasUID {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   path + ".params.user_id",
						Message: "assign_user with strategy 'specific' requires 'user_id' parameter",
					})
				}
			}
		}
	case ActionSendWebhook:
		if _, ok := action.Params["url"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.url",
				Message: "send_webhook requires 'url' parameter",
			})
		}
	case ActionDelay:
		if raw, ok := action.Params["duration_sec"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.duration_sec",
				Message: "delay requires 'duration_sec' parameter",
			})
		} else {
			sec := 0.0
			switch v := raw.(type) {
			case float64:
				sec = v
			case int:
				sec = float64(v)
			case json.Number:
				sec, _ = v.Float64()
			}
			if sec <= 0 {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.duration_sec",
					Message: "duration_sec must be a positive integer",
				})
			} else if sec != float64(int(sec)) {
				// Reject fractional seconds (e.g. 60.5) — integer math only
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.duration_sec",
					Message: fmt.Sprintf("duration_sec must be a whole number, got %g", sec),
				})
			} else if sec > 2592000 { // 30 days
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.duration_sec",
					Message: fmt.Sprintf("duration_sec %d exceeds maximum of 2592000 (30 days)", int(sec)),
				})
			}
		}
	case ActionUpdateRecord, ActionUpdateContact:
		// Validate the "updates" array: []{ field, op, value }
		updatesRaw, hasUpdates := action.Params["updates"]
		if hasUpdates {
			// New format: params.updates = [{ field, op, value }, ...]
			updatesSlice, ok := updatesRaw.([]any)
			if !ok {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.updates",
					Message: "update_contact 'updates' must be an array of { field, op, value }",
				})
			} else if len(updatesSlice) == 0 {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.updates",
					Message: "update_contact 'updates' array must not be empty",
				})
			} else {
				for idx, entry := range updatesSlice {
					entryMap, ok := entry.(map[string]any)
					if !ok {
						result.Valid = false
						result.Errors = append(result.Errors, ValidationError{
							Field:   fmt.Sprintf("%s.params.updates[%d]", path, idx),
							Message: "each update must be an object with 'field' and 'op'",
						})
						continue
					}
					uPath := fmt.Sprintf("%s.params.updates[%d]", path, idx)
					// Validate field
					fieldVal, _ := entryMap["field"].(string)
					if fieldVal == "" {
						result.Valid = false
						result.Errors = append(result.Errors, ValidationError{
							Field:   uPath + ".field",
							Message: "'field' is required in each update",
						})
					}
					// Validate op
					opVal, _ := entryMap["op"].(string)
					validOps := map[string]bool{"set": true, "add": true, "remove": true, "increment": true, "decrement": true, "clear": true}
					if opVal == "" {
						result.Valid = false
						result.Errors = append(result.Errors, ValidationError{
							Field:   uPath + ".op",
							Message: "'op' is required in each update",
						})
					} else if !validOps[opVal] {
						result.Valid = false
						result.Errors = append(result.Errors, ValidationError{
							Field:   uPath + ".op",
							Message: fmt.Sprintf("invalid op '%s'. Valid: set, add, remove, increment, decrement, clear", opVal),
						})
					}
					// Value required for non-clear ops
					if opVal != "" && opVal != "clear" {
						if _, hasValue := entryMap["value"]; !hasValue {
							result.Valid = false
							result.Errors = append(result.Errors, ValidationError{
								Field:   uPath + ".value",
								Message: fmt.Sprintf("'value' is required for op '%s'", opVal),
							})
						}
					}

					// ── Schema-aware checks (field existence, op/type, value type) ──
					if fieldVal != "" && opVal != "" && validOps[opVal] {
						validateUpdateFieldSchema(fieldVal, opVal, entryMap["value"], uPath, result)
					}
				}
			}
		} else {
			// Legacy fallback: flat { field, operation, value }
			if _, ok := action.Params["field"]; !ok {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.field",
					Message: "update_contact requires 'updates' array or legacy 'field' parameter",
				})
			}
			if op, ok := action.Params["operation"]; !ok {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   path + ".params.operation",
					Message: "update_contact requires 'operation' parameter",
				})
			} else {
				opStr, _ := op.(string)
				validOps := map[string]bool{"set": true, "add": true, "remove": true, "increment": true, "decrement": true, "clear": true}
				if !validOps[opStr] {
					result.Valid = false
					result.Errors = append(result.Errors, ValidationError{
						Field:   path + ".params.operation",
						Message: fmt.Sprintf("invalid operation: '%s'. Valid: set, add, remove, increment, decrement, clear", opStr),
					})
				}
				if opStr != "clear" {
					if _, hasValue := action.Params["value"]; !hasValue {
						result.Valid = false
						result.Errors = append(result.Errors, ValidationError{
							Field:   path + ".params.value",
							Message: fmt.Sprintf("update_contact with operation '%s' requires 'value' parameter", opStr),
						})
					}
				}
			}
		}
	}

	// Check for template references (warning, not block)
	for key, val := range action.Params {
		if str, ok := val.(string); ok {
			matches := templatePattern.FindAllStringSubmatch(str, -1)
			for _, match := range matches {
				if len(match) >= 2 {
					p := match[1]
					parts := strings.SplitN(p, ".", 2)
					rootKey := parts[0]
					validRoots := map[string]bool{
						"contact": true, "deal": true, "trigger": true,
						"org": true, "user": true, "actions": true,
					}
					if !validRoots[rootKey] {
						result.Warnings = append(result.Warnings, fmt.Sprintf(
							"%s.params.%s: template '{{%s}}' references unknown root '%s'",
							path, key, p, rootKey,
						))
					}
				}
			}
		}
	}
}

func validTriggerTypesList() string {
	types := make([]string, 0)
	for t := range ValidTriggerTypes {
		types = append(types, t)
	}
	return strings.Join(types, ", ")
}

// isEmailOrTemplate returns true if s is a valid email address or contains a {{template}} variable.
func isEmailOrTemplate(s string) bool {
	// Template variable (e.g. {{contact.email}})
	if strings.Contains(s, "{{") && strings.Contains(s, "}}") {
		return true
	}
	return isValidEmail(s)
}

// isValidEmail returns true if s is a valid email address (no template support).
// Used for runtime validation after template resolution.
func isValidEmail(s string) bool {
	// Basic email validation: has @ and at least one dot after @
	at := strings.LastIndex(s, "@")
	if at < 1 {
		return false
	}
	domain := s[at+1:]
	return strings.Contains(domain, ".") && !strings.HasSuffix(domain, ".") && !strings.HasPrefix(domain, ".")
}

// ── Schema-aware update_record field validation ──────────────────────

// contactFieldTypes maps built-in contact column paths to their data types.
var contactFieldTypes = map[string]string{
	"first_name":    "string",
	"last_name":     "string",
	"email":         "string",
	"phone":         "string",
	"owner_user_id": "string",
	"company_id":    "string",
	"tags":          "array",
}

// dealFieldTypes maps built-in deal column paths to their data types.
var dealFieldTypes = map[string]string{
	"title":           "string",
	"value":           "number",
	"probability":     "number",
	"stage_id":        "string",
	"contact_id":      "string",
	"company_id":      "string",
	"owner_user_id":   "string",
	"is_won":          "boolean",
	"is_lost":         "boolean",
}

// opsValidForType defines which operations are valid per field type.
var opsValidForType = map[string]map[string]bool{
	"string": {"set": true, "add": true, "clear": true},
	"number": {"set": true, "add": true, "increment": true, "decrement": true, "clear": true},
	"array":  {"set": true, "add": true, "remove": true, "clear": true},
	"boolean": {"set": true, "clear": true},
	"date":    {"set": true, "clear": true},
	"select":  {"set": true, "clear": true},
}

// validateUpdateFieldSchema performs schema-aware checks on a single update entry:
//  1. Field existence: is the field path a known column, custom_field, or tags?
//  2. Operation/type compatibility: e.g., can't increment a string field.
//  3. Value type match: e.g., increment value must be numeric (after coercion).
//
// Does NOT block if the field starts with "custom_fields." (custom fields are
// org-specific and may not be known at pure-validation time — see warning).
func validateUpdateFieldSchema(fieldPath, op string, value any, uPath string, result *ValidationResult) {
	// Determine entity from field path prefix
	entity := "contact"
	field := fieldPath
	isCustomObject := false
	if strings.HasPrefix(fieldPath, "deal.") {
		entity = "deal"
		field = strings.TrimPrefix(fieldPath, "deal.")
	} else if strings.HasPrefix(fieldPath, "contact.") {
		field = strings.TrimPrefix(fieldPath, "contact.")
	} else if dotIdx := strings.IndexByte(fieldPath, '.'); dotIdx > 0 {
		prefix := fieldPath[:dotIdx]
		// custom_fields and tags are sub-paths of contact/deal, NOT custom objects
		if prefix != "custom_fields" && prefix != "tags" {
			// Custom object: e.g. "ticket.status" — skip strict field registry
			// Custom objects store data as JSONB, so we accept any field name
			entity = prefix
			field = fieldPath[dotIdx+1:]
			isCustomObject = true
		}
	}

	// For custom objects, skip the built-in field registry check
	// (their fields are dynamic JSONB, not static columns)
	if isCustomObject {
		// Still validate op is valid
		return
	}

	fieldRegistry := contactFieldTypes
	if entity == "deal" {
		fieldRegistry = dealFieldTypes
	}

	// --- 1. Field existence check ---
	var fieldType string

	if entity == "contact" && field == "tags" {
		fieldType = "array"
	} else if strings.HasPrefix(field, "custom_fields.") {
		if op == "increment" || op == "decrement" {
			fieldType = "number"
		} else {
			fieldType = "string"
		}
	} else if ft, ok := fieldRegistry[field]; ok {
		fieldType = ft
	} else {
		result.Valid = false
		valid := make([]string, 0, len(fieldRegistry))
		for k := range fieldRegistry {
			valid = append(valid, k)
		}
		result.Errors = append(result.Errors, ValidationError{
			Field: uPath + ".field",
			Message: fmt.Sprintf(
				"unknown %s field '%s'. Valid: %s, or custom_fields.<key>",
				entity, fieldPath, strings.Join(valid, ", "),
			),
		})
		return
	}

	// --- 2. Operation/type compatibility ---
	allowedOps, typeKnown := opsValidForType[fieldType]
	if typeKnown && !allowedOps[op] {
		result.Valid = false
		allowed := make([]string, 0, len(allowedOps))
		for k := range allowedOps {
			allowed = append(allowed, k)
		}
		result.Errors = append(result.Errors, ValidationError{
			Field: uPath + ".op",
			Message: fmt.Sprintf(
				"operation '%s' is not valid for %s field '%s'. Allowed: %s",
				op, fieldType, fieldPath, strings.Join(allowed, ", "),
			),
		})
		return
	}

	// --- 3. Value type compatibility (skip for 'clear' which has no value) ---
	if op == "clear" || value == nil {
		return
	}

	// Skip type checking if value is a template variable (will be resolved at runtime)
	if strVal, ok := value.(string); ok && strings.Contains(strVal, "{{") {
		return
	}

	switch fieldType {
	case "number":
		// increment/decrement/set on number: value must be numeric or coercible
		if !isNumericValue(value) {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   uPath + ".value",
				Message: fmt.Sprintf("value for %s field '%s' must be numeric, got %T", op, fieldPath, value),
			})
		}
	case "array":
		// add/remove/set on array: value should be a string, []string, or []any
		switch value.(type) {
		case string, []any, []string:
			// valid
		default:
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   uPath + ".value",
				Message: fmt.Sprintf("value for array field '%s' must be a string or array, got %T", fieldPath, value),
			})
		}
	case "boolean":
		// set on boolean: value should be bool or "true"/"false"
		switch v := value.(type) {
		case bool:
			// valid
		case string:
			if v != "true" && v != "false" {
				result.Valid = false
				result.Errors = append(result.Errors, ValidationError{
					Field:   uPath + ".value",
					Message: fmt.Sprintf("value for boolean field '%s' must be true/false, got '%s'", fieldPath, v),
				})
			}
		default:
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   uPath + ".value",
				Message: fmt.Sprintf("value for boolean field '%s' must be a boolean, got %T", fieldPath, value),
			})
		}
	// string, date, select: any string value is acceptable
	}
}

// isNumericValue checks if a value is numeric or can be coerced to a number.
func isNumericValue(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, int32:
		return true
	case json.Number:
		return true
	case string:
		s := v.(string)
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	default:
		return false
	}
}

