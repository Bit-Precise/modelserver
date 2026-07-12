package adminv1

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

func registerModelOperations(api huma.API, server *Server) {
	read := authz.System(authz.PermissionSystemModelsRead)
	write := authz.System(authz.PermissionSystemModelsManage)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "listModels",
		Method:        http.MethodGet,
		Path:          "/api/v1/models",
		Summary:       "List models",
		Tags:          []string{"Models"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        read,
		Authorize:     server.authorizationMiddleware,
	}, server.listModels)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "getModel",
		Method:        http.MethodGet,
		Path:          "/api/v1/models/{name}",
		Summary:       "Get model",
		Tags:          []string{"Models"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        read,
		Authorize:     server.authorizationMiddleware,
	}, server.getModel)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "createModel",
		Method:        http.MethodPost,
		Path:          "/api/v1/models",
		Summary:       "Create model",
		Tags:          []string{"Models"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError},
		Access:        write,
		Authorize:     server.authorizationMiddleware,
	}, server.createModel)

	// Legacy exposes both PATCH and PUT on the same handler. Register both —
	// Huma treats them as distinct operations but they share the handler.
	for _, method := range []string{http.MethodPatch, http.MethodPut} {
		opID := "updateModel"
		if method == http.MethodPut {
			opID = "updateModelPut"
		}
		contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
			ID:            opID,
			Method:        method,
			Path:          "/api/v1/models/{name}",
			Summary:       "Update model",
			Tags:          []string{"Models"},
			DefaultStatus: http.StatusOK,
			Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
			Access:        write,
			Authorize:     server.authorizationMiddleware,
		}, server.updateModel)
	}

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "deleteModel",
		Method:        http.MethodDelete,
		Path:          "/api/v1/models/{name}",
		Summary:       "Delete model",
		Tags:          []string{"Models"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
		Access:        write,
		Authorize:     server.authorizationMiddleware,
	}, server.deleteModel)
}

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

// createModel handles POST /api/v1/models.
// Follows legacy handleCreateModel exactly:
// - validateModelPayload (name, aliases, status, rates) → 400 bad_request on error
// - validatePublisher → 400 bad_request on error
// - Default DisplayName = Name if empty
// - Construct types.Model and call s.Models.CreateModel
//   - unique violation → 409 conflict; other error → 400 bad_request
// - refreshCatalog after success
// - Return 201 with {data: model}
func (s *Server) createModel(ctx context.Context, input *CreateModelInput) (*CreateModelOutput, error) {
	if s == nil || s.Models == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "model management store is not configured", nil)
	}

	body := &input.Body
	if err := validateModelPayload(body.Name, body.Aliases, body.Status, body.DefaultCreditRate, body.DefaultImageCreditRate); err != nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
	}
	if err := validatePublisher(body.Publisher); err != nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
	}
	if body.DisplayName == "" {
		body.DisplayName = body.Name
	}

	m := &types.Model{
		Name:                   body.Name,
		DisplayName:            body.DisplayName,
		Description:            body.Description,
		Aliases:                body.Aliases,
		DefaultCreditRate:      body.DefaultCreditRate,
		DefaultImageCreditRate: body.DefaultImageCreditRate,
		Status:                 body.Status,
		Publisher:              body.Publisher,
		Metadata:               body.Metadata,
	}
	if err := s.Models.CreateModel(m); err != nil {
		if isUniqueViolation(err) {
			return nil, contract.NewError(http.StatusConflict, "conflict", err.Error(), nil)
		}
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
	}

	s.refreshCatalog(ctx)

	return &CreateModelOutput{
		Body: DataResponse[types.Model]{
			Data: *m,
		},
	}, nil
}

// refreshCatalog reloads the in-memory model catalog after a successful
// model write. Matches the legacy handler's log-and-continue behavior:
// on error, the in-memory view stays as-is until the periodic reload
// tick recovers.
func (s *Server) refreshCatalog(ctx context.Context) {
	if s == nil || s.Models == nil || s.Catalog == nil {
		return
	}
	models, err := s.Models.ListModels()
	if err != nil {
		slog.Default().ErrorContext(ctx, "admin: failed to refresh model catalog after write", "error", err)
		return
	}
	s.Catalog.Swap(models)
}

