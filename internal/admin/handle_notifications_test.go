package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The tests below intentionally exercise the router with real handler
// wiring (via a small helper) rather than calling handler funcs
// directly — they verify both the handler body and the middleware chain
// (RequireSuperadmin).

func TestNotificationsAdmin_RequireSuperadmin(t *testing.T) {
	st := openTestAdminStore(t)                 // helper defined in the admin test package
	nonAdmin := seedAdminTestUser(t, st, false) // helper: creates user, returns access token
	r := newAdminTestRouter(t, st)              // helper: builds router with routes mounted

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

func TestNotificationsUser_UnreadCount_And_ListMarksReadable(t *testing.T) {
	st := openTestAdminStore(t)
	me := seedAdminTestUser(t, st, false)
	other := seedAdminTestUser(t, st, true)
	r := newAdminTestRouter(t, st)

	// Admin creates two global notifications visible to `me`.
	for _, title := range []string{"one", "two"} {
		body := `{"title":"` + title + `","body":"b","audience_type":"global"}`
		req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+other.token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("seed create %s: status=%d body=%s", title, rec.Code, rec.Body.String())
		}
	}

	// unread_count == 2 for `me`.
	req := httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unread_count status=%d", rec.Code)
	}
	var countResp struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 2 {
		t.Fatalf("unread_count = %d, want 2", countResp.Data.Count)
	}

	// List returns 2 items, both with read_at == nil.
	req = httptest.NewRequest(http.MethodGet, "/notifications?page=1&per_page=10", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var listResp struct {
		Data []struct {
			ID     string  `json:"id"`
			ReadAt *string `json:"read_at,omitempty"`
		} `json:"data"`
		Meta struct{ Total int } `json:"meta"`
	}
	json.Unmarshal(rec.Body.Bytes(), &listResp)
	if listResp.Meta.Total != 2 || len(listResp.Data) != 2 {
		t.Fatalf("list total=%d len=%d, want 2/2", listResp.Meta.Total, len(listResp.Data))
	}
	for _, it := range listResp.Data {
		if it.ReadAt != nil {
			t.Fatalf("unexpected read_at on unread item %s: %v", it.ID, *it.ReadAt)
		}
	}

	// Mark the first one read.
	firstID := listResp.Data[0].ID
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+firstID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("mark read status=%d", rec.Code)
	}

	// Idempotent: second POST still 200.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+firstID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second mark read status=%d, want 200 (idempotent)", rec.Code)
	}

	// unread_count now 1.
	req = httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 1 {
		t.Fatalf("unread_count after one read = %d, want 1", countResp.Data.Count)
	}

	// Mark-all-read → marked == 1 (only the remaining unread).
	req = httptest.NewRequest(http.MethodPost, "/notifications/read_all", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("read_all status=%d", rec.Code)
	}
	var markedResp struct {
		Data struct {
			Marked int `json:"marked"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &markedResp)
	if markedResp.Data.Marked != 1 {
		t.Fatalf("read_all marked = %d, want 1", markedResp.Data.Marked)
	}

	// unread_count now 0.
	req = httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 0 {
		t.Fatalf("unread_count after read_all = %d, want 0", countResp.Data.Count)
	}
}

func TestNotificationsUser_MarkReadIsSilentOnInvisibleOrDeleted(t *testing.T) {
	st := openTestAdminStore(t)
	me := seedAdminTestUser(t, st, false)
	admin := seedAdminTestUser(t, st, true)
	other := seedAdminTestUser(t, st, false)
	r := newAdminTestRouter(t, st)

	// Notification visible to `other` only.
	otherID := other.user.ID
	body := `{"title":"t","body":"b","audience_type":"user","audience_id":"` + otherID + `"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin.token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var createResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &createResp)
	invisibleID := createResp.Data.ID

	// me marks the invisible notification read → 200 silent no-op.
	req = httptest.NewRequest(http.MethodPost, "/notifications/"+invisibleID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("invisible read status=%d, want 200 (silent)", rec.Code)
	}
	// And unread_count for me stays 0.
	req = httptest.NewRequest(http.MethodGet, "/notifications/unread_count", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var countResp struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &countResp)
	if countResp.Data.Count != 0 {
		t.Fatalf("unread_count after invisible mark-read = %d, want 0", countResp.Data.Count)
	}

	// admin soft-deletes a notification; me marking it also returns 200.
	body = `{"title":"delme","body":"b","audience_type":"global"}`
	req = httptest.NewRequest(http.MethodPost, "/admin/notifications", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+admin.token)
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	json.Unmarshal(rec.Body.Bytes(), &createResp)
	delID := createResp.Data.ID
	req = httptest.NewRequest(http.MethodDelete, "/admin/notifications/"+delID, nil)
	req.Header.Set("Authorization", "Bearer "+admin.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete during test setup: status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/notifications/"+delID+"/read", nil)
	req.Header.Set("Authorization", "Bearer "+me.token)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deleted read status=%d, want 200 (silent)", rec.Code)
	}
}

func TestNotificationsUser_RequiresAuth(t *testing.T) {
	st := openTestAdminStore(t)
	r := newAdminTestRouter(t, st)

	for _, ep := range []string{"/notifications", "/notifications/unread_count"} {
		req := httptest.NewRequest(http.MethodGet, ep, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s unauthenticated status=%d, want 401", ep, rec.Code)
		}
	}
}
