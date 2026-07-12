# Huma Admin API — Batch 3: Users write + Plans writes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate 4 superadmin write routes onto the Huma-typed contract: `PUT /api/v1/users/{userID}`, `POST /api/v1/plans`, `PUT /api/v1/plans/{planID}`, `DELETE /api/v1/plans/{planID}`. Establishes the plan-catalog dependency wire on `Server` and the typed-partial-update pattern with pointer-field DTOs.

**Architecture:** Follow the batches-02-14 template. No new authz — all four use existing `System(PermissionSystemUsersWrite)` or `System(PermissionSystemPlansManage)`. New primitives: `Server.Catalog modelcatalog.Catalog` (wired from `cmd/modelserver/main.go`), typed partial-update DTOs using pointer fields (nil = not set) matching legacy `map[string]interface{}` behavior. Two helpers (`normalizeRateMapKeys`, `validateCreditRules`) either duplicated or extracted from `internal/admin/handle_plans.go`.

**Tech Stack:** Go 1.24, Huma v2, chi v5. No new dependencies.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md` §5 rows B1, C1, C2, C3.
- **Wire compatibility:** exact preservation of legacy `handleUpdateUser`, `handleCreatePlan`, `handleUpdatePlan`, `handleDeletePlan` response bodies and status codes. Envelope fixture tests guard.
- **Single-router rule:** the 4 chi routes migrated by this batch are deleted from `internal/admin/routes.go` in Task 6.
- **No behavior change:** no new validation, no tightened errors, no new response fields. In particular preserve:
  - `handleDeletePlan` returns 500 on any store error and 204 on success — it does NOT check "plan exists"; a delete of a nonexistent plan just returns 204 as long as the store doesn't error.
  - `handleUpdateUser` and `handleUpdatePlan` return 400 when no valid fields are provided.
  - `handleUpdatePlan` marshals `model_credit_rates`, `client_model_credit_rates`, `credit_rules`, `classic_rules` to JSON bytes before passing to `store.UpdatePlan` — the store expects `[]byte` for those columns.
- **No dashboard hook migration in this batch** — Dashboard's admin/plans management pages use React Query hooks. The generated `schema.ts` gets regenerated so a future batch can migrate the hooks. Wire compatibility is preserved, so unchanged hooks continue to work.
- **Every commit:** message ends with `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`.

---

## File Structure

**Files added:**

- `internal/api/admin/v1/users_write.go` — DTOs + handler + registration for `PUT /users/{userID}`.
- `internal/api/admin/v1/users_write_test.go` — handler behavior tests.
- `internal/api/admin/v1/plans_write.go` — DTOs + handlers + registration for the 3 plan write routes.
- `internal/api/admin/v1/plans_write_test.go` — handler behavior tests.
- `internal/api/admin/v1/plans_write_envelopes_test.go` — wire-compat fixture tests for create + update responses.

**Files modified:**

- `internal/api/admin/v1/subsystem_stores.go` — flesh out `usersWriteStore` and `plansStore` (rename `planReadStore` → `plansStore` or extend it; see Task 1).
- `internal/api/admin/v1/server.go` — extend `Server` with `Catalog modelcatalog.Catalog` field. If Task 1 renames the store, update the `Plans` field type.
- `internal/api/admin/v1/plans.go` — if `planReadStore` was renamed to `plansStore`, update this file's local usage.
- `internal/api/admin/v1/operations.go` — call `registerUserWriteOperations(api, server)` and `registerPlanWriteOperations(api, server)` from `Register`.
- `internal/admin/routes.go` — delete the 4 lines registering the 4 chi routes.
- `cmd/modelserver/main.go` — populate the new `Server.Catalog` field.
- `internal/api/contract/invariants_test.go` — add `TestBatch03NoLegacyChiOverlap`.
- `api/openapi/admin.openapi.json` — regenerated after Task 6.
- `dashboard/src/api/generated/schema.ts` — regenerated after Task 9.
- `docs/admin-api-openapi-rbac.md` — append the 4 new migrated operations.

**Files NOT touched:**

- `internal/admin/handle_auth.go` (except potentially the `handleUpdateUser` function, which stays for now — Batch 14 cleanup removes it if grep-audit shows no callers)
- `internal/admin/handle_plans.go` (handler bodies stay for now; helper functions `normalizeRateMapKeys`, `normalizeRateMapKeysRaw`, `validateCreditRules` need care — see Task 3 for the extraction/duplication decision)

---

## Task 1: Server.Catalog field + plansStore interface

**Files:**
- Modify: `internal/api/admin/v1/server.go` — add `Catalog modelcatalog.Catalog` field
- Modify: `internal/api/admin/v1/subsystem_stores.go` — replace `plansWriteStore interface{}` stub with real interface; extend or rename `planReadStore` to include write methods

**Interfaces:**
- Consumes: `modelcatalog.Catalog`, existing `types.Plan`, `types.User`, `types.PaginationParams`
- Produces:
  - New `Server.Catalog modelcatalog.Catalog` field
  - `plansStore` interface with 5 methods: `ListPlansPaginated`, `GetPlanByID` (from planReadStore) + `CreatePlan`, `UpdatePlan`, `DeletePlan` (new)
  - `usersWriteStore` interface with `UpdateUser(id string, updates map[string]any) error` and `GetUserByID(id string) (*types.User, error)`

**Design choice — extend vs. rename:** the current `planReadStore` name is now inaccurate (it will grow to include writes). Choose one:
  - **Rename to `plansStore`** (recommended — cleaner going forward). Requires updating `internal/api/admin/v1/plans.go`'s type reference and `Server.Plans planReadStore` → `Server.Plans plansStore`.
  - Or add a second `plansWriteStore` interface and a second Server field. Uglier.

**Recommend rename.** The rename cascades only through the adminv1 package.

Similarly `usersWriteStore` — `Server.Auth authStore` (from Batch 2) already has `UpdateUser` and `GetUserByID`. Options:
  - Reuse `Server.Auth` for the users write handler. Cleanest — no new field on Server.
  - Add `Server.UsersWrite usersWriteStore`. Duplicative.

**Recommend reuse.** The `usersWriteStore` stub in `subsystem_stores.go` becomes unnecessary — delete it and document that B1 consumes `Server.Auth`.

- [ ] **Step 1: Rename `planReadStore` → `plansStore` and extend**

In `internal/api/admin/v1/plans.go`, rename the `planReadStore` type + all references:

```go
type plansStore interface {
	ListPlansPaginated(types.PaginationParams) ([]types.Plan, int, error)
	GetPlanByID(string) (*types.Plan, error)
	CreatePlan(*types.Plan) error
	UpdatePlan(id string, updates map[string]any) error
	DeletePlan(id string) error
}
```

Move this declaration to `internal/api/admin/v1/subsystem_stores.go` (replacing the current `plansWriteStore interface{}` stub — delete the stub entirely, since `plansStore` covers both reads and writes).

Update `Server.Plans` field type from `planReadStore` to `plansStore`.

- [ ] **Step 2: Delete the `usersWriteStore` stub**

In `subsystem_stores.go`, delete the `type usersWriteStore interface{}` line. Add a comment on the `authStore` interface noting that B1 (PUT /users/{userID}) also consumes it via `Server.Auth`.

- [ ] **Step 3: Add `Catalog modelcatalog.Catalog` to Server**

In `internal/api/admin/v1/server.go`, add the field alongside `Auth`, `JWT`, `EncKey`:

```go
Catalog modelcatalog.Catalog
```

Add the `modelcatalog` import to `server.go` if not already present.

- [ ] **Step 4: Compile check**

```
cd /root/coding/modelserver
go build ./internal/api/admin/v1/
```

Expected: success.

- [ ] **Step 5: Run existing tests to ensure no regressions**

```
go test ./internal/api/admin/v1/ ./internal/api/contract/
```

Expected: all green. In particular `TestCommittedOpenAPIDocumentIsCurrent` continues to pass (no OpenAPI change yet since no new operations are registered).

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin/v1/plans.go internal/api/admin/v1/subsystem_stores.go internal/api/admin/v1/server.go
git commit -m "feat(adminv1): plansStore + Server.Catalog surface for batch 03

Renames planReadStore → plansStore and extends with CreatePlan/
UpdatePlan/DeletePlan for the upcoming batch 3 write handlers. Adds
Server.Catalog (modelcatalog.Catalog) field the plan write handlers
will use to validate model_credit_rates keys. B1 (PUT /users/{userID})
will consume the existing Server.Auth authStore rather than a
duplicate usersWriteStore field.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: B1 handler — PUT /api/v1/users/{userID}

**Files:**
- Create: `internal/api/admin/v1/users_write.go` (DTOs + handler; registration deferred to Task 6)
- Create: `internal/api/admin/v1/users_write_test.go`

**Interfaces:**
- Consumes: `Server.Auth.UpdateUser`, `Server.Auth.GetUserByID`, `contract.NewError`
- Produces:
  - `UpdateUserInput` — path param `userID` + body with 4 optional pointer fields
  - `UpdateUserOutput` — `Body DataResponse[User]`
  - `(*Server).updateUser(ctx, in *UpdateUserInput) (*UpdateUserOutput, error)`

**Wire preservation:**
- Legacy iterates over `body["nickname"]`, `body["status"]`, `body["is_superadmin"]`, `body["max_projects"]` — any present field is passed through to `st.UpdateUser` as-is
- Empty updates → 400 `bad_request` "no valid fields to update"
- Success → 200 with `{data: user}` (using `adminv1.User` DTO)

**DTO:**

```go
type UpdateUserInput struct {
	UserID string `path:"userID" doc:"User identifier."`
	Body   struct {
		Nickname     *string `json:"nickname,omitempty"`
		Status       *string `json:"status,omitempty" enum:"active,disabled"`
		IsSuperadmin *bool   `json:"is_superadmin,omitempty"`
		MaxProjects  *int    `json:"max_projects,omitempty" minimum:"0"`
	}
}

