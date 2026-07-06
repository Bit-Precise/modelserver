# CNY Price Bump 7/6× + max_140x/160x/180x/220x Plans Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a 7/6× CNY price bump on every existing paid plan AND four new max_Nx tiers (140x/160x/180x/220x) at the post-bump anchor price, effective on the next deploy via two database migrations.

**Architecture:** Two up-only SQL migrations run in order — `060` UPDATEs every non-free row's `price_cny_fen` by ROUND(× 7.0/6.0), then `061` INSERTs four new tiers born at their final post-bump CNY prices (`979999`/`1119999`/`1259999`/`1539999` fen) with USD at `N × $10` and rates cloned from the live `pro` row. Existing test `TestMigration059_PlanRowsPresent` is patched to the new mini/nano values (`6999`/`3499`), and two new test files cover the bump and the four new rows.

**Tech Stack:** Go 1.x, PostgreSQL (via pgx pool), existing `internal/store` numbered SQL migration framework.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-06-cny-price-bump-7-6x-design.md`.
- **Migration slots:** `060` (bump) and `061` (new tiers). Latest existing is `059_add_mini_nano_plans.sql`. Order matters — 060 must run before 061.
- **Bump ratio:** `price_cny_fen * 7.0 / 6.0` then `ROUND()` to nearest fen. Written as float literals (not `7/6` which is integer 1).
- **Bump scope:** `WHERE slug <> 'free'` — includes custom operator-created plans by design (same as 044).
- **USD (`price_usd_cents`) is NOT touched by 060.**
- **`extra_usage.credit_price_cny_fen` (config.yml) is NOT touched.**
- **Rate maps (`model_credit_rates`, `client_model_credit_rates`) are NOT touched by 060.**
- **New tier per-multiplier anchors** (from spec §3): tier_level = N × 100, price_cny_fen = N × 7000 − 1, price_usd_cents = N × 10000, 5h credits = N × 550000, 7d credits = N × 4166665.
- **New tiers exact values:**
  - `max_140x`: tier_level=14000, price_cny_fen=979999, price_usd_cents=140000, 5h=77000000, 7d=583333100.
  - `max_160x`: tier_level=16000, price_cny_fen=1119999, price_usd_cents=160000, 5h=88000000, 7d=666666400.
  - `max_180x`: tier_level=18000, price_cny_fen=1259999, price_usd_cents=180000, 5h=99000000, 7d=749999700.
  - `max_220x`: tier_level=22000, price_cny_fen=1539999, price_usd_cents=220000, 5h=121000000, 7d=916666300.
- **New tiers' description strings:** `Same usage limits as Claude Max (Nx)` verbatim per existing convention.
- **New tiers clone from pro:** `SELECT model_credit_rates, client_model_credit_rates FROM plans WHERE slug='pro'` (059 pattern), NOT the older hardcoded rate map (037/041 pattern).
- **Idempotency:** every INSERT uses `ON CONFLICT (slug) DO NOTHING`.
- **Post-060 mini/nano CNY values:** mini=6999, nano=3499 (must update `migration059Plans` expected values in `internal/store/migrations_059_test.go`).
- **Post-060 pre-existing CNY values (locked in migration 060 test):**
  ```
  free=0, pro=13999, mini=6999, nano=3499,
  max_2x=27999, max_5x=69999, max_20x=139999,
  max_40x=279999, max_60x=419999, max_80x=559999,
  max_100x=699999, max_120x=839999,
  max_200x=1399999, max_240x=1679999.
  ```
- **Post-061 additions (locked in migration 061 test):** max_140x=979999, max_160x=1119999, max_180x=1259999, max_220x=1539999.
- **Frontend, order pipeline, billing webhooks, `handle_plans` admin API — none change.**

## File Structure

- **Create:** `internal/store/migrations/060_bump_plan_prices_cny_7_6x.sql`
- **Create:** `internal/store/migrations/061_add_max_140x_160x_180x_220x_plans.sql`
- **Create:** `internal/store/migrations_060_test.go`
- **Create:** `internal/store/migrations_061_test.go`
- **Modify:** `internal/store/migrations_059_test.go` (post-060 mini/nano values)
- **Modify:** `internal/types/policy.go` (add 4 `PlanMax*` constants + extend `Subscription.PlanName` enum comment)

---

### Task 1: Update `TestMigration059_PlanRowsPresent` expectations for post-060 mini/nano CNY

**Files:**
- Modify: `internal/store/migrations_059_test.go:24-43` (the `migration059Plans` map entries for `mini` and `nano`)

**Interfaces:**
- Consumes: nothing.
- Produces: the test suite continues to pass once migration 060 (Task 2) lands. Migrations run to head before each test executes, so 059's asserted CNY values must reflect the post-060 state.

- [ ] **Step 1: Update the two struct entries**

Open `internal/store/migrations_059_test.go`. Replace the `mini` and `nano` entries in the `migration059Plans` map with the versions below. Only `PriceCNYFen` changes; also prepend a short comment on each to explain the discrepancy between the numeric value and migration 059's own INSERT.

```go
	"mini": {
		// PriceCNYFen was 5999 at migration 059; migration 060 bumps
		// non-free CNY prices by 7/6 (5999 * 7/6 ROUND = 6999). Tests
		// run all migrations to head before executing, so we assert
		// the post-060 terminal value here.
		Name: "Mini", DisplayName: "Mini",
		Description:   "Half of Pro's usage limits",
		TierLevel:     50,
		PriceCNYFen:   6999,
		PriceUSDCents: 1000,
		PeriodMonths:  1,
		Credit5h:      275000,
		Credit7d:      2500000,
	},
	"nano": {
		// PriceCNYFen was 2999 at migration 059; migration 060 bumps
		// it 7/6× to 3499. See mini comment above.
		Name: "Nano", DisplayName: "Nano",
		Description:   "Quarter of Pro's usage limits",
		TierLevel:     25,
		PriceCNYFen:   3499,
		PriceUSDCents: 500,
		PeriodMonths:  1,
		Credit5h:      137500,
		Credit7d:      1250000,
	},
