package adminv1

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// ModelListRow wraps types.Model with per-row reference counts, matching the
// legacy modelListResponseRow. Embedded types.Model preserves the same JSON
// field ordering as the legacy handler.
type ModelListRow struct {
	types.Model
	ReferenceCounts store.ModelReferenceCounts `json:"reference_counts"`
}

// ListModelsInput is the request for GET /api/v1/models with optional status filter.
type ListModelsInput struct {
	Status string `query:"status,omitempty" doc:"Optional status filter (active|disabled)."`
}

type ListModelsOutput struct {
	Body DataResponse[[]ModelListRow]
}

type GetModelInput struct {
	Name string `path:"name" doc:"Canonical model name (lowercase; letters/digits/dot/underscore/dash)."`
}

type GetModelOutput struct {
	Body DataResponse[types.Model]
}

type CreateModelInput struct {
	Body struct {
		Name                   string                 `json:"name" minLength:"1"`
		DisplayName            string                 `json:"display_name,omitempty"`
		Description            string                 `json:"description,omitempty"`
		Aliases                []string               `json:"aliases,omitempty"`
		DefaultCreditRate      *types.CreditRate      `json:"default_credit_rate,omitempty"`
		DefaultImageCreditRate *types.ImageCreditRate `json:"default_image_credit_rate,omitempty"`
		Status                 string                 `json:"status,omitempty" enum:"active,disabled"`
		Publisher              string                 `json:"publisher"`
		Metadata               types.ModelMetadata    `json:"metadata,omitempty"`
	}
}

type CreateModelOutput struct {
	Body DataResponse[types.Model]
}

// UpdateModelInput uses pointer-field partial-update semantics. `Name` is
// declared here (with a pointer) so a body containing `"name": ...` is
// rejected in the handler with the legacy "canonical name is immutable"
// message — matching the legacy `if _, ok := body["name"]; ok` check.
// Note: this preserves parity for `"name": "x"` but not `"name": null`
// (JSON null → nil pointer, indistinguishable from omission). Dashboard
// does not send `"name"` in update payloads; this is documented in the
// batch's PR body as an accepted deviation.
type UpdateModelInput struct {
	Name string `path:"name" doc:"Canonical model name."`
	Body struct {
		Name                   *string         `json:"name,omitempty"`
		DisplayName            *string         `json:"display_name,omitempty"`
		Description            *string         `json:"description,omitempty"`
		Status                 *string         `json:"status,omitempty" enum:"active,disabled"`
		Aliases                *[]string       `json:"aliases,omitempty"`
		DefaultCreditRate      json.RawMessage `json:"default_credit_rate,omitempty"`
		DefaultImageCreditRate json.RawMessage `json:"default_image_credit_rate,omitempty"`
		Publisher              *string         `json:"publisher,omitempty"`
		Metadata               json.RawMessage `json:"metadata,omitempty"`
	}
}

type UpdateModelOutput struct {
	Body DataResponse[types.Model]
}

type DeleteModelInput struct {
	Name string `path:"name" doc:"Canonical model name."`
}

type DeleteModelOutput struct{}

// listModels handles GET /api/v1/models with optional status filter.
// Behavior:
// - If input.Status != "": call s.Models.ListModelsByStatus(input.Status)
// - Else: call s.Models.ListModels()
// - Error → 500 internal "failed to list models"
// - For each model, call s.Models.ModelReferenceCountsFor(m.Name).
//   Error → 500 internal "failed to count references: "+err.Error()
// - Return {data: [ModelListRow{Model: m, ReferenceCounts: counts}]}
// - Even if models slice is empty, emit {data: []} (not {data: null})
func (s *Server) listModels(ctx context.Context, input *ListModelsInput) (*ListModelsOutput, error) {
	if s == nil || s.Models == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "model management store is not configured", nil)
	}

	var (
		models []types.Model
		err    error
	)
	if input.Status != "" {
		models, err = s.Models.ListModelsByStatus(input.Status)
	} else {
		models, err = s.Models.ListModels()
	}
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list models", nil)
	}

	rows := make([]ModelListRow, 0, len(models))
	for _, m := range models {
		counts, err := s.Models.ModelReferenceCountsFor(m.Name)
		if err != nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to count references: "+err.Error(), nil)
		}
		rows = append(rows, ModelListRow{Model: m, ReferenceCounts: counts})
	}

	return &ListModelsOutput{
		Body: DataResponse[[]ModelListRow]{
			Data: rows,
		},
	}, nil
}

// getModel handles GET /api/v1/models/{name}.
// Behavior:
// - s.Models.GetModelByName(input.Name)
// - Store error → 500 internal "failed to get model"
// - Nil model → 404 not_found "model not found"
// - Happy path → 200 with {data: model}
func (s *Server) getModel(ctx context.Context, input *GetModelInput) (*GetModelOutput, error) {
	if s == nil || s.Models == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "model management store is not configured", nil)
	}

	m, err := s.Models.GetModelByName(input.Name)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to get model", nil)
	}
	if m == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "model not found", nil)
	}

	return &GetModelOutput{
		Body: DataResponse[types.Model]{
			Data: *m,
		},
	}, nil
}
