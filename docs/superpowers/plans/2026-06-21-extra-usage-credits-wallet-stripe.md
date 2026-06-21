# Extra-Usage Credits Wallet + Stripe Topup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the extra-usage subsystem's fen balance with a credits-denominated wallet; add Stripe as a third topup channel alongside wechat/alipay.

**Architecture:** Single `balance_credits` column per project, fed by per-channel unit prices (`credit_price_cny_fen` and `credit_price_usd_cents`, both independent integers). Stripe topups encode the credits-purchased at order-creation time onto the order row; the delivery webhook applies the pre-computed value, immune to later config changes. Refunds reverse the original topup's credits via a new webhook handler and may drive the balance negative (the guard middleware then rejects further requests until topped up or admin-adjusted). All existing fen state (settings, ledger, requests, orders) is migrated one-shot at deploy.

**Tech Stack:** Go 1.26 modelserver, PostgreSQL via pgx v5, payserver (already supports `channel=stripe`), React+Vite dashboard.

## Global Constraints

Copied verbatim from `docs/superpowers/specs/2026-06-21-extra-usage-credits-wallet-stripe-design.md`:

- Unit of account is **credits** (BIGINT). Costs are computed in credits via `tokens × model.DefaultCreditRate`, no fen conversion in the hot path.
- Channel → wallet mapping: `wechat`/`alipay` → CNY pricing; `stripe` → USD pricing.
- Topup-time conversion: `credits = amount_fen × 1_000_000 / credit_price_cny_fen` (CNY) or `credits = amount_cents × 1_000_000 / credit_price_usd_cents` (USD).
- Default unit prices: `credit_price_cny_fen=5438`, `credit_price_usd_cents=907` (implicit 1USD ≈ 6CNY, but no `USDToCNYRate` config exists).
- Naming convention: `_fen` singular (Chinese mass noun, matches existing); `_cents` plural (matches existing `PriceUSDCents` in `types/plan.go:15`).
- Rounding: topup credits = `floor`; deduction cost credits = `ceil`.
- Refund policy (B1): reverse original topup credits in full; balance may go negative; extra-usage guard rejects until rectified.
- Per-channel min/max stay in payment currency (`min_topup_cny_fen`, `min_topup_usd_cents`, etc.). Daily cap is currency-agnostic (`daily_topup_limit_credits`).
- Migration is one-shot, idempotent (audit table enforces single application), forward-only.

---

## File Structure

**New:**
- `internal/store/migrations/052_extra_usage_credits.sql` — schema rename + audit table + refund idempotency index
- `internal/store/extra_usage_data_migration.go` — Go-side post-schema data converter (reads env var, runs UPDATEs, writes audit row)
- `internal/admin/handle_refund.go` — Stripe refund webhook handler

**Modified:**
- `internal/store/store.go` — call data migrator after schema migrations
- `internal/types/extra_usage.go` — `BalanceCredits`, `AmountCredits`, `BalanceAfterCredits`
- `internal/types/request.go` — `ExtraUsageCostCredits`
- `internal/types/order.go` — `ExtraUsageAmountCredits`
- `internal/store/extra_usage.go` — `DeductExtraUsageReq.AmountCredits`, `TopUpExtraUsageReq.AmountCredits`, SQL column references, new `RefundExtraUsageTopup` method
- `internal/store/orders.go` — `SumDailyExtraUsageTopupCredits` (renamed + reads new column)
- `internal/store/requests.go` — `extra_usage_cost_credits` in `CompleteRequest`
- `internal/store/usage.go` — `GetMonthlyExtraSpendCredits` (renamed), `GetExtraUsageSpendInWindow` reads from ledger as credits (already in latest code)
- `internal/proxy/executor.go` — `computeExtraUsageCostCredits` returns credits directly, settle paths use `AmountCredits`, the 4 failure-path `Request` constructors propagate `ExtraUsageCostCredits`
- `internal/proxy/extra_usage_guard_middleware.go` — `BalanceCredits <= 0` reject, `GetMonthlyExtraSpendCredits` call site
- `internal/proxy/image_extra_usage_cost.go` — `computeImageExtraUsageCostCredits`
- `internal/config/config.go` — `ExtraUsageConfig` reshape; validation; defaults
- `internal/admin/handle_extra_usage.go` — topup handler routes on channel, request-payload schema, GET response fields
- `internal/admin/handle_delivery.go` — reads `ExtraUsageAmountCredits` from order
- `internal/admin/handle_requests.go` — pass credits to `ComputeCostBreakdown`
- `internal/admin/routes.go` — mount `POST /api/v1/billing/webhook/refund`
- `internal/billing/savings.go` — `ComputeCostBreakdown(extraUsageCredits int64, ...)` (signature change)
- `internal/billing/savings_test.go` — update fixtures
- `internal/metrics/metrics.go` — `IncExtraUsageTopup(channel, currency)` counter
- `dashboard/src/pages/extra-usage/*.tsx` (exact path TBD by implementer) — topup form + balance display

---

## Phase 1: Credits unit conversion (no-op to users — internal rename + computation change)

### Task 1: Migration 052 + Go data converter

**Files:**
- Create: `internal/store/migrations/052_extra_usage_credits.sql`
- Create: `internal/store/extra_usage_data_migration.go`
- Create: `internal/store/extra_usage_data_migration_test.go`
- Modify: `internal/store/store.go:61-130` — call data migrator after the schema migration loop

**Interfaces:**
- Consumes: existing schema (`extra_usage_settings.balance_fen`, etc.), `MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN` env var
- Produces:
  - Renamed columns: `balance_credits` / `amount_credits` / `balance_after_credits` / `extra_usage_cost_credits` / `extra_usage_amount_credits`
  - Table: `extra_usage_credit_migration_audit(id SERIAL PK, credit_price_cny_fen BIGINT NOT NULL, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`
  - Index: `uniq_eut_refund_order ON extra_usage_transactions(order_id) WHERE type='refund' AND order_id IS NOT NULL`
  - Function (Go): `func (s *Store) convertExtraUsageDataToCredits(ctx context.Context, divisor int64) error`

- [ ] **Step 1: Write the migration SQL file**

Create `internal/store/migrations/052_extra_usage_credits.sql`:

```sql
-- 052_extra_usage_credits.sql
--
-- Renames fen → credits throughout the extra-usage subsystem and adds the
-- supporting audit table + refund idempotency index. The actual data
-- conversion (multiply old fen × 1_000_000 / credit_price_cny_fen) is
-- performed by Store.convertExtraUsageDataToCredits AFTER this schema
-- migration commits — it needs a deploy-time env var that can't live in
-- a pure SQL file.

BEGIN;

ALTER TABLE extra_usage_settings
    RENAME COLUMN balance_fen TO balance_credits;
ALTER TABLE extra_usage_settings
    RENAME COLUMN monthly_limit_fen TO monthly_limit_credits;

ALTER TABLE extra_usage_transactions
    RENAME COLUMN amount_fen TO amount_credits;
ALTER TABLE extra_usage_transactions
    RENAME COLUMN balance_after_fen TO balance_after_credits;

ALTER TABLE requests
    RENAME COLUMN extra_usage_cost_fen TO extra_usage_cost_credits;

ALTER TABLE orders
    RENAME COLUMN extra_usage_amount_fen TO extra_usage_amount_credits;

CREATE TABLE IF NOT EXISTS extra_usage_credit_migration_audit (
    id                   SERIAL      PRIMARY KEY,
    credit_price_cny_fen BIGINT      NOT NULL,
    applied_at           TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Symmetrical to uniq_eut_topup_order (migration 017). Prevents a duplicate
-- Stripe refund webhook from double-reversing the same order's credits.
CREATE UNIQUE INDEX IF NOT EXISTS uniq_eut_refund_order
    ON extra_usage_transactions (order_id)
    WHERE type = 'refund' AND order_id IS NOT NULL;

COMMIT;
```

- [ ] **Step 2: Write the Go data converter**

Create `internal/store/extra_usage_data_migration.go`:

