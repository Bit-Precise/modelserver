# Client-Aware Routing + Pricing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Thread two new dimensions through the proxy: a per-request `client_bucket ∈ {claude-code-cli, claude-desktop, codex-cli, codex-desktop, other}` derived from the existing `ClientKind*` enum, and a per-request `billing_mode ∈ {subscription, extra_usage}` derived from the already-existing `reqCtx.IsExtraUsage`. Routes can optionally be scoped by both. Plans can override the credit rate per `(client, model)` for subscription consumption. Extra-usage pricing is unchanged.

**Architecture:** Two layers, one PR. Routing layer extends `Route` with `clients []string` + `billing_modes []string` (empty = match any) and `Router.Match` with a weighted-specificity tiebreak (project 100, clients 10, billing_modes 1; then `match_priority desc`; then `id asc`). Pricing layer extends `Plan` with `client_model_credit_rates map[client]map[model]CreditRate` and adds `Policy.ComputeCreditsForClient` that resolves per-client → per-model → catalog default → plan `_default`. **No new middleware, no new balance check** — `billing_mode` is just `IsExtraUsage` rendered as a string. Backward-compat invariant: plans without the new field produce identical credit counts to today; routes with empty arrays match any client/mode.

**Tech Stack:** Go 1.x, `pgx` (`pool.Begin` / `tx.Exec`), `JSONB`, stdlib `testing`. React 19, `@tanstack/react-query` v5, Tailwind v4. Two SQL migrations (056, 057). No new dependencies.

## Global Constraints

