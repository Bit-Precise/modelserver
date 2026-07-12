package adminv1

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/modelserver/modelserver/internal/api/contract"
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
	// list all
	listAllItems          []types.Notification
	listAllTotal          int
	listAllErr            error
	lastListAllIncluded   bool
	lastListAllAudience   string
	lastListAllPag        types.PaginationParams
	// get
	getItem   *types.Notification
	getErr    error
	lastGetID string
	// count
	countVal        int
	countErr        error
	lastCountUserID string
	// mark read
	markReadErr        error
	lastMarkReadUserID string
	lastMarkReadID     string
	// mark all read
	markAllMarked     int
	markAllErr        error
	lastMarkAllUserID string
	// create
	createCalledWith *types.Notification
	createErr        error
	// update
	updateCalledWith struct {
		ID           string
		Title        string
		Body         string
		AudienceType string
		AudienceID   *string
	}
	updateErr error
	// delete
	deleteCalledWith string
	deleteErr        error
	// GetProjectByID / GetUserByID for audience resolution
	resolveProject *types.Project
	resolveProjectErr error
	resolveUser    *types.User
	resolveUserErr error
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

// adminNotificationsServer builds a test server with a superadmin user
// and the given fakeNotificationsStore as the Notifications subsystem.
func adminNotificationsServer(ns *fakeNotificationsStore) *Server {
	store := &fakeManagementStore{user: activeUser(true)}
	s := testServer(store)
	s.Notifications = &fullFakeNotificationsStore{fakeNotificationsStore: ns}
	return s
}

// adminRequest creates an authenticated request with admin authorization.
func adminRequest(method, target string) *http.Request {
	return authenticatedRequest(method, target)
}

// fullFakeNotificationsStore satisfies the full notificationsStore interface by
// embedding fakeNotificationsStore for the 4 user methods and stubbing out the
// admin-only methods.
type fullFakeNotificationsStore struct {
	*fakeNotificationsStore
}

func (f *fullFakeNotificationsStore) ListAllNotifications(includeDeleted bool, audienceType string, p types.PaginationParams) ([]types.Notification, int, error) {
	f.lastListAllIncluded = includeDeleted
	f.lastListAllAudience = audienceType
	f.lastListAllPag = p
	return f.listAllItems, f.listAllTotal, f.listAllErr
}
func (f *fullFakeNotificationsStore) GetNotification(id string) (*types.Notification, error) {
	f.lastGetID = id
	return f.getItem, f.getErr
}
func (f *fullFakeNotificationsStore) CreateNotification(n *types.Notification) error {
	f.createCalledWith = n
	return f.createErr
}
func (f *fullFakeNotificationsStore) UpdateNotification(id, title, body, audienceType string, audienceID *string) error {
	f.updateCalledWith.ID = id
	f.updateCalledWith.Title = title
	f.updateCalledWith.Body = body
	f.updateCalledWith.AudienceType = audienceType
	f.updateCalledWith.AudienceID = audienceID
	return f.updateErr
}
func (f *fullFakeNotificationsStore) SoftDeleteNotification(id string) error {
	f.deleteCalledWith = id
	return f.deleteErr
}
func (f *fullFakeNotificationsStore) GetProjectByID(_ string) (*types.Project, error) {
	return f.resolveProject, f.resolveProjectErr
}
func (f *fullFakeNotificationsStore) GetUserByID(_ string) (*types.User, error) {
	return f.resolveUser, f.resolveUserErr
}

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

// Test 8: listAllNotifications empty audience_type → success, all rows
func TestListAllNotificationsHappyPath(t *testing.T) {
	item := types.Notification{ID: "admin-notif-1", Title: "Admin Alert", Body: "System event"}
	ns := &fakeNotificationsStore{
		listAllItems: []types.Notification{item},
		listAllTotal: 1,
	}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodGet, "/api/v1/admin/notifications"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.lastListAllIncluded != false {
		t.Fatalf("includeDeleted = %v, want false", ns.lastListAllIncluded)
	}
	if ns.lastListAllAudience != "" {
		t.Fatalf("audienceType = %q, want empty", ns.lastListAllAudience)
	}
	var body ListResponse[types.Notification]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "admin-notif-1" {
		t.Fatalf("response data = %#v", body.Data)
	}
	if body.Meta.Total != 1 {
		t.Fatalf("meta.total = %d, want 1", body.Meta.Total)
	}
}

