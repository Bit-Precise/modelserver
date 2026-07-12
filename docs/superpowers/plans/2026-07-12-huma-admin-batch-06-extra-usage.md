# Huma Admin API — Batch 6: Extra Usage (admin + project)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate 8 extra-usage routes across two subsystem letters — G admin (3, superadmin) + G project (5, project-scoped). Activates 3 new permissions from Batch 1's catalog, introduces first real `RequireProjectMembership()` and `WithResource` + resolver use.

**Architecture:** Follow the batches-02-14 template. Introduces 3 firsts:
- First real activation of `project.extra_usage.read` / `.write` / `.topup` permissions (pre-added in Batch 1's role grants: read→all, write→maintainer+owner, topup→maintainer+owner)
- First real `RequireProjectMembership()` DSL option on G7 (superadmin does not bypass, matching legacy's implicit `requireRole` behavior — payment audit stays clean)
- First real `Resource` binding + resolver on G8 — `extra-usage-topup` resolver fetches the Order, verifies it's an extra-usage topup (`OrderType == OrderTypeExtraUsageTopup`), returns the `ProjectID` for the containment check

**Tech Stack:** Go 1.24, Huma v2, chi v5. No new deps.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md` §5 rows G1–G8.
- **Wire compatibility:** exact preservation of the 8 legacy handlers' bodies, status codes, and error-code strings.
- **Custom response shapes preserved:**
  - G4 GetExtraUsage → `extraUsageGetResponse` struct (settings + monthly spent + pricing knobs + min/max topup + daily cap)
  - G7 CreateTopup → `{order_id, channel, currency, amount, credits, payment_url, payment_ref}` at 201
  - G2 AdminDirectTopup → `{project_id, balance_credits}` at 200
  - G1 AdminOverview → `[]adminExtraUsageOverviewRow` (embeds `types.ExtraUsageSettings` + `spend_7d_credits`)
- **G7 special semantics:**
  - Daily-cap check via `SumDailyExtraUsageTopupCredits` → 409 `daily_topup_limit` if would exceed
  - Payment provider nil → 503 `payment_not_configured` (with order marked failed)
  - Payment provider error → 503 `payment_provider_error` (order marked failed)
  - Metrics: `metrics.IncExtraUsageTopupIntent(channel)` on success
- **G8 resource resolver:**
  - Fetch `Order` via `GetOrderByID`
  - Reject non-topup orders (`OrderType != OrderTypeExtraUsageTopup`) — return empty ProjectID → middleware 404
  - Cross-project via containment policy (baked into middleware)
- **Every commit:** message ends with `Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>`.

---

## File Structure

**Files added:**
- `internal/api/admin/v1/extra_usage.go` — 8 handlers + DTOs + registration
- `internal/api/admin/v1/extra_usage_test.go`
- `internal/api/admin/v1/extra_usage_envelopes_test.go`
- `internal/api/admin/v1/resolvers/extra_usage_topup.go` — the resource resolver

**Files modified:**
- `internal/api/admin/v1/subsystem_stores.go` — flesh out `extraUsageStore interface` (G subsystem)
- `internal/api/admin/v1/server.go` — add `ExtraUsage extraUsageStore`, `PayClient billing.PaymentClient`, `BillingCfg config.BillingConfig`, `ExtraUsageCfg config.ExtraUsageConfig` fields
- `internal/api/admin/v1/operations.go` — call `registerExtraUsageOperations`
- `internal/admin/routes.go` — delete 8 chi lines + collapse empty blocks
- `cmd/modelserver/main.go` — wire the 4 new Server fields
- `cmd/modelserver/admin_routes_test.go` — extend `routeTestStore` with new methods
- `internal/api/contract/invariants_test.go` — add `TestBatch06NoLegacyChiOverlap`
- `internal/api/admin/v1/resolvers/registry.go` — register the topup resolver via init or Task 8 explicit call
- `api/openapi/admin.openapi.json` — regen
- `dashboard/src/api/generated/schema.ts` — regen (Task 10)
- `docs/admin-api-openapi-rbac.md` — append 8 migrated operations

---

## Task 1: Store interface + Server surface + resolver

**Interfaces produced:**

```go
type extraUsageStore interface {
    GetExtraUsageSettings(projectID string) (*types.ExtraUsageSettings, error)
    UpsertExtraUsageSettings(projectID string, enabled bool, monthlyLimit int64) (*types.ExtraUsageSettings, error)
    GetMonthlyExtraSpendCredits(projectID string, monthStart time.Time) (int64, error)
    ListExtraUsageTransactions(projectID string, p types.PaginationParams, typeFilter string) ([]types.ExtraUsageTransaction, int, error)
    SumDailyExtraUsageTopupCredits(projectID string, dayStart time.Time) (int64, error)
    CreateOrder(*types.Order) error
    UpdateOrderStatus(orderID, status string) error
    UpdateOrderPayment(orderID, paymentRef, paymentURL, status string) error
    GetOrderByID(id string) (*types.Order, error)
    TopUpExtraUsage(req store.TopUpExtraUsageReq) (int64, error)
    ListExtraUsageSettings() ([]types.ExtraUsageSettings, error)
    SumRecentExtraUsageSpendCredits(projectID string, days int) (int64, error)
    SetExtraUsageBypass(projectID string, bypass bool) (*types.ExtraUsageSettings, error)
}
```

Verify signatures against actual `internal/store/` methods before adding.

**Server fields:**
```go
ExtraUsage    extraUsageStore
PayClient     billing.PaymentClient
BillingCfg    config.BillingConfig
ExtraUsageCfg config.ExtraUsageConfig
```

Add `billing` + `config` imports (some already present).

**Resource resolver** `internal/api/admin/v1/resolvers/extra_usage_topup.go`:

```go
package resolvers

import (
    "context"

    "github.com/modelserver/modelserver/internal/authz"
    "github.com/modelserver/modelserver/internal/types"
)

type orderReader interface {
    GetOrderByID(id string) (*types.Order, error)
}

// ExtraUsageTopupResolver resolves the extra-usage-topup resource by fetching
// the Order and verifying it is a topup order. Non-topup orders (subscription,
// etc.) return an empty ProjectID so the middleware's containment check treats
// them as not-found — matching legacy behavior which returned 404 for any
// order.OrderType != OrderTypeExtraUsageTopup.
type ExtraUsageTopupResolver struct {
    Store orderReader
}

func (r ExtraUsageTopupResolver) Resolve(_ context.Context, ref authz.ResourceReference) (authz.Resource, error) {
    order, err := r.Store.GetOrderByID(ref.ID)
    if err != nil || order == nil {
        return authz.Resource{}, err
    }
    if order.OrderType != types.OrderTypeExtraUsageTopup {
        return authz.Resource{}, nil // empty ProjectID → 404 via containment
    }
    return authz.Resource{
        Type:      "extra-usage-topup",
        ID:        order.ID,
        ProjectID: order.ProjectID,
    }, nil
}
```

Register the resolver via the `resolvers.Default()` registry — either add to `Default()`'s initialization or expose a Registration helper called from Task 8 during main.go wiring. **Recommend init-time registration** by extending the `init()` in `resolvers/registry.go` (or a per-file init in `extra_usage_topup.go`) that calls `Default().Register("extra-usage-topup", ExtraUsageTopupResolver{Store: <needs Server>})`. But wait — the resolver needs a store reference, which isn't available at package-init time. Two options:

- **Option A**: Register the resolver in `cmd/modelserver/main.go` after constructing the store, via `resolvers.Default().Register("extra-usage-topup", resolvers.ExtraUsageTopupResolver{Store: st})`.
- **Option B**: Make the resolver look up the store dynamically — awkward.

**Choose Option A.** Include the registration in Task 8's main.go wiring.

---

## Task 2: G4 (GetExtraUsage) + G5 (UpdateExtraUsage)

**G4 handler**: returns `extraUsageGetResponse` with settings, monthly spent (via `GetMonthlyExtraSpendCredits(projectID, store.MonthWindowStart())`), pricing knobs from `ExtraUsageCfg`. If settings is nil, returns defaults with `Enabled: false, BalanceCredits: 0`. Always populates `MonthlyWindowStart`, `CreditUnitPrices`, `MinTopup`, `MaxTopup`, `DailyTopupLimit`.

**Access**: `Project(PermissionProjectExtraUsageRead, "projectID")` — new permission granted to all 3 project roles.

**G5 handler**: partial update body `{enabled *bool, monthly_limit_credits *int64}`. Preserves-unspecified pattern: fetch existing, override only the fields provided. `monthly_limit_credits < 0` → 400 bad_request. Calls `UpsertExtraUsageSettings(projectID, enabled, monthlyLimit)`. Returns updated settings.

**Access**: `Project(PermissionProjectExtraUsageWrite, "projectID")` — granted to maintainer+owner (matches legacy `requireRole(Owner, Maintainer)`).

Tests (7): G4 empty-settings default, G4 with settings, G4 store error, G5 empty body → 400 or preserves, G5 negative monthly_limit → 400, G5 happy path, G5 store error.

---

## Task 3: G6 (ListExtraUsageTransactions)

Pagination + optional `?type=` filter. Access: `Project(PermissionProjectExtraUsageRead, "projectID")`. Returns `ListResponse[types.ExtraUsageTransaction]`. Store error → 500 "failed to list transactions".

Tests (3): happy path, filter passthrough, store error.

---

## Task 4: G7 (CreateTopup) — biggest handler in batch

Full behavior from `handleCreateExtraUsageTopup:165-315`:

1. Decode body `{channel, amount_fen?, amount_cents?}`
2. Channel dispatch:
   - `wechat`/`alipay`: requires `amount_fen`, forbids `amount_cents`. Validate min/max via `ExtraUsageCfg.MinTopupCNYFen/MaxTopupCNYFen`. `credits = (amt * 1_000_000) / CreditPriceCNYFen`, currency = "CNY"
   - `stripe`: requires `amount_cents`, forbids `amount_fen`. Same pattern with USD config. Currency = "USD"
   - Unknown channel → 400 "channel must be one of: wechat, alipay, stripe"
3. Daily-cap check: `SumDailyExtraUsageTopupCredits(projectID, store.DayWindowStart())`; if `DailyTopupLimitCredits > 0 && today+credits > limit` → 409 `daily_topup_limit`
4. Create Order (`OrderType: OrderTypeExtraUsageTopup`, `Status: Pending`, `Metadata: "{}"`, `ExtraUsageAmountCredits: credits`)
5. If `PayClient == nil` → mark order Failed + 503 `payment_not_configured`
6. `PayClient.CreatePayment(ctx, PaymentRequest{OrderID, ProductName, Channel, Currency, Amount, NotifyURL: BillingCfg.NotifyURL, ReturnURL: BillingCfg.ReturnURL})` → on error, log + mark Failed + 503 `payment_provider_error`
7. `UpdateOrderPayment(orderID, PaymentRef, PaymentURL, OrderStatusPaying)`
8. `metrics.IncExtraUsageTopupIntent(channel)` — preserve
9. **Return 201** with `{order_id, channel, currency, amount, credits, payment_url, payment_ref}`

**Access**: `Project(PermissionProjectExtraUsageTopup, "projectID", RequireProjectMembership())`. Superadmin does NOT bypass (per plan doc §5 G7).

**Custom output DTO**:
```go
type CreateExtraUsageTopupInput struct {
    ProjectID string `path:"projectID" format:"uuid"`
    Body struct {
        Channel     string `json:"channel"`
        AmountFen   *int64 `json:"amount_fen,omitempty"`
        AmountCents *int64 `json:"amount_cents,omitempty"`
    }
}

type CreateExtraUsageTopupResponseData struct {
    OrderID    string `json:"order_id"`
    Channel    string `json:"channel"`
    Currency   string `json:"currency"`
    Amount     int64  `json:"amount"`
    Credits    int64  `json:"credits"`
    PaymentURL string `json:"payment_url"`
    PaymentRef string `json:"payment_ref"`
}

type CreateExtraUsageTopupOutput struct {
    Body DataResponse[CreateExtraUsageTopupResponseData]
}
```

Fake payment client + full test coverage (10+): channel validation branches (5), daily cap 409, payment nil 503, payment error 503, happy paths per channel (2-3), metrics recording verification.

---

## Task 5: G8 (GetTopup) — first `WithResource` + resolver user

Access: `Project(PermissionProjectExtraUsageRead, "projectID", WithResource("extra-usage-topup", "orderID"))`.

Handler is simple: `s.ExtraUsage.GetOrderByID(orderID)` — but the resolver already validated OrderType + containment via the middleware, so the handler doesn't need to re-check. Returns `{data: order}`.

Actually — per the middleware pattern in Batch 1, `resolveAndEvaluatePolicies` runs the resolver, then feeds the `Resource` into the `PolicyInput`. The handler doesn't automatically get the resolved resource — it fetches on its own. So the handler still calls `GetOrderByID` a second time. That's the pattern. Fine — small cost, clean layering.

But for wire preservation: legacy checked both `!sameProjectID` and `OrderType != Topup` → 404. The resolver handles both (empty ProjectID → 404). So handler just fetches and returns.

Tests (3): resource resolver not-found (fake order → 404), resource resolver wrong type (fake non-topup order → 404), happy path.

---

## Task 6: G1 (AdminOverview) — N+1 aggregation

Legacy at `handleAdminExtraUsageOverview:343-361`. `ListExtraUsageSettings()` → for each row, `SumRecentExtraUsageSpendCredits(projectID, 7)` → assemble `[]adminExtraUsageOverviewRow{ExtraUsageSettings, Spend7DaysCredits}`. First store error → 500.

Preserve the N+1 — not this batch's optimization job.

Access: `System(PermissionSystemExtraUsageRead)`.

Custom DTO wrapping `types.ExtraUsageSettings`:
```go
type AdminExtraUsageOverviewRow struct {
    types.ExtraUsageSettings
    Spend7DaysCredits int64 `json:"spend_7d_credits"`
}

type AdminExtraUsageOverviewOutput struct {
    Body DataResponse[[]AdminExtraUsageOverviewRow]
}
```

Tests (3): happy path 2 rows, list error, per-row spend error.

---

## Task 7: G2 (AdminDirectTopup) + G3 (AdminSetBypass)

**G2** at `handleAdminExtraUsageDirectTopup:365-395`. Body `{amount_credits, description}`. `amount_credits <= 0` → 400. Calls `TopUpExtraUsage(TopUpExtraUsageReq{ProjectID, AmountCredits, Reason: ExtraUsageReasonAdminAdjust, Description})`. Returns `{project_id, balance_credits}`. Preserve `metrics.SetExtraUsageBalance(projectID, bal)`.

Access: `System(PermissionSystemExtraUsageManage)`.

**G3** at `handleAdminExtraUsageSetBypass:431-463`. Body `{bypass *bool}`. Non-nil required (nil → 400 "bypass field required"). Calls `SetExtraUsageBypass(projectID, *body.Bypass)`. Logs actor ID via UserFromContext-equivalent (`authorizationFromContext(ctx).Principal.UserID`). Returns updated settings.

Access: `System(PermissionSystemExtraUsageManage)`.

Note: both endpoints have `{projectID}` in the URL but scope is System (superadmin). Use `authz.SystemOnProjectPath(PermissionSystemExtraUsageManage, "projectID")` — same pattern the Subscriptions override uses (per spec §5 M table). This gives us the `x-modelserver-authz` accurate for the audit trail (system permission on project path).

Wait — actually for G1 there's no projectID path param. For G2/G3 there is. G2/G3 need `SystemOnProjectPath`. G1 uses plain `System`.

Tests (5): G2 negative amount → 400, G2 happy path, G3 nil bypass → 400, G3 happy path, G3 actor logging.

---

## Task 8: Register + delete chi + wire main.go + regen spec

Register 8 operations:
- G1 GET `/api/v1/admin/extra-usage/overview` — `System(PermissionSystemExtraUsageRead)`
- G2 POST `/api/v1/admin/extra-usage/projects/{projectID}/topup` — `SystemOnProjectPath(PermissionSystemExtraUsageManage, "projectID")` — 200
- G3 PUT `/api/v1/admin/extra-usage/projects/{projectID}/bypass` — same access — 200
- G4 GET `/api/v1/projects/{projectID}/extra-usage` — `Project(PermissionProjectExtraUsageRead, "projectID")` — 200
- G5 PUT `/api/v1/projects/{projectID}/extra-usage` — `Project(PermissionProjectExtraUsageWrite, "projectID")` — 200
- G6 GET `/api/v1/projects/{projectID}/extra-usage/transactions` — `Project(PermissionProjectExtraUsageRead, "projectID")` — 200
- G7 POST `/api/v1/projects/{projectID}/extra-usage/topup` — `Project(PermissionProjectExtraUsageTopup, "projectID", RequireProjectMembership())` — **201**
- G8 GET `/api/v1/projects/{projectID}/extra-usage/topup/{orderID}` — `Project(PermissionProjectExtraUsageRead, "projectID", WithResource("extra-usage-topup","orderID"))` — 200

Delete 8 chi lines from `internal/admin/routes.go` — inside `/admin/extra-usage` block (G1/G2/G3), and inside `/projects/{projectID}` block (G4/G5/G6/G7/G8). Collapse empty `/admin/extra-usage` route block. Leave `/projects/{projectID}` as-is (still has many other routes).

Wire main.go:
```go
ExtraUsage:    st,
PayClient:     payClient,       // already in scope for admin.MountRoutes
BillingCfg:    cfg.Billing,
ExtraUsageCfg: cfg.ExtraUsage,
```

Plus register the topup resolver:
```go
resolvers.Default().Register("extra-usage-topup", resolvers.ExtraUsageTopupResolver{Store: st})
```

Regen spec.

---

## Task 9: Envelope fixtures + dual-reg guard

Envelope fixtures:
- `extraUsageGetResponse` shape
- `CreateExtraUsageTopupResponseData` at 201
- `AdminExtraUsageOverviewRow` embedded shape
- Admin direct topup response `{project_id, balance_credits}`
- `DataResponse[types.ExtraUsageSettings]` for G3/G5

Batch invariant: `TestBatch06NoLegacyChiOverlap` covers all 8 paths + trailing-slash spellings where applicable.

---

## Task 10: Dashboard regen + docs + verify

`pnpm api:generate` + tsc + build. Append 8 operations to `docs/admin-api-openapi-rbac.md`.

---

## Task 11: Final whole-branch review + PR

Dispatch code-reviewer. Address Critical/Important. Open PR.

---

## Self-Review Notes

Documented deviations to note in PR body:
- Systemic 422 vs 400 for `format:"uuid"` on projectID path params (inherited)
- Same for numeric body validation on `amount_credits` / `monthly_limit_credits` (typed handler still catches negatives → 400)

Deferred to Batch 14:
- Legacy `handle_extra_usage.go` bodies retained
- `deliverExtraUsageTopupOrder` helper (used by billing webhook — stays in chi until webhook migration)

Known-tricky spots for reviewer to check:
- G7 payment provider failure paths (503 with order marked Failed)
- G8 resolver correctly rejects non-topup orders → 404 (empty ProjectID)
- `SystemOnProjectPath` first real user — verify runtime enforces superadmin correctly
- `RequireProjectMembership()` first real use — verify superadmin gets 403 on G7 topup
