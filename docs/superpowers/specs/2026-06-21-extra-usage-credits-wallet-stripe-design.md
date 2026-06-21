# Extra-Usage Credits Wallet + Stripe Topup Path

## Overview

Replace the extra-usage subsystem's fen-denominated balance with a
**credits-denominated wallet**, and add a Stripe payment path so users
can fund the wallet with USD as well as CNY (wechat/alipay).

A single wallet per project holds credits; channel-specific unit prices
determine how much each currency-denominated payment buys. The
`1 USD = 6 CNY` exchange rate is encoded as a config knob applied only
at the moment of USD topup — credits are stable after that, and any
future rate change affects only subsequent payments.

Existing fen-denominated state (`extra_usage_settings.balance_fen`,
`extra_usage_transactions.amount_fen`, `requests.extra_usage_cost_fen`)
is migrated one-shot to credits using the current `credit_price_fen`
at deploy time.

## Confirmed Decisions

| Topic | Decision |
|---|---|
| Unit of account | **credits** (BIGINT), the same unit `computeExtraUsageCostFen` already derives from `tokens × model.DefaultCreditRate` |
| Wallet shape | **Single** `balance_credits` column. Channel is provenance in the ledger, not a wallet attribute |
| CNY topup pricing | `credits = amount_fen × 1_000_000 / credit_price_fen` (existing semantics) |
| USD topup pricing | `credits = amount_cents × USDToCNYRate × 1_000_000 / credit_price_fen` (USDToCNYRate = 6.0 by default) — equivalently, $1 buys 6× more credits than ¥1 |
| Rate location | `MODELSERVER_EXTRA_USAGE_USD_TO_CNY_RATE` env / `extra_usage.usd_to_cny_rate` yaml, default `6.0`. Read at topup time only |
| Deduction | Unchanged conceptually: compute cost in credits, `balance_credits -= cost`. No spillover, no priority order (single wallet) |
| Subscription ↔ wallet | No coupling. Any project on any plan can topup via any channel. UI naturally lays out "微信/支付宝 → CNY 单价 → credits" vs "Stripe → USD 单价 → credits" |
| Refund (Stripe) | MVP: reverse the original topup's credits (ledger `refund` row, `amount_credits = -orig`). Balance may go negative; extra-usage guard rejects until topped up or admin-adjusted |
| Refund (wechat/alipay) | Same code path. Refunds via these channels are rare in practice |
| Disputes / chargebacks | Same as refund (B1 in brainstorm). Account state may go negative; ops can `admin_adjust` to recover |
| Rounding (topup credits) | `floor` — user gets whole credits; sub-credit fraction goes to platform (≤ 1 credit) |
| Rounding (deduction cost) | `ceil` (existing) — platform doesn't undercharge |
| Migration of historical fen | One-shot at deploy: `balance_credits = balance_fen × 1_000_000 / credit_price_fen`. Same conversion for `extra_usage_transactions.amount_fen` and `requests.extra_usage_cost_fen`. fen columns dropped |
| Dashboard primary unit | credits balance; with informational "≈ ¥X.XX (at current unit price)" alongside |
| Existing schema column rename | `extra_usage_settings.balance_fen` → `balance_credits`; `extra_usage_transactions.amount_fen` → `amount_credits`; `requests.extra_usage_cost_fen` → `extra_usage_cost_credits`; `orders.extra_usage_amount_fen` → `extra_usage_amount_credits` |

## §1 — Schema Changes (single migration)

```sql
-- 1. settings: rename + change semantics
ALTER TABLE extra_usage_settings
    RENAME COLUMN balance_fen TO balance_credits;

-- 2. ledger: rename
ALTER TABLE extra_usage_transactions
    RENAME COLUMN amount_fen TO amount_credits;
ALTER TABLE extra_usage_transactions
    RENAME COLUMN balance_after_fen TO balance_after_credits;

-- 3. requests: rename
ALTER TABLE requests
    RENAME COLUMN extra_usage_cost_fen TO extra_usage_cost_credits;

-- 4. orders: rename
ALTER TABLE orders
    RENAME COLUMN extra_usage_amount_fen TO extra_usage_amount_credits;

-- 5. One-shot data conversion. Uses the credit_price_fen value pinned
--    into the migration as a literal (operators must NOT change
--    credit_price_fen between writing the migration and applying it;
--    the runner aborts if the value differs from a checkpoint table).
--    See §6 "Migration safety" for the safeguard.
UPDATE extra_usage_settings
   SET balance_credits = (balance_credits * 1000000) / <CREDIT_PRICE_FEN>;
UPDATE extra_usage_transactions
   SET amount_credits = (amount_credits * 1000000) / <CREDIT_PRICE_FEN>,
       balance_after_credits = (balance_after_credits * 1000000) / <CREDIT_PRICE_FEN>;
UPDATE requests
   SET extra_usage_cost_credits = (extra_usage_cost_credits * 1000000) / <CREDIT_PRICE_FEN>
   WHERE extra_usage_cost_credits > 0;
UPDATE orders
   SET extra_usage_amount_credits = (extra_usage_amount_credits * 1000000) / <CREDIT_PRICE_FEN>
   WHERE extra_usage_amount_credits > 0;
```

