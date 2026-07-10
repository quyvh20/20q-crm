package ai

import (
	"errors"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// Repro harness: feed the EXACT content prod returned in a 422 through the inline
// parser. Set PROD_DRAFT_JSON to the saved response file; skipped otherwise.
func TestParseInlineToolCalls_ProdRepro(t *testing.T) {
	path := os.Getenv("PROD_DRAFT_JSON")
	if path == "" {
		t.Skip("PROD_DRAFT_JSON not set")
	}
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var body struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &body))
	require.NotEmpty(t, body.Error)
	t.Logf("content head: %.80q", body.Error)
	t.Logf("content tail: %.60q", body.Error[len(body.Error)-60:])

	tools := []Tool{
		{Name: "get_workflow_schema", Params: map[string]any{"required": []string{}}},
		{Name: "draft_workflow", Params: map[string]any{"required": []string{"name", "trigger", "steps"}}},
	}
	// Step-by-step diagnosis: regex match? JSON unmarshal? tool inference?
	matches := inlineToolCallRe.FindAllStringSubmatch(body.Error, -1)
	t.Logf("regex matches: %d", len(matches))
	for i, m := range matches {
		var obj map[string]json.RawMessage
		err := json.Unmarshal([]byte(m[1]), &obj)
		t.Logf("match %d: len=%d unmarshalErr=%v", i, len(m[1]), err)
		if err != nil {
			var syn *json.SyntaxError
			if errors.As(err, &syn) {
				lo := max(0, int(syn.Offset)-70)
				hi := min(len(m[1]), int(syn.Offset)+30)
				t.Logf("match %d syntax error at offset %d: …%q…", i, syn.Offset, m[1][lo:hi])
			}
			t.Logf("match %d head: %.80q", i, m[1])
			t.Logf("match %d tail: %.60q", i, m[1][len(m[1])-60:])
		} else {
			keys := make([]string, 0, len(obj))
			for k := range obj {
				keys = append(keys, k)
			}
			t.Logf("match %d keys: %v -> inferred %q", i, keys, inferToolByParams(obj, tools))
		}
	}

	calls, remaining := parseInlineToolCalls(body.Error, tools)
	t.Logf("calls: %d, remaining: %.60q", len(calls), remaining)
	require.Len(t, calls, 1, "the exact prod content must parse")
	require.Equal(t, "draft_workflow", calls[0].Name)
}
