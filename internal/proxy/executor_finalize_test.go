package proxy

import (
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
