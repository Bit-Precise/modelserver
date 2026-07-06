# Mini & Nano Subscription Plans Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add two new paid subscription tiers (`mini` and `nano`) at 1/2 and 1/4 of Pro's usage limits and price, taking effect on the next deploy via a single database migration.

**Architecture:** One up-only SQL migration (`059_add_mini_nano_plans.sql`) inserts two rows into `plans`, cloning `model_credit_rates` from the live `pro` row and setting halved / quartered `credit_rules` and prices. Two small Go patches add plan-slug constants and extend `mapPlanToProjectType` so the OAuth profile endpoint reports Mini/Nano subscribers as `project_type: "pro"` to Claude Code clients. The frontend requires no changes — the subscription page renders whatever plans the API returns.

**Tech Stack:** Go 1.x, PostgreSQL (via pgx pool), existing `internal/store` migration framework (numbered SQL files auto-applied on startup).

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-06-add-mini-nano-plans-design.md`.
- **Migration number:** `059` (bumped from originally-planned `058` when PR #71 landed its own `058_add_sonnet_5_fable_5.sql` on main first).
- **model_credit_rates policy:** Copy from `pro` at migration time — one-shot snapshot, not a foreign-key relationship. Do NOT hardcode the per-model rate map.
- **CNY prices:** integer fen. Mini = `5999`, Nano = `2999`.
- **USD prices:** integer cents. Mini = `1000`, Nano = `500`.
- **tier_level:** Mini = `50`, Nano = `25` (slotting between `free=0` and `pro=100`).
- **credit_rules windows:** `5h` sliding, `7d` sliding (matching Pro's shape). Mini: `275000` / `2500000`. Nano: `137500` / `1250000`.
- **description strings** must be single-quoted English (SQL-safe with `''` for apostrophe): `Half of Pro''s usage limits` / `Quarter of Pro''s usage limits`.
- **Idempotency:** every INSERT uses `ON CONFLICT (slug) DO NOTHING` — matches the `001_init.sql` seed convention.
- **Currency behavior:** unchanged. `handleCreateOrder`'s currency locking applies to Mini/Nano the same way it applies to Pro; nothing to configure.
- **No frontend changes.** No admin UI changes. No billing pipeline changes.

## File Structure

- **Create:** `internal/store/migrations/059_add_mini_nano_plans.sql`
- **Create:** `internal/store/migrations_059_test.go`
- **Modify:** `internal/types/policy.go` (add two constants, extend one comment)
- **Modify:** `internal/proxy/me_handler.go` (extend `mapPlanToProjectType` switch + docstring)
- **Modify:** `internal/proxy/me_handler_test.go` (add two rows to `TestMapPlanToProjectType`)

---

### Task 1: `mapPlanToProjectType` treats mini/nano as pro

**Files:**
- Modify: `internal/proxy/me_handler.go:72-89` (function body and preceding docstring)
- Test: `internal/proxy/me_handler_test.go:213-231` (extend the existing table test)

**Interfaces:**
- Consumes: nothing new.
- Produces: `mapPlanToProjectType("mini") == "pro"` and `mapPlanToProjectType("nano") == "pro"`. Existing signature `func mapPlanToProjectType(planName string) string` is unchanged.

- [ ] **Step 1: Add the failing test cases first**

Open `internal/proxy/me_handler_test.go`, find the `TestMapPlanToProjectType` table at line ~215, and add two rows in the paid-tier block (just after the `{"max_200x", "max"}` line, before the "Anything else" comment):

```go
{"mini", "pro"},
{"nano", "pro"},
```

The final table should look like:

```go
func TestMapPlanToProjectType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pro", "pro"},
		{"mini", "pro"},
		{"nano", "pro"},
		{"max_2x", "max"},
		{"max_5x", "max"},
		{"max_200x", "max"},
		// Anything else maps to empty string (omitempty drops it from response).
		{"free", ""},
		{"", ""},
		{"max", ""},      // bare "max" without _Nx suffix — no real modelserver plan uses this
		{"custom", ""},
	} {
		if got := mapPlanToProjectType(tc.in); got != tc.want {
			t.Errorf("mapPlanToProjectType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd /root/coding/modelserver && go test ./internal/proxy/ -run TestMapPlanToProjectType -v`

Expected: FAIL with two error lines like:
```
mapPlanToProjectType("mini") = "", want "pro"
mapPlanToProjectType("nano") = "", want "pro"
```

- [ ] **Step 3: Extend the switch and update the docstring**

Open `internal/proxy/me_handler.go`. Replace the block at lines 72-89 (the docstring paragraph plus the function) with:

```go
// mapPlanToProjectType maps a modelserver Subscription.PlanName onto the
// project_type values surfaced in the response. modelserver's plan set
// (per migrations 040/049/059 etc.) is {free, pro, mini, nano, max_2x..max_240x};
// `pro`, `mini`, `nano`, and the `max_*` family are the paid tiers, so those
// are the only values the mapping can produce. Mini and Nano subscribers see
// project_type="pro" so Claude Code clients (which only recognize pro/max)
// treat them as paid users rather than degrading them to free-tier behavior.
// Anything else (no subscription, free, custom slugs) returns the empty
// string, which omitempty drops from the response — clients then see
// project_type as absent rather than being misled by a synthetic value.
func mapPlanToProjectType(planName string) string {
	switch {
	case planName == "pro" || planName == "mini" || planName == "nano":
		return "pro"
	case strings.HasPrefix(planName, "max_"):
		return "max"
	default:
		return ""
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /root/coding/modelserver && go test ./internal/proxy/ -run TestMapPlanToProjectType -v`

Expected: PASS. Output ends with `--- PASS: TestMapPlanToProjectType`.

- [ ] **Step 5: Run the surrounding proxy test suite to catch collateral damage**

Run: `cd /root/coding/modelserver && go test ./internal/proxy/ -run 'TestMapPlanToProjectType|TestHandleOAuthProfile|TestBuildIdentity'`

Expected: PASS on all three tests.

- [ ] **Step 6: Commit**

```bash
cd /root/coding/modelserver
git add internal/proxy/me_handler.go internal/proxy/me_handler_test.go
git commit -m "feat(proxy): treat mini/nano as pro in mapPlanToProjectType

Mini and Nano subscribers should surface as project_type='pro' to
Claude Code clients so they are not silently downgraded to free-tier
behavior. They share Pro's per-model credit rates and differ only in
credit budgets and price.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Add PlanMini and PlanNano constants

**Files:**
- Modify: `internal/types/policy.go:5-17` (const block) and `internal/types/policy.go:217` (Subscription.PlanName comment)

**Interfaces:**
- Consumes: nothing.
- Produces: `types.PlanMini` (= `"mini"`) and `types.PlanNano` (= `"nano"`) — documentation aides for grep; no runtime code currently switches on any `PlanXxx` constant. The `Subscription.PlanName` field's enum comment is extended to include the two new slugs.

- [ ] **Step 1: Add the two constants**

Open `internal/types/policy.go`. Replace the block at lines 5-17 with:

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
	PlanMax200x = "max_200x"
	PlanMax240x = "max_240x"
)
```

- [ ] **Step 2: Update the Subscription.PlanName comment**

In the same file, find line 217:

```go
	PlanName  string    `json:"plan_name"` // "pro", "max_5x", "max_20x", "max_40x", "max_60x", "max_80x", "max_100x", "max_120x", "max_200x", "max_240x", or custom
```

Replace with:

```go
	PlanName  string    `json:"plan_name"` // "pro", "mini", "nano", "max_5x", "max_20x", "max_40x", "max_60x", "max_80x", "max_100x", "max_120x", "max_200x", "max_240x", or custom
```

- [ ] **Step 3: Verify the package still compiles**

Run: `cd /root/coding/modelserver && go build ./internal/types/...`

Expected: exit 0, no output.

- [ ] **Step 4: Run the types package tests**

Run: `cd /root/coding/modelserver && go test ./internal/types/...`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add internal/types/policy.go
git commit -m "types: add PlanMini and PlanNano constants

Symmetry with PlanPro / PlanMax*. No runtime code switches on these
constants today; they exist so callers grepping for PlanMini find a
canonical definition, and to keep the Subscription.PlanName enum
comment in sync with the plan set introduced by migration 059.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Migration 059 — insert Mini and Nano rows

**Files:**
- Create: `internal/store/migrations/059_add_mini_nano_plans.sql`

**Interfaces:**
- Consumes: the existing `plans` table (schema from `001_init.sql` + `049_plans_multi_currency.sql`), and the presence of the seed `pro` row inserted by `001_init.sql`.
- Produces: two new rows in `plans` with slugs `mini` and `nano`, cloning `model_credit_rates` from `pro`. `GET /api/plans` will return them starting with the next deploy.

- [ ] **Step 1: Create the migration file**

Create `internal/store/migrations/059_add_mini_nano_plans.sql` with this exact content:

```sql
-- 059_add_mini_nano_plans.sql
--
-- Introduces two new paid tiers, Mini and Nano, positioned between Free and
-- Pro. Both reuse Pro's per-model credit rates verbatim — they differ from
-- Pro only in credit_rules (halved / quartered) and price (halved /
-- quartered).
--
-- model_credit_rates are copied from the live pro row at migration time
-- (rather than hardcoded) because the pro rate map has been mutated by
-- migrations 044 (×1.2 prices — unrelated) and 047, 053 (×2 GPT rates), and
-- future tuning is easier if we do not fork a second source of truth here.
-- If pro's rates are later tuned, mini/nano do NOT auto-follow — operators
-- must reapply the tuning if they want parity.
--
-- Idempotent via ON CONFLICT (slug) DO NOTHING, matching the seed inserts
-- in 001_init.sql. If pro is somehow absent when this runs, the SELECT
-- returns no rows and both INSERTs become no-ops; that is acceptable
-- because every deployment we ship seeds pro from 001_init.sql.

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates)
SELECT 'Mini', 'mini', 'Mini', 'Half of Pro''s usage limits', 50,
       5999, 1000, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":275000,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":2500000,"scope":"project"}]'::jsonb,
       model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;

INSERT INTO plans (name, slug, display_name, description, tier_level,
                   price_cny_fen, price_usd_cents, period_months,
                   credit_rules, model_credit_rates)
SELECT 'Nano', 'nano', 'Nano', 'Quarter of Pro''s usage limits', 25,
       2999, 500, 1,
       '[{"window":"5h","window_type":"sliding","max_credits":137500,"scope":"project"},
         {"window":"7d","window_type":"sliding","max_credits":1250000,"scope":"project"}]'::jsonb,
       model_credit_rates
FROM plans WHERE slug = 'pro'
ON CONFLICT (slug) DO NOTHING;
```

- [ ] **Step 2: Verify the file's SQL parses (dry-run against a scratch DB — optional if psql isn't available)**

If you have psql access:

```bash
psql -h localhost -U postgres -d postgres -c "EXPLAIN $(cat internal/store/migrations/059_add_mini_nano_plans.sql | tr '\n' ' ')" 2>&1 | head -5
```

If no psql is handy, skip this step — the migration test in Task 4 will catch syntax errors by actually running it.

- [ ] **Step 3: Commit the migration file**

```bash
cd /root/coding/modelserver
git add internal/store/migrations/059_add_mini_nano_plans.sql
git commit -m "migration: add mini and nano plans (059)

Two new paid tiers between Free and Pro at 1/2 and 1/4 of Pro's
usage limits and price. model_credit_rates are cloned from the
live pro row at migration time (one-shot snapshot).

- mini:  5h=275000, 7d=2500000, CNY 5999 fen, USD 1000 cents
- nano:  5h=137500, 7d=1250000, CNY 2999 fen, USD  500 cents

Ships in the next deploy; the migration runner picks it up on
container start.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Migration 059 test — assert the two rows landed correctly

**Files:**
- Create: `internal/store/migrations_059_test.go`

**Interfaces:**
- Consumes: `openTestStore(t)` from `internal/store/extra_usage_db_test.go:13`, which skips if `TEST_DATABASE_URL` is unset and otherwise applies all migrations up to and including 059.
- Produces: two test functions that assert the exact fields for `mini` and `nano` match spec values, and that `model_credit_rates` matches Pro's map key-for-key.

- [ ] **Step 1: Write the failing test file**

Create `internal/store/migrations_059_test.go` with this content:

```go
package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// migration059Plans holds the exact scalar fields each new plan must carry
// after migration 059 runs. credit_rules are asserted separately since they
// are jsonb.
var migration059Plans = map[string]struct {
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
	"mini": {
		Name: "Mini", DisplayName: "Mini",
		Description:   "Half of Pro's usage limits",
		TierLevel:     50,
		PriceCNYFen:   5999,
		PriceUSDCents: 1000,
		PeriodMonths:  1,
		Credit5h:      275000,
		Credit7d:      2500000,
	},
	"nano": {
		Name: "Nano", DisplayName: "Nano",
		Description:   "Quarter of Pro's usage limits",
		TierLevel:     25,
		PriceCNYFen:   2999,
		PriceUSDCents: 500,
		PeriodMonths:  1,
		Credit5h:      137500,
		Credit7d:      1250000,
	},
}

// TestMigration059_PlanRowsPresent asserts the two new plan rows exist with
// the expected scalar fields and credit_rules windows.
func TestMigration059_PlanRowsPresent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration059Plans {
		var (
			name, displayName, description string
			tierLevel, priceCNYFen, priceUSDCents, periodMonths int64
			creditRulesJSON []byte
		)
		err := st.pool.QueryRow(ctx, `
			SELECT name, display_name, description, tier_level,
			       price_cny_fen, price_usd_cents, period_months,
			       credit_rules
			FROM plans WHERE slug = $1`, slug).
			Scan(&name, &displayName, &description, &tierLevel,
				&priceCNYFen, &priceUSDCents, &periodMonths, &creditRulesJSON)
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

// TestMigration059_ModelRatesClonedFromPro asserts model_credit_rates on
// mini and nano exactly match pro's map at migration time. This locks in
// the "clone from pro" contract stated in the migration's own comment.
func TestMigration059_ModelRatesClonedFromPro(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var proRates []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT model_credit_rates FROM plans WHERE slug = 'pro'`).Scan(&proRates); err != nil {
		t.Fatalf("read pro rates: %v", err)
	}

	var proMap map[string]any
	if err := json.Unmarshal(proRates, &proMap); err != nil {
		t.Fatalf("unmarshal pro rates: %v", err)
	}

	for _, slug := range []string{"mini", "nano"} {
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
	}
}
```

- [ ] **Step 2: Run the test to verify it passes when a test DB is available**

Run: `cd /root/coding/modelserver && TEST_DATABASE_URL="$TEST_DATABASE_URL" go test ./internal/store/ -run TestMigration059 -v`

Expected: If `TEST_DATABASE_URL` is set — both `TestMigration059_PlanRowsPresent` and `TestMigration059_ModelRatesClonedFromPro` PASS. If unset — both SKIP with the standard message (`set TEST_DATABASE_URL to run ...`); that is a green result, not a failure.

- [ ] **Step 3: Run the rest of the store tests to confirm nothing else regressed**

Run: `cd /root/coding/modelserver && go test ./internal/store/ -run TestMigration -v`

Expected: All `TestMigration*` tests either PASS or SKIP uniformly. In particular, `TestMigration049_USDPricesBackfilled` must still PASS — it does not know about `mini`/`nano` and only asserts the slugs it was written for, so adding two extra plan rows does not affect it.

- [ ] **Step 4: Commit**

```bash
cd /root/coding/modelserver
git add internal/store/migrations_059_test.go
git commit -m "test(store): assert migration 059 inserts mini and nano correctly

Two tests: one covers the scalar plan fields and credit_rules windows,
the other locks in the 'clone model_credit_rates from pro at migration
time' contract by asserting deep equality with pro's map.

Both tests skip when TEST_DATABASE_URL is unset, matching the pattern
used by migrations_049_test.go and siblings.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: Full-repo test sweep + push branch

**Files:** none new. This task exists so a reviewer can gate on "all tests pass together" separately from any individual task above.

**Interfaces:** none.

- [ ] **Step 1: Build the whole tree**

Run: `cd /root/coding/modelserver && go build ./...`

Expected: exit 0, no output.

- [ ] **Step 2: Run the full test suite**

Run: `cd /root/coding/modelserver && go test ./...`

Expected: PASS. `store` migration tests should either PASS (if `TEST_DATABASE_URL` is set in this environment) or SKIP; both outcomes are acceptable.

- [ ] **Step 3: Confirm the migration file lints against the numbering convention**

Run: `cd /root/coding/modelserver && ls internal/store/migrations/ | sort | tail -5`

Expected output ends with `059_add_mini_nano_plans.sql` in the correct position after `057_plan_client_credit_rates.sql`.

- [ ] **Step 4: Verify branch state**

Run: `cd /root/coding/modelserver && git log --oneline main..HEAD`

Expected: four new commits on top of `spec/add-mini-nano-plans` (the spec commit + Tasks 1-4 commits, in some order that ends with Task 4 or 5's HEAD). Exact order does not matter as long as all four are present.

- [ ] **Step 5: Push the branch**

```bash
cd /root/coding/modelserver
git push -u origin spec/add-mini-nano-plans
```

Expected: push succeeds. This is the last action before opening a PR — the actual PR creation is deferred to a follow-up because the user has not yet asked for it.

---

## Deploy-Time Verification (post-merge, not a task)

After this branch merges to `main` and the next deploy runs, verify manually:

1. **Migration applied:**
   ```sql
   SELECT slug, name, price_cny_fen, price_usd_cents, tier_level
     FROM plans WHERE slug IN ('mini','nano') ORDER BY tier_level;
   ```
   Expected two rows matching the spec table.

2. **API surface:** `curl -H 'Authorization: Bearer <admin>' $HOST/api/plans | jq '[.[] | select(.slug=="mini" or .slug=="nano")]'` — both plans present with expected credit_rules.

3. **Dashboard:** open `/subscriptions` — Mini and Nano cards render alongside Pro / Max\*.

4. **OAuth profile:** subscribe a test project to Mini or Nano, then `curl` `/api/oauth/profile` with that project's OAuth token — response includes `"project_type": "pro"`.

5. **End-to-end purchase:** place a Mini order via wechat/alipay (CNY) and stripe (USD) channels; confirm delivery flips the subscription to `active` and the credit windows populate at 275000 / 2500000.
