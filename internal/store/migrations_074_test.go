package store

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestMigration074_DeepSeekV41FlashPricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	m, err := st.GetModelByName("deepseek-v4.1-flash")
	if err != nil || m == nil {
		t.Fatalf("get versioned model: model=%v err=%v", m, err)
	}
	if m.DisplayName != "DeepSeek V4.1 Flash" || m.Publisher != "deepseek" || m.Status != types.ModelStatusActive ||
		m.Metadata.ContextWindow != 1_000_000 || !slices.Contains(m.Metadata.Capabilities, "vision") {
		t.Fatalf("unexpected model metadata: %+v", m)
	}
	if len(m.Aliases) != 0 {
		t.Fatalf("unexpected model aliases: %v", m.Aliases)
	}
	assertDeepSeekV41FlashRates(t, m.DefaultCreditRate, 1)

	rows, err := st.pool.Query(ctx, `SELECT slug, model_credit_rates->'deepseek-v4.1-flash' FROM plans`)
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
		assertDeepSeekV41FlashRates(t, &rate, 0.066)
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no plans checked")
	}
}

// Compare the stored rates with the published busy-hour CNY prices, allowing
// only the documented rounding. This also catches USD conversion or use of
// idle-hour prices in either the catalog or subscription rates.
func assertDeepSeekV41FlashRates(t *testing.T, rate *types.CreditRate, multiplier float64) {
	t.Helper()
	if rate == nil {
		t.Fatal("missing credit rate")
	}
	cachePrecision := 1e6
	if multiplier == 0.066 {
		cachePrecision = 1e7
	}
	want := types.CreditRate{
		InputRate:     math.Round(2/54.38*multiplier*1e6) / 1e6,
		OutputRate:    math.Round(8/54.38*multiplier*1e6) / 1e6,
		CacheReadRate: math.Round(0.04/54.38*multiplier*cachePrecision) / cachePrecision,
	}
	if *rate != want {
		t.Fatalf("rate=%+v, want %+v (official CNY prices * %v / 54.38)", *rate, want, multiplier)
	}
}

func TestMigration074_PreservesOverridesAndReruns(t *testing.T) {
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
		  INSERT INTO users (email) VALUES ('migration074-' || gen_random_uuid()::text || '@test.local') RETURNING id
		)
		INSERT INTO projects (name, created_by) SELECT 'migration074', id FROM owner RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}

	// Exercise both tables even on a fresh database with no existing policies.
	// Include an unrelated model to detect accidental replacement of rate maps.
	cases := []struct {
		name    string
		initial any
	}{
		{"null", nil},
		{"empty", `{}`},
		{"other", `{"deepseek-v4-flash":{"input_rate":123}}`},
		{"custom", `{"deepseek-v4.1-flash":{"input_rate":42,"output_rate":43},"deepseek-v4-flash":{"input_rate":123}}`},
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
				err = tx.QueryRow(ctx, `INSERT INTO plans (name, slug, model_credit_rates)
					VALUES ($1, 'migration074-' || gen_random_uuid()::text, $2::jsonb) RETURNING id`, tc.name, tc.initial).Scan(&id)
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
	migration, err := migrationsFS.ReadFile("migrations/074_add_deepseek_v4_1_flash.sql")
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
		for _, f := range fixtures {
			var raw []byte
			if err := tx.QueryRow(ctx, `SELECT model_credit_rates FROM `+f.table+` WHERE id = $1`, f.id).Scan(&raw); err != nil {
				t.Fatal(err)
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
			if _, custom := want["deepseek-v4.1-flash"]; !custom {
				rate := got["deepseek-v4.1-flash"]
				assertDeepSeekV41FlashRates(t, &rate, 0.066)
				want["deepseek-v4.1-flash"] = rate
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s pass %d: got %v, want %v", f.table, pass, got, want)
			}
		}
		var aliases []string
		if err := tx.QueryRow(ctx, `SELECT aliases FROM models WHERE name = 'deepseek-v4.1-flash'`).Scan(&aliases); err != nil {
			t.Fatal(err)
		}
		if len(aliases) != 0 {
			t.Fatalf("aliases after pass %d: %v", pass, aliases)
		}
	}
}
