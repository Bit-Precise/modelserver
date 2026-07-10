# HTTP-log S3 upload: publisher allowlist (default anthropic + openai)

Status: **draft — awaiting review**
Author: (assistant, drafted with user 2026-07-10)
Motivation: currently only `publisher = 'anthropic'` traffic (Claude family)
has request/response bodies uploaded to S3. Operators want GPT
(`publisher = 'openai'`) — including `gpt-5.6-sol` / `terra` / `luna` and
`gpt-image-2` — logged too, for audit and debugging (e.g. the recent
`Namespace 'collaboration' is reserved for encrypted tool use by this
model` failure).

## 1. Goal

Replace the hard-coded `publisher == "anthropic"` gate at
`internal/proxy/handler.go:513-517` with a configurable allowlist so
operators can enable HTTP-log uploads for any set of publishers without a
code change. Default the allowlist to `["anthropic", "openai"]` so a
fleet upgrade turns GPT logging on automatically, matching the requested
outcome.

Out of scope:

- Per-project / per-model gates (spec §4 `Alternatives`, option C). Not
  requested; adds admin-UI + schema churn out of proportion to today's
  need. B remains extensible in that direction if the ask arrives.
- Any change to the S3 upload pipeline itself (`internal/httplog/*.go`,
  ~500 lines). It's protocol-agnostic — headers + body bytes — and
  already handles OpenAI streams and OpenAI Images multipart bodies via
  the existing `TeeReadCloser` / `MaxRequestBody` / `MaxResponseBody`
  paths used by Claude traffic.
- The `count_tokens` handler's explicit `HttpLogEnabled: false` override
  (`handler.go:621`). Stays — it's a high-frequency editor probe and
  never wants body logs regardless of publisher.

## 2. Design

### 2.1 Config change

`HttpLogConfig` (`internal/config/config.go:200-212`) gains one field:

```go
type HttpLogConfig struct {
    Enabled         bool
    Bucket          string
    Region          string
    Endpoint        string
    AccessKeyID     string
    SecretAccessKey string
    PathStyle       bool
    MaxRequestBody  int64
    MaxResponseBody int64
    BufferSize      int
    Publishers      []string `yaml:"publishers" mapstructure:"publishers"`
}
```

`setDefaults`:

```go
v.SetDefault("http_log.publishers", []string{"anthropic", "openai"})
```

`config.example.yml` gets a documented block explaining the semantics
and the default.

### 2.2 Handler gate

`handler.go:513-517` becomes:

```go
if h.httpLogger != nil {
    if m := ModelFromContext(r.Context()); m != nil && h.httpLogAllowsPublisher(m.Publisher) {
        reqCtx.HttpLogEnabled = true
    }
}
```

`httpLogAllowsPublisher` is a small method on `Handler` that reads a
precomputed `map[string]struct{}` built once in `NewHandler` from the
`Publishers` slice. Empty-string publishers (`Publisher == ""`, from
early catalog rows like `031_seed_gpt_image_2.sql`) are only allowed if
`""` itself appears in the config — a conservative choice that leaves
today's unlabelled rows non-logging by default. The `count_tokens`
handler override remains unaffected because it sets `HttpLogEnabled:
false` after this gate.

### 2.3 Nil / empty Publishers

- Nil (`Publishers == nil` because `HttpLogConfig` was constructed
  without going through viper — e.g. tests): treated as "no publisher
  allowed". Gate never fires. Explicit tests will construct the config
  the same way viper does (via defaults).
- Empty slice (`Publishers = []string{}` explicitly): treated as "no
  publisher allowed". Operator wants HTTP logging off despite `enabled:
  true` — a legitimate configuration for the S3 client still being
  provisioned for the retriever API but no writes desired. Cheap to
  support, no extra code.
- Duplicates / whitespace / case: allowlist is compared verbatim after
  a single `strings.TrimSpace + strings.ToLower` normalization on both
  sides. `Publisher` in the DB is already lowercase, so this only
  guards against operator typos in yaml (`"OpenAI"` still works).

### 2.4 Backward compatibility

Existing deploys that never had a `http_log.publishers` line in
`config.yml` will pick up the new default `["anthropic", "openai"]` on
their next restart. This is the intended behavior — the goal is to make
GPT logging happen without operator action. Ops that need to preserve
the old anthropic-only behavior explicitly set:

```yaml
http_log:
  publishers: ["anthropic"]
