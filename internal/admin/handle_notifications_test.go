package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// The tests below intentionally exercise the router with real handler
// wiring (via a small helper) rather than calling handler funcs
// directly — they verify both the handler body and the middleware chain
// (RequireSuperadmin).

func TestNotificationsAdmin_RequireSuperadmin(t *testing.T) {
	st := openTestAdminStore(t)                 // helper defined in the admin test package
	nonAdmin := seedAdminTestUser(t, st, false) // helper: creates user, returns access token
	r := newAdminTestRouter(t, st)              // helper: builds chi.Router with routes mounted

	req := httptest.NewRequest(http.MethodGet, "/admin/notifications", nil)
	req.Header.Set("Authorization", "Bearer "+nonAdmin.token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin GET /admin/notifications = %d, want 403", rec.Code)
	}
}

func TestNotificationsAdmin_CreateGetListDeleteRoundTrip(t *testing.T) {
	st := openTestAdminStore(t)
	admin := seedAdminTestUser(t, st, true)
	r := newAdminTestRouter(t, st)

	// Create a global notification.
	createBody := `{"title":"Maintenance","body":"Down at 2am","audience_type":"global"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(createBody))
	req.Header.Set("Authorization", "Bearer "+admin.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data struct {
			ID    string `json:"id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if createResp.Data.ID == "" || createResp.Data.Title != "Maintenance" {
		t.Fatalf("unexpected create body: %+v", createResp)
	}
	id := createResp.Data.ID

	// Get one.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get: status=%d", rec.Code)
	}

	// List — total == 1.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications?page=1&per_page=10", nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var listResp struct {
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 1 {
		t.Fatalf("list total = %d, want 1", listResp.Meta.Total)
	}

	// Delete (soft).
	req = httptest.NewRequest(http.MethodDelete, "/admin/notifications/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Second delete → 404.
	req = httptest.NewRequest(http.MethodDelete, "/admin/notifications/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second delete: status=%d, want 404", rec.Code)
	}

	// Default list excludes deleted → total == 0.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications?page=1&per_page=10", nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 0 {
		t.Fatalf("alive-only list total = %d, want 0 after delete", listResp.Meta.Total)
	}

	// include_deleted=1 → total == 1.
	req = httptest.NewRequest(http.MethodGet, "/admin/notifications?page=1&per_page=10&include_deleted=1", nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 1 {
		t.Fatalf("include_deleted list total = %d, want 1", listResp.Meta.Total)
	}
	_ = chi.NewRouter // silence unused import when future edits remove one
}

func TestNotificationsAdmin_CreateValidation(t *testing.T) {
	st := openTestAdminStore(t)
	admin := seedAdminTestUser(t, st, true)
	r := newAdminTestRouter(t, st)

	cases := []struct {
		name string
		body string
		want int
		code string // JSON error.code
	}{
		{"empty title", `{"title":"","body":"b","audience_type":"global"}`, http.StatusBadRequest, "invalid_input"},
		{"title too long", `{"title":"` + strings.Repeat("x", 201) + `","body":"b","audience_type":"global"}`, http.StatusBadRequest, "invalid_input"},
		{"body too long", `{"title":"t","body":"` + strings.Repeat("x", 20001) + `","audience_type":"global"}`, http.StatusBadRequest, "invalid_input"},
		{"invalid audience_type", `{"title":"t","body":"b","audience_type":"team"}`, http.StatusBadRequest, "invalid_input"},
		{"global with audience_id", `{"title":"t","body":"b","audience_type":"global","audience_id":"00000000-0000-0000-0000-000000000000"}`, http.StatusBadRequest, "invalid_input"},
		{"project missing audience_id", `{"title":"t","body":"b","audience_type":"project"}`, http.StatusBadRequest, "invalid_input"},
		{"user missing audience_id", `{"title":"t","body":"b","audience_type":"user"}`, http.StatusBadRequest, "invalid_input"},
		{"project audience_id not found", `{"title":"t","body":"b","audience_type":"project","audience_id":"00000000-0000-0000-0000-000000000001"}`, http.StatusBadRequest, "invalid_audience"},
		{"user audience_id not found", `{"title":"t","body":"b","audience_type":"user","audience_id":"00000000-0000-0000-0000-000000000002"}`, http.StatusBadRequest, "invalid_audience"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+admin.token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status=%d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			var errResp struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			json.Unmarshal(rec.Body.Bytes(), &errResp)
			if errResp.Error.Code != tc.code {
				t.Fatalf("error.code=%q, want %q; body=%s", errResp.Error.Code, tc.code, rec.Body.String())
			}
		})
	}
}
