# Client-Aware Routing + Pricing — Design

> **Revision history.** First draft proposed a `BillingModeMiddleware` that
> resolved `billing_mode` up-front from subscription balance. Audit caught
> that this collides with the existing rate-limit/extra-usage decision
> chain (which is the authoritative balance gate today) and would either
> double-count balance or enable free rides. This revision aligns
> `billing_mode` with the already-existing `IsExtraUsage` decision and
> only exposes it as a routing axis — no new balance check is introduced.

## Problem

Current routing keys requests by `(project, model, request_kind)`; pricing
keys credits by `(model, usage)`. Neither axis knows which **client** sent
the request, and routing doesn't know whether the request will be billed
against the project's **subscription** or its **extra-usage** balance. As
a result:

- All clients hitting the same model land on the same upstream group, so
  we can't dedicate OAuth-backed Claude Code / Codex upstreams (which
  consume the operator's first-party subscriptions) to the subset of
  traffic that actually originates from those clients.
- Subscription-bound requests and extra-usage requests share upstream
  groups, so the operator can't keep them on separate pools (e.g.
  subscription on OAuth Claude Code, extra-usage on paid API keys).
- All clients are billed at the same rate, so the operator can't reflect
  reality where a Claude Code request against the Pro plan should consume
  subscription credits at the plan's rate, while an arbitrary OpenAI SDK
  request against the same model should consume extra-usage credits at
  the catalog's official-API rate.

## What this spec does and does NOT change about the billing decision

`billing_mode` in this spec is a label that **already exists** in the
runtime, just not by that name. Today the flow is:

```
RateLimitMiddleware
  subscription PreCheck
  → credit budget ok  → request continues as subscription
  → credit exhausted  → withExtraUsageIntent(reason=rate_limited)
  → ineligible client → classic-only PreCheck → withExtraUsageIntent(reason=client_restriction)

ExtraUsageGuardMiddleware
  → intent present + extra-usage enabled + balance > 0 → approve, stamp IsExtraUsage=true on RequestContext
  → intent present + any precondition fails           → 429

Executor.Execute
  → reads reqCtx.IsExtraUsage  (stamped above)
  → on settle, branches by IsExtraUsage:
       false → policy.ComputeCreditsWithDefault(model, catalog default, …)  [subscription rate]
       true  → settleExtraUsage → computeExtraUsageCostCredits(model, usage) [catalog default rate]
```

`IsExtraUsage` becomes the canonical `billing_mode`:

```go
mode := BillingModeSubscription
if reqCtx.IsExtraUsage { mode = BillingModeExtraUsage }
```

**This spec does NOT introduce a new balance check.** It does NOT change
when or how a request descends from subscription to extra-usage. It does
NOT enable any "automatic fall-through" behavior beyond what the existing
rate-limit + guard chain already produces. The fall-through wording in the
brainstorming dialogue described the existing behavior, not a new layer.

What this spec DOES change:

1. **Routes can additionally be scoped by `clients` and `billing_modes`.**
2. **Pricing for subscription consumption can be overridden per `(client, model)`** on a plan.
3. **The 5-bucket `ClientBucket` identity is introduced**, derived from
   the existing 6-value `ClientKind` enum.

## Goal

- **Routing key:** `(project_id, model, request_kind, client_bucket, billing_mode)`.
- **Subscription pricing:** plan rate, with optional per-client override on top.
- **Extra-usage pricing:** unchanged — catalog `DefaultCreditRate` (the official-API rate). Per-client overrides do NOT apply here; the catalog default is the official rate of record.

## Non-goals

- **No new balance check** at routing time. Whether a request is
  subscription or extra-usage is still decided by the existing
  RateLimit + ExtraUsageGuard chain, exactly as today.
- **No automatic balance-exhaustion fall-through that today's chain
  doesn't already produce.** If a project hasn't enabled extra-usage,
  a balance-exhausted Claude Code request still gets 429, as today.
- **No per-route pricing.** Pricing follows plan + catalog.
- **No per-client overlay on the catalog default.** Extra-usage stays
  single-rate. (Operator-requestable in a future spec.)
- **No Plans dashboard editor for `client_model_credit_rates`.** Edit
  via the existing `PUT /plans/{slug}` admin API.
- **No change to owner-removal, OAuth grants, or `requireSession` /
  client-restriction policy.** Spec scope is strictly routing + pricing.

## Scope decisions (from brainstorming + audit)

