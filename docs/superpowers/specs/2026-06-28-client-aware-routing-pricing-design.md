# Client-Aware Routing + Pricing — Design

> **Revision history.**
> - v1 (first draft): introduced a `BillingModeMiddleware` with its own balance check. Audit caught the collision with the existing RateLimit + ExtraUsageGuard chain.
> - v2: removed the new middleware; `billing_mode` derived from `reqCtx.IsExtraUsage` in the Executor. Routing key was `(project, model, kind, client, billing_mode)`.
> - **v3 (this version):** drops `billing_mode` from the routing key. Routes are scoped by client only; the reason for client-based routing is upstream-format compatibility (Claude Code / Codex upstreams only accept their respective request shapes), not billing differentiation. The billing-vs-subscription distinction is handled entirely by the existing runtime chain (`SubscriptionEligibilityMiddleware` → `RateLimitMiddleware` → `ExtraUsageGuardMiddleware`) and exposed only at pricing time, not at routing time. Per-client pricing overrides on plans stay (subscription path only); extra-usage path stays at catalog default rate.

## Problem

Current routing keys requests by `(project, model, request_kind)`; pricing
keys credits by `(model, usage)`. Neither axis knows which **client** sent
the request. As a result:

- All clients hitting the same model land on the same upstream group, so
  we can't dedicate OAuth-backed Claude Code or Codex upstreams (which
  only accept their respective wire formats — Claude Code's
  `metadata.user_id` shape, Codex's session-id header, etc.) to the
  subset of traffic that originates from those clients. A non-first-party
  client sent to a Claude Code OAuth upstream would fail upstream-format
  validation; a Claude Code request sent to a generic OpenAI-compatible
  upstream would not be billable under the operator's Anthropic
  subscription.
- All clients are billed at the same plan rate, so the operator can't
  reflect reality where a Claude Code request against the Pro plan
  should consume subscription credits at the plan's discount rate, while
  an arbitrary OpenAI-SDK request against the same model should consume
  extra-usage credits at the catalog's official-API rate.

## What this spec changes and what it explicitly does NOT change

### Changes

1. **Routing key** becomes `(project_id, model, request_kind, client)`.
   Routes can optionally be scoped by `clients []string` (empty = match
   any client bucket). Matching uses weighted specificity (project beats
   client beats neither) then `match_priority` then route ID.
2. **Five-bucket `ClientBucket` identity** (`claude-code-cli`,
   `claude-desktop`, `codex-cli`, `codex-desktop` reserved, `other`)
   derived from the existing 6-value `ClientKind` enum.
3. **Per-client subscription pricing overlay** on plans:
   `client_model_credit_rates map[client]map[model]CreditRate`. Affects
   the subscription billing path only.

### Does NOT change

- **No new balance check at routing time.** The decision "is this
  request subscription or extra-usage?" stays inside the existing
  `RateLimitMiddleware` → `ExtraUsageGuardMiddleware` chain, exactly as
  today.
- **No new middleware.** Trace middleware gains one line (writes
  `ctxClientBucket`); `SubscriptionEligibilityMiddleware` output shape
  is unchanged.
- **No new routing dimension beyond client.** `billing_mode` does NOT
  appear in the routing table, the matcher, the admin API, or the
  dashboard. The runtime distinction between subscription and
  extra-usage is invisible to the router; it surfaces only at pricing
  time inside the Executor.
- **No change to extra-usage pricing.** Extra-usage billing goes
  through `computeExtraUsageCostCredits` reading
  `model.DefaultCreditRate` (the catalog default = official API rate).
  Per-client overrides have NO effect on extra-usage. By design, since
  extra-usage represents operator-paid passthrough at the upstream's
  posted rate.
- **No change to `SubscriptionEligibilityMiddleware`'s policy.** Today
  it gates anthropic-publisher requests to claude-code/claude-desktop
  clients; non-anthropic models are unconditionally eligible. That
  behavior is unchanged by this spec. If a future requirement emerges
  to make every non-first-party client universally ineligible
  regardless of model publisher, it's a separate spec.
- **No automatic fall-through that today's chain doesn't already
  produce.** If a project hasn't enabled extra-usage, a
  balance-exhausted Claude Code request still 429s, as today.

## Why route by client (not by billing_mode)

Claude Code and Codex upstreams are wire-format gated:

- A Claude Code OAuth upstream rejects a request that lacks
  `metadata.user_id` of the SDK-attested shape.
- A Codex OAuth upstream rejects a request without a Codex session-id
  header.
- A paid Anthropic API upstream accepts both Claude Code-shaped and
  generic Anthropic-shaped requests.
- A paid OpenAI API upstream accepts Codex-shaped and generic
  Chat/Responses requests.

