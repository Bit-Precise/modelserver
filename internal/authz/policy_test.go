package authz

import (
	"context"
	"testing"
)

type testResourceResolver struct{}

func (testResourceResolver) Resolve(_ context.Context, reference ResourceReference) (Resource, error) {
	return Resource{
		Type:      reference.Type,
		ID:        reference.ID,
		ProjectID: "actual-project",
		OwnerID:   "owner-1",
	}, nil
}

type testOwnPolicy struct{}

func (testOwnPolicy) ID() PolicyID { return PolicyOwnResource }

func (testOwnPolicy) Evaluate(_ context.Context, input PolicyInput) (bool, error) {
	return input.Resource != nil && input.Principal.UserID == input.Resource.OwnerID, nil
}

var (
	_ ResourceResolver = testResourceResolver{}
	_ Policy           = testOwnPolicy{}
)

func TestPolicyAndResourceResolverExtensionPoints(t *testing.T) {
	t.Parallel()

	resource, err := (testResourceResolver{}).Resolve(context.Background(), ResourceReference{
		Type:      "key",
		ID:        "key-1",
		ProjectID: "asserted-project",
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resource.ProjectID != "actual-project" {
		t.Fatalf("resolved ProjectID = %q", resource.ProjectID)
	}

	policy := testOwnPolicy{}
	allowed, err := policy.Evaluate(context.Background(), PolicyInput{
		Principal: Principal{UserID: "owner-1"},
		Access: Project(
			PermissionProjectKeysManage,
			"projectID",
			WithResource("key", "keyID"),
			WithPolicies(PolicyOwnResource),
		),
		Role:      RoleDeveloper,
		ProjectID: "asserted-project",
		Resource:  &resource,
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if !allowed {
		t.Fatal("owner policy denied matching owner")
	}
}