| Decision | Choice |
|---|---|
| Packaging | One spec, one PR, two layers (routing + pricing). |
| Client buckets | `claude-code-cli`, `claude-desktop`, `codex-cli`, `codex-desktop` (reserved), `other`. |
| Routing column shape | Multi-valued `clients []string` + `billing_modes []string`; empty = match any. Mirrors `RequestKinds`. |
| Tiebreak | Weighted specificity: project 100, clients 10, billing_modes 1; within the same total, `match_priority` desc; final tiebreak `ID asc`. |
| `billing_mode` source | Derived in the Executor from the already-existing `reqCtx.IsExtraUsage` flag. NO new middleware. NO new balance check. |
| Pricing schema | Plans gain `client_model_credit_rates map[client]map[model]CreditRate`. |
| Pricing scope | Plans (subscription consumption) only. Extra-usage stays catalog default. |
| Codex Desktop detection | Bucket reserved; deriver returns `other` until Codex ships a desktop client. |
| Old routes | Migration only adds columns with empty defaults; existing routes match any client / any mode. |
| Admin UI | Routes page only (columns + dialog + matrix filters). No Plans editor. |

## Client buckets

New `internal/types/client_bucket.go`:

```go
const (
    ClientBucketClaudeCodeCLI = "claude-code-cli"
    ClientBucketClaudeDesktop = "claude-desktop"
    ClientBucketCodexCLI      = "codex-cli"
    ClientBucketCodexDesktop  = "codex-desktop" // reserved; mapper returns "other" today
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

// MapClientKindToBucket is a pure function: takes the existing 6-value
// ClientKind* output of deriveClientKind and projects it onto the
// 5-bucket axis. Lives next to deriveClientKind in trace_middleware.go.
func MapClientKindToBucket(kind string) string {
    switch kind {
    case ClientKindClaudeCode:    return ClientBucketClaudeCodeCLI
    case ClientKindClaudeDesktop: return ClientBucketClaudeDesktop
    case ClientKindCodex:         return ClientBucketCodexCLI
    default:                      return ClientBucketOther
    }
}
```

The codex-desktop branch is intentionally absent today; the public mapping
function documents that codex-desktop will be added when its identification
rule lands. Today every codex-desktop traffic falls into `other`.

`ClientKindFromContext` already runs in the trace middleware. A new context
key `ctxClientBucket` is populated alongside it (in the same middleware,
one line below `ctx = context.WithValue(ctx, ctxClientKind, kind)`), so
the bucket is available to every later middleware and the executor without
re-deriving:

```go
bucket := MapClientKindToBucket(kind)
ctx = context.WithValue(ctx, ctxClientBucket, bucket)
```

A `ClientBucketFromContext(ctx) string` getter is added.

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
ALTER TABLE routes
    ADD COLUMN IF NOT EXISTS clients       TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS billing_modes TEXT[] NOT NULL DEFAULT '{}';
```

All existing routes get empty arrays — the matcher treats those as "match
any value". Zero behavior change at deploy time.

### Matcher

`Router.Match` signature evolves to:

```go
func (r *Router) Match(projectID, model, kind, client, billingMode string) (*resolvedGroup, error)
```

The 5 existing callers (1 in `executor.go`, 4 in tests) are all updated
in the same commit that lands this change — no compatibility wrapper.

The shared `matchesGlobalRoute` predicate grows two clauses:

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

### Tiebreak (weighted specificity)

`Match` collects all candidates that pass `matchesGlobalRoute`, scores
them, and picks the head of a sort:

```
spec = 0
if route.ProjectID != ""     : spec += 100
if len(route.Clients) > 0    : spec += 10
if len(route.BillingModes) > 0 : spec += 1
```

Weights (100, 10, 1) form a strict lexicographic order: project trumps
everything; among same-project candidates, client-specificity beats
mode-specificity; mode-specificity is the third tier. Within the same
`spec` bucket, `MatchPriority desc` decides; the final tiebreak is
`ID asc` so identical-spec identical-priority routes resolve identically
across nodes.

`MatrixGlobal` walks the same way (with `projectID == ""`) and emits the
winning cell per `(model, kind, client, billing_mode)` 4-tuple. The matrix
admin endpoint accepts `?client=` and `?billing_mode=` query params to
let the dashboard filter server-side.

## `billing_mode` derivation (no new middleware)

`billing_mode` is **derived in the Executor**, one line before
`router.Match`:

```go
billingMode := types.BillingModeSubscription
if reqCtx.IsExtraUsage {
    billingMode = types.BillingModeExtraUsage
}
client := ClientBucketFromContext(r.Context())
group, err := e.router.Match(reqCtx.ProjectID, reqCtx.Model,
                              reqCtx.RequestKind, client, billingMode)
