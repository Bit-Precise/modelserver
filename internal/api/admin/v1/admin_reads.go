package adminv1

import (
	"bytes"
	"context"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/ratelimit"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// ListAllProjectsInput represents the query parameters for GET /api/v1/admin/projects.
type ListAllProjectsInput struct {
	Page      int    `query:"page" default:"1" minimum:"1"`
	PerPage   int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort      string `query:"sort" default:"created_at"`
	Order     string `query:"order" default:"desc" enum:"asc,desc"`
	ProjectID string `query:"project_id,omitempty" format:"uuid"`
	Owner     string `query:"owner,omitempty" format:"uuid"`
}

func (input *ListAllProjectsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

// ListAllProjectsOutput represents the response envelope for GET /api/v1/admin/projects.
type ListAllProjectsOutput struct {
	Body ListResponse[Project]
}

// ListAllRequestsInput represents the query parameters for GET /api/v1/admin/requests.
type ListAllRequestsInput struct {
	Page        int    `query:"page" default:"1" minimum:"1"`
	PerPage     int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort        string `query:"sort" default:"created_at"`
	Order       string `query:"order" default:"desc" enum:"asc,desc"`
	Model       string `query:"model,omitempty"`
	RequestKind string `query:"request_kind,omitempty"`
	Status      string `query:"status,omitempty"`
	CreatedBy   string `query:"created_by,omitempty"`
	Since       string `query:"since,omitempty" doc:"RFC3339 lower bound (inclusive)"`
	Until       string `query:"until,omitempty" doc:"RFC3339 upper bound (inclusive)"`
}

func (input *ListAllRequestsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

// ListAllRequestsOutput represents the response envelope for GET /api/v1/admin/requests.
type ListAllRequestsOutput struct {
	Body ListResponse[types.Request]
}

// registerAdminReadOperations registers the admin global read operations.
func registerAdminReadOperations(api huma.API, server *Server) {
	projectsAccess := authz.System(authz.PermissionSystemProjectsRead)
	requestsAccess := authz.System(authz.PermissionSystemRequestsRead)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "listAllProjects",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/projects",
		Summary:       "List all projects",
		Tags:          []string{"Admin"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        projectsAccess,
		Authorize:     server.authorizationMiddleware,
	}, server.listAllProjects)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "listAllRequests",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/requests",
		Summary:       "List all requests",
		Tags:          []string{"Admin"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        requestsAccess,
		Authorize:     server.authorizationMiddleware,
	}, server.listAllRequests)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "adminProjectsSubscriptionsOverview",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/projects/subscriptions-overview",
		Summary:       "Get subscriptions overview for projects",
		Tags:          []string{"Admin"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        projectsAccess,
		Authorize:     server.authorizationMiddleware,
	}, server.adminProjectsSubscriptionsOverview)

	contract.Register(api, contract.Operation{
		ID:            "getAdminHttpLog",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/requests/{requestID}/http-log",
		Summary:       "Get HTTP log for a request",
		Tags:          []string{"Admin"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusInternalServerError},
		Access:        requestsAccess,
		Authorize:     server.authorizationMiddleware,
	}, server.getAdminHttpLog)
}

// GetAdminHttpLogInput represents the path parameters for
// GET /api/v1/admin/requests/{requestID}/http-log.
type GetAdminHttpLogInput struct {
	RequestID string `path:"requestID" doc:"Request identifier."`
}

// GetAdminHttpLogOutput carries the raw http log bytes with an explicit
// Content-Type override to application/json (instead of the default
// application/octet-stream that contract.BytesResponse would otherwise emit).
type GetAdminHttpLogOutput struct {
	ContentType   string `header:"Content-Type"`
	ContentLength int64  `header:"Content-Length,omitempty"`
	Body          contract.BytesResponse
}

