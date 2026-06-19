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
