package adminv1

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/api/contract"
)

func TestCommittedOpenAPIDocumentIsCurrent(t *testing.T) {
	router := chi.NewRouter()
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	Register(api, nil)

	generated, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		t.Fatalf("encode generated OpenAPI: %v", err)
	}
	generated = append(generated, '\n')

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	repositoryRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", "..", ".."))
	contractPath := filepath.Join(repositoryRoot, "api", "openapi", "admin.openapi.json")
	committed, err := os.ReadFile(contractPath)
	if err != nil {
		t.Fatalf("read committed OpenAPI document: %v", err)
	}

	if !bytes.Equal(generated, committed) {
		t.Fatalf("%s is stale; run `go run ./cmd/openapi` from the repository root", contractPath)
	}
}
