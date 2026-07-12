# Huma Admin API — Batches 2–14 Index and Per-Batch Template

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to instantiate this template into a per-batch plan file before executing. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Provide the shared task template that every subsystem migration batch (2–14) instantiates, plus a compact index describing what each batch touches. Each batch instantiation lives at `docs/superpowers/plans/2026-07-11-huma-admin-batch-NN-<name>.md` and is authored just before that batch begins execution, using this template as the skeleton.

**Architecture:** Each batch follows the same 12-task shape: new permissions/policies/resolvers (if any) → DTOs + input/output types → typed handlers + registration → per-endpoint authorization tests → chi deregistration → OpenAPI regenerate → dashboard hook migration → dashboard build → merge-gate check. The invariants added in Batch 1 (§Task 12 of the Batch 1 plan) run in CI and continuously guard every batch.

**Tech Stack:** Go 1.24, `github.com/danielgtaylor/huma/v2`, `github.com/go-chi/chi/v5`, `openapi-typescript@7.13.0`, `openapi-fetch@0.17.0`.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md`. Every deviation is called out in the batch's plan file.
- **Wire compatibility:** existing `data` / `meta` / `error` envelopes, HTTP status codes, field names, and trailing-slash aliases stay unchanged. Envelope fixture diffs run per batch.
- **Single-router rule:** every `(method, path)` is owned by exactly one router. After each batch, the chi entries the batch migrates are deleted in the same PR. `TestNoDualRegistrationInsideHuma` guards Huma's side; a batch-scoped fixture (Task 6 below) guards the chi↔huma boundary for the routes migrated by this batch.
- **Default deny:** unknown permissions, unknown roles, missing resource resolvers, missing policies, and resolver errors all deny.
- **Batch 1 is a hard prerequisite.** Do not start Batch 2 until Batch 1's plan is fully executed and its commits are merged.
- **Behavior preservation.** No batch tightens or relaxes existing rules except where the spec explicitly says so. The only rule change scheduled for the whole migration is Batch 8 relaxing `canSetMemberQuota` — nothing else.
- **Every commit ends with:** `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## Index — Batches 2 through 14

Each row: batch number, subsystem letters from spec §2, endpoint count, headline difficulties, and the plan file to author when starting the batch.

