# Huma Admin API — Batch 4: Models Catalog Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the 5 superadmin model-catalog routes onto the Huma-typed contract: `GET /api/v1/models`, `GET /api/v1/models/{name}`, `POST /api/v1/models`, `PATCH+PUT /api/v1/models/{name}`, `DELETE /api/v1/models/{name}`. Establishes the non-UUID free-form-string path parameter pattern and reuses `Server.Catalog.Swap(models)` after every write.

**Architecture:** Follow the batches-02-14 template. No new authz — all 5 use existing `System(PermissionSystemModelsRead)` or `System(PermissionSystemModelsManage)`. New primitives: full `modelsStore` interface, in-handler catalog refresh via `Server.Catalog.Swap`. Six validation helpers duplicated from `internal/admin/handle_models.go` into `internal/api/admin/v1/model_helpers.go`.

**Tech Stack:** Go 1.24, Huma v2, chi v5. No new dependencies.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md` §5 D1–D5.
- **Wire compatibility:** exact preservation of legacy `handleListModels`, `handleGetModel`, `handleCreateModel`, `handleUpdateModel`, `handleDeleteModel` bodies and status codes. Envelope fixture tests guard.
- **Single-router rule:** the 6 chi routes migrated by this batch (list + get + create + patch + put + delete — 6, because legacy has both PATCH and PUT on the same handler) are deleted from `internal/admin/routes.go` in Task 7.
- **No behavior change:** preserve every branch of the legacy handlers including:
  - `handleUpdateModel` supports **both** `PATCH` and `PUT` mounted on the same handler — the typed contract registers both operations to the same handler function
  - Model `{name}` path parameter is a **non-UUID free-form string** — no `format:"uuid"` tag; the legal-character validation happens inside the handler for create/update via `validateModelName`
  - `handleUpdateModel` rejects presence of `"name"` in body with 400 "canonical name is immutable; create a new model and retire this one instead" — see Task 5 for the pointer-field partial-update DTO approach
  - `handleDeleteModel` checks `ModelReferenceCountsFor` and returns 409 with the counts as details if any reference exists
  - `refreshCatalog` logs on error and continues (never fails the request) — typed handler preserves this
  - `handleCreateModel` returns 409 on Postgres unique-violation (existing name); other DB errors → 400
- **Dashboard hook migration deferred** — `dashboard/src/api/models.ts` (if it exists) continues using existing hooks; wire preserved so hooks keep working. Generated `schema.ts` refreshed.
- **Every commit:** message ends with `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`.

---

## File Structure

**Files added:**

- `internal/api/admin/v1/models.go` — DTOs + all 5 handler methods + `registerModelOperations`
- `internal/api/admin/v1/models_test.go` — handler tests
- `internal/api/admin/v1/models_envelopes_test.go` — wire-compat fixture tests
- `internal/api/admin/v1/model_helpers.go` — duplicated validation helpers

**Files modified:**

- `internal/api/admin/v1/subsystem_stores.go` — flesh out `modelsStore` interface (currently `interface{}` stub)
- `internal/api/admin/v1/operations.go` — call `registerModelOperations(api, server)` from `Register`
- `internal/admin/routes.go` — delete the 6 lines registering the model chi routes (Get, Post, Get/{name}, Patch/{name}, Put/{name}, Delete/{name})
- `cmd/modelserver/admin_routes_test.go` — extend `routeTestStore` with any new `modelsStore` methods it needs to satisfy the widened interface
- `internal/api/contract/invariants_test.go` — add `TestBatch04NoLegacyChiOverlap`
- `api/openapi/admin.openapi.json` — regenerated after Task 7
- `dashboard/src/api/generated/schema.ts` — regenerated in Task 9
- `docs/admin-api-openapi-rbac.md` — append the 5 new migrated operations

**Files NOT touched:**

- `internal/admin/handle_models.go` — handler bodies + helpers remain until Batch 14 cleanup

---

## Task 1: modelsStore interface + DTOs

**Files:**
- Modify: `internal/api/admin/v1/subsystem_stores.go` — replace `modelsStore interface{}` stub with real interface
- Create: `internal/api/admin/v1/models.go` — DTOs only in this task (handlers land in Tasks 2–6)

**Interfaces produced:**

`modelsStore` interface with 7 methods:
```go
type modelsStore interface {
    ListModels() ([]types.Model, error)
    ListModelsByStatus(status string) ([]types.Model, error)
    GetModelByName(name string) (*types.Model, error)
    CreateModel(*types.Model) error
    UpdateModel(name string, updates map[string]any) error
    DeleteModel(name string) error
    ModelReferenceCountsFor(name string) (store.ModelReferenceCounts, error)
}
```

Note the `store.ModelReferenceCounts` import — this is why the store subpackage is imported into subsystem_stores.go.

`Server.Models modelsStore` field added to `server.go`.

**DTOs (in `models.go`):**

```go
package adminv1