type UpdateUserOutput struct {
	Body DataResponse[User]
}
```

**Note on `UserID`:** legacy handler passes `chi.URLParam(r, "userID")` directly to `st.UpdateUser` without UUID validation. Preserve — no `format:"uuid"` tag.

**Note on Huma-vs-map partial update semantics:** with pointer fields, `omitempty` in JSON tag ensures unset fields don't appear in the marshaled request body's OpenAPI schema as required. When decoded, nil pointer means "field absent". This exactly mirrors the legacy map-based behavior.

Handler:

```go
func (s *Server) updateUser(ctx context.Context, input *UpdateUserInput) (*UpdateUserOutput, error) {
	if s == nil || s.Auth == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "user write handler is not configured", nil)
	}

	updates := map[string]any{}
	if input.Body.Nickname != nil {
		updates["nickname"] = *input.Body.Nickname
	}
	if input.Body.Status != nil {
		updates["status"] = *input.Body.Status
	}
	if input.Body.IsSuperadmin != nil {
		updates["is_superadmin"] = *input.Body.IsSuperadmin
	}
	if input.Body.MaxProjects != nil {
		updates["max_projects"] = *input.Body.MaxProjects
	}
	if len(updates) == 0 {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "no valid fields to update", nil)
	}

	if err := s.Auth.UpdateUser(input.UserID, updates); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update user", nil)
	}
	user, err := s.Auth.GetUserByID(input.UserID)
	if err != nil || user == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update user", nil)
	}
	return &UpdateUserOutput{Body: DataResponse[User]{Data: userDTO(user)}}, nil
}
```

Test cases (TDD):

1. Empty body → 400 `bad_request` "no valid fields to update"
2. `UpdateUser` error → 500 `internal`
3. `GetUserByID` returns nil after successful update → 500 `internal` (unusual but possible on race)
4. Happy path with all 4 fields → 200 with updated user in `data`
5. Happy path with only `nickname` → 200; assert only `nickname` was in the updates map (use a recording fake)

- [ ] **Step 1: Write failing tests**

Extend `internal/api/admin/v1/users_write_test.go` with the 5 cases above. Use a recording fake `authStore` that captures the `updates` map argument.

- [ ] **Step 2: Run and verify failing**

```
go test ./internal/api/admin/v1/ -run TestUpdateUser -v
```

Expected: FAIL — `s.updateUser undefined`.

- [ ] **Step 3: Implement handler in `users_write.go`**

Include the imports for `context`, `net/http`, `contract` package.

- [ ] **Step 4: Run tests, verify green**

```
go test ./internal/api/admin/v1/ -run TestUpdateUser -v
```

Expected: 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/users_write.go internal/api/admin/v1/users_write_test.go
git commit -m "feat(adminv1/users): typed PUT /users/{userID} handler + tests

Preserves legacy handleUpdateUser wire behavior byte-for-byte: only
fields present in the request body get propagated to the store; empty
body → 400 bad_request no valid fields; store or lookup failure → 500
internal. Uses pointer-field DTO to mirror the legacy
map[string]interface{} 'field absent' semantics.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: C1 handler — POST /api/v1/plans

**Files:**
- Create: `internal/api/admin/v1/plans_write.go` (DTOs + `createPlan` handler; other handlers land in Tasks 4-5)
- Create: `internal/api/admin/v1/plans_write_test.go`

**Interfaces:**
- Consumes: `Server.Plans.CreatePlan`, `Server.Catalog.NormalizeNames`, `contract.NewError`, `types.Plan`, `types.CreditRule`, `types.CreditRate`, `types.ClassicRule`
- Produces:
  - `CreatePlanInput` — body with 12 fields matching legacy
  - `CreatePlanOutput` — `Body DataResponse[Plan]` where `Plan` is either `types.Plan` (simplest) or a new `adminv1.Plan` DTO
  - `(*Server).createPlan(ctx, in *CreatePlanInput) (*CreatePlanOutput, error)`
  - Local helper `normalizeRateMapKeys` **duplicated** from `internal/admin/handle_plans.go` into `internal/api/admin/v1/plan_helpers.go` (following Batch 2's oauth_helpers precedent — legacy version stays until Batch 14 grep-audit).
  - Local helper `validateCreditRules` also duplicated to `plan_helpers.go`.

**Plan DTO decision:** legacy `st.CreatePlan(&types.Plan{...})` uses `types.Plan` directly. If Huma's schema registry duplicate-name panic that hit Batch 2 (User) also hits Plan, we'll need `adminv1.Plan` DTO. **Try `types.Plan` first**; if the schema-registry panic fires, extract a DTO. Record the outcome in the report.

**Wire preservation:**
- Legacy body has 12 top-level fields; keep the exact JSON keys
- Validation: `name` and `slug` required → 400 "name and slug are required"
- Default: `PeriodMonths <= 0` → set to 1
- `validateCreditRules` → 400 with the error's message
- `normalizeRateMapKeys` catalog error → 400 `unknown_models` with details (see `writeUnknownModelsError` in `internal/admin` for the exact envelope)
- Success → 201 with `{data: plan}`

**writeUnknownModelsError envelope:** this legacy helper returns `{"error":{"code":"unknown_models","message":"unknown model names: ...","details":["model1","model2"]}}` (approximately). Read `internal/admin/handle_models.go` or similar for the exact shape and replicate via `contract.NewError(400, "unknown_models", msg, details)`.

**Test cases (TDD):**

1. Missing `name` → 400 `bad_request` "name and slug are required"
2. Missing `slug` (name present) → same
3. Empty body → same (both missing)
4. `PeriodMonths = 0` → success, stored value is 1 (assert via recording fake)
5. `PeriodMonths = -1` → success, stored value is 1
6. Invalid credit rule (month-window with fixed type) → 400 `bad_request` with legacy error message
7. Unknown model in `model_credit_rates` → 400 `unknown_models` with `details`
8. `_default` sentinel key in `model_credit_rates` preserved verbatim (not sent to `NormalizeNames`)
9. Happy path → 201 with `{data: plan}` and `IsActive: true`

Handler code follows the legacy pattern in `handle_plans.go:81-138` with these substitutions:
- `writeError(...)` → `contract.NewError(...)`
- `writeData(w, 201, plan)` → `return &CreatePlanOutput{Body: DataResponse[types.Plan]{Data: *plan}}, nil` (and set `DefaultStatus: 201` on the Operation in Task 6)

- [ ] **Step 1: Write failing tests + fake plansStore**

Add a `fakePlansStore` type at file scope in `plans_write_test.go`:

```go
type fakePlansStore struct {
	created *types.Plan
	updated struct {
		id      string
		updates map[string]any
	}
	deleted string
	getErr  error
	plan    *types.Plan
}

