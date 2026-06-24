# Identity Endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close [issue #62](https://github.com/Bit-Precise/modelserver/issues/62) — give an OAuth-authorized third-party app a way to read back the `project_id` / `user_id` bound to its access token — by adding one thin identity-echo endpoint at `GET /api/oauth/profile`. The response shape is modelserver-native (single `project` object reflecting modelserver's 1:1 key↔project auth model) rather than impersonating Anthropic's multi-tenant `organization` model.

**Architecture:** One new file `internal/proxy/me_handler.go` exposes a `buildIdentity()` resolver plus `HandleOAuthProfile`. Identity is derived from the values `AuthMiddleware` already writes to the request context (apiKey, project, subscription) plus a best-effort `GetUserByID` and `GetExtraUsageSettings`. No changes to `AuthMiddleware` other than introducing a single shared constant.

**Tech Stack:** Go (Go 1.x, chi router, pgx), JSON via stdlib `encoding/json`, tests via stdlib `testing` + table-driven cases. Uses the codebase's existing `slog` + `internal/proxy/router.go` wiring.

## Background

OAuth access tokens issued by modelserver's Hydra consent flow are opaque to the client (Hydra default). A third-party "Authorized App" that holds a valid access token has no public way to read back which `project_id` it's been bound to, forcing every integrator to either re-prompt the user for a project or require operators to flip Hydra to JWT tokens (which changes revocation semantics globally).

**Why one endpoint, not three:** an earlier draft of this plan proposed mimicking three upstream surfaces (OpenAI `/v1/me`, Anthropic `/api/oauth/profile`, OpenAI Codex `/v1/user-auth-credential/whoami`) so the matching client SDKs would work unchanged. Two of those were dropped:

- `/v1/me` was a custom shape with no real consumer — OpenAI doesn't document `/v1/me` and no SDK consumes it; trying to mirror its `orgs[]` list shape was semantically wrong for modelserver (one key is bound to one project, not a list of orgs).
- `/v1/user-auth-credential/whoami` was Codex CLI's PAT-info endpoint — modelserver isn't an OpenAI/Codex shim, no concrete near-term caller.

The remaining `/api/oauth/profile` survives because the path borrows from Anthropic's Claude Code OAuth flow (third-party tooling that grew up around Anthropic's API will look there) but the response shape is **deliberately not Anthropic-compatible** — Claude Code itself reads `response.organization.organization_type` and will get `undefined` from this response (we return a `project` block instead). That trade-off is explicit: modelserver returns its native data model rather than impersonate Anthropic's multi-tenant org layer.

## Global Constraints

