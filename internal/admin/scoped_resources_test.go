package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/types"
)

type scopedResourceStoreStub struct {
	key          *types.APIKey
	policy       *types.RateLimitPolicy
	subscription *types.Subscription
	order        *types.Order
	trace        *types.Trace

	mutationCalls     int
	traceRequestCalls int
	lastProjectID     string
	lastResourceID    string
}

func (s *scopedResourceStoreStub) GetAPIKeyByID(string) (*types.APIKey, error) {
	return s.key, nil
}

func (s *scopedResourceStoreStub) UpdateAPIKeyForProject(projectID, id string, _ map[string]interface{}) (bool, error) {
	s.mutationCalls++
	s.lastProjectID = projectID
	s.lastResourceID = id
	return true, nil
}

func (s *scopedResourceStoreStub) DeleteAPIKeyForProject(projectID, id string) (bool, error) {
	s.mutationCalls++
	s.lastProjectID = projectID
	s.lastResourceID = id
	return true, nil
}

func (s *scopedResourceStoreStub) GetPolicyByID(string) (*types.RateLimitPolicy, error) {
	return s.policy, nil
}

func (s *scopedResourceStoreStub) UpdatePolicyForProject(projectID, id string, _ map[string]interface{}) (bool, error) {
	s.mutationCalls++
	s.lastProjectID = projectID
	s.lastResourceID = id
	return true, nil
}

func (s *scopedResourceStoreStub) DeletePolicyForProject(projectID, id string) (bool, error) {
	s.mutationCalls++
	s.lastProjectID = projectID
	s.lastResourceID = id
	return true, nil
}

func (s *scopedResourceStoreStub) GetSubscriptionByID(string) (*types.Subscription, error) {
	return s.subscription, nil
}

func (s *scopedResourceStoreStub) UpdateSubscriptionStatusForProject(projectID, id, _ string) (bool, error) {
	s.mutationCalls++
	s.lastProjectID = projectID
	s.lastResourceID = id
	return true, nil
}

func (s *scopedResourceStoreStub) GetOrderByID(string) (*types.Order, error) {
	return s.order, nil
}

func (s *scopedResourceStoreStub) CancelOrderForProject(projectID, id string) (bool, error) {
	s.mutationCalls++
	s.lastProjectID = projectID
	s.lastResourceID = id
	return true, nil
}

func (s *scopedResourceStoreStub) GetTraceByID(string) (*types.Trace, error) {
	return s.trace, nil
}

func (s *scopedResourceStoreStub) ListRequestsByTraceID(string) ([]types.Request, error) {
	s.traceRequestCalls++
	return []types.Request{}, nil
}

func requestWithAdminContext(req *http.Request, user *types.User, member *types.ProjectMember) *http.Request {
	ctx := context.WithValue(req.Context(), ctxUser, user)
	if member != nil {
		ctx = context.WithValue(ctx, ctxMember, member)
	}
	return req.WithContext(ctx)
}

func assertErrorResponse(t *testing.T, rr *httptest.ResponseRecorder, status int, code string) types.ErrorResponse {
	t.Helper()
	if rr.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, status, rr.Body.String())
	}
	var body types.ErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, rr.Body.String())
	}
	if body.Error.Code != code {
		t.Fatalf("error.code = %q, want %q; body=%s", body.Error.Code, code, rr.Body.String())
	}
	return body
}

func TestSameProjectID(t *testing.T) {
	const canonical = "550e8400-e29b-41d4-a716-446655440000"
	for _, tc := range []struct {
		name  string
		left  string
		right string
		want  bool
	}{
		{"exact", canonical, canonical, true},
		{"equivalent UUID spelling", canonical, "550E8400-E29B-41D4-A716-446655440000", true},
		{"different UUID", canonical, "550e8400-e29b-41d4-a716-446655440001", false},
		{"exact non UUID", "project-1", "project-1", true},
		{"different non UUID", "PROJECT-1", "project-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameProjectID(tc.left, tc.right); got != tc.want {
				t.Fatalf("sameProjectID(%q, %q) = %v, want %v", tc.left, tc.right, got, tc.want)
			}
		})
	}
}

