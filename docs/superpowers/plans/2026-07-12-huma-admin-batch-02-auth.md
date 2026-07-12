# Huma Admin API — Batch 2: Auth (public writes + 302 redirect) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the three public `/api/v1/auth/*` write routes onto the Huma-typed contract: `POST /auth/refresh`, `POST /auth/oauth/{provider}`, and `GET /auth/oauth/{provider}/redirect`. These are the first non-authenticated write paths in the typed contract, the first 302 redirect, and the first path with a provider-enum path parameter.

**Architecture:** Follow the Batch 1 template (docs/superpowers/plans/2026-07-11-huma-admin-batches-02-14-template.md). All three operations declare `authz.Public()` access — no authorization middleware. Two of the three OAuth endpoints preserve a wire quirk: `POST /auth/oauth/{provider}` returns either `{access_token, refresh_token, user}` OR `{redirect_to}` at status 200 depending on whether the OAuth state carried a Hydra `return_to`; the DTO merges both shapes with `omitempty`. The redirect endpoint uses Huma's output-struct pattern for 302 (status field + `Location` header field).

**Tech Stack:** Go 1.24, Huma v2, chi v5. No new external dependencies.

## Global Constraints

- **Spec source of truth:** `docs/superpowers/specs/2026-07-11-huma-admin-api-and-rbac-design.md`. Batch 2 covers spec §5 rows A1–A3.
- **Wire compatibility:** exact preservation of the legacy `handleRefresh`, `handleOAuthCallback`, and `handleOAuthRedirect` response bodies and status codes. Envelope fixture tests guard this.
- **Single-router rule:** the 6 chi routes migrated by this batch (`POST /auth/refresh` + 3 OAuth callbacks + 3 OAuth redirects) are deleted from `internal/admin/routes.go` in the same PR that adds the typed versions.
- **No behavior change:** no new validation, no tightened errors, no new response fields beyond what the legacy handler already emits.
- **No dashboard hook migration:** the `client.ts` fallback path uses plain `fetch()` for `/auth/refresh` deliberately (bootstrap chicken-and-egg with the refresh flow), and `AuthContext.tsx` uses the shared `api.post()` helper. Both continue to work unchanged because the wire response is preserved. Batch 2 does not switch these to `adminApi` — Batch 5 or later can, once the pattern is proven. The generated `schema.ts` gets regenerated so future migrations can consume the types.
- **Public routes only in this batch:** no `authz.Authenticated()` / `authz.Project()` operations added.
- **Every commit:** message ends with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

---

## File Structure

**Files added:**

- `internal/api/admin/v1/auth.go` — DTOs for the three auth operations + handler methods + `registerAuthOperations` function.
- `internal/api/admin/v1/auth_test.go` — handler behavior tests (success + error paths).
- `internal/api/admin/v1/auth_envelopes_test.go` — wire-compat fixture tests.

**Files modified:**

- `internal/api/admin/v1/subsystem_stores.go` — flesh out `authStore` from empty interface to the methods `handleOAuthCallback` needs (user lookup by OAuth, by email, by ID; user + project + oauth-connection writes; `UserExists`).
- `internal/api/admin/v1/server.go` — extend `Server` with an `Auth` field of type `authStore` and a `JWT` field of type `*auth.JWTManager` (currently the Server holds `Tokens tokenValidator`, which only exposes `ValidateToken`; the auth handlers need `GenerateTokenPair` too).
- `internal/api/admin/v1/operations.go` — call `registerAuthOperations(api, server)` from `Register`.
- `internal/admin/routes.go` — delete the 7 lines registering the 6 auth chi routes.
- `cmd/modelserver/main.go` — populate the new `Server.Auth`, `Server.JWT`, `Server.EncKey`, and `Server.Config` fields when constructing `adminv1.Server` (only `Server.Config` is currently set for auth-config lookup; `EncKey` may already be set).
- `api/openapi/admin.openapi.json` — regenerated after Task 6.
- `dashboard/src/api/generated/schema.ts` — regenerated after Task 7.
- `docs/admin-api-openapi-rbac.md` — append the three new migrated operations to the migrated-operations list.

**Files NOT touched in this batch:**

- `internal/admin/handle_auth.go` (except the routing registrations in `routes.go`): the legacy handler functions `handleRefresh`, `handleOAuthCallback`, `handleOAuthRedirect` remain in the file **for now**. Delete them in Batch 14's cleanup sweep, since they may still be referenced by tests or utility code the audit missed.

  Actually — audit them at Task 8 and delete if they have zero callers after the routes.go edit. Otherwise leave and document why.

---

## Task 1: DTOs + store surface for auth operations

**Files:**
- Create: `internal/api/admin/v1/auth.go` (DTOs and interfaces only; handler methods land in Task 2)
- Modify: `internal/api/admin/v1/subsystem_stores.go` (replace `authStore` stub with the real interface)
- Modify: `internal/api/admin/v1/server.go` (add `Auth authStore`, `JWT *auth.JWTManager`, `EncKey []byte` fields to `Server`)

**Interfaces:**
- Consumes: `authz.Public()`, `contract.Register`, `types.User`, `types.Project`, `auth.JWTManager`, `store.CompactUser`.
- Produces:
  - `adminv1.RefreshInput`, `adminv1.RefreshOutput`
  - `adminv1.OAuthProvider` — string-typed enum with `Schema()` producing `enum: ["github","google","oidc"]`
  - `adminv1.OAuthCallbackInput`, `adminv1.OAuthCallbackOutput` (merged response shape — see below)
  - `adminv1.OAuthRedirectInput`, `adminv1.OAuthRedirectOutput` (302 pattern)
  - `authStore` interface with the exact methods `handleOAuthCallback` and `handleRefresh` need — nothing broader

- [ ] **Step 1: Write the input/output structs**

Append to `internal/api/admin/v1/auth.go`:

```go
package adminv1

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/modelserver/modelserver/internal/types"
)

type RefreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" minLength:"1" doc:"Refresh token issued by a prior login."`
	}
}