// updateModel handles PATCH /api/v1/models/{name} (and PUT, registered in Task 7).
// Preserves handleUpdateModel wire behavior exactly:
//  1. Fetch existing via GetModelByName — 404 if nil OR error (legacy conflates both).
//  2. Reject if Body.Name is non-nil → 400 "canonical name is immutable".
//  3. Propagate scalar pointer fields (DisplayName, Description, Status, Publisher, Aliases)
//     when non-nil; validate Status and Aliases values.
//  4. Handle default_credit_rate and default_image_credit_rate as json.RawMessage:
//     nil/empty → skip; []byte("null") → clear (store nil); object → validate then store []byte.
//  5. Propagate Metadata (RawMessage) when non-empty; no validation.
//  6. Empty updates map → 400 "no valid fields to update".
//  7. UpdateModel error → 400 bad_request (legacy behavior — not 500).
//  8. refreshCatalog, then refetch via GetModelByName (silently ignore refetch error).
func (s *Server) updateModel(ctx context.Context, input *UpdateModelInput) (*UpdateModelOutput, error) {
	if s == nil || s.Models == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "model management store is not configured", nil)
	}

	existing, err := s.Models.GetModelByName(input.Name)
	if err != nil || existing == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "model not found", nil)
	}

	if input.Body.Name != nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "canonical name is immutable; create a new model and retire this one instead", nil)
	}

	updates := make(map[string]any)

	if input.Body.DisplayName != nil {
		updates["display_name"] = *input.Body.DisplayName
	}
	if input.Body.Description != nil {
		updates["description"] = *input.Body.Description
	}
	if input.Body.Status != nil {
		status := *input.Body.Status
		if status != types.ModelStatusActive && status != types.ModelStatusDisabled {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request", "status must be active or disabled", nil)
		}
		updates["status"] = status
	}
	if input.Body.Aliases != nil {
		aliases := *input.Body.Aliases
		if err := validateAliases(existing.Name, aliases); err != nil {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
		}
		updates["aliases"] = aliases
	}
	if len(input.Body.DefaultCreditRate) > 0 {
		if bytes.Equal(input.Body.DefaultCreditRate, []byte("null")) {
			updates["default_credit_rate"] = nil
		} else {
			var rate types.CreditRate
			if err := json.Unmarshal(input.Body.DefaultCreditRate, &rate); err != nil {
				return nil, contract.NewError(http.StatusBadRequest, "bad_request", "invalid default_credit_rate", nil)
			}
			if err := validateCreditRate(&rate); err != nil {
				return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
			}
			updates["default_credit_rate"] = []byte(input.Body.DefaultCreditRate)
		}
	}
	if len(input.Body.DefaultImageCreditRate) > 0 {
		if bytes.Equal(input.Body.DefaultImageCreditRate, []byte("null")) {
			updates["default_image_credit_rate"] = nil
		} else {
			var rate types.ImageCreditRate
			if err := json.Unmarshal(input.Body.DefaultImageCreditRate, &rate); err != nil {
				return nil, contract.NewError(http.StatusBadRequest, "bad_request", "invalid default_image_credit_rate", nil)
			}
			if err := validateImageCreditRate(&rate); err != nil {
				return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
			}
			updates["default_image_credit_rate"] = []byte(input.Body.DefaultImageCreditRate)
		}
	}
	if input.Body.Publisher != nil {
		pub := *input.Body.Publisher
		if err := validatePublisher(pub); err != nil {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
		}
		updates["publisher"] = pub
	}
	if len(input.Body.Metadata) > 0 {
		updates["metadata"] = []byte(input.Body.Metadata)
	}

	if len(updates) == 0 {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "no valid fields to update", nil)
	}

	if err := s.Models.UpdateModel(input.Name, updates); err != nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
	}

	s.refreshCatalog(ctx)

	updated, _ := s.Models.GetModelByName(input.Name)
	if updated == nil {
		return &UpdateModelOutput{Body: DataResponse[types.Model]{}}, nil
	}
	return &UpdateModelOutput{Body: DataResponse[types.Model]{Data: *updated}}, nil
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

// deleteModel handles DELETE /api/v1/models/{name}.
// Preserves handleDeleteModel wire behavior exactly:
//  1. Fetch existing via GetModelByName — 404 if nil OR error (legacy conflates both).
//  2. s.Models.ModelReferenceCountsFor(name) — 500 internal "failed to count references" on error.
//  3. If counts.Total() > 0 → 409 conflict "model is referenced; set status=disabled or clear references first" with counts as details.
//  4. s.Models.DeleteModel(name) — 400 bad_request on error (not 500 — preserve legacy 400).
//  5. refreshCatalog(ctx).
//  6. Return (&DeleteModelOutput{}, nil).
func (s *Server) deleteModel(ctx context.Context, input *DeleteModelInput) (*DeleteModelOutput, error) {
	if s == nil || s.Models == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "model management store is not configured", nil)
	}

	existing, err := s.Models.GetModelByName(input.Name)
	if err != nil || existing == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "model not found", nil)
	}

	counts, err := s.Models.ModelReferenceCountsFor(input.Name)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to count references", nil)
	}
	if counts.Total() > 0 {
		return nil, contract.NewError(http.StatusConflict, "conflict",
			"model is referenced; set status=disabled or clear references first", counts)
	}

	if err := s.Models.DeleteModel(input.Name); err != nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", err.Error(), nil)
	}

	s.refreshCatalog(ctx)

	return &DeleteModelOutput{}, nil
}
