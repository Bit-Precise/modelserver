package store

import (
	"context"
	"math"
	"testing"
)

// TestMigration066_CatalogRowPresent asserts that kimi-k3 was inserted into
// the models table with the expected official-rate JSONB payload. Kimi K3 is
// USD-priced (÷7.5), so input=$3→0.4, output=$15→2.0, cache_hit=$0.30→0.04,
// cache_creation=0 (cache miss billed as ordinary input). No long_context
// block — Moonshot prices the full 1M context window flat.
func TestMigration066_CatalogRowPresent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var input, output, cacheCreate, cacheRead float64
	var publisher string
	var ctxWindow int
	err := st.pool.QueryRow(ctx, `
		SELECT
		  (default_credit_rate->>'input_rate')::float8,
		  (default_credit_rate->>'output_rate')::float8,
		  (default_credit_rate->>'cache_creation_rate')::float8,
		  (default_credit_rate->>'cache_read_rate')::float8,
		  publisher,
		  (metadata->>'context_window')::int
		FROM models WHERE name = 'kimi-k3'`).
		Scan(&input, &output, &cacheCreate, &cacheRead, &publisher, &ctxWindow)
	if err != nil {
		t.Fatalf("query catalog kimi-k3: %v", err)
	}
	if math.Abs(input-0.4) > 1e-9 || math.Abs(output-2.0) > 1e-9 || math.Abs(cacheCreate-0) > 1e-9 || math.Abs(cacheRead-0.04) > 1e-9 {
		t.Fatalf("kimi-k3 catalog rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.4/2.0/0/0.04",
			input, output, cacheCreate, cacheRead)
	}
	if publisher != "moonshot" {
		t.Fatalf("kimi-k3 publisher = %q, want %q", publisher, "moonshot")
	}
	if ctxWindow != 1_048_576 {
		t.Fatalf("kimi-k3 context_window = %d, want 1048576", ctxWindow)
	}

	// Catalog rows MUST NOT carry a long_context block — Kimi K3 is flat.
	var hasLC bool
	if err := st.pool.QueryRow(ctx, `
		SELECT default_credit_rate ? 'long_context' FROM models WHERE name = 'kimi-k3'`).
		Scan(&hasLC); err != nil {
		t.Fatalf("query long_context presence: %v", err)
	}
	if hasLC {
		t.Fatalf("kimi-k3 catalog has long_context block, want none")
	}
}

// TestMigration066_PlansSeeded asserts every plan now has kimi-k3 in
// model_credit_rates with the expected plan-rate values (catalog * 0.1).
func TestMigration066_PlansSeeded(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var missing int
	if err := st.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM plans WHERE NOT (model_credit_rates ? 'kimi-k3')`).
		Scan(&missing); err != nil {
		t.Fatalf("count missing kimi-k3: %v", err)
	}
	if missing != 0 {
		t.Fatalf("%d plan(s) missing kimi-k3 after migration", missing)
	}

	// Spot-check rate values on the well-known 'pro' plan.
	var input, output, cacheCreate, cacheRead float64
	err := st.pool.QueryRow(ctx, `
		SELECT
		  (model_credit_rates->'kimi-k3'->>'input_rate')::float8,
		  (model_credit_rates->'kimi-k3'->>'output_rate')::float8,
		  (model_credit_rates->'kimi-k3'->>'cache_creation_rate')::float8,
		  (model_credit_rates->'kimi-k3'->>'cache_read_rate')::float8
		FROM plans WHERE slug = 'pro'`).
		Scan(&input, &output, &cacheCreate, &cacheRead)
	if err != nil {
		t.Fatalf("query pro plan kimi-k3: %v", err)
	}
	if math.Abs(input-0.04) > 1e-9 || math.Abs(output-0.2) > 1e-9 || math.Abs(cacheCreate-0) > 1e-9 || math.Abs(cacheRead-0.004) > 1e-9 {
		t.Fatalf("pro plan kimi-k3 rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.04/0.2/0/0.004",
			input, output, cacheCreate, cacheRead)
	}
}

// TestMigration066_PoliciesSeeded mirrors TestMigration066_PlansSeeded but
// against rate_limit_policies. Skips silently if no policies exist (fresh
// installs have none).
func TestMigration066_PoliciesSeeded(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var totalPolicies int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_limit_policies`).
		Scan(&totalPolicies); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if totalPolicies == 0 {
		t.Skip("no rate_limit_policies rows to verify (fresh install)")
	}

	var missing int
	if err := st.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM rate_limit_policies WHERE NOT (model_credit_rates ? 'kimi-k3')`).
		Scan(&missing); err != nil {
		t.Fatalf("count missing policy kimi-k3: %v", err)
	}
	if missing != 0 {
		t.Fatalf("%d policy/policies missing kimi-k3 after migration", missing)
	}
}
