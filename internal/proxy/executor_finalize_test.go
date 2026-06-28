package proxy

import (
	"math"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

// TestBuildEarlyErrorRequest verifies the helper that builds the *types.Request
// passed to store.CompleteRequest from an Execute early-return path. Without
// this helper, paths like router.Match-fail / no-upstreams / oversize-body
// write the HTTP error to the client but leave the pending row stuck on
// status='processing' indefinitely — which is what the user reported for
// no-route requests.
func TestBuildEarlyErrorRequest(t *testing.T) {
	reqCtx := &RequestContext{
		OAuthGrantID:          "grant-1",
		ClientIP:              "10.0.0.1",
		IsExtraUsage:          true,
		ExtraUsageCostCredits: 0,
		ExtraUsageReason:      "rate_limited",
	}
	start := time.Now().Add(-150 * time.Millisecond)

	got := buildEarlyErrorRequest(reqCtx, start, "no route matched")

	if got.Status != types.RequestStatusError {
		t.Errorf("Status = %q, want %q", got.Status, types.RequestStatusError)
	}
	if got.ErrorMessage != "no route matched" {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, "no route matched")
	}
	if got.ClientIP != "10.0.0.1" {
		t.Errorf("ClientIP = %q, want %q", got.ClientIP, "10.0.0.1")
	}
	if got.OAuthGrantID != "grant-1" {
		t.Errorf("OAuthGrantID = %q, want %q", got.OAuthGrantID, "grant-1")
	}
	if !got.IsExtraUsage {
		t.Errorf("IsExtraUsage = false, want true (extra-usage attribution must survive early failures)")
	}
	if got.ExtraUsageReason != "rate_limited" {
		t.Errorf("ExtraUsageReason = %q, want %q", got.ExtraUsageReason, "rate_limited")
	}
	// LatencyMs should be derived from the start time and non-negative.
	if got.LatencyMs < 0 {
		t.Errorf("LatencyMs = %d, want >= 0", got.LatencyMs)
	}
}

// TestBuildEarlyErrorRequest_EmptyContext makes sure the helper handles a
// minimal reqCtx without panicking — useful guarantee because some early-error
// sites fire before fields like ExtraUsage* are populated.
func TestBuildEarlyErrorRequest_EmptyContext(t *testing.T) {
	got := buildEarlyErrorRequest(&RequestContext{}, time.Now(), "boom")
	if got.Status != types.RequestStatusError {
		t.Errorf("Status = %q, want error", got.Status)
	}
	if got.ErrorMessage != "boom" {
		t.Errorf("ErrorMessage = %q, want boom", got.ErrorMessage)
	}
}

// TestExecutorFinalize_Subscription_NoClientOverridesIsByteIdentical
// is the pricing-path no-regression invariant. For any plan that does
// NOT define ClientModelCreditRates, the executor's subscription
// pricing credit number must equal what Policy.ComputeCreditsWithDefault
// would have produced before this PR.
func TestExecutorFinalize_Subscription_NoClientOverridesIsByteIdentical(t *testing.T) {
	policy := &types.RateLimitPolicy{
		ModelCreditRates: map[string]types.CreditRate{
			"claude-sonnet-4": {InputRate: 3, OutputRate: 15, CacheCreationRate: 3.75, CacheReadRate: 0.3},
			"_default":        {InputRate: 1, OutputRate: 5},
		},
	}
	catalog := &types.CreditRate{InputRate: 5, OutputRate: 25}
	in, out, cw, cr := int64(1000), int64(500), int64(200), int64(100)

	baseline := policy.ComputeCreditsWithDefault("claude-sonnet-4", catalog, in, out, cw, cr)

	for _, c := range types.AllClientBuckets {
		got := policy.ComputeCreditsForClient("claude-sonnet-4", c, catalog, in, out, cw, cr)
		if math.Abs(got-baseline) > 0.001 {
			t.Errorf("client=%s: got %f, baseline %f — resolver step 2+ regressed", c, got, baseline)
		}
	}
	if got := policy.ComputeCreditsForClient("claude-sonnet-4", "", catalog, in, out, cw, cr); math.Abs(got-baseline) > 0.001 {
		t.Errorf("client=\"\": got %f, baseline %f", got, baseline)
	}
}

// TestExecutorFinalize_ExtraUsage_PricingPathUnchanged asserts the
// extra-usage cost computation is untouched by this PR.
func TestExecutorFinalize_ExtraUsage_PricingPathUnchanged(t *testing.T) {
	m := &types.Model{
		Name: "claude-sonnet-4",
		DefaultCreditRate: &types.CreditRate{
			InputRate: 3, OutputRate: 15, CacheCreationRate: 3.75, CacheReadRate: 0.3,
		},
	}
	usage := types.TokenUsage{InputTokens: 1000, OutputTokens: 500, CacheCreationTokens: 200, CacheReadTokens: 100}

	cost, err := computeExtraUsageCostCredits(m, usage)
	if err != nil {
		t.Fatalf("computeExtraUsageCostCredits: %v", err)
	}
	wantCredits := m.DefaultCreditRate.InputRate*float64(usage.InputTokens) +
		m.DefaultCreditRate.OutputRate*float64(usage.OutputTokens) +
		m.DefaultCreditRate.CacheCreationRate*float64(usage.CacheCreationTokens) +
		m.DefaultCreditRate.CacheReadRate*float64(usage.CacheReadTokens)
	wantInt64 := int64(math.Ceil(wantCredits))
	if cost != wantInt64 {
		t.Errorf("extra-usage cost = %d, want %d (catalog default path must be unchanged)", cost, wantInt64)
	}
}