```

Ops that want to opt into every future publisher we add (gemini,
deepseek, glm, …) set the slice to include those publishers by name
whenever they enable them; no wildcard — a wildcard would silently
enable logging on any new publisher landed by a catalog migration,
which is exactly the surprise we want operators to opt into
consciously.

Storage impact: for a deploy where GPT already sees comparable request
volume to Claude, S3 usage roughly doubles. Existing
`http_log.max_request_body` and `max_response_body` (50 MB each by
default) still apply and still truncate. `http_log.enabled: false`
remains the emergency kill switch — the `httpLogger` isn't even
constructed in that case.

## 3. Change list

| File | Action | Change |
|---|---|---|
| `internal/config/config.go` | Modify | Add `Publishers []string` to `HttpLogConfig`; register default in `setDefaults` |
| `internal/config/config_test.go` (or new) | Modify/Create | Verify default is `["anthropic","openai"]`, explicit yaml override wins, empty slice is respected |
| `internal/proxy/handler.go` | Modify | Replace hard-coded gate at 513-517 with `httpLogAllowsPublisher`; precompute allowlist set in `NewHandler` |
| `internal/proxy/handler.go` + `cmd/modelserver` | Modify | Extend `NewHandler` signature to accept `HttpLogConfig` (same struct already handed to `NewExecutor`) — see §5 |
| `internal/proxy/handler_test.go` (or `handler_denylist_test.go` neighbourhood) | Modify | Cover 4 cases: openai model + default → logged; openai model + `[anthropic]` config → not logged; unknown-publisher model + default → not logged; count_tokens with openai model → still off |
| `config.example.yml` | Modify | Add commented `publishers:` block under `http_log:` |
| `docs/superpowers/specs/…` | New (this file) | Design doc |
| `docs/superpowers/plans/…` | New (from writing-plans) | Implementation plan |

**Not changed**: `internal/httplog/*` (upload pipeline), any provider
transformer, catalog rows, migrations, database schema, dashboard.

## 4. Alternatives considered

- **A. Hard-code the gate to also match `openai`.** 2-line change but
  no opt-out and no forward compatibility. Next time we want gemini or
  deepseek logged, another code change. Rejected — cheap to do B right
  now.
- **C. Per-project / per-model metadata gate.** Most flexible; a
  `models.metadata.http_log_enabled` or `projects.metadata.http_log`
  field could override the publisher default. Requires admin-UI work,
  metadata schema documentation, more test surface. Not requested.
  Compatible with B if it lands later (B provides the default; C
  provides the override).
- **Metadata-based opt-out on B**  
  (`metadata.http_log_disabled = true`). Considered for excluding
  gpt-image-2's multi-MB multipart bodies. Not adopted this iteration —
  simpler to leave the 50 MB truncation defaults do their job and
  revisit if S3 line-item is uncomfortable.

## 5. Wiring choice — `Publishers` reaches the handler how?

`HttpLogConfig` is already constructed in `cmd/modelserver` and passed
to `NewExecutor(..., blCfg HttpLogConfig, ...)`. The handler currently
receives only the `*httplog.Logger` (`NewHandler(..., bl *httplog.Logger)`).
Two options:

1. **Extend `NewHandler`** to take `blCfg HttpLogConfig` too (same
   config struct the executor already gets). Symmetric; the allowlist
   check lives with the handler that owns the request-lifecycle gate.
2. **Move the allowlist onto `httplog.Logger`** as a
   `Logger.PublisherAllowed(pub string) bool` method. Fewer wires, but
   couples the logger (a pure S3 uploader) to modelserver-specific
   policy semantics.

**Recommend option 1**. The gate is a policy decision at the request
boundary, not an S3-uploader concern. Executor already carries the
config, adding the same to handler keeps the two aligned. Constructor
signature change is contained to `cmd/modelserver` (single caller).

## 6. Validation

1. `go test ./internal/config/... ./internal/proxy/...`
2. Manual: start modelserver with the default `http_log` config;
   send one Claude request and one gpt-5.6-sol request; both should
   produce `http_log_path` on their `requests` rows. Send another
   gpt-5.6-sol request after adding `http_log.publishers: ["anthropic"]`
   to config and restarting; the second GPT request should NOT have an
   `http_log_path`.
3. Manual: with `http_log.enabled: false`, verify no publisher gets
   logs regardless of the allowlist (the `h.httpLogger == nil` short-
   circuit still wins).

## 7. Rollback

Config-only: set `http_log.publishers: ["anthropic"]` in `config.yml`
and restart. No DB migration, no data change, no code deploy needed.

## 8. Risks

- **R1 — S3 line item doubles on upgrade.** Default flip enables GPT
  logging without operator opt-in. Documented in the release notes and
  in `config.example.yml`. Ops with tight S3 budgets can set
  `publishers: ["anthropic"]` before upgrading.
- **R2 — Sensitive data in GPT bodies.** Same S3 bucket, same access
  controls as Claude bodies today. No new exposure surface — anyone
  who could read Claude logs can now read GPT logs. Called out in the
  release notes so ops confirms the bucket ACL is appropriate.
- **R3 — Image `multipart/form-data` bodies contain uploaded image
  bytes.** `gpt-image-2` edit requests can exceed the default 50 MB
  `max_request_body` and get truncated. Same truncation behavior
  Claude requests already experience for oversized bodies; nothing
  new. If auditability of full image uploads matters, ops raises
  `max_request_body`. Documented.