// getAdminHttpLog handles GET /api/v1/admin/requests/{requestID}/http-log.
//
// Project-membership check omitted intentionally: the legacy handleGetHttpLog
// had a check (lines 30–37) that was gated on chi.URLParam(r, "projectID") != "".
// The superadmin path (/admin/requests/{requestID}/http-log) never carries a
// projectID segment, so the check was always inert on this variant. The
// project-scoped variant (/projects/{projectID}/requests/{requestID}/http-log)
// remains on chi until Batch 12.
func (s *Server) getAdminHttpLog(ctx context.Context, input *GetAdminHttpLogInput) (*GetAdminHttpLogOutput, error) {
	if s == nil || s.AdminSuper == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "admin store is not configured", nil)
	}
	if s.HTTPLog == nil {
		return nil, contract.NewError(http.StatusServiceUnavailable, "unavailable", "http logging is not configured", nil)
	}
	req, err := s.AdminSuper.GetRequest(input.RequestID)
	if err != nil || req == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "request not found", nil)
	}
	if req.HttpLogPath == "" {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "no http log available for this request", nil)
	}
	data, err := s.HTTPLog.Retrieve(ctx, req.HttpLogPath)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to retrieve http log", nil)
	}
	return &GetAdminHttpLogOutput{
		ContentType:   "application/json",
		ContentLength: int64(len(data)),
		Body:          contract.BytesResponse{Reader: bytes.NewReader(data)},
	}, nil
}

