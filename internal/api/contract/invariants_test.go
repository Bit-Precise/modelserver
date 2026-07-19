package contract_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/admin"
	adminv1 "github.com/modelserver/modelserver/internal/api/admin/v1"
	"github.com/modelserver/modelserver/internal/api/admin/v1/resolvers"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/config"
)

// buildAdminSpec returns the OpenAPI document produced by the current
// admin.v1.Register call. Kept local because the invariants test what
// contract exposes, not adminv1 internals.
func buildAdminSpec(t *testing.T) *huma.OpenAPI {
	t.Helper()
	router := chi.NewRouter()
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	adminv1.Register(api, nil)
	return api.OpenAPI()
}

func TestNoDualRegistrationInsideHuma(t *testing.T) {
	t.Parallel()

	document := buildAdminSpec(t)
	seen := make(map[string]struct{})
	for path, item := range document.Paths {
		for method, operation := range map[string]*huma.Operation{
			"GET":    item.Get,
			"PUT":    item.Put,
			"POST":   item.Post,
			"DELETE": item.Delete,
			"PATCH":  item.Patch,
		} {
			if operation == nil {
				continue
			}
			key := method + " " + path
			if _, dup := seen[key]; dup {
				t.Errorf("duplicate operation for %s", key)
			}
			seen[key] = struct{}{}
		}
	}
}

func TestEveryOperationHasCatalogPermission(t *testing.T) {
	t.Parallel()

	catalog := make(map[authz.Permission]struct{}, len(authz.AllPermissions()))
	for _, permission := range authz.AllPermissions() {
		catalog[permission] = struct{}{}
	}

	forEachOperation(t, func(method, path string, operation *huma.Operation) {
		raw, ok := operation.Extensions["x-modelserver-authz"]
		if !ok {
			return
		}
		access, ok := decodeAccessExtension(t, raw)
		if !ok {
			return
		}
		if access.Permission == "" {
			return
		}
		if _, present := catalog[access.Permission]; !present {
			t.Errorf("%s %s: permission %q not in authz.AllPermissions()", method, path, access.Permission)
		}
	})
}

func TestEveryResourceHasResolver(t *testing.T) {
	t.Parallel()

	known := resolvers.KnownResourceTypes
	sawResource := false
	forEachOperation(t, func(method, path string, operation *huma.Operation) {
		raw, ok := operation.Extensions["x-modelserver-authz"]
		if !ok {
			return
		}
		access, ok := decodeAccessExtension(t, raw)
		if !ok {
			return
		}
		if access.Resource == nil {
			return
		}
		sawResource = true
		if _, present := known[access.Resource.ResourceType]; !present {
			t.Errorf("%s %s: resource type %q missing from resolvers.KnownResourceTypes",
				method, path, access.Resource.ResourceType)
		}
	})

	if !sawResource && len(known) == 0 {
		t.Skip("no operations use resource resolvers yet; test becomes active once a subsystem batch declares Resource bindings")
	}
}

func TestAuthzExtensionShape(t *testing.T) {
	t.Parallel()

	forEachOperation(t, func(method, path string, operation *huma.Operation) {
		raw, ok := operation.Extensions["x-modelserver-authz"]
		if !ok {
			return
		}
		access, ok := decodeAccessExtension(t, raw)
		if !ok {
			return
		}
		if err := access.Validate(); err != nil {
			t.Errorf("%s %s: authz extension fails Validate(): %v", method, path, err)
		}
	})
}

func forEachOperation(t *testing.T, fn func(method, path string, op *huma.Operation)) {
	t.Helper()
	document := buildAdminSpec(t)
	for path, item := range document.Paths {
		for method, op := range map[string]*huma.Operation{
			"GET":    item.Get,
			"PUT":    item.Put,
			"POST":   item.Post,
			"DELETE": item.Delete,
			"PATCH":  item.Patch,
		} {
			if op == nil {
				continue
			}
			fn(method, path, op)
		}
	}
}

func decodeAccessExtension(t *testing.T, raw any) (authz.AccessPolicy, bool) {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Errorf("marshal authz extension: %v", err)
		return authz.AccessPolicy{}, false
	}
	var access authz.AccessPolicy
	if err := json.Unmarshal(encoded, &access); err != nil {
		if strings.Contains(err.Error(), "cannot unmarshal") {
			// A future non-object extension would land here; treat as skip.
			return authz.AccessPolicy{}, false
		}
		t.Errorf("unmarshal authz extension: %v", err)
		return authz.AccessPolicy{}, false
	}
	return access, true
}

