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

### Task 3: Migration 057 — plans.client_model_credit_rates

**Files:**
- Create: `internal/store/migrations/057_plan_client_credit_rates.sql`
- Create: `internal/store/migrations_057_test.go`

**Interfaces:**
- Consumes: existing `plans` table schema. The plan row already carries a sibling `model_credit_rates JSONB` column (verified at `internal/store/migrations/001_init.sql:95` and `:184`); 057 adds the parallel client-keyed column.
- Produces: new column `plans.client_model_credit_rates JSONB` (nullable). Subsequent tasks (`types.Plan` field + `internal/store/plans.go` upsert/select extension + `Plan.ToPolicy` carryover + `Policy.ComputeCreditsForClient` resolver) consume this column.

- [ ] **Step 1: Write the SQL**

Create `internal/store/migrations/057_plan_client_credit_rates.sql`:

```sql
-- 057_plan_client_credit_rates.sql
--
-- Per-client per-model credit rate overlay for subscription consumption.
-- Shape (JSON object indexed by client bucket, then model name):
--
--   {
--     "claude-code-cli": {
--       "claude-sonnet-4": { "input_rate": 3, "output_rate": 15, ... },
--       "claude-opus-4":   { "input_rate": 15, "output_rate": 75, ... }
--     },
--     "codex-cli": {
--       "gpt-5":           { "input_rate": 0.5, "output_rate": 4 }
--     }
--   }
--
-- Resolution order at runtime (Policy.ComputeCreditsForClient):
--   1. client_model_credit_rates[client][model]   (this column)
--   2. model_credit_rates[model]                  (existing column)
--   3. catalog model.default_credit_rate           (catalog truth)
--   4. model_credit_rates["_default"]              (plan-wide safety net)
--   5. zero (no billing)
--
-- Extra-usage requests do NOT consult this column — they bill at the
-- catalog default rate via computeExtraUsageCostCredits.
--
-- Default NULL on existing rows. NULL is treated as "no overrides" by
-- the resolver. Idempotent via IF NOT EXISTS. No down step.

ALTER TABLE plans
    ADD COLUMN IF NOT EXISTS client_model_credit_rates JSONB;
```

- [ ] **Step 2: Write the migration test**

Create `internal/store/migrations_057_test.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"testing"
)

// TestMigration057_AddsPlanClientRatesColumnNullByDefault asserts the
// migration adds client_model_credit_rates as a nullable JSONB, leaves
// existing rows with NULL, and round-trips a populated JSON object.
func TestMigration057_AddsPlanClientRatesColumnNullByDefault(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Seed a plan using ONLY the pre-057 columns. The new column must
	// accept the row via its NULL default.
	var planID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO plans (name, slug, display_name, description, tier_level,
		    price_cny_fen, period_months, is_active)
		VALUES ('mig057-test', 'mig057-test', 'Migration 057 Test', '', 0,
		        0, 1, FALSE)
		RETURNING id`).Scan(&planID); err != nil {
		t.Fatalf("seed old-style plan: %v", err)
	}

	// Read the new column back; expect NULL.
	var raw []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT client_model_credit_rates FROM plans WHERE id = $1`, planID).
		Scan(&raw); err != nil {
		t.Fatalf("select new column: %v", err)
	}
	if raw != nil {
		t.Errorf("default client_model_credit_rates = %q, want NULL", raw)
	}

	// Populate with a realistic shape and assert it round-trips.
	want := map[string]map[string]map[string]float64{
		"claude-code-cli": {
			"claude-sonnet-4": {"input_rate": 3, "output_rate": 15},
		},
		"codex-cli": {
			"gpt-5": {"input_rate": 0.5, "output_rate": 4},
		},
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`UPDATE plans SET client_model_credit_rates = $1 WHERE id = $2`,
		wantJSON, planID); err != nil {
		t.Fatalf("populate column: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT client_model_credit_rates FROM plans WHERE id = $1`, planID).
		Scan(&raw); err != nil {
		t.Fatalf("select populated column: %v", err)
	}
	var got map[string]map[string]map[string]float64
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	for client, models := range want {
		gm, ok := got[client]
		if !ok {
			t.Errorf("got missing client %q", client)
			continue
		}
		for model, rates := range models {
			gr, ok := gm[model]
			if !ok {
				t.Errorf("got[%q] missing model %q", client, model)
				continue
			}
			for field, v := range rates {
				if gr[field] != v {
					t.Errorf("got[%q][%q][%q] = %v, want %v",
						client, model, field, gr[field], v)
				}
			}
		}
	}
}