import (
    "encoding/json"

    "github.com/danielgtaylor/huma/v2"
    "github.com/modelserver/modelserver/internal/store"
    "github.com/modelserver/modelserver/internal/types"
)

// ModelListRow wraps types.Model with per-row reference counts, matching the
// legacy modelListResponseRow. Embedded types.Model preserves the same JSON
// field ordering as the legacy handler.
type ModelListRow struct {
    types.Model
    ReferenceCounts store.ModelReferenceCounts `json:"reference_counts"`
}

// ListModelsInput is the request for GET /api/v1/models with optional status filter.
type ListModelsInput struct {
    Status string `query:"status,omitempty" doc:"Optional status filter (active|disabled)."`
}

type ListModelsOutput struct {
    Body DataResponse[[]ModelListRow]
}

type GetModelInput struct {
    Name string `path:"name" doc:"Canonical model name (lowercase; letters/digits/dot/underscore/dash)."`
}

type GetModelOutput struct {
    Body DataResponse[types.Model]
}

type CreateModelInput struct {
    Body struct {
        Name                   string                 `json:"name" minLength:"1"`
        DisplayName            string                 `json:"display_name,omitempty"`
        Description            string                 `json:"description,omitempty"`
        Aliases                []string               `json:"aliases,omitempty"`
        DefaultCreditRate      *types.CreditRate      `json:"default_credit_rate,omitempty"`
        DefaultImageCreditRate *types.ImageCreditRate `json:"default_image_credit_rate,omitempty"`
        Status                 string                 `json:"status,omitempty" enum:"active,disabled"`
        Publisher              string                 `json:"publisher"`
        Metadata               types.ModelMetadata    `json:"metadata,omitempty"`
    }
}

type CreateModelOutput struct {
    Body DataResponse[types.Model]
}

// UpdateModelInput uses pointer-field partial-update semantics. `Name` is
// declared here (with a pointer) so a body containing `"name": ...` is
// rejected in the handler with the legacy "canonical name is immutable"
// message — matching the legacy `if _, ok := body["name"]; ok` check.
// Note: this preserves parity for `"name": "x"` but not `"name": null`
// (JSON null → nil pointer, indistinguishable from omission). Dashboard
// does not send `"name"` in update payloads; this is documented in the
// batch's PR body as an accepted deviation.
type UpdateModelInput struct {
    Name string `path:"name" doc:"Canonical model name."`
    Body struct {
        Name                   *string         `json:"name,omitempty"`
        DisplayName            *string         `json:"display_name,omitempty"`
        Description            *string         `json:"description,omitempty"`
        Status                 *string         `json:"status,omitempty" enum:"active,disabled"`
        Aliases                *[]string       `json:"aliases,omitempty"`
        DefaultCreditRate      json.RawMessage `json:"default_credit_rate,omitempty"`
        DefaultImageCreditRate json.RawMessage `json:"default_image_credit_rate,omitempty"`
        Publisher              *string         `json:"publisher,omitempty"`
        Metadata               json.RawMessage `json:"metadata,omitempty"`
    }
}

