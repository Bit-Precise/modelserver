package contract

import (
	"context"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/authz"
)

const authzExtension = "x-modelserver-authz"

// AuthorizationMiddlewareFactory binds an operation's declarative access
// policy to the middleware which enforces it. Spec generation uses the same
// factory as runtime registration; the returned middleware is not executed
// while generating a document.
type AuthorizationMiddlewareFactory func(authz.AccessPolicy) func(huma.Context, func(huma.Context))

// Operation is the ModelServer-owned subset of huma.Operation. Keeping access
// metadata outside comments makes it possible to validate and enforce the
// exact policy which is emitted into OpenAPI.
type Operation struct {
	ID            string
	Method        string
	Path          string
	Hidden        bool
	Summary       string
	Description   string
	Tags          []string
	DefaultStatus int
	MaxBodyBytes  int64
	Errors        []int
	Access        authz.AccessPolicy
	Authorize     AuthorizationMiddlewareFactory
}

// Register installs a typed operation and adds its contract to OpenAPI. An
// invalid or unenforceable access descriptor is a startup error, so this
// function deliberately panics rather than exposing an unprotected route.
func Register[I, O any](api huma.API, operation Operation, handler func(context.Context, *I) (*O, error)) {
	if operation.ID == "" {
		panic("contract: operation ID is required")
	}
	if err := operation.Access.Validate(); err != nil {
		panic(fmt.Sprintf("contract: operation %s has invalid access policy: %v", operation.ID, err))
	}

	humaOperation := huma.Operation{
		OperationID:   operation.ID,
		Method:        operation.Method,
		Path:          operation.Path,
		Hidden:        operation.Hidden,
		Summary:       operation.Summary,
		Description:   operation.Description,
		Tags:          append([]string(nil), operation.Tags...),
		DefaultStatus: operation.DefaultStatus,
		MaxBodyBytes:  operation.MaxBodyBytes,
		Errors:        append([]int(nil), operation.Errors...),
		Metadata: map[string]any{
			authzExtension: operation.Access,
		},
		Extensions: map[string]any{
			authzExtension: operation.Access,
		},
	}

	switch operation.Access.Mode {
	case authz.AccessModePublic:
		// A non-nil empty slice explicitly overrides any future top-level
		// security declaration.
		humaOperation.Security = []map[string][]string{}
	case authz.AccessModeAuthenticated, authz.AccessModeRBAC:
		humaOperation.Security = []map[string][]string{{AdminJWTSecurityScheme: {}}}
		if operation.Authorize == nil {
			panic(fmt.Sprintf("contract: operation %s has no authorization middleware", operation.ID))
		}
		humaOperation.Middlewares = append(humaOperation.Middlewares, operation.Authorize(operation.Access))
	case authz.AccessModeHMAC:
		panic(fmt.Sprintf("contract: operation %s uses HMAC, which is not enabled for the dashboard contract", operation.ID))
	default:
		panic(fmt.Sprintf("contract: operation %s has unsupported access mode %q", operation.ID, operation.Access.Mode))
	}

	huma.Register(api, humaOperation, handler)
}
