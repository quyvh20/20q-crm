package marketing

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// TestResendEventData_TagCampaignID proves the webhook extractor reads campaign_id from
// the tags Resend echoes — in both the object form (how webhooks serialize them) and the
// array form (how they were sent) — and is nil-safe on absent/malformed tags.
func TestResendEventData_TagCampaignID(t *testing.T) {
	cid := uuid.New()

	object := resendEventData{Tags: json.RawMessage(`{"campaign_id":"` + cid.String() + `","contact_id":"` + uuid.NewString() + `"}`)}
	if got := object.tagCampaignID(); got == nil || *got != cid {
		t.Fatalf("object-form campaign_id = %v, want %s", got, cid)
	}

	array := resendEventData{Tags: json.RawMessage(`[{"name":"campaign_id","value":"` + cid.String() + `"}]`)}
	if got := array.tagCampaignID(); got == nil || *got != cid {
		t.Fatalf("array-form campaign_id = %v, want %s", got, cid)
	}

	if got := (resendEventData{}).tagCampaignID(); got != nil {
		t.Fatalf("no tags must yield nil, got %v", got)
	}

	malformed := resendEventData{Tags: json.RawMessage(`{"campaign_id":"not-a-uuid"}`)}
	if got := malformed.tagCampaignID(); got != nil {
		t.Fatalf("malformed campaign_id must yield nil, got %v", got)
	}
}