type UpdateModelOutput struct {
    Body DataResponse[types.Model]
}

type DeleteModelInput struct {
    Name string `path:"name" doc:"Canonical model name."`
}

type DeleteModelOutput struct{}
```

Note: `ModelListRow` embeds `types.Model` for wire fidelity. If Huma's schema-registry panics on duplicate name (as it did for User in Batch 2), extract an `adminv1.Model` DTO — but try direct first.

- [ ] **Step 1: Flesh out modelsStore interface**

In `internal/api/admin/v1/subsystem_stores.go`, replace `modelsStore interface{}` with the 7-method interface above. Add the `store` import to the file if not present.

- [ ] **Step 2: Add Server.Models field**

In `internal/api/admin/v1/server.go`, add the `Models modelsStore` field alongside existing `Auth`, `JWT`, `EncKey`, `AssignFreePlan`, `Catalog`, `Users`, `Plans`, `Store`.

- [ ] **Step 3: Create `internal/api/admin/v1/models.go` with the DTOs above**

No handler implementations in this task.

- [ ] **Step 4: Compile**

```
cd /root/coding/modelserver
go build ./internal/api/admin/v1/
```

Expected: build succeeds.

- [ ] **Step 5: Extend routeTestStore (if compile fails)**

The `routeTestStore` in `cmd/modelserver/admin_routes_test.go` will need to satisfy the new `modelsStore` interface once `Server.Models` is present. Add stub methods returning zero values.

- [ ] **Step 6: Full-repo compile check**

```
go build ./...
```

Expected: build succeeds. Then:

```
go test ./internal/api/admin/v1/ ./internal/api/contract/ ./cmd/modelserver/
```

Expected: all green (no operations registered yet, so no OpenAPI drift).

- [ ] **Step 7: Commit**

```bash
git add internal/api/admin/v1/subsystem_stores.go internal/api/admin/v1/server.go internal/api/admin/v1/models.go cmd/modelserver/admin_routes_test.go
git commit -m "feat(adminv1): modelsStore + DTOs for batch 04 models catalog

Adds the 7-method modelsStore interface, Server.Models field, and
DTOs for the 5 typed model-catalog operations (List, Get, Create,
Update, Delete). No handlers registered yet — Tasks 2-7 wire them.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: List + Get handlers

**Files:**
- Modify: `internal/api/admin/v1/models.go` (append `listModels` + `getModel` handlers)
- Create: `internal/api/admin/v1/models_test.go` (fake modelsStore + tests)

**Interfaces:**
- Consumes: `Server.Models.ListModels`, `ListModelsByStatus`, `ModelReferenceCountsFor`, `GetModelByName`
- Produces: `(*Server).listModels(...)`, `(*Server).getModel(...)`

**Legacy behavior:**
- `handleListModels`: if `?status=` present → `ListModelsByStatus`, else `ListModels`. For each row, call `ModelReferenceCountsFor(m.Name)`. Return `{data: [ModelListRow]}`.
- `handleGetModel`: 500 on store error, 404 if nil, else `{data: model}`.

**Test cases (7 total):**

listModels (4):
1. Store error on ListModels → 500 internal
2. Empty status → ListModels called, not ListModelsByStatus; happy path
3. Status="active" → ListModelsByStatus called with "active"
4. Reference counts store error → 500 internal "failed to count references"

getModel (3):
1. Store error → 500 internal "failed to get model"
2. Nil model → 404 not_found "model not found"
3. Happy path → 200 with `{data: model}`

- [ ] **Step 1: Write failing tests + fakes**

Add `fakeModelsStore` with recording + configurable errors. Handler-test helper pattern per Batch 3's `newPlansServer`.

- [ ] **Step 2: Verify failing**

```
go test ./internal/api/admin/v1/ -run 'TestListModels|TestGetModel' -v
```

Expected: FAIL — undefined handlers.

- [ ] **Step 3: Implement handlers**

Match legacy behavior exactly.

- [ ] **Step 4: Verify green**

