// Package contract provides the shared Go-first HTTP contract infrastructure
// for ModelServer's management API.
package contract

import (
	"io"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

const (
	// AdminJWTSecurityScheme is the OpenAPI component name used by management
	// operations authenticated with an admin access token.
	AdminJWTSecurityScheme = "AdminJWT"

	adminOpenAPIPath = "/api-docs/openapi"
	adminDocsPath    = "/api-docs"
)

// APIOptions controls runtime-only endpoints. Contract generation disables
// them so the generated specification contains only product operations.
type APIOptions struct {
	ServeDocs bool
}

// NewAdminAPI creates the Huma API which is mounted on the existing chi
// router. The default schema-link transformer is deliberately disabled: it
// would add $schema fields and Link headers to v1 response payloads, changing
// the existing wire contract.
func NewAdminAPI(router chi.Router, options APIOptions) huma.API {
	configureErrors()

	config := huma.DefaultConfig("ModelServer Management API", "1.0.0")
	config.OpenAPI.Info.Description = "Typed management API used by the ModelServer dashboard. Proxy/LLM compatibility endpoints are intentionally excluded."
	config.OpenAPI.Servers = []*huma.Server{{URL: "/"}}
	config.OpenAPI.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		AdminJWTSecurityScheme: {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "ModelServer admin access token.",
		},
	}

	// Register application/octet-stream so that BytesResponse bodies are
	// streamed verbatim without going through the JSON pipeline.
	config.Formats["application/octet-stream"] = huma.Format{
		Marshal: func(w io.Writer, v any) error {
			if br, ok := v.(BytesResponse); ok && br.Reader != nil {
				_, err := io.Copy(w, br.Reader)
				return err
			}
			return nil
		},
	}

	// DefaultConfig installs a response transformer which changes response
	// bodies. Keep Huma's JSON schema registry, but do not expose or inject
	// per-schema links for the existing /api/v1 contract.
	config.CreateHooks = nil
	config.SchemasPath = ""
	config.OpenAPIPath = ""
	config.DocsPath = ""
	if options.ServeDocs {
		config.OpenAPIPath = adminOpenAPIPath
		config.DocsPath = adminDocsPath
		config.DocsRenderer = huma.DocsRendererScalar
	}

	return humachi.New(router, config)
}
