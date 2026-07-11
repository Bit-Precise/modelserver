# Huma-driven Admin API + RBAC Contract — Design

**Status:** approved for planning
**Author (drafted with):** brainstorming session, 2026-07-11
**Related:** `docs/admin-api-openapi-rbac.md` (existing contract-flow doc), `.github/workflows/management-contract.yml`

## §1 Goals & non-goals

### Goals

1. Route the entire ModelServer **management** API through the single
   pipeline **Go → Huma → OpenAPI 3.1 → openapi-typescript → openapi-fetch**,
   so the Go DTOs are the single source of truth for the dashboard's typed
   surface.
2. Consolidate RBAC. All of `RequireSuperadmin`, `requireRole`,
   `projectAccessMiddleware`, `canSetMemberQuota`, `canAccessKey`, and
   `sameProjectID` collapse into one `authz.AccessPolicy` DSL. The DSL value
   is the single source of truth for both runtime enforcement and OpenAPI
   `x-modelserver-authz` metadata.
3. Eliminate dual registration between chi and huma: every route is owned by
   exactly one router. A CI test fails the build if the same
   `(method, path)` is registered twice.
4. Preserve wire compatibility. Existing `data` / `meta` / `error`
   envelopes, HTTP status codes, field names, and trailing-slash aliases
   stay unchanged.

### Non-goals

- Proxy / LLM compatibility routes (`/v1/*`, `/v1beta/*`, streaming,
  multipart, `/api/oauth/profile`, provider passthroughs) do **not** enter
  the contract.
- Hydra login / consent, device-flow verification page, OAuth redirects,
  and any HTML-form endpoints stay on chi.
- HMAC billing webhooks stay on chi. `authz.AccessModeHMAC` is already
  refused by `contract.Register` and stays that way for the dashboard
  contract.
- Behavior changes to existing endpoints are out of scope. In particular,
  `canSetMemberQuota` is relaxed only per §5 (owner + maintainer may set
  quota on anyone) and no other implicit business rules are introduced.

## §2 Current state and targets

### Already in place (untracked working tree)

- `internal/authz/` — `Permission` catalog (19 system + 25 project),
  `ProjectRole` (owner / maintainer / developer), `AccessPolicy` DSL
  (`Public`, `Authenticated`, `System`, `Project`, `WithResource`,
  `WithPolicies`, `RequireProjectMembership`), `Policy` interface, three
  built-in policies (`own-resource`, `resource-project-containment`,
  `member-role-hierarchy`), `ResourceResolver` interface.
- `internal/api/contract/` — Huma wrapper (`NewAdminAPI`, `Register`),
  `Operation` (with `AccessPolicy` + `Authorize` factory), unified error
  envelope, `x-modelserver-authz` extension emission.
- `internal/api/admin/v1/` — `Server`, `authorizationMiddleware`, and 11
  read-only operations already migrated: authConfig, `/me`, capabilities,
  projects list/get, users list/get/compact, plans list/get, project
  capabilities.
- `cmd/openapi/main.go` — offline generator; `api/openapi/admin.openapi.json`
  already committed.
- Dashboard `admin-client.ts` + `dashboard/src/api/generated/schema.ts`.
- Migration 063 — `project_members.role` CHECK constrains to
  `owner`/`maintainer`/`developer`.

### Target: ~120 remaining legacy chi routes

Grouped by subsystem (this grouping is also the batching boundary in §6).