```go
package store

import (
	"context"
	"fmt"
	"os"
	"strconv"
)

// convertExtraUsageDataToCredits performs the one-shot fen → credits
// conversion of historical extra-usage state after migration 052 renames
// the columns. Idempotent: writes a row to extra_usage_credit_migration_audit
// on success; refuses to run if a row already exists.
//
// The conversion divisor is the deployment's existing credit_price_fen
// config value at the moment of deploy. It must be passed via env var
// MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN — the migration cannot
// silently read it from runtime config because a stale or changed value
// would be invisible at deploy time.
//
// All pre-migration topups were CNY (Stripe path didn't exist), so the
// CNY divisor is the unambiguous correct one for converting every
// affected row.
func (s *Store) convertExtraUsageDataToCredits(ctx context.Context) error {
	// Idempotency: skip if already applied.
	var existingCount int
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM extra_usage_credit_migration_audit`).Scan(&existingCount)
	if err != nil {
		return fmt.Errorf("check audit table: %w", err)
	}
	if existingCount > 0 {
		return nil
	}

	envName := "MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN"
	raw := os.Getenv(envName)
	if raw == "" {
		return fmt.Errorf("migration 052 data conversion requires env %s "+
			"(the deployment's existing credit_price_fen value)", envName)
	}
	divisor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("parse %s=%q: %w", envName, raw, err)
	}
	if divisor <= 0 {
		return fmt.Errorf("%s must be > 0, got %d", envName, divisor)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Convert in deterministic order; each statement is a single UPDATE.
	for _, stmt := range []string{
		`UPDATE extra_usage_settings
		    SET balance_credits = (balance_credits * 1000000) / $1`,
		`UPDATE extra_usage_settings
		    SET monthly_limit_credits = (monthly_limit_credits * 1000000) / $1
		  WHERE monthly_limit_credits > 0`,
		`UPDATE extra_usage_transactions
		    SET amount_credits = (amount_credits * 1000000) / $1,
		        balance_after_credits = (balance_after_credits * 1000000) / $1`,
		`UPDATE requests
		    SET extra_usage_cost_credits = (extra_usage_cost_credits * 1000000) / $1
		  WHERE extra_usage_cost_credits > 0`,
		`UPDATE orders
		    SET extra_usage_amount_credits = (extra_usage_amount_credits * 1000000) / $1
		  WHERE extra_usage_amount_credits > 0`,
	} {
		if _, err := tx.Exec(ctx, stmt, divisor); err != nil {
			return fmt.Errorf("convert: %w (stmt: %s)", err, stmt)
		}
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO extra_usage_credit_migration_audit (credit_price_cny_fen) VALUES ($1)`,
		divisor); err != nil {
		return fmt.Errorf("write audit row: %w", err)
	}

	return tx.Commit(ctx)
}
```

- [ ] **Step 3: Wire the data converter into Store.New**

Modify `internal/store/store.go`. After the `migrate()` call returns successfully, invoke the converter:

```go
// After:  if err := s.migrate(ctx); err != nil { return nil, err }
// Add:
if err := s.convertExtraUsageDataToCredits(ctx); err != nil {
    return nil, fmt.Errorf("extra-usage credits conversion: %w", err)
}
```

The exact line varies by current `Store.New` shape — locate the post-`migrate` continuation and insert before the function returns. Failing fast at startup is intentional: a half-migrated DB is more dangerous than a non-starting service.

- [ ] **Step 4: Write the migration test**

Create `internal/store/extra_usage_data_migration_test.go`:

```go
package store

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

// TestExtraUsageDataMigration_HappyPath walks the full convert flow on a
// fresh test DB. Requires TEST_DATABASE_URL (skips otherwise).
func TestExtraUsageDataMigration_HappyPath(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := New(dbURL, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	_, projectID := seedUserAndProject(t, st)
	ctx := context.Background()

	// Seed: pre-migration shape simulated by inserting rows AFTER the
	// columns have already been renamed (since migrations have run).
	// Use the NEW column names but assign values as if they were in fen.
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO extra_usage_settings (project_id, balance_credits)
		VALUES ($1, 2000) ON CONFLICT (project_id) DO UPDATE SET balance_credits = 2000`,
		projectID); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO extra_usage_transactions (project_id, type, amount_credits, balance_after_credits)
		VALUES ($1, 'topup', 2000, 2000)`, projectID); err != nil {
		t.Fatalf("seed topup tx: %v", err)
	}

	// Wipe any audit row from a prior run so the converter actually runs.
	if _, err := st.pool.Exec(ctx, `TRUNCATE extra_usage_credit_migration_audit`); err != nil {
		t.Fatalf("truncate audit: %v", err)
	}

	t.Setenv("MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN", "5438")
	if err := st.convertExtraUsageDataToCredits(ctx); err != nil {
		t.Fatalf("convert: %v", err)
	}

	// 2000 fen × 1_000_000 / 5438 = 367,782 (integer division)
	var got int64
	err = st.pool.QueryRow(ctx, `SELECT balance_credits FROM extra_usage_settings WHERE project_id = $1`, projectID).Scan(&got)
	if err != nil {
		t.Fatalf("read balance: %v", err)
	}
	if got != 367782 {
		t.Errorf("balance_credits = %d, want 367782", got)
	}

	// Audit row written.
	var auditDivisor int64
	if err := st.pool.QueryRow(ctx, `SELECT credit_price_cny_fen FROM extra_usage_credit_migration_audit ORDER BY id DESC LIMIT 1`).Scan(&auditDivisor); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if auditDivisor != 5438 {
		t.Errorf("audit divisor = %d, want 5438", auditDivisor)
	}

	// Idempotency: second call should no-op (no error, no further changes).
	if err := st.convertExtraUsageDataToCredits(ctx); err != nil {
		t.Fatalf("second convert: %v", err)
	}
	var auditCount int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM extra_usage_credit_migration_audit`).Scan(&auditCount); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit count = %d, want 1 (idempotent)", auditCount)
	}
}

// TestExtraUsageDataMigration_MissingEnvVar verifies the runner refuses to
// proceed when MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN is unset.
func TestExtraUsageDataMigration_MissingEnvVar(t *testing.T) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	st, err := New(dbURL, slog.Default())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if _, err := st.pool.Exec(context.Background(), `TRUNCATE extra_usage_credit_migration_audit`); err != nil {
		t.Fatalf("truncate audit: %v", err)
	}
	t.Setenv("MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN", "")

	err = st.convertExtraUsageDataToCredits(context.Background())
	if err == nil {
		t.Fatal("expected error when env unset")
	}
}
```

- [ ] **Step 5: Run the migration tests**

```
TEST_DATABASE_URL=postgres://... go test ./internal/store/ -run TestExtraUsageDataMigration -v
```

Expected: both PASS. Without `TEST_DATABASE_URL` the tests skip — that's fine for CI continuity.

- [ ] **Step 6: Run the full store package tests**

```
go test ./internal/store/ -count=1
```

Expected: all existing tests PASS (the schema rename doesn't affect any compile-time call site yet — call sites still reference old field names because Task 2 hasn't run; existing tests use struct field names which we haven't renamed yet either). If any FAIL, the migration broke something — investigate before continuing.

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/052_extra_usage_credits.sql \
        internal/store/extra_usage_data_migration.go \
        internal/store/extra_usage_data_migration_test.go \
        internal/store/store.go
git commit -m "feat(extra-usage): migration 052 — rename fen columns to credits + data converter

Schema rename (settings/transactions/requests/orders) + audit table +
refund idempotency index in 052_extra_usage_credits.sql. The fen →
credits data conversion is a separate Go-side step (read env var, run
UPDATEs, write audit row) because the divisor (deployment's current
credit_price_fen value) can't live in a pure SQL file. Idempotent via
the audit table.

Conversion divisor must be passed at deploy time via
MODELSERVER_MIGRATION_052_CREDIT_PRICE_CNY_FEN."
```

---

### Task 2: Type + signature renames; settle returns credits

**Files:**
- Modify: `internal/types/extra_usage.go` — `Settings.BalanceCredits`, `Settings.MonthlyLimitCredits`, `Transaction.AmountCredits`, `Transaction.BalanceAfterCredits`
- Modify: `internal/types/request.go` — `Request.ExtraUsageCostCredits`
- Modify: `internal/types/order.go` — `Order.ExtraUsageAmountCredits`
- Modify: `internal/store/extra_usage.go` — `DeductExtraUsageReq.AmountCredits`, `TopUpExtraUsageReq.AmountCredits`, all SQL column references, every Scan target, error messages, doc comments
- Modify: `internal/store/orders.go` — `SumDailyExtraUsageTopupCredits` (renamed function + column references)
- Modify: `internal/store/requests.go` — `extra_usage_cost_credits` column in `CompleteRequest`, Scan in `ListRequests` if it reads the column (it does NOT currently — out of scope)
- Modify: `internal/store/usage.go` — `GetMonthlyExtraSpendCredits` rename; `GetExtraUsageSpendInWindow` body already reads `extra_usage_transactions.amount_credits` after Task 1's rename, no change needed there
- Modify: `internal/proxy/executor.go` — rename `computeExtraUsageCostFen` → `computeExtraUsageCostCredits`, return `(int64, error)` (credits as integer); `settleExtraUsage` uses `AmountCredits`; the 4 failure-path `Request` constructors propagate `ExtraUsageCostCredits` (was `ExtraUsageCostFen`)
- Modify: `internal/proxy/extra_usage_guard_middleware.go` — `BalanceCredits <= 0`, `MonthlyLimitCredits`
- Modify: `internal/proxy/image_extra_usage_cost.go` — `computeImageExtraUsageCostCredits` rename + return credits
- Modify: `internal/proxy/extra_usage_guard_middleware_test.go` — fake store interface update (signature renames)
- Modify: any test files that construct `DeductExtraUsageReq` / `TopUpExtraUsageReq` / `ExtraUsageSettings` / etc.

**Interfaces:**
- Consumes: Task 1's renamed schema
- Produces:
  - `types.ExtraUsageSettings.BalanceCredits int64`
  - `types.ExtraUsageSettings.MonthlyLimitCredits int64`
  - `types.ExtraUsageTransaction.AmountCredits int64`
  - `types.ExtraUsageTransaction.BalanceAfterCredits int64`
  - `types.Request.ExtraUsageCostCredits int64`
  - `types.Order.ExtraUsageAmountCredits int64`
  - `store.DeductExtraUsageReq{ProjectID, AmountCredits, RequestID, Reason, Description, MonthWindowStart}`
  - `store.TopUpExtraUsageReq{ProjectID, AmountCredits, OrderID, Reason, Description}`
  - `store.Store.GetMonthlyExtraSpendCredits(projectID string, monthStart time.Time) (int64, error)`
  - `store.Store.SumDailyExtraUsageTopupCredits(projectID string, dayStart time.Time) (int64, error)`
  - `proxy.computeExtraUsageCostCredits(m *types.Model, u types.TokenUsage) (int64, error)` — note: the `creditPriceFen` parameter is **removed** (no longer needed; credits is the natural output)
  - `proxy.computeImageExtraUsageCostCredits(m *types.Model, u ImageTokenUsage) (int64, error)`