// TestMigration057_Idempotent asserts re-running the migration is a no-op.
func TestMigration057_Idempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.pool.Exec(ctx,
		`ALTER TABLE plans ADD COLUMN IF NOT EXISTS client_model_credit_rates JSONB`); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}
}
```

- [ ] **Step 3: Run the migration test**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./internal/store/ -run TestMigration057 -v`
Expected: SKIP without `TEST_DATABASE_URL`; PASS with it.

- [ ] **Step 4: Run the full store package**

Run: `cd /root/coding/modelserver && go test ./internal/store/...`
Expected: PASS (skips fine).

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations/057_plan_client_credit_rates.sql internal/store/migrations_057_test.go
git commit -m "feat(store): migration 057 — plans.client_model_credit_rates JSONB

Adds a nullable JSONB column for per-client per-model credit-rate
overlays on subscription consumption. Resolution order (in
Policy.ComputeCreditsForClient, landing in a later task):
  client_model_credit_rates[client][model]
    -> model_credit_rates[model]
    -> catalog model.default_credit_rate
    -> model_credit_rates['_default']
    -> zero

Extra-usage requests bypass this column entirely — they bill at the
catalog default rate via computeExtraUsageCostCredits.

Idempotent via IF NOT EXISTS; no down step.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: types.Route + types.Plan + store load/save wiring

**Files:**
- Modify: `internal/types/route.go` (add `Clients`, `BillingModes` fields)
- Modify: `internal/types/plan.go` (add `ClientModelCreditRates` field + thread through `ToPolicy`)
- Modify: `internal/types/policy.go` (add `ClientModelCreditRates` field on `RateLimitPolicy`)
- Modify: `internal/store/routes.go` (extend `routeSelectCols`, `CreateRoute`, `scanRoute`, `GetRouteByID`)
- Modify: `internal/store/plans.go` (extend `CreatePlan`, `GetPlanByID`, `GetPlanBySlug`, `ListPlans`, `ListPlansPaginated`, `ListPlansForProject`, `scanPlans`, `unmarshalPlanJSON`)

**Interfaces:**
- Consumes: migration 056 + 057 (Tasks 2-3).
- Produces:
  ```go
  // route.go
  type Route struct {
      // ... existing ...
      Clients      []string `json:"clients"`
      BillingModes []string `json:"billing_modes"`
  }

  // plan.go
  type Plan struct {
      // ... existing ...
      ClientModelCreditRates map[string]map[string]CreditRate `json:"client_model_credit_rates,omitempty"`
  }

  // policy.go
  type RateLimitPolicy struct {
      // ... existing ...
      ClientModelCreditRates map[string]map[string]CreditRate `json:"client_model_credit_rates,omitempty"`
  }
  ```
  No behavior change yet: `Router.Match` still ignores the new route fields; `Policy.ComputeCreditsWithDefault` still ignores the new rate map. Tasks 6 + 7 wire them in.

This task is the data plane: every CRUD path round-trips the new fields, every existing test continues to pass. The new fields stay invisible to consumers until Tasks 6-7 light them up.

- [ ] **Step 1: Extend `types.Route`**

In `internal/types/route.go`, change the struct to:

