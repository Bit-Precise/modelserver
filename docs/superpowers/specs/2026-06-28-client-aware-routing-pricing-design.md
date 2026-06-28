# Client-Aware Routing + Pricing — Design

## Problem

Current routing keys requests by `(project, model, request_kind)`; pricing
keys credits by `(model, usage)`. Neither axis knows which **client** sent
the request, and neither knows whether the request should be billed against
the project's **subscription** or its **extra-usage** balance. As a result:

- All clients hitting the same model land on the same upstream group, so we
  can't dedicate OAuth-backed Claude Code / Codex upstreams (which consume
  the operator's first-party subscriptions) to the subset of traffic that
  actually originates from those clients.
- All clients are billed at the same rate, so the operator can't reflect
  reality where a Claude Code request against the Pro plan should consume
  subscription credits at the plan's rate, while an arbitrary OpenAI SDK
  request against the same model should consume extra-usage credits at the
  official API rate.

## Goal

Introduce a per-request **`billing_mode ∈ {subscription, extra_usage}`**
derived from `(client, subscription_balance)`, and route + price by it:

- **Routing key:** `(project_id, model, request_kind, client, billing_mode)`.
- **Pricing rule:**
  - Claude Code / Claude Desktop / Codex CLI / Codex Desktop requests:
    consume the project's active subscription first (using the plan's rates,
    with an optional per-client overlay). If the subscription is missing or
    its remaining credits are ≤ 0, fall through to extra-usage and use the
    catalog's default rate (the official API rate).
  - All other clients (`other` bucket): always extra-usage, always catalog
    rate. They never consume subscription credits.

Both decisions share the client-bucket identity and the `billing_mode`
derivation. The two halves are decomposed into independent layers but ship
as one PR.

## Non-goals

- **Per-route pricing.** Pricing follows the plan + catalog, not the route.
  Bundling them would couple two concerns and complicate `routes` schema.
- **Per-client overlays at catalog-default level.** Extra-usage always uses
  the catalog's single `default_credit_rate`. If an operator later wants
  per-client extra-usage pricing, that's a separate spec.
- **Plans-page editor for `client_model_credit_rates`.** Plans CRUD already
  accepts arbitrary JSON via the admin API; per-client rate edits land that
  way. A nested two-dimensional editor on the Plans dashboard page is
  YAGNI for v1.
- **Owner-removal protection, OAuth grant changes, or any unrelated cleanup
  in adjacent files.** Scope is strictly routing + pricing.
- **Spoofing-resistant client attestation.** `client_kind` derivation
  remains based on client-controlled features (UA / body shape / trace
  header). The existing security note in
  `subscription_eligibility_middleware.go` already covers the accepted
  tradeoff; this spec inherits it.

## Scope decisions (from brainstorming)

| Decision | Choice |
|---|---|
| Packaging | One spec, one PR, two layers (routing + pricing). |
| Client values | Five buckets: `claude-code-cli`, `claude-desktop`, `codex-cli`, `codex-desktop`, `other`. |
| Routing column shape | Multi-valued `clients []string` + `billing_modes []string`; empty = match any (mirrors `RequestKinds`). |
| Tiebreak | Specificity-first: more-specific routes (non-empty `clients` / `billing_modes`) beat less-specific ones; within the same specificity bucket, `match_priority` desc decides. |
| Pricing schema | Add `client_model_credit_rates: map[client]map[model]CreditRate` to plans/policies. |
| Pricing scope | Plans only. Extra-usage stays single-rate (catalog default). |
| Billing-mode decision site | Extend `SubscriptionEligibilityMiddleware` to also resolve `billing_mode`. Keep the existing `SubscriptionEligibility` output for backward compat. |
| Quota probe | Check `subscription.RemainingCredits > 0` inside the eligibility middleware. Race with later precise debit accepted. |
| Codex Desktop detection | Spec reserves the bucket and the constant; `deriveClientBucket` returns `other` until Codex ships its desktop client and the detection rule is added. |
| Old-route compat | Migration only adds columns with empty-array defaults; existing routes continue to match anything. Operators refine on their own schedule. |
| Admin UI | Routes page only (table columns + Create/Edit dialog + Matrix filters). No new Plans editor. |

## Client buckets

A new file `internal/types/client_bucket.go` defines five constants and one
derivation function:

