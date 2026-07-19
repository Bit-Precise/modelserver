package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/admin"
	adminv1 "github.com/modelserver/modelserver/internal/api/admin/v1"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/config"
	storepkg "github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

const routeTestUserID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"

type routeTestStore struct {
	user *types.User
	plan *types.Plan
}

func (store *routeTestStore) GetUserByID(string) (*types.User, error) { return store.user, nil }
func (*routeTestStore) GetProjectMember(string, string) (*types.ProjectMember, error) {
	return nil, nil
}
func (*routeTestStore) ListUserProjects(string, types.PaginationParams) ([]types.Project, int, error) {
	return []types.Project{}, 0, nil
}
func (*routeTestStore) GetProjectByID(string) (*types.Project, error) { return nil, nil }
func (store *routeTestStore) ListUsers(types.PaginationParams) ([]types.User, int, error) {
	return []types.User{*store.user}, 1, nil
}
func (*routeTestStore) ListAllUsersCompact() ([]storepkg.CompactUser, error) {
	return []storepkg.CompactUser{}, nil
}
func (store *routeTestStore) ListPlansPaginated(types.PaginationParams) ([]types.Plan, int, error) {
	return []types.Plan{*store.plan}, 1, nil
}
func (store *routeTestStore) GetPlanByID(string) (*types.Plan, error) { return store.plan, nil }
func (*routeTestStore) CreatePlan(*types.Plan) error                  { return nil }
func (*routeTestStore) UpdatePlan(string, map[string]any) error       { return nil }
func (*routeTestStore) DeletePlan(string) error                       { return nil }
func (*routeTestStore) ListModels() ([]types.Model, error)            { return []types.Model{}, nil }
func (*routeTestStore) ListModelsByStatus(string) ([]types.Model, error) {
	return []types.Model{}, nil
}
func (*routeTestStore) GetModelByName(string) (*types.Model, error) { return nil, nil }
func (*routeTestStore) CreateModel(*types.Model) error               { return nil }
func (*routeTestStore) UpdateModel(string, map[string]any) error     { return nil }
func (*routeTestStore) DeleteModel(string) error                     { return nil }
func (*routeTestStore) ModelReferenceCountsFor(string) (storepkg.ModelReferenceCounts, error) {
	return storepkg.ModelReferenceCounts{}, nil
}

// Extra usage store stubs (interface expanded in Batch 6)
func (*routeTestStore) GetExtraUsageSettings(string) (*types.ExtraUsageSettings, error) {
	return nil, nil
}
func (*routeTestStore) UpsertExtraUsageSettings(string, bool, int64) (*types.ExtraUsageSettings, error) {
	return nil, nil
}
func (*routeTestStore) GetMonthlyExtraSpendCredits(string, time.Time) (int64, error) {
	return 0, nil
}
func (*routeTestStore) ListExtraUsageTransactions(string, types.PaginationParams, string) ([]types.ExtraUsageTransaction, int, error) {
	return []types.ExtraUsageTransaction{}, 0, nil
}
func (*routeTestStore) SumDailyExtraUsageTopupCredits(string, time.Time) (int64, error) {
	return 0, nil
}
func (*routeTestStore) CreateOrder(*types.Order) error {
	return nil
}
func (*routeTestStore) UpdateOrderStatus(string, string) error {
	return nil
}
func (*routeTestStore) UpdateOrderPayment(string, string, string, string) error {
	return nil
}
func (*routeTestStore) GetOrderByID(string) (*types.Order, error) {
	return nil, nil
}
func (*routeTestStore) TopUpExtraUsage(storepkg.TopUpExtraUsageReq) (int64, error) {
	return 0, nil
}
func (*routeTestStore) ListExtraUsageSettings() ([]types.ExtraUsageSettings, error) {
	return []types.ExtraUsageSettings{}, nil
}
func (*routeTestStore) SumRecentExtraUsageSpendCredits(string, int) (int64, error) {
	return 0, nil
}
func (*routeTestStore) SetExtraUsageBypass(string, bool) (*types.ExtraUsageSettings, error) {
	return nil, nil
}

type routeTestTokens struct{}

func (routeTestTokens) ValidateToken(string) (*auth.Claims, error) {
	return &auth.Claims{UserID: routeTestUserID, TokenType: "access"}, nil
}

func TestTypedAdminRoutesCoexistWithLegacyMount(t *testing.T) {
	router := chi.NewRouter()
	cfg := &config.Config{}
	jwtManager := auth.NewJWTManager("test-secret-at-least-32-characters-long", time.Minute, time.Hour)
	data := &routeTestStore{
		user: &types.User{
			ID:           routeTestUserID,
			Email:        "admin@example.com",
			Nickname:     "Admin",
			IsSuperadmin: true,
			Status:       types.UserStatusActive,
		},
		plan: &types.Plan{ID: "plan-1", Name: "Plan", Slug: "plan", DisplayName: "Plan", IsActive: true},
	}

	// Mount in the same order as main. Dependencies which are only captured by
	// unrelated legacy handlers may be nil because this test exercises the
	// public typed operation.
	admin.MountRoutes(router, nil, cfg, nil, jwtManager, nil, nil, nil)
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	adminv1.Register(api, &adminv1.Server{
		Store:  data,
		Users:  data,
		Plans:  data,
		Tokens: routeTestTokens{},
		Config: cfg,
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/config", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("typed route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got, want := recorder.Body.String(), "{\"oauth_providers\":[]}\n"; got != want {
		t.Fatalf("typed route body = %q, want %q", got, want)
	}

	// A fake token is deliberately invalid for the legacy JWT middleware but
	// valid for the typed test authorizer. A 200 therefore proves that exact
	// typed GET routes win while sibling legacy write routes remain mounted.
	for _, target := range []string{
		"/api/v1/users",
		"/api/v1/users/",
		"/api/v1/users/compact",
		"/api/v1/users/" + routeTestUserID,
		"/api/v1/plans",
		"/api/v1/plans/",
		"/api/v1/plans/plan-1",
		"/api/v1/plans/plan-1/",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Authorization", "Bearer typed-test-token")
		router.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Errorf("typed GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}
}
