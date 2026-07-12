package adminv1

import (
	"context"
	"encoding/json"
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

// UpdatePlanInput is the request for PUT /api/v1/plans/{planID}.
// Scalar fields use typed pointers so the zero value (nil) signals absence.
// Complex JSON columns use json.RawMessage to preserve arbitrary shapes while
// still allowing the handler to inspect and validate them before storage.
type UpdatePlanInput struct {
	PlanID string `path:"planID" doc:"Plan identifier."`
	Body   struct {
		Name          *string `json:"name,omitempty"`
		Slug          *string `json:"slug,omitempty"`
		DisplayName   *string `json:"display_name,omitempty"`
		Description   *string `json:"description,omitempty"`
		TierLevel     *int    `json:"tier_level,omitempty"`
		GroupTag      *string `json:"group_tag,omitempty"`
		PriceCNYFen   *int64  `json:"price_cny_fen,omitempty"`
		PriceUSDCents *int64  `json:"price_usd_cents,omitempty"`
		PeriodMonths  *int    `json:"period_months,omitempty"`
		IsActive      *bool   `json:"is_active,omitempty"`

		// Complex fields — raw JSON so arbitrary shapes round-trip unchanged.
		CreditRules            json.RawMessage            `json:"credit_rules,omitempty"`
		ClassicRules           json.RawMessage            `json:"classic_rules,omitempty"`
		ModelCreditRates       map[string]json.RawMessage `json:"model_credit_rates,omitempty"`
		ClientModelCreditRates map[string]json.RawMessage `json:"client_model_credit_rates,omitempty"`
	}
}

// UpdatePlanOutput wraps a types.Plan in the standard data envelope.
type UpdatePlanOutput struct {
	Body DataResponse[types.Plan]
}

// updatePlan implements PUT /api/v1/plans/{planID}.
// Behavior preserved byte-for-byte from internal/admin/handle_plans.go:152-238.
func (s *Server) updatePlan(_ context.Context, in *UpdatePlanInput) (*UpdatePlanOutput, error) {
	if s == nil || s.Plans == nil || s.Catalog == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "plan management store is not configured", nil)
	}

	updates := make(map[string]any)

	// Scalar fields: include in updates map only when pointer is non-nil.
	if in.Body.Name != nil {
		updates["name"] = *in.Body.Name
	}
	if in.Body.Slug != nil {
		updates["slug"] = *in.Body.Slug
	}
	if in.Body.DisplayName != nil {
		updates["display_name"] = *in.Body.DisplayName
	}
	if in.Body.Description != nil {
		updates["description"] = *in.Body.Description
	}
	if in.Body.TierLevel != nil {
		updates["tier_level"] = *in.Body.TierLevel
	}
	if in.Body.GroupTag != nil {
		updates["group_tag"] = *in.Body.GroupTag
	}
	if in.Body.PriceCNYFen != nil {
		updates["price_cny_fen"] = *in.Body.PriceCNYFen
	}
	if in.Body.PriceUSDCents != nil {
		updates["price_usd_cents"] = *in.Body.PriceUSDCents
	}
	if in.Body.PeriodMonths != nil {
		updates["period_months"] = *in.Body.PeriodMonths
	}
	if in.Body.IsActive != nil {
		updates["is_active"] = *in.Body.IsActive
	}

	// model_credit_rates: normalize model-name keys, then marshal to []byte.
	if in.Body.ModelCreditRates != nil {
		normalized, err := normalizeRateMapKeysRaw(s.Catalog, in.Body.ModelCreditRates)
		if err != nil {
			var uerr *modelcatalog.UnknownModelsError
			if errors.As(err, &uerr) {
				return nil, contract.NewError(http.StatusBadRequest, "unknown_model", uerr.Error(), map[string]any{"unknown": uerr.Names})
			}
			return nil, contract.NewError(http.StatusInternalServerError, "internal", err.Error(), nil)
		}
		b, _ := json.Marshal(normalized)
		updates["model_credit_rates"] = b
	}

	// client_model_credit_rates: validate every bucket key, then marshal to []byte.
	if in.Body.ClientModelCreditRates != nil {
		for bucket := range in.Body.ClientModelCreditRates {
			if !types.IsValidClientBucket(bucket) {
				return nil, contract.NewError(http.StatusBadRequest, "bad_request", "invalid client bucket in client_model_credit_rates: "+bucket, nil)
			}
		}
		b, _ := json.Marshal(in.Body.ClientModelCreditRates)
		updates["client_model_credit_rates"] = b
	}

	// credit_rules: marshal then validate, store as []byte.
	if len(in.Body.CreditRules) > 0 {
		var rules []types.CreditRule
		if err := json.Unmarshal(in.Body.CreditRules, &rules); err == nil {
			if err := validateCreditRules(rules); err != nil {
				return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
			}
		}
		b, _ := json.Marshal(json.RawMessage(in.Body.CreditRules))
		updates["credit_rules"] = b
	}

	// classic_rules: marshal to []byte for storage.
	if len(in.Body.ClassicRules) > 0 {
		b, _ := json.Marshal(json.RawMessage(in.Body.ClassicRules))
		updates["classic_rules"] = b
	}

	if len(updates) == 0 {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "no valid fields to update", nil)
	}

	if err := s.Plans.UpdatePlan(in.PlanID, updates); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update plan", nil)
	}

	plan, err := s.Plans.GetPlanByID(in.PlanID)
	if err != nil || plan == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to retrieve updated plan", nil)
	}

	return &UpdatePlanOutput{Body: DataResponse[types.Plan]{Data: *plan}}, nil
}

// DeletePlanInput is the request for DELETE /api/v1/plans/{planID}.
type DeletePlanInput struct {
	PlanID string `path:"planID" doc:"Plan identifier."`
}

// DeletePlanOutput is an empty response struct for 204 No Content.
type DeletePlanOutput struct{}

// deletePlan implements DELETE /api/v1/plans/{planID}.
// Behavior preserved byte-for-byte from internal/admin/handle_plans.go:240-249.
// No existence check before delete; 204 on success whether plan existed or not.
func (s *Server) deletePlan(_ context.Context, in *DeletePlanInput) (*DeletePlanOutput, error) {
	if s == nil || s.Plans == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "plan management store is not configured", nil)
	}

	if err := s.Plans.DeletePlan(in.PlanID); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to delete plan", nil)
	}

	return &DeletePlanOutput{}, nil
}