```go
package types

import "time"

// Route maps a set of canonical model names to an upstream group
// (nginx: location block). The route matches a request when its
// canonical model name (post-alias-resolution) appears in ModelNames,
// the request kind appears in RequestKinds, the client bucket appears
// in Clients (or Clients is empty = match any), and the billing mode
// appears in BillingModes (or BillingModes is empty = match any).
// Ordering among competing routes is given by weighted specificity
// then MatchPriority — see internal/proxy/router_engine.go.
type Route struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"project_id,omitempty"` // "" = global route
	ModelNames      []string          `json:"model_names"`          // Canonical model names only (no aliases, no globs)
	RequestKinds    []string          `json:"request_kinds"`        // Wire-level endpoint kinds; values from internal/types/request_kind.go
	Clients         []string          `json:"clients"`              // ClientBucket values; empty = match any. See internal/types/client_bucket.go.
	BillingModes    []string          `json:"billing_modes"`        // BillingMode values; empty = match any. See internal/types/billing_mode.go.
	UpstreamGroupID string            `json:"upstream_group_id"`
	MatchPriority   int               `json:"match_priority"`
	Conditions      map[string]string `json:"conditions,omitempty"`
	Status          string            `json:"status"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}
```

- [ ] **Step 2: Extend `types.Plan` + `Plan.ToPolicy`**

In `internal/types/plan.go`:

Add the field to the struct (preserve all other fields):

```go
type Plan struct {
	// ... all existing fields, unchanged ...
	ModelCreditRates       map[string]CreditRate            `json:"model_credit_rates,omitempty"`
	ClientModelCreditRates map[string]map[string]CreditRate `json:"client_model_credit_rates,omitempty"`
	// ... other existing fields ...
}
```

Extend `ToPolicy` to thread the new field:

```go
func (p *Plan) ToPolicy(projectID string, subscriptionStartsAt *time.Time) *RateLimitPolicy {
	rules := make([]CreditRule, len(p.CreditRules))
	copy(rules, p.CreditRules)
	if subscriptionStartsAt != nil {
		for i := range rules {
			if rules[i].WindowType == WindowTypeFixed {
				t := *subscriptionStartsAt
				rules[i].AnchorTime = &t
			}
		}
	}
	return &RateLimitPolicy{
		ID:                     "plan:" + p.ID,
		ProjectID:              projectID,
		Name:                   p.Name,
		CreditRules:            rules,
		ModelCreditRates:       p.ModelCreditRates,
		ClientModelCreditRates: p.ClientModelCreditRates, // NEW
		ClassicRules:           p.ClassicRules,
	}
}
```

- [ ] **Step 3: Extend `RateLimitPolicy`**

In `internal/types/policy.go`, add the field to the struct (preserve every other field):

```go
type RateLimitPolicy struct {
	// ... all existing fields, unchanged ...
	ModelCreditRates       map[string]CreditRate            `json:"model_credit_rates,omitempty"`
	ClientModelCreditRates map[string]map[string]CreditRate `json:"client_model_credit_rates,omitempty"`
	// ... other existing fields ...
}
```

- [ ] **Step 4: Build + run types tests**

Run: `cd /root/coding/modelserver && go build ./internal/types/... && go test ./internal/types/...`
Expected: green. Existing `TestComputeCredits` etc. continue to pass because the new field is unused by today's resolver.

- [ ] **Step 5: Extend `internal/store/routes.go`**

Edit the `routeSelectCols` constant and the three call sites that scan / insert routes. The new column order appends `clients, billing_modes` AFTER the existing columns so SELECT-list / Scan-list / INSERT-list all stay in lockstep.

Replace the const at the top of the file:

```go
const routeSelectCols = `id, COALESCE(project_id::text, ''), model_names, request_kinds,
	upstream_group_id, match_priority, conditions, status, created_at, updated_at,
	clients, billing_modes`
```

Update `CreateRoute`:

```go
func (s *Store) CreateRoute(r *types.Route) error {
	conditionsJSON, _ := json.Marshal(r.Conditions)
	if r.Conditions == nil {
		conditionsJSON = []byte("{}")
	}
	modelNames := r.ModelNames
	if modelNames == nil {
		modelNames = []string{}
	}
	requestKinds := r.RequestKinds
	if requestKinds == nil {
		requestKinds = []string{}
	}
	clients := r.Clients
	if clients == nil {
		clients = []string{}
	}
	billingModes := r.BillingModes
	if billingModes == nil {
		billingModes = []string{}
	}
	return s.pool.QueryRow(context.Background(), `
		INSERT INTO routes (project_id, model_names, request_kinds, upstream_group_id,
		    match_priority, conditions, status, clients, billing_modes)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at`,
		nullString(r.ProjectID), modelNames, requestKinds, r.UpstreamGroupID,
		r.MatchPriority, conditionsJSON, r.Status, clients, billingModes,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
}
```