`<CREDIT_PRICE_FEN>` is the deployment's currently-configured value at
the moment the migration runs. Captured from runtime config (or
injected via the same `SET LOCAL` GUC mechanism used by
`002_tenants.sql` in payserver). Recorded in the migration's audit
output so the conversion can be reproduced later.

The same migration file also creates:

```sql
-- §6 audit table: records the conversion factor used so any later
-- audit / re-derivation knows the exact divisor applied.
CREATE TABLE IF NOT EXISTS extra_usage_credit_migration_audit (
    id               SERIAL PRIMARY KEY,
    credit_price_fen BIGINT NOT NULL,
    applied_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- §4 refund idempotency: a single order can produce at most one
-- ledger row of each non-topup type. Topups already have
-- uniq_eut_topup_order (migration 017); add the symmetrical guard
-- for refunds.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_eut_refund_order
    ON extra_usage_transactions (order_id)
    WHERE type = 'refund' AND order_id IS NOT NULL;
```

Column NOT NULL / CHECK constraints carry over unchanged. The
column-rename is forward-only — rollback requires running a reverse
conversion migration, not just renaming columns back. The reverse
migration is intentionally not provided; if a rollback is needed,
ops accept that historical USD-sourced topups can't be cleanly
expressed in fen and must be re-keyed via admin adjustment.

## §2 — Configuration

`internal/config/config.go` — `ExtraUsageConfig` gains one field:

```go
type ExtraUsageConfig struct {
    Enabled            bool    ...
    CreditPriceFen     int64   ... // unchanged — CNY unit price
    USDToCNYRate       float64 `mapstructure:"usd_to_cny_rate" yaml:"usd_to_cny_rate"`
    MinTopupFen        int64   ...
    MaxTopupFen        int64   ...
    DailyTopupLimitFen int64   ...
}
```

- Default: `6.0`
- Env: `MODELSERVER_EXTRA_USAGE_USD_TO_CNY_RATE`
- Validation at `Load`: must be > 0 (a zero or negative rate would
  let a USD topup buy infinite or negative credits)
- `MinTopupFen` / `MaxTopupFen` / `DailyTopupLimitFen` are explicitly
  kept in **fen** because they are payment-side caps (the user pays
  in CNY or USD; the cap is on the *amount paid*, not on the credits
  received). USD payment caps are derived: `MinTopupCents = MinTopupFen
  × 100 / (USDToCNYRate × 100)` rounded sensibly. See §3 for the path.

## §3 — Topup Routing

The existing `POST /api/v1/projects/{id}/extra-usage/topup` handler is
extended to accept an explicit `channel` and corresponding
denominated amount. The wire shape:

```json
{
  "channel": "wechat" | "alipay" | "stripe",
  "amount_fen": 500       // present when channel = wechat or alipay
  "amount_cents": 500     // present when channel = stripe
}
```

(Exactly one of `amount_fen` / `amount_cents` must be set per request.
Submitting both, neither, or the wrong field for the channel returns
400.)

Server side:

