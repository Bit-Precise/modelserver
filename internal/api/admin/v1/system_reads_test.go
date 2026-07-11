package adminv1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	storepkg "github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

const testPlanID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

func TestSystemReadOperationsPreserveLegacySuperadminDenial(t *testing.T) {
	store := &fakeManagementStore{user: activeUser(false)}
	for _, target := range []string{"/api/v1/users", "/api/v1/plans"} {
		recorder := httptest.NewRecorder()
		testRouter(testServer(store)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target))
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("GET %s status = %d, want 403; body = %s", target, recorder.Code, recorder.Body.String())
		}
		if got, want := recorder.Body.String(), "{\"error\":{\"code\":\"forbidden\",\"message\":\"superadmin access required\"}}\n"; got != want {
			t.Fatalf("GET %s body = %q, want %q", target, got, want)
		}
	}
}

func TestUserReadOperationsPreserveLegacyWireShape(t *testing.T) {
	current := activeUser(true)
	target := *activeUser(false)
	target.ID = testTargetID
	target.Email = "target@example.com"
	target.Nickname = "Target"
	store := &fakeManagementStore{
		user:      current,
		users:     []types.User{target},
		usersByID: map[string]*types.User{testTargetID: &target},
		compactUsers: []storepkg.CompactUser{{
			ID:       target.ID,
			Nickname: target.Nickname,
			Email:    target.Email,
		}},
	}
	router := testRouter(testServer(store))

	t.Run("list", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/users?page=2&per_page=10&sort=email&order=asc"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		wantPagination := types.PaginationParams{Page: 2, PerPage: 10, Sort: "email", Order: "asc"}
		if store.lastPagination != wantPagination {
			t.Fatalf("pagination = %#v, want %#v", store.lastPagination, wantPagination)
		}
		want, err := json.Marshal(types.ListResponse[types.User]{
			Data: store.users,
			Meta: types.Meta{Total: 1, Page: 2, PerPage: 10, TotalPages: 1},
		})
		if err != nil {
			t.Fatalf("marshal legacy response: %v", err)
		}
		want = append(want, '\n')
		if got := recorder.Body.String(); got != string(want) {
			t.Fatalf("response = %s, want %s", got, want)
		}
	})

	t.Run("compact", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/users/compact"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		if got, want := recorder.Body.String(), "{\"data\":[{\"id\":\""+testTargetID+"\",\"nickname\":\"Target\",\"email\":\"target@example.com\"}]}\n"; got != want {
			t.Fatalf("response = %q, want %q", got, want)
		}
	})

	t.Run("detail", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/users/"+testTargetID))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		want, err := json.Marshal(types.DataResponse[types.User]{Data: target})
		if err != nil {
			t.Fatalf("marshal legacy response: %v", err)
		}
		want = append(want, '\n')
		if got := recorder.Body.String(); got != string(want) {
			t.Fatalf("response = %s, want %s", got, want)
		}
	})

	t.Run("malformed ID remains not found", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/users/not-a-uuid"))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("legacy list slash", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/users/"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestPlanReadOperationsPreserveLegacyWireShape(t *testing.T) {
	anchor := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	plan := types.Plan{
		ID:            testPlanID,
		Name:          "Pro",
		Slug:          "pro",
		DisplayName:   "Pro",
		Description:   "Example plan",
		TierLevel:     2,
		PriceCNYFen:   1000,
		PriceUSDCents: 200,
		PeriodMonths:  1,
		CreditRules: []types.CreditRule{{
			Window:     "1M",
			WindowType: "fixed",
			MaxCredits: 1000000,
			AnchorTime: &anchor,
		}},
		ModelCreditRates: map[string]types.CreditRate{
			"model-a": {
				InputRate:  1,
				OutputRate: 2,
				LongContext: &types.LongContextCreditRate{
					ThresholdInputTokens: 200000,
					InputMultiplier:      2,
					OutputMultiplier:     1.5,
				},
			},
		},
		ClientModelCreditRates: map[string]map[string]types.CreditRate{
			"claude_code": {"model-a": {InputRate: 0.5, OutputRate: 1}},
		},
		ClassicRules: []types.ClassicRule{{Metric: "rpm", Limit: 60}},
		IsActive:     true,
		CreatedAt:    anchor,
		UpdatedAt:    anchor,
	}
	store := &fakeManagementStore{
		user:  activeUser(true),
		plans: []types.Plan{plan},
		plan:  &plan,
	}
	router := testRouter(testServer(store))

	t.Run("list", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/plans?page=1&per_page=20"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
		want, err := json.Marshal(types.ListResponse[types.Plan]{
			Data: []types.Plan{plan},
			Meta: types.Meta{Total: 1, Page: 1, PerPage: 20, TotalPages: 1},
		})
		if err != nil {
			t.Fatalf("marshal legacy response: %v", err)
		}
		want = append(want, '\n')
		if got := recorder.Body.String(); got != string(want) {
			t.Fatalf("response = %s, want %s", got, want)
		}
	})

	for _, target := range []string{
		"/api/v1/plans/" + testPlanID,
		"/api/v1/plans/" + testPlanID + "/",
	} {
		t.Run(target, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, target))
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			want, err := json.Marshal(types.DataResponse[types.Plan]{Data: plan})
			if err != nil {
				t.Fatalf("marshal legacy response: %v", err)
			}
			want = append(want, '\n')
			if got := recorder.Body.String(); got != string(want) {
				t.Fatalf("response = %s, want %s", got, want)
			}
		})
	}

	t.Run("legacy list slash", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/plans/"))
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}
	})

	t.Run("malformed ID remains not found", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/plans/not-a-uuid"))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
		}
	})
}
