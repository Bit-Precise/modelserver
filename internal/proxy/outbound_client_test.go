package proxy

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/crypto"
	"github.com/modelserver/modelserver/internal/types"
)

type socksObservation struct {
	username string
	password string
	target   string
}

func TestValidateOutboundProxyConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     OutboundProxyConfig
		wantErr string
	}{
		{name: "empty means environment", cfg: OutboundProxyConfig{}},
		{name: "direct", cfg: OutboundProxyConfig{Mode: types.ProxyModeDirect}},
		{name: "socks5", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5://proxy.example:1080"}},
		{name: "socks5h auth", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5h://proxy.example:1080", Username: "user", Password: "secret"}},
		{name: "unknown mode", cfg: OutboundProxyConfig{Mode: "auto"}, wantErr: "proxy_mode"},
		{name: "missing url", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5}, wantErr: "required"},
		{name: "http rejected", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "http://proxy.example:1080"}, wantErr: "scheme"},
		{name: "missing port", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5h://proxy.example"}, wantErr: "explicit port"},
		{name: "userinfo rejected", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5h://user:secret@proxy.example:1080"}, wantErr: "must not include credentials"},
		{name: "path rejected", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5h://proxy.example:1080/path"}, wantErr: "path"},
		{name: "password requires username", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5h://proxy.example:1080", Password: "secret"}, wantErr: "username is required"},
		{name: "long username", cfg: OutboundProxyConfig{Mode: types.ProxyModeSOCKS5, URL: "socks5h://proxy.example:1080", Username: strings.Repeat("u", 256)}, wantErr: "255 bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateOutboundProxyConfig(tt.cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateOutboundProxyConfig() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestOutboundClientFactorySOCKSProxyAndPasswordDecryption(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	encrypted, err := crypto.Encrypt(key, []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	factory := NewOutboundClientFactory(key)
	u := &types.Upstream{
		ProxyMode:                   types.ProxyModeSOCKS5,
		SocksProxyURL:               "socks5h://proxy.example:1080/",
		SocksProxyUsername:          "alice",
		SocksProxyPasswordEncrypted: encrypted,
	}
	client, err := factory.ClientFor(u, 3*time.Second)
	if err != nil {
		t.Fatalf("ClientFor() error = %v", err)
	}
	if client.Timeout != 3*time.Second {
		t.Fatalf("timeout = %v", client.Timeout)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T", client.Transport)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://upstream.example/v1", nil)
	proxyURL, err := tr.Proxy(req)
	if err != nil {
		t.Fatalf("Proxy() error = %v", err)
	}
	if got := proxyURL.String(); got != "socks5h://alice:secret@proxy.example:1080" {
		t.Fatalf("proxy URL = %q", got)
	}

	client2, err := factory.ClientFor(u, 0)
	if err != nil {
		t.Fatal(err)
	}
	if client2.Transport != client.Transport {
		t.Fatal("equivalent SOCKS configurations did not reuse their transport")
	}
}

func TestOutboundClientFactoryFailsClosedOnBadEncryptedPassword(t *testing.T) {
	factory := NewOutboundClientFactory([]byte("0123456789abcdef0123456789abcdef"))
	_, err := factory.ClientFor(&types.Upstream{
		ProxyMode:                   types.ProxyModeSOCKS5,
		SocksProxyURL:               "socks5h://proxy.example:1080",
		SocksProxyUsername:          "alice",
		SocksProxyPasswordEncrypted: []byte("not-ciphertext"),
	}, 0)
	if err == nil || !strings.Contains(err.Error(), "decrypt socks proxy password") {
		t.Fatalf("error = %v", err)
	}
}

func TestOutboundClientFactoryDirectHasNoProxy(t *testing.T) {
	factory := NewOutboundClientFactory(nil)
	client, err := factory.ClientFor(&types.Upstream{ProxyMode: types.ProxyModeDirect}, 0)
	if err != nil {
		t.Fatal(err)
	}
	tr := client.Transport.(*http.Transport)
	if tr.Proxy != nil {
		t.Fatal("direct transport unexpectedly has a proxy function")
	}
}

func TestOutboundClientFactorySOCKS5HandshakeAndRemoteDNS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	observed := make(chan socksObservation, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		obs, err := serveSingleSOCKSRequest(conn)
		if err != nil {
			serverErr <- err
			return
		}
		observed <- obs
	}()

	factory := NewOutboundClientFactory(nil)
	client, err := factory.ClientForConfig(OutboundProxyConfig{
		Mode:     types.ProxyModeSOCKS5,
		URL:      "socks5h://" + ln.Addr().String(),
		Username: "alice",
		Password: "secret",
	}, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get("http://upstream.invalid:8080/v1/test")
	if err != nil {
		t.Fatalf("request through SOCKS proxy failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	select {
	case err := <-serverErr:
		t.Fatal(err)
	case obs := <-observed:
		if obs.username != "alice" || obs.password != "secret" {
			t.Fatalf("credentials = %q/%q", obs.username, obs.password)
		}
		if obs.target != "upstream.invalid:8080" {
			t.Fatalf("CONNECT target = %q; hostname should be resolved by SOCKS", obs.target)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for SOCKS observation")
	}
}

func serveSingleSOCKSRequest(conn net.Conn) (socksObservation, error) {
	var obs socksObservation
	reader := bufio.NewReader(conn)
	header := make([]byte, 2)
	if _, err := io.ReadFull(reader, header); err != nil {
		return obs, err
	}
	if header[0] != 5 {
		return obs, fmt.Errorf("SOCKS version = %d", header[0])
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, methods); err != nil {
		return obs, err
	}
	if _, err := conn.Write([]byte{5, 2}); err != nil { // username/password
		return obs, err
	}

	if _, err := io.ReadFull(reader, header); err != nil {
		return obs, err
	}
	username := make([]byte, int(header[1]))
	if _, err := io.ReadFull(reader, username); err != nil {
		return obs, err
	}
	var passwordLen [1]byte
	if _, err := io.ReadFull(reader, passwordLen[:]); err != nil {
		return obs, err
	}
	password := make([]byte, int(passwordLen[0]))
	if _, err := io.ReadFull(reader, password); err != nil {
		return obs, err
	}
	obs.username, obs.password = string(username), string(password)
	if _, err := conn.Write([]byte{1, 0}); err != nil {
		return obs, err
	}

	connectHeader := make([]byte, 4)
	if _, err := io.ReadFull(reader, connectHeader); err != nil {
		return obs, err
	}
	if connectHeader[0] != 5 || connectHeader[1] != 1 {
		return obs, fmt.Errorf("unexpected SOCKS command: %v", connectHeader)
	}
	var host string
	switch connectHeader[3] {
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return obs, err
		}
		name := make([]byte, int(size[0]))
		if _, err := io.ReadFull(reader, name); err != nil {
			return obs, err
		}
		host = string(name)
	default:
		return obs, fmt.Errorf("CONNECT address type = %d, want domain", connectHeader[3])
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(reader, portBytes[:]); err != nil {
		return obs, err
	}
	obs.target = fmt.Sprintf("%s:%d", host, binary.BigEndian.Uint16(portBytes[:]))
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return obs, err
	}

	req, err := http.ReadRequest(reader)
	if err != nil {
		return obs, err
	}
	req.Body.Close()
	_, err = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nOK")
	return obs, err
}
