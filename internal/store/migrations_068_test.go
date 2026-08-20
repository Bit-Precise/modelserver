package store

import (
	"context"
	"math"
	"testing"
)

// TestMigration068_CatalogRowPresent asserts that glm-5.3 was inserted into
// the models table with the expected official-rate JSONB payload. GLM-5.3 is
// USD-priced (÷7.5) and shares GLM-5.2's pricing, so input=$1.40→0.187,
// output=$4.40→0.587, cache_read=$0.26→0.035, cache_creation=0 (cache miss
// billed as ordinary input). No long_context block — Z.AI prices the full 1M
// context window flat.
func TestMigration068_CatalogRowPresent(t *testing.T) {
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
		FROM models WHERE name = 'glm-5.3'`).
		Scan(&input, &output, &cacheCreate, &cacheRead, &publisher, &ctxWindow)
	if err != nil {
		t.Fatalf("query catalog glm-5.3: %v", err)
	}
	if math.Abs(input-0.187) > 1e-9 || math.Abs(output-0.587) > 1e-9 || math.Abs(cacheCreate-0) > 1e-9 || math.Abs(cacheRead-0.035) > 1e-9 {
		t.Fatalf("glm-5.3 catalog rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.187/0.587/0/0.035",
			input, output, cacheCreate, cacheRead)
	}
	if publisher != "zhipu" {
		t.Fatalf("glm-5.3 publisher = %q, want %q", publisher, "zhipu")
	}
	if ctxWindow != 1_000_000 {
		t.Fatalf("glm-5.3 context_window = %d, want 1000000", ctxWindow)
	}

	// Catalog rows MUST NOT carry a long_context block — GLM-5.3 is flat.
	var hasLC bool
	if err := st.pool.QueryRow(ctx, `
		SELECT default_credit_rate ? 'long_context' FROM models WHERE name = 'glm-5.3'`).
		Scan(&hasLC); err != nil {
		t.Fatalf("query long_context presence: %v", err)
	}
	if hasLC {
		t.Fatalf("glm-5.3 catalog has long_context block, want none")
	}
}

// TestMigration068_PlansSeeded asserts every plan now has glm-5.3 in
// model_credit_rates with the expected plan-rate values (catalog * 0.1).
func TestMigration068_PlansSeeded(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var missing int
	if err := st.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM plans WHERE NOT (model_credit_rates ? 'glm-5.3')`).
		Scan(&missing); err != nil {
		t.Fatalf("count missing glm-5.3: %v", err)
	}
	if missing != 0 {
		t.Fatalf("%d plan(s) missing glm-5.3 after migration", missing)
	}

	// Spot-check rate values on the well-known 'pro' plan.
	var input, output, cacheCreate, cacheRead float64
	err := st.pool.QueryRow(ctx, `
		SELECT
		  (model_credit_rates->'glm-5.3'->>'input_rate')::float8,
		  (model_credit_rates->'glm-5.3'->>'output_rate')::float8,
		  (model_credit_rates->'glm-5.3'->>'cache_creation_rate')::float8,
		  (model_credit_rates->'glm-5.3'->>'cache_read_rate')::float8
		FROM plans WHERE slug = 'pro'`).
		Scan(&input, &output, &cacheCreate, &cacheRead)
	if err != nil {
		t.Fatalf("query pro plan glm-5.3: %v", err)
	}
	if math.Abs(input-0.0187) > 1e-9 || math.Abs(output-0.0587) > 1e-9 || math.Abs(cacheCreate-0) > 1e-9 || math.Abs(cacheRead-0.0035) > 1e-9 {
		t.Fatalf("pro plan glm-5.3 rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.0187/0.0587/0/0.0035",
			input, output, cacheCreate, cacheRead)
	}
}

// TestMigration068_PoliciesSeeded mirrors TestMigration068_PlansSeeded but
// against rate_limit_policies. Skips silently if no policies exist (fresh
// installs have none).
func TestMigration068_PoliciesSeeded(t *testing.T) {
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
		SELECT COUNT(*) FROM rate_limit_policies WHERE NOT (model_credit_rates ? 'glm-5.3')`).
		Scan(&missing); err != nil {
		t.Fatalf("count missing policy glm-5.3: %v", err)
	}
	if missing != 0 {
		t.Fatalf("%d policy/policies missing glm-5.3 after migration", missing)
	}
}