// Test 9: listAllNotifications invalid audience_type → 400 invalid_input
func TestListAllNotificationsInvalidAudienceType(t *testing.T) {
	ns := &fakeNotificationsStore{}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodGet, "/api/v1/admin/notifications?audience_type=invalid"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	var errResp contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Payload.Code != "invalid_input" {
		t.Fatalf("error code = %q, want invalid_input", errResp.Payload.Code)
	}
	if !strings.Contains(errResp.Payload.Message, "audience_type filter must be one of") {
		t.Fatalf("error message = %q, want message about audience_type", errResp.Payload.Message)
	}
}

// Test 10: listAllNotifications store error → 500
func TestListAllNotificationsStoreError(t *testing.T) {
	ns := &fakeNotificationsStore{listAllErr: errors.New("db failure")}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodGet, "/api/v1/admin/notifications"))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", recorder.Code, recorder.Body.String())
	}
}

// Test 11: getNotification pgx.ErrNoRows → 404 not_found
func TestGetNotificationNotFound(t *testing.T) {
	// Return pgx.ErrNoRows to simulate "not found" case
	ns := &fakeNotificationsStore{getItem: nil, getErr: pgx.ErrNoRows}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodGet, "/api/v1/admin/notifications/notif-999"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
	}
	var errResp contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Payload.Code != "not_found" {
		t.Fatalf("error code = %q, want not_found", errResp.Payload.Code)
	}
}

// Test 12: getNotification happy path → 200 with {data: notification}
func TestGetNotificationHappyPath(t *testing.T) {
	item := types.Notification{ID: "admin-notif-42", Title: "Alert", Body: "Event"}
	ns := &fakeNotificationsStore{getItem: &item}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodGet, "/api/v1/admin/notifications/admin-notif-42"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.lastGetID != "admin-notif-42" {
		t.Fatalf("notification id = %q, want %q", ns.lastGetID, "admin-notif-42")
	}
	var body DataResponse[types.Notification]
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data.ID != "admin-notif-42" {
		t.Fatalf("response data.id = %q, want %q", body.Data.ID, "admin-notif-42")
	}
}

// authenticatedJSONRequest creates an authenticated POST/PUT request with a JSON body.
func authenticatedJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")
	return req
}

// Test 13: createNotification — validation error (invalid audience_type) → 400 invalid_input
func TestCreateNotificationValidationError(t *testing.T) {
	ns := &fakeNotificationsStore{}
	recorder := httptest.NewRecorder()
	body := `{"title":"Hello","body":"World","audience_type":"INVALID"}`
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, authenticatedJSONRequest(http.MethodPost, "/api/v1/admin/notifications", body))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	var errResp contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Payload.Code != "invalid_input" {
		t.Fatalf("error code = %q, want invalid_input", errResp.Payload.Code)
	}
}

// Test 14: createNotification — audience resolve: project not found → 400 invalid_audience
func TestCreateNotificationAudienceProjectNotFound(t *testing.T) {
	ns := &fakeNotificationsStore{
		resolveProject: nil, // project not found
	}
	recorder := httptest.NewRecorder()
	projectID := "proj-999"
	body := `{"title":"Hello","body":"World","audience_type":"project","audience_id":"` + projectID + `"}`
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, authenticatedJSONRequest(http.MethodPost, "/api/v1/admin/notifications", body))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	var errResp contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Payload.Code != "invalid_audience" {
		t.Fatalf("error code = %q, want invalid_audience", errResp.Payload.Code)
	}
	if !strings.Contains(errResp.Payload.Message, "not found") {
		t.Fatalf("error message = %q, want message about not found", errResp.Payload.Message)
	}
}

// Test 15: createNotification — audience resolve: project archived → 400 invalid_audience
func TestCreateNotificationAudienceProjectArchived(t *testing.T) {
	ns := &fakeNotificationsStore{
		resolveProject: &types.Project{ID: "proj-archived", Status: types.ProjectStatusArchived},
	}
	recorder := httptest.NewRecorder()
	body := `{"title":"Hello","body":"World","audience_type":"project","audience_id":"proj-archived"}`
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, authenticatedJSONRequest(http.MethodPost, "/api/v1/admin/notifications", body))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", recorder.Code, recorder.Body.String())
	}
	var errResp contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Payload.Code != "invalid_audience" {
		t.Fatalf("error code = %q, want invalid_audience", errResp.Payload.Code)
	}
	if !strings.Contains(errResp.Payload.Message, "archived") {
		t.Fatalf("error message = %q, want message about archived", errResp.Payload.Message)
	}
}

