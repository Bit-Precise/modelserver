package types

import "testing"

func TestIsAssignableRole(t *testing.T) {
	cases := []struct {
		role string
		want bool
	}{
		{RoleMaintainer, true},
		{RoleDeveloper, true},
		{RoleOwner, false},  // owner is only set by CreateProject / TransferProjectOwnership
		{"", false},
		{"janitor", false},
		{"OWNER", false},     // case-sensitive
	}
	for _, c := range cases {
		if got := IsAssignableRole(c.role); got != c.want {
			t.Errorf("IsAssignableRole(%q) = %v, want %v", c.role, got, c.want)
		}
	}
}
