package store

import (
	"context"
	"math"
	"testing"
)

// TestMigration070_AliyunOpenCodePricing verifies the 15 previously absent
// OpenCode Token Plan names. Prices are Alibaba CNY/M-token rates converted to
// catalog credits (÷54.38); plan rates use the migration's documented
// subscription multipliers. The test is skipped when no test database is
// configured, like the other migration integration tests in this package.
func TestMigration070_AliyunOpenCodePricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	type modelCase struct {
		name, publisher                        string
		catalogInput, catalogOutput            float64
		planInput, planOutput                  float64
		catalogCacheCreation, catalogCacheRead float64
		planCacheCreation, planCacheRead       float64
		catalogLongContext, planLongContext    bool
	}
	cases := []modelCase{
		{"qwen3.8-max", "qwen", .220669, .662008, .022067, .066201, .275837, .022067, .027584, .002207, false, false},
		{"qwen3.8-flash", "qwen", .014711, .049651, .001471, .004965, .018389, .001471, .001839, .000147, false, false},
		{"qwen3.7-max", "qwen", .220669, .662008, .022067, .066201, .275837, .022067, .027584, .002207, false, false},
		{"qwen3.7-plus", "qwen", .036778, .147113, .003678, .014711, .045973, .003678, .004597, .000368, true, true},
		{"qwen3.6-plus", "qwen", .036778, .220669, .003678, .022067, .045973, .003678, .004597, .000368, true, true},
		{"qwen3.6-flash", "qwen", .022067, .132402, .002207, .013240, .027584, .002207, .002758, .000221, true, true},
		{"deepseek-v4-pro-0813", "deepseek", .165502, .496506, .010923, .032769, 0, 0, 0, 0, false, false},
		{"deepseek-v4-flash-0731", "deepseek", .055167, .165502, .003641, .010923, 0, 0, 0, 0, false, false},
		{"deepseek-v3.2", "deepseek", .036778, .055167, .002427, .003641, .045973, .003678, .003034, .000243, false, false},
		{"kimi-k2.7-code", "moonshot", .119529, .496506, .011953, .049651, .149412, .011953, .014941, .001195, false, false},
		{"kimi-k2.6", "moonshot", .119529, .496506, .011953, .049651, .149412, .011953, .014941, .001195, false, false},
		{"kimi-k2.5", "moonshot", .073556, .386171, .007356, .038617, .091946, .007356, .009195, .000736, false, false},
		{"glm-5.1", "zhipu", .110335, .441339, .011033, .044134, .137918, .011033, .013792, .001103, true, true},
		{"glm-5", "zhipu", .073556, .331004, .007356, .033100, 0, 0, 0, 0, true, true},
		{"minimax-m2.5", "minimax", .038617, .154469, .003862, .015447, 0, 0, 0, 0, false, false},
	}

	for _, tc := range cases {
		var ci, co, ccc, ccr, pi, po, pcc, pcr float64
		var publisher string
		var catalogLong, planLong bool
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (default_credit_rate->>'input_rate')::float8,
			  (default_credit_rate->>'output_rate')::float8,
			  (default_credit_rate->>'cache_creation_rate')::float8,
			  (default_credit_rate->>'cache_read_rate')::float8,
			  default_credit_rate ? 'long_context', publisher
			FROM models WHERE name = $1`, tc.name).
			Scan(&ci, &co, &ccc, &ccr, &catalogLong, &publisher)
		if err != nil {
			t.Fatalf("%s catalog query: %v", tc.name, err)
		}
		if !closeEnough(ci, tc.catalogInput) || !closeEnough(co, tc.catalogOutput) ||
			!closeEnough(ccc, tc.catalogCacheCreation) || !closeEnough(ccr, tc.catalogCacheRead) {
			t.Fatalf("%s catalog rates: input=%v output=%v cache_creation=%v cache_read=%v",
				tc.name, ci, co, ccc, ccr)
		}
		if publisher != tc.publisher {
			t.Fatalf("%s publisher=%q, want %q", tc.name, publisher, tc.publisher)
		}
		if catalogLong != tc.catalogLongContext {
			t.Fatalf("%s catalog long_context=%v, want %v", tc.name, catalogLong, tc.catalogLongContext)
		}

		err = st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_creation_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8,
			  model_credit_rates->$1 ? 'long_context'
			FROM plans WHERE slug = 'pro'`, tc.name).
			Scan(&pi, &po, &pcc, &pcr, &planLong)
		if err != nil {
			t.Fatalf("%s plan query: %v", tc.name, err)
		}
		if !closeEnough(pi, tc.planInput) || !closeEnough(po, tc.planOutput) ||
			!closeEnough(pcc, tc.planCacheCreation) || !closeEnough(pcr, tc.planCacheRead) {
			t.Fatalf("%s plan rates: input=%v output=%v cache_creation=%v cache_read=%v",
				tc.name, pi, po, pcc, pcr)
		}
		if planLong != tc.planLongContext {
			t.Fatalf("%s plan long_context=%v, want %v", tc.name, planLong, tc.planLongContext)
		}
	}

	// Policies are empty on a fresh install, so only assert the invariant when
	// the database has policy rows.
	var policyCount, missing int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_limit_policies`).Scan(&policyCount); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if policyCount > 0 {
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rate_limit_policies
			WHERE NOT (model_credit_rates ? 'qwen3.8-max')`).Scan(&missing); err != nil {
			t.Fatalf("count missing policy models: %v", err)
		}
		if missing != 0 {
			t.Fatalf("%d policy row(s) missing qwen3.8-max after migration", missing)
		}
	}
}

func closeEnough(got, want float64) bool {
	return math.Abs(got-want) <= 1e-6
}