- **Spec:** `docs/superpowers/specs/2026-06-28-client-aware-routing-pricing-design.md` — re-read before each task.
- **`billing_mode` IS NOT a new balance check.** It is `reqCtx.IsExtraUsage` rendered as a string in the Executor, one line before `router.Match`. Do NOT add middleware that queries subscription balance. Do NOT change `SubscriptionEligibilityMiddleware`'s `SubscriptionEligibility{Eligible, Reason}` output shape.
- **Extra-usage pricing is unchanged.** Do NOT touch `computeExtraUsageCostCredits` or `settleExtraUsage`. Per-client overrides apply ONLY to the subscription pricing path (`executor.go:1090, :1328`).
- **Backward-compat invariants:**
  - A plan WITHOUT `client_model_credit_rates` MUST produce identical credit counts to today (resolver falls through to existing `ModelCreditRates[model]` at step 2).
  - A route with empty `clients` AND empty `billing_modes` MUST match every request (= today's behavior).
- **Client bucket values:** exactly `claude-code-cli`, `claude-desktop`, `codex-cli`, `codex-desktop`, `other`. Constants in `internal/types/client_bucket.go`.
- **Billing mode values:** exactly `subscription`, `extra_usage`. Constants in `internal/types/billing_mode.go`.
- **`ClientBucketCodexDesktop` is reserved.** `MapClientKindToBucket` returns `other` for any input today — codex-desktop will be wired when the product ships an identifiable client.
- **Specificity weights:** project=100, clients=10, billing_modes=1. Final tiebreak `ID asc`. Same code path used by `Match` AND `MatrixGlobal` via the shared `matchesGlobalRoute` predicate.
- **Migrations:** numbered 056 (routes columns) and 057 (plans column). `IF NOT EXISTS` guarded. No down step.
- **No frontend test framework** — dashboard tasks verify via `pnpm exec tsc -b && pnpm build` + manual smoke.
- **Commit message footer:** every commit ends with
  ```
  Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
  ```

## File Structure

**Backend — create:**
- `internal/types/client_bucket.go` — 5 constants + `MapClientKindToBucket` + `IsValidClientBucket` + `AllClientBuckets`.
- `internal/types/client_bucket_test.go`
- `internal/types/billing_mode.go` — 2 constants + `IsValidBillingMode` + `AllBillingModes`.
- `internal/types/billing_mode_test.go`
- `internal/store/migrations/056_route_client_billing.sql`
- `internal/store/migrations_056_test.go`
- `internal/store/migrations/057_plan_client_credit_rates.sql`
- `internal/store/migrations_057_test.go`

**Backend — modify:**
- `internal/types/route.go` — add `Clients []string` + `BillingModes []string` fields.
- `internal/types/plan.go` — add `ClientModelCreditRates map[string]map[string]CreditRate` + thread through `ToPolicy`.
- `internal/types/policy.go` — add `ClientModelCreditRates` field + `ComputeCreditsForClient(model, client, catalogDefault, …)` resolver; keep `ComputeCreditsWithDefault` as a thin wrapper.
- `internal/types/policy_test.go` — add resolver tests + backward-compat invariant test.
- `internal/store/routes.go` — extend `routeSelectCols`, `CreateRoute`, `scanRoute`, `handleUpdateRoutingRoute` field allow-list with the two new columns.
- `internal/store/plans.go` — extend `CreatePlan`, `GetPlanByID`, `GetPlanBySlug`, `ListPlans`, `ListPlansPaginated`, `ListPlansForProject`, `scanPlans`, `unmarshalPlanJSON` with the new column position.
- `internal/proxy/router_engine.go` — extend `Router.Match` signature; extend `matchesGlobalRoute`; replace priority-only sort with weighted-specificity sort; extend `MatrixGlobal` shape + add optional `client` / `billingMode` filter params; extend `MatrixCell` with `Clients []string` and `BillingModes []string`.
- `internal/proxy/router_engine_test.go` — update existing call sites; add new precedence / tiebreak / matrix-filter tests.
- `internal/proxy/trace_middleware.go` — write `ctxClientBucket` next to existing `ctxClientKind`; export `ClientBucketFromContext`.
- `internal/proxy/trace_middleware_test.go` — assert bucket is populated for every `ClientKind*`.
- `internal/proxy/executor.go` — compute `client + billingMode` one line before `router.Match`; switch the two subscription pricing call sites to `ComputeCreditsForClient(model, client, …)`.
- `internal/proxy/executor_finalize_test.go` — add no-regression invariant tests (subscription credit count identical when `ClientModelCreditRates` is absent; extra-usage credit count identical period).
- `internal/admin/handle_routing_routes.go` — accept + validate the two new fields on create/update; ensure GET-by-id and list paths return them (automatic via the struct change).
- `internal/admin/handle_routing_matrix.go` — accept `?client=`, `?billing_mode=` query params; include `clients` / `billing_modes` in each cell; pass filters into `MatrixGlobal`.
- `internal/admin/handle_routing_matrix_test.go` — extend with filter cases.
- `internal/admin/routes.go` — register two new GET endpoints: `/routing/clients` and `/routing/billing-modes`.

**Frontend — modify:**
- `dashboard/src/api/types.ts` — extend `RoutingRoute` + `RoutingMatrixCell` with new fields.
- `dashboard/src/api/upstreams.ts` — add `useClientBuckets()` and `useBillingModes()` hooks; extend `useRoutingMatrix` to accept optional `{ client?, billingMode? }`.
- `dashboard/src/pages/admin/RoutesPage.tsx` — two new table columns; two new multi-select controls in the Create/Edit dialog.
- `dashboard/src/pages/admin/RoutesMatrixView.tsx` — two filter dropdowns at the top; cells subscript with mode-specificity hint.

---

### Task 1: ClientBucket + BillingMode type primitives

**Files:**
- Create: `internal/types/client_bucket.go`
- Create: `internal/types/client_bucket_test.go`
- Create: `internal/types/billing_mode.go`
- Create: `internal/types/billing_mode_test.go`

**Interfaces:**
- Consumes: existing `ClientKind*` constants in `internal/types/extra_usage.go`.
- Produces:
  ```go
  // client_bucket.go
  const (
      ClientBucketClaudeCodeCLI = "claude-code-cli"
      ClientBucketClaudeDesktop = "claude-desktop"
      ClientBucketCodexCLI      = "codex-cli"
      ClientBucketCodexDesktop  = "codex-desktop"
      ClientBucketOther         = "other"
  )
  var AllClientBuckets []string
  func IsValidClientBucket(s string) bool
  func MapClientKindToBucket(kind string) string

  // billing_mode.go
  type BillingMode = string
  const (
      BillingModeSubscription BillingMode = "subscription"
      BillingModeExtraUsage   BillingMode = "extra_usage"
  )
  var AllBillingModes []string
  func IsValidBillingMode(s string) bool
  ```

Pure additions. No callers yet. Task 5 wires `MapClientKindToBucket` into the trace middleware; Tasks 6-7 wire `BillingMode` constants into the executor.

- [ ] **Step 1: Write the failing tests**

Create `internal/types/client_bucket_test.go`:

```go
package types

import "testing"

func TestMapClientKindToBucket(t *testing.T) {
	cases := []struct {
		kind, want string
	}{
		{ClientKindClaudeCode, ClientBucketClaudeCodeCLI},
		{ClientKindClaudeDesktop, ClientBucketClaudeDesktop},
		{ClientKindCodex, ClientBucketCodexCLI},
		{ClientKindOpenCode, ClientBucketOther},
		{ClientKindOpenClaw, ClientBucketOther},
		{ClientKindUnknown, ClientBucketOther},
		{"", ClientBucketOther},
		{"some-future-thing", ClientBucketOther},
	}
	for _, c := range cases {
		if got := MapClientKindToBucket(c.kind); got != c.want {
			t.Errorf("MapClientKindToBucket(%q) = %q, want %q", c.kind, got, c.want)
		}
	}
}

func TestIsValidClientBucket(t *testing.T) {
	for _, b := range AllClientBuckets {
		if !IsValidClientBucket(b) {
			t.Errorf("IsValidClientBucket(%q) = false, want true", b)
		}
	}
	for _, b := range []string{"", "claude-code", "anything-else"} {
		if IsValidClientBucket(b) {
			t.Errorf("IsValidClientBucket(%q) = true, want false", b)
		}
	}
}

func TestAllClientBuckets_ContainsFive(t *testing.T) {
	if got := len(AllClientBuckets); got != 5 {
		t.Errorf("len(AllClientBuckets) = %d, want 5", got)
	}
}

func TestClientBucketCodexDesktop_ReservedReturnsOther(t *testing.T) {
	// Today no client_kind maps to codex-desktop — the bucket is reserved
	// for a future product. Confirm the mapping function does not return it.
	for _, k := range []string{ClientKindClaudeCode, ClientKindClaudeDesktop,
		ClientKindCodex, ClientKindOpenCode, ClientKindOpenClaw, ClientKindUnknown} {
		if got := MapClientKindToBucket(k); got == ClientBucketCodexDesktop {
			t.Errorf("ClientKind %q unexpectedly maps to codex-desktop", k)
		}
	}
}
```

Create `internal/types/billing_mode_test.go`:

```go
package types

import "testing"

func TestIsValidBillingMode(t *testing.T) {
	for _, m := range AllBillingModes {
		if !IsValidBillingMode(m) {
			t.Errorf("IsValidBillingMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "sub", "extra-usage", "subscription "} {
		if IsValidBillingMode(m) {
			t.Errorf("IsValidBillingMode(%q) = true, want false", m)
		}
	}
}

func TestAllBillingModes_ContainsTwo(t *testing.T) {
	if got := len(AllBillingModes); got != 2 {
		t.Errorf("len(AllBillingModes) = %d, want 2", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail with "undefined"**

Run: `cd /root/coding/modelserver && go test ./internal/types/ -run 'TestMapClientKindToBucket|TestIsValidClientBucket|TestAllClientBuckets|TestClientBucketCodexDesktop|TestIsValidBillingMode|TestAllBillingModes' -v`
Expected: build errors — undefined constants and functions.

- [ ] **Step 3: Implement `client_bucket.go`**

Create `internal/types/client_bucket.go`:

```go
package types

// Client bucket constants. Five-bucket projection of the existing
// ClientKind* enum, used for routing and per-client pricing.
//
// claude-code-cli, claude-desktop, codex-cli are derived from today's
// deriveClientKind output. codex-desktop is reserved for a future Codex
// desktop product; MapClientKindToBucket returns "other" for every
// current input. The bucket exists in the schema today so admin tools
// and the dashboard can name it without a follow-up migration.
const (
	ClientBucketClaudeCodeCLI = "claude-code-cli"
	ClientBucketClaudeDesktop = "claude-desktop"
	ClientBucketCodexCLI      = "codex-cli"
	ClientBucketCodexDesktop  = "codex-desktop"
	ClientBucketOther         = "other"
)

// AllClientBuckets enumerates every ClientBucket* constant.
// Used by admin input validation and the dashboard dropdown.
var AllClientBuckets = []string{
	ClientBucketClaudeCodeCLI,
	ClientBucketClaudeDesktop,
	ClientBucketCodexCLI,
	ClientBucketCodexDesktop,
	ClientBucketOther,
}

// IsValidClientBucket reports whether s is one of the five bucket values.
func IsValidClientBucket(s string) bool {
	for _, b := range AllClientBuckets {
		if b == s {
			return true
		}
	}
	return false
}

// MapClientKindToBucket projects the six-value ClientKind* enum onto the
// five-bucket axis used by routing and pricing.
//
// The codex-desktop case is intentionally absent: no current
// deriveClientKind output identifies that product. When Codex ships a
// desktop client with a recognizable signature (UA / header / body),
// add a dedicated case here. Today every codex-desktop request falls
// into "other".
func MapClientKindToBucket(kind string) string {
	switch kind {
	case ClientKindClaudeCode:
		return ClientBucketClaudeCodeCLI
	case ClientKindClaudeDesktop:
		return ClientBucketClaudeDesktop
	case ClientKindCodex:
		return ClientBucketCodexCLI
	default:
		// ClientKindOpenCode, ClientKindOpenClaw, ClientKindUnknown,
		// and any future kind not explicitly mapped above.
		return ClientBucketOther
	}
}
```

- [ ] **Step 4: Implement `billing_mode.go`**

Create `internal/types/billing_mode.go`:

```go
package types

// BillingMode tags whether a request consumes the project's subscription
// or its extra-usage balance. The value is derived from reqCtx.IsExtraUsage
// in the Executor (one line before router.Match) — it is NOT computed by
// a middleware and does NOT involve a balance check at routing time. The
// authoritative balance gating lives in RateLimitMiddleware +
// ExtraUsageGuardMiddleware, exactly as today.
type BillingMode = string

const (
	BillingModeSubscription BillingMode = "subscription"
	BillingModeExtraUsage   BillingMode = "extra_usage"
)

// AllBillingModes enumerates every BillingMode constant. Used by admin
// input validation and the dashboard dropdown.
var AllBillingModes = []string{
	BillingModeSubscription,
	BillingModeExtraUsage,
}

// IsValidBillingMode reports whether s is one of the two mode values.
func IsValidBillingMode(s string) bool {
	for _, m := range AllBillingModes {
		if m == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /root/coding/modelserver && go test ./internal/types/ -run 'TestMapClientKindToBucket|TestIsValidClientBucket|TestAllClientBuckets|TestClientBucketCodexDesktop|TestIsValidBillingMode|TestAllBillingModes' -v`
Expected: all PASS.

- [ ] **Step 6: Run full types package to confirm no regressions**

Run: `cd /root/coding/modelserver && go test ./internal/types/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add internal/types/client_bucket.go internal/types/client_bucket_test.go \
        internal/types/billing_mode.go internal/types/billing_mode_test.go
git commit -m "feat(types): ClientBucket (5) + BillingMode (2) primitives

ClientBucket is a five-value projection of the existing ClientKind*
enum used for routing and per-client pricing. MapClientKindToBucket
collapses claude-desktop, codex-cli, codex-desktop, other onto the
bucket axis; codex-desktop is reserved and always returns other today.

BillingMode is the subscription | extra_usage label that the routing
table can target. It is rendered from reqCtx.IsExtraUsage in the
Executor — this commit only defines the constants; later commits wire
them in.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Migration 056 — routes.clients + routes.billing_modes

**Files:**
- Create: `internal/store/migrations/056_route_client_billing.sql`
- Create: `internal/store/migrations_056_test.go`

**Interfaces:**
- Consumes: existing `routes` table schema (`internal/store/migrations/001_init.sql` + later).
- Produces: two new columns `routes.clients TEXT[]` and `routes.billing_modes TEXT[]`, both `NOT NULL DEFAULT '{}'`. Subsequent tasks (`types.Route` struct change, store load/save updates, `Router.Match` extension) consume these columns.

Pre-existing migration max is `055_revoke_orphaned_api_keys.sql` (merged via PR #65). 056 is the next number.

- [ ] **Step 1: Write the SQL**

Create `internal/store/migrations/056_route_client_billing.sql`:

```sql
-- 056_route_client_billing.sql
--
-- Add two routing dimensions to the routes table:
--
--   clients        — when populated, only requests whose derived
--                    ClientBucket (5 values: claude-code-cli,
--                    claude-desktop, codex-cli, codex-desktop, other)
--                    is in this list match the route.
--   billing_modes  — when populated, only requests whose billing_mode
--                    (subscription | extra_usage) is in this list match
--                    the route.
--
-- Empty array means "match any value", preserving today's behavior for
-- every existing route. Migration is therefore safe to deploy ahead of
-- the matcher upgrade — old routes simply continue to match every
-- request as they do today.
--
-- Idempotent via IF NOT EXISTS. No down step.

ALTER TABLE routes
    ADD COLUMN IF NOT EXISTS clients       TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS billing_modes TEXT[] NOT NULL DEFAULT '{}';
```

- [ ] **Step 2: Write the migration test**

Create `internal/store/migrations_056_test.go`:

```go
package store

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// TestMigration056_AddsRouteColumnsWithEmptyDefault asserts the migration
// adds clients + billing_modes as TEXT[] NOT NULL DEFAULT '{}', leaves
// existing rows with empty arrays, and round-trips populated values.
func TestMigration056_AddsRouteColumnsWithEmptyDefault(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Seed an upstream group so the FK on routes.upstream_group_id is satisfied.
	var groupID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO upstream_groups (name, lb_policy, status)
		VALUES ('mig056-test', 'weighted_random', 'active')
		RETURNING id`).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}

	// Insert a route using ONLY the pre-056 columns. The new columns
	// must accept the row via their defaults.
	var oldRouteID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO routes (model_names, request_kinds, upstream_group_id, match_priority, status)
		VALUES (ARRAY['claude-sonnet'], ARRAY[$1::text], $2, 10, 'active')
		RETURNING id`, types.KindAnthropicMessages, groupID).Scan(&oldRouteID); err != nil {
		t.Fatalf("insert old-style route: %v", err)
	}

	// Read it back; the two new columns must be present as empty arrays.
	var clients, modes []string
	if err := st.pool.QueryRow(ctx,
		`SELECT clients, billing_modes FROM routes WHERE id = $1`, oldRouteID).
		Scan(&clients, &modes); err != nil {
		t.Fatalf("select new columns: %v", err)
	}
	if len(clients) != 0 {
		t.Errorf("default clients = %v, want []", clients)
	}
	if len(modes) != 0 {
		t.Errorf("default billing_modes = %v, want []", modes)
	}

	// Insert a route WITH the new columns populated.
	var newRouteID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO routes (model_names, request_kinds, upstream_group_id, match_priority, status, clients, billing_modes)
		VALUES (ARRAY['claude-sonnet'], ARRAY[$1::text], $2, 20, 'active',
		        ARRAY['claude-code-cli','claude-desktop'], ARRAY['subscription'])
		RETURNING id`, types.KindAnthropicMessages, groupID).Scan(&newRouteID); err != nil {
		t.Fatalf("insert new-style route: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT clients, billing_modes FROM routes WHERE id = $1`, newRouteID).
		Scan(&clients, &modes); err != nil {
		t.Fatalf("select populated columns: %v", err)
	}
	wantClients := []string{"claude-code-cli", "claude-desktop"}
	wantModes := []string{"subscription"}
	if !equalStringSlices(clients, wantClients) {
		t.Errorf("populated clients = %v, want %v", clients, wantClients)
	}
	if !equalStringSlices(modes, wantModes) {
		t.Errorf("populated billing_modes = %v, want %v", modes, wantModes)
	}
}

// TestMigration056_Idempotent asserts re-running the migration is a no-op.
func TestMigration056_Idempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	_, err := st.pool.Exec(ctx, `
		ALTER TABLE routes
		    ADD COLUMN IF NOT EXISTS clients       TEXT[] NOT NULL DEFAULT '{}',
		    ADD COLUMN IF NOT EXISTS billing_modes TEXT[] NOT NULL DEFAULT '{}'`)
	if err != nil {
		t.Fatalf("re-run migration: %v", err)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

If `equalStringSlices` already exists in the package (it might, from prior migration tests), drop the local copy and reuse the existing one.

- [ ] **Step 3: Run the migration test**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/store/ -run TestMigration056 -v`

Without `TEST_DATABASE_URL` set the test prints SKIP — acceptable for local quick checks; CI exercises it. With the var set, expected: both PASS.

- [ ] **Step 4: Run the full store package**

Run: `cd /root/coding/modelserver && go test ./internal/store/...`
Expected: PASS (skips fine; no test failures).

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations/056_route_client_billing.sql internal/store/migrations_056_test.go
git commit -m "feat(store): migration 056 — routes.clients + routes.billing_modes

Adds two TEXT[] columns to routes with empty-array defaults. Existing
routes carry empty arrays and continue to match every request (= today's
behavior). Subsequent tasks wire the matcher to read them.

Idempotent via IF NOT EXISTS; no down step.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

> **End of installment 1 (Tasks 1-2).** The plan continues with:
> - Task 3: Migration 057 + test (plans.client_model_credit_rates)
> - Task 4: types.Route, types.Plan, store load/save wiring
> - Task 5: trace middleware ctxClientBucket plumbing
> - Task 6: Router.Match signature + weighted specificity + MatrixGlobal extension + tests
> - Task 7: Policy.ComputeCreditsForClient + Executor pricing call sites + invariant tests
> - Task 8: Admin API validation + new GET endpoints + matrix filters
> - Task 9: Dashboard Routes page + Matrix tab UI

