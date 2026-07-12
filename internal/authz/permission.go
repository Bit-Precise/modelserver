package authz

import "sort"

// Permission is a stable authorization capability identifier. Permission
// values are part of the API contract and should not be renamed casually.
type Permission string

const (
	// System-scoped permissions are only granted to explicit superadmins.
	PermissionSystemUsersRead            Permission = "system.users.read"
	PermissionSystemUsersWrite           Permission = "system.users.write"
	PermissionSystemPlansRead            Permission = "system.plans.read"
	PermissionSystemPlansManage          Permission = "system.plans.manage"
	PermissionSystemModelsRead           Permission = "system.models.read"
	PermissionSystemModelsManage         Permission = "system.models.manage"
	PermissionSystemProjectsRead         Permission = "system.projects.read"
	PermissionSystemRequestsRead         Permission = "system.requests.read"
	PermissionSystemExtraUsageRead       Permission = "system.extra_usage.read"
	PermissionSystemExtraUsageManage     Permission = "system.extra_usage.manage"
	PermissionSystemUpstreamsRead        Permission = "system.upstreams.read"
	PermissionSystemUpstreamsManage      Permission = "system.upstreams.manage"
	PermissionSystemUpstreamGroupsRead   Permission = "system.upstream_groups.read"
	PermissionSystemUpstreamGroupsManage Permission = "system.upstream_groups.manage"
	PermissionSystemOAuthClientsRead     Permission = "system.oauth_clients.read"
	PermissionSystemOAuthClientsManage   Permission = "system.oauth_clients.manage"
	PermissionSystemRoutingRead          Permission = "system.routing.read"
	PermissionSystemRoutingManage        Permission = "system.routing.manage"
	PermissionSystemSubscriptionOverride Permission = "system.subscription.override"
	PermissionSystemNotificationsRead    Permission = "system.notifications.read"
	PermissionSystemNotificationsManage  Permission = "system.notifications.manage"

	// Project-scoped permissions are granted by project roles. Resource-level
	// policies may further narrow a permission (for example, to owned keys).
	PermissionProjectRead              Permission = "project.read"
	PermissionProjectSettingsWrite     Permission = "project.settings.write"
	PermissionProjectArchive           Permission = "project.archive"
	PermissionProjectOwnershipTransfer Permission = "project.ownership.transfer"
	PermissionProjectModelsRead        Permission = "project.models.read"
	PermissionProjectMembersRead       Permission = "project.members.read"
	PermissionProjectMembersManage     Permission = "project.members.manage"
	PermissionProjectKeysCreate        Permission = "project.keys.create"
	PermissionProjectKeysRead          Permission = "project.keys.read"
	PermissionProjectKeysManage        Permission = "project.keys.manage"
	PermissionProjectOAuthGrantsRead   Permission = "project.oauth_grants.read"
	PermissionProjectOAuthGrantsManage Permission = "project.oauth_grants.manage"
	PermissionProjectPoliciesRead      Permission = "project.policies.read"
	PermissionProjectPoliciesManage    Permission = "project.policies.manage"
	PermissionProjectSubscriptionsRead Permission = "project.subscriptions.read"
	PermissionProjectPlansRead         Permission = "project.plans.read"
	PermissionProjectOrdersRead        Permission = "project.orders.read"
	PermissionProjectOrdersCreate      Permission = "project.orders.create"
	PermissionProjectOrdersManage      Permission = "project.orders.manage"
	PermissionProjectBillingRead       Permission = "project.billing.read"
	PermissionProjectBillingManage     Permission = "project.billing.manage"
	PermissionProjectRequestsRead        Permission = "project.requests.read"
	PermissionProjectUsageRead           Permission = "project.usage.read"
	PermissionProjectTracesRead          Permission = "project.traces.read"
	PermissionProjectMembersUsageRead    Permission = "project.members.usage.read"
	PermissionProjectExtraUsageRead      Permission = "project.extra_usage.read"
	PermissionProjectExtraUsageWrite     Permission = "project.extra_usage.write"
	PermissionProjectExtraUsageTopup     Permission = "project.extra_usage.topup"
)

