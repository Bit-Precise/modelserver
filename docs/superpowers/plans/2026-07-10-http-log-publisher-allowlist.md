# HTTP-log Publisher Allowlist Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the hard-coded `publisher == "anthropic"` gate at `internal/proxy/handler.go:513-517` with a config-driven allowlist (`http_log.publishers`), default `["anthropic","openai"]`, so GPT/gpt-image-2/gpt-5.6 request+response bodies begin uploading to S3 after a fleet restart.

**Architecture:** Add `Publishers []string` field to `HttpLogConfig` with a viper default. Thread the config into `NewHandler` (already handed to `NewExecutor`). Handler precomputes an allowlist set in `NewHandler` and consults it in a new method `httpLogAllowsPublisher(pub string) bool`; the existing per-request gate at `handler.go:513-517` calls that method instead of the hard-coded `== types.PublisherAnthropic` check. The S3 upload pipeline (`internal/httplog/*`) is untouched — protocol-agnostic and already handles OpenAI streams and multipart uploads.

**Tech Stack:** Go 1.x, viper (config), chi (routing — unchanged), aws-sdk-go-v2 (S3 — unchanged).

## Global Constraints

- **Default allowlist:** `["anthropic","openai"]` (via `v.SetDefault("http_log.publishers", ...)` in `internal/config/config.go setDefaults`).
- **Normalization:** allowlist entries and the model's `Publisher` field are both compared after `strings.TrimSpace` + `strings.ToLower` on each side. Model catalog rows already store lowercase publishers; normalization only guards operator-yaml typos like `"OpenAI"`.
- **Nil / empty semantics:** `Publishers == nil` (impossible via viper defaults but possible in test-constructed `HttpLogConfig{}` literals) AND `Publishers = []string{}` (explicit) both mean "no publisher allowed"; the gate never fires. Only publishers explicitly listed pass.
- **Empty-string publisher (`Publisher == ""` in the catalog):** only allowed if `""` itself appears in the config allowlist. Conservative — the historical unlabelled rows from `031_seed_gpt_image_2.sql` and similar stay non-logging by default. This is not a Nil vs Empty distinction — it's a "what happens if the input to the check is an empty string" rule that applies uniformly.
- **`count_tokens` handler:** unchanged. Its explicit `HttpLogEnabled: false` override at `handler.go:621` remains and is unaffected by the new gate (it sets the flag AFTER the publisher check would have run in the sibling `handleProxyRequest` path).
- **`http_log.enabled: false`:** unchanged. When disabled, `httpLogger == nil` and the entire allowlist check short-circuits. This is the emergency kill switch.
- **`NewHandler` signature change:** takes one new parameter, `blCfg config.HttpLogConfig`, immediately after `bl *httplog.Logger`. Single caller (`cmd/modelserver/main.go:212`) — no test callers to update.
- **No wildcards.** A `"*"` entry does NOT expand to "all publishers". `"*"` compares as a literal string — matches only a hypothetical row whose `Publisher` field equals `"*"`, which no seeded row has. Rationale in spec §2.4: adding gemini/deepseek later must be an explicit operator opt-in, not a silent expansion when a catalog migration introduces a new publisher.

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `internal/config/config.go` | Modify | Add `Publishers []string` to `HttpLogConfig`; register `SetDefault("http_log.publishers", ...)` |
| `internal/config/config_test.go` | Modify | Extend `TestLoadDefaults` + add `TestLoadCustomValues`-style coverage for the allowlist and yaml override |
| `internal/proxy/handler.go` | Modify | Add `httpLogAllowlist map[string]struct{}` field, populate it in `NewHandler`, add `httpLogAllowsPublisher` method, replace the hard-coded gate at 513-517 |
| `internal/proxy/handler.go` (signature) | Modify | `NewHandler` takes `blCfg config.HttpLogConfig` after `bl` |
| `internal/proxy/handler_publisher_allowlist_test.go` | Create | Table-driven test of `httpLogAllowsPublisher` covering: default set, explicit `[anthropic]`, empty slice, nil slice, empty-string publisher, whitespace/case normalization |
| `cmd/modelserver/main.go` | Modify | Pass `cfg.HttpLog` to `NewHandler` |
| `config.example.yml` | Modify | Add commented `publishers:` block under `http_log:` |