| # | Subsystems | Endpoints | Headline difficulties | Plan file |
|---|-----------|-----------|-----------------------|-----------|
| 2 | **A — Auth (public)** | 6 | Public writes; 302 redirects (`GET /auth/oauth/{provider}/redirect`); OAuth callback body with `code`+`state` | `2026-07-11-huma-admin-batch-02-auth.md` |
| 3 | **B — Users write (1)** + **C — Plans writes (3)** | 4 | Simple superadmin writes; catalog validation for plans | `2026-07-11-huma-admin-batch-03-users-plans-writes.md` |
| 4 | **D — Models catalog (5)** | 5 | Non-UUID `{name}` path parameter; in-memory catalog refresh on writes | `2026-07-11-huma-admin-batch-04-models.md` |
| 5 | **E — Admin global reads (4)** + **F — Notifications user (4)** + **F — Notifications admin (4)** | 12 | Global requests; http-log binary response (uses `contract.BytesResponse`); audience validation in handler; new `system.notifications.*` permissions | `2026-07-11-huma-admin-batch-05-admin-notifications.md` |
| 6 | **G — Extra usage (8)** | 8 | New `project.extra_usage.*` permissions; payment provider integration; `RequireProjectMembership()` on topup POST to keep audit trail clean | `2026-07-11-huma-admin-batch-06-extra-usage.md` |
| 7 | **H — Projects CRUD (5)** | 5 | `POST /projects` with no project scope (falls back to `Authenticated()`); max_projects check stays in handler | `2026-07-11-huma-admin-batch-07-projects.md` |
| 8 | **I — Members (9)** | 9 | `member` resource resolver; `PolicyMemberSelfOrElevated`; quota rule **relaxed** to owner+maintainer on any target (only behavior change in the migration); flip Batch 1's Task 12 resolver-registry skip to `Skip` no longer needed once `member` resolver registers | `2026-07-11-huma-admin-batch-08-members.md` |
| 9 | **J — API Keys (5)** | 5 | `PolicyKeyOwnedByCallerForDeveloper`; `api-key` resolver returning `Resource.OwnerID = key.CreatedBy`; `POST /keys` response has top-level `key` + `data` (custom body struct — see spec §4.1) | `2026-07-11-huma-admin-batch-09-keys.md` |
| 10 | **K — OAuth grants (2)** + **L — Policies (5)** | 7 | Hydra grant revocation side effect; policy JSON validation; `oauth-grant` + `policy` resolvers | `2026-07-11-huma-admin-batch-10-oauth-grants-policies.md` |
| 11 | **M — Subscriptions (6)** + **N — Orders + Plans (6)** | 12 | `SystemOnProjectPath` first real user (`POST`/`PUT` subscriptions); order cancel state machine 409; payment provider integration; `subscription` + `order` resolvers | `2026-07-11-huma-admin-batch-11-subscriptions-orders.md` |
| 12 | **O — Traces (3)** + **P — Requests + http-log (3)** + **Q — Usage (1)** | 7 | Three-level path (`/traces/{traceID}/requests`); binary http-log via `contract.BytesResponse`; large aggregated usage response | `2026-07-11-huma-admin-batch-12-traces-requests-usage.md` |
| 13 | **R — Upstreams (15)** + **S — Upstream groups (7)** + **T — OAuth clients (5)** | 27 | Encrypted secret DTO masking; per-upstream utilization variants; Hydra API proxy for OAuth clients; `upstream` + `upstream-group` + `oauth-client` + `routing-route` resolvers (routing-route lands in Batch 14) | `2026-07-11-huma-admin-batch-13-upstreams-groups-oauth-clients.md` |
| 14 | **U — Routing (8)** + **V — My-quota / my-membership (2)** + **cleanup** | 10 + cleanup | Delete legacy `projectAccessMiddleware`, `RequireSuperadmin`, `requireRole`, `canSetMemberQuota`, `canAccessKey`, `sameProjectID`, `MemberFromContext`, `UserFromContext`; `internal/admin/` retains only Hydra, device flow, OAuth callbacks, HMAC webhooks | `2026-07-11-huma-admin-batch-14-routing-cleanup.md` |

Endpoint totals: 6 + 4 + 5 + 12 + 8 + 5 + 9 + 5 + 7 + 12 + 7 + 27 + 10 = **117** migrated routes.

---

## When to author a batch plan

Author each batch's plan file **immediately before starting the batch**, not upfront. Rationale:

- Handler code for many subsystems has drifted since the spec was drafted; reading the current legacy handler right before writing the plan produces sharper task-level detail than speculating up front.
- Dashboard hook layout for each subsystem is easier to plan after the previous batch's dashboard PR shows how `unwrapAdminResponse` fits in that subsystem's file tree.
- Batch 8's quota-rule relaxation is the only behavior change; keeping its plan authored close to execution avoids stale rationale.

Follow the template exactly; if a subsystem lacks a new permission (Batches 7, 10, 12, 13, 14 have zero new permissions or policies), delete Task 1 and renumber.

---

## Per-Batch Template (12 tasks)

Copy this template into `2026-07-11-huma-admin-batch-NN-<name>.md`. Fill every `<PLACEHOLDER>` with the concrete value. Do not ship a batch plan that still contains angle-bracketed placeholders.

### File Structure section (top of the batch plan)

For each new file, write:

- Absolute repository path
- One-line description of its responsibility
- Whether it is new or modified

Group by directory: `internal/authz/` (only if new permissions/policies), `internal/api/admin/v1/`, `internal/api/admin/v1/resolvers/`, `internal/admin/routes.go` (deletions), `dashboard/src/api/`, `dashboard/src/features/<subsystem>/` (or equivalent).

### Task 1: New permissions / policies / resolvers for this batch

**Skip this task entirely if the batch introduces none.** Otherwise, for each new permission:

- One `TDD` cycle: fail-test in `internal/authz/permission_test.go` → add constant + role grant → pass-test.
- One commit `feat(authz): add <permission_name>`.

