# Huma Admin API — Batch 5: Admin global reads + Notifications user + Notifications admin

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate 12 routes onto the Huma-typed contract in three subsystems: **E — Admin global reads** (4 routes, superadmin), **F user — Notifications inbox** (4 routes, authenticated), **F admin — Notifications management** (4 routes, superadmin).

**Architecture:** Follow the batches-02-14 template. First real user of `authz.Authenticated()` (user inbox) and `contract.BytesResponse` (E4 http-log download — with a Content-Type header field to override the format's default). First 2 new permissions consumed (`system.notifications.read/manage`, pre-added in Batch 1). Notifications write paths preserve two legacy wire quirks: POST returns 200 (not 201), DELETE returns `{data:{deleted:true}}` (not 204).

**Tech Stack:** Go 1.24, Huma v2, chi v5. No new deps.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md` §5 rows E1–E4, F1–F8.
- **Wire compatibility:** exact preservation of the 12 legacy handlers' bodies, status codes, and error-code strings. Envelope fixture tests guard.
- **Legacy wire quirks preserved:**
  - `POST /admin/notifications` returns **200** (not 201) with `{data: notification}` — preserve
  - `DELETE /admin/notifications/{id}` returns **200** with `{data: {"deleted": true}}` (not 204 no-body) — preserve
  - `POST /notifications/{id}/read` returns `{data: {"ok": true}}`
  - `POST /notifications/read_all` returns `{data: {"marked": N}}`
  - `GET /notifications/unread_count` returns `{data: {"count": N}}`
  - Notifications validation errors use code `invalid_input` (not `bad_request`)
  - Notifications audience errors use code `invalid_audience`
  - `MarkNotificationRead` silently returns success on unknown/deleted id (documented "silent 200 on unknown id" contract)
- **`handleGetHttpLog` is DUAL-USE in legacy** (superadmin `/admin/requests/{id}/http-log` AND project-scoped `/projects/{id}/requests/{rid}/http-log`). Batch 5 migrates ONLY the superadmin path (E4). Batch 12 migrates the project-scoped variant. **The legacy handler function stays intact** until Batch 12 also completes.
- **`contract.BytesResponse` content-type override:** E4 returns `application/json` (the raw JSON bytes of the captured http log), not `application/octet-stream`. The output struct declares a `ContentType string` field with `header:"Content-Type"` tag which Huma will emit as the response header, overriding `BytesResponse`'s default.
- **Dashboard hook migration deferred** — existing `dashboard/src/api/notifications.ts` (if it exists) continues using its React Query hooks; wire preserved.
- **Every commit:** message ends with `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`.

---

## File Structure

**Files added:**
- `internal/api/admin/v1/admin_reads.go` — E1-E4 DTOs + handlers + register
- `internal/api/admin/v1/admin_reads_test.go`
- `internal/api/admin/v1/notifications.go` — F1-F8 DTOs + handlers + register
- `internal/api/admin/v1/notifications_test.go`
- `internal/api/admin/v1/notification_helpers.go` — duplicated `validateNotificationPayload`, `resolveAudience`
- `internal/api/admin/v1/admin_reads_envelopes_test.go` + `notifications_envelopes_test.go`

**Files modified:**
- `internal/api/admin/v1/subsystem_stores.go` — flesh out `adminSuperStore` (E subsystem) and `notificationsStore` (F subsystem)
- `internal/api/admin/v1/server.go` — add `AdminSuper adminSuperStore`, `Notifications notificationsStore`, `HTTPLog *httplog.Logger` fields
- `internal/api/admin/v1/operations.go` — call `registerAdminReadOperations`, `registerNotificationOperations`
- `internal/admin/routes.go` — delete 12 chi lines across `/admin/projects`, `/admin/requests`, `/notifications`, `/admin/notifications` blocks; drop empty enclosing `r.Route(...)` blocks per Batch 3-4 pattern
- `cmd/modelserver/main.go` — wire the three new Server fields
- `cmd/modelserver/admin_routes_test.go` — extend `routeTestStore` with new interface methods
- `internal/api/contract/invariants_test.go` — add `TestBatch05NoLegacyChiOverlap`
- `api/openapi/admin.openapi.json` — regenerated in Task 8
- `dashboard/src/api/generated/schema.ts` — regenerated in Task 10
- `docs/admin-api-openapi-rbac.md` — append the 12 migrated operations

**Files NOT touched:**
- `internal/admin/handle_projects.go` (except deletion of the two admin route lines) — the `projectOwnerSnapshot`, `projectSubscriptionOverview` types + subscription-overview business logic stay in the legacy file **for now**; typed handler duplicates just what it needs
- `internal/admin/handle_requests.go`, `handle_http_log.go`, `handle_notifications.go` — handler bodies retained until Batch 14 (except: `handleGetHttpLog` stays because Batch 12's project-scoped variant is still legacy)

---

## Task 1: Store interfaces + Server surface

**Files:**
- Modify: `internal/api/admin/v1/subsystem_stores.go` (replace stubs for `adminSuperStore` E and `notificationsStore` F)
- Modify: `internal/api/admin/v1/server.go` (add fields)
- Modify: `cmd/modelserver/admin_routes_test.go` (extend `routeTestStore` with stubs if compile fails)

**Interfaces produced:**

```go
// E — Admin global reads (superadmin)
type adminSuperStore interface {
    ListAllProjects(p types.PaginationParams, filters store.ProjectListFilters) ([]types.Project, int, error)
    GetActiveSubscriptionsByProjectIDs(projectIDs []string) (map[string]*types.Subscription, error)
    GetProjectOwnersByProjectIDs(projectIDs []string) (map[string]*types.User, error)
    SumCreditsSinceByProjects(periodStarts map[string]time.Time) (map[string]int64, error)
    ListPlans(includeInactive bool) ([]types.Plan, error)
    ListAllRequests(p types.PaginationParams, filters store.RequestFilters) ([]types.Request, int, error)
    GetRequest(id string) (*types.Request, error)
}

// F — Notifications (user + admin)
type notificationsStore interface {
    ListAllNotifications(includeDeleted bool, audienceType string, p types.PaginationParams) ([]types.Notification, int, error)
    GetNotification(id string) (*types.Notification, error)
    CreateNotification(*types.Notification) error
    UpdateNotification(id, title, body, audienceType string, audienceID *string) error
    SoftDeleteNotification(id string) error
    ListVisibleForUser(userID string, p types.PaginationParams) ([]types.Notification, int, error)
    CountUnreadForUser(userID string) (int, error)
    MarkNotificationRead(userID, notificationID string) error
    MarkAllNotificationsRead(userID string) (int, error)
    // Also needed by notifications-create/update audience validation:
    GetProjectByID(id string) (*types.Project, error)
    GetUserByID(id string) (*types.User, error)
}
```

Server fields to add:
- `AdminSuper adminSuperStore`
- `Notifications notificationsStore`
- `HTTPLog *httplog.Logger` (nil-safe — E4 returns 503 if nil per legacy)

- [ ] **Step 1: Extend both interfaces** — replace the two `interface{}` stubs in `subsystem_stores.go`. Add store + types imports.
- [ ] **Step 2: Add 3 Server fields** in `server.go` alongside existing fields.
- [ ] **Step 3: `go build ./internal/api/admin/v1/`** — should succeed.
- [ ] **Step 4: Extend `routeTestStore`** with stub methods if the build test file breaks.
- [ ] **Step 5: `go test ./internal/api/admin/v1/ ./internal/api/contract/ ./cmd/modelserver/`** — all green.
- [ ] **Step 6: Commit** — `feat(adminv1): adminSuper + notifications store surface for batch 05`

---

## Task 2: E1 (ListAllProjects) + E3 (ListAllRequests) — simple list handlers

Both handlers have query-param filters and return `writeList` (`{data:[...], meta:{...}}`).

**DTOs:**
```go
type ListAllProjectsInput struct {
    Page      int    `query:"page" default:"1" minimum:"1"`
    PerPage   int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
    Sort      string `query:"sort" default:"created_at"`
    Order     string `query:"order" default:"desc" enum:"asc,desc"`
    ProjectID string `query:"project_id,omitempty" format:"uuid"`
    Owner     string `query:"owner,omitempty" format:"uuid"`
}

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
```

Output uses `ListResponse[types.Project]` / `ListResponse[types.Request]`.

**Deviation to note:** legacy E1 rejects invalid UUID in `project_id`/`owner` with a hand-written 400. Typed DTO's `format:"uuid"` will produce a Huma 422 with a schema message instead. Systemic drift already accepted for Batches 2-4; document in PR body.

**Deviation to note:** legacy E3 silently ignores unparseable `since`/`until` (falls back to zero time). Typed DTO uses `string`; handler parses via `time.Parse(time.RFC3339, in.Since)` and preserves the silent-ignore.

Tests: 5 per handler (filters propagate, invalid UUID → typed validator, empty list, store error, happy path).

- [ ] Standard TDD cycle for each handler; single commit at end.

---

## Task 3: E2 (SubscriptionsOverview) — the aggregation handler

Legacy at `handle_projects.go:104-260` (~150 lines). Preserve verbatim:
- Query param `project_ids` (comma-separated); empty → `{data: []}` immediately (200, not 400)
- Load: active subs, owners, plans (`ListPlans(false)`)
- Compute per-project period-start (from active subscription's `StartsAt`)
- `SumCreditsSinceByProjects` for the period credits map
- Build per-project ratelimit windows using `plan.ToPolicy(...).CreditRules` and `ratelimit.WindowStartTime`
- **Preserve the N+1 windows aggregation via `SumCreditsForWindow`** — this is a documented perf choice; not this batch's job to optimize

**DTOs** — copy the `projectOwnerSnapshot` and `projectSubscriptionOverview` struct definitions verbatim from `handle_projects.go` into `admin_reads.go` (rename to exported: `ProjectOwnerSnapshot`, `ProjectSubscriptionOverview`).

**Store methods used**: `GetActiveSubscriptionsByProjectIDs`, `GetProjectOwnersByProjectIDs`, `SumCreditsSinceByProjects`, `ListPlans`, plus **per-window credit-sum queries** — currently uses `st.SumCreditsForWindow(projectID, windowStart, windowEnd)` inside the N+1 loop. Add this to `adminSuperStore` too:

```go
    SumCreditsForWindow(projectID string, windowStart, windowEnd time.Time) (int64, error)
```

Tests (4):
1. Empty `project_ids` → 200 with `{data: []}`
2. Missing subscriptions → project omitted from response
3. Store error on `ListAllProjects` (or one of the intermediate calls) → 500 internal
4. Happy path with 2 projects → both appear with expected windows

- [ ] Standard TDD cycle; commit `feat(adminv1/admin): typed GET /admin/projects/subscriptions-overview handler + tests`

---

## Task 4: E4 (GetHttpLog) — first real `contract.BytesResponse` user

Legacy `handleGetHttpLog` is dual-use. Batch 5's typed handler is admin-only.

**Response strategy:**

```go
type GetHttpLogInput struct {
    RequestID string `path:"requestID" doc:"Request identifier."`
}

type GetHttpLogOutput struct {
    ContentType   string `header:"Content-Type"`
    ContentLength int64  `header:"Content-Length,omitempty"`
    Body          contract.BytesResponse
}
```

Handler:
```go
func (s *Server) getHttpLog(ctx context.Context, input *GetHttpLogInput) (*GetHttpLogOutput, error) {
    if s == nil || s.AdminSuper == nil {
        return nil, contract.NewError(http.StatusInternalServerError, "internal", ..., nil)
    }
    if s.HTTPLog == nil {
        return nil, contract.NewError(http.StatusServiceUnavailable, "unavailable", "http logging is not configured", nil)
    }
    req, err := s.AdminSuper.GetRequest(input.RequestID)
    if err != nil {
        return nil, contract.NewError(http.StatusNotFound, "not_found", "request not found", nil)
    }
    if req.HttpLogPath == "" {
        return nil, contract.NewError(http.StatusNotFound, "not_found", "no http log available for this request", nil)
    }
    data, err := s.HTTPLog.Retrieve(ctx, req.HttpLogPath)
    if err != nil {
        return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to retrieve http log", nil)
    }
    return &GetHttpLogOutput{
        ContentType:   "application/json",
        ContentLength: int64(len(data)),
        Body:          contract.BytesResponse{Reader: bytes.NewReader(data)},
    }, nil
}
```

**No project-membership check in the typed handler** — legacy had one but it only fires when `chi.URLParam(r, "projectID") != ""`, i.e. the project-scoped variant. The superadmin-only path this batch registers never has a `projectID`; the check was inert. Explicitly document this in the report.

Tests (4):
- HTTPLog nil → 503 unavailable
- GetRequest error → 404
- Empty HttpLogPath → 404
- Happy path → recorder.Code == 200, Content-Type == application/json, body == expected bytes

- [ ] Standard TDD; commit `feat(adminv1/admin): typed GET /admin/requests/{requestID}/http-log handler + BytesResponse first user`

---

## Task 5: F1-F4 user notifications inbox (Authenticated access)

Four small handlers, all `authz.Authenticated()`.

**F1 ListMyNotifications** — pagination-only, returns `{data:[notification], meta:{}}`

**F2 UnreadNotificationCount** — no input, returns `{data: {"count": N}}`. Custom output struct:
```go
type UnreadNotificationCountOutput struct {
    Body struct {
        Data struct {
            Count int `json:"count"`
        } `json:"data"`
    }
}
```

**F3 MarkNotificationRead** — path `{id}`, returns `{data: {"ok": true}}`. Silent-200 on unknown id per legacy contract.

**F4 MarkAllNotificationsRead** — no input, returns `{data: {"marked": N}}`.

All 4 handlers read `Principal.UserID` from the request-authorization context. Since `authz.Authenticated()` runs the middleware, `authorizationFromContext(ctx)` returns a populated struct — no `Auth.GetUserByID` call needed inside the handler.

Tests (5+): one per handler + one for store error on List. Use fake `notificationsStore`.

- [ ] TDD; commit `feat(adminv1/notifications): typed user inbox handlers (F1-F4)`

---

## Task 6: F5 (admin list) + F7 (admin get)

**F5 ListAllNotifications** — pagination + `include_deleted` query bool + `audience_type` query enum filter (validated in handler: must be `global|project|user` or empty).

**F7 GetNotification** — path `{id}`, returns `{data: notification}`, 404 on `pgx.ErrNoRows`, 500 on other errors.

Tests (5): audience_type valid/invalid, include_deleted filter passes through, get 404 on nil, get happy path, get 500 on other store error.

- [ ] TDD; commit `feat(adminv1/notifications): typed admin list + get (F5, F7)`

---

## Task 7: F6 (create) + F8 (update + delete)

**F6 CreateNotification** — big:
- Body: `title`, `body`, `audience_type`, `audience_id` (nullable)
- `validateNotificationPayload` — 400 with legacy codes (`invalid_input`)
- For non-global audience: `resolveAudience` → 400 `invalid_audience` OR 500 `internal`
- Store `CreateNotification` → 500 `internal`
- **Returns 200 (not 201)** with `{data: notification}` — preserve

**F8a UpdateNotification** — similar validation + audience resolve, then `UpdateNotification` → 404 on `pgx.ErrNoRows`, 500 on other errors. Refetch via `GetNotification` — 500 if that fails. Return `{data: notification}`.

**F8b DeleteNotification** — path `{id}`, `SoftDeleteNotification` → 404 on `pgx.ErrNoRows`, 500 on other errors. **Returns 200 with `{data: {"deleted": true}}` (not 204)** — preserve.

Duplicate `validateNotificationPayload` + `resolveAudience` into new file `internal/api/admin/v1/notification_helpers.go`.

Tests (8+): each validation branch of create, audience-resolve project not found, audience-resolve project archived, update refetch, delete happy path, delete not found.

- [ ] TDD; commit `feat(adminv1/notifications): typed admin create + update + delete (F6, F8)`

---

## Task 8: Register + delete chi + wire main.go + regen spec

**Register operations:**

- E subsystem (all `System(authz.PermissionSystemProjectsRead)` OR `System(authz.PermissionSystemRequestsRead)` per legacy):
  - `listAllProjects` GET `/api/v1/admin/projects` — PermissionSystemProjectsRead
  - `adminProjectsSubscriptionsOverview` GET `/api/v1/admin/projects/subscriptions-overview` — PermissionSystemProjectsRead
  - `listAllRequests` GET `/api/v1/admin/requests` — PermissionSystemRequestsRead
  - `getAdminHttpLog` GET `/api/v1/admin/requests/{requestID}/http-log` — PermissionSystemRequestsRead

- F subsystem — user inbox (all `authz.Authenticated()`):
  - `listMyNotifications` GET `/api/v1/notifications` — 200
  - `unreadNotificationCount` GET `/api/v1/notifications/unread_count` — 200
  - `markNotificationRead` POST `/api/v1/notifications/{id}/read` — 200
  - `markAllNotificationsRead` POST `/api/v1/notifications/read_all` — 200

- F subsystem — admin (all `System(authz.PermissionSystemNotificationsRead)` OR `PermissionSystemNotificationsManage`):
  - `listAllNotifications` GET `/api/v1/admin/notifications` — PermissionSystemNotificationsRead
  - `createNotification` POST `/api/v1/admin/notifications` — PermissionSystemNotificationsManage — 200 (not 201)
  - `getNotification` GET `/api/v1/admin/notifications/{id}` — PermissionSystemNotificationsRead
  - `updateNotification` PUT `/api/v1/admin/notifications/{id}` — PermissionSystemNotificationsManage — 200
  - `deleteNotification` DELETE `/api/v1/admin/notifications/{id}` — PermissionSystemNotificationsManage — 200 (not 204)

**Registration variant:** dashboard grep to determine bare vs trailing-slash. Legacy chi mounts them all at `r.Get("/")` etc within `r.Route("/notifications", ...)`, `r.Route("/admin/notifications", ...)`, `r.Route("/admin/projects", ...)`, `r.Route("/admin/requests", ...)`. **Recommend `RegisterWithLegacyTrailingSlash` for all list/create ops** (paths without `{param}` suffix); use plain `Register` for `{id}`-terminated paths. Matches Batch 4 conservative choice.

**Chi deletions in `internal/admin/routes.go`:**
- Inside `/admin/projects` block: `r.Get("/", ...)`, `r.Get("/subscriptions-overview", ...)`. Block becomes empty (only middleware) → delete entire `r.Route` block.
- Inside `/admin/requests` block: `r.Get("/", ...)`, `r.Get("/{requestID}/http-log", ...)`. Block becomes empty → delete.
- Inside `/notifications` block: 4 routes. Block empty → delete.
- Inside `/admin/notifications` block: 5 routes. Block empty → delete.

12 chi lines + 4 route blocks removed.

**Wire main.go:**
```go
AdminSuper:    st,
Notifications: st,
HTTPLog:       httpLogger,  // the *httplog.Logger var already in scope
```

**Regen spec.**

- [ ] Full task cycle; single commit `feat(admin): register typed admin + notifications ops, remove legacy chi routes`

---

## Task 9: Envelope fixtures + dual-registration guard

**Envelope fixtures** — one per response shape that isn't the standard `DataResponse[T]`:
- `{data: {count: N}}` for unread_count
- `{data: {ok: true}}` for mark-read
- `{data: {marked: N}}` for mark-all-read
- `{data: {deleted: true}}` for delete-notification
- `DataResponse[types.Notification]` for create/get/update
- `DataResponse[[]ProjectSubscriptionOverview]` for subscriptions-overview
- Standard `ListResponse[types.Project|Request|Notification]` for the list endpoints

**Dual-registration guard** — `TestBatch05NoLegacyChiOverlap` with all 12 paths + trailing-slash variants where applicable.

- [ ] Two commits: envelope fixtures then invariant guard.

---

## Task 10: Dashboard regen + docs + verify

- `pnpm api:generate` (large diff — 12 new operations)
- `pnpm exec tsc --noEmit`
- `pnpm build`
- Update `docs/admin-api-openapi-rbac.md` with Batch 5 migrated operations list
- Single commit

---

## Task 11: Final whole-branch review + PR

- Dispatch code-reviewer on the full Batch 5 diff
- Address Critical/Important findings
- Push branch, open PR

---

## Self-Review Notes

11 tasks; ~40 test cases total across handlers; ~12 chi lines + 4 route blocks removed.

Known accepted deviations to document in PR body:
- `project_id` / `owner` UUID query params — Huma 422 instead of legacy 400 (systemic; inherited from Batches 2-4)
- Refetch-nil edge cases in update paths (same as Batch 4 pattern)
- Notifications' unusual response shapes (POST → 200, DELETE → `{deleted:true}`) — preserved verbatim; not deviations, just quirks worth calling out

Deferred to Batch 14:
- Legacy handlers in `internal/admin/handle_notifications.go` (except `validateNotificationPayload` + `resolveAudience` are still needed until `handle_notifications.go` is deleted)
- `handleGetHttpLog` legacy stays — it's dual-use with Batch 12's project-scoped variant. Full removal only when Batch 12 also migrates and both callers are gone.

Batch 5 is the biggest to date. If any task requires cross-cutting store/interface widening beyond what's declared here, escalate BLOCKED and I'll adjudicate.