var permissionScopes = map[Permission]Scope{
	PermissionSystemUsersRead:            ScopeSystem,
	PermissionSystemUsersWrite:           ScopeSystem,
	PermissionSystemPlansRead:            ScopeSystem,
	PermissionSystemPlansManage:          ScopeSystem,
	PermissionSystemModelsRead:           ScopeSystem,
	PermissionSystemModelsManage:         ScopeSystem,
	PermissionSystemProjectsRead:         ScopeSystem,
	PermissionSystemRequestsRead:         ScopeSystem,
	PermissionSystemExtraUsageRead:       ScopeSystem,
	PermissionSystemExtraUsageManage:     ScopeSystem,
	PermissionSystemUpstreamsRead:        ScopeSystem,
	PermissionSystemUpstreamsManage:      ScopeSystem,
	PermissionSystemUpstreamGroupsRead:   ScopeSystem,
	PermissionSystemUpstreamGroupsManage: ScopeSystem,
	PermissionSystemOAuthClientsRead:     ScopeSystem,
	PermissionSystemOAuthClientsManage:   ScopeSystem,
	PermissionSystemRoutingRead:          ScopeSystem,
	PermissionSystemRoutingManage:        ScopeSystem,
	PermissionSystemSubscriptionOverride: ScopeSystem,
	PermissionSystemNotificationsRead:    ScopeSystem,
	PermissionSystemNotificationsManage:  ScopeSystem,
	PermissionProjectRead:                ScopeProject,
	PermissionProjectSettingsWrite:       ScopeProject,
	PermissionProjectArchive:             ScopeProject,
	PermissionProjectOwnershipTransfer:   ScopeProject,
	PermissionProjectModelsRead:          ScopeProject,
	PermissionProjectMembersRead:         ScopeProject,
	PermissionProjectMembersManage:       ScopeProject,
	PermissionProjectKeysCreate:          ScopeProject,
	PermissionProjectKeysRead:            ScopeProject,
	PermissionProjectKeysManage:          ScopeProject,
	PermissionProjectOAuthGrantsRead:     ScopeProject,
	PermissionProjectOAuthGrantsManage:   ScopeProject,
	PermissionProjectPoliciesRead:        ScopeProject,
	PermissionProjectPoliciesManage:      ScopeProject,
	PermissionProjectSubscriptionsRead:   ScopeProject,
	PermissionProjectPlansRead:           ScopeProject,
	PermissionProjectOrdersRead:          ScopeProject,
	PermissionProjectOrdersCreate:        ScopeProject,
	PermissionProjectOrdersManage:        ScopeProject,
	PermissionProjectBillingRead:         ScopeProject,
	PermissionProjectBillingManage:       ScopeProject,
	PermissionProjectRequestsRead:        ScopeProject,
	PermissionProjectUsageRead:           ScopeProject,
	PermissionProjectTracesRead:          ScopeProject,
	PermissionProjectMembersUsageRead:    ScopeProject,
	PermissionProjectExtraUsageRead:      ScopeProject,
	PermissionProjectExtraUsageWrite:     ScopeProject,
	PermissionProjectExtraUsageTopup:     ScopeProject,
}

// Valid reports whether p belongs to the built-in permission catalog.
func (p Permission) Valid() bool {
	_, ok := permissionScopes[p]
	return ok
}

// Scope returns the scope in which p can be granted. Unknown permissions have
// ScopeNone and false.
func (p Permission) Scope() (Scope, bool) {
	scope, ok := permissionScopes[p]
	if !ok {
		return ScopeNone, false
	}
	return scope, ok
}

// AllPermissions returns the complete permission catalog in stable lexical
// order. The returned slice is a copy.
func AllPermissions() []Permission {
	permissions := make([]Permission, 0, len(permissionScopes))
	for permission := range permissionScopes {
		permissions = append(permissions, permission)
	}
	sort.Slice(permissions, func(i, j int) bool {
		return permissions[i] < permissions[j]
	})
	return permissions
}

func permissionsInScope(scope Scope) []Permission {
	permissions := make([]Permission, 0, len(permissionScopes))
	for permission, permissionScope := range permissionScopes {
		if permissionScope == scope {
			permissions = append(permissions, permission)
		}
	}
	return permissions
}