```go
const (
    ClientBucketClaudeCodeCLI = "claude-code-cli"
    ClientBucketClaudeDesktop = "claude-desktop"
    ClientBucketCodexCLI      = "codex-cli"
    ClientBucketCodexDesktop  = "codex-desktop" // reserved; deriver returns "other" today
    ClientBucketOther         = "other"
)

var AllClientBuckets = []string{
    ClientBucketClaudeCodeCLI,
    ClientBucketClaudeDesktop,
    ClientBucketCodexCLI,
    ClientBucketCodexDesktop,
    ClientBucketOther,
}

func IsValidClientBucket(s string) bool { ... }
```

`DeriveClientBucket(clientKind string, r *http.Request) string` lives in
`internal/proxy/trace_middleware.go` next to `deriveClientKind` and maps
the existing six `ClientKind*` values to the five buckets:

| Source `ClientKind*` | Bucket |
|---|---|
| `ClientKindClaudeCode` | `claude-code-cli` |
| `ClientKindClaudeDesktop` | `claude-desktop` |
| `ClientKindCodex` | `codex-cli` |
| `ClientKindOpenCode`, `ClientKindOpenClaw`, `ClientKindUnknown` | `other` |
| (future: codex-desktop UA detection) | `codex-desktop` |

The `codex-desktop` branch is a stub returning `false` today; the function
documents the placeholder so a future commit only needs to flip a
one-line predicate.

## Routing layer

### Schema

`internal/types/route.go`:

```go
type Route struct {
    // ... existing fields ...
    Clients      []string `json:"clients"`       // empty = match any client bucket
    BillingModes []string `json:"billing_modes"` // empty = match any billing mode
}
```

Migration `056_route_client_billing.sql`:

```sql
-- 056_route_client_billing.sql
ALTER TABLE routes
    ADD COLUMN IF NOT EXISTS clients       TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS billing_modes TEXT[] NOT NULL DEFAULT '{}';
```

All existing routes get empty arrays, which the matcher treats as "match
any value". Zero behavior change at deploy time.

### Matcher

`Router.Match` signature evolves to:

```go
func (r *Router) Match(projectID, model, kind, client, billingMode string) (*resolvedGroup, error)
```

The shared `matchesGlobalRoute` predicate (introduced last PR) grows two
clauses:

```go
func matchesGlobalRoute(route types.Route, projectID, model, kind, client, billingMode string) bool {
    if route.Status != "active" { return false }
    if route.ProjectID != projectID { return false }
    if !slices.Contains(route.ModelNames, model) { return false }
    if !slices.Contains(route.RequestKinds, kind) { return false }
    if len(route.Clients) > 0 && !slices.Contains(route.Clients, client) { return false }
    if len(route.BillingModes) > 0 && !slices.Contains(route.BillingModes, billingMode) { return false }
    return true
}
```

### Tiebreak (specificity-first)

`Match` no longer just walks `r.routes` priority-desc and takes the first
hit. It now:

1. Collects all candidates that pass `matchesGlobalRoute` for the current
   `(projectID, …)` and again for the global fallback (`projectID == ""`).
2. Computes a specificity score for each:

   ```
   spec = 0
   if route.ProjectID != "":         spec += 4
   if len(route.Clients) > 0:        spec += 2
   if len(route.BillingModes) > 0:   spec += 1
   ```

3. Sorts by `(spec desc, MatchPriority desc, ID asc)` and picks the head.
   `ID asc` is the deterministic third key so equal-spec equal-priority
   routes resolve identically across nodes.

`MatrixGlobal` walks the same way (with `projectID == ""`) and emits the
winning cell per `(model, kind, client, billingMode)` 4-tuple. Matrix
endpoint accepts optional `?client=` and `?billing_mode=` filters so the
UI can show a focused slice.

The shared `matchesGlobalRoute` invariant — extracted in the prior PR to
prevent `Match` / `MatrixGlobal` drift — is preserved.

## Billing-mode middleware

`SubscriptionEligibilityMiddleware` is extended in place. Its responsibility
broadens from "is this request allowed to consume subscription?" to "this
request will be billed against `subscription` or `extra_usage`, and the
client bucket is X". The middleware name stays so existing call sites and
tests keep working; its doc comment documents the upgrade.

