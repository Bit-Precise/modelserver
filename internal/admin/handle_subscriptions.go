package admin

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

type subscriptionReader interface {
	GetSubscriptionByID(id string) (*types.Subscription, error)
}

type subscriptionUpdater interface {
	subscriptionReader
	UpdateSubscriptionStatusForProject(projectID, id, status string) (bool, error)
}

// requireSubscriptionOverride restricts the legacy direct subscription write
// endpoints to explicit superadmin operations. Normal project billing must go
// through orders and the signed delivery webhook.
func requireSubscriptionOverride(w http.ResponseWriter, r *http.Request) bool {
	user := UserFromContext(r.Context())
	if user == nil || !user.IsSuperadmin {
		writeError(w, http.StatusForbidden, "forbidden", "superadmin access required")
		return false
	}
	return true
}

func handleListSubscriptions(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		subs, err := st.ListSubscriptions(projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to list subscriptions")
			return
		}
		writeData(w, http.StatusOK, subs)
	}
}

func handleCreateSubscription(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireSubscriptionOverride(w, r) {
			return
		}
		projectID := chi.URLParam(r, "projectID")
		var body struct {
			PlanName  string `json:"plan_name"`
			StartsAt  string `json:"starts_at"`
			ExpiresAt string `json:"expires_at"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if body.PlanName == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "plan_name is required")
			return
		}

		plan, err := st.GetPlanBySlug(body.PlanName)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to look up plan")
			return
		}
		if plan == nil {
			writeError(w, http.StatusBadRequest, "bad_request", "unknown plan: "+body.PlanName)
			return
		}

		startsAt := time.Now()
		expiresAt := startsAt.AddDate(0, 1, 0) // Default: 1 month.
		if body.StartsAt != "" {
			if t, err := time.Parse(time.RFC3339, body.StartsAt); err == nil {
				startsAt = t
			}
		}
		if body.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, body.ExpiresAt); err == nil {
				expiresAt = t
			}
		}

		sub, err := st.CreateSubscriptionFromPlan(projectID, plan, startsAt, expiresAt)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to create subscription: "+err.Error())
			return
		}
		writeData(w, http.StatusCreated, sub)
	}
}

func handleGetSubscription(st subscriptionReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		sub, err := st.GetSubscriptionByID(chi.URLParam(r, "subID"))
		if err != nil || sub == nil || !sameProjectID(sub.ProjectID, projectID) {
			writeError(w, http.StatusNotFound, "not_found", "subscription not found")
			return
		}
		writeData(w, http.StatusOK, sub)
	}
}

func handleUpdateSubscription(st subscriptionUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireSubscriptionOverride(w, r) {
			return
		}
		projectID := chi.URLParam(r, "projectID")
		subID := chi.URLParam(r, "subID")
		sub, err := st.GetSubscriptionByID(subID)
		if err != nil || sub == nil || !sameProjectID(sub.ProjectID, projectID) {
			writeError(w, http.StatusNotFound, "not_found", "subscription not found")
			return
		}
		var body struct {
			Status string `json:"status"`
		}
		if err := decodeBody(r, &body); err != nil || body.Status == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "status is required")
			return
		}

		updated, err := st.UpdateSubscriptionStatusForProject(projectID, subID, body.Status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to update subscription")
			return
		}
		if !updated {
			writeError(w, http.StatusNotFound, "not_found", "subscription not found")
			return
		}

		sub, _ = st.GetSubscriptionByID(subID)
		writeData(w, http.StatusOK, sub)
	}
}
