package admin

import (
	"net/http"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestCanSetMemberQuota(t *testing.T) {
	owner := &types.ProjectMember{UserID: "u-owner", Role: types.RoleOwner}
	maint := &types.ProjectMember{UserID: "u-maint", Role: types.RoleMaintainer}
	dev := &types.ProjectMember{UserID: "u-dev", Role: types.RoleDeveloper}

	cases := []struct {
		name               string
		caller             *types.ProjectMember
		callerIsSuperadmin bool
		targetRole         string
		isSelf             bool
		wantOK             bool
		wantStatus         int
	}{
		// Superadmin privilege is explicit; a nil member alone is denied.
		{"superadmin_on_owner", nil, true, types.RoleOwner, false, true, 0},
		{"superadmin_on_self_owner", nil, true, types.RoleOwner, true, true, 0},
		{"missing_member_without_superadmin", nil, false, types.RoleDeveloper, false, false, http.StatusForbidden},

		// Owner caller — can set quota on anyone, including self and other owners.
		{"owner_on_owner", owner, false, types.RoleOwner, false, true, 0},
		{"owner_on_self", owner, false, types.RoleOwner, true, true, 0},
		{"owner_on_maintainer", owner, false, types.RoleMaintainer, false, true, 0},
		{"owner_on_developer", owner, false, types.RoleDeveloper, false, true, 0},

		// Maintainer caller — can set on non-owners, including self and other maintainers.
		{"maintainer_on_owner", maint, false, types.RoleOwner, false, false, http.StatusForbidden},
		{"maintainer_on_self", maint, false, types.RoleMaintainer, true, true, 0},
		{"maintainer_on_other_maintainer", maint, false, types.RoleMaintainer, false, true, 0},
		{"maintainer_on_developer", maint, false, types.RoleDeveloper, false, true, 0},

		// Developer caller — never has quota permissions (route-level requireRole
		// already blocks them; helper still returns 403 for defence in depth).
		{"developer_on_developer", dev, false, types.RoleDeveloper, false, false, http.StatusForbidden},
		{"developer_on_self", dev, false, types.RoleDeveloper, true, false, http.StatusForbidden},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, status, code, msg := canSetMemberQuota(c.caller, c.callerIsSuperadmin, c.targetRole, c.isSelf)
			if ok != c.wantOK {
				t.Fatalf("ok=%v, want %v (status=%d code=%q msg=%q)", ok, c.wantOK, status, code, msg)
			}
			if !ok && status != c.wantStatus {
				t.Fatalf("status=%d, want %d (msg=%q)", status, c.wantStatus, msg)
			}
			if !ok && (code == "" || msg == "") {
				t.Fatalf("rejection must include code+msg; got code=%q msg=%q", code, msg)
			}
		})
	}
}