```

- [ ] **Step 2: Verify the store package still compiles**

Run: `cd /root/coding/modelserver && go build ./internal/store/...`

Expected: exit 0, no output.

- [ ] **Step 3: Run the 059 test to confirm it still SKIPs cleanly without DB**

Run: `cd /root/coding/modelserver && go test ./internal/store/ -run TestMigration059 -v`

Expected: `--- SKIP: TestMigration059_PlanRowsPresent` and `--- SKIP: TestMigration059_ModelRatesClonedFromPro`, one per line, with the standard `set TEST_DATABASE_URL to run` message. Exit 0. (If `TEST_DATABASE_URL` is set, both should still PASS because migration 060 hasn't landed yet — the DB currently has mini=5999/nano=2999, which would make this test fail. In that case, note the failure in the report and proceed anyway — the test will go green once Task 2 lands. This is intentional TDD: the expectation moves ahead of the code by one commit.)

- [ ] **Step 4: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations_059_test.go
git commit -m "test(store): update 059 mini/nano CNY expectations for post-060 state

Migration 060 (next commit) bumps all non-free CNY prices by 7/6, so
mini=5999→6999 and nano=2999→3499. Tests run all migrations to head
before executing, so the 059 test must assert the terminal state, not
the value 059's own INSERT wrote.

This commit will fail its own test if TEST_DATABASE_URL is set and
060 has not yet landed — that is intentional TDD ordering.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Migration 060 — CNY price bump 7/6×

**Files:**
- Create: `internal/store/migrations/060_bump_plan_prices_cny_7_6x.sql`

**Interfaces:**
- Consumes: the existing `plans` table (post-049 schema with `price_cny_fen` column).
- Produces: every `plans` row where `slug <> 'free'` has its `price_cny_fen` multiplied by 7.0/6.0 and ROUND()ed to integer fen. Row count, other columns, and rate maps are untouched. `updated_at` is refreshed on every affected row.

- [ ] **Step 1: Create the migration file**

Create `internal/store/migrations/060_bump_plan_prices_cny_7_6x.sql` with this exact content:

```sql
-- 060_bump_plan_prices_cny_7_6x.sql
--
-- Across-the-board 7/6× bump on every priced plan's CNY price. The Free tier
-- (slug='free', price_cny_fen=0) is left alone — multiplying by 7/6 is a
-- no-op there anyway, but excluding it makes the intent explicit and
-- protects against accidentally activating a non-zero "free" tier in the
-- future. Custom operator-created plans are included by design (same
-- WHERE slug <> 'free' pattern as 044), so they stay in step with the
-- built-in tiers.
--
-- Only CNY is bumped. price_usd_cents (Stripe channel) is intentionally
-- left unchanged: USD tiers price in whole dollars, and 7/6 would produce
-- fractional-dollar amounts. Operators who want the USD side to move must
-- ship a separate migration.
--
-- extra_usage.credit_price_cny_fen (config.yml runtime setting) is also
-- unaffected — it lives outside the database. If it should move with
-- plans, operators update config at deploy time.
--
-- Pricing is stored as an integer number of fen (1/100 CNY). Multiplying
-- by 7/6 produces fractions (e.g. 11999 → 13998.833…); we ROUND() to the
-- nearest fen, matching the convention established by 044.
--
-- Already-issued orders snapshot unit_price/amount at checkout time (see
-- orders table in 001_init.sql), so this migration only affects future
-- purchases. Active subscriptions stay valid at their original purchase
-- price.
--
-- The schema_migrations table guarantees this migration runs exactly once
-- per database, so the bump is idempotent across redeploys.

