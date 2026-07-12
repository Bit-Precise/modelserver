package adminv1

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// fakeAdminSuperStore records ListAllProjects and ListAllRequests calls.
type fakeAdminSuperStore struct {
	listProjectsCall *struct {
		pagination types.PaginationParams
		filters    store.ProjectListFilters
	}
	projects       []types.Project
	projectsTotal  int
	listProjectsErr error

	listRequestsCall *struct {
		pagination types.PaginationParams
		filters    store.RequestFilters
	}
	requests       []types.Request
	requestsTotal  int
	listRequestsErr error
}

func (s *fakeAdminSuperStore) ListAllProjects(p types.PaginationParams, filters store.ProjectListFilters) ([]types.Project, int, error) {
	s.listProjectsCall = &struct {
		pagination types.PaginationParams
		filters    store.ProjectListFilters
	}{
		pagination: p,
		filters:    filters,
	}
	if s.listProjectsErr != nil {
		return nil, 0, s.listProjectsErr
	}
	return s.projects, s.projectsTotal, nil
}

func (s *fakeAdminSuperStore) ListAllRequests(p types.PaginationParams, filters store.RequestFilters) ([]types.Request, int, error) {
	s.listRequestsCall = &struct {
		pagination types.PaginationParams
		filters    store.RequestFilters
	}{
		pagination: p,
		filters:    filters,
	}
	if s.listRequestsErr != nil {
		return nil, 0, s.listRequestsErr
	}
	return s.requests, s.requestsTotal, nil
}

// Other methods required by adminSuperStore interface but not used in these tests.
func (s *fakeAdminSuperStore) GetActiveSubscriptionsByProjectIDs(projectIDs []string) (map[string]*types.Subscription, error) {
	return nil, nil
}

func (s *fakeAdminSuperStore) GetProjectOwnersByProjectIDs(projectIDs []string) (map[string]*types.User, error) {
	return nil, nil
}

func (s *fakeAdminSuperStore) SumCreditsSinceByProjects(periodStarts map[string]time.Time) (map[string]float64, error) {
	return nil, nil
}

func (s *fakeAdminSuperStore) ListPlans(activeOnly bool) ([]types.Plan, error) {
	return nil, nil
}

func (s *fakeAdminSuperStore) GetRequest(id string) (*types.Request, error) {
	return nil, nil
}

// --- ListAllProjects Tests (5) ---

// Test 1: Empty result → 200 with {data: [], meta: {}}
func TestListAllProjectsEmpty(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		projects:      []types.Project{},
		projectsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	input := &ListAllProjectsInput{}
	output, err := server.listAllProjects(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllProjects() error = %v", err)
	}
	if output == nil {
		t.Fatal("expected output, got nil")
	}
	if len(output.Body.Data) != 0 {
		t.Errorf("data length = %d, want 0", len(output.Body.Data))
	}
	if output.Body.Meta.Total != 0 {
		t.Errorf("meta.total = %d, want 0", output.Body.Meta.Total)
	}
}

// Test 2: Store error → 500 internal
func TestListAllProjectsStoreError(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		listProjectsErr: errors.New("database connection failed"),
	}
	server := &Server{AdminSuper: store}

	input := &ListAllProjectsInput{}
	_, err := server.listAllProjects(context.Background(), input)

	assertStatusError(t, err, 500, "internal")
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to list projects" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to list projects")
	}
}

// Test 3: project_id filter passes through → captured in fake
func TestListAllProjectsProjectIDFilter(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		projects:      []types.Project{},
		projectsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	projectID := "123e4567-e89b-12d3-a456-426614174000"
	input := &ListAllProjectsInput{
		ProjectID: projectID,
	}
	_, err := server.listAllProjects(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllProjects() error = %v", err)
	}
	if store.listProjectsCall == nil {
		t.Fatal("listProjectsCall is nil")
	}
	if store.listProjectsCall.filters.ProjectID != projectID {
		t.Errorf("ProjectID filter = %q, want %q", store.listProjectsCall.filters.ProjectID, projectID)
	}
}

// Test 4: owner filter passes through
func TestListAllProjectsOwnerFilter(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		projects:      []types.Project{},
		projectsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	ownerID := "223e4567-e89b-12d3-a456-426614174000"
	input := &ListAllProjectsInput{
		Owner: ownerID,
	}
	_, err := server.listAllProjects(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllProjects() error = %v", err)
	}
	if store.listProjectsCall == nil {
		t.Fatal("listProjectsCall is nil")
	}
	if store.listProjectsCall.filters.CreatedBy != ownerID {
		t.Errorf("CreatedBy filter = %q, want %q", store.listProjectsCall.filters.CreatedBy, ownerID)
	}
}