- [ ] **Step 1: Rename type fields**

`internal/types/extra_usage.go`:

```go
type ExtraUsageSettings struct {
	ProjectID            string    `json:"project_id"`
	Enabled              bool      `json:"enabled"`
	BalanceCredits       int64     `json:"balance_credits"`        // was BalanceFen
	MonthlyLimitCredits  int64     `json:"monthly_limit_credits"`  // was MonthlyLimitFen
	BypassBalanceCheck   bool      `json:"bypass_balance_check"`
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

type ExtraUsageTransaction struct {
	ID                   string    `json:"id"`
	ProjectID            string    `json:"project_id"`
	Type                 string    `json:"type"`
	AmountCredits        int64     `json:"amount_credits"`         // was AmountFen
	BalanceAfterCredits  int64     `json:"balance_after_credits"`  // was BalanceAfterFen
	RequestID            string    `json:"request_id,omitempty"`
	OrderID              string    `json:"order_id,omitempty"`
	Reason               string    `json:"reason,omitempty"`
	Description          string    `json:"description,omitempty"`
	CreatedAt            time.Time `json:"created_at"`
}
```

`internal/types/request.go` — replace the 3-field extra-usage attribution block:

```go
// Extra-usage attribution. See the comment in this file's prior commit
// (preserved verbatim with field rename).
IsExtraUsage          bool   `json:"is_extra_usage,omitempty"`
ExtraUsageCostCredits int64  `json:"extra_usage_cost_credits,omitempty"`  // was ExtraUsageCostFen
ExtraUsageReason      string `json:"extra_usage_reason,omitempty"`
```

`internal/types/order.go` — find `ExtraUsageAmountFen` field, rename:

```go
ExtraUsageAmountCredits int64 `json:"extra_usage_amount_credits"`  // was ExtraUsageAmountFen
```

- [ ] **Step 2: Rename store layer field references**

`internal/store/extra_usage.go`:

```go
type DeductExtraUsageReq struct {
	ProjectID        string
	AmountCredits    int64   // was AmountFen
	RequestID        string
	Reason           string
	Description      string
	MonthWindowStart time.Time
}

type TopUpExtraUsageReq struct {
	ProjectID     string
	AmountCredits int64   // was AmountFen
	OrderID       string
	Reason        string
	Description   string
}
```

Update every SQL statement in `extra_usage.go` to use the new column names. The key strings to replace:
- `balance_fen` → `balance_credits`
- `monthly_limit_fen` → `monthly_limit_credits`
- `amount_fen` → `amount_credits`
- `balance_after_fen` → `balance_after_credits`

Update the `Scan` argument lists to read into the renamed struct fields. Update parameter binding (e.g., `req.AmountFen` → `req.AmountCredits`).

The **PR #52 Go-side negation** at the deduction INSERT must be preserved:
```go
_, err = tx.Exec(ctx, `
    INSERT INTO extra_usage_transactions
      (project_id, type, amount_credits, balance_after_credits, request_id, reason, description)
    VALUES ($1, 'deduction', $2, $3, $4, $5, $6)`,
    req.ProjectID, -req.AmountCredits, newBalance,   // negate in Go, never SQL `-$2`
    nullString(req.RequestID), req.Reason, req.Description,
)
```

- [ ] **Step 3: Rename store layer functions**

`internal/store/extra_usage.go`:

```go
// GetMonthlyExtraSpendCredits — renamed from GetMonthlyExtraSpendFen.
// Same SQL body with column name swap.
func (s *Store) GetMonthlyExtraSpendCredits(projectID string, monthStart time.Time) (int64, error) {
	var spent int64
	err := s.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(-amount_credits), 0)::bigint
		FROM extra_usage_transactions
		WHERE project_id = $1
		  AND type = 'deduction'
		  AND created_at >= $2`,
		projectID, monthStart,
	).Scan(&spent)
	if err != nil {
		return 0, fmt.Errorf("sum monthly spend: %w", err)
	}
	return spent, nil
}
```

`internal/store/orders.go`:

```go
// SumDailyExtraUsageTopupCredits — renamed; reads the new column.
func (s *Store) SumDailyExtraUsageTopupCredits(projectID string, dayStart time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(extra_usage_amount_credits), 0)::bigint
		FROM orders
		WHERE project_id = $1
		  AND order_type = 'extra_usage_topup'
		  AND status IN ('paying','paid','delivered')
		  AND created_at >= $2`,
		projectID, dayStart,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("sum daily topup: %w", err)
	}
	return total, nil
}
```

`internal/store/requests.go` — `CompleteRequest` already lists `is_extra_usage = $19, extra_usage_cost_fen = $20`. Rename the column to `extra_usage_cost_credits = $20` and pass `r.ExtraUsageCostCredits` in the binding list.

`internal/store/usage.go`:

```go
// GetExtraUsageSpendInWindow already queries extra_usage_transactions
// (PR #54). After Task 1 the column is amount_credits, so update:
func (s *Store) GetExtraUsageSpendInWindow(projectID string, since, until time.Time) (int64, error) {
	var total int64
	err := s.pool.QueryRow(context.Background(), `
		SELECT COALESCE(SUM(-amount_credits), 0)::bigint
		FROM extra_usage_transactions
		WHERE project_id = $1
		  AND type = 'deduction'
		  AND created_at >= $2 AND created_at < $3`,
		projectID, since, until,
	).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("extra usage spend: %w", err)
	}
	return total, nil
}
```

- [ ] **Step 4: Rewrite the cost helpers in proxy**

`internal/proxy/executor.go` — replace `computeExtraUsageCostFen`:

```go
// computeExtraUsageCostCredits converts a TokenUsage to credits using
// the catalog's DefaultCreditRate. Extra-usage cost is always priced
// at official API rates (the plan-override credit rate is NOT used) —
// see spec §4.1. Returns (credits, err). Credits round UP so sub-credit
// fractions don't undercharge.
func computeExtraUsageCostCredits(m *types.Model, u types.TokenUsage) (int64, error) {
	if m == nil || m.DefaultCreditRate == nil {
		return 0, ErrMissingDefaultCreditRate
	}
	rate := types.ApplyLongContextCreditRate(*m.DefaultCreditRate, u.InputTokens+u.CacheCreationTokens+u.CacheReadTokens)
	credits := rate.InputRate*float64(u.InputTokens) +
		rate.OutputRate*float64(u.OutputTokens) +
		rate.CacheCreationRate*float64(u.CacheCreationTokens) +
		rate.CacheReadRate*float64(u.CacheReadTokens)
	if credits <= 0 {
		return 0, nil
	}
	return int64(math.Ceil(credits)), nil
}
```

Note: the `creditPriceFen` parameter is **removed**. Credits is the natural output of the catalog rate × tokens computation.

Same pattern for `internal/proxy/image_extra_usage_cost.go`:

```go
func computeImageExtraUsageCostCredits(m *types.Model, u ImageTokenUsage) (int64, error) {
    // ... existing body, return ceil(credits) instead of fen ...
}
```

- [ ] **Step 5: Update the two settle functions**

`internal/proxy/executor.go` — `settleExtraUsage`:

```go
func (e *Executor) settleExtraUsage(ctx context.Context, rc *RequestContext, usage types.TokenUsage) {
	if !rc.HasExtraUsageCtx {
		return
	}
	euCtx := rc.ExtraUsageCtx
	// IsExtraUsage / ExtraUsageReason stamped in Execute on guard approval.

	if usage.InputTokens+usage.OutputTokens+usage.CacheCreationTokens+usage.CacheReadTokens == 0 {
		e.logger.Warn("extra_usage_settle_no_usage",
			"project_id", rc.ProjectID, "request_id", rc.RequestID)
		metrics.IncExtraUsageDeduction("no_usage")
		return
	}

	costCredits, err := computeExtraUsageCostCredits(rc.ModelRef, usage)
	if err != nil {
		modelName := rc.Model
		if rc.ModelRef != nil {
			modelName = rc.ModelRef.Name
		}
		e.logger.Error("extra_usage_missing_default_rate",
			"project_id", rc.ProjectID, "model", modelName, "error", err)
		metrics.IncExtraUsageMissingRate(modelName)
		return
	}
	rc.ExtraUsageCostCredits = costCredits  // was rc.ExtraUsageCostFen = costFen

	newBal, err := e.store.DeductExtraUsage(store.DeductExtraUsageReq{
		ProjectID:        rc.ProjectID,
		AmountCredits:    costCredits,
		RequestID:        rc.RequestID,
		Reason:           euCtx.Reason,
		Description:      fmt.Sprintf("%s | model=%s", euCtx.Reason, rc.Model),
		MonthWindowStart: store.MonthWindowStart(),
	})
	// switch block stays as-is — sentinel errors and metric labels unchanged.
	// ...
}
```

Update `settleImageExtraUsage` identically — drop `costFen` intermediate, work in credits.

