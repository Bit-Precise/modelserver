package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelserver/modelserver/internal/config"
	"github.com/modelserver/modelserver/internal/types"
)

// withAPIKey injects a fake API key into the request context,
// simulating what AuthMiddleware does (which runs before TraceMiddleware).
func withAPIKey(r *http.Request, keyID string) *http.Request {
	ctx := context.WithValue(r.Context(), ctxAPIKey, &types.APIKey{ID: keyID})
	return r.WithContext(ctx)
}

func TestTraceMiddleware_RequireSession_RejectsAnonymousPOST(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:    "X-Trace-Id",
		RequireSession: true,
	}

	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestTraceMiddleware_RequireSession_AllowsWithTraceID(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:    "X-Trace-Id",
		RequireSession: true,
	}

	called := false
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	req.Header.Set("X-Trace-Id", "session-123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should have been called")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestTraceMiddleware_RequireSession_AllowsGETWithoutSession(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:    "X-Trace-Id",
		RequireSession: true,
	}

	called := false
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should have been called for GET")
	}
}

func TestTraceMiddleware_RequireSessionDisabled_AllowsAnonymous(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:    "X-Trace-Id",
		RequireSession: false,
	}

	called := false
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !called {
		t.Error("handler should have been called when require_session is false")
	}
}

func TestExtractClaudeTraceID(t *testing.T) {
	tests := []struct {
		name   string
		userID string
		want   string
	}{
		// Current JSON format (Claude Code ≥ v2.1)
		{
			name:   "json format with all fields",
			userID: `{"device_id":"264a5b050a3a389cafb40a1e7f5980bd6450b1f366e404b00c2a40a550ab945a","account_uuid":"04633d98-7e59-4420-afb8-675468f67c71","session_id":"68c6d0ca-3753-43b2-aa92-8ccb0701ebff"}`,
			want:   "68c6d0ca-3753-43b2-aa92-8ccb0701ebff",
		},
		{
			name:   "json format with empty account_uuid",
			userID: `{"device_id":"264a5b050a3a389cafb40a1e7f5980bd6450b1f366e404b00c2a40a550ab945a","account_uuid":"","session_id":"68c6d0ca-3753-43b2-aa92-8ccb0701ebff"}`,
			want:   "68c6d0ca-3753-43b2-aa92-8ccb0701ebff",
		},
		{
			name:   "json format with extra metadata fields",
			userID: `{"custom_key":"custom_value","device_id":"264a5b050a3a389cafb40a1e7f5980bd6450b1f366e404b00c2a40a550ab945a","account_uuid":"","session_id":"aabbccdd-1234-5678-9abc-def012345678"}`,
			want:   "aabbccdd-1234-5678-9abc-def012345678",
		},
		// Legacy string format with account UUID
		{
			name:   "legacy format with account uuid",
			userID: "user_264a5b050a3a389cafb40a1e7f5980bd6450b1f366e404b00c2a40a550ab945a_account_04633d98-7e59-4420-afb8-675468f67c71_session_68c6d0ca-3753-43b2-aa92-8ccb0701ebff",
			want:   "68c6d0ca-3753-43b2-aa92-8ccb0701ebff",
		},
		// Legacy string format without account UUID
		{
			name:   "legacy format without account uuid",
			userID: "user_264a5b050a3a389cafb40a1e7f5980bd6450b1f366e404b00c2a40a550ab945a_account__session_68c6d0ca-3753-43b2-aa92-8ccb0701ebff",
			want:   "68c6d0ca-3753-43b2-aa92-8ccb0701ebff",
		},
		// Invalid inputs
		{
			name:   "empty string",
			userID: "",
			want:   "",
		},
		{
			name:   "random string",
			userID: "not-a-valid-format",
			want:   "",
		},
		{
			name:   "json without session_id",
			userID: `{"device_id":"abc","account_uuid":"def"}`,
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractClaudeTraceID(tt.userID)
			if got != tt.want {
				t.Errorf("extractClaudeTraceID() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTraceMiddleware_OpenClaw_DetectsViaUserAgent(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:          "X-Trace-Id",
		RequireSession:       true,
		OpenClawTraceEnabled: true,
	}

	var gotSource string
	var gotTraceID string
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSource = TraceSourceFromContext(r.Context())
		gotTraceID = TraceIDFromContext(r.Context())
	}))

	req := withAPIKey(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), "key-abc-123")
	req.Header.Set("User-Agent", "openclaw/2026.3.14")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if gotSource != types.TraceSourceOpenClaw {
		t.Errorf("expected source %q, got %q", types.TraceSourceOpenClaw, gotSource)
	}
	if gotTraceID != "key-abc-123" {
		t.Errorf("expected trace ID %q, got %q", "key-abc-123", gotTraceID)
	}
}

func TestTraceMiddleware_OpenClaw_DetectsViaOriginator(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:          "X-Trace-Id",
		OpenClawTraceEnabled: true,
	}

	var gotSource string
	var gotTraceID string
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSource = TraceSourceFromContext(r.Context())
		gotTraceID = TraceIDFromContext(r.Context())
	}))

	req := withAPIKey(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), "key-orig-456")
	req.Header.Set("originator", "openclaw")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if gotSource != types.TraceSourceOpenClaw {
		t.Errorf("expected source %q, got %q", types.TraceSourceOpenClaw, gotSource)
	}
	if gotTraceID != "key-orig-456" {
		t.Errorf("expected trace ID %q, got %q", "key-orig-456", gotTraceID)
	}
}