func TestProjectScopedHandlers_HideCrossProjectResources(t *testing.T) {
	const (
		urlProjectID      = "url-project"
		resourceProjectID = "resource-project"
	)

	st := &scopedResourceStoreStub{
		key: &types.APIKey{
			ID:        "key-1",
			ProjectID: resourceProjectID,
			CreatedBy: "user-1",
			Status:    types.APIKeyStatusRevoked,
		},
		policy: &types.RateLimitPolicy{
			ID:        "policy-1",
			ProjectID: resourceProjectID,
		},
		subscription: &types.Subscription{
			ID:        "subscription-1",
			ProjectID: resourceProjectID,
		},
		order: &types.Order{
			ID:        "order-1",
			ProjectID: resourceProjectID,
			Status:    types.OrderStatusPending,
			OrderType: types.OrderTypeExtraUsageTopup,
		},
		trace: &types.Trace{
			ID:        "trace-1",
			ProjectID: resourceProjectID,
		},
	}

	owner := &types.User{ID: "user-1", Status: types.UserStatusActive}
	ownerMember := &types.ProjectMember{
		UserID:    owner.ID,
		ProjectID: urlProjectID,
		Role:      types.RoleOwner,
	}
	superadmin := &types.User{ID: "admin-1", IsSuperadmin: true, Status: types.UserStatusActive}

	tests := []struct {
		name        string
		method      string
		pattern     string
		path        string
		body        string
		handler     http.Handler
		user        *types.User
		member      *types.ProjectMember
		wantMessage string
	}{
		{"get key", http.MethodGet, "/projects/{projectID}/keys/{keyID}", "/projects/url-project/keys/key-1", "", handleGetKey(st), owner, ownerMember, "key not found"},
		{"update key", http.MethodPut, "/projects/{projectID}/keys/{keyID}", "/projects/url-project/keys/key-1", `{}`, handleUpdateKey(st, nil), owner, ownerMember, "key not found"},
		{"delete key", http.MethodDelete, "/projects/{projectID}/keys/{keyID}", "/projects/url-project/keys/key-1", "", handleDeleteKey(st), owner, ownerMember, "key not found"},
		{"get policy", http.MethodGet, "/projects/{projectID}/policies/{policyID}", "/projects/url-project/policies/policy-1", "", handleGetPolicy(st), owner, ownerMember, "policy not found"},
		{"update policy", http.MethodPut, "/projects/{projectID}/policies/{policyID}", "/projects/url-project/policies/policy-1", `{}`, handleUpdatePolicy(st, nil), owner, ownerMember, "policy not found"},
		{"delete policy", http.MethodDelete, "/projects/{projectID}/policies/{policyID}", "/projects/url-project/policies/policy-1", "", handleDeletePolicy(st), owner, ownerMember, "policy not found"},
		{"get subscription", http.MethodGet, "/projects/{projectID}/subscriptions/{subID}", "/projects/url-project/subscriptions/subscription-1", "", handleGetSubscription(st), owner, ownerMember, "subscription not found"},
		{"update subscription", http.MethodPut, "/projects/{projectID}/subscriptions/{subID}", "/projects/url-project/subscriptions/subscription-1", `{"status":"revoked"}`, handleUpdateSubscription(st), superadmin, nil, "subscription not found"},
		{"get order", http.MethodGet, "/projects/{projectID}/orders/{orderID}", "/projects/url-project/orders/order-1", "", handleGetOrder(st), owner, ownerMember, "order not found"},
		{"cancel order", http.MethodPost, "/projects/{projectID}/orders/{orderID}/cancel", "/projects/url-project/orders/order-1/cancel", "", handleCancelOrder(st), owner, ownerMember, "order not found"},
		{"get trace", http.MethodGet, "/projects/{projectID}/traces/{traceID}", "/projects/url-project/traces/trace-1", "", handleGetTrace(st), owner, ownerMember, "trace not found"},
		{"list trace requests", http.MethodGet, "/projects/{projectID}/traces/{traceID}/requests", "/projects/url-project/traces/trace-1/requests", "", handleListTraceRequests(st), owner, ownerMember, "trace not found"},
		{"get extra usage topup", http.MethodGet, "/projects/{projectID}/extra-usage/topup/{orderID}", "/projects/url-project/extra-usage/topup/order-1", "", handleGetExtraUsageTopup(st), owner, ownerMember, "order not found"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutationsBefore := st.mutationCalls
			traceRequestsBefore := st.traceRequestCalls

			router := chi.NewRouter()
			router.Method(tc.method, tc.pattern, tc.handler)
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req = requestWithAdminContext(req, tc.user, tc.member)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			errBody := assertErrorResponse(t, rr, http.StatusNotFound, "not_found")
			if errBody.Error.Message != tc.wantMessage {
				t.Fatalf("error.message = %q, want %q", errBody.Error.Message, tc.wantMessage)
			}
			if st.mutationCalls != mutationsBefore {
				t.Fatalf("cross-project request invoked a mutation; before=%d after=%d", mutationsBefore, st.mutationCalls)
			}
			if st.traceRequestCalls != traceRequestsBefore {
				t.Fatalf("cross-project request loaded trace requests; before=%d after=%d", traceRequestsBefore, st.traceRequestCalls)
			}
		})
	}
}

