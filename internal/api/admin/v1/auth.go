package adminv1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/auth"
	"github.com/modelserver/modelserver/internal/types"
)

type RefreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" minLength:"1" doc:"Refresh token issued by a prior login."`
	}
}

type authTokensBody struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	User         *User  `json:"user,omitempty"`
	RedirectTo   string `json:"redirect_to,omitempty"`
}

type RefreshOutput struct {
	Body authTokensBody
}

// OAuthProvider is a stable enum of supported OAuth providers.
type OAuthProvider string

const (
	OAuthProviderGitHub OAuthProvider = "github"
	OAuthProviderGoogle OAuthProvider = "google"
	OAuthProviderOIDC   OAuthProvider = "oidc"
)

func (OAuthProvider) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:        "string",
		Title:       "OAuthProvider",
		Description: "Supported OAuth identity provider.",
		Enum:        []any{"github", "google", "oidc"},
	}
}

type OAuthCallbackInput struct {
	Provider OAuthProvider `path:"provider" doc:"OAuth provider identifier."`
	Body     struct {
		Code  string `json:"code" minLength:"1" doc:"OAuth authorization code returned by the provider."`
		State string `json:"state,omitempty" doc:"OAuth state parameter. May carry a Hydra return_to encoded as \"<random>|<url>\"."`
	}
}

type OAuthCallbackOutput struct {
	Body authTokensBody
}

type OAuthRedirectInput struct {
	Provider        OAuthProvider `path:"provider" doc:"OAuth provider identifier."`
	ReturnTo        string        `query:"return_to,omitempty" doc:"Optional Hydra login return URL. Only /oauth/login-prefixed values are honored."`
	XForwardedProto string        `header:"X-Forwarded-Proto" doc:"Forwarded protocol scheme from reverse proxy."`
	host            string
}

// Resolve is a Huma hook that fires after standard binding. It populates the
// unexported host from the runtime context, which is where Go's net/http puts
// the incoming Host header (r.Host) as opposed to r.Header, which no longer
// contains it after ReadRequest.
func (i *OAuthRedirectInput) Resolve(ctx huma.Context) []error {
	i.host = ctx.Host()
	return nil
}

// OAuthRedirectOutput streams a 302 redirect to the provider's authorize URL.
// Status is 302; Location carries the target.
type OAuthRedirectOutput struct {
	Status   int    `header:"-" json:"-"`
	Location string `header:"Location" doc:"Provider authorize URL."`
}

func (s *Server) refresh(_ context.Context, input *RefreshInput) (*RefreshOutput, error) {
	if s == nil || s.Auth == nil || s.Tokens == nil || s.JWT == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "auth handlers are not configured", nil)
	}

	claims, err := s.Tokens.ValidateToken(input.Body.RefreshToken)
	if err != nil || claims == nil {
		return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "invalid refresh token", nil)
	}
	if claims.TokenType != "refresh" {
		return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "expected refresh token", nil)
	}

	user, err := s.Auth.GetUserByID(claims.UserID)
	if err != nil || user == nil || user.Status != types.UserStatusActive {
		return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "user not found or disabled", nil)
	}

	access, refresh, err := s.JWT.GenerateTokenPair(user.ID, user.Email, user.IsSuperadmin)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to generate tokens", nil)
	}

	dto := userDTO(user)
	return &RefreshOutput{Body: authTokensBody{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         &dto,
	}}, nil
}

// oauthCallback handles POST /auth/oauth/{provider}.
//
// It preserves the full wire behaviour of the legacy handleOAuthCallback in
// internal/admin/handle_auth.go:
//
//   - Unsupported provider  → 400 bad_request
//   - Unconfigured provider → 501 not_configured
//   - Exchange failure      → 401 "OAuth exchange failed: <err>"
//   - Empty email           → 400 bad_request
//   - User lookup: by OAuth connection first, then by email
//   - Profile sync on existing users (nickname, picture)
//   - First registered user becomes superadmin (MaxProjects=100)
//   - Duplicate-email race on create: retry lookup by email
//   - Auto-create "Default Project" for newly created users
//   - Free-plan assignment delegated via s.AssignFreePlan callback (nil → skip)
//   - Hydra return_to (state "<random>|<url>"): 200 with {redirect_to}, no 302
//   - Normal login: 200 with {access_token, refresh_token, user}
func (s *Server) oauthCallback(ctx context.Context, input *OAuthCallbackInput) (*OAuthCallbackOutput, error) {
	if s == nil || s.Auth == nil || s.Config == nil || s.JWT == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "auth handlers are not configured", nil)
	}

	info, err := s.exchangeOAuthCode(ctx, input.Provider, input.Body.Code)
	if err != nil {
		return nil, err
	}
	if info.Email == "" {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "OAuth provider did not return an email address", nil)
	}

	user, err := s.resolveOrCreateOAuthUser(info)
	if err != nil {
		return nil, err
	}
	if user.Status != types.UserStatusActive {
		return nil, contract.NewError(http.StatusForbidden, "forbidden", "account is disabled", nil)
	}

	// Hydra return_to flow: state encodes "<random>|<return_to>".
	// The response carries {redirect_to}; the frontend performs navigation.
	// Status remains 200 — do NOT emit 302 on a POST.
	if input.Body.State != "" {
		if idx := strings.Index(input.Body.State, "|"); idx >= 0 {
			returnTo := input.Body.State[idx+1:]
			if isValidReturnTo(returnTo) {
				authToken := buildAuthToken(s.EncKey, user.ID)
				redirectURL := appendQueryParam(returnTo, "auth_token", authToken)
				return &OAuthCallbackOutput{Body: authTokensBody{RedirectTo: redirectURL}}, nil
			}
		}
	}

	access, refresh, err := s.JWT.GenerateTokenPair(user.ID, user.Email, user.IsSuperadmin)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to generate tokens", nil)
	}
	dto := userDTO(user)
	return &OAuthCallbackOutput{Body: authTokensBody{
		AccessToken:  access,
		RefreshToken: refresh,
		User:         &dto,
	}}, nil
}

