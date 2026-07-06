# Add Mini and Nano Subscription Plans

**Date:** 2026-07-06
**Status:** Approved (brainstorming)

## Goal

Introduce two new paid subscription tiers positioned between `free` and `pro`:

- **Mini** — half the usage limits of Pro, at half the price.
- **Nano** — a quarter of Pro's usage limits, at a quarter of the price.

Both tiers reuse Pro's per-model credit rates verbatim — they differ from Pro
only in `credit_rules` (the credit budget per time window) and the two price
columns.

The change is delivered as a single up-only SQL migration plus two small Go
patches. It ships in the next deploy; the migration runner picks it up on
container start.

## Non-Goals

- No changes to the frontend. `dashboard/src/pages/subscriptions/SubscriptionPage.tsx`
  fetches the plan list from `GET /api/plans` and renders whatever comes back
  keyed by `slug`, so new rows appear automatically.
- No changes to the ordering / billing pipeline. `handleCreateOrder` looks up a
  plan by `plan_slug` from the database and does not gate on a hardcoded set.
- No changes to `model_credit_rates` for existing plans. Mini/Nano clone Pro's
  current jsonb value at migration time; subsequent per-model rate changes to
  Pro do **not** propagate to Mini/Nano automatically (this is a one-shot copy,
  not a foreign-key relationship). Operators who tune Pro's rates later must
  either accept drift or replay the same tuning against Mini/Nano.

## Plan Table

| Field | Pro (current) | Mini | Nano |
|---|---|---|---|
| `name` | `Pro` | `Mini` | `Nano` |
| `slug` | `pro` | `mini` | `nano` |
| `display_name` | `Pro` | `Mini` | `Nano` |
| `description` | `Same usage limits as Claude Pro` | `Half of Pro's usage limits` | `Quarter of Pro's usage limits` |
| `tier_level` | `100` | `50` | `25` |
| `price_cny_fen` | `11999` | `5999` | `2999` |
| `price_usd_cents` | `2000` | `1000` | `500` |
| `period_months` | `1` | `1` | `1` |
| `credit_rules[0]` (5h sliding) | `max_credits: 550000` | `275000` | `137500` |
| `credit_rules[1]` (7d sliding) | `max_credits: 5000000` | `2500000` | `1250000` |
| `model_credit_rates` | current pro value | copied from pro at migration time | copied from pro at migration time |

Pricing keeps Pro's `xxxx99` convention for CNY (5999 / 2999 fen) and rounds
cleanly to the nearest dollar for USD (1000 / 500 cents). Credit budgets are
exact halves and quarters of Pro's, rounded to integers.

`tier_level` gets `nano=25`, `mini=50` so both slot between `free=0` and
`pro=100`, evenly spaced.

## Implementation

### 1. Migration `059_add_mini_nano_plans.sql`

Single up-only migration. Inserts two rows into `plans`, cloning
`model_credit_rates` from the current `pro` row (avoids duplicating the ~12
per-model rate entries that migrations 044, 047, 053 have already mutated
into their present shape):

