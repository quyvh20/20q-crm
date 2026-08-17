package automation

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Automation-side tests for the email_opened trigger (arc G).

func validateTriggerJSON(t *testing.T, trigger map[string]any) *ValidationResult {
	t.Helper()
	raw, err := json.Marshal(trigger)
	require.NoError(t, err)
	result := &ValidationResult{Valid: true}
	validateTrigger(raw, result)
	return result
}

func TestEmailOpened_IsValidTriggerType(t *testing.T) {
	assert.True(t, IsValidTriggerType(TriggerEmailOpened))
	// "_opened" is NOT a dynamic wildcard suffix — only the const makes it valid.
	assert.False(t, IsValidTriggerType("campaign_opened"))
}

func TestEmailOpened_ValidatorAcceptsBareAndWildcard(t *testing.T) {
	for _, params := range []any{nil, map[string]any{}, map[string]any{"campaign_id": ""}, map[string]any{"campaign_id": "*"}, map[string]any{"campaign_id": uuid.NewString()}} {
		trig := map[string]any{"type": "email_opened"}
		if params != nil {
			trig["params"] = params
		}
		res := validateTriggerJSON(t, trig)
		assert.True(t, res.Valid, "params %v should be valid: %v", params, res.Errors)
	}
}

func TestEmailOpened_ValidatorRejectsBadCampaignID(t *testing.T) {
	for _, bad := range []any{"not-a-uuid", 42} {
		res := validateTriggerJSON(t, map[string]any{"type": "email_opened", "params": map[string]any{"campaign_id": bad}})
		assert.False(t, res.Valid, "campaign_id %v must be rejected", bad)
	}
}

func TestEmailOpened_EntityKindIsContact(t *testing.T) {
	assert.Equal(t, "contact", entityKindForTrigger(TriggerEmailOpened))
}

func TestEmailOpened_IdempKeyOncePerMessage(t *testing.T) {
	wf := uuid.New()
	k1 := emailOpenedIdempKey(wf, TriggerEmailOpened, "contact-1", "re_msg_1")
	// Same (workflow, contact, message) → same key: repeated pixel loads absorbed.
	assert.Equal(t, k1, emailOpenedIdempKey(wf, TriggerEmailOpened, "contact-1", "re_msg_1"))
	// A different message, contact, or workflow each yields a distinct key.
	assert.NotEqual(t, k1, emailOpenedIdempKey(wf, TriggerEmailOpened, "contact-1", "re_msg_2"))
	assert.NotEqual(t, k1, emailOpenedIdempKey(wf, TriggerEmailOpened, "contact-2", "re_msg_1"))
	assert.NotEqual(t, k1, emailOpenedIdempKey(uuid.New(), TriggerEmailOpened, "contact-1", "re_msg_1"))
}

func TestEmailOpened_CampaignPinMatchesNonCanonicalForms(t *testing.T) {
	canonical := "f47ac10b-58cc-4372-a567-0e02b2c3d479"
	// The validator accepts any parseable UUID form; dispatch must match them all.
	for _, req := range []string{
		canonical,
		"F47AC10B-58CC-4372-A567-0E02B2C3D479",
		"{f47ac10b-58cc-4372-a567-0e02b2c3d479}",
		"urn:uuid:f47ac10b-58cc-4372-a567-0e02b2c3d479",
		"f47ac10b58cc4372a5670e02b2c3d479",
	} {
		assert.True(t, campaignPinMatches(req, canonical), "form %q must match", req)
	}
	assert.False(t, campaignPinMatches(uuid.NewString(), canonical))
	assert.False(t, campaignPinMatches("not-a-uuid", canonical))
}