```

`reqCtx.IsExtraUsage` is already stamped earlier in `Execute()`
(currently at executor.go:240) from the `ExtraUsageContextFromContext`
helper, which was set by `ExtraUsageGuardMiddleware` on approval. So by
the time we reach `Match`, the question "is this subscription or
extra-usage?" is already answered authoritatively by the existing chain.

**There is no new balance check, no second eligibility decision, and no
race with the existing rate-limit / guard middleware.** The new routing
axis just exposes a label the runtime already computes.

`types.BillingMode` is a new string-alias type for documentation; the
constants live in `internal/types/billing_mode.go`:

```go
type BillingMode = string
const (
    BillingModeSubscription BillingMode = "subscription"
    BillingModeExtraUsage   BillingMode = "extra_usage"
)
var AllBillingModes = []string{BillingModeSubscription, BillingModeExtraUsage}
func IsValidBillingMode(s string) bool { ... }
```

`SubscriptionEligibilityMiddleware` and its `SubscriptionEligibility`
output are **unchanged** by this spec. The "client restriction" semantics
it produces (anthropic publisher + non-first-party client → not eligible
for subscription) keep working exactly as today and continue feeding
`RateLimitMiddleware`'s classic-only branch.

## Pricing layer — subscription path

### Schema

`internal/types/plan.go`:

```go
type Plan struct {
    // ... existing fields ...
    ClientModelCreditRates map[string]map[string]CreditRate `json:"client_model_credit_rates,omitempty"`
    //                          ^client     ^model
}
```

Stored on a new `plans.client_model_credit_rates JSONB` column
(verified: `model_credit_rates` is already its own `JSONB` column at
`internal/store/migrations/001_init.sql:95` and `:184`, and is
load/saved in `internal/store/plans.go`; the new field follows the same
pattern). Migration:

`057_plan_client_credit_rates.sql`:

```sql
ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS client_model_credit_rates JSONB;
```

Default NULL on existing rows. The store's plan upsert/select grows one
column position; the marshal/unmarshal mirrors `model_credit_rates`
exactly. `Plan.ToPolicy(...)` carries `ClientModelCreditRates` through to
the resolved `*RateLimitPolicy`.

### Resolver

`Policy.ComputeCreditsForClient(model, client string, catalogDefault
*CreditRate, …)` is added with resolution order:

1. `policy.ClientModelCreditRates[client][model]` — per-client per-model override
2. `policy.ModelCreditRates[model]` — existing plan override
3. `catalogDefault` — catalog per-model truth
4. `policy.ModelCreditRates["_default"]` — plan-wide safety net
5. zero rate

`ApplyLongContextCreditRate` (existing post-selection adjustment) runs
unchanged after the rate is picked.

The existing `Policy.ComputeCreditsWithDefault` is **kept as a thin
wrapper** that calls `ComputeCreditsForClient(model, "", catalogDefault, …)`.
With `client = ""`, step 1 always misses (the empty client never appears
as a map key in admin write paths) and we fall through to step 2 — the
behavior today. This preserves backward compatibility for any future
caller that doesn't yet thread client through.

The two `Executor` subscription pricing call sites
(`executor.go:1090` and `:1328`) switch to `ComputeCreditsForClient` and
pass the client bucket from context. **No other call site changes.**

### Invariant guarantees (no regression in today's billing)

- A plan that does NOT define `client_model_credit_rates`:
  `ComputeCreditsForClient` resolves at step 2 (`ModelCreditRates[model]`)
  → identical to today.
- A plan that defines `client_model_credit_rates` for some clients but
  not all: the un-named clients still resolve at step 2 → no regression
  for traffic from those clients.
- Subscription requests today never enter the catalog default rate path
  unless step 2 also misses → unchanged.

### Extra-usage path — explicitly unchanged

Extra-usage billing goes through `computeExtraUsageCostCredits(rc.ModelRef,
usage)` (`executor.go:1689`), which reads `rc.ModelRef.DefaultCreditRate`
(the catalog default — i.e. the official-API rate). This spec **does not
modify that function or its callers**. Per-client overrides do NOT apply
to extra-usage billing. This is by design: extra-usage represents the
operator-paid passthrough billing, where the catalog default IS the
external API rate and is the natural unit of attribution.

If you want a discount for `claude-code-cli` on extra-usage in the
future, that's a separate spec (it requires either per-client catalog
overlay or per-client extra-usage settings).

## Admin API

- `POST /api/v1/routing/routes` and `PUT /api/v1/routing/routes/{id}`:
  accept `clients []string` and `billing_modes []string`. Each member
  validated against `types.AllClientBuckets` and `types.AllBillingModes`
  respectively. Empty arrays are valid. Reject unknown values with 400.
- `GET /api/v1/routing/clients`: returns `{"data": [...AllClientBuckets...]}`.
  Mirrors `/routing/request-kinds`.
- `GET /api/v1/routing/billing-modes`: returns
  `{"data": ["subscription", "extra_usage"]}`.
- `GET /api/v1/routing/matrix`: response cells gain optional `clients` and
  `billing_modes` arrays carried from the winning route. Accepts
  `?client=<bucket>` and `?billing_mode=<mode>` query params; server-side
  filter is applied before sparseness computation so empty cells reflect
  the filtered view.
- `PUT /api/v1/plans/{slug}` accepts `client_model_credit_rates` in the
  body via the existing free-form plan JSON path. Server validates that
  each top-level key is a known `ClientBucket*` value and rejects unknown
  ones with 400 (today every other rate field is similarly validated by
  the catalog).

## Dashboard (Routes page only)

`dashboard/src/pages/admin/RoutesPage.tsx` and
`RoutesMatrixView.tsx`:

- **List tab columns**: new **Clients** and **Billing Modes** columns.
  Each renders one `<Badge>` per array element, or a muted "Any" when
  empty.
- **Create/Edit dialog**: two new multi-select controls between the
  existing Request Kinds and Upstream Group fields. The Clients dropdown
  lists the 5 buckets (the four named ones get a tooltip noting
  first-party identification; `other` notes "catch-all"). Both controls
  default to empty (= match any).
- **Matrix tab filters**: two dropdowns at the top of the matrix
  (Client, Billing Mode). Default "All". Selection re-fetches with
  query params and writes them into the URL
  (`?view=matrix&client=…&billing_mode=…`) so the view survives reload.
  Filled cells additionally show a small subscript when the source route
  is mode-specific (`anthropic-pool · sub`).

The Plans page is **not** modified.

## Data flow (end-to-end after this PR)

```
Request arrives
  AuthMiddleware                             (unchanged)
  TraceMiddleware                            (extended: also writes ctxClientBucket)
  ResolveModelMiddleware                     (unchanged)
  SubscriptionEligibilityMiddleware          (unchanged)
  RateLimitMiddleware                        (unchanged)
  ExtraUsageGuardMiddleware                  (unchanged)
  Executor.Execute
    reqCtx.IsExtraUsage already stamped from the chain above.
    derive: client       = ClientBucketFromContext(ctx)
            billingMode  = subscription if !IsExtraUsage else extra_usage
    router.Match(projectID, model, kind, client, billingMode)
    select upstream + dispatch
    on settle:
       if !IsExtraUsage:
         credits = policy.ComputeCreditsForClient(model, client, catalogDefault, …)
       else:
         credits = computeExtraUsageCostCredits(model, usage)   // unchanged