func TestTraceMiddleware_OpenClaw_DisabledDoesNotDetect(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:          "X-Trace-Id",
		RequireSession:       true,
		OpenClawTraceEnabled: false,
	}

	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached")
	}))

	req := withAPIKey(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), "key-abc-123")
	req.Header.Set("User-Agent", "openclaw/2026.3.14")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 when openclaw detection disabled, got %d", rr.Code)
	}
}

func TestTraceMiddleware_OpenClaw_ExplicitHeaderTakesPrecedence(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:          "X-Trace-Id",
		OpenClawTraceEnabled: true,
	}

	var gotSource string
	var gotTraceID string
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSource = TraceSourceFromContext(r.Context())
		gotTraceID = TraceIDFromContext(r.Context())
	}))

	req := withAPIKey(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), "key-abc-123")
	req.Header.Set("User-Agent", "openclaw/2026.3.14")
	req.Header.Set("X-Trace-Id", "explicit-session-123")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if gotSource != types.TraceSourceHeader {
		t.Errorf("expected source %q, got %q", types.TraceSourceHeader, gotSource)
	}
	if gotTraceID != "explicit-session-123" {
		t.Errorf("expected trace ID %q, got %q", "explicit-session-123", gotTraceID)
	}
}

func TestTraceMiddleware_OpenClaw_SameKeyGetsSameTraceID(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:          "X-Trace-Id",
		OpenClawTraceEnabled: true,
	}

	var ids []string
	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, TraceIDFromContext(r.Context()))
	}))

	for i := 0; i < 3; i++ {
		req := withAPIKey(httptest.NewRequest(http.MethodPost, "/v1/messages", nil), "key-stable-789")
		req.Header.Set("User-Agent", "openclaw/2026.3.14")
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}

	for i, id := range ids {
		if id != "key-stable-789" {
			t.Errorf("request %d: expected trace ID %q, got %q", i, "key-stable-789", id)
		}
	}
}

func TestTraceMiddleware_RequireSession_RejectsOpenAIResponses(t *testing.T) {
	cfg := config.TraceConfig{
		TraceHeader:    "X-Trace-Id",
		RequireSession: true,
	}

	handler := TraceMiddleware(cfg, nil, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be reached for /v1/responses without session")
	}))

	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestTraceMiddleware_WritesClientBucket(t *testing.T) {
	var gotKind, gotBucket string
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKind = ClientKindFromContext(r.Context())
		gotBucket = ClientBucketFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name       string
		setup      func(*http.Request)
		wantKind   string
		wantBucket string
	}{
		{
			name:       "claude_code_cli",
			setup:      func(r *http.Request) { r.Header.Set("User-Agent", "claude-cli/1.0 (external, cli)"); r.Body = io.NopCloser(strings.NewReader(`{"metadata":{"user_id":"user_` + strings.Repeat("a", 64) + `_account__session_00000000-0000-0000-0000-000000000000"}}`)) },
			wantKind:   types.ClientKindClaudeCode,
			wantBucket: types.ClientBucketClaudeCodeCLI,
		},
		{
			name:       "claude_desktop",
			setup:      func(r *http.Request) { r.Header.Set("User-Agent", "Mozilla/5.0 Claude/1.0 (Electron/30.0)") },
			wantKind:   types.ClientKindClaudeDesktop,
			wantBucket: types.ClientBucketClaudeDesktop,
		},
		{
			name:       "codex_cli",
			setup:      func(r *http.Request) { r.Header.Set("Session-Id", "00000000-0000-0000-0000-000000000000") },
			wantKind:   types.ClientKindCodex,
			wantBucket: types.ClientBucketCodexCLI,
		},
		{
			name:       "opencode_other",
			setup:      func(r *http.Request) { r.Header.Set("User-Agent", "opencode/0.1.0") },
			wantKind:   types.ClientKindOpenCode,
			wantBucket: types.ClientBucketOther,
		},
		{
			name:       "unknown_other",
			setup:      func(r *http.Request) { r.Header.Set("User-Agent", "curl/8.0") },
			wantKind:   types.ClientKindUnknown,
			wantBucket: types.ClientBucketOther,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotKind, gotBucket = "", ""
			mw := TraceMiddleware(config.TraceConfig{TraceHeader: "X-Trace-Id"}, nil, nil)
			req := httptest.NewRequest("POST", "/v1/messages", nil)
			c.setup(req)
			mw(probe).ServeHTTP(httptest.NewRecorder(), req)
			if gotKind != c.wantKind {
				t.Errorf("client_kind = %q, want %q", gotKind, c.wantKind)
			}
			if gotBucket != c.wantBucket {
				t.Errorf("client_bucket = %q, want %q", gotBucket, c.wantBucket)
			}
		})
	}
}

func TestClientBucketFromContext_Default(t *testing.T) {
	if got := ClientBucketFromContext(context.Background()); got != types.ClientBucketOther {
		t.Errorf("ClientBucketFromContext(empty) = %q, want %q", got, types.ClientBucketOther)
	}
}
