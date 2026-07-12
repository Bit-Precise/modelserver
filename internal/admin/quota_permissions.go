package admin

import (
	"net/http"

	"github.com/modelserver/modelserver/internal/types"
)

// canSetMemberQuota encodes the only remaining rule for member-quota mutation:
// only an owner may set a quota on an owner. Maintainers may set quota on any
// non-owner (including themselves and other maintainers); owners may set
// quota on anyone (including themselves and other owners); developers have no
// quota permissions (the calling route's requireRole already enforces this,
// but the helper also returns 403 for any unexpected caller role).
//
// callerIsSuperadmin is explicit because a missing member context must never be
// treated as proof of system-level privilege. Superadmins bypass all checks;
// other callers must have a recognized project role.
//
// On rejection, the returned (status, code, msg) match writeError's signature
// so callers can forward them verbatim.
func canSetMemberQuota(caller *types.ProjectMember, callerIsSuperadmin bool, targetRole string, isSelf bool) (ok bool, status int, code, msg string) {
	_ = isSelf // self-checks are intentionally absent; kept in signature so the
	// rule's domain is explicit at call sites and future tightening is local.

	if callerIsSuperadmin {
		return true, 0, "", ""
	}
	if caller == nil {
		return false, http.StatusForbidden, "forbidden", "insufficient permissions"
	}
	switch caller.Role {
	case types.RoleOwner:
		return true, 0, "", ""
	case types.RoleMaintainer:
		if targetRole == types.RoleOwner {
			return false, http.StatusForbidden, "forbidden", "maintainers cannot set quota on an owner"
		}
		return true, 0, "", ""
	default:
		return false, http.StatusForbidden, "forbidden", "insufficient permissions"
	}
}