Same command. 7/7 pass.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/models.go internal/api/admin/v1/models_test.go
git commit -m "feat(adminv1/models): typed GET /models + GET /models/{name} handlers

Preserves legacy handleListModels behavior including the N+1
ModelReferenceCountsFor call per row (not optimized in this batch)
and the optional ?status= filter dispatching to
ListModelsByStatus vs ListModels.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Create handler + validation helpers

**Files:**
- Modify: `internal/api/admin/v1/models.go` (append `createModel` handler + `refreshCatalog` local helper)
- Modify: `internal/api/admin/v1/models_test.go` (append tests)
- Create: `internal/api/admin/v1/model_helpers.go` (duplicated helpers)

**Interfaces:**
- Consumes: `Server.Models.CreateModel`, `Server.Models.ListModels`, `Server.Catalog.Swap`
- Produces: `(*Server).createModel(...)`, `refreshCatalog(s *Server)` local helper

**Duplicate helpers into `model_helpers.go`** (copy verbatim from `internal/admin/handle_models.go`):
- `modelLegalChars` const
- `validateModelPayload(name, aliases, status, rate, imageRate)`
- `validateModelName(s)`
- `validateAliases(canonical, aliases)`
- `validatePublisher(p)`
- `validateCreditRate(r)`
- `validateImageCreditRate(r)`
- `isUniqueViolation(err)` — needs `github.com/jackc/pgx/v5/pgconn` import

**refreshCatalog** — local Server method (not a duplicated helper because it needs access to `s.Models` and `s.Catalog`):

```go
// refreshCatalog reloads the in-memory model catalog after a successful
// write. On error the in-memory view stays as-is until the periodic 30s
// reload tick recovers; the log matches the legacy handler.
func (s *Server) refreshCatalog(ctx context.Context) {
    models, err := s.Models.ListModels()
    if err != nil {
        slog.Default().ErrorContext(ctx, "admin: failed to refresh model catalog after write", "error", err)
        return
    }
    s.Catalog.Swap(models)
}
```

Add `log/slog` and `context` imports to models.go.

**createModel handler:**

Follow legacy `handleCreateModel` at lines 111-167 exactly. Substitutions:
- `writeError(400, "bad_request", err.Error())` → `contract.NewError(400, "bad_request", err.Error(), nil)`
- `writeError(409, "conflict", err.Error())` for unique violation → `contract.NewError(409, "conflict", err.Error(), nil)`
- Empty `body.DisplayName` → defaults to `body.Name`
- After `s.Models.CreateModel(m)`, call `s.refreshCatalog(ctx)`
- Return 201 with `{data: model}`

**Test cases (7):**

1. Missing `name` → 400 "name is required"
2. Invalid name (uppercase) → 400 "name must be lowercase"
3. Invalid alias (duplicate) → 400 "duplicate alias"
4. Alias equals canonical → 400 "alias ... cannot equal canonical name"
5. Missing publisher → 400 "publisher is required"
6. Unique-violation from store → 409 conflict
7. Happy path → 201 with `{data: model}`; assert `catalog.swappedModels` reflects the refresh; assert `DisplayName` defaults to `Name` when body's DisplayName is empty

For the unique-violation test, construct a fake error that satisfies `errors.As(err, &*pgconn.PgError)` with `Code: "23505"`.

Extend `fakeCatalog` to record `swappedModels` — this lets tests assert refresh was called.

- [ ] **Step 1: Write failing tests + helpers file**
- [ ] **Step 2: Verify failing**
- [ ] **Step 3: Implement `createModel` + `refreshCatalog`**
- [ ] **Step 4: Verify green (7/7)**
- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/models.go internal/api/admin/v1/models_test.go internal/api/admin/v1/model_helpers.go
git commit -m "feat(adminv1/models): typed POST /models handler + validation helpers

Duplicates validateModelPayload, validateModelName, validateAliases,
validatePublisher, validateCreditRate, validateImageCreditRate, and
isUniqueViolation from internal/admin/handle_models.go into
plan_helpers.go (batch 14 cleanup will remove the legacy copies).
Refresh helper matches legacy log-and-continue on catalog swap errors.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Update handler