UPDATE plans
SET price_cny_fen = ROUND(price_cny_fen * 7.0 / 6.0),
    updated_at    = NOW()
WHERE slug <> 'free';
```

- [ ] **Step 2: Verify build stays clean**

Run: `cd /root/coding/modelserver && go build ./...`

Expected: exit 0, no output. (Migrations are embedded via go:embed; adding one to the directory pulls it into the binary.)

- [ ] **Step 3: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations/060_bump_plan_prices_cny_7_6x.sql
git commit -m "migration: bump all non-free CNY plan prices by 7/6 (060)

Across-the-board CNY price bump matching the 044 (1.2×) template.
USD prices, extra-usage credit price, and per-model rate maps are
intentionally untouched. Custom operator-created plans are included
per the WHERE slug <> 'free' precedent. Already-issued orders and
active subscriptions are unaffected — orders snapshot price at
checkout.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Migration 060 test — assert bump correctness and USD/free non-perturbation

**Files:**
- Create: `internal/store/migrations_060_test.go`

**Interfaces:**
- Consumes: `openTestStore(t)` from `internal/store/extra_usage_db_test.go:13` (skips gracefully when `TEST_DATABASE_URL` is unset).
- Produces: three tests that lock in the CNY bump values, the USD-untouched contract, and the free-still-zero contract. Also implicitly covers the four new tiers introduced by Task 4's migration 061 (in the same map) because tests run all migrations to head.

- [ ] **Step 1: Create the test file**

Create `internal/store/migrations_060_test.go` with this content:

```go
package store

import (
	"context"
	"testing"
)

// migration060CNYAfter holds the expected price_cny_fen value for every
// built-in slug after migration 060 has run. Since tests run all migrations
// to head before executing, the map also includes the four new tiers
// introduced by migration 061 (max_140x/160x/180x/220x) at their as-inserted
// post-bump values.
var migration060CNYAfter = map[string]int64{
	"free":     0,
	"pro":      13999,
	"mini":     6999,
	"nano":     3499,
	"max_2x":   27999,
	"max_5x":   69999,
	"max_20x":  139999,
	"max_40x":  279999,
	"max_60x":  419999,
	"max_80x":  559999,
	"max_100x": 699999,
	"max_120x": 839999,
	"max_140x": 979999,  // introduced by 061 at post-bump price
	"max_160x": 1119999, // introduced by 061 at post-bump price
	"max_180x": 1259999, // introduced by 061 at post-bump price
	"max_200x": 1399999,
	"max_220x": 1539999, // introduced by 061 at post-bump price
	"max_240x": 1679999,
}

// migration060USDAfter holds the price_usd_cents value that must be
// preserved verbatim across migration 060. Values come from migration 049's
// backfill for pre-049 slugs, from 059 for mini/nano, and from 061 for the
// four new max tiers. Locking these in ensures 060 did not accidentally
// touch the USD column.
var migration060USDAfter = map[string]int64{
	"free":     0,
	"pro":      2000,
	"mini":     1000,
	"nano":     500,
	"max_2x":   4000,
	"max_5x":   10000,
	"max_20x":  20000,
	"max_40x":  40000,
	"max_60x":  60000,
	"max_80x":  80000,
	"max_100x": 100000,
	"max_120x": 120000,
	"max_140x": 140000, // introduced by 061
	"max_160x": 160000, // introduced by 061
	"max_180x": 180000, // introduced by 061
	"max_200x": 200000,
	"max_220x": 220000, // introduced by 061
	"max_240x": 240000,
}

