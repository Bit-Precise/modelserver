# Payserver Standalone Multi-Tenant Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn payserver into a true multi-tenant payment gateway: every upstream product gets its own `tenant_id+secret`, its own callback URL, and an OIDC-protected admin UI to manage tenants and inspect payments. Payment-provider credentials stay platform-global (one Stripe / one wechat / one alipay forever).

**Architecture:** Add `tenants` table + bcrypt-auth middleware + `payments.tenant_id` FK. Notify/compensate handlers derive callback URL+secret per-row by joining `payment.tenant_id → tenants`. New `/admin/*` route subtree behind OIDC (company IdP); React+Vite+TS frontend embedded into the Go binary via `//go:embed admin_dist`. Migration 002 creates a `default` tenant from existing legacy config fields so modelserver upgrade is a one-time env swap.

**Tech Stack:** Go 1.26 (payserver module), `github.com/spf13/viper` (new), `golang.org/x/crypto/bcrypt` (new), `github.com/coreos/go-oidc/v3` (new), `golang.org/x/oauth2` (new), PostgreSQL/pgx v5, React 18 + Vite + Tailwind + shadcn (admin frontend), `//go:embed` for static asset bundling.

**Spec:** `docs/superpowers/specs/2026-06-19-payserver-standalone-multitenant-design.md`

## Global Constraints

- Payment provider credentials (Stripe secret_key, wechat MCH key, alipay app key) are **always** platform-global. Per-tenant provider credentials are out of scope forever.
- `payments.order_id` stays globally UNIQUE across tenants. Tenants must use UUIDs as order IDs. Cross-tenant reuse → 409 with conflict-naming message.
- HMAC callback signature header stays `X-Webhook-Signature` (hex-encoded HMAC-SHA256). modelserver's existing verifier (`internal/billing/hmac_middleware.go`) is the contract.
- Tenant secret: 32 random bytes → base64-RawURL string; stored as bcrypt hash (cost 10); cleartext shown only at create/rotate response.
- Bearer header format: `Authorization: Bearer <tenant_uuid>:<secret>`.
- `payments.tenant_id` is `UUID NOT NULL` FK to `tenants(id)` with `ON DELETE RESTRICT`.
- Admin UI is admin-only (no tenant self-service). One role = OIDC-authenticated operator.
- Frontend `admin_dist/` is build output: in `.gitignore` + ships stub for fresh `go build`.
- `//go:embed admin_dist` serves at `/admin/`; SPA fallback for any unmatched `/admin/*` path returns `index.html`.
- OIDC rescue is **first-class**, not Future Work: `payserver admin rescue --email <addr>` issues a one-hour signed session cookie bypassing OIDC, using `PAYSERVER_OIDC_SESSION_SECRET`.
- All existing migrations stay byte-untouched. New migration `002_tenants.sql` must NOT include its own `BEGIN/COMMIT` — the runner wraps each migration in a tx already.
- Migration 002 reads three default-tenant bootstrap values via `current_setting('payserver.default_tenant_secret_hash')` / `..._callback_url` / `..._callback_secret`; the runner injects them via `SET LOCAL` inside the same tx before `tx.Exec(content)`.
- Working directory: `/root/coding/modelserver`. Branch: `spec/payserver-standalone-multitenant` is the current spec branch — for implementation, branch off `main` as `feat/payserver-multitenant`.

## Phase Layout

This plan has **5 phases**. Each phase is independently green (build + tests pass) so you can checkpoint for review:

| Phase | Tasks | Deliverable |
|---|---|---|
| 1. Config refactor (viper) | 1 | All existing functionality unchanged, config now flows through viper |
| 2. Tenant model + migration + auth | 2–5 | Tenants table exists; bearer middleware enforces `<id>:<secret>`; default tenant auto-created |
| 3. Per-tenant callback routing | 6–9 | CallbackClient refactored; notify+compensate use per-row tenant lookup |
| 4. Admin API + OIDC | 10–14 | `/admin/*` CRUD + OIDC login + rescue subcommand |
| 5. Admin frontend | 15–18 | React UI embedded in binary; full smoke test |

A 19th task is final-sweep + PR creation.

---

## Phase 1 — Config refactor to viper

## Task 1: Replace bespoke `Load`/`ApplyEnvOverrides` with viper

**Files:**
- Modify: `services/payserver/internal/config/config.go` (entire rewrite of Load + env handling)
- Modify: `services/payserver/cmd/payserver/main.go` (config loading entrypoint shifts)
- Modify: `services/payserver/go.mod` / `go.sum` (add `github.com/spf13/viper`)
- Create: `services/payserver/internal/config/config_test.go` (round-trip: yaml + env override)

**Interfaces:**
- Consumes: nothing (refactor only)
- Produces:
  - `func Load(configPath string) (*Config, error)` — single entrypoint; reads file (optional) + env. Removes `ApplyEnvOverrides` from the surface.
  - `Config` struct shape **unchanged** at the Go-type level. Field tags add `mapstructure:"…"` alongside `yaml:"…"` so viper can unmarshal both YAML and bound env vars.
  - Env var naming convention preserved exactly: `PAYSERVER_<UPPER_SNAKE_KEY_PATH>` (e.g., `PAYSERVER_DB_URL`, `PAYSERVER_STRIPE_SECRET_KEY`, `PAYSERVER_WECHAT_MCH_PRIVATE_KEY_PEM`).
  - `cfg.WeChat.MchPrivateKeyPEM` / `cfg.Alipay.PrivateKeyPEM` / `cfg.Alipay.AlipayPublicKeyPEM` go through `normalizePEM(...)` post-unmarshal (the existing helper stays).

- [ ] **Step 1: Add viper dependency**

Run:
```bash
cd services/payserver && go get github.com/spf13/viper@latest
```

Expected: `go.mod` gains `github.com/spf13/viper`, `go.sum` updated.

- [ ] **Step 2: Write the failing test**

Create `services/payserver/internal/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_YAMLBaseline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`
server:
  addr: ":9090"
db:
  url: "postgres://test/test"
callback:
  modelserver_url: "https://ms.example/webhook"
  webhook_secret: "wh-secret"
  timeout: 7s
api_key: "from-yaml"
log:
  level: "debug"
  format: "console"
stripe:
  secret_key: "sk_test_yaml"
  webhook_secret: "whsec_yaml"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("Server.Addr = %q", cfg.Server.Addr)
	}
	if cfg.DB.URL != "postgres://test/test" {
		t.Errorf("DB.URL = %q", cfg.DB.URL)
	}
	if cfg.Callback.ModelserverURL != "https://ms.example/webhook" {
		t.Errorf("Callback.ModelserverURL = %q", cfg.Callback.ModelserverURL)
	}
	if cfg.Callback.Timeout != 7*time.Second {
		t.Errorf("Callback.Timeout = %v", cfg.Callback.Timeout)
	}
	if cfg.APIKey != "from-yaml" {
		t.Errorf("APIKey = %q", cfg.APIKey)
	}
	if cfg.Log.Format != "console" {
		t.Errorf("Log.Format = %q", cfg.Log.Format)
	}
	if cfg.Stripe.SecretKey != "sk_test_yaml" {
		t.Errorf("Stripe.SecretKey = %q", cfg.Stripe.SecretKey)
	}
}

func TestLoad_EnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(`
api_key: "from-yaml"
db:
  url: "postgres://yaml/yaml"
stripe:
  secret_key: "sk_yaml"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("PAYSERVER_API_KEY", "from-env")
	t.Setenv("PAYSERVER_DB_URL", "postgres://env/env")
	t.Setenv("PAYSERVER_STRIPE_SECRET_KEY", "sk_env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.APIKey != "from-env" {
		t.Errorf("APIKey: env should win, got %q", cfg.APIKey)
	}
	if cfg.DB.URL != "postgres://env/env" {
		t.Errorf("DB.URL: env should win, got %q", cfg.DB.URL)
	}
	if cfg.Stripe.SecretKey != "sk_env" {
		t.Errorf("Stripe.SecretKey: env should win, got %q", cfg.Stripe.SecretKey)
	}
}

func TestLoad_NoFile_EnvOnly(t *testing.T) {
	t.Setenv("PAYSERVER_DB_URL", "postgres://env-only/db")
	t.Setenv("PAYSERVER_API_KEY", "env-only")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.URL != "postgres://env-only/db" {
		t.Errorf("DB.URL = %q", cfg.DB.URL)
	}
	if cfg.APIKey != "env-only" {
		t.Errorf("APIKey = %q", cfg.APIKey)
	}
	// Defaults still apply
	if cfg.Server.Addr != ":8090" {
		t.Errorf("Server.Addr default = %q, want :8090", cfg.Server.Addr)
	}
	if cfg.Log.Level != "info" {
		t.Errorf("Log.Level default = %q, want info", cfg.Log.Level)
	}
}

func TestLoad_NormalizesPEM(t *testing.T) {
	// Raw base64 (no -----BEGIN----- prefix) — normalizePEM should wrap it.
	rawB64 := "MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQDXXXXXXXXXX"
	t.Setenv("PAYSERVER_WECHAT_MCH_PRIVATE_KEY_PEM", rawB64)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !contains(cfg.WeChat.MchPrivateKeyPEM, "-----BEGIN PRIVATE KEY-----") {
		t.Errorf("PEM was not wrapped: %q", cfg.WeChat.MchPrivateKeyPEM)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
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

- [ ] **Step 3: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/config -run TestLoad -v`
Expected: FAIL — `Load(path)` undefined (current signature takes `io.Reader` and there's no path-based entry).

- [ ] **Step 4: Rewrite config.go**

Replace the entire body of `services/payserver/internal/config/config.go` with:

```go
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"     yaml:"server"`
	DB       DBConfig       `mapstructure:"db"         yaml:"db"`
	Callback CallbackConfig `mapstructure:"callback"   yaml:"callback"`
	APIKey   string         `mapstructure:"api_key"    yaml:"api_key"`
	Log      LogConfig      `mapstructure:"log"        yaml:"log"`
	WeChat   WeChatConfig   `mapstructure:"wechat"     yaml:"wechat"`
	Alipay   AlipayConfig   `mapstructure:"alipay"     yaml:"alipay"`
	Stripe   StripeConfig   `mapstructure:"stripe"     yaml:"stripe"`
}

type ServerConfig struct {
	Addr string `mapstructure:"addr" yaml:"addr"`
}

type DBConfig struct {
	URL string `mapstructure:"url" yaml:"url"`
}

type CallbackConfig struct {
	ModelserverURL string        `mapstructure:"modelserver_url" yaml:"modelserver_url"`
	WebhookSecret  string        `mapstructure:"webhook_secret"  yaml:"webhook_secret"`
	Timeout        time.Duration `mapstructure:"timeout"         yaml:"timeout"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"  yaml:"level"`
	Format string `mapstructure:"format" yaml:"format"`
}

type WeChatConfig struct {
	AppID             string `mapstructure:"app_id"               yaml:"app_id"`
	MchID             string `mapstructure:"mch_id"               yaml:"mch_id"`
	MchAPIv3Key       string `mapstructure:"mch_api_v3_key"       yaml:"mch_api_v3_key"`
	MchSerialNo       string `mapstructure:"mch_serial_no"        yaml:"mch_serial_no"`
	MchPrivateKeyPath string `mapstructure:"mch_private_key_path" yaml:"mch_private_key_path"`
	MchPrivateKeyPEM  string `mapstructure:"mch_private_key_pem"  yaml:"mch_private_key_pem"`
	NotifyURL         string `mapstructure:"notify_url"           yaml:"notify_url"`
}

type AlipayConfig struct {
	AppID               string `mapstructure:"app_id"                  yaml:"app_id"`
	PrivateKeyPath      string `mapstructure:"private_key_path"        yaml:"private_key_path"`
	PrivateKeyPEM       string `mapstructure:"private_key_pem"         yaml:"private_key_pem"`
	AlipayPublicKeyPath string `mapstructure:"alipay_public_key_path"  yaml:"alipay_public_key_path"`
	AlipayPublicKeyPEM  string `mapstructure:"alipay_public_key_pem"   yaml:"alipay_public_key_pem"`
	NotifyURL           string `mapstructure:"notify_url"              yaml:"notify_url"`
	ReturnURL           string `mapstructure:"return_url"              yaml:"return_url"`
}

type StripeConfig struct {
	SecretKey     string `mapstructure:"secret_key"     yaml:"secret_key"`
	WebhookSecret string `mapstructure:"webhook_secret" yaml:"webhook_secret"`
	SuccessURL    string `mapstructure:"success_url"    yaml:"success_url"`
	CancelURL     string `mapstructure:"cancel_url"     yaml:"cancel_url"`
	DefaultLocale string `mapstructure:"default_locale" yaml:"default_locale"`
}

// Load reads the optional config file (path may be "") and overlays env vars
// with the PAYSERVER_ prefix. Nested keys translate from dots/underscores:
// PAYSERVER_DB_URL → db.url; PAYSERVER_STRIPE_SECRET_KEY → stripe.secret_key.
// Env values always win over file values.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("PAYSERVER")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults
	v.SetDefault("server.addr", ":8090")
	v.SetDefault("callback.timeout", 10*time.Second)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "json")

	// File (optional)
	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}

	// Every leaf key must be explicitly bound for AutomaticEnv to find it
	// through nested-struct unmarshal (viper's known limitation).
	for _, key := range []string{
		"server.addr",
		"db.url",
		"callback.modelserver_url",
		"callback.webhook_secret",
		"callback.timeout",
		"api_key",
		"log.level",
		"log.format",
		"wechat.app_id",
		"wechat.mch_id",
		"wechat.mch_api_v3_key",
		"wechat.mch_serial_no",
		"wechat.mch_private_key_path",
		"wechat.mch_private_key_pem",
		"wechat.notify_url",
		"alipay.app_id",
		"alipay.private_key_path",
		"alipay.private_key_pem",
		"alipay.alipay_public_key_path",
		"alipay.alipay_public_key_pem",
		"alipay.notify_url",
		"alipay.return_url",
		"stripe.secret_key",
		"stripe.webhook_secret",
		"stripe.success_url",
		"stripe.cancel_url",
		"stripe.default_locale",
	} {
		_ = v.BindEnv(key)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	cfg.WeChat.MchPrivateKeyPEM = normalizePEM(cfg.WeChat.MchPrivateKeyPEM, "PRIVATE KEY")
	cfg.Alipay.PrivateKeyPEM = normalizePEM(cfg.Alipay.PrivateKeyPEM, "PRIVATE KEY")
	cfg.Alipay.AlipayPublicKeyPEM = normalizePEM(cfg.Alipay.AlipayPublicKeyPEM, "PUBLIC KEY")

	return &cfg, nil
}