| # | Subsystem | Endpoints | Primary files | Notable difficulties |
|---|-----------|-----------|---------------|----------------------|
| A | Auth (public) | 6 | `handle_auth.go` | No auth; OAuth exchange body; 302 redirects |
| B | Users writes | 1 | `handle_projects.go` (should move) | `PUT /users/{userID}` — system.users.write |
| C | Plans writes | 3 | `handle_plans.go` | catalog validation |
| D | Models catalog | 5 | `handle_models.go` | non-UUID `{name}` path param + catalog refresh |
| E | Admin (superadmin) | 8 | `handle_projects.go` + `handle_requests.go` + `handle_http_log.go` + `handle_notifications.go` | global requests; http-log binary body |
| F | Notifications user + admin | 8 | `handle_notifications.go` | audience validation |
| G | Extra usage user + admin | 8 | `handle_extra_usage.go` | payment client, complex body |
| H | Projects CRUD | 5 | `handle_projects.go` | `POST /projects` has no `{projectID}` |
| I | Project members | 9 | `handle_projects.go` | quota rule; usage sub-routes |
| J | API Keys | 5 | `handle_keys.go` | **own-resource** for developers |
| K | OAuth grants | 2 | `handle_oauth_grants.go` | Hydra revocation side effect |
| L | Project policies | 5 | `handle_policies.go` | policy JSON + catalog |
| M | Subscriptions | 6 | `handle_subscriptions.go` + `handle_orders.go` | superadmin bypass on project path |
| N | Available plans + Orders | 6 | `handle_orders.go` | payment client; cancel state machine |
| O | Traces | 3 | `handle_traces.go` | three-level path `/traces/{traceID}/requests` |
| P | Requests + http-log | 3 | `handle_requests.go` | binary/gzip body |
| Q | Usage | 1 | inline | large aggregated response |
| R | Upstreams | 15 | `handle_upstreams.go` | encrypted secrets; utilization variants |
| S | Upstream groups | 7 | `handle_upstream_groups.go` | group members sub-collection |
| T | OAuth clients | 5 | `handle_oauth_clients.go` | Hydra API proxy |
| U | Routing | 8 | `handle_routing_*.go` | enums + matrix + routes CRUD |
| V | My-quota / my-membership | 2 | `handle_projects.go` | self-scoped project reads |

## §3 Contract infrastructure additions

Additions on top of the existing skeleton; nothing here is a rewrite.

### 3.1 `internal/authz/`

**New permissions** (fills the catalog so every migrated operation has a
canonical permission):

```go
// system
PermissionSystemNotificationsRead   Permission = "system.notifications.read"
PermissionSystemNotificationsManage Permission = "system.notifications.manage"

// project
PermissionProjectMembersUsageRead   Permission = "project.members.usage.read"
PermissionProjectSubscriptionsWrite Permission = "project.subscriptions.write"
PermissionProjectExtraUsageRead     Permission = "project.extra_usage.read"
PermissionProjectExtraUsageWrite    Permission = "project.extra_usage.write"
PermissionProjectExtraUsageTopup    Permission = "project.extra_usage.topup"
```

Existing `PermissionSystemSubscriptionOverride` remains, and is what
`SystemOnProjectPath` (below) enforces.

**New policy IDs and semantics:**

- `PolicyKeyOwnedByCallerForDeveloper` — passes if caller is superadmin
  or holds project role `owner`/`maintainer`; otherwise requires
  `Resource.OwnerID == Principal.UserID`. Encodes the existing
  "developers can only touch keys they created" rule.
- `PolicyRequireSuperadmin` — passes iff `Principal.Superadmin`. Used
  under `SystemOnProjectPath` for subscription overrides.
- `PolicyMemberSelfOrElevated` — passes for a caller reading their own
  member row (`Resource.ID == Principal.UserID`), or for owner /
  maintainer / superadmin.

**New DSL helper:**

- `authz.SystemOnProjectPath(permission Permission, projectIDPathParam string)`
  — returns an `AccessPolicy` with `Mode=RBAC`, `Scope=System`,
  `Permission=permission`, `Superadmin=SuperadminRequired`, but
  additionally records `ProjectIDPathParam` for use by resource resolvers
  and audit logging. Runtime behavior is identical to `System(...)`:
  requires an explicit superadmin. Existing `Validate()` grows a special
  case allowing `ProjectIDPathParam` on system scope only when set via
  this helper (guarded by a private flag on `AccessPolicy`).

**New resource resolvers** (`internal/api/admin/v1/resolvers/`):

| ResourceType | Store method | ProjectID source | OwnerID source |
|--------------|--------------|------------------|----------------|
| `api-key`    | `GetAPIKeyByID` | `key.ProjectID` | `key.CreatedBy` |
| `policy`     | `GetPolicyByID` | `policy.ProjectID` | – |
| `subscription` | `GetSubscriptionByID` | `subscription.ProjectID` | – |
| `order`      | `GetOrderByID` | `order.ProjectID` | `order.CreatedBy` |
| `trace`      | `GetTraceByID` | `trace.ProjectID` | – |
| `request`    | `GetRequestByID` | `request.ProjectID` | – |
| `oauth-grant`| `GetOAuthGrantByID` | `grant.ProjectID` | `grant.UserID` |
| `member`     | in-memory (URL-only) | `URL projectID` | – |
| `extra-usage-topup` | `GetOrderByID` + kind check | same as order | same as order |
| `model`      | `GetModelByName` | – (system) | – |
| `upstream`, `upstream-group`, `oauth-client`, `routing-route` | corresponding `GetByID` | – (system) | – |