**Files:**
- Modify: `internal/api/admin/v1/models.go` (append `updateModel` handler)
- Modify: `internal/api/admin/v1/models_test.go` (append tests)

**Legacy behavior** (`handleUpdateModel` lines 169-285) — the trickiest handler in this batch:

- 404 if `existing == nil`
- Reject if body contains `"name"` (immutable) with 400
- Iterate scalar fields: `display_name`, `description`
- `status` must be `active|disabled` else 400
- `aliases` must be `[]string`, validated via `validateAliases(existing.Name, aliases)`
- `default_credit_rate` and `default_image_credit_rate`: null → clear (store `nil` in updates), object → marshal + validate → store as `[]byte`
- `publisher` validated via `validatePublisher`
- `metadata` marshaled to `[]byte` (no validation)
- Empty updates → 400 "no valid fields to update"
- On store error → 400 (unusual — most store errors are 500 elsewhere; preserve the legacy 400)
- After successful update: `refreshCatalog(ctx)`
- Refetch via `GetModelByName(name)` → return `{data: updated}`

**Test cases (9):**

1. Model not found → 404 (existing == nil)
2. Body has `"name"` field → 400 "canonical name is immutable"
3. Empty body → 400 "no valid fields to update"
4. Invalid status → 400
5. Duplicate alias → 400
6. `default_credit_rate: null` → stored as nil in updates map (assert)
7. `default_credit_rate: {input_rate: -1}` → 400 "credit rates must be non-negative"
8. Valid `default_image_credit_rate` object → stored as `[]byte`
9. Happy path with several fields → 200; assert refresh called; assert refetched via GetModelByName

**Handling `{"name": null}` deviation:** the pointer-field DTO can't distinguish `"name": null` from key absent. This IS a wire deviation from legacy. Grep dashboard to confirm no one sends `"name": null` in update payloads. Document in PR body.

- [ ] **Step 1: Write failing tests**
- [ ] **Step 2: Verify failing**
- [ ] **Step 3: Implement `updateModel`**
- [ ] **Step 4: Verify green (9/9)**
- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/models.go internal/api/admin/v1/models_test.go
git commit -m "feat(adminv1/models): typed PUT+PATCH /models/{name} handler + tests

Preserves handleUpdateModel wire behavior: pointer-field partial
update rejecting presence of \"name\" in body (immutable), null-vs-
object handling for default_credit_rate and default_image_credit_rate
(null clears, object validates then stores as []byte), refresh on
success, refetch via GetModelByName. Same handler serves both
PATCH and PUT — Task 7 registers both.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Delete handler

**Files:**
- Modify: `internal/api/admin/v1/models.go` (append `deleteModel` handler)
- Modify: `internal/api/admin/v1/models_test.go` (append tests)

**Legacy behavior** (`handleDeleteModel` lines 287-317):

- 404 if `existing == nil`
- `ModelReferenceCountsFor(name)` — 500 on error
- If `counts.Total() > 0` → 409 conflict with `counts` as details
- `DeleteModel(name)` — 400 on error (not 500 — preserve)
- `refreshCatalog(ctx)`
- 204

**Test cases (4):**

1. Model not found → 404
2. ReferenceCountsFor error → 500 internal
3. counts.Total() > 0 → 409 conflict with counts as details
4. Happy path (Total() == 0) → 204; refresh called; DeleteModel called

- [ ] **Step 1: Write failing tests**
- [ ] **Step 2: Verify failing**
- [ ] **Step 3: Implement `deleteModel`**
- [ ] **Step 4: Verify green (4/4)**
- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/models.go internal/api/admin/v1/models_test.go
git commit -m "feat(adminv1/models): typed DELETE /models/{name} handler + tests

Preserves reference-count gate — returns 409 with counts as details
if any references exist. Refreshes catalog after successful delete.
Legacy 400 (not 500) on DeleteModel store error preserved.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 6: Register operations + delete chi + regen

