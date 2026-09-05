package store

import (
	"context"
	"math"
	"testing"
)

// TestMigration071_GPT6AstraPricing verifies the catalog and subscription
// payloads for GPT-6 Astra. The catalog values are the documented API prices
// divided by 7.5; plan and policy values are the catalog * 0.4 subscription
// rates used by the current GPT family.
func TestMigration071_GPT6AstraPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var catalogInput, catalogOutput, catalogCacheCreation, catalogCacheRead float64
	var hasCatalogLongContext bool
	var publisher string
	err := st.pool.QueryRow(ctx, `
		SELECT
		  (default_credit_rate->>'input_rate')::float8,
		  (default_credit_rate->>'output_rate')::float8,
		  (default_credit_rate->>'cache_creation_rate')::float8,
		  (default_credit_rate->>'cache_read_rate')::float8,
		  default_credit_rate ? 'long_context', publisher
		FROM models WHERE name = 'gpt-6-astra'`).
		Scan(&catalogInput, &catalogOutput, &catalogCacheCreation, &catalogCacheRead,
			&hasCatalogLongContext, &publisher)
	if err != nil {
		t.Fatalf("query gpt-6-astra catalog row: %v", err)
	}
	if !gpt6AstraPricingCloseEnough(catalogInput, 1.333) ||
		!gpt6AstraPricingCloseEnough(catalogOutput, 6.667) ||
		!gpt6AstraPricingCloseEnough(catalogCacheCreation, 1.667) ||
		!gpt6AstraPricingCloseEnough(catalogCacheRead, 0.133) {
		t.Fatalf("catalog rates: input=%v output=%v cache_creation=%v cache_read=%v; want 1.333/6.667/1.667/0.133",
			catalogInput, catalogOutput, catalogCacheCreation, catalogCacheRead)
	}
	if publisher != "openai" {
		t.Fatalf("publisher = %q, want openai", publisher)
	}
	if !hasCatalogLongContext {
		t.Fatal("catalog row has no long_context block")
	}

	var planInput, planOutput, planCacheCreation, planCacheRead float64
	var hasPlanLongContext bool
	err = st.pool.QueryRow(ctx, `
		SELECT
		  (model_credit_rates->'gpt-6-astra'->>'input_rate')::float8,
		  (model_credit_rates->'gpt-6-astra'->>'output_rate')::float8,
		  (model_credit_rates->'gpt-6-astra'->>'cache_creation_rate')::float8,
		  (model_credit_rates->'gpt-6-astra'->>'cache_read_rate')::float8,
		  model_credit_rates->'gpt-6-astra' ? 'long_context'
		FROM plans WHERE slug = 'pro'`).
		Scan(&planInput, &planOutput, &planCacheCreation, &planCacheRead, &hasPlanLongContext)
	if err != nil {
		t.Fatalf("query pro gpt-6-astra rate: %v", err)
	}
	if !gpt6AstraPricingCloseEnough(planInput, 0.5332) ||
		!gpt6AstraPricingCloseEnough(planOutput, 2.6668) ||
		!gpt6AstraPricingCloseEnough(planCacheCreation, 0.6668) ||
		!gpt6AstraPricingCloseEnough(planCacheRead, 0.0532) {
		t.Fatalf("plan rates: input=%v output=%v cache_creation=%v cache_read=%v; want 0.5332/2.6668/0.6668/0.0532",
			planInput, planOutput, planCacheCreation, planCacheRead)
	}
	if !hasPlanLongContext {
		t.Fatal("plan rate has no long_context block")
	}

	var policyCount, missing int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_limit_policies`).Scan(&policyCount); err != nil {
		t.Fatalf("count rate-limit policies: %v", err)
	}
	if policyCount > 0 {
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rate_limit_policies
			WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-astra')`).Scan(&missing); err != nil {
			t.Fatalf("count policies missing gpt-6-astra: %v", err)
		}
		if missing != 0 {
			t.Fatalf("%d rate-limit policy row(s) missing gpt-6-astra", missing)
		}
	}
}

func gpt6AstraPricingCloseEnough(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}
