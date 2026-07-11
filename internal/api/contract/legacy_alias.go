package contract

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// RegisterWithLegacyTrailingSlash installs a typed operation at the given
// canonical path, then registers a hidden alias whose path has a trailing
// slash. The alias runs the same handler under the same AccessPolicy so
// legacy Dashboard clients still work, but the emitted OpenAPI contract
// keeps a single canonical URL.
func RegisterWithLegacyTrailingSlash[I, O any](
	api huma.API,
	operation Operation,
	handler func(context.Context, *I) (*O, error),
) {
	Register(api, operation, handler)

	alias := operation
	alias.ID += "LegacyTrailingSlash"
	alias.Path += "/"
	alias.Hidden = true
	Register(api, alias, handler)
}