No other files change. `internal/httplog/*`, `internal/proxy/executor.go`, provider transformers, catalog rows, migrations, dashboard — all untouched.

---

### Task 1: Config field + defaults + tests

**Files:**
- Modify: `internal/config/config.go` — `HttpLogConfig` struct (~line 201-212), `setDefaults` (~line 228)
- Modify: `internal/config/config_test.go` — add coverage to existing `TestLoadDefaults` and `TestLoadCustomValues`

**Interfaces:**
- Consumes: nothing new (viper mechanics already in place).
- Produces: `config.HttpLogConfig.Publishers []string`, defaulted to `["anthropic","openai"]` when a caller Loads config without setting the key. Task 2 wires this through `NewHandler`; Task 3 reads from it inside the handler.

- [ ] **Step 1: Write failing tests**

Open `internal/config/config_test.go`. First, extend the existing `TestLoadDefaults` function — locate its body (starts at line 18) and append a new assertion block near the end (right before the closing `}`). The exact insertion point: the last existing assertion in that function is the anchor; add these lines after it:

```go
	if got, want := cfg.HttpLog.Publishers, []string{"anthropic", "openai"}; !equalStringSlice(got, want) {
		t.Errorf("HttpLog.Publishers = %v, want %v", got, want)
	}
```

Then append the following NEW test function at the end of `internal/config/config_test.go`:

```go
// TestLoadHttpLogPublishersOverride verifies operator-supplied publishers
// override the default and that an explicit empty slice is respected
// (empty allowlist = no publisher logs, even with http_log.enabled=true).
func TestLoadHttpLogPublishersOverride(t *testing.T) {
	setValidJWTSecret(t)

	t.Run("explicit anthropic-only", func(t *testing.T) {
		cfg, err := Load([]byte(`
db:
  url: "postgres://x@y/z"
encryption:
  key: "12345678901234567890123456789012"
http_log:
  publishers: ["anthropic"]
`))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got, want := cfg.HttpLog.Publishers, []string{"anthropic"}; !equalStringSlice(got, want) {
			t.Errorf("HttpLog.Publishers = %v, want %v", got, want)
		}
	})

	t.Run("explicit empty slice", func(t *testing.T) {
		cfg, err := Load([]byte(`
db:
  url: "postgres://x@y/z"
encryption:
  key: "12345678901234567890123456789012"
http_log:
  publishers: []
`))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(cfg.HttpLog.Publishers) != 0 {
			t.Errorf("HttpLog.Publishers = %v, want empty", cfg.HttpLog.Publishers)
		}
	})
}

// equalStringSlice compares two string slices element by element in order.
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /root/coding/modelserver && go test ./internal/config/ -run 'TestLoadDefaults|TestLoadHttpLogPublishersOverride' -v`
Expected: compile error `cfg.HttpLog.Publishers undefined` (field not yet on struct). This proves the field is genuinely new, not a namespace collision.

- [ ] **Step 3: Add the field and default**

In `internal/config/config.go`, locate the `HttpLogConfig` struct definition (starts at line 201). It currently ends with `BufferSize int ...`. Add one more line inside the struct, immediately after `BufferSize`:

```go
	// Publishers is the allowlist of model publishers whose request+response
	// bodies get uploaded to S3. A request whose resolved model's publisher
	// field is not in this set skips upload even when http_log.enabled is
	// true and the model is otherwise routable. Default is
	// ["anthropic", "openai"] — set explicitly to ["anthropic"] to preserve
	// pre-2026-07 behavior, or expand as new providers land (e.g.
	// ["anthropic", "openai", "gemini"]). Empty-string publishers (unlabelled
	// catalog rows) are only allowed if "" itself appears here. No wildcard —
	// adding a publisher must be an explicit operator opt-in so a future
	// catalog migration that introduces a new publisher does not silently
	// enable body logging for it.
	Publishers []string `yaml:"publishers" mapstructure:"publishers"`
```

Then locate `setDefaults` (starts around line 228). Find the block that sets HTTP-log defaults — search for existing `v.SetDefault("http_log.` lines. If none exist yet (only `BindEnv`s), add the default near the other `http_log.*` lines (or, if no `http_log.*` line exists in `setDefaults`, add it after the `images.*` defaults for locality). Add:

