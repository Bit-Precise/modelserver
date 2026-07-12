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

// fakeAdminSuperStore records ListAllProjects and ListAllRequests calls,
// and supports stubbing for subscriptions-overview tests.
type fakeAdminSuperStore struct {
	listProjectsCall *struct {
		pagination types.PaginationParams
		filters    store.ProjectListFilters
	}
	projects        []types.Project
	projectsTotal   int
	listProjectsErr error

	listRequestsCall *struct {
		pagination types.PaginationParams
		filters    store.RequestFilters
	}
	requests        []types.Request
	requestsTotal   int
	listRequestsErr error

	// subscriptions-overview stubs
	activeSubsProjectIDs  []string
	activeSubs            map[string]*types.Subscription
	activeSubsErr         error
	projectOwners         map[string]*types.User
	projectOwnersErr      error
	periodCredits         map[string]float64
	sumCreditsSinceErr    error
	windowCredits         map[string]float64
	sumCreditsInWindowErr error
	plans                 []types.Plan
	listPlansErr          error
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
	s.activeSubsProjectIDs = projectIDs
	if s.activeSubsErr != nil {
		return nil, s.activeSubsErr
	}
	return s.activeSubs, nil
}

func (s *fakeAdminSuperStore) GetProjectOwnersByProjectIDs(projectIDs []string) (map[string]*types.User, error) {
	if s.projectOwnersErr != nil {
		return nil, s.projectOwnersErr
	}
	return s.projectOwners, nil
}

func (s *fakeAdminSuperStore) SumCreditsSinceByProjects(periodStarts map[string]time.Time) (map[string]float64, error) {
	if s.sumCreditsSinceErr != nil {
		return nil, s.sumCreditsSinceErr
	}
	return s.periodCredits, nil
}

func (s *fakeAdminSuperStore) SumCreditsInWindowByProjects(projectIDs []string, windowStart time.Time) (map[string]float64, error) {
	if s.sumCreditsInWindowErr != nil {
		return nil, s.sumCreditsInWindowErr
	}
	return s.windowCredits, nil
}

func (s *fakeAdminSuperStore) ListPlans(activeOnly bool) ([]types.Plan, error) {
	if s.listPlansErr != nil {
		return nil, s.listPlansErr
	}
	return s.plans, nil
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

// --- AdminProjectsSubscriptionsOverview Tests (4) ---

// Test 1: Empty project_ids → 200 with {data: []}
func TestAdminProjectsSubscriptionsOverviewEmpty(t *testing.T) {
	t.Parallel()
	st := &fakeAdminSuperStore{}
	server := &Server{AdminSuper: st}

	input := &AdminProjectsSubscriptionsOverviewInput{ProjectIDs: ""}
	output, err := server.adminProjectsSubscriptionsOverview(context.Background(), input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if output == nil {
		t.Fatal("expected output, got nil")
	}
	if len(output.Body.Data) != 0 {
		t.Errorf("data length = %d, want 0", len(output.Body.Data))
	}
}

// Test 2: Whitespace-only project_ids → 200 with {data: []}
func TestAdminProjectsSubscriptionsOverviewWhitespace(t *testing.T) {
	t.Parallel()
	st := &fakeAdminSuperStore{}
	server := &Server{AdminSuper: st}

	input := &AdminProjectsSubscriptionsOverviewInput{ProjectIDs: "   "}
	output, err := server.adminProjectsSubscriptionsOverview(context.Background(), input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(output.Body.Data) != 0 {
		t.Errorf("data length = %d, want 0", len(output.Body.Data))
	}
}

// Test 3: Store error on GetActiveSubscriptionsByProjectIDs → 500 "failed to load subscriptions"
func TestAdminProjectsSubscriptionsOverviewSubsError(t *testing.T) {
	t.Parallel()
	st := &fakeAdminSuperStore{
		activeSubsErr: errors.New("db error"),
	}
	server := &Server{AdminSuper: st}

	input := &AdminProjectsSubscriptionsOverviewInput{ProjectIDs: "proj-1"}
	_, err := server.adminProjectsSubscriptionsOverview(context.Background(), input)

	assertStatusError(t, err, 500, "internal")
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to load subscriptions" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to load subscriptions")
	}
}

// Test 4: Happy path with 1 project, active sub, owner, 1 credit rule → proper row
func TestAdminProjectsSubscriptionsOverviewHappyPath(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	pid := "proj-happy"

	plan := types.Plan{
		ID:          "plan-1",
		Name:        "pro",
		Slug:        "pro",
		DisplayName: "Pro Plan",
		CreditRules: []types.CreditRule{
			{Window: "1M", WindowType: types.WindowTypeCalendar, MaxCredits: 1000000},
		},
	}
	sub := &types.Subscription{
		ID:        "sub-1",
		ProjectID: pid,
		PlanID:    plan.ID,
		PlanName:  plan.Slug,
		Status:    "active",
		StartsAt:  now,
	}
	owner := &types.User{
		ID:       "user-1",
		Email:    "owner@example.com",
		Nickname: "owner",
	}

	st := &fakeAdminSuperStore{
		activeSubs:    map[string]*types.Subscription{pid: sub},
		projectOwners: map[string]*types.User{pid: owner},
		periodCredits: map[string]float64{pid: 500000},
		windowCredits: map[string]float64{pid: 200000},
		plans:         []types.Plan{plan},
	}
	server := &Server{AdminSuper: st}

	input := &AdminProjectsSubscriptionsOverviewInput{ProjectIDs: pid}
	output, err := server.adminProjectsSubscriptionsOverview(context.Background(), input)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(output.Body.Data) != 1 {
		t.Fatalf("data length = %d, want 1", len(output.Body.Data))
	}

	row := output.Body.Data[0]
	if row.ProjectID != pid {
		t.Errorf("ProjectID = %q, want %q", row.ProjectID, pid)
	}
	if row.PlanID != plan.ID {
		t.Errorf("PlanID = %q, want %q", row.PlanID, plan.ID)
	}
	if row.PlanName != plan.Slug {
		t.Errorf("PlanName = %q, want %q", row.PlanName, plan.Slug)
	}
	if row.DisplayName != plan.DisplayName {
		t.Errorf("DisplayName = %q, want %q", row.DisplayName, plan.DisplayName)
	}
	if row.Owner == nil {
		t.Fatal("Owner is nil, want non-nil")
	}
	if row.Owner.Email != owner.Email {
		t.Errorf("Owner.Email = %q, want %q", row.Owner.Email, owner.Email)
	}
	if row.PeriodCreditsK == nil {
		t.Fatal("PeriodCreditsK is nil, want non-nil")
	}
	if *row.PeriodCreditsK != 500 {
		t.Errorf("PeriodCreditsK = %d, want 500", *row.PeriodCreditsK)
	}
	if row.Windows == nil {
		t.Fatal("Windows is nil, want empty slice (not nil)")
	}
	if len(row.Windows) != 1 {
		t.Fatalf("Windows length = %d, want 1", len(row.Windows))
	}
	if row.Windows[0].Window != "1M" {
		t.Errorf("Windows[0].Window = %q, want %q", row.Windows[0].Window, "1M")
	}
	// calendar window type → ResetsAt must be set
	if row.Windows[0].ResetsAt == "" {
		t.Error("Windows[0].ResetsAt is empty, want RFC3339 string for calendar window")
	}
}
