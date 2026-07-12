package adminv1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// fakeNotificationsStore records the arguments passed to each call.
type fakeNotificationsStore struct {
	// list
	listItems      []types.Notification
	listTotal      int
	listErr        error
	lastListUserID string
	lastListPag    types.PaginationParams
	// count
	countVal        int
	countErr        error
	lastCountUserID string
	// mark read
	markReadErr        error
	lastMarkReadUserID string
	lastMarkReadID     string
	// mark all read
	markAllMarked       int
	markAllErr          error
	lastMarkAllUserID   string
}

func (f *fakeNotificationsStore) ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error) {
	f.lastListUserID = userID
	f.lastListPag = p
	return f.listItems, f.listTotal, f.listErr
}

func (f *fakeNotificationsStore) CountUnreadForUser(userID string) (int, error) {
	f.lastCountUserID = userID
	return f.countVal, f.countErr
}

func (f *fakeNotificationsStore) MarkNotificationRead(userID, notificationID string) error {
	f.lastMarkReadUserID = userID
	f.lastMarkReadID = notificationID
	return f.markReadErr
}

func (f *fakeNotificationsStore) MarkAllNotificationsRead(userID string) (int, error) {
	f.lastMarkAllUserID = userID
	return f.markAllMarked, f.markAllErr
}

// notificationsServer builds a test server wired with fakeNotificationsStore
// as the Notifications subsystem and the standard fakeManagementStore user.
func notificationsServer(ns *fakeNotificationsStore) *Server {
	store := &fakeManagementStore{user: activeUser(false)}
	s := testServer(store)
	s.Notifications = &fullFakeNotificationsStore{fakeNotificationsStore: ns}
	return s
}

// fullFakeNotificationsStore satisfies the full notificationsStore interface by
// embedding fakeNotificationsStore for the 4 user methods and stubbing out the
// admin-only methods.
type fullFakeNotificationsStore struct {
	*fakeNotificationsStore
}

func (f *fullFakeNotificationsStore) ListAllNotifications(_ bool, _ string, _ types.PaginationParams) ([]types.Notification, int, error) {
	return nil, 0, nil
}
func (f *fullFakeNotificationsStore) GetNotification(_ string) (*types.Notification, error) {
	return nil, nil
}
func (f *fullFakeNotificationsStore) CreateNotification(_ *types.Notification) error { return nil }
func (f *fullFakeNotificationsStore) UpdateNotification(_, _, _, _ string, _ *string) error {
	return nil
}
func (f *fullFakeNotificationsStore) SoftDeleteNotification(_ string) error { return nil }
func (f *fullFakeNotificationsStore) GetProjectByID(_ string) (*types.Project, error) {
	return nil, nil
}
func (f *fullFakeNotificationsStore) GetUserByID(_ string) (*types.User, error) { return nil, nil }

// Test 1: listMyNotifications happy path
func TestListMyNotificationsHappyPath(t *testing.T) {
	item := types.Notification{ID: "notif-1", Title: "Hello", Body: "World"}
	ns := &fakeNotificationsStore{
		listItems: []types.Notification{item},
		listTotal: 1,
	}
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/notifications"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.lastListUserID != testUserID {
		t.Fatalf("userID = %q, want %q", ns.lastListUserID, testUserID)
	}
	var body ListResponse[types.Notification]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "notif-1" {
		t.Fatalf("response data = %#v", body.Data)
	}
	if body.Meta.Total != 1 {
		t.Fatalf("meta.total = %d, want 1", body.Meta.Total)
	}
}

// Test 2: listMyNotifications store error → 500
func TestListMyNotificationsStoreError(t *testing.T) {
	ns := &fakeNotificationsStore{listErr: errors.New("db failure")}
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/notifications"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", recorder.Code, recorder.Body.String())
	}
}

// Test 3: unreadNotificationCount happy path
func TestUnreadNotificationCountHappyPath(t *testing.T) {
	ns := &fakeNotificationsStore{countVal: 7}
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/notifications/unread_count"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.lastCountUserID != testUserID {
		t.Fatalf("userID = %q, want %q", ns.lastCountUserID, testUserID)
	}
	var body struct {
		Data struct {
			Count int `json:"count"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Count != 7 {
		t.Fatalf("count = %d, want 7", body.Data.Count)
	}
}

// Test 4: unreadNotificationCount store error → 500
func TestUnreadNotificationCountStoreError(t *testing.T) {
	ns := &fakeNotificationsStore{countErr: errors.New("db failure")}
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodGet, "/api/v1/notifications/unread_count"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", recorder.Code, recorder.Body.String())
	}
}

// Test 5: markNotificationRead happy path (silent 200 on unknown id preserved)
func TestMarkNotificationReadHappyPath(t *testing.T) {
	ns := &fakeNotificationsStore{markReadErr: nil} // silent nil for any id
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/api/v1/notifications/notif-42/read"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.lastMarkReadUserID != testUserID {
		t.Fatalf("userID = %q, want %q", ns.lastMarkReadUserID, testUserID)
	}
	if ns.lastMarkReadID != "notif-42" {
		t.Fatalf("notification id = %q, want %q", ns.lastMarkReadID, "notif-42")
	}
	var body struct {
		Data struct {
			OK bool `json:"ok"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Data.OK {
		t.Fatalf("ok = false, want true")
	}
}

// Test 6: markAllNotificationsRead happy path
func TestMarkAllNotificationsReadHappyPath(t *testing.T) {
	ns := &fakeNotificationsStore{markAllMarked: 3}
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/api/v1/notifications/read_all"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.lastMarkAllUserID != testUserID {
		t.Fatalf("userID = %q, want %q", ns.lastMarkAllUserID, testUserID)
	}
	var body struct {
		Data struct {
			Marked int `json:"marked"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.Marked != 3 {
		t.Fatalf("marked = %d, want 3", body.Data.Marked)
	}
}

// Test 7: markAllNotificationsRead store error → 500
func TestMarkAllNotificationsReadStoreError(t *testing.T) {
	ns := &fakeNotificationsStore{markAllErr: errors.New("db failure")}
	recorder := httptest.NewRecorder()
	testRouter(notificationsServer(ns)).ServeHTTP(recorder, authenticatedRequest(http.MethodPost, "/api/v1/notifications/read_all"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", recorder.Code, recorder.Body.String())
	}
}
