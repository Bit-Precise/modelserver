package authz

import (
	"slices"
	"testing"
)

func TestPermissionCatalog(t *testing.T) {
	t.Parallel()

	permissions := AllPermissions()
	if len(permissions) == 0 {
		t.Fatal("AllPermissions() returned an empty catalog")
	}
	if !slices.IsSorted(permissions) {
		t.Fatalf("AllPermissions() is not sorted: %v", permissions)
	}

	seen := make(map[Permission]struct{}, len(permissions))
	scopeCounts := map[Scope]int{}
	for _, permission := range permissions {
		if _, exists := seen[permission]; exists {
			t.Fatalf("duplicate permission %q", permission)
		}
		seen[permission] = struct{}{}

		if !permission.Valid() {
			t.Errorf("catalog permission %q is not valid", permission)
		}
		scope, ok := permission.Scope()
		if !ok {
			t.Errorf("catalog permission %q has no scope", permission)
		}
		if scope != ScopeSystem && scope != ScopeProject {
			t.Errorf("catalog permission %q has invalid grant scope %q", permission, scope)
		}
		scopeCounts[scope]++
	}

	if scopeCounts[ScopeSystem] == 0 || scopeCounts[ScopeProject] == 0 {
		t.Fatalf("expected both system and project permissions, got counts %v", scopeCounts)
	}
}

func TestAllPermissionsReturnsCopy(t *testing.T) {
	t.Parallel()

	first := AllPermissions()
	original := first[0]
	first[0] = Permission("mutated")

	second := AllPermissions()
	if second[0] != original {
		t.Fatalf("caller mutation escaped into permission catalog: got %q, want %q", second[0], original)
	}
}

func TestUnknownPermissionDefaultsToDenied(t *testing.T) {
	t.Parallel()

	unknown := Permission("project.unknown.manage")
	if unknown.Valid() {
		t.Fatal("unknown permission reported as valid")
	}
	if scope, ok := unknown.Scope(); ok || scope != ScopeNone {
		t.Fatalf("unknown permission scope = (%q, %v), want (%q, false)", scope, ok, ScopeNone)
	}
}

func TestNewPermissionsAppearInCatalog(t *testing.T) {
	t.Parallel()

	want := []Permission{
		PermissionSystemNotificationsRead,
		PermissionSystemNotificationsManage,
		PermissionProjectMembersUsageRead,
		PermissionProjectExtraUsageRead,
		PermissionProjectExtraUsageWrite,
		PermissionProjectExtraUsageTopup,
	}
	catalog := AllPermissions()
	for _, permission := range want {
		if !permission.Valid() {
			t.Errorf("new permission %q is not Valid()", permission)
		}
		found := false
		for _, catalogued := range catalog {
			if catalogued == permission {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("permission %q missing from AllPermissions()", permission)
		}
	}

	systemWant := []Permission{
		PermissionSystemNotificationsRead,
		PermissionSystemNotificationsManage,
	}
	for _, permission := range systemWant {
		scope, _ := permission.Scope()
		if scope != ScopeSystem {
			t.Errorf("permission %q scope = %q, want %q", permission, scope, ScopeSystem)
		}
	}
	projectWant := []Permission{
		PermissionProjectMembersUsageRead,
		PermissionProjectExtraUsageRead,
		PermissionProjectExtraUsageWrite,
		PermissionProjectExtraUsageTopup,
	}
	for _, permission := range projectWant {
		scope, _ := permission.Scope()
		if scope != ScopeProject {
			t.Errorf("permission %q scope = %q, want %q", permission, scope, ScopeProject)
		}
	}
}