// implement plansStore + ...
```

Add a `fakeCatalog` implementing enough of `modelcatalog.Catalog` for the tests. Only `NormalizeNames(names []string) ([]string, error)` matters — return the input verbatim by default, or return an error for the unknown-model test.

Write the 9 test cases.

- [ ] **Step 2: Run and verify failing**

```
go test ./internal/api/admin/v1/ -run TestCreatePlan -v
```

Expected: FAIL — `s.createPlan undefined`.

- [ ] **Step 3: Implement `createPlan` in `plans_write.go` and helpers in `plan_helpers.go`**

Include the local `normalizeRateMapKeys` and `validateCreditRules` copies. Match the legacy behavior in `handle_plans.go` exactly.

- [ ] **Step 4: Run and verify green**

```
go test ./internal/api/admin/v1/ -run TestCreatePlan -v
```

Expected: 9 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/plans_write.go internal/api/admin/v1/plans_write_test.go internal/api/admin/v1/plan_helpers.go
git commit -m "feat(adminv1/plans): typed POST /plans handler + tests

Preserves legacy handleCreatePlan behavior byte-for-byte: name/slug
required (400), PeriodMonths defaults to 1, validateCreditRules on
CreditRules, normalizeRateMapKeys on ModelCreditRates (with
_default sentinel preserved), IsActive: true on the created plan,
201 with {data: plan}. Helpers duplicated from internal/admin/
handle_plans.go; the legacy copies stay until Batch 14 cleanup.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: C2 handler — PUT /api/v1/plans/{planID}

**Files:**
- Modify: `internal/api/admin/v1/plans_write.go` (append `updatePlan` handler)
- Modify: `internal/api/admin/v1/plans_write_test.go` (append tests)

**Interfaces:**
- Consumes: `Server.Plans.UpdatePlan`, `Server.Plans.GetPlanByID`, `Server.Catalog.NormalizeNames`, `contract.NewError`, `validateCreditRules`, `normalizeRateMapKeysRaw`
- Produces:
  - `UpdatePlanInput` — path `planID` + partial-update body (see below)
  - `UpdatePlanOutput` — `Body DataResponse[types.Plan]`
  - `(*Server).updatePlan(ctx, in *UpdatePlanInput) (*UpdatePlanOutput, error)`
  - Helper `normalizeRateMapKeysRaw` duplicated to `plan_helpers.go` (legacy version stays)

**DTO — partial update with mixed typed + raw JSON fields:**

Legacy accepts a `map[string]interface{}` body with ~13 optional fields, several of which are complex nested structures marshaled to JSON before storage. To preserve exactly while giving a typed OpenAPI:

```go
type UpdatePlanInput struct {
	PlanID string `path:"planID" doc:"Plan identifier."`
	Body   struct {
		Name          *string `json:"name,omitempty"`
		Slug          *string `json:"slug,omitempty"`
		DisplayName   *string `json:"display_name,omitempty"`
		Description   *string `json:"description,omitempty"`
		TierLevel     *int    `json:"tier_level,omitempty"`
		GroupTag      *string `json:"group_tag,omitempty"`
		PriceCNYFen   *int64  `json:"price_cny_fen,omitempty"`
		PriceUSDCents *int64  `json:"price_usd_cents,omitempty"`
		PeriodMonths  *int    `json:"period_months,omitempty"`
		IsActive      *bool   `json:"is_active,omitempty"`

		// Complex fields — legacy expects json.RawMessage semantics.
		// Use json.RawMessage so the DTO round-trips arbitrary shapes and
		// the handler can inspect them for validation.
		CreditRules            json.RawMessage            `json:"credit_rules,omitempty"`
		ClassicRules           json.RawMessage            `json:"classic_rules,omitempty"`
		ModelCreditRates       map[string]json.RawMessage `json:"model_credit_rates,omitempty"`
		ClientModelCreditRates map[string]json.RawMessage `json:"client_model_credit_rates,omitempty"`
	}
}
```

Handler unmarshals `CreditRules` into `[]types.CreditRule` for `validateCreditRules`, calls `normalizeRateMapKeys` on `ModelCreditRates`, validates client buckets in `ClientModelCreditRates`, then marshals back to `[]byte` for the store's `updates` map.

**Wire preservation notes:**

- `model_credit_rates` non-object → 400 "model_credit_rates must be an object"
- `client_model_credit_rates` non-object (when not nil) → 400 "client_model_credit_rates must be an object"
- Invalid client bucket → 400 "invalid client bucket in client_model_credit_rates: <bucket>"
- `credit_rules` validation via `validateCreditRules`
- Empty updates → 400 "no valid fields to update"
- Success → 200 with `{data: plan}`
- Store failure → 500 "failed to update plan"

**Test cases (TDD):**

1. Empty body → 400 `bad_request` "no valid fields to update"
2. `model_credit_rates` with unknown model → 400 `unknown_models`
3. `_default` sentinel preserved in `model_credit_rates`
4. `client_model_credit_rates` with invalid bucket → 400 `bad_request`
5. `client_model_credit_rates` with all valid buckets → success
6. Invalid `credit_rules` (fixed window with month) → 400 `bad_request`
7. Happy path with all fields → 200 with updated plan; assert the store's `updates` map contains the expected keys with `[]byte` values for complex fields
8. Only `is_active: false` in body → 200; assert only `is_active` in updates map

- [ ] **Step 1: Write failing tests**

Append the 8 cases to `plans_write_test.go`. Reuse `fakePlansStore` and `fakeCatalog` from Task 3.

- [ ] **Step 2: Run and verify failing**

```
go test ./internal/api/admin/v1/ -run TestUpdatePlan -v
```

Expected: FAIL — `s.updatePlan undefined`.

- [ ] **Step 3: Implement `updatePlan`**

Full handler; see the legacy at `internal/admin/handle_plans.go:152-238`. Substitutions:
- `writeError` → `contract.NewError` returning error
- `writeUnknownModelsError` → `contract.NewError(400, "unknown_models", msg, details)` with the same details shape
- `st.UpdatePlan(planID, updates)` → `s.Plans.UpdatePlan(planID, updates)`
- Final `writeData` → `return &UpdatePlanOutput{Body: DataResponse[types.Plan]{Data: *plan}}, nil`

- [ ] **Step 4: Run and verify green**

```
go test ./internal/api/admin/v1/ -run TestUpdatePlan -v
```

Expected: 8 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/plans_write.go internal/api/admin/v1/plans_write_test.go internal/api/admin/v1/plan_helpers.go
git commit -m "feat(adminv1/plans): typed PUT /plans/{planID} handler + tests

Preserves legacy handleUpdatePlan wire behavior: partial updates via
pointer-field DTO for scalar columns + json.RawMessage for complex
JSON columns (credit_rules, classic_rules, model_credit_rates,
client_model_credit_rates). Complex fields are validated then
re-marshaled to []byte for the store, matching legacy behavior.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: C3 handler — DELETE /api/v1/plans/{planID}

**Files:**
- Modify: `internal/api/admin/v1/plans_write.go` (append `deletePlan` handler)
- Modify: `internal/api/admin/v1/plans_write_test.go` (append tests)

**Interfaces:**
- Consumes: `Server.Plans.DeletePlan`, `contract.NewError`
- Produces:
  - `DeletePlanInput` — path `planID` only
  - `DeletePlanOutput` — empty; response is 204 No Content
  - `(*Server).deletePlan(ctx, in *DeletePlanInput) (*DeletePlanOutput, error)`

**Wire preservation:**
- Legacy does NOT check "plan exists" — a delete of a nonexistent plan just returns 204 as long as store doesn't error
- 500 `internal` "failed to delete plan" on store error
- 204 No Content on success

**204 pattern in Huma:** the operation output struct can be an empty struct `struct{}` with `DefaultStatus: 204` on the Operation. The handler returns `&DeletePlanOutput{}, nil`.

Actually the cleanest: return `(*DeletePlanOutput, error)` where `DeletePlanOutput` is `struct{}`. Alternatively return `(*struct{}, error)`. Either works.

**Test cases (TDD):**

1. Store returns error → 500 `internal`
2. Success (whether plan existed or not) → 204 No Content, no body

- [ ] **Step 1: Write failing tests**
- [ ] **Step 2: Run and verify failing**
- [ ] **Step 3: Implement `deletePlan`**

```go
type DeletePlanInput struct {
	PlanID string `path:"planID" doc:"Plan identifier."`
}

