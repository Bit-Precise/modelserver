# CNY Price Bump 7/6× + New max_140x/160x/180x/220x Plans

**Date:** 2026-07-06
**Status:** Approved (brainstorming)

## Goal

Two independent-but-batched pricing changes shipped in the same deploy:

1. **CNY price bump.** Raise every paid plan's CNY price by 7/6, rounded to
   the nearest fen. USD prices, extra-usage credit price, and per-model
   credit rates are untouched.
2. **Four new max_Nx tiers.** Add `max_140x`, `max_160x`, `max_180x`, and
   `max_220x`, filling gaps between existing 120x/200x/240x. Prices are
   set directly at post-bump values (the new tiers do not need to pass
   through the 7/6 multiplier — they are born at the target price).

Direct precedent for the bump: migration 044 (`044_bump_plan_prices_1_2x.sql`),
which did the same shape of bump at 1.2× when `price_per_period` was the CNY
column name. Direct precedent for the new tiers: 037/040/041 (each
introduced one max_Nx row); the pattern is well-worn. The new tiers'
`model_credit_rates` and `client_model_credit_rates` clone from `pro`
(same pattern as migration 059), not the older 037/041 style of hardcoding
the rate map.

## Non-Goals

- **USD (`price_usd_cents`) is not bumped.** USD tiers price in whole dollars
  for aesthetic reasons (\$20, \$40, ...); ×7/6 produces fractional-dollar
  amounts. Operators who want the USD side to move ship a separate migration.
- **`extra_usage.credit_price_cny_fen` (config.yml runtime setting) is not
  bumped.** It lives outside the database. If operators want it to move in
  step, they update config / env at deploy time — this migration does not
  touch it.
- **No per-model credit-rate changes.** Rate maps (`model_credit_rates`,
  `client_model_credit_rates`) are unaffected.
- **No refund or top-up for existing subscriptions.** Orders snapshot
  `unit_price` and `amount` at checkout time (see `orders` table in
  `001_init.sql`); active subscriptions keep their originally-paid price
  until they expire and the next purchase pays the new price.

## Migration Ordering

- **060** runs first: bumps every existing non-free CNY price by 7/6.
- **061** runs second: inserts the four new tiers at final post-bump prices.

The order matters. If 061 ran first, the new tiers would be inserted at
their pre-bump equivalents and then 060 would multiply them; making the
new tiers "born final" avoids that indirection.

## Implementation

### 1. Migration `060_bump_plan_prices_cny_7_6x.sql`

Single up-only migration. One UPDATE. Comment follows 044's structure so
future readers see them as a matched pair.

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

### 2. Expected new CNY prices (built-in slugs)

All values are `ROUND(pre_060_value * 7.0 / 6.0)`. The pre-060 values are
what 044 (×1.2) + 049 (rename only, no numeric change) + any later plan
inserts (058 sonnet-5 catalog is model-only, 059 mini/nano at 5999/2999)
leave in the row.

| slug | pre-060 fen | ¥ before | post-060 fen | ¥ after |
|---|---:|---:|---:|---:|
| free | 0 | — | 0 | — |
| pro | 11999 | 119.99 | 13999 | 139.99 |
| mini | 5999 | 59.99 | 6999 | 69.99 |
| nano | 2999 | 29.99 | 3499 | 34.99 |
| max_2x | 23999 | 239.99 | 27999 | 279.99 |
| max_5x | 59999 | 599.99 | 69999 | 699.99 |
| max_20x | 119999 | 1199.99 | 139999 | 1399.99 |
| max_40x | 239999 | 2399.99 | 279999 | 2799.99 |
| max_60x | 359999 | 3599.99 | 419999 | 4199.99 |
| max_80x | 479999 | 4799.99 | 559999 | 5599.99 |
| max_100x | 599999 | 5999.99 | 699999 | 6999.99 |
| max_120x | 719999 | 7199.99 | 839999 | 8399.99 |
| max_200x | 1199999 | 11999.99 | 1399999 | 13999.99 |
| max_240x | 1439999 | 14399.99 | 1679999 | 16799.99 |

Custom (operator-created) plans go through the same `ROUND(x * 7/6)`
formula. Their exact new values depend on their current values, which the
migration reads from the row it updates.

### 3. Migration `061_add_max_140x_160x_180x_220x_plans.sql`

Single up-only migration. Four `INSERT ... SELECT` statements, each cloning
`model_credit_rates` and `client_model_credit_rates` from the current `pro`
row (same pattern established by 059). Prices are the post-060 target values
directly; `credit_rules` scale linearly at the per-multiplier rate anchored
by `max_20x` and above (5h=550,000 and 7d=4,166,665 per unit).

