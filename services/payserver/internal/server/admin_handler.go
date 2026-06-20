package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

func handleListTenants(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := pagination(r)
		rows, total, err := st.ListTenants(limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": rows,
			"meta":  map[string]any{"total": total, "limit": limit, "offset": offset},
		})
	}
}

func handleCreateTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name           string `json:"name"`
			CallbackURL    string `json:"callback_url"`
			CallbackSecret string `json:"callback_secret"`
			Description    string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		if body.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if body.CallbackURL != "" {
			if err := validateReturnURL(body.CallbackURL); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		secret, err := tenant.GenerateSecret()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate secret"})
			return
		}
		hash, err := tenant.HashSecret(secret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash secret"})
			return
		}
		t := &tenant.Tenant{
			Name: body.Name, SecretHash: hash,
			CallbackURL: body.CallbackURL, CallbackSecret: body.CallbackSecret,
			Description: body.Description, IsActive: true,
		}
		if err := st.CreateTenant(t); err != nil {
			if errors.Is(err, store.ErrTenantNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "name already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"tenant": t,
			"secret": secret,
		})
	}
}

func handleGetTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, err := st.GetTenantByID(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tenant": t})
	}
}

func handleUpdateTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		// Filter to allowed fields silently. UpdateTenant returns error on
		// unknown keys; we keep the client-facing surface forgiving by
		// dropping them here, but the store still enforces the whitelist
		// internally so any bypass would fail there too.
		filtered := map[string]any{}
		for _, k := range []string{"callback_url", "callback_secret", "description", "is_active"} {
			if v, ok := body[k]; ok {
				filtered[k] = v
			}
		}
		if cb, ok := filtered["callback_url"].(string); ok && cb != "" {
			if err := validateReturnURL(cb); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		if err := st.UpdateTenant(id, filtered); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		t, err := st.GetTenantByID(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tenant": t})
	}
}

func handleDeleteTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		err := st.DeleteTenant(id)
		if errors.Is(err, store.ErrTenantHasPayments) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "tenant has payments; deactivate via PATCH is_active=false instead",
				"code":  "tenant_has_payments",
			})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleRotateTenantSecret(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, err := st.GetTenantByID(id)
		if err != nil || t == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		secret, err := tenant.GenerateSecret()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate"})
			return
		}
		hash, err := tenant.HashSecret(secret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash"})
			return
		}
		if err := st.RotateTenantSecret(id, hash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rotate failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"secret": secret})
	}
}

func handleListPayments(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := pagination(r)
		filters := store.PaymentFilters{
			TenantID: r.URL.Query().Get("tenant_id"),
			Status:   r.URL.Query().Get("status"),
			Channel:  r.URL.Query().Get("channel"),
		}
		rows, total, err := st.ListPayments(limit, offset, filters)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": rows,
			"meta":  map[string]any{"total": total, "limit": limit, "offset": offset},
		})
	}
}

func handleGetPayment(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		p, err := st.GetPaymentByID(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}
		if p == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"payment": p})
	}
}

func pagination(r *http.Request) (limit, offset int) {
	limit = 50
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, _ := strconv.Atoi(v); n >= 0 {
			offset = n
		}
	}
	return
}