```go
type BillingMode = string
const (
    BillingModeSubscription = "subscription"
    BillingModeExtraUsage   = "extra_usage"
)

type SubscriptionEligibility struct {
    Eligible bool
    Reason   string
    Mode     BillingMode  // NEW: subscription | extra_usage
}

// New context keys (declared in trace_middleware.go alongside ctxClientKind):
//   ctxClientBucket contextKey = "client_bucket"
//   ctxBillingMode  contextKey = "billing_mode"
```

### Decision

For every request:

```
bucket   = DeriveClientBucket(ClientKindFromContext(ctx), r)
eligible = existing_anthropic_publisher_check(model.Publisher, bucket)

isFirstParty := bucket == ClientBucketClaudeCodeCLI ||
                bucket == ClientBucketClaudeDesktop ||
                bucket == ClientBucketCodexCLI ||
                bucket == ClientBucketCodexDesktop

mode := BillingModeExtraUsage
if eligible && isFirstParty {
    sub := store.GetActiveSubscription(projectID)
    if sub != nil && sub.RemainingCredits > 0 {
        mode = BillingModeSubscription
    }
}

ctx = WithValue(ctx, ctxClientBucket, bucket)
ctx = WithValue(ctx, ctxBillingMode,  mode)
ctx = WithValue(ctx, ctxSubscriptionEligibility,
    SubscriptionEligibility{Eligible: eligible, Reason: reason, Mode: mode})
```

The existing `isAnthropicSubscriptionClient` check folds into the
`eligible` computation; its security note (accepting that client kind is
client-controlled) carries forward unchanged.

### Race with later precise debit

`RateLimitMiddleware` still performs the authoritative credit debit later
in the chain. The billing-mode middleware's quota probe may return "1
credit available" right before the debit drains the last credit, leaving
this request marked `subscription`. Acceptable because the misclassification
direction is `extra_usage` → `subscription` (the user gets one extra
subscription-rate request) and the period reset granularity is days /
months. The opposite misclassification (cached "0 credits" right before a
top-up) self-heals within the cache TTL.

### Fail-open vs fail-closed

If `GetActiveSubscription` errors, the middleware **fails OPEN to
`subscription`** for first-party clients (the existing `SubscriptionEligibility`
fail-open posture for quota / denylist hydration applies here too). This
diverges from the membership check in `AuthMiddleware`, which fails closed
— that one is the authorization gate. Documented inline.

## Pricing layer

### Schema

`internal/types/plan.go`:

```go
type Plan struct {
    // ... existing fields ...
    ClientModelCreditRates map[string]map[string]CreditRate `json:"client_model_credit_rates,omitempty"`
    //                          ^client     ^model
}
```

`plans.model_credit_rates` is its own `JSONB` column (verified at
`internal/store/migrations/001_init.sql:95` and the upsert in
`internal/store/plans.go:21`). A parallel new column
`plans.client_model_credit_rates JSONB` is added by migration 057. The
plan upsert/select statements grow one more column position. `Plan`
serialization stays JSON-as-Go-map; load/save mirrors what
`model_credit_rates` already does.

`Plan.ToPolicy(...)` carries `ClientModelCreditRates` through to the
resolved `*Policy`.

### Resolver

`Policy.ComputeCreditsWithDefault` is renamed to
`ComputeCreditsForClient(model, client, catalogDefault, …)`. Resolution
order:

1. `policy.ClientModelCreditRates[client][model]` (per-client per-model
   override)
2. `policy.ModelCreditRates[model]` (existing plan override)
3. `catalogDefault` (catalog per-model truth)
4. `policy.ModelCreditRates["_default"]` (plan-wide safety net)
5. zero rate (no billing)

`long_context` multipliers (the existing post-resolution adjustment)
apply unchanged after selection.

The original `ComputeCreditsWithDefault` signature stays as a thin
wrapper that passes `client = ""` for the few callers that don't yet
know the client (zero matches at step 1; falls through to step 2 — same
as today). The two `Executor` call sites (`executor.go:1090`, `:1328`)
are migrated to call `ComputeCreditsForClient` with
`ClientBucketFromContext(reqCtx.Context)` so subscription requests get
the client-aware rate.

### Extra-usage path is unchanged

Extra-usage requests do NOT enter the policy resolver. They go through
the catalog default rate (`model.DefaultCreditRate`) just like today. So
the per-client overlay only affects subscription consumption. Spec
requirement met.