```go
	v.SetDefault("http_log.publishers", []string{"anthropic", "openai"})
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /root/coding/modelserver && go test ./internal/config/ -v`
Expected: PASS on `TestLoadDefaults`, `TestLoadCustomValues`, `TestLoadHttpLogPublishersOverride`, and every other existing test in the package.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add http_log.publishers allowlist (default anthropic+openai)

New HttpLogConfig.Publishers field, defaulted via viper to
['anthropic','openai']. Empty slice = no logging (respected). Sets up
the data source for the handler-side gate change (next commit).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Handler gate + wiring + tests

**Files:**
- Modify: `internal/proxy/handler.go` — struct, `NewHandler`, replace gate at 513-517, add `httpLogAllowsPublisher` method
- Modify: `cmd/modelserver/main.go` — line 212 caller
- Create: `internal/proxy/handler_publisher_allowlist_test.go`

**Interfaces:**
- Consumes: `config.HttpLogConfig.Publishers []string` (from Task 1). Field is populated with `["anthropic","openai"]` when no explicit config was set.
- Produces: nothing exported. `Handler.httpLogAllowsPublisher(pub string) bool` is a lowercase method used only by `handler.go` internally.

- [ ] **Step 1: Write the failing test**

Create `internal/proxy/handler_publisher_allowlist_test.go`:

```go
package proxy

import (
	"testing"

	"github.com/modelserver/modelserver/internal/config"
)

// TestHandlerHttpLogAllowsPublisher table-tests every allowlist rule
// documented in the spec:
//   - default anthropic+openai allows both, denies others
//   - explicit [anthropic] excludes openai
//   - nil / empty slice deny all publishers
//   - empty-string publisher denied by default, allowed only when "" is
//     explicitly listed
//   - operator yaml typos "OpenAI" (case) or " anthropic " (whitespace)
//     match the corresponding catalog rows after normalization
func TestHandlerHttpLogAllowsPublisher(t *testing.T) {
	cases := []struct {
		name       string
		publishers []string
		input      string
		want       bool
	}{
		{"default allows anthropic", []string{"anthropic", "openai"}, "anthropic", true},
		{"default allows openai", []string{"anthropic", "openai"}, "openai", true},
		{"default denies gemini", []string{"anthropic", "openai"}, "gemini", false},
		{"default denies empty publisher", []string{"anthropic", "openai"}, "", false},
		{"anthropic-only denies openai", []string{"anthropic"}, "openai", false},
		{"anthropic-only allows anthropic", []string{"anthropic"}, "anthropic", true},
		{"nil slice denies anthropic", nil, "anthropic", false},
		{"empty slice denies anthropic", []string{}, "anthropic", false},
		{"empty in list allows empty", []string{""}, "", true},
		{"case-insensitive: OpenAI matches openai", []string{"OpenAI"}, "openai", true},
		{"case-insensitive: OpenAI matches OPENAI catalog row", []string{"openai"}, "OPENAI", true},
		{"whitespace trimmed on both sides", []string{" anthropic "}, "anthropic ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{}
			h.httpLogAllowlist = buildHttpLogAllowlist(config.HttpLogConfig{Publishers: tc.publishers})
			if got := h.httpLogAllowsPublisher(tc.input); got != tc.want {
				t.Errorf("httpLogAllowsPublisher(%q) with publishers=%v = %v, want %v",
					tc.input, tc.publishers, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/coding/modelserver && go test ./internal/proxy/ -run TestHandlerHttpLogAllowsPublisher -v`
Expected: compile error — `httpLogAllowlist` field undefined, `buildHttpLogAllowlist` and `httpLogAllowsPublisher` undefined. Good.

- [ ] **Step 3: Modify the Handler struct and NewHandler**

Open `internal/proxy/handler.go`. First, extend imports to include the config package. The current import block (lines 3-22) does NOT import `config`. Add it — the alphabetized position is right after `collector`:

```go
	"github.com/modelserver/modelserver/internal/collector"
	"github.com/modelserver/modelserver/internal/config"
	"github.com/modelserver/modelserver/internal/httplog"
```

Next, modify the `Handler` struct (lines 24-35). Add ONE new field at the bottom (after `httpLogger`):

```go
	httpLogAllowlist  map[string]struct{}
```

The struct after edit reads:

```go
// Handler handles proxied LLM API requests.
type Handler struct {
	executor          *Executor
	router            *Router
	store             *store.Store
	collector         *collector.Collector
	catalog           modelcatalog.Catalog
	logger            *slog.Logger
	maxBodySize       int64
	imagesMaxBodySize int64
	httpLogger        *httplog.Logger
	httpLogAllowlist  map[string]struct{}
}
```

