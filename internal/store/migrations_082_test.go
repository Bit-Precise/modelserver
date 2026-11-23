package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

var glm54CatalogRate = types.CreditRate{
	InputRate:         0.187,
	OutputRate:        0.587,
	CacheCreationRate: 0,
	CacheReadRate:     0.035,
}

var glm54PlanRate = types.CreditRate{
	InputRate:         0.0187,
	OutputRate:        0.0587,
	CacheCreationRate: 0,
	CacheReadRate:     0.0035,
}

func TestMigration082_GLM54CatalogAndPlans(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	m, err := st.GetModelByName("glm-5.4")
	if err != nil || m == nil {
		t.Fatalf("get glm-5.4: model=%v err=%v", m, err)
	}
	if m.DisplayName != "GLM-5.4" || m.Publisher != "zhipu" || m.Status != types.ModelStatusActive {
		t.Fatalf("unexpected model metadata: %+v", m)
	}
	if m.Metadata.ContextWindow != 1_000_000 || m.Metadata.Category != "chat" {
		t.Fatalf("unexpected GLM-5.4 metadata: %+v", m.Metadata)
	}
	if !reflect.DeepEqual(m.DefaultCreditRate, &glm54CatalogRate) {
		t.Fatalf("catalog rate=%+v, want %+v", m.DefaultCreditRate, glm54CatalogRate)
	}

	rows, err := st.pool.Query(ctx, `SELECT slug, model_credit_rates->$1 FROM plans`, "glm-5.4")
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
		var got types.CreditRate
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("plan %s rate is invalid: %v", slug, err)
		}
		if !reflect.DeepEqual(got, glm54PlanRate) {
			t.Fatalf("plan %s rate=%+v, want %+v", slug, got, glm54PlanRate)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count == 0 {
		t.Fatal("no plans checked")
	}
}

func TestMigration082_PreservesPlanAndPolicyOverrides(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var planRate []byte
	if err := tx.QueryRow(ctx, `
		INSERT INTO plans (name, slug, model_credit_rates)
		VALUES ('migration082 override', 'migration082-' || gen_random_uuid()::text,
		        '{"glm-5.4":{"input_rate":42,"output_rate":43}}'::jsonb)
		RETURNING model_credit_rates->'glm-5.4'`).Scan(&planRate); err != nil {
		t.Fatal(err)
	}
	var gotPlan types.CreditRate
	if err := json.Unmarshal(planRate, &gotPlan); err != nil {
		t.Fatal(err)
	}
	if gotPlan.InputRate != 42 || gotPlan.OutputRate != 43 {
		t.Fatalf("operator plan override changed before migration: %+v", gotPlan)
	}

	var projectID string
	if err := tx.QueryRow(ctx, `
		WITH owner AS (
		  INSERT INTO users (email) VALUES ('migration082-' || gen_random_uuid()::text || '@test.local') RETURNING id
		)
		INSERT INTO projects (name, created_by) SELECT 'migration082', id FROM owner RETURNING id`).Scan(&projectID); err != nil {
		t.Fatal(err)
	}
	var policyRate []byte
	if err := tx.QueryRow(ctx, `
		INSERT INTO rate_limit_policies (project_id, name, model_credit_rates)
		VALUES ($1, 'migration082 override', '{"glm-5.4":{"input_rate":52,"output_rate":53}}'::jsonb)
		RETURNING model_credit_rates->'glm-5.4'`, projectID).Scan(&policyRate); err != nil {
		t.Fatal(err)
	}
	var gotPolicy types.CreditRate
	if err := json.Unmarshal(policyRate, &gotPolicy); err != nil {
		t.Fatal(err)
	}
	if gotPolicy.InputRate != 52 || gotPolicy.OutputRate != 53 {
		t.Fatalf("operator policy override changed before migration: %+v", gotPolicy)
	}

	migration, err := migrationsFS.ReadFile("migrations/082_add_glm_5_4.sql")
	if err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		if _, err := tx.Exec(ctx, string(migration)); err != nil {
			t.Fatalf("migration pass %d: %v", pass, err)
		}
		if err := tx.QueryRow(ctx, `SELECT model_credit_rates->'glm-5.4' FROM plans WHERE slug LIKE 'migration082-%'`).Scan(&planRate); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(planRate, &gotPlan); err != nil {
			t.Fatal(err)
		}
		if gotPlan.InputRate != 42 || gotPlan.OutputRate != 43 {
			t.Fatalf("plan override changed on migration pass %d: %+v", pass, gotPlan)
		}
		if err := tx.QueryRow(ctx, `SELECT model_credit_rates->'glm-5.4' FROM rate_limit_policies WHERE name = 'migration082 override'`).Scan(&policyRate); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(policyRate, &gotPolicy); err != nil {
			t.Fatal(err)
		}
		if gotPolicy.InputRate != 52 || gotPolicy.OutputRate != 53 {
			t.Fatalf("policy override changed on migration pass %d: %+v", pass, gotPolicy)
		}
	}
}
