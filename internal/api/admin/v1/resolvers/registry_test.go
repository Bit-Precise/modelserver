package resolvers

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/authz"
)

type stubResolver struct{ projectID string }

func (r stubResolver) Resolve(_ context.Context, ref authz.ResourceReference) (authz.Resource, error) {
	return authz.Resource{Type: ref.Type, ID: ref.ID, ProjectID: r.projectID}, nil
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	t.Parallel()

	registry := New()
	registry.Register("api-key", stubResolver{projectID: "p1"})

	resolver, ok := registry["api-key"]
	if !ok {
		t.Fatal("api-key resolver missing after registration")
	}
	resource, err := resolver.Resolve(context.Background(), authz.ResourceReference{Type: "api-key", ID: "k1"})
	if err != nil {
		t.Fatalf("Resolve error = %v", err)
	}
	if resource.ProjectID != "p1" {
		t.Fatalf("Resource.ProjectID = %q", resource.ProjectID)
	}
}

func TestRegistryPanicsOnEmptyType(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Register with empty type did not panic")
		}
	}()
	New().Register("", stubResolver{})
}

func TestRegistryPanicsOnDuplicate(t *testing.T) {
	t.Parallel()

	registry := New()
	registry.Register("api-key", stubResolver{})
	defer func() {
		if recover() == nil {
			t.Fatal("duplicate Register did not panic")
		}
	}()
	registry.Register("api-key", stubResolver{})
}

func TestDefaultReturnsEmptyRegistryForNow(t *testing.T) {
	t.Parallel()

	if len(Default()) != 0 {
		t.Fatalf("Default() = %v; expected empty until subsystem batches register resolvers", Default())
	}
}