- Issue reference: [Bit-Precise/modelserver#62](https://github.com/Bit-Precise/modelserver/issues/62).
- No database schema migration. All data flows through values `AuthMiddleware` already writes to the request context plus opportunistic `GetUserByID` / `GetProjectMember` / `GetExtraUsageSettings`.
- No changes to `AuthMiddleware` logic — only one new struct field-less constant (`syntheticOAuthAPIKeyName`) added.
- Endpoint goes through the full `wire()` chain in MountRoutes (Auth + Trace + ResolveModel + SubscriptionEligibility + RateLimit + ExtraUsageGuard). The model-aware middlewares are no-ops for this GET (no model in body), but Trace/RateLimit are genuinely useful: without them the endpoint becomes an un-throttled, un-audited token-validity oracle.
- OAuth-only: API-key callers receive HTTP 401 with body `"oauth token required"`. modelserver-issued API keys are scoped tighter than OAuth tokens and don't carry any field this endpoint surfaces that isn't already in the API key's own management view.
- Response must never echo the credential. Account-level fields come from the bound user row; project-level fields from the bound subscription. No `oauth_client_id`, no `scopes`, no `api_key_id` — the endpoint answers "who am I?" not "what's my session?".
- Tests live in `internal/proxy/me_handler_test.go` with a fake `identityStore` — no DB required (`TEST_DATABASE_URL` unset).
- Commits follow the existing convention (lowercase type prefix, no period) and end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>` because we are on a non-main branch.

---

## File Structure

**Created:**
- `internal/proxy/me_handler.go` — `identityStore` interface, `buildIdentity()` resolver, `mapPlanToProjectType()` mapper, `HandleOAuthProfile` + testable `writeOAuthProfile` core, response structs.
- `internal/proxy/me_handler_test.go` — table-driven coverage of `buildIdentity`, plan mapping, and `HandleOAuthProfile` happy-path + degraded variants + auth rejection + cache headers.

**Modified:**
- `internal/proxy/auth_middleware.go` — add `syntheticOAuthAPIKeyName` constant and `deleteIntrospectCache` test helper.
- `internal/proxy/router.go` — register `r.Route("/api/oauth", …)` block alongside the existing `/v1` block.

**Reviewed but not modified:**
- `internal/admin/hydra_client.go` — confirms the existing OAuth introspection path the proxy already consumes.
- `internal/types/policy.go` — source of `Subscription.PlanName` and `IsActive()`.
- `internal/types/project.go`, `internal/types/user.go`, `internal/types/apikey.go`, `internal/types/extra_usage.go` — source of surfaced fields.

---

## Response Shape

### Endpoint

```
GET /api/oauth/profile
Authorization: Bearer <oauth_access_token>
```

### Account block

| Field | Type | Source | Notes |
|---|---|---|---|
| `uuid` | string | `apiKey.CreatedBy` | always populated |
| `email` | string | `user.Email` | empty when user row missing |
| `display_name` | string | `user.Nickname` | `omitempty` |
| `created_at` | string (RFC3339) | `user.CreatedAt` | `omitempty` |

### Project block

| Field | Type | Source | Behavior when absent |
|---|---|---|---|
| `uuid` | string | `project.ID` | always populated |
| `project_type` | string | `mapPlanToProjectType(subscription.PlanName)` | `omitempty` when no active subscription |
| `rate_limit_tier` | `*string` | `subscription.PlanName` | JSON `null` |
| `seat_tier` | `*string` | `subscription.PlanName` | JSON `null` |
| `has_extra_usage_enabled` | bool | `GetExtraUsageSettings(project.ID).Enabled` | `false` on missing row / DB error |
| `billing_type` | `*string` | `"stripe"` when `subscription.Currency != ""` | JSON `null` |
| `cc_onboarding_flags` | object | always `{}` | always `{}` (no onboarding concept in modelserver) |
| `subscription_created_at` | string (RFC3339) | `subscription.CreatedAt` | `omitempty` |

### Plan → project_type mapping

modelserver's plan slugs (per migrations 040/049) are `free`, `pro`, and `max_2x`..`max_240x`. Only `pro` and `max_*` are paid tiers, so those are the only values the mapping produces:

```go
func mapPlanToProjectType(planName string) string {
    switch {
    case planName == "pro":
        return "pro"
    case strings.HasPrefix(planName, "max_"):
        return "max"
    default:
        return ""  // omitempty → field omitted entirely
    }
}
```

### Example: active `max_5x` subscription, extra-usage enabled

```json
{
  "account": {
    "uuid": "user_alice_01HXYZ",
    "email": "alice@example.com",
    "display_name": "Alice",
    "created_at": "2024-03-15T10:00:00Z"
  },
  "project": {
    "uuid": "proj_01ABCD",
    "project_type": "max",
    "rate_limit_tier": "max_5x",
    "seat_tier": "max_5x",
    "has_extra_usage_enabled": true,
    "billing_type": "stripe",
    "cc_onboarding_flags": {},
    "subscription_created_at": "2025-01-01T00:00:00Z"
  }
}
```

### Example: no active subscription (free tier)

```json
{
  "account": {
    "uuid": "user_bob_01HZZZ",
    "email": "bob@example.com",
    "display_name": "Bob",
    "created_at": "2025-05-20T08:30:00Z"
  },
  "project": {
    "uuid": "proj_01XYZA",
    "rate_limit_tier": null,
    "seat_tier": null,
    "has_extra_usage_enabled": false,
    "billing_type": null,
    "cc_onboarding_flags": {}
  }
}
```

(`project_type` and `subscription_created_at` omitted via `omitempty`.)

---

## Auth path discrimination

`AuthMiddleware` writes the same `*types.APIKey` context value on both auth paths. On the OAuth path the synthetic key has `ID=""` and `Name=syntheticOAuthAPIKeyName`. The constant lives in `auth_middleware.go` so producer and consumer can never drift:

```go
const syntheticOAuthAPIKeyName = "hydra-token"  // declared in auth_middleware.go
```

`buildIdentity()` discriminates on the same constant:

```go
switch {
case apiKey.ID == "" && apiKey.Name == syntheticOAuthAPIKeyName:
    id.authKind = "oauth_token"
default:
    id.authKind = "api_key"
}
```

The `Name` match alone is sufficient (a real API key always has a non-empty ID); the `ID == ""` belt is defense against a future code path that fabricates an APIKey literal whose `Name` happens to be `"hydra-token"`.

---

## Steps

### 1. Add `syntheticOAuthAPIKeyName` constant + `deleteIntrospectCache` helper

- [ ] In `internal/proxy/auth_middleware.go`, declare `const syntheticOAuthAPIKeyName = "hydra-token"` and reference it from `handleTokenIntrospectionAuth`.
- [ ] Add a `deleteIntrospectCache(tokenHash string)` package-private helper alongside `getIntrospectCache` / `setIntrospectCache` for test cleanup.

### 2. Implement `me_handler.go`

- [ ] Create `internal/proxy/me_handler.go` with:
  - `identityStore` interface (`GetUserByID`, `GetProjectMember`, `GetExtraUsageSettings`)
  - `identity` struct + `buildIdentity()` resolver
  - `mapPlanToProjectType()`
  - `oauthProfileAccount` / `oauthProfileProject` / `oauthProfileResponse` structs
  - `HandleOAuthProfile` + testable `writeOAuthProfile` core
- [ ] `writeOAuthProfile`: short-circuit with 500 if `buildIdentity` returns nil, 401 with `"oauth token required"` if `authKind != "oauth_token"`. Always set `Content-Type: application/json`, `Cache-Control: no-store, private`, `Vary: Authorization`.

### 3. Mount the route

- [ ] In `internal/proxy/router.go` `MountRoutes`, add a sibling `r.Route("/api/oauth", func(r chi.Router) { wire(r); r.Get("/profile", handler.HandleOAuthProfile) })` so the endpoint shares the wire() chain with `/v1`.

### 4. Tests

- [ ] Create `internal/proxy/me_handler_test.go` with a `fakeIdentityStore` satisfying the interface. Tests:
  - `buildIdentity`: API-key path, OAuth path, missing user row (DB error degrades silently), nil context.
  - `mapPlanToProjectType`: full table (`pro`, `max_*`, free/bare/custom → "").
  - `HandleOAuthProfile`: max subscription happy path, no subscription (null fields, omitted `project_type`), expired subscription (no leaked plan fields), missing user row degrades, EU DB error degrades, API-key rejected with 401, missing context 500, cache headers present.

### 5. Verification

- [ ] `go vet ./...` clean.
- [ ] `go test ./internal/proxy/...` passes.
- [ ] `go test ./...` passes (no regressions elsewhere).

---

## Out of scope

- `/v1/me` and `/v1/user-auth-credential/whoami` — earlier-draft endpoints that didn't survive scope review.
- Real `billing_type` differentiation (Stripe vs manual vs enterprise). v1 emits `"stripe"` for any subscription with a non-empty currency; precise mapping would need a join into `orders` and isn't required by issue #62.
- Real `cc_onboarding_flags` payload. modelserver has no onboarding concept; the field is `{}` always so a client that reads `?? {}` defensively works.
- A `?include=usage` query expansion. Costs another DB hit, no upstream precedent. Keep idempotent.
- Listing the OAuth scopes granted at consent. Scopes are an authorization concept; the endpoint answers identity, not session detail. Add when a concrete caller needs it.