**Files:**
- Modify: `internal/api/admin/v1/models.go` (add `registerModelOperations`)
- Modify: `internal/api/admin/v1/operations.go` (call it from `Register`)
- Modify: `internal/admin/routes.go` (delete 6 lines, then delete the empty `/models` route block)
- Modify: `cmd/modelserver/main.go` (populate `Server.Models: st`)
- Regenerate: `api/openapi/admin.openapi.json`

**Registration:**

```go
func registerModelOperations(api huma.API, server *Server) {
    read := authz.System(authz.PermissionSystemModelsRead)
    write := authz.System(authz.PermissionSystemModelsManage)

    contract.Register(api, contract.Operation{
        ID:            "listModels",
        Method:        http.MethodGet,
        Path:          "/api/v1/models",
        Summary:       "List models",
        Tags:          []string{"Models"},
        DefaultStatus: http.StatusOK,
        Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
        Access:        read,
        Authorize:     server.authorizationMiddleware,
    }, server.listModels)

    contract.Register(api, contract.Operation{
        ID:            "getModel",
        Method:        http.MethodGet,
        Path:          "/api/v1/models/{name}",
        Summary:       "Get model",
        Tags:          []string{"Models"},
        DefaultStatus: http.StatusOK,
        Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
        Access:        read,
        Authorize:     server.authorizationMiddleware,
    }, server.getModel)

    contract.Register(api, contract.Operation{
        ID:            "createModel",
        Method:        http.MethodPost,
        Path:          "/api/v1/models",
        Summary:       "Create model",
        Tags:          []string{"Models"},
        DefaultStatus: http.StatusCreated,
        Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError},
        Access:        write,
        Authorize:     server.authorizationMiddleware,
    }, server.createModel)

    // Legacy exposes both PATCH and PUT on the same handler function. Register
    // both — Huma treats them as distinct operations, but they share the
    // handler and the AccessPolicy.
    for _, method := range []string{http.MethodPatch, http.MethodPut} {
        opID := "updateModel"
        if method == http.MethodPut {
            opID = "updateModelPut"
        }
        contract.Register(api, contract.Operation{
            ID:            opID,
            Method:        method,
            Path:          "/api/v1/models/{name}",
            Summary:       "Update model",
            Tags:          []string{"Models"},
            DefaultStatus: http.StatusOK,
            Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
            Access:        write,
            Authorize:     server.authorizationMiddleware,
        }, server.updateModel)
    }

    contract.Register(api, contract.Operation{
        ID:            "deleteModel",
        Method:        http.MethodDelete,
        Path:          "/api/v1/models/{name}",
        Summary:       "Delete model",
        Tags:          []string{"Models"},
        DefaultStatus: http.StatusNoContent,
        Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusConflict, http.StatusInternalServerError},
        Access:        write,
        Authorize:     server.authorizationMiddleware,
    }, server.deleteModel)
}
```

**Chi deletions in `internal/admin/routes.go`** — inside the `/models` block, delete these 6 lines (and then delete the now-empty `r.Route("/models", ...)` block including its `RequireSuperadmin`):

```go
r.Get("/", handleListModels(st))
r.Post("/", handleCreateModel(st, catalog))
// inside r.Route("/{name}", ...):
r.Get("/", handleGetModel(st))
r.Patch("/", handleUpdateModel(st, catalog))
r.Put("/", handleUpdateModel(st, catalog))
r.Delete("/", handleDeleteModel(st, catalog))
```

