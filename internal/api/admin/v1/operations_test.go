package adminv1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/config"
	storepkg "github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

const (
	testUserID    = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	testTargetID  = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	testProjectID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
)

type fakeManagementStore struct {
	user           *types.User
	member         *types.ProjectMember
	projects       []types.Project
	project        *types.Project
	users          []types.User
	compactUsers   []storepkg.CompactUser
	usersByID      map[string]*types.User
	plans          []types.Plan
	plan           *types.Plan
	lastPagination types.PaginationParams
}

func (store *fakeManagementStore) GetUserByID(id string) (*types.User, error) {
	if id == testUserID {
		return store.user, nil
	}
	if store.usersByID != nil {
		return store.usersByID[id], nil
	}
	return store.user, nil
}

func (store *fakeManagementStore) GetProjectMember(string, string) (*types.ProjectMember, error) {
	return store.member, nil
}

func (store *fakeManagementStore) ListUserProjects(_ string, pagination types.PaginationParams) ([]types.Project, int, error) {
	store.lastPagination = pagination
	return store.projects, len(store.projects), nil
}

func (store *fakeManagementStore) GetProjectByID(string) (*types.Project, error) {
	return store.project, nil
}

func (store *fakeManagementStore) ListUsers(pagination types.PaginationParams) ([]types.User, int, error) {
	store.lastPagination = pagination
	return store.users, len(store.users), nil
}

func (store *fakeManagementStore) ListAllUsersCompact() ([]storepkg.CompactUser, error) {
	return store.compactUsers, nil
}

func (store *fakeManagementStore) ListPlansPaginated(pagination types.PaginationParams) ([]types.Plan, int, error) {
	store.lastPagination = pagination
	return store.plans, len(store.plans), nil
}

func (store *fakeManagementStore) GetPlanByID(id string) (*types.Plan, error) {
	if store.plan != nil && store.plan.ID == id {
		return store.plan, nil
	}
	return nil, nil
}

func (store *fakeManagementStore) CreatePlan(*types.Plan) error {
	return nil
}

func (store *fakeManagementStore) UpdatePlan(id string, updates map[string]any) error {
	return nil
}

func (store *fakeManagementStore) DeletePlan(id string) error {
	return nil
}

type fakeTokenValidator struct {
	claims *auth.Claims
}

func (validator fakeTokenValidator) ValidateToken(string) (*auth.Claims, error) {
	return validator.claims, nil
}

