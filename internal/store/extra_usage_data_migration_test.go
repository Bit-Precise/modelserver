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