Resolvers that return `err != nil` or `resource.ProjectID == ""` (for
project-scope operations) drive a 404 through the shared middleware,
matching legacy IDOR-suppression behavior.

Existing `Policy` interface is **not** widened. The quota-rule relaxation
(see §5, I5) removes the last case that would have required body-aware
policies, so `PolicyInput` keeps its current shape.

### 3.2 `internal/api/contract/`

- Promote `registerWithLegacyTrailingSlash` from `adminv1` into
  `contract.RegisterWithLegacyTrailingSlash`. Multiple subsystems
  (`plans`, `users`, `projects`, `orders`, `policies`, ...) need it.
- Provide `contract.BytesResponse` — a documented `struct { Body io.Reader;
  ContentType string; ContentLength int64 }`. Handlers with
  `application/octet-stream` bodies (http-log) use it. Emits
  `responses.200.content.application/octet-stream: {schema: {type: string,
  format: binary}}` into the spec.
- `contract.WriteError` already exists. No changes.
- Keep `configureErrorsOnce` as-is; do not expose a Reset. Tests share the
  one-time configured error factory.

### 3.3 `internal/api/admin/v1/`: store interface fanning

Rather than growing `managementStore` into a giant interface, add one
narrow store interface per subsystem alongside the existing
`userReadStore` / `planReadStore` pattern:

```go
type keysStore     interface { ... }
type policiesStore interface { ... }
type ordersStore   interface { ... }
type membersStore  interface { ... }
// ...
```

Each is a field on `*Server`. Tests wire fakes without pulling in the
full `*store.Store`.

### 3.4 `internal/api/admin/v1/authz_middleware.go`

Move `authorizationMiddleware` and helpers out of `server.go` into a
dedicated file. No behavior change. Isolates the surface that would be
touched if body-aware policies ever return.

## §4 Representative API blueprints

Three examples cover the recurring hard cases. The rest of the subsystems
follow the same shape and are summarized in §5.

### 4.1 J — API Keys (own-resource representative)

**Files added:** `internal/api/admin/v1/keys.go`,
`internal/api/admin/v1/resolvers/keys.go`, `keys_test.go`.

```go
type keysStore interface {
    ListAPIKeysByProject(projectID string, p types.PaginationParams) ([]types.APIKey, int, error)
    GetAPIKeyByID(id string) (*types.APIKey, error)
    CreateAPIKey(input store.CreateAPIKeyInput) (types.APIKey, string, error)
    UpdateAPIKeyForProject(projectID, keyID string, updates map[string]any) (bool, error)
    DeleteAPIKeyForProject(projectID, keyID string) (bool, error)
}

type APIKey struct {
    ID            string    `json:"id" format:"uuid"`
    ProjectID     string    `json:"project_id" format:"uuid"`
    Name          string    `json:"name"`
    Description   string    `json:"description,omitempty"`
    Status        string    `json:"status" enum:"active,revoked"`
    AllowedModels []string  `json:"allowed_models,omitempty" nullable:"false"`
    CreatedBy     string    `json:"created_by" format:"uuid"`
    CreatedAt     time.Time `json:"created_at"`
    // ... aligned with types.APIKey; ciphertext fields never surfaced
}

type createKeyInput struct {
    ProjectID string `path:"projectID" format:"uuid"`
    Body struct {
        Name          string   `json:"name" minLength:"1"`
        Description   string   `json:"description,omitempty"`
        AllowedModels []string `json:"allowed_models,omitempty" nullable:"false"`
    }
}

// Preserve legacy top-level "key" + "data" shape.
type createKeyResponseBody struct {
    Data      APIKey `json:"data"`
    Plaintext string `json:"key"`
}
type createKeyOutput struct { Body createKeyResponseBody }
```

**Authorization matrix:**

