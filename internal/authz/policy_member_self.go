package authz

import "context"

// MemberSelfOrElevatedPolicy authorizes read access to a per-member
// endpoint when the caller is either an elevated principal (superadmin,
// owner, maintainer) or the resource IS the caller's own member row.
type MemberSelfOrElevatedPolicy struct{}

// ID returns PolicyMemberSelfOrElevated.
func (MemberSelfOrElevatedPolicy) ID() PolicyID { return PolicyMemberSelfOrElevated }

// Evaluate implements Policy.
func (MemberSelfOrElevatedPolicy) Evaluate(_ context.Context, input PolicyInput) (bool, error) {
	if input.Principal.Superadmin {
		return true, nil
	}
	if input.Role == RoleOwner || input.Role == RoleMaintainer {
		return true, nil
	}
	if input.Resource == nil || input.Principal.UserID == "" {
		return false, nil
	}
	return input.Resource.ID == input.Principal.UserID, nil
}

var _ Policy = MemberSelfOrElevatedPolicy{}