// Test 5: Happy path with 2 projects → data has 2 rows
func TestListAllProjectsHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	projects := []types.Project{
		{
			ID:        "proj-1",
			Name:      "Project 1",
			CreatedBy: "user-1",
			Status:    "active",
			CreatedAt: now,
			UpdatedAt: now,
		},
		{
			ID:        "proj-2",
			Name:      "Project 2",
			CreatedBy: "user-1",
			Status:    "active",
			CreatedAt: now,
			UpdatedAt: now,
		},
	}
	store := &fakeAdminSuperStore{
		projects:      projects,
		projectsTotal: 2,
	}
	server := &Server{AdminSuper: store}

	input := &ListAllProjectsInput{}
	output, err := server.listAllProjects(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllProjects() error = %v", err)
	}
	if output == nil {
		t.Fatal("expected output, got nil")
	}
	if len(output.Body.Data) != 2 {
		t.Errorf("data length = %d, want 2", len(output.Body.Data))
	}
	if output.Body.Data[0].ID != "proj-1" || output.Body.Data[1].ID != "proj-2" {
		t.Errorf("project IDs don't match, got %v", output.Body.Data)
	}
	if output.Body.Meta.Total != 2 {
		t.Errorf("meta.total = %d, want 2", output.Body.Meta.Total)
	}
}

// --- ListAllRequests Tests (5) ---

// Test 1: Empty result → 200
func TestListAllRequestsEmpty(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		requests:      []types.Request{},
		requestsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	input := &ListAllRequestsInput{}
	output, err := server.listAllRequests(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllRequests() error = %v", err)
	}
	if output == nil {
		t.Fatal("expected output, got nil")
	}
	if len(output.Body.Data) != 0 {
		t.Errorf("data length = %d, want 0", len(output.Body.Data))
	}
	if output.Body.Meta.Total != 0 {
		t.Errorf("meta.total = %d, want 0", output.Body.Meta.Total)
	}
}

// Test 2: Store error → 500
func TestListAllRequestsStoreError(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		listRequestsErr: errors.New("database error"),
	}
	server := &Server{AdminSuper: store}

	input := &ListAllRequestsInput{}
	_, err := server.listAllRequests(context.Background(), input)

	assertStatusError(t, err, 500, "internal")
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to list requests" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to list requests")
	}
}

// Test 3: Filter passthrough for model + status → captured
func TestListAllRequestsFilterPassthrough(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		requests:      []types.Request{},
		requestsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	input := &ListAllRequestsInput{
		Model:  "gpt-4",
		Status: "success",
	}
	_, err := server.listAllRequests(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllRequests() error = %v", err)
	}
	if store.listRequestsCall == nil {
		t.Fatal("listRequestsCall is nil")
	}
	if store.listRequestsCall.filters.Model != "gpt-4" {
		t.Errorf("Model filter = %q, want %q", store.listRequestsCall.filters.Model, "gpt-4")
	}
	if store.listRequestsCall.filters.Status != "success" {
		t.Errorf("Status filter = %q, want %q", store.listRequestsCall.filters.Status, "success")
	}
}

// Test 4: Unparseable since → filter's Since stays zero time
func TestListAllRequestsUnparseableSince(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		requests:      []types.Request{},
		requestsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	input := &ListAllRequestsInput{
		Since: "not-a-valid-timestamp",
	}
	_, err := server.listAllRequests(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllRequests() error = %v", err)
	}
	if store.listRequestsCall == nil {
		t.Fatal("listRequestsCall is nil")
	}
	zeroTime := time.Time{}
	if store.listRequestsCall.filters.Since != zeroTime {
		t.Errorf("Since filter = %v, want zero time", store.listRequestsCall.filters.Since)
	}
}

// Test 5: Valid RFC3339 since + until → both parsed and captured
func TestListAllRequestsValidTimestamps(t *testing.T) {
	t.Parallel()
	store := &fakeAdminSuperStore{
		requests:      []types.Request{},
		requestsTotal: 0,
	}
	server := &Server{AdminSuper: store}

	sinceStr := "2026-01-01T00:00:00Z"
	untilStr := "2026-12-31T23:59:59Z"
	sinceTime, _ := time.Parse(time.RFC3339, sinceStr)
	untilTime, _ := time.Parse(time.RFC3339, untilStr)

	input := &ListAllRequestsInput{
		Since: sinceStr,
		Until: untilStr,
	}
	_, err := server.listAllRequests(context.Background(), input)

	if err != nil {
		t.Fatalf("listAllRequests() error = %v", err)
	}
	if store.listRequestsCall == nil {
		t.Fatal("listRequestsCall is nil")
	}
	if !store.listRequestsCall.filters.Since.Equal(sinceTime) {
		t.Errorf("Since filter = %v, want %v", store.listRequestsCall.filters.Since, sinceTime)
	}
	if !store.listRequestsCall.filters.Until.Equal(untilTime) {
		t.Errorf("Until filter = %v, want %v", store.listRequestsCall.filters.Until, untilTime)
	}
}