| Route | AccessPolicy |
|-------|--------------|
| `GET  /projects/{projectID}/keys` | `Project(PermissionProjectKeysRead, "projectID")` |
| `POST /projects/{projectID}/keys` | `Project(PermissionProjectKeysCreate, "projectID")` |
| `GET  /projects/{projectID}/keys/{keyID}` | `Project(PermissionProjectKeysRead, "projectID", WithResource("api-key","keyID"), WithPolicies(PolicyKeyOwnedByCallerForDeveloper))` |
| `PUT  /projects/{projectID}/keys/{keyID}` | `Project(PermissionProjectKeysManage, "projectID", WithResource("api-key","keyID"), WithPolicies(PolicyKeyOwnedByCallerForDeveloper))` |
| `DELETE /projects/{projectID}/keys/{keyID}` | `Project(PermissionProjectKeysManage, "projectID", WithResource("api-key","keyID"))` |

**Tests:**

- Authorization matrix: 3 roles × 5 methods × {own key, other key} = 30
  cases.
- Cross-project IDOR: URL project A, `keyID` in project B → 404.
- Create returns both `data.id` and top-level `key` (plaintext).
- `allowed_models` catalog validation returns 400 `unknown_models` with
  the offending names in `details`.
- Delete requires status `revoked`; otherwise 400 `bad_request`.

### 4.2 M — Subscriptions (superadmin bypass on project path)

**Files added:** `internal/api/admin/v1/subscriptions.go`,
`internal/api/admin/v1/resolvers/subscriptions.go`,
`subscriptions_test.go`.

Existing legacy `handleCreateSubscription` and `handleUpdateSubscription`
require the caller to be **superadmin** (not project owner). They sit
under the project chi sub-router today.

New layout uses `SystemOnProjectPath` so the OpenAPI extension explicitly
shows a system permission at a project path, and the runtime enforces
`Principal.Superadmin` without hitting `project_members`:

| Route | AccessPolicy |
|-------|--------------|
| `GET  /projects/{projectID}/subscriptions` | `Project(PermissionProjectSubscriptionsRead, "projectID")` |
| `GET  /projects/{projectID}/subscription/usage` | `Project(PermissionProjectSubscriptionsRead, "projectID")` |
| `GET  /projects/{projectID}/subscriptions/{subID}` | `Project(PermissionProjectSubscriptionsRead, "projectID", WithResource("subscription","subID"))` |
| `POST /projects/{projectID}/subscriptions` | `SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID")` |
| `PUT  /projects/{projectID}/subscriptions/{subID}` | `SystemOnProjectPath(PermissionSystemSubscriptionOverride, "projectID") + WithResource("subscription","subID")` |

Tests:

- Project owner (non-superadmin) is rejected on POST/PUT.
- Superadmin succeeds even without membership on the project.
- `subID` belonging to another project is 404 via
  `resource-project-containment`.
- Read paths follow standard project-role matrix.

### 4.3 O — Traces (three-level path with shared resolver)

**Files added:** `internal/api/admin/v1/traces.go`,
`internal/api/admin/v1/resolvers/traces.go`, `traces_test.go`.

| Route | AccessPolicy |
|-------|--------------|
| `GET /projects/{projectID}/traces` | `Project(PermissionProjectTracesRead, "projectID")` |
| `GET /projects/{projectID}/traces/{traceID}` | `Project(PermissionProjectTracesRead, "projectID", WithResource("trace","traceID"))` |
| `GET /projects/{projectID}/traces/{traceID}/requests` | `Project(PermissionProjectTracesRead, "projectID", WithResource("trace","traceID"))` |

The `trace` resolver is shared; the sub-collection route consumes the
same authorization decision.

## §5 Full subsystem matrix

Abbreviations: `PP` = `PermissionProject`, `PS` = `PermissionSystem`,
`Auth` = `Authenticated()`, `Pub` = `Public()`. "Res" is `WithResource`
type. "Pol" is `WithPolicies` list.

