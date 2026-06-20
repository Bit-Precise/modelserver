package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequireSession_NoCookie_JSONReturns401(t *testing.T) {
	auth := &OIDCAuth{
		sessionSecret: []byte("test-session-secret-32-bytes-min"),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h := auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/admin/tenants", nil)
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

func TestRequireSession_NoCookie_HTMLRedirects(t *testing.T) {
	auth := &OIDCAuth{
		sessionSecret: []byte("test-session-secret-32-bytes-min"),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h := auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/admin/tenants", nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	if w.Header().Get("Location") != "/admin/login" {
		t.Errorf("Location = %q", w.Header().Get("Location"))
	}
}

func TestRequireSession_ValidCookiePasses(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	token, _ := EncodeSession(AdminSession{
		Email: "ops@example.com", ExpiresAt: time.Now().Add(time.Hour),
	}, secret)
	auth := &OIDCAuth{
		sessionSecret: secret,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	var captured *AdminSession
	h := auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = SessionFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/admin/tenants", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	if captured == nil || captured.Email != "ops@example.com" {
		t.Errorf("ctx session = %+v", captured)
	}
}

func TestWhoami_NoSession_401(t *testing.T) {
	auth := &OIDCAuth{
		sessionSecret: []byte("s"),
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest("GET", "/admin/whoami", nil)
	w := httptest.NewRecorder()
	auth.WhoamiHandler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", w.Code)
	}
}

func TestWhoami_WithSession_200(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	token, _ := EncodeSession(AdminSession{
		Email: "x@y.z", Name: "Mr X", ExpiresAt: time.Now().Add(time.Hour),
	}, secret)
	auth := &OIDCAuth{
		sessionSecret: secret,
		logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest("GET", "/admin/whoami", nil)
	req.AddCookie(&http.Cookie{Name: adminSessionCookieName, Value: token})
	w := httptest.NewRecorder()

	// Whoami must run inside RequireSession to populate context.
	h := auth.RequireSession(http.HandlerFunc(auth.WhoamiHandler))
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, want 200", w.Code)
	}
	var body struct {
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	_ = json.NewDecoder(w.Body).Decode(&body)
	if body.Email != "x@y.z" || body.Name != "Mr X" {
		t.Errorf("body = %+v", body)
	}
}
