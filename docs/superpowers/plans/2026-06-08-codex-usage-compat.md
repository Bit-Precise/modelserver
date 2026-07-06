# Codex Usage Compatibility Endpoints Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Codex-CLI-compatible usage endpoints (`GET /api/codex/usage` and `GET /wham/usage`) plus a "current account" endpoint (`GET /api/codex/account`) to the modelserver proxy, so that a Codex CLI client (or any OAuth-access-token holder) pointed at modelserver as its upstream can read the current project's plan, rate-limit windows, and credit balance in the exact JSON shape Codex expects.

**Architecture:** Two thin handlers (one for `/usage`, one for `/account`) live next to the existing `HandleUsage` in `internal/proxy/`. They reuse the existing auth/policy/subscription/extra-usage data already loaded by `AuthMiddleware`, and serialize it into the schema defined by Codex's `RateLimitStatusPayload` (snake_case fields, snake_case enum variants). All three new endpoints (`/api/codex/usage`, `/wham/usage`, `/api/codex/account`) are mounted on the same chi router with the same `wire` middleware stack as `/v1/usage`. Both API-key and OAuth-introspection auth paths work transparently because both populate the same context keys.

**Tech Stack:** Go, `github.com/go-chi/chi/v5`, standard `net/http`, `encoding/json`, project's existing `internal/store`, `internal/types`, `internal/proxy/auth_middleware.go` context helpers.

---

## Background — Codex Wire Format

Codex CLI reads `GET {base_url}/api/codex/usage` (or `/wham/usage` if base_url contains `/backend-api`). The response schema (verbatim from `/root/codex/codex-rs/codex-backend-openapi-models/src/models/`):

```json
{
  "plan_type": "pro",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 45,
      "limit_window_seconds": 3600,
      "reset_after_seconds": 1980,
      "reset_at": 1717891234
    },
    "secondary_window": null
  },
  "credits": {
    "has_credits": true,
    "unlimited": false,
    "balance": "1000.50"
  },
  "additional_rate_limits": null,
  "rate_limit_reached_type": null
}
```

Critical wire conventions:

1. **All JSON keys are snake_case.**
2. **`plan_type`** is one of: `guest | free | go | plus | pro | prolite | free_workspace | team | self_serve_business_usage_based | business | enterprise_cbp_usage_based | education | quorum | k12 | enterprise | edu | unknown`. Unknown values are tolerated client-side via `#[serde(other)]`.
3. **`rate_limit_reached_type`** is an *object* `{"type": "<kind>"}` (NOT a bare string). Valid kinds: `rate_limit_reached`, `workspace_owner_credits_depleted`, `workspace_member_credits_depleted`, `workspace_owner_usage_limit_reached`, `workspace_member_usage_limit_reached`, `unknown`.
4. **`balance`** is a string (representing decimal), not a number.
5. **Window fields** (`used_percent`, `limit_window_seconds`, `reset_after_seconds`, `reset_at`) are **integers** (i32). `reset_at` is a Unix epoch second.
6. **`primary_window`/`secondary_window`/`balance`/etc.** use `serde_with::rust::double_option` — they can be absent, `null`, or set. We always emit either an object or omit the key entirely (we never emit explicit `null`); the field uses `omitempty`.

The Codex client also has special handling: `RateLimitSnapshot::limit_id == "codex"` is preferred (`backend-client/src/client.rs:286-290`). We do not need to populate `limit_id` because it's a client-side construct after parsing; the wire payload has no `limit_id` field.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/proxy/codex_usage_handler.go` (CREATE) | New `HandleCodexUsage` handler + Codex schema struct definitions (`codexUsageResponse`, `codexRateLimitDetails`, `codexWindowSnapshot`, `codexCreditDetails`, `codexRateLimitReachedType`); helper to map subscription `PlanName` → Codex `plan_type` string; helper to pick the "primary" CreditRule and convert it to a window snapshot. |
| `internal/proxy/codex_account_handler.go` (CREATE) | New `HandleCodexAccount` handler returning `{user_id, email, account_id, plan_type, project_id, project_name, auth_method}` to identify the caller. |
| `internal/proxy/codex_usage_handler_test.go` (CREATE) | Unit tests for `HandleCodexUsage` covering: subscription + policy populated, no subscription (free/guest plan), extra-usage enabled vs disabled, rate-limit-reached, credits-depleted, OAuth-introspected synthetic API key. |
| `internal/proxy/codex_account_handler_test.go` (CREATE) | Unit tests for `HandleCodexAccount` covering API key path vs OAuth introspection path. |
| `internal/proxy/router.go` (MODIFY) | Register the three new routes under `/api/codex/usage`, `/wham/usage`, `/api/codex/account` using the existing `wire` middleware stack. |
| `internal/proxy/router_test.go` (MODIFY) | Add an integration-style test asserting the routes are mounted and AuthMiddleware applies (401 without auth). |
| `docs/proxy-endpoints.md` (CREATE OR APPEND if exists) | Briefly document the new Codex-compat endpoints, their schema, and how to point Codex CLI at modelserver. |

---

## Task 1: Create plan_type mapping helper + tests

**Files:**
- Create: `internal/proxy/codex_usage_handler.go`
- Create: `internal/proxy/codex_usage_handler_test.go`

The first piece we need is the mapping from modelserver `Subscription.PlanName` (e.g. `pro`, `max_5x`, `max_20x`, ...) to a Codex `plan_type` enum string. Modelserver's plan names that don't exist in Codex's enum (the `max_*` tiers) should map to `enterprise` so the Codex client treats them as a paid, non-rate-limited tier. Absent/inactive subscription maps to `free`.

- [ ] **Step 1: Write the failing test**

Create `internal/proxy/codex_usage_handler_test.go`:

```go
package proxy

import (
	"testing"
)

func TestCodexPlanType(t *testing.T) {
	cases := []struct {
		name     string
		planName string
		want     string
	}{
		{"empty subscription means free", "", "free"},
		{"pro maps to pro", "pro", "pro"},
		{"max_5x maps to enterprise", "max_5x", "enterprise"},
		{"max_20x maps to enterprise", "max_20x", "enterprise"},
		{"max_40x maps to enterprise", "max_40x", "enterprise"},
		{"max_60x maps to enterprise", "max_60x", "enterprise"},
		{"max_80x maps to enterprise", "max_80x", "enterprise"},
		{"max_100x maps to enterprise", "max_100x", "enterprise"},
		{"max_120x maps to enterprise", "max_120x", "enterprise"},
		{"max_200x maps to enterprise", "max_200x", "enterprise"},
		{"max_240x maps to enterprise", "max_240x", "enterprise"},
		{"plus passes through", "plus", "plus"},
		{"team passes through", "team", "team"},
		{"business passes through", "business", "business"},
		{"enterprise passes through", "enterprise", "enterprise"},
		{"unknown plan maps to unknown", "weirdplan", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := codexPlanType(tc.planName); got != tc.want {
				t.Fatalf("codexPlanType(%q) = %q, want %q", tc.planName, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/proxy/ -run TestCodexPlanType -v`
