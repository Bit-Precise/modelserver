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
	order, err := r.Store.GetOrderByID(ref.ID)
	if err != nil || order == nil {
		return authz.Resource{}, err
	}
	if order.OrderType != types.OrderTypeExtraUsageTopup {
		return authz.Resource{}, nil
	}
	return authz.Resource{
		Type:      "extra-usage-topup",
		ID:        order.ID,
		ProjectID: order.ProjectID,
	}, nil
}

func init() {
	KnownResourceTypes["extra-usage-topup"] = struct{}{}
}
