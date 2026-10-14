package store

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestMigration077_GPT61SolPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m, err := st.GetModelByName("gpt-6.1-sol")
	if err != nil || m == nil {
		t.Fatalf("get gpt-6.1-sol: model=%v err=%v", m, err)
	}
	if m.DisplayName != "GPT-6.1 Sol" || m.Publisher != "openai" || m.Status != types.ModelStatusActive ||
		m.Metadata.ContextWindow != 1_050_000 || m.Metadata.Category != "chat" || len(m.Aliases) != 0 {
		t.Fatalf("unexpected model metadata: %+v", m)
	}
	assertGPT61SolRate(t, m.DefaultCreditRate, 1)

	rows, err := st.pool.Query(ctx, `SELECT slug, model_credit_rates->'gpt-6.1-sol' FROM plans`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var slug string
		var raw []byte
		if err := rows.Scan(&slug, &raw); err != nil {
			t.Fatal(err)
		}
		var rate types.CreditRate
		if err := json.Unmarshal(raw, &rate); err != nil {
			t.Fatalf("plan %s missing valid rate: %v", slug, err)
		}
		assertGPT61SolRate(t, &rate, 0.4)
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no plans checked")
	}
}

// Derive expectations from the official standard USD prices using the GPT
// convention: round the catalog to 3 dp, then apply the subscription discount.
// In particular, GPT-6.1 Sol's cache-read price is $0.10, not GPT-6 Sol's $0.20.
func assertGPT61SolRate(t *testing.T, rate *types.CreditRate, multiplier float64) {
	t.Helper()
	convert := func(usd float64) float64 {
		catalog := math.Round(usd/7.5*1000) / 1000
		return math.Round(catalog*multiplier*10000) / 10000
	}
	want := &types.CreditRate{
		InputRate:         convert(2),
		OutputRate:        convert(10),
		CacheCreationRate: convert(2.5),
		CacheReadRate:     convert(0.1),
		LongContext: &types.LongContextCreditRate{
			ThresholdInputTokens: 272000,
			InputMultiplier:      2,
			OutputMultiplier:     1.5,
		},
	}
	if !reflect.DeepEqual(rate, want) {
		t.Fatalf("rate=%+v, want %+v (official USD prices / 7.5, rounded, then * %v)", rate, want, multiplier)
	}
}

func TestMigration077_PreservesOverridesAndReruns(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var projectID string
	if err := tx.QueryRow(ctx, `
		WITH owner AS (
		  INSERT INTO users (email) VALUES ('migration077-' || gen_random_uuid()::text || '@test.local') RETURNING id
		)
		INSERT INTO projects (name, created_by) SELECT 'migration077', id FROM owner RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}

	// Exercise actual policies even on a fresh database. Verify null/empty maps
	// are seeded and existing model rates and client-specific overlays survive.
	const overlay = `{"codex-cli":{"gpt-6.1-sol":{"input_rate":9}}}`
	cases := []struct {
		name    string
		initial any
	}{
		{"null", nil},
		{"empty", `{}`},
		{"other", `{"gpt-6-sol":{"input_rate":123}}`},
		{"custom", `{"gpt-6.1-sol":{"input_rate":42,"output_rate":43},"gpt-6-sol":{"input_rate":123}}`},
	}
	type fixture struct {
		table, id string
		initial   any
	}
	var fixtures []fixture
	for _, tc := range cases {
		for _, table := range []string{"plans", "rate_limit_policies"} {
			var id string
			if table == "plans" {
				err = tx.QueryRow(ctx, `INSERT INTO plans (name, slug, model_credit_rates, client_model_credit_rates)
					VALUES ($1, 'migration077-' || gen_random_uuid()::text, $2::jsonb, $3::jsonb) RETURNING id`, tc.name, tc.initial, overlay).Scan(&id)
			} else {
				err = tx.QueryRow(ctx, `INSERT INTO rate_limit_policies (project_id, name, model_credit_rates)
					VALUES ($1, $2, $3::jsonb) RETURNING id`, projectID, tc.name, tc.initial).Scan(&id)
			}
			if err != nil {
				t.Fatalf("seed %s/%s: %v", table, tc.name, err)
			}
			fixtures = append(fixtures, fixture{table, id, tc.initial})
		}
	}
	migration, err := migrationsFS.ReadFile("migrations/077_add_gpt_6_1_sol.sql")
	if err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
		for _, f := range fixtures {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT model_credit_rates FROM `+f.table+` WHERE id = $1`, f.id).Scan(&raw); err != nil {
				t.Fatal(err)
			}
			if f.table == "plans" {
				var overlayUnchanged bool
				if err := tx.QueryRow(ctx, `SELECT client_model_credit_rates = $2::jsonb FROM plans WHERE id = $1`, f.id, overlay).
					Scan(&overlayUnchanged); err != nil {
					t.Fatal(err)
				}
				if !overlayUnchanged {
					t.Fatalf("plan pass %d: client-specific overlay changed", pass)
				}
			}
			var got map[string]types.CreditRate
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]types.CreditRate{}
			if f.initial != nil {
				if err := json.Unmarshal([]byte(f.initial.(string)), &want); err != nil {
					t.Fatal(err)
				}
			}
			if _, custom := want["gpt-6.1-sol"]; !custom {
				rate := got["gpt-6.1-sol"]
				assertGPT61SolRate(t, &rate, 0.4)
				want["gpt-6.1-sol"] = rate
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s pass %d: got %v, want %v", f.table, pass, got, want)
			}
		}
	}
}
