package adminv1

import (
	"encoding/json"

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