Update the 4 failure-path `Request{}` constructors in `executor.go` (the PR #54 fix sites at lines ~541, 609, 765, 904) to use `ExtraUsageCostCredits` instead of `ExtraUsageCostFen`.

Update the `RequestContext` struct in `executor.go` — rename `ExtraUsageCostFen` field → `ExtraUsageCostCredits`. The `IsExtraUsage`, `ExtraUsageReason` fields stay.

- [ ] **Step 6: Update guard middleware**

`internal/proxy/extra_usage_guard_middleware.go`:
- `settings.BalanceFen <= 0` → `settings.BalanceCredits <= 0`
- `settings.MonthlyLimitFen > 0` → `settings.MonthlyLimitCredits > 0`
- `st.GetMonthlyExtraSpendFen(...)` → `st.GetMonthlyExtraSpendCredits(...)`
- Update the interface `extraUsageStore` to match
- The "X-Extra-Usage-Balance-Fen" header in `writeExtraUsageRejected` becomes `X-Extra-Usage-Balance-Credits` — same semantic shift

- [ ] **Step 7: Build + run unit tests**

```
go build ./...
go test ./internal/types/ ./internal/store/ ./internal/proxy/ -count=1
```

Expected: clean build, all tests PASS. If any tests use `BalanceFen`, `AmountFen`, etc., update them to the new names — they were renamed in Step 1 so the struct literal compile-errors point you to every site.

- [ ] **Step 8: Commit**

```bash
git add internal/types/ internal/store/ internal/proxy/
git commit -m "refactor(extra-usage): rename fen → credits across types/store/proxy

Internal-only refactor. Field renames cascade from the schema rename
in 052:
  ExtraUsageSettings.BalanceFen           → BalanceCredits
  ExtraUsageSettings.MonthlyLimitFen      → MonthlyLimitCredits
  ExtraUsageTransaction.AmountFen         → AmountCredits
  ExtraUsageTransaction.BalanceAfterFen   → BalanceAfterCredits
  Request.ExtraUsageCostFen               → ExtraUsageCostCredits
  Order.ExtraUsageAmountFen               → ExtraUsageAmountCredits
  DeductExtraUsageReq.AmountFen           → AmountCredits
  TopUpExtraUsageReq.AmountFen            → AmountCredits
  Store.GetMonthlyExtraSpendFen           → GetMonthlyExtraSpendCredits
  Store.SumDailyExtraUsageTopupFen        → SumDailyExtraUsageTopupCredits
  proxy.computeExtraUsageCostFen          → computeExtraUsageCostCredits
                                          (returns credits int64 directly;
                                           creditPriceFen param removed)
  proxy.computeImageExtraUsageCostFen     → computeImageExtraUsageCostCredits

The Go-side negation in DeductExtraUsage (PR #52 fix) preserved. The
attribution-on-failure-path fix (PR #54 fix) preserved with renamed field."
```

---

### Task 3: Config reshape

**Files:**
- Modify: `internal/config/config.go` — `ExtraUsageConfig` rename + new fields + validation
- Modify: `internal/config/config_test.go` — new defaults + new validation cases
- Modify: every site in modelserver that reads `cfg.ExtraUsage.CreditPriceFen` (executor was updated in Task 2 to drop the param, but check)

**Interfaces:**
- Consumes: Task 2's settle function (now no longer reads `CreditPriceFen`)
- Produces:
  - `ExtraUsageConfig.CreditPriceCNYFen int64` (default 5438)
  - `ExtraUsageConfig.CreditPriceUSDCents int64` (default 907)
  - `ExtraUsageConfig.MinTopupCNYFen int64` (default 1000) — renamed from `MinTopupFen`
  - `ExtraUsageConfig.MaxTopupCNYFen int64` (default 200000) — renamed from `MaxTopupFen`
  - `ExtraUsageConfig.MinTopupUSDCents int64` (default 167)
  - `ExtraUsageConfig.MaxTopupUSDCents int64` (default 33333)
  - `ExtraUsageConfig.DailyTopupLimitCredits int64` (default ~919M, see calculation below)

- [ ] **Step 1: Reshape the config struct**

`internal/config/config.go`:

```go
type ExtraUsageConfig struct {
	Enabled                bool   `yaml:"enabled"                   mapstructure:"enabled"`

	// Per-channel unit prices. fen and cents are minor-unit integers
	// in each currency. Independent — no auto-derived rate between them.
	CreditPriceCNYFen      int64  `yaml:"credit_price_cny_fen"      mapstructure:"credit_price_cny_fen"`
	CreditPriceUSDCents    int64  `yaml:"credit_price_usd_cents"    mapstructure:"credit_price_usd_cents"`

	// Per-channel min/max topup amounts, in payment-side currency.
	MinTopupCNYFen         int64  `yaml:"min_topup_cny_fen"         mapstructure:"min_topup_cny_fen"`
	MaxTopupCNYFen         int64  `yaml:"max_topup_cny_fen"         mapstructure:"max_topup_cny_fen"`
	MinTopupUSDCents       int64  `yaml:"min_topup_usd_cents"       mapstructure:"min_topup_usd_cents"`
	MaxTopupUSDCents       int64  `yaml:"max_topup_usd_cents"       mapstructure:"max_topup_usd_cents"`

	// Currency-agnostic per-day cap on credits purchased.
	DailyTopupLimitCredits int64  `yaml:"daily_topup_limit_credits" mapstructure:"daily_topup_limit_credits"`
}
```

- [ ] **Step 2: Update defaults**

In `internal/config/config.go`'s `newViper` / `SetDefault` block, replace:

```go
v.SetDefault("extra_usage.credit_price_fen", 5438)
v.SetDefault("extra_usage.min_topup_fen", 1000)
v.SetDefault("extra_usage.max_topup_fen", 200000)
v.SetDefault("extra_usage.daily_topup_limit_fen", 500000)
```

With:

```go
v.SetDefault("extra_usage.credit_price_cny_fen", 5438)
v.SetDefault("extra_usage.credit_price_usd_cents", 907)   // ≈ 5438/6 (1USD ≈ 6CNY)
v.SetDefault("extra_usage.min_topup_cny_fen", 1000)        // ¥10
v.SetDefault("extra_usage.max_topup_cny_fen", 200000)      // ¥2000
v.SetDefault("extra_usage.min_topup_usd_cents", 167)       // $1.67 ≈ ¥10
v.SetDefault("extra_usage.max_topup_usd_cents", 33333)     // $333.33 ≈ ¥2000
// Historical default daily cap was 500_000 fen (¥5000). Convert to credits
// using the same default credit_price_cny_fen=5438:
//   500_000 × 1_000_000 / 5438 ≈ 91,945,500 credits → round to 91_945_000.
v.SetDefault("extra_usage.daily_topup_limit_credits", 91945000)
```

Add `BindEnv` for each new key so the env vars work:

```go
_ = v.BindEnv("extra_usage.credit_price_cny_fen")
_ = v.BindEnv("extra_usage.credit_price_usd_cents")
_ = v.BindEnv("extra_usage.min_topup_cny_fen")
_ = v.BindEnv("extra_usage.max_topup_cny_fen")
_ = v.BindEnv("extra_usage.min_topup_usd_cents")
_ = v.BindEnv("extra_usage.max_topup_usd_cents")
_ = v.BindEnv("extra_usage.daily_topup_limit_credits")
```

Remove the old `BindEnv("extra_usage.credit_price_fen")` etc.

- [ ] **Step 3: Add startup validation**

In `internal/config/config.go`'s `unmarshal` (or wherever validation lives), append:

```go
if cfg.ExtraUsage.CreditPriceCNYFen <= 0 {
    return nil, fmt.Errorf("extra_usage.credit_price_cny_fen must be > 0, got %d", cfg.ExtraUsage.CreditPriceCNYFen)
}
if cfg.ExtraUsage.CreditPriceUSDCents <= 0 {
    return nil, fmt.Errorf("extra_usage.credit_price_usd_cents must be > 0, got %d", cfg.ExtraUsage.CreditPriceUSDCents)
}
if cfg.ExtraUsage.MinTopupCNYFen > cfg.ExtraUsage.MaxTopupCNYFen {
    return nil, fmt.Errorf("extra_usage.min_topup_cny_fen (%d) > max_topup_cny_fen (%d)",
        cfg.ExtraUsage.MinTopupCNYFen, cfg.ExtraUsage.MaxTopupCNYFen)
}
if cfg.ExtraUsage.MinTopupUSDCents > cfg.ExtraUsage.MaxTopupUSDCents {
    return nil, fmt.Errorf("extra_usage.min_topup_usd_cents (%d) > max_topup_usd_cents (%d)",
        cfg.ExtraUsage.MinTopupUSDCents, cfg.ExtraUsage.MaxTopupUSDCents)
}
if cfg.ExtraUsage.DailyTopupLimitCredits < 0 {
    return nil, fmt.Errorf("extra_usage.daily_topup_limit_credits must be >= 0, got %d", cfg.ExtraUsage.DailyTopupLimitCredits)
}
```

- [ ] **Step 4: Update tests in `internal/config/config_test.go`**

Find any test that references `CreditPriceFen`, `MinTopupFen`, etc., and update to new names. Add new tests:

```go
func TestExtraUsageConfig_NewDefaults(t *testing.T) {
	cfg, err := Load([]byte(""))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ExtraUsage.CreditPriceCNYFen != 5438 {
		t.Errorf("CreditPriceCNYFen = %d, want 5438", cfg.ExtraUsage.CreditPriceCNYFen)
	}
	if cfg.ExtraUsage.CreditPriceUSDCents != 907 {
		t.Errorf("CreditPriceUSDCents = %d, want 907", cfg.ExtraUsage.CreditPriceUSDCents)
	}
	if cfg.ExtraUsage.MinTopupUSDCents != 167 {
		t.Errorf("MinTopupUSDCents = %d, want 167", cfg.ExtraUsage.MinTopupUSDCents)
	}
	if cfg.ExtraUsage.DailyTopupLimitCredits != 91945000 {
		t.Errorf("DailyTopupLimitCredits = %d, want 91945000", cfg.ExtraUsage.DailyTopupLimitCredits)
	}
}

func TestExtraUsageConfig_ZeroUSDPriceRejected(t *testing.T) {
	yaml := `extra_usage:
  credit_price_usd_cents: 0`
	_, err := Load([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for credit_price_usd_cents=0")
	}
}

func TestExtraUsageConfig_InvertedMinMaxRejected(t *testing.T) {
	yaml := `extra_usage:
  min_topup_cny_fen: 5000
  max_topup_cny_fen: 1000`
	_, err := Load([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for min > max")
	}
}
```

- [ ] **Step 5: Build + test config package**

```
go build ./...
go test ./internal/config/ -count=1 -v
```

Expected: clean build (Task 2 already dropped the `creditPriceFen` parameter from `computeExtraUsageCostCredits`, so no caller still passes it). All tests PASS.

If `go build` reports other call sites still reading `cfg.ExtraUsage.CreditPriceFen` etc., grep:

```
grep -rn "CreditPriceFen\|MinTopupFen\|MaxTopupFen\|DailyTopupLimitFen" --include="*.go" internal/
```

Fix each with the renamed field. The handler in `internal/admin/handle_extra_usage.go` reads `cfg.CreditPriceFen` (now `CreditPriceCNYFen`) — that's Task 4's territory but a quick mechanical edit here is fine if the build fails.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): reshape ExtraUsageConfig for dual-currency topup

