package adminv1

// oauth_helpers.go contains small pure-function utilities used by the OAuth
// callback handler. These are duplicated from internal/admin/handle_auth.go
// so that the typed handler has no import dependency on the legacy chi-based
// admin package. The two copies will be unified once Batch 14 removes the
// last legacy chi handler that references the originals.

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/modelserver/modelserver/internal/crypto"
)

// isValidReturnTo validates the return_to URL to prevent open redirects.
// Accepts relative paths ("/oauth/login?...") and absolute URLs whose path
// starts with "/oauth/login". For absolute URLs the host is not restricted
// because the login-page domain varies by deployment; safety relies entirely
// on the path prefix — "/oauth/login" is only served by the Hydra login
// handler.
func isValidReturnTo(raw string) bool {
	if strings.HasPrefix(raw, "/oauth/login") {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return (parsed.Scheme == "http" || parsed.Scheme == "https") &&
		strings.HasPrefix(parsed.Path, "/oauth/login")
}

// buildAuthToken creates an encrypted token containing the user ID and a
// timestamp. The Hydra login handler can decrypt it to accept the login
// without a session cookie (avoids the cross-domain Set-Cookie problem when
// the frontend and API are on different origins).
func buildAuthToken(encKey []byte, userID string) string {
	payload := fmt.Sprintf("%s|%d", userID, time.Now().Unix())
	ciphertext, err := crypto.Encrypt(encKey, []byte(payload))
	if err != nil {
		log.Printf("WARN: failed to build auth token: %v", err)
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(ciphertext)
}

// appendQueryParam appends a single query parameter to rawURL.
// Returns rawURL unchanged if the URL cannot be parsed.
func appendQueryParam(rawURL, key, value string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := parsed.Query()
	q.Set(key, value)
	parsed.RawQuery = q.Encode()
	return parsed.String()
}
