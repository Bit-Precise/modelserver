// Package resolvers wires resource resolvers used by admin operations.
// Later batches add resolvers here (api-key, order, subscription, ...).
package resolvers

import (
	"strings"

	"github.com/modelserver/modelserver/internal/authz"
)

// Registry maps a ResourceType (as used in AccessPolicy.Resource.Type) to the
// resolver that loads the resource for runtime authorization.
type Registry map[string]authz.ResourceResolver

// New returns an empty resolver registry.
func New() Registry {
	return Registry{}
}

// Register attaches a resolver to a resource type. It panics on empty type
// or duplicate registration so misconfigurations fail at startup.
func (r Registry) Register(resourceType string, resolver authz.ResourceResolver) {
	if strings.TrimSpace(resourceType) == "" {
		panic("resolvers: resource type must not be empty")
	}
	if resolver == nil {
		panic("resolvers: resolver must not be nil")
	}
	if _, exists := r[resourceType]; exists {
		panic("resolvers: duplicate registration for " + resourceType)
	}
	r[resourceType] = resolver
}

// Default returns the resolver set the admin server uses when no
// registry is supplied. Runtime callers (main.go) always pass an
// explicit registry via Server.Resolvers, so Default stays empty:
// any fallback to Default at runtime is treated as misconfiguration
// (containment check on an unregistered type denies by default).
func Default() Registry {
	return defaultRegistry
}

var defaultRegistry = New()

// KnownResourceTypes declares the resource types that admin operations
// may reference in AccessPolicy.Resource. Batches register their type
// via init() so the contract-level invariant test can verify that every
// declared Resource has a corresponding known type — without exposing a
// callable stub resolver in the runtime registry.
var KnownResourceTypes = map[string]struct{}{}
