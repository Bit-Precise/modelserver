package contract

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/authz"
)

type aliasInput struct{}
type aliasOutput struct {
	Body map[string]string
}

func TestRegisterWithLegacyTrailingSlashRegistersBothSpellings(t *testing.T) {
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})

	RegisterWithLegacyTrailingSlash(api, Operation{
		ID:            "listExamples",
		Method:        http.MethodGet,
		Path:          "/api/v1/examples",
		DefaultStatus: http.StatusOK,
		Access:        authz.Public(),
	}, func(context.Context, *aliasInput) (*aliasOutput, error) {
		return &aliasOutput{Body: map[string]string{"ok": "yes"}}, nil
	})

	for _, target := range []string{"/api/v1/examples", "/api/v1/examples/"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("GET %s status = %d, body = %s", target, recorder.Code, recorder.Body.String())
		}
	}

	// The canonical path is present in the OpenAPI document; the trailing-slash
	// alias is hidden.
	if api.OpenAPI().Paths["/api/v1/examples"] == nil {
		t.Fatal("canonical path missing from OpenAPI")
	}
	if api.OpenAPI().Paths["/api/v1/examples/"] != nil {
		t.Fatal("trailing-slash alias was emitted into OpenAPI")
	}
}
