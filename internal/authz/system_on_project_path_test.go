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

func TestSystemOnProjectPathJSONDoesNotLeakOptInFlag(t *testing.T) {
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
	} {
		if !strings.Contains(got, needle) {
			t.Errorf("JSON %s does not contain %s", got, needle)
		}
	}
	for _, forbidden := range []string{"systemOnProjectPath", "sysProjectPath", "optIn"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("JSON %s leaked internal flag %s", got, forbidden)
		}
	}
}
