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
		name       string
		caller     *types.ProjectMember
		targetRole string
		isSelf     bool
		wantOK     bool
		wantStatus int
	}{
		// Superadmin (nil caller) — always allowed.
		{"superadmin_on_owner", nil, types.RoleOwner, false, true, 0},
		{"superadmin_on_self_owner", nil, types.RoleOwner, true, true, 0},

		// Owner caller — can set quota on anyone, including self and other owners.
		{"owner_on_owner", owner, types.RoleOwner, false, true, 0},
		{"owner_on_self", owner, types.RoleOwner, true, true, 0},
		{"owner_on_maintainer", owner, types.RoleMaintainer, false, true, 0},
		{"owner_on_developer", owner, types.RoleDeveloper, false, true, 0},

		// Maintainer caller — can set on non-owners, including self and other maintainers.
		{"maintainer_on_owner", maint, types.RoleOwner, false, false, http.StatusForbidden},
		{"maintainer_on_self", maint, types.RoleMaintainer, true, true, 0},
		{"maintainer_on_other_maintainer", maint, types.RoleMaintainer, false, true, 0},
		{"maintainer_on_developer", maint, types.RoleDeveloper, false, true, 0},

		// Developer caller — never has quota permissions (route-level requireRole
		// already blocks them; helper still returns 403 for defence in depth).
		{"developer_on_developer", dev, types.RoleDeveloper, false, false, http.StatusForbidden},
		{"developer_on_self", dev, types.RoleDeveloper, true, false, http.StatusForbidden},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, status, code, msg := canSetMemberQuota(c.caller, c.targetRole, c.isSelf)
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