Update `GetRouteByID` Scan argument list (append the two new pointers — order must match `routeSelectCols`):

```go
func (s *Store) GetRouteByID(id string) (*types.Route, error) {
	r := &types.Route{}
	var conditionsRaw []byte
	err := s.pool.QueryRow(context.Background(),
		fmt.Sprintf(`SELECT %s FROM routes WHERE id = $1`, routeSelectCols), id,
	).Scan(&r.ID, &r.ProjectID, &r.ModelNames, &r.RequestKinds, &r.UpstreamGroupID,
		&r.MatchPriority, &conditionsRaw, &r.Status, &r.CreatedAt, &r.UpdatedAt,
		&r.Clients, &r.BillingModes)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get route: %w", err)
	}
	r.Conditions = unmarshalConditions(conditionsRaw)
	return r, nil
}
```

Update `scanRoute`:

```go
func scanRoute(rows pgx.Rows) (*types.Route, error) {
	r := &types.Route{}
	var conditionsRaw []byte
	if err := rows.Scan(&r.ID, &r.ProjectID, &r.ModelNames, &r.RequestKinds,
		&r.UpstreamGroupID, &r.MatchPriority, &conditionsRaw, &r.Status,
		&r.CreatedAt, &r.UpdatedAt, &r.Clients, &r.BillingModes); err != nil {
		return nil, err
	}
	r.Conditions = unmarshalConditions(conditionsRaw)
	return r, nil
}
```

`ListRoutes`, `ListRoutesPaginated`, and `ListRoutesForProject` all use `routeSelectCols` + `scanRoute` so they need no further edits.

The admin update path (`handleUpdateRoutingRoute` in `internal/admin/handle_routing_routes.go`) maintains a `for _, field := range []string{"project_id", "model_names", "request_kinds", "upstream_group_id", "match_priority", "conditions", "status"} { ... }` allow-list — Task 8 extends that with `"clients", "billing_modes"`. This task does NOT touch the admin handler; the new fields are read-only on the round-trip via the store layer change alone, which is enough to keep `go build ./...` green.

- [ ] **Step 6: Extend `internal/store/plans.go`**

The plan SELECT list is **inlined verbatim in five places** (`GetPlanByID`, `GetPlanBySlug`, `ListPlans`, `ListPlansPaginated`, `ListPlansForProject`). Extending without breaking them requires adding the new column to every SELECT + every Scan + the INSERT.

Add the column at a stable position in every SELECT list, e.g. immediately after `model_credit_rates` (apply to every site):

```go
// BEFORE:
SELECT id, name, slug, display_name, description, tier_level, group_tag,
    price_cny_fen, price_usd_cents, period_months, credit_rules, model_credit_rates,
    classic_rules, is_active, created_at, updated_at
FROM plans ...

// AFTER:
SELECT id, name, slug, display_name, description, tier_level, group_tag,
    price_cny_fen, price_usd_cents, period_months, credit_rules, model_credit_rates,
    client_model_credit_rates,
    classic_rules, is_active, created_at, updated_at
FROM plans ...
```

`scanPlans` and the individual `Scan` calls grow one more `[]byte` pointer for the new column. Add a local `var clientRates []byte` alongside the existing `var creditRules, rates, classic []byte`, and pass `&clientRates` into the Scan in the matching position.

Extend `unmarshalPlanJSON` to accept the new bytes and decode into `p.ClientModelCreditRates`:

