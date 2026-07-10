package store

import (
	"context"
	"math"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// TestMigration062_CatalogRowsPresent asserts the three gpt-5.6 catalog rows
// were inserted with the expected default_credit_rate payload (input,
// output, cache_read, cache_creation) and long_context block.
func TestMigration062_CatalogRowsPresent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name          string
		wantInput     float64
		wantOutput    float64
		wantCacheRead float64
		wantCacheCrea float64
	}{
		{"gpt-5.6-sol", 0.667, 4.0, 0.067, 0.833},
		{"gpt-5.6-terra", 0.333, 2.0, 0.033, 0.417},
		{"gpt-5.6-luna", 0.133, 0.8, 0.013, 0.167},
	}

	for _, tc := range cases {
		var input, output, cacheRead, cacheCrea, lcIn, lcOut float64
		var lcThresh int
		var publisher, status string
		var ctxWindow int
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (default_credit_rate->>'input_rate')::float8,
			  (default_credit_rate->>'output_rate')::float8,
			  (default_credit_rate->>'cache_read_rate')::float8,
			  (default_credit_rate->>'cache_creation_rate')::float8,
			  (default_credit_rate->'long_context'->>'input_multiplier')::float8,
			  (default_credit_rate->'long_context'->>'output_multiplier')::float8,
			  (default_credit_rate->'long_context'->>'threshold_input_tokens')::int,
			  publisher, status,
			  (metadata->>'context_window')::int
			FROM models WHERE name = $1`, tc.name).
			Scan(&input, &output, &cacheRead, &cacheCrea, &lcIn, &lcOut, &lcThresh,
				&publisher, &status, &ctxWindow)
		if err != nil {
			t.Fatalf("%s: query catalog: %v", tc.name, err)
		}
		if input != tc.wantInput || output != tc.wantOutput ||
			cacheRead != tc.wantCacheRead || cacheCrea != tc.wantCacheCrea {
			t.Fatalf("%s catalog rates: input=%v output=%v cache_read=%v cache_creation=%v; want %v/%v/%v/%v",
				tc.name, input, output, cacheRead, cacheCrea,
				tc.wantInput, tc.wantOutput, tc.wantCacheRead, tc.wantCacheCrea)
		}
		if lcIn != 2.0 || lcOut != 1.5 || lcThresh != 272000 {
			t.Fatalf("%s long_context: in_mult=%v out_mult=%v thresh=%v; want 2/1.5/272000",
				tc.name, lcIn, lcOut, lcThresh)
		}
		if publisher != "openai" || status != "active" || ctxWindow != 1050000 {
			t.Fatalf("%s metadata: publisher=%q status=%q ctxWindow=%d; want openai/active/1050000",
				tc.name, publisher, status, ctxWindow)
		}
	}
}

// TestMigration062_AliasResolution asserts the family-level 'gpt-5.6' alias
// resolves to gpt-5.6-sol (mirrors OpenAI's own alias table).
func TestMigration062_AliasResolution(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var canonical string
	err := st.pool.QueryRow(ctx, `
		SELECT name FROM models WHERE 'gpt-5.6' = ANY(aliases)`).Scan(&canonical)
	if err != nil {
		t.Fatalf("query alias: %v", err)
	}
	if canonical != "gpt-5.6-sol" {
		t.Fatalf("alias gpt-5.6 -> %q, want gpt-5.6-sol", canonical)
	}
}

// TestMigration062_PlansSeeded asserts every plan now has all three keys
// in model_credit_rates with the expected plan-rate values (catalog * 0.2),
// including the long_context block.
func TestMigration062_PlansSeeded(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	cases := []struct {
		key           string
		wantInput     float64
		wantOutput    float64
		wantCacheRead float64
		wantCacheCrea float64
	}{
		{"gpt-5.6-sol", 0.1334, 0.8, 0.0134, 0.1668},
		{"gpt-5.6-terra", 0.0666, 0.4, 0.0066, 0.0834},
		{"gpt-5.6-luna", 0.0266, 0.16, 0.0027, 0.0334},
	}

	for _, tc := range cases {
		var missing int
		if err := st.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM plans WHERE NOT (model_credit_rates ? $1)`, tc.key).
			Scan(&missing); err != nil {
			t.Fatalf("count missing %s: %v", tc.key, err)
		}
		if missing != 0 {
			t.Fatalf("%d plan(s) missing %s after migration", missing, tc.key)
		}

		var input, output, cacheRead, cacheCrea, lcIn, lcOut float64
		var lcThresh int
		err := st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8,
			  (model_credit_rates->$1->>'cache_creation_rate')::float8,
			  (model_credit_rates->$1->'long_context'->>'input_multiplier')::float8,
			  (model_credit_rates->$1->'long_context'->>'output_multiplier')::float8,
			  (model_credit_rates->$1->'long_context'->>'threshold_input_tokens')::int
			FROM plans WHERE slug = 'pro'`, tc.key).
			Scan(&input, &output, &cacheRead, &cacheCrea, &lcIn, &lcOut, &lcThresh)
		if err != nil {
			t.Fatalf("query pro plan %s: %v", tc.key, err)
		}
		if input != tc.wantInput || output != tc.wantOutput ||
			cacheRead != tc.wantCacheRead || cacheCrea != tc.wantCacheCrea {
			t.Fatalf("pro plan %s: input=%v output=%v cache_read=%v cache_creation=%v; want %v/%v/%v/%v",
				tc.key, input, output, cacheRead, cacheCrea,
				tc.wantInput, tc.wantOutput, tc.wantCacheRead, tc.wantCacheCrea)
		}
		if lcIn != 2.0 || lcOut != 1.5 || lcThresh != 272000 {
			t.Fatalf("pro plan %s long_context: in_mult=%v out_mult=%v thresh=%v; want 2/1.5/272000",
				tc.key, lcIn, lcOut, lcThresh)
		}
	}
}

// TestMigration062_PoliciesSeeded mirrors PlansSeeded against
// rate_limit_policies. Skips when no policies exist (fresh install).
func TestMigration062_PoliciesSeeded(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var total int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_limit_policies`).
		Scan(&total); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if total == 0 {
		t.Skip("no rate_limit_policies rows to verify (fresh install)")
	}
	for _, key := range []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		var missing int
		if err := st.pool.QueryRow(ctx,
			`SELECT COUNT(*) FROM rate_limit_policies WHERE NOT (model_credit_rates ? $1)`, key).
			Scan(&missing); err != nil {
			t.Fatalf("count missing policy %s: %v", key, err)
		}
		if missing != 0 {
			t.Fatalf("%d policy/policies missing %s after migration", missing, key)
		}
	}
}