| # | Method + Path | AccessPolicy summary | Notes |
|---|---------------|----------------------|-------|
| A1 | `POST /auth/refresh` | `Pub` | body: refresh token |
| A2 | `POST /auth/oauth/{provider}` | `Pub` | provider enum: github, google, oidc |
| A3 | `GET  /auth/oauth/{provider}/redirect` | `Pub` | 302 via Huma output `Status` + `Location` |
| B1 | `PUT  /users/{userID}` | `System(PS.UsersWrite)` | superadmin write |
| C1 | `POST /plans` | `System(PS.PlansManage)` | catalog validation in handler |
| C2 | `PUT /plans/{planID}` | `System(PS.PlansManage)` | |
| C3 | `DELETE /plans/{planID}` | `System(PS.PlansManage)` | |
| D1 | `GET  /models` | `System(PS.ModelsRead)` | |
| D2 | `POST /models` | `System(PS.ModelsManage)` | |
| D3 | `GET  /models/{name}` | `System(PS.ModelsRead) + WithResource("model","name")` | non-UUID name |
| D4 | `PATCH+PUT /models/{name}` | `System(PS.ModelsManage) + WithResource("model","name")` | |
| D5 | `DELETE /models/{name}` | `System(PS.ModelsManage) + WithResource("model","name")` | |
| E1 | `GET /admin/projects` | `System(PS.ProjectsRead)` | |
| E2 | `GET /admin/projects/subscriptions-overview` | `System(PS.ProjectsRead)` | |
| E3 | `GET /admin/requests` | `System(PS.RequestsRead)` | |
| E4 | `GET /admin/requests/{requestID}/http-log` | `System(PS.RequestsRead) + WithResource("request","requestID")` | binary response |
| F1 | `GET /notifications` | `Auth` | user inbox |
| F2 | `GET /notifications/unread_count` | `Auth` | `{data: number}` wrapped |
| F3 | `POST /notifications/{id}/read` | `Auth` | 204 |
| F4 | `POST /notifications/read_all` | `Auth` | 204 |
| F5 | `GET /admin/notifications` | `System(PS.NotificationsRead)` | new permission |
| F6 | `POST /admin/notifications` | `System(PS.NotificationsManage)` | audience validation in handler |
| F7 | `GET /admin/notifications/{id}` | `System(PS.NotificationsRead)` | |
| F8 | `PUT+DELETE /admin/notifications/{id}` | `System(PS.NotificationsManage)` | |
| G1 | `GET /admin/extra-usage/overview` | `System(PS.ExtraUsageRead)` | |
| G2 | `POST /admin/extra-usage/projects/{projectID}/topup` | `System(PS.ExtraUsageManage)` | |
| G3 | `PUT /admin/extra-usage/projects/{projectID}/bypass` | `System(PS.ExtraUsageManage)` | |
| G4 | `GET /projects/{projectID}/extra-usage` | `Project(PP.ExtraUsageRead, "projectID")` | new permission |
| G5 | `PUT /projects/{projectID}/extra-usage` | `Project(PP.ExtraUsageWrite, "projectID")` | new permission |
| G6 | `GET /projects/{projectID}/extra-usage/transactions` | `Project(PP.ExtraUsageRead, "projectID")` | |
| G7 | `POST /projects/{projectID}/extra-usage/topup` | `Project(PP.ExtraUsageTopup, "projectID", RequireProjectMembership())` | new permission; deny superadmin bypass to keep payment-audit clean |
| G8 | `GET /projects/{projectID}/extra-usage/topup/{orderID}` | `Project(PP.ExtraUsageRead, "projectID", WithResource("extra-usage-topup","orderID"))` | |
| H1 | `POST /projects` | `Auth` | max_projects check stays in handler (400) |
| H2 | `PUT /projects/{projectID}` | `Project(PP.SettingsWrite, "projectID")` | |
| H3 | `POST /projects/{projectID}/archive` | `Project(PP.Archive, "projectID")` | owner-only via role table |
| H4 | `POST /projects/{projectID}/unarchive` | `Project(PP.Archive, "projectID")` | owner-only |
| H5 | `GET /projects/{projectID}/models` | `Project(PP.ModelsRead, "projectID")` | |
| I1 | `GET /projects/{projectID}/members` | `Project(PP.MembersRead, "projectID")` | |
| I2 | `GET /projects/{projectID}/members/compact` | `Project(PP.MembersRead, "projectID")` | |
| I3 | `POST /projects/{projectID}/members` | `Project(PP.MembersManage, "projectID")` | body validation in handler |
| I4 | `GET /projects/{projectID}/members/usage` | `Project(PP.MembersUsageRead, "projectID")` | new permission (subset of MembersRead) |
| I5 | `PUT /projects/{projectID}/members/{userID}` | `Project(PP.MembersManage, "projectID", WithResource("member","userID"))` | **quota rule relaxed**: owner + maintainer may set quota on anyone |
| I6 | `DELETE /projects/{projectID}/members/{userID}` | `Project(PP.MembersManage, "projectID", WithResource("member","userID"))` | Hydra revocation side effect |
| I7 | `GET /projects/{projectID}/members/{userID}/affected-keys` | `Project(PP.MembersManage, "projectID")` | |
| I8 | `GET /projects/{projectID}/members/{userID}/quota-usage` | `Project(PP.MembersRead, "projectID", WithResource("member","userID"), WithPolicies(PolicyMemberSelfOrElevated))` | reader may see own; else needs manage-tier |
| I9a | `GET /projects/{projectID}/my-quota` | `Project(PP.MembersRead, "projectID")` | self read in handler |
| I9b | `GET /projects/{projectID}/my-membership` | `Project(PP.MembersRead, "projectID")` | self read in handler |
| J1..J5 | Keys | see §4.1 | |
| K1 | `GET /projects/{projectID}/oauth-grants` | `Project(PP.OAuthGrantsRead, "projectID")` | |
| K2 | `DELETE /projects/{projectID}/oauth-grants/{grantID}` | `Project(PP.OAuthGrantsManage, "projectID", WithResource("oauth-grant","grantID"))` | Hydra revoke |
| L1 | `GET /projects/{projectID}/policies` | `Project(PP.PoliciesRead, "projectID")` | |
| L2 | `POST /projects/{projectID}/policies` | `Project(PP.PoliciesManage, "projectID")` | |
| L3 | `GET /projects/{projectID}/policies/{policyID}` | `Project(PP.PoliciesRead, "projectID", WithResource("policy","policyID"))` | |
| L4 | `PUT /projects/{projectID}/policies/{policyID}` | `Project(PP.PoliciesManage, "projectID", WithResource("policy","policyID"))` | |
| L5 | `DELETE /projects/{projectID}/policies/{policyID}` | `Project(PP.PoliciesManage, "projectID", WithResource("policy","policyID"))` | |
| M1..M6 | Subscriptions | see §4.2 | |
| N1 | `GET /projects/{projectID}/available-plans` | `Project(PP.PlansRead, "projectID")` | |
| N2 | `GET /projects/{projectID}/orders` | `Project(PP.OrdersRead, "projectID")` | |
| N3 | `POST /projects/{projectID}/orders` | `Project(PP.OrdersCreate, "projectID")` | |
| N4 | `GET /projects/{projectID}/orders/{orderID}` | `Project(PP.OrdersRead, "projectID", WithResource("order","orderID"))` | |
| N5 | `POST /projects/{projectID}/orders/{orderID}/cancel` | `Project(PP.OrdersManage, "projectID", WithResource("order","orderID"))` | state machine 409 in handler |
| O1..O3 | Traces | see §4.3 | |
| P1 | `GET /projects/{projectID}/requests` | `Project(PP.RequestsRead, "projectID")` | |
| P2 | `GET /projects/{projectID}/requests/{requestID}/http-log` | `Project(PP.RequestsRead, "projectID", WithResource("request","requestID"))` | binary via `contract.BytesResponse` |
| Q1 | `GET /projects/{projectID}/usage` | `Project(PP.UsageRead, "projectID")` | large aggregated response |
| R1..R15 | Upstreams (list/create/get/put/delete/test/oauth start/exchange/status/refresh, utilization variants) | `System(PS.UpstreamsRead)` / `System(PS.UpstreamsManage)` | `WithResource("upstream","upstreamID")` on per-resource routes; secret fields DTO-masked |
| S1..S7 | Upstream groups (list/create/get/put/delete + members list/add/remove) | `System(PS.UpstreamGroupsRead)` / `System(PS.UpstreamGroupsManage)` | `WithResource("upstream-group","groupID")` |
| T1..T5 | OAuth clients | `System(PS.OAuthClientsRead)` / `System(PS.OAuthClientsManage)` | `WithResource("oauth-client","clientID")`; Hydra proxy |
| U1..U8 | Routing (routes CRUD, request-kinds, clients, matrix) | `System(PS.RoutingRead)` / `System(PS.RoutingManage)` | `WithResource("routing-route","routeID")` on per-resource routes |

