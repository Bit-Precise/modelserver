package authz

import (
	"context"
	"testing"
)

func TestKeyOwnedByCallerForDeveloperPolicy(t *testing.T) {
	t.Parallel()

	policy := KeyOwnedByCallerForDeveloperPolicy{}
	if policy.ID() != PolicyKeyOwnedByCallerForDeveloper {
		t.Fatalf("ID() = %q", policy.ID())
	}

	callerA := Principal{UserID: "user-a"}
	callerB := Principal{UserID: "user-b"}
	superadmin := Principal{UserID: "admin-1", Superadmin: true}
	own := Resource{Type: "api-key", ID: "k1", ProjectID: "p1", OwnerID: "user-a"}

	tests := []struct {
		name    string
		input   PolicyInput
		want    bool
	}{
		{name: "superadmin without resource", input: PolicyInput{Principal: superadmin}, want: true},
		{name: "owner role", input: PolicyInput{Principal: callerB, Role: RoleOwner, Resource: &own}, want: true},
		{name: "maintainer role", input: PolicyInput{Principal: callerB, Role: RoleMaintainer, Resource: &own}, want: true},
		{name: "developer owning resource", input: PolicyInput{Principal: callerA, Role: RoleDeveloper, Resource: &own}, want: true},
		{name: "developer other's resource", input: PolicyInput{Principal: callerB, Role: RoleDeveloper, Resource: &own}, want: false},
		{name: "developer nil resource", input: PolicyInput{Principal: callerA, Role: RoleDeveloper}, want: false},
		{name: "anonymous with owner role", input: PolicyInput{Principal: Principal{}, Role: RoleOwner}, want: true},
		{name: "developer unknown owner", input: PolicyInput{Principal: callerA, Role: RoleDeveloper, Resource: &Resource{OwnerID: ""}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := policy.Evaluate(context.Background(), test.input)
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("Evaluate() = %v, want %v", got, test.want)
			}
		})
	}
}
