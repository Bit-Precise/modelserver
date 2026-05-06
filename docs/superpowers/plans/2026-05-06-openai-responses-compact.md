# OpenAI `/v1/responses/compact` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `POST /v1/responses/compact` to modelserver as a thin pass-through proxy for direct OpenAI and ChatGPT-subscription Codex channels, billed exactly like `/v1/responses`.

**Architecture:** New request kind `openai_responses_compact`; new `HandleResponsesCompact` handler that delegates to the existing `handleProxyRequest` pipeline; new chi route inside `/v1`; forward-only DB migration that auto-extends eligible existing routes. Reuses `OpenAITransformer` and `CodexTransformer` unchanged; reuses `ParseOpenAINonStreamingResponse` for billing.

**Tech Stack:** Go 1.22+, chi v5, pgx v5, PostgreSQL.

**Spec:** `docs/superpowers/specs/2026-05-06-openai-responses-compact-design.md`

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/types/request_kind.go` | Modify | Add `KindOpenAIResponsesCompact` constant + slice entry. |
| `internal/types/request_kind_test.go` | Modify | Update size assertion; add positive lookup. |
| `internal/proxy/handler.go` | Modify | Add 3-line `HandleResponsesCompact` method. |
| `internal/proxy/router.go` | Modify | Register `r.Post("/responses/compact", …)` inside `r.Route("/v1", …)`. |
| `internal/proxy/router_test.go` | Modify | Add `TestMountRoutes_ResponsesCompactIsRegistered`. |
| `internal/proxy/router_engine_test.go` | Modify | Add `TestMatch_RespectsResponsesCompactKind`. |
| `internal/proxy/codex_test.go` | Modify | Add `TestDirectorSetCodexUpstream_ResponsesCompactPath`. |
| `internal/proxy/openai_parser_test.go` | Modify | Add `TestParseOpenAINonStreamingResponse_CompactBodyNoUsage`. |
| `internal/store/migrations/036_responses_compact_request_kind.sql` | Create | CHECK-constraint update + idempotent backfill. |
| `internal/store/migrations_036_test.go` | Create | DB-gated regression test for the migration backfill predicate. |

No changes to: `provider_codex.go`, `provider_openai.go`, `openai_handler.go`, `openai_parser.go` (production code), `openai_stream.go`, `executor.go`, the load balancer, or any auth/token manager.

## Constraints / Conventions

- TDD: write the failing test, watch it fail, write minimal code, watch it pass, commit.
- One concept per commit. Use Conventional Commits (`feat(scope):`, `test(scope):`, etc.).
- Run `go vet ./...` and the touched package's tests before each commit.
- DB-gated tests skip when `TEST_DATABASE_URL` is unset (existing pattern in `internal/store/extra_usage_db_test.go`).

---

## Task 1 — Add `KindOpenAIResponsesCompact` constant

**Files:**
- Modify: `internal/types/request_kind.go`
- Modify: `internal/types/request_kind_test.go`

- [ ] **Step 1: Update the size assertion in the existing test**

Open `internal/types/request_kind_test.go`. Replace the `TestAllRequestKinds_ContainsExactlySeven` block (lines 21–25) with:

```go
func TestAllRequestKinds_ContainsExactlyEight(t *testing.T) {
	if got := len(AllRequestKinds); got != 8 {
		t.Errorf("len(AllRequestKinds) = %d, want 8", got)
	}
}

func TestIsValidRequestKind_OpenAIResponsesCompact(t *testing.T) {
	if !IsValidRequestKind(KindOpenAIResponsesCompact) {
		t.Errorf("IsValidRequestKind(%q) = false, want true", KindOpenAIResponsesCompact)
	}
}
```

- [ ] **Step 2: Run test to verify failure**

Run: `go test ./internal/types/ -run 'TestAllRequestKinds_ContainsExactlyEight|TestIsValidRequestKind_OpenAIResponsesCompact' -v`

Expected: COMPILE FAIL with `undefined: KindOpenAIResponsesCompact`.

- [ ] **Step 3: Add the constant and slice entry**

In `internal/types/request_kind.go`, replace the entire file with:

```go
package types

