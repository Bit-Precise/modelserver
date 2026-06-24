package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

// ----- test scaffolding -----

// fakeIdentityStore satisfies identityStore for tests without a database.
type fakeIdentityStore struct {
	users    map[string]*types.User
	euByProj map[string]*types.ExtraUsageSettings // key = projectID

	// Toggles to simulate transient DB failure.
	userErr error
	euErr   error
}

func (f *fakeIdentityStore) GetUserByID(id string) (*types.User, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	return f.users[id], nil
}

func (f *fakeIdentityStore) GetExtraUsageSettings(projectID string) (*types.ExtraUsageSettings, error) {
	if f.euErr != nil {
		return nil, f.euErr
	}
	return f.euByProj[projectID], nil
}

// authCtx returns a context populated with the values that AuthMiddleware
// would have written for either the API-key or OAuth path. When
// oauthRawToken is non-empty it also seeds the global introspect cache
// (read by handleTokenIntrospectionAuth in production) and registers a
// t.Cleanup to delete the entry so per-test seeded entries don't leak
// across packages or `go test` runs.
type authCtxOpts struct {
	apiKey        *types.APIKey
	project       *types.Project
	subscription  *types.Subscription
	oauthRawToken string
	oauthClientID string
}

func authCtx(t *testing.T, opts authCtxOpts) context.Context {
	t.Helper()
	ctx := context.WithValue(context.Background(), ctxAPIKey, opts.apiKey)
	ctx = context.WithValue(ctx, ctxProject, opts.project)
	if opts.subscription != nil {
		ctx = context.WithValue(ctx, ctxSubscription, opts.subscription)
	}
	if opts.oauthRawToken != "" {
		hash := sha256HexForTest(opts.oauthRawToken)
		setIntrospectCache(hash, &TokenIntrospectResult{
			Active:   true,
			ClientID: opts.oauthClientID,
		})
		t.Cleanup(func() { deleteIntrospectCache(hash) })
	}
	return ctx
}

// sha256HexForTest replicates the inline hash done by
// handleTokenIntrospectionAuth so the test can seed introspectCache with
// the same key the auth path would look up.
func sha256HexForTest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// apiKeyFixture / oauthSyntheticAPIKeyFixture / projectFixture / userFixture
// build minimal valid auth context values.
func apiKeyFixture() *types.APIKey {
	return &types.APIKey{
		ID:        "key_abc",
		ProjectID: "proj_test",
		CreatedBy: "user_alice",
		KeySuffix: "wxyz",
		Name:      "alice's key",
		Status:    types.APIKeyStatusActive,
	}
}

// oauthSyntheticAPIKeyFixture matches what handleTokenIntrospectionAuth
// builds: ID="" and Name=syntheticOAuthAPIKeyName. buildIdentity
// discriminates on this exact pair.
func oauthSyntheticAPIKeyFixture() *types.APIKey {
	return &types.APIKey{
		ID:        "",
		ProjectID: "proj_test",
		CreatedBy: "user_alice",
		Name:      syntheticOAuthAPIKeyName,
		Status:    types.APIKeyStatusActive,
	}
}

func projectFixture() *types.Project {
	return &types.Project{
		ID:     "proj_test",
		Name:   "Test Project",
		Status: types.ProjectStatusActive,
	}
}

func userFixture() *types.User {
	return &types.User{
		ID:        "user_alice",
		Email:     "alice@example.com",
		Nickname:  "Alice",
		Status:    types.UserStatusActive,
		CreatedAt: time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC),
	}
}

func activeSubFixture(planName string) *types.Subscription {
	return &types.Subscription{
		PlanName:  planName,
		Status:    types.SubscriptionStatusActive,
		StartsAt:  time.Now().Add(-24 * time.Hour),
		ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
		Currency:  "USD",
		CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
}

// requestWithAuthHeader builds an httptest GET request with the supplied
// raw token in Authorization: Bearer form.
func requestWithAuthHeader(ctx context.Context, rawToken string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/oauth/profile", nil)
	if rawToken != "" {
		r.Header.Set("Authorization", "Bearer "+rawToken)
	}
	return r.WithContext(ctx)
}

// ----- buildIdentity -----

func TestBuildIdentity_APIKey(t *testing.T) {
	store := &fakeIdentityStore{
		users: map[string]*types.User{"user_alice": userFixture()},
	}
	ctx := authCtx(t, authCtxOpts{
		apiKey:  apiKeyFixture(),
		project: projectFixture(),
	})
	id := buildIdentity(store, requestWithAuthHeader(ctx, "ms-not-real"))
	if id == nil {
		t.Fatal("buildIdentity returned nil")
	}
	if id.authKind != "api_key" {
		t.Errorf("authKind = %q, want api_key", id.authKind)
	}
	if id.user == nil || id.user.Email != "alice@example.com" {
		t.Errorf("user not hydrated, got %+v", id.user)
	}
}

func TestBuildIdentity_OAuthToken(t *testing.T) {
	store := &fakeIdentityStore{
		users: map[string]*types.User{"user_alice": userFixture()},
	}
	const token = "oat_buildidentity_oauth"
	ctx := authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		oauthRawToken: token,
		oauthClientID: "client_xyz",
	})
	id := buildIdentity(store, requestWithAuthHeader(ctx, token))
	if id == nil {
		t.Fatal("buildIdentity returned nil")
	}
	if id.authKind != "oauth_token" {
		t.Errorf("authKind = %q, want oauth_token", id.authKind)
	}
}

