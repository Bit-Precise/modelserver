package proxy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/modelserver/modelserver/internal/types"
)

const vertexOAuthScope = "https://www.googleapis.com/auth/cloud-platform"

// VertexTokenManager manages OAuth2 access tokens for Vertex AI upstreams.
// Each upstream has its own service account and independently cached token.
type VertexTokenManager struct {
	mu              sync.RWMutex
	tokens          map[string]*vertexToken
	outboundClients *OutboundClientFactory
}

type vertexToken struct {
	source oauth2.TokenSource
}

// NewVertexTokenManager creates a new token manager.
func NewVertexTokenManager(clients ...*OutboundClientFactory) *VertexTokenManager {
	m := &VertexTokenManager{
		tokens: make(map[string]*vertexToken),
	}
	if len(clients) > 0 {
		m.outboundClients = clients[0]
	}
	return m
}

// Register parses a service account JSON key and creates a token source for
// the given upstream. The token source handles caching and automatic refresh.
func (m *VertexTokenManager) Register(upstream *types.Upstream, serviceAccountJSON []byte) error {
	if upstream == nil {
		return fmt.Errorf("upstream is nil")
	}
	ctx := context.Background()
	if m.outboundClients != nil {
		client, err := m.outboundClients.ClientFor(upstream, 15*time.Second)
		if err != nil {
			return fmt.Errorf("resolving outbound proxy for upstream %s: %w", upstream.ID, err)
		}
		ctx = context.WithValue(ctx, oauth2.HTTPClient, client)
	}
	creds, err := google.CredentialsFromJSON(ctx, serviceAccountJSON, vertexOAuthScope)
	if err != nil {
		return fmt.Errorf("parsing service account JSON for upstream %s: %w", upstream.ID, err)
	}
	source := oauth2.ReuseTokenSource(nil, creds.TokenSource)
	m.mu.Lock()
	m.tokens[upstream.ID] = &vertexToken{source: source}
	m.mu.Unlock()
	return nil
}

// registerWithSource is a test helper that registers an upstream with a custom
// token source, bypassing JSON key parsing.
func (m *VertexTokenManager) registerWithSource(upstreamID string, source oauth2.TokenSource) {
	m.mu.Lock()
	m.tokens[upstreamID] = &vertexToken{source: oauth2.ReuseTokenSource(nil, source)}
	m.mu.Unlock()
}

// GetToken returns a valid access token for the given upstream.
// The underlying ReuseTokenSource handles caching and refresh.
func (m *VertexTokenManager) GetToken(upstreamID string) (string, error) {
	m.mu.RLock()
	entry, ok := m.tokens[upstreamID]
	m.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("no vertex token source registered for upstream %s", upstreamID)
	}
	tok, err := entry.source.Token()
	if err != nil {
		return "", fmt.Errorf("getting token for upstream %s: %w", upstreamID, err)
	}
	return tok.AccessToken, nil
}

// Clear removes all registered token sources. Called by Router.Reload()
// before re-registering upstreams.
func (m *VertexTokenManager) Clear() {
	m.mu.Lock()
	m.tokens = make(map[string]*vertexToken)
	m.mu.Unlock()
}

// Deregister removes a single upstream's token source.
func (m *VertexTokenManager) Deregister(upstreamID string) {
	m.mu.Lock()
	delete(m.tokens, upstreamID)
	m.mu.Unlock()
}
