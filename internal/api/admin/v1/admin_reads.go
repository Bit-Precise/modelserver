package adminv1

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// ListAllProjectsInput represents the query parameters for GET /api/v1/admin/projects.
type ListAllProjectsInput struct {
	Page      int    `query:"page" default:"1" minimum:"1"`
	PerPage   int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort      string `query:"sort" default:"created_at"`
	Order     string `query:"order" default:"desc" enum:"asc,desc"`
	ProjectID string `query:"project_id,omitempty" format:"uuid"`
	Owner     string `query:"owner,omitempty" format:"uuid"`
}

func (input *ListAllProjectsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

// ListAllProjectsOutput represents the response envelope for GET /api/v1/admin/projects.
type ListAllProjectsOutput struct {
	Body ListResponse[types.Project]
}

// ListAllRequestsInput represents the query parameters for GET /api/v1/admin/requests.
type ListAllRequestsInput struct {
	Page        int    `query:"page" default:"1" minimum:"1"`
	PerPage     int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort        string `query:"sort" default:"created_at"`
	Order       string `query:"order" default:"desc" enum:"asc,desc"`
	Model       string `query:"model,omitempty"`
	RequestKind string `query:"request_kind,omitempty"`
	Status      string `query:"status,omitempty"`
	CreatedBy   string `query:"created_by,omitempty"`
	Since       string `query:"since,omitempty" doc:"RFC3339 lower bound (inclusive)"`
	Until       string `query:"until,omitempty" doc:"RFC3339 upper bound (inclusive)"`
}

func (input *ListAllRequestsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

// ListAllRequestsOutput represents the response envelope for GET /api/v1/admin/requests.
type ListAllRequestsOutput struct {
	Body ListResponse[types.Request]
}

// registerAdminReadOperations registers the admin global read operations.
func registerAdminReadOperations(api huma.API, server *Server) {
	projectsAccess := authz.System(authz.PermissionSystemProjectsRead)
	requestsAccess := authz.System(authz.PermissionSystemRequestsRead)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "listAllProjects",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/projects",
		Summary:       "List all projects",
		Tags:          []string{"Admin"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        projectsAccess,
		Authorize:     server.authorizationMiddleware,
	}, server.listAllProjects)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "listAllRequests",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/requests",
		Summary:       "List all requests",
		Tags:          []string{"Admin"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        requestsAccess,
		Authorize:     server.authorizationMiddleware,
	}, server.listAllRequests)
}

// listAllProjects handles GET /api/v1/admin/projects with optional filters.
// Returns all projects with pagination and optional project_id/owner filters.
func (s *Server) listAllProjects(_ context.Context, input *ListAllProjectsInput) (*ListAllProjectsOutput, error) {
	if s == nil || s.AdminSuper == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "admin store is not configured", nil)
	}

	pagination := input.pagination()
	filters := store.ProjectListFilters{
		ProjectID: input.ProjectID,
		CreatedBy: input.Owner,
	}

	projects, total, err := s.AdminSuper.ListAllProjects(pagination, filters)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list projects", nil)
	}

	if projects == nil {
		projects = []types.Project{}
	}

	return &ListAllProjectsOutput{Body: ListResponse[types.Project]{
		Data: projects,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

// listAllRequests handles GET /api/v1/admin/requests with optional filters.
// Returns all requests with pagination and optional filters for model, request_kind, status, created_by.
// Silently ignores unparseable since/until RFC3339 timestamps, falling back to zero time.
func (s *Server) listAllRequests(_ context.Context, input *ListAllRequestsInput) (*ListAllRequestsOutput, error) {
	if s == nil || s.AdminSuper == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "admin store is not configured", nil)
	}

	pagination := input.pagination()
	filters := store.RequestFilters{
		Model:       input.Model,
		RequestKind: input.RequestKind,
		Status:      input.Status,
		CreatedBy:   input.CreatedBy,
	}

	// Parse RFC3339 timestamps, silently ignoring errors and falling back to zero time.
	if input.Since != "" {
		if t, err := time.Parse(time.RFC3339, input.Since); err == nil {
			filters.Since = t
		}
	}
	if input.Until != "" {
		if t, err := time.Parse(time.RFC3339, input.Until); err == nil {
			filters.Until = t
		}
	}

	requests, total, err := s.AdminSuper.ListAllRequests(pagination, filters)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list requests", nil)
	}

	if requests == nil {
		requests = []types.Request{}
	}

	return &ListAllRequestsOutput{Body: ListResponse[types.Request]{
		Data: requests,
		Meta: paginationMeta(total, pagination),
	}}, nil
}