For each new policy (Batches 8 and 9 only if Batch 1 already added the concrete struct, this task registers it — otherwise this task adds both the struct and the registration):

- Fail-test in `internal/authz/policy_<name>_test.go` covering the truth table.
- Implement the `Policy` struct with its `ID()` + `Evaluate()`.
- Register in `DefaultPolicies()` (`internal/api/admin/v1/policies_registry.go`).
- Add a regression case to `TestDefaultPoliciesInstallsNewPolicies` (extend the ID list at the top of that test).
- Commit `feat(authz): add <policy_name>`.

For each new resolver:

- Fail-test in `internal/api/admin/v1/resolvers/<type>_test.go` covering (a) the happy path returning `Resource.ProjectID`, (b) not-found returning `error` (or empty `Resource` — pick one and stay consistent), and (c) `OwnerID` population when the resource has an owning user.
- Implement `<Type>Resolver` and register it via `Default().Register(...)` inside an `init()` function in `resolvers/<type>.go`.
- Commit `feat(adminv1/resolvers): <type> resolver`.

**Bail-out condition:** if a resolver's underlying `store.GetXByID` does not exist, add the store method first in its own commit (with its own store-level test) — do **not** inline a new store method into the resolver commit.

### Task 2: DTOs, input structs, output structs for this batch

Location: `internal/api/admin/v1/<subsystem>.go` (mirrors existing `plans.go` / `users.go` shape).

- Declare every request input struct with path/query/header tags Huma understands (`path:"..."`, `query:"..."`, `header:"..."`).
- Declare every response body via `DataResponse[T]`, `ListResponse[T]`, or an explicit body struct when the wire shape does not fit the envelope (record the reason inline; the only known cases are `POST /keys` and any endpoint returning `application/octet-stream`).
- Declare `<subsystem>Store` interface methods with the exact signatures the handlers will need. Match legacy handler return types byte-for-byte; do not introduce new fields until the migration is complete and can carry a separate rename PR.

TDD cycle: for each DTO with non-trivial normalization (pagination defaults, enum validation, integer bounds), write a `<Name>_test.go` decode/encode assertion. Commit `feat(adminv1/<subsystem>): DTOs and store surface`.

### Task 3: Handler methods and `register<Subsystem>Operations`

Location: same file as Task 2.

Each handler method:

- Signature `func (s *Server) <name>(ctx context.Context, input *<inputType>) (*<outputType>, error)`.
- On error, return `contract.NewError(...)` — never write to the response directly.
- Wrap store calls; translate store's `nil` object to `not_found`, DB errors to `internal`, malformed body to `bad_request`.
- Call `authorizationFromContext(ctx)` when the handler needs `Principal` / `Role` / `Resource`.
- **Do not** re-check permissions inside the handler; the middleware has already done it. The handler enforces business invariants (order state, catalog membership, etc.), not authz.

Registration:

- Add a `register<Subsystem>Operations(api huma.API, server *Server)` function.
- Call `contract.Register` (or `contract.RegisterWithLegacyTrailingSlash` for chi legacy aliases) with an `Operation` per route.
- Wire the `AccessPolicy` exactly as spec §5 states for each row.
- Call `register<Subsystem>Operations(api, server)` from `Register(api, server)` in `operations.go`.

Commit `feat(adminv1/<subsystem>): typed <count> operations`.

### Task 4: Per-endpoint authorization matrix tests

Location: `internal/api/admin/v1/<subsystem>_test.go`.

For each route in the batch, iterate:

- 3 roles (developer / maintainer / owner) × {member of project, non-member} × {superadmin toggle}.
- For resource-bound routes, add {resource in same project, resource in another project (IDOR)}.
- Assert HTTP status code and error `code` (never message text).

Use a `newTestServer(t)` helper local to the file. Wire fake stores that record captured arguments so the test can assert both the auth decision and, when required, the store method call.

Commit `test(adminv1/<subsystem>): authorization matrix`.

### Task 5: Envelope-fixture regression tests

Location: `internal/api/admin/v1/<subsystem>_envelopes_test.go`.

For every list/single response added in this batch, capture a golden fixture of the expected JSON body (with fixed timestamps, IDs) and compare byte-for-byte. This is the ratchet that prevents accidental `omitempty` / null-vs-empty-slice drift.

