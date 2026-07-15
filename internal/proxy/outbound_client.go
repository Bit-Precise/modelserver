package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelserver/modelserver/internal/crypto"
	"github.com/modelserver/modelserver/internal/types"
)

// OutboundProxyConfig is the plaintext, runtime form of an upstream's proxy
// configuration. Password is write-only at API boundaries and must never be
// logged or serialized in responses.
type OutboundProxyConfig struct {
	Mode     string
	URL      string
	Username string
	Password string
}

// NormalizeProxyMode keeps hand-built upstreams and pre-migration values on
// the historical environment-proxy behavior.
func NormalizeProxyMode(mode string) string {
	if mode == "" {
		return types.ProxyModeEnvironment
	}
	return mode
}

// ValidateOutboundProxyConfig validates a plaintext proxy configuration.
func ValidateOutboundProxyConfig(cfg OutboundProxyConfig) error {
	cfg.Mode = NormalizeProxyMode(cfg.Mode)
	switch cfg.Mode {
	case types.ProxyModeEnvironment, types.ProxyModeDirect, types.ProxyModeSOCKS5:
	default:
		return fmt.Errorf("proxy_mode must be one of environment, direct, or socks5")
	}
	if len([]byte(cfg.Username)) > 255 {
		return fmt.Errorf("socks proxy username must not exceed 255 bytes")
	}
	if len([]byte(cfg.Password)) > 255 {
		return fmt.Errorf("socks proxy password must not exceed 255 bytes")
	}
	if cfg.Mode == types.ProxyModeSOCKS5 {
		if cfg.Password != "" && cfg.Username == "" {
			return fmt.Errorf("socks proxy username is required when a password is configured")
		}
		if cfg.URL == "" {
			return fmt.Errorf("socks_proxy_url is required when proxy_mode is socks5")
		}
		if _, err := parseSOCKSProxyURL(cfg.URL); err != nil {
			return err
		}
	}
	return nil
}

func parseSOCKSProxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid socks_proxy_url: %w", err)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "socks5" && u.Scheme != "socks5h" {
		return nil, fmt.Errorf("socks_proxy_url scheme must be socks5 or socks5h")
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("socks_proxy_url must include a hostname")
	}
	if u.User != nil {
		return nil, fmt.Errorf("socks_proxy_url must not include credentials; use the username and password fields")
	}
	if u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("socks_proxy_url must not include a path")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("socks_proxy_url must not include a query or fragment")
	}
	port := u.Port()
	if port == "" {
		return nil, fmt.Errorf("socks_proxy_url must include an explicit port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("socks_proxy_url port must be between 1 and 65535")
	}
	// Canonicalize the semantically empty root path so cache keys and
	// connection-pool keys do not split on a trailing slash.
	u.Path = ""
	return u, nil
}

// OutboundClientFactory owns reusable transports for all outbound proxy
// modes. http.Client values are cheap wrappers; the cached transports retain
// connection pools across requests and across upstreams sharing a proxy.
type OutboundClientFactory struct {
	encryptionKey []byte
	environment   *http.Transport
	direct        *http.Transport

	mu         sync.Mutex
	transports map[string]*http.Transport
}

func NewOutboundClientFactory(encryptionKey []byte) *OutboundClientFactory {
	return &OutboundClientFactory{
		encryptionKey: append([]byte(nil), encryptionKey...),
		environment:   newOutboundTransport(http.ProxyFromEnvironment),
		direct:        newOutboundTransport(nil),
		transports:    make(map[string]*http.Transport),
	}
}

func newOutboundTransport(proxyFn func(*http.Request) (*url.URL, error)) *http.Transport {
	return &http.Transport{
		Proxy:               proxyFn,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}
}

// ClientFor resolves an encrypted stored upstream configuration.
func (f *OutboundClientFactory) ClientFor(upstream *types.Upstream, timeout time.Duration) (*http.Client, error) {
	if upstream == nil {
		return nil, fmt.Errorf("upstream is nil")
	}
	cfg := OutboundProxyConfig{
		Mode:     upstream.EffectiveProxyMode(),
		URL:      upstream.SocksProxyURL,
		Username: upstream.SocksProxyUsername,
	}
	if cfg.Mode == types.ProxyModeSOCKS5 && len(upstream.SocksProxyPasswordEncrypted) > 0 {
		plaintext, err := crypto.Decrypt(f.encryptionKey, upstream.SocksProxyPasswordEncrypted)
		if err != nil {
			return nil, fmt.Errorf("decrypt socks proxy password: %w", err)
		}
		cfg.Password = string(plaintext)
	}
	return f.ClientForConfig(cfg, timeout)
}

// ClientForConfig resolves a plaintext configuration. It is also used by the
// pre-create OAuth exchange flow, where no persisted upstream exists yet.
func (f *OutboundClientFactory) ClientForConfig(cfg OutboundProxyConfig, timeout time.Duration) (*http.Client, error) {
	cfg.Mode = NormalizeProxyMode(cfg.Mode)
	if err := ValidateOutboundProxyConfig(cfg); err != nil {
		return nil, err
	}

	var transport http.RoundTripper
	switch cfg.Mode {
	case types.ProxyModeEnvironment:
		transport = f.environment
	case types.ProxyModeDirect:
		transport = f.direct
	case types.ProxyModeSOCKS5:
		tr, err := f.socksTransport(cfg)
		if err != nil {
			return nil, err
		}
		transport = tr
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}

func (f *OutboundClientFactory) socksTransport(cfg OutboundProxyConfig) (*http.Transport, error) {
	h := sha256.New()
	h.Write([]byte(cfg.URL))
	h.Write([]byte{0})
	h.Write([]byte(cfg.Username))
	h.Write([]byte{0})
	h.Write([]byte(cfg.Password))
	key := hex.EncodeToString(h.Sum(nil))

	f.mu.Lock()
	defer f.mu.Unlock()
	if tr := f.transports[key]; tr != nil {
		return tr, nil
	}

	proxyURL, err := parseSOCKSProxyURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	if cfg.Username != "" {
		proxyURL.User = url.UserPassword(cfg.Username, cfg.Password)
	}
	tr := newOutboundTransport(http.ProxyURL(proxyURL))
	f.transports[key] = tr
	return tr, nil
}

// CloseIdleConnections releases pooled outbound connections during shutdown
// or tests. Active streaming connections are unaffected.
func (f *OutboundClientFactory) CloseIdleConnections() {
	f.environment.CloseIdleConnections()
	f.direct.CloseIdleConnections()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, tr := range f.transports {
		tr.CloseIdleConnections()
	}
}