```go
switch channel {
case "wechat", "alipay":
    if amount_fen < cfg.MinTopupFen { reject 400 }
    if amount_fen > cfg.MaxTopupFen { reject 400 }
    credits = (amount_fen * 1_000_000) / cfg.CreditPriceFen
    currency = "CNY"
    payment_amount = amount_fen        // in fen, passed to payserver

case "stripe":
    // Per-cent caps derived from the per-fen caps + current rate.
    // Helper: cfg.minTopupCents() = ceil(MinTopupFen / USDToCNYRate)
    //         cfg.maxTopupCents() = floor(MaxTopupFen / USDToCNYRate)
    // Asymmetric rounding so the user-facing range stays within the
    // operator-set fen bounds (a $0.16 minimum would be 16 cents → 96
    // fen, below the ¥10 floor; ceiling to 17 cents ensures any
    // accepted payment is ≥ ¥10.02).
    if amount_cents < cfg.minTopupCents() { reject 400 }
    if amount_cents > cfg.maxTopupCents() { reject 400 }
    // 1 USD = USDToCNYRate CNY = USDToCNYRate * 100 fen
    // 1 cent = USDToCNYRate fen
    fen_equivalent := int64(float64(amount_cents) * cfg.USDToCNYRate)
    credits = (fen_equivalent * 1_000_000) / cfg.CreditPriceFen
    currency = "USD"
    payment_amount = amount_cents      // in cents, passed to payserver
}

order = CreateOrder{
    Amount:                     payment_amount,
    Currency:                   currency,
    Channel:                    channel,
    OrderType:                  "extra_usage_topup",
    ExtraUsageAmountCredits:    credits,   // <-- pre-computed at order time
}
```

Pre-computing `credits` at order creation time pins the conversion
rate to *this order*: any subsequent change to `CreditPriceFen` or
`USDToCNYRate` does NOT retroactively change how many credits the
user receives when this order finally delivers. The delivery handler
(`internal/admin/handle_delivery.go`) reads the value from the order
row, not from current config.

Daily-topup-limit check: the cap is **enforced in fen-equivalent**
for both channels. `SumDailyExtraUsageTopupFen` is renamed
`SumDailyExtraUsageTopupFenEquivalent` and converts each topup's
payment amount to fen at sum time:

```sql
SELECT COALESCE(SUM(
    CASE WHEN currency = 'CNY' THEN amount
         WHEN currency = 'USD' THEN (amount * <USDToCNYRate>)::bigint
         ELSE 0
    END), 0)
FROM orders
WHERE project_id = $1 AND order_type = 'extra_usage_topup'
  AND status IN ('paying','paid','delivered')
  AND created_at >= $2
```

The `<USDToCNYRate>` is passed as a parameter at call time so a future
config change takes effect immediately (vs. baking into the SQL).
This keeps `DailyTopupLimitFen` as the single per-day cap regardless
of which channel is used.

## §4 — Deduction (`settleExtraUsage`)

Unchanged conceptually. The settle code already computes credits as
an intermediate value (`credits` is the second return from
`computeExtraUsageCostFen`). The change is to:

1. Stop computing the fen cost. `computeExtraUsageCostFen` → renamed
   `computeExtraUsageCostCredits`, returns `(creditsInt64, err)`. The
   `cost × creditPriceFen / 1_000_000` step is removed.
2. `DeductExtraUsageReq.AmountFen` → `AmountCredits`.
3. SQL UPDATE on `extra_usage_settings`: `balance_credits` instead
   of `balance_fen`.
4. Ledger insert: `amount_credits` (negative) and `balance_after_credits`.

