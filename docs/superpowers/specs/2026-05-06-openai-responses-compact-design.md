# OpenAI `/v1/responses/compact` Support — Design

**Date:** 2026-05-06
**Status:** Design — pending implementation plan
**Owner:** mryao
**Related code:** `internal/proxy/{router,handler,codex,openai_handler,openai_parser,provider_codex,provider_openai}.go`, `internal/types/request_kind.go`, `internal/store/migrations/`
**Reference upstream:** OpenAI Responses API `responses/compact` (as used by `/root/codex` Rust client — `codex-rs/codex-api/src/endpoint/compact.rs`, `codex-rs/core/src/client.rs::compact_conversation_history`)

## Goal

Add `POST /v1/responses/compact` to modelserver as a thin pass-through proxy for two upstream channels:

1. **Direct OpenAI** (`provider = openai`) — forwarded to `<openai-base>/v1/responses/compact` with the client's API key.
2. **ChatGPT-subscription Codex** (`provider = codex`) — forwarded to `<codex-base>/responses/compact` with OAuth access token + `ChatGPT-Account-ID` + codex fingerprint headers (`User-Agent`, `Originator`, `Version`, `session_id`).

Out of scope: `vertex-openai`, `bedrock-openai`, Anthropic, Gemini. Those backends do not expose this endpoint.

## Background

The codex CLI's compaction flow (`run_remote_compact_task_inner_impl`) calls a unary endpoint that takes the conversation transcript and returns a "compacted" transcript. The wire shape is a stripped Responses API request:

```jsonc
// Request body (matches codex-rs/codex-api/src/common.rs::CompactionInput)
{
  "model": "gpt-5.x",
  "input":  [ /* ResponseItem[] */ ],
  "instructions": "...",          // omitted when empty
  "tools":  [ /* tool defs */ ],
  "parallel_tool_calls": true,
  "reasoning": { "effort": "...", "summary": "..." },   // optional
  "text":      { "verbosity": "...", "format": {...} }  // optional
}
```

```jsonc
// Response body (codex deserialises only `output`; `usage` may exist on the wire)
{ "output": [ /* ResponseItem[] */ ] }
```

The endpoint is **unary** (no streaming) — codex's client is `client.compact(...)` returning a single `Vec<ResponseItem>`.

## Requirements

| # | Requirement |
|---|---|
| R1 | New HTTP route `POST /v1/responses/compact` mounted on the same `/v1` chi sub-router that hosts `/v1/responses`. |
| R2 | Auth, model resolution, subscription eligibility, rate-limit, and extra-usage-guard middleware all apply. |
| R3 | A new request kind `openai_responses_compact` exists and is enforced by the routes table CHECK constraint. |
| R4 | Routes whose `request_kinds` already includes `openai_responses` AND whose upstream group is purely `openai`+`codex` automatically gain `openai_responses_compact` via a forward-only DB migration. |
| R5 | Billing is identical to `/v1/responses`: `usage` is best-effort parsed from the response body and zero-defaults if absent. |
| R6 | No new SDK dependencies. No changes to `OpenAITransformer` / `CodexTransformer` / `openai_parser.go` / `openai_stream.go`. |
| R7 | Tests cover: route mounting, request-kind propagation, codex director URL, parser zero-default, and end-to-end billing record creation. |

## Non-requirements

- We do **not** validate the request body against `CompactionInput`. Schema errors are deferred to the upstream's 4xx, matching how `/v1/responses` is treated today.
- We do **not** add a streaming code path. Compact is unary upstream.
- We do **not** auto-extend routes whose upstream group contains `vertex-openai` or `bedrock-openai`. Operators who want compact on those routes must opt in manually.
- We do **not** introduce a separate per-kind rate-limit category. Compact shares the existing per-(project, model) bucket with `/v1/responses`.

## Architecture

### High-level

```
client ── POST /v1/responses/compact ─┐
                                       │
       Auth → Trace → ResolveModel → SubscriptionEligibility → RateLimit
                                       │
       → ExtraUsageGuard → HandleResponsesCompact → handleProxyRequest
                                       │            (kind = openai_responses_compact,
                                       │             ingress = openai)
                                       │
                                       ▼
       Router engine matches a route whose request_kinds ∋ openai_responses_compact
                                       │
                                       ▼
       Load balancer picks an upstream from the bound group
                                       │
                       ┌───────────────┴───────────────┐
                       ▼                               ▼
       OpenAITransformer.SetUpstream     CodexTransformer.SetUpstream
       URL: <base>/v1/responses/compact   URL: <base>/responses/compact
       Auth: client API key               Auth: OAuth access token + codex headers
                       │                               │
                       └───────────────┬───────────────┘
                                       ▼
                       Upstream returns JSON {output, usage?}
                                       │
                                       ▼
       ParseResponse → Collector records (model, id, input_tokens,
                                          output_tokens, cache_read_tokens=0)
       Body streamed back to client byte-for-byte
```