// oauthRedirect handles GET /auth/oauth/{provider}/redirect.
//
// It initiates the OAuth authorization flow by generating a random state token,
// constructing the provider's authorize URL, and emitting a 302 redirect.
//
// Deviation from legacy handleOAuthRedirect: when X-Forwarded-Proto is absent,
// the legacy handler defaulted to "http" unless r.TLS != nil. The typed handler
// cannot access r.TLS, so it defaults to "https". Modern operators use TLS or a
// reverse proxy that sets X-Forwarded-Proto.
func (s *Server) oauthRedirect(ctx context.Context, input *OAuthRedirectInput) (*OAuthRedirectOutput, error) {
	if s == nil || s.Config == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "auth handlers are not configured", nil)
	}

	callbackURL := s.oauthCallbackURL(input)
	state := generateOAuthState()
	if isValidReturnTo(input.ReturnTo) {
		state = state + "|" + input.ReturnTo
	}

	var authURL string
	switch input.Provider {
	case OAuthProviderGitHub:
		if s.Config.Auth.OAuth.GitHub.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "GitHub OAuth not configured", nil)
		}
		gh := auth.NewGitHubOAuth(s.Config.Auth.OAuth.GitHub.ClientID, s.Config.Auth.OAuth.GitHub.ClientSecret, "")
		authURL = gh.AuthCodeURL(state, callbackURL)
	case OAuthProviderGoogle:
		if s.Config.Auth.OAuth.Google.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "Google OAuth not configured", nil)
		}
		g := auth.NewGoogleOAuth(s.Config.Auth.OAuth.Google.ClientID, s.Config.Auth.OAuth.Google.ClientSecret, "")
		authURL = g.AuthCodeURL(state, callbackURL)
	case OAuthProviderOIDC:
		if s.Config.Auth.OAuth.OIDC.IssuerURL == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "OIDC not configured", nil)
		}
		p, err := auth.NewOIDCProvider(ctx, s.Config.Auth.OAuth.OIDC.IssuerURL, s.Config.Auth.OAuth.OIDC.ClientID, s.Config.Auth.OAuth.OIDC.ClientSecret, callbackURL)
		if err != nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to initialize OIDC", nil)
		}
		authURL = p.AuthCodeURL(state, callbackURL)
	default:
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "unsupported provider", nil)
	}

	return &OAuthRedirectOutput{
		Status:   http.StatusFound,
		Location: authURL,
	}, nil
}

// oauthCallbackURL returns the OAuth callback URL for this provider.
// For OIDC, the configured RedirectURI is used when set. Otherwise the
// URL is constructed from the X-Forwarded-Proto and Host headers.
func (s *Server) oauthCallbackURL(input *OAuthRedirectInput) string {
	if input.Provider == OAuthProviderOIDC && s.Config.Auth.OAuth.OIDC.RedirectURI != "" {
		return s.Config.Auth.OAuth.OIDC.RedirectURI
	}
	scheme := input.XForwardedProto
	if scheme == "" {
		// Default to https; operators without TLS termination in front should
		// set X-Forwarded-Proto explicitly. This is a documented deviation
		// from the legacy handler, which defaulted to http.
		scheme = "https"
	}
	return scheme + "://" + input.host + "/auth/callback/" + string(input.Provider)
}

// generateOAuthState generates a 16-byte cryptographically random hex state token.
func generateOAuthState() string {
	stateBytes := make([]byte, 16)
	_, _ = rand.Read(stateBytes)
	return hex.EncodeToString(stateBytes)
}