// TestMigration060_CNYBumpedForKnownSlugs asserts every built-in slug's
// price_cny_fen matches the post-060 terminal value.
func TestMigration060_CNYBumpedForKnownSlugs(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration060CNYAfter {
		var got int64
		err := st.pool.QueryRow(ctx,
			`SELECT price_cny_fen FROM plans WHERE slug = $1`, slug).Scan(&got)
		if err != nil {
			t.Fatalf("query slug %s: %v", slug, err)
		}
		if got != want {
			t.Errorf("slug %s: price_cny_fen = %d, want %d", slug, got, want)
		}
	}
}

// TestMigration060_USDUnchanged locks in that migration 060 did not touch
// price_usd_cents on any known slug.
func TestMigration060_USDUnchanged(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration060USDAfter {
		var got int64
		err := st.pool.QueryRow(ctx,
			`SELECT price_usd_cents FROM plans WHERE slug = $1`, slug).Scan(&got)
		if err != nil {
			t.Fatalf("query slug %s: %v", slug, err)
		}
		if got != want {
			t.Errorf("slug %s: price_usd_cents = %d, want %d (must not change across 060)",
				slug, got, want)
		}
	}
}

// TestMigration060_FreeUnchanged is an explicit contract that the free tier
// stays at price_cny_fen=0 regardless of the bump. Overlaps with the map
// above but stands alone in case future edits drop free from the map.
func TestMigration060_FreeUnchanged(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var got int64
	err := st.pool.QueryRow(ctx,
		`SELECT price_cny_fen FROM plans WHERE slug = 'free'`).Scan(&got)
	if err != nil {
		t.Fatalf("query free: %v", err)
	}
	if got != 0 {
		t.Errorf("free tier: price_cny_fen = %d, want 0 (migration 060 must skip free)", got)
	}
}
```

- [ ] **Step 2: Verify build + run 060 tests**

Run these two commands in sequence:

```bash
cd /root/coding/modelserver && go build ./...
cd /root/coding/modelserver && go test ./internal/store/ -run TestMigration060 -v
```

Expected: build exits 0; tests SKIP with the standard `set TEST_DATABASE_URL to run` message (or PASS if `TEST_DATABASE_URL` is set and migration 061 has ALSO landed — otherwise the max_140x/160x/180x/220x rows will be missing and CNY/USD queries for those slugs will error out with `no rows in result set`, which is expected pre-Task 4 and OK to note in the report).

- [ ] **Step 3: Run the surrounding migration suite to confirm no regression**

Run: `cd /root/coding/modelserver && go test ./internal/store/ -run TestMigration -v`

Expected: all `TestMigration*` tests uniformly SKIP (or uniformly PASS if `TEST_DATABASE_URL` is set — but in the DB-set case, some may fail transiently until Task 4 lands, per above).

- [ ] **Step 4: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations_060_test.go
git commit -m "test(store): assert 060 CNY bump + USD/free non-perturbation

Three tests: CNY-after-bump table for every built-in slug (including
061's new max_140x/160x/180x/220x), USD-unchanged table, explicit
free-stays-zero. Follows the 049/053 skip-when-no-DB pattern.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Migration 061 — insert max_140x/160x/180x/220x at post-bump prices

**Files:**
- Create: `internal/store/migrations/061_add_max_140x_160x_180x_220x_plans.sql`

**Interfaces:**
- Consumes: the seed `pro` row (for cloning `model_credit_rates` and `client_model_credit_rates`).
- Produces: four new `plans` rows with the exact values in Global Constraints. `GET /api/plans` returns them starting next deploy.

- [ ] **Step 1: Create the migration file**

Create `internal/store/migrations/061_add_max_140x_160x_180x_220x_plans.sql` with this exact content:

```sql
-- 061_add_max_140x_160x_180x_220x_plans.sql
--
-- Introduce four new Max tiers filling the gaps in the existing 20x /
-- 40x / 60x / 80x / 100x / 120x / 200x / 240x ladder:
--   max_140x, max_160x, max_180x, max_220x
--
-- All four use the same conventions established by prior max_Nx additions:
--   - tier_level        = N * 100        (max_140x = 14000, etc.)
--   - price_cny_fen     = N * 7000 - 1   (xxxx99 rounding, post-060 anchor)
--   - price_usd_cents   = N * 10000      (N * $10, per migration 049's anchor)
--   - 5h credits        = N * 550000     (per-unit rate from max_20x onward)
--   - 7d credits        = N * 4166665
--   - model_credit_rates        = cloned from pro at migration time
--   - client_model_credit_rates = cloned from pro at migration time
--
-- Migration 049 requires every new plan to populate BOTH price_cny_fen
-- and price_usd_cents directly, so both are set explicitly here.
--
-- Cloning rates from pro (rather than hardcoding) follows the pattern
-- established by 059. If pro's rates or client overlay are later tuned,
-- these four tiers do NOT auto-follow — operators must reapply the tuning.
--
-- These rows are born at post-060 prices. Migration 060 runs first and
-- bumps existing rows 7/6×; 061 then inserts new rows already at the
-- final anchor, so 060 has no effect on them (nor could it — they did
-- not exist when 060 ran).
--
-- Idempotent via ON CONFLICT (slug) DO NOTHING, matching the seed inserts
-- in 001_init.sql and 059. If pro is somehow absent when this runs, all
-- four SELECTs return no rows and the INSERTs are no-ops; every deployment
-- we ship seeds pro from 001_init.sql.

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 140x', 'max_140x', 'Max 140x',
       'Same usage limits as Claude Max (140x)', 14000,
       979999, 140000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":77000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":583333100,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 160x', 'max_160x', 'Max 160x',
       'Same usage limits as Claude Max (160x)', 16000,
       1119999, 160000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":88000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":666666400,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 180x', 'max_180x', 'Max 180x',
       'Same usage limits as Claude Max (180x)', 18000,
       1259999, 180000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":99000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":749999700,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates, client_model_credit_rates)
