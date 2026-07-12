package domain

import "testing"

// TestCapabilityCatalogCoversAllCapabilities is the guard that a new capability
// can't ship without human-facing copy (P6): CapabilityCatalog must stay 1:1 with
// AllCapabilities, every entry must carry a label/description in a known group,
// and the ⚠ sensitive set must match the plan (§3.2) exactly.
func TestCapabilityCatalogCoversAllCapabilities(t *testing.T) {
	if len(CapabilityCatalog) != len(AllCapabilities) {
		t.Fatalf("CapabilityCatalog has %d entries, AllCapabilities has %d — they must be 1:1", len(CapabilityCatalog), len(AllCapabilities))
	}

	byCode := make(map[string]CapabilityInfo, len(CapabilityCatalog))
	for _, ci := range CapabilityCatalog {
		if _, dup := byCode[ci.Code]; dup {
			t.Fatalf("duplicate catalog entry for %q", ci.Code)
		}
		byCode[ci.Code] = ci
	}

	validGroup := make(map[string]bool, len(CapabilityGroups))
	for _, g := range CapabilityGroups {
		validGroup[g] = true
	}

	for _, code := range AllCapabilities {
		ci, ok := byCode[code]
		if !ok {
			t.Errorf("capability %q missing from CapabilityCatalog", code)
			continue
		}
		if ci.Label == "" || ci.Description == "" {
			t.Errorf("capability %q missing a label/description", code)
		}
		if !validGroup[ci.Group] {
			t.Errorf("capability %q has group %q not in CapabilityGroups", code, ci.Group)
		}
	}
	for _, ci := range CapabilityCatalog {
		if !IsCapability(ci.Code) {
			t.Errorf("catalog code %q is not a real capability", ci.Code)
		}
	}

	wantSensitive := map[string]bool{
		CapRolesManage:     true,
		CapMembersManage:   true,
		CapWorkflowsManage: true,
		CapWorkflowsRunAny: true,
		CapOrgSettings:     true,
		CapDataExport:      true,
	}
	for _, ci := range CapabilityCatalog {
		if ci.Sensitive != wantSensitive[ci.Code] {
			t.Errorf("capability %q sensitive=%v, want %v", ci.Code, ci.Sensitive, wantSensitive[ci.Code])
		}
	}
}
