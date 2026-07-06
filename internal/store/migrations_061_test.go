package store

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

// migration061Plans holds the exact scalar fields each new max_Nx plan
// must carry after migration 061 runs.
var migration061Plans = map[string]struct {
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
	"max_140x": {
		Name: "Max 140x", DisplayName: "Max 140x",
		Description:   "Same usage limits as Claude Max (140x)",
		TierLevel:     14000,
		PriceCNYFen:   979999,
		PriceUSDCents: 140000,
		PeriodMonths:  1,
		Credit5h:      77000000,
		Credit7d:      583333100,
	},
	"max_160x": {
		Name: "Max 160x", DisplayName: "Max 160x",
		Description:   "Same usage limits as Claude Max (160x)",
		TierLevel:     16000,
		PriceCNYFen:   1119999,
		PriceUSDCents: 160000,
		PeriodMonths:  1,
		Credit5h:      88000000,
		Credit7d:      666666400,
	},
	"max_180x": {
		Name: "Max 180x", DisplayName: "Max 180x",
		Description:   "Same usage limits as Claude Max (180x)",
		TierLevel:     18000,
		PriceCNYFen:   1259999,
		PriceUSDCents: 180000,
		PeriodMonths:  1,
		Credit5h:      99000000,
		Credit7d:      749999700,
	},
	"max_220x": {
		Name: "Max 220x", DisplayName: "Max 220x",
		Description:   "Same usage limits as Claude Max (220x)",
		TierLevel:     22000,
		PriceCNYFen:   1539999,
		PriceUSDCents: 220000,
		PeriodMonths:  1,
		Credit5h:      121000000,
		Credit7d:      916666300,
	},
}

// TestMigration061_NewMaxPlansPresent asserts the four new plan rows exist
// with the expected scalar fields, credit_rules windows, and is_active=true.
func TestMigration061_NewMaxPlansPresent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	for slug, want := range migration061Plans {
		var (
			name, displayName, description                      string
			tierLevel, priceCNYFen, priceUSDCents, periodMonths int64
			creditRulesJSON                                     []byte
			isActive                                            bool
		)
		err := st.pool.QueryRow(ctx, `
			SELECT name, display_name, description, tier_level,
			       price_cny_fen, price_usd_cents, period_months,
			       credit_rules, is_active
			FROM plans WHERE slug = $1`, slug).
			Scan(&name, &displayName, &description, &tierLevel,
				&priceCNYFen, &priceUSDCents, &periodMonths, &creditRulesJSON, &isActive)
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
		if !isActive {
			t.Errorf("slug %s: is_active = false, want true", slug)
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

// TestMigration061_NewMaxPlansCloneRatesFromPro asserts each new tier's
// model_credit_rates AND client_model_credit_rates deep-equal pro's. Same
// shape as TestMigration059_ModelRatesClonedFromPro, extended to four slugs.
func TestMigration061_NewMaxPlansCloneRatesFromPro(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// pro reference values.
	var proRates []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT model_credit_rates FROM plans WHERE slug = 'pro'`).Scan(&proRates); err != nil {
		t.Fatalf("read pro rates: %v", err)
	}
	var proMap map[string]any
	if err := json.Unmarshal(proRates, &proMap); err != nil {
		t.Fatalf("unmarshal pro rates: %v", err)
	}

	var proClient []byte
	if err := st.pool.QueryRow(ctx,
		`SELECT client_model_credit_rates FROM plans WHERE slug = 'pro'`).Scan(&proClient); err != nil {
		t.Fatalf("read pro client rates: %v", err)
	}
	var proClientMap map[string]any
	if proClient != nil {
		if err := json.Unmarshal(proClient, &proClientMap); err != nil {
			t.Fatalf("unmarshal pro client rates: %v", err)
		}
	}

	for _, slug := range []string{"max_140x", "max_160x", "max_180x", "max_220x"} {
		// model_credit_rates
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

		// client_model_credit_rates (may be NULL on all; both-NULL counts equal).
		var rawClient []byte
		if err := st.pool.QueryRow(ctx,
			`SELECT client_model_credit_rates FROM plans WHERE slug = $1`, slug).Scan(&rawClient); err != nil {
			t.Fatalf("read %s client rates: %v", slug, err)
		}
		if (rawClient == nil) != (proClient == nil) {
			t.Errorf("slug %s: client_model_credit_rates NULL-ness differs from pro (got nil=%v, pro nil=%v)",
				slug, rawClient == nil, proClient == nil)
			continue
		}
		if rawClient == nil {
			continue // both NULL — equal
		}
		var gotClient map[string]any
		if err := json.Unmarshal(rawClient, &gotClient); err != nil {
			t.Fatalf("unmarshal %s client rates: %v", slug, err)
		}
		if !reflect.DeepEqual(gotClient, proClientMap) {
			t.Errorf("slug %s: client_model_credit_rates does not match pro exactly", slug)
		}
	}
}
