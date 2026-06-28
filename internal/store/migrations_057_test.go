package store

import (
	"context"
	"encoding/json"
	"testing"
)

func TestMigration057_AddsPlanClientRatesColumnNullByDefault(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var planID string
	if err := st.pool.QueryRow(ctx, `
		INSERT INTO plans (name, slug, display_name, description, tier_level,
		    price_cny_fen, period_months, is_active)
		VALUES ('mig057-test', 'mig057-test', 'Migration 057 Test', '', 0,
		        0, 1, FALSE)
		RETURNING id`).Scan(&planID); err != nil {
		t.Fatalf("seed old-style plan: %v", err)
	}

	var raw []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT client_model_credit_rates FROM plans WHERE id = $1`, planID).
		Scan(&raw); err != nil {
		t.Fatalf("select new column: %v", err)
	}
	if raw != nil {
		t.Errorf("default client_model_credit_rates = %q, want NULL", raw)
	}

	want := map[string]map[string]map[string]float64{
		"claude-code-cli": {"claude-sonnet-4": {"input_rate": 3, "output_rate": 15}},
		"codex-cli":       {"gpt-5": {"input_rate": 0.5, "output_rate": 4}},
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal want: %v", err)
	}
	if _, err := st.pool.Exec(ctx,
		`UPDATE plans SET client_model_credit_rates = $1 WHERE id = $2`,
		wantJSON, planID); err != nil {
		t.Fatalf("populate column: %v", err)
	}
	if err := st.pool.QueryRow(ctx,
		`SELECT client_model_credit_rates FROM plans WHERE id = $1`, planID).
		Scan(&raw); err != nil {
		t.Fatalf("select populated column: %v", err)
	}
	var got map[string]map[string]map[string]float64
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}
	for client, models := range want {
		for model, rates := range models {
			for field, v := range rates {
				if got[client][model][field] != v {
					t.Errorf("got[%q][%q][%q] = %v, want %v",
						client, model, field, got[client][model][field], v)
				}
			}
		}
	}
}

func TestMigration057_Idempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.pool.Exec(ctx,
		`ALTER TABLE plans ADD COLUMN IF NOT EXISTS client_model_credit_rates JSONB`); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}
}
