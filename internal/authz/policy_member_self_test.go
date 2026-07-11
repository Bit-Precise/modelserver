package authz

import (
	"context"
	"testing"
)

func TestMemberSelfOrElevatedPolicy(t *testing.T) {
	t.Parallel()

	policy := MemberSelfOrElevatedPolicy{}
	if policy.ID() != PolicyMemberSelfOrElevated {
		t.Fatalf("ID() = %q", policy.ID())
	}

	developer := Principal{UserID: "user-a"}
	other := Principal{UserID: "user-b"}
	superadmin := Principal{UserID: "admin-1", Superadmin: true}
	selfRow := Resource{Type: "member", ID: "user-a", ProjectID: "p1"}
	otherRow := Resource{Type: "member", ID: "user-b", ProjectID: "p1"}

	tests := []struct {
		name  string
		input PolicyInput
		want  bool
	}{
		{name: "owner", input: PolicyInput{Principal: other, Role: RoleOwner, Resource: &otherRow}, want: true},
		{name: "maintainer", input: PolicyInput{Principal: other, Role: RoleMaintainer, Resource: &otherRow}, want: true},
		{name: "superadmin", input: PolicyInput{Principal: superadmin, Resource: &otherRow}, want: true},
		{name: "developer self", input: PolicyInput{Principal: developer, Role: RoleDeveloper, Resource: &selfRow}, want: true},
		{name: "developer other", input: PolicyInput{Principal: developer, Role: RoleDeveloper, Resource: &otherRow}, want: false},
		{name: "developer nil resource", input: PolicyInput{Principal: developer, Role: RoleDeveloper}, want: false},
		{name: "anonymous", input: PolicyInput{Principal: Principal{}, Resource: &selfRow}, want: false},
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