// TestMigration062_LongContextMath verifies the semantics we rely on:
// ApplyLongContextCreditRate on gpt-5.6-sol's plan rate doubles input /
// cache_read / cache_creation and multiplies output by 1.5 when total
// input tokens exceed 272000. Short context stays as base rate.
func TestMigration062_LongContextMath(t *testing.T) {
	base := types.CreditRate{
		InputRate:         0.1334,
		OutputRate:        0.8,
		CacheCreationRate: 0.1668,
		CacheReadRate:     0.0134,
		LongContext: &types.LongContextCreditRate{
			ThresholdInputTokens: 272000,
			InputMultiplier:      2.0,
			OutputMultiplier:     1.5,
		},
	}
	short := types.ApplyLongContextCreditRate(base, 200_000)
	if short.InputRate != 0.1334 || short.OutputRate != 0.8 ||
		short.CacheReadRate != 0.0134 || short.CacheCreationRate != 0.1668 {
		t.Fatalf("short: %+v; want base", short)
	}
	long := types.ApplyLongContextCreditRate(base, 300_000)
	const eps = 1e-9
	if math.Abs(long.InputRate-0.2668) > eps || math.Abs(long.OutputRate-1.2) > eps ||
		math.Abs(long.CacheReadRate-0.0268) > eps || math.Abs(long.CacheCreationRate-0.3336) > eps {
		t.Fatalf("long: %+v; want 2x/1.5x", long)
	}
}