// normalizePEM accepts PEM content in any of these forms and returns a
// valid multi-line PEM string:
//   - Standard multi-line PEM with headers
//   - Single-line PEM with literal \n separators
//   - Raw base64 without headers (output of scripts/pem-encode.sh)
func normalizePEM(s, label string) string {
	if s == "" {
		return s
	}
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.TrimSpace(s)

	if strings.HasPrefix(s, "-----") {
		return s
	}

	var lines []string
	lines = append(lines, "-----BEGIN "+label+"-----")
	for len(s) > 64 {
		lines = append(lines, s[:64])
		s = s[64:]
	}
	if len(s) > 0 {
		lines = append(lines, s)
	}
	lines = append(lines, "-----END "+label+"-----")
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 5: Update main.go to use new Load signature**

In `services/payserver/cmd/payserver/main.go`, replace the entire config-loading block (lines 28–45) with:

```go
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	// Auto-discover config.yml in the working directory if --config not given.
	path := *configPath
	if path == "" {
		if _, statErr := os.Stat("config.yml"); statErr == nil {
			path = "config.yml"
		}
	}
	cfg, err := config.Load(path)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	if cfg.APIKey == "" {
		log.Fatal("api_key is required (api_key in config or PAYSERVER_API_KEY env)")
	}
```

The `cfg.ApplyEnvOverrides()` call goes away. The `strings` import may now be unused — drop it if so.

- [ ] **Step 6: Run config tests**

Run: `cd services/payserver && go test ./internal/config -v -count=1`
Expected: 4/4 PASS (`TestLoad_YAMLBaseline`, `TestLoad_EnvOverridesYAML`, `TestLoad_NoFile_EnvOnly`, `TestLoad_NormalizesPEM`).

- [ ] **Step 7: Run full payserver tests for no-regression**

Run: `cd services/payserver && go test ./... -count=1`
Expected: PASS across all packages. Notify/gateway/server/store tests don't touch config loading so they should be unaffected, but this confirms.

- [ ] **Step 8: Commit**

```bash
git add services/payserver/internal/config/config.go \
        services/payserver/internal/config/config_test.go \
        services/payserver/cmd/payserver/main.go \
        services/payserver/go.mod services/payserver/go.sum
git commit -m "refactor(payserver/config): replace bespoke Load+ApplyEnvOverrides with viper"
```

---

## Phase 2 — Tenant model + migration + auth

## Task 2: Add tenant types + crypto helpers

**Files:**
- Create: `services/payserver/internal/tenant/tenant.go`
- Create: `services/payserver/internal/tenant/tenant_test.go`
- Modify: `services/payserver/go.mod` / `go.sum` (add `golang.org/x/crypto`)

**Interfaces:**
- Consumes: nothing (pure types + crypto)
- Produces:
  - `type Tenant struct { ID, Name, SecretHash, CallbackURL, CallbackSecret, Description string; IsActive bool; CreatedAt, UpdatedAt time.Time }`
  - `func GenerateSecret() (string, error)` — 32 random bytes → base64-RawURL string
  - `func HashSecret(secret string) (string, error)` — bcrypt cost 10
  - `func VerifySecret(hash, secret string) bool`

- [ ] **Step 1: Add bcrypt dependency**

```bash
cd services/payserver && go get golang.org/x/crypto/bcrypt@latest
```

Expected: `go.mod` gains `golang.org/x/crypto`, `go.sum` updated.

- [ ] **Step 2: Write the failing test**

Create `services/payserver/internal/tenant/tenant_test.go`:

```go
package tenant

import (
	"strings"
	"testing"
)

func TestGenerateSecret(t *testing.T) {
	s1, err := GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	// 32 bytes -> base64 RawURL is 43 chars (no padding).
	if len(s1) != 43 {
		t.Errorf("len(secret) = %d, want 43", len(s1))
	}
	// URL-safe base64: no +/= characters.
	if strings.ContainsAny(s1, "+/=") {
		t.Errorf("secret contains non-RawURL chars: %q", s1)
	}

	// Non-determinism: two consecutive calls must differ.
	s2, _ := GenerateSecret()
	if s1 == s2 {
		t.Errorf("GenerateSecret produced equal values back-to-back")
	}
}

func TestHashAndVerifySecret_Roundtrip(t *testing.T) {
	secret := "test-secret-123"
	hash, err := HashSecret(secret)
	if err != nil {
		t.Fatalf("HashSecret: %v", err)
	}
	if hash == "" {
		t.Fatal("HashSecret returned empty hash")
	}
	if hash == secret {
		t.Fatal("HashSecret returned cleartext (no bcrypt applied)")
	}
	if !VerifySecret(hash, secret) {
		t.Error("VerifySecret rejected correct secret")
	}
	if VerifySecret(hash, "wrong-secret") {
		t.Error("VerifySecret accepted wrong secret")
	}
}

func TestVerifySecret_BadHashRejects(t *testing.T) {
	if VerifySecret("not-a-bcrypt-hash", "anything") {
		t.Error("VerifySecret accepted a non-bcrypt hash")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/tenant -v`
Expected: FAIL — package doesn't exist yet.

- [ ] **Step 4: Implement tenant.go**

Create `services/payserver/internal/tenant/tenant.go`:

```go
// Package tenant defines the Tenant type and secret crypto helpers used by
// payserver to identify upstream callers. Tenants are the unit of
// callback-URL isolation: each tenant owns a callback_url + HMAC secret
// that payserver POSTs DeliveryPayload to after a payment succeeds.
package tenant

import (
	"crypto/rand"
	"encoding/base64"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type Tenant struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	SecretHash     string    `json:"-"`
	CallbackURL    string    `json:"callback_url"`
	CallbackSecret string    `json:"-"`
	Description    string    `json:"description"`
	IsActive       bool      `json:"is_active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// GenerateSecret returns 32 bytes of cryptographic randomness encoded as
// URL-safe base64 without padding (43 chars). The cleartext is what
// callers store and send in the Authorization header; it never goes to
// the database — only its bcrypt hash does.
func GenerateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func HashSecret(secret string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func VerifySecret(hash, secret string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(secret)) == nil
}
```

- [ ] **Step 5: Run test**

Run: `cd services/payserver && go test ./internal/tenant -v -count=1`
Expected: 3/3 PASS.

- [ ] **Step 6: Commit**

```bash
git add services/payserver/internal/tenant/ services/payserver/go.mod services/payserver/go.sum
git commit -m "feat(payserver): add tenant types + bcrypt secret helpers"
```

---

## Task 3: Migration 002 — tenants table + bootstrap injection + payments.tenant_id

**Files:**
- Create: `services/payserver/internal/store/migrations/002_tenants.sql`
- Modify: `services/payserver/internal/store/store.go` (extend `Store.New` signature; inject bootstrap into migration tx; add `MigrationBootstrap` type)
- Create: `services/payserver/internal/store/migrations_002_test.go`
- Modify: `services/payserver/internal/store/payments.go` (add `TenantID` to `Payment` struct + all SQL touchpoints)
- Modify: `services/payserver/internal/notify/{stripe,wechat,alipay}.go` (test fixture migration — they construct `store.Payment` literals)
- Modify: `services/payserver/cmd/payserver/main.go` (wire bootstrap)

**Interfaces:**
- Consumes: Task 2 (`tenant.HashSecret`)
- Produces:
  - SQL file `002_tenants.sql` (no `BEGIN/COMMIT`)
  - `type store.MigrationBootstrap struct { DefaultTenantSecretHash, DefaultCallbackURL, DefaultCallbackSecret string }`
  - `func store.New(databaseURL string, logger *slog.Logger, bootstrap MigrationBootstrap) (*Store, error)` — third arg added; existing callers must adapt
  - `store.Payment` struct gains `TenantID string \`json:"tenant_id"\`` field
  - All payments.go SELECT/INSERT carry `tenant_id`
  - DB invariant: every `payments` row has non-null `tenant_id` FK to `tenants(id)`
  - DB invariant: default tenant always exists post-migration, name=`default`

- [ ] **Step 1: Write the failing test (migration shape)**

Create `services/payserver/internal/store/migrations_002_test.go`:

```go
package store

import (
	"context"
	"os"
	"testing"

	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

// openTestStoreWithBootstrap mirrors openTestStore but feeds a real
// bootstrap so migration 002 succeeds on first apply. PAYSERVER_TEST_DB_URL
// must be set; uses "default-test-secret" for the bootstrap secret.
func openTestStoreWithBootstrap(t *testing.T) *Store {
	t.Helper()
	dbURL := os.Getenv("PAYSERVER_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("set PAYSERVER_TEST_DB_URL to run (e.g. postgres://user:pass@localhost:5432/payserver_test?sslmode=disable)")
	}
	hash, err := tenant.HashSecret("default-test-secret")
	if err != nil {
		t.Fatalf("hash bootstrap secret: %v", err)
	}
	bootstrap := MigrationBootstrap{
		DefaultTenantSecretHash: hash,
		DefaultCallbackURL:      "https://test.example/webhook",
		DefaultCallbackSecret:   "test-callback-secret",
	}
	st, err := New(dbURL, testLogger(), bootstrap)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigration002_TenantsTableExists(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	var exists bool
	if err := st.pool.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'tenants')
	`).Scan(&exists); err != nil {
		t.Fatalf("check tenants table: %v", err)
	}
	if !exists {
		t.Fatal("tenants table missing after migration 002")
	}
}

func TestMigration002_DefaultTenantPresent(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	var name, callbackURL, callbackSecret, secretHash string
	if err := st.pool.QueryRow(ctx, `
		SELECT name, callback_url, callback_secret, secret_hash FROM tenants WHERE name = 'default'
	`).Scan(&name, &callbackURL, &callbackSecret, &secretHash); err != nil {
		t.Fatalf("read default tenant: %v", err)
	}
	if callbackURL != "https://test.example/webhook" {
		t.Errorf("default tenant callback_url = %q", callbackURL)
	}
	if callbackSecret != "test-callback-secret" {
		t.Errorf("default tenant callback_secret = %q", callbackSecret)
	}
	if !tenant.VerifySecret(secretHash, "default-test-secret") {
		t.Errorf("default tenant secret_hash doesn't verify against bootstrap secret")
	}
}