const (
	KindAnthropicMessages       = "anthropic_messages"
	KindAnthropicCountTokens    = "anthropic_count_tokens"
	KindOpenAIChatCompletions   = "openai_chat_completions"
	KindOpenAIResponses         = "openai_responses"
	KindOpenAIResponsesCompact  = "openai_responses_compact"
	KindOpenAIImagesGenerations = "openai_images_generations"
	KindOpenAIImagesEdits       = "openai_images_edits"
	KindGoogleGenerateContent   = "google_generate_content"
)

var AllRequestKinds = []string{
	KindAnthropicMessages,
	KindAnthropicCountTokens,
	KindOpenAIChatCompletions,
	KindOpenAIResponses,
	KindOpenAIResponsesCompact,
	KindOpenAIImagesGenerations,
	KindOpenAIImagesEdits,
	KindGoogleGenerateContent,
}

func IsValidRequestKind(s string) bool {
	for _, k := range AllRequestKinds {
		if k == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests in the types package**

Run: `go test ./internal/types/ -v`

Expected: all pass, including the two new ones from Step 1.

- [ ] **Step 5: Commit**

```bash
git add internal/types/request_kind.go internal/types/request_kind_test.go
git commit -m "feat(types): add openai_responses_compact request kind"
```

---

## Task 2 — Add the codex director regression test

The director already strips `/v1` and `path.Join`s, so this test is expected to PASS the first time. It locks the behaviour against future regressions.

**Files:**
- Modify: `internal/proxy/codex_test.go`

- [ ] **Step 1: Add the test**

Append to `internal/proxy/codex_test.go` (immediately after `TestDirectorSetCodexUpstream_NonV1PathPassesThrough`):

```go
func TestDirectorSetCodexUpstream_ResponsesCompactPath(t *testing.T) {
	// /v1/responses/compact must arrive at the codex backend as
	// /backend-api/codex/responses/compact (no /v1, single /responses).
	r := httptest.NewRequest(http.MethodPost, "http://proxy.local/v1/responses/compact", nil)
	directorSetCodexUpstream(r, "", "tok", "", "up-1")
	if r.URL.Path != "/backend-api/codex/responses/compact" {
		t.Errorf("Path = %q, want /backend-api/codex/responses/compact", r.URL.Path)
	}
}
```

- [ ] **Step 2: Run test to verify it passes**

Run: `go test ./internal/proxy/ -run TestDirectorSetCodexUpstream_ResponsesCompactPath -v`

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/proxy/codex_test.go
git commit -m "test(proxy): lock codex director path for /v1/responses/compact"
```

---

## Task 3 — Add the OpenAI parser regression test

The parser already tolerates a missing `usage` field via Go's zero-value semantics. This test pins that behaviour for the compact response shape.

**Files:**
- Modify: `internal/proxy/openai_parser_test.go`

- [ ] **Step 1: Add the test**

Append to `internal/proxy/openai_parser_test.go` (after `TestParseOpenAINonStreamingResponse_InvalidJSON`):

```go
func TestParseOpenAINonStreamingResponse_CompactBodyNoUsage(t *testing.T) {
	// Compact endpoint may return only `output` with no `usage` field.
	// Parser must zero-default usage and not error.
	body := []byte(`{"output":[{"type":"message","content":"x"}]}`)

	model, respID, usage, err := ParseOpenAINonStreamingResponse(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if model != "" {
		t.Errorf("model = %q, want empty", model)
	}
	if respID != "" {
		t.Errorf("respID = %q, want empty", respID)
	}
	if usage.InputTokens != 0 || usage.OutputTokens != 0 || usage.TotalTokens != 0 {
		t.Errorf("usage non-zero: %+v", usage)
	}
	if usage.InputTokensDetails.CachedTokens != 0 {
		t.Errorf("cached_tokens = %d, want 0", usage.InputTokensDetails.CachedTokens)
	}
}

func TestParseOpenAINonStreamingResponse_CompactBodyWithUsage(t *testing.T) {
	// Some upstreams may include usage on the compact response. When present,
	// it MUST be parsed identically to /v1/responses.
	body := []byte(`{"output":[],"id":"resp_compact_1","model":"gpt-5","usage":{"input_tokens":42,"output_tokens":8,"total_tokens":50,"input_tokens_details":{"cached_tokens":10}}}`)

	model, respID, usage, err := ParseOpenAINonStreamingResponse(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if model != "gpt-5" {
		t.Errorf("model = %q, want gpt-5", model)
	}
	if respID != "resp_compact_1" {
		t.Errorf("respID = %q, want resp_compact_1", respID)
	}
	if usage.InputTokens != 42 || usage.OutputTokens != 8 {
		t.Errorf("usage = %+v, want input=42 output=8", usage)
	}
	if usage.InputTokensDetails.CachedTokens != 10 {
		t.Errorf("cached_tokens = %d, want 10", usage.InputTokensDetails.CachedTokens)
	}
}
```

- [ ] **Step 2: Run tests to verify both pass**

Run: `go test ./internal/proxy/ -run 'TestParseOpenAINonStreamingResponse_CompactBody' -v`

Expected: PASS for both subtests.

- [ ] **Step 3: Commit**

```bash
git add internal/proxy/openai_parser_test.go
git commit -m "test(proxy): cover compact-shaped bodies in openai parser"
```

---

## Task 4 — Add the route + handler

**Files:**
- Modify: `internal/proxy/router_test.go`
- Modify: `internal/proxy/handler.go`
- Modify: `internal/proxy/router.go`

- [ ] **Step 1: Write the failing route-registration test**

Append to `internal/proxy/router_test.go` (after `TestMountRoutes_ImageEndpointsAreRegistered`):

```go
func TestMountRoutes_ResponsesCompactIsRegistered(t *testing.T) {
	r := chi.NewRouter()
	MountRoutes(
		r,
		nil,
		&Handler{},
		config.TraceConfig{},
		nil,
		nil,
		config.ExtraUsageConfig{},
		16<<20,
		200<<20,
		nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
	)

	req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{"model":"gpt-5","input":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("/v1/responses/compact was not registered; got %d body %q", w.Code, w.Body.String())
	}
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d before auth", w.Code, http.StatusUnauthorized)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (404)**

Run: `go test ./internal/proxy/ -run TestMountRoutes_ResponsesCompactIsRegistered -v`

Expected: FAIL with `/v1/responses/compact was not registered; got 404 …`.

- [ ] **Step 3: Add the handler method**

Open `internal/proxy/handler.go`. Locate `HandleResponses` (around line 80). Immediately after the `HandleResponses` function (i.e. between lines 82 and 84), insert:

```go
// HandleResponsesCompact proxies OpenAI /v1/responses/compact (unary).
// Routes are matched against KindOpenAIResponsesCompact.
func (h *Handler) HandleResponsesCompact(w http.ResponseWriter, r *http.Request) {
	h.handleProxyRequest(w, r, IngressOpenAI, types.KindOpenAIResponsesCompact)
}
```

- [ ] **Step 4: Register the route**

Open `internal/proxy/router.go`. Inside the `r.Route("/v1", func(r chi.Router) {…})` block, add a line after `r.Post("/responses", handler.HandleResponses)`:

```go
		r.Post("/responses", handler.HandleResponses)
		r.Post("/responses/compact", handler.HandleResponsesCompact)
		r.Post("/chat/completions", handler.HandleChatCompletions)
```

(The existing two surrounding lines are shown for context — only the middle line is new.)

- [ ] **Step 5: Run the route-registration test to verify it passes**

Run: `go test ./internal/proxy/ -run TestMountRoutes_ResponsesCompactIsRegistered -v`

Expected: PASS (status 401 — auth middleware rejects with no API key, which is what the test asserts).

- [ ] **Step 6: Run the full proxy package to catch regressions**

Run: `go test ./internal/proxy/ -count=1`

Expected: PASS for all tests.

- [ ] **Step 7: Commit**

```bash
git add internal/proxy/handler.go internal/proxy/router.go internal/proxy/router_test.go
git commit -m "feat(proxy): wire POST /v1/responses/compact handler and route"
```

---

## Task 5 — Router-engine kind-matching test

Verify the engine matches the new kind to a route only when the route's `request_kinds` contains it.

**Files:**
- Modify: `internal/proxy/router_engine_test.go`

- [ ] **Step 1: Write the test**

Append to `internal/proxy/router_engine_test.go`:

```go
// TestMatch_RespectsResponsesCompactKind ensures the router only matches
// /v1/responses/compact traffic to routes whose request_kinds explicitly
// include openai_responses_compact. A route configured for openai_responses
// must NOT silently absorb compact traffic.
func TestMatch_RespectsResponsesCompactKind(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	upstreams := []types.Upstream{
		{ID: "up-openai", Provider: types.ProviderOpenAI, Status: types.UpstreamStatusActive, Weight: 1, SupportedModels: []string{"gpt-5"}},
	}
	groups := []store.UpstreamGroupWithMembers{{
		UpstreamGroup: types.UpstreamGroup{ID: "grp", Name: "grp", LBPolicy: types.LBPolicyWeightedRandom, Status: "active"},
		Members: []store.UpstreamGroupMemberDetail{
			{UpstreamGroupMember: types.UpstreamGroupMember{UpstreamGroupID: "grp", UpstreamID: "up-openai"}},
		},
	}}

	t.Run("responses-only route does not match compact", func(t *testing.T) {
		routes := []types.Route{{
			ID:              "r1",
			ModelNames:      []string{"gpt-5"},
			RequestKinds:    []string{types.KindOpenAIResponses},
			UpstreamGroupID: "grp",
			MatchPriority:   1,
			Status:          "active",
		}}
		r := NewRouter(upstreams, groups, routes, nil, logger, time.Hour, nil, nil, nil)
		if _, err := r.Match("", "gpt-5", types.KindOpenAIResponsesCompact); err == nil {
			t.Fatal("expected no-match error for compact kind, got nil")
		}
	})

	t.Run("route with both kinds matches both", func(t *testing.T) {
		routes := []types.Route{{
			ID:              "r2",
			ModelNames:      []string{"gpt-5"},
			RequestKinds:    []string{types.KindOpenAIResponses, types.KindOpenAIResponsesCompact},
			UpstreamGroupID: "grp",
			MatchPriority:   1,
			Status:          "active",
		}}
		r := NewRouter(upstreams, groups, routes, nil, logger, time.Hour, nil, nil, nil)
		if _, err := r.Match("", "gpt-5", types.KindOpenAIResponses); err != nil {
			t.Fatalf("Match(openai_responses) failed: %v", err)
		}
		if _, err := r.Match("", "gpt-5", types.KindOpenAIResponsesCompact); err != nil {
			t.Fatalf("Match(openai_responses_compact) failed: %v", err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify both subtests pass**

Run: `go test ./internal/proxy/ -run TestMatch_RespectsResponsesCompactKind -v`

Expected: PASS for both subtests.

- [ ] **Step 3: Commit**

```bash
git add internal/proxy/router_engine_test.go
git commit -m "test(proxy): pin router-engine matching for openai_responses_compact"
```

---

## Task 6 — Database migration 036

**Files:**
- Create: `internal/store/migrations/036_responses_compact_request_kind.sql`

- [ ] **Step 1: Verify the next migration number is 036**

Run: `ls internal/store/migrations/ | sort | tail -3`

Expected output (highest existing number must be 035):
```
033_deepseek_v4.sql
034_rename_bedrock_provider.sql
035_seed_catalog_default_credit_rates.sql
```

If a 036_*.sql already exists, choose the next free integer and rename the file/test accordingly.

- [ ] **Step 2: Write the migration file**

Create `internal/store/migrations/036_responses_compact_request_kind.sql` with the following exact content:

```sql
-- 036_responses_compact_request_kind.sql
--
-- Adds the openai_responses_compact request kind, used by the new
-- POST /v1/responses/compact route. The route is a thin pass-through
-- to the OpenAI Responses API compact endpoint
-- (https://api.openai.com/v1/responses/compact) and to the equivalent
-- ChatGPT-subscription codex endpoint
-- (https://chatgpt.com/backend-api/codex/responses/compact).
--
-- Auto-extends every existing route that already serves
-- openai_responses AND whose upstream group is purely openai/codex,
-- so callers get compact support without an admin action. Routes
-- whose group includes vertex-openai or bedrock-openai (neither of
-- which exposes a real compact endpoint) are skipped — operators
-- who want compact on those routes must opt in manually.
--
-- Idempotent: the NOT … = ANY guard skips already-extended routes.
-- Forward-only: removing the kind from the CHECK in a future
-- migration would require first stripping it from every route that
-- references it.

BEGIN;

ALTER TABLE routes DROP CONSTRAINT routes_request_kinds_valid;
ALTER TABLE routes ADD CONSTRAINT routes_request_kinds_valid CHECK (
    request_kinds <@ ARRAY[
        'anthropic_messages',
        'anthropic_count_tokens',
        'openai_chat_completions',
        'openai_responses',
        'openai_responses_compact',
        'google_generate_content',
        'openai_images_generations',
        'openai_images_edits'
    ]::TEXT[]
    AND array_length(request_kinds, 1) >= 1
);

WITH eligible AS (
    SELECT rt.id
    FROM routes rt
    WHERE 'openai_responses' = ANY(rt.request_kinds)
      AND NOT 'openai_responses_compact' = ANY(rt.request_kinds)
      AND NOT EXISTS (
          SELECT 1
          FROM upstream_group_members m
          JOIN upstreams u ON u.id = m.upstream_id
          WHERE m.upstream_group_id = rt.upstream_group_id
            AND u.provider NOT IN ('openai', 'codex')
      )
)
UPDATE routes
SET request_kinds = array_append(request_kinds, 'openai_responses_compact')
WHERE id IN (SELECT id FROM eligible);

COMMIT;
```

- [ ] **Step 3: Verify the file was written correctly**

Run: `head -5 internal/store/migrations/036_responses_compact_request_kind.sql`

Expected: the comment header lines starting with `-- 036_responses_compact_request_kind.sql`.

- [ ] **Step 4: Commit the migration file**

```bash
git add internal/store/migrations/036_responses_compact_request_kind.sql
git commit -m "feat(db): migration 036 — openai_responses_compact request kind"
```

---

## Task 7 — DB-gated migration regression test

This test runs only when `TEST_DATABASE_URL` is set, mirroring the pattern in `internal/store/extra_usage_db_test.go`. It seeds three routes (one openai-only, one mixed openai+vertex-openai, one openai-only that already has the compact kind) and asserts the migration's backfill predicate is correct.

**Files:**
- Create: `internal/store/migrations_036_test.go`

- [ ] **Step 1: Write the test file**

Create `internal/store/migrations_036_test.go` with the following exact content:

```go
package store

import (
	"context"
	"testing"
)

// TestMigration036_BackfillsOnlyOpenAIOrCodexRoutes asserts the eligibility
// predicate of migration 036:
//
//   - A route on a purely openai (or codex) upstream group whose
//     request_kinds includes openai_responses MUST be auto-extended with
//     openai_responses_compact.
//   - A route whose group includes any non-openai/non-codex upstream
//     (e.g. vertex-openai, bedrock-openai) MUST NOT be auto-extended.
//   - A route that already lists openai_responses_compact MUST be left
//     unchanged (no duplicate, idempotent).
//
// The migration is applied by openTestStore on first connect. This test
// seeds rows AFTER migrations have applied, then re-runs the migration's
// UPDATE statement to exercise the predicate against the new fixture.
// Each row name is suffixed with gen_random_uuid() so re-runs and parallel
// tests are hermetic.
func TestMigration036_BackfillsOnlyOpenAIOrCodexRoutes(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	// Helper: insert an upstream with a known provider; name suffixed for uniqueness.
	insertUpstream := func(label, provider string) string {
		var id string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO upstreams (name, provider, status, weight, supported_models)
			VALUES ('mig036-' || $1 || '-' || gen_random_uuid()::text, $2, 'active', 1, ARRAY['gpt-5'])
			RETURNING id`, label, provider).Scan(&id); err != nil {
			t.Fatalf("insert upstream %s: %v", label, err)
		}
		return id
	}

	// Helper: insert an upstream group containing the given upstreams.
	insertGroup := func(label string, upstreamIDs ...string) string {
		var gid string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO upstream_groups (name, lb_policy, status)
			VALUES ('mig036-' || $1 || '-' || gen_random_uuid()::text, 'weighted_random', 'active')
			RETURNING id`, label).Scan(&gid); err != nil {
			t.Fatalf("insert group %s: %v", label, err)
		}
		for _, uid := range upstreamIDs {
			if _, err := st.pool.Exec(ctx, `
				INSERT INTO upstream_group_members (upstream_group_id, upstream_id)
				VALUES ($1, $2)`, gid, uid); err != nil {
				t.Fatalf("insert member: %v", err)
			}
		}
		return gid
	}

	// Helper: insert a route with the given request_kinds against a group.
	insertRoute := func(label, gid string, kinds []string) string {
		var rid string
		if err := st.pool.QueryRow(ctx, `
			INSERT INTO routes (model_names, request_kinds, upstream_group_id, match_priority, status)
			VALUES (ARRAY['gpt-5'], $1, $2, 1, 'active')
			RETURNING id`, kinds, gid).Scan(&rid); err != nil {
			t.Fatalf("insert route %s: %v", label, err)
		}
		return rid
	}

	upOpenAI := insertUpstream("openai", "openai")
	upCodex := insertUpstream("codex", "codex")
	upVertexOA := insertUpstream("vertex-openai", "vertex-openai")

	gOpenAIOnly := insertGroup("openai-only", upOpenAI)
	gOpenAICodex := insertGroup("openai-codex", upOpenAI, upCodex)
	gMixed := insertGroup("mixed", upOpenAI, upVertexOA)

	rOpenAIOnly := insertRoute("openai-only", gOpenAIOnly, []string{"openai_responses"})
	rOpenAICodex := insertRoute("openai-codex", gOpenAICodex, []string{"openai_responses"})
	rMixed := insertRoute("mixed", gMixed, []string{"openai_responses"})
	rAlready := insertRoute("already-has-compact", gOpenAIOnly, []string{"openai_responses", "openai_responses_compact"})

	t.Cleanup(func() {
		// Best-effort cleanup so we don't leave fixtures behind.
		for _, rid := range []string{rOpenAIOnly, rOpenAICodex, rMixed, rAlready} {
			st.pool.Exec(ctx, `DELETE FROM routes WHERE id = $1`, rid)
		}
		for _, gid := range []string{gOpenAIOnly, gOpenAICodex, gMixed} {
			st.pool.Exec(ctx, `DELETE FROM upstream_group_members WHERE upstream_group_id = $1`, gid)
			st.pool.Exec(ctx, `DELETE FROM upstream_groups WHERE id = $1`, gid)
		}
		for _, uid := range []string{upOpenAI, upCodex, upVertexOA} {
			st.pool.Exec(ctx, `DELETE FROM upstreams WHERE id = $1`, uid)
		}
	})

	// Re-run the migration's UPDATE statement against the seeded fixture.
	if _, err := st.pool.Exec(ctx, `
		WITH eligible AS (
			SELECT rt.id
			FROM routes rt
			WHERE 'openai_responses' = ANY(rt.request_kinds)
			  AND NOT 'openai_responses_compact' = ANY(rt.request_kinds)
			  AND NOT EXISTS (
				  SELECT 1
				  FROM upstream_group_members m
				  JOIN upstreams u ON u.id = m.upstream_id
				  WHERE m.upstream_group_id = rt.upstream_group_id
					AND u.provider NOT IN ('openai', 'codex')
			  )
		)
		UPDATE routes
		SET request_kinds = array_append(request_kinds, 'openai_responses_compact')
		WHERE id IN (SELECT id FROM eligible)`); err != nil {
		t.Fatalf("re-run backfill: %v", err)
	}

	kindsFor := func(rid string) []string {
		var kinds []string
		if err := st.pool.QueryRow(ctx, `SELECT request_kinds FROM routes WHERE id = $1`, rid).Scan(&kinds); err != nil {
			t.Fatalf("read kinds for %s: %v", rid, err)
		}
		return kinds
	}

	hasKind := func(kinds []string, kind string) bool {
		for _, k := range kinds {
			if k == kind {
				return true
			}
		}
		return false
	}

	countKind := func(kinds []string, kind string) int {
		n := 0
		for _, k := range kinds {
			if k == kind {
				n++
			}
		}
		return n
	}

	if got := kindsFor(rOpenAIOnly); !hasKind(got, "openai_responses_compact") {
		t.Errorf("openai-only route kinds = %v, want includes openai_responses_compact", got)
	}
	if got := kindsFor(rOpenAICodex); !hasKind(got, "openai_responses_compact") {
		t.Errorf("openai-codex route kinds = %v, want includes openai_responses_compact", got)
	}
	if got := kindsFor(rMixed); hasKind(got, "openai_responses_compact") {
		t.Errorf("mixed route kinds = %v, want NOT to include openai_responses_compact", got)
	}
	if got := kindsFor(rAlready); countKind(got, "openai_responses_compact") != 1 {
		t.Errorf("already-has-compact kinds = %v, want exactly one openai_responses_compact", got)
	}
}
```

The helper functions `hasKind` / `countKind` are scoped inside the test (closures) so they can't collide with package-level helpers. No additional imports beyond `context` and `testing`.

- [ ] **Step 2: Run with TEST_DATABASE_URL unset to confirm it skips cleanly**

Run: `go test ./internal/store/ -run TestMigration036_BackfillsOnlyOpenAIOrCodexRoutes -v`

Expected: SKIP with the message `set TEST_DATABASE_URL to run …`.

- [ ] **Step 3: Run with TEST_DATABASE_URL set against a throwaway DB**

Provision an empty Postgres database (e.g. via `docker run --rm -d -e POSTGRES_PASSWORD=pw -p 5433:5432 postgres:16`) and run:

```bash
TEST_DATABASE_URL='postgres://postgres:pw@localhost:5433/postgres?sslmode=disable' \
  go test ./internal/store/ -run TestMigration036_BackfillsOnlyOpenAIOrCodexRoutes -v
```

Expected: PASS. If it fails, the most likely cause is a column-name mismatch with the seed inserts — verify against `internal/store/migrations/021_route_request_kinds.sql` and the latest schema before debugging further.

- [ ] **Step 4: Commit**

```bash
git add internal/store/migrations_036_test.go
git commit -m "test(store): regression test for migration 036 backfill predicate"
```

---

## Task 8 — Verification across the touched packages

- [ ] **Step 1: Vet and full unit-test run**

Run:
```bash
go vet ./...
go test ./internal/types/ ./internal/proxy/ -count=1
```

Expected: no vet warnings; all tests pass.

- [ ] **Step 2: Quick chi route enumeration sanity check (optional but cheap)**

Run:
```bash
go test ./internal/proxy/ -run 'TestMountRoutes' -v
```

Expected: `TestMountRoutes_ImageEndpointsAreRegistered` and `TestMountRoutes_ResponsesCompactIsRegistered` both PASS.

- [ ] **Step 3: Manual smoke (do this before any deploy)**

Start modelserver against a dev DB with at least one openai-provider upstream and one route that includes `openai_responses_compact` in its `request_kinds`. Then:

```bash
curl -sS -X POST http://localhost:8080/v1/responses/compact \
  -H "Authorization: Bearer <api-key>" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-5","input":[{"type":"message","role":"user","content":"hi"}],"instructions":"summarise","tools":[],"parallel_tool_calls":false}' | jq
```

Expected: an HTTP 200 response with a JSON body containing an `output` array (or, if upstream rejects, the same error body that `/v1/responses` would surface — pass-through). In the modelserver logs, a `requests` row should be inserted with `request_kind=openai_responses_compact` and (if upstream returned `usage`) non-zero token counts.

Repeat with a `codex`-provider upstream to confirm the codex path works as well.

---

## Stretch / deferred

- **Full end-to-end billing test with stub upstream.** Mentioned in the spec under §5 but deferred from this plan: setting up a stub HTTP upstream + Collector + Store would dwarf the rest of the implementation, and the meaningful behaviours are already covered by the unit tests above (kind propagation in router engine, parser zero-default, route registration). Add only if a regression in compact billing is observed in production.

---

## Self-review notes (already addressed in this draft)

- Spec §1–§6 each map to at least one task: §1 architecture → Task 4; §2 files → Tasks 1/4/6; §3 data flow → Task 5 (kind matching) + Task 4 (route plumbing); §4 migration → Task 6; §5 testing → Tasks 2/3/4/5/7; §6 risks → noted in stretch section.
- No "TBD"/"TODO" placeholders. Every code step contains the literal code to type.
- Type/symbol consistency: `KindOpenAIResponsesCompact` introduced in Task 1 and referenced verbatim in Tasks 4, 5, 7. `HandleResponsesCompact` introduced in Task 4 and referenced in Tasks 4 (registration) and 8 (smoke).
- Migration filename `036_responses_compact_request_kind.sql` is consistent across Tasks 6, 7.