func TestProjectScopedHandlers_AllowSameProjectResources(t *testing.T) {
	const projectID = "project-1"

	owner := &types.User{ID: "user-1", Status: types.UserStatusActive}
	ownerMember := &types.ProjectMember{
		UserID:    owner.ID,
		ProjectID: projectID,
		Role:      types.RoleOwner,
	}
	superadmin := &types.User{ID: "admin-1", IsSuperadmin: true, Status: types.UserStatusActive}

	tests := []struct {
		name          string
		method        string
		pattern       string
		path          string
		body          string
		resourceID    string
		wantStatus    int
		wantMutations int
		wantTraceRead int
		user          *types.User
		member        *types.ProjectMember
		makeHandler   func(*scopedResourceStoreStub) http.Handler
	}{
		{"get key", http.MethodGet, "/projects/{projectID}/keys/{keyID}", "/projects/project-1/keys/key-1", "", "key-1", http.StatusOK, 0, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleGetKey(st) }},
		{"update key", http.MethodPut, "/projects/{projectID}/keys/{keyID}", "/projects/project-1/keys/key-1", `{"name":"renamed"}`, "key-1", http.StatusOK, 1, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleUpdateKey(st, nil) }},
		{"delete key", http.MethodDelete, "/projects/{projectID}/keys/{keyID}", "/projects/project-1/keys/key-1", "", "key-1", http.StatusNoContent, 1, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleDeleteKey(st) }},
		{"get policy", http.MethodGet, "/projects/{projectID}/policies/{policyID}", "/projects/project-1/policies/policy-1", "", "policy-1", http.StatusOK, 0, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleGetPolicy(st) }},
		{"update policy", http.MethodPut, "/projects/{projectID}/policies/{policyID}", "/projects/project-1/policies/policy-1", `{"name":"renamed"}`, "policy-1", http.StatusOK, 1, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleUpdatePolicy(st, nil) }},
		{"delete policy", http.MethodDelete, "/projects/{projectID}/policies/{policyID}", "/projects/project-1/policies/policy-1", "", "policy-1", http.StatusNoContent, 1, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleDeletePolicy(st) }},
		{"get subscription", http.MethodGet, "/projects/{projectID}/subscriptions/{subID}", "/projects/project-1/subscriptions/subscription-1", "", "subscription-1", http.StatusOK, 0, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleGetSubscription(st) }},
		{"update subscription", http.MethodPut, "/projects/{projectID}/subscriptions/{subID}", "/projects/project-1/subscriptions/subscription-1", `{"status":"revoked"}`, "subscription-1", http.StatusOK, 1, 0, superadmin, nil, func(st *scopedResourceStoreStub) http.Handler { return handleUpdateSubscription(st) }},
		{"get order", http.MethodGet, "/projects/{projectID}/orders/{orderID}", "/projects/project-1/orders/order-1", "", "order-1", http.StatusOK, 0, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleGetOrder(st) }},
		{"cancel order", http.MethodPost, "/projects/{projectID}/orders/{orderID}/cancel", "/projects/project-1/orders/order-1/cancel", "", "order-1", http.StatusOK, 1, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleCancelOrder(st) }},
		{"get trace", http.MethodGet, "/projects/{projectID}/traces/{traceID}", "/projects/project-1/traces/trace-1", "", "trace-1", http.StatusOK, 0, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleGetTrace(st) }},
		{"list trace requests", http.MethodGet, "/projects/{projectID}/traces/{traceID}/requests", "/projects/project-1/traces/trace-1/requests", "", "trace-1", http.StatusOK, 0, 1, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleListTraceRequests(st) }},
		{"get extra usage topup", http.MethodGet, "/projects/{projectID}/extra-usage/topup/{orderID}", "/projects/project-1/extra-usage/topup/order-1", "", "order-1", http.StatusOK, 0, 0, owner, ownerMember, func(st *scopedResourceStoreStub) http.Handler { return handleGetExtraUsageTopup(st) }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &scopedResourceStoreStub{
				key: &types.APIKey{
					ID:        "key-1",
					ProjectID: projectID,
					CreatedBy: owner.ID,
					Status:    types.APIKeyStatusRevoked,
				},
				policy:       &types.RateLimitPolicy{ID: "policy-1", ProjectID: projectID},
				subscription: &types.Subscription{ID: "subscription-1", ProjectID: projectID},
				order: &types.Order{
					ID:        "order-1",
					ProjectID: projectID,
					Status:    types.OrderStatusPending,
					OrderType: types.OrderTypeExtraUsageTopup,
				},
				trace: &types.Trace{ID: "trace-1", ProjectID: projectID},
			}

			router := chi.NewRouter()
			router.Method(tc.method, tc.pattern, tc.makeHandler(st))
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req = requestWithAdminContext(req, tc.user, tc.member)
			rr := httptest.NewRecorder()
			router.ServeHTTP(rr, req)

			if rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body.String())
			}
			if st.mutationCalls != tc.wantMutations {
				t.Fatalf("mutation calls = %d, want %d", st.mutationCalls, tc.wantMutations)
			}
			if st.traceRequestCalls != tc.wantTraceRead {
				t.Fatalf("trace request reads = %d, want %d", st.traceRequestCalls, tc.wantTraceRead)
			}
			if tc.wantMutations > 0 && (st.lastProjectID != projectID || st.lastResourceID != tc.resourceID) {
				t.Fatalf("mutation scope = (%q, %q), want (%q, %q)",
					st.lastProjectID, st.lastResourceID, projectID, tc.resourceID)
			}
		})
	}
}

