package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// migration058Plans holds the exact scalar fields each new plan must carry
// after migration 058 runs. credit_rules are asserted separately since they
// are jsonb.
var migration058Plans = map[string]struct {
	Name          string
	DisplayName   string
	Description   string
	TierLevel     int64
	PriceCNYFen   int64
	PriceUSDCents int64
	PeriodMonths  int64
	Credit5h      int64
	Credit7d      int64
}{
	"mini": {
		Name: "Mini", DisplayName: "Mini",
		Description:   "Half of Pro's usage limits",
		TierLevel:     50,
		PriceCNYFen:   5999,
		PriceUSDCents: 1000,
		PeriodMonths:  1,
		Credit5h:      275000,
		Credit7d:      2500000,
	},
	"nano": {
		Name: "Nano", DisplayName: "Nano",
		Description:   "Quarter of Pro's usage limits",
		TierLevel:     25,
		PriceCNYFen:   2999,
		PriceUSDCents: 500,
		PeriodMonths:  1,
		Credit5h:      137500,
		Credit7d:      1250000,
	},
}

// TestMigration058_PlanRowsPresent asserts the two new plan rows exist with
// the expected scalar fields and credit_rules windows.
func TestMigration058_PlanRowsPresent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration058Plans {
		var (
			name, displayName, description string
			tierLevel, priceCNYFen, priceUSDCents, periodMonths int64
			creditRulesJSON []byte
		)
		err := st.pool.QueryRow(ctx, `
			SELECT name, display_name, description, tier_level,
			       price_cny_fen, price_usd_cents, period_months,
			       credit_rules
			FROM plans WHERE slug = $1`, slug).
			Scan(&name, &displayName, &description, &tierLevel,
				&priceCNYFen, &priceUSDCents, &periodMonths, &creditRulesJSON)
		if err != nil {
			t.Fatalf("query slug %s: %v", slug, err)
		}

		if name != want.Name {
			t.Errorf("slug %s: name = %q, want %q", slug, name, want.Name)
		}
		if displayName != want.DisplayName {
			t.Errorf("slug %s: display_name = %q, want %q", slug, displayName, want.DisplayName)
		}
		if description != want.Description {
			t.Errorf("slug %s: description = %q, want %q", slug, description, want.Description)
		}
		if tierLevel != want.TierLevel {
			t.Errorf("slug %s: tier_level = %d, want %d", slug, tierLevel, want.TierLevel)
		}
		if priceCNYFen != want.PriceCNYFen {
			t.Errorf("slug %s: price_cny_fen = %d, want %d", slug, priceCNYFen, want.PriceCNYFen)
		}
		if priceUSDCents != want.PriceUSDCents {
			t.Errorf("slug %s: price_usd_cents = %d, want %d", slug, priceUSDCents, want.PriceUSDCents)
		}
		if periodMonths != want.PeriodMonths {
			t.Errorf("slug %s: period_months = %d, want %d", slug, periodMonths, want.PeriodMonths)
		}

		// credit_rules: a two-element array; assert window + max_credits on each.
		var rules []struct {
			Window     string `json:"window"`
			WindowType string `json:"window_type"`
			MaxCredits int64  `json:"max_credits"`
			Scope      string `json:"scope"`
		}
		if err := json.Unmarshal(creditRulesJSON, &rules); err != nil {
			t.Fatalf("slug %s: unmarshal credit_rules: %v", slug, err)
		}
		if len(rules) != 2 {
			t.Fatalf("slug %s: got %d credit_rules, want 2", slug, len(rules))
		}
		if rules[0].Window != "5h" || rules[0].WindowType != "sliding" ||
			rules[0].Scope != "project" || rules[0].MaxCredits != want.Credit5h {
			t.Errorf("slug %s: 5h rule = %+v, want window=5h window_type=sliding scope=project max_credits=%d",
				slug, rules[0], want.Credit5h)
		}
		if rules[1].Window != "7d" || rules[1].WindowType != "sliding" ||
			rules[1].Scope != "project" || rules[1].MaxCredits != want.Credit7d {
			t.Errorf("slug %s: 7d rule = %+v, want window=7d window_type=sliding scope=project max_credits=%d",
				slug, rules[1], want.Credit7d)
		}
	}
}

// TestMigration058_ModelRatesClonedFromPro asserts model_credit_rates on
// mini and nano exactly match pro's map at migration time. This locks in
// the "clone from pro" contract stated in the migration's own comment.
func TestMigration058_ModelRatesClonedFromPro(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var proRates []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT model_credit_rates FROM plans WHERE slug = 'pro'`).Scan(&proRates); err != nil {
		t.Fatalf("read pro rates: %v", err)
	}

	var proMap map[string]any
	if err := json.Unmarshal(proRates, &proMap); err != nil {
		t.Fatalf("unmarshal pro rates: %v", err)
	}

	for _, slug := range []string{"mini", "nano"} {
		var raw []byte
		if err := st.pool.QueryRow(ctx,
			`SELECT model_credit_rates FROM plans WHERE slug = $1`, slug).Scan(&raw); err != nil {
			t.Fatalf("read %s rates: %v", slug, err)
		}
		var got map[string]any
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal %s rates: %v", slug, err)
		}
		if !reflect.DeepEqual(got, proMap) {
			t.Errorf("slug %s: model_credit_rates does not match pro exactly", slug)
		}
	}
}
