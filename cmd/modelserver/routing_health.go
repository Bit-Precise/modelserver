package main

import (
	"strings"
	"time"

	"github.com/modelserver/modelserver/internal/admin"
	"github.com/modelserver/modelserver/internal/proxy"
	"github.com/modelserver/modelserver/internal/proxy/lb"
	"github.com/modelserver/modelserver/internal/store"
)

// routingHealthProvider adapts the proxy router's runtime state (circuit
// breaker, health checker, metrics, connection tracker) plus the persistent
// upstream/group catalog into the shape the admin API serves.
type routingHealthProvider struct {
	st     *store.Store
	router *proxy.Router
}

func newRoutingHealthProvider(st *store.Store, router *proxy.Router) *routingHealthProvider {
	return &routingHealthProvider{st: st, router: router}
}

func (p *routingHealthProvider) GetRoutingHealth() admin.RoutingHealthResponse {
	resp := admin.RoutingHealthResponse{
		Upstreams: []admin.UpstreamHealth{},
		Groups:    []admin.GroupHealth{},
	}

	upstreams, err := p.st.ListUpstreams()
	if err != nil {
		return resp
	}

	cb := p.router.CircuitBreaker()
	hc := p.router.HealthChecker()
	metrics := p.router.Metrics()
	conns := p.router.ConnTracker()

	healthByID := make(map[string]lb.HealthStatus, len(upstreams))
	circuitByID := make(map[string]string, len(upstreams))
	resp.Upstreams = make([]admin.UpstreamHealth, 0, len(upstreams))
	for i := range upstreams {
		u := &upstreams[i]
		hs := hc.Status(u.ID)
		healthByID[u.ID] = hs
		// FE keys use underscores ("half_open"); CircuitState.String() returns "half-open".
		circuitState := strings.ReplaceAll(cb.State(u.ID).String(), "-", "_")
		circuitByID[u.ID] = circuitState

		row := admin.UpstreamHealth{
			ID:                u.ID,
			Name:              u.Name,
			Provider:          u.Provider,
			CircuitState:      circuitState,
			HealthStatus:      hs.String(),
			ActiveConnections: conns.Count(u.ID),
		}
		if stats := metrics.GetStats(u.ID); stats != nil {
			row.RecentErrors = stats.RecentErrors.Load()
			if v, ok := stats.LastErrorAt.Load().(time.Time); ok && !v.IsZero() {
				t := v
				row.LastErrorAt = &t
			}
			if v, ok := stats.LastSuccessAt.Load().(time.Time); ok && !v.IsZero() {
				t := v
				row.LastCheckAt = &t
			}
		}
		resp.Upstreams = append(resp.Upstreams, row)
	}

	groups, err := p.st.ListUpstreamGroupsWithMembers()
	if err != nil {
		return resp
	}
	resp.Groups = make([]admin.GroupHealth, 0, len(groups))
	for _, g := range groups {
		healthy := 0
		for _, m := range g.Members {
			// A member counts as healthy if its circuit isn't open and the
			// active probe hasn't marked it down. Unknown counts as healthy
			// (probe just hasn't run yet) so newly added upstreams aren't
			// reported as failing.
			if circuitByID[m.Upstream.ID] == "open" {
				continue
			}
			if healthByID[m.Upstream.ID] == lb.HealthDown {
				continue
			}
			healthy++
		}
		resp.Groups = append(resp.Groups, admin.GroupHealth{
			ID:             g.ID,
			Name:           g.Name,
			LBPolicy:       g.LBPolicy,
			HealthyMembers: healthy,
			TotalMembers:   len(g.Members),
		})
	}

	return resp
}