```sql
-- 061_add_max_140x_160x_180x_220x_plans.sql
--
-- Introduce four new Max tiers filling the gaps in the existing 20x /
-- 40x / 60x / 80x / 100x / 120x / 200x / 240x ladder:
--   max_140x, max_160x, max_180x, max_220x
--
-- All four use the same conventions established by prior max_Nx additions:
--   - tier_level  = N * 100        (max_140x = 14000, etc.)
--   - price_cny_fen  = N * 7000 - 1  (xxxx99 rounding, post-060 anchor)
--   - price_usd_cents = N * 10000   (N * $10, per migration 049's anchor)
--   - 5h credits  = N * 550000     (per-unit rate from max_20x onward)
--   - 7d credits  = N * 4166665
--   - model_credit_rates      = cloned from pro at migration time
--   - client_model_credit_rates = cloned from pro at migration time
--
-- Migration 049 requires every new plan to populate BOTH price_cny_fen
-- and price_usd_cents directly, so both are set explicitly here.
--
-- Idempotent via ON CONFLICT (slug) DO NOTHING, matching 001_init.sql
-- and 059's convention.
--
-- These rows are born at post-060 prices. Migration 060 bumps existing
-- rows 7/6× and runs first; 061 then inserts new rows already at the
-- final anchor.

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

New tier summary (all values final, post-060 semantics):

| slug | tier_level | credit_rules 5h | credit_rules 7d | price_cny_fen | ¥ | price_usd_cents | \$ |
|---|---:|---:|---:|---:|---:|---:|---:|
| max_140x | 14000 | 77,000,000 | 583,333,100 | 979999 | 9799.99 | 140000 | 1400 |
| max_160x | 16000 | 88,000,000 | 666,666,400 | 1119999 | 11199.99 | 160000 | 1600 |
| max_180x | 18000 | 99,000,000 | 749,999,700 | 1259999 | 12599.99 | 180000 | 1800 |
| max_220x | 22000 | 121,000,000 | 916,666,300 | 1539999 | 15399.99 | 220000 | 2200 |

The `pro`-absent no-op edge case is the same as 059: if `pro` is missing
when 061 runs, all four SELECTs return no rows and the INSERTs are no-ops.
Every deployment we ship seeds `pro` from `001_init.sql`, so this is fine
in practice.

### 4. Update to 059 test

`TestMigration059_PlanRowsPresent` in `internal/store/migrations_059_test.go`
currently asserts:

```go
"mini": { PriceCNYFen: 5999, ... }
"nano": { PriceCNYFen: 2999, ... }
```

Migrations run to head before this test executes, so after 060 lands the
DB will show mini=6999 and nano=3499. The test must be updated in the
**same commit** as the migration (else the test fails).

Change `migration059Plans` to the post-060 values, and add a one-line
comment on those two entries:

```go
"mini": {
    // PriceCNYFen was 5999 at migration 059; migration 060 bumped
    // it 7/6× to 6999. Tests run all migrations to head.
    Name: "Mini", DisplayName: "Mini",
    Description:   "Half of Pro's usage limits",
    TierLevel:     50,
    PriceCNYFen:   6999,
    PriceUSDCents: 1000,
    ...
},
"nano": {
    // PriceCNYFen was 2999 at migration 059; migration 060 bumped
    // it 7/6× to 3499.
    ...
    PriceCNYFen:   3499,
    PriceUSDCents: 500,
    ...
},
```

### 5. New test file `internal/store/migrations_060_test.go`

Three tests, all gated by `openTestStore(t)` (skip when
`TEST_DATABASE_URL` is unset), following the 049/053 pattern.

**`TestMigration060_CNYBumpedForKnownSlugs`** — assert `price_cny_fen`
matches the post-060 column of the table above for every built-in slug.
Free must remain 0. Since migrations run to head before the test executes,
the observed values include 061's inserts too — so the map here covers
the FULL slug set after 060+061.

```go
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
    "max_140x": 979999,   // introduced by 061 at post-bump price
    "max_160x": 1119999,  // introduced by 061 at post-bump price
    "max_180x": 1259999,  // introduced by 061 at post-bump price
    "max_200x": 1399999,
    "max_220x": 1539999,  // introduced by 061 at post-bump price
    "max_240x": 1679999,
}
```

**`TestMigration060_USDUnchanged`** — assert `price_usd_cents` matches
migration 049's backfill values verbatim for every slug 049 populated,
plus mini=1000 / nano=500 from 059. Locks in the "USD not touched by
060" contract. Does NOT assert the new 061 slugs' USD (those are
covered in the 061 test).

**`TestMigration060_FreeUnchanged`** — an explicit single-row assertion
that `price_cny_fen = 0` on `slug='free'`. Overlaps with the map above,
but exists as a standalone contract in case a future change removes
`free` from the general table.

### 6. New test file `internal/store/migrations_061_test.go`

Two tests, same skip/harness conventions as 059.

**`TestMigration061_NewMaxPlansPresent`** — for each of the four new
slugs, assert every scalar field (name, display_name, description,
tier_level, price_cny_fen, price_usd_cents, period_months, is_active)
plus the two credit_rules windows. Table-driven, one row per slug,
values verbatim from the spec table in §3.

**`TestMigration061_NewMaxPlansCloneRatesFromPro`** — for each of the
four new slugs, `reflect.DeepEqual` both `model_credit_rates` and
`client_model_credit_rates` against `pro`. Same shape as the 059 clone
test; both-NULL on `client_model_credit_rates` counts as equal.

### 7. What we are NOT changing

- **Frontend** (`SubscriptionPage.tsx`): renders whatever `/api/plans`
  returns; new prices appear automatically.
- **Order pipeline** (`handleCreateOrder`, `DeliverOrder`,
  webhook handlers): reads `price_cny_fen` from the plan row at
  checkout; new price takes effect on next purchase.
- **Existing orders and active subscriptions**: `orders.unit_price` and
  `orders.amount` are snapshotted; not modified.
- **Rate maps** (`model_credit_rates`, `client_model_credit_rates`):
  untouched.
- **`price_usd_cents`**: untouched.
- **`extra_usage.credit_price_cny_fen`** (config.yml runtime setting):
  untouched. Operators who want it to move update config at deploy time.
- **Migration test suites for 044, 049, 053, 058**: assert their own
  invariants at their own point in the timeline; none of them assert
  `price_cny_fen` values that this migration would perturb (049's
  `migration049USDPrices` is USD-only). No changes needed.
- **Go constants** (`internal/types/policy.go`): the `PlanMax*` constant
  block does not need `PlanMax140x` / `PlanMax160x` / `PlanMax180x` /
  `PlanMax220x` for correctness — no runtime code switches on these
  constants (they exist for grep symmetry). We ADD them anyway to keep
  the naming grid complete and to keep `Subscription.PlanName`'s enum
  comment accurate. Two-line addition, one comment update. No behavior
  change.
- **OAuth `mapPlanToProjectType`**: the existing `strings.HasPrefix(
  planName, "max_")` branch already maps every `max_*` slug to
  `"max"`. The four new slugs match that prefix, so no code change is
  required.

## Compatibility & Rollback

- **Purely additive.** No column added, no constraint changed, no row
  deleted. One UPDATE that touches every non-free `plans` row.
- **Rollback.** A reverse migration `UPDATE plans SET price_cny_fen =
  ROUND(price_cny_fen * 6.0 / 7.0) WHERE slug <> 'free'` would restore
  prices to within 1 fen of pre-060 values — the ROUND on both sides
  loses precision, so it is approximate, not exact. For an exact
  rollback, pre-060 values would need to be snapshotted to a side table
  first; that is out of scope for this spec (not currently required, no
  precedent in 044).
- **Currency lock.** Existing behavior. A project with an active CNY
  subscription can only re-purchase CNY plans; the currency-locking
  logic in `handleCreateOrder` reads `activeSub.Currency`, not any
  plan-level allowlist, so bumping CNY prices does not affect the lock.

## Success Criteria

1. `SELECT slug, price_cny_fen FROM plans WHERE slug <> 'free';` — every
   pre-existing row matches `ROUND(previous_value * 7.0 / 6.0)`; every
   new row (max_140x/160x/180x/220x) matches the spec table in §3.
2. `SELECT price_cny_fen FROM plans WHERE slug = 'free';` — 0.
3. `SELECT slug, price_usd_cents FROM plans;` — every pre-060 slug's
   USD is unchanged; the four new slugs' USD is N × 10000 cents.
4. `SELECT COUNT(*) FROM plans WHERE slug IN
   ('max_140x','max_160x','max_180x','max_220x');` returns 4.
5. Dashboard `/subscriptions` page renders each card with the new CNY
   price (¥139.99 for Pro, etc.) and includes the four new Max tiers.
6. New wechat/alipay orders quote the new price. Stripe orders quote
   the (unchanged) USD price.
7. Existing orders' `amount` and existing subscriptions' entitlement
   are unchanged.
8. `go test ./...` stays green, with:
   - Updated `TestMigration059_PlanRowsPresent` (mini=6999, nano=3499).
   - New `TestMigration060_*` and `TestMigration061_*` tests PASS
     when `TEST_DATABASE_URL` is set, otherwise SKIP.
   - `TestMapPlanToProjectType` still passes — the four new slugs
     match the existing `strings.HasPrefix("max_")` branch.
