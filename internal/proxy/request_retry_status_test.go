package proxy

import (
	"errors"
	"net/http"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestApplyRetryFields(t *testing.T) {
	tests := []struct {
		name       string
		ctx        RequestContext
		status     string
		wantStatus string
	}{
		{
			name:       "first attempt success is normal",
			ctx:        RequestContext{Attempt: 1},
			status:     types.RequestStatusSuccess,
			wantStatus: types.RequestRetryStatusNormal,
		},
		{
			name:       "unretried failure is non retryable",
			ctx:        RequestContext{Attempt: 1},
			status:     types.RequestStatusError,
			wantStatus: types.RequestRetryStatusNonRetryableError,
		},
		{
			name: "recovered retry is preserved on success",
			ctx: RequestContext{
				Attempt:     2,
				RetryStatus: types.RequestRetryStatusRetryableError,
				RetryReason: "codex_capacity",
			},
			status:     types.RequestStatusSuccess,
			wantStatus: types.RequestRetryStatusRetryableError,
		},
		{
			name: "exhausted retry is preserved on failure",
			ctx: RequestContext{
				Attempt:     3,
				RetryStatus: types.RequestRetryStatusRetryExhausted,
				RetryReason: "5xx",
			},
			status:     types.RequestStatusError,
			wantStatus: types.RequestRetryStatusRetryExhausted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := types.Request{Status: tt.status}
			applyRetryFields(&tt.ctx, &req)
			if req.RetryStatus != tt.wantStatus {
				t.Fatalf("RetryStatus = %q, want %q", req.RetryStatus, tt.wantStatus)
			}
			if req.Attempt != tt.ctx.Attempt {
				t.Fatalf("Attempt = %d, want %d", req.Attempt, tt.ctx.Attempt)
			}
			if req.RetryReason != tt.ctx.RetryReason {
				t.Fatalf("RetryReason = %q, want %q", req.RetryReason, tt.ctx.RetryReason)
			}
		})
	}
}

func TestRetryReason(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
		err  error
		want string
	}{
		{name: "connection", err: errors.New("dial failed"), want: "connection_error"},
		{name: "rate limit", resp: &http.Response{StatusCode: http.StatusTooManyRequests}, want: "429"},
		{name: "server error", resp: &http.Response{StatusCode: http.StatusServiceUnavailable}, want: "5xx"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := retryReason(tt.resp, tt.err); got != tt.want {
				t.Fatalf("retryReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