// listAllProjects handles GET /api/v1/admin/projects with optional filters.
// Returns all projects with pagination and optional project_id/owner filters.
func (s *Server) listAllProjects(_ context.Context, input *ListAllProjectsInput) (*ListAllProjectsOutput, error) {
	if s == nil || s.AdminSuper == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "admin store is not configured", nil)
	}

	pagination := input.pagination()
	filters := store.ProjectListFilters{
		ProjectID: input.ProjectID,
		CreatedBy: input.Owner,
	}

	projects, total, err := s.AdminSuper.ListAllProjects(pagination, filters)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list projects", nil)
	}

	if projects == nil {
		projects = []types.Project{}
	}

	// Convert internal types.Project to admin/v1 Project DTO
	items := make([]Project, len(projects))
	for i := range projects {
		items[i] = projectDTO(&projects[i])
	}

	return &ListAllProjectsOutput{Body: ListResponse[Project]{
		Data: items,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

// ProjectOwnerSnapshot is the minimal owner info returned in the subscriptions
// overview. Exported so it appears in the OpenAPI schema.
type ProjectOwnerSnapshot struct {
	ID       string `json:"id"`
	Email    string `json:"email,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Picture  string `json:"picture,omitempty"`
}

// ProjectSubscriptionOverview is the per-project payload returned by the
// admin subscriptions-overview endpoint. Exported for OpenAPI schema.
type ProjectSubscriptionOverview struct {
	ProjectID   string                         `json:"project_id"`
	PlanID      string                         `json:"plan_id,omitempty"`
	PlanName    string                         `json:"plan_name,omitempty"`
	DisplayName string                         `json:"display_name,omitempty"`
	Windows     []ratelimit.CreditWindowStatus `json:"windows" nullable:"false"`
	Owner       *ProjectOwnerSnapshot          `json:"owner,omitempty"`
	// PeriodCreditsK is credits consumed since the active subscription's
	// StartsAt, rounded to integer thousands. Absent when there is no
	// active subscription.
	PeriodCreditsK *int64 `json:"period_credits_k,omitempty"`
}

// AdminProjectsSubscriptionsOverviewInput represents the query parameters for
// GET /api/v1/admin/projects/subscriptions-overview.
type AdminProjectsSubscriptionsOverviewInput struct {
	ProjectIDs string `query:"project_ids,omitempty" doc:"Comma-separated project IDs."`
}

// AdminProjectsSubscriptionsOverviewOutput represents the response envelope for
// GET /api/v1/admin/projects/subscriptions-overview.
type AdminProjectsSubscriptionsOverviewOutput struct {
	Body struct {
		Data []ProjectSubscriptionOverview `json:"data" nullable:"false"`
	}
}

// listAllRequests handles GET /api/v1/admin/requests with optional filters.
// Returns all requests with pagination and optional filters for model, request_kind, status, created_by.
// Silently ignores unparseable since/until RFC3339 timestamps, falling back to zero time.
func (s *Server) listAllRequests(_ context.Context, input *ListAllRequestsInput) (*ListAllRequestsOutput, error) {
	if s == nil || s.AdminSuper == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "admin store is not configured", nil)
	}

	pagination := input.pagination()
	filters := store.RequestFilters{
		Model:       input.Model,
		RequestKind: input.RequestKind,
		Status:      input.Status,
		CreatedBy:   input.CreatedBy,
	}

	// Parse RFC3339 timestamps, silently ignoring errors and falling back to zero time.
	if input.Since != "" {
		if t, err := time.Parse(time.RFC3339, input.Since); err == nil {
			filters.Since = t
		}
	}
	if input.Until != "" {
		if t, err := time.Parse(time.RFC3339, input.Until); err == nil {
			filters.Until = t
		}
	}

	requests, total, err := s.AdminSuper.ListAllRequests(pagination, filters)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list requests", nil)
	}

	if requests == nil {
		requests = []types.Request{}
	}

	return &ListAllRequestsOutput{Body: ListResponse[types.Request]{
		Data: requests,
		Meta: paginationMeta(total, pagination),
	}}, nil
}

// adminProjectsSubscriptionsOverview handles
// GET /api/v1/admin/projects/subscriptions-overview.
// Returns active subscription + credit window usage for many projects in a
// single response. Query param: project_ids (comma-separated). Empty → 200 {data:[]}.
func (s *Server) adminProjectsSubscriptionsOverview(_ context.Context, input *AdminProjectsSubscriptionsOverviewInput) (*AdminProjectsSubscriptionsOverviewOutput, error) {
	if s == nil || s.AdminSuper == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "admin store is not configured", nil)
	}

	raw := strings.TrimSpace(input.ProjectIDs)
	empty := &AdminProjectsSubscriptionsOverviewOutput{}
	empty.Body.Data = []ProjectSubscriptionOverview{}
	if raw == "" {
		return empty, nil
	}
	projectIDs := make([]string, 0, 16)
	for _, id := range strings.Split(raw, ",") {
		id = strings.TrimSpace(id)
		if id != "" {
			projectIDs = append(projectIDs, id)
		}
	}
	if len(projectIDs) == 0 {
		return empty, nil
	}

	activeSubs, err := s.AdminSuper.GetActiveSubscriptionsByProjectIDs(projectIDs)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to load subscriptions", nil)
	}

	owners, err := s.AdminSuper.GetProjectOwnersByProjectIDs(projectIDs)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to load project owners", nil)
	}

	// Per-project credits since the active subscription's StartsAt.
	// Projects without an active subscription are simply omitted.
	periodStarts := make(map[string]time.Time, len(activeSubs))
	for pid, sub := range activeSubs {
		if sub != nil {
			periodStarts[pid] = sub.StartsAt
		}
	}
	periodCredits, err := s.AdminSuper.SumCreditsSinceByProjects(periodStarts)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to load period credits", nil)
	}

	plans, err := s.AdminSuper.ListPlans(false)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to load plans", nil)
	}
	// subscription.PlanName stores the plan slug. Plan.Name is the human-facing tier name.
	plansBySlug := make(map[string]*types.Plan, len(plans))
	for i := range plans {
		plansBySlug[plans[i].Slug] = &plans[i]
	}

	// Bucket (projectID, rule) by windowStart so we can issue one aggregate
	// query per unique window start across all projects.
	type ruleRef struct {
		window      string
		maxCred     int64
		windowTyp   string
		anchor      *time.Time
		windowStart time.Time
	}
	bucketsByStart := make(map[time.Time]map[string]struct{}) // windowStart -> set of projectIDs
	rulesByProject := make(map[string][]ruleRef, len(projectIDs))

	for _, pid := range projectIDs {
		sub := activeSubs[pid]
		if sub == nil {
			continue
		}
		plan := plansBySlug[sub.PlanName]
		if plan == nil {
			continue
		}
		policy := plan.ToPolicy(pid, &sub.StartsAt)
		for _, rule := range policy.CreditRules {
			ws := ratelimit.WindowStartTime(rule.Window, rule.WindowType, rule.AnchorTime)
			if bucketsByStart[ws] == nil {
				bucketsByStart[ws] = make(map[string]struct{})
			}
			bucketsByStart[ws][pid] = struct{}{}
			rulesByProject[pid] = append(rulesByProject[pid], ruleRef{
				window:      rule.Window,
				maxCred:     rule.MaxCredits,
				windowTyp:   rule.WindowType,
				anchor:      rule.AnchorTime,
				windowStart: ws,
			})
		}
	}

	// One SUM query per unique windowStart across all projects in that bucket.
	// Keyed by (projectID, windowStart) so duplicate window names on the
	// same project (rare but possible) don't collide.
	type usageKey struct {
		projectID   string
		windowStart time.Time
	}
	usedByRule := make(map[usageKey]float64)
	for ws, pidSet := range bucketsByStart {
		pids := make([]string, 0, len(pidSet))
		for pid := range pidSet {
			pids = append(pids, pid)
		}
		sums, err := s.AdminSuper.SumCreditsInWindowByProjects(pids, ws)
		if err != nil {
			// Silently ignored per legacy behavior — the row shows 0 used.
			continue
		}
		for pid, total := range sums {
			usedByRule[usageKey{pid, ws}] = total
		}
	}

	out := make([]ProjectSubscriptionOverview, 0, len(projectIDs))
	for _, pid := range projectIDs {
		row := ProjectSubscriptionOverview{ProjectID: pid, Windows: []ratelimit.CreditWindowStatus{}}
		sub := activeSubs[pid]
		if sub != nil {
			row.PlanID = sub.PlanID
			row.PlanName = sub.PlanName
			if plan := plansBySlug[sub.PlanName]; plan != nil {
				row.DisplayName = plan.DisplayName
			}
		}
		if owner := owners[pid]; owner != nil {
			row.Owner = &ProjectOwnerSnapshot{
				ID:       owner.ID,
				Email:    owner.Email,
				Nickname: owner.Nickname,
				Picture:  owner.Picture,
			}
		}
		if sub != nil {
			// Round to integer thousands at the API boundary.
			k := int64(math.Round(periodCredits[pid] / 1000))
			row.PeriodCreditsK = &k
		}
		for _, rr := range rulesByProject[pid] {
			used := usedByRule[usageKey{pid, rr.windowStart}]
			percentage := 0.0
			if rr.maxCred > 0 {
				percentage = (used / float64(rr.maxCred)) * 100
				if percentage > 100 {
					percentage = 100
				}
			}
			percentage = math.Round(percentage*100) / 100
			s := ratelimit.CreditWindowStatus{
				Window:     rr.window,
				Percentage: percentage,
			}
			if rr.windowTyp == types.WindowTypeCalendar || rr.windowTyp == types.WindowTypeFixed {
				resetDur := ratelimit.WindowResetDuration(rr.window, rr.windowTyp, rr.anchor)
				s.ResetsAt = time.Now().UTC().Add(resetDur).Format(time.RFC3339)
			}
			row.Windows = append(row.Windows, s)
		}
		out = append(out, row)
	}

	output := &AdminProjectsSubscriptionsOverviewOutput{}
	output.Body.Data = out
	return output, nil
}