So **routing decisions are governed by the request's wire format**,
which is correlated with the client bucket. The operator places
appropriate upstreams in each group:

- `claude-code-pool` group: Claude Code OAuth upstream(s), optionally
  with a paid Anthropic API fallback member to handle OAuth quota
  exhaustion via the existing LB retry mechanism.
- `codex-pool` group: Codex OAuth upstream(s), optionally with a paid
  OpenAI API fallback.
- `default-pool` group: paid API upstreams only.

A route `clients: ["claude-code-cli"]` directs Claude Code requests to
`claude-code-pool`. The runtime decision "did this request consume
subscription or extra-usage credits?" is independent of which upstream
in the group handled it — it's a separate accounting question handled
by the existing rate-limit + extra-usage-guard chain.

## Scope decisions (from brainstorming + audit + v3 revision)

| Decision | Choice |
|---|---|
| Packaging | One spec, one PR. Routing + pricing together. |
| Client buckets | `claude-code-cli`, `claude-desktop`, `codex-cli`, `codex-desktop` (reserved), `other`. |
| Routing column shape | Single multi-valued `clients []string`; empty = match any. Mirrors `RequestKinds`. |
| `billing_mode` as a routing dimension | **No.** Per user feedback (v3). Routing is by client only. The runtime subscription/extra-usage decision is invisible to the router. |
| Tiebreak | Weighted specificity: project=10, clients=1; then `match_priority` desc; then `ID asc`. |
| Pricing schema | Plans gain `client_model_credit_rates map[client]map[model]CreditRate`. |
| Pricing scope | Plans (subscription consumption) only. Extra-usage stays catalog default. |
| `SubscriptionEligibilityMiddleware` | Unchanged. Today's (publisher × client) policy stays. |
| Codex Desktop detection | Bucket reserved; deriver returns `other` until Codex ships a desktop client. |
| Old routes | Migration only adds `clients` column with empty default; existing routes match any client. |
| Admin UI | Routes page: one new column (`Clients`), one new toggle-button selector in the dialog, one new filter dropdown on the Matrix tab. No `billing_mode` UI. |

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

The codex-desktop branch is intentionally absent today; the public
mapping function documents that codex-desktop will be added when its
identification rule lands.

`ClientKindFromContext` already runs in the trace middleware. A new
context key `ctxClientBucket` is populated alongside it (one line below
the existing `ctxClientKind` write):

```go
kind, sdkSource := deriveClientKind(r, traceCfg)
ctx = context.WithValue(ctx, ctxClientKind, kind)
ctx = context.WithValue(ctx, ctxClientBucket, types.MapClientKindToBucket(kind))
```

A `ClientBucketFromContext(ctx) string` getter is added (returns
`ClientBucketOther` on miss).

## Routing layer

### Schema

`internal/types/route.go`:

```go
type Route struct {
    // ... existing fields ...
    Clients []string `json:"clients"` // empty = match any client bucket
}
```

Migration `056_route_clients.sql`:

```sql
ALTER TABLE routes
    ADD COLUMN IF NOT EXISTS clients TEXT[] NOT NULL DEFAULT '{}';
```

All existing routes get an empty array — the matcher treats it as "match
any value". Zero behavior change at deploy time.

### Matcher

`Router.Match` signature evolves to:

```go
func (r *Router) Match(projectID, model, kind, client string) (*resolvedGroup, error)
```

The 5 existing callers (1 in `executor.go`, 4 in tests) are all updated
in the same commit that lands this change — no compatibility wrapper.

The shared `matchesGlobalRoute` predicate (extracted in a prior PR to
prevent `Match`/`MatrixGlobal` drift) grows one clause:

```go
func matchesGlobalRoute(route types.Route, projectID, model, kind, client string) bool {
    if route.Status != "active" { return false }
    if route.ProjectID != projectID { return false }
    if !slices.Contains(route.ModelNames, model) { return false }
    if !slices.Contains(route.RequestKinds, kind) { return false }
    if len(route.Clients) > 0 && !slices.Contains(route.Clients, client) { return false }
    return true
}
```

### Tiebreak (weighted specificity)

`Match` collects all candidates that pass `matchesGlobalRoute`, scores
them, and picks the head of a sort:

```
spec = 0
if route.ProjectID != ""    : spec += 10
if len(route.Clients) > 0   : spec += 1
```

Weights (10, 1) form a strict lexicographic order: project trumps
client; client is the only secondary lever. Within the same `spec`
bucket, `MatchPriority desc` decides; the final tiebreak is `ID asc` so
identical-spec identical-priority routes resolve identically across
nodes.

`MatrixGlobal` walks the same way (with `projectID == ""`) and emits the
winning cell per `(model, kind, client)` 3-tuple. The matrix admin
endpoint accepts an optional `?client=` query param to let the dashboard
filter server-side.