// exchangeOAuthCode dispatches the OAuth code exchange to the appropriate
// provider client and returns the normalised OAuthUserInfo on success.
func (s *Server) exchangeOAuthCode(ctx context.Context, provider OAuthProvider, code string) (*auth.OAuthUserInfo, error) {
	switch provider {
	case OAuthProviderGitHub:
		if s.Config.Auth.OAuth.GitHub.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "GitHub OAuth not configured", nil)
		}
		gh := auth.NewGitHubOAuth(s.Config.Auth.OAuth.GitHub.ClientID, s.Config.Auth.OAuth.GitHub.ClientSecret, "")
		info, err := gh.ExchangeAndGetUser(ctx, code)
		if err != nil {
			return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "OAuth exchange failed: "+err.Error(), nil)
		}
		return info, nil
	case OAuthProviderGoogle:
		if s.Config.Auth.OAuth.Google.ClientID == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "Google OAuth not configured", nil)
		}
		g := auth.NewGoogleOAuth(s.Config.Auth.OAuth.Google.ClientID, s.Config.Auth.OAuth.Google.ClientSecret, "")
		info, err := g.ExchangeAndGetUser(ctx, code)
		if err != nil {
			return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "OAuth exchange failed: "+err.Error(), nil)
		}
		return info, nil
	case OAuthProviderOIDC:
		if s.Config.Auth.OAuth.OIDC.IssuerURL == "" {
			return nil, contract.NewError(http.StatusNotImplemented, "not_configured", "OIDC not configured", nil)
		}
		p, err := auth.NewOIDCProvider(ctx, s.Config.Auth.OAuth.OIDC.IssuerURL, s.Config.Auth.OAuth.OIDC.ClientID, s.Config.Auth.OAuth.OIDC.ClientSecret, s.Config.Auth.OAuth.OIDC.RedirectURI)
		if err != nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to initialize OIDC", nil)
		}
		info, err := p.ExchangeAndGetUser(ctx, code)
		if err != nil {
			return nil, contract.NewError(http.StatusUnauthorized, "unauthorized", "OAuth exchange failed: "+err.Error(), nil)
		}
		return info, nil
	default:
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "unsupported provider", nil)
	}
}

// resolveOrCreateOAuthUser looks up the user by OAuth connection then by
// email, syncing profile fields if found. If not found, it creates a new
// user. The first registered user becomes superadmin (MaxProjects=100). On a
// duplicate-email create race, it retries the email lookup.
func (s *Server) resolveOrCreateOAuthUser(info *auth.OAuthUserInfo) (*types.User, error) {
	// Look up user: by OAuth connection first, then by email.
	user, _ := s.Auth.GetUserByOAuth(info.Provider, info.ProviderID)
	if user == nil {
		user, _ = s.Auth.GetUserByEmail(info.Email)
	}

	if user != nil {
		// Existing user — ensure OAuth connection exists and sync profile fields.
		_ = s.Auth.CreateOAuthConnection(user.ID, info.Provider, info.ProviderID)
		updates := map[string]any{}
		if info.Name != "" && info.Name != user.Nickname {
			updates["nickname"] = info.Name
		}
		if info.Picture != "" && info.Picture != user.Picture {
			updates["picture"] = info.Picture
		}
		if len(updates) > 0 {
			if err := s.Auth.UpdateUser(user.ID, updates); err != nil {
				log.Printf("WARN: failed to update OAuth user %s: %v", user.ID, err)
			}
			if fresh, err := s.Auth.GetUserByID(user.ID); err == nil && fresh != nil {
				user = fresh
			}
		}
		return user, nil
	}

	// New user — first registered user becomes superadmin.
	isFirst := false
	if exists, err := s.Auth.UserExists(); err == nil && !exists {
		isFirst = true
	}
	user = &types.User{
		Email:        info.Email,
		Nickname:     info.Name,
		Picture:      info.Picture,
		IsSuperadmin: isFirst,
		MaxProjects:  5,
		Status:       types.UserStatusActive,
	}
	if isFirst {
		user.MaxProjects = 100
	}
	if err := s.Auth.CreateUser(user); err != nil {
		// Likely a duplicate email — race or stale lookup. Retry by email.
		log.Printf("WARN: create OAuth user failed (email=%s): %v, retrying lookup", info.Email, err)
		user, _ = s.Auth.GetUserByEmail(info.Email)
		if user == nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to create user", nil)
		}
	} else {
		// Auto-create default project for new OAuth user.
		// Inline assignFreePlan for now; will be extracted to a shared helper
		// when Batch 7 migrates the projects CRUD path. See server.go for the
		// AssignFreePlan wiring shim.
		project := &types.Project{
			Name:      "Default Project",
			CreatedBy: user.ID,
			Status:    types.ProjectStatusActive,
		}
		if err := s.Auth.CreateProject(project); err != nil {
			log.Printf("WARN: failed to create default project for OAuth user %s: %v", user.ID, err)
		} else if s.AssignFreePlan != nil {
			s.AssignFreePlan(project.ID)
		}
	}
	_ = s.Auth.CreateOAuthConnection(user.ID, info.Provider, info.ProviderID)
	return user, nil
}