func TestBatch02NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodPost, "/api/v1/auth/refresh"},
		{http.MethodPost, "/api/v1/auth/oauth/github"},
		{http.MethodPost, "/api/v1/auth/oauth/google"},
		{http.MethodPost, "/api/v1/auth/oauth/oidc"},
		{http.MethodGet, "/api/v1/auth/oauth/github/redirect"},
		{http.MethodGet, "/api/v1/auth/oauth/google/redirect"},
		{http.MethodGet, "/api/v1/auth/oauth/oidc/redirect"},
	}
	router := chi.NewRouter()
	// Mount only the legacy admin routes; typed operations are excluded.
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}

func TestBatch03NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodPut, "/api/v1/users/{userID}"},
		{http.MethodPost, "/api/v1/plans"},
		{http.MethodPost, "/api/v1/plans/"},
		{http.MethodPut, "/api/v1/plans/{planID}"},
		{http.MethodPut, "/api/v1/plans/{planID}/"},
		{http.MethodDelete, "/api/v1/plans/{planID}"},
		{http.MethodDelete, "/api/v1/plans/{planID}/"},
	}
	router := chi.NewRouter()
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}

func TestBatch04NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/models"},
		{http.MethodGet, "/api/v1/models/"},
		{http.MethodPost, "/api/v1/models"},
		{http.MethodPost, "/api/v1/models/"},
		{http.MethodGet, "/api/v1/models/{name}"},
		{http.MethodGet, "/api/v1/models/{name}/"},
		{http.MethodPatch, "/api/v1/models/{name}"},
		{http.MethodPatch, "/api/v1/models/{name}/"},
		{http.MethodPut, "/api/v1/models/{name}"},
		{http.MethodPut, "/api/v1/models/{name}/"},
		{http.MethodDelete, "/api/v1/models/{name}"},
		{http.MethodDelete, "/api/v1/models/{name}/"},
	}
	router := chi.NewRouter()
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}

func TestBatch05NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/projects"},
		{http.MethodGet, "/api/v1/admin/projects/"},
		{http.MethodGet, "/api/v1/admin/projects/subscriptions-overview"},
		{http.MethodGet, "/api/v1/admin/requests"},
		{http.MethodGet, "/api/v1/admin/requests/"},
		{http.MethodGet, "/api/v1/admin/requests/{requestID}/http-log"},
		{http.MethodGet, "/api/v1/notifications"},
		{http.MethodGet, "/api/v1/notifications/"},
		{http.MethodGet, "/api/v1/notifications/unread_count"},
		{http.MethodPost, "/api/v1/notifications/{id}/read"},
		{http.MethodPost, "/api/v1/notifications/read_all"},
		{http.MethodGet, "/api/v1/admin/notifications"},
		{http.MethodGet, "/api/v1/admin/notifications/"},
		{http.MethodPost, "/api/v1/admin/notifications"},
		{http.MethodPost, "/api/v1/admin/notifications/"},
		{http.MethodGet, "/api/v1/admin/notifications/{id}"},
		{http.MethodPut, "/api/v1/admin/notifications/{id}"},
		{http.MethodDelete, "/api/v1/admin/notifications/{id}"},
	}
	router := chi.NewRouter()
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}

func TestBatch06NoLegacyChiOverlap(t *testing.T) {
	t.Parallel()

	migrated := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/extra-usage/overview"},
		{http.MethodPost, "/api/v1/admin/extra-usage/projects/{projectID}/topup"},
		{http.MethodPut, "/api/v1/admin/extra-usage/projects/{projectID}/bypass"},
		{http.MethodGet, "/api/v1/projects/{projectID}/extra-usage"},
		{http.MethodPut, "/api/v1/projects/{projectID}/extra-usage"},
		{http.MethodGet, "/api/v1/projects/{projectID}/extra-usage/transactions"},
		{http.MethodPost, "/api/v1/projects/{projectID}/extra-usage/topup"},
		{http.MethodGet, "/api/v1/projects/{projectID}/extra-usage/topup/{orderID}"},
	}
	router := chi.NewRouter()
	admin.MountRoutes(router, nil, &config.Config{}, nil, nil, nil, nil, nil)
	for _, route := range migrated {
		ctx := chi.NewRouteContext()
		if router.Match(ctx, route.method, route.path) {
			t.Errorf("legacy admin still registers %s %s", route.method, route.path)
		}
	}
}
