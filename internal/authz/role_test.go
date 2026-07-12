package authz

import (
	"slices"
	"testing"
)

func TestProjectRoleValidity(t *testing.T) {
	t.Parallel()

	roles := AllProjectRoles()
	if !slices.Equal(roles, []ProjectRole{RoleOwner, RoleMaintainer, RoleDeveloper}) {
		t.Fatalf("AllProjectRoles() = %v", roles)
	}
	roles[0] = ProjectRole("mutated")
	if AllProjectRoles()[0] != RoleOwner {
		t.Fatal("caller mutation affected built-in project roles")
	}

	for _, role := range AllProjectRoles() {
		if !role.Valid() {
			t.Errorf("built-in role %q is invalid", role)
		}
	}
	for _, role := range []ProjectRole{"", "admin", "OWNER", "unknown"} {
		if role.Valid() {
			t.Errorf("unknown role %q is valid", role)
		}
	}
}

func TestRolePermissionHierarchy(t *testing.T) {
	t.Parallel()

	developer, ok := PermissionsForRole(RoleDeveloper)
	if !ok {
		t.Fatal("developer role was not found")
	}
	maintainer, ok := PermissionsForRole(RoleMaintainer)
	if !ok {
		t.Fatal("maintainer role was not found")
	}
	owner, ok := PermissionsForRole(RoleOwner)
	if !ok {
		t.Fatal("owner role was not found")
	}

	for _, permission := range developer.Permissions() {
		if !maintainer.Has(permission) {
			t.Errorf("maintainer does not inherit developer permission %q", permission)
		}
	}
	for _, permission := range maintainer.Permissions() {
		if !owner.Has(permission) {
			t.Errorf("owner does not inherit maintainer permission %q", permission)
		}
	}

	if developer.Has(PermissionProjectSettingsWrite) {
		t.Error("developer unexpectedly has project settings write")
	}
	if developer.Has(PermissionProjectMembersManage) {
		t.Error("developer unexpectedly has project member management")
	}
	if developer.Has(PermissionProjectBillingManage) {
		t.Error("developer unexpectedly has project billing management")
	}
	if maintainer.Has(PermissionProjectArchive) {
		t.Error("maintainer unexpectedly has project archive")
	}
	if maintainer.Has(PermissionProjectOwnershipTransfer) {
		t.Error("maintainer unexpectedly has ownership transfer")
	}
	if !owner.Has(PermissionProjectArchive) || !owner.Has(PermissionProjectOwnershipTransfer) {
		t.Error("owner lacks owner-only permissions")
	}
	if owner.Len() != len(permissionsInScope(ScopeProject)) {
		t.Errorf("owner grants %d permissions; project catalog contains %d", owner.Len(), len(permissionsInScope(ScopeProject)))
	}
}

func TestUnknownRoleGrantsNothing(t *testing.T) {
	t.Parallel()

	permissions, ok := PermissionsForRole(ProjectRole("admin"))
	if ok {
		t.Fatal("unknown role reported as found")
	}
	if permissions.Len() != 0 {
		t.Fatalf("unknown role grants %v", permissions.Permissions())
	}

	principal := Principal{UserID: "user-1"}
	capabilities := ProjectCapabilities(principal, ProjectRole("admin"))
	if capabilities.Len() != 0 {
		t.Fatalf("unknown role capabilities = %v, want empty", capabilities.Permissions())
	}
	if HasProjectPermission(principal, ProjectRole("admin"), PermissionProjectArchive) {
		t.Fatal("unknown role passed HasProjectPermission")
	}
}