Next, change the `NewHandler` signature (line 38). Add `blCfg config.HttpLogConfig` immediately after `bl *httplog.Logger`. Replace the whole `NewHandler` function (lines 38-50) with:

```go
// NewHandler creates a new proxy handler.
func NewHandler(executor *Executor, router *Router, st *store.Store, coll *collector.Collector, catalog modelcatalog.Catalog, logger *slog.Logger, maxBodySize int64, imagesMaxBodySize int64, bl *httplog.Logger, blCfg config.HttpLogConfig) *Handler {
	return &Handler{
		executor:          executor,
		router:            router,
		store:             st,
		collector:         coll,
		catalog:           catalog,
		logger:            logger,
		maxBodySize:       maxBodySize,
		imagesMaxBodySize: imagesMaxBodySize,
		httpLogger:        bl,
		httpLogAllowlist:  buildHttpLogAllowlist(blCfg),
	}
}

// buildHttpLogAllowlist normalizes cfg.Publishers into a lookup set. See
// httpLogAllowsPublisher for the match rules; this function is the write
// side of the same normalization contract (both sides apply
// strings.TrimSpace + strings.ToLower before comparing).
func buildHttpLogAllowlist(cfg config.HttpLogConfig) map[string]struct{} {
	m := make(map[string]struct{}, len(cfg.Publishers))
	for _, p := range cfg.Publishers {
		m[strings.ToLower(strings.TrimSpace(p))] = struct{}{}
	}
	return m
}

// httpLogAllowsPublisher reports whether the given model publisher is on
// the S3-upload allowlist. Comparison is case-insensitive and trims
// surrounding whitespace on both sides so an operator yaml entry like
// "OpenAI" or " anthropic " still matches. An empty-string publisher
// ("" — unlabelled catalog rows from migration 031) only matches when
// "" is explicitly in the allowlist; the default list does not include
// it, so unlabelled rows are non-logging by default.
func (h *Handler) httpLogAllowsPublisher(pub string) bool {
	_, ok := h.httpLogAllowlist[strings.ToLower(strings.TrimSpace(pub))]
	return ok
}
```

Then replace the gate at lines 513-517. Current block:

```go
	if h.httpLogger != nil {
		if m := ModelFromContext(r.Context()); m != nil && m.Publisher == types.PublisherAnthropic {
			reqCtx.HttpLogEnabled = true
		}
	}
```

Replace with:

```go
	if h.httpLogger != nil {
		if m := ModelFromContext(r.Context()); m != nil && h.httpLogAllowsPublisher(m.Publisher) {
			reqCtx.HttpLogEnabled = true
		}
	}
```

Note: `types.PublisherAnthropic` no longer referenced in this file after the edit. Grep to confirm there is no other reference in `handler.go` before removing any import — the `types` import stays because it's used elsewhere in the file (e.g. `types.KindAnthropicMessages`). Do NOT remove any imports; only `types.PublisherAnthropic` becomes unused at the reference site, but the `types` package import is still active.

- [ ] **Step 4: Update the single caller**

Open `cmd/modelserver/main.go`. Line 212 currently reads:

```go
	proxyHandler := proxy.NewHandler(executor, router, st, coll, catalog, logger, cfg.Server.MaxRequestBody, cfg.Images.MaxBodySize, httpLogger)
```

Change to (append `cfg.HttpLog`):

```go
	proxyHandler := proxy.NewHandler(executor, router, st, coll, catalog, logger, cfg.Server.MaxRequestBody, cfg.Images.MaxBodySize, httpLogger, cfg.HttpLog)
```

- [ ] **Step 5: Run the new test and full proxy + main build**

Run:
```bash
cd /root/coding/modelserver
go build ./...
go test ./internal/proxy/ -run TestHandlerHttpLogAllowsPublisher -v
```
Expected: build succeeds; the 12 sub-tests of `TestHandlerHttpLogAllowsPublisher` all PASS.

- [ ] **Step 6: Run full proxy + config test sweep for no regressions**