The empty `r.Route("/{name}", ...)` wrapper and the outer `/models` block both become empty — delete them entirely (Batch 3's Task 8 established this pattern).

**cmd/modelserver/main.go**: add `Models: st` to the `adminv1.Server{...}` construction.

- [ ] **Step 1: Add `registerModelOperations`**
- [ ] **Step 2: Call it from `Register`**
- [ ] **Step 3: Delete chi lines + empty blocks**
- [ ] **Step 4: Wire Server.Models in main.go**
- [ ] **Step 5: Run `go test ./...` — expect `TestCommittedOpenAPIDocumentIsCurrent` to fail (spec drift)**
- [ ] **Step 6: Regenerate spec + re-run tests all green**

```
go run ./cmd/openapi
go test ./...
```

- [ ] **Step 7: Commit**

```bash
git add internal/api/admin/v1/models.go internal/api/admin/v1/operations.go internal/admin/routes.go cmd/modelserver/main.go api/openapi/admin.openapi.json
git commit -m "feat(admin): register typed models operations, remove legacy chi routes

Registers listModels, getModel, createModel, updateModel (PATCH+PUT
share handler), deleteModel. Removes the 6 legacy chi lines and the
now-empty /models route block. Wires Server.Models in main.go.
Includes the regenerated OpenAPI spec.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: Envelope fixtures + dual-registration guard

**Files:**
- Create: `internal/api/admin/v1/models_envelopes_test.go`
- Modify: `internal/api/contract/invariants_test.go` (append `TestBatch04NoLegacyChiOverlap`)

Envelope fixtures:
1. `DataResponse[[]ModelListRow]` serializes to `{"data":[{...}]}` with each row containing `types.Model` fields + `reference_counts` sub-object
2. `DataResponse[types.Model]` for create/update responses

Dual-registration guard — the 6 paths to check:
- `GET /api/v1/models`
- `POST /api/v1/models`
- `GET /api/v1/models/{name}`
- `PATCH /api/v1/models/{name}`
- `PUT /api/v1/models/{name}`
- `DELETE /api/v1/models/{name}`

- [ ] **Step 1: Write envelope fixtures + commit**
- [ ] **Step 2: Add invariant test + commit**

```bash
git add internal/api/admin/v1/models_envelopes_test.go
git commit -m "test(adminv1/models): envelope fixtures for list + single responses

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"

git add internal/api/contract/invariants_test.go
git commit -m "test(contract): guard batch 04 single-registration invariant

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: Dashboard schema regen + docs + final verification

**Files:**
- Modify: `dashboard/src/api/generated/schema.ts` (regen)
- Modify: `docs/admin-api-openapi-rbac.md` (append Batch 4 operations)

- [ ] **Step 1: Regen dashboard schema**

```
cd dashboard
pnpm api:generate
pnpm exec tsc --noEmit
pnpm build
```

All green.

- [ ] **Step 2: Full-repo go test**

```
cd /root/coding/modelserver
go test ./...
```

- [ ] **Step 3: Update docs**

Append to the migrated-operations list in `docs/admin-api-openapi-rbac.md`:

```
And these migrated model-catalog operations (Batch 4):

- `GET /api/v1/models`
- `GET /api/v1/models/{name}`
- `POST /api/v1/models`
- `PATCH /api/v1/models/{name}`
- `PUT /api/v1/models/{name}`
- `DELETE /api/v1/models/{name}`
```

- [ ] **Step 4: Commit**

```bash
git add dashboard/src/api/generated/schema.ts docs/admin-api-openapi-rbac.md
git commit -m "chore(dashboard,docs): regenerate schema.ts + docs for batch 04

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- 8 tasks total (Task 1 setup, Tasks 2–5 handlers, Task 6 register/chi/regen, Task 7 tests, Task 8 dashboard+docs).
- Deferred to Batch 14: dead legacy handlers in `internal/admin/handle_models.go` + duplicated helpers.
- Known wire deviations to document in the PR body:
  - `{"name": null}` in update body: typed pointer-DTO can't distinguish from omission; dashboard doesn't send this shape.
  - Any framework decode error for malformed complex fields (`default_credit_rate` as an integer instead of object): Huma returns 422/400 with a schema-message; legacy returned an explicit 400 message. Dashboard sends only well-formed payloads.
- Non-UUID path param `{name}` gets no `format:"uuid"` tag — value is validated inside the handler via `validateModelName` for create/update. Get/Delete pass the value through as-is (legacy did too).
