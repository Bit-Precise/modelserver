# Rejected Request Logging Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `RateLimitMiddleware` and `ExtraUsageGuardMiddleware` write `requests` rows whose shape matches the success path — `metadata.user_agent`, `request_kind`, `streaming`, `oauth_grant_id`, `provider` — so dashboards can drill into rejected requests by client and surface.

**Architecture:** One new file `internal/proxy/rejected_request.go` exposes three package-private helpers (`buildRejectedRequestRow`, `requestKindFromRequest`, `peekStreaming`). Two existing rejection writers (`emitGuardRejection`, `logRateLimitRejectionMsg`) are rewritten to delegate to the helper. The `requests.metadata` JSONB column already holds `user_agent` on the success path, so no schema change.

**Tech Stack:** Go (Go 1.x, chi router, pgx), JSON via stdlib `encoding/json`, tests via stdlib `testing` + table-driven cases. The codebase uses `slog` for structured logging and is wired via `internal/proxy/router.go`.

## Global Constraints

- Spec path: `docs/superpowers/specs/2026-06-19-extra-usage-rejected-request-logging-design.md` — every requirement there is in scope.
- No database schema migration. All new persisted fields live in `requests.metadata` (JSONB) or existing typed columns (`request_kind`, `streaming`, `oauth_grant_id`, `provider`).
- No changes to the success path in `internal/proxy/handler.go`.
- No changes to auth / model-allowed / earlier rejection points.
- Helper returns `nil` when `Project` or `APIKey` is absent from context (matches current skip-on-missing-attribution policy at `extra_usage_guard_middleware.go:381`).
- Slog `extra_usage_rejected` line in `emitGuardRejection` is unchanged — it carries diagnostic fields (`client_kind`, `user_id_shape`, `sub_reason`) that are deliberately not persisted to the row.
- Tests live in `internal/proxy/` with `_test.go` suffix, using table-driven style consistent with the rest of the package.
- Commits follow the existing convention (lowercase type prefix, no period) and end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` because we are on a non-main branch.

---

## File Structure

**Created:**
- `internal/proxy/rejected_request.go` — three exported-package-private helpers (`buildRejectedRequestRow`, `requestKindFromRequest`, `peekStreaming`).
- `internal/proxy/rejected_request_test.go` — unit tests for all three helpers.

**Modified:**
- `internal/proxy/extra_usage_guard_middleware.go` — replace the request-row construction tail of `emitGuardRejection`.
- `internal/proxy/ratelimit_middleware.go` — replace the body of `logRateLimitRejectionMsg`.
- `internal/proxy/extra_usage_guard_middleware_test.go` — extend `TestExtraUsageGuard_ClientRestriction_NotEnabled_LogsRequest` with new field assertions (UA, kind, streaming, oauth grant, provider).

**Reviewed but not modified:**
- `internal/proxy/router.go` — confirm path-to-kind mapping table is exhaustive.
- `internal/proxy/handler.go` — reference for which fields `CreateRequest` populates on success.
- `internal/types/request_kind.go` — source of the `Kind*` constants.

---

## Task 1: New file `rejected_request.go` with `requestKindFromRequest`

**Files:**
- Create: `internal/proxy/rejected_request.go`
- Create: `internal/proxy/rejected_request_test.go`

**Interfaces:**
- Consumes: nothing (stdlib + existing package).
- Produces: `requestKindFromRequest(r *http.Request) string`. Returns a `types.Kind*` constant or `""`.

This task ships the path-to-kind mapping standalone so the next two tasks can rely on it. Pure function, table-driven test, easy to land first.

- [ ] **Step 1: Write the failing test**

Create `internal/proxy/rejected_request_test.go`:

```go
package proxy