Run:
```bash
cd /root/coding/modelserver
go test ./internal/config/ ./internal/proxy/
```
Expected: PASS everywhere. `handler_denylist_test.go`, `resolve_model_middleware_test.go`, `trace_middleware_test.go`, `executor_finalize_test.go`, `chatcompletions_*_test.go`, `openai_*_test.go`, etc. must all remain green.

- [ ] **Step 7: Commit**

```bash
cd /root/coding/modelserver
git add internal/proxy/handler.go internal/proxy/handler_publisher_allowlist_test.go cmd/modelserver/main.go
git commit -m "feat(proxy): honor http_log.publishers allowlist for S3 upload gate

Replaces the hard-coded publisher==anthropic check at handler.go:513
with a config-driven allowlist. Handler precomputes the lookup set in
NewHandler (case-insensitive, whitespace-trimmed); the per-request
gate consults httpLogAllowsPublisher(m.Publisher). Default list
[anthropic,openai] means GPT/gpt-image-2/gpt-5.6 request+response
bodies begin uploading after fleet restart; ops that want to preserve
Claude-only behavior sets publishers: [anthropic] explicitly.

count_tokens override at handler.go:621 is unchanged. The
internal/httplog/* upload pipeline is unchanged — it is protocol-
agnostic and already handles OpenAI streams and multipart bodies.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: config.example.yml documentation + end-to-end verify + PR

**Files:**
- Modify: `config.example.yml` (~line 129-147)

**Interfaces:** none — documentation + verification only.

- [ ] **Step 1: Edit config.example.yml**

Open `config.example.yml`. Locate the `http_log:` block (starts at line 129, ends at line 147). Add a new documented option immediately after the existing `buffer_size:` line (line 147). Insert:

```yaml
  # Model publishers whose request+response bodies get uploaded to S3.
  # A request whose model's publisher is not in this list skips upload
  # even when enabled=true. Default is [anthropic, openai] — GPT bodies
  # (including gpt-5.6-sol/terra/luna and gpt-image-2) upload alongside
  # Claude bodies. Set to [anthropic] to preserve pre-2026-07 behavior.
  # No wildcard — add new publishers (gemini, deepseek, glm, …) by
  # name so future catalog migrations do not silently enable logging.
  publishers: ["anthropic", "openai"]
```

- [ ] **Step 2: Verify the yaml parses**

Run:
```bash
cd /root/coding/modelserver
go run ./cmd/modelserver --config config.example.yml --check 2>&1 | head -5
```

If `--check` is not a supported flag, alternatively use a small parse smoke:

```bash
cd /root/coding/modelserver
go vet ./... && echo "vet OK"
python3 -c "import yaml; yaml.safe_load(open('config.example.yml'))" && echo "yaml OK"
```

Expected: `yaml OK` (and, if run, `vet OK`). Either confirms the yaml document is well-formed and modelserver can parse it.

- [ ] **Step 3: Full test sweep**

Run:
```bash
cd /root/coding/modelserver
go build ./...
go test ./internal/config/... ./internal/proxy/... ./internal/store/...
```
Expected: build succeeds; every test passes. DB-touching tests SKIP without `TEST_DATABASE_URL` — that is expected and consistent with the ledger's prior features on this project.

- [ ] **Step 4: Manual verification (deferred to ops acceptance if no dev DB)**

If a dev database + Anthropic + OpenAI upstreams are available:

  1. Start modelserver against dev config with `http_log.enabled: true` and no explicit `publishers:` (uses the default `[anthropic, openai]`).
  2. Send a Claude request and a gpt-5.6-sol request through the proxy.
  3. `SELECT id, model, http_log_path FROM requests ORDER BY created_at DESC LIMIT 5;` — both rows should have non-empty `http_log_path`.
  4. Restart with `http_log.publishers: ["anthropic"]` explicitly set; send another gpt-5.6-sol request. New row's `http_log_path` should be empty; Claude row still populated.
  5. Restart with `http_log.enabled: false`; confirm no `http_log_path` on new rows regardless of publisher.

Otherwise (no dev DB): defer to ops acceptance after merge, consistent with recent PRs in the ledger (feat/cny-price-bump-7-6x, feat/gpt-5-6-family).

- [ ] **Step 5: Commit doc + push branch + open PR**

```bash
cd /root/coding/modelserver
git add config.example.yml
git commit -m "docs(config): document http_log.publishers allowlist

Adds the new field to config.example.yml with rationale for the
[anthropic, openai] default and the no-wildcard policy.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"