The `Executor` finalize block picks the path explicitly via
`ctxBillingMode`:

```go
if mode == BillingModeSubscription {
    credits = policy.ComputeCreditsForClient(model, client, catalogDefault, ...)
} else {
    credits = catalog.RateForModel(model, ...)  // existing extra-usage path
}
```

## Admin API

- `handleCreateRoutingRoute` / `handleUpdateRoutingRoute` accept two new
  body fields, `clients []string` and `billing_modes []string`. Validate
  each member against `types.AllClientBuckets` and
  `{BillingModeSubscription, BillingModeExtraUsage}` respectively. Empty
  arrays are valid (= "match any").
- `GET /api/v1/routing/clients` → `{"data": [...AllClientBuckets...]}`.
  Mirrors `/routing/request-kinds` shape.
- `GET /api/v1/routing/billing-modes` → `{"data": ["subscription", "extra_usage"]}`.
- `GET /api/v1/routing/matrix` response: each cell gains optional
  `clients` and `billing_modes` arrays so the UI can render them.
  Accepts optional query params `?client=<bucket>` and
  `?billing_mode=<mode>` for server-side filtering (saves the dashboard
  one client-side dimension-reduction pass).
- Plans CRUD is unchanged at the API surface; `client_model_credit_rates`
  passes through as a JSON field on the existing `PUT /plans/{slug}`.

## Dashboard

Routes page (`dashboard/src/pages/admin/RoutesPage.tsx` +
`RoutesMatrixView.tsx`):

- **List tab table:** two new columns, **Clients** and **Billing Modes**.
  Each renders a `<Badge>` per value, or a muted "Any" when empty.
- **Create/Edit dialog:** two new multi-select `Select` controls. Both
  default to empty ("Any"). The Clients dropdown lists the 5 buckets
  with the four named ones marked "(first-party)" in a tooltip and
  `other` flagged with a subtitle explaining the catch-all semantics.
- **Matrix tab:** two filter dropdowns at the top (Client, Billing
  Mode). Each defaults to "All". Selecting a value re-fetches
  `useRoutingMatrix({ client, billingMode })` with the filter pushed
  into the URL (`?view=matrix&client=…&billing_mode=…`). Cells now
  display, in addition to the group badge, a small "modes" subscript
  when the route is mode-specific (e.g. `anthropic-pool ·sub`).

No changes to the Plans page (operators edit
`client_model_credit_rates` via the existing API).

## Data flow

```
Request arrives
  AuthMiddleware
    → loads project + api_key (existing)
  TraceMiddleware
    → derives clientKind (existing), writes ctxClientKind (6 values)
  ResolveModelMiddleware
    → writes ctxModel (existing)
  SubscriptionEligibilityMiddleware (EXTENDED)
    → DeriveClientBucket(ctxClientKind, r) → ctxClientBucket (5 values)
    → computeEligibility(model.Publisher, bucket) → eligible bool
    → if eligible && first-party:
         sub = GetActiveSubscription(projectID)
         mode = subscription if (sub != nil && sub.RemainingCredits > 0) else extra_usage
       else:
         mode = extra_usage
    → writes ctxBillingMode + SubscriptionEligibility{Eligible, Reason, Mode}
  RateLimitMiddleware (existing; reads SubscriptionEligibility, unchanged)
  ExtraUsageGuardMiddleware (existing; reads ctxBillingMode for new branch decision)
  Executor.Run
    → router.Match(projectID, model, kind, clientBucket, billingMode)
         resolves to upstream group via specificity-then-priority sort
    → select upstream + dispatch
    → finalize:
       if mode == subscription:
         credits = policy.ComputeCreditsForClient(model, client, catalogDefault, ...)
       else:
         credits = catalog.RateForModel(model, ...)  (existing)
```

## Error handling