```go
func unmarshalPlanJSON(p *types.Plan, creditRules, rates, clientRates, classic []byte) error {
	if creditRules != nil {
		if err := json.Unmarshal(creditRules, &p.CreditRules); err != nil {
			return fmt.Errorf("unmarshal credit_rules: %w", err)
		}
	}
	if rates != nil {
		if err := json.Unmarshal(rates, &p.ModelCreditRates); err != nil {
			return fmt.Errorf("unmarshal model_credit_rates: %w", err)
		}
	}
	if clientRates != nil {
		if err := json.Unmarshal(clientRates, &p.ClientModelCreditRates); err != nil {
			return fmt.Errorf("unmarshal client_model_credit_rates: %w", err)
		}
	}
	if classic != nil {
		if err := json.Unmarshal(classic, &p.ClassicRules); err != nil {
			return fmt.Errorf("unmarshal classic_rules: %w", err)
		}
	}
	return nil
}
```

Update every caller of `unmarshalPlanJSON` to pass the new arg in the same position.

Extend `CreatePlan`:

```go
func (s *Store) CreatePlan(p *types.Plan) error {
	creditRulesJSON, _ := marshalJSON(p.CreditRules)
	ratesJSON, _ := marshalJSON(p.ModelCreditRates)
	clientRatesJSON, _ := marshalJSON(p.ClientModelCreditRates)
	classicJSON, _ := marshalJSON(p.ClassicRules)

	return s.pool.QueryRow(context.Background(), `
		INSERT INTO plans (name, slug, display_name, description, tier_level, group_tag,
			price_cny_fen, price_usd_cents, period_months, credit_rules, model_credit_rates,
			client_model_credit_rates, classic_rules, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		RETURNING id, created_at, updated_at`,
		p.Name, p.Slug, p.DisplayName, p.Description, p.TierLevel, p.GroupTag,
		p.PriceCNYFen, p.PriceUSDCents, p.PeriodMonths, creditRulesJSON, ratesJSON,
		clientRatesJSON, classicJSON, p.IsActive,
	).Scan(&p.ID, &p.CreatedAt, &p.UpdatedAt)
}
```

The admin update path (`internal/admin/handle_plans.go`'s field allow-list) is touched in Task 8; this task does NOT modify it. `UpdatePlan` is generic (`buildUpdateQuery`) so it accepts whatever map the admin handler passes — no change needed here.

- [ ] **Step 7: Build + run store tests**

Run: `cd /root/coding/modelserver && go build ./... && go test ./internal/store/...`
Expected: all green (including the migration tests from Tasks 2 + 3 if `TEST_DATABASE_URL` is set; skip otherwise). Existing plan / route tests must continue to PASS — the new fields are zero-value on every prior fixture, which round-trips fine.

- [ ] **Step 8: Commit**

```bash
cd /root/coding/modelserver
git add internal/types/route.go internal/types/plan.go internal/types/policy.go \
        internal/store/routes.go internal/store/plans.go
git commit -m "feat(types,store): plumb Clients/BillingModes/ClientModelCreditRates

Route gains two []string fields; Plan and RateLimitPolicy gain a
two-level map[client][model]CreditRate field. Store load/save round-trip
the new columns added in migrations 056+057.

No behavior change yet: Router.Match still ignores the new route fields
and Policy.ComputeCreditsWithDefault still ignores the new rate map.
Tasks 6 + 7 wire them in.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

> **End of installment 2 (Tasks 3-4).** Tasks 1-4 together build the data plane: types know the new fields, store round-trips them, migrations make the columns exist. Nothing reads them yet — the matcher and pricing resolver are still on their old code paths.
>
> Remaining installments:
> - **Installment 3 (Task 5):** trace middleware `ctxClientBucket` plumbing + getter + tests.
> - **Installment 4 (Task 6):** `Router.Match` signature break + `matchesGlobalRoute` extension + weighted specificity sort + `MatrixGlobal` extension + executor caller update + full router_engine_test.go overhaul.
> - **Installment 5 (Task 7):** `Policy.ComputeCreditsForClient` resolver + Executor pricing call-site update + no-regression invariant tests.
> - **Installment 6 (Task 8):** admin API validation for new route fields + `GET /routing/clients` + `GET /routing/billing-modes` + matrix endpoint filter params + plan admin allow-list update.
> - **Installment 7 (Task 9):** dashboard Routes page columns + Create/Edit dialog selectors + Matrix tab filter dropdowns + manual smoke checklist.
> - **Final installment:** plan self-review section + execution handoff.

