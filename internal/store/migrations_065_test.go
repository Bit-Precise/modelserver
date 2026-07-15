package store

import (
	"os"
	"strings"
	"testing"
)

func TestMigration065AddsUpstreamSOCKSProxyFields(t *testing.T) {
	b, err := os.ReadFile("migrations/065_upstream_socks_proxy.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(b)
	for _, fragment := range []string{
		"proxy_mode TEXT NOT NULL DEFAULT 'environment'",
		"socks_proxy_url TEXT NOT NULL DEFAULT ''",
		"socks_proxy_username TEXT NOT NULL DEFAULT ''",
		"socks_proxy_password_encrypted BYTEA",
		"'environment', 'direct', 'socks5'",
	} {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("migration missing %q", fragment)
		}
	}
}
