// Command openapi generates the management API contract without starting the
// server or connecting to a database.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-chi/chi/v5"
	adminv1 "github.com/modelserver/modelserver/internal/api/admin/v1"
	"github.com/modelserver/modelserver/internal/api/contract"
)

func main() {
	output := flag.String("output", "api/openapi/admin.openapi.json", "path to the generated OpenAPI document")
	flag.Parse()

	router := chi.NewRouter()
	api := contract.NewAdminAPI(router, contract.APIOptions{})
	adminv1.Register(api, nil)

	document, err := json.MarshalIndent(api.OpenAPI(), "", "  ")
	if err != nil {
		fail("encode OpenAPI document: %v", err)
	}
	document = append(document, '\n')

	if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
		fail("create output directory: %v", err)
	}
	if err := os.WriteFile(*output, document, 0o644); err != nil {
		fail("write %s: %v", *output, err)
	}
}

func fail(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, "openapi: "+format+"\n", args...)
	os.Exit(1)
}