SELECT 'Max 220x', 'max_220x', 'Max 220x',
       'Same usage limits as Claude Max (220x)', 22000,
       1539999, 220000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":121000000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":916666300,"scope":"project"}]'::jsonb,
       model_credit_rates, client_model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;
```

- [ ] **Step 2: Verify build**

Run: `cd /root/coding/modelserver && go build ./...`

Expected: exit 0, no output.

- [ ] **Step 3: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations/061_add_max_140x_160x_180x_220x_plans.sql
git commit -m "migration: add max_140x/160x/180x/220x plans (061)

Fill the gaps in the existing 20x/40x/60x/80x/100x/120x/200x/240x
ladder with four new tiers. Prices are set at the post-060 target
anchor (N × 7000 − 1 fen, N × \$10 USD); rates are cloned from pro
(model_credit_rates + client_model_credit_rates) per the 059 pattern.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Migration 061 test — assert four new rows land correctly and rates match pro

**Files:**
- Create: `internal/store/migrations_061_test.go`

**Interfaces:**
- Consumes: `openTestStore(t)` (skip-without-DB).
- Produces: two tests locking in scalar fields, credit_rules windows, and rate-map clone contract for each of the four new slugs.

- [ ] **Step 1: Create the test file**

Create `internal/store/migrations_061_test.go` with this content:

```go
package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// migration061Plans holds the exact scalar fields each new max_Nx plan
// must carry after migration 061 runs.
var migration061Plans = map[string]struct {
	Name          string
	DisplayName   string
	Description   string
	TierLevel     int64
	PriceCNYFen   int64
	PriceUSDCents int64
	PeriodMonths  int64
	Credit5h      int64
	Credit7d      int64
}{
	"max_140x": {
		Name: "Max 140x", DisplayName: "Max 140x",
		Description:   "Same usage limits as Claude Max (140x)",
		TierLevel:     14000,
		PriceCNYFen:   979999,
		PriceUSDCents: 140000,
		PeriodMonths:  1,
		Credit5h:      77000000,
		Credit7d:      583333100,
	},
	"max_160x": {
		Name: "Max 160x", DisplayName: "Max 160x",
		Description:   "Same usage limits as Claude Max (160x)",
		TierLevel:     16000,
		PriceCNYFen:   1119999,
		PriceUSDCents: 160000,
		PeriodMonths:  1,
		Credit5h:      88000000,
		Credit7d:      666666400,
	},
	"max_180x": {
		Name: "Max 180x", DisplayName: "Max 180x",
		Description:   "Same usage limits as Claude Max (180x)",
		TierLevel:     18000,
		PriceCNYFen:   1259999,
		PriceUSDCents: 180000,
		PeriodMonths:  1,
		Credit5h:      99000000,
		Credit7d:      749999700,
	},
	"max_220x": {
		Name: "Max 220x", DisplayName: "Max 220x",
		Description:   "Same usage limits as Claude Max (220x)",
		TierLevel:     22000,
		PriceCNYFen:   1539999,
		PriceUSDCents: 220000,
		PeriodMonths:  1,
		Credit5h:      121000000,
		Credit7d:      916666300,
	},
}

