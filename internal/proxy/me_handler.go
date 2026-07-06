package proxy

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

// identityStore is the narrow subset of *store.Store that HandleOAuthProfile
// needs. Keeping it narrow lets the tests substitute a fake without
// standing up a database.
type identityStore interface {
	GetUserByID(id string) (*types.User, error)
	GetExtraUsageSettings(projectID string) (*types.ExtraUsageSettings, error)
}

// identity is the resolved view of the auth context used by
// HandleOAuthProfile. buildIdentity assembles it from the values
// AuthMiddleware already populated; the handler then reshapes it into the
// response below.
type identity struct {
	authKind     string // "api_key" | "oauth_token"
	apiKey       *types.APIKey
	project      *types.Project
	user         *types.User // may be nil when the user row is missing
	subscription *types.Subscription
}

// buildIdentity assembles an identity from the request context. Returns nil
// when AuthMiddleware did not write the expected values (which should be
// impossible if AuthMiddleware passed the request through — the handler
// treats nil as a 500).
//
// Auth-path discrimination uses a load-bearing implicit contract with
// AuthMiddleware: handleTokenIntrospectionAuth constructs a synthetic
// APIKey with ID="" and Name=syntheticOAuthAPIKeyName. The Name match alone
// is enough (a real API key always has a non-empty ID); the ID==""  check
// is belt-and-suspenders against a future code path that fabricates an
// APIKey literal.
func buildIdentity(st identityStore, r *http.Request) *identity {
	apiKey := APIKeyFromContext(r.Context())
	project := ProjectFromContext(r.Context())
	if apiKey == nil || project == nil {
		return nil
	}

	id := &identity{
		apiKey:       apiKey,
		project:      project,
		subscription: SubscriptionFromContext(r.Context()),
	}

	switch {
	case apiKey.ID == "" && apiKey.Name == syntheticOAuthAPIKeyName:
		id.authKind = "oauth_token"
	default:
		id.authKind = "api_key"
	}

	if apiKey.CreatedBy != "" {
		if u, err := st.GetUserByID(apiKey.CreatedBy); err == nil && u != nil {
			id.user = u
		}
	}

	return id
}

// mapPlanToProjectType maps a modelserver Subscription.PlanName onto the
// project_type values surfaced in the response. modelserver's plan set
// (per migrations 040/049/059 etc.) is {free, pro, mini, nano, max_2x..max_240x};
// `pro`, `mini`, `nano`, and the `max_*` family are the paid tiers, so those
// are the only values the mapping can produce. Mini and Nano subscribers see
// project_type="pro" so Claude Code clients (which only recognize pro/max)
// treat them as paid users rather than degrading them to free-tier behavior.
// Anything else (no subscription, free, custom slugs) returns the empty
// string, which omitempty drops from the response — clients then see
// project_type as absent rather than being misled by a synthetic value.
func mapPlanToProjectType(planName string) string {
	switch {
	case planName == "pro" || planName == "mini" || planName == "nano":
		return "pro"
	case strings.HasPrefix(planName, "max_"):
		return "max"
	default:
		return ""
	}
}

// ----- /api/oauth/profile -----

// oauthProfileAccount holds the per-user fields. The shape borrows from
// Claude Code's /api/oauth/profile contract (account.{uuid,email,
// display_name,created_at}) so that downstream consumers with a similar
// mental model can decode the response with familiar field names, even
// though Claude Code itself cannot consume this endpoint as-is (it reads
// an `organization` block this response does not include).
type oauthProfileAccount struct {
	UUID        string `json:"uuid"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"` // RFC3339
}

// oauthProfileProject holds the per-project subscription/billing fields.
// Inner field names mirror Claude Code's `organization` block
// (rate_limit_tier, seat_tier, has_extra_usage_enabled, ...) so the
// per-field semantics stay legible, but the outer key is `project` and
// the type field is `project_type` because modelserver's auth model is
// 1:1 key↔project with no organization layer.
//
// This is a deliberate divergence from Claude Code's contract: Claude
// Code reads t.organization.organization_type and will get undefined
// against this response. The trade-off is that modelserver returns its
// native data shape rather than impersonating Anthropic's multi-tenant
// model.
type oauthProfileProject struct {
	UUID                  string                 `json:"uuid"`
	ProjectType           string                 `json:"project_type,omitempty"`
	RateLimitTier         *string                `json:"rate_limit_tier"`
	SeatTier              *string                `json:"seat_tier"`
	HasExtraUsageEnabled  bool                   `json:"has_extra_usage_enabled"`
	BillingType           *string                `json:"billing_type"`
	CcOnboardingFlags     map[string]interface{} `json:"cc_onboarding_flags"`
	SubscriptionCreatedAt string                 `json:"subscription_created_at,omitempty"` // RFC3339
}

type oauthProfileResponse struct {
	Account oauthProfileAccount `json:"account"`
	Project oauthProfileProject `json:"project"`
}

// HandleOAuthProfile returns the caller's account + project identity in a
// project-scoped shape. OAuth-only: API-key callers get a 401. The path
// name (/api/oauth/profile) is borrowed from Anthropic's Claude Code
// integration but the response shape diverges deliberately — see
// oauthProfileProject docstring.
func (h *Handler) HandleOAuthProfile(w http.ResponseWriter, r *http.Request) {
	writeOAuthProfile(h.store, w, r)
}

func writeOAuthProfile(st identityStore, w http.ResponseWriter, r *http.Request) {
	id := buildIdentity(st, r)
	if id == nil {
		writeProxyError(w, http.StatusInternalServerError, "missing auth context")
		return
	}
	if id.authKind != "oauth_token" {
		writeProxyError(w, http.StatusUnauthorized, "oauth token required")
		return
	}

	resp := oauthProfileResponse{
		Account: oauthProfileAccount{
			UUID: id.apiKey.CreatedBy,
		},
		Project: oauthProfileProject{
			UUID:              id.project.ID,
			CcOnboardingFlags: map[string]interface{}{}, // modelserver has no onboarding concept; always {}
		},
	}
	if id.user != nil {
		resp.Account.Email = id.user.Email
		resp.Account.DisplayName = id.user.Nickname
		if !id.user.CreatedAt.IsZero() {
			resp.Account.CreatedAt = id.user.CreatedAt.Format(time.RFC3339)
		}
	}

	// Subscription-derived fields. AuthMiddleware stores the subscription
	// unconditionally — it does not strip expired/cancelled rows before
	// writing the context value — so guard with IsActive() to avoid
	// echoing a stale paid plan after expiry.
	if id.subscription != nil && id.subscription.IsActive() {
		sub := id.subscription
		if sub.PlanName != "" {
			plan := sub.PlanName
			resp.Project.RateLimitTier = &plan
			resp.Project.SeatTier = &plan
			resp.Project.ProjectType = mapPlanToProjectType(plan)
		}
		if sub.Currency != "" {
			bt := "stripe"
			resp.Project.BillingType = &bt
		}
		if !sub.CreatedAt.IsZero() {
			resp.Project.SubscriptionCreatedAt = sub.CreatedAt.Format(time.RFC3339)
		}
	}

	// Best-effort extra_usage read. Missing settings row or DB error
	// degrades to false rather than 500'ing the identity request.
	if eus, err := st.GetExtraUsageSettings(id.project.ID); err == nil && eus != nil {
		resp.Project.HasExtraUsageEnabled = eus.Enabled
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Vary", "Authorization")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}
