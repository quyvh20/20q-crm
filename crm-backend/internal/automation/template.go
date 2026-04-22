package automation

import (
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
)

// templatePattern matches {{path.to.field}} with optional whitespace inside braces.
var templatePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.]+)\s*\}\}`)

// escapedPattern matches \{\{...\}\} which should render as literal {{...}}.
var escapedPattern = regexp.MustCompile(`\\\{\\\{(.+?)\\\}\\\}`)

// InterpolateTemplate resolves template variables in a string against an EvalContext.
// - {{path.to.field}} is replaced with the resolved value.
// - Missing paths render as empty string (logged as warning, never error).
// - \{\{literal\}\} renders as {{literal}}.
func InterpolateTemplate(template string, ctx EvalContext) string {
	// First, replace escaped patterns with a placeholder
	const escapePlaceholder = "\x00ESCAPED_BRACE\x00"
	escaped := make(map[string]string)
	result := escapedPattern.ReplaceAllStringFunc(template, func(match string) string {
		// Extract inner content
		inner := escapedPattern.FindStringSubmatch(match)
		if len(inner) < 2 {
			return match
		}
		key := fmt.Sprintf("%s%d%s", escapePlaceholder, len(escaped), escapePlaceholder)
		escaped[key] = "{{" + inner[1] + "}}"
		return key
	})

	// Now resolve template variables
	result = templatePattern.ReplaceAllStringFunc(result, func(match string) string {
		groups := templatePattern.FindStringSubmatch(match)
		if len(groups) < 2 {
			return match
		}
		path := groups[1]
		val := resolvePath(path, ctx)
		if val == nil {
			slog.Warn("template: unresolved path", "path", path)
			return ""
		}
		return formatValue(val)
	})

	// Restore escaped braces
	for placeholder, literal := range escaped {
		result = strings.Replace(result, placeholder, literal, 1)
	}

	return result
}

// resolvePath resolves a dotted path against the EvalContext.
// Root keys: contact, deal, trigger, org, user, actions
func resolvePath(path string, ctx EvalContext) any {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 0 {
		return nil
	}

	root := parts[0]
	var data map[string]any

	switch root {
	case "contact":
		data = ctx.Contact
	case "deal":
		data = ctx.Deal
	case "trigger":
		data = ctx.Trigger
	case "org":
		data = ctx.Org
	case "user":
		data = ctx.User
	case "actions":
		data = ctx.Actions
	default:
		return nil
	}

	if data == nil {
		return nil
	}

	if len(parts) == 1 {
		return data
	}

	return resolveNestedPath(parts[1], data)
}

// resolveNestedPath walks a dotted path through nested maps and slices.
func resolveNestedPath(path string, data any) any {
	parts := strings.Split(path, ".")
	current := data

	for _, part := range parts {
		if current == nil {
			return nil
		}

		switch v := current.(type) {
		case map[string]any:
			val, ok := v[part]
			if !ok {
				return nil
			}
			current = val
		case map[string]string:
			val, ok := v[part]
			if !ok {
				return nil
			}
			current = val
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(v) {
				return nil
			}
			current = v[idx]
		default:
			return nil
		}
	}

	return current
}

// formatValue converts a value to its string representation.
func formatValue(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	case float64:
		// Use int64 cents for money — no floats for money (spec §15).
		// For display, format nicely.
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(v), 'f', -1, 32)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case bool:
		return strconv.FormatBool(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}