Renames CreditPriceFen → CreditPriceCNYFen and adds CreditPriceUSDCents
(default 907 ≈ 5438/6 reflecting the 1USD≈6CNY business rule). No
standalone USDToCNYRate config — implicit rate is the ratio of the two
unit prices.

Renames MinTopupFen/MaxTopupFen → MinTopupCNYFen/MaxTopupCNYFen and
adds USD-side equivalents. DailyTopupLimitFen → DailyTopupLimitCredits
since cross-channel cap is currency-agnostic in the new model.

Startup validation: both unit prices > 0; min ≤ max per channel; daily
cap ≥ 0.

Deployment: operator must update env vars accordingly. Old names
(CREDIT_PRICE_FEN etc.) are no longer read."
```

---

### Task 4: Admin + savings layer to credits

**Files:**
- Modify: `internal/billing/savings.go` — `ComputeCostBreakdown` takes `extraUsageCredits int64` instead of `extraUsageFen`
- Modify: `internal/billing/savings_test.go` — update fixtures
- Modify: `internal/admin/handle_extra_usage.go` — GET response uses new field names + adds per-channel min/max + adds `credit_unit_prices`; `handleAdminExtraUsageDirectTopup` uses `AmountCredits`
- Modify: `internal/admin/handle_extra_usage_bypass_test.go` — update field references
- Modify: `internal/admin/handle_requests.go` — pass `extraCredits` to `ComputeCostBreakdown`
- Modify: `internal/admin/handle_extra_usage_permissions_test.go` — fix field refs if any

**Interfaces:**
- Consumes: Task 2's renamed store + types; Task 3's new config fields
- Produces:
  - `billing.CostBreakdown.ExtraUsageCredits int64` (was `ExtraUsageFen`); related fields stay in fen for now (subscription_fen is unchanged business semantic — plan price is set in fen)
  - GET `/api/v1/projects/{id}/extra-usage` response shape per spec §7

- [ ] **Step 1: Update CostBreakdown**

`internal/billing/savings.go`:

```go
type CostBreakdown struct {
	APIStandardFen      int64     `json:"api_standard_fen"`
	SubscriptionFen     int64     `json:"subscription_fen"`
	ExtraUsageCredits   int64     `json:"extra_usage_credits"`   // was ExtraUsageFen
	ActualPaidFen       int64     `json:"actual_paid_fen"`       // = SubscriptionFen + credits×CreditPriceCNYFen/1e6
	SavedFen            int64     `json:"saved_fen"`
	PeriodStart         time.Time `json:"period_start"`
	PeriodEnd           time.Time `json:"period_end"`
	HasActiveSub        bool      `json:"has_active_subscription"`
}

func ComputeCostBreakdown(
	sums []store.PerModelTokenSums,
	extraUsageCredits int64,          // was extraUsageFen
	catalog modelcatalog.Catalog,
	creditPriceCNYFen int64,           // renamed parameter
	sub *types.Subscription,
	plan *types.Plan,
	fallbackStart, fallbackEnd time.Time,
	activeCurrency string,
) CostBreakdown {
	// ... existing currency-gate logic unchanged ...

	// Convert credits back to fen-equivalent for the actual_paid calculation,
	// using the CNY unit price. Tooltip in the dashboard explains this is an
	// approximation: real cost was paid in whatever currency the topup used.
	extraUsageFenEquivalent := (extraUsageCredits * creditPriceCNYFen) / 1_000_000

	out := CostBreakdown{
		APIStandardFen:    apiFen,
		ExtraUsageCredits: extraUsageCredits,
	}
	if sub != nil && plan != nil {
		out.HasActiveSub = true
		out.SubscriptionFen = plan.PriceCNYFen
		out.PeriodStart = sub.StartsAt
		out.PeriodEnd = sub.ExpiresAt
	} else {
		out.PeriodStart = fallbackStart
		out.PeriodEnd = fallbackEnd
	}
	out.ActualPaidFen = out.SubscriptionFen + extraUsageFenEquivalent
	if out.APIStandardFen > out.ActualPaidFen {
		out.SavedFen = out.APIStandardFen - out.ActualPaidFen
	}
	return out
}
```

- [ ] **Step 2: Update savings_test.go fixtures**

Find every call to `ComputeCostBreakdown(...)` and pass `extraUsageCredits` instead of `extraUsageFen`. Assertions on `CostBreakdown.ExtraUsageFen` become `CostBreakdown.ExtraUsageCredits`. Add a new test:

```go
func TestComputeCostBreakdown_ExtraUsageCreditsConverted(t *testing.T) {
	// 5,000,000 credits × 5438 fen / 1M = 27,190 fen
	cb := billing.ComputeCostBreakdown(
		nil, 5_000_000, nil, 5438, nil, nil,
		time.Now(), time.Now(), "",
	)
	if cb.ExtraUsageCredits != 5_000_000 {
		t.Errorf("ExtraUsageCredits = %d, want 5_000_000", cb.ExtraUsageCredits)
	}
	if cb.ActualPaidFen != 27190 {
		t.Errorf("ActualPaidFen = %d, want 27190", cb.ActualPaidFen)
	}
}
```

- [ ] **Step 3: Update handle_requests.go**

`internal/admin/handle_requests.go` around line 197 — currently:

```go
extraFen, err := st.GetExtraUsageSpendInWindow(projectID, since, until)
// ...
cb := billing.ComputeCostBreakdown(sums, extraFen, catalog, creditPriceFen, ...)
```

Becomes:

```go
extraCredits, err := st.GetExtraUsageSpendInWindow(projectID, since, until)
if err != nil {
    writeError(w, http.StatusInternalServerError, "internal", "failed to get usage")
    return
}
// ...
cb := billing.ComputeCostBreakdown(sums, extraCredits, catalog, cfg.ExtraUsage.CreditPriceCNYFen, ...)
```

- [ ] **Step 4: Update handle_extra_usage.go GET response**

`internal/admin/handle_extra_usage.go` — `handleGetExtraUsage` response struct:

```go
type extraUsageGetResponse struct {
	Enabled              bool                  `json:"enabled"`
	BalanceCredits       int64                 `json:"balance_credits"`
	MonthlyLimitCredits  int64                 `json:"monthly_limit_credits"`
	MonthlySpentCredits  int64                 `json:"monthly_spent_credits"`
	MonthlyWindowStart   string                `json:"monthly_window_start"`
	BypassBalanceCheck   bool                  `json:"bypass_balance_check"`
	UpdatedAt            time.Time             `json:"updated_at,omitempty"`

	CreditUnitPrices     creditUnitPrices      `json:"credit_unit_prices"`
	MinTopup             topupAmounts          `json:"min_topup"`
	MaxTopup             topupAmounts          `json:"max_topup"`
	DailyTopupLimit      int64                 `json:"daily_topup_limit_credits"`
}

type creditUnitPrices struct {
	CNYFenPerMillion   int64   `json:"cny_fen_per_million"`
	USDCentsPerMillion int64   `json:"usd_cents_per_million"`
	ImplicitUSDToCNY   float64 `json:"implicit_usd_to_cny_rate"`
}

type topupAmounts struct {
	CNYFen   int64 `json:"cny_fen"`
	USDCents int64 `json:"usd_cents"`
}