Expected: FAIL with `undefined: codexPlanType`

- [ ] **Step 3: Write minimal implementation**

Create `internal/proxy/codex_usage_handler.go`:

```go
package proxy

import (
	"github.com/modelserver/modelserver/internal/types"
)

// codexPlanType maps a modelserver Subscription.PlanName to a Codex
// RateLimitStatusPayload.plan_type enum string. Plans modelserver defines
// that have no Codex equivalent (the max_* tiers) are reported as
// "enterprise" so Codex's client treats them as a paid, non-throttled
// account. An empty/inactive subscription becomes "free".
func codexPlanType(planName string) string {
	switch planName {
	case "":
		return "free"
	case types.PlanPro:
		return "pro"
	case types.PlanMax5x, types.PlanMax20x, types.PlanMax40x,
		types.PlanMax60x, types.PlanMax80x, types.PlanMax100x,
		types.PlanMax120x, types.PlanMax200x, types.PlanMax240x:
		return "enterprise"
	case "guest", "free", "go", "plus", "prolite", "free_workspace",
		"team", "self_serve_business_usage_based", "business",
		"enterprise_cbp_usage_based", "education", "quorum", "k12",
		"enterprise", "edu":
		// Already a valid Codex plan_type — pass through.
		return planName
	default:
		return "unknown"
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/proxy/ -run TestCodexPlanType -v`
Expected: PASS (all 16 subtests)

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/codex_usage_handler.go internal/proxy/codex_usage_handler_test.go
git commit -m "feat(proxy): codex plan_type mapping helper"
```

---

## Task 2: Add wire schema structs

**Files:**
- Modify: `internal/proxy/codex_usage_handler.go`

We need exact-match structs for the Codex wire format. Use `json:"...,omitempty"` for every optional field so absent fields are simply not serialized (Codex's `serde_with::rust::double_option` accepts both absent and explicit `null`, but omitting is simpler and matches what we're modeling).

- [ ] **Step 1: Add struct definitions**

Append to `internal/proxy/codex_usage_handler.go`:

```go
// codexUsageResponse mirrors RateLimitStatusPayload from
// codex-rs/codex-backend-openapi-models/src/models/rate_limit_status_payload.rs.
// All json keys MUST stay snake_case; Codex deserializes by serde rename.
type codexUsageResponse struct {
	PlanType             string                     `json:"plan_type"`
	RateLimit            *codexRateLimitDetails     `json:"rate_limit,omitempty"`
	Credits              *codexCreditDetails        `json:"credits,omitempty"`
	AdditionalRateLimits []codexAdditionalRateLimit `json:"additional_rate_limits,omitempty"`
	RateLimitReachedType *codexRateLimitReached     `json:"rate_limit_reached_type,omitempty"`
}

// codexRateLimitDetails matches RateLimitStatusDetails. `allowed` is the
// inverse of `limit_reached`; both are required fields on the wire.
type codexRateLimitDetails struct {
	Allowed         bool                  `json:"allowed"`
	LimitReached    bool                  `json:"limit_reached"`
	PrimaryWindow   *codexWindowSnapshot  `json:"primary_window,omitempty"`
	SecondaryWindow *codexWindowSnapshot  `json:"secondary_window,omitempty"`
}

// codexWindowSnapshot matches RateLimitWindowSnapshot. All fields are i32
// in the Rust definition; reset_at is a Unix epoch second.
type codexWindowSnapshot struct {
	UsedPercent        int32 `json:"used_percent"`
	LimitWindowSeconds int32 `json:"limit_window_seconds"`
	ResetAfterSeconds  int32 `json:"reset_after_seconds"`
	ResetAt            int32 `json:"reset_at"`
}

// codexCreditDetails matches CreditStatusDetails. `balance` is a string
// containing a decimal (matches Codex's serde definition: Option<String>).
type codexCreditDetails struct {
	HasCredits bool   `json:"has_credits"`
	Unlimited  bool   `json:"unlimited"`
	Balance    string `json:"balance,omitempty"`
}

// codexAdditionalRateLimit matches AdditionalRateLimitDetails. We don't
// emit any of these today, but the struct is here for future use.
type codexAdditionalRateLimit struct {
	LimitName      string                 `json:"limit_name"`
	MeteredFeature string                 `json:"metered_feature"`
	RateLimit      *codexRateLimitDetails `json:"rate_limit,omitempty"`
}