func activeUser(superadmin bool) *types.User {
	now := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	return &types.User{
		ID:           testUserID,
		Email:        "user@example.com",
		Nickname:     "User",
		IsSuperadmin: superadmin,
		MaxProjects:  10,
		Status:       types.UserStatusActive,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
}

func testServer(store *fakeManagementStore) *Server {
	return &Server{
		Store: store,
		Users: store,
		Plans: store,
		Tokens: fakeTokenValidator{claims: &auth.Claims{
			UserID:    testUserID,
			TokenType: "access",
		}},
	}
}

func testRouter(server *Server) *chi.Mux {
	router := chi.NewRouter()
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	Register(api, server)
	return router
}

func authenticatedRequest(method, target string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.Header.Set("Authorization", "Bearer test-token")
	return request
}

func TestRegisterDocumentsSecurityAndAuthorizationFromOnePolicy(t *testing.T) {
	router := chi.NewRouter()
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	Register(api, nil)

	expectedGetPaths := []string{
		"/api/v1/auth/config",
		"/api/v1/auth/oauth/{provider}/redirect",
		"/api/v1/me",
		"/api/v1/me/capabilities",
		"/api/v1/projects",
		"/api/v1/projects/{projectID}",
		"/api/v1/projects/{projectID}/capabilities",
		"/api/v1/users",
		"/api/v1/users/compact",
		"/api/v1/users/{userID}",
		"/api/v1/plans",
		"/api/v1/plans/{planID}",
	}
	expectedPostPaths := []string{
		"/api/v1/auth/refresh",
		"/api/v1/auth/oauth/{provider}",
	}
	wantPathCount := len(expectedGetPaths) + len(expectedPostPaths)
	if len(api.OpenAPI().Paths) != wantPathCount {
		t.Fatalf("documented path count = %d, want %d", len(api.OpenAPI().Paths), wantPathCount)
	}
	for _, path := range expectedGetPaths {
		if api.OpenAPI().Paths[path] == nil || api.OpenAPI().Paths[path].Get == nil {
			t.Errorf("missing documented GET %s", path)
		}
	}
	for _, path := range expectedPostPaths {
		if api.OpenAPI().Paths[path] == nil || api.OpenAPI().Paths[path].Post == nil {
			t.Errorf("missing documented POST %s", path)
		}
	}

	publicOperation := api.OpenAPI().Paths["/api/v1/auth/config"].Get
	if publicOperation.Security == nil || len(publicOperation.Security) != 0 {
		t.Fatalf("public security = %#v, want explicit empty array", publicOperation.Security)
	}
	publicPolicy, ok := publicOperation.Extensions["x-modelserver-authz"].(authz.AccessPolicy)
	if !ok || !reflect.DeepEqual(publicPolicy, authz.Public()) {
		t.Fatalf("public authorization extension = %#v", publicOperation.Extensions["x-modelserver-authz"])
	}

	projectOperation := api.OpenAPI().Paths["/api/v1/projects/{projectID}"].Get
	if !reflect.DeepEqual(projectOperation.Security, []map[string][]string{{contract.AdminJWTSecurityScheme: {}}}) {
		t.Fatalf("project security = %#v", projectOperation.Security)
	}
	projectPolicy, ok := projectOperation.Extensions["x-modelserver-authz"].(authz.AccessPolicy)
	if !ok {
		t.Fatalf("project authorization extension type = %T", projectOperation.Extensions["x-modelserver-authz"])
	}
	if projectPolicy.Scope != authz.ScopeProject || projectPolicy.Permission != authz.PermissionProjectRead || projectPolicy.Superadmin != authz.SuperadminBypass {
		t.Fatalf("project authorization extension = %#v", projectPolicy)
	}

	for path, permission := range map[string]authz.Permission{
		"/api/v1/users": authz.PermissionSystemUsersRead,
		"/api/v1/plans": authz.PermissionSystemPlansRead,
	} {
		policy, ok := api.OpenAPI().Paths[path].Get.Extensions["x-modelserver-authz"].(authz.AccessPolicy)
		if !ok || policy.Scope != authz.ScopeSystem || policy.Permission != permission || policy.Superadmin != authz.SuperadminRequired {
			t.Errorf("%s authorization extension = %#v", path, policy)
		}
	}

	for path := range api.OpenAPI().Paths {
		if path == "/v1" || len(path) >= 4 && path[:4] == "/v1/" || len(path) >= 8 && path[:8] == "/v1beta/" {
			t.Errorf("proxy path leaked into management contract: %s", path)
		}
	}
}

func TestAuthConfigPreservesPublicWireShape(t *testing.T) {
	server := &Server{Config: &config.Config{}}
	server.Config.Auth.OAuth.GitHub.ClientID = "github-client"
	server.Config.Auth.OAuth.OIDC.IssuerURL = "https://issuer.example.com"
	server.Config.Auth.OAuth.OIDC.DisplayName = "Company SSO"
	server.Config.Auth.LoginDescription = "Sign in"

	recorder := httptest.NewRecorder()
	testRouter(server).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, wrapped := body["data"]; wrapped {
		t.Fatalf("auth config unexpectedly uses data envelope: %s", recorder.Body.String())
	}
	if !reflect.DeepEqual(body["oauth_providers"], []any{"github", "oidc"}) {
		t.Fatalf("oauth providers = %#v", body["oauth_providers"])
	}
}

func TestProtectedOperationUsesExistingErrorEnvelope(t *testing.T) {
	store := &fakeManagementStore{user: activeUser(false)}
	recorder := httptest.NewRecorder()
	testRouter(testServer(store)).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/me", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if got, want := recorder.Body.String(), "{\"error\":{\"code\":\"unauthorized\",\"message\":\"missing or invalid authorization header\"}}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestProtectedOperationRejectsEmptyValidatorResult(t *testing.T) {
	store := &fakeManagementStore{user: activeUser(false)}
	server := &Server{Store: store, Tokens: fakeTokenValidator{}}
	recorder := httptest.NewRecorder()
	testRouter(server).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/me"))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestProtectedOperationPreservesRefreshTokenError(t *testing.T) {
	store := &fakeManagementStore{user: activeUser(false)}
	server := &Server{
		Store:  store,
		Tokens: fakeTokenValidator{claims: &auth.Claims{UserID: testUserID, TokenType: "refresh"}},
	}
	recorder := httptest.NewRecorder()
	testRouter(server).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/me"))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", recorder.Code, recorder.Body.String())
	}
	if got, want := recorder.Body.String(), "{\"error\":{\"code\":\"unauthorized\",\"message\":\"expected access token\"}}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestCurrentUserPreservesLegacyWireResponse(t *testing.T) {
	user := activeUser(false)
	user.Picture = "https://images.example.com/user.png"
	store := &fakeManagementStore{user: user}
	recorder := httptest.NewRecorder()
	testRouter(testServer(store)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/me"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	legacy, err := json.Marshal(types.DataResponse[types.User]{Data: *user})
	if err != nil {
		t.Fatalf("marshal legacy response: %v", err)
	}
	legacy = append(legacy, '\n')
	if got, want := recorder.Body.String(), string(legacy); got != want {
		t.Fatalf("typed wire response = %s, want legacy response %s", got, want)
	}
}

func TestListProjectsUsesTypedPaginationDefaults(t *testing.T) {
	store := &fakeManagementStore{
		user: activeUser(false),
		projects: []types.Project{{
			ID:        testProjectID,
			Name:      "Example",
			CreatedBy: testUserID,
			Status:    types.ProjectStatusActive,
		}},
	}
	recorder := httptest.NewRecorder()
	testRouter(testServer(store)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/projects"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if want := types.DefaultPagination(); store.lastPagination != want {
		t.Fatalf("pagination = %#v, want %#v", store.lastPagination, want)
	}
	var body ListResponse[Project]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 1 || body.Meta.Total != 1 || body.Meta.TotalPages != 1 {
		t.Fatalf("response = %#v", body)
	}
}

func TestLegacyProjectTrailingSlashAliasesPreserveRoutingWithoutEnteringContract(t *testing.T) {
	store := &fakeManagementStore{
		user:   activeUser(false),
		member: &types.ProjectMember{UserID: testUserID, ProjectID: testProjectID, Role: string(authz.RoleDeveloper)},
		projects: []types.Project{{
			ID:        testProjectID,
			Name:      "Example",
			CreatedBy: testUserID,
			Status:    types.ProjectStatusActive,
		}},
		project: &types.Project{
			ID:        testProjectID,
			Name:      "Example",
			CreatedBy: testUserID,
			Status:    types.ProjectStatusActive,
		},
	}
	router := chi.NewRouter()
	// Reproduce the legacy mount which still owns sibling POST/PUT methods.
	// Exact typed GET aliases must win over these mounted sub-router patterns.
	router.Route("/api/v1", func(router chi.Router) {
		router.Route("/projects", func(router chi.Router) {
			router.Post("/", func(http.ResponseWriter, *http.Request) {})
			router.Route("/{projectID}", func(router chi.Router) {
				router.Put("/", func(http.ResponseWriter, *http.Request) {})
			})
		})
	})
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	Register(api, testServer(store))

	for _, target := range []string{
		"/api/v1/projects/",
		"/api/v1/projects/" + testProjectID + "/",
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}

		unauthenticated := httptest.NewRecorder()
		router.ServeHTTP(unauthenticated, httptest.NewRequest(http.MethodGet, target, nil))
		if unauthenticated.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET %s status = %d, want 401", target, unauthenticated.Code)
		}
	}

	for _, path := range []string{
		"/api/v1/projects/",
		"/api/v1/projects/{projectID}/",
	} {
		if api.OpenAPI().Paths[path] != nil {
			t.Errorf("legacy trailing-slash alias leaked into OpenAPI: %s", path)
		}
	}
}

