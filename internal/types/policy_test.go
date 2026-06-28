package types

import (
	"math"
	"testing"
	"time"
)

func TestComputeCredits(t *testing.T) {
	policy := &RateLimitPolicy{
		ModelCreditRates: map[string]CreditRate{
			"claude-opus-4": {
				InputRate:         0.667,
				OutputRate:        3.333,
				CacheCreationRate: 0.667,
				CacheReadRate:     0,
			},
			"_default": {
				InputRate:         0.4,
				OutputRate:        2.0,
				CacheCreationRate: 0.4,
				CacheReadRate:     0,
			},
		},
	}

	tests := []struct {
		name     string
		model    string
		in, out  int64
		cacheW   int64
		cacheR   int64
		expected float64
	}{
		{
			name:     "opus with all token types",
			model:    "claude-opus-4",
			in:       1000,
			out:      500,
			cacheW:   200,
			cacheR:   100,
			expected: 0.667*1000 + 3.333*500 + 0.667*200 + 0*100,
		},
		{
			name:     "unknown model uses default",
			model:    "claude-unknown-99",
			in:       1000,
			out:      500,
			expected: 0.4*1000 + 2.0*500,
		},
		{
			name:     "zero tokens",
			model:    "claude-opus-4",
			in:       0,
			out:      0,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := policy.ComputeCredits(tt.model, tt.in, tt.out, tt.cacheW, tt.cacheR)
			if math.Abs(got-tt.expected) > 0.001 {
				t.Errorf("ComputeCredits() = %f, want %f", got, tt.expected)
			}
		})
	}
}

func TestComputeCreditsNoRates(t *testing.T) {
	policy := &RateLimitPolicy{}
	got := policy.ComputeCredits("claude-opus-4", 1000, 500, 0, 0)
	if got != 0 {
		t.Errorf("expected 0 credits with no rates, got %f", got)
	}
}

// TestComputeCreditsWithDefault_FallbackOrder pins down the four-step
// resolution order from the model-catalog spec: plan override → catalog
// default → plan _default → 0.
func TestComputeCreditsWithDefault_FallbackOrder(t *testing.T) {
	planOverride := CreditRate{InputRate: 1, OutputRate: 1}
	catalogDefault := CreditRate{InputRate: 2, OutputRate: 2}
	planDefault := CreditRate{InputRate: 3, OutputRate: 3}

	cases := []struct {
		name      string
		policy    *RateLimitPolicy
		catalog   *CreditRate
		wantInput float64
	}{
		{
			"plan override wins over everything else",
			&RateLimitPolicy{ModelCreditRates: map[string]CreditRate{"m": planOverride, "_default": planDefault}},
			&catalogDefault,
			1,
		},
		{
			"catalog default wins when plan has no override",
			&RateLimitPolicy{ModelCreditRates: map[string]CreditRate{"_default": planDefault}},
			&catalogDefault,
			2,
		},
		{
			"plan _default wins when catalog has no default",
			&RateLimitPolicy{ModelCreditRates: map[string]CreditRate{"_default": planDefault}},
			nil,
			3,
		},
		{
			"zero when nothing is configured",
			&RateLimitPolicy{},
			nil,
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.policy.ComputeCreditsWithDefault("m", tc.catalog, 1, 0, 0, 0)
			if math.Abs(got-tc.wantInput) > 1e-9 {
				t.Errorf("got %v, want %v", got, tc.wantInput)
			}
		})
	}
}

func TestComputeCreditsWithDefault_LongContext(t *testing.T) {
	policy := &RateLimitPolicy{
		ModelCreditRates: map[string]CreditRate{
			"gpt-5.4": {
				InputRate:     0.333,
				OutputRate:    2,
				CacheReadRate: 0.033,
				LongContext: &LongContextCreditRate{
					ThresholdInputTokens: 272000,
					InputMultiplier:      2,
					OutputMultiplier:     1.5,
				},
			},
		},
	}

	gotShort := policy.ComputeCredits("gpt-5.4", 271000, 1000, 0, 1000)
	wantShort := 0.333*271000 + 2.0*1000 + 0.033*1000
	if math.Abs(gotShort-wantShort) > 0.001 {
		t.Fatalf("short-context credits = %v, want %v", gotShort, wantShort)
	}

	gotLong := policy.ComputeCredits("gpt-5.4", 271001, 1000, 0, 1000)
	wantLong := (0.333*2)*271001 + (2.0*1.5)*1000 + (0.033*2)*1000
	if math.Abs(gotLong-wantLong) > 0.001 {
		t.Fatalf("long-context credits = %v, want %v", gotLong, wantLong)
	}
}

func TestPolicyIsActive(t *testing.T) {
	now := time.Now()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)
	farPast := now.Add(-2 * time.Hour)

	tests := []struct {
		name     string
		starts   *time.Time
		expires  *time.Time
		expected bool
	}{
		{"no bounds", nil, nil, true},
		{"within window", &past, &future, true},
		{"not started yet", &future, nil, false},
		{"already expired", &farPast, &past, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &RateLimitPolicy{StartsAt: tt.starts, ExpiresAt: tt.expires}
			if p.IsActive() != tt.expected {
				t.Errorf("IsActive() = %v, want %v", p.IsActive(), tt.expected)
			}
		})
	}
}

func TestCreditRuleEffectiveScope(t *testing.T) {
	r1 := CreditRule{Scope: ""}
	if r1.EffectiveScope() != CreditScopeProject {
		t.Errorf("empty scope should default to project, got %s", r1.EffectiveScope())
	}
	r2 := CreditRule{Scope: CreditScopeKey}
	if r2.EffectiveScope() != CreditScopeKey {
		t.Errorf("explicit key scope should stay key, got %s", r2.EffectiveScope())
	}
}

