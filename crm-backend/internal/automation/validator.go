package automation

import (
	"encoding/json"
	"fmt"
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

// ValidateWorkflowPayload validates trigger, conditions, and actions payloads.
func ValidateWorkflowPayload(triggerJSON, conditionsJSON, actionsJSON []byte) *ValidationResult {
	result := &ValidationResult{Valid: true}

	// Validate trigger
	validateTrigger(triggerJSON, result)

	// Validate conditions (nullable)
	if conditionsJSON != nil && len(conditionsJSON) > 0 && string(conditionsJSON) != "null" {
		validateConditions(conditionsJSON, result)
	}

	// Validate actions
	validateActions(actionsJSON, result)

	return result
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

	if !ValidTriggerTypes[trigger.Type] {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "trigger.type",
			Message: fmt.Sprintf("unknown trigger type: '%s'. Valid types: %s", trigger.Type, validTriggerTypesList()),
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
		} else if _, ok := trigger.Params["to_stage"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   "trigger.params.to_stage",
				Message: "deal_stage_changed requires 'to_stage' parameter",
			})
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
		if _, ok := action.Params["duration_sec"]; !ok {
			result.Valid = false
			result.Errors = append(result.Errors, ValidationError{
				Field:   path + ".params.duration_sec",
				Message: "delay requires 'duration_sec' parameter",
			})
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
	// Basic email validation: has @ and at least one dot after @
	at := strings.LastIndex(s, "@")
	if at < 1 {
		return false
	}
	domain := s[at+1:]
	return strings.Contains(domain, ".") && !strings.HasSuffix(domain, ".") && !strings.HasPrefix(domain, ".")
}
