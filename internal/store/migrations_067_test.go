package store

import (
	"context"
	"math"
	"testing"
)

// Plan-rate matrix that must appear in every plan / policy after 067.
// Every entry is exactly 2x the post-053 plan rate
// (see migration053PlanRates in migrations_053_test.go) and 2x the 062
// gpt-5.6 seeds — i.e. 4x the 047 baseline for the 053 key set.
var migration067PlanRates = map[string]struct {
	Input, Output, CacheCreation, CacheRead float64
	HasLongContext                          bool
}{
	"gpt-5.5":            {0.2668, 1.6, 0, 0.0268, false},
	"gpt-5.4":            {0.1332, 0.8, 0, 0.0132, false},
	"gpt-5.4-mini":       {0.0132, 0.1068, 0, 0.0012, true},
	"gpt-5.4-nano":       {0.0028, 0.0212, 0, 0.0004, true},
	"gpt-5.3-codex":      {0.0932, 0.7468, 0, 0.0092, false},
	"gpt-5.2":            {0.0932, 0.7468, 0, 0.0092, false},
	"gpt-5.2-codex":      {0.0932, 0.7468, 0, 0.0092, false},
	"gpt-5.1":            {0.0668, 0.5332, 0, 0.0068, false},
	"gpt-5.1-codex":      {0.0668, 0.5332, 0, 0.0068, false},
	"gpt-5.1-codex-max":  {0.0668, 0.5332, 0, 0.0068, false},
	"gpt-5.1-codex-mini": {0.0132, 0.1068, 0, 0.0012, false},
	"codex-auto-review":  {0.0932, 0.7468, 0, 0.0092, false},
	"gpt-5.6-sol":        {0.2668, 1.6, 0.3336, 0.0268, true},
	"gpt-5.6-terra":      {0.1332, 0.8, 0.1668, 0.0132, true},
	"gpt-5.6-luna":       {0.0532, 0.32, 0.0668, 0.0054, true},
}

// TestMigration067_PlansDoubled asserts every plan now has the 15 gpt /
// codex entries set to the 2x-of-053 plan-rate matrix.
func TestMigration067_PlansDoubled(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for name, want := range migration067PlanRates {
		var missing int
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM plans WHERE NOT (model_credit_rates ? $1)`, name).
			Scan(&missing); err != nil {
			t.Fatalf("count missing %s in plans: %v", name, err)
		}
		if missing != 0 {
			t.Fatalf("%d plan(s) missing %s after migration 067", missing, name)
		}

		// Spot-check rates on the 'pro' plan.
		var input, output, cacheCreation, cacheRead float64
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_creation_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8
			FROM plans WHERE slug = 'pro'`, name).
			Scan(&input, &output, &cacheCreation, &cacheRead)
		if err != nil {
			t.Fatalf("pro plan %s query: %v", name, err)
		}
		if math.Abs(input-want.Input) > 1e-9 || math.Abs(output-want.Output) > 1e-9 ||
			math.Abs(cacheCreation-want.CacheCreation) > 1e-9 || math.Abs(cacheRead-want.CacheRead) > 1e-9 {
			t.Fatalf("pro plan %s: input=%v output=%v cache_creation=%v cache_read=%v; want %v/%v/%v/%v",
				name, input, output, cacheCreation, cacheRead,
				want.Input, want.Output, want.CacheCreation, want.CacheRead)
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

// TestMigration067_PoliciesDoubled mirrors the plans check against
// rate_limit_policies. Skips silently if no policies exist.
func TestMigration067_PoliciesDoubled(t *testing.T) {
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

	for name, want := range migration067PlanRates {
		var missing int
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rate_limit_policies WHERE NOT (model_credit_rates ? $1)`, name).
			Scan(&missing); err != nil {
			t.Fatalf("count missing %s in policies: %v", name, err)
		}
		if missing != 0 {
			t.Fatalf("%d polic(y/ies) missing %s after migration 067", missing, name)
		}

		var input, output, cacheCreation, cacheRead float64
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_creation_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8
			FROM rate_limit_policies WHERE name = $2`, name, policyName).
			Scan(&input, &output, &cacheCreation, &cacheRead)
		if err != nil {
			t.Fatalf("policy %s rate query: %v", name, err)
		}
		if math.Abs(input-want.Input) > 1e-9 || math.Abs(output-want.Output) > 1e-9 ||
			math.Abs(cacheCreation-want.CacheCreation) > 1e-9 || math.Abs(cacheRead-want.CacheRead) > 1e-9 {
			t.Fatalf("policy %s rates: input=%v output=%v cache_creation=%v cache_read=%v; want %v/%v/%v/%v",
				name, input, output, cacheCreation, cacheRead,
				want.Input, want.Output, want.CacheCreation, want.CacheRead)
		}

		var hasLC bool
		if err := st.pool.QueryRow(ctx, `
			SELECT model_credit_rates->$1 ? 'long_context' FROM rate_limit_policies WHERE name = $2`, name).
			Scan(&hasLC); err != nil {
			t.Fatalf("policy %s long_context check: %v", name, err)
		}
		if hasLC != want.HasLongContext {
			t.Fatalf("policy %s long_context present = %v, want %v", name, hasLC, want.HasLongContext)
		}
	}
}

// TestMigration067_CatalogUntouched asserts the catalog (default_credit_rate
// on the models table) is NOT bumped by 067 — only plan / policy rates
// double. The catalog row represents the real OpenAI API cost and is what
// extra-usage / non-subscribers bill against.
//
// Unlike its 053 sibling, rows that don't exist are skipped rather than
// fatal: on a fresh install the legacy gpt-5.1/5.2 catalog rows are absent
// (047's backfill is an UPDATE against rows only production has), while
// the 062 gpt-5.6 rows always exist — so the check keeps its teeth.
func TestMigration067_CatalogUntouched(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// 047 catalog values for the older family, 062 catalog values for
	// gpt-5.6 — 067 must leave them all alone.
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
		{"gpt-5.6-sol", 0.667, 4.0, 0.067},
		{"gpt-5.6-terra", 0.333, 2.0, 0.033},
		{"gpt-5.6-luna", 0.133, 0.8, 0.013},
	}

	found := 0
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
			continue // no catalog row for this model on this install
		}
		found++
		if math.Abs(input-tc.wantInput) > 1e-9 || math.Abs(output-tc.wantOutput) > 1e-9 || math.Abs(cacheRead-tc.wantCacheRead) > 1e-9 {
			t.Fatalf("%s catalog changed by 067: input=%v output=%v cache_read=%v; want %v/%v/%v",
				tc.name, input, output, cacheRead, tc.wantInput, tc.wantOutput, tc.wantCacheRead)
		}
	}
	if found == 0 {
		t.Skip("no gpt catalog rows exist on this install")
	}
}