// TestMigration061_NewMaxPlansPresent asserts the four new plan rows exist
// with the expected scalar fields, credit_rules windows, and is_active=true.
func TestMigration061_NewMaxPlansPresent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration061Plans {
		var (
			name, displayName, description                      string
			tierLevel, priceCNYFen, priceUSDCents, periodMonths int64
			creditRulesJSON                                     []byte
			isActive                                            bool
		)
		err := st.pool.QueryRow(ctx, `
			SELECT name, display_name, description, tier_level,
			       price_cny_fen, price_usd_cents, period_months,
			       credit_rules, is_active
			FROM plans WHERE slug = $1`, slug).
			Scan(&name, &displayName, &description, &tierLevel,
				&priceCNYFen, &priceUSDCents, &periodMonths, &creditRulesJSON, &isActive)
		if err != nil {
			t.Fatalf("query slug %s: %v", slug, err)
		}

		if name != want.Name {
			t.Errorf("slug %s: name = %q, want %q", slug, name, want.Name)
		}
		if displayName != want.DisplayName {
			t.Errorf("slug %s: display_name = %q, want %q", slug, displayName, want.DisplayName)
		}
		if description != want.Description {
			t.Errorf("slug %s: description = %q, want %q", slug, description, want.Description)
		}
		if tierLevel != want.TierLevel {
			t.Errorf("slug %s: tier_level = %d, want %d", slug, tierLevel, want.TierLevel)
		}
		if priceCNYFen != want.PriceCNYFen {
			t.Errorf("slug %s: price_cny_fen = %d, want %d", slug, priceCNYFen, want.PriceCNYFen)
		}
		if priceUSDCents != want.PriceUSDCents {
			t.Errorf("slug %s: price_usd_cents = %d, want %d", slug, priceUSDCents, want.PriceUSDCents)
		}
		if periodMonths != want.PeriodMonths {
			t.Errorf("slug %s: period_months = %d, want %d", slug, periodMonths, want.PeriodMonths)
		}
		if !isActive {
			t.Errorf("slug %s: is_active = false, want true", slug)
		}

		// credit_rules: a two-element array; assert window + max_credits on each.
		var rules []struct {
			Window     string `json:"window"`
			WindowType string `json:"window_type"`
			MaxCredits int64  `json:"max_credits"`
			Scope      string `json:"scope"`
		}
		if err := json.Unmarshal(creditRulesJSON, &rules); err != nil {
			t.Fatalf("slug %s: unmarshal credit_rules: %v", slug, err)
		}
		if len(rules) != 2 {
			t.Fatalf("slug %s: got %d credit_rules, want 2", slug, len(rules))
		}
		if rules[0].Window != "5h" || rules[0].WindowType != "sliding" ||
			rules[0].Scope != "project" || rules[0].MaxCredits != want.Credit5h {
			t.Errorf("slug %s: 5h rule = %+v, want window=5h window_type=sliding scope=project max_credits=%d",
				slug, rules[0], want.Credit5h)
		}
		if rules[1].Window != "7d" || rules[1].WindowType != "sliding" ||
			rules[1].Scope != "project" || rules[1].MaxCredits != want.Credit7d {
			t.Errorf("slug %s: 7d rule = %+v, want window=7d window_type=sliding scope=project max_credits=%d",
				slug, rules[1], want.Credit7d)
		}
	}
}