## `billing_mode` derivation (executor-internal only)

The Executor computes a string label for documentation / future
telemetry, but it does NOT feed routing:

```go
billingMode := "subscription"
if reqCtx.IsExtraUsage {
    billingMode = "extra_usage"
}
```

This local variable is used to pick the pricing path (see below). The
label never leaves the executor scope; it does not appear in `Route`,
in the admin API, or in the dashboard.

The `BillingMode` Go type is **not** added in this spec — there is no
public API that needs it. If a future spec exposes the label (e.g. to
include in request logs), it can be promoted to `types.BillingMode`
then.

`reqCtx.IsExtraUsage` is already stamped on the request context earlier
in `Execute()` from the existing `ExtraUsageContextFromContext` helper,
which was set by `ExtraUsageGuardMiddleware` on approval. The flag is
authoritative — `RateLimitMiddleware + ExtraUsageGuardMiddleware`
already decided whether this request consumes subscription or
extra-usage credits before we reach the executor's settle path.

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
`internal/store/migrations/001_init.sql:95` and `:184`, load/saved in
`internal/store/plans.go`; the new field follows the same pattern).

Migration `057_plan_client_credit_rates.sql`:

```sql
ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS client_model_credit_rates JSONB;
```

Default NULL on existing rows. The store's plan upsert/select grows one
column position; marshal/unmarshal mirrors `model_credit_rates` exactly.
`Plan.ToPolicy(...)` carries `ClientModelCreditRates` through to the
resolved `*RateLimitPolicy`.

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
pass the client bucket from `reqCtx.ClientBucket` (a new field on
`RequestContext` populated in `Execute()` from
`ClientBucketFromContext(r.Context())`).

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

Extra-usage billing goes through
`computeExtraUsageCostCredits(rc.ModelRef, usage)` (executor.go:1689),
which reads `rc.ModelRef.DefaultCreditRate` (the catalog default — i.e.
the official-API rate). This spec **does not modify that function or
its callers**. Per-client overrides do NOT apply to extra-usage
billing. This is by design: extra-usage represents operator-paid
passthrough billing, where the catalog default IS the external API rate
and is the natural unit of attribution.

If a future requirement emerges for a per-client extra-usage discount,
that's a separate spec (it requires either a per-client catalog overlay
or per-client extra-usage settings).

## Admin API

- `POST /api/v1/routing/routes` and `PUT /api/v1/routing/routes/{id}`:
  accept `clients []string`. Each member validated against
  `types.AllClientBuckets`. Empty array is valid (= match any). Reject
  unknown values with 400.
- `GET /api/v1/routing/clients`: returns
  `{"data": [...AllClientBuckets...]}`. Mirrors
  `/routing/request-kinds`.
- `GET /api/v1/routing/matrix`: response cells gain optional `clients`
  array carried from the winning route, plus a `client` field naming
  the bucket this cell was resolved for. Accepts `?client=<bucket>`
  query param; server-side filter is applied before sparseness
  computation so empty cells reflect the filtered view.
- `PUT /api/v1/plans/{slug}` accepts `client_model_credit_rates` in the
  body via the existing free-form plan JSON path. Server validates that
  each top-level key is a known `ClientBucket*` value and rejects
  unknown ones with 400.

## Dashboard (Routes page only)

`dashboard/src/pages/admin/RoutesPage.tsx` and
`RoutesMatrixView.tsx`:

- **List tab columns**: one new **Clients** column between Endpoints
  and Upstream Group. Renders one `<Badge>` per array element, or a
  muted "Any" when empty.
- **Create/Edit dialog**: one new multi-select toggle-button row,
  immediately after the existing Request Kinds row, fed by the new
  `useClientBuckets` hook. Defaults to empty (= match any). Each
  bucket gets a tooltip noting first-party identification or, for
  `other`, "catch-all".
- **Matrix tab filter**: one new dropdown at the top (Client).
  Default "All". Selection re-fetches with the query param and writes
  it into the URL (`?view=matrix&client=…`) so the view survives
  reload.

Plans page is **not** modified.

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
    reqCtx.ClientBucket populated from ClientBucketFromContext(r.Context()).
    router.Match(projectID, model, kind, reqCtx.ClientBucket)
    select upstream + dispatch
    on settle:
       if !IsExtraUsage:
         credits = policy.ComputeCreditsForClient(model, reqCtx.ClientBucket, catalogDefault, …)
       else:
         credits = computeExtraUsageCostCredits(model, usage)   // unchanged