// Test 16: createNotification — store error → 500 internal
func TestCreateNotificationStoreError(t *testing.T) {
	ns := &fakeNotificationsStore{createErr: errors.New("db failure")}
	recorder := httptest.NewRecorder()
	body := `{"title":"Hello","body":"World","audience_type":"global"}`
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, authenticatedJSONRequest(http.MethodPost, "/api/v1/admin/notifications", body))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", recorder.Code, recorder.Body.String())
	}
}

// Test 17: createNotification — happy path (global audience) → 200 with {data: notification}
// Asserts CreatedBy is set from the auth context (testUserID).
func TestCreateNotificationHappyPath(t *testing.T) {
	ns := &fakeNotificationsStore{}
	recorder := httptest.NewRecorder()
	body := `{"title":"Hello","body":"World","audience_type":"global"}`
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, authenticatedJSONRequest(http.MethodPost, "/api/v1/admin/notifications", body))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.createCalledWith == nil {
		t.Fatal("CreateNotification was not called")
	}
	if ns.createCalledWith.CreatedBy != testUserID {
		t.Fatalf("CreatedBy = %q, want %q", ns.createCalledWith.CreatedBy, testUserID)
	}
	if ns.createCalledWith.AudienceType != "global" {
		t.Fatalf("AudienceType = %q, want global", ns.createCalledWith.AudienceType)
	}
	var respBody DataResponse[types.Notification]
	if err := json.Unmarshal(recorder.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

// Test 18: updateNotification — happy path → 200; assert refetch triggered
func TestUpdateNotificationHappyPath(t *testing.T) {
	updated := types.Notification{ID: "notif-upd", Title: "Updated", Body: "New body", AudienceType: "global"}
	ns := &fakeNotificationsStore{getItem: &updated}
	recorder := httptest.NewRecorder()
	body := `{"title":"Updated","body":"New body","audience_type":"global"}`
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, authenticatedJSONRequest(http.MethodPut, "/api/v1/admin/notifications/notif-upd", body))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.updateCalledWith.ID != "notif-upd" {
		t.Fatalf("update ID = %q, want notif-upd", ns.updateCalledWith.ID)
	}
	// GetNotification (refetch) should have been called
	if ns.lastGetID != "notif-upd" {
		t.Fatalf("refetch GetNotification ID = %q, want notif-upd", ns.lastGetID)
	}
	var respBody DataResponse[types.Notification]
	if err := json.Unmarshal(recorder.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if respBody.Data.ID != "notif-upd" {
		t.Fatalf("response data.id = %q, want notif-upd", respBody.Data.ID)
	}
}

// Test 19: deleteNotification — pgx.ErrNoRows → 404 not_found "notification not found or already deleted"
func TestDeleteNotificationNotFound(t *testing.T) {
	ns := &fakeNotificationsStore{deleteErr: pgx.ErrNoRows}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodDelete, "/api/v1/admin/notifications/notif-gone"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", recorder.Code, recorder.Body.String())
	}
	var errResp contract.ErrorEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errResp.Payload.Code != "not_found" {
		t.Fatalf("error code = %q, want not_found", errResp.Payload.Code)
	}
	if !strings.Contains(errResp.Payload.Message, "notification not found or already deleted") {
		t.Fatalf("error message = %q", errResp.Payload.Message)
	}
}

// Test 20: deleteNotification — happy path → 200 with {data: {"deleted": true}}
// Asserts SoftDeleteNotification was called with the correct ID.
func TestDeleteNotificationHappyPath(t *testing.T) {
	ns := &fakeNotificationsStore{}
	recorder := httptest.NewRecorder()
	testRouter(adminNotificationsServer(ns)).ServeHTTP(recorder, adminRequest(http.MethodDelete, "/api/v1/admin/notifications/notif-del"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", recorder.Code, recorder.Body.String())
	}
	if ns.deleteCalledWith != "notif-del" {
		t.Fatalf("SoftDeleteNotification called with %q, want notif-del", ns.deleteCalledWith)
	}
	var respBody DataResponse[DeleteNotificationResponseData]
	if err := json.Unmarshal(recorder.Body.Bytes(), &respBody); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !respBody.Data.Deleted {
		t.Fatal("response data.deleted = false, want true")
	}
}
