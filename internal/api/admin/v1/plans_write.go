package adminv1

import (
	"context"
	"errors"
	"net/http"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/types"
)

// CreatePlanInput is the request body for POST /api/v1/plans.
// The 12 fields match the legacy handleCreatePlan body byte-for-byte.
type CreatePlanInput struct {
	Body struct {
		Name             string                      `json:"name"`
		Slug             string                      `json:"slug"`
		DisplayName      string                      `json:"display_name"`
		Description      string                      `json:"description"`
		TierLevel        int                         `json:"tier_level"`
		GroupTag         string                      `json:"group_tag"`
		PriceCNYFen      int64                       `json:"price_cny_fen"`
		PriceUSDCents    int64                       `json:"price_usd_cents"`
		PeriodMonths     int                         `json:"period_months"`
		CreditRules      []types.CreditRule          `json:"credit_rules"`
		ModelCreditRates map[string]types.CreditRate `json:"model_credit_rates"`
		ClassicRules     []types.ClassicRule         `json:"classic_rules"`
	}
}

// CreatePlanOutput wraps a types.Plan in the standard data envelope.
type CreatePlanOutput struct {
	Body DataResponse[types.Plan]
}

// createPlan implements POST /api/v1/plans.
// Behavior preserved byte-for-byte from internal/admin/handle_plans.go:81-138.
func (s *Server) createPlan(_ context.Context, in *CreatePlanInput) (*CreatePlanOutput, error) {
	if s == nil || s.Plans == nil || s.Catalog == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "plan management store is not configured", nil)
	}

	if in.Body.Name == "" || in.Body.Slug == "" {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "name and slug are required", nil)
	}

	if in.Body.PeriodMonths <= 0 {
		in.Body.PeriodMonths = 1
	}

	if err := validateCreditRules(in.Body.CreditRules); err != nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
	}

	rates, err := normalizeRateMapKeys(s.Catalog, in.Body.ModelCreditRates)
	if err != nil {
		var uerr *modelcatalog.UnknownModelsError
		if errors.As(err, &uerr) {
			return nil, contract.NewError(http.StatusBadRequest, "unknown_model", uerr.Error(), map[string]any{"unknown": uerr.Names})
		}
		return nil, contract.NewError(http.StatusInternalServerError, "internal", err.Error(), nil)
	}

	plan := &types.Plan{
		Name:             in.Body.Name,
		Slug:             in.Body.Slug,
		DisplayName:      in.Body.DisplayName,
		Description:      in.Body.Description,
		TierLevel:        in.Body.TierLevel,
		GroupTag:         in.Body.GroupTag,
		PriceCNYFen:      in.Body.PriceCNYFen,
		PriceUSDCents:    in.Body.PriceUSDCents,
		PeriodMonths:     in.Body.PeriodMonths,
		CreditRules:      in.Body.CreditRules,
		ModelCreditRates: rates,
		ClassicRules:     in.Body.ClassicRules,
		IsActive:         true,
	}

	if err := s.Plans.CreatePlan(plan); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to create plan: "+err.Error(), nil)
	}

	return &CreatePlanOutput{Body: DataResponse[types.Plan]{Data: *plan}}, nil
}