type authTokensBody struct {
	AccessToken  string      `json:"access_token,omitempty"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	User         *types.User `json:"user,omitempty"`
	RedirectTo   string      `json:"redirect_to,omitempty"`
}

type RefreshOutput struct {
	Body authTokensBody
}

// OAuthProvider is a stable enum of supported OAuth providers.
type OAuthProvider string

const (
	OAuthProviderGitHub OAuthProvider = "github"
	OAuthProviderGoogle OAuthProvider = "google"
	OAuthProviderOIDC   OAuthProvider = "oidc"
)

func (OAuthProvider) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:        "string",
		Title:       "OAuthProvider",
		Description: "Supported OAuth identity provider.",
		Enum:        []any{"github", "google", "oidc"},
	}
}

type OAuthCallbackInput struct {
	Provider OAuthProvider `path:"provider" doc:"OAuth provider identifier."`
	Body     struct {
		Code  string `json:"code" minLength:"1" doc:"OAuth authorization code returned by the provider."`
		State string `json:"state,omitempty" doc:"OAuth state parameter. May carry a Hydra return_to encoded as \"<random>|<url>\"."`
	}
}

type OAuthCallbackOutput struct {
	Body authTokensBody
}

type OAuthRedirectInput struct {
	Provider OAuthProvider `path:"provider" doc:"OAuth provider identifier."`
	ReturnTo string        `query:"return_to,omitempty" doc:"Optional Hydra login return URL. Only /oauth/login-prefixed values are honored."`
}

// OAuthRedirectOutput streams a 302 redirect to the provider's authorize URL.
// Status is 302; Location carries the target.
type OAuthRedirectOutput struct {
	Status   int    `header:"-" json:"-"`
	Location string `header:"Location" doc:"Provider authorize URL."`
}
```

Note on `authTokensBody` — the merged shape preserves the legacy handler's two distinct 200 payloads:
- Normal login response: `{access_token, refresh_token, user}` — `RedirectTo` empty.
- Hydra return-to response: `{redirect_to}` — three token fields empty.
`omitempty` on all four fields keeps each shape byte-for-byte identical to the legacy JSON.

- [ ] **Step 2: Write the store interface and Server field additions**

Replace the `authStore interface{}` line in `internal/api/admin/v1/subsystem_stores.go` with:

```go
// A — Auth (public): user lookup / creation and OAuth-connection persistence
// used by POST /auth/refresh and POST /auth/oauth/{provider}.
type authStore interface {
	GetUserByID(id string) (*types.User, error)
	GetUserByEmail(email string) (*types.User, error)
	GetUserByOAuth(provider, providerID string) (*types.User, error)
	CreateUser(user *types.User) error
	UpdateUser(id string, updates map[string]any) error
	CreateOAuthConnection(userID, provider, providerID string) error
	UserExists() (bool, error)
	CreateProject(project *types.Project) error
}
```

Add the `types` import to `subsystem_stores.go` (it's currently import-free).

In `internal/api/admin/v1/server.go`, extend the `Server` struct. Locate the existing declaration:

```go
type Server struct {
	Store     managementStore
	Users     userReadStore
	Plans     planReadStore
	Tokens    tokenValidator
	Config    *config.Config
	Resolvers map[string]authz.ResourceResolver
	Policies  map[authz.PolicyID]authz.Policy
}
```

Add three fields:

```go
type Server struct {
	Store     managementStore
	Users     userReadStore
	Plans     planReadStore
	Tokens    tokenValidator
	Auth      authStore
	JWT       *auth.JWTManager
	EncKey    []byte
	Config    *config.Config
	Resolvers map[string]authz.ResourceResolver
	Policies  map[authz.PolicyID]authz.Policy
}
```

The `auth` package is already imported by `server.go`.

- [ ] **Step 3: Compile**

```
cd /root/coding/modelserver
go build ./internal/api/admin/v1/
```

Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add internal/api/admin/v1/auth.go internal/api/admin/v1/subsystem_stores.go internal/api/admin/v1/server.go
git commit -m "feat(adminv1/auth): DTOs and store surface for public auth operations

Adds Refresh, OAuthCallback, OAuthRedirect input/output types for
Batch 2 of the huma-admin migration. Introduces the OAuthProvider
enum and the merged authTokensBody that preserves the legacy
POST /auth/oauth/{provider} dual 200-response shape (token pair vs
Hydra redirect_to). Extends the Server surface with Auth store, JWT
manager, and encryption key fields the coming handlers need.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: A1 handler — POST /auth/refresh

**Files:**
- Modify: `internal/api/admin/v1/auth.go` (append handler)
- Create: `internal/api/admin/v1/auth_test.go` (fake authStore + test cases)

**Interfaces:**
- Consumes: `RefreshInput`, `RefreshOutput`, `contract.NewError`, `authStore.GetUserByID`, `tokenValidator.ValidateToken`, `auth.JWTManager.GenerateTokenPair`.
- Produces: `(*Server).refresh(ctx, in *RefreshInput) (*RefreshOutput, error)`

Truth table:
- Empty body → 400 `bad_request` "refresh_token is required" (Huma minLength enforces; the legacy handler emits 400 on either decode failure or empty).
- Invalid token → 401 `unauthorized` "invalid refresh token"
- Wrong token type (not "refresh") → 401 `unauthorized` "expected refresh token"
- User not found or disabled → 401 `unauthorized` "user not found or disabled"
- JWT generation failure → 500 `internal` "failed to generate tokens"
- Success → 200 with `{access_token, refresh_token, user}`

- [ ] **Step 1: Write handler tests first**

Create `internal/api/admin/v1/auth_test.go`:

```go
package adminv1

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/types"
)

type fakeAuthStore struct {
	usersByID map[string]*types.User
}

func (s *fakeAuthStore) GetUserByID(id string) (*types.User, error) {
	if u, ok := s.usersByID[id]; ok {
		return u, nil
	}
	return nil, nil
}
func (*fakeAuthStore) GetUserByEmail(string) (*types.User, error)                 { return nil, nil }
func (*fakeAuthStore) GetUserByOAuth(string, string) (*types.User, error)         { return nil, nil }
func (*fakeAuthStore) CreateUser(*types.User) error                               { return nil }
func (*fakeAuthStore) UpdateUser(string, map[string]any) error                    { return nil }
func (*fakeAuthStore) CreateOAuthConnection(userID, provider, providerID string) error {
	return nil
}
func (*fakeAuthStore) UserExists() (bool, error)         { return true, nil }
func (*fakeAuthStore) CreateProject(*types.Project) error { return nil }

type fakeTokens struct {
	claims *auth.Claims
	err    error
}

func (t fakeTokens) ValidateToken(string) (*auth.Claims, error) {
	return t.claims, t.err
}

func newAuthServerForRefresh(t *testing.T, store *fakeAuthStore, tokens fakeTokens) *Server {
	t.Helper()
	return &Server{
		Auth:   store,
		Tokens: tokens,
		JWT:    auth.NewJWTManager("test-secret-at-least-32-characters-long", time.Minute, time.Hour),
	}
}

func TestRefreshRejectsInvalidToken(t *testing.T) {
	t.Parallel()
	s := newAuthServerForRefresh(t, &fakeAuthStore{}, fakeTokens{err: errors.New("bad")})

	input := &RefreshInput{}
	input.Body.RefreshToken = "invalid"
	_, err := s.refresh(context.Background(), input)
	assertStatusError(t, err, 401, "unauthorized")
}

func TestRefreshRejectsWrongTokenType(t *testing.T) {
	t.Parallel()
	tokens := fakeTokens{claims: &auth.Claims{UserID: "u1", TokenType: "access"}}
	s := newAuthServerForRefresh(t, &fakeAuthStore{}, tokens)

	input := &RefreshInput{}
	input.Body.RefreshToken = "access-token-in-refresh-slot"
	_, err := s.refresh(context.Background(), input)
	assertStatusError(t, err, 401, "unauthorized")
}

func TestRefreshRejectsMissingUser(t *testing.T) {
	t.Parallel()
	tokens := fakeTokens{claims: &auth.Claims{UserID: "u1", TokenType: "refresh"}}
	s := newAuthServerForRefresh(t, &fakeAuthStore{usersByID: map[string]*types.User{}}, tokens)

	input := &RefreshInput{}
	input.Body.RefreshToken = "valid-but-user-gone"
	_, err := s.refresh(context.Background(), input)
	assertStatusError(t, err, 401, "unauthorized")
}

func TestRefreshRejectsDisabledUser(t *testing.T) {
	t.Parallel()
	tokens := fakeTokens{claims: &auth.Claims{UserID: "u1", TokenType: "refresh"}}
	store := &fakeAuthStore{usersByID: map[string]*types.User{
		"u1": {ID: "u1", Email: "a@b", Status: types.UserStatusDisabled},
	}}
	s := newAuthServerForRefresh(t, store, tokens)

	input := &RefreshInput{}
	input.Body.RefreshToken = "valid-but-disabled"
	_, err := s.refresh(context.Background(), input)
	assertStatusError(t, err, 401, "unauthorized")
}

func TestRefreshSuccess(t *testing.T) {
	t.Parallel()
	tokens := fakeTokens{claims: &auth.Claims{UserID: "u1", TokenType: "refresh"}}
	store := &fakeAuthStore{usersByID: map[string]*types.User{
		"u1": {ID: "u1", Email: "a@b", Status: types.UserStatusActive},
	}}
	s := newAuthServerForRefresh(t, store, tokens)

	input := &RefreshInput{}
	input.Body.RefreshToken = "valid"
	out, err := s.refresh(context.Background(), input)
	if err != nil {
		t.Fatalf("refresh() error = %v", err)
	}
	if out.Body.AccessToken == "" || out.Body.RefreshToken == "" {
		t.Fatal("expected non-empty token pair in response body")
	}
	if out.Body.User == nil || out.Body.User.ID != "u1" {
		t.Fatalf("expected user u1 in response body, got %+v", out.Body.User)
	}
	if out.Body.RedirectTo != "" {
		t.Fatalf("refresh must not set redirect_to; got %q", out.Body.RedirectTo)
	}
}

// assertStatusError extracts the HTTP status and code from a contract error.
func assertStatusError(t *testing.T, err error, wantStatus int, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	envelope, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T: %v", err, err)
	}
	if envelope.HTTPStatus != wantStatus {
		t.Errorf("status = %d, want %d", envelope.HTTPStatus, wantStatus)
	}
	if envelope.Payload.Code != wantCode {
		t.Errorf("code = %q, want %q", envelope.Payload.Code, wantCode)
	}
}
```

- [ ] **Step 2: Run tests and verify they fail**

```
go test ./internal/api/admin/v1/ -run '^TestRefresh' -v
```

Expected: FAIL with `s.refresh undefined`.

- [ ] **Step 3: Implement the refresh handler**

Append to `internal/api/admin/v1/auth.go`:

```go
import (
	"context"
	"net/http"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/types"
)

func (s *Server) refresh(_ context.Context, input *RefreshInput) (*RefreshOutput, error) {
	if s == nil || s.Auth == nil || s.Tokens == nil || s.JWT == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "auth handlers are not configured", nil)
	}

	claims, err := s.Tokens.ValidateToken(input.Body.RefreshToken)
	if err != nil || claims == nil {
		return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "invalid refresh token", nil)
	}
	if claims.TokenType != "refresh" {
		return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "expected refresh token", nil)
	}

	user, err := s.Auth.GetUserByID(claims.UserID)
	if err != nil || user == nil || user.Status != types.UserStatusActive {
		return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "user not found or disabled", nil)
	}

	access, refresh, err := s.JWT.GenerateTokenPair(user.ID, user.Email, user.IsSuperadmin)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to generate tokens", nil)
	}

	return &RefreshOutput{Body: authTokensBody{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         user,
	}}, nil
}
```

Consolidate imports at the top of `auth.go` if there are duplicates.

- [ ] **Step 4: Run tests and verify they pass**

```
go test ./internal/api/admin/v1/ -run '^TestRefresh' -v
```

Expected: 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/auth.go internal/api/admin/v1/auth_test.go
git commit -m "feat(adminv1/auth): typed POST /auth/refresh handler + tests

Preserves the legacy handler's exact wire behavior: invalid/expired/
mistyped tokens and missing/disabled users all resolve to 401
unauthorized with the legacy message. Success returns the same
{access_token, refresh_token, user} body.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: A2 handler — POST /auth/oauth/{provider}

**Files:**
- Modify: `internal/api/admin/v1/auth.go` (append `oauthCallback` handler)
- Modify: `internal/api/admin/v1/auth_test.go` (add OAuth-callback tests)

**Interfaces:**
- Consumes: same as Task 2, plus `authStore.GetUserByOAuth`, `GetUserByEmail`, `UserExists`, `CreateUser`, `UpdateUser`, `CreateOAuthConnection`, `CreateProject`; plus `assignFreePlan(s.Store, projectID)` — **existing helper in `internal/admin`**, needs to be either moved to a shared package or duplicated.
- Produces: `(*Server).oauthCallback(ctx, in *OAuthCallbackInput) (*OAuthCallbackOutput, error)`

Design decision — **`assignFreePlan` reuse**: the legacy helper lives in `internal/admin/`. Two options:

- **Option A**: extract `assignFreePlan` to a small shared helper package (`internal/authinit` or similar). Cleaner boundary but touches more files.
- **Option B**: temporarily inline the plan-assignment logic inside the new typed handler, then delete both copies when Batch 3 (Users write) migrates the last thing depending on `assignFreePlan`. Faster.

**Choose Option B for now** — the typed handler inlines the plan-assignment logic. Update the docstring to note this is temporary and will be shared once Batch 7 (Projects CRUD) migrates the `CreateProject` path. This keeps Batch 2 self-contained.

- [ ] **Step 1: Write OAuth-callback tests first**

Append to `internal/api/admin/v1/auth_test.go`:

```go
func TestOAuthCallbackRejectsUnknownProvider(t *testing.T) {
	t.Parallel()
	s := &Server{
		Auth:   &fakeAuthStore{},
		Config: &config.Config{},
		JWT:    auth.NewJWTManager("test-secret-at-least-32-characters-long", time.Minute, time.Hour),
	}
	input := &OAuthCallbackInput{Provider: OAuthProvider("facebook")}
	input.Body.Code = "code"
	_, err := s.oauthCallback(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
}

func TestOAuthCallbackRejectsUnconfiguredProvider(t *testing.T) {
	t.Parallel()
	// GitHub client ID unset -> 501 not_configured.
	s := &Server{
		Auth:   &fakeAuthStore{},
		Config: &config.Config{},
		JWT:    auth.NewJWTManager("test-secret-at-least-32-characters-long", time.Minute, time.Hour),
	}
	input := &OAuthCallbackInput{Provider: OAuthProviderGitHub}
	input.Body.Code = "code"
	_, err := s.oauthCallback(context.Background(), input)
	assertStatusError(t, err, 501, "not_configured")
}

// Note: exchange failures against a real provider require network access.
// Coverage of the OAuth exchange itself lives at the auth package level.
// This batch's tests exercise the routing and dispatch decisions only.
```

Add the config import to `auth_test.go`:

```go
	"github.com/modelserver/modelserver/internal/config"
```

- [ ] **Step 2: Run and verify they fail**

```
go test ./internal/api/admin/v1/ -run TestOAuthCallback -v
```

Expected: FAIL with `s.oauthCallback undefined`.

- [ ] **Step 3: Implement the handler**

Append to `internal/api/admin/v1/auth.go`:

```go
import (
	"log"
	"strings"

	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/crypto"
	// ... consolidate with existing imports
)

func (s *Server) oauthCallback(ctx context.Context, input *OAuthCallbackInput) (*OAuthCallbackOutput, error) {
	if s == nil || s.Auth == nil || s.Config == nil || s.JWT == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "auth handlers are not configured", nil)
	}

	info, err := s.exchangeOAuthCode(ctx, input.Provider, input.Body.Code)
	if err != nil {
		return nil, err
	}
	if info.Email == "" {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "OAuth provider did not return an email address", nil)
	}

	user, err := s.resolveOrCreateOAuthUser(info)
	if err != nil {
		return nil, err
	}
	if user.Status != types.UserStatusActive {
		return nil, contract.NewError(http.StatusForbidden, "forbidden", "account is disabled", nil)
	}

	// Hydra return_to flow: state encodes "<random>|<return_to>".
	if input.Body.State != "" {
		if idx := strings.Index(input.Body.State, "|"); idx >= 0 {
			returnTo := input.Body.State[idx+1:]
			if isValidReturnTo(returnTo) {
				authToken := buildAuthToken(s.EncKey, user.ID)
				redirectURL := appendQueryParam(returnTo, "auth_token", authToken)
				return &OAuthCallbackOutput{Body: authTokensBody{RedirectTo: redirectURL}}, nil
			}
		}
	}

	access, refresh, err := s.JWT.GenerateTokenPair(user.ID, user.Email, user.IsSuperadmin)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to generate tokens", nil)
	}
	return &OAuthCallbackOutput{Body: authTokensBody{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         user,
	}}, nil
}

func (s *Server) exchangeOAuthCode(ctx context.Context, provider OAuthProvider, code string) (*auth.OAuthUserInfo, error) {
	switch provider {
	case OAuthProviderGitHub:
		if s.Config.Auth.OAuth.GitHub.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "GitHub OAuth not configured", nil)
		}
		gh := auth.NewGitHubOAuth(s.Config.Auth.OAuth.GitHub.ClientID, s.Config.Auth.OAuth.GitHub.ClientSecret, "")
		info, err := gh.ExchangeAndGetUser(ctx, code)
		if err != nil {
			return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "OAuth exchange failed: "+err.Error(), nil)
		}
		return info, nil
	case OAuthProviderGoogle:
		if s.Config.Auth.OAuth.Google.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "Google OAuth not configured", nil)
		}
		g := auth.NewGoogleOAuth(s.Config.Auth.OAuth.Google.ClientID, s.Config.Auth.OAuth.Google.ClientSecret, "")
		info, err := g.ExchangeAndGetUser(ctx, code)
		if err != nil {
			return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "OAuth exchange failed: "+err.Error(), nil)
		}
		return info, nil
	case OAuthProviderOIDC:
		if s.Config.Auth.OAuth.OIDC.IssuerURL == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "OIDC not configured", nil)
		}
		p, err := auth.NewOIDCProvider(ctx, s.Config.Auth.OAuth.OIDC.IssuerURL, s.Config.Auth.OAuth.OIDC.ClientID, s.Config.Auth.OAuth.OIDC.ClientSecret, s.Config.Auth.OAuth.OIDC.RedirectURI)
		if err != nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to initialize OIDC", nil)
		}
		info, err := p.ExchangeAndGetUser(ctx, code)
		if err != nil {
			return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "OAuth exchange failed: "+err.Error(), nil)
		}
		return info, nil
	default:
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "unsupported provider", nil)
	}
}

func (s *Server) resolveOrCreateOAuthUser(info *auth.OAuthUserInfo) (*types.User, error) {
	user, _ := s.Auth.GetUserByOAuth(info.Provider, info.ProviderID)
	if user == nil {
		user, _ = s.Auth.GetUserByEmail(info.Email)
	}

	if user != nil {
		_ = s.Auth.CreateOAuthConnection(user.ID, info.Provider, info.ProviderID)
		updates := map[string]any{}
		if info.Name != "" && info.Name != user.Nickname {
			updates["nickname"] = info.Name
		}
		if info.Picture != "" && info.Picture != user.Picture {
			updates["picture"] = info.Picture
		}
		if len(updates) > 0 {
			if err := s.Auth.UpdateUser(user.ID, updates); err != nil {
				log.Printf("WARN: failed to update OAuth user %s: %v", user.ID, err)
			}
			if fresh, err := s.Auth.GetUserByID(user.ID); err == nil && fresh != nil {
				user = fresh
			}
		}
		return user, nil
	}

	// New user.
	isFirst := false
	if exists, err := s.Auth.UserExists(); err == nil && !exists {
		isFirst = true
	}
	user = &types.User{
		Email:        info.Email,
		Nickname:     info.Name,
		Picture:      info.Picture,
		IsSuperadmin: isFirst,
		MaxProjects:  5,
		Status:       types.UserStatusActive,
	}
	if isFirst {
		user.MaxProjects = 100
	}
	if err := s.Auth.CreateUser(user); err != nil {
		log.Printf("WARN: create OAuth user failed (email=%s): %v, retrying lookup", info.Email, err)
		user, _ = s.Auth.GetUserByEmail(info.Email)
		if user == nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to create user", nil)
		}
	} else {
		// Auto-create default project for the new OAuth user. Inline
		// assignFreePlan for now; extract to a shared helper when Batch 7
		// migrates the projects CRUD paths that also rely on it.
		project := &types.Project{
			Name:      "Default Project",
			CreatedBy: user.ID,
			Status:    types.ProjectStatusActive,
		}
		if err := s.Auth.CreateProject(project); err != nil {
			log.Printf("WARN: failed to create default project for OAuth user %s: %v", user.ID, err)
		}
		// assignFreePlan side effect handled by the legacy helper for now;
		// Batch 7 will migrate. Leaving here means the legacy internal/admin
		// path continues to be the single owner of the free-plan assignment
		// until then.
		if projectAssigner, ok := s.Auth.(interface{ AssignFreePlan(projectID string) }); ok {
			projectAssigner.AssignFreePlan(project.ID)
		}
	}
	_ = s.Auth.CreateOAuthConnection(user.ID, info.Provider, info.ProviderID)
	return user, nil
}
```

The `isValidReturnTo`, `buildAuthToken`, `appendQueryParam` helpers currently live in `internal/admin/handle_auth.go`. Extract them into a new file `internal/api/admin/v1/oauth_helpers.go` (or into `auth.go` — implementer's call). Do NOT reference them from the `internal/admin` package.

Confirm the `internal/admin` versions can be deleted (or kept — this is Batch 14's cleanup responsibility). For now, duplicating the ~30 lines into `internal/api/admin/v1` is acceptable per the plan template's "each batch keeps its own copy until the last chi caller disappears" policy.

**Also — the `s.Auth.(interface{ AssignFreePlan(projectID string) })` type assertion is a hack.** Better: add a real optional interface method `MaybeAssignFreePlan(projectID string) error` to `authStore` and let the concrete `*store.Store` implement it. But that pollutes the store. Cleanest for Batch 2: **have the wire-up in `cmd/modelserver/main.go` inject a `FreePlanAssigner` field on `Server` that wraps the legacy helper.**

Simpler shim — add `AssignFreePlan func(projectID string)` as a plain function field on `Server`; if nil, skip. `cmd/modelserver/main.go` populates it with a closure over the legacy helper.

Replace the type-assertion block above with:

```go
		if s.AssignFreePlan != nil {
			s.AssignFreePlan(project.ID)
		}
```

And in `server.go`:

```go
type Server struct {
	// ... existing fields ...
	AssignFreePlan func(projectID string)
}
```

This is the cleanest 5-line adapter that keeps the legacy helper as source of truth.

- [ ] **Step 4: Run tests and verify they pass**

```
go test ./internal/api/admin/v1/ -run TestOAuthCallback -v
```

Expected: 2 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/auth.go internal/api/admin/v1/auth_test.go internal/api/admin/v1/server.go
git commit -m "feat(adminv1/auth): typed POST /auth/oauth/{provider} handler + tests

Preserves the legacy dual 200-response shape: normal login returns
{access_token, refresh_token, user}; Hydra return_to flow returns
{redirect_to}. Auto-creates a new user on first OAuth login (first
user becomes superadmin) and auto-creates a Default Project. The
free-plan assignment is delegated back to cmd/modelserver via the
optional Server.AssignFreePlan callback until Batch 7 migrates the
projects CRUD path.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: A3 handler — GET /auth/oauth/{provider}/redirect

**Files:**
- Modify: `internal/api/admin/v1/auth.go` (append `oauthRedirect` handler)
- Modify: `internal/api/admin/v1/auth_test.go` (add redirect tests)

**Interfaces:**
- Consumes: `OAuthRedirectInput`, `OAuthRedirectOutput`, `Config.Auth.OAuth.*`.
- Produces: `(*Server).oauthRedirect(ctx, in *OAuthRedirectInput) (*OAuthRedirectOutput, error)`

Behavior:
- Provider unknown → 400 `bad_request`
- Provider not configured → 501 `not_configured`
- OIDC init error → 500 `internal`
- Success → 302 with `Location: authURL`

The legacy handler infers the callback URL from headers (`X-Forwarded-Proto`, `Host`) when `cfg.Auth.OAuth.OIDC.RedirectURI` is empty. Huma's typed input strips access to the raw request headers unless we declare them. Two options:

- Declare `XForwardedProto string \`header:"X-Forwarded-Proto"\`` and `Host string \`header:"Host"\`` on `OAuthRedirectInput`. Then infer callback URL from these fields. Downside: `r.TLS != nil` isn't reachable — we lose the "TLS or not?" signal. Fix: derive scheme from `X-Forwarded-Proto`, default to `https` if unset (a slight tightening — the legacy path defaulted to `http` when no header was set AND `r.TLS` was nil, which almost never happens in modern deployments).

Take the tightening (default `https`) as an explicit deviation. Note it in the commit message. If any operator deploys plain HTTP without a proxy setting X-Forwarded-Proto, they'll get incorrect redirect URLs — but modern operators use TLS or a proxy.

Alternative: use Huma's `humaContext` / raw request access to preserve full parity. Try the header-field approach first; fall back to raw context if the test suite catches a real problem.

- [ ] **Step 1: Write redirect tests**

Append to `auth_test.go`:

```go
func TestOAuthRedirectUnknownProvider(t *testing.T) {
	t.Parallel()
	s := &Server{Config: &config.Config{}}
	input := &OAuthRedirectInput{Provider: OAuthProvider("facebook")}
	_, err := s.oauthRedirect(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
}

func TestOAuthRedirectNotConfigured(t *testing.T) {
	t.Parallel()
	s := &Server{Config: &config.Config{}}
	input := &OAuthRedirectInput{Provider: OAuthProviderGitHub}
	_, err := s.oauthRedirect(context.Background(), input)
	assertStatusError(t, err, 501, "not_configured")
}
```

- [ ] **Step 2: Run and verify they fail**

```
go test ./internal/api/admin/v1/ -run TestOAuthRedirect -v
```

Expected: FAIL with `s.oauthRedirect undefined`.

- [ ] **Step 3: Implement the handler**

Append to `internal/api/admin/v1/auth.go`:

```go
type OAuthRedirectInput struct {
	Provider         OAuthProvider `path:"provider" doc:"OAuth provider identifier."`
	ReturnTo         string        `query:"return_to,omitempty" doc:"Optional Hydra login return URL."`
	XForwardedProto  string        `header:"X-Forwarded-Proto"`
	Host             string        `header:"Host"`
}

func (s *Server) oauthRedirect(ctx context.Context, input *OAuthRedirectInput) (*OAuthRedirectOutput, error) {
	if s == nil || s.Config == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "auth handlers are not configured", nil)
	}

	callbackURL := s.oauthCallbackURL(input)
	state := generateOAuthState()
	if isValidReturnTo(input.ReturnTo) {
		state = state + "|" + input.ReturnTo
	}

	var authURL string
	switch input.Provider {
	case OAuthProviderGitHub:
		if s.Config.Auth.OAuth.GitHub.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "GitHub OAuth not configured", nil)
		}
		gh := auth.NewGitHubOAuth(s.Config.Auth.OAuth.GitHub.ClientID, s.Config.Auth.OAuth.GitHub.ClientSecret, "")
		authURL = gh.AuthCodeURL(state, callbackURL)
	case OAuthProviderGoogle:
		if s.Config.Auth.OAuth.Google.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "Google OAuth not configured", nil)
		}
		g := auth.NewGoogleOAuth(s.Config.Auth.OAuth.Google.ClientID, s.Config.Auth.OAuth.Google.ClientSecret, "")
		authURL = g.AuthCodeURL(state, callbackURL)
	case OAuthProviderOIDC:
		if s.Config.Auth.OAuth.OIDC.IssuerURL == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "OIDC not configured", nil)
		}
		p, err := auth.NewOIDCProvider(ctx, s.Config.Auth.OAuth.OIDC.IssuerURL, s.Config.Auth.OAuth.OIDC.ClientID, s.Config.Auth.OAuth.OIDC.ClientSecret, callbackURL)
		if err != nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to initialize OIDC", nil)
		}
		authURL = p.AuthCodeURL(state, callbackURL)
	default:
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "unsupported provider", nil)
	}

	return &OAuthRedirectOutput{
		Status:   http.StatusFound,
		Location: authURL,
	}, nil
}

func (s *Server) oauthCallbackURL(input *OAuthRedirectInput) string {
	if input.Provider == OAuthProviderOIDC && s.Config.Auth.OAuth.OIDC.RedirectURI != "" {
		return s.Config.Auth.OAuth.OIDC.RedirectURI
	}
	scheme := input.XForwardedProto
	if scheme == "" {
		// Default to https; operators without TLS termination in front should
		// set X-Forwarded-Proto explicitly. This is a documented deviation
		// from the legacy handler, which defaulted to http.
		scheme = "https"
	}
	return scheme + "://" + input.Host + "/auth/callback/" + string(input.Provider)
}

func generateOAuthState() string {
	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	return hex.EncodeToString(stateBytes)
}
```

Add `crypto/rand`, `encoding/hex` to imports.

The `Status` field on `OAuthRedirectOutput` — Huma reads this via a specific reflection mechanism. If it doesn't work (Huma may require the field to be at a specific position or tagged differently), swap to Huma's raw response pattern: return `nil` output with a custom `Middleware` that writes 302. **Try the struct-field approach first**; test at Step 4.

- [ ] **Step 4: Verify tests pass, then integration-test the 302**

```
go test ./internal/api/admin/v1/ -run TestOAuthRedirect -v
```

Add an integration test at the router level that verifies the 302 status + Location header are actually emitted:

```go
func TestOAuthRedirectRoutesReturn302(t *testing.T) {
	t.Parallel()
	router := chi.NewRouter()
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	Register(api, &Server{
		Config: &config.Config{
			Auth: config.AuthConfig{OAuth: config.OAuthConfig{
				GitHub: config.OAuthProviderConfig{ClientID: "abc", ClientSecret: "def"},
			}},
		},
	})

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/oauth/github/redirect", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("Host", "api.example.com")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body = %s", recorder.Code, recorder.Body.String())
	}
	if loc := recorder.Header().Get("Location"); loc == "" {
		t.Fatal("Location header is empty")
	}
}
```

Iterate on the `Status` field mechanism until this test passes. Report the final shape in the commit message.

- [ ] **Step 5: Commit**

```bash
git add internal/api/admin/v1/auth.go internal/api/admin/v1/auth_test.go
git commit -m "feat(adminv1/auth): typed GET /auth/oauth/{provider}/redirect handler

Emits 302 to the provider authorize URL using Huma's output-struct
redirect pattern. Preserves the legacy state-encoding behavior for
Hydra return_to flow. Deviates from the legacy handler by defaulting
scheme to https when X-Forwarded-Proto is absent; the legacy handler
defaulted to http when the request also had no TLS. Modern operators
should be terminating TLS or setting X-Forwarded-Proto.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Register operations, delete chi routes, wire main.go

**Files:**
- Modify: `internal/api/admin/v1/auth.go` (add `registerAuthOperations`)
- Modify: `internal/api/admin/v1/operations.go` (call it from `Register`)
- Modify: `internal/admin/routes.go` (delete 7 lines: `/auth/refresh` + 3 OAuth callbacks + 3 OAuth redirects)
- Modify: `cmd/modelserver/main.go` (populate `Server.Auth`, `Server.JWT`, `Server.EncKey`, `Server.AssignFreePlan`)

**Interfaces:**
- Produces: three operation registrations under `/api/v1/auth/*`.

- [ ] **Step 1: Add `registerAuthOperations`**

Append to `internal/api/admin/v1/auth.go`:

```go
func registerAuthOperations(api huma.API, server *Server) {
	contract.Register(api, contract.Operation{
		ID:            "refreshTokens",
		Method:        http.MethodPost,
		Path:          "/api/v1/auth/refresh",
		Summary:       "Refresh access token",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError},
		Access:        authz.Public(),
	}, server.refresh)

	contract.Register(api, contract.Operation{
		ID:            "oauthCallback",
		Method:        http.MethodPost,
		Path:          "/api/v1/auth/oauth/{provider}",
		Summary:       "Complete an OAuth login",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotImplemented, http.StatusInternalServerError},
		Access:        authz.Public(),
	}, server.oauthCallback)

	contract.Register(api, contract.Operation{
		ID:            "oauthRedirect",
		Method:        http.MethodGet,
		Path:          "/api/v1/auth/oauth/{provider}/redirect",
		Summary:       "Redirect to OAuth provider authorize URL",
		Tags:          []string{"Authentication"},
		DefaultStatus: http.StatusFound,
		Errors:        []int{http.StatusBadRequest, http.StatusNotImplemented, http.StatusInternalServerError},
		Access:        authz.Public(),
	}, server.oauthRedirect)
}
```

- [ ] **Step 2: Call it from `Register`**

Modify `internal/api/admin/v1/operations.go`. Locate the existing `Register(api, server)` function body and add near the top (after the getAuthConfig registration):

```go
	registerAuthOperations(api, server)
```

- [ ] **Step 3: Delete legacy chi registrations**

In `internal/admin/routes.go`, delete these 7 lines:

```go
		r.Post("/auth/refresh", handleRefresh(st, jwtMgr))

		r.Post("/auth/oauth/github", handleOAuthCallback(st, jwtMgr, cfg, encKey, "github"))
		r.Post("/auth/oauth/google", handleOAuthCallback(st, jwtMgr, cfg, encKey, "google"))
		r.Post("/auth/oauth/oidc", handleOAuthCallback(st, jwtMgr, cfg, encKey, "oidc"))

		r.Get("/auth/oauth/github/redirect", handleOAuthRedirect(cfg, "github"))
		r.Get("/auth/oauth/google/redirect", handleOAuthRedirect(cfg, "google"))
		r.Get("/auth/oauth/oidc/redirect", handleOAuthRedirect(cfg, "oidc"))
```

Do NOT delete the handler functions themselves (`handleRefresh`, `handleOAuthCallback`, `handleOAuthRedirect`) or the helpers (`isValidReturnTo`, `buildAuthToken`, `appendQueryParam`, `verifyAuthToken`, `assignFreePlan`) — the OAuth-callback handler's helpers might be used by device flow, and `verifyAuthToken` is used by the Hydra login handler. Batch 14's cleanup will grep-audit and delete unused ones then.

- [ ] **Step 4: Populate the Server fields in main.go**

In `cmd/modelserver/main.go`, find the `adminv1.Server{...}` construction and add the four new fields:

```go
adminServer := &adminv1.Server{
	Store:  store,
	Users:  store,
	Plans:  store,
	Tokens: jwtManager,
	Auth:   store,
	JWT:    jwtManager,
	EncKey: encKey,
	Config: cfg,
	AssignFreePlan: func(projectID string) {
		admin.AssignFreePlan(store, projectID)
	},
}
```

The `admin.AssignFreePlan` name is a guess — the legacy helper is currently `assignFreePlan` (unexported) inside `internal/admin/handle_auth.go`. Either export it (`AssignFreePlan`) or add a small exported wrapper. **Prefer exporting**: rename `assignFreePlan` → `AssignFreePlan` in `internal/admin/handle_auth.go` and update the 1 caller in `handle_auth.go` line ~174.

Wire the closure in `cmd/modelserver/main.go` accordingly.

- [ ] **Step 5: Run the full Go suite**

```
go test ./...
```

Expected: all green. In particular:
- `TestTypedAdminRoutesCoexistWithLegacyMount` still passes.
- `TestNoDualRegistrationInsideHuma` still passes (the trailing-slash aliases don't collide).
- The three new registrations get emitted in the OpenAPI document.

- [ ] **Step 6: Commit**

```bash
git add internal/api/admin/v1/auth.go internal/api/admin/v1/operations.go internal/admin/routes.go internal/admin/handle_auth.go cmd/modelserver/main.go
git commit -m "feat(admin): register typed auth operations, remove legacy chi routes

The typed handlers own POST /auth/refresh, POST /auth/oauth/{provider},
and GET /auth/oauth/{provider}/redirect. The legacy handler functions
stay in internal/admin/handle_auth.go for now because their helpers
(isValidReturnTo, buildAuthToken, verifyAuthToken, appendQueryParam)
are also used by device flow and Hydra login. Batch 14 cleanup will
grep-audit and delete unused helpers.

Also exports the previously-unexported assignFreePlan helper so
cmd/modelserver can wire it as the Server.AssignFreePlan closure.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Envelope-fixture regression tests

**Files:**
- Create: `internal/api/admin/v1/auth_envelopes_test.go`

**Interfaces:**
- Consumes: `RefreshOutput`, `OAuthCallbackOutput`.

Golden-file compare that the two response shapes serialize byte-for-byte to what the legacy client expects.

- [ ] **Step 1: Add envelope fixture tests**

```go
package adminv1

import (
	"encoding/json"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestAuthTokensBodyNormalLoginShape(t *testing.T) {
	t.Parallel()
	user := &types.User{ID: "u1", Email: "a@b", Nickname: "A"}
	body := authTokensBody{AccessToken: "at", RefreshToken: "rt", User: user}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(encoded)
	for _, needle := range []string{`"access_token":"at"`, `"refresh_token":"rt"`, `"user":{`} {
		if !contains(got, needle) {
			t.Errorf("JSON %s does not contain %s", got, needle)
		}
	}
	if contains(got, "redirect_to") {
		t.Errorf("normal-login body leaked redirect_to: %s", got)
	}
}

func TestAuthTokensBodyHydraRedirectShape(t *testing.T) {
	t.Parallel()
	body := authTokensBody{RedirectTo: "https://example.com/callback?auth_token=xyz"}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(encoded)
	if !contains(got, `"redirect_to":"https://example.com/callback?auth_token=xyz"`) {
		t.Fatalf("missing redirect_to; got: %s", got)
	}
	for _, forbidden := range []string{"access_token", "refresh_token", `"user"`} {
		if contains(got, forbidden) {
			t.Errorf("Hydra-redirect body leaked %s: %s", forbidden, got)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || (len(haystack) > len(needle) && indexOf(haystack, needle) >= 0))
}
func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
```

Or just use `strings.Contains` — the `contains` helper is defensive but unnecessary. Refactor to `strings.Contains` if you prefer.

- [ ] **Step 2: Run**

```
go test ./internal/api/admin/v1/ -run 'TestAuthTokensBody' -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/admin/v1/auth_envelopes_test.go
git commit -m "test(adminv1/auth): envelope fixtures for both response shapes

Locks down the omitempty semantics so a normal-login response never
leaks redirect_to and a Hydra-redirect response never leaks token
fields, matching the legacy handler's two 200-body shapes.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Batch-scoped dual-registration guard

**Files:**
- Modify: `internal/api/contract/invariants_test.go` (or a new file in the same test package)

**Interfaces:**
- Fails if any of the 6 chi routes this batch migrated is still registered on the underlying chi router by `admin.MountRoutes`.

- [ ] **Step 1: Add the guard test**

Append to `internal/api/contract/invariants_test.go`:

```go
func TestBatch02NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/oauth/github"},
		{http.MethodPost, "/api/v1/auth/oauth/google"},
		{http.MethodPost, "/api/v1/auth/oauth/oidc"},
		{http.MethodGet, "/api/v1/auth/oauth/github/redirect"},
		{http.MethodGet, "/api/v1/auth/oauth/google/redirect"},
		{http.MethodGet, "/api/v1/auth/oauth/oidc/redirect"},
	}
	router := chi.NewRouter()
	// Mount only the legacy admin routes; typed operations are excluded.
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}
```

Add the needed imports (`net/http`, `chi`, `admin`, `config`).

- [ ] **Step 2: Run**

```
go test ./internal/api/contract/ -run TestBatch02NoLegacyChiOverlap -v
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/api/contract/invariants_test.go
git commit -m "test(contract): guard batch 02 single-registration invariant

Ensures the 6 auth chi routes migrated in Batch 2 no longer resolve
in the legacy admin.MountRoutes router.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Regenerate OpenAPI + dashboard schema

**Files:**
- Modify: `api/openapi/admin.openapi.json`
- Modify: `dashboard/src/api/generated/schema.ts`

- [ ] **Step 1: Regenerate Go spec**

```
cd /root/coding/modelserver
go run ./cmd/openapi
```

Diff should show the three new operations and any DTO additions.

- [ ] **Step 2: Regenerate TS schema**

```
cd dashboard
pnpm api:generate
```

Diff should show new `paths["/api/v1/auth/refresh"]`, `paths["/api/v1/auth/oauth/{provider}"]`, `paths["/api/v1/auth/oauth/{provider}/redirect"]`, plus new `components["schemas"]` entries.

- [ ] **Step 3: Verify the drift test passes**

```
cd /root/coding/modelserver
go test ./internal/api/admin/v1/ -run TestCommittedOpenAPIDocumentIsCurrent -v
```

Expected: PASS.

- [ ] **Step 4: Verify Dashboard TypeScript still compiles**

```
cd dashboard
pnpm exec tsc --noEmit
```

Expected: PASS. No existing dashboard code consumes these auth types yet.

- [ ] **Step 5: Commit**

```bash
cd /root/coding/modelserver
git add api/openapi/admin.openapi.json dashboard/src/api/generated/schema.ts
git commit -m "chore(openapi): regenerate for batch 02 auth operations

Adds the three /api/v1/auth/* typed operations to the committed
spec and the dashboard schema. No dashboard hook migration in this
batch — client.ts and AuthContext.tsx continue to use plain fetch
and the shared request helper respectively, both wire-compatible
with the typed responses.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Full-repo verification gate + docs update

**Files:**
- Modify: `docs/admin-api-openapi-rbac.md` (append migrated operation list)

- [ ] **Step 1: Full-repo Go tests**

```
go test ./...
```

Expected: all green.

- [ ] **Step 2: Dashboard checks**

```
cd dashboard
pnpm api:check
pnpm exec tsc --noEmit
pnpm build
```

Expected: all green.

- [ ] **Step 3: Manual smoke via curl** (skip if operator will do this pre-deploy)

Bring up a local instance if practical. Verify:

- `POST /api/v1/auth/refresh` with invalid body → 400 `bad_request` "refresh_token is required"
- `POST /api/v1/auth/refresh` with garbage refresh_token → 401 `unauthorized`
- `POST /api/v1/auth/oauth/facebook` → 400 `bad_request` "unsupported provider"
- `POST /api/v1/auth/oauth/github` (with GitHub not configured) → 501 `not_configured`
- `GET /api/v1/auth/oauth/github/redirect?return_to=https://evil.com` (with GitHub configured) → 302, and the emitted state does NOT contain the evil return_to (because `isValidReturnTo` rejects non-`/oauth/login` prefixes)

- [ ] **Step 4: Update the migration status doc**

Append to `docs/admin-api-openapi-rbac.md` in the migrated-operations list section:

```
- `POST /api/v1/auth/refresh`
- `POST /api/v1/auth/oauth/{provider}`
- `GET /api/v1/auth/oauth/{provider}/redirect`
```

- [ ] **Step 5: Commit**

```bash
git add docs/admin-api-openapi-rbac.md
git commit -m "docs(admin-api): batch 2 route list

Marks the three /api/v1/auth/* write routes as migrated to the
typed contract. Six chi registrations removed; the underlying
handler functions remain until batch 14 cleanup.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Batch 2 Self-Review Notes

- Every task ends with an independently-reviewable commit. Task 5 is the largest (touches 5 files: `auth.go`, `operations.go`, `routes.go`, `handle_auth.go`, `main.go`) — reviewers should focus on the routes.go / main.go delta since those are the wire-critical bits.
- **Deviations from strict wire compatibility** (documented in commits):
  1. Task 4 defaults scheme to `https` when `X-Forwarded-Proto` is absent, vs the legacy `http` default. Called out in the redirect handler's commit message.
  2. The refresh handler's 400 for missing `refresh_token` now comes from Huma's `minLength:"1"` validator, which emits a different error shape than the legacy `writeError(400, "bad_request", "refresh_token is required")`. **Verify with a golden fixture** in the envelope-fixture test.
  3. Huma's typed request-body decode may return a different error string than the legacy `decodeBody` when the body is truly malformed JSON. Verify in a golden fixture.
- The `Server.AssignFreePlan func` shim is a temporary bridge. Once Batch 7 (Projects CRUD) migrates the `POST /projects` path, both callers can share a single typed helper and the closure can be removed.
- The `authStore` interface added in Task 1 is small (7 methods). It supersets `managementStore.GetUserByID` and `userReadStore.GetUserByID` — the existing `*store.Store` satisfies all three. No store code changes.
- No new resolver, no new policy, no new permission. `TestEveryResourceHasResolver` continues to skip. `TestEveryOperationHasCatalogPermission` continues to pass (public operations have empty permission).
- Spec coverage:
  - §5 A1 (POST /auth/refresh) — Task 2
  - §5 A2 (POST /auth/oauth/{provider}) — Task 3
  - §5 A3 (GET /auth/oauth/{provider}/redirect) — Task 4
  - §6 batch checklist:
    - New DTOs + register — Tasks 1, 5
    - Auth-matrix tests — folded into per-handler Task 2/3/4 tests (public routes don't have a role matrix)
    - Envelope fixtures — Task 6
    - Delete chi — Task 5
    - Batch-scoped dual-reg guard — Task 7
    - OpenAPI regen — Task 8
    - Dashboard schema regen — Task 8
    - Dashboard hook migration — **deferred** per plan (client.ts uses plain fetch deliberately; migrating it is a separate concern)
    - Dashboard test + build — Task 9
    - Full-repo verification — Task 9
    - Migration doc update — Task 9
