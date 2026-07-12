package adminv1

import (
	"testing"

	"github.com/modelserver/modelserver/internal/authz"
)

func TestDefaultPoliciesInstallsNewPolicies(t *testing.T) {
	t.Parallel()

	policies := DefaultPolicies()

	for _, id := range []authz.PolicyID{
		authz.PolicyKeyOwnedByCallerForDeveloper,
		authz.PolicyMemberSelfOrElevated,
	} {
		policy, ok := policies[id]
		if !ok || policy == nil {
			t.Errorf("DefaultPolicies() missing %q", id)
			continue
		}
		if policy.ID() != id {
			t.Errorf("policy for %q reports ID %q", id, policy.ID())
		}
	}
}
