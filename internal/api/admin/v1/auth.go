package adminv1

import (
	"github.com/danielgtaylor/huma/v2"

	"github.com/modelserver/modelserver/internal/types"
)

type RefreshInput struct {
	Body struct {
		RefreshToken string `json:"refresh_token" minLength:"1" doc:"Refresh token issued by a prior login."`
	}
}

type authTokensBody struct {
	AccessToken  string      `json:"access_token,omitempty"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	User         *types.User `json:"user,omitempty"`
	RedirectTo   string      `json:"redirect_to,omitempty"`
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
	XForwardedProto string        `header:"X-Forwarded-Proto,omitempty" doc:"Forwarded protocol scheme from reverse proxy."`
	Host            string        `header:"Host,omitempty" doc:"HTTP Host header for callback URL construction."`
}

// OAuthRedirectOutput streams a 302 redirect to the provider's authorize URL.
// Status is 302; Location carries the target.
type OAuthRedirectOutput struct {
	Status   int    `header:"-" json:"-"`
	Location string `header:"Location" doc:"Provider authorize URL."`
}
