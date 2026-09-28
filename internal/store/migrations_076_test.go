package store

import (
	"context"
	"math"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

// Run the actual migration against a temporary pre-migration table, leaving
// permanent routes untouched. WebSocket must be opt-in, never backfilled.
func TestMigration076ResponsesWebsocketKind(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `
		CREATE TEMP TABLE routes (
			id INT PRIMARY KEY,
			request_kinds TEXT[] NOT NULL,
			CONSTRAINT routes_request_kinds_valid CHECK (
				request_kinds <@ ARRAY['openai_responses', 'openai_responses_compact']::TEXT[]
			)
		) ON COMMIT DROP;
		INSERT INTO routes VALUES (1, ARRAY['openai_responses']);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := migrationsFS.ReadFile("migrations/076_responses_websocket_request_kind.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration run %d: %v", i+1, err)
		}
	}
	var kinds []string
	if err := tx.QueryRow(ctx, "SELECT request_kinds FROM routes WHERE id = 1").Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if !equalStringSlices(kinds, []string{types.KindOpenAIResponses}) {
		t.Fatalf("existing HTTP route was changed: %v", kinds)
	}
	for i, kind := range types.AllRequestKinds {
		if _, err := tx.Exec(ctx, "INSERT INTO routes VALUES ($1, $2)", i+2, []string{kind}); err != nil {
			t.Fatalf("kind %s rejected by SQL: %v", kind, err)
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO routes VALUES (100, $1)", []string{types.KindOpenAIResponses, types.KindOpenAIResponsesWebsocket}); err != nil {
		t.Fatalf("explicit combined route rejected: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO routes VALUES (101, ARRAY['unknown_kind'])"); err == nil {
		t.Fatal("unknown request kind accepted")
	}
}

// TestMigration076_GPT6SolLunaPricing verifies the official OpenAI prices in
// catalog credit units and the matching 0.4x subscription/policy rates.
func TestMigration076_GPT6SolLunaPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	cases := []struct {
		name                                   string
		catalogInput, catalogOutput            float64
		catalogCacheCreation, catalogCacheRead float64
		planInput, planOutput                  float64
		planCacheCreation, planCacheRead       float64
	}{
		{"gpt-6-sol", 0.267, 1.333, 0.333, 0.027, 0.1068, 0.5332, 0.1332, 0.0108},
		{"gpt-6-luna", 0.013, 0.067, 0.017, 0.001, 0.0052, 0.0268, 0.0068, 0.0004},
	}

	for _, tc := range cases {
		m, err := st.GetModelByName(tc.name)
		if err != nil {
			t.Fatalf("get %s: %v", tc.name, err)
		}
		if m == nil || m.DisplayName == "" {
			t.Fatalf("%s catalog row is missing", tc.name)
		}
		if m.Status != types.ModelStatusActive || m.Publisher != "openai" {
			t.Fatalf("%s metadata: status=%q publisher=%q; want active/openai", tc.name, m.Status, m.Publisher)
		}
		if m.Metadata.ContextWindow != 1_050_000 || m.Metadata.Category != "chat" {
			t.Fatalf("%s metadata: %+v; want context_window=1050000 category=chat", tc.name, m.Metadata)
		}
		if m.DefaultCreditRate == nil {
			t.Fatalf("%s has no catalog credit rate", tc.name)
		}
		assertGPT6SolLunaRate(t, tc.name+" catalog", m.DefaultCreditRate,
			tc.catalogInput, tc.catalogOutput, tc.catalogCacheCreation, tc.catalogCacheRead)
		if m.DefaultCreditRate.LongContext == nil ||
			m.DefaultCreditRate.LongContext.ThresholdInputTokens != 272000 ||
			m.DefaultCreditRate.LongContext.InputMultiplier != 2.0 ||
			m.DefaultCreditRate.LongContext.OutputMultiplier != 1.5 {
			t.Fatalf("%s catalog long_context: %+v", tc.name, m.DefaultCreditRate.LongContext)
		}

		var pi, po, pcc, pcr float64
		var hasLongContext bool
		err = st.pool.QueryRow(ctx, `
			SELECT
			  (model_credit_rates->$1->>'input_rate')::float8,
			  (model_credit_rates->$1->>'output_rate')::float8,
			  (model_credit_rates->$1->>'cache_creation_rate')::float8,
			  (model_credit_rates->$1->>'cache_read_rate')::float8,
			  model_credit_rates->$1 ? 'long_context'
			FROM plans WHERE slug = 'pro'`, tc.name).
			Scan(&pi, &po, &pcc, &pcr, &hasLongContext)
		if err != nil {
			t.Fatalf("query pro %s rate: %v", tc.name, err)
		}
		assertGPT6SolLunaRate(t, tc.name+" plan", &types.CreditRate{
			InputRate: pi, OutputRate: po, CacheCreationRate: pcc, CacheReadRate: pcr,
		}, tc.planInput, tc.planOutput, tc.planCacheCreation, tc.planCacheRead)
		if !hasLongContext {
			t.Fatalf("%s plan rate has no long_context block", tc.name)
		}
	}

	var policyCount, missing int
	if err := st.pool.QueryRow(ctx, `SELECT COUNT(*) FROM rate_limit_policies`).Scan(&policyCount); err != nil {
		t.Fatalf("count rate-limit policies: %v", err)
	}
	if policyCount > 0 {
		if err := st.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM rate_limit_policies
			WHERE NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-sol')
			   OR NOT (COALESCE(model_credit_rates, '{}'::jsonb) ? 'gpt-6-luna')`).Scan(&missing); err != nil {
			t.Fatalf("count policies missing GPT-6 rates: %v", err)
		}
		if missing != 0 {
			t.Fatalf("%d rate-limit policy row(s) missing GPT-6 rates", missing)
		}
	}
}

func assertGPT6SolLunaRate(t *testing.T, label string, got *types.CreditRate, input, output, cacheCreation, cacheRead float64) {
	t.Helper()
	if math.Abs(got.InputRate-input) > 1e-9 || math.Abs(got.OutputRate-output) > 1e-9 ||
		math.Abs(got.CacheCreationRate-cacheCreation) > 1e-9 || math.Abs(got.CacheReadRate-cacheRead) > 1e-9 {
		t.Fatalf("%s rates: %+v; want input=%v output=%v cache_creation=%v cache_read=%v",
			label, *got, input, output, cacheCreation, cacheRead)
	}
}
