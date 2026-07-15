package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUpstreamProxyPasswordIsNotSerialized(t *testing.T) {
	u := Upstream{
		ProxyMode:                   ProxyModeSOCKS5,
		SocksProxyURL:               "socks5h://proxy.example:1080",
		SocksProxyUsername:          "alice",
		SocksProxyPasswordEncrypted: []byte("encrypted-secret"),
		SocksProxyPasswordSet:       true,
	}
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if strings.Contains(got, "encrypted-secret") {
		t.Fatalf("serialized upstream leaked proxy password ciphertext: %s", got)
	}
	if !strings.Contains(got, `"socks_proxy_password_set":true`) {
		t.Fatalf("serialized upstream omitted password-set indicator: %s", got)
	}
}

func TestUpstreamEffectiveProxyModeDefaultsToEnvironment(t *testing.T) {
	if got := (&Upstream{}).EffectiveProxyMode(); got != ProxyModeEnvironment {
		t.Fatalf("EffectiveProxyMode() = %q", got)
	}
}