Aggregate additions summarized:

- **New permissions:** `system.notifications.read`,
  `system.notifications.manage`, `project.members.usage.read`,
  `project.subscriptions.write`, `project.extra_usage.read`,
  `project.extra_usage.write`, `project.extra_usage.topup`.
- **New policies:** `key-owned-by-caller-for-developer`,
  `require-superadmin`, `member-self-or-elevated`.
- **New DSL helper:** `authz.SystemOnProjectPath(...)`.
- **Binary/302 responses:** E4, P2 (bytes); A3 (302). All three surfaces
  need explicit spec entries and are the only endpoints that skip typed
  bodies.

## §6 Migration blueprint and order

Each PR ("batch") completes: backend migration + spec regenerate +
dashboard hook switch to `adminApi` + removal of the matching chi
registration. This is the cadence chosen in §7.

### 14 batches

| Batch | Contents | Rationale |
|-------|----------|-----------|
| 1 | Contract infrastructure fill-in: §3.1 new permissions / policies / resolver scaffolding; §3.2 helpers (`RegisterWithLegacyTrailingSlash`, `BytesResponse`); §3.3 per-subsystem store interfaces defined empty | One-off; unblocks every later batch |
| 2 | **Auth (A1–A3)** | `AccessModePublic` writes; establishes 302 and body patterns |
| 3 | **Users write (B1)** + **Plans writes (C1–C3)** | Simple superadmin writes |
| 4 | **Models (D1–D5)** | Independent; non-UUID path param |
| 5 | **Admin global reads (E1–E4)** + **Notifications user (F1–F4)** + **Notifications admin (F5–F8)** | End-to-end sweep of notifications frontend + backend |
| 6 | **Extra usage admin (G1–G3)** + **Extra usage project (G4–G8)** | New permissions; keep types adjacent |
| 7 | **Projects CRUD (H1–H5)** | First batch with an operation that has no `{projectID}` (`POST /projects`) |
| 8 | **Members (I1–I9)** | Introduces `member` resolver and `PolicyMemberSelfOrElevated` |
| 9 | **Keys (J1–J5)** | Own-resource end-to-end |
| 10 | **OAuth grants (K1–K2)** + **Project policies (L1–L5)** | Simple structure; combined |
| 11 | **Subscriptions (M1–M6)** + **Orders (N1–N6)** + **Available plans (N1)** | Heavy cross-references; ship together |
| 12 | **Traces (O1–O3)** + **Requests + http-log (P1–P3)** + **Usage (Q1)** | Binary responses concentrated |
| 13 | **Upstreams (R)** + **Upstream groups (S)** + **OAuth clients (T)** | All system-scoped; secret handling grouped |
| 14 | **Routing (U)** + cleanup: delete `projectAccessMiddleware`, `RequireSuperadmin`, `requireRole`, `canSetMemberQuota`, `canAccessKey`, `sameProjectID`; `internal/admin/` retains only Hydra, device flow, OAuth callbacks, HMAC webhooks |

