package resolvers

import (
	"context"

	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/types"
)

type orderReader interface {
	GetOrderByID(id string) (*types.Order, error)
}

// ExtraUsageTopupResolver resolves the extra-usage-topup resource by fetching
// the Order and verifying it is a topup order. Non-topup orders (subscription,
// etc.) return an empty ProjectID so the middleware's containment check treats
// them as not-found — matching legacy behavior which returned 404 for any
// order.OrderType != OrderTypeExtraUsageTopup.
type ExtraUsageTopupResolver struct {
	Store orderReader
}

func (r ExtraUsageTopupResolver) Resolve(_ context.Context, ref authz.ResourceReference) (authz.Resource, error) {
	if r.Store == nil {
		// During offline spec generation, return a placeholder resource.
		// At runtime, the resolver will be properly wired with a store.
		return authz.Resource{
			Type:      "extra-usage-topup",
			ID:        ref.ID,
			ProjectID: "", // unknown without store access
		}, nil
	}
	order, err := r.Store.GetOrderByID(ref.ID)
	if err != nil || order == nil {
		return authz.Resource{}, err
	}
	if order.OrderType != types.OrderTypeExtraUsageTopup {
		return authz.Resource{}, nil // empty ProjectID → 404 via containment
	}
	return authz.Resource{
		Type:      "extra-usage-topup",
		ID:        order.ID,
		ProjectID: order.ProjectID,
	}, nil
}

func init() {
	// Register a stub resolver for offline spec generation.
	// At runtime (in main.go), the resolver will be wired with the proper store.
	Default().Register("extra-usage-topup", ExtraUsageTopupResolver{})
}
