package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const maxCallbackURLLen = 2048

// validateCallbackURL is a light-weight defense-in-depth check applied at
// Send time. The admin write path (handle_admin.go) is the primary
// gatekeeper; this is a second wall in case a tenant row was inserted by
// a path that didn't validate (e.g. migration 002's default-tenant
// bootstrap from legacy config). Rejects:
//   - non-http(s) schemes
//   - missing host
//   - embedded userinfo
//   - oversize URLs (>2048 chars)
// It does NOT block private-network destinations or apply DNS-rebind
// defenses — those would require a custom transport and are out of scope
// for v1; tenants are trusted internal products at this stage.
func validateCallbackURL(raw string) error {
	if len(raw) > maxCallbackURLLen {
		return fmt.Errorf("callback url too long")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse callback url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("callback url scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("callback url missing host")
	}
	if u.User != nil {
		return fmt.Errorf("callback url must not contain userinfo")
	}
	return nil
}

type DeliveryPayload struct {
	OrderID    string `json:"order_id"`
	PaymentRef string `json:"payment_ref"`
	Status     string `json:"status"`
	PaidAmount int64  `json:"paid_amount"`
	PaidAt     string `json:"paid_at"`
}

// CallbackTarget identifies where + how to deliver a webhook for a
// specific payment. Resolved per-row from the tenant that owns the
// payment: target.URL = tenant.callback_url, target.Secret =
// tenant.callback_secret.
type CallbackTarget struct {
	URL    string
	Secret string
}

type CallbackClient struct {
	httpClient *http.Client
}

func NewCallbackClient(timeout time.Duration) *CallbackClient {
	return &CallbackClient{
		httpClient: &http.Client{Timeout: timeout},
	}
}

// Send POSTs the payload to target.URL HMAC-SHA256-signed with
// target.Secret. Empty target.URL is treated as a no-op success — a
// tenant that doesn't configure a callback URL is read-only by design
// (e.g. test/sandbox tenant).
//
// An empty target.Secret with a non-empty URL is a hard error: signing
// with an empty key would emit forgeable signatures. Operators configure
// the secret alongside the URL via the admin UI; the bootstrap path
// (default-tenant migration) also pulls both together from legacy config.
func (c *CallbackClient) Send(ctx context.Context, target CallbackTarget, payload DeliveryPayload) error {
	if target.URL == "" {
		return nil
	}
	if err := validateCallbackURL(target.URL); err != nil {
		return fmt.Errorf("invalid callback target: %w", err)
	}
	if target.Secret == "" {
		return fmt.Errorf("callback target has URL but no signing secret")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(target.Secret))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.URL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", signature)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("send callback: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("callback target returned status %d", resp.StatusCode)
	}
	return nil
}

// uuidFromCompact restores a 32-hex-char compact UUID to the standard
// 8-4-4-4-12 format. If the input is already formatted or has an unexpected
// length it is returned unchanged.
func uuidFromCompact(s string) string {
	if len(s) != 32 {
		return s
	}
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