### Per-batch checklist

1. New / extended files under `internal/api/admin/v1/<sys>.go`: DTOs,
   input/output structs, handler methods, `register<Sys>Operations(api,
   server)`.
2. New permissions / policies / resolvers added to `internal/authz` (or
   `internal/api/admin/v1/resolvers`) first, with unit tests.
3. Register operations from `Register(api, server)`. Use
   `contract.RegisterWithLegacyTrailingSlash` when a chi trailing-slash
   spelling exists.
4. Remove the matching entries from `internal/admin/routes.go`. Each
   `(method, path)` is owned by exactly one router after this step.
5. Tests:
   - authorization matrix (role × method × resource-ownership),
   - envelope compatibility (fixture diff against pre-migration
     response),
   - IDOR (resource-in-other-project → 404).
6. `go run ./cmd/openapi` regenerates `api/openapi/admin.openapi.json`.
7. `pnpm api:generate` regenerates `dashboard/src/api/generated/schema.ts`
   and dashboard hooks flip from `client.ts` methods to
   `adminApi.GET|POST|...` + `unwrapAdminResponse`; the legacy client
   loses the matching methods in the same PR.
8. `go test ./...`, `pnpm api:check`, and `pnpm build` all pass.

### Migration-time guardrails (CI)

- **Spec drift** (already present): committed spec == offline-generated
  spec.
