package store

import (
	"context"
	"math"
	"testing"
)

// TestMigration069_OpenAIPricing verifies the current OpenAI prices are
// represented in catalog credit units and that plan subscription maps use the
// corresponding 0.4x rates. The policy update mirrors the same SQL payload;
// the test is skipped when no test database is configured, like the other
// migration integration tests in this package.
func TestMigration069_OpenAIPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name                                   string
		catalogInput, catalogOutput            float64
		catalogCacheCreation, catalogCacheRead float64
		planInput, planOutput                  float64
		planCacheCreation, planCacheRead       float64
		catalogLongContext, planLongContext    bool
	}{
		{"gpt-5.6-sol", 0.533, 2.667, 0.667, 0.053, 0.2132, 1.0668, 0.2668, 0.0212, true, true},
		{"gpt-5.6-terra", 0.267, 1.6, 0.333, 0.027, 0.1068, 0.64, 0.1332, 0.0108, true, true},
		{"gpt-5.6-luna", 0.027, 0.16, 0.033, 0.003, 0.0108, 0.064, 0.0132, 0.0012, true, true},
		{"gpt-5.4-mini", 0.1, 0.6, 0, 0.01, 0.04, 0.24, 0, 0.004, false, false},
		{"gpt-5.4-nano", 0.027, 0.167, 0, 0.003, 0.0108, 0.0668, 0, 0.0012, false, false},
	}

	for _, tc := range cases {
		var ci, co, ccc, ccr float64
		var hasCatalogLongContext bool
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (default_credit_rate->>'input_rate')::float8,
			  (default_credit_rate->>'output_rate')::float8,
			  (default_credit_rate->>'cache_creation_rate')::float8,
			  (default_credit_rate->>'cache_read_rate')::float8,
			  default_credit_rate ? 'long_context'
			FROM models WHERE name = $1`, tc.name).
			Scan(&ci, &co, &ccc, &ccr, &hasCatalogLongContext)
		if err != nil {
			t.Fatalf("%s catalog query: %v", tc.name, err)
		}
		if !openAIPricingCloseEnough(ci, tc.catalogInput) || !openAIPricingCloseEnough(co, tc.catalogOutput) ||
			!openAIPricingCloseEnough(ccc, tc.catalogCacheCreation) || !openAIPricingCloseEnough(ccr, tc.catalogCacheRead) {
			t.Fatalf("%s catalog rates: input=%v output=%v cache_creation=%v cache_read=%v; want %v/%v/%v/%v",
				tc.name, ci, co, ccc, ccr,
				tc.catalogInput, tc.catalogOutput, tc.catalogCacheCreation, tc.catalogCacheRead)
		}
		if hasCatalogLongContext != tc.catalogLongContext {
			t.Fatalf("%s catalog long_context=%v; want %v", tc.name, hasCatalogLongContext, tc.catalogLongContext)
		}

		var pi, po, pcc, pcr float64
		var hasPlanLongContext bool
		err = st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_creation_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8,
			  model_credit_rates->$1 ? 'long_context'
			FROM plans WHERE slug = 'pro'`, tc.name).
			Scan(&pi, &po, &pcc, &pcr, &hasPlanLongContext)
		if err != nil {
			t.Fatalf("%s plan query: %v", tc.name, err)
		}
		if !openAIPricingCloseEnough(pi, tc.planInput) || !openAIPricingCloseEnough(po, tc.planOutput) ||
			!openAIPricingCloseEnough(pcc, tc.planCacheCreation) || !openAIPricingCloseEnough(pcr, tc.planCacheRead) {
			t.Fatalf("%s plan rates: input=%v output=%v cache_creation=%v cache_read=%v; want %v/%v/%v/%v",
				tc.name, pi, po, pcc, pcr,
				tc.planInput, tc.planOutput, tc.planCacheCreation, tc.planCacheRead)
		}
		if hasPlanLongContext != tc.planLongContext {
			t.Fatalf("%s plan long_context=%v; want %v", tc.name, hasPlanLongContext, tc.planLongContext)
		}
	}
}

func openAIPricingCloseEnough(got, want float64) bool {
	return math.Abs(got-want) <= 1e-9
}