func TestCapabilitySetIsIsolatedAndSorted(t *testing.T) {
	t.Parallel()

	set := NewCapabilitySet(
		PermissionProjectTracesRead,
		Permission("project.unknown"),
		PermissionProjectRead,
		PermissionProjectRead,
	)
	if set.Len() != 2 {
		t.Fatalf("Len() = %d, want 2", set.Len())
	}
	if got := set.Permissions(); !slices.Equal(got, []Permission{PermissionProjectRead, PermissionProjectTracesRead}) {
		t.Fatalf("Permissions() = %v", got)
	}
	if got := set.Strings(); !slices.Equal(got, []string{"project.read", "project.traces.read"}) {
		t.Fatalf("Strings() = %v", got)
	}

	permissions, _ := PermissionsForRole(RoleOwner)
	returned := permissions.Permissions()
	returned[0] = Permission("mutated")
	again, _ := PermissionsForRole(RoleOwner)
	if again.Has(Permission("mutated")) || !again.Has(PermissionProjectRead) {
		t.Fatalf("caller mutation affected role mapping: %v", again.Permissions())
	}
}

func TestCapabilityCalculationRequiresExplicitIdentityAndSuperadmin(t *testing.T) {
	t.Parallel()

	regular := Principal{UserID: "user-1"}
	if got := SystemCapabilities(regular); got.Len() != 0 {
		t.Fatalf("regular system capabilities = %v, want empty", got.Permissions())
	}
	if HasSystemPermission(regular, PermissionSystemUsersRead) {
		t.Fatal("regular user has system permission")
	}

	superadmin := Principal{UserID: "admin-1", Superadmin: true}
	system := SystemCapabilities(superadmin)
	if system.Len() != len(permissionsInScope(ScopeSystem)) {
		t.Fatalf("superadmin system capability count = %d", system.Len())
	}
	if !HasSystemPermission(superadmin, PermissionSystemUsersRead) {
		t.Fatal("explicit superadmin lacks system permission")
	}
	project := ProjectCapabilities(superadmin, "")
	if project.Len() != len(permissionsInScope(ScopeProject)) {
		t.Fatalf("superadmin project capability count = %d", project.Len())
	}
	if !project.Has(PermissionProjectArchive) {
		t.Fatal("explicit superadmin lacks project permission")
	}

	anonymousSuperadmin := Principal{Superadmin: true}
	if got := SystemCapabilities(anonymousSuperadmin); got.Len() != 0 {
		t.Fatalf("anonymous superadmin system capabilities = %v", got.Permissions())
	}
	if got := ProjectCapabilities(anonymousSuperadmin, RoleOwner); got.Len() != 0 {
		t.Fatalf("anonymous superadmin project capabilities = %v", got.Permissions())
	}
}

func TestPermissionChecksEnforceScope(t *testing.T) {
	t.Parallel()

	owner := Principal{UserID: "owner-1"}
	if HasProjectPermission(owner, RoleOwner, PermissionSystemUsersRead) {
		t.Fatal("system permission passed project permission check")
	}

	superadmin := Principal{UserID: "admin-1", Superadmin: true}
	if HasSystemPermission(superadmin, PermissionProjectRead) {
		t.Fatal("project permission passed system permission check")
	}
}

func TestNewProjectPermissionsRoleGrants(t *testing.T) {
	t.Parallel()

	developer, _ := PermissionsForRole(RoleDeveloper)
	maintainer, _ := PermissionsForRole(RoleMaintainer)
	owner, _ := PermissionsForRole(RoleOwner)

	allRoles := []Permission{
		PermissionProjectMembersUsageRead,
		PermissionProjectExtraUsageRead,
	}
	for _, permission := range allRoles {
		if !developer.Has(permission) {
			t.Errorf("developer missing %q", permission)
		}
		if !maintainer.Has(permission) {
			t.Errorf("maintainer missing %q", permission)
		}
		if !owner.Has(permission) {
			t.Errorf("owner missing %q", permission)
		}
	}

	maintainerAndUp := []Permission{
		PermissionProjectExtraUsageWrite,
		PermissionProjectExtraUsageTopup,
	}
	for _, permission := range maintainerAndUp {
		if developer.Has(permission) {
			t.Errorf("developer unexpectedly has %q", permission)
		}
		if !maintainer.Has(permission) {
			t.Errorf("maintainer missing %q", permission)
		}
		if !owner.Has(permission) {
			t.Errorf("owner missing %q", permission)
		}
	}
}
