package adminv1

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/types"
)

const projectIDPathParam = "projectID"

type emptyInput struct{}

type authConfigOutput struct {
	Body AuthConfig
}

type currentUserOutput struct {
	Body DataResponse[User]
}

type globalCapabilitiesOutput struct {
	Body DataResponse[GlobalCapabilities]
}

type listProjectsInput struct {
	Page    int    `query:"page" default:"1" minimum:"1" doc:"Page number, starting at one."`
	PerPage int    `query:"per_page" default:"20" minimum:"1" maximum:"100" doc:"Number of projects returned per page."`
	Sort    string `query:"sort" default:"created_at" doc:"Project field used for ordering."`
	Order   string `query:"order" default:"desc" enum:"asc,desc" doc:"Sort direction."`
}

func (input *listProjectsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

type listProjectsOutput struct {
	Body ListResponse[Project]
}

type projectInput struct {
	ProjectID string `path:"projectID" format:"uuid" doc:"Project identifier."`
}

type projectOutput struct {
	Body DataResponse[Project]
}

type projectCapabilitiesOutput struct {
	Body DataResponse[ProjectCapabilities]
}

// Register installs the typed operations which have been migrated to the v1
// management contract. The same function is used by the running server and by
// the offline OpenAPI generator, preventing the generated document from
// drifting away from runtime route registration.
func Register(api huma.API, server *Server) {
	if server == nil {
		server = &Server{}
	}

	contract.Register(api, contract.Operation{
		ID:            "getAuthConfig",
		Method:        http.MethodGet,
		Path:          "/api/v1/auth/config",
		Summary:       "Get login configuration",
		Description:   "Returns the public OAuth provider and login-page configuration.",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusOK,
		Access:        authz.Public(),
	}, server.getAuthConfig)

	contract.Register(api, contract.Operation{
		ID:            "getCurrentUser",
		Method:        http.MethodGet,
		Path:          "/api/v1/me",
		Summary:       "Get current user",
		Tags:          []string{"Current user"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden},
		Access:        authz.Authenticated(),
		Authorize:     server.authorizationMiddleware,
	}, server.getCurrentUser)

	contract.Register(api, contract.Operation{
		ID:            "getCurrentUserCapabilities",
		Method:        http.MethodGet,
		Path:          "/api/v1/me/capabilities",
		Summary:       "Get current user's system capabilities",
		Description:   "Returns stable system-scoped permission identifiers granted to the current user.",
		Tags:          []string{"Current user", "Authorization"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden},
		Access:        authz.Authenticated(),
		Authorize:     server.authorizationMiddleware,
	}, server.getCurrentUserCapabilities)

	listProjectsOperation := contract.Operation{
		ID:            "listProjects",
		Method:        http.MethodGet,
		Path:          "/api/v1/projects",
		Summary:       "List projects",
		Description:   "Lists non-archived projects visible to the current user.",
		Tags:          []string{"Projects"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden},
		Access:        authz.Authenticated(),
		Authorize:     server.authorizationMiddleware,
	}
	registerWithLegacyTrailingSlash(api, listProjectsOperation, server.listProjects)

	projectRead := authz.Project(authz.PermissionProjectRead, projectIDPathParam)
	getProjectOperation := contract.Operation{
		ID:            "getProject",
		Method:        http.MethodGet,
		Path:          "/api/v1/projects/{projectID}",
		Summary:       "Get project",
		Tags:          []string{"Projects"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Access:        projectRead,
		Authorize:     server.authorizationMiddleware,
	}
	registerWithLegacyTrailingSlash(api, getProjectOperation, server.getProject)

	contract.Register(api, contract.Operation{
		ID:            "getProjectCapabilities",
		Method:        http.MethodGet,
		Path:          "/api/v1/projects/{projectID}/capabilities",
		Summary:       "Get project capabilities",
		Description:   "Returns the current user's role and stable permission identifiers for a project.",
		Tags:          []string{"Projects", "Authorization"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound},
		Access:        projectRead,
		Authorize:     server.authorizationMiddleware,
	}, server.getProjectCapabilities)

	registerUserReadOperations(api, server)
	registerPlanReadOperations(api, server)
}

// registerWithLegacyTrailingSlash keeps the two routes which historically
// lived at chi sub-router roots compatible with both spellings. The alias is
// registered through the same contract path so it enforces the exact same
// AccessPolicy. Hidden keeps generated clients on one canonical URL without
// relying on document post-processing.
func registerWithLegacyTrailingSlash[I, O any](
	api huma.API,
	operation contract.Operation,
	handler func(context.Context, *I) (*O, error),
) {
	contract.Register(api, operation, handler)

	alias := operation
	alias.ID += "LegacyTrailingSlash"
	alias.Path += "/"
	alias.Hidden = true
	contract.Register(api, alias, handler)
}

func (s *Server) getAuthConfig(context.Context, *emptyInput) (*authConfigOutput, error) {
	if s == nil || s.Config == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "management API configuration is not available", nil)
	}

	providers := make([]string, 0, 3)
	if s.Config.Auth.OAuth.GitHub.ClientID != "" {
		providers = append(providers, "github")
	}
	if s.Config.Auth.OAuth.Google.ClientID != "" {
		providers = append(providers, "google")
	}
	if s.Config.Auth.OAuth.OIDC.IssuerURL != "" {
		providers = append(providers, "oidc")
	}

	labels := map[string]string{}
	if displayName := s.Config.Auth.OAuth.OIDC.DisplayName; displayName != "" {
		labels["oidc"] = displayName
	}

	return &authConfigOutput{Body: AuthConfig{
		OAuthProviders:   providers,
		LoginDescription: s.Config.Auth.LoginDescription,
		OAuthLabels:      labels,
		LoginFooterHTML:  s.Config.Auth.LoginFooterHTML,
		GitHubURL:        s.Config.Auth.GitHubURL,
	}}, nil
}