func handleGetExtraUsage(st *store.Store, cfg config.ExtraUsageConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectID := chi.URLParam(r, "projectID")
		settings, err := st.GetExtraUsageSettings(projectID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to load extra usage settings")
			return
		}
		monthStart := store.MonthWindowStart()
		spent, err := st.GetMonthlyExtraSpendCredits(projectID, monthStart)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to sum monthly spend")
			return
		}

		resp := extraUsageGetResponse{
			MonthlyWindowStart: monthStart.Format(time.RFC3339),
			CreditUnitPrices: creditUnitPrices{
				CNYFenPerMillion:   cfg.CreditPriceCNYFen,
				USDCentsPerMillion: cfg.CreditPriceUSDCents,
				ImplicitUSDToCNY:   float64(cfg.CreditPriceCNYFen) / float64(cfg.CreditPriceUSDCents),
			},
			MinTopup:        topupAmounts{CNYFen: cfg.MinTopupCNYFen, USDCents: cfg.MinTopupUSDCents},
			MaxTopup:        topupAmounts{CNYFen: cfg.MaxTopupCNYFen, USDCents: cfg.MaxTopupUSDCents},
			DailyTopupLimit: cfg.DailyTopupLimitCredits,
		}
		if settings != nil {
			resp.Enabled = settings.Enabled
			resp.BalanceCredits = settings.BalanceCredits
			resp.MonthlyLimitCredits = settings.MonthlyLimitCredits
			resp.BypassBalanceCheck = settings.BypassBalanceCheck
			resp.UpdatedAt = settings.UpdatedAt
		}
		resp.MonthlySpentCredits = spent
		writeData(w, http.StatusOK, resp)
	}
}
```

`handleUpdateExtraUsage` body's `monthlyLimit` variable is now in credits:

```go
var monthlyLimit int64
if existing != nil {
    enabled = existing.Enabled
    monthlyLimit = existing.MonthlyLimitCredits   // was MonthlyLimitFen
}
// ...
if body.MonthlyLimitCredits != nil {              // request field rename
    if *body.MonthlyLimitCredits < 0 {
        writeError(...)
        return
    }
    monthlyLimit = *body.MonthlyLimitCredits
}
out, err := st.UpsertExtraUsageSettings(projectID, enabled, monthlyLimit)  // signature unchanged; arg semantics now credits
```

`handleAdminExtraUsageDirectTopup`:

```go
body struct {
    AmountCredits int64  `json:"amount_credits"`    // was amount_fen
    Description   string `json:"description"`
}
// ...
if body.AmountCredits <= 0 {
    writeError(w, http.StatusBadRequest, "bad_request", "amount_credits must be > 0")
    return
}
bal, err := st.TopUpExtraUsage(store.TopUpExtraUsageReq{
    ProjectID:     projectID,
    AmountCredits: body.AmountCredits,
    Reason:        types.ExtraUsageReasonAdminAdjust,
    Description:   body.Description,
})
```

- [ ] **Step 5: Build + run all tests**

```
go build ./...
go test ./internal/billing/ ./internal/admin/ ./internal/store/ ./internal/proxy/ ./internal/config/ -count=1
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/billing/ internal/admin/
git commit -m "refactor(extra-usage): admin handlers + savings.ComputeCostBreakdown to credits

GET /api/v1/projects/{id}/extra-usage now returns balance_credits +
monthly_limit_credits + monthly_spent_credits as the primary unit, plus
credit_unit_prices (both CNY and USD with implicit rate for info) and
per-channel min/max + currency-agnostic daily_topup_limit_credits.

handleUpdateExtraUsage takes monthly_limit_credits.
handleAdminExtraUsageDirectTopup takes amount_credits.

billing.ComputeCostBreakdown takes extraUsageCredits and converts back
to fen via creditPriceCNYFen for the actual_paid_fen / saved_fen
arithmetic that the dashboard's 'Saved by Plan' card depends on."
```

---

## Phase 2: Stripe topup path

### Task 5: Topup handler — channel routing

**Files:**
- Modify: `internal/admin/handle_extra_usage.go` — `handleCreateExtraUsageTopup` accepts `channel`, splits per-channel validation, pre-computes credits, passes them to `CreateOrder`
- Modify: `internal/metrics/metrics.go` — add `IncExtraUsageTopup(channel)` counter

**Interfaces:**
- Consumes: Task 3's config fields; existing `Store.CreateOrder` / `billing.PaymentClient.CreatePayment`
- Produces: `POST /api/v1/projects/{id}/extra-usage/topup` accepting either `{channel: "wechat"|"alipay", amount_fen: N}` or `{channel: "stripe", amount_cents: N}`; order row gets `Currency` set per channel and `ExtraUsageAmountCredits` pre-computed

- [ ] **Step 1: Add metric counter**

`internal/metrics/metrics.go`:

```go
var extraUsageTopupsTotal = newCounter("extra_usage_topups_total", "channel")

