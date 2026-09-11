package store

import (
	"context"
	"math"
	"testing"
)

// TestMigration073_GLM53FlashPricing verifies the catalog and subscription
// payloads for GLM-5.3-Flash. Catalog values are Z.AI's published USD prices
// divided by 7.5; plan and policy values use the GLM family's 0.1 multiplier.
func TestMigration073_GLM53FlashPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var catalogInput, catalogOutput, catalogCacheCreation, catalogCacheRead float64
	var contextWindow int
	var hasCatalogLongContext bool
	var publisher string
	err := st.pool.QueryRow(ctx, `
		SELECT
		  (default_credit_rate->>'input_rate')::float8,
		  (default_credit_rate->>'output_rate')::float8,
		  (default_credit_rate->>'cache_creation_rate')::float8,
		  (default_credit_rate->>'cache_read_rate')::float8,
		  (metadata->>'context_window')::int,
		  default_credit_rate ? 'long_context',
		  publisher
		FROM models WHERE name = 'glm-5.3-flash'`).
		Scan(&catalogInput, &catalogOutput, &catalogCacheCreation, &catalogCacheRead,
			&contextWindow, &hasCatalogLongContext, &publisher)
	if err != nil {
		t.Fatalf("query glm-5.3-flash catalog row: %v", err)
	}
	if !glm53FlashPricingCloseEnough(catalogInput, 0.02) ||
		!glm53FlashPricingCloseEnough(catalogOutput, 0.067) ||
		!glm53FlashPricingCloseEnough(catalogCacheCreation, 0) ||
		!glm53FlashPricingCloseEnough(catalogCacheRead, 0.004) {
		t.Fatalf("catalog rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.02/0.067/0/0.004",
			catalogInput, catalogOutput, catalogCacheCreation, catalogCacheRead)
	}
	if publisher != "zhipu" {
		t.Fatalf("publisher = %q, want zhipu", publisher)
	}
	if contextWindow != 1_000_000 {
		t.Fatalf("context_window = %d, want 1000000", contextWindow)
	}
	if hasCatalogLongContext {
		t.Fatal("catalog row has a long_context block; want flat pricing")
	}

	var missingPlans int
	if err := st.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM plans
		WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'glm-5.3-flash')`).Scan(&missingPlans); err != nil {
		t.Fatalf("count plans missing glm-5.3-flash: %v", err)
	}
	if missingPlans != 0 {
		t.Fatalf("%d plan row(s) missing glm-5.3-flash", missingPlans)
	}

	var planInput, planOutput, planCacheCreation, planCacheRead float64
	var hasPlanLongContext bool
	err = st.pool.QueryRow(ctx, `
		SELECT
		  (model_credit_rates->'glm-5.3-flash'->>'input_rate')::float8,
		  (model_credit_rates->'glm-5.3-flash'->>'output_rate')::float8,
		  (model_credit_rates->'glm-5.3-flash'->>'cache_creation_rate')::float8,
		  (model_credit_rates->'glm-5.3-flash'->>'cache_read_rate')::float8,
		  model_credit_rates->'glm-5.3-flash' ? 'long_context'
		FROM plans WHERE slug = 'pro'`).
		Scan(&planInput, &planOutput, &planCacheCreation, &planCacheRead, &hasPlanLongContext)
	if err != nil {
		t.Fatalf("query pro glm-5.3-flash rate: %v", err)
	}
	if !glm53FlashPricingCloseEnough(planInput, 0.002) ||
		!glm53FlashPricingCloseEnough(planOutput, 0.0067) ||
		!glm53FlashPricingCloseEnough(planCacheCreation, 0) ||
		!glm53FlashPricingCloseEnough(planCacheRead, 0.0004) {
		t.Fatalf("plan rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.002/0.0067/0/0.0004",
			planInput, planOutput, planCacheCreation, planCacheRead)
	}
	if hasPlanLongContext {
		t.Fatal("plan rate has a long_context block; want flat pricing")
	}

	var policyCount, missing int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_limit_policies`).Scan(&policyCount); err != nil {
		t.Fatalf("count rate-limit policies: %v", err)
	}
	if policyCount > 0 {
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rate_limit_policies
			WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'glm-5.3-flash')`).Scan(&missing); err != nil {
			t.Fatalf("count policies missing glm-5.3-flash: %v", err)
		}
		if missing != 0 {
			t.Fatalf("%d rate-limit policy row(s) missing glm-5.3-flash", missing)
		}
	}
}

func glm53FlashPricingCloseEnough(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}