type DeletePlanOutput struct{}

func (s *Server) deletePlan(_ context.Context, input *DeletePlanInput) (*DeletePlanOutput, error) {
	if s == nil || s.Plans == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "plan management store is not configured", nil)
	}
	if err := s.Plans.DeletePlan(input.PlanID); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to delete plan", nil)
	}
	return &DeletePlanOutput{}, nil
}
```

- [ ] **Step 4: Run and verify green**
- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/plans_write.go internal/api/admin/v1/plans_write_test.go
git commit -m "feat(adminv1/plans): typed DELETE /plans/{planID} handler + tests

Preserves legacy handleDeletePlan behavior: no existence check
before delete, 204 on success (whether plan existed or not),
500 on store error.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: Register operations, delete chi routes, wire main.go

**Files:**
- Modify: `internal/api/admin/v1/users_write.go` (add `registerUserWriteOperations`)
- Modify: `internal/api/admin/v1/plans_write.go` (add `registerPlanWriteOperations`)
- Modify: `internal/api/admin/v1/operations.go` (call both from `Register`)
- Modify: `internal/admin/routes.go` (delete 4 lines: `PUT /users/{userID}`, `POST /plans/`, `PUT /plans/{planID}/`, `DELETE /plans/{planID}/`)
- Modify: `cmd/modelserver/main.go` (populate `Server.Catalog: catalog`)

**Interfaces:**
- Produces: 4 operation registrations

- [ ] **Step 1: Add `registerUserWriteOperations`**

```go
func registerUserWriteOperations(api huma.API, server *Server) {
	contract.Register(api, contract.Operation{
		ID:            "updateUser",
		Method:        http.MethodPut,
		Path:          "/api/v1/users/{userID}",
		Summary:       "Update user",
		Tags:          []string{"Users"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authz.System(authz.PermissionSystemUsersWrite),
		Authorize:     server.authorizationMiddleware,
	}, server.updateUser)
}
```

- [ ] **Step 2: Add `registerPlanWriteOperations`**

```go
func registerPlanWriteOperations(api huma.API, server *Server) {
	access := authz.System(authz.PermissionSystemPlansManage)

	contract.Register(api, contract.Operation{
		ID:            "createPlan",
		Method:        http.MethodPost,
		Path:          "/api/v1/plans",
		Summary:       "Create plan",
		Tags:          []string{"Plans"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}, server.createPlan)

	contract.Register(api, contract.Operation{
		ID:            "updatePlan",
		Method:        http.MethodPut,
		Path:          "/api/v1/plans/{planID}",
		Summary:       "Update plan",
		Tags:          []string{"Plans"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}, server.updatePlan)

	contract.Register(api, contract.Operation{
		ID:            "deletePlan",
		Method:        http.MethodDelete,
		Path:          "/api/v1/plans/{planID}",
		Summary:       "Delete plan",
		Tags:          []string{"Plans"},
		DefaultStatus: http.StatusNoContent,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        access,
		Authorize:     server.authorizationMiddleware,
	}, server.deletePlan)
}
```

Note: legacy plans routes are chi-mounted with a trailing slash: `r.Post("/", ...)`, `r.Put("/", ...)`, `r.Delete("/", ...)`. The typed contract uses canonical paths without trailing slash. Use `contract.RegisterWithLegacyTrailingSlash` for the 3 plan write operations if the dashboard/callers rely on the trailing-slash spelling. Check dashboard's usage first:

```
grep -rn "/api/v1/plans" dashboard/src/ | grep -v generated
```

If callers use `/api/v1/plans` (no trailing slash), plain `contract.Register` is fine. If they use `/api/v1/plans/`, use `RegisterWithLegacyTrailingSlash`.

- [ ] **Step 3: Call both from `Register`**

Add to `internal/api/admin/v1/operations.go`'s `Register` function, alongside the existing `registerUserReadOperations(api, server)` and `registerPlanReadOperations(api, server)`:

```go
registerUserWriteOperations(api, server)
registerPlanWriteOperations(api, server)
```

- [ ] **Step 4: Delete legacy chi registrations**

In `internal/admin/routes.go`, delete these 4 lines (inside the respective `r.Route(...)` blocks):

```go
r.Put("/{userID}", handleUpdateUser(st))                                    // inside /users route
r.Post("/", handleCreatePlan(st, catalog))                                  // inside /plans route
r.Put("/", handleUpdatePlan(st, catalog))                                   // inside /plans/{planID} route
r.Delete("/", handleDeletePlan(st))                                         // inside /plans/{planID} route
```

Do NOT delete the handler function bodies from `handle_auth.go` / `handle_plans.go` yet — Batch 14 cleanup grep-audits and removes unused ones.

- [ ] **Step 5: Populate `Server.Catalog` in main.go**

In `cmd/modelserver/main.go`, find the `adminv1.Server{...}` construction and add:

```go
Catalog: catalog,
```

The `catalog` local variable is already in scope (see how `admin.MountRoutes` receives it).

- [ ] **Step 6: Run full-repo tests**

```
go test ./...
```

Expected: all green.

- [ ] **Step 7: Commit**

```bash
git add internal/api/admin/v1/users_write.go internal/api/admin/v1/plans_write.go internal/api/admin/v1/operations.go internal/admin/routes.go cmd/modelserver/main.go
git commit -m "feat(admin): register typed users+plans write ops, remove legacy chi routes

The typed handlers own PUT /users/{userID}, POST /plans, PUT /plans/
{planID}, DELETE /plans/{planID}. Four chi registrations removed;
the handler function bodies stay in internal/admin for now (batch 14
cleanup will grep-audit + delete unused).

Wires Server.Catalog from cmd/modelserver/main.go so the plan write
handlers can validate model_credit_rates keys.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: Envelope-fixture regression tests

**Files:**
- Create: `internal/api/admin/v1/plans_write_envelopes_test.go`

**Interfaces:** none new.

Golden asserts on `DataResponse[types.Plan]` and `DataResponse[User]` for the write responses. These match the shapes already tested by Batch 1 (read envelopes) but adding explicit fixtures for the write paths catches accidental field drift.

- [ ] **Step 1: Write fixture tests**

Cover:
- `DataResponse[types.Plan]` marshaled to JSON contains `"data":{...}` where the inner object has expected plan fields
- `DataResponse[User]` (already tested for reads; add a write-path fixture) — `updateUser` response body encodes correctly with all 4 optional fields present
- Empty updates body decoding — verify Huma accepts an empty JSON object `{}` and the handler translates it to 400 no-valid-fields (may already be covered by handler tests; if so, skip)

- [ ] **Step 2: Run**

```
go test ./internal/api/admin/v1/ -run 'TestPlanWriteEnvelope|TestUserWriteEnvelope' -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/admin/v1/plans_write_envelopes_test.go
git commit -m "test(adminv1): envelope fixtures for batch 03 write responses

Locks down DataResponse[types.Plan] and DataResponse[User] serialize
to the legacy {data: ...} wire shape for PUT /users/{userID},
POST /plans, and PUT /plans/{planID}.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: Batch-scoped dual-registration guard

**Files:**
- Modify: `internal/api/contract/invariants_test.go`

- [ ] **Step 1: Add `TestBatch03NoLegacyChiOverlap`**

```go
func TestBatch03NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodPut, "/api/v1/users/{userID}"},
		{http.MethodPost, "/api/v1/plans"},
		{http.MethodPost, "/api/v1/plans/"},
		{http.MethodPut, "/api/v1/plans/{planID}"},
		{http.MethodPut, "/api/v1/plans/{planID}/"},
		{http.MethodDelete, "/api/v1/plans/{planID}"},
		{http.MethodDelete, "/api/v1/plans/{planID}/"},
	}
	router := chi.NewRouter()
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}
```

Note both spellings (with and without trailing slash) for plan routes since the legacy uses trailing.

- [ ] **Step 2: Run**

```
go test ./internal/api/contract/ -run TestBatch03NoLegacyChiOverlap -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/contract/invariants_test.go
git commit -m "test(contract): guard batch 03 single-registration invariant