// TestMigration061_NewMaxPlansCloneRatesFromPro asserts each new tier's
// model_credit_rates AND client_model_credit_rates deep-equal pro's. Same
// shape as TestMigration059_ModelRatesClonedFromPro, extended to four slugs.
func TestMigration061_NewMaxPlansCloneRatesFromPro(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// pro reference values.
	var proRates []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT model_credit_rates FROM plans WHERE slug = 'pro'`).Scan(&proRates); err != nil {
		t.Fatalf("read pro rates: %v", err)
	}
	var proMap map[string]any
	if err := json.Unmarshal(proRates, &proMap); err != nil {
		t.Fatalf("unmarshal pro rates: %v", err)
	}

	var proClient []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT client_model_credit_rates FROM plans WHERE slug = 'pro'`).Scan(&proClient); err != nil {
		t.Fatalf("read pro client rates: %v", err)
	}
	var proClientMap map[string]any
	if proClient != nil {
		if err := json.Unmarshal(proClient, &proClientMap); err != nil {
			t.Fatalf("unmarshal pro client rates: %v", err)
		}
	}

	for _, slug := range []string{"max_140x", "max_160x", "max_180x", "max_220x"} {
		// model_credit_rates
		var raw []byte
		if err := st.pool.QueryRow(ctx,
			`SELECT model_credit_rates FROM plans WHERE slug = $1`, slug).Scan(&raw); err != nil {
			t.Fatalf("read %s rates: %v", slug, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal %s rates: %v", slug, err)
		}
		if !reflect.DeepEqual(got, proMap) {
			t.Errorf("slug %s: model_credit_rates does not match pro exactly", slug)
		}

		// client_model_credit_rates (may be NULL on all; both-NULL counts equal).
		var rawClient []byte
		if err := st.pool.QueryRow(ctx,
			`SELECT client_model_credit_rates FROM plans WHERE slug = $1`, slug).Scan(&rawClient); err != nil {
			t.Fatalf("read %s client rates: %v", slug, err)
		}
		if (rawClient == nil) != (proClient == nil) {
			t.Errorf("slug %s: client_model_credit_rates NULL-ness differs from pro (got nil=%v, pro nil=%v)",
				slug, rawClient == nil, proClient == nil)
			continue
		}
		if rawClient == nil {
			continue // both NULL — equal
		}
		var gotClient map[string]any
		if err := json.Unmarshal(rawClient, &gotClient); err != nil {
			t.Fatalf("unmarshal %s client rates: %v", slug, err)
		}
		if !reflect.DeepEqual(gotClient, proClientMap) {
			t.Errorf("slug %s: client_model_credit_rates does not match pro exactly", slug)
		}
	}
}
```

- [ ] **Step 2: Verify build + run 061 tests**

```bash
cd /root/coding/modelserver && go build ./...
cd /root/coding/modelserver && go test ./internal/store/ -run TestMigration061 -v
```

Expected: build exits 0; both tests SKIP with the standard `set TEST_DATABASE_URL to run` message (or PASS if `TEST_DATABASE_URL` is set).

- [ ] **Step 3: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations_061_test.go
git commit -m "test(store): assert 061 inserts max_140x/160x/180x/220x correctly

Two tests: scalar fields + credit_rules + is_active for each of the
four new slugs, and deep-equal of model_credit_rates and
client_model_credit_rates against pro (both-NULL client rates count
as equal, same shape as 059's clone test).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Add `PlanMax140x`/`PlanMax160x`/`PlanMax180x`/`PlanMax220x` constants

**Files:**
- Modify: `internal/types/policy.go:5-18` (const block) and `internal/types/policy.go:217` (`Subscription.PlanName` enum comment)

**Interfaces:**
- Consumes: nothing.
- Produces: four new documentation-aid constants (`PlanMax140x = "max_140x"`, etc.). No runtime code currently switches on these constants; they exist for grep symmetry. The `Subscription.PlanName` field's enum comment is extended to include the four new slugs.

- [ ] **Step 1: Update the const block**

Open `internal/types/policy.go`. Replace the entire predefined-plan-names const block (currently lines 5-18, containing `PlanPro` through `PlanMax240x`) with the version below. Keep the alphabetical-within-magnitude ordering that matches existing style (mini/nano stay grouped after PlanPro; new max_Nx are inserted in ascending numeric order).

```go
// Predefined plan names.
const (
	PlanPro     = "pro"
	PlanMini    = "mini"
	PlanNano    = "nano"
	PlanMax5x   = "max_5x"
	PlanMax20x  = "max_20x"
	PlanMax40x  = "max_40x"
	PlanMax60x  = "max_60x"
	PlanMax80x  = "max_80x"
	PlanMax100x = "max_100x"
	PlanMax120x = "max_120x"
	PlanMax140x = "max_140x"
	PlanMax160x = "max_160x"
	PlanMax180x = "max_180x"
	PlanMax200x = "max_200x"
	PlanMax220x = "max_220x"
	PlanMax240x = "max_240x"
)
```

- [ ] **Step 2: Update the `Subscription.PlanName` enum comment**

In the same file, find the current line 217:

```go
	PlanName  string    `json:"plan_name"` // "pro", "mini", "nano", "max_5x", "max_20x", "max_40x", "max_60x", "max_80x", "max_100x", "max_120x", "max_200x", "max_240x", or custom