func TestProjectDTOPreservesRawSettingsWireEncoding(t *testing.T) {
	tests := []struct {
		name     string
		settings json.RawMessage
	}{
		{name: "object", settings: json.RawMessage(`{"nested":{"enabled":true}}`)},
		{name: "large integer", settings: json.RawMessage(`{"exact":9007199254740993}`)},
		{name: "array", settings: json.RawMessage(`["legacy",1]`)},
		{name: "scalar", settings: json.RawMessage(`"legacy"`)},
		{name: "null", settings: json.RawMessage(`null`)},
		{name: "empty omitted"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			project := types.Project{
				ID:        testProjectID,
				Name:      "Example",
				CreatedBy: testUserID,
				Status:    types.ProjectStatusActive,
				Settings:  test.settings,
			}
			legacy, err := json.Marshal(types.DataResponse[types.Project]{Data: project})
			if err != nil {
				t.Fatalf("marshal legacy response: %v", err)
			}
			typed, err := json.Marshal(DataResponse[Project]{Data: projectDTO(&project)})
			if err != nil {
				t.Fatalf("marshal typed response: %v", err)
			}
			if got, want := string(typed), string(legacy); got != want {
				t.Fatalf("typed wire response = %s, want legacy response %s", got, want)
			}
		})
	}
}

