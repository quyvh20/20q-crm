package marketing

import (
	"testing"

	"crm-backend/internal/automation"

	"gorm.io/datatypes"
)

// TestWorkflowHasMarketingSend gates the "is this a drip sequence?" check used to reject
// enrolling a segment into a workflow that sends no marketing mail.
func TestWorkflowHasMarketingSend(t *testing.T) {
	cases := []struct {
		name string
		wf   *automation.Workflow
		want bool
	}{
		{
			name: "top-level marketing send (via Steps)",
			wf:   &automation.Workflow{Steps: datatypes.JSON(`[{"type":"action","id":"s1","action":{"type":"send_email","id":"s1","params":{"channel":"marketing","content_id":"x"}}}]`)},
			want: true,
		},
		{
			name: "marketing send inside an If/Else branch",
			wf:   &automation.Workflow{Steps: datatypes.JSON(`[{"type":"condition","id":"c1","yes_steps":[{"type":"action","id":"s2","action":{"type":"send_email","id":"s2","params":{"channel":"marketing","content_id":"x"}}}],"no_steps":[]}]`)},
			want: true,
		},
		{
			name: "transactional-only workflow",
			wf:   &automation.Workflow{Steps: datatypes.JSON(`[{"type":"action","id":"s1","action":{"type":"send_email","id":"s1","params":{"to":"x@y.com"}}}]`)},
			want: false,
		},
		{
			name: "no send at all",
			wf:   &automation.Workflow{Steps: datatypes.JSON(`[{"type":"action","id":"s1","action":{"type":"create_task","id":"s1","params":{}}}]`)},
			want: false,
		},
		// The deprecated flat-Actions FALLBACK is gone (R5 deploy 1) — flattenWorkflowActions
		// reads the steps tree and nothing else, exactly as its contract always said it
		// would once the column went. A workflow with no steps executes nothing, so it is
		// not a drip sequence.
		{
			name: "no steps at all is not a drip sequence",
			wf:   &automation.Workflow{},
			want: false,
		},
		{
			name: "unparseable steps is not a drip sequence",
			wf:   &automation.Workflow{Steps: datatypes.JSON(`{not a steps array`)},
			want: false,
		},
	}
	for _, c := range cases {
		if got := workflowHasMarketingSend(c.wf); got != c.want {
			t.Errorf("%s: workflowHasMarketingSend = %v, want %v", c.name, got, c.want)
		}
	}
}