import (
	"net/http/httptest"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestRequestKindFromRequest_AllRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		want         string
	}{
		// Proxy POST surface mounted in router.go.
		{"POST", "/v1/messages", types.KindAnthropicMessages},
		{"POST", "/v1/messages/count_tokens", types.KindAnthropicCountTokens},
		{"POST", "/v1/responses", types.KindOpenAIResponses},
		{"POST", "/v1/responses/compact", types.KindOpenAIResponsesCompact},
		{"POST", "/v1/chat/completions", types.KindOpenAIChatCompletions},
		{"POST", "/v1/images/generations", types.KindOpenAIImagesGenerations},
		{"POST", "/v1/images/edits", types.KindOpenAIImagesEdits},
		{"POST", "/v1beta/models/gemini-2.5-flash:generateContent", types.KindGoogleGenerateContent},
		{"POST", "/v1beta/models/gemini-2.5-flash:streamGenerateContent", types.KindGoogleGenerateContent},
		{"POST", "/v1beta/models/foo:streamRawPredict", types.KindGoogleGenerateContent},

		// Non-proxy surface — should not be classified.
		{"GET", "/v1/messages", ""},
		{"GET", "/v1/models", ""},
		{"GET", "/v1/usage", ""},
		{"POST", "/admin/upstreams", ""},
		{"POST", "/healthz", ""},
		{"POST", "/v1beta/other/path", ""},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		if got := requestKindFromRequest(r); got != c.want {
			t.Errorf("%s %s → %q, want %q", c.method, c.path, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestRequestKindFromRequest_AllRoutes -v`
Expected: FAIL with `undefined: requestKindFromRequest`.

- [ ] **Step 3: Implement `requestKindFromRequest`**

Create `internal/proxy/rejected_request.go`:

```go
// Package proxy — rejected_request.go: shared helpers for persisting a
// requests row when the middleware chain rejects a request before any
// handler runs. Today this fires from RateLimitMiddleware (classic 4xx)
// and ExtraUsageGuardMiddleware (extra-usage 429s). The goal is row
// shape parity with the success path's CreateRequest — same metadata
// (UA), request_kind, streaming, oauth_grant_id, provider — so dashboards
// pivoting on those columns include the rejected traffic.
package proxy

import (
	"net/http"
	"strings"

	"github.com/modelserver/modelserver/internal/types"
)

// requestKindFromRequest maps the incoming path + method to a
// types.Kind* constant. Mirrors the per-handler constants chosen in
// handler.go (search for `RequestKind: types.Kind`). Returns "" if
// the path is outside the proxy's POST request surface (admin,
// health, GETs on /v1/models or /v1/usage, etc.); the caller treats
// "" the same as the production success path treats an unrouted
// pre-handler rejection: the column stays empty.
//
// The mapping is intentionally a switch rather than a re-run of the
// chi router: chi is not exposed at this point in the middleware
// chain and a small switch is easier to keep in sync with router.go
// than a router clone.
func requestKindFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if r.Method != http.MethodPost {
		return ""
	}
	switch r.URL.Path {
	case "/v1/messages":
		return types.KindAnthropicMessages
	case "/v1/messages/count_tokens":
		return types.KindAnthropicCountTokens
	case "/v1/responses":
		return types.KindOpenAIResponses
	case "/v1/responses/compact":
		return types.KindOpenAIResponsesCompact
	case "/v1/chat/completions":
		return types.KindOpenAIChatCompletions
	case "/v1/images/generations":
		return types.KindOpenAIImagesGenerations
	case "/v1/images/edits":
		return types.KindOpenAIImagesEdits
	}
	// Gemini native: /v1beta/models/{model}:{method}. The handler in
	// router.go binds POST /v1beta/models/* unconditionally and lets
	// HandleGemini classify by suffix; we do the same here.
	if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		return types.KindGoogleGenerateContent
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/ -run TestRequestKindFromRequest_AllRoutes -v`
Expected: PASS, 1 test, all 16 sub-cases pass.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/rejected_request.go internal/proxy/rejected_request_test.go
git commit -m "feat(proxy): add requestKindFromRequest path→kind mapping

Pure helper that returns the same types.Kind* constants the success
path's handlers pick, based on method+path. Used in a follow-up to
populate requests.request_kind for pre-handler 4xx rejections.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `peekStreaming` helper

**Files:**
- Modify: `internal/proxy/rejected_request.go`
- Modify: `internal/proxy/rejected_request_test.go`

**Interfaces:**
- Consumes: `requestKindFromRequest` (Task 1) is in the same file; not called here.
- Produces: `peekStreaming(r *http.Request) bool`. Returns true if the request is streaming. Read-and-restore semantics on the body so downstream readers still see it.

- [ ] **Step 1: Write the failing tests**

Add to `internal/proxy/rejected_request_test.go`. First, extend the existing top-of-file import block so it reads:

```go
import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)
```

Then append:

```go
func TestPeekStreaming(t *testing.T) {
	cases := []struct {
		name, method, path, body string
		want                     bool
	}{
		// Path-based streaming (Gemini native).
		{"gemini stream", "POST", "/v1beta/models/gemini-2.5-flash:streamGenerateContent", "", true},
		{"gemini stream raw predict", "POST", "/v1beta/models/foo:streamRawPredict", "", true},
		{"gemini unary", "POST", "/v1beta/models/gemini-2.5-flash:generateContent", "", false},

		// Body-based streaming (Anthropic / OpenAI).
		{"anthropic stream true", "POST", "/v1/messages", `{"model":"x","stream":true}`, true},
		{"anthropic stream false", "POST", "/v1/messages", `{"model":"x","stream":false}`, false},
		{"anthropic stream absent", "POST", "/v1/messages", `{"model":"x"}`, false},
		{"openai stream true", "POST", "/v1/chat/completions", `{"stream":true}`, true},
		{"openai responses stream true", "POST", "/v1/responses", `{"stream":true}`, true},

		// Non-streaming surfaces — always false.
		{"count_tokens with stream true ignored", "POST", "/v1/messages/count_tokens", `{"stream":true}`, false},
		{"images generations", "POST", "/v1/images/generations", `{"stream":true}`, false},
		{"images edits", "POST", "/v1/images/edits", `{"stream":true}`, false},

		// Malformed inputs.
		{"malformed body", "POST", "/v1/messages", `{not json`, false},
		{"empty body", "POST", "/v1/messages", "", false},
		{"unknown path", "POST", "/v1/whatever", `{"stream":true}`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r *http.Request
			if c.body == "" {
				r = httptest.NewRequest(c.method, c.path, nil)
			} else {
				r = httptest.NewRequest(c.method, c.path, strings.NewReader(c.body))
			}
			got := peekStreaming(r)
			if got != c.want {
				t.Errorf("peekStreaming(%s %s, body=%q)=%v, want %v", c.method, c.path, c.body, got, c.want)
			}
			// Body must remain readable downstream.
			if c.body != "" && r.Body != nil {
				rest, _ := io.ReadAll(r.Body)
				if string(rest) != c.body {
					t.Errorf("body not restored: got %q, want %q", string(rest), c.body)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestPeekStreaming -v`
Expected: FAIL with `undefined: peekStreaming`.

- [ ] **Step 3: Implement `peekStreaming`**

Add to `internal/proxy/rejected_request.go` (top of file imports also needs `bytes`, `encoding/json`, `io`):

```go
// At top of file, extend imports:
//   "bytes"
//   "encoding/json"
//   "io"

// streamingBodyPaths lists the proxy surfaces whose streaming flag lives
// in the JSON request body as {"stream": bool}. Other surfaces either
// signal streaming via the path (Gemini :stream*) or have no streaming
// variant at all (count_tokens, images/*).
var streamingBodyPaths = map[string]bool{
	"/v1/messages":          true,
	"/v1/responses":         true,
	"/v1/responses/compact": true,
	"/v1/chat/completions":  true,
}

// peekStreaming reports whether the incoming request is streaming
// without consuming the body. Three sources of truth:
//
//   - Gemini native paths whose suffix is :stream<Anything> → true.
//   - JSON POSTs to streamingBodyPaths → parse {"stream": bool}.
//   - Everything else → false.
//
// Body reads are restored so downstream middleware/handlers still see
// the original payload. Errors (bad JSON, IO failures) return false;
// the success path's metadata population has the same best-effort style.
func peekStreaming(r *http.Request) bool {
	if r == nil {
		return false
	}
	if strings.HasPrefix(r.URL.Path, "/v1beta/models/") {
		// Gemini native: any `:stream*` suffix means streaming.
		if i := strings.LastIndex(r.URL.Path, ":"); i >= 0 {
			suffix := r.URL.Path[i+1:]
			if strings.HasPrefix(suffix, "stream") {
				return true
			}
		}
		return false
	}
	if !streamingBodyPaths[r.URL.Path] {
		return false
	}
	if r.Body == nil {
		return false
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	var shape struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &shape)
	return shape.Stream
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/ -run TestPeekStreaming -v`
Expected: PASS, all sub-cases including body-restoration check.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/rejected_request.go internal/proxy/rejected_request_test.go
git commit -m "feat(proxy): add peekStreaming helper for rejection logging

Read-and-restore parse of {\"stream\": bool} for Anthropic/OpenAI
POST bodies, plus path-suffix detection for Gemini native
:stream<Anything> endpoints. Best-effort: bad JSON or unknown paths
return false. Used by buildRejectedRequestRow in the next task to
populate requests.streaming for pre-handler 4xx rejections.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `buildRejectedRequestRow` helper

**Files:**
- Modify: `internal/proxy/rejected_request.go`
- Modify: `internal/proxy/rejected_request_test.go`

**Interfaces:**
- Consumes: `requestKindFromRequest` (Task 1), `peekStreaming` (Task 2), existing `peekModel` (`ratelimit_middleware.go:121`), existing context getters `ProjectFromContext`, `APIKeyFromContext`, `ModelFromContext`, `OAuthGrantIDFromContext`, `TraceIDFromContext`.
- Produces:
  ```go
  func buildRejectedRequestRow(
      r *http.Request,
      status string,
      errMsg string,
      extraUsageReason string,
  ) *types.Request
  ```
  Returns `nil` when `Project` or `APIKey` is absent. Otherwise returns a fully-populated `*types.Request` ready for `st.CreateRequest`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/proxy/rejected_request_test.go`. First extend the top-of-file import block to include `"context"` and `"net/http"`:

```go
import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)
```

(`"net/http"` is needed once `httptest.NewRequest` results are passed around as `*http.Request`; if `go vet` reports it unused after Step 3 lands, remove it.)

Then append:

```go
func TestBuildRejectedRequestRow_FullContext(t *testing.T) {
	body := `{"model":"claude-opus-4-7","stream":true}`
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	r.Header.Set("User-Agent", "foo/1.0")
	r.RemoteAddr = "10.0.0.5:54321"
	ctx := context.WithValue(r.Context(), ctxProject, &types.Project{ID: "p1"})
	ctx = context.WithValue(ctx, ctxAPIKey, &types.APIKey{ID: "k1", CreatedBy: "u1"})
	ctx = context.WithValue(ctx, ctxModel, &types.Model{Name: "claude-opus-4-7", Publisher: types.PublisherAnthropic})
	ctx = context.WithValue(ctx, ctxOAuthGrantID, "grant-xyz")
	ctx = context.WithValue(ctx, ctxTraceID, "trace-xyz")
	r = r.WithContext(ctx)

	got := buildRejectedRequestRow(r, types.RequestStatusRateLimited, "denied", "client_restriction")
	if got == nil {
		t.Fatalf("got nil, want populated request row")
	}
	if got.ProjectID != "p1" {
		t.Errorf("ProjectID=%q, want p1", got.ProjectID)
	}
	if got.APIKeyID != "k1" {
		t.Errorf("APIKeyID=%q, want k1", got.APIKeyID)
	}
	if got.CreatedBy != "u1" {
		t.Errorf("CreatedBy=%q, want u1", got.CreatedBy)
	}
	if got.TraceID != "trace-xyz" {
		t.Errorf("TraceID=%q, want trace-xyz", got.TraceID)
	}
	if got.OAuthGrantID != "grant-xyz" {
		t.Errorf("OAuthGrantID=%q, want grant-xyz", got.OAuthGrantID)
	}
	if got.Model != "claude-opus-4-7" {
		t.Errorf("Model=%q, want claude-opus-4-7", got.Model)
	}
	if got.Provider != types.PublisherAnthropic {
		t.Errorf("Provider=%q, want %q", got.Provider, types.PublisherAnthropic)
	}
	if got.RequestKind != types.KindAnthropicMessages {
		t.Errorf("RequestKind=%q, want %q", got.RequestKind, types.KindAnthropicMessages)
	}
	if !got.Streaming {
		t.Errorf("Streaming=false, want true")
	}
	if got.Status != types.RequestStatusRateLimited {
		t.Errorf("Status=%q, want %q", got.Status, types.RequestStatusRateLimited)
	}
	if got.ErrorMessage != "denied" {
		t.Errorf("ErrorMessage=%q, want denied", got.ErrorMessage)
	}
	if got.ExtraUsageReason != "client_restriction" {
		t.Errorf("ExtraUsageReason=%q, want client_restriction", got.ExtraUsageReason)
	}
	if got.ClientIP != "10.0.0.5:54321" {
		t.Errorf("ClientIP=%q, want 10.0.0.5:54321", got.ClientIP)
	}
	if got.Metadata["user_agent"] != "foo/1.0" {
		t.Errorf("metadata[user_agent]=%q, want foo/1.0", got.Metadata["user_agent"])
	}
}

func TestBuildRejectedRequestRow_MissingProjectOrAPIKey(t *testing.T) {
	mk := func(seed func(context.Context) context.Context) *http.Request {
		r := httptest.NewRequest("POST", "/v1/messages", nil)
		if seed != nil {
			r = r.WithContext(seed(r.Context()))
		}
		return r
	}
	cases := []struct {
		name string
		seed func(context.Context) context.Context
	}{
		{"no project, no apikey", nil},
		{"project only", func(ctx context.Context) context.Context {
			return context.WithValue(ctx, ctxProject, &types.Project{ID: "p1"})
		}},
		{"apikey only", func(ctx context.Context) context.Context {
			return context.WithValue(ctx, ctxAPIKey, &types.APIKey{ID: "k1"})
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildRejectedRequestRow(mk(c.seed), types.RequestStatusRateLimited, "msg", "")
			if got != nil {
				t.Errorf("want nil, got %+v", got)
			}
		})
	}
}

func TestBuildRejectedRequestRow_NoUserAgent(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/messages", nil)
	ctx := context.WithValue(r.Context(), ctxProject, &types.Project{ID: "p1"})
	ctx = context.WithValue(ctx, ctxAPIKey, &types.APIKey{ID: "k1"})
	r = r.WithContext(ctx)

	got := buildRejectedRequestRow(r, types.RequestStatusRateLimited, "msg", "")
	if got == nil {
		t.Fatalf("got nil")
	}
	if _, ok := got.Metadata["user_agent"]; ok {
		t.Errorf("metadata.user_agent must be absent when no UA header, got %q", got.Metadata["user_agent"])
	}
}

func TestBuildRejectedRequestRow_ModelFallsBackToBodyPeek(t *testing.T) {
	// No ModelRef in context — must fall back to peekModel reading the body.
	r := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"x"}`))
	ctx := context.WithValue(r.Context(), ctxProject, &types.Project{ID: "p1"})
	ctx = context.WithValue(ctx, ctxAPIKey, &types.APIKey{ID: "k1"})
	r = r.WithContext(ctx)

	got := buildRejectedRequestRow(r, types.RequestStatusRateLimited, "msg", "")
	if got == nil {
		t.Fatalf("got nil")
	}
	if got.Model != "x" {
		t.Errorf("Model=%q, want x", got.Model)
	}
	if got.Provider != "" {
		t.Errorf("Provider=%q, want empty (no ModelRef in ctx)", got.Provider)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run TestBuildRejectedRequestRow -v`
Expected: FAIL with `undefined: buildRejectedRequestRow`.

- [ ] **Step 3: Implement `buildRejectedRequestRow`**

Append to `internal/proxy/rejected_request.go`:

```go
// buildRejectedRequestRow assembles a *types.Request suitable for
// fire-and-forget persistence on 4xx pre-handler rejections. It captures
// every field that's knowable from the *http.Request + context at the
// rejection point — UA, kind, streaming, oauth grant, trace id, client
// ip, model, provider — so the row matches the shape of successful rows
// (handler.go CreateRequest) except for the rejection-specific
// status / error_message / extra_usage_reason fields.
//
// Returns nil when Project or APIKey is missing from context. That's
// the same skip-on-missing-attribution policy the original
// emitGuardRejection used: 5xx infra paths that bypass auth would
// otherwise produce orphan rows.
func buildRejectedRequestRow(
	r *http.Request,
	status string,
	errMsg string,
	extraUsageReason string,
) *types.Request {
	if r == nil {
		return nil
	}
	project := ProjectFromContext(r.Context())
	apiKey := APIKeyFromContext(r.Context())
	if project == nil || apiKey == nil {
		return nil
	}

	model := ""
	provider := ""
	if m := ModelFromContext(r.Context()); m != nil {
		model = m.Name
		provider = m.Publisher
	}
	if model == "" {
		// Fall back to body shape — covers the case where ResolveModel
		// ran but the catalog had no entry (the success path's pending
		// row also stores whatever the client sent).
		model = peekModel(r)
	}

	metadata := map[string]string{}
	if ua := r.Header.Get("User-Agent"); ua != "" {
		metadata["user_agent"] = ua
	}

	return &types.Request{
		ProjectID:        project.ID,
		APIKeyID:         apiKey.ID,
		OAuthGrantID:     OAuthGrantIDFromContext(r.Context()),
		CreatedBy:        apiKey.CreatedBy,
		TraceID:          TraceIDFromContext(r.Context()),
		Provider:         provider,
		RequestKind:      requestKindFromRequest(r),
		Model:            model,
		Streaming:        peekStreaming(r),
		Status:           status,
		ClientIP:         r.RemoteAddr,
		ErrorMessage:     errMsg,
		ExtraUsageReason: extraUsageReason,
		Metadata:         metadata,
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run TestBuildRejectedRequestRow -v`
Expected: PASS for all four tests.

Also run the whole package to make sure nothing broke:

Run: `go test ./internal/proxy/ -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/rejected_request.go internal/proxy/rejected_request_test.go
git commit -m "feat(proxy): add buildRejectedRequestRow helper

Assembles a *types.Request whose shape matches the success path's
CreateRequest row — UA in metadata, request_kind from path, streaming
from body/path, oauth_grant_id from context, provider from catalog
model. Returns nil when Project or APIKey is absent so 5xx infra paths
skip persistence the way they always have. Used by the two existing
rejection writers in follow-up commits.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Wire `emitGuardRejection` to the helper

**Files:**
- Modify: `internal/proxy/extra_usage_guard_middleware.go:381-395` (the persistence tail)
- Modify: `internal/proxy/extra_usage_guard_middleware_test.go:297-361` (extend `TestExtraUsageGuard_ClientRestriction_NotEnabled_LogsRequest`)

**Interfaces:**
- Consumes: `buildRejectedRequestRow` (Task 3).
- Produces: nothing new — `emitGuardRejection`'s signature is unchanged.

The slog block in `emitGuardRejection` is preserved verbatim: it carries `client_kind`, `user_id_shape`, `opencode_header`, `codex_session`, `openclaw_match`, etc. that are intentionally not persisted to the row.

- [ ] **Step 1: Extend the existing guard test with new field assertions**

Open `internal/proxy/extra_usage_guard_middleware_test.go` and find `TestExtraUsageGuard_ClientRestriction_NotEnabled_LogsRequest` (starts at line 297). Apply two edits:

(a) After `r := httptest.NewRequest("POST", "/v1/messages", body)` (line 304), add a `User-Agent` header and seed a ModelRef + OAuthGrantID in context. The full edited block becomes:

```go
	body := strings.NewReader(`{"model":"claude-haiku-4-5","stream":true}`)
	r := httptest.NewRequest("POST", "/v1/messages", body)
	r.Header.Set("User-Agent", "foo/1.0")
	ctx := context.WithValue(r.Context(), ctxExtraUsageIntent, ExtraUsageIntent{Reason: "client_restriction"})
	ctx = context.WithValue(ctx, ctxProject, &types.Project{ID: "p1"})
	ctx = context.WithValue(ctx, ctxAPIKey, &types.APIKey{ID: "k1", CreatedBy: "u1"})
	ctx = context.WithValue(ctx, ctxTraceID, "trace-xyz")
	ctx = context.WithValue(ctx, ctxModel, &types.Model{Name: "claude-haiku-4-5", Publisher: types.PublisherAnthropic})
	ctx = context.WithValue(ctx, ctxOAuthGrantID, "grant-xyz")
	r = r.WithContext(ctx)
```

(b) After the existing `wantMsg` assertion block (around line 360, just before the function's closing `}`), append:

```go
	if got.RequestKind != types.KindAnthropicMessages {
		t.Errorf("RequestKind=%q, want %q", got.RequestKind, types.KindAnthropicMessages)
	}
	if !got.Streaming {
		t.Errorf("Streaming=false, want true")
	}
	if got.OAuthGrantID != "grant-xyz" {
		t.Errorf("OAuthGrantID=%q, want grant-xyz", got.OAuthGrantID)
	}
	if got.Provider != types.PublisherAnthropic {
		t.Errorf("Provider=%q, want %q", got.Provider, types.PublisherAnthropic)
	}
	if got.Metadata["user_agent"] != "foo/1.0" {
		t.Errorf("metadata[user_agent]=%q, want foo/1.0", got.Metadata["user_agent"])
	}
```

- [ ] **Step 2: Run the extended test to verify it fails**

Run: `go test ./internal/proxy/ -run TestExtraUsageGuard_ClientRestriction_NotEnabled_LogsRequest -v`
Expected: FAIL — multiple assertion errors (`RequestKind=""`, `Streaming=false`, `OAuthGrantID=""`, `Provider=""`, `metadata[user_agent]=""`).

- [ ] **Step 3: Rewrite `emitGuardRejection` persistence tail**

Open `internal/proxy/extra_usage_guard_middleware.go`. Find the function `emitGuardRejection` (starts at line 329). The slog block (lines 359-379, the `if logger != nil { logger.Warn(...) }`) is unchanged. Replace **only** the persistence tail (current lines 381-395):

Old:

```go
	if st == nil || project == nil || apiKey == nil {
		return
	}
	req := &types.Request{
		ProjectID:        project.ID,
		APIKeyID:         apiKey.ID,
		CreatedBy:        apiKey.CreatedBy,
		TraceID:          traceID,
		Model:            modelName,
		Status:           types.RequestStatusRateLimited,
		ClientIP:         r.RemoteAddr,
		ErrorMessage:     message,
		ExtraUsageReason: reason,
	}
	go st.CreateRequest(req)
}
```

New:

```go
	if st == nil {
		return
	}
	// buildRejectedRequestRow re-derives the same project/apiKey/model
	// the slog block above already extracted; we deliberately don't pass
	// them through to keep the helper context-driven (so callers from
	// other rejection sites — see ratelimit_middleware.go — share one
	// extraction path).
	req := buildRejectedRequestRow(r, types.RequestStatusRateLimited, message, reason)
	if req == nil {
		return
	}
	go st.CreateRequest(req)
}
```

The unused locals from the slog block (`project`, `apiKey`, `model`, `bodyModel`, `modelName`, `publisher`, `projectID`, `apiKeyID`, `createdBy`, `traceID`) all remain in scope above and are still consumed by `logger.Warn(...)` — do not remove them.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/proxy/ -run TestExtraUsageGuard_ClientRestriction_NotEnabled_LogsRequest -v`
Expected: PASS.

Run the full guard test suite to make sure none of the other tests broke:

Run: `go test ./internal/proxy/ -run TestExtraUsageGuard -v`
Expected: PASS — all sub-cases including `GlobalDisabled_LogsRequest`, `ClientRestriction_LogsRequestDetails`.

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/extra_usage_guard_middleware.go internal/proxy/extra_usage_guard_middleware_test.go
git commit -m "feat(proxy): persist UA/kind/streaming/oauth_grant on extra-usage 429s

emitGuardRejection now builds the persisted requests row via the
shared buildRejectedRequestRow helper, so the row carries the same
metadata.user_agent / request_kind / streaming / oauth_grant_id /
provider fields the success path populates. The slog
extra_usage_rejected line is unchanged.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Wire `logRateLimitRejectionMsg` to the helper

**Files:**
- Modify: `internal/proxy/ratelimit_middleware.go:103-118` (whole body of `logRateLimitRejectionMsg`)

**Interfaces:**
- Consumes: `buildRejectedRequestRow` (Task 3).
- Produces: nothing — function signature unchanged so the two call sites at lines 51 and 75 (and the wrapper `logRateLimitRejection` at line 98) don't need editing.

No new test is added here. The rationale is in the spec: this function is now a thin wrapper around `buildRejectedRequestRow`, and the helper has full unit coverage. The existing `RateLimitMiddleware` tests still exercise the wrapper end-to-end and would fail loudly if the call broke.

- [ ] **Step 1: Rewrite `logRateLimitRejectionMsg`**

Open `internal/proxy/ratelimit_middleware.go`. Replace lines 103-118 in full:

Old:

```go
func logRateLimitRejectionMsg(st *store.Store, r *http.Request, project *types.Project, apiKey *types.APIKey, msg string) {
	model := peekModel(r)
	traceID := TraceIDFromContext(r.Context())
	req := &types.Request{
		ProjectID:    project.ID,
		APIKeyID:     apiKey.ID,
		CreatedBy:    apiKey.CreatedBy,
		TraceID:      traceID,
		Provider:     "",
		Model:        model,
		Status:       types.RequestStatusRateLimited,
		ClientIP:     r.RemoteAddr,
		ErrorMessage: msg,
	}
	go st.CreateRequest(req)
}
```

New:

```go
// logRateLimitRejectionMsg persists a requests row for a classic
// rate-limit rejection. The project/apiKey parameters are kept for
// signature stability (call sites at lines 51 and 75 supply them
// already); buildRejectedRequestRow re-reads them from context to
// share one extraction path with the extra-usage guard's
// emitGuardRejection. ExtraUsageReason is empty because classic
// rate-limit rejections are not on the extra-usage path.
func logRateLimitRejectionMsg(st *store.Store, r *http.Request, _ *types.Project, _ *types.APIKey, msg string) {
	req := buildRejectedRequestRow(r, types.RequestStatusRateLimited, msg, "")
	if req == nil {
		return
	}
	go st.CreateRequest(req)
}
```

The `_ *types.Project, _ *types.APIKey` underscore-discard parameters preserve the signature so call sites need no change.

- [ ] **Step 2: Build to confirm signature compatibility**

Run: `go build ./internal/proxy/...`
Expected: success, no errors.

- [ ] **Step 3: Run all proxy tests**

Run: `go test ./internal/proxy/ -count=1`
Expected: PASS for the whole package. This is the catch-all check that no rate-limit middleware test or other consumer regressed.

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/ratelimit_middleware.go
git commit -m "feat(proxy): persist UA/kind/streaming/oauth_grant on classic 4xx rate-limit rejections

logRateLimitRejectionMsg now delegates row construction to the
shared buildRejectedRequestRow helper, putting it in parity with
emitGuardRejection. requests rows for classic rate-limit rejections
gain metadata.user_agent, request_kind, streaming, oauth_grant_id,
and provider. Function signature is preserved; call sites are
unchanged.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Repository-wide sanity check

**Files:** none modified.

This task is the final gate before declaring done. It runs the project's whole test suite and the linter (if one is configured) to catch any cross-package consequence of the changes.

- [ ] **Step 1: Full test suite**

Run: `go test ./... -count=1`
Expected: PASS across the repo. If any pre-existing flaky test fails, re-run once; if it still fails, investigate whether it's related to the changes.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no findings.

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: success.

- [ ] **Step 4: Confirm spec coverage by skimming the diff**

Run: `git log --oneline main..HEAD`
Expected: five commits (Tasks 1-5), titles consistent with the spec sections.

Run: `git diff main --stat`
Expected: three Go files modified, two created, roughly:
- `internal/proxy/rejected_request.go` (new, ~110 lines)
- `internal/proxy/rejected_request_test.go` (new, ~200 lines)
- `internal/proxy/extra_usage_guard_middleware.go` (small diff, ~15 lines net)
- `internal/proxy/ratelimit_middleware.go` (small diff, ~5 lines net)
- `internal/proxy/extra_usage_guard_middleware_test.go` (small diff, ~15 lines net)

If file count, line count, or naming differs significantly from this expectation, re-read the spec section for the affected task and reconcile.

No commit at this task — it's a verification pass only.