func TestProjectCapabilitiesFollowCentralRoleMatrix(t *testing.T) {
	roles := []authz.ProjectRole{authz.RoleOwner, authz.RoleMaintainer, authz.RoleDeveloper}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			store := &fakeManagementStore{
				user:    activeUser(false),
				member:  &types.ProjectMember{UserID: testUserID, ProjectID: testProjectID, Role: string(role)},
				project: &types.Project{ID: testProjectID, Name: "Example", CreatedBy: testUserID, Status: types.ProjectStatusActive},
			}
			recorder := httptest.NewRecorder()
			testRouter(testServer(store)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/projects/"+testProjectID+"/capabilities"))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}

			var body DataResponse[ProjectCapabilities]
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			want, _ := authz.PermissionsForRole(role)
			if got := permissionsFromDTO(body.Data.Permissions); !reflect.DeepEqual(got, want.Permissions()) {
				t.Fatalf("permissions = %v, want %v", got, want.Permissions())
			}
			if body.Data.Role != ProjectRole(role) {
				t.Fatalf("role = %q, want %q", body.Data.Role, role)
			}
		})
	}
}

func TestProjectCapabilitiesSuperadminStillRequiresExistingProject(t *testing.T) {
	store := &fakeManagementStore{user: activeUser(true)}
	recorder := httptest.NewRecorder()
	testRouter(testServer(store)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/projects/"+testProjectID+"/capabilities"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestProjectAuthorizationRejectsMalformedUUIDBeforeStoreLookup(t *testing.T) {
	store := &fakeManagementStore{user: activeUser(false)}
	recorder := httptest.NewRecorder()
	testRouter(testServer(store)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/projects/not-a-uuid"))

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422; body = %s", recorder.Code, recorder.Body.String())
	}
}

func permissionsFromDTO(permissions []Permission) []authz.Permission {
	result := make([]authz.Permission, len(permissions))
	for i, permission := range permissions {
		result[i] = authz.Permission(permission)
	}
	return result
}
