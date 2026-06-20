package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

func adminTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbURL := os.Getenv("PAYSERVER_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("set PAYSERVER_TEST_DB_URL")
	}
	hash, _ := tenant.HashSecret("default-test-secret")
	st, err := store.New(dbURL, slog.New(slog.NewTextHandler(io.Discard, nil)),
		store.MigrationBootstrap{
			DefaultTenantSecretHash: hash,
			DefaultCallbackURL:      "https://test.example/webhook",
			DefaultCallbackSecret:   "test",
		})
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func adminRouter(st *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Get("/admin/tenants", handleListTenants(st))
	r.Post("/admin/tenants", handleCreateTenant(st))
	r.Get("/admin/tenants/{id}", handleGetTenant(st))
	r.Patch("/admin/tenants/{id}", handleUpdateTenant(st))
	r.Delete("/admin/tenants/{id}", handleDeleteTenant(st))
	r.Post("/admin/tenants/{id}/rotate-secret", handleRotateTenantSecret(st))
	r.Get("/admin/payments", handleListPayments(st))
	r.Get("/admin/payments/{id}", handleGetPayment(st))
	return r
}

func TestAdminCreateTenant_ServerGeneratesSecret(t *testing.T) {
	st := adminTestStore(t)
	body := map[string]any{
		"name":            "create-test-" + t.Name(),
		"callback_url":    "https://x.example/cb",
		"callback_secret": "cb-secret",
		"description":     "test",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b)).WithContext(context.Background())
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Tenant struct {
			ID         string `json:"id"`
			SecretHash string `json:"secret_hash"` // must be empty (json:"-")
		} `json:"tenant"`
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Secret == "" {
		t.Error("response did not include cleartext secret")
	}
	if len(resp.Secret) != 43 {
		t.Errorf("secret length = %d, want 43 (32 bytes base64)", len(resp.Secret))
	}
	if resp.Tenant.SecretHash != "" {
		t.Errorf("response leaked secret_hash: %q", resp.Tenant.SecretHash)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, resp.Tenant.ID)
	})
}

func TestAdminCreateTenant_NameRequired(t *testing.T) {
	st := adminTestStore(t)
	b, _ := json.Marshal(map[string]any{"name": ""})
	req := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b))
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestAdminCreateTenant_DuplicateName409(t *testing.T) {
	st := adminTestStore(t)
	body := map[string]any{"name": "dup-" + t.Name()}
	b, _ := json.Marshal(body)
	r1 := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b))
	w1 := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first create code = %d", w1.Code)
	}
	var resp struct{ Tenant struct{ ID string `json:"id"` } `json:"tenant"` }
	_ = json.NewDecoder(w1.Body).Decode(&resp)
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, resp.Tenant.ID)
	})

	r2 := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b))
	w2 := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w2, r2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("second create code = %d, want 409", w2.Code)
	}
}

func TestAdminUpdateTenant_NameIgnored(t *testing.T) {
	st := adminTestStore(t)
	tt := &tenant.Tenant{Name: "upd-test-" + t.Name(), SecretHash: "$2a$10$dummy", IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	b, _ := json.Marshal(map[string]any{
		"name":        "renamed",      // must be ignored
		"description": "updated desc", // allowed
	})
	req := httptest.NewRequest("PATCH", "/admin/tenants/"+tt.ID, bytes.NewReader(b))
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", w.Code, w.Body.String())
	}
	got, _ := st.GetTenantByID(tt.ID)
	if got.Name == "renamed" {
		t.Error("name was modified by PATCH (should be immutable)")
	}
	if got.Description != "updated desc" {
		t.Errorf("description = %q", got.Description)
	}
}

func TestAdminRotateSecret_OldSecretFails(t *testing.T) {
	st := adminTestStore(t)
	oldHash, _ := tenant.HashSecret("old-secret")
	tt := &tenant.Tenant{Name: "rot-" + t.Name(), SecretHash: oldHash, IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	req := httptest.NewRequest("POST", "/admin/tenants/"+tt.ID+"/rotate-secret", nil)
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct{ Secret string `json:"secret"` }
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Secret == "" {
		t.Fatal("rotate response missing new secret")
	}

	got, _ := st.GetTenantByID(tt.ID)
	if !tenant.VerifySecret(got.SecretHash, resp.Secret) {
		t.Error("new secret doesn't verify against stored hash")
	}
	if tenant.VerifySecret(got.SecretHash, "old-secret") {
		t.Error("old secret still works (rotation failed)")
	}
}

// TestUpdateTenant_NotFound_Returns404 confirms handleUpdateTenant
// returns 404 (not 200 with a null tenant body) when the target id
// does not exist. Before the fix, UpdateTenant silently succeeded on
// no-match and the handler then wrote {"tenant": null} with 200.
func TestUpdateTenant_NotFound_Returns404(t *testing.T) {
	st := adminTestStore(t)
	b, _ := json.Marshal(map[string]any{"description": "x"})
	req := httptest.NewRequest("PATCH",
		"/admin/tenants/00000000-0000-0000-0000-000000000000",
		bytes.NewReader(b))
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404; body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Error == "" {
		t.Errorf("expected non-empty error in body")
	}
}

// TestAdminDeleteTenant_TypedErrorCode confirms the 409 response on
// FK-conflict carries the machine-readable code field consumed by
// the admin frontend.
func TestAdminDeleteTenant_TypedErrorCode(t *testing.T) {
	st := adminTestStore(t)
	tt := &tenant.Tenant{Name: "del-code-" + t.Name(), SecretHash: "$2a$10$dummy", IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt.ID)
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})
	if _, err := st.Pool().Exec(t.Context(), `
		INSERT INTO payments (tenant_id, order_id, channel, amount, status)
		VALUES ($1, $2, 'wechat', 1, 'pending')`, tt.ID, "del-code-"+t.Name()); err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/admin/tenants/"+tt.ID, nil)
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", w.Code)
	}
	var resp struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Code != "tenant_has_payments" {
		t.Errorf("code = %q, want tenant_has_payments", resp.Code)
	}
}

func TestAdminDeleteTenant_409OnPayments(t *testing.T) {
	st := adminTestStore(t)
	tt := &tenant.Tenant{Name: "del-" + t.Name(), SecretHash: "$2a$10$dummy", IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt.ID)
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	if _, err := st.Pool().Exec(t.Context(), `
		INSERT INTO payments (tenant_id, order_id, channel, amount, status)
		VALUES ($1, $2, 'wechat', 1, 'pending')`, tt.ID, "del-test-"+t.Name()); err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/admin/tenants/"+tt.ID, nil)
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", w.Code)
	}
}