```sql
-- 059_add_mini_nano_plans.sql
--
-- Introduces two new paid tiers, Mini and Nano, positioned between Free and
-- Pro. Both reuse Pro's per-model credit rates verbatim — they differ from Pro
-- only in credit_rules (halved / quartered) and price (halved / quartered).
--
-- model_credit_rates are copied from the live pro row at migration time
-- (rather than hardcoded) because the pro rate map has been mutated by
-- migrations 044 (×1.2 prices — unrelated) and 047, 053 (×2 GPT rates), and
-- future tuning is easier if we do not fork a second source of truth here.
-- If pro's rates are later tuned, mini/nano do NOT auto-follow — operators
-- must reapply the tuning if they want parity.

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

`ON CONFLICT (slug) DO NOTHING` makes the migration idempotent for reruns
against databases where it already applied — matches the pattern used by the
seed inserts in `001_init.sql`.

**Edge case:** if the `pro` row is absent when this migration runs, the
`SELECT ... FROM plans WHERE slug = 'pro'` returns no rows and both inserts
become no-ops. Every deployment we ship has seeded `pro` from `001_init.sql`,
so this is fine in practice; the migration does not need to fabricate a Pro
row.

### 2. Go constants — `internal/types/policy.go`

Add two constants beside the existing plan-slug block, and extend the
`Subscription.PlanName` docstring's enum comment:

```go
const (
    PlanPro     = "pro"
    PlanMini    = "mini"   // new
    PlanNano    = "nano"   // new
    PlanMax5x   = "max_5x"
    // ... unchanged ...
)
```

```go
// Update comment on Subscription.PlanName:
PlanName  string    `json:"plan_name"` // "pro", "mini", "nano", "max_5x", ..., or custom
```

These constants are documentation aides — no runtime code currently switches
on `PlanPro` (nor will it need to for mini/nano). We add them for symmetry
and so callers grepping for `PlanMini` find something.

### 3. `mapPlanToProjectType` — `internal/proxy/me_handler.go`

The OAuth profile endpoint (`/api/oauth/profile`) reports `project_type` to
Claude Code clients. Currently only `"pro"` and the `"max_*"` family return
non-empty; anything else drops the field (which clients treat as
"unsubscribed"). Mini and Nano must not silently downgrade to free-tier
behavior — they are paid tiers with the same per-model rates as Pro.

Change:

```go
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

Also update the function's docstring paragraph about the plan set to include
`mini` and `nano`.

### 4. Tests — `internal/proxy/me_handler_test.go`

Extend the `TestMapPlanToProjectType` table:

```go
{"mini", "pro"},
{"nano", "pro"},
```

No new integration test is required. The existing `HandleOAuthProfile`
tests exercise the `pro` case, and mini/nano flow through the same code path.

## What We Are Not Changing

- **`internal/store/migrations_049_test.go`** — asserts the USD price
  backfill from migration 049 for the plan set that existed at that point
  (`{free, pro, max_2x, max_5x, ...}`). Mini and Nano are introduced afterward
  (migration 059) and set their own `price_usd_cents` directly in the INSERT,
  so they are outside 049's scope and this test needs no change.
- **`internal/admin/handle_plans.go`, `dashboard/src/pages/admin/PlansPage.tsx`** —
  admins can still create/edit any plan through the existing UI. No new
  admin capability is needed.
- **`internal/billing/*`** — pricing, currency locking, and order delivery
  read `plan_slug` from the DB and route accordingly; no allowlist to
  extend.
- **Frontend subscription page** — renders plans returned by the API; the
  two new cards appear automatically after the migration runs.

## Compatibility and Rollback

- **Forward compatibility:** purely additive. Existing subscriptions,
  orders, and rate policies are untouched. No column added, no constraint
  changed.
- **Currency locking (see `handleCreateOrder`):** existing behavior applies
  unchanged. A project with an active CNY subscription can only upgrade to
  another CNY-priced plan. Mini/Nano have both CNY and USD prices set, so
  they are purchasable in either currency.
- **Rollback:** delete the two rows manually:
  ```sql
  DELETE FROM plans WHERE slug IN ('mini','nano')
    AND NOT EXISTS (SELECT 1 FROM subscriptions s WHERE s.plan_id = plans.id);
  ```
  If any subscription has already been sold against them, decide per-order
  whether to refund + revoke before deleting.

## Success Criteria

1. After deploy, `SELECT slug, price_cny_fen, price_usd_cents FROM plans
   WHERE slug IN ('mini','nano')` returns both rows with the values in
   the table above.
2. `GET /api/plans` includes both.
3. The dashboard `/subscriptions` page renders Mini and Nano cards
   alongside Pro / Max\*.
4. An OAuth token bound to a project with an active `mini` or `nano`
   subscription receives `"project_type": "pro"` from
   `/api/oauth/profile`.
5. Purchasing a Mini or Nano subscription end-to-end (order creation →
   payment → delivery → active subscription) works via both wechat/alipay
   (CNY) and stripe (USD) channels.
6. `go test ./...` stays green, with the new `mini`/`nano` cases in
   `TestMapPlanToProjectType`.