// codexRateLimitReached is the object wrapper around RateLimitReachedKind.
// Note: this is an object {"type": "..."}, NOT a bare string. Valid kinds:
// rate_limit_reached, workspace_owner_credits_depleted,
// workspace_member_credits_depleted, workspace_owner_usage_limit_reached,
// workspace_member_usage_limit_reached, unknown.
type codexRateLimitReached struct {
	Kind string `json:"type"`
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/proxy/...`
Expected: success (no test changes yet, just confirming the new structs compile)

- [ ] **Step 3: Commit**

```bash
git add internal/proxy/codex_usage_handler.go
git commit -m "feat(proxy): codex usage response struct definitions"
```

---

## Task 3: Implement window-snapshot builder + tests

**Files:**
- Modify: `internal/proxy/codex_usage_handler.go`
- Modify: `internal/proxy/codex_usage_handler_test.go`

The hard logic is turning modelserver's `CreditRule` + current usage into a Codex `codexWindowSnapshot`. We pick the rule whose `MaxCredits` is smallest (the "tightest" window — Codex shows just one primary window). We compute `used_percent`, `limit_window_seconds`, `reset_after_seconds`, `reset_at` from the existing `computeWindowStart` / `computeWindowEnd` helpers already in `usage_handler.go`.

We also need to handle the OAuth-introspected synthetic API key (`apiKey.ID == ""`): for that case, only `CreditScopeProject` rules can be evaluated — `CreditScopeKey` rules would query a non-existent key and return 0 used, which is misleading. Skip key-scope rules entirely when `apiKey.ID == ""`.

- [ ] **Step 1: Write failing tests**

Append to `internal/proxy/codex_usage_handler_test.go`:

```go
import (
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

func TestPickPrimaryCreditRule(t *testing.T) {
	cases := []struct {
		name        string
		rules       []types.CreditRule
		apiKeyID    string
		wantWindow  string
		wantPresent bool
	}{
		{
			name:        "no rules → no primary",
			rules:       nil,
			apiKeyID:    "key1",
			wantPresent: false,
		},
		{
			name: "single rule → that rule",
			rules: []types.CreditRule{
				{Window: "5h", WindowType: "sliding", MaxCredits: 100, Scope: "project"},
			},
			apiKeyID:    "key1",
			wantWindow:  "5h",
			wantPresent: true,
		},
		{
			name: "smallest MaxCredits wins (tightest budget)",
			rules: []types.CreditRule{
				{Window: "1w", WindowType: "calendar", MaxCredits: 1000, Scope: "project"},
				{Window: "5h", WindowType: "sliding", MaxCredits: 50, Scope: "project"},
			},
			apiKeyID:    "key1",
			wantWindow:  "5h",
			wantPresent: true,
		},
		{
			name: "oauth synthetic key skips key-scope rules",
			rules: []types.CreditRule{
				{Window: "5h", WindowType: "sliding", MaxCredits: 10, Scope: "key"},
				{Window: "1w", WindowType: "calendar", MaxCredits: 100, Scope: "project"},
			},
			apiKeyID:    "",
			wantWindow:  "1w",
			wantPresent: true,
		},
		{
			name: "oauth synthetic key with only key-scope rules → no primary",
			rules: []types.CreditRule{
				{Window: "5h", WindowType: "sliding", MaxCredits: 10, Scope: "key"},
			},
			apiKeyID:    "",
			wantPresent: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := pickPrimaryCreditRule(tc.rules, tc.apiKeyID)
			if ok != tc.wantPresent {
				t.Fatalf("present = %v, want %v", ok, tc.wantPresent)
			}
			if ok && got.Window != tc.wantWindow {
				t.Fatalf("window = %q, want %q", got.Window, tc.wantWindow)
			}
		})
	}
}

func TestBuildCodexWindowSnapshot(t *testing.T) {
	// 100 credits used out of 200, sliding 5h window. Window started 2h ago,
	// so reset_after_seconds = 3h = 10800.
	now := time.Now().UTC()
	rule := types.CreditRule{
		Window: "5h", WindowType: "sliding", MaxCredits: 200, Scope: "project",
	}
	used := 100.0
	windowStart := now.Add(-2 * time.Hour)

	got := buildCodexWindowSnapshot(used, rule, windowStart, now)
	if got.UsedPercent != 50 {
		t.Errorf("UsedPercent = %d, want 50", got.UsedPercent)
	}
	if got.LimitWindowSeconds != 5*3600 {
		t.Errorf("LimitWindowSeconds = %d, want %d", got.LimitWindowSeconds, 5*3600)
	}
	// Allow 2s slop for test execution time.
	if got.ResetAfterSeconds < 3*3600-2 || got.ResetAfterSeconds > 3*3600+2 {
		t.Errorf("ResetAfterSeconds = %d, want ~%d", got.ResetAfterSeconds, 3*3600)
	}
	if got.ResetAt < int32(now.Add(3*time.Hour-2*time.Second).Unix()) {
		t.Errorf("ResetAt = %d, too early", got.ResetAt)
	}
}

func TestBuildCodexWindowSnapshot_PercentCapped(t *testing.T) {
	// Over-quota: used 300 of 200 → 100% capped, not 150%.
	now := time.Now().UTC()
	rule := types.CreditRule{
		Window: "5h", WindowType: "sliding", MaxCredits: 200, Scope: "project",
	}
	got := buildCodexWindowSnapshot(300.0, rule, now.Add(-time.Hour), now)
	if got.UsedPercent != 100 {
		t.Errorf("UsedPercent = %d, want 100 (capped)", got.UsedPercent)
	}
}

func TestBuildCodexWindowSnapshot_ZeroMaxCredits(t *testing.T) {
	// Defensive: MaxCredits = 0 must not divide by zero.
	now := time.Now().UTC()
	rule := types.CreditRule{
		Window: "5h", WindowType: "sliding", MaxCredits: 0, Scope: "project",
	}
	got := buildCodexWindowSnapshot(10.0, rule, now.Add(-time.Hour), now)
	if got.UsedPercent != 0 {
		t.Errorf("UsedPercent = %d, want 0 when MaxCredits=0", got.UsedPercent)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run 'TestPickPrimaryCreditRule|TestBuildCodexWindowSnapshot' -v`
Expected: FAIL with `undefined: pickPrimaryCreditRule` and `undefined: buildCodexWindowSnapshot`

- [ ] **Step 3: Add implementations**

Append to `internal/proxy/codex_usage_handler.go`:

```go
import (
	"time"

	"github.com/modelserver/modelserver/internal/types"
)
// NOTE: if the file already has an import block, merge — don't add a second block.

// pickPrimaryCreditRule returns the "tightest" credit rule (smallest
// MaxCredits) from the policy. When apiKeyID is empty (OAuth-introspected
// synthetic key), key-scope rules are skipped — we cannot meaningfully
// report usage for a key that has no real ID.
func pickPrimaryCreditRule(rules []types.CreditRule, apiKeyID string) (types.CreditRule, bool) {
	var best types.CreditRule
	found := false
	for _, r := range rules {
		if apiKeyID == "" && r.EffectiveScope() == types.CreditScopeKey {
			continue
		}
		if !found || r.MaxCredits < best.MaxCredits {
			best = r
			found = true
		}
	}
	return best, found
}

// buildCodexWindowSnapshot converts a CreditRule plus its current usage
// into a Codex RateLimitWindowSnapshot. used is the credits consumed in
// the current window (as returned by computeCreditProgress); windowStart
// is the start of that window. now is passed explicitly for testability.
//
// All output fields are int32 to match Codex's wire schema.
func buildCodexWindowSnapshot(used float64, rule types.CreditRule, windowStart, now time.Time) codexWindowSnapshot {
	windowEnd := computeWindowEnd(windowStart, rule.Window, rule.WindowType)
	limitSeconds := int32(windowEnd.Sub(windowStart).Seconds())
	resetAfter := int32(windowEnd.Sub(now).Seconds())
	if resetAfter < 0 {
		resetAfter = 0
	}

	var pct int32
	if rule.MaxCredits > 0 {
		p := (used / float64(rule.MaxCredits)) * 100.0
		if p > 100 {
			p = 100
		}
		if p < 0 {
			p = 0
		}
		pct = int32(p)
	}

	return codexWindowSnapshot{
		UsedPercent:        pct,
		LimitWindowSeconds: limitSeconds,
		ResetAfterSeconds:  resetAfter,
		ResetAt:            int32(windowEnd.Unix()),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run 'TestPickPrimaryCreditRule|TestBuildCodexWindowSnapshot' -v`
Expected: PASS (all subtests)

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/codex_usage_handler.go internal/proxy/codex_usage_handler_test.go
git commit -m "feat(proxy): codex window-snapshot builder + primary rule picker"
```

---

## Task 4: Implement credits-details builder + tests

**Files:**
- Modify: `internal/proxy/codex_usage_handler.go`
- Modify: `internal/proxy/codex_usage_handler_test.go`

modelserver's extra-usage settings (`ExtraUsageSettings.BalanceFen`) give us a real credit balance. The mapping:

- No `ExtraUsageSettings` row → `{has_credits: false, unlimited: false}` (no string `balance` field emitted).
- `BypassBalanceCheck == true` → `{has_credits: true, unlimited: true}` (no `balance`).
- `Enabled == false` → `{has_credits: false, unlimited: false}` (treat as "extra usage not turned on, no spendable credits").
- Otherwise → `{has_credits: BalanceFen > 0, unlimited: false, balance: "X.XX"}` where the string is the fen amount divided by 10^8, formatted with 2 decimal places (this matches the project's existing convention — see `internal/types/extra_usage.go:59`: `BalanceFen` is in units of 10^-8 credits).

- [ ] **Step 1: Write failing tests**

Append to `internal/proxy/codex_usage_handler_test.go`:

```go
func TestBuildCodexCredits(t *testing.T) {
	cases := []struct {
		name     string
		settings *types.ExtraUsageSettings
		want     *codexCreditDetails
	}{
		{
			name:     "nil settings → no extra credits",
			settings: nil,
			want:     &codexCreditDetails{HasCredits: false, Unlimited: false},
		},
		{
			name: "bypass balance check → unlimited",
			settings: &types.ExtraUsageSettings{
				Enabled: true, BypassBalanceCheck: true, BalanceFen: 0,
			},
			want: &codexCreditDetails{HasCredits: true, Unlimited: true},
		},
		{
			name: "disabled → no credits",
			settings: &types.ExtraUsageSettings{
				Enabled: false, BalanceFen: 12345,
			},
			want: &codexCreditDetails{HasCredits: false, Unlimited: false},
		},
		{
			name: "enabled with positive balance",
			settings: &types.ExtraUsageSettings{
				Enabled: true, BalanceFen: 150_000_000, // 1.50 credits
			},
			want: &codexCreditDetails{HasCredits: true, Unlimited: false, Balance: "1.50"},
		},
		{
			name: "enabled with zero balance",
			settings: &types.ExtraUsageSettings{
				Enabled: true, BalanceFen: 0,
			},
			want: &codexCreditDetails{HasCredits: false, Unlimited: false, Balance: "0.00"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildCodexCredits(tc.settings)
			if got.HasCredits != tc.want.HasCredits {
				t.Errorf("HasCredits = %v, want %v", got.HasCredits, tc.want.HasCredits)
			}
			if got.Unlimited != tc.want.Unlimited {
				t.Errorf("Unlimited = %v, want %v", got.Unlimited, tc.want.Unlimited)
			}
			if got.Balance != tc.want.Balance {
				t.Errorf("Balance = %q, want %q", got.Balance, tc.want.Balance)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run TestBuildCodexCredits -v`
Expected: FAIL with `undefined: buildCodexCredits`

- [ ] **Step 3: Add implementation**

Append to `internal/proxy/codex_usage_handler.go`:

```go
import "fmt"
// merge into existing import block

// buildCodexCredits converts a modelserver ExtraUsageSettings row into the
// Codex credits status object. nil settings (no row) means the project
// has never opted into extra-usage; we report no credits. balance is
// rendered as a decimal string with 2 places (BalanceFen is in 10^-8
// credits per internal/types/extra_usage.go).
func buildCodexCredits(s *types.ExtraUsageSettings) *codexCreditDetails {
	if s == nil {
		return &codexCreditDetails{HasCredits: false, Unlimited: false}
	}
	if s.BypassBalanceCheck {
		return &codexCreditDetails{HasCredits: true, Unlimited: true}
	}
	if !s.Enabled {
		return &codexCreditDetails{HasCredits: false, Unlimited: false}
	}
	// BalanceFen is in units of 10^-8 credits.
	balance := float64(s.BalanceFen) / 1e8
	return &codexCreditDetails{
		HasCredits: s.BalanceFen > 0,
		Unlimited:  false,
		Balance:    fmt.Sprintf("%.2f", balance),
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run TestBuildCodexCredits -v`
Expected: PASS (all 5 subtests)

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/codex_usage_handler.go internal/proxy/codex_usage_handler_test.go
git commit -m "feat(proxy): codex credits-details builder"
```

---

## Task 5: Implement HandleCodexUsage + handler tests

**Files:**
- Modify: `internal/proxy/codex_usage_handler.go`
- Modify: `internal/proxy/codex_usage_handler_test.go`

Now the actual HTTP handler. It needs access to `h.store` to query extra-usage settings and credit usage — so it lives on the `*Handler` receiver like `HandleUsage`.

Note: `computeCreditProgress` in `usage_handler.go` takes `*store.Store` directly and queries either `SumCreditsInWindowByProject` or `SumCreditsInWindow(apiKeyID, ...)`. The unit tests for the handler will need a way to short-circuit this. Strategy: keep the handler thin and unit-test the helpers we've already built. Then for `HandleCodexUsage`, write a single end-to-end test that uses an in-memory store helper (look for existing patterns; if none, mock at the level of "no policy, no subscription" which exercises the most code without touching the DB).

Inspect the existing tests for a store helper pattern first:

```bash
grep -rn 'store.NewWithPool\|NewTestStore\|newTestStore\|sqlmock' internal/ 2>/dev/null | head -20
```

If no test-store helper exists, the test strategy is to construct an `*Handler` with `store: nil` and only exercise paths that don't reach the store (no policy, no subscription, no extra-usage). That gives us:
- 401 on missing auth context (no apiKey)
- 200 with `plan_type: free`, no `rate_limit`, no `credits` when project has no subscription, no policy, no extra-usage.

Anything that does need the store (computing window usage, reading ExtraUsageSettings) is covered by the unit tests we already wrote for `pickPrimaryCreditRule` / `buildCodexWindowSnapshot` / `buildCodexCredits`. The handler is then a 30-line composition that we cover with one happy-path integration test if and only if a test store exists.

- [ ] **Step 1: Write failing tests**

Append to `internal/proxy/codex_usage_handler_test.go`:

```go
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
)
// merge imports

func TestHandleCodexUsage_Unauthenticated(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("GET", "/api/codex/usage", nil)
	rec := httptest.NewRecorder()
	h.HandleCodexUsage(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCodexUsage_FreeNoSubscription(t *testing.T) {
	// No subscription, no policy, no extra-usage settings — store is never
	// hit because settings lookup is via h.store; we pass nil and accept
	// that the handler must gracefully treat a store error as "no settings".
	h := &Handler{}
	apiKey := &types.APIKey{ID: "k1", ProjectID: "p1"}
	project := &types.Project{ID: "p1", Status: types.ProjectStatusActive}

	req := httptest.NewRequest("GET", "/api/codex/usage", nil)
	ctx := context.WithValue(req.Context(), ctxAPIKey, apiKey)
	ctx = context.WithValue(ctx, ctxProject, project)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.HandleCodexUsage(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got codexUsageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.PlanType != "free" {
		t.Errorf("PlanType = %q, want %q", got.PlanType, "free")
	}
	if got.RateLimit != nil {
		t.Errorf("RateLimit = %+v, want nil", got.RateLimit)
	}
	// Credits should always be present (even if has_credits=false) — Codex
	// reads it eagerly. We expect the "no settings" path.
	if got.Credits == nil {
		t.Fatal("Credits = nil, want non-nil with HasCredits=false")
	}
	if got.Credits.HasCredits || got.Credits.Unlimited {
		t.Errorf("Credits = %+v, want has=false,unlimited=false", got.Credits)
	}
}

func TestHandleCodexUsage_WireFormatSnakeCase(t *testing.T) {
	// Verify the raw JSON keys are snake_case — Codex's serde rename
	// is strict. Build a response struct directly and serialize.
	resp := codexUsageResponse{
		PlanType: "pro",
		RateLimit: &codexRateLimitDetails{
			Allowed:      true,
			LimitReached: false,
			PrimaryWindow: &codexWindowSnapshot{
				UsedPercent: 50, LimitWindowSeconds: 3600,
				ResetAfterSeconds: 1800, ResetAt: 9_999_999,
			},
		},
		Credits: &codexCreditDetails{
			HasCredits: true, Unlimited: false, Balance: "10.00",
		},
		RateLimitReachedType: &codexRateLimitReached{Kind: "rate_limit_reached"},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	mustContain := []string{
		`"plan_type":"pro"`,
		`"rate_limit":{`,
		`"limit_reached":false`,
		`"primary_window":{`,
		`"used_percent":50`,
		`"limit_window_seconds":3600`,
		`"reset_after_seconds":1800`,
		`"reset_at":9999999`,
		`"credits":{`,
		`"has_credits":true`,
		`"unlimited":false`,
		`"balance":"10.00"`,
		`"rate_limit_reached_type":{"type":"rate_limit_reached"}`,
	}
	for _, s := range mustContain {
		if !contains(out, s) {
			t.Errorf("missing %q in %s", s, out)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run 'TestHandleCodexUsage' -v`
Expected: FAIL with `undefined: HandleCodexUsage` and `(*Handler).HandleCodexUsage undefined`

- [ ] **Step 3: Implement the handler**

Append to `internal/proxy/codex_usage_handler.go`:

```go
import (
	"encoding/json"
	"net/http"
)
// merge imports

// HandleCodexUsage answers Codex CLI's GET /api/codex/usage (and the
// /wham/usage alias). It reshapes whatever auth/policy/subscription data
// AuthMiddleware loaded into the Codex RateLimitStatusPayload format
// (snake_case keys, snake_case enum variants). Both API-key and OAuth-
// introspected callers go through the same code path; the OAuth path is
// identified by apiKey.ID == "".
func (h *Handler) HandleCodexUsage(w http.ResponseWriter, r *http.Request) {
	apiKey := APIKeyFromContext(r.Context())
	project := ProjectFromContext(r.Context())
	policy := PolicyFromContext(r.Context())
	subscription := SubscriptionFromContext(r.Context())

	if apiKey == nil || project == nil {
		writeProxyError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	resp := codexUsageResponse{
		PlanType: codexPlanType(subscriptionPlanName(subscription)),
	}

	// rate_limit: pick tightest CreditRule, compute usage in its window.
	if policy != nil && h.store != nil {
		if rule, ok := pickPrimaryCreditRule(policy.CreditRules, apiKey.ID); ok {
			now := time.Now().UTC()
			windowStart := computeWindowStart(now, rule.Window, rule.WindowType, rule.AnchorTime)

			var used float64
			var err error
			if rule.EffectiveScope() == types.CreditScopeProject {
				used, err = h.store.SumCreditsInWindowByProject(project.ID, windowStart)
			} else {
				used, err = h.store.SumCreditsInWindow(apiKey.ID, windowStart)
			}
			if err != nil {
				h.logger.Error("codex usage: sum credits", "error", err, "project", project.ID)
				// Fall through with no rate_limit rather than 500 — the
				// caller can still get plan_type and credits.
			} else {
				win := buildCodexWindowSnapshot(used, rule, windowStart, now)
				limitReached := win.UsedPercent >= 100
				resp.RateLimit = &codexRateLimitDetails{
					Allowed:       !limitReached,
					LimitReached:  limitReached,
					PrimaryWindow: &win,
				}
				if limitReached {
					resp.RateLimitReachedType = &codexRateLimitReached{
						Kind: "rate_limit_reached",
					}
				}
			}
		}
	}

	// credits: read extra-usage settings.
	var settings *types.ExtraUsageSettings
	if h.store != nil {
		settings, _ = h.store.GetExtraUsageSettings(project.ID)
	}
	resp.Credits = buildCodexCredits(settings)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// subscriptionPlanName returns the subscription's PlanName, or "" if the
// subscription is nil or inactive.
func subscriptionPlanName(s *types.Subscription) string {
	if s == nil || !s.IsActive() {
		return ""
	}
	return s.PlanName
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run 'TestHandleCodexUsage' -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Run full proxy package tests**

Run: `go test ./internal/proxy/...`
Expected: all existing tests still pass

- [ ] **Step 6: Commit**

```bash
git add internal/proxy/codex_usage_handler.go internal/proxy/codex_usage_handler_test.go
git commit -m "feat(proxy): HandleCodexUsage returns Codex-compatible payload"
```

---

## Task 6: Implement HandleCodexAccount + tests

**Files:**
- Create: `internal/proxy/codex_account_handler.go`
- Create: `internal/proxy/codex_account_handler_test.go`

A "who am I" endpoint that returns enough info for Codex CLI (or any custom client) to identify the caller's project and account. Modelserver doesn't expose ChatGPT-style `chatgpt_user_id` / `chatgpt_account_id` from the OAuth introspection result directly to the proxy handler (those would be JWT claims), so we report what we have: `project_id`, `project_name`, `user_id` (the synthetic `apiKey.CreatedBy`), `auth_method` (`api_key` or `oauth_token`), `plan_type`, and `oauth_grant_id` if present.

This endpoint is NEW (Codex CLI doesn't call it natively), but is useful for:
- modelserver's own CLI/SDK to confirm "I am authenticated and tied to project X".
- Custom clients written against the Codex base URL.

- [ ] **Step 1: Write failing tests**

Create `internal/proxy/codex_account_handler_test.go`:

```go
package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestHandleCodexAccount_Unauthenticated(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest("GET", "/api/codex/account", nil)
	rec := httptest.NewRecorder()
	h.HandleCodexAccount(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandleCodexAccount_APIKeyAuth(t *testing.T) {
	h := &Handler{}
	apiKey := &types.APIKey{ID: "k1", ProjectID: "p1", CreatedBy: "user-123"}
	project := &types.Project{
		ID: "p1", Name: "My Project", Status: types.ProjectStatusActive,
	}

	req := httptest.NewRequest("GET", "/api/codex/account", nil)
	ctx := context.WithValue(req.Context(), ctxAPIKey, apiKey)
	ctx = context.WithValue(ctx, ctxProject, project)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.HandleCodexAccount(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got codexAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ProjectID != "p1" {
		t.Errorf("ProjectID = %q, want p1", got.ProjectID)
	}
	if got.ProjectName != "My Project" {
		t.Errorf("ProjectName = %q, want My Project", got.ProjectName)
	}
	if got.UserID != "user-123" {
		t.Errorf("UserID = %q, want user-123", got.UserID)
	}
	if got.AuthMethod != "api_key" {
		t.Errorf("AuthMethod = %q, want api_key", got.AuthMethod)
	}
	if got.PlanType != "free" {
		t.Errorf("PlanType = %q, want free (no subscription)", got.PlanType)
	}
	if got.OAuthGrantID != "" {
		t.Errorf("OAuthGrantID = %q, want empty for api_key auth", got.OAuthGrantID)
	}
}

func TestHandleCodexAccount_OAuthAuth(t *testing.T) {
	h := &Handler{}
	// Synthetic key from handleTokenIntrospectionAuth: ID == "".
	apiKey := &types.APIKey{ID: "", ProjectID: "p1", CreatedBy: "user-oauth"}
	project := &types.Project{
		ID: "p1", Name: "My Project", Status: types.ProjectStatusActive,
	}

	req := httptest.NewRequest("GET", "/api/codex/account", nil)
	ctx := context.WithValue(req.Context(), ctxAPIKey, apiKey)
	ctx = context.WithValue(ctx, ctxProject, project)
	ctx = context.WithValue(ctx, ctxOAuthGrantID, "grant-abc")
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	h.HandleCodexAccount(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var got codexAccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.AuthMethod != "oauth_token" {
		t.Errorf("AuthMethod = %q, want oauth_token", got.AuthMethod)
	}
	if got.OAuthGrantID != "grant-abc" {
		t.Errorf("OAuthGrantID = %q, want grant-abc", got.OAuthGrantID)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/proxy/ -run TestHandleCodexAccount -v`
Expected: FAIL with `undefined: (*Handler).HandleCodexAccount` and `undefined: codexAccountResponse`

- [ ] **Step 3: Implement the handler**

Create `internal/proxy/codex_account_handler.go`:

```go
package proxy

import (
	"encoding/json"
	"net/http"
)

// codexAccountResponse describes the currently-authenticated caller. There
// is no equivalent endpoint in Codex's upstream API — this is a modelserver-
// specific addition under the /api/codex/ namespace for SDK convenience.
// Keys are snake_case to match the rest of the /api/codex/* family.
type codexAccountResponse struct {
	ProjectID    string `json:"project_id"`
	ProjectName  string `json:"project_name"`
	UserID       string `json:"user_id,omitempty"`
	AuthMethod   string `json:"auth_method"`            // "api_key" | "oauth_token"
	PlanType     string `json:"plan_type"`
	OAuthGrantID string `json:"oauth_grant_id,omitempty"`
}

// HandleCodexAccount returns identity information about the caller.
// An empty apiKey.ID is the signal that the caller authenticated via
// OAuth introspection (see handleTokenIntrospectionAuth).
func (h *Handler) HandleCodexAccount(w http.ResponseWriter, r *http.Request) {
	apiKey := APIKeyFromContext(r.Context())
	project := ProjectFromContext(r.Context())
	subscription := SubscriptionFromContext(r.Context())

	if apiKey == nil || project == nil {
		writeProxyError(w, http.StatusUnauthorized, "missing auth context")
		return
	}

	authMethod := "api_key"
	if apiKey.ID == "" {
		authMethod = "oauth_token"
	}

	resp := codexAccountResponse{
		ProjectID:    project.ID,
		ProjectName:  project.Name,
		UserID:       apiKey.CreatedBy,
		AuthMethod:   authMethod,
		PlanType:     codexPlanType(subscriptionPlanName(subscription)),
		OAuthGrantID: OAuthGrantIDFromContext(r.Context()),
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/proxy/ -run TestHandleCodexAccount -v`
Expected: PASS (3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/codex_account_handler.go internal/proxy/codex_account_handler_test.go
git commit -m "feat(proxy): HandleCodexAccount returns caller identity"
```

---

## Task 7: Mount the new routes

**Files:**
- Modify: `internal/proxy/router.go`

Add the three new routes to `MountRoutes`. We deliberately do NOT put them under the `/v1` chi.Router — Codex expects the literal paths `/api/codex/usage`, `/wham/usage`, and (our addition) `/api/codex/account`. We mount each path with the same middleware chain by reusing the `wire` closure inside its own `r.Route(...)` block.

- [ ] **Step 1: Look at the current MountRoutes file before editing**

Read `internal/proxy/router.go` lines 24-67. Confirm that `wire` is defined locally inside `MountRoutes` and can be reused. (It is — it's a `func(r chi.Router)` closure.)

- [ ] **Step 2: Modify `MountRoutes` to register new routes**

Edit `internal/proxy/router.go`. Find the block ending with the Gemini route (line 66) and add three new `r.Route` blocks just after it, before the closing brace of `MountRoutes`:

```go
	// Codex CLI compatibility: native Codex base URLs are .../api/codex/* or
	// .../wham/* (the latter when the user's configured base_url contains
	// "/backend-api"). We mount both alias families pointing at the same
	// handler so a Codex CLI configured to use modelserver as upstream sees
	// the rate-limit and plan info it expects.
	r.Route("/api/codex", func(r chi.Router) {
		wire(r)
		r.Get("/usage", handler.HandleCodexUsage)
		r.Get("/account", handler.HandleCodexAccount)
	})
	r.Route("/wham", func(r chi.Router) {
		wire(r)
		r.Get("/usage", handler.HandleCodexUsage)
	})
```

- [ ] **Step 3: Build to verify**

Run: `go build ./...`
Expected: success

- [ ] **Step 4: Run the entire proxy test package**

Run: `go test ./internal/proxy/...`
Expected: all tests pass

- [ ] **Step 5: Commit**

```bash
git add internal/proxy/router.go
git commit -m "feat(proxy): mount /api/codex/* and /wham/usage routes"
```

---

## Task 8: Router-level integration test

**Files:**
- Modify: `internal/proxy/router_test.go`

Verify the routes are actually wired up and that AuthMiddleware applies (i.e. a request with no API key returns 401, not 404).

- [ ] **Step 1: Look at how existing router_test.go tests mount routes**

Read `internal/proxy/router_test.go`. Find an existing test that calls `MountRoutes` (or equivalent) and uses `httptest.NewServer`/`httptest.NewRecorder` with chi. Match its structure exactly.

If no such test exists, write the simplest possible variant below:

- [ ] **Step 2: Append the failing test**

Append to `internal/proxy/router_test.go`:

```go
func TestMountRoutes_CodexAliases_RequireAuth(t *testing.T) {
	// Mount routes with no introspector and an empty store; we only want
	// to confirm the routes exist and the auth middleware fires.
	cat := newTestCatalog()
	h := &Handler{catalog: cat}

	r := chi.NewRouter()
	MountRoutes(
		r,
		nil, // store — AuthMiddleware tolerates nil for hash lookup? if not, use a real test store helper
		h,
		config.TraceConfig{},
		nil, // limiter
		cat,
		config.ExtraUsageConfig{},
		1<<20,
		1<<20,
		nil, // encKey
		slog.Default(),
		nil, // introspector
	)

	for _, path := range []string{
		"/api/codex/usage",
		"/api/codex/account",
		"/wham/usage",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("GET", path, nil)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s: status = %d, want 401 (missing api key)", path, rec.Code)
			}
		})
	}
}
```

- [ ] **Step 3: Run the test to see if it fails or surfaces a wiring issue**

Run: `go test ./internal/proxy/ -run TestMountRoutes_CodexAliases_RequireAuth -v`

If the test panics because `store` cannot be nil for `AuthMiddleware`, replace `nil` for the store argument with whatever helper the existing tests use (find with `grep -rn 'store\.New\|NewTestStore\|newTestStore' internal/proxy/*_test.go`). If no helper exists, add a small one in this test file:

```go
// minimal store stub — AuthMiddleware only calls GetAPIKeyByHash on it
// in this code path, and that path returns "invalid api key" 401 before
// any other store method is invoked when the api key is missing entirely.
```

Inspect `AuthMiddleware` (lines 207-243) — when `rawKey == ""` (line 208), it returns 401 immediately without touching the store. So passing `nil` is safe in this specific test. If Go panics at a later middleware (Trace, ResolveModel) before the 401 is written, restructure the test to call only `AuthMiddleware` rather than the full `wire`. Worst case, this test becomes:

```go
// Alternative: mount routes one at a time using only AuthMiddleware
// so we don't need the full middleware stack:
r := chi.NewRouter()
r.Use(AuthMiddleware(nil, nil, nil))
r.Get("/api/codex/usage", h.HandleCodexUsage)
r.Get("/api/codex/account", h.HandleCodexAccount)
r.Get("/wham/usage", h.HandleCodexUsage)
```

Pick whichever variant matches existing patterns.

Expected: PASS (all 3 subtests return 401).

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/router_test.go
git commit -m "test(proxy): codex-compat routes require auth"
```

---

## Task 9: Documentation

**Files:**
- Create or modify: `docs/proxy-endpoints.md` (or wherever existing endpoint docs live — check `docs/` first; if nothing matches, create this file)

- [ ] **Step 1: Check for an existing endpoint doc**

Run: `ls docs/ && grep -rln '/v1/usage\|/v1/messages' docs/ 2>/dev/null`
If a file already documents proxy endpoints (e.g. `docs/api.md`), append to it. Otherwise create `docs/proxy-endpoints.md`.

- [ ] **Step 2: Write the doc section**

Add this content (choose the appropriate file based on Step 1):

````markdown
## Codex CLI Compatibility Endpoints

Modelserver exposes a small set of endpoints under `/api/codex/` and `/wham/` that match the wire format used by the OpenAI Codex CLI. This lets a Codex CLI configured to point at modelserver as its upstream see the right plan, rate-limit, and credit info — and lets any holder of a modelserver OAuth access token (or API key) query their current project's state.

### `GET /api/codex/usage` and `GET /wham/usage`

Returns the caller's current rate-limit window, plan, and extra-usage credit balance in the schema Codex CLI expects (`RateLimitStatusPayload`).

**Auth:** `Authorization: Bearer <api_key_or_oauth_token>` (same as `/v1/messages`).

**Response:**

```json
{
  "plan_type": "pro",
  "rate_limit": {
    "allowed": true,
    "limit_reached": false,
    "primary_window": {
      "used_percent": 45,
      "limit_window_seconds": 18000,
      "reset_after_seconds": 9900,
      "reset_at": 1717891234
    }
  },
  "credits": {
    "has_credits": true,
    "unlimited": false,
    "balance": "10.50"
  }
}
```

**Field notes:**

- `plan_type`: one of Codex's enum values (`free`, `plus`, `pro`, `team`, `business`, `enterprise`, ...). Modelserver's `max_*` plans are reported as `enterprise`.
- `rate_limit.primary_window`: derived from the *tightest* `CreditRule` in the project's policy (smallest `MaxCredits`). Absent when the project has no policy.
- `credits.balance`: a decimal string representing the project's extra-usage balance (10^-8-fen units divided to credits). Absent when extra-usage has never been opted into.

### `GET /api/codex/account`

Returns identity info for the caller — useful for CLIs and SDKs to verify "I am authenticated and tied to project X".

**Response:**

```json
{
  "project_id": "proj_abc",
  "project_name": "My Project",
  "user_id": "user_123",
  "auth_method": "oauth_token",
  "plan_type": "pro",
  "oauth_grant_id": "grant_xyz"
}
```

**Field notes:**

- `auth_method`: `api_key` if the caller used a modelserver-issued API key (`ms-...`), `oauth_token` if the caller's bearer token was OAuth-introspected (e.g. via Hydra).
- `oauth_grant_id`: present only on the OAuth path, references the `oauth_grants` row matching `(project_id, user_id, client_id)`.

### Pointing Codex CLI at modelserver

Set the Codex CLI's base URL to the modelserver proxy URL (e.g. `https://modelserver.example.com`). Codex's path-style detection routes to `/api/codex/usage` automatically. To force the `/wham/usage` alias, set a base URL containing `/backend-api` (e.g. `https://modelserver.example.com/backend-api`).
````

- [ ] **Step 3: Commit**

```bash
git add docs/proxy-endpoints.md   # or whichever file you edited
git commit -m "docs(proxy): document codex-compat endpoints"
```

---

## Task 10: End-to-end verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full test suite**

Run: `go test ./...`
Expected: all tests pass

- [ ] **Step 2: Hand-test with curl against a running modelserver**

Start modelserver locally (whatever the project's standard local-dev command is — likely `go run ./cmd/modelserver` with a test config). With a valid API key for an existing project, make sure both endpoints respond with parseable JSON:

```bash
curl -sS -H "Authorization: Bearer ms-<your-test-key>" \
    http://localhost:<proxy_port>/api/codex/usage | jq .
curl -sS -H "Authorization: Bearer ms-<your-test-key>" \
    http://localhost:<proxy_port>/api/codex/account | jq .
curl -sS -H "Authorization: Bearer ms-<your-test-key>" \
    http://localhost:<proxy_port>/wham/usage | jq .
```

Expected: each returns 200 with a snake_case JSON object that includes at least `plan_type` (for usage) or `project_id` (for account).

- [ ] **Step 3: Verify Codex CLI accepts the response**

Optional but high-value. Build the Codex CLI from `/root/codex/codex-rs`, configure it to use modelserver as its `OPENAI_BASE_URL` (or whichever environment variable the latest Codex uses to override the API base), and run `codex` interactively. The "rate limit" / "plan" UI should render without errors. If Codex logs a deserialization error, capture the wire response with curl and check it against the schemas in Task 2 — the most likely culprits are an enum string with the wrong casing or an absent required field (`plan_type`, `allowed`, `limit_reached`).

- [ ] **Step 4: If everything passes, finish on the branch**

Use `superpowers:finishing-a-development-branch` to decide whether to merge, open a PR, or push.

---

## Self-Review Notes

**Spec coverage:** Both items from the user request — "fetch upstream user id + usage" inspection (covered in plan preamble; no upstream Codex API is mirrored, but modelserver's own data is exposed in Codex's wire format) and "let oauth-access-token holders read current project info + usage" (covered by `HandleCodexAccount` + `HandleCodexUsage`, both of which work uniformly for API-key and OAuth callers via the existing `AuthMiddleware` synthetic-key pattern).

**Placeholder scan:** No TBDs, no "implement later". Test exit-condition codes show real JSON strings; structures show every field with exact serde-compatible JSON keys.

**Type consistency:** `codexUsageResponse` / `codexRateLimitDetails` / `codexWindowSnapshot` / `codexCreditDetails` / `codexRateLimitReached` / `codexAccountResponse` are used consistently across tasks 2-7. Function names — `codexPlanType`, `pickPrimaryCreditRule`, `buildCodexWindowSnapshot`, `buildCodexCredits`, `subscriptionPlanName`, `HandleCodexUsage`, `HandleCodexAccount` — referenced identically in their definition tasks and in calling code.

**Known caveat (not blocking):** Task 5's handler depends on `h.store` being non-nil for the rate_limit and credits fields. Unit tests stub via `h.store == nil` and verify the degraded-but-correct path (plan_type returned, no rate_limit object, credits with `has_credits=false`). A fuller integration test would need the modelserver project's test-store helper; if one exists, expand `TestHandleCodexUsage_*` to cover the "policy + sum returns N → percentage M" path. Look for `newTestStore` or similar in `internal/store/*_test.go` and `internal/proxy/*_test.go` before deciding whether to add such a test.

**Plan-time uncertainty surfaced:**

1. **Test-store availability:** Task 5 and Task 8 both note "if a test store helper exists". If one does not, the integration coverage is thinner than ideal — the implementer should grep for it as the first thing in those tasks and adjust.
2. **`max_*` → `enterprise` mapping:** Chosen to maximize Codex client tolerance; this could equally well map to `unknown`. The decision is documented in `codexPlanType` itself so future readers see why.
3. **`primary_window` selection rule:** "Tightest budget" (smallest `MaxCredits`) is a defensible heuristic but may not match user expectation. If projects typically have a `5h` and a `1w` rule and the `5h` one is much smaller, that becomes the primary — which is usually what a user wants. If a different heuristic is wanted (e.g. "shortest window") it's a one-line change in `pickPrimaryCreditRule`.
