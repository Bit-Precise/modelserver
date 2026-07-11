package authz

// ProjectRole is a built-in role assigned to a project member.
type ProjectRole string

const (
	RoleOwner      ProjectRole = "owner"
	RoleMaintainer ProjectRole = "maintainer"
	RoleDeveloper  ProjectRole = "developer"
)

// AllProjectRoles returns all built-in roles from most to least privileged.
func AllProjectRoles() []ProjectRole {
	return []ProjectRole{RoleOwner, RoleMaintainer, RoleDeveloper}
}

// Valid reports whether r is one of the built-in project roles. Unknown roles
// are never treated as a privileged fallback.
func (r ProjectRole) Valid() bool {
	switch r {
	case RoleOwner, RoleMaintainer, RoleDeveloper:
		return true
	default:
		return false
	}
}

var developerPermissions = []Permission{
	PermissionProjectRead,
	PermissionProjectModelsRead,
	PermissionProjectMembersRead,
	PermissionProjectKeysCreate,
	PermissionProjectKeysRead,
	PermissionProjectKeysManage,
	PermissionProjectOAuthGrantsRead,
	PermissionProjectPoliciesRead,
	PermissionProjectSubscriptionsRead,
	PermissionProjectPlansRead,
	PermissionProjectOrdersRead,
	PermissionProjectBillingRead,
	PermissionProjectRequestsRead,
	PermissionProjectUsageRead,
	PermissionProjectTracesRead,
}

var maintainerPermissions = appendPermissions(developerPermissions,
	PermissionProjectSettingsWrite,
	PermissionProjectMembersManage,
	PermissionProjectOAuthGrantsManage,
	PermissionProjectPoliciesManage,
	PermissionProjectOrdersCreate,
	PermissionProjectOrdersManage,
	PermissionProjectBillingManage,
)

var ownerPermissions = appendPermissions(maintainerPermissions,
	PermissionProjectArchive,
	PermissionProjectOwnershipTransfer,
)

var permissionsByRole = map[ProjectRole][]Permission{
	RoleDeveloper:  developerPermissions,
	RoleMaintainer: maintainerPermissions,
	RoleOwner:      ownerPermissions,
}

// CapabilitySet is an immutable-by-API set of granted permissions. Its zero
// value is a valid empty set.
type CapabilitySet struct {
	permissions map[Permission]struct{}
}

// NewCapabilitySet builds a capability set from valid permission IDs. Unknown
// permissions are ignored, preserving a default-deny posture.
func NewCapabilitySet(permissions ...Permission) CapabilitySet {
	set := CapabilitySet{permissions: make(map[Permission]struct{}, len(permissions))}
	for _, permission := range permissions {
		if permission.Valid() {
			set.permissions[permission] = struct{}{}
		}
	}
	return set
}

// Has reports whether permission belongs to the set.
func (s CapabilitySet) Has(permission Permission) bool {
	_, ok := s.permissions[permission]
	return ok
}

// Len returns the number of permissions in the set.
func (s CapabilitySet) Len() int {
	return len(s.permissions)
}

// Permissions returns the set in stable lexical order.
func (s CapabilitySet) Permissions() []Permission {
	permissions := make([]Permission, 0, len(s.permissions))
	for permission := range s.permissions {
		permissions = append(permissions, permission)
	}
	sortPermissions(permissions)
	return permissions
}

// Strings returns the permission IDs in stable lexical order, convenient for
// capability API responses.
func (s CapabilitySet) Strings() []string {
	permissions := s.Permissions()
	result := make([]string, len(permissions))
	for i, permission := range permissions {
		result[i] = string(permission)
	}
	return result
}

// PermissionsForRole returns an isolated capability set for a built-in role.
// The boolean is false for unknown roles, which therefore grant nothing.
func PermissionsForRole(role ProjectRole) (CapabilitySet, bool) {
	permissions, ok := permissionsByRole[role]
	if !ok {
		return CapabilitySet{}, false
	}
	return NewCapabilitySet(permissions...), true
}

// ProjectCapabilities computes the project capabilities visible to a
// principal. Explicit superadmins receive every project permission. A normal
// user must have a known role; a missing or unknown role grants nothing.
func ProjectCapabilities(principal Principal, role ProjectRole) CapabilitySet {
	if !principal.Authenticated() {
		return CapabilitySet{}
	}
	if principal.Superadmin {
		return NewCapabilitySet(permissionsInScope(ScopeProject)...)
	}
	permissions, ok := PermissionsForRole(role)
	if !ok {
		return CapabilitySet{}
	}
	return permissions
}

// SystemCapabilities computes system-wide capabilities. Only an explicitly
// authenticated superadmin receives them.
func SystemCapabilities(principal Principal) CapabilitySet {
	if !principal.Authenticated() || !principal.Superadmin {
		return CapabilitySet{}
	}
	return NewCapabilitySet(permissionsInScope(ScopeSystem)...)
}

// HasProjectPermission is a convenience check over ProjectCapabilities.
func HasProjectPermission(principal Principal, role ProjectRole, permission Permission) bool {
	scope, valid := permission.Scope()
	return valid && scope == ScopeProject && ProjectCapabilities(principal, role).Has(permission)
}

// HasSystemPermission is a convenience check over SystemCapabilities.
func HasSystemPermission(principal Principal, permission Permission) bool {
	scope, valid := permission.Scope()
	return valid && scope == ScopeSystem && SystemCapabilities(principal).Has(permission)
}

func appendPermissions(base []Permission, additions ...Permission) []Permission {
	result := make([]Permission, 0, len(base)+len(additions))
	result = append(result, base...)
	result = append(result, additions...)
	return result
}

func sortPermissions(permissions []Permission) {
	for i := 1; i < len(permissions); i++ {
		for j := i; j > 0 && permissions[j] < permissions[j-1]; j-- {
			permissions[j], permissions[j-1] = permissions[j-1], permissions[j]
		}
	}
}
