package authz

import "testing"

func TestPrincipalAuthenticationAndValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		principal     Principal
		authenticated bool
		valid         bool
	}{
		{name: "user", principal: Principal{UserID: "user-1"}, authenticated: true, valid: true},
		{name: "superadmin", principal: Principal{UserID: "admin-1", Superadmin: true}, authenticated: true, valid: true},
		{name: "anonymous", principal: Principal{}, authenticated: false, valid: false},
		{name: "whitespace ID", principal: Principal{UserID: "  \t"}, authenticated: false, valid: false},
		{name: "anonymous superadmin", principal: Principal{Superadmin: true}, authenticated: false, valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := test.principal.Authenticated(); got != test.authenticated {
				t.Errorf("Authenticated() = %v, want %v", got, test.authenticated)
			}
			if err := test.principal.Validate(); (err == nil) != test.valid {
				t.Errorf("Validate() error = %v, valid want %v", err, test.valid)
			}
		})
	}
}
