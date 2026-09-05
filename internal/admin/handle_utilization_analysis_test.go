package admin

import (
	"math"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

func TestSuggestRatesForFixedLimit(t *testing.T) {
	reset := time.Unix(1_700_000_000, 0)
	snaps := []store.UtilizationSnapshot{
		{OfficialPct: 10, TotalCredits: 1_100_000, ResetsAt: &reset},
		{OfficialPct: 25, TotalCredits: 2_750_000, ResetsAt: &reset},
		{OfficialPct: 50, TotalCredits: 5_500_000, ResetsAt: &reset},
	}

	got := suggestRatesForFixedLimit(snaps, "5h", utilizationAnalysisBaseRates)
	if got == nil {
		t.Fatal("suggestRatesForFixedLimit returned nil")
	}
	if math.Abs(got.KnownLimit-11_000_000) > 0.5 {
		t.Fatalf("KnownLimit = %v, want 11000000", got.KnownLimit)
	}
	if math.Abs(got.SuggestedRateMultiplier-1) > 0.000001 {
		t.Fatalf("SuggestedRateMultiplier = %v, want 1", got.SuggestedRateMultiplier)
	}
	if math.Abs(got.TargetCredits-9_350_000) > 0.5 {
		t.Fatalf("TargetCredits = %v, want 9350000", got.TargetCredits)
	}
	if got.RMSEPct != 0 {
		t.Fatalf("RMSEPct = %v, want 0", got.RMSEPct)
	}
}

func TestSuggestRatesForFixedLimitScalesKnownRates(t *testing.T) {
	got := suggestRatesForFixedLimit([]store.UtilizationSnapshot{
		{
			OfficialPct:  6,
			TotalCredits: 10_097_386.538,
			ModelBreakdown: map[string]*store.UpstreamTokenBreakdown{
				"gpt-5.5": {},
			},
		},
	}, "5h", map[string]types.CreditRate{
		"gpt-5.5": {InputRate: 0.667, OutputRate: 4, CacheCreationRate: 0, CacheReadRate: 0.067},
	})
	if got == nil {
		t.Fatal("suggestRatesForFixedLimit returned nil")
	}
	if math.Abs(got.SuggestedRateMultiplier-0.065363) > 0.000001 {
		t.Fatalf("SuggestedRateMultiplier = %v, want 0.065363", got.SuggestedRateMultiplier)
	}
	rate := got.SuggestedRates["gpt-5.5"]
	if math.Abs(rate.InputRate-0.043596) > 0.000001 {
		t.Fatalf("InputRate = %v, want 0.043596", rate.InputRate)
	}
	if math.Abs(rate.OutputRate-0.261454) > 0.000001 {
		t.Fatalf("OutputRate = %v, want 0.261454", rate.OutputRate)
	}
}

func TestUtilizationAnalysisBaseRates_GPT55SubscriptionDiscount(t *testing.T) {
	rate := utilizationAnalysisBaseRates["gpt-5.5"]
	if math.Abs(rate.InputRate-0.2668) > 0.000001 {
		t.Fatalf("InputRate = %v, want subscription discount 0.2668 (catalog x 0.4 after 053/067 doubles)", rate.InputRate)
	}
	if math.Abs(rate.OutputRate-1.6) > 0.000001 {
		t.Fatalf("OutputRate = %v, want subscription discount 1.6 (catalog x 0.4 after 053/067 doubles)", rate.OutputRate)
	}
	if math.Abs(rate.CacheReadRate-0.0268) > 0.000001 {
		t.Fatalf("CacheReadRate = %v, want subscription discount 0.0268 (catalog x 0.4 after 053/067 doubles)", rate.CacheReadRate)
	}
	// Migration 047 strips the long_context block from gpt-5.5 (the
	// rebased plan entry has none, so the OLS base rate has none either).
	if rate.LongContext != nil {
		t.Fatalf("LongContext = %+v, want nil after 047 rebase", rate.LongContext)
	}
}

func TestScaleCreditRatePreservesLongContext(t *testing.T) {
	rate := scaleCreditRate(types.CreditRate{
		InputRate:  0.667,
		OutputRate: 4,
		LongContext: &types.LongContextCreditRate{
			ThresholdInputTokens: 272000,
			InputMultiplier:      2,
			OutputMultiplier:     1.5,
		},
	}, 0.5)
	if rate.LongContext == nil {
		t.Fatal("LongContext is nil")
	}
	if rate.LongContext.ThresholdInputTokens != 272000 ||
		rate.LongContext.InputMultiplier != 2 ||
		rate.LongContext.OutputMultiplier != 1.5 {
		t.Fatalf("LongContext = %+v", rate.LongContext)
	}
}

func TestSuggestRatesForFixedLimitNoUsableCredits(t *testing.T) {
	got := suggestRatesForFixedLimit([]store.UtilizationSnapshot{
		{OfficialPct: 10, TotalCredits: 0},
		{OfficialPct: 20, TotalCredits: -1},
	}, "5h", utilizationAnalysisBaseRates)
	if got != nil {
		t.Fatalf("suggestRatesForFixedLimit = %+v, want nil", got)
	}
}

// TestUtilizationBaseRates_GPT56 asserts the utilization analyser knows the
// three gpt-5.6 models at their plan-rate values (catalog * 0.4 after the
// 053/067 doubles) with the long_context multipliers preserved.
func TestUtilizationBaseRates_GPT56(t *testing.T) {
	cases := []struct {
		name          string
		wantInput     float64
		wantOutput    float64
		wantCacheRead float64
		wantCacheCrea float64
	}{
		{"gpt-5.6-sol", 0.2132, 1.0668, 0.0212, 0.2668},
		{"gpt-5.6-terra", 0.1068, 0.64, 0.0108, 0.1332},
		{"gpt-5.6-luna", 0.0108, 0.064, 0.0012, 0.0132},
	}
	for _, tc := range cases {
		r, ok := utilizationAnalysisBaseRates[tc.name]
		if !ok {
			t.Fatalf("%s missing from utilizationAnalysisBaseRates", tc.name)
		}
		if r.InputRate != tc.wantInput || r.OutputRate != tc.wantOutput ||
			r.CacheReadRate != tc.wantCacheRead || r.CacheCreationRate != tc.wantCacheCrea {
			t.Fatalf("%s rates: %+v; want in=%v out=%v cr=%v cc=%v",
				tc.name, r, tc.wantInput, tc.wantOutput, tc.wantCacheRead, tc.wantCacheCrea)
		}
		if r.LongContext == nil ||
			r.LongContext.ThresholdInputTokens != 272000 ||
			r.LongContext.InputMultiplier != 2.0 ||
			r.LongContext.OutputMultiplier != 1.5 {
			t.Fatalf("%s long_context: %+v; want thr=272000 in=2 out=1.5", tc.name, r.LongContext)
		}
	}
}

func TestUtilizationBaseRates_GPT6Astra(t *testing.T) {
	r, ok := utilizationAnalysisBaseRates["gpt-6-astra"]
	if !ok {
		t.Fatal("gpt-6-astra missing from utilizationAnalysisBaseRates")
	}
	if r.InputRate != 0.5332 || r.OutputRate != 2.6668 ||
		r.CacheReadRate != 0.0532 || r.CacheCreationRate != 0.6668 {
		t.Fatalf("gpt-6-astra rates: %+v; want in=0.5332 out=2.6668 cr=0.0532 cc=0.6668", r)
	}
	if r.LongContext == nil ||
		r.LongContext.ThresholdInputTokens != 272000 ||
		r.LongContext.InputMultiplier != 2.0 ||
		r.LongContext.OutputMultiplier != 1.5 {
		t.Fatalf("gpt-6-astra long_context: %+v; want thr=272000 in=2 out=1.5", r.LongContext)
	}
}

func TestUtilizationBaseRates_GPT54MiniNano(t *testing.T) {
	cases := []struct {
		name          string
		wantInput     float64
		wantOutput    float64
		wantCacheRead float64
	}{
		{"gpt-5.4-mini", 0.04, 0.24, 0.004},
		{"gpt-5.4-nano", 0.0108, 0.0668, 0.0012},
	}
	for _, tc := range cases {
		r, ok := utilizationAnalysisBaseRates[tc.name]
		if !ok {
			t.Fatalf("%s missing from utilizationAnalysisBaseRates", tc.name)
		}
		if r.InputRate != tc.wantInput || r.OutputRate != tc.wantOutput ||
			r.CacheCreationRate != 0 || r.CacheReadRate != tc.wantCacheRead {
			t.Fatalf("%s rates: %+v; want in=%v out=%v cc=0 cr=%v",
				tc.name, r, tc.wantInput, tc.wantOutput, tc.wantCacheRead)
		}
		if r.LongContext != nil {
			t.Fatalf("%s long_context: %+v; want nil", tc.name, r.LongContext)
		}
	}
}