```

The only NEW behavior at the runtime decision points:

1. Trace middleware additionally writes `ctxClientBucket`.
2. Executor's Match call additionally passes `client` and `billing_mode`.
3. Executor's subscription settle additionally passes `client` to the
   resolver. If the plan has no `client_model_credit_rates`, the resolver
   produces identical numbers to today.

## Error handling

| Failure | Behavior |
|---|---|
| Trace middleware can't classify → `ClientKindUnknown` | `MapClientKindToBucket` returns `other`. Routing falls through to client-agnostic routes; subscription pricing resolves `ClientModelCreditRates["other"][model]` first, then plan default. |
| `Router.Match` finds no matching route | Existing `"no route configured for model %s on endpoint %s"` error. Message includes the client + billing_mode in the new error text to help operators diagnose over-narrow rules. |
| Plan JSON has an unknown client bucket key in `client_model_credit_rates` | Silently ignored at resolution time. Admin write path validates against `AllClientBuckets` so the data should never appear. |
| Migration 056 or 057 runs twice | `IF NOT EXISTS` makes both idempotent. |
| Old dashboard sends route writes without the two new fields | Treated as empty arrays (= match any). Same as the migration default. |

## Testing

### Backend

- `internal/types/client_bucket_test.go` (new): each `ClientKind*` →
  expected `ClientBucket*` mapping; unknown / opencode / openclaw → `other`.
- `internal/types/billing_mode_test.go` (new): `IsValidBillingMode`
  accepts the two values and rejects unknown.
- `internal/types/policy_test.go` (extend): `TestComputeCreditsForClient`
  exhaustively covers the 4-level resolution order, plus
  `TestComputeCreditsWithDefault_BackwardCompat` asserting the existing
  wrapper produces identical numbers to today on plans without
  `ClientModelCreditRates`.
- `internal/proxy/router_engine_test.go` (extend):
  - `TestRouter_Match_ClientSpecificity` — a route with `clients=[X]`
    beats a route with `clients=[]` for an X-client request.
  - `TestRouter_Match_BillingModeSpecificity` — analogous for modes.
  - `TestRouter_Match_FullPrecedence` — `(project + clients + modes)`
    beats `(project + clients)` beats `(project alone)` beats
    `(clients + modes global)` beats `(plain global)`; within the same
    spec bucket, `match_priority` decides.
  - `TestRouter_Match_LegacyEmptyMatchesAny` — pre-migration routes
    (both arrays empty) match every request.
  - `TestRouter_Match_DeterministicTiebreak` — same spec & priority →
    `ID asc` wins (stable across nodes).
  - `TestRouter_MatrixGlobal_EmitsNewDimensions` — matrix cells carry
    `clients` and `billing_modes` when set on the source route.
  - `TestRouter_MatrixGlobal_FilterByClient` /
    `TestRouter_MatrixGlobal_FilterByBillingMode` — query-param filters
    return a focused slice.
- `internal/proxy/trace_middleware_test.go` (extend): every
  `deriveClientKind` outcome also produces the expected `ctxClientBucket`.
- `internal/admin/handle_routing_routes_test.go` (extend if scaffolding
  allows): write paths reject unknown client / billing-mode values; the
  GET-by-id round-trips the new fields.
- `internal/admin/handle_routing_matrix_test.go` (extend): matrix
  response cells carry the new fields; query-param filters work.
- `internal/store/migrations_056_test.go` (new): columns added with
  empty defaults; INSERT with populated arrays round-trips.
- `internal/store/migrations_057_test.go` (new): column added as JSONB;
  NULL on pre-existing rows; UPDATE to populated JSON round-trips
  through `Plan.ToPolicy`.

### Invariant tests (no-regression)

A separate sub-suite in `internal/proxy/executor_finalize_test.go`
asserts:
- A subscription request from any client against a plan WITHOUT
  `client_model_credit_rates` produces the same credit count as a
  baseline `Policy.ComputeCreditsWithDefault` call. Catches accidental
  changes to step 2+ in the resolver.
- An extra-usage request produces the same credit count as today.
  Catches accidental changes to the catalog-default path.

### Frontend

No test framework in this repo. Verification per task:
`pnpm exec tsc -b && pnpm build` clean + manual smoke listed in the
plan.

## Migration / deploy order

1. **Backend deploy.** Migration 056 adds `routes.clients` and
   `routes.billing_modes` (both `TEXT[] NOT NULL DEFAULT '{}'`).
   Migration 057 adds `plans.client_model_credit_rates JSONB`
   (NULL-default). Both idempotent on re-run.
   Existing routes carry empty arrays → match any client / mode →
   identical to today. Existing plans carry NULL → resolver step 1
   misses → identical to today.
2. **Dashboard deploy.** Routes page shows the two new columns + dialog
   selectors + matrix filters. Operators can begin creating
   client-scoped and mode-scoped routes.
3. **Operator action (when desired).** Replace catch-all routes with
   `[subscription]`-scoped routes targeting OAuth Claude Code / Codex
   upstreams and `[extra_usage]`-scoped routes targeting paid-API
   upstreams. Optional further refinement by client bucket.
4. **Plans-level pricing changes.** Operators add
   `client_model_credit_rates` entries to plans via the existing
   `PUT /api/v1/plans/{slug}` admin endpoint. No UI change in v1.

Rollback safety:
- Migration 056 / 057 only ADD columns with safe defaults; rolling back
  the backend leaves columns present but unused — harmless.
- Subscription pricing without `ClientModelCreditRates` produces
  identical numbers to today (invariant test guards this).
- Extra-usage pricing path is untouched.