Commit `test(adminv1/<subsystem>): envelope fixtures`.

### Task 6: Delete the migrated chi routes

Location: `internal/admin/routes.go` and, if a whole sub-router disappears, the corresponding `handle_<subsystem>.go` (delete legacy handler functions that are no longer referenced).

- Remove `r.Get/Post/Put/Delete/Patch` lines that overlap with this batch's Huma registrations.
- If a subsystem's entire chi sub-router disappears (Batches 3, 4, 6, 10, 13, 14), delete the enclosing `r.Route(...)` block too.
- Grep for orphaned handler functions:

  ```
  grep -rn "func handle<Subsystem>" internal/admin/
  ```

  Delete every function no longer referenced from `routes.go`.
- Grep for orphaned test helpers:

  ```
  grep -rn "^func Test" internal/admin/ | grep <subsystem>
  ```

  Delete tests whose subject function no longer exists; migrate any surviving invariant tests into `internal/api/admin/v1/`.

Commit `refactor(admin): remove legacy <subsystem> chi routes`.

### Task 7: Batch-scoped dual-registration guard

Add a batch-specific test in `internal/api/contract/invariants_test.go` (or an adjacent file that shares the package) that fails if any of the paths this batch migrates is still registered on the underlying chi router **through the legacy admin package**. Concrete shape:

```go
func TestBatch<NN>NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{"GET", "/api/v1/<path-1>"},
		// ... one entry per migrated route
	}
	router := chi.NewRouter()
	admin.MountRoutes(router, nil, &config.Config{}, nil, testJWT(t), nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}
```

Commit `test(contract): guard batch <NN> single-registration invariant`.

### Task 8: Regenerate OpenAPI

- Run `go run ./cmd/openapi`.
- Diff `api/openapi/admin.openapi.json`. The diff MUST include the batch's new operations under `paths` and MUST NOT include any operation not owned by this batch or Batch 1.
- Commit the regenerated file: `chore(openapi): regenerate for batch <NN>`.

### Task 9: Dashboard hook migration for this batch's routes

- Run `pnpm api:generate` in `dashboard/`; commit the regenerated `dashboard/src/api/generated/schema.ts`.
- Identify every React Query hook / mutation touching this batch's routes. Grep:

  ```
  grep -rn "/api/v1/<subsystem-prefix>" dashboard/src/
  ```

- Rewrite each hook to use `adminApi.GET|POST|PUT|DELETE(...)` + `unwrapAdminResponse` from `dashboard/src/api/admin-client.ts`.
- Delete the matching methods from `dashboard/src/api/client.ts` (they are no longer used).
- If any local type in `dashboard/src/types.ts` (or elsewhere) is now duplicated by an entry in `components["schemas"]`, delete the local type and import from the generated schema.

Commit `refactor(dashboard): migrate <subsystem> hooks to adminApi`.

### Task 10: Dashboard test + build

- Run `pnpm exec tsc --noEmit` and fix every type error introduced by the schema change.
- Run `pnpm build` and confirm no build errors.
- Commit any adjustments as `fix(dashboard): typescript fallout from <subsystem> migration`.

### Task 11: Full-repo verification gate

