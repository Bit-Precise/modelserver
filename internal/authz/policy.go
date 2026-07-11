package authz

import "context"

// PolicyID is a stable identifier referenced by AccessPolicy.Policies and
// resolved by the runtime Authorizer.
type PolicyID string

const (
	PolicyOwnResource                PolicyID = "own-resource"
	PolicyResourceProjectContainment PolicyID = "resource-project-containment"
	PolicyMemberRoleHierarchy        PolicyID = "member-role-hierarchy"

	PolicyKeyOwnedByCallerForDeveloper PolicyID = "key-owned-by-caller-for-developer"
	PolicyMemberSelfOrElevated         PolicyID = "member-self-or-elevated"
)

// ResourceReference is the unresolved reference extracted by an HTTP adapter.
// ProjectID is the project asserted by the request path; a resolver returns the
// resource's actual ProjectID so containment can be checked.
type ResourceReference struct {
	Type      string
	ID        string
	ProjectID string
}

// Resource contains authorization-relevant attributes. Resolvers should avoid
// placing secrets or large domain objects in Attributes.
type Resource struct {
	Type       string
	ID         string
	ProjectID  string
	OwnerID    string
	Attributes map[string]any
}

// ResourceResolver loads authorization attributes for a kind of resource.
// Implementations are registered by ResourceType outside this package.
type ResourceResolver interface {
	Resolve(context.Context, ResourceReference) (Resource, error)
}

// PolicyInput is the complete, transport-independent input to a conditional
// authorization policy.
type PolicyInput struct {
	Principal Principal
	Access    AccessPolicy
	Role      ProjectRole
	ProjectID string
	Resource  *Resource
}

// Policy evaluates one named condition. Returning false is a denial; callers
// must also treat errors and missing policy implementations as denials.
type Policy interface {
	ID() PolicyID
	Evaluate(context.Context, PolicyInput) (bool, error)
}
