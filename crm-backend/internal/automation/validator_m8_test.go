package automation

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func m8HasError(res *ValidationResult, substr string) bool {
	for _, e := range res.Errors {
		if strings.Contains(e.Field, substr) || strings.Contains(e.Message, substr) {
			return true
		}
	}
	return false
}

// TestValidate_MarketingSendRequiresContentID pins the save-time guard: a
// channel=marketing send step must reference content_id (else it fails permanently at
// send); a transactional send is unaffected.
func TestValidate_MarketingSendRequiresContentID(t *testing.T) {
	trigger := []byte(`{"type":"contact_created"}`)

	res := ValidateWorkflowPayload(trigger, nil, stepsFromActionsJSON(t, []byte(`[{"type":"send_email","id":"a1","params":{"to":"{{contact.email}}","channel":"marketing"}}]`)))
	assert.False(t, res.Valid, "marketing send without content_id must be invalid")
	assert.True(t, m8HasError(res, "content_id"), "error should name content_id")

	res = ValidateWorkflowPayload(trigger, nil, stepsFromActionsJSON(t, []byte(`[{"type":"send_email","id":"a1","params":{"to":"{{contact.email}}","channel":"marketing","content_id":"`+uuid.NewString()+`"}}]`)))
	assert.True(t, res.Valid, "marketing send with a valid content_id must be valid: %+v", res.Errors)

	res = ValidateWorkflowPayload(trigger, nil, stepsFromActionsJSON(t, []byte(`[{"type":"send_email","id":"a1","params":{"to":"{{contact.email}}","channel":"marketing","content_id":"not-a-uuid"}}]`)))
	assert.False(t, res.Valid, "malformed content_id must be invalid")

	res = ValidateWorkflowPayload(trigger, nil, stepsFromActionsJSON(t, []byte(`[{"type":"send_email","id":"a1","params":{"to":"x@test.com"}}]`)))
	assert.True(t, res.Valid, "transactional send needs no content_id: %+v", res.Errors)
}