git push -u origin HEAD
gh pr create --base main --title "feat: HTTP-log publisher allowlist (default anthropic+openai)" --body "$(cat <<'EOF'
Replaces the hard-coded \`publisher==anthropic\` gate at
\`internal/proxy/handler.go:513-517\` with a config-driven allowlist
(\`http_log.publishers\`), default \`[anthropic, openai]\`.

## Effect

- After restart, GPT (\`gpt-5.6-sol\`, \`gpt-5.6-terra\`, \`gpt-5.6-luna\`,
  \`gpt-image-2\`, all other \`publisher='openai'\` rows) begin uploading
  request+response bodies to S3 alongside Claude traffic.
- Ops wanting Claude-only behavior sets \`http_log.publishers: [anthropic]\`.
- \`http_log.enabled: false\` remains the emergency kill switch.
- \`count_tokens\` handler override unchanged (still forces off).

## No S3 pipeline changes

\`internal/httplog/\` is protocol-agnostic — headers + body bytes. It
already handles OpenAI streams (\`TeeReadCloser\`) and multipart Images
bodies (\`MaxRequestBody\` truncation). Nothing there needed touching.

## Docs

Design: \`docs/superpowers/specs/2026-07-10-http-log-publisher-allowlist-design.md\`
Plan: \`docs/superpowers/plans/2026-07-10-http-log-publisher-allowlist.md\`

## Risks

- S3 storage line item roughly doubles for deploys with comparable
  Claude/GPT traffic. Ops with tight budgets should set
  \`publishers: [anthropic]\` before upgrading.
- Sensitive data in GPT bodies lands in the same bucket as Claude
  bodies. Same ACL, no new exposure surface; ops confirms bucket
  permissions are appropriate before rollout.
- Large \`gpt-image-2\` multipart uploads may exceed the default 50 MB
  \`max_request_body\` and truncate — same behavior Claude requests
  already see for oversized bodies.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR opens against `main`.

---

## Self-review

1. **Spec coverage** vs `docs/superpowers/specs/2026-07-10-http-log-publisher-allowlist-design.md`:
   - §2.1 Config change → Task 1 ✅
   - §2.2 Handler gate → Task 2 (replace at 513-517 + add `httpLogAllowsPublisher`) ✅
   - §2.3 Nil / empty Publishers → Task 2's test cases "nil slice denies anthropic", "empty slice denies anthropic" ✅
   - §2.3 case + whitespace normalization → Task 2 test cases "case-insensitive: …", "whitespace trimmed on both sides" ✅
   - §2.3 empty-string publisher rule → Task 2 test cases "default denies empty publisher" + "empty in list allows empty" ✅
   - §2.4 backward compatibility (default behavior, no wildcard) → Task 1 doc-comment + Task 3 yaml comment explicitly state this ✅
   - §3 File change list — Task 1 (config.go + config_test.go), Task 2 (handler.go + handler_publisher_allowlist_test.go + main.go), Task 3 (config.example.yml) ✅
   - §4 Alternatives — informational; no task needed ✅
   - §5 Wiring choice — Task 2 Step 3-4 implements option 1 (extend `NewHandler`) ✅
   - §6 Validation → Task 3 Steps 3-4 ✅
   - §7 Rollback — documented in PR body ✅
   - §8 Risks — documented in PR body ✅

2. **Placeholder scan:** no TBD/TODO; every code block is complete; no "handle edge cases" hand-waves. Every test asserts concrete values.

3. **Type consistency:**
   - `config.HttpLogConfig.Publishers []string` used identically in Task 1 (definition), Task 2 (`NewHandler` parameter — `blCfg config.HttpLogConfig`), and Task 3 (yaml key `publishers`). ✅
   - `Handler.httpLogAllowlist map[string]struct{}` — same field name in struct declaration and in the test file's `h.httpLogAllowlist = ...` assignment. ✅
   - `buildHttpLogAllowlist(cfg config.HttpLogConfig) map[string]struct{}` — same signature in Task 2's code addition and the test's usage. ✅
   - `httpLogAllowsPublisher(pub string) bool` — same signature in Task 2's method definition, test usage, and the replaced gate site. ✅
   - `strings.TrimSpace + strings.ToLower` normalization applied identically on both sides (`buildHttpLogAllowlist` write side + `httpLogAllowsPublisher` read side) — same order, same set of functions. ✅
