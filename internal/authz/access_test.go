package authz

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAccessPolicyConstructors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		access AccessPolicy
	}{
		{name: "public", access: Public()},
		{name: "authenticated", access: Authenticated()},
		{name: "hmac", access: HMAC()},
		{name: "system", access: System(PermissionSystemUsersRead)},
		{name: "project", access: Project(PermissionProjectRead, "projectID")},
		{
			name: "project resource policies",
			access: Project(
				PermissionProjectKeysManage,
				"projectID",
				WithResource("key", "keyID"),
				WithPolicies(PolicyResourceProjectContainment, PolicyOwnResource),
			),
		},
		{
			name: "project requiring membership",
			access: Project(
				PermissionProjectKeysCreate,
				"projectID",
				RequireProjectMembership(),
			),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.access.Validate(); err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}

	if got := System(PermissionSystemUsersRead).Superadmin; got != SuperadminRequired {
		t.Errorf("System() superadmin rule = %q, want %q", got, SuperadminRequired)
	}
	if got := Project(PermissionProjectRead, "projectID").Superadmin; got != SuperadminBypass {
		t.Errorf("Project() superadmin rule = %q, want %q", got, SuperadminBypass)
	}
	if got := Project(PermissionProjectRead, "projectID", RequireProjectMembership()).Superadmin; got != SuperadminNone {
		t.Errorf("RequireProjectMembership() rule = %q, want %q", got, SuperadminNone)
	}
}

func TestAccessPolicyValidateRejectsInvalidDescriptors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		access    AccessPolicy
		wantError string
	}{
		{name: "zero value", access: AccessPolicy{}, wantError: "invalid access mode"},
		{
			name:      "invalid scope",
			access:    AccessPolicy{Mode: AccessModePublic, Scope: Scope("tenant"), Superadmin: SuperadminNone},
			wantError: "invalid scope",
		},
		{
			name:      "public with permission",
			access:    AccessPolicy{Mode: AccessModePublic, Scope: ScopeNone, Permission: PermissionProjectRead, Superadmin: SuperadminNone},
			wantError: "cannot declare RBAC metadata",
		},
		{
			name:      "authenticated superadmin bypass",
			access:    AccessPolicy{Mode: AccessModeAuthenticated, Scope: ScopeNone, Superadmin: SuperadminBypass},
			wantError: "cannot grant special superadmin behavior",
		},
		{name: "unknown permission", access: Project(Permission("project.unknown"), "projectID"), wantError: "unknown permission"},
		{name: "system permission on project", access: Project(PermissionSystemUsersRead, "projectID"), wantError: "has scope"},
		{name: "project permission on system", access: System(PermissionProjectRead), wantError: "has scope"},
		{
			name:      "system without explicit superadmin",
			access:    AccessPolicy{Mode: AccessModeRBAC, Scope: ScopeSystem, Permission: PermissionSystemUsersRead, Superadmin: SuperadminNone},
			wantError: "explicitly require superadmin",
		},
		{
			name: "system with project parameter",
			access: System(PermissionSystemUsersRead, func(a *AccessPolicy) {
				a.ProjectIDPathParam = "projectID"
			}),
			wantError: "cannot declare a project",
		},
		{name: "project without project parameter", access: Project(PermissionProjectRead, "  "), wantError: "requires a project path"},
		{
			name:      "project requiring superadmin",
			access:    AccessPolicy{Mode: AccessModeRBAC, Scope: ScopeProject, Permission: PermissionProjectRead, ProjectIDPathParam: "projectID", Superadmin: SuperadminRequired},
			wantError: "invalid superadmin rule",
		},
		{name: "empty resource type", access: Project(PermissionProjectKeysRead, "projectID", WithResource("", "keyID")), wantError: "resource type is required"},
		{name: "empty resource ID parameter", access: Project(PermissionProjectKeysRead, "projectID", WithResource("key", "")), wantError: "resource ID path parameter is required"},
		{name: "empty policy", access: Project(PermissionProjectRead, "projectID", WithPolicies(PolicyID(""))), wantError: "policy ID is required"},
		{name: "duplicate policy", access: Project(PermissionProjectRead, "projectID", WithPolicies(PolicyOwnResource, PolicyOwnResource)), wantError: "duplicate policy"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.access.Validate()
			if err == nil {
				t.Fatalf("Validate() succeeded, want error containing %q", test.wantError)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %q, want substring %q", err, test.wantError)
			}
		})
	}
}

func TestAccessPolicyJSONMetadata(t *testing.T) {
	t.Parallel()

	access := Project(
		PermissionProjectKeysManage,
		"projectID",
		WithResource("key", "keyID"),
		WithPolicies(PolicyResourceProjectContainment, PolicyOwnResource),
	)
	encoded, err := json.Marshal(access)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	got := string(encoded)
	for _, field := range []string{
		"\"mode\":\"rbac\"",
		"\"scope\":\"project\"",
		"\"permission\":\"project.keys.manage\"",
		"\"projectIdPathParam\":\"projectID\"",
		"\"resource\":{\"type\":\"key\",\"idPathParam\":\"keyID\"}",
		"\"conditions\":[\"resource-project-containment\",\"own-resource\"]",
		"\"superadmin\":\"bypass\"",
	} {
		if !strings.Contains(got, field) {
			t.Errorf("JSON %s does not contain %s", got, field)
		}
	}
}
