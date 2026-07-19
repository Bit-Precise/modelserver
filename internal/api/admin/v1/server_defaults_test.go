package adminv1

import (
	"testing"

	"github.com/modelserver/modelserver/internal/authz"
)

func TestServerEffectivePoliciesFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	server := &Server{}
	got := server.effectivePolicies()
	for _, id := range []authz.PolicyID{
		authz.PolicyKeyOwnedByCallerForDeveloper,
		authz.PolicyMemberSelfOrElevated,
	} {
		if _, ok := got[id]; !ok {
			t.Errorf("default effective policies missing %q", id)
		}
	}
}

func TestServerEffectivePoliciesRespectsExplicit(t *testing.T) {
	t.Parallel()

	explicit := map[authz.PolicyID]authz.Policy{
		authz.PolicyOwnResource: nil, // marker only
	}
	server := &Server{Policies: explicit}
	got := server.effectivePolicies()
	if _, ok := got[authz.PolicyOwnResource]; !ok {
		t.Fatal("explicit policies were replaced by defaults")
	}
	if _, ok := got[authz.PolicyKeyOwnedByCallerForDeveloper]; ok {
		t.Fatal("defaults leaked into explicit policies")
	}
}

func TestServerEffectiveResolversFallsBackToDefaults(t *testing.T) {
	t.Parallel()

	server := &Server{}
	got := server.effectiveResolvers()
	if got == nil {
		t.Fatal("effectiveResolvers() returned nil")
	}
	// Task 8's Default() is currently empty; verify shape only.
	if len(got) != 0 {
		t.Errorf("default resolver registry has %d entries; expected 0 until subsystem batches register", len(got))
	}
}
