package admin

import (
	"fmt"
	"net/http"
	"time"

	"github.com/modelserver/modelserver/internal/proxy"
	"github.com/modelserver/modelserver/internal/store"
)

type oauthExchangeDependencies struct {
	store   *store.Store
	clients *proxy.OutboundClientFactory
}

func oauthExchangeClient(
	deps oauthExchangeDependencies,
	upstreamID string,
	cfg proxy.OutboundProxyConfig,
	timeout time.Duration,
) (*http.Client, error) {
	if deps.clients == nil {
		deps.clients = proxy.NewOutboundClientFactory(nil)
	}
	if upstreamID == "" {
		return deps.clients.ClientForConfig(cfg, timeout)
	}
	if deps.store == nil {
		return nil, fmt.Errorf("upstream_id cannot be used in this context")
	}
	u, err := deps.store.GetUpstreamByID(upstreamID)
	if err != nil {
		return nil, fmt.Errorf("load upstream proxy configuration: %w", err)
	}
	if u == nil {
		return nil, fmt.Errorf("upstream not found")
	}
	if cfg.Mode == "" && cfg.URL == "" && cfg.Username == "" && cfg.Password == "" {
		return deps.clients.ClientFor(u, timeout)
	}

	// A blank password on edit means "use the saved password". This is safe
	// only while the visible proxy fields still match the stored configuration;
	// otherwise the operator must provide the password again.
	if cfg.Password == "" {
		mode := proxy.NormalizeProxyMode(cfg.Mode)
		if mode == u.EffectiveProxyMode() && cfg.URL == u.SocksProxyURL && cfg.Username == u.SocksProxyUsername {
			return deps.clients.ClientFor(u, timeout)
		}
		return nil, fmt.Errorf("re-enter the SOCKS proxy password after changing proxy settings")
	}
	return deps.clients.ClientForConfig(cfg, timeout)
}