- Run `go test ./...` in the repository root.
- Run `pnpm api:check`, `pnpm exec tsc --noEmit`, and `pnpm build` in `dashboard/`.
- Confirm all four contract invariants (from Batch 1's Task 12) pass. In particular:
  - `TestEveryResourceHasResolver` — no longer skips once **any** batch declares a `Resource` binding (Batch 8 onward).
  - `TestEveryOperationHasCatalogPermission` — every new operation's permission must be in `authz.AllPermissions()`. If any is missing, back-fill via Task 1.

No commit unless a fix is applied in the same run.

### Task 12: Update the migration status in `docs/admin-api-openapi-rbac.md`

The existing operator-facing doc lists migrated read operations at the top. After this batch merges, append the newly migrated operations to that list so operators reading the doc see current reality.

Commit `docs(admin-api): batch <NN> route list`.

---

## Notes that apply to specific batches only

**Batch 5 — Notifications.** Task 1 adds two system permissions but no policy. The audience-validation logic (`resolveAudience`) stays inside the handler because it is a business invariant, not an authorization decision (spec §9, "policies decide 'may the caller do this?'").

**Batch 8 — Members.** The `member` resolver is a synthetic in-memory resolver: `Resolve` returns `authz.Resource{Type: "member", ID: ref.ID, ProjectID: ref.ProjectID, OwnerID: ""}`. It does not read from the store; the URL parameter alone provides everything the containment + self-check policies need. Additionally, this batch performs the one intentional behavior change of the migration: `canSetMemberQuota` is deleted, and the new authorization for `PUT /projects/{projectID}/members/{userID}` is `Project(PermissionProjectMembersManage, "projectID", WithResource("member","userID"))` — meaning any maintainer or owner can set any quota. Call this out at the top of the batch plan.

**Batch 9 — Keys.** The `POST /projects/{projectID}/keys` response has both `data` and top-level `key` fields (the plaintext secret). Use the explicit `createKeyResponseBody` struct from spec §4.1 rather than `DataResponse[T]`. Task 5's envelope fixture is critical here — a schema-first migration could easily nest the plaintext under `data` accidentally.

**Batch 11 — Subscriptions.** `SystemOnProjectPath` gets its first two real users (`POST /projects/{projectID}/subscriptions`, `PUT /projects/{projectID}/subscriptions/{subID}`). The authorization matrix test in Task 4 must include a case where a non-superadmin project owner is rejected with 403 — this is the load-bearing behavior of the DSL helper.

**Batch 12 — Http-log binary.** Two operations (`GET /admin/requests/{requestID}/http-log` and `GET /projects/{projectID}/requests/{requestID}/http-log`) return `application/octet-stream` via `contract.BytesResponse`. The dashboard downloads these via a direct `fetch(...)` (not `adminApi.GET`), because openapi-fetch cannot type a binary body. Task 9's dashboard rewrite documents this explicitly.

**Batch 13 — Secrets.** Every upstream / oauth-client DTO must mask secret fields at the DTO layer. Never surface encrypted or plaintext credential material through the typed contract. Task 2's DTO commit must include a test asserting the DTO omits the secret field even when the underlying `types.Upstream` carries one.

**Batch 14 — Cleanup.** After Task 6 in Batch 14 removes the last chi handler in `internal/admin/routes.go` inside the `/api/v1` scope, add a final commit that deletes the now-orphaned helpers: `projectAccessMiddleware`, `RequireSuperadmin`, `requireRole`, `canSetMemberQuota`, `canAccessKey`, `sameProjectID`, `MemberFromContext`, `UserFromContext`, `ctxMember`, `ctxUser`. Grep the entire repository for each name — if any producer or test still references it, the batch is incomplete.

---

## Self-Review Notes

- Every batch plan file created from this template will have 12 tasks in the same order, and every task ends with a commit. Tasks 8, 9, 11 are single-commit tasks; Tasks 2, 3, 4, 5, 6, 7, 10, 12 are single-commit; Task 1 may be multi-commit (one per permission/policy/resolver).
- Cross-batch dependencies: only Batch 1 must complete before any of 2–14 start. Batches 2–14 are otherwise independent enough to reorder if a real-world constraint appears — but the recommended order (spec §6) minimizes churn in shared files (`operations.go`, `admin-client.ts`, `types.ts`).
- The template deliberately avoids inlining example code beyond what the spec already carries. Each per-batch plan file, when authored, MUST include the exact code snippets for that batch's DTOs, handlers, and tests — the template is not itself an execution-ready plan.
- Spec coverage of the template: §3.1 additions live in Batch 1; §3.2 helpers live in Batch 1; §3.3 store fanning is filled by Task 2 of each batch; §3.4 file split is deferred (see Batch 1 self-review); §4.1–4.3 examples are consumed by Batches 9, 11, 12; §5 matrix rows map to per-batch Tasks 3 and 4; §6 order matches this index; §7 dashboard cadence maps to Tasks 9–10 of each batch; §8 CI is Batch 1's Task 13; §9 risks are addressed inline where each risk surfaces.