func TestSubscriptionOverrides_RequireSuperadmin(t *testing.T) {
	regularUser := &types.User{ID: "user-1", Status: types.UserStatusActive}
	member := &types.ProjectMember{
		UserID:    regularUser.ID,
		ProjectID: "project-1",
		Role:      types.RoleOwner,
	}

	t.Run("create", func(t *testing.T) {
		router := chi.NewRouter()
		router.Post("/projects/{projectID}/subscriptions", handleCreateSubscription(nil))
		req := httptest.NewRequest(http.MethodPost, "/projects/project-1/subscriptions", bytes.NewBufferString(`{"plan_name":"pro"}`))
		req = requestWithAdminContext(req, regularUser, member)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assertErrorResponse(t, rr, http.StatusForbidden, "forbidden")
	})

	t.Run("update", func(t *testing.T) {
		st := &scopedResourceStoreStub{
			subscription: &types.Subscription{ID: "subscription-1", ProjectID: "project-1"},
		}
		router := chi.NewRouter()
		router.Put("/projects/{projectID}/subscriptions/{subID}", handleUpdateSubscription(st))
		req := httptest.NewRequest(http.MethodPut, "/projects/project-1/subscriptions/subscription-1", bytes.NewBufferString(`{"status":"revoked"}`))
		req = requestWithAdminContext(req, regularUser, member)
		rr := httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assertErrorResponse(t, rr, http.StatusForbidden, "forbidden")
		if st.mutationCalls != 0 {
			t.Fatalf("non-superadmin update invoked mutation %d time(s)", st.mutationCalls)
		}
	})
}

func TestMissingMemberContextDoesNotImplySuperadmin(t *testing.T) {
	regularUser := &types.User{ID: "user-1", Status: types.UserStatusActive}
	req := httptest.NewRequest(http.MethodGet, "/projects/project-1/keys/key-1", nil)
	req = requestWithAdminContext(req, regularUser, nil)

	t.Run("role guard", func(t *testing.T) {
		rr := httptest.NewRecorder()
		if requireRole(rr, req, types.RoleOwner) {
			t.Fatal("requireRole allowed a non-superadmin request with no member context")
		}
		assertErrorResponse(t, rr, http.StatusForbidden, "forbidden")
	})

	t.Run("key access", func(t *testing.T) {
		if canAccessKey(req, &types.APIKey{CreatedBy: regularUser.ID}) {
			t.Fatal("canAccessKey allowed a non-superadmin request with no member context")
		}
	})

	t.Run("explicit superadmin", func(t *testing.T) {
		adminReq := httptest.NewRequest(http.MethodGet, "/projects/project-1/keys/key-1", nil)
		adminReq = requestWithAdminContext(adminReq, &types.User{ID: "admin-1", IsSuperadmin: true}, nil)
		rr := httptest.NewRecorder()
		if !requireRole(rr, adminReq, types.RoleOwner) {
			t.Fatalf("requireRole rejected explicit superadmin; body=%s", rr.Body.String())
		}
		if !canAccessKey(adminReq, &types.APIKey{}) {
			t.Fatal("canAccessKey rejected explicit superadmin")
		}
	})
}