### Components changed

| File | Change | Lines |
|---|---|---|
| `internal/types/request_kind.go` | Add `KindOpenAIResponsesCompact = "openai_responses_compact"` constant; append to `AllRequestKinds`. | +2 |
| `internal/proxy/handler.go` | Add 3-line method `HandleResponsesCompact` that delegates to `handleProxyRequest(w, r, IngressOpenAI, types.KindOpenAIResponsesCompact)`. | +6 |
| `internal/proxy/router.go` | Inside `r.Route("/v1", …)`, add `r.Post("/responses/compact", handler.HandleResponsesCompact)`. (chi v5 routes by full-path segment match, so `/responses` and `/responses/compact` coexist without ordering concerns.) | +1 |
| `internal/store/migrations/036_responses_compact_request_kind.sql` | New migration: drop+re-add the `routes_request_kinds_valid` CHECK constraint with the new kind included; UPDATE eligible routes to append the kind. | +~50 |

No file deletions; no signature changes to existing public functions.

### Components reused unchanged

- `OpenAITransformer` (`provider_openai.go`) — `SetUpstream` joins `req.URL.Path` (`/v1/responses/compact`) onto the upstream base; correct for `https://api.openai.com` (empty path) → `https://api.openai.com/v1/responses/compact`.
- `CodexTransformer` (`provider_codex.go` → `directorSetCodexUpstream` in `codex.go`) — already strips `/v1` and `path.Join`s onto codex base (`https://chatgpt.com/backend-api/codex`), producing `https://chatgpt.com/backend-api/codex/responses/compact`.
- `ParseOpenAINonStreamingResponse` (`openai_parser.go`) — `openaiResponseEnvelope` deserialises `{id, model, usage}` and zero-defaults `usage` if the field is absent. Unknown fields like `output` are ignored.
- `Executor`, `Collector`, `Router`, `RouterEngine`, all middleware, all auth/token managers — no changes.

## Data flow detail

### Request kind propagation

`handleProxyRequest` already sets `RequestContext.RequestKind = kind`, which is consumed by `Router.SelectRoute` via `RouterEngine` (`router_engine.go:251,265` use `slices.Contains(route.RequestKinds, kind)`). Adding the new kind requires no router changes — only that the kind appears in the route's `request_kinds` array.

### Body handling

`handleProxyRequest` reads the body once into `bodyBytes` (subject to `maxBodySize`), unmarshals into `{Stream, Model}`, runs `ResolveModelMiddleware`-style canonicalisation, and rewrites the body via `sjson.SetBytes` if the model name was non-canonical. The compact body has a `model` field, so this works as-is. `Stream` will deserialise to `false` (compact bodies omit it), so the response is treated as non-streaming and `ParseResponse` is invoked on the upstream body — which is exactly what we need.

### Path and auth (per provider)

| Provider | Incoming | Director step | Outgoing URL | Auth |
|---|---|---|---|---|
| `openai` | `POST /v1/responses/compact` | `directorSetOpenAIUpstream` joins `target.Path` (empty) with `/v1/responses/compact` | `https://api.openai.com/v1/responses/compact` | `Authorization: Bearer <client-supplied-or-stored API key>` |
| `codex` | `POST /v1/responses/compact` | `directorSetCodexUpstream` strips `/v1` → `/responses/compact`, joins with codex base path | `https://chatgpt.com/backend-api/codex/responses/compact` | `Authorization: Bearer <oauth-access-token>`, `ChatGPT-Account-ID`, `User-Agent: codex_cli_rs/0.124.0 …`, `Originator: codex_cli_rs`, `Version: 0.124.0`, `session_id: <uuid>` |

### Billing

`ParseOpenAINonStreamingResponse` returns `(model, respID, usage, err)`. `OpenAITransformer.ParseResponse` and `CodexTransformer.ParseResponse` both compute:

```
input  = usage.InputTokens - usage.InputTokensDetails.CachedTokens   (clamped ≥ 0)
output = usage.OutputTokens
cached = usage.InputTokensDetails.CachedTokens
```

If the upstream omits `usage`, all three are 0; the request is recorded with 0 tokens and 0 cost. This is the same behaviour as a malformed `/v1/responses` body today and is acceptable per user direction ("和 /v1/responses 处理一致").

## DB migration (036)

```sql
-- 036_responses_compact_request_kind.sql
-- Adds openai_responses_compact to the request_kinds CHECK constraint, then
-- backfills the kind onto every route that already serves openai_responses
-- and whose upstream group contains only openai/codex providers.

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
            AND u.provider NOT IN ('openai','codex')
      )
)
UPDATE routes
SET request_kinds = array_append(request_kinds, 'openai_responses_compact')
WHERE id IN (SELECT id FROM eligible);

COMMIT;
```