func IncExtraUsageTopup(channel string) {
	extraUsageTopupsTotal.inc(1, labelPair{"channel", quote(channel)})
}
```

(Follow the existing pattern of other counters in this file — `newCounter` may be different in modelserver; mirror an existing example like `IncExtraUsageDeduction`.)

- [ ] **Step 2: Rewrite handleCreateExtraUsageTopup**

`internal/admin/handle_extra_usage.go`:

```go
func handleCreateExtraUsageTopup(st *store.Store, payClient billing.PaymentClient, billingCfg config.BillingConfig, euCfg config.ExtraUsageConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !requireRole(w, r, types.RoleOwner, types.RoleMaintainer) {
			return
		}
		projectID := chi.URLParam(r, "projectID")

		var body struct {
			Channel     string `json:"channel"`
			AmountFen   *int64 `json:"amount_fen,omitempty"`
			AmountCents *int64 `json:"amount_cents,omitempty"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}

		var (
			credits       int64
			currency      string
			paymentAmount int64
		)
		switch body.Channel {
		case "wechat", "alipay":
			if body.AmountFen == nil {
				writeError(w, http.StatusBadRequest, "bad_request",
					"amount_fen is required for channel="+body.Channel)
				return
			}
			if body.AmountCents != nil {
				writeError(w, http.StatusBadRequest, "bad_request",
					"amount_cents is not valid for channel="+body.Channel)
				return
			}
			amt := *body.AmountFen
			if amt < euCfg.MinTopupCNYFen {
				writeError(w, http.StatusBadRequest, "bad_request",
					fmt.Sprintf("amount_fen must be >= %d", euCfg.MinTopupCNYFen))
				return
			}
			if amt > euCfg.MaxTopupCNYFen {
				writeError(w, http.StatusBadRequest, "bad_request",
					fmt.Sprintf("amount_fen must be <= %d", euCfg.MaxTopupCNYFen))
				return
			}
			credits = (amt * 1_000_000) / euCfg.CreditPriceCNYFen
			currency = "CNY"
			paymentAmount = amt

		case "stripe":
			if body.AmountCents == nil {
				writeError(w, http.StatusBadRequest, "bad_request",
					"amount_cents is required for channel=stripe")
				return
			}
			if body.AmountFen != nil {
				writeError(w, http.StatusBadRequest, "bad_request",
					"amount_fen is not valid for channel=stripe")
				return
			}
			amt := *body.AmountCents
			if amt < euCfg.MinTopupUSDCents {
				writeError(w, http.StatusBadRequest, "bad_request",
					fmt.Sprintf("amount_cents must be >= %d", euCfg.MinTopupUSDCents))
				return
			}
			if amt > euCfg.MaxTopupUSDCents {
				writeError(w, http.StatusBadRequest, "bad_request",
					fmt.Sprintf("amount_cents must be <= %d", euCfg.MaxTopupUSDCents))
				return
			}
			credits = (amt * 1_000_000) / euCfg.CreditPriceUSDCents
			currency = "USD"
			paymentAmount = amt

		default:
			writeError(w, http.StatusBadRequest, "bad_request",
				"channel must be one of: wechat, alipay, stripe")
			return
		}

		// Daily cap (currency-agnostic, in credits).
		dayStart := store.DayWindowStart()
		todayCredits, err := st.SumDailyExtraUsageTopupCredits(projectID, dayStart)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to check daily topup cap")
			return
		}
		if euCfg.DailyTopupLimitCredits > 0 && todayCredits+credits > euCfg.DailyTopupLimitCredits {
			writeError(w, http.StatusConflict, "daily_topup_limit",
				fmt.Sprintf("daily topup limit %d credits reached", euCfg.DailyTopupLimitCredits))
			return
		}

		order := &types.Order{
			ProjectID:               projectID,
			Periods:                 1,
			UnitPrice:               paymentAmount,
			Amount:                  paymentAmount,
			Currency:                currency,
			Status:                  types.OrderStatusPending,
			Channel:                 body.Channel,
			Metadata:                "{}",
			OrderType:               types.OrderTypeExtraUsageTopup,
			ExtraUsageAmountCredits: credits,
		}
		if err := st.CreateOrder(order); err != nil {
			writeError(w, http.StatusInternalServerError, "internal", "failed to create order: "+err.Error())
			return
		}

		if payClient == nil {
			_ = st.UpdateOrderStatus(order.ID, types.OrderStatusFailed)
			writeError(w, http.StatusServiceUnavailable, "payment_not_configured", "payment provider is not configured")
			return
		}
		payResp, err := payClient.CreatePayment(r.Context(), billing.PaymentRequest{
			OrderID:     order.ID,
			ProductName: fmt.Sprintf("extra-usage topup %d credits", credits),
			Channel:     body.Channel,
			Currency:    currency,
			Amount:      paymentAmount,
			NotifyURL:   billingCfg.NotifyURL,
			ReturnURL:   billingCfg.ReturnURL,
		})
		if err != nil {
			_ = st.UpdateOrderStatus(order.ID, types.OrderStatusFailed)
			writeError(w, http.StatusServiceUnavailable, "payment_provider_error", err.Error())
			return
		}

		metrics.IncExtraUsageTopup(body.Channel)

		writeData(w, http.StatusCreated, map[string]any{
			"order_id":    order.ID,
			"channel":     body.Channel,
			"currency":    currency,
			"amount":      paymentAmount,
			"credits":     credits,
			"payment_url": payResp.PaymentURL,
			"payment_ref": payResp.PaymentRef,
		})
	}
}
```

- [ ] **Step 3: Write tests**

Append to `internal/admin/handle_extra_usage_permissions_test.go` (or a new `handle_extra_usage_topup_test.go`):

```go
func TestCreateTopup_WechatChannel_HappyPath(t *testing.T) {
	// ... setup with a fake PaymentClient that records the request ...
	// Send POST {"channel":"wechat","amount_fen":1000}
	// Assert: 201, response.credits = 1000 * 1e6 / 5438 = 183890,
	//         order.Currency="CNY", order.ExtraUsageAmountCredits=183890
}

func TestCreateTopup_StripeChannel_HappyPath(t *testing.T) {
	// Send POST {"channel":"stripe","amount_cents":167}
	// Assert: 201, response.credits = 167 * 1e6 / 907 = 184123,
	//         order.Currency="USD", order.ExtraUsageAmountCredits=184123
}

func TestCreateTopup_WechatWithCents_Rejected(t *testing.T) {
	// POST {"channel":"wechat","amount_cents":100} → 400
}

func TestCreateTopup_StripeBelowMin_Rejected(t *testing.T) {
	// POST {"channel":"stripe","amount_cents":50} → 400, with min mention
}

func TestCreateTopup_UnknownChannel_Rejected(t *testing.T) {
	// POST {"channel":"bitcoin","amount_fen":1000} → 400
}

func TestCreateTopup_DailyCap_HitsCreditsLimit(t *testing.T) {
	// Seed SumDailyExtraUsageTopupCredits to return DailyTopupLimit-100,
	// post amount that pushes over; expect 409 daily_topup_limit
}
```

- [ ] **Step 4: Build + run tests**

```
go build ./...
go test ./internal/admin/ -count=1 -v -run TestCreateTopup
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/handle_extra_usage.go internal/admin/handle_extra_usage_topup_test.go internal/metrics/metrics.go
git commit -m "feat(extra-usage): topup handler routes by channel; Stripe path added

POST /api/v1/projects/{id}/extra-usage/topup now accepts:
  {channel:'wechat'|'alipay', amount_fen: N}      → CNY pricing
  {channel:'stripe',          amount_cents: N}    → USD pricing

Credits are pre-computed at order creation time and pinned to the order
row's extra_usage_amount_credits. Future config changes (unit prices,
rate) don't retroactively change how many credits the user gets when
this order finally delivers.

Daily cap enforced in credits (currency-agnostic).

New metric: extra_usage_topups_total{channel=...}"
```

---

### Task 6: Delivery handler uses pre-computed credits

**Files:**
- Modify: `internal/admin/handle_delivery.go` — read `order.ExtraUsageAmountCredits` (not derive from amount_fen)

**Interfaces:**
- Consumes: order row's pre-computed `ExtraUsageAmountCredits`
- Produces: TopUp ledger row in credits

- [ ] **Step 1: Read current delivery handler shape**

Find the section in `internal/admin/handle_delivery.go` that handles `order_type=extra_usage_topup`. It currently calls `st.TopUpExtraUsage(...)`. Confirm the binding source.

- [ ] **Step 2: Edit to use pre-computed credits**

Replace any `amount_fen`-derived computation with the order's stored credits:

```go
case types.OrderTypeExtraUsageTopup:
    if order.ExtraUsageAmountCredits <= 0 {
        // Defensive: every extra_usage_topup order is created with this
        // field set by handleCreateExtraUsageTopup. Zero means schema/data
        // corruption — refuse rather than silently no-op.
        logger.Error("delivery: extra_usage_topup order has no credits",
            "order_id", order.ID)
        // status stays at 'paid' so a human can investigate
        return
    }
    newBal, err := st.TopUpExtraUsage(store.TopUpExtraUsageReq{
        ProjectID:     order.ProjectID,
        AmountCredits: order.ExtraUsageAmountCredits,
        OrderID:       order.ID,
        Reason:        types.ExtraUsageReasonUserTopup,
        Description:   fmt.Sprintf("order=%s channel=%s currency=%s", order.ID, order.Channel, order.Currency),
    })
    if err != nil {
        // ... existing error handling
    }
    metrics.SetExtraUsageBalance(order.ProjectID, newBal)
    // mark order delivered
```

- [ ] **Step 3: Build + run delivery test if one exists**

```
go test ./internal/admin/ -count=1 -run TestDelivery
```

If no test exists for the extra-usage topup delivery branch, add a minimal one that seeds an order with `ExtraUsageAmountCredits=12345` and confirms balance increments by exactly that.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/handle_delivery.go internal/admin/*_test.go
git commit -m "feat(extra-usage): delivery handler applies pre-computed credits

Replaces the derived 'amount_fen × …' path with a direct read of
order.ExtraUsageAmountCredits (pre-computed at order-creation time by
handleCreateExtraUsageTopup). Any future change to credit_price_cny_fen
or credit_price_usd_cents won't retroactively change how many credits a
pending order delivers."
```

---

### Task 7: Refund webhook + Store.RefundExtraUsageTopup

**Files:**
- Modify: `internal/store/extra_usage.go` — new method `RefundExtraUsageTopup(orderID string) (int64, error)`
- Create: `internal/admin/handle_refund.go` — `handleRefund` HTTP handler
- Modify: `internal/admin/routes.go` — mount `POST /api/v1/billing/webhook/refund` behind `HMACAuthMiddleware`

**Interfaces:**
- Consumes: payserver refund webhook (TBD body shape — assume `{order_id, amount, currency}` per spec)
- Produces:
  - `Store.RefundExtraUsageTopup(orderID string) (newBalance int64, err error)` — finds the original topup ledger row, inserts a `refund` row with negated credits, decrements balance, all in one tx
  - Idempotent via `uniq_eut_refund_order` (created in Task 1)

- [ ] **Step 1: Write the Store method**

`internal/store/extra_usage.go`:

```go
// RefundExtraUsageTopup reverses a previously-applied topup by inserting a
// 'refund' ledger row with the negated credits and decrementing the
// project's balance. The refund credits value mirrors what the original
// topup added: any subsequent unit-price changes don't affect the reversal
// amount.
//
// Idempotent: the uniq_eut_refund_order partial unique index causes the
// INSERT to fail with PG unique-violation on retry; this method maps that
// to a no-op return (current balance, nil error). A caller that wants to
// distinguish "already refunded" from "newly refunded" can compare the
// returned balance with a prior read.
//
// Balance may go negative if the user spent the credits before the refund
// landed. The extra-usage guard's BalanceCredits <= 0 check rejects further
// requests until rectified via TopUp or admin_adjust.
func (s *Store) RefundExtraUsageTopup(orderID string) (int64, error) {
	ctx := context.Background()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Locate the original topup row.
	var (
		projectID   string
		creditsOrig int64
	)
	err = tx.QueryRow(ctx, `
		SELECT project_id, amount_credits
		FROM extra_usage_transactions
		WHERE order_id = $1 AND type = 'topup'`, orderID,
	).Scan(&projectID, &creditsOrig)
	if err == pgx.ErrNoRows {
		return 0, fmt.Errorf("refund: no topup for order %s", orderID)
	}
	if err != nil {
		return 0, fmt.Errorf("refund: lookup topup: %w", err)
	}

	// Decrement balance (allow negative — no CHECK constraint after PR migration 022 dropped them).
	var newBalance int64
	err = tx.QueryRow(ctx, `
		UPDATE extra_usage_settings
		   SET balance_credits = balance_credits - $1, updated_at = NOW()
		 WHERE project_id = $2
		 RETURNING balance_credits`,
		creditsOrig, projectID,
	).Scan(&newBalance)
	if err != nil {
		return 0, fmt.Errorf("refund: decrement balance: %w", err)
	}

	// Insert refund ledger row. Negate the credits in Go (per the PR #52
	// lesson — SQL `-$N` triggers SQLSTATE 42725 on untyped params).
	_, err = tx.Exec(ctx, `
		INSERT INTO extra_usage_transactions
		  (project_id, type, amount_credits, balance_after_credits, order_id, reason, description)
		VALUES ($1, 'refund', $2, $3, $4, $5, $6)`,
		projectID, -creditsOrig, newBalance, orderID,
		types.ExtraUsageReasonAdminRefund,
		fmt.Sprintf("refund of topup order %s (credits=%d)", orderID, creditsOrig),
	)
	if err != nil {
		// Check for the partial-unique-index violation (idempotent re-run).
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			// Already refunded — roll back the balance decrement and report current.
			tx.Rollback(ctx)
			var curBalance int64
			_ = s.pool.QueryRow(ctx, `SELECT balance_credits FROM extra_usage_settings WHERE project_id = $1`, projectID).Scan(&curBalance)
			return curBalance, nil
		}
		return 0, fmt.Errorf("refund: insert ledger row: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("refund: commit tx: %w", err)
	}
	return newBalance, nil
}
```

- [ ] **Step 2: Write the refund handler**

Create `internal/admin/handle_refund.go`:

```go
package admin

import (
	"log/slog"
	"net/http"

	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// handleBillingRefundWebhook applies a payserver-delivered refund event
// against the originating order. Mounted behind HMACAuthMiddleware so
// only payserver-signed requests reach this handler.
func handleBillingRefundWebhook(st *store.Store, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			OrderID  string `json:"order_id"`
			Amount   int64  `json:"amount"`    // informational — actual reversal uses the order row's stored credits
			Currency string `json:"currency"`  // informational
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "bad_request", "invalid body")
			return
		}
		if body.OrderID == "" {
			writeError(w, http.StatusBadRequest, "bad_request", "order_id required")
			return
		}

		order, err := st.GetOrderByID(body.OrderID)
		if err != nil || order == nil {
			writeError(w, http.StatusNotFound, "not_found", "order not found")
			return
		}

		switch order.OrderType {
		case types.OrderTypeExtraUsageTopup:
			newBal, err := st.RefundExtraUsageTopup(body.OrderID)
			if err != nil {
				logger.Error("refund failed", "order_id", body.OrderID, "err", err)
				writeError(w, http.StatusInternalServerError, "internal", "refund failed")
				return
			}
			logger.Info("refund applied",
				"order_id", body.OrderID,
				"new_balance_credits", newBal)
			writeData(w, http.StatusOK, map[string]any{
				"order_id":            body.OrderID,
				"new_balance_credits": newBal,
			})

		case types.OrderTypeSubscription:
			// Subscription refunds: out of scope for this PR; no-op with
			// observability so ops can investigate manually.
			logger.Warn("subscription refund received but unhandled",
				"order_id", body.OrderID)
			writeData(w, http.StatusAccepted, map[string]string{"status": "unhandled"})

		default:
			writeError(w, http.StatusBadRequest, "bad_request",
				"unknown order_type "+order.OrderType)
		}
	}
}
```

- [ ] **Step 3: Mount the route**

`internal/admin/routes.go` — find the existing `r.Route("/billing/webhook", ...)` block (mounted behind `HMACAuthMiddleware`). Add the refund route:

```go
r.Route("/billing/webhook", func(r chi.Router) {
    r.Use(billing.HMACAuthMiddleware(cfg.Billing.WebhookSecret))
    r.Post("/delivery", handleBillingDelivery(st, ...))
    r.Post("/refund",   handleBillingRefundWebhook(st, httpLogger))   // NEW
})
```

- [ ] **Step 4: Write tests**

Add to a new `internal/admin/handle_refund_test.go` or extend an existing delivery test:

```go
func TestRefund_HappyPath(t *testing.T) {
	// Seed: a delivered extra_usage_topup order with credits=10000;
	//       balance_credits=10000 after the topup ledger row.
	// Call handleBillingRefundWebhook with that order_id.
	// Assert: 200 OK; ledger has 'refund' row with amount=-10000;
	//         balance_credits=0
}

func TestRefund_Idempotent(t *testing.T) {
	// Same seed, call refund twice.
	// Assert: second call returns 200 with same new_balance_credits;
	//         only one 'refund' ledger row exists
}

func TestRefund_NotFoundOrder(t *testing.T) {
	// Call refund with unknown order_id.
	// Assert: 404
}

func TestRefund_AllowsNegativeBalance(t *testing.T) {
	// Seed: topup 10000 credits → balance 10000. Deduct 8000 → balance 2000.
	// Refund the full topup of 10000.
	// Assert: balance_credits = -8000 (allowed under bypass-dropped CHECKs)
	// Assert: subsequent guard middleware rejects further extra-usage requests
}
```

- [ ] **Step 5: Build + run tests**

```
go build ./...
go test ./internal/store/ ./internal/admin/ -count=1 -v -run "Refund"
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/store/extra_usage.go internal/admin/handle_refund.go internal/admin/routes.go internal/admin/handle_refund_test.go
git commit -m "feat(extra-usage): refund webhook + Store.RefundExtraUsageTopup

POST /api/v1/billing/webhook/refund (HMAC-signed) receives payserver
refund events. For extra_usage_topup orders, calls
Store.RefundExtraUsageTopup which:
  - looks up the original topup ledger row
  - decrements the balance by the same credits the topup added
  - inserts a 'refund' ledger row
  - all atomic in one tx

Idempotent via uniq_eut_refund_order (created in migration 052).
Balance may go negative — guard middleware's BalanceCredits<=0 reject
handles this safely; ops can admin_adjust to recover.

Negation done in Go, not SQL — preserves the PR #52 fix.

Subscription refunds: out of scope; logged + 202 Accepted for manual
investigation."
```

---

## Phase 3: Frontend

### Task 8: Dashboard topup form + credits balance display

**Files:**
- Modify: `dashboard/src/...` — exact paths TBD by implementer; expect to find an extra-usage page under `pages/extra-usage/` or similar
- Modify: API client TypeScript types to match new response shape

**Interfaces:**
- Consumes: new GET `/api/v1/projects/{id}/extra-usage` shape (Task 4); topup POST request schemas (Task 5)
- Produces: working frontend for the new wallet

- [ ] **Step 1: Locate the existing extra-usage UI**

```
grep -rln "extra.usage\|ExtraUsage" dashboard/src/ | head -10
```

Note the relevant files. The existing UI shows a single fen balance, an enable toggle, monthly-limit input, topup button, and transaction list.

- [ ] **Step 2: Update TypeScript types**

Wherever the `ExtraUsageGetResponse` (or similar) type is defined, replace fen fields with credits fields and add the new sub-objects. Match the JSON shape produced by Task 4.

- [ ] **Step 3: Update the balance card**

```tsx
<Card>
  <CardTitle>Extra-Usage Wallet</CardTitle>
  <CardContent>
    <div className="text-2xl">{formatCredits(data.balance_credits)} credits</div>
    <div className="text-sm text-muted">
      ≈ ¥{formatYuan(data.balance_credits * data.credit_unit_prices.cny_fen_per_million / 1_000_000)}
    </div>
  </CardContent>
</Card>
```

`formatCredits` and `formatYuan` are small helpers — group thousands with commas, etc.

- [ ] **Step 4: Update the topup dialog**

Add a channel selector (radio group: WeChat / Alipay / Stripe). The amount input swaps between fen (display as ¥) and cents (display as $) based on selection.

Per-channel min/max comes from `data.min_topup` / `data.max_topup`. Show below the input as helper text.

Submit dispatches to the right payload shape:

```tsx
const submit = async () => {
  const body = channel === "stripe"
    ? { channel, amount_cents: amount }
    : { channel, amount_fen: amount };
  const res = await api.post(`/projects/${projectID}/extra-usage/topup`, body);
  window.location.href = res.data.payment_url;  // existing behavior
};
```

Show the computed credits live as the user types: `(amount × 1_000_000 / unit_price)` for the chosen channel.

- [ ] **Step 5: Update the transaction list**

Each row now shows credits as the primary unit. For topup rows joined with the order, show channel + native currency amount as a subtitle: `"+183,890 credits (微信支付 ¥10.00)"`.

- [ ] **Step 6: Update the monthly-limit input**

The current form takes fen; update it to take credits. Display the user-equivalent: typing `1000000` shows `≈ ¥54.38`.

- [ ] **Step 7: Smoke-test in dev**

```
cd dashboard && pnpm dev
```

Walk through: load project, see credits balance, open topup dialog, switch channels, see live-computed credits, submit (will fail in dev without payserver but the request shape goes out).

- [ ] **Step 8: Commit**

```bash
git add dashboard/src/
git commit -m "feat(dashboard): credits-denominated extra-usage UI + Stripe channel

- Balance card shows credits as primary, CNY-equivalent as derived.
- Topup dialog gains a channel selector; amount input switches between
  fen (¥) and cents (\$) display based on selection; live-computes
  credits-to-be-received as the user types.
- Transaction list shows credits + native-currency subtitle for topups.
- Monthly-limit input is now credits-denominated with live ¥-preview."
```

---

## Self-Review

**1. Spec coverage:**
- §1 Schema → Task 1 ✓
- §2 Configuration → Task 3 ✓
- §3 Topup Routing → Task 5 (+ Task 6 delivery) ✓
- §4 Deduction (settle returns credits) → Task 2 ✓
- §5 Refund Wiring → Task 7 ✓
- §6 Migration Safety → Task 1 (Go converter + audit table) ✓
- §7 Dashboard Display → Task 4 (backend response) + Task 8 (frontend) ✓
- §8 Caveats & out-of-scope → no task needed (informational)
- §9 Test Plan → embedded in tasks 1, 5, 7
- §10 Deployment Order → matches phase split (Phase 1 ships independently as no-op rename; Phase 2 adds Stripe)
- §11 File / Module Changes → covered by File Structure section above
- §12 Implementation Plan → this document

**2. Placeholder scan:** "TBD" appears in Task 8 ("exact paths TBD by implementer") which is intentional — the dashboard's exact file layout has shifted over time and Step 1 of that task is to locate it. No other placeholders.

**3. Type consistency:**
- `BalanceCredits` used consistently across Tasks 2/3/4/5/7
- `AmountCredits` consistent in `DeductExtraUsageReq` / `TopUpExtraUsageReq`
- `CreditPriceCNYFen` / `CreditPriceUSDCents` consistent across Tasks 3/4/5
- `MonthWindowStart()` / `DayWindowStart()` (no-arg form per PR #54) consistently used
- `GetMonthlyExtraSpendCredits` rename consistent across Tasks 2/3 (guard middleware test uses it)
- `SumDailyExtraUsageTopupCredits` rename consistent in Tasks 2 and 5

**4. Ambiguity check:** Refund webhook body shape (Task 7) is "TBD by implementer based on payserver" — the spec was vague here too. I've assumed `{order_id, amount, currency}` which mirrors the delivery webhook; if payserver sends something different, the handler's `decodeBody` needs to match.

---

## Out-of-Scope (deferred per spec)

- Currency-display preference per user (V2)
- Pro-rated subscription credit accrual (separate feature)
- Concurrent topup orders bounded by daily cap (no change)
- Runtime-mutable unit prices via DB row (V2 — env config is sufficient now)
- Partial-refund credit recovery (B1 reversal is full-only)
- CI `TEST_DATABASE_URL` wiring (separate workstream — but Task 1's tests need it locally)
