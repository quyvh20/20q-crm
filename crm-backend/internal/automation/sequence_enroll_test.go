package automation

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"gorm.io/datatypes"
)

// TestContactEnrollContext_IsDepthZero proves a sequence-enrolled contact run is DEPTH 0
// (its context omits _enroll_depth), so any enroll steps inside the sequence keep the
// full depth-2 headroom.
func TestContactEnrollContext_IsDepthZero(t *testing.T) {
	cid := uuid.New()
	enrollID := uuid.New()
	fields := map[string]any{"id": cid.String(), "email": "jane@acme.com", "first_name": "Jane"}
	tc := contactEnrollContext(fields, cid, "manual", enrollID)

	raw, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	run := &WorkflowRun{ID: uuid.New(), TriggerContext: datatypes.JSON(raw)}
	if d := enrollDepthOf(run); d != 0 {
		t.Fatalf("enrolled contact run must be depth 0, got %d", d)
	}
	// The run must carry its enrollment tag so per-enrollment progress can be counted.
	if tc[marketingEnrollmentKey] != enrollID.String() {
		t.Fatalf("enrollment tag = %v, want %s", tc[marketingEnrollmentKey], enrollID)
	}
}

// TestContactEnrollContext_HydratesContact proves the enrolled run's eval context exposes
// contact.* (so {{contact.email}} in a send step resolves).
func TestContactEnrollContext_HydratesContact(t *testing.T) {
	cid := uuid.New()
	fields := map[string]any{"id": cid.String(), "email": "jane@acme.com", "first_name": "Jane"}
	tc := contactEnrollContext(fields, cid, "manual", uuid.Nil)
	raw, _ := json.Marshal(tc)

	e := &Engine{} // buildEvalContext only reads the payload for a contact-carrying context
	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New(), TriggerContext: datatypes.JSON(raw)}
	ec := e.buildEvalContext(run)
	if ec.Contact == nil {
		t.Fatal("contact not hydrated into eval context")
	}
	if ec.Contact["email"] != "jane@acme.com" {
		t.Errorf("contact.email = %v, want jane@acme.com", ec.Contact["email"])
	}
}