```

Replace it with:

```go
	PlanName  string    `json:"plan_name"` // "pro", "mini", "nano", "max_5x", "max_20x", "max_40x", "max_60x", "max_80x", "max_100x", "max_120x", "max_140x", "max_160x", "max_180x", "max_200x", "max_220x", "max_240x", or custom
```

- [ ] **Step 3: Verify the types package compiles**

Run: `cd /root/coding/modelserver && go build ./internal/types/...`

Expected: exit 0, no output.

- [ ] **Step 4: Run the types package tests**

Run: `cd /root/coding/modelserver && go test ./internal/types/...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add internal/types/policy.go
git commit -m "types: add PlanMax140x/160x/180x/220x constants

Symmetry with the existing PlanMax* block, matching the four new
tiers introduced by migration 061. No runtime code switches on
these constants; they exist for grep and to keep the
Subscription.PlanName enum comment aligned with the plan set.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Full-repo build + test sweep + push branch

**Files:** none new. Verification gate before opening a PR.

**Interfaces:** none.

- [ ] **Step 1: Full build**

Run: `cd /root/coding/modelserver && go build ./...`

Expected: exit 0, no output.

- [ ] **Step 2: Full test suite**

Run: `cd /root/coding/modelserver && go test ./...`

Expected: PASS across every package. In the store package, all `TestMigration*` tests uniformly SKIP if `TEST_DATABASE_URL` is unset. If it IS set, everything should PASS end-to-end (059 test now expects the post-060 values, 060 test expects the bumped values, 061 test expects the new rows).

- [ ] **Step 3: Confirm migration file layout**

Run: `cd /root/coding/modelserver && ls internal/store/migrations/ | sort | tail -8`

Expected output ends with:
```
054_add_request_indexes.sql
055_revoke_orphaned_api_keys.sql
056_route_clients.sql
057_plan_client_credit_rates.sql
058_add_sonnet_5_fable_5.sql
059_add_mini_nano_plans.sql
060_bump_plan_prices_cny_7_6x.sql
061_add_max_140x_160x_180x_220x_plans.sql
```

- [ ] **Step 4: Confirm the branch commits**

Run: `cd /root/coding/modelserver && git log --oneline main..HEAD`

Expected: 7 commits (spec + Tasks 1-6), youngest first.

- [ ] **Step 5: Push the branch**

Run: `cd /root/coding/modelserver && git push -u origin feat/cny-price-bump-7-6x`

Expected: push succeeds. PR creation is deferred to a follow-up.

---

## Deploy-Time Verification (post-merge, not a task)

After merge and next deploy:

1. **Bump applied:**
   ```sql
   SELECT slug, price_cny_fen, price_usd_cents
     FROM plans ORDER BY tier_level;
   ```
   Every non-free row matches the post-060 map; the four new max_Nx rows exist at their spec values; `price_usd_cents` matches migration 049's backfill (pre-049 slugs) or the values in 059/061 (new slugs).
2. **Free preserved:** `SELECT price_cny_fen FROM plans WHERE slug = 'free';` → 0.
3. **Row count:** `SELECT COUNT(*) FROM plans WHERE slug IN ('max_140x','max_160x','max_180x','max_220x');` → 4.
4. **API surface:** `GET /api/plans` returns all rows including the four new tiers.
5. **Dashboard:** `/subscriptions` renders each card at the new CNY price (¥139.99 Pro, ¥69.99 Mini, ¥34.99 Nano, ¥9799.99 Max 140x, etc.) and includes the four new Max tiers.
6. **OAuth `project_type`:** subscribers on any of the four new tiers see `project_type: "max"` (via existing `strings.HasPrefix("max_")` branch).
7. **Wechat/alipay orders:** new orders quote the new CNY price; existing orders' `amount` unchanged.
8. **Stripe orders:** new orders quote the (unchanged) USD price.
9. **`extra_usage.credit_price_cny_fen`:** unchanged (still 5438 fen unless operators updated config themselves).