Properties:
- **Idempotent** — `NOT 'openai_responses_compact' = ANY(...)` skips already-extended routes; safe to re-run by accident.
- **Provider-bounded** — vertex-openai / bedrock-openai routes are not auto-extended, since neither backend exposes a real compact endpoint.
- **Reversible** — operators can `UPDATE routes SET request_kinds = array_remove(request_kinds, 'openai_responses_compact')` via the admin route API to disable compact on a per-route basis.
- **Forward-only** — no down migration. Removing the kind from the CHECK constraint while routes still reference it would fail; if a future migration needs to roll this back, it must first remove the kind from every route.

## Error handling

All errors flow through existing paths:

| Failure | Surface |
|---|---|
| Body too large | `413` from `http.MaxBytesReader` (existing). |
| Unknown / disabled model | OpenAI-shaped error envelope from `writeUnsupportedModelError(IngressOpenAI, ...)` (existing). |
| Model not in `apiKey.AllowedModels` | `403 model not allowed for this API key` (existing). |
| No matching route for kind | Router engine returns no route → `503` with no-route error (existing). |
| Upstream 4xx/5xx | Body and status are streamed back unchanged (existing). |
| Upstream returns malformed JSON | `ParseResponse` error is logged; usage recorded as 0; the response body still reaches the client (existing). |

No new error envelopes, no new HTTP status codes.

## Testing strategy

| Test | Location | What it verifies |
|---|---|---|
| Route registration | `internal/proxy/router_test.go` (extend) | `POST /v1/responses/compact` resolves to `HandleResponsesCompact`; `RequestContext.RequestKind == openai_responses_compact`. |
| Router engine kind matching | `internal/proxy/router_engine_test.go` (extend) | A route with `request_kinds = ['openai_responses']` does NOT match a compact request; one with `['openai_responses_compact']` does. |
| Codex director path | `internal/proxy/codex_test.go` (extend) | Incoming `/v1/responses/compact` → outgoing path ends with `/responses/compact` (single segment, no double `/responses`, no `/v1` prefix). |
| OpenAI parser tolerates missing usage | `internal/proxy/openai_parser_test.go` (extend) | `ParseOpenAINonStreamingResponse([]byte("{\"output\":[]}"))` returns zero-valued `ResponseUsage` and `nil` error. |
| End-to-end | New `internal/proxy/responses_compact_test.go` | With a stub upstream returning `{"output":[],"usage":{"input_tokens":42,"output_tokens":8}}`, `HandleResponsesCompact` (a) inserts a `requests` row with `RequestKind = openai_responses_compact`, (b) records 42 input + 8 output tokens, (c) streams the upstream body verbatim to the client. |
| Migration | `internal/store/migrations_test.go` (or equivalent existing pattern) | After running 036 against a fixture DB with one openai-only route + one mixed openai/vertex-openai route, only the openai-only route's `request_kinds` contains `openai_responses_compact`. |

## Risks and tradeoffs

1. **Upstream response shape may diverge from `/v1/responses`.** OpenAI documents compact's request body but the response shape isn't on a stable public contract. Codex's client only reads `output`. If upstream stops including `usage`, billing silently records 0 tokens for compact — visible as an "always-zero" pattern in metrics rather than as a hard failure. Mitigation: a Grafana check on `request_kind = openai_responses_compact AND input_tokens = 0` ratio over time.

2. **Codex fingerprint headers are pinned.** `CodexVersion = "0.124.0"` and `CodexUserAgent` must be bumped in lockstep with the codex CLI. This is already the case for `/v1/responses` and is a known maintenance task — compact inherits the same risk, no new exposure.

3. **No body schema validation.** A misbehaving client can send anything; we'll forward and let upstream 4xx. This matches `/v1/responses`. If schema drift causes a flood of upstream 4xx, the existing per-project rate-limit and extra-usage-guard catch it.

4. **Auto-migration mutates production routes.** The 036 migration changes operator-configured `request_kinds` arrays. Mitigation: provider-bounded eligibility (only purely openai/codex groups), idempotent guard, and a NOTICE block could be added if operators want a preview — but per design choice the user has accepted automatic extension.

## Open questions

None remaining as of 2026-05-06.

## Implementation outline (for the writing-plans handoff)

In rough dependency order:

1. Add `KindOpenAIResponsesCompact` constant + slice entry; update `IsValidRequestKind` coverage if any test enumerates the list.
2. Add `HandleResponsesCompact` and the `/v1/responses/compact` route.
3. Write migration 036 and run it against a local PG instance to verify the backfill predicate.
4. Add the five tests above.
5. Manual smoke test against a stub upstream (or against api.openai.com if a key is available) for both `openai` and `codex` providers.
