package store

import (
	"context"
	"os"
	"testing"
)

// TestMigration051_AlreadyAppliedToActiveFreeRows asserts that any active
// Free-tier subscription left over from openTestStore has currency=''.
// Since migration 051 runs as part of openTestStore, this confirms the
// migration's invariant holds post-apply: no active Free row carries a
// non-empty currency.
func TestMigration051_AlreadyAppliedToActiveFreeRows(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var count int
	if err := st.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM subscriptions
		WHERE status = 'active' AND plan_name = 'free' AND currency <> ''`).
		Scan(&count); err != nil {
		t.Fatalf("count locked-free subs: %v", err)
	}
	if count != 0 {
		t.Fatalf("found %d active Free subs with non-empty currency; migration 051 should have cleared them", count)
	}
}

// TestMigration051_SQLClearsSyntheticBrokenRow synthesizes the exact broken
// state migration 050 produced (an active Free row whose currency got
// backfilled to "CNY" from an earlier paid order on the same project), runs
// migration 051's SQL by hand, and asserts the row was cleared. This is
// the real correctness gate — the migration only runs once per DB during
// schema setup, so we can't assert on its effect against a freshly-applied
// fixture; we have to recreate the scenario and replay the SQL.
func TestMigration051_SQLClearsSyntheticBrokenRow(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 1) seed a user + project for isolation
	userID, projectID := seedUserAndProject(t, st)

	// 2) free plan id (free is seeded by 001_init.sql)
	var freePlanID string
	if err := st.pool.QueryRow(ctx,
		`SELECT id FROM plans WHERE slug = 'free'`).Scan(&freePlanID); err != nil {
		t.Fatalf("look up free plan: %v", err)
	}

	// 3) insert an active Free sub that is INCORRECTLY currency='CNY' —
	// this mirrors what migration 050's backfill produced for projects
	// that paid then expired before 050 ran.
	var subID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO subscriptions
		    (project_id, plan_id, plan_name, status, starts_at, expires_at, currency)
		VALUES ($1, $2, 'free', 'active', NOW(), NOW() + interval '100 years', 'CNY')
		RETURNING id`, projectID, freePlanID).Scan(&subID); err != nil {
		t.Fatalf("insert synthetic broken row: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM subscriptions WHERE id = $1`, subID)
		_, _ = st.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = st.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	// Sanity-check the seed.
	var got string
	if err := st.pool.QueryRow(ctx,
		`SELECT currency FROM subscriptions WHERE id = $1`, subID).Scan(&got); err != nil {
		t.Fatalf("read seeded row: %v", err)
	}
	if got != "CNY" {
		t.Fatalf("seeded currency = %q, want 'CNY' (test fixture broken)", got)
	}

	// 4) run migration 051's SQL verbatim.
	migration, err := os.ReadFile("migrations/051_unlock_active_free_subscriptions.sql")
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if _, err := st.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 051 SQL: %v", err)
	}

	// 5) the synthetic broken row must now be cleared.
	if err := st.pool.QueryRow(ctx,
		`SELECT currency FROM subscriptions WHERE id = $1`, subID).Scan(&got); err != nil {
		t.Fatalf("read row after fix: %v", err)
	}
	if got != "" {
		t.Fatalf("after migration 051: currency = %q, want ''", got)
	}
}

// TestMigration051_LeavesPaidSubscriptionsAlone asserts the corrective
// UPDATE doesn't touch paid subscriptions, which legitimately carry
// currency from DeliverOrder.
func TestMigration051_LeavesPaidSubscriptionsAlone(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	userID, projectID := seedUserAndProject(t, st)

	// max_5x is seeded; use any non-free plan to represent a paid sub.
	var planID string
	if err := st.pool.QueryRow(ctx,
		`SELECT id FROM plans WHERE slug = 'max_5x'`).Scan(&planID); err != nil {
		t.Fatalf("look up max_5x plan: %v", err)
	}

	var subID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO subscriptions
		    (project_id, plan_id, plan_name, status, starts_at, expires_at, currency)
		VALUES ($1, $2, 'max_5x', 'active', NOW(), NOW() + interval '30 days', 'CNY')
		RETURNING id`, projectID, planID).Scan(&subID); err != nil {
		t.Fatalf("insert paid sub: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM subscriptions WHERE id = $1`, subID)
		_, _ = st.pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, projectID)
		_, _ = st.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID)
	})

	migration, err := os.ReadFile("migrations/051_unlock_active_free_subscriptions.sql")
	if err != nil {
		t.Fatalf("read migration file: %v", err)
	}
	if _, err := st.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatalf("apply migration 051 SQL: %v", err)
	}

	var got string
	if err := st.pool.QueryRow(ctx,
		`SELECT currency FROM subscriptions WHERE id = $1`, subID).Scan(&got); err != nil {
		t.Fatalf("read paid sub after fix: %v", err)
	}
	if got != "CNY" {
		t.Fatalf("paid sub currency was modified: got %q, want 'CNY' (migration 051 should leave paid subs alone)", got)
	}
}
