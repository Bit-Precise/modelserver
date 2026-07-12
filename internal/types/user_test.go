package types

import "testing"

func TestIsValidProjectRole(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{RoleOwner, true},
		{RoleMaintainer, true},
		{RoleDeveloper, true},
		{"", false},
		{"administrator", false},
		{"OWNER", false},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := IsValidProjectRole(tt.role); got != tt.want {
				t.Fatalf("IsValidProjectRole(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}
