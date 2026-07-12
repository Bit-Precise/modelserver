package adminv1

import "github.com/modelserver/modelserver/internal/authz"

// DefaultPolicies returns the admin runtime's policy set. Every policy
// referenced by an operation must appear here; missing policies are
// treated as a deny by the authorization middleware.
//
// Built-in policies from internal/authz that do not yet have concrete
// implementations in this repository are absent from this map on
// purpose: middleware defaults to deny, so unwired IDs behave safely.
// Each subsequent batch that starts using a built-in policy registers
// its implementation here alongside the migration.
func DefaultPolicies() map[authz.PolicyID]authz.Policy {
	return map[authz.PolicyID]authz.Policy{
		authz.PolicyKeyOwnedByCallerForDeveloper: authz.KeyOwnedByCallerForDeveloperPolicy{},
		authz.PolicyMemberSelfOrElevated:         authz.MemberSelfOrElevatedPolicy{},
	}
}