Ensures the 4 users+plans write chi routes migrated in batch 3 no
longer resolve in the legacy admin.MountRoutes router (both bare and
trailing-slash spellings for the 3 plan routes).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: Regenerate OpenAPI + dashboard schema

- [ ] **Step 1: Regenerate Go spec**

```
cd /root/coding/modelserver
go run ./cmd/openapi
git diff --stat api/openapi/admin.openapi.json
```

Expected: diff shows the 4 new operations + their DTOs.

- [ ] **Step 2: Regenerate TS schema**

```
cd dashboard
pnpm api:generate
git diff --stat src/api/generated/schema.ts
```

Expected: diff shows the 4 new paths + component schemas.

- [ ] **Step 3: TypeScript compiles**

```
pnpm exec tsc --noEmit
```

Expected: no errors.

- [ ] **Step 4: Dashboard builds**

```
pnpm build
```

Expected: successful build.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add api/openapi/admin.openapi.json dashboard/src/api/generated/schema.ts
git commit -m "chore(openapi): regenerate for batch 03 users+plans write operations

Adds updateUser, createPlan, updatePlan, deletePlan to the committed
spec and dashboard schema. No dashboard hook migration in this batch.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 10: Full-repo verification + docs update

- [ ] **Step 1: Full-repo Go tests**

