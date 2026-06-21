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
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx)

	// Serialize concurrent converters across the cluster so two pods starting
	// simultaneously cannot both pass the idempotency check and double-convert.
	// The advisory lock is released automatically when the transaction ends.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(7295356544049729359)`); err != nil {
		return fmt.Errorf("acquire migration lock: %w", err)
	}

	// Re-check inside the lock: skip if a peer already converted.
	var existingCount int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM extra_usage_credit_migration_audit`).Scan(&existingCount); err != nil {
		return fmt.Errorf("check audit table: %w", err)
	}
	if existingCount > 0 {
		return nil // rollback is fine; we changed nothing
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
