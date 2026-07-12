package authz

import "context"

// KeyOwnedByCallerForDeveloperPolicy authorizes an API-key operation for
// callers who are project owners, maintainers, or superadmins; otherwise
// it requires the caller to own the resource. The policy encodes the
// legacy "developers can only touch keys they created" rule as one
// AccessPolicy condition so it appears in x-modelserver-authz metadata.
type KeyOwnedByCallerForDeveloperPolicy struct{}

// ID returns PolicyKeyOwnedByCallerForDeveloper.
func (KeyOwnedByCallerForDeveloperPolicy) ID() PolicyID { return PolicyKeyOwnedByCallerForDeveloper }

// Evaluate implements Policy. Superadmins and owner/maintainer roles
// short-circuit to allow; other callers must match the resource owner.
func (KeyOwnedByCallerForDeveloperPolicy) Evaluate(_ context.Context, input PolicyInput) (bool, error) {
	if input.Principal.Superadmin {
		return true, nil
	}
	if input.Role == RoleOwner || input.Role == RoleMaintainer {
		return true, nil
	}
	if input.Resource == nil {
		return false, nil
	}
	if input.Principal.UserID == "" || input.Resource.OwnerID == "" {
		return false, nil
	}
	return input.Principal.UserID == input.Resource.OwnerID, nil
}

var _ Policy = KeyOwnedByCallerForDeveloperPolicy{}