```
go test ./...
```

Expected: all green.

- [ ] **Step 2: Dashboard checks**

```
cd dashboard
pnpm api:check
pnpm exec tsc --noEmit
pnpm build
```

- [ ] **Step 3: Update `docs/admin-api-openapi-rbac.md`**

Add the 4 new migrated operations to the migrated-operations list (append after the Batch 2 auth entries).

- [ ] **Step 4: Commit docs update**

```bash
cd /root/coding/modelserver
git add docs/admin-api-openapi-rbac.md
git commit -m "docs(admin-api): batch 03 route list

Marks the 4 users+plans write routes as migrated to the typed
contract. Four chi registrations removed; handler function bodies
remain until batch 14 cleanup.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- Every task ends with an independently-reviewable commit.
- Types used across tasks: `plansStore` interface (Task 1), `Server.Catalog` (Task 1), `UpdateUserInput/Output` (Task 2), `CreatePlanInput/Output` (Task 3), `UpdatePlanInput/Output` (Task 4), `DeletePlanInput/Output` (Task 5), `registerUserWriteOperations`, `registerPlanWriteOperations` (Task 6).
- All new operations use `System(...)` access — no new resources, no new policies, no new resolvers.
- Two duplicated helpers (`normalizeRateMapKeys`, `validateCreditRules`, and possibly `normalizeRateMapKeysRaw`) into `plan_helpers.go`. Legacy copies stay until Batch 14.
- Spec coverage: §5 B1 (Task 2), §5 C1 (Task 3), §5 C2 (Task 4), §5 C3 (Task 5). Full §6 batch checklist covered by Tasks 1–10.
- No deferred risks anticipated from this batch's scope. Potential surprise: the `Plan` DTO decision (types.Plan direct vs. adminv1.Plan) — if the schema-registry panic hits, extract a DTO in Task 3 and update Task 4/5 to match.