```

The only NEW behavior:
1. Trace middleware additionally writes `ctxClientBucket`.
2. Executor stores `reqCtx.ClientBucket` and passes it to `Match` and
   to the subscription pricing resolver.
3. If the plan has no `client_model_credit_rates`, the resolver
   produces identical numbers to today.

## Error handling

| Failure | Behavior |
|---|---|
| Trace middleware can't classify → `ClientKindUnknown` | `MapClientKindToBucket` returns `other`. Routing falls through to client-agnostic routes; subscription pricing resolves `ClientModelCreditRates["other"][model]` first, then plan default. |
| `Router.Match` finds no matching route | Error message includes the client for diagnosability: `"no route configured for model X on endpoint Y (client=Z)"`. |
| Plan JSON has an unknown client bucket key in `client_model_credit_rates` | Silently ignored at resolution time. Admin write path validates against `AllClientBuckets` so the data should never appear. |
| Migration 056 or 057 runs twice | `IF NOT EXISTS` makes both idempotent. |
| Old dashboard sends route writes without the new field | Treated as empty array (= match any). Same as the migration default. |

## Testing

### Backend

- `internal/types/client_bucket_test.go` (new): each `ClientKind*` →
  expected `ClientBucket*` mapping; unknown / opencode / openclaw → `other`.
- `internal/types/policy_test.go` (extend): `TestComputeCreditsForClient`
  exhaustively covers the 5-level resolution order, plus
  `TestComputeCreditsWithDefault_BackwardCompat` asserting the wrapper
  produces identical numbers to today on plans without
  `ClientModelCreditRates`.
- `internal/proxy/router_engine_test.go` (extend):
  - `TestRouter_Match_ClientSpecificity` — `clients=[X]` beats
    `clients=[]` for an X-client request; loses for a Y-client request.
  - `TestRouter_Match_FullPrecedence` — `(project + clients) >
    (project alone) > (clients global) > (plain global)`; within the
    same spec bucket `match_priority` decides.
  - `TestRouter_Match_LegacyEmptyMatchesAny` — pre-migration routes
    (empty `Clients`) match every request.
  - `TestRouter_Match_DeterministicTiebreak` — same spec & priority →
    `ID asc` wins.
  - `TestRouter_MatrixGlobal_EmitsClient` — matrix cells carry
    `client` + `clients`.
  - `TestRouter_MatrixGlobal_FilterByClient` — query-param filter
    returns a focused slice.
- `internal/proxy/trace_middleware_test.go` (extend): every
  `deriveClientKind` outcome also produces the expected `ctxClientBucket`.
- `internal/admin/handle_routing_routes_test.go` (extend if scaffolding
  allows): write paths reject unknown client values; GET-by-id
  round-trips the new field.
- `internal/admin/handle_routing_matrix_test.go` (extend): matrix
  response cells carry the new fields; query-param filter works.
- `internal/store/migrations_056_test.go` (new): column added with
  empty default; INSERT with populated array round-trips.
- `internal/store/migrations_057_test.go` (new): column added as
  JSONB; NULL on pre-existing rows; UPDATE to populated JSON
  round-trips through `Plan.ToPolicy`.

### Invariant tests (no-regression)

A separate sub-suite in `internal/proxy/executor_finalize_test.go`
asserts:
- A subscription request from any client against a plan WITHOUT
  `client_model_credit_rates` produces the same credit count as a
  baseline `Policy.ComputeCreditsWithDefault` call.
- An extra-usage request produces the same credit count as today.

### Frontend

No test framework in this repo. Verification per task:
`pnpm exec tsc -b && pnpm build` clean + manual smoke listed in the
plan.

## Migration / deploy order

1. **Backend deploy.** Migration 056 adds `routes.clients`
   (`TEXT[] NOT NULL DEFAULT '{}'`). Migration 057 adds
   `plans.client_model_credit_rates JSONB` (NULL-default). Both
   idempotent on re-run.
   Existing routes carry an empty array → match any client → identical
   to today. Existing plans carry NULL → resolver step 1 misses →
   identical to today.
2. **Dashboard deploy.** Routes page shows the new column + dialog
   selector + matrix filter. Operators can begin creating
   client-scoped routes.
3. **Operator action (when desired).** Replace catch-all routes with
   client-specific routes (e.g. `clients: ["claude-code-cli"]` →
   claude-code-pool group; `clients: ["codex-cli"]` → codex-pool
   group; catch-all stays → default-pool group).
4. **Plans-level pricing changes.** Operators add
   `client_model_credit_rates` entries to plans via the existing
   `PUT /api/v1/plans/{slug}` admin endpoint. No UI change in v1.

Rollback safety:
- Migration 056 / 057 only ADD columns with safe defaults; rolling
  back the backend leaves columns present but unused — harmless.
- Subscription pricing without `ClientModelCreditRates` produces
  identical numbers to today (invariant test guards this).
- Extra-usage pricing path is untouched.
