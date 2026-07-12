package adminv1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/config"
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

func TestOAuthCallbackRejectsUnconfiguredGoogleProvider(t *testing.T) {
	t.Parallel()
	// Google client ID unset -> 501 not_configured.
	s := &Server{
		Auth:   &fakeAuthStore{},
		Config: &config.Config{},
		JWT:    auth.NewJWTManager("test-secret-at-least-32-characters-long", time.Minute, time.Hour),
	}
	input := &OAuthCallbackInput{Provider: OAuthProviderGoogle}
	input.Body.Code = "code"
	_, err := s.oauthCallback(context.Background(), input)
	assertStatusError(t, err, 501, "not_configured")
}

func TestOAuthCallbackRejectsUnconfiguredOIDCProvider(t *testing.T) {
	t.Parallel()
	// OIDC issuer URL unset -> 501 not_configured.
	s := &Server{
		Auth:   &fakeAuthStore{},
		Config: &config.Config{},
		JWT:    auth.NewJWTManager("test-secret-at-least-32-characters-long", time.Minute, time.Hour),
	}
	input := &OAuthCallbackInput{Provider: OAuthProviderOIDC}
	input.Body.Code = "code"
	_, err := s.oauthCallback(context.Background(), input)
	assertStatusError(t, err, 501, "not_configured")
}

// Note: exchange failures against a real provider require network access.
// Coverage of the OAuth exchange itself lives at the auth package level.
// This batch's tests exercise the routing and dispatch decisions only.

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