The negation must continue to happen in Go (the PR #52 fix); SQL
`-$2` would re-introduce SQLSTATE 42725.

Refunds use the existing `type = 'refund'` ledger row shape, with
`amount_credits = -(original_topup_credits)`. A new
`Store.RefundExtraUsageTopup(orderID string) error` method finds the
original topup row by `order_id`, computes the reverse credits, and
applies it inside one transaction. Idempotent: a partial unique index
on `extra_usage_transactions(order_id, type)` prevents a duplicate
refund row.

## §5 — Refund Wiring

Stripe webhook handler in payserver already routes
`charge.refunded` events back to modelserver. Modelserver currently
has no refund handler for extra-usage topups — needs to be added:

```
POST /api/v1/billing/webhook/refund   (signed via HMACAuthMiddleware,
                                       same as delivery)
body: { "order_id": "...", "amount": ..., "currency": ... }

handler: lookup order, branch on order_type:
  - subscription:    existing subscription-refund path (currently no-op)
  - extra_usage_topup: call Store.RefundExtraUsageTopup(orderID)
```

Wechat/alipay refunds are operator-initiated via the payserver admin
UI (per the recent payserver redesign — out of scope here, but the
same `RefundExtraUsageTopup` will be the modelserver-side entry point
when payserver's refund admin endpoint calls back).

Balance may go negative after refund. The extra-usage guard already
checks `BalanceFen <= 0 → reject` (renamed: `BalanceCredits <= 0`).
Recovery: user tops up again, or operator runs
`POST /api/v1/admin/extra-usage/projects/{id}/topup` (direct
admin_adjust path that bypasses payment provider).

## §6 — Migration Safety

The conversion factor `credit_price_fen` is part of runtime config,
not the schema. Migration must use the same value the production
server is configured with, or migrated balances will silently
misvalue.

Safeguard: the migration runner reads the value from one of two
sources, in order:

1. Environment variable `MODELSERVER_MIGRATION_017_CREDIT_PRICE_FEN`
   (set explicitly by the deploying operator to the value being baked
   in to the conversion). The runner refuses to start if this env is
   unset.
2. (Not auto-discovered from `extra_usage.credit_price_fen` — the
   config layer is plumbing the migration shouldn't depend on, and
   a stale value at deploy time would be invisible.)

After the conversion completes, the runner writes one row to a new
audit table:

```sql
CREATE TABLE IF NOT EXISTS extra_usage_credit_migration_audit (
    id              SERIAL PRIMARY KEY,
    credit_price_fen BIGINT NOT NULL,
    applied_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

If the migration is ever re-run (idempotency check: `SELECT COUNT(*)`
on this table), it aborts. This rules out double-conversion if a
deploy partially fails and is retried.

## §7 — Dashboard Display

The `/api/v1/projects/{id}/extra-usage` endpoint's response gains
new fields, while old fields are renamed:

```json
{
  "enabled": true,
  "balance_credits": 651675,            // primary unit, replaces balance_fen
  "monthly_limit_credits": 100000000,   // replaces monthly_limit_fen
  "monthly_spent_credits": 12345,
  "monthly_window_start": "2026-06-01T00:00:00+08:00",

  "credit_unit_prices": {
    "cny_fen_per_million": 5438,           // = CreditPriceFen
    "usd_cents_per_million": 906,          // = ceil(CreditPriceFen / USDToCNYRate)
    "usd_to_cny_rate": 6.0                 // for context / consistency check
  },

  "min_topup": {
    "cny_fen": 1000,    // ¥10
    "usd_cents": 167    // = ceil(MinTopupFen / USDToCNYRate)
  },
  "max_topup": { "cny_fen": 200000, "usd_cents": 3334 },
  "daily_topup_limit": { "cny_fen": 500000, "usd_cents": 8334 },

  "bypass_balance_check": false,
  "updated_at": "..."
}
```

Frontend computes display values as needed:
- "余额 651,675 credits ≈ ¥35.43" using `balance_credits × cny_fen_per_million / 1_000_000 / 100`
- Topup form shows both channel options with their unit prices baked
  into the help text

`MonthlySpentCredits` is computed identically to the old
`MonthlySpentFen` — sum the deduction ledger rows for the current
month — but in credits. The function `Store.GetMonthlyExtraSpendFen`
is renamed `GetMonthlyExtraSpendCredits`.

`GetExtraUsageSpendInWindow` (the "Period Paid" backend) likewise
returns credits. Dashboard renders as credits primary, with the
"≈ ¥X.XX" annotation derived from current `credit_unit_prices`.

## §8 — Caveats & Out-of-Scope

**Out of scope for this spec / V2 follow-ups:**

1. **Per-channel daily-topup caps in credits.** The existing
   `DailyTopupLimitFen` is per-currency. With dual channels and
   credits unification, a meaningful cross-channel cap requires
   conversion math. V1 keeps the cap as fen-equivalent: convert USD
   topups to fen at request time and sum against the same daily limit.
   Edge: a user could pay $X via Stripe and have it count against the
   "fen daily limit" via `amount_cents × USDToCNYRate`. Acceptable
   approximation for V1.

2. **Currency-display preference per user.** Some users may want
   their dashboard to show "≈ $X.XX" instead of "≈ ¥X.XX". V1
   shows CNY-equivalent as the secondary display universally;
   user-preference toggle is V2.

3. **Pro-rated subscription credit accrual.** Some platforms credit
   the wallet with each subscription period. Out of scope — this is
   strictly an extra-usage wallet.

4. **Multiple concurrent topup orders per project.** Existing daily
   cap implicitly bounds this. Not changing.

5. **Variable USDToCNYRate at runtime via DB row.** Env-var is
   sufficient for now. A future "promotional rate" feature
   (e.g., $1 = 6.5 CNY during Black Friday) would justify moving to
   a DB-backed config; defer until needed.

6. **Refund partial recovery from spent credits.** The B1 policy
   (full reversal, allow negative) is intentionally simple. Per-
   topup remaining-credit accounting is V2.

**Caveats:**

- `credit_price_fen` is the runtime divisor for every conversion. An
  operator who changes it changes the price of all future topups
  proportionally. Existing credit balances are unaffected (they're
  whole numbers, not derived). The dashboard's "≈ ¥X.XX" display
  WILL change for everyone after such a change — this is correct: a
  credit is a fixed amount of compute, and the CNY-equivalent of one
  credit just got cheaper.

- Negative balances are possible (post-refund, post-dispute). Guard
  middleware's `BalanceCredits <= 0` reject is the runtime safety
  net. Ops should monitor `extra_usage_settings` rows where
  `balance_credits < 0` for collection follow-up.

- The migration is one-way. Reverting requires re-deriving fen from
  credits using the SAME `credit_price_fen` snapshot. The audit row
  in §6 preserves that value forever.

## §9 — Test Plan

Unit tests:
- `computeExtraUsageCostCredits` — happy path, all-zero usage, missing
  rate, credit_price_fen = 0
- topup credits conversion — CNY and USD, edge case of exactly
  divisible values, rounding-down behavior
- `RefundExtraUsageTopup` — happy path, no-such-order, double-refund
  idempotency

Integration tests (DB-backed, gated on `TEST_DATABASE_URL`):
- Full topup → deduction → refund cycle in CNY
- Same in USD
- Mixed: CNY topup → partial deduction → USD topup → deduction → CNY
  refund → balance correctness
- Migration: seed pre-migration data with `balance_fen`, run migration,
  assert `balance_credits` value and ledger rows

End-to-end:
- Frontend topup form for both channels: pay → webhook → balance
  increment → balance reflects in dashboard

## §10 — Deployment Order

1. Deploy modelserver containing the schema migration but NOT the new
   Stripe topup wire path (CNY only initially). Migration runs;
   `extra_usage_credit_migration_audit` row written. Dashboard now
   shows credits primary with CNY-equivalent secondary. CNY topup
   continues to work unchanged in user-visible behavior (now credits-
   denominated under the hood).

2. After 24h stability, deploy update that enables the Stripe topup
   path. Frontend reveals the USD topup channel option in the form.

Staged so the schema/migration risk is decoupled from the
Stripe-integration risk.

## §11 — File / Module Changes (high-level)

```
internal/store/migrations/
  052_extra_usage_credits.sql      # NEW: §1 schema + data convert + §6 audit table + refund-idempotency index (single migration file — no reason to split)

internal/store/extra_usage.go       # rename helpers, AmountCredits everywhere
internal/store/orders.go            # ExtraUsageAmountCredits column
internal/store/requests.go          # extra_usage_cost_credits in CompleteRequest
internal/store/usage.go             # GetExtraUsageSpendInWindow returns credits

internal/types/extra_usage.go       # ExtraUsageSettings.BalanceCredits, etc.
internal/types/request.go           # ExtraUsageCostCredits field
internal/types/order.go             # ExtraUsageAmountCredits field

internal/proxy/executor.go          # settle*: AmountCredits, computeCredits
internal/proxy/extra_usage_guard_middleware.go
                                    # BalanceCredits compare

internal/config/config.go           # ExtraUsageConfig.USDToCNYRate

internal/admin/handle_extra_usage.go
                                    # topup channel routing + extra fields
internal/admin/handle_delivery.go   # use ExtraUsageAmountCredits
internal/admin/handle_requests.go   # extra_usage_cost_credits read

internal/billing/savings.go         # ComputeCostBreakdown takes credits
dashboard/...                       # display credits + unit-price table
```

## §12 — Implementation Plan

Will be authored in a separate document via the writing-plans skill
after this spec is approved. Anticipated phases:

1. **Schema migration + Go renames** (largest mechanical change; no
   user-visible behavior change)
2. **Stripe topup wire path** (handler routing, payserver integration
   already supports `channel=stripe`)
3. **Refund webhook handler** (new endpoint, lookup-and-reverse)
4. **Frontend changes** (topup form, dashboard display)
5. **Tests + observability** (metrics for `topup_credits_total{channel=}`,
   `deduction_credits_total{result=}`)