- **`TestNoDualRegistration` (new):** walk chi's registered routes and
  Huma's registered operations; fail if the same `(method, path)` shows
  up in both.
- **`TestEveryOperationHasCatalogPermission` (new):** iterate the spec's
  `x-modelserver-authz`; every non-public operation's permission must
  belong to `authz.AllPermissions()`.
- **`TestEveryResourceHasResolver` (new):** every `Resource.Type` in the
  spec has a registered resolver.
- **`TestSuccessEnvelopesUnchanged`:** golden-file compare selected
  responses (data / meta / error keys stable).

## §7 Dashboard switching strategy

- `dashboard/src/api/admin-client.ts` (already in place). Each PR flips
  the corresponding hooks / mutations to `adminApi.GET(...)` +
  `unwrapAdminResponse`, and deletes matching wrappers from
  `client.ts`.
- `client.ts` degrades to a shared-utilities file (`API_BASE`,
  `authenticatedFetch`, `toAPIError`, bearer refresh). Target final size:
  well under 50 lines.
- Generator: existing `pnpm api:generate` runs `openapi-typescript
  ../api/openapi/admin.openapi.json -o src/api/generated/schema.ts`. CI
  runs `api:check` to enforce that the committed schema matches
  generation.
- Type consolidation: dashboard's shared types (`Permission`,
  `ProjectRole`, `Project`, `User`, `APIKey`) resolve to
  `components["schemas"]` from the generated schema. `src/types.ts`
  loses locally-duplicated definitions as each subsystem migrates.

## §8 Validation and CI

- Go: `go test ./...` covers the new authorization matrices;
  `TestOpenAPISpecMatchesGenerated` (already present) extends to every
  new operation.
- Dashboard: `pnpm api:check` and `pnpm build`.
- New workflow: `.github/workflows/management-contract.yml` (already in
  the tree as untracked) is committed in Batch 1. It runs the offline
  generator, verifies the committed spec, runs Go tests, runs the
  dashboard checks, and runs the four new invariant tests from §6.

## §9 Risks and mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Wire micro-differences between chi and Huma handlers (`nil` vs `[]`, `omitempty` bounds, integer widths) | Silent compatibility regression | Envelope fixture tests per batch; keep existing `ProjectSettings` `json.RawMessage` pattern to avoid `map[string]any` round-tripping |
| Huma path validation stricter than chi (UUID format enforced at boundary) | Older clients could see 400s | Add `format:"uuid"` only where callers are the dashboard; keep IDs unconstrained on paths that mirror the legacy 404-on-anything behavior (documented explicitly in the `plans` / `users` DTOs) |
| Generated TS clashes with hand-authored `src/types.ts` | Build breakage | Migrate hooks batch-by-batch; when a subsystem migrates, its local types are deleted and consumers pull from `components["schemas"]` |
| Policies become a dumping ground for business validation (state machines, audience checks, catalog checks) | Blurred boundary between authz and validation | Explicit rule: **policies decide "may the caller do this?"** using principal, role, resource, and static access metadata only. Business invariants (audience exists, order status legal, model catalogued) stay in handlers and return 400/404/409 |
| Notification `unread_count` and similar irregular responses produce ugly generated types | UI type awkwardness | Always wrap in `DataResponse[T]` (existing envelope), even for scalar payloads |
| Binary http-log responses cannot be typed | Dashboard can't use `openapi-fetch` for these | Spec declares `application/octet-stream`; dashboard downloads them via `fetch` directly rather than `adminApi` |
| Huma middleware ordering vs body decode | Body-aware authz would see empty body | Body-aware authz is excluded from scope (quota relaxation removes the last case). Policies never read bodies |
| `SystemOnProjectPath` blurs system vs project scope | Confusing DSL | Runtime behavior identical to `System(...)`; `ProjectIDPathParam` is metadata-only for audit / resource resolution. Add explicit tests for the two subscription writes |

## §10 Deliverable summary

- Spec: this document.
- Companion (already present, kept authoritative for operators):
  `docs/admin-api-openapi-rbac.md`. Updates from this design (new
  permissions, `SystemOnProjectPath`, migration status per batch) land
  incrementally as their batches ship.
- Follow-on artifact: implementation plan (via `superpowers:writing-plans`)
  organized around the 14 batches in §6, one plan per batch, wired to a
  common infrastructure task from Batch 1.