func TestComputeCreditsForClient_FallbackOrder(t *testing.T) {
	clientOverride := CreditRate{InputRate: 0.1, OutputRate: 0.5}
	planOverride := CreditRate{InputRate: 1, OutputRate: 5}
	catalogDef := CreditRate{InputRate: 10, OutputRate: 50}
	planDefault := CreditRate{InputRate: 100, OutputRate: 500}

	tests := []struct {
		name           string
		policy         *RateLimitPolicy
		client         string
		catalog        *CreditRate
		expectedInput  float64
		expectedOutput float64
	}{
		{
			name: "client-model override wins (step 1)",
			policy: &RateLimitPolicy{
				ClientModelCreditRates: map[string]map[string]CreditRate{
					"claude-code-cli": {"m": clientOverride},
				},
				ModelCreditRates: map[string]CreditRate{"m": planOverride, "_default": planDefault},
			},
			client: "claude-code-cli", catalog: &catalogDef,
			expectedInput: clientOverride.InputRate, expectedOutput: clientOverride.OutputRate,
		},
		{
			name: "plan model override (step 2 — different client)",
			policy: &RateLimitPolicy{
				ClientModelCreditRates: map[string]map[string]CreditRate{
					"claude-code-cli": {"m": clientOverride},
				},
				ModelCreditRates: map[string]CreditRate{"m": planOverride, "_default": planDefault},
			},
			client: "codex-cli", catalog: &catalogDef,
			expectedInput: planOverride.InputRate, expectedOutput: planOverride.OutputRate,
		},
		{
			name: "plan model override (step 2 — client absent from outer map)",
			policy: &RateLimitPolicy{
				ModelCreditRates: map[string]CreditRate{"m": planOverride, "_default": planDefault},
			},
			client: "claude-code-cli", catalog: &catalogDef,
			expectedInput: planOverride.InputRate, expectedOutput: planOverride.OutputRate,
		},
		{
			name: "plan model override (step 2 — client present but model absent)",
			policy: &RateLimitPolicy{
				ClientModelCreditRates: map[string]map[string]CreditRate{
					"claude-code-cli": {"other-model": clientOverride},
				},
				ModelCreditRates: map[string]CreditRate{"m": planOverride, "_default": planDefault},
			},
			client: "claude-code-cli", catalog: &catalogDef,
			expectedInput: planOverride.InputRate, expectedOutput: planOverride.OutputRate,
		},
		{
			name: "catalog default (step 3 — no plan overrides)",
			policy: &RateLimitPolicy{
				ModelCreditRates: map[string]CreditRate{"_default": planDefault},
			},
			client: "claude-code-cli", catalog: &catalogDef,
			expectedInput: catalogDef.InputRate, expectedOutput: catalogDef.OutputRate,
		},
		{
			name: "plan _default (step 4 — no catalog default)",
			policy: &RateLimitPolicy{
				ModelCreditRates: map[string]CreditRate{"_default": planDefault},
			},
			client: "claude-code-cli", catalog: nil,
			expectedInput: planDefault.InputRate, expectedOutput: planDefault.OutputRate,
		},
		{
			name:          "zero (step 5 — nothing matches)",
			policy:        &RateLimitPolicy{},
			client:        "claude-code-cli", catalog: nil,
			expectedInput: 0, expectedOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.policy.ComputeCreditsForClient("m", tt.client, tt.catalog, 1000, 500, 0, 0)
			want := tt.expectedInput*1000 + tt.expectedOutput*500
			if math.Abs(got-want) > 0.001 {
				t.Errorf("ComputeCreditsForClient = %f, want %f", got, want)
			}
		})
	}
}

func TestComputeCreditsWithDefault_BackwardCompat(t *testing.T) {
	planOverride := CreditRate{InputRate: 1, OutputRate: 5, CacheCreationRate: 0.5, CacheReadRate: 0.1}
	catalogDef := CreditRate{InputRate: 10, OutputRate: 50}
	planDefault := CreditRate{InputRate: 100, OutputRate: 500}

	policy := &RateLimitPolicy{
		ModelCreditRates: map[string]CreditRate{"m": planOverride, "_default": planDefault},
		// ClientModelCreditRates intentionally absent.
	}

	cases := []struct {
		model           string
		in, out, cw, cr int64
		expectedFromLegacy float64
	}{
		{"m", 1000, 500, 200, 100,
			planOverride.InputRate*1000 + planOverride.OutputRate*500 + planOverride.CacheCreationRate*200 + planOverride.CacheReadRate*100},
		{"unknown", 1000, 500, 0, 0,
			catalogDef.InputRate*1000 + catalogDef.OutputRate*500},
	}

	for _, c := range cases {
		want := c.expectedFromLegacy
		gotLegacy := policy.ComputeCreditsWithDefault(c.model, &catalogDef, c.in, c.out, c.cw, c.cr)
		if math.Abs(gotLegacy-want) > 0.001 {
			t.Errorf("ComputeCreditsWithDefault(%s) = %f, want %f", c.model, gotLegacy, want)
		}
		gotNew := policy.ComputeCreditsForClient(c.model, "", &catalogDef, c.in, c.out, c.cw, c.cr)
		if math.Abs(gotNew-want) > 0.001 {
			t.Errorf("ComputeCreditsForClient(%s, \"\") = %f, want %f", c.model, gotNew, want)
		}
		gotAnyClient := policy.ComputeCreditsForClient(c.model, "claude-code-cli", &catalogDef, c.in, c.out, c.cw, c.cr)
		if math.Abs(gotAnyClient-want) > 0.001 {
			t.Errorf("ComputeCreditsForClient(%s, \"claude-code-cli\") with absent client map = %f, want %f", c.model, gotAnyClient, want)
		}
	}
}