func (s *Server) getCurrentUser(ctx context.Context, _ *emptyInput) (*currentUserOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok || authorization.User == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	return &currentUserOutput{Body: DataResponse[User]{Data: userDTO(authorization.User)}}, nil
}

func (s *Server) getCurrentUserCapabilities(ctx context.Context, _ *emptyInput) (*globalCapabilitiesOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	return &globalCapabilitiesOutput{Body: DataResponse[GlobalCapabilities]{Data: GlobalCapabilities{
		IsSuperadmin: authorization.Principal.Superadmin,
		Permissions:  permissionsDTO(authz.SystemCapabilities(authorization.Principal)),
	}}}, nil
}

func (s *Server) listProjects(ctx context.Context, input *listProjectsInput) (*listProjectsOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}

	pagination := input.pagination()
	projects, total, err := s.Store.ListUserProjects(authorization.Principal.UserID, pagination)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list projects", nil)
	}
	items := make([]Project, len(projects))
	for i := range projects {
		items[i] = projectDTO(&projects[i])
	}

	return &listProjectsOutput{Body: ListResponse[Project]{
		Data: items,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

func (s *Server) getProject(_ context.Context, input *projectInput) (*projectOutput, error) {
	project, err := s.Store.GetProjectByID(input.ProjectID)
	if err != nil || project == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "project not found", nil)
	}
	return &projectOutput{Body: DataResponse[Project]{Data: projectDTO(project)}}, nil
}

func (s *Server) getProjectCapabilities(ctx context.Context, input *projectInput) (*projectCapabilitiesOutput, error) {
	authorization, ok := authorizationFromContext(ctx)
	if !ok {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "request authorization context is missing", nil)
	}
	project, err := s.Store.GetProjectByID(input.ProjectID)
	if err != nil || project == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "project not found", nil)
	}
	capabilities := authz.ProjectCapabilities(authorization.Principal, authorization.Role)
	return &projectCapabilitiesOutput{Body: DataResponse[ProjectCapabilities]{Data: ProjectCapabilities{
		Role:        ProjectRole(authorization.Role),
		Permissions: permissionsDTO(capabilities),
	}}}, nil
}