func TestMigration002_PaymentsHaveTenantID(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	var nullCount int
	if err := st.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM payments WHERE tenant_id IS NULL`).Scan(&nullCount); err != nil {
		t.Fatalf("count null tenant_id: %v", err)
	}
	if nullCount != 0 {
		t.Fatalf("found %d payments with null tenant_id after migration 002", nullCount)
	}
}

func TestMigration002_DefaultTenantCannotBeDeleted(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	ctx := context.Background()
	// Seed a payment to make sure FK is enforced.
	var defaultID string
	if err := st.pool.QueryRow(ctx, `SELECT id FROM tenants WHERE name = 'default'`).Scan(&defaultID); err != nil {
		t.Fatalf("get default tenant id: %v", err)
	}
	if _, err := st.pool.Exec(ctx, `
		INSERT INTO payments (tenant_id, order_id, channel, amount, status)
		VALUES ($1, $2, 'wechat', 1, 'pending')
	`, defaultID, "test-fk-order-"+t.Name()); err != nil {
		t.Fatalf("insert payment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(ctx, `DELETE FROM payments WHERE order_id = $1`, "test-fk-order-"+t.Name())
	})
	_, err := st.pool.Exec(ctx, `DELETE FROM tenants WHERE id = $1`, defaultID)
	if err == nil {
		t.Fatal("expected FK violation on default tenant delete; got nil")
	}
}
```

Also create `services/payserver/internal/store/testhelpers_test.go` (if it doesn't already exist):

```go
package store

import (
	"io"
	"log/slog"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
```

If the file already exists from a prior task, just add the function. If `testLogger` is already defined, skip.

- [ ] **Step 2: Run test to verify it fails**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./internal/store -run TestMigration002 -v
```

Expected: FAIL — compile error because `New` only takes 2 args. **This is the worklist signal**; don't proceed to "write migration" until you've signed up for the signature change.

- [ ] **Step 3: Define `MigrationBootstrap` and extend `Store.New`**

In `services/payserver/internal/store/store.go`, before `type Store struct {`:

```go
// MigrationBootstrap carries one-shot values consumed only by migration
// 002. After 002 has been applied to a database, these values are
// computed by main.go but never consulted. The runner reads them inside
// the migration's tx via SET LOCAL so the SQL can current_setting() them.
type MigrationBootstrap struct {
	DefaultTenantSecretHash string
	DefaultCallbackURL      string
	DefaultCallbackSecret   string
}
```

Modify `New` to accept a third parameter and stash it on `Store`:

```go
type Store struct {
	pool      *pgxpool.Pool
	logger    *slog.Logger
	bootstrap MigrationBootstrap // consumed only by migration 002
}

func New(databaseURL string, logger *slog.Logger, bootstrap MigrationBootstrap) (*Store, error) {
	ctx := context.Background()

	if err := ensureDatabase(ctx, databaseURL, logger); err != nil {
		return nil, fmt.Errorf("ensure database exists: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	poolCfg.MaxConns = 10
	poolCfg.MinConns = 3

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	s := &Store{pool: pool, logger: logger, bootstrap: bootstrap}
	if err := s.migrate(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return s, nil
}
```

In the migrate loop, around the `tx.Exec(ctx, string(content))` call, prepend the SET LOCAL block when handling `002_tenants.sql`:

```go
		// Run migration + record in a single transaction to prevent partial application.
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin tx for migration %s: %w", name, err)
		}

		// Migration 002 reads three current_setting() values; inject them
		// inside the same tx so they're scoped to this migration only.
		// bcrypt output / URLs / secrets must not contain single quotes
		// (asserted below); operator-supplied values from viper-loaded
		// config have already cleared the SQL injection threat surface
		// — guard is belt-and-braces.
		if name == "002_tenants.sql" && s.bootstrap.DefaultTenantSecretHash != "" {
			for _, kv := range []struct{ key, val string }{
				{"payserver.default_tenant_secret_hash", s.bootstrap.DefaultTenantSecretHash},
				{"payserver.default_callback_url", s.bootstrap.DefaultCallbackURL},
				{"payserver.default_callback_secret", s.bootstrap.DefaultCallbackSecret},
			} {
				if strings.ContainsRune(kv.val, '\'') {
					tx.Rollback(ctx)
					return fmt.Errorf("migration 002 bootstrap value for %s contains a single quote (unsupported)", kv.key)
				}
				if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL %s = '%s'", kv.key, kv.val)); err != nil {
					tx.Rollback(ctx)
					return fmt.Errorf("set local %s: %w", kv.key, err)
				}
			}
		}

		if _, err := tx.Exec(ctx, string(content)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		// ... rest unchanged
```

Don't forget the `"strings"` import.

- [ ] **Step 4: Create the migration SQL**

Create `services/payserver/internal/store/migrations/002_tenants.sql`:

```sql
-- 002_tenants.sql
--
-- Multi-tenant migration: introduces the tenants table, creates a
-- "default" tenant from legacy config fields, and adds the tenant_id FK
-- on payments so every existing row stays accounted for under the
-- default tenant.
--
-- This SQL is wrapped by the migration runner in a tx; do NOT add
-- BEGIN/COMMIT here (nesting would close the outer tx and the
-- schema_migrations record would land outside the migration's scope).
--
-- current_setting() values are injected into this same tx by the runner
-- (see store.go's SET LOCAL block guarded by name == "002_tenants.sql").
-- Operators who forget PAYSERVER_DEFAULT_TENANT_SECRET will see a clean
-- error: 'unrecognized configuration parameter
-- "payserver.default_tenant_secret_hash"'.

CREATE TABLE IF NOT EXISTS tenants (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT NOT NULL UNIQUE,
    secret_hash     TEXT NOT NULL,
    callback_url    TEXT NOT NULL DEFAULT '',
    callback_secret TEXT NOT NULL DEFAULT '',
    description     TEXT NOT NULL DEFAULT '',
    is_active       BOOLEAN NOT NULL DEFAULT TRUE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_tenants_name ON tenants(name);

INSERT INTO tenants (name, secret_hash, callback_url, callback_secret, description)
VALUES (
    'default',
    current_setting('payserver.default_tenant_secret_hash'),
    current_setting('payserver.default_callback_url'),
    current_setting('payserver.default_callback_secret'),
    'Auto-created during migration 002. Holds the legacy cfg.APIKey / cfg.Callback.* values. Rename via admin UI once multi-tenancy is in use.'
)
ON CONFLICT (name) DO NOTHING;

ALTER TABLE payments ADD COLUMN IF NOT EXISTS tenant_id UUID;

UPDATE payments
SET tenant_id = (SELECT id FROM tenants WHERE name = 'default')
WHERE tenant_id IS NULL;

ALTER TABLE payments ALTER COLUMN tenant_id SET NOT NULL;

ALTER TABLE payments
    ADD CONSTRAINT fk_payments_tenant
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE RESTRICT;

CREATE INDEX IF NOT EXISTS idx_payments_tenant ON payments(tenant_id);
```

- [ ] **Step 5: Update `Store.Payment` + payments.go SQL touchpoints**

In `services/payserver/internal/store/payments.go`:

Add `TenantID string \`json:"tenant_id"\`` to the `Payment` struct, between `ID` and `OrderID`:

```go
type Payment struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	OrderID         string     `json:"order_id"`
	Channel         string     `json:"channel"`
	// ... rest unchanged
}
```

Modify every SQL statement in `payments.go` to include `tenant_id` in both the column list and scan/bind arg list. Specifically:

- `InsertOrGetPayment` INSERT — add `tenant_id` to the column list, `$N+1` placeholder, and `p.TenantID` to bind args. RETURNING list already returns `id, callback_status, callback_retries, created_at, updated_at` — leave it alone.
- `GetPaymentByOrderID` — SELECT list and Scan args both grow by one.
- `GetPaymentByID` — same.
- `ListPendingCallbacks` — same.

Example for `InsertOrGetPayment`:

```go
err := s.pool.QueryRow(ctx, `
    INSERT INTO payments (tenant_id, order_id, channel, trade_no, payment_url, amount, status)
    VALUES ($1, $2, $3, $4, $5, $6, $7)
    ON CONFLICT (order_id) DO NOTHING
    RETURNING id, callback_status, callback_retries, created_at, updated_at`,
    p.TenantID, p.OrderID, p.Channel, p.TradeNo, p.PaymentURL, p.Amount, p.Status,
).Scan(&p.ID, &p.CallbackStatus, &p.CallbackRetries, &p.CreatedAt, &p.UpdatedAt)
```

Apply identical pattern to the other three SQL sites; SELECT lists become `id, tenant_id, order_id, channel, trade_no, payment_url, amount, status, callback_status, callback_retries, raw_notify, paid_at, created_at, updated_at` with matching scan args.

- [ ] **Step 6: Update main.go and existing callers of Store.New**

In `services/payserver/cmd/payserver/main.go`, before `store.New(...)`:

```go
	// Bootstrap values for migration 002 (consumed only on first apply).
	// On subsequent boots, 002 already exists in schema_migrations so the
	// runner skips the SQL and these values go unread.
	var bootstrap store.MigrationBootstrap
	if defaultSecret := os.Getenv("PAYSERVER_DEFAULT_TENANT_SECRET"); defaultSecret != "" {
		hash, err := tenant.HashSecret(defaultSecret)
		if err != nil {
			log.Fatalf("hash default tenant secret: %v", err)
		}
		bootstrap = store.MigrationBootstrap{
			DefaultTenantSecretHash: hash,
			DefaultCallbackURL:      cfg.Callback.ModelserverURL,
			DefaultCallbackSecret:   cfg.Callback.WebhookSecret,
		}
	}

	st, err := store.New(cfg.DB.URL, logger, bootstrap)
```

Add `"github.com/modelserver/modelserver/services/payserver/internal/tenant"` to imports.

- [ ] **Step 7: Run migration tests**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
# If the DB has migration 002 from a prior failed run, you may need a fresh DB:
docker exec agentserver-noise-test-pg psql -U postgres -c "DROP DATABASE payserver_stripe_test;" 2>&1
docker exec agentserver-noise-test-pg psql -U postgres -c "CREATE DATABASE payserver_stripe_test;" 2>&1
cd services/payserver && go test ./internal/store -run TestMigration002 -v -count=1
```

Expected: 4/4 PASS.

- [ ] **Step 8: Run full payserver tests (some will fail — that's the next task)**

Run: `cd services/payserver && go test ./... -count=1`
Expected: notify/server tests fail because they construct `store.Payment{...}` literals without `TenantID`, or call `store.InsertOrGetPayment` without setting `TenantID`. These will be fixed when callers are updated in Task 5 (admin call wiring) and Task 7 (notify per-tenant routing). For now, **build must succeed** but tests in `internal/notify/*_test.go` and `internal/server/*_test.go` may fail with FK violations on the inserts because they don't set `TenantID`.

If you see compile errors (vs. runtime FK violations), fix them now — they're missing struct-field reference syntax. Runtime FK violations are tolerated until Task 5.

- [ ] **Step 9: Commit**

```bash
git add services/payserver/internal/store/migrations/002_tenants.sql \
        services/payserver/internal/store/migrations_002_test.go \
        services/payserver/internal/store/store.go \
        services/payserver/internal/store/payments.go \
        services/payserver/internal/store/testhelpers_test.go \
        services/payserver/cmd/payserver/main.go
git commit -m "feat(payserver/store): tenants table + payments.tenant_id FK (migration 002)

Adds tenants table, payments.tenant_id NOT NULL FK, and a default tenant
auto-created from legacy cfg.APIKey/cfg.Callback.* fields. Store.New
signature gains a third MigrationBootstrap arg that's only consumed on
the first apply.

Tests for callers still fail at FK level (TenantID unset on test-fixture
inserts) — fixed by tasks 5 and 7."
```

---

## Task 4: Tenant CRUD store methods

**Files:**
- Create: `services/payserver/internal/store/tenants.go`
- Create: `services/payserver/internal/store/tenants_test.go`

**Interfaces:**
- Consumes: Task 2 (`tenant.Tenant` struct), Task 3 (tenants table exists)
- Produces:
  - `func (s *Store) CreateTenant(t *tenant.Tenant) error` — INSERT; populates `ID`, `CreatedAt`, `UpdatedAt`; returns error wrapping `pq` unique-violation as a sentinel
  - `func (s *Store) GetTenantByID(id string) (*tenant.Tenant, error)` — returns `(nil, nil)` on not-found
  - `func (s *Store) GetTenantByName(name string) (*tenant.Tenant, error)` — same not-found semantics
  - `func (s *Store) ListTenants(limit, offset int) ([]tenant.Tenant, int, error)` — returns `(rows, total, err)`
  - `func (s *Store) UpdateTenant(id string, updates map[string]any) error` — whitelist fields: `callback_url`, `callback_secret`, `description`, `is_active`. Returns error for unknown keys.
  - `func (s *Store) RotateTenantSecret(id, newHash string) error` — updates secret_hash only
  - `func (s *Store) DeleteTenant(id string) error` — DELETE; surfaces FK RESTRICT as a `ErrTenantHasPayments` sentinel
  - `var ErrTenantNameTaken = errors.New("tenant name already taken")`
  - `var ErrTenantHasPayments = errors.New("tenant has payments and cannot be deleted")`

- [ ] **Step 1: Write the failing test**

Create `services/payserver/internal/store/tenants_test.go`:

```go
package store

import (
	"errors"
	"testing"

	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

func TestCreateTenant_AndGetByID(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	hash, _ := tenant.HashSecret("secret-1")
	tt := &tenant.Tenant{
		Name:           "test-create-" + t.Name(),
		SecretHash:     hash,
		CallbackURL:    "https://x.example/cb",
		CallbackSecret: "cb-secret",
		Description:    "test",
		IsActive:       true,
	}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID) })

	if tt.ID == "" {
		t.Fatal("ID not populated")
	}
	got, err := st.GetTenantByID(tt.ID)
	if err != nil {
		t.Fatalf("GetTenantByID: %v", err)
	}
	if got.Name != tt.Name {
		t.Errorf("Name = %q", got.Name)
	}
	if got.CallbackURL != "https://x.example/cb" {
		t.Errorf("CallbackURL = %q", got.CallbackURL)
	}
}

func TestCreateTenant_DuplicateName(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	hash, _ := tenant.HashSecret("s")
	tt1 := &tenant.Tenant{Name: "dup-" + t.Name(), SecretHash: hash, IsActive: true}
	tt2 := &tenant.Tenant{Name: "dup-" + t.Name(), SecretHash: hash, IsActive: true}
	if err := st.CreateTenant(tt1); err != nil {
		t.Fatalf("first create: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt1.ID) })

	err := st.CreateTenant(tt2)
	if !errors.Is(err, ErrTenantNameTaken) {
		t.Fatalf("expected ErrTenantNameTaken, got %v", err)
	}
}

func TestGetTenantByID_NotFound(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	got, err := st.GetTenantByID("00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("GetTenantByID: %v", err)
	}
	if got != nil {
		t.Fatalf("got non-nil tenant: %+v", got)
	}
}

func TestListTenants_Pagination(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	// default tenant + at least one we seed → total >= 2
	hash, _ := tenant.HashSecret("s")
	seeded := &tenant.Tenant{Name: "list-" + t.Name(), SecretHash: hash, IsActive: true}
	if err := st.CreateTenant(seeded); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, seeded.ID) })

	rows, total, err := st.ListTenants(50, 0)
	if err != nil {
		t.Fatalf("ListTenants: %v", err)
	}
	if total < 2 {
		t.Fatalf("total = %d, want >= 2", total)
	}
	if len(rows) < 2 {
		t.Fatalf("len(rows) = %d, want >= 2", len(rows))
	}
}

func TestUpdateTenant_Whitelist(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	hash, _ := tenant.HashSecret("s")
	tt := &tenant.Tenant{Name: "upd-" + t.Name(), SecretHash: hash, IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID) })

	// Allowed update
	if err := st.UpdateTenant(tt.ID, map[string]any{"callback_url": "https://new.example/cb", "is_active": false}); err != nil {
		t.Fatalf("UpdateTenant allowed: %v", err)
	}
	got, _ := st.GetTenantByID(tt.ID)
	if got.CallbackURL != "https://new.example/cb" || got.IsActive {
		t.Errorf("update didn't take: %+v", got)
	}

	// Forbidden field
	err := st.UpdateTenant(tt.ID, map[string]any{"name": "evil"})
	if err == nil {
		t.Error("expected error updating name")
	}
	err = st.UpdateTenant(tt.ID, map[string]any{"secret_hash": "evil"})
	if err == nil {
		t.Error("expected error updating secret_hash via UpdateTenant")
	}
}

func TestRotateTenantSecret(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	oldHash, _ := tenant.HashSecret("old")
	tt := &tenant.Tenant{Name: "rot-" + t.Name(), SecretHash: oldHash, IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() { _, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID) })

	newHash, _ := tenant.HashSecret("new")
	if err := st.RotateTenantSecret(tt.ID, newHash); err != nil {
		t.Fatalf("RotateTenantSecret: %v", err)
	}
	got, _ := st.GetTenantByID(tt.ID)
	if !tenant.VerifySecret(got.SecretHash, "new") {
		t.Error("new secret doesn't verify")
	}
	if tenant.VerifySecret(got.SecretHash, "old") {
		t.Error("old secret still verifies (rotation didn't replace)")
	}
}

func TestDeleteTenant_BlockedByPayments(t *testing.T) {
	st := openTestStoreWithBootstrap(t)
	hash, _ := tenant.HashSecret("s")
	tt := &tenant.Tenant{Name: "del-" + t.Name(), SecretHash: hash, IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt.ID)
		_, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	// No payments → delete works
	if err := st.DeleteTenant(tt.ID); err != nil {
		t.Fatalf("delete (empty): %v", err)
	}

	// Re-create and seed a payment
	tt2 := &tenant.Tenant{Name: "del2-" + t.Name(), SecretHash: hash, IsActive: true}
	if err := st.CreateTenant(tt2); err != nil {
		t.Fatalf("seed2: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.pool.Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt2.ID)
		_, _ = st.pool.Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt2.ID)
	})
	if _, err := st.pool.Exec(t.Context(), `
		INSERT INTO payments (tenant_id, order_id, channel, amount, status)
		VALUES ($1, $2, 'wechat', 1, 'pending')`, tt2.ID, "del-test-"+t.Name()); err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	err := st.DeleteTenant(tt2.ID)
	if !errors.Is(err, ErrTenantHasPayments) {
		t.Fatalf("expected ErrTenantHasPayments, got %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/store -run "TestCreateTenant|TestGetTenantByID|TestListTenants|TestUpdateTenant|TestRotateTenantSecret|TestDeleteTenant" -v -count=1`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Implement tenants.go**

Create `services/payserver/internal/store/tenants.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

var (
	ErrTenantNameTaken   = errors.New("tenant name already taken")
	ErrTenantHasPayments = errors.New("tenant has payments and cannot be deleted")
)

// updateAllowedFields enumerates the columns UpdateTenant may touch.
// name is immutable through this path (external systems may reference it
// in audit/log); secret_hash goes through RotateTenantSecret.
var updateAllowedFields = map[string]bool{
	"callback_url":    true,
	"callback_secret": true,
	"description":     true,
	"is_active":       true,
}

func (s *Store) CreateTenant(t *tenant.Tenant) error {
	err := s.pool.QueryRow(context.Background(), `
		INSERT INTO tenants (name, secret_hash, callback_url, callback_secret, description, is_active)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		t.Name, t.SecretHash, t.CallbackURL, t.CallbackSecret, t.Description, t.IsActive,
	).Scan(&t.ID, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			return ErrTenantNameTaken
		}
		return fmt.Errorf("insert tenant: %w", err)
	}
	return nil
}

const tenantSelectCols = `id, name, secret_hash, callback_url, callback_secret, description, is_active, created_at, updated_at`

func scanTenant(row pgx.Row) (*tenant.Tenant, error) {
	t := &tenant.Tenant{}
	err := row.Scan(&t.ID, &t.Name, &t.SecretHash, &t.CallbackURL, &t.CallbackSecret,
		&t.Description, &t.IsActive, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (s *Store) GetTenantByID(id string) (*tenant.Tenant, error) {
	t, err := scanTenant(s.pool.QueryRow(context.Background(),
		`SELECT `+tenantSelectCols+` FROM tenants WHERE id = $1`, id))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant by id: %w", err)
	}
	return t, nil
}

func (s *Store) GetTenantByName(name string) (*tenant.Tenant, error) {
	t, err := scanTenant(s.pool.QueryRow(context.Background(),
		`SELECT `+tenantSelectCols+` FROM tenants WHERE name = $1`, name))
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant by name: %w", err)
	}
	return t, nil
}

func (s *Store) ListTenants(limit, offset int) ([]tenant.Tenant, int, error) {
	ctx := context.Background()
	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM tenants`).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count tenants: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+tenantSelectCols+` FROM tenants ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()
	var out []tenant.Tenant
	for rows.Next() {
		t, err := scanTenant(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("scan tenant: %w", err)
		}
		out = append(out, *t)
	}
	return out, total, rows.Err()
}

func (s *Store) UpdateTenant(id string, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	setClauses := make([]string, 0, len(updates))
	args := make([]any, 0, len(updates)+1)
	i := 1
	for k, v := range updates {
		if !updateAllowedFields[k] {
			return fmt.Errorf("field %q is not updateable through UpdateTenant", k)
		}
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", k, i))
		args = append(args, v)
		i++
	}
	args = append(args, id)
	q := fmt.Sprintf(`UPDATE tenants SET %s, updated_at = NOW() WHERE id = $%d`,
		joinComma(setClauses), i)
	_, err := s.pool.Exec(context.Background(), q, args...)
	if err != nil {
		return fmt.Errorf("update tenant: %w", err)
	}
	return nil
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func (s *Store) RotateTenantSecret(id, newHash string) error {
	_, err := s.pool.Exec(context.Background(),
		`UPDATE tenants SET secret_hash = $1, updated_at = NOW() WHERE id = $2`,
		newHash, id)
	if err != nil {
		return fmt.Errorf("rotate tenant secret: %w", err)
	}
	return nil
}

func (s *Store) DeleteTenant(id string) error {
	_, err := s.pool.Exec(context.Background(), `DELETE FROM tenants WHERE id = $1`, id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			return ErrTenantHasPayments
		}
		return fmt.Errorf("delete tenant: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests**

Run: `cd services/payserver && go test ./internal/store -run "TestCreateTenant|TestGetTenantByID|TestListTenants|TestUpdateTenant|TestRotateTenantSecret|TestDeleteTenant" -v -count=1`
Expected: 7/7 PASS.

- [ ] **Step 5: Commit**

```bash
git add services/payserver/internal/store/tenants.go services/payserver/internal/store/tenants_test.go
git commit -m "feat(payserver/store): tenant CRUD methods with FK-aware error sentinels"
```

---

## Task 5: Tenant auth middleware + handleCreatePayment writes tenant_id

**Files:**
- Create: `services/payserver/internal/server/auth.go`
- Create: `services/payserver/internal/server/auth_test.go`
- Modify: `services/payserver/internal/server/handler.go` (handleCreatePayment reads tenant from ctx; sets `payment.TenantID`)
- Modify: `services/payserver/internal/server/routes.go` (`Config` keeps `APIKey` field but it becomes unused; replace `bearerAuthMiddleware(cfg.APIKey)` with `tenantAuthMiddleware(cfg.Store, cfg.Logger)`)
- Modify: `services/payserver/internal/server/handler_test.go` (existing `TestBearerAuth` deleted; replaced by auth_test.go)
- Delete: `bearerAuthMiddleware` in handler.go (no longer used; keep `paymentAPIRequest`, `paymentAPIResponse`, `validateReturnURL`, `writeJSON`)

**Interfaces:**
- Consumes: Task 2 (`tenant.VerifySecret`), Task 4 (`Store.GetTenantByID`)
- Produces:
  - `func TenantFromContext(ctx context.Context) *tenant.Tenant` — panics if no tenant in ctx (programmer error)
  - `func tenantAuthMiddleware(st *store.Store, logger *slog.Logger) func(http.Handler) http.Handler`
  - `handleCreatePayment` now stamps `payment.TenantID = TenantFromContext(r.Context()).ID`

- [ ] **Step 1: Write the failing test**

Create `services/payserver/internal/server/auth_test.go`:

```go
package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

func openTestStoreServer(t *testing.T) *store.Store {
	t.Helper()
	dbURL := os.Getenv("PAYSERVER_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("set PAYSERVER_TEST_DB_URL")
	}
	hash, _ := tenant.HashSecret("default-test-secret")
	bootstrap := store.MigrationBootstrap{
		DefaultTenantSecretHash: hash,
		DefaultCallbackURL:      "https://test.example/webhook",
		DefaultCallbackSecret:   "test-callback-secret",
	}
	st, err := store.New(dbURL, slog.New(slog.NewTextHandler(io.Discard, nil)), bootstrap)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func seedTestTenant(t *testing.T, st *store.Store, secret string) *tenant.Tenant {
	t.Helper()
	hash, _ := tenant.HashSecret(secret)
	tt := &tenant.Tenant{
		Name:           "auth-test-" + t.Name(),
		SecretHash:     hash,
		CallbackURL:    "https://auth.example/cb",
		CallbackSecret: "cb",
		IsActive:       true,
	}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})
	return tt
}

func runAuth(t *testing.T, st *store.Store, hdr string) (int, *tenant.Tenant) {
	t.Helper()
	var captured *tenant.Tenant
	mw := tenantAuthMiddleware(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = TenantFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("POST", "/payments", nil)
	if hdr != "" {
		req.Header.Set("Authorization", hdr)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req.WithContext(context.Background()))
	return w.Code, captured
}

func TestTenantAuth_MissingHeader(t *testing.T) {
	st := openTestStoreServer(t)
	code, _ := runAuth(t, st, "")
	if code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", code)
	}
}

func TestTenantAuth_MalformedToken(t *testing.T) {
	st := openTestStoreServer(t)
	code, _ := runAuth(t, st, "Bearer no-colon-here")
	if code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", code)
	}
}

func TestTenantAuth_UnknownTenantID(t *testing.T) {
	st := openTestStoreServer(t)
	code, _ := runAuth(t, st, "Bearer 00000000-0000-0000-0000-000000000000:any-secret")
	if code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", code)
	}
}

func TestTenantAuth_WrongSecret(t *testing.T) {
	st := openTestStoreServer(t)
	tt := seedTestTenant(t, st, "right-secret")
	code, _ := runAuth(t, st, "Bearer "+tt.ID+":wrong-secret")
	if code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", code)
	}
}

func TestTenantAuth_InactiveTenant(t *testing.T) {
	st := openTestStoreServer(t)
	tt := seedTestTenant(t, st, "secret")
	if err := st.UpdateTenant(tt.ID, map[string]any{"is_active": false}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	code, _ := runAuth(t, st, "Bearer "+tt.ID+":secret")
	if code != http.StatusUnauthorized {
		t.Fatalf("code = %d, want 401", code)
	}
}

func TestTenantAuth_Success(t *testing.T) {
	st := openTestStoreServer(t)
	tt := seedTestTenant(t, st, "secret")
	code, ctxTenant := runAuth(t, st, "Bearer "+tt.ID+":secret")
	if code != http.StatusOK {
		t.Fatalf("code = %d, want 200", code)
	}
	if ctxTenant == nil || ctxTenant.ID != tt.ID {
		t.Fatalf("ctx tenant = %+v, want id=%s", ctxTenant, tt.ID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/server -run TestTenantAuth -v -count=1`
Expected: FAIL — `tenantAuthMiddleware` undefined.

- [ ] **Step 3: Implement auth.go**

Create `services/payserver/internal/server/auth.go`:

```go
package server

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

type ctxKey int

const ctxKeyTenant ctxKey = iota

// TenantFromContext returns the authenticated tenant. Panics if invoked
// from a handler that wasn't wrapped by tenantAuthMiddleware — that's a
// programmer error, not a runtime one.
func TenantFromContext(ctx context.Context) *tenant.Tenant {
	t, ok := ctx.Value(ctxKeyTenant).(*tenant.Tenant)
	if !ok {
		panic("TenantFromContext called from a non-authenticated handler")
	}
	return t
}

// dummyBcryptHash is a fixed bcrypt hash used to maintain ~constant-time
// latency when the tenant id doesn't exist (otherwise the absence of a
// VerifySecret call would leak existence via timing).
const dummyBcryptHash = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

func tenantAuthMiddleware(st *store.Store, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing bearer token"})
				return
			}
			token := auth[7:]
			id, secret, ok := strings.Cut(token, ":")
			if !ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "malformed token; expected <tenant_id>:<secret>"})
				return
			}
			t, err := st.GetTenantByID(id)
			if err != nil {
				logger.Error("auth: get tenant", "tenant_id", id, "error", err)
				_ = tenant.VerifySecret(dummyBcryptHash, secret)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
				return
			}
			if t == nil || !t.IsActive {
				_ = tenant.VerifySecret(dummyBcryptHash, secret)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
				return
			}
			if !tenant.VerifySecret(t.SecretHash, secret) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyTenant, t)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 4: Update handleCreatePayment**

In `services/payserver/internal/server/handler.go`, modify `handleCreatePayment` to stamp `TenantID`:

```go
// At the top of the handler, after r.Body bounding and before req decode:
		currentTenant := TenantFromContext(r.Context())
```

Then in the payment construction:

```go
		payment := &store.Payment{
			TenantID: currentTenant.ID,
			OrderID:  req.OrderID,
			Channel:  req.Channel,
			Amount:   req.Amount,
			Status:   "pending",
		}
```

Delete the `bearerAuthMiddleware` function from handler.go entirely. Keep `validateReturnURL`, `writeJSON`, the struct types.

- [ ] **Step 5: Update routes.go**

In `services/payserver/internal/server/routes.go`, replace the bearer-auth Group:

```go
	r.Group(func(r chi.Router) {
		r.Use(tenantAuthMiddleware(cfg.Store, cfg.Logger))
		r.Post("/payments", handleCreatePayment(cfg.Store, cfg.Gateways, cfg.Logger))
	})
```

`Config.APIKey` field is now dead — delete it. main.go's wiring drops the `APIKey: cfg.APIKey` line.

- [ ] **Step 6: Delete the old handler test, update other handler tests for new auth flow**

In `services/payserver/internal/server/handler_test.go`:
- Delete `TestBearerAuth` entirely (auth tests live in auth_test.go now).
- Keep `TestParsePaymentRequest` — it doesn't touch auth.
- Keep `TestValidateReturnURL` — pure unit.

- [ ] **Step 7: Run server tests**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./internal/server -v -count=1
```

Expected: auth tests 6/6 PASS, ParsePaymentRequest + ValidateReturnURL still PASS.

- [ ] **Step 8: Run full payserver build (notify tests will still fail until Task 7)**

Run: `cd services/payserver && go build ./...`
Expected: PASS. Notify integration tests still fail at FK level — that's Task 7's scope.

- [ ] **Step 9: Commit**

```bash
git add services/payserver/internal/server/auth.go \
        services/payserver/internal/server/auth_test.go \
        services/payserver/internal/server/handler.go \
        services/payserver/internal/server/handler_test.go \
        services/payserver/internal/server/routes.go \
        services/payserver/cmd/payserver/main.go
git commit -m "feat(payserver/server): tenant bearer auth middleware; handleCreatePayment stamps TenantID

Replaces the single-API-key bearerAuthMiddleware with a per-tenant
middleware that parses Authorization: Bearer <tenant_id>:<secret>,
verifies via bcrypt against tenants.secret_hash, and injects the
authenticated Tenant into request context. Notify-side tests still fail
at FK level — fixed by Task 7."
```

---

## Phase 3 — Per-tenant callback routing

## Task 6: Refactor CallbackClient — target is per-call, not constructor

**Files:**
- Modify: `services/payserver/internal/notify/callback.go` (signature change)
- Modify: `services/payserver/internal/notify/callback_test.go` (existing tests need new signature)

**Interfaces:**
- Consumes: nothing new
- Produces:
  - `type CallbackTarget struct { URL, Secret string }`
  - `func NewCallbackClient(timeout time.Duration) *CallbackClient` — constructor no longer takes URL/secret
  - `func (c *CallbackClient) Send(ctx context.Context, target CallbackTarget, payload DeliveryPayload) error` — target now per-call
  - Empty `target.URL` is a no-op success (interpret as "this tenant doesn't want callbacks")

- [ ] **Step 1: Write the failing test**

Replace `services/payserver/internal/notify/callback_test.go` with:

```go
package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCallback_Send_PerCallTargetSigning(t *testing.T) {
	secret := "test-webhook-secret"

	var receivedBody []byte
	var receivedSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedSig = r.Header.Get("X-Webhook-Signature")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewCallbackClient(5 * time.Second)
	payload := DeliveryPayload{
		OrderID: "order-123", PaymentRef: "pay-456", Status: "paid",
		PaidAmount: 2000, PaidAt: "2026-03-11T12:00:00Z",
	}

	target := CallbackTarget{URL: srv.URL, Secret: secret}
	if err := client.Send(t.Context(), target, payload); err != nil {
		t.Fatalf("Send: %v", err)
	}

	var got DeliveryPayload
	if err := json.Unmarshal(receivedBody, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.OrderID != "order-123" {
		t.Errorf("OrderID = %q", got.OrderID)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(receivedBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if receivedSig != expected {
		t.Errorf("signature = %q, want %q", receivedSig, expected)
	}
}

func TestCallback_Send_EmptyURLIsNoop(t *testing.T) {
	client := NewCallbackClient(5 * time.Second)
	target := CallbackTarget{URL: "", Secret: "anything"}
	err := client.Send(t.Context(), target, DeliveryPayload{OrderID: "x"})
	if err != nil {
		t.Errorf("empty URL should be no-op success, got: %v", err)
	}
}

func TestCallback_Send_Non2xxIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := NewCallbackClient(5 * time.Second)
	target := CallbackTarget{URL: srv.URL, Secret: "s"}
	err := client.Send(t.Context(), target, DeliveryPayload{OrderID: "x"})
	if err == nil {
		t.Error("expected error on 500 response")
	}
}

func TestCallback_Send_PerCallDifferentSecrets(t *testing.T) {
	var sig1, sig2 string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sig1 == "" {
			sig1 = r.Header.Get("X-Webhook-Signature")
		} else {
			sig2 = r.Header.Get("X-Webhook-Signature")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewCallbackClient(5 * time.Second)
	pl := DeliveryPayload{OrderID: "x"}
	_ = client.Send(t.Context(), CallbackTarget{URL: srv.URL, Secret: "secret-a"}, pl)
	_ = client.Send(t.Context(), CallbackTarget{URL: srv.URL, Secret: "secret-b"}, pl)

	if sig1 == sig2 {
		t.Error("different secrets produced same signature — secret not used per-call")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/notify -run TestCallback_ -v -count=1`
Expected: FAIL — `NewCallbackClient` signature is `(url, secret string, timeout time.Duration)` not `(timeout time.Duration)`.

- [ ] **Step 3: Rewrite callback.go**

Replace `services/payserver/internal/notify/callback.go`:

```go
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
	"time"
)

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
func (c *CallbackClient) Send(ctx context.Context, target CallbackTarget, payload DeliveryPayload) error {
	if target.URL == "" {
		return nil
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
// 8-4-4-4-12 format. If the input is already formatted or has an
// unexpected length it is returned unchanged.
func uuidFromCompact(s string) string {
	if len(s) != 32 {
		return s
	}
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
```

- [ ] **Step 4: Run callback tests**

Run: `cd services/payserver && go test ./internal/notify -run TestCallback_ -v -count=1`
Expected: 4/4 PASS.

- [ ] **Step 5: Don't build full package yet (Task 7 fixes the notify handlers that depend on old signature)**

Run: `cd services/payserver && go build ./internal/notify/`
Expected: FAIL. `wechat.go` / `alipay.go` / `stripe.go` still call old `NewCallbackClient(url, secret, timeout)` and `h.callback.Send(ctx, payload)` (two-arg). That's Task 7.

- [ ] **Step 6: Commit**

```bash
git add services/payserver/internal/notify/callback.go services/payserver/internal/notify/callback_test.go
git commit -m "refactor(payserver/notify): CallbackClient takes target per-call, not constructor

CallbackTarget carries URL+secret separately; empty URL = no-op success.
Wechat/alipay/stripe handlers are intentionally broken until Task 7."
```

---

## Task 7: Notify handlers resolve target per-tenant

**Files:**
- Modify: `services/payserver/internal/notify/wechat.go`
- Modify: `services/payserver/internal/notify/alipay.go`
- Modify: `services/payserver/internal/notify/stripe.go`
- Modify: `services/payserver/internal/notify/wechat_test.go` — currently doesn't exist; but stripe_test.go and alipay_test.go do
- Modify: `services/payserver/internal/notify/stripe_test.go` (the harness `seedPendingPayment` needs to set `TenantID`)
- Modify: `services/payserver/internal/notify/alipay_test.go` (no payment-store interaction; safe)

**Interfaces:**
- Consumes: Task 4 (`Store.GetTenantByID`), Task 6 (`CallbackClient.Send(ctx, target, payload)`)
- Produces:
  - Each handler, after locating `payment` by order_id, calls `t, _ := h.store.GetTenantByID(payment.TenantID)`; if `t == nil || !t.IsActive`, log warn + skip callback (do NOT IncrCallbackRetries — tenant is gone, retry won't help).
  - On callback failure with active tenant: `IncrCallbackRetries(orderID)` as before.

- [ ] **Step 1: Update stripe.go**

In `services/payserver/internal/notify/stripe.go`, the relevant block (around line 130–155) becomes:

```go
	// Ack Stripe before calling modelserver.
	w.WriteHeader(http.StatusOK)

	// Phase 2: resolve tenant + deliver callback.
	t, err := h.store.GetTenantByID(payment.TenantID)
	if err != nil {
		h.logger.Error("stripe notify: tenant lookup failed",
			"order_id", orderID, "tenant_id", payment.TenantID,
			"event_id", event.ID, "error", err)
		// Don't IncrCallbackRetries — DB error is transient, the
		// compensate worker will retry the lookup on its next pass.
		return
	}
	if t == nil || !t.IsActive {
		h.logger.Warn("stripe notify: tenant missing or inactive; skipping callback",
			"order_id", orderID, "tenant_id", payment.TenantID, "event_id", event.ID)
		// Mark failed: a deleted/disabled tenant will never accept callbacks.
		// Compensate worker should not retry forever.
		h.store.MarkCallbackFailed(orderID)
		return
	}

	target := CallbackTarget{URL: t.CallbackURL, Secret: t.CallbackSecret}
	payload := DeliveryPayload{
		OrderID:    orderID,
		PaymentRef: payment.ID,
		Status:     "paid",
		PaidAmount: payment.Amount,
		PaidAt:     paidAt.Format(time.RFC3339),
	}
	cbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.callback.Send(cbCtx, target, payload); err != nil {
		h.logger.Warn("stripe notify: callback failed, will retry",
			"order_id", orderID, "tenant_id", t.ID, "event_id", event.ID, "error", err)
		h.store.IncrCallbackRetries(orderID)
		return
	}
	h.store.MarkCallbackSuccess(orderID)
```

- [ ] **Step 2: Update wechat.go**

In `services/payserver/internal/notify/wechat.go`, apply the same pattern around lines 100–125:

```go
	// Reply success to WeChat immediately
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"code": "SUCCESS", "message": "OK"})

	// Phase 2: resolve tenant + deliver callback.
	t, err := h.store.GetTenantByID(payment.TenantID)
	if err != nil {
		h.logger.Error("wechat notify: tenant lookup failed",
			"order_id", orderID, "tenant_id", payment.TenantID, "error", err)
		return
	}
	if t == nil || !t.IsActive {
		h.logger.Warn("wechat notify: tenant missing or inactive; skipping callback",
			"order_id", orderID, "tenant_id", payment.TenantID)
		h.store.MarkCallbackFailed(orderID)
		return
	}

	target := CallbackTarget{URL: t.CallbackURL, Secret: t.CallbackSecret}
	payload := DeliveryPayload{
		OrderID:    orderID,
		PaymentRef: payment.ID,
		Status:     "paid",
		PaidAmount: payment.Amount,
		PaidAt:     paidAt.Format(time.RFC3339),
	}
	cbCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.callback.Send(cbCtx, target, payload); err != nil {
		h.logger.Warn("wechat notify: callback failed, will retry",
			"order_id", orderID, "tenant_id", t.ID, "error", err)
		h.store.IncrCallbackRetries(orderID)
		return
	}
	h.store.MarkCallbackSuccess(orderID)
```

- [ ] **Step 3: Update alipay.go**

In `services/payserver/internal/notify/alipay.go`, apply the same pattern around lines 105–127. The diff body is identical to wechat's — just adjust the log prefix to `"alipay notify: ..."`.

- [ ] **Step 4: Update stripe_test.go harness for TenantID**

In `services/payserver/internal/notify/stripe_test.go`, the helper `seedPendingPayment` constructs `store.Payment` without `TenantID`. The helper should seed a tenant first and use its ID:

```go
// Add near the top of the file's helpers section:
func seedTenant(t *testing.T, st *store.Store) *tenant.Tenant {
	t.Helper()
	hash, _ := tenant.HashSecret("test-secret")
	tt := &tenant.Tenant{
		Name:           "notify-test-" + t.Name(),
		SecretHash:     hash,
		CallbackURL:    "https://will-be-overridden.example",
		CallbackSecret: "stub",
		IsActive:       true,
	}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt.ID)
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})
	return tt
}

// Modify seedPendingPayment signature to accept a tenant:
func seedPendingPayment(t *testing.T, st *store.Store, tenantID, channel string, amount int64) string {
	t.Helper()
	p := &store.Payment{
		TenantID: tenantID,
		OrderID:  newTestUUID(),
		Channel:  channel,
		Amount:   amount,
		Status:   "pending",
	}
	_, err := st.InsertOrGetPayment(p)
	if err != nil {
		t.Fatalf("seed pending: %v", err)
	}
	return p.OrderID
}
// Same change for seedPaidPayment.
```

And in each test, the harness `newStripeHarness` should also seed the tenant and override `CallbackURL` to point at the test's stub HTTP server:

```go
// In newStripeHarness, after creating cb stub server:
tt := seedTenant(t, st)
// Override the tenant's callback to point at our stub
if err := st.UpdateTenant(tt.ID, map[string]any{
	"callback_url":    cbServer.URL,
	"callback_secret": "stub-secret",
}); err != nil {
	t.Fatalf("update tenant callback: %v", err)
}
// Then later when seeding payments, pass tt.ID:
seedPendingPayment(t, st, tt.ID, "stripe", 2000)
```

Apply the analogous change to every test that calls `seedPendingPayment` / `seedPaidPayment` — they all need a tenant context now.

- [ ] **Step 5: Build + test**

```bash
cd services/payserver && go build ./...
```

Expected: PASS.

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./internal/notify -v -count=1
```

Expected: stripe notify tests 9/9 PASS, callback tests 4/4 PASS, alipay tests still PASS.

- [ ] **Step 6: Commit**

```bash
git add services/payserver/internal/notify/
git commit -m "feat(payserver/notify): resolve callback target per-tenant; mark failed on missing tenant"
```

---

## Task 8: Compensate worker — per-tenant target lookup

**Files:**
- Modify: `services/payserver/internal/compensate/compensate.go`
- Modify: `services/payserver/internal/compensate/compensate_test.go`

**Interfaces:**
- Consumes: Task 4 (`Store.GetTenantByID`), Task 6 (`CallbackClient.Send(ctx, target, payload)`)
- Produces: Worker loop unchanged externally; internally resolves per-row tenant before calling `Send`.

- [ ] **Step 1: Update compensate.go**

The current worker likely receives a `*CallbackClient` constructed with global URL+secret. After Task 6's refactor, that's gone — the constructor takes only timeout. The worker now needs the `*Store` for tenant lookups.

In `services/payserver/internal/compensate/compensate.go`, update the worker construction and per-row loop:

```go
// Worker fields stay the same — it already has st *store.Store and
// callback *notify.CallbackClient. The change is in the row-processing
// loop. Find the call that does:
//   err := w.callback.Send(ctx, payload)
// and replace with:

for _, p := range rows {
    t, err := w.store.GetTenantByID(p.TenantID)
    if err != nil {
        w.logger.Error("compensate: tenant lookup failed; skipping this pass",
            "payment_id", p.ID, "tenant_id", p.TenantID, "error", err)
        continue
    }
    if t == nil || !t.IsActive {
        w.logger.Warn("compensate: tenant missing or inactive; marking failed",
            "payment_id", p.ID, "tenant_id", p.TenantID)
        if err := w.store.MarkCallbackFailed(p.OrderID); err != nil {
            w.logger.Error("compensate: mark failed", "order_id", p.OrderID, "error", err)
        }
        continue
    }
    target := notify.CallbackTarget{URL: t.CallbackURL, Secret: t.CallbackSecret}
    payload := notify.DeliveryPayload{
        OrderID:    p.OrderID,
        PaymentRef: p.ID,
        Status:     "paid",
        PaidAmount: p.Amount,
        PaidAt:     /* existing PaidAt formatting */,
    }
    if err := w.callback.Send(ctx, target, payload); err != nil {
        w.logger.Warn("compensate: callback failed; retrying later",
            "order_id", p.OrderID, "tenant_id", t.ID, "error", err)
        w.store.IncrCallbackRetries(p.OrderID)
        continue
    }
    w.store.MarkCallbackSuccess(p.OrderID)
}
```

(Adapt the exact PaidAt formatting to what the existing code uses; the existing payload construction is the source of truth.)

- [ ] **Step 2: Update compensate_test.go**

The existing test seeds payments and asserts callbacks. After this PR, it must also seed a tenant and set its callback_url to the test stub. The pattern mirrors the stripe_test changes from Task 7.

Specifically:
- In `compensate_test.go`'s setup helper, before any `InsertOrGetPayment`, seed a tenant; capture its `ID`; set `payment.TenantID` to it.
- Update the tenant's `callback_url` to point at the test's httptest server.
- Assertions on retry count / success count stay the same.

- [ ] **Step 3: Run tests**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./internal/compensate -v -count=1
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add services/payserver/internal/compensate/
git commit -m "feat(payserver/compensate): per-tenant callback target lookup; mark failed on missing tenant"
```

---

## Task 9: main.go wiring + drop dead config fields

**Files:**
- Modify: `services/payserver/cmd/payserver/main.go`
- Modify: `services/payserver/internal/server/routes.go` (remove `APIKey` field from `Config`)
- Modify: `services/payserver/internal/config/config.go` — leave `APIKey` and `CallbackConfig` fields present (used by migration 002 bootstrap); rename `APIKey` deprecation in the doc comment.

**Interfaces:**
- Consumes: Tasks 5, 6, 7, 8
- Produces: payserver binary that:
  - Constructs `CallbackClient` with only `cfg.Callback.Timeout`
  - Passes `cfg.Store` to `server.Config` (not `cfg.APIKey`)
  - Doesn't blow up on `PAYSERVER_API_KEY` env (it's just ignored)
  - On startup, if `PAYSERVER_API_KEY` is set, log a one-line warn: "PAYSERVER_API_KEY is deprecated and ignored; use per-tenant credentials via the admin UI"

- [ ] **Step 1: Update main.go wiring**

In `services/payserver/cmd/payserver/main.go`, find the `notifyPkg.NewCallbackClient(...)` call and replace:

```go
	callbackClient := notifyPkg.NewCallbackClient(cfg.Callback.Timeout)
```

Find the `server.NewRouter(server.Config{...})` call and drop the `APIKey:` line:

```go
	router := server.NewRouter(server.Config{
		Store:        st,
		Gateways:     gateways,
		WeChatNotify: wechatNotify,
		AlipayNotify: alipayNotify,
		StripeNotify: stripeNotify,
		Logger:       logger,
	})
```

Add the deprecation warning near config validation:

```go
	if cfg.APIKey != "" {
		logger.Warn("PAYSERVER_API_KEY / cfg.api_key is deprecated and ignored; manage credentials per-tenant via the admin UI")
	}
```

Delete the old `if cfg.APIKey == "" { log.Fatal(...) }` block — it no longer gates startup.

- [ ] **Step 2: Update routes.go Config**

In `services/payserver/internal/server/routes.go`, remove `APIKey string` from the `Config` struct.

- [ ] **Step 3: Build + run all tests**

```bash
cd services/payserver && go build ./...
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
go test ./... -count=1
```

Expected: build green; all tests pass.

- [ ] **Step 4: Commit**

```bash
git add services/payserver/cmd/payserver/main.go services/payserver/internal/server/routes.go
git commit -m "chore(payserver): drop dead cfg.APIKey wiring; new CallbackClient signature

PAYSERVER_API_KEY is now ignored (warn on startup). cfg.Callback.* still
exist because migration 002 reads them when bootstrapping the default
tenant on first apply."
```

---

## Phase 4 — Admin API + OIDC

## Task 10: OIDC config + session signing helpers

**Files:**
- Modify: `services/payserver/internal/config/config.go` (add OIDCConfig)
- Create: `services/payserver/internal/server/session.go` (cookie HMAC sign/verify)
- Create: `services/payserver/internal/server/session_test.go`
- Modify: `services/payserver/go.mod` (`github.com/coreos/go-oidc/v3`, `golang.org/x/oauth2`)

**Interfaces:**
- Consumes: Task 1 (viper config)
- Produces:
  - `type config.OIDCConfig struct { IssuerURL, ClientID, ClientSecret, RedirectURL string; Scopes []string; AllowedEmails []string; SessionSecret string }`
  - `type AdminSession struct { Email, Name string; ExpiresAt time.Time }`
  - `func EncodeSession(s AdminSession, secret []byte) (string, error)` — base64(json) + ".") + HMAC-SHA256 hex
  - `func DecodeSession(token string, secret []byte) (*AdminSession, error)` — verifies HMAC, decodes, checks expiry
  - `func NewSessionCookie(value string, maxAge time.Duration) *http.Cookie` — `payserver_admin_session`, HttpOnly + Secure + SameSite=Lax

- [ ] **Step 1: Add OIDC deps**

```bash
cd services/payserver && go get github.com/coreos/go-oidc/v3@latest golang.org/x/oauth2@latest
```

- [ ] **Step 2: Extend config.go**

In `services/payserver/internal/config/config.go`:

```go
type Config struct {
	// ... existing ...
	OIDC     OIDCConfig     `mapstructure:"oidc"      yaml:"oidc"`
}

type OIDCConfig struct {
	IssuerURL     string   `mapstructure:"issuer_url"     yaml:"issuer_url"`
	ClientID      string   `mapstructure:"client_id"      yaml:"client_id"`
	ClientSecret  string   `mapstructure:"client_secret"  yaml:"client_secret"`
	RedirectURL   string   `mapstructure:"redirect_url"   yaml:"redirect_url"`
	Scopes        []string `mapstructure:"scopes"         yaml:"scopes"`
	AllowedEmails []string `mapstructure:"allowed_emails" yaml:"allowed_emails"`
	SessionSecret string   `mapstructure:"session_secret" yaml:"session_secret"`
}
```

In `Load()`, the BindEnv list grows:

```go
"oidc.issuer_url",
"oidc.client_id",
"oidc.client_secret",
"oidc.redirect_url",
"oidc.scopes",
"oidc.allowed_emails",
"oidc.session_secret",
```

Default scopes `["openid", "profile", "email"]` when empty (apply after Unmarshal):

```go
if len(cfg.OIDC.Scopes) == 0 {
    cfg.OIDC.Scopes = []string{"openid", "profile", "email"}
}
```

Note: viper reads comma-separated env vars into `[]string` automatically via mapstructure.

- [ ] **Step 3: Write the failing session test**

Create `services/payserver/internal/server/session_test.go`:

```go
package server

import (
	"testing"
	"time"
)

func TestSession_Roundtrip(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	s := AdminSession{
		Email:     "ops@example.com",
		Name:      "Ops User",
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second),
	}
	token, err := EncodeSession(s, secret)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, err := DecodeSession(token, secret)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Email != s.Email || got.Name != s.Name {
		t.Errorf("got %+v", got)
	}
	if !got.ExpiresAt.Equal(s.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, s.ExpiresAt)
	}
}

func TestSession_TamperedRejected(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	s := AdminSession{Email: "a@b.c", ExpiresAt: time.Now().Add(time.Hour)}
	token, _ := EncodeSession(s, secret)
	// Flip a byte in the middle
	tampered := token[:len(token)/2] + "X" + token[len(token)/2+1:]
	_, err := DecodeSession(tampered, secret)
	if err == nil {
		t.Error("tampered token decoded without error")
	}
}

func TestSession_WrongSecretRejected(t *testing.T) {
	s := AdminSession{Email: "a@b.c", ExpiresAt: time.Now().Add(time.Hour)}
	token, _ := EncodeSession(s, []byte("secret-A-padded-to-32-byteslong!"))
	_, err := DecodeSession(token, []byte("secret-B-padded-to-32-byteslong!"))
	if err == nil {
		t.Error("decoded with wrong secret")
	}
}

func TestSession_ExpiredRejected(t *testing.T) {
	secret := []byte("test-session-secret-32-bytes-min")
	s := AdminSession{Email: "a@b.c", ExpiresAt: time.Now().Add(-time.Minute)}
	token, _ := EncodeSession(s, secret)
	_, err := DecodeSession(token, secret)
	if err == nil {
		t.Error("expired token accepted")
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/server -run TestSession_ -v`
Expected: FAIL — package undefined.

- [ ] **Step 5: Implement session.go**

Create `services/payserver/internal/server/session.go`:

```go
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
```

- [ ] **Step 6: Run tests**

Run: `cd services/payserver && go test ./internal/server -run TestSession_ -v -count=1`
Expected: 4/4 PASS.

- [ ] **Step 7: Commit**

```bash
git add services/payserver/internal/config/config.go \
        services/payserver/internal/server/session.go \
        services/payserver/internal/server/session_test.go \
        services/payserver/go.mod services/payserver/go.sum
git commit -m "feat(payserver/server): OIDC config + HMAC-signed admin session cookies"
```

---

## Task 11: OIDC handlers — /login, /callback, /logout, /whoami, middleware

**Files:**
- Create: `services/payserver/internal/server/oidc.go`
- Create: `services/payserver/internal/server/oidc_test.go` (middleware only — full OIDC dance is too heavy for unit tests)
- Modify: `services/payserver/internal/server/routes.go` (mount `/admin/{login,callback,logout,whoami}` + `oidcRequiredMiddleware`)
- Modify: `services/payserver/cmd/payserver/main.go` (build the OIDC provider on startup)

**Interfaces:**
- Consumes: Task 10 (session helpers)
- Produces:
  - `type OIDCAuth struct { ... }` with methods `LoginHandler`, `CallbackHandler`, `LogoutHandler`, `WhoamiHandler`, `RequireSession(http.Handler) http.Handler`
  - `func NewOIDCAuth(ctx context.Context, cfg config.OIDCConfig) (*OIDCAuth, error)` — wires the go-oidc provider + oauth2 config; validates allowed_emails as set; returns error on missing required fields
  - Routes:
    - `GET /admin/login` → 302 to IdP authorize endpoint, state cookie set
    - `GET /admin/callback` → exchange code, verify ID token, check allowed_emails, set session cookie, 302 `/admin/`
    - `POST /admin/logout` → clear cookie, 302 `/admin/login`
    - `GET /admin/whoami` → JSON `{"email","name"}` (200) or 401

- [ ] **Step 1: Write a minimal middleware test**

Create `services/payserver/internal/server/oidc_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/server -run "TestRequireSession_|TestWhoami_" -v`
Expected: FAIL — OIDCAuth undefined.

- [ ] **Step 3: Implement oidc.go**

Create `services/payserver/internal/server/oidc.go`:

```go
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
		Name: oauthStateCookie, Value: state, Path: "/admin", HttpOnly: true,
		Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 600,
	})
	http.Redirect(w, r, a.oauth2Cfg.AuthCodeURL(state), http.StatusFound)
}

func (a *OIDCAuth) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(oauthStateCookie)
	if err != nil || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	// Clear state cookie
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
		Email: claims.Email, Name: claims.Name,
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
		Name: adminSessionCookieName, Path: "/admin", MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
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
```

- [ ] **Step 4: Wire OIDC routes in routes.go**

In `services/payserver/internal/server/routes.go`, add to `Config`:

```go
type Config struct {
    Store        *store.Store
    Gateways     map[string]gateway.Gateway
    WeChatNotify *notify.WeChatNotifyHandler
    AlipayNotify *notify.AlipayNotifyHandler
    StripeNotify *notify.StripeNotifyHandler
    OIDCAuth     *OIDCAuth   // nil = admin disabled
    Logger       *slog.Logger
}
```

In `NewRouter`, mount `/admin/*` block only when `cfg.OIDCAuth != nil`:

```go
if cfg.OIDCAuth != nil {
    r.Route("/admin", func(r chi.Router) {
        r.Get("/login", cfg.OIDCAuth.LoginHandler)
        r.Get("/callback", cfg.OIDCAuth.CallbackHandler)
        r.Post("/logout", cfg.OIDCAuth.LogoutHandler)
        // Everything else under /admin requires a session.
        r.Group(func(r chi.Router) {
            r.Use(cfg.OIDCAuth.RequireSession)
            r.Get("/whoami", cfg.OIDCAuth.WhoamiHandler)
            // Tenant + payment CRUD routes mounted in Task 12.
        })
    })
}
```

- [ ] **Step 5: Wire OIDC in main.go**

In `services/payserver/cmd/payserver/main.go`, before the server.NewRouter call:

```go
var oidcAuth *server.OIDCAuth
if cfg.OIDC.IssuerURL != "" {
    oidcAuth, err = server.NewOIDCAuth(ctx, cfg.OIDC, logger)
    if err != nil {
        log.Fatalf("oidc init: %v", err)
    }
    logger.Info("oidc enabled", "issuer", cfg.OIDC.IssuerURL)
}
```

Add `OIDCAuth: oidcAuth` to the server.Config literal.

- [ ] **Step 6: Run tests**

```bash
cd services/payserver && go test ./internal/server -run "TestRequireSession_|TestWhoami_|TestSession_" -v -count=1
```
Expected: 7/7 PASS.

```bash
go build ./...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add services/payserver/internal/server/oidc.go \
        services/payserver/internal/server/oidc_test.go \
        services/payserver/internal/server/routes.go \
        services/payserver/cmd/payserver/main.go \
        services/payserver/go.mod services/payserver/go.sum
git commit -m "feat(payserver/server): OIDC code-grant flow + RequireSession middleware"
```

---

## Task 12: Admin tenant CRUD handlers

**Files:**
- Create: `services/payserver/internal/server/admin_handler.go`
- Create: `services/payserver/internal/server/admin_handler_test.go`
- Modify: `services/payserver/internal/server/routes.go` (mount tenant + payments routes under `/admin`)

**Interfaces:**
- Consumes: Task 4 (`Store.{Create,Get,List,Update,Rotate,Delete}Tenant`), Task 2 (`tenant.GenerateSecret`, `tenant.HashSecret`), Task 11 (`RequireSession` middleware)
- Produces:
  - `func handleListTenants(st *store.Store) http.HandlerFunc`
  - `func handleCreateTenant(st *store.Store) http.HandlerFunc`
  - `func handleGetTenant(st *store.Store) http.HandlerFunc`
  - `func handleUpdateTenant(st *store.Store) http.HandlerFunc`
  - `func handleDeleteTenant(st *store.Store) http.HandlerFunc`
  - `func handleRotateTenantSecret(st *store.Store) http.HandlerFunc`
  - `func handleListPayments(st *store.Store) http.HandlerFunc`
  - `func handleGetPayment(st *store.Store) http.HandlerFunc`

- [ ] **Step 1: Write the failing test (CRUD shape)**

Create `services/payserver/internal/server/admin_handler_test.go`:

```go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

func adminTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbURL := os.Getenv("PAYSERVER_TEST_DB_URL")
	if dbURL == "" {
		t.Skip("set PAYSERVER_TEST_DB_URL")
	}
	hash, _ := tenant.HashSecret("default-test-secret")
	st, err := store.New(dbURL, slog.New(slog.NewTextHandler(io.Discard, nil)),
		store.MigrationBootstrap{
			DefaultTenantSecretHash: hash,
			DefaultCallbackURL:      "https://test.example/webhook",
			DefaultCallbackSecret:   "test",
		})
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func adminRouter(st *store.Store) http.Handler {
	r := chi.NewRouter()
	r.Get("/admin/tenants", handleListTenants(st))
	r.Post("/admin/tenants", handleCreateTenant(st))
	r.Get("/admin/tenants/{id}", handleGetTenant(st))
	r.Patch("/admin/tenants/{id}", handleUpdateTenant(st))
	r.Delete("/admin/tenants/{id}", handleDeleteTenant(st))
	r.Post("/admin/tenants/{id}/rotate-secret", handleRotateTenantSecret(st))
	r.Get("/admin/payments", handleListPayments(st))
	r.Get("/admin/payments/{id}", handleGetPayment(st))
	return r
}

func TestAdminCreateTenant_ServerGeneratesSecret(t *testing.T) {
	st := adminTestStore(t)
	body := map[string]any{
		"name":            "create-test-" + t.Name(),
		"callback_url":    "https://x.example/cb",
		"callback_secret": "cb-secret",
		"description":     "test",
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b)).WithContext(context.Background())
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("code = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Tenant struct {
			ID         string `json:"id"`
			SecretHash string `json:"secret_hash"` // must be empty (json:"-")
		} `json:"tenant"`
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Secret == "" {
		t.Error("response did not include cleartext secret")
	}
	if len(resp.Secret) != 43 {
		t.Errorf("secret length = %d, want 43 (32 bytes base64)", len(resp.Secret))
	}
	if resp.Tenant.SecretHash != "" {
		t.Errorf("response leaked secret_hash: %q", resp.Tenant.SecretHash)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, resp.Tenant.ID)
	})
}

func TestAdminCreateTenant_NameRequired(t *testing.T) {
	st := adminTestStore(t)
	b, _ := json.Marshal(map[string]any{"name": ""})
	req := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b))
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestAdminCreateTenant_DuplicateName409(t *testing.T) {
	st := adminTestStore(t)
	body := map[string]any{"name": "dup-" + t.Name()}
	b, _ := json.Marshal(body)
	r1 := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b))
	w1 := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w1, r1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("first create code = %d", w1.Code)
	}
	var resp struct{ Tenant struct{ ID string `json:"id"` } `json:"tenant"` }
	_ = json.NewDecoder(w1.Body).Decode(&resp)
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, resp.Tenant.ID)
	})

	r2 := httptest.NewRequest("POST", "/admin/tenants", bytes.NewReader(b))
	w2 := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w2, r2)
	if w2.Code != http.StatusConflict {
		t.Fatalf("second create code = %d, want 409", w2.Code)
	}
}

func TestAdminUpdateTenant_NameIgnored(t *testing.T) {
	st := adminTestStore(t)
	tt := &tenant.Tenant{Name: "upd-test-" + t.Name(), SecretHash: "$2a$10$dummy", IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	b, _ := json.Marshal(map[string]any{
		"name":         "renamed",      // must be ignored
		"description":  "updated desc", // allowed
	})
	req := httptest.NewRequest("PATCH", "/admin/tenants/"+tt.ID, bytes.NewReader(b))
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", w.Code, w.Body.String())
	}
	got, _ := st.GetTenantByID(tt.ID)
	if got.Name == "renamed" {
		t.Error("name was modified by PATCH (should be immutable)")
	}
	if got.Description != "updated desc" {
		t.Errorf("description = %q", got.Description)
	}
}

func TestAdminRotateSecret_OldSecretFails(t *testing.T) {
	st := adminTestStore(t)
	oldHash, _ := tenant.HashSecret("old-secret")
	tt := &tenant.Tenant{Name: "rot-" + t.Name(), SecretHash: oldHash, IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	req := httptest.NewRequest("POST", "/admin/tenants/"+tt.ID+"/rotate-secret", nil)
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", w.Code, w.Body.String())
	}
	var resp struct{ Secret string `json:"secret"` }
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp.Secret == "" {
		t.Fatal("rotate response missing new secret")
	}

	got, _ := st.GetTenantByID(tt.ID)
	if !tenant.VerifySecret(got.SecretHash, resp.Secret) {
		t.Error("new secret doesn't verify against stored hash")
	}
	if tenant.VerifySecret(got.SecretHash, "old-secret") {
		t.Error("old secret still works (rotation failed)")
	}
}

func TestAdminDeleteTenant_409OnPayments(t *testing.T) {
	st := adminTestStore(t)
	tt := &tenant.Tenant{Name: "del-" + t.Name(), SecretHash: "$2a$10$dummy", IsActive: true}
	if err := st.CreateTenant(tt); err != nil {
		t.Fatalf("seed: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM payments WHERE tenant_id = $1`, tt.ID)
		_, _ = st.Pool().Exec(t.Context(), `DELETE FROM tenants WHERE id = $1`, tt.ID)
	})

	if _, err := st.Pool().Exec(t.Context(), `
		INSERT INTO payments (tenant_id, order_id, channel, amount, status)
		VALUES ($1, $2, 'wechat', 1, 'pending')`, tt.ID, "del-test-"+t.Name()); err != nil {
		t.Fatalf("seed payment: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/admin/tenants/"+tt.ID, nil)
	w := httptest.NewRecorder()
	adminRouter(st).ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("code = %d, want 409", w.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd services/payserver && go test ./internal/server -run TestAdmin -v -count=1`
Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement admin_handler.go**

Create `services/payserver/internal/server/admin_handler.go`:

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/modelserver/modelserver/services/payserver/internal/store"
	"github.com/modelserver/modelserver/services/payserver/internal/tenant"
)

func handleListTenants(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := pagination(r)
		rows, total, err := st.ListTenants(limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": rows,
			"meta":  map[string]any{"total": total, "limit": limit, "offset": offset},
		})
	}
}

func handleCreateTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name           string `json:"name"`
			CallbackURL    string `json:"callback_url"`
			CallbackSecret string `json:"callback_secret"`
			Description    string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		if body.Name == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name is required"})
			return
		}
		if body.CallbackURL != "" {
			if err := validateReturnURL(body.CallbackURL); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		secret, err := tenant.GenerateSecret()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate secret"})
			return
		}
		hash, err := tenant.HashSecret(secret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash secret"})
			return
		}
		t := &tenant.Tenant{
			Name: body.Name, SecretHash: hash,
			CallbackURL: body.CallbackURL, CallbackSecret: body.CallbackSecret,
			Description: body.Description, IsActive: true,
		}
		if err := st.CreateTenant(t); err != nil {
			if errors.Is(err, store.ErrTenantNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "name already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"tenant": t,
			"secret": secret,
		})
	}
}

func handleGetTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, err := st.GetTenantByID(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}
		if t == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"tenant": t})
	}
}

func handleUpdateTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		// Filter to allowed fields silently. UpdateTenant returns error on
		// unknown keys; we keep the client-facing surface forgiving by
		// dropping them here, but the store still enforces the whitelist
		// internally so any bypass would fail there too.
		filtered := map[string]any{}
		for _, k := range []string{"callback_url", "callback_secret", "description", "is_active"} {
			if v, ok := body[k]; ok {
				filtered[k] = v
			}
		}
		if cb, ok := filtered["callback_url"].(string); ok && cb != "" {
			if err := validateReturnURL(cb); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
				return
			}
		}
		if err := st.UpdateTenant(id, filtered); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		t, _ := st.GetTenantByID(id)
		writeJSON(w, http.StatusOK, map[string]any{"tenant": t})
	}
}

func handleDeleteTenant(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		err := st.DeleteTenant(id)
		if errors.Is(err, store.ErrTenantHasPayments) {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "tenant has payments; deactivate via PATCH is_active=false instead",
			})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleRotateTenantSecret(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		t, err := st.GetTenantByID(id)
		if err != nil || t == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
			return
		}
		secret, err := tenant.GenerateSecret()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "generate"})
			return
		}
		hash, err := tenant.HashSecret(secret)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "hash"})
			return
		}
		if err := st.RotateTenantSecret(id, hash); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "rotate failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"secret": secret})
	}
}

func handleListPayments(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, offset := pagination(r)
		filters := store.PaymentFilters{
			TenantID: r.URL.Query().Get("tenant_id"),
			Status:   r.URL.Query().Get("status"),
			Channel:  r.URL.Query().Get("channel"),
		}
		rows, total, err := st.ListPayments(limit, offset, filters)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": rows,
			"meta":  map[string]any{"total": total, "limit": limit, "offset": offset},
		})
	}
}

func handleGetPayment(st *store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		p, err := st.GetPaymentByID(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup failed"})
			return
		}
		if p == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "payment not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"payment": p})
	}
}

func pagination(r *http.Request) (limit, offset int) {
	limit = 50
	offset = 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 && n <= 200 {
			limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, _ := strconv.Atoi(v); n >= 0 {
			offset = n
		}
	}
	return
}
```

- [ ] **Step 4: Add ListPayments to the store**

In `services/payserver/internal/store/payments.go`, add:

```go
type PaymentFilters struct {
	TenantID string
	Status   string
	Channel  string
}

func (s *Store) ListPayments(limit, offset int, f PaymentFilters) ([]Payment, int, error) {
	ctx := context.Background()
	where := "WHERE 1=1"
	args := []any{}
	i := 1
	if f.TenantID != "" {
		where += fmt.Sprintf(" AND tenant_id = $%d", i)
		args = append(args, f.TenantID)
		i++
	}
	if f.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", i)
		args = append(args, f.Status)
		i++
	}
	if f.Channel != "" {
		where += fmt.Sprintf(" AND channel = $%d", i)
		args = append(args, f.Channel)
		i++
	}

	var total int
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM payments `+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count payments: %w", err)
	}

	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, limit, offset)
	rows, err := s.pool.Query(ctx,
		`SELECT id, tenant_id, order_id, channel, trade_no, payment_url, amount, status,
			callback_status, callback_retries, raw_notify, paid_at, created_at, updated_at
		FROM payments `+where+
			fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, i, i+1),
		queryArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()

	var out []Payment
	for rows.Next() {
		var p Payment
		if err := rows.Scan(&p.ID, &p.TenantID, &p.OrderID, &p.Channel, &p.TradeNo,
			&p.PaymentURL, &p.Amount, &p.Status, &p.CallbackStatus, &p.CallbackRetries,
			&p.RawNotify, &p.PaidAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, 0, fmt.Errorf("scan payment: %w", err)
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}
```

- [ ] **Step 5: Mount the routes**

In `services/payserver/internal/server/routes.go`, inside the `RequireSession` group from Task 11:

```go
r.Group(func(r chi.Router) {
    r.Use(cfg.OIDCAuth.RequireSession)
    r.Get("/whoami", cfg.OIDCAuth.WhoamiHandler)

    r.Get("/tenants", handleListTenants(cfg.Store))
    r.Post("/tenants", handleCreateTenant(cfg.Store))
    r.Get("/tenants/{id}", handleGetTenant(cfg.Store))
    r.Patch("/tenants/{id}", handleUpdateTenant(cfg.Store))
    r.Delete("/tenants/{id}", handleDeleteTenant(cfg.Store))
    r.Post("/tenants/{id}/rotate-secret", handleRotateTenantSecret(cfg.Store))

    r.Get("/payments", handleListPayments(cfg.Store))
    r.Get("/payments/{id}", handleGetPayment(cfg.Store))
})
```

- [ ] **Step 6: Run tests**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./internal/server -run TestAdmin -v -count=1
```
Expected: 6/6 PASS.

```bash
cd services/payserver && go build ./...
```
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add services/payserver/internal/server/admin_handler.go \
        services/payserver/internal/server/admin_handler_test.go \
        services/payserver/internal/server/routes.go \
        services/payserver/internal/store/payments.go
git commit -m "feat(payserver/server): admin tenant + payments CRUD handlers"
```

---

## Task 13: OIDC rescue subcommand

**Files:**
- Modify: `services/payserver/cmd/payserver/main.go`
- Create: `services/payserver/cmd/payserver/rescue_test.go` (unit test for the subcommand logic)

**Interfaces:**
- Consumes: Task 10 (`EncodeSession`)
- Produces: `payserver admin rescue --email <addr>` prints a 1-hour signed session cookie to stdout. Uses `PAYSERVER_OIDC_SESSION_SECRET` from env (not from config; subcommand path is meant to run when config might be unparseable).

- [ ] **Step 1: Restructure main.go to detect subcommand**

In `services/payserver/cmd/payserver/main.go`, replace the top of `main()`:

```go
func main() {
	// Subcommand dispatcher: `payserver admin rescue --email <addr>`
	if len(os.Args) >= 3 && os.Args[1] == "admin" && os.Args[2] == "rescue" {
		runRescue(os.Args[3:])
		return
	}
	runServer()
}

func runRescue(args []string) {
	fs := flag.NewFlagSet("rescue", flag.ExitOnError)
	email := fs.String("email", "", "operator email to encode into the session")
	ttl := fs.Duration("ttl", time.Hour, "session lifetime")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *email == "" {
		fmt.Fprintln(os.Stderr, "rescue: --email is required")
		os.Exit(2)
	}
	secret := os.Getenv("PAYSERVER_OIDC_SESSION_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "rescue: PAYSERVER_OIDC_SESSION_SECRET is required")
		os.Exit(2)
	}
	sess := server.AdminSession{
		Email:     *email,
		Name:      *email,
		ExpiresAt: time.Now().Add(*ttl),
	}
	token, err := server.EncodeSession(sess, []byte(secret))
	if err != nil {
		fmt.Fprintf(os.Stderr, "rescue: encode session: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("issued rescue session for=%s ttl=%s\n", *email, *ttl)
	fmt.Println("set this cookie on your /admin/* domain to bypass OIDC:")
	fmt.Printf("  payserver_admin_session=%s\n", token)
	fmt.Fprintf(os.Stderr, "audit: rescue session issued for=%s ttl=%s pid=%d\n", *email, *ttl, os.Getpid())
}
```

Rename the existing body of `main()` (everything from "configPath := flag.String..." to the end) into a function `runServer()`. The `fmt`, `os/exec`, `time` imports should already be there.

- [ ] **Step 2: Write a smoke test**

Create `services/payserver/cmd/payserver/rescue_test.go`:

```go
package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestRescue_HappyPath compiles the binary and runs `admin rescue` to
// confirm the subcommand parses args + prints a cookie line. The encoded
// token's verifiability is already tested in internal/server.
func TestRescue_HappyPath(t *testing.T) {
	bin := t.TempDir() + "/payserver-test"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	c := exec.Command(bin, "admin", "rescue", "--email", "test@example.com", "--ttl", "1h")
	c.Env = append(os.Environ(), "PAYSERVER_OIDC_SESSION_SECRET=test-secret-32-bytes-padded-okay!")
	out, err := c.Output()
	if err != nil {
		t.Fatalf("rescue exec: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "payserver_admin_session=") {
		t.Errorf("output missing cookie line:\n%s", s)
	}
}
```

- [ ] **Step 3: Run test**

```bash
cd services/payserver && go test ./cmd/payserver -run TestRescue_ -v
```
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add services/payserver/cmd/payserver/main.go services/payserver/cmd/payserver/rescue_test.go
git commit -m "feat(payserver): admin rescue subcommand issues bypass session cookie"
```

---

## Task 14: Phase 4 sweep — full payserver tests + smoke against real OIDC stub

**Files:** none (verification gate)

- [ ] **Step 1: Full sweep**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./... -count=1
```
Expected: all packages green.

- [ ] **Step 2: Manual whoami smoke (with rescue)**

```bash
export PAYSERVER_OIDC_SESSION_SECRET="$(openssl rand -base64 32)"
# Need OIDC env even though we'll bypass — RequireSession needs the cookie validator wired
export PAYSERVER_OIDC_ISSUER_URL="https://example.com" # bogus is fine; rescue doesn't dial issuer
export PAYSERVER_OIDC_CLIENT_ID="x"
export PAYSERVER_OIDC_CLIENT_SECRET="y"
export PAYSERVER_OIDC_REDIRECT_URL="https://example.com/admin/callback"
# Issuer discover will fail. For smoke we skip the server, just confirm CLI works:
go run ./cmd/payserver admin rescue --email ops@example.com --ttl 5m
# Expected: prints "payserver_admin_session=..."
```

For a real OIDC test you'll need an actual issuer. That's the Phase-5 manual smoke step.

- [ ] **Step 3: Commit**

No code changes. Phase 4 done.

---

## Phase 5 — Admin Frontend

## Task 15: Scaffold React+Vite+TS admin app + embed plumbing

**Files:**
- Create: `services/payserver/admin/package.json`
- Create: `services/payserver/admin/pnpm-lock.yaml` (generated)
- Create: `services/payserver/admin/vite.config.ts`
- Create: `services/payserver/admin/tsconfig.json`
- Create: `services/payserver/admin/tsconfig.node.json`
- Create: `services/payserver/admin/index.html`
- Create: `services/payserver/admin/postcss.config.js`
- Create: `services/payserver/admin/tailwind.config.js`
- Create: `services/payserver/admin/components.json` (shadcn config)
- Create: `services/payserver/admin/src/main.tsx`
- Create: `services/payserver/admin/src/App.tsx`
- Create: `services/payserver/admin/src/index.css`
- Create: `services/payserver/admin/src/lib/utils.ts`
- Create: `services/payserver/admin/src/api/client.ts`
- Create: `services/payserver/admin/src/api/tenants.ts`
- Create: `services/payserver/admin/src/api/payments.ts`
- Create: `services/payserver/admin/src/api/types.ts`
- Create: `services/payserver/admin/src/components/AppShell.tsx`
- Create: `services/payserver/admin/src/components/SecretRevealOnce.tsx`
- Create: `services/payserver/admin/src/pages/TenantsPage.tsx` (stub: shows "Tenants page TODO")
- Create: `services/payserver/admin/src/pages/TenantDetailPage.tsx` (stub)
- Create: `services/payserver/admin/src/pages/PaymentsPage.tsx` (stub)
- Create: `services/payserver/admin/.gitignore`
- Create: `services/payserver/.gitignore` (adds `admin/dist/` + `admin_dist/`)
- Create: `services/payserver/admin_dist/.gitkeep`
- Create: `services/payserver/admin_dist/index.html` (minimal stub so `go:embed` finds files on fresh checkouts)
- Modify: `services/payserver/cmd/payserver/main.go` (add `//go:embed admin_dist` + serve at `/admin/`)
- Modify: `services/payserver/internal/server/routes.go` (route `/admin/` and `/admin/assets/*` to embed FS; SPA fallback)
- Create: `services/payserver/Makefile`

**Interfaces:**
- Consumes: Task 11+12 (admin API routes exist)
- Produces:
  - `pnpm build` produces `admin/dist/`; Makefile copies → `admin_dist/`
  - `go:embed admin_dist` serves at `/admin/` + SPA fallback for `/admin/tenants`, `/admin/payments` etc.
  - Three stub pages exist (real implementation in Task 16-17)
  - AppShell renders Tenants / Payments nav links, top-right user email + Logout

The page implementations are deferred to Tasks 16-17 so the scaffold can land independently. After this task you can build payserver, hit `/admin/`, see an empty SPA shell, click `Tenants` and see "TODO" — proves the embed pipeline works.

- [ ] **Step 1: Initialize React+Vite+TS project**

```bash
cd services/payserver
mkdir -p admin
cd admin
pnpm init
pnpm add react react-dom react-router @tanstack/react-query
pnpm add -D typescript @types/react @types/react-dom @types/node vite @vitejs/plugin-react tailwindcss postcss autoprefixer
pnpm add class-variance-authority clsx tailwind-merge lucide-react sonner
# shadcn primitives are copy-in, no install
```

- [ ] **Step 2: Create config files (verbatim)**

`services/payserver/admin/package.json`:

```json
{
  "name": "payserver-admin",
  "private": true,
  "version": "0.1.0",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc -b && vite build",
    "preview": "vite preview"
  }
}
```

`services/payserver/admin/vite.config.ts`:

```ts
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import path from "node:path";

export default defineConfig({
  plugins: [react()],
  base: "/admin/",
  server: {
    port: 5174,
    proxy: {
      "/admin/api": "http://localhost:8090",
      "/admin/login": "http://localhost:8090",
      "/admin/callback": "http://localhost:8090",
      "/admin/logout": "http://localhost:8090",
      "/admin/whoami": "http://localhost:8090",
    },
  },
  resolve: {
    alias: { "@": path.resolve(__dirname, "src") },
  },
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});
```

`services/payserver/admin/tsconfig.json`:

```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "noUnusedLocals": true,
    "noUnusedParameters": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "isolatedModules": true,
    "noEmit": true,
    "baseUrl": ".",
    "paths": { "@/*": ["src/*"] }
  },
  "include": ["src"],
  "references": [{ "path": "./tsconfig.node.json" }]
}
```

`services/payserver/admin/tsconfig.node.json`:

```json
{
  "compilerOptions": {
    "composite": true,
    "module": "ESNext",
    "moduleResolution": "bundler",
    "allowSyntheticDefaultImports": true,
    "skipLibCheck": true
  },
  "include": ["vite.config.ts"]
}
```

`services/payserver/admin/index.html`:

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Payserver Admin</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/admin/src/main.tsx"></script>
  </body>
</html>
```

`services/payserver/admin/tailwind.config.js`:

```js
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: {} },
  plugins: [],
};
```

`services/payserver/admin/postcss.config.js`:

```js
export default {
  plugins: { tailwindcss: {}, autoprefixer: {} },
};
```

`services/payserver/admin/src/index.css`:

```css
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  font-family: ui-sans-serif, system-ui, sans-serif;
}
```

`services/payserver/admin/src/lib/utils.ts`:

```ts
import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
```

`services/payserver/admin/src/main.tsx`:

```tsx
import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router";
import { Toaster } from "sonner";
import App from "./App";
import "./index.css";

const qc = new QueryClient({ defaultOptions: { queries: { staleTime: 30_000 } } });

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={qc}>
      <BrowserRouter basename="/admin">
        <App />
        <Toaster />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
);
```

`services/payserver/admin/src/App.tsx`:

```tsx
import { Routes, Route, Navigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { AppShell } from "@/components/AppShell";
import { TenantsPage } from "@/pages/TenantsPage";
import { TenantDetailPage } from "@/pages/TenantDetailPage";
import { PaymentsPage } from "@/pages/PaymentsPage";

export default function App() {
  const { data: who, isLoading, error } = useQuery({
    queryKey: ["whoami"],
    queryFn: async () => {
      const r = await fetch("/admin/whoami", { headers: { Accept: "application/json" } });
      if (r.status === 401) {
        window.location.href = "/admin/login";
        throw new Error("unauthenticated");
      }
      if (!r.ok) throw new Error(`whoami ${r.status}`);
      return r.json() as Promise<{ email: string; name: string }>;
    },
    retry: false,
  });

  if (isLoading) return <div className="p-6 text-sm text-muted-foreground">Loading…</div>;
  if (error) return <div className="p-6 text-sm text-destructive">Auth error: {String(error)}</div>;
  if (!who) return null;

  return (
    <Routes>
      <Route element={<AppShell email={who.email} />}>
        <Route index element={<Navigate to="/tenants" replace />} />
        <Route path="tenants" element={<TenantsPage />} />
        <Route path="tenants/:id" element={<TenantDetailPage />} />
        <Route path="payments" element={<PaymentsPage />} />
      </Route>
    </Routes>
  );
}
```

`services/payserver/admin/src/components/AppShell.tsx`:

```tsx
import { Link, Outlet, useLocation } from "react-router";
import { cn } from "@/lib/utils";

export function AppShell({ email }: { email: string }) {
  const loc = useLocation();
  const nav = [
    { to: "/tenants", label: "Tenants" },
    { to: "/payments", label: "Payments" },
  ];
  async function logout() {
    await fetch("/admin/logout", { method: "POST" });
    window.location.href = "/admin/login";
  }
  return (
    <div className="min-h-screen flex">
      <aside className="w-56 border-r p-4 space-y-1">
        <div className="text-sm font-semibold mb-3">Payserver Admin</div>
        {nav.map((n) => (
          <Link
            key={n.to}
            to={n.to}
            className={cn(
              "block rounded px-3 py-2 text-sm hover:bg-accent",
              loc.pathname.startsWith(n.to) && "bg-accent font-medium",
            )}
          >
            {n.label}
          </Link>
        ))}
      </aside>
      <div className="flex-1 flex flex-col">
        <header className="flex items-center justify-end gap-4 border-b px-6 py-2 text-sm">
          <span className="text-muted-foreground">{email}</span>
          <button onClick={logout} className="text-sm underline">Logout</button>
        </header>
        <main className="flex-1 p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
```

Stub pages (`services/payserver/admin/src/pages/{TenantsPage,TenantDetailPage,PaymentsPage}.tsx`):

```tsx
// TenantsPage.tsx
export function TenantsPage() { return <div>Tenants page — implemented in Task 16</div>; }
// TenantDetailPage.tsx
export function TenantDetailPage() { return <div>Tenant detail — implemented in Task 16</div>; }
// PaymentsPage.tsx
export function PaymentsPage() { return <div>Payments page — implemented in Task 17</div>; }
```

`services/payserver/admin/src/api/types.ts`:

```ts
export interface Tenant {
  id: string;
  name: string;
  callback_url: string;
  description: string;
  is_active: boolean;
  created_at: string;
  updated_at: string;
}

export interface Payment {
  id: string;
  tenant_id: string;
  order_id: string;
  channel: string;
  trade_no: string;
  payment_url: string;
  amount: number;
  status: string;
  callback_status: string;
  callback_retries: number;
  raw_notify: string | null;
  paid_at: string | null;
  created_at: string;
  updated_at: string;
}
```

`services/payserver/admin/src/api/client.ts`:

```ts
export async function adminFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const r = await fetch(`/admin${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", Accept: "application/json", ...(init?.headers ?? {}) },
  });
  if (r.status === 401) {
    window.location.href = "/admin/login";
    throw new Error("unauthenticated");
  }
  if (!r.ok) {
    let msg = `HTTP ${r.status}`;
    try {
      const body = await r.json();
      if (body?.error) msg = body.error;
    } catch {}
    throw new Error(msg);
  }
  return r.json() as Promise<T>;
}
```

`services/payserver/admin/src/api/tenants.ts` and `payments.ts`: stubbed empty for now (the queries land in Tasks 16-17).

```ts
// tenants.ts
import type { Tenant } from "./types";
import { adminFetch } from "./client";
export type CreateTenantInput = { name: string; callback_url: string; callback_secret: string; description: string };
export type CreateTenantResponse = { tenant: Tenant; secret: string };
```

```ts
// payments.ts
import type { Payment } from "./types";
import { adminFetch } from "./client";
export type ListPaymentsParams = { tenant_id?: string; status?: string; channel?: string; limit?: number; offset?: number };
```

`services/payserver/admin/src/components/SecretRevealOnce.tsx`: full implementation per spec §6. It's referenced by TenantsPage in Task 16; ship it here so Task 16 just consumes:

```tsx
import { useState } from "react";

export function SecretRevealOnce({
  secret, onAcknowledge,
}: { secret: string; onAcknowledge: () => void }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="w-[480px] space-y-4 rounded-md bg-background p-6 shadow-lg">
        <h2 className="text-lg font-semibold">Save this secret now</h2>
        <p className="text-sm text-muted-foreground">
          This is the only time the secret will be shown. Copy it and store it in the upstream's
          config. If you lose it, you'll need to rotate.
        </p>
        <pre className="overflow-x-auto break-all rounded border bg-muted p-3 font-mono text-sm">
          {secret}
        </pre>
        <div className="flex justify-end gap-2">
          <button
            onClick={async () => {
              await navigator.clipboard.writeText(secret);
              setCopied(true);
            }}
            className="rounded border px-3 py-1 text-sm"
          >
            {copied ? "Copied" : "Copy"}
          </button>
          <button
            disabled={!copied}
            onClick={onAcknowledge}
            className="rounded bg-primary px-3 py-1 text-sm text-primary-foreground disabled:opacity-50"
          >
            I've saved it
          </button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Add the .gitignores + admin_dist stub**

`services/payserver/admin/.gitignore`:

```
node_modules
dist
.vite
```

`services/payserver/.gitignore`:

```
admin/node_modules/
admin/dist/
admin_dist/
```

`services/payserver/admin_dist/.gitkeep`: empty file (the git-track for the dir).

`services/payserver/admin_dist/index.html`: minimal placeholder so a fresh checkout's `go build` doesn't fail with "pattern admin_dist: no matching files found":

```html
<!doctype html>
<html><body>admin_dist not built — run `make admin` in services/payserver/</body></html>
```

Wait — `.gitignore` excludes `admin_dist/` but we also need to track `index.html` and `.gitkeep` inside it. Use a force-include:

`services/payserver/.gitignore` (corrected):

```
admin/node_modules/
admin/dist/
admin_dist/*
!admin_dist/index.html
!admin_dist/.gitkeep
```

This way the stub `index.html` is tracked but real build output (assets etc) is gitignored.

- [ ] **Step 4: Wire the embed in Go**

In `services/payserver/cmd/payserver/main.go`, near the top after imports:

```go
//go:embed admin_dist
var adminDistFS embed.FS
```

Add `"embed"`, `"io/fs"`, `"net/http"` imports if not present.

Pass `adminDistFS` to `server.NewRouter` via a new Config field (or compute the sub-FS here):

```go
adminSubFS, err := fs.Sub(adminDistFS, "admin_dist")
if err != nil {
    log.Fatalf("admin sub-fs: %v", err)
}
```

Add `AdminDistFS fs.FS` to `server.Config` and assign `adminSubFS`. In `routes.go`, when mounting `/admin`:

```go
if cfg.AdminDistFS != nil {
    fileServer := http.FileServerFS(cfg.AdminDistFS)
    r.Get("/admin/assets/*", http.StripPrefix("/admin/", fileServer).ServeHTTP)
    // Any other static asset path (favicon, etc):
    r.Get("/admin/vite.svg", http.StripPrefix("/admin/", fileServer).ServeHTTP)

    // SPA: serve index.html for /admin/ and any unmatched /admin/* (except API/login routes).
    serveSPA := func(w http.ResponseWriter, r *http.Request) {
        f, err := cfg.AdminDistFS.Open("index.html")
        if err != nil {
            http.Error(w, "admin not built", http.StatusNotFound)
            return
        }
        defer f.Close()
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        _, _ = io.Copy(w, f)
    }
    r.Get("/admin", serveSPA)
    r.Get("/admin/", serveSPA)
    r.Get("/admin/tenants", serveSPA)
    r.Get("/admin/tenants/*", serveSPA)
    r.Get("/admin/payments", serveSPA)
    r.Get("/admin/payments/*", serveSPA)
}
```

The explicit route enumeration over a wildcard `/admin/*` keeps chi happy (chi's prefix matching can be picky around mixed wildcards + explicit handlers; enumerating is cheaper than debugging).

- [ ] **Step 5: Create the Makefile**

`services/payserver/Makefile`:

```makefile
.PHONY: admin admin_dist build all clean

admin:
	cd admin && pnpm install --frozen-lockfile && pnpm build

admin_dist: admin
	rm -rf admin_dist
	mkdir -p admin_dist
	cp -r admin/dist/* admin_dist/

build: admin_dist
	go build -o payserver ./cmd/payserver

all: build

clean:
	rm -rf admin/node_modules admin/dist admin_dist payserver
```

- [ ] **Step 6: First build + visual smoke**

```bash
cd services/payserver && make all
```

Expected: builds the binary. `./payserver --help` works.

```bash
./payserver
# Visit http://localhost:8090/admin/ — should redirect to /admin/login because session
# cookie isn't set. With rescue cookie set, AppShell renders with two stub pages.
```

- [ ] **Step 7: Commit**

```bash
git add services/payserver/admin/ services/payserver/admin_dist/ \
        services/payserver/.gitignore services/payserver/Makefile \
        services/payserver/cmd/payserver/main.go \
        services/payserver/internal/server/routes.go
git commit -m "feat(payserver/admin): React+Vite+TS scaffold + go:embed wiring

Empty SPA shell + stub pages + Makefile-driven build pipeline that pumps
admin/dist into admin_dist for go:embed. Real pages land in Tasks 16-17."
```

---

## Task 16: Tenants page + TenantDetail page

**Files:**
- Modify: `services/payserver/admin/src/pages/TenantsPage.tsx`
- Modify: `services/payserver/admin/src/pages/TenantDetailPage.tsx`
- Modify: `services/payserver/admin/src/api/tenants.ts`

**Interfaces:**
- Consumes: Task 15 (scaffold), Task 12 (admin tenant CRUD endpoints)
- Produces: list + create + detail-edit + rotate + delete UI

- [ ] **Step 1: tenants.ts API helpers**

Replace `services/payserver/admin/src/api/tenants.ts`:

```ts
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { adminFetch } from "./client";
import type { Tenant } from "./types";

export type CreateTenantInput = {
  name: string;
  callback_url: string;
  callback_secret: string;
  description: string;
};
export type CreateTenantResponse = { tenant: Tenant; secret: string };
export type RotateSecretResponse = { secret: string };

export function useTenants() {
  return useQuery({
    queryKey: ["tenants"],
    queryFn: () => adminFetch<{ items: Tenant[]; meta: { total: number } }>("/tenants"),
  });
}

export function useTenant(id: string) {
  return useQuery({
    queryKey: ["tenants", id],
    queryFn: () => adminFetch<{ tenant: Tenant }>(`/tenants/${id}`).then((r) => r.tenant),
    enabled: !!id,
  });
}

export function useCreateTenant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateTenantInput) =>
      adminFetch<CreateTenantResponse>("/tenants", { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tenants"] }),
  });
}

export function useUpdateTenant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, patch }: { id: string; patch: Partial<Tenant> & { callback_secret?: string } }) =>
      adminFetch<{ tenant: Tenant }>(`/tenants/${id}`, { method: "PATCH", body: JSON.stringify(patch) }),
    onSuccess: (_d, v) => {
      qc.invalidateQueries({ queryKey: ["tenants"] });
      qc.invalidateQueries({ queryKey: ["tenants", v.id] });
    },
  });
}

export function useDeleteTenant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => adminFetch<{}>(`/tenants/${id}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["tenants"] }),
  });
}

export function useRotateSecret() {
  return useMutation({
    mutationFn: (id: string) =>
      adminFetch<RotateSecretResponse>(`/tenants/${id}/rotate-secret`, { method: "POST" }),
  });
}
```

- [ ] **Step 2: TenantsPage**

Replace `services/payserver/admin/src/pages/TenantsPage.tsx`:

```tsx
import { useState } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import {
  useTenants, useCreateTenant, useDeleteTenant, useRotateSecret, useUpdateTenant,
} from "@/api/tenants";
import { SecretRevealOnce } from "@/components/SecretRevealOnce";

export function TenantsPage() {
  const { data, isLoading, error } = useTenants();
  const create = useCreateTenant();
  const del = useDeleteTenant();
  const rotate = useRotateSecret();
  const update = useUpdateTenant();

  const [dialogOpen, setDialogOpen] = useState(false);
  const [form, setForm] = useState({ name: "", callback_url: "", callback_secret: "", description: "" });
  const [secretReveal, setSecretReveal] = useState<string | null>(null);

  if (isLoading) return <div className="text-sm text-muted-foreground">Loading…</div>;
  if (error) return <div className="text-sm text-destructive">Error: {String(error)}</div>;

  const items = data?.items ?? [];

  async function submitCreate() {
    if (!form.name.trim()) {
      toast.error("Name is required");
      return;
    }
    try {
      const res = await create.mutateAsync(form);
      setDialogOpen(false);
      setForm({ name: "", callback_url: "", callback_secret: "", description: "" });
      setSecretReveal(res.secret);
    } catch (e: unknown) {
      toast.error(String((e as Error).message ?? e));
    }
  }

  async function handleRotate(id: string) {
    if (!confirm("Rotate this tenant's secret? The old secret will fail immediately.")) return;
    try {
      const res = await rotate.mutateAsync(id);
      setSecretReveal(res.secret);
    } catch (e: unknown) {
      toast.error(String((e as Error).message ?? e));
    }
  }

  async function handleDelete(id: string) {
    if (!confirm("Delete this tenant? Cannot be undone.")) return;
    try {
      await del.mutateAsync(id);
      toast.success("Tenant deleted");
    } catch (e: unknown) {
      const msg = String((e as Error).message ?? e);
      toast.error(msg);
      if (msg.includes("payments")) {
        if (confirm("Tenant has payments; deactivate instead?")) {
          try {
            await update.mutateAsync({ id, patch: { is_active: false } });
            toast.success("Tenant deactivated");
          } catch (e2: unknown) {
            toast.error(String((e2 as Error).message ?? e2));
          }
        }
      }
    }
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Tenants</h1>
        <button
          onClick={() => setDialogOpen(true)}
          className="rounded bg-primary px-3 py-1 text-sm text-primary-foreground"
        >
          + New Tenant
        </button>
      </div>

      <table className="w-full text-sm">
        <thead className="border-b">
          <tr>
            <th className="px-2 py-2 text-left">Name</th>
            <th className="px-2 py-2 text-left">Callback URL</th>
            <th className="px-2 py-2 text-left">Status</th>
            <th className="px-2 py-2 text-left">Created</th>
            <th className="px-2 py-2 text-right">Actions</th>
          </tr>
        </thead>
        <tbody>
          {items.map((t) => (
            <tr key={t.id} className="border-b hover:bg-accent/30">
              <td className="px-2 py-2">
                <Link to={`/tenants/${t.id}`} className="font-medium underline-offset-2 hover:underline">
                  {t.name}
                </Link>
              </td>
              <td className="px-2 py-2 truncate max-w-md text-muted-foreground">{t.callback_url || "—"}</td>
              <td className="px-2 py-2">
                <span className={`rounded px-2 py-0.5 text-xs ${t.is_active ? "bg-emerald-100 text-emerald-900" : "bg-zinc-100 text-zinc-700"}`}>
                  {t.is_active ? "active" : "inactive"}
                </span>
              </td>
              <td className="px-2 py-2 text-muted-foreground">{new Date(t.created_at).toLocaleDateString()}</td>
              <td className="px-2 py-2 text-right space-x-2">
                <button onClick={() => handleRotate(t.id)} className="text-xs underline">Rotate</button>
                <button onClick={() => handleDelete(t.id)} className="text-xs text-destructive underline">Delete</button>
              </td>
            </tr>
          ))}
          {items.length === 0 && (
            <tr><td colSpan={5} className="px-2 py-6 text-center text-muted-foreground">No tenants yet</td></tr>
          )}
        </tbody>
      </table>

      {dialogOpen && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-[480px] space-y-4 rounded-md bg-background p-6 shadow-lg">
            <h2 className="text-lg font-semibold">New Tenant</h2>
            <div className="space-y-3">
              <div>
                <label className="text-sm">Name <span className="text-destructive">*</span></label>
                <input className="w-full rounded border px-2 py-1 text-sm" value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="e.g. modelserver"/>
              </div>
              <div>
                <label className="text-sm">Callback URL</label>
                <input className="w-full rounded border px-2 py-1 text-sm" value={form.callback_url}
                  onChange={(e) => setForm({ ...form, callback_url: e.target.value })}
                  placeholder="https://yourapp.example/webhook"/>
              </div>
              <div>
                <label className="text-sm">Callback HMAC Secret</label>
                <input className="w-full rounded border px-2 py-1 text-sm font-mono" value={form.callback_secret}
                  onChange={(e) => setForm({ ...form, callback_secret: e.target.value })}
                  placeholder="shared with the upstream's verifier"/>
              </div>
              <div>
                <label className="text-sm">Description</label>
                <input className="w-full rounded border px-2 py-1 text-sm" value={form.description}
                  onChange={(e) => setForm({ ...form, description: e.target.value })}/>
              </div>
              <p className="text-xs text-muted-foreground">
                Use UUIDs as your order IDs. <code>payments.order_id</code> is globally unique;
                reusing across tenants returns 409.
              </p>
            </div>
            <div className="flex justify-end gap-2">
              <button onClick={() => setDialogOpen(false)} className="rounded border px-3 py-1 text-sm">Cancel</button>
              <button onClick={submitCreate} disabled={create.isPending}
                className="rounded bg-primary px-3 py-1 text-sm text-primary-foreground disabled:opacity-50">
                {create.isPending ? "Creating…" : "Create"}
              </button>
            </div>
          </div>
        </div>
      )}

      {secretReveal && (
        <SecretRevealOnce secret={secretReveal} onAcknowledge={() => setSecretReveal(null)}/>
      )}
    </div>
  );
}
```

- [ ] **Step 3: TenantDetailPage**

Replace `services/payserver/admin/src/pages/TenantDetailPage.tsx`:

```tsx
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";
import { toast } from "sonner";
import { useTenant, useUpdateTenant } from "@/api/tenants";

export function TenantDetailPage() {
  const { id = "" } = useParams();
  const { data: t, isLoading, error } = useTenant(id);
  const update = useUpdateTenant();

  const [form, setForm] = useState({
    callback_url: "", callback_secret: "", description: "", is_active: true,
  });
  useEffect(() => {
    if (t) setForm({
      callback_url: t.callback_url, callback_secret: "",
      description: t.description, is_active: t.is_active,
    });
  }, [t?.id]);

  if (isLoading) return <div className="text-sm text-muted-foreground">Loading…</div>;
  if (error) return <div className="text-sm text-destructive">Error: {String(error)}</div>;
  if (!t) return null;

  async function save() {
    const patch: Record<string, unknown> = {
      callback_url: form.callback_url, description: form.description, is_active: form.is_active,
    };
    if (form.callback_secret) patch.callback_secret = form.callback_secret;
    try {
      await update.mutateAsync({ id, patch });
      toast.success("Saved");
    } catch (e: unknown) { toast.error(String((e as Error).message ?? e)); }
  }

  return (
    <div className="space-y-6 max-w-2xl">
      <div>
        <Link to="/tenants" className="text-sm text-muted-foreground hover:underline">← Tenants</Link>
        <h1 className="text-xl font-semibold mt-2">{t.name}</h1>
        <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1 text-sm text-muted-foreground">
          <dt>ID</dt><dd className="font-mono text-xs">{t.id}</dd>
          <dt>Created</dt><dd>{new Date(t.created_at).toLocaleString()}</dd>
        </dl>
      </div>

      <div className="space-y-3">
        <div>
          <label className="text-sm">Callback URL</label>
          <input className="w-full rounded border px-2 py-1 text-sm" value={form.callback_url}
            onChange={(e) => setForm({ ...form, callback_url: e.target.value })}/>
        </div>
        <div>
          <label className="text-sm">Callback HMAC Secret (leave empty to keep)</label>
          <input className="w-full rounded border px-2 py-1 text-sm font-mono" type="password" value={form.callback_secret}
            onChange={(e) => setForm({ ...form, callback_secret: e.target.value })}/>
        </div>
        <div>
          <label className="text-sm">Description</label>
          <input className="w-full rounded border px-2 py-1 text-sm" value={form.description}
            onChange={(e) => setForm({ ...form, description: e.target.value })}/>
        </div>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={form.is_active}
            onChange={(e) => setForm({ ...form, is_active: e.target.checked })}/>
          Active
        </label>
        <button onClick={save} disabled={update.isPending}
          className="rounded bg-primary px-3 py-1 text-sm text-primary-foreground disabled:opacity-50">
          {update.isPending ? "Saving…" : "Save"}
        </button>
      </div>

      <Link to={`/payments?tenant_id=${t.id}`} className="text-sm underline">
        View this tenant's payments →
      </Link>
    </div>
  );
}
```

- [ ] **Step 4: Build + smoke**

```bash
cd services/payserver && make all
./payserver &
# Visit /admin/tenants, create, rotate, delete (a tenant with no payments)
```

- [ ] **Step 5: Commit**

```bash
git add services/payserver/admin/src/api/tenants.ts \
        services/payserver/admin/src/pages/TenantsPage.tsx \
        services/payserver/admin/src/pages/TenantDetailPage.tsx \
        services/payserver/admin_dist/
git commit -m "feat(payserver/admin): tenants list + create + detail-edit + rotate + delete UI"
```

---

## Task 17: PaymentsPage with filters + detail dialog

**Files:**
- Modify: `services/payserver/admin/src/pages/PaymentsPage.tsx`
- Modify: `services/payserver/admin/src/api/payments.ts`

**Interfaces:**
- Consumes: Task 12 (list/get payments), Task 16 (tenant list for filter dropdown)
- Produces: filter bar (tenant_id / status / channel) + paginated table + click-to-detail dialog showing raw_notify

- [ ] **Step 1: payments.ts API helpers**

Replace `services/payserver/admin/src/api/payments.ts`:

```ts
import { useQuery } from "@tanstack/react-query";
import { adminFetch } from "./client";
import type { Payment } from "./types";

export type ListPaymentsParams = {
  tenant_id?: string; status?: string; channel?: string;
  limit?: number; offset?: number;
};

export function usePayments(params: ListPaymentsParams) {
  const qs = new URLSearchParams();
  if (params.tenant_id) qs.set("tenant_id", params.tenant_id);
  if (params.status) qs.set("status", params.status);
  if (params.channel) qs.set("channel", params.channel);
  qs.set("limit", String(params.limit ?? 50));
  qs.set("offset", String(params.offset ?? 0));
  return useQuery({
    queryKey: ["payments", params],
    queryFn: () => adminFetch<{ items: Payment[]; meta: { total: number; limit: number; offset: number } }>(`/payments?${qs}`),
  });
}

export function usePayment(id: string) {
  return useQuery({
    queryKey: ["payments", id],
    queryFn: () => adminFetch<{ payment: Payment }>(`/payments/${id}`).then((r) => r.payment),
    enabled: !!id,
  });
}
```

- [ ] **Step 2: PaymentsPage**

Replace `services/payserver/admin/src/pages/PaymentsPage.tsx`:

```tsx
import { useMemo, useState } from "react";
import { useSearchParams } from "react-router";
import { usePayments, usePayment } from "@/api/payments";
import { useTenants } from "@/api/tenants";
import type { Payment } from "@/api/types";

const STATUSES = ["", "pending", "paid", "failed"];
const CHANNELS = ["", "wechat", "alipay", "stripe"];
const PAGE = 50;

export function PaymentsPage() {
  const [search, setSearch] = useSearchParams();
  const tenantID = search.get("tenant_id") ?? "";
  const status = search.get("status") ?? "";
  const channel = search.get("channel") ?? "";
  const offset = Number(search.get("offset") ?? "0");

  const { data: tenants } = useTenants();
  const { data, isLoading, error } = usePayments({
    tenant_id: tenantID, status, channel, offset, limit: PAGE,
  });
  const tenantsByID = useMemo(() => {
    const m = new Map<string, string>();
    (tenants?.items ?? []).forEach((t) => m.set(t.id, t.name));
    return m;
  }, [tenants]);

  const [openID, setOpenID] = useState<string | null>(null);

  function setFilter(k: string, v: string) {
    const sp = new URLSearchParams(search);
    if (v) sp.set(k, v); else sp.delete(k);
    sp.delete("offset");
    setSearch(sp);
  }
  function setOffset(n: number) {
    const sp = new URLSearchParams(search);
    sp.set("offset", String(n));
    setSearch(sp);
  }

  if (error) return <div className="text-sm text-destructive">Error: {String(error)}</div>;

  const items = data?.items ?? [];
  const total = data?.meta.total ?? 0;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold">Payments</h1>
        <div className="text-sm text-muted-foreground">{total} total</div>
      </div>

      <div className="flex flex-wrap gap-2 items-end">
        <FilterSelect label="Tenant" value={tenantID} options={[["", "All"], ...(tenants?.items ?? []).map((t) => [t.id, t.name] as [string, string])]} onChange={(v) => setFilter("tenant_id", v)} />
        <FilterSelect label="Status" value={status} options={STATUSES.map((s) => [s, s || "All"])} onChange={(v) => setFilter("status", v)} />
        <FilterSelect label="Channel" value={channel} options={CHANNELS.map((c) => [c, c || "All"])} onChange={(v) => setFilter("channel", v)} />
      </div>

      {isLoading ? (
        <div className="text-sm text-muted-foreground">Loading…</div>
      ) : (
        <table className="w-full text-xs">
          <thead className="border-b">
            <tr>
              <th className="px-2 py-2 text-left">Created</th>
              <th className="px-2 py-2 text-left">Order</th>
              <th className="px-2 py-2 text-left">Tenant</th>
              <th className="px-2 py-2 text-left">Channel</th>
              <th className="px-2 py-2 text-right">Amount</th>
              <th className="px-2 py-2 text-left">Status</th>
              <th className="px-2 py-2 text-left">Callback</th>
              <th className="px-2 py-2 text-right">Retries</th>
            </tr>
          </thead>
          <tbody>
            {items.map((p: Payment) => (
              <tr key={p.id} onClick={() => setOpenID(p.id)} className="cursor-pointer border-b hover:bg-accent/30">
                <td className="px-2 py-1.5 text-muted-foreground">{new Date(p.created_at).toLocaleString()}</td>
                <td className="px-2 py-1.5 font-mono">{p.order_id.slice(0, 12)}…</td>
                <td className="px-2 py-1.5">{tenantsByID.get(p.tenant_id) ?? p.tenant_id.slice(0, 8)}</td>
                <td className="px-2 py-1.5">{p.channel}</td>
                <td className="px-2 py-1.5 text-right font-mono">{p.amount}</td>
                <td className="px-2 py-1.5">{p.status}</td>
                <td className="px-2 py-1.5">{p.callback_status}</td>
                <td className="px-2 py-1.5 text-right">{p.callback_retries}</td>
              </tr>
            ))}
            {items.length === 0 && (
              <tr><td colSpan={8} className="px-2 py-6 text-center text-muted-foreground">No payments match these filters</td></tr>
            )}
          </tbody>
        </table>
      )}

      <div className="flex items-center justify-between text-sm">
        <button onClick={() => setOffset(Math.max(0, offset - PAGE))} disabled={offset === 0}
          className="rounded border px-3 py-1 disabled:opacity-50">← Prev</button>
        <span className="text-muted-foreground">{offset + 1}–{Math.min(offset + PAGE, total)} of {total}</span>
        <button onClick={() => setOffset(offset + PAGE)} disabled={offset + PAGE >= total}
          className="rounded border px-3 py-1 disabled:opacity-50">Next →</button>
      </div>

      {openID && <PaymentDetailDialog id={openID} onClose={() => setOpenID(null)} />}
    </div>
  );
}

function FilterSelect({
  label, value, options, onChange,
}: { label: string; value: string; options: [string, string][]; onChange: (v: string) => void; }) {
  return (
    <div className="text-sm">
      <div className="text-xs text-muted-foreground mb-1">{label}</div>
      <select className="rounded border px-2 py-1 text-sm" value={value} onChange={(e) => onChange(e.target.value)}>
        {options.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
      </select>
    </div>
  );
}

function PaymentDetailDialog({ id, onClose }: { id: string; onClose: () => void }) {
  const { data: p, isLoading } = usePayment(id);
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
      <div className="max-h-[80vh] w-[640px] overflow-auto space-y-3 rounded-md bg-background p-6 shadow-lg">
        <div className="flex items-center justify-between">
          <h2 className="text-lg font-semibold">Payment</h2>
          <button onClick={onClose} className="text-sm underline">Close</button>
        </div>
        {isLoading ? "Loading…" : p && (
          <dl className="space-y-1 text-sm">
            <Row label="ID" value={p.id} mono />
            <Row label="Tenant ID" value={p.tenant_id} mono />
            <Row label="Order ID" value={p.order_id} mono />
            <Row label="Channel" value={p.channel} />
            <Row label="Amount" value={String(p.amount)} />
            <Row label="Status" value={p.status} />
            <Row label="Callback status" value={p.callback_status} />
            <Row label="Callback retries" value={String(p.callback_retries)} />
            <Row label="Trade No" value={p.trade_no || "—"} mono />
            <Row label="Payment URL" value={p.payment_url || "—"} />
            <Row label="Paid at" value={p.paid_at ?? "—"} />
            <div>
              <dt className="text-muted-foreground text-xs mt-3">Raw notify</dt>
              <pre className="rounded bg-muted p-2 font-mono text-xs whitespace-pre-wrap break-all">
                {p.raw_notify ? prettyJSON(p.raw_notify) : "—"}
              </pre>
            </div>
          </dl>
        )}
      </div>
    </div>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="grid grid-cols-3 gap-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className={`col-span-2 ${mono ? "font-mono text-xs" : ""}`}>{value}</dd>
    </div>
  );
}

function prettyJSON(s: string): string {
  try { return JSON.stringify(JSON.parse(s), null, 2); }
  catch { return s; }
}
```

- [ ] **Step 2: Build + smoke**

```bash
cd services/payserver && make all
./payserver &
# Visit /admin/payments — see all rows, filter, click for detail
```

- [ ] **Step 3: Commit**

```bash
git add services/payserver/admin/src/api/payments.ts \
        services/payserver/admin/src/pages/PaymentsPage.tsx \
        services/payserver/admin_dist/
git commit -m "feat(payserver/admin): payments page with filters + detail dialog"
```

---

## Task 18: Phase 5 sweep — full e2e smoke

**Files:** none (verification gate)

- [ ] **Step 1: Full payserver tests**

```bash
export PAYSERVER_TEST_DB_URL="postgres://postgres:test@localhost:54399/payserver_stripe_test?sslmode=disable"
cd services/payserver && go test ./... -count=1
```
Expected: all green.

- [ ] **Step 2: pnpm build clean**

```bash
cd services/payserver/admin && pnpm install && pnpm build
```
Expected: zero TS errors.

- [ ] **Step 3: Full e2e smoke against real OIDC (Keycloak/Google/your IdP)**

```bash
# Set real OIDC env
export PAYSERVER_OIDC_ISSUER_URL="https://idp.example.com"
export PAYSERVER_OIDC_CLIENT_ID="..."
export PAYSERVER_OIDC_CLIENT_SECRET="..."
export PAYSERVER_OIDC_REDIRECT_URL="https://payserver-test.example/admin/callback"
export PAYSERVER_OIDC_SESSION_SECRET="$(openssl rand -base64 32)"

# Start
cd services/payserver && ./payserver &

# 1. Visit /admin/login → IdP login → return to /admin/tenants
# 2. See 1 tenant: "default"
# 3. New Tenant "smoke-test" with callback_url = a request-bin URL. Copy secret.
# 4. From a separate shell, hit /payments with the smoke-test bearer:
curl -X POST -H "Authorization: Bearer <id>:<secret>" -H "Content-Type: application/json" \
  -d '{"order_id":"<uuid>","product_name":"test","channel":"stripe","currency":"USD","amount":2000,"return_url":"https://example.com/done"}' \
  http://localhost:8090/payments
# 5. Use Stripe test mode to complete the checkout. Verify request-bin received the callback HMAC-signed with smoke-test's callback_secret.
# 6. In /admin/payments, see the smoke-test row with status=paid + callback_status=success.
# 7. Rotate smoke-test secret. Old bearer fails immediately; new bearer works.
# 8. Deactivate smoke-test. New /payments with its bearer returns 401. The previous payment row remains.
# 9. Try to delete smoke-test → 409 (has payment). Deactivate path offered.
# 10. modelserver continues to work via the default tenant (regression check).
```

- [ ] **Step 4: Drift / dead-code grep**

```bash
grep -rn "cfg.APIKey" services/payserver/ --include="*.go"
# Expected: only places that read it for the deprecation warn (1 hit in main.go)
grep -rn "ApplyEnvOverrides" services/payserver/ --include="*.go"
# Expected: zero hits
```

- [ ] **Step 5: Commit any docs / cleanup discovered during smoke**

If smoke surfaces config.example.yml needing an oidc section, add it now:

```yaml
oidc:
  issuer_url: ""                  # e.g. https://idp.example.com
  client_id: ""
  client_secret: ""
  redirect_url: ""                # e.g. https://payserver.example.com/admin/callback
  scopes: []                      # default ["openid","profile","email"]
  allowed_emails: []              # empty = any OIDC-validated user
  session_secret: ""              # 32+ random bytes for cookie HMAC
```

```bash
git add services/payserver/config.example.yml
git commit -m "docs(payserver): config.example.yml documents oidc section"
```

---

## Task 19: Open PR

- [ ] **Step 1: Push branch + open PR**

```bash
git push -u origin feat/payserver-multitenant
gh pr create --base main --head feat/payserver-multitenant \
  --title "feat(payserver): standalone multi-tenant gateway with OIDC admin UI" \
  --body-file - <<'EOF_BODY'
## Summary

Turns payserver into a true multi-tenant payment gateway. Every upstream
product gets its own `tenant_id+secret`, its own callback URL/HMAC
secret, and an OIDC-authenticated admin UI to manage tenants and inspect
payments.

Payment provider credentials (one Stripe / one wechat / one alipay) stay
**permanently** platform-global — per-tenant provider credentials are
explicitly not a goal.

- Spec: docs/superpowers/specs/2026-06-19-payserver-standalone-multitenant-design.md
- Plan: docs/superpowers/plans/2026-06-20-payserver-standalone-multitenant.md

## What changes for modelserver

One env swap during deploy. See **Cross-service env coupling** in the spec §3.

```bash
# old
MODELSERVER_BILLING_PAYMENT_API_KEY=<legacy-api-key>
# new
MODELSERVER_BILLING_PAYMENT_API_KEY=<default-tenant-uuid>:<PAYSERVER_DEFAULT_TENANT_SECRET>
```

`MODELSERVER_BILLING_WEBHOOK_SECRET` stays exactly as-is — migration 002
copies it into the default tenant's `callback_secret` automatically.

## Deployment order (mandatory)

1. Set `PAYSERVER_DEFAULT_TENANT_SECRET` (32 random bytes from openssl).
2. Set OIDC env vars (`PAYSERVER_OIDC_*`) plus `PAYSERVER_OIDC_SESSION_SECRET`.
3. Deploy + restart payserver. Migration 002 runs once, prints `default tenant id=<uuid>`.
4. Update modelserver's `MODELSERVER_BILLING_PAYMENT_API_KEY=<uuid>:<secret>`.
5. Restart modelserver.

Window between step 3 and 5: modelserver→payserver `POST /payments` returns 401 (old bearer format). Subscription ordering is low-frequency; minute-scale window is acceptable for a planned deploy.

## OIDC rescue

If OIDC is misconfigured, you cannot reach `/admin/*`. Recover via:

```bash
payserver admin rescue --email ops@example.com --ttl 1h
# prints a session cookie value; set it on the /admin path manually.
```

## Phases

Logical checkpoints if you want to review piece by piece:

- Phase 1 (1 commit): viper refactor
- Phase 2 (4 commits): tenant model, migration 002, CRUD methods, bearer middleware
- Phase 3 (4 commits): per-tenant callback routing through notify + compensate
- Phase 4 (5 commits): OIDC + admin API CRUD + rescue subcommand
- Phase 5 (4 commits): React admin frontend + embed pipeline

19 commits, 1 PR.

## Out of scope

- Per-tenant payment provider credentials (intentionally never)
- Tenant self-service portal (admin UI is operator-only)
- Audit log of admin actions
- Per-tenant rate limiting
- Payserver split to own git repo

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF_BODY
```

- [ ] **Step 2: Mark plan task done**

In the SDD ledger, append:

```
PR opened: https://github.com/modelserver/modelserver/pull/<NN>
```

Done.

---

## Self-Review

**Spec coverage:**

| Spec § | Task(s) |
|---|---|
| §1 File changes | All tasks; scaffold in Task 15 |
| §2 Tenant model + auth | Tasks 2, 4, 5 |
| §3 Migration 002 + bootstrap | Task 3 |
| §3 Cross-service env coupling | PR body + Task 19 |
| §4 Per-tenant callback routing | Tasks 6, 7, 8, 9 |
| §4 Provider webhook routing intent | Inherited from spec; no code change needed |
| §5 Admin API + OIDC routes | Tasks 11, 12 |
| §5 Tenant CRUD + secret response semantics | Task 12 |
| §6 Admin frontend scaffold + pages | Tasks 15, 16, 17 |
| §6 SecretRevealOnce | Task 15 |
| §6 Hosting via go:embed | Task 15 |
| §7 Tests + Out of Scope | Tasks 14, 18 |
| §7 OIDC rescue (first-class) | Task 13 |
| §7 Deployment order + window | Task 19 PR body |

Every spec section maps to a task. Phase 1 (viper) is an addition not covered in the spec body but covered by user request mid-flight.

**Placeholder scan:** zero "TBD" / "TODO" / "implement later". Every code block is complete. Two intentional "TODO" strings exist in Task 15 stub pages — those are the page-content stubs that Tasks 16/17 replace, called out explicitly.

**Type consistency:**

- `MigrationBootstrap` struct fields (`DefaultTenantSecretHash`, `DefaultCallbackURL`, `DefaultCallbackSecret`) match across Task 3 (definition), Task 13 (rescue doesn't use), and all openTestStore-style helpers.
- `tenant.Tenant` struct fields match between Task 2 (definition), Task 4 (store CRUD), Task 5 (auth middleware), Task 7 (notify handlers), Task 12 (admin handlers), Task 16 (frontend types).
- `CallbackTarget` struct (`URL`, `Secret`) consistent between Task 6 (definition) and Tasks 7, 8 (consumers).
- `AdminSession` (`Email`, `Name`, `ExpiresAt`) consistent across Tasks 10, 11, 13.
- HTTP routes: `/admin/login`, `/admin/callback`, `/admin/logout`, `/admin/whoami`, `/admin/tenants`, `/admin/tenants/{id}`, `/admin/tenants/{id}/rotate-secret`, `/admin/payments`, `/admin/payments/{id}` consistent between Tasks 11, 12, 15 (frontend client).
- `Payment.TenantID` added in Task 3 is read by Tasks 7, 8 (notify+compensate) and exposed via API in Task 12.
- `payments(order_id)` still UNIQUE globally — spec §4 says so; plan Tasks 3 + 12 both honor it (no composite unique index added).
