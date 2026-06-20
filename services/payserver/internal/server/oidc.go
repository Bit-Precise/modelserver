package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/modelserver/modelserver/services/payserver/internal/config"
)

type OIDCAuth struct {
	provider      *oidc.Provider
	verifier      *oidc.IDTokenVerifier
	oauth2Cfg     *oauth2.Config
	allowedEmails map[string]bool // empty = allow any OIDC-validated user
	sessionSecret []byte
	logger        *slog.Logger
}

func NewOIDCAuth(ctx context.Context, cfg config.OIDCConfig, logger *slog.Logger) (*OIDCAuth, error) {
	if cfg.IssuerURL == "" || cfg.ClientID == "" || cfg.ClientSecret == "" || cfg.RedirectURL == "" {
		return nil, errors.New("oidc: issuer_url, client_id, client_secret, and redirect_url are all required")
	}
	if cfg.SessionSecret == "" {
		return nil, errors.New("oidc: session_secret is required (32+ random bytes)")
	}
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc provider discover: %w", err)
	}
	oauth2Cfg := &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  cfg.RedirectURL,
		Scopes:       cfg.Scopes,
	}
	allowed := make(map[string]bool, len(cfg.AllowedEmails))
	for _, e := range cfg.AllowedEmails {
		allowed[strings.ToLower(strings.TrimSpace(e))] = true
	}
	return &OIDCAuth{
		provider:      provider,
		verifier:      provider.Verifier(&oidc.Config{ClientID: cfg.ClientID}),
		oauth2Cfg:     oauth2Cfg,
		allowedEmails: allowed,
		sessionSecret: []byte(cfg.SessionSecret),
		logger:        logger,
	}, nil
}

type sessionCtxKey int

const ctxKeySession sessionCtxKey = iota

// SessionFromContext returns nil (not panic) when no session is present in ctx.
func SessionFromContext(ctx context.Context) *AdminSession {
	s, _ := ctx.Value(ctxKeySession).(*AdminSession)
	return s
}

// RequireSession returns 401 (with Accept: application/json) or 302 to
// /admin/login (with Accept: text/html or anything else) when the
// session cookie is missing/invalid/expired.
func (a *OIDCAuth) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(adminSessionCookieName)
		if err != nil {
			a.unauthorized(w, r)
			return
		}
		s, err := DecodeSession(cookie.Value, a.sessionSecret)
		if err != nil {
			a.unauthorized(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeySession, s)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *OIDCAuth) unauthorized(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

const oauthStateCookie = "payserver_oauth_state"

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (a *OIDCAuth) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state, err := randomState()
	if err != nil {
		http.Error(w, "internal", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthStateCookie,
		Value:    state,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   600,
	})
	http.Redirect(w, r, a.oauth2Cfg.AuthCodeURL(state), http.StatusFound)
}

func (a *OIDCAuth) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	// Clear state cookie.
	http.SetCookie(w, &http.Cookie{Name: oauthStateCookie, Path: "/admin", MaxAge: -1})

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	tok, err := a.oauth2Cfg.Exchange(r.Context(), code)
	if err != nil {
		a.logger.Error("oidc: code exchange", "error", err)
		http.Error(w, "exchange failed", http.StatusBadGateway)
		return
	}
	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok {
		http.Error(w, "id_token missing from oauth response", http.StatusBadGateway)
		return
	}
	idToken, err := a.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		a.logger.Error("oidc: id_token verify", "error", err)
		http.Error(w, "id_token invalid", http.StatusUnauthorized)
		return
	}
	var claims struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, "claims decode failed", http.StatusBadGateway)
		return
	}
	if claims.Email == "" {
		http.Error(w, "no email claim in id_token", http.StatusBadGateway)
		return
	}
	if len(a.allowedEmails) > 0 && !a.allowedEmails[strings.ToLower(claims.Email)] {
		a.logger.Warn("oidc: email not in allowlist", "email", claims.Email)
		http.Error(w, "email not allowed", http.StatusForbidden)
		return
	}
	sess := AdminSession{
		Email:     claims.Email,
		Name:      claims.Name,
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}
	cookieValue, err := EncodeSession(sess, a.sessionSecret)
	if err != nil {
		http.Error(w, "encode session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, NewSessionCookie(cookieValue, 24*time.Hour))
	http.Redirect(w, r, "/admin/", http.StatusFound)
}

func (a *OIDCAuth) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     adminSessionCookieName,
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

func (a *OIDCAuth) WhoamiHandler(w http.ResponseWriter, r *http.Request) {
	s := SessionFromContext(r.Context())
	if s == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"email": s.Email, "name": s.Name})
}