| Failure | Behavior |
|---|---|
| `DeriveClientBucket` cannot match any of the four named buckets | Returns `other`. Already the default outcome; not a failure mode. |
| `GetActiveSubscription` DB error in eligibility middleware | Fail OPEN to `subscription` for first-party clients (matches existing fail-open posture for quota / denylist). Logged at WARN. |
| `Router.Match` finds no matching route | Existing `"no route configured for model %s on endpoint %s"` error, no change. (Operator could still find that adding `billing_modes=["subscription"]` excluded their request — surface this in admin logs by including `client` and `billing_mode` in the error message.) |
| Plan JSON contains an unknown client bucket as a top-level key in `client_model_credit_rates` | Silently ignored at resolution time. Admin write paths validate against `AllClientBuckets` so the data should never appear. |
| Migration 056 runs on a row that already has the columns | `IF NOT EXISTS` makes the migration idempotent. |

## Testing

### Backend

- `internal/types/client_bucket_test.go` (new): every `ClientKind*` →
  expected `ClientBucket*` mapping; `ClientKindUnknown` → `other`.
- `internal/types/policy_test.go` (extend): `TestComputeCreditsForClient`
  covers the four-level resolution order, including the "client present,
  but no entry for this model → falls through to ModelCreditRates"
  branch and the "client not in map → falls through" branch.
- `internal/proxy/subscription_eligibility_middleware_test.go` (extend):
  full matrix of `(bucket, eligible, sub_state) → mode`.
- `internal/proxy/router_engine_test.go` (extend):
  - `TestRouter_Match_ClientSpecificity` — a route with `clients=[X]`
    beats a route with `clients=[]` for a request from client X.
  - `TestRouter_Match_BillingModeSpecificity` — analogous for billing
    modes.
  - `TestRouter_Match_FullPrecedence` — `(project + client + mode)` beats
    `(project + client)` beats `(project alone)` beats `(client + mode
    global)` beats `(global plain)`; within the same spec bucket,
    `match_priority` decides.
  - `TestRouter_Match_LegacyEmptyMatchesAny` — pre-migration routes
    (empty `clients` + empty `billing_modes`) still match every request.
  - `TestRouter_Match_DeterministicTiebreak` — equal-spec equal-priority
    routes resolve by `ID asc` (stable across nodes).
  - `TestRouter_MatrixGlobal_EmitsNewDimensions` — matrix cells carry
    `clients` and `billing_modes` when set.
- `internal/admin/handle_routing_routes_test.go` (extend, if test
  scaffolding allows; otherwise validation is covered at integration
  level by `TestHandleRoutingMatrix_HappyPath`): admin write rejects
  invalid client / billing-mode values with 400.
- `internal/store/migrations_056_test.go` (new): `routes.clients` and
  `routes.billing_modes` added with empty defaults; INSERT with
  populated arrays round-trips correctly.
- `internal/store/migrations_057_test.go` (new): `plans.client_model_credit_rates`
  exists as JSONB; NULL on pre-existing rows; UPDATE to a populated
  JSON object round-trips through `Plan.ToPolicy`.

### Frontend

No test framework in this repo. Verification per task: `pnpm exec tsc -b
&& pnpm build` clean, plus the manual smoke list in the plan's frontend
task.

## Migration / deploy order

1. **Backend deploy.** Migration 056 adds the two columns on `routes`
   (`clients`, `billing_modes`, both `TEXT[] NOT NULL DEFAULT '{}'`).
   Migration 057 adds `plans.client_model_credit_rates JSONB`. Existing
   routes carry empty arrays; existing plans carry NULL (resolver
   handles nil-map cleanly). `BillingModeMiddleware` starts deriving
   mode for every request; `Executor` reads `ctxBillingMode` to pick the
   pricing path. `Router.Match` accepts the new arguments. Old
   dashboards continue to function (they ignore the new fields on
   responses and don't send them on writes).
2. **Dashboard deploy.** Routes page exposes the two columns + dialog
   selectors + matrix filters. Operators can now create routes scoped
   by client and / or billing_mode.
3. **Operator action (when desired).** Replace the catch-all routes with
   `[subscription]` routes pointing at OAuth-backed first-party
   upstreams and `[extra_usage]` routes pointing at paid-API upstreams.
   Optional per-client refinement on top.
4. **Plans-level pricing changes.** Operators add
   `client_model_credit_rates` entries to plans via the existing
   `PUT /plans/{slug}` admin endpoint. No UI change required for v1.

No rollback hazard. Migration 056 only adds columns with safe defaults.
A rollback to the prior backend would leave the columns present but
unused — harmless. The new pricing code paths only fire when a plan
actually populates `client_model_credit_rates`; absent that, behavior is
identical to today.