func TestBuildIdentity_MissingUserRow(t *testing.T) {
	// User lookup failure must not block identity construction —
	// account fields just degrade.
	store := &fakeIdentityStore{userErr: errors.New("transient db error")}
	ctx := authCtx(t, authCtxOpts{apiKey: apiKeyFixture(), project: projectFixture()})
	id := buildIdentity(store, requestWithAuthHeader(ctx, "ms-fake"))
	if id == nil {
		t.Fatal("buildIdentity returned nil on user lookup error")
	}
	if id.user != nil {
		t.Errorf("user should be nil on lookup error, got %+v", id.user)
	}
}

func TestBuildIdentity_NilContext(t *testing.T) {
	store := &fakeIdentityStore{}
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	if id := buildIdentity(store, r); id != nil {
		t.Errorf("expected nil for empty context, got %+v", id)
	}
}

// ----- mapPlanToProjectType -----

func TestMapPlanToProjectType(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"pro", "pro"},
		{"max_2x", "max"},
		{"max_5x", "max"},
		{"max_200x", "max"},
		// Anything else maps to empty string (omitempty drops it from response).
		{"free", ""},
		{"", ""},
		{"max", ""},      // bare "max" without _Nx suffix — no real modelserver plan uses this
		{"custom", ""},
	} {
		if got := mapPlanToProjectType(tc.in); got != tc.want {
			t.Errorf("mapPlanToProjectType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ----- HandleOAuthProfile -----

func TestHandleOAuthProfile_OK_MaxSubscription(t *testing.T) {
	store := &fakeIdentityStore{
		users:    map[string]*types.User{"user_alice": userFixture()},
		euByProj: map[string]*types.ExtraUsageSettings{"proj_test": {Enabled: true}},
	}
	const token = "oat_profile_max"
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		subscription:  activeSubFixture("max_5x"),
		oauthRawToken: token,
		oauthClientID: "client_anthropic",
	}), token)
	writeOAuthProfile(store, w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var resp oauthProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// account
	if resp.Account.UUID != "user_alice" || resp.Account.Email != "alice@example.com" || resp.Account.DisplayName != "Alice" {
		t.Errorf("account = %+v", resp.Account)
	}
	if resp.Account.CreatedAt != "2024-03-15T10:00:00Z" {
		t.Errorf("account.created_at = %q, want 2024-03-15T10:00:00Z", resp.Account.CreatedAt)
	}

	// project
	if resp.Project.UUID != "proj_test" {
		t.Errorf("project.uuid = %q", resp.Project.UUID)
	}
	if resp.Project.ProjectType != "max" {
		t.Errorf("project_type = %q, want max", resp.Project.ProjectType)
	}
	if resp.Project.RateLimitTier == nil || *resp.Project.RateLimitTier != "max_5x" {
		t.Errorf("rate_limit_tier = %v, want *max_5x", resp.Project.RateLimitTier)
	}
	if resp.Project.SeatTier == nil || *resp.Project.SeatTier != "max_5x" {
		t.Errorf("seat_tier = %v, want *max_5x", resp.Project.SeatTier)
	}
	if !resp.Project.HasExtraUsageEnabled {
		t.Errorf("has_extra_usage_enabled = false, want true")
	}
	if resp.Project.BillingType == nil || *resp.Project.BillingType != "stripe" {
		t.Errorf("billing_type = %v, want *stripe", resp.Project.BillingType)
	}
	if resp.Project.SubscriptionCreatedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("subscription_created_at = %q", resp.Project.SubscriptionCreatedAt)
	}
	if resp.Project.CcOnboardingFlags == nil {
		t.Errorf("cc_onboarding_flags must be {} not nil — it's a contract field")
	}
}

