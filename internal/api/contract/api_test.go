package contract

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/authz"
)

type hiddenOperationInput struct{}

type hiddenOperationOutput struct {
	Body map[string]string
}

func TestNewAdminAPIDisablesRuntimeDocsForGeneration(t *testing.T) {
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})

	if api.OpenAPI().OpenAPI != "3.1.0" {
		t.Fatalf("OpenAPI version = %q, want 3.1.0", api.OpenAPI().OpenAPI)
	}
	if _, ok := api.OpenAPI().Components.SecuritySchemes[AdminJWTSecurityScheme]; !ok {
		t.Fatalf("missing %s security scheme", AdminJWTSecurityScheme)
	}

	req := httptest.NewRequest(http.MethodGet, adminOpenAPIPath+".json", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled OpenAPI endpoint status = %d, want 404", rec.Code)
	}
}

func TestNewAdminAPIServesOpenAPIWhenEnabled(t *testing.T) {
	router := chi.NewRouter()
	NewAdminAPI(router, APIOptions{ServeDocs: true})

	req := httptest.NewRequest(http.MethodGet, adminOpenAPIPath+".json", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("OpenAPI endpoint status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode OpenAPI document: %v", err)
	}
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("OpenAPI document version = %#v", doc["openapi"])
	}
}

func TestManagementErrorEnvelope(t *testing.T) {
	err := NewError(http.StatusForbidden, "forbidden", "access denied", nil)
	b, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatalf("marshal error: %v", marshalErr)
	}
	want := `{"error":{"code":"forbidden","message":"access denied"}}`
	if string(b) != want {
		t.Fatalf("error JSON = %s, want %s", b, want)
	}
	if err.GetStatus() != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", err.GetStatus())
	}
}

func TestHiddenOperationIsRoutableButExcludedFromOpenAPI(t *testing.T) {
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})
	Register(api, Operation{
		ID:            "legacyHiddenAlias",
		Method:        http.MethodGet,
		Path:          "/legacy/",
		Hidden:        true,
		DefaultStatus: http.StatusOK,
		Access:        authz.Public(),
	}, func(context.Context, *hiddenOperationInput) (*hiddenOperationOutput, error) {
		return &hiddenOperationOutput{Body: map[string]string{"status": "ok"}}, nil
	})

	if api.OpenAPI().Paths["/legacy/"] != nil {
		t.Fatal("hidden operation was included in OpenAPI")
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/legacy/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("hidden route status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if got, want := recorder.Body.String(), "{\"status\":\"ok\"}\n"; got != want {
		t.Fatalf("hidden route body = %q, want %q", got, want)
	}
}
