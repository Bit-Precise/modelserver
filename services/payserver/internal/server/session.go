package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

// AdminSession is the OIDC-authenticated operator identity carried in
// the session cookie. Encoded as base64(json) + "." + HMAC-SHA256 hex.
// SessionSecret signs/verifies — 32+ random bytes.
type AdminSession struct {
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	ExpiresAt time.Time `json:"exp"`
}

func EncodeSession(s AdminSession, secret []byte) (string, error) {
	body, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(bodyB64))
	sig := hex.EncodeToString(mac.Sum(nil))
	return bodyB64 + "." + sig, nil
}

func DecodeSession(token string, secret []byte) (*AdminSession, error) {
	bodyB64, sig, ok := strings.Cut(token, ".")
	if !ok {
		return nil, errors.New("malformed session token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(bodyB64))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return nil, errors.New("invalid session signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return nil, err
	}
	var s AdminSession
	if err := json.Unmarshal(body, &s); err != nil {
		return nil, err
	}
	if time.Now().After(s.ExpiresAt) {
		return nil, errors.New("session expired")
	}
	return &s, nil
}

const adminSessionCookieName = "payserver_admin_session"

func NewSessionCookie(value string, maxAge time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    value,
		Path:     "/admin",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(maxAge.Seconds()),
	}
}
