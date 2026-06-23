package store

import (
	"context"
	"math"
	"testing"
)

// Plan-rate matrix that must appear in every plan / policy after 053.
// Every entry is exactly 2x the post-047 plan rate
// (see migration047PlanRates in migrations_047_test.go).
var migration053PlanRates = map[string]struct {
	Input, Output, CacheRead float64
	HasLongContext           bool
}{
	"gpt-5.5":            {0.1334, 0.8, 0.0134, false},
	"gpt-5.4":            {0.0666, 0.4, 0.0066, false},
	"gpt-5.4-mini":       {0.0066, 0.0534, 0.0006, true},
	"gpt-5.4-nano":       {0.0014, 0.0106, 0.0002, true},
	"gpt-5.3-codex":      {0.0466, 0.3734, 0.0046, false},
	"gpt-5.2":            {0.0466, 0.3734, 0.0046, false},
	"gpt-5.2-codex":      {0.0466, 0.3734, 0.0046, false},
	"gpt-5.1":            {0.0334, 0.2666, 0.0034, false},
	"gpt-5.1-codex":      {0.0334, 0.2666, 0.0034, false},
	"gpt-5.1-codex-max":  {0.0334, 0.2666, 0.0034, false},
	"gpt-5.1-codex-mini": {0.0066, 0.0534, 0.0006, false},
	"codex-auto-review":  {0.0466, 0.3734, 0.0046, false},
}

// TestMigration053_PlansDoubled asserts every plan now has the 12 gpt /
// codex entries set to the 2x plan-rate matrix.
func TestMigration053_PlansDoubled(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for name, want := range migration053PlanRates {
		var missing int
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM plans WHERE NOT (model_credit_rates ? $1)`, name).
			Scan(&missing); err != nil {
			t.Fatalf("count missing %s in plans: %v", name, err)
		}
		if missing != 0 {
			t.Fatalf("%d plan(s) missing %s after migration 053", missing, name)
		}

		// Spot-check rates on the 'pro' plan.
		var input, output, cacheRead float64
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8
			FROM plans WHERE slug = 'pro'`, name).
			Scan(&input, &output, &cacheRead)
		if err != nil {
			t.Fatalf("pro plan %s query: %v", name, err)
		}
		if math.Abs(input-want.Input) > 1e-9 || math.Abs(output-want.Output) > 1e-9 || math.Abs(cacheRead-want.CacheRead) > 1e-9 {
			t.Fatalf("pro plan %s: input=%v output=%v cache_read=%v; want %v/%v/%v",
				name, input, output, cacheRead, want.Input, want.Output, want.CacheRead)
		}

		// long_context presence check (preserved across the 2x bump).
		var hasLC bool
		if err := st.pool.QueryRow(ctx, `
			SELECT model_credit_rates->$1 ? 'long_context' FROM plans WHERE slug = 'pro'`, name).
			Scan(&hasLC); err != nil {
			t.Fatalf("pro plan %s long_context check: %v", name, err)
		}
		if hasLC != want.HasLongContext {
			t.Fatalf("pro plan %s long_context present = %v, want %v", name, hasLC, want.HasLongContext)
		}
	}
}

// TestMigration053_PoliciesDoubled mirrors the plans check against
// rate_limit_policies. Skips silently if no policies exist.
func TestMigration053_PoliciesDoubled(t *testing.T) {
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

	var policyName string
	if err := st.pool.QueryRow(ctx, `SELECT name FROM rate_limit_policies LIMIT 1`).Scan(&policyName); err != nil {
		t.Fatalf("pick a policy: %v", err)
	}

	for name, want := range migration053PlanRates {
		var missing int
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rate_limit_policies WHERE NOT (model_credit_rates ? $1)`, name).
			Scan(&missing); err != nil {
			t.Fatalf("count missing %s in policies: %v", name, err)
		}
		if missing != 0 {
			t.Fatalf("%d polic(y/ies) missing %s after migration 053", missing, name)
		}

		var input, output, cacheRead float64
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8
			FROM rate_limit_policies WHERE name = $2`, name, policyName).
			Scan(&input, &output, &cacheRead)
		if err != nil {
			t.Fatalf("policy %s rate query: %v", name, err)
		}
		if math.Abs(input-want.Input) > 1e-9 || math.Abs(output-want.Output) > 1e-9 || math.Abs(cacheRead-want.CacheRead) > 1e-9 {
			t.Fatalf("policy %s rates: input=%v output=%v cache_read=%v; want %v/%v/%v",
				name, input, output, cacheRead, want.Input, want.Output, want.CacheRead)
		}

		var hasLC bool
		if err := st.pool.QueryRow(ctx, `
			SELECT model_credit_rates->$1 ? 'long_context' FROM rate_limit_policies WHERE name = $2`, name, policyName).
			Scan(&hasLC); err != nil {
			t.Fatalf("policy %s long_context check: %v", name, err)
		}
		if hasLC != want.HasLongContext {
			t.Fatalf("policy %s long_context present = %v, want %v", name, hasLC, want.HasLongContext)
		}
	}
}

// TestMigration053_CatalogUntouched asserts the catalog (default_credit_rate
// on the models table) is NOT bumped by 053 — only plan / policy rates
// double. The catalog row represents the real OpenAI API cost and is what
// extra-usage / non-subscribers bill against.
func TestMigration053_CatalogUntouched(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// These are the 047 catalog values; 053 must leave them alone.
	cases := []struct {
		name                                 string
		wantInput, wantOutput, wantCacheRead float64
	}{
		{"gpt-5.1", 0.167, 1.333, 0.017},
		{"gpt-5.1-codex", 0.167, 1.333, 0.017},
		{"gpt-5.1-codex-max", 0.167, 1.333, 0.017},
		{"gpt-5.1-codex-mini", 0.033, 0.267, 0.003},
		{"gpt-5.2", 0.233, 1.867, 0.023},
		{"gpt-5.2-codex", 0.233, 1.867, 0.023},
	}

	for _, tc := range cases {
		var input, output, cacheRead float64
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (default_credit_rate->>'input_rate')::float8,
			  (default_credit_rate->>'output_rate')::float8,
			  (default_credit_rate->>'cache_read_rate')::float8
			FROM models WHERE name = $1`, tc.name).
			Scan(&input, &output, &cacheRead)
		if err != nil {
			t.Fatalf("%s: query catalog: %v", tc.name, err)
		}
		if math.Abs(input-tc.wantInput) > 1e-9 || math.Abs(output-tc.wantOutput) > 1e-9 || math.Abs(cacheRead-tc.wantCacheRead) > 1e-9 {
			t.Fatalf("%s catalog changed by 053: input=%v output=%v cache_read=%v; want %v/%v/%v",
				tc.name, input, output, cacheRead, tc.wantInput, tc.wantOutput, tc.wantCacheRead)
		}
	}
}
