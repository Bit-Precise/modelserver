package authz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSystemOnProjectPathBuildsValidPolicy(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID")
	if err := access.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if access.Mode != AccessModeRBAC {
		t.Errorf("Mode = %q, want %q", access.Mode, AccessModeRBAC)
	}
	if access.Scope != ScopeSystem {
		t.Errorf("Scope = %q, want %q", access.Scope, ScopeSystem)
	}
	if access.Superadmin != SuperadminRequired {
		t.Errorf("Superadmin = %q, want %q", access.Superadmin, SuperadminRequired)
	}
	if access.Permission != PermissionSystemSubscriptionOverride {
		t.Errorf("Permission = %q, want %q", access.Permission, PermissionSystemSubscriptionOverride)
	}
	if access.ProjectIDPathParam != "projectID" {
		t.Errorf("ProjectIDPathParam = %q, want projectID", access.ProjectIDPathParam)
	}
}

func TestSystemOnProjectPathRejectsBlankParam(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "  ")
	if err := access.Validate(); err == nil || !strings.Contains(err.Error(), "project path parameter") {
		t.Fatalf("Validate() error = %v, want project-path-parameter error", err)
	}
}

func TestSystemOnProjectPathRejectsProjectPermission(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionProjectRead, "projectID")
	if err := access.Validate(); err == nil || !strings.Contains(err.Error(), "has scope") {
		t.Fatalf("Validate() error = %v, want scope error", err)
	}
}

// The already-existing System() helper must still reject a manually set
// ProjectIDPathParam — SystemOnProjectPath is the only opt-in.
func TestSystemWithoutOptInRejectsProjectParam(t *testing.T) {
	t.Parallel()

	access := System(PermissionSystemSubscriptionOverride, func(a *AccessPolicy) {
		a.ProjectIDPathParam = "projectID"
	})
	if err := access.Validate(); err == nil || !strings.Contains(err.Error(), "cannot declare a project") {
		t.Fatalf("Validate() error = %v, want cannot-declare error", err)
	}
}

func TestSystemOnProjectPathJSONRoundTripPreservesOptIn(t *testing.T) {
	t.Parallel()

	original := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID")
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded AccessPolicy
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-tripped policy failed Validate(): %v", err)
	}
	if !decoded.SystemOnProjectPath {
		t.Errorf("decoded.SystemOnProjectPath = false, want true")
	}
}

// TestSystemOnProjectPathJSONRoundTripNegative asserts that a plain System
// policy (no ProjectIDPathParam) round-trips cleanly and does NOT accidentally
// receive the SystemOnProjectPath flag.
func TestSystemOnProjectPathJSONRoundTripNegative(t *testing.T) {
	t.Parallel()

	original := System(PermissionSystemSubscriptionOverride)
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var decoded AccessPolicy
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("round-tripped System() policy failed Validate(): %v", err)
	}
	if decoded.SystemOnProjectPath {
		t.Errorf("decoded.SystemOnProjectPath = true, want false")
	}
}

func TestSystemOnProjectPathJSONExposesOptInFlag(t *testing.T) {
	t.Parallel()

	access := SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID")
	encoded, err := json.Marshal(access)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	got := string(encoded)
	for _, needle := range []string{
		"\"mode\":\"rbac\"",
		"\"scope\":\"system\"",
		"\"permission\":\"system.subscription.override\"",
		"\"projectIdPathParam\":\"projectID\"",
		"\"superadmin\":\"required\"",
		"\"systemOnProjectPath\":true",
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("JSON %s does not contain %s", got, needle)
		}
	}
	// Sanity: no misspellings of the key.
	for _, forbidden := range []string{"system_on_project_path", "sysProjectPath", "optIn"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("JSON %s contains unexpected key %s", got, forbidden)
		}
	}
}

// TestSystemPolicyForgedViaJSONWithProjectParamStillRejected proves that
// hand-crafted JSON cannot forge a valid SystemOnProjectPath policy by omitting
// the systemOnProjectPath field. Without the explicit flag, Validate() must
// reject the project path parameter on a system-scoped policy.
func TestSystemPolicyForgedViaJSONWithProjectParamStillRejected(t *testing.T) {
	t.Parallel()

	raw := `{"mode":"rbac","scope":"system","permission":"system.subscription.override","projectIdPathParam":"projectID","superadmin":"required"}`
	var decoded AccessPolicy
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	err := decoded.Validate()
	if err == nil {
		t.Fatal("Validate() succeeded, want error containing \"cannot declare a project path parameter\"")
	}
	if !strings.Contains(err.Error(), "cannot declare a project path parameter") {
		t.Errorf("Validate() error = %q, want substring \"cannot declare a project path parameter\"", err)
	}
}
