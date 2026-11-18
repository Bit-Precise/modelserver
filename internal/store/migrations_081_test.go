package store

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

var gpt62Models = []struct {
	name, displayName string
	inputUSD          float64
}{
	{"gpt-6.2-sol", "GPT-6.2 Sol", 2},
	{"gpt-6.2-astra", "GPT-6.2 Astra", 8},
}

// Derive expectations from standard USD prices, rounding the catalog first
// and then applying the subscription multiplier, as for the earlier GPTs.
func gpt62Rate(inputUSD, multiplier float64) types.CreditRate {
	convert := func(usd float64) float64 {
		catalog := math.Round(usd/7.5*1000) / 1000
		return math.Round(catalog*multiplier*10000) / 10000
	}
	return types.CreditRate{
		InputRate:         convert(inputUSD),
		OutputRate:        convert(inputUSD * 5),
		CacheCreationRate: convert(inputUSD * 1.25),
		CacheReadRate:     convert(inputUSD * 0.05),
		LongContext: &types.LongContextCreditRate{
			ThresholdInputTokens: 272000,
			InputMultiplier:      2,
			OutputMultiplier:     1.5,
		},
	}
}

func TestMigration081_GPT62Pricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	for _, tc := range gpt62Models {
		t.Run(tc.name, func(t *testing.T) {
			m, err := st.GetModelByName(tc.name)
			if err != nil || m == nil {
				t.Fatalf("get model: model=%v err=%v", m, err)
			}
			if m.DisplayName != tc.displayName || m.Publisher != "openai" || m.Status != types.ModelStatusActive ||
				m.Metadata.ContextWindow != 1_050_000 || m.Metadata.Category != "chat" || len(m.Aliases) != 0 {
				t.Fatalf("unexpected model metadata: %+v", m)
			}
			wantCatalog := gpt62Rate(tc.inputUSD, 1)
			if !reflect.DeepEqual(m.DefaultCreditRate, &wantCatalog) {
				t.Fatalf("catalog rate=%+v, want %+v", m.DefaultCreditRate, wantCatalog)
			}

			rows, err := st.pool.Query(ctx, `SELECT slug, model_credit_rates->$1 FROM plans`, tc.name)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			count := 0
			wantPlan := gpt62Rate(tc.inputUSD, 0.4)
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
				if !reflect.DeepEqual(rate, wantPlan) {
					t.Fatalf("plan %s rate=%+v, want %+v", slug, rate, wantPlan)
				}
				count++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if count == 0 {
				t.Fatal("no plans checked")
			}
		})
	}
}

func TestMigration081_PreservesOverridesAndReruns(t *testing.T) {
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
		  INSERT INTO users (email) VALUES ('migration081-' || gen_random_uuid()::text || '@test.local') RETURNING id
		)
		INSERT INTO projects (name, created_by) SELECT 'migration081', id FROM owner RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range gpt62Models {
		if _, err := tx.Exec(ctx, `UPDATE models SET aliases = ARRAY[$2], default_credit_rate = NULL WHERE name = $1`, tc.name, "operator-"+tc.name); err != nil {
			t.Fatal(err)
		}
	}

	// Exercise real policies even on a fresh database, plus partial overrides:
	// an override for one new model must not prevent seeding the other model.
	const overlay = `{"codex-cli":{"gpt-6.2-sol":{"input_rate":9},"gpt-6.2-astra":{"input_rate":10}}}`
	cases := []struct {
		name    string
		initial any
	}{
		{"null", nil},
		{"empty", `{}`},
		{"other", `{"gpt-6.1-sol":{"input_rate":123}}`},
		{"sol", `{"gpt-6.2-sol":{"input_rate":42,"output_rate":43}}`},
		{"astra", `{"gpt-6.2-astra":{"input_rate":44,"output_rate":45}}`},
		{"both", `{"gpt-6.2-sol":{"input_rate":42},"gpt-6.2-astra":{"input_rate":44},"_default":{"input_rate":123}}`},
	}
	type fixture struct {
		table, id string
		want      map[string]types.CreditRate
	}
	var fixtures []fixture
	for _, tc := range cases {
		want := map[string]types.CreditRate{}
		if tc.initial != nil {
			if err := json.Unmarshal([]byte(tc.initial.(string)), &want); err != nil {
				t.Fatal(err)
			}
		}
		for _, model := range gpt62Models {
			if _, exists := want[model.name]; !exists {
				want[model.name] = gpt62Rate(model.inputUSD, 0.4)
			}
		}
		for _, table := range []string{"plans", "rate_limit_policies"} {
			var id string
			if table == "plans" {
				err = tx.QueryRow(ctx, `INSERT INTO plans (name, slug, model_credit_rates, client_model_credit_rates)
					VALUES ($1, 'migration081-' || gen_random_uuid()::text, $2::jsonb, $3::jsonb) RETURNING id`, tc.name, tc.initial, overlay).Scan(&id)
			} else {
				err = tx.QueryRow(ctx, `INSERT INTO rate_limit_policies (project_id, name, model_credit_rates)
					VALUES ($1, $2, $3::jsonb) RETURNING id`, projectID, tc.name, tc.initial).Scan(&id)
			}
			if err != nil {
				t.Fatalf("seed %s/%s: %v", table, tc.name, err)
			}
			fixtures = append(fixtures, fixture{table, id, want})
		}
	}
	migration, err := migrationsFS.ReadFile("migrations/081_add_gpt_6_2_sol_astra.sql")
	if err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
		for _, tc := range gpt62Models {
			var aliases []string
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT aliases, default_credit_rate FROM models WHERE name = $1`, tc.name).Scan(&aliases, &raw); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(aliases, []string{"operator-" + tc.name}) {
				t.Fatalf("%s pass %d: operator alias changed: %v", tc.name, pass, aliases)
			}
			var rate types.CreditRate
			if err := json.Unmarshal(raw, &rate); err != nil {
				t.Fatal(err)
			}
			if want := gpt62Rate(tc.inputUSD, 1); !reflect.DeepEqual(rate, want) {
				t.Fatalf("%s pass %d: catalog rate=%+v, want %+v", tc.name, pass, rate, want)
			}
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
			if !reflect.DeepEqual(got, f.want) {
				t.Fatalf("%s pass %d: got %v, want %v", f.table, pass, got, f.want)
			}
		}
	}
}