func TestHandleOAuthProfile_OK_NoSubscription(t *testing.T) {
	// Free-tier / no-subscription caller: rate_limit_tier, seat_tier,
	// billing_type all JSON null; project_type omitted via omitempty.
	store := &fakeIdentityStore{users: map[string]*types.User{"user_alice": userFixture()}}
	const token = "oat_profile_free"
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		oauthRawToken: token,
	}), token)
	writeOAuthProfile(store, w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	// Nullable fields must serialize as JSON null when no subscription.
	for _, want := range []string{
		`"rate_limit_tier":null`,
		`"seat_tier":null`,
		`"billing_type":null`,
		`"has_extra_usage_enabled":false`,
		`"cc_onboarding_flags":{}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing %q; body=%s", want, body)
		}
	}
	// project_type uses omitempty when no subscription — must be absent.
	if strings.Contains(body, `"project_type"`) {
		t.Errorf("project_type should be omitted when no subscription, body=%s", body)
	}
}

func TestHandleOAuthProfile_ExpiredSubscriptionDoesNotLeak(t *testing.T) {
	// AuthMiddleware stores subscription regardless of expiry — the
	// handler must defend against echoing a stale paid plan after expiry.
	store := &fakeIdentityStore{users: map[string]*types.User{"user_alice": userFixture()}}
	const token = "oat_profile_expired"
	expired := &types.Subscription{
		PlanName:  "max_5x",
		Status:    types.SubscriptionStatusActive, // status flag stale; IsActive() checks dates too
		StartsAt:  time.Now().Add(-60 * 24 * time.Hour),
		ExpiresAt: time.Now().Add(-1 * time.Hour),
		Currency:  "USD",
	}
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		subscription:  expired,
		oauthRawToken: token,
	}), token)
	writeOAuthProfile(store, w, r)

	var resp oauthProfileResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Project.ProjectType != "" {
		t.Errorf("expired subscription must not surface project_type, got %q", resp.Project.ProjectType)
	}
	if resp.Project.SeatTier != nil {
		t.Errorf("expired subscription must not surface seat_tier, got %v", resp.Project.SeatTier)
	}
	if resp.Project.BillingType != nil {
		t.Errorf("expired subscription must not surface billing_type, got %v", resp.Project.BillingType)
	}
}

func TestHandleOAuthProfile_MissingUserDegrades(t *testing.T) {
	// User row missing — account.uuid still set, optional fields omitted.
	store := &fakeIdentityStore{} // no users seeded
	const token = "oat_profile_no_user"
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		oauthRawToken: token,
	}), token)
	writeOAuthProfile(store, w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 on missing user row", w.Code)
	}
	var resp oauthProfileResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Account.UUID != "user_alice" {
		t.Errorf("account.uuid = %q, want user_alice", resp.Account.UUID)
	}
	if resp.Account.Email != "" || resp.Account.DisplayName != "" || resp.Account.CreatedAt != "" {
		t.Errorf("optional account fields must be empty when user row missing, got %+v", resp.Account)
	}
}

func TestHandleOAuthProfile_ExtraUsageDBErrorFailsOpen(t *testing.T) {
	// EU lookup failure must not block the identity response —
	// has_extra_usage_enabled just degrades to false.
	store := &fakeIdentityStore{
		users: map[string]*types.User{"user_alice": userFixture()},
		euErr: errors.New("transient db error"),
	}
	const token = "oat_profile_eu_err"
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		oauthRawToken: token,
	}), token)
	writeOAuthProfile(store, w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 on EU lookup error", w.Code)
	}
	var resp oauthProfileResponse
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Project.HasExtraUsageEnabled {
		t.Errorf("has_extra_usage_enabled must default to false on DB error")
	}
}

func TestHandleOAuthProfile_RejectsAPIKey(t *testing.T) {
	store := &fakeIdentityStore{users: map[string]*types.User{"user_alice": userFixture()}}
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:  apiKeyFixture(),
		project: projectFixture(),
	}), "ms-fake")
	writeOAuthProfile(store, w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for API-key on OAuth-only endpoint", w.Code)
	}
	if !strings.Contains(w.Body.String(), "oauth token required") {
		t.Errorf("body should mention 'oauth token required', got: %s", w.Body.String())
	}
}

func TestHandleOAuthProfile_MissingContext(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/oauth/profile", nil)
	writeOAuthProfile(&fakeIdentityStore{}, w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 on missing auth context", w.Code)
	}
}

func TestHandleOAuthProfile_CacheControlHeaders(t *testing.T) {
	// Identity responses must never sit in an HTTP cache — bodies are
	// per-caller and vary by Authorization header.
	store := &fakeIdentityStore{users: map[string]*types.User{"user_alice": userFixture()}}
	const token = "oat_profile_headers"
	w := httptest.NewRecorder()
	r := requestWithAuthHeader(authCtx(t, authCtxOpts{
		apiKey:        oauthSyntheticAPIKeyFixture(),
		project:       projectFixture(),
		oauthRawToken: token,
	}), token)
	writeOAuthProfile(store, w, r)

	if got := w.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Errorf("Cache-Control = %q, want 'no-store, private'", got)
	}
	if got := w.Header().Get("Vary"); got != "Authorization" {
		t.Errorf("Vary = %q, want 'Authorization'", got)
	}
}
