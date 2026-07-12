package adminv1

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// fakeModelsStore records ListModels, ListModelsByStatus, GetModelByName, and
// ModelReferenceCountsFor calls and satisfies modelsStore.
type fakeModelsStore struct {
	models            []types.Model
	listErr           error
	listByStatusErr   error
	model             *types.Model
	getErr            error
	referenceErr      error
	createErr         error
	createdModel      *types.Model
	lastListedStatus  string
	lastQueriedName   string
	callRecords       struct {
		listModels           int
		listModelsByStatus   int
		getModelByName       int
		modelReferenceCounts int
		createModel          int
	}
}

func (s *fakeModelsStore) ListModels() ([]types.Model, error) {
	s.callRecords.listModels++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.models, nil
}

func (s *fakeModelsStore) ListModelsByStatus(status string) ([]types.Model, error) {
	s.callRecords.listModelsByStatus++
	s.lastListedStatus = status
	if s.listByStatusErr != nil {
		return nil, s.listByStatusErr
	}
	return s.models, nil
}

func (s *fakeModelsStore) GetModelByName(name string) (*types.Model, error) {
	s.callRecords.getModelByName++
	s.lastQueriedName = name
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.model, nil
}

func (s *fakeModelsStore) ModelReferenceCountsFor(name string) (store.ModelReferenceCounts, error) {
	s.callRecords.modelReferenceCounts++
	if s.referenceErr != nil {
		return store.ModelReferenceCounts{}, s.referenceErr
	}
	return store.ModelReferenceCounts{}, nil
}

func (s *fakeModelsStore) CreateModel(m *types.Model) error {
	s.callRecords.createModel++
	if s.createErr != nil {
		return s.createErr
	}
	s.createdModel = m
	return nil
}

func (s *fakeModelsStore) UpdateModel(name string, updates map[string]any) error {
	return nil
}

func (s *fakeModelsStore) DeleteModel(name string) error {
	return nil
}

// newModelsServer returns a Server wired for models tests.
func newModelsServer(_ *testing.T, store *fakeModelsStore) *Server {
	return &Server{
		Models: store,
	}
}

// newModelsServerWithCatalog returns a Server wired for models write tests.
func newModelsServerWithCatalog(_ *testing.T, store *fakeModelsStore, catalog modelcatalog.Catalog) *Server {
	return &Server{
		Models:  store,
		Catalog: catalog,
	}
}

// --- listModels Tests (4) ---

// Test 1: Store error on ListModels → 500 internal
func TestListModelsStoreError(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		listErr: errors.New("database connection lost"),
	}
	s := newModelsServer(t, store)
	input := &ListModelsInput{}

	_, err := s.listModels(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to list models" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to list models")
	}
}

// Test 2: Empty status → ListModels called, not ListModelsByStatus
func TestListModelsEmptyStatus(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		models: []types.Model{
			{Name: "gpt-4", DisplayName: "GPT-4", Status: "active"},
		},
	}
	s := newModelsServer(t, store)
	input := &ListModelsInput{Status: ""}

	output, err := s.listModels(context.Background(), input)
	if err != nil {
		t.Fatalf("listModels() error = %v", err)
	}

	if store.callRecords.listModels != 1 {
		t.Errorf("ListModels called %d times, want 1", store.callRecords.listModels)
	}
	if store.callRecords.listModelsByStatus != 0 {
		t.Errorf("ListModelsByStatus called %d times, want 0", store.callRecords.listModelsByStatus)
	}

	if output == nil || len(output.Body.Data) != 1 {
		t.Fatalf("expected 1 row in output, got %v", output)
	}
	if output.Body.Data[0].Name != "gpt-4" {
		t.Errorf("model name = %q, want %q", output.Body.Data[0].Name, "gpt-4")
	}
}

// Test 3: Status="active" → ListModelsByStatus called with "active"
func TestListModelsWithStatus(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		models: []types.Model{
			{Name: "gpt-4", DisplayName: "GPT-4", Status: "active"},
		},
	}
	s := newModelsServer(t, store)
	input := &ListModelsInput{Status: "active"}

	output, err := s.listModels(context.Background(), input)
	if err != nil {
		t.Fatalf("listModels() error = %v", err)
	}

	if store.callRecords.listModels != 0 {
		t.Errorf("ListModels called %d times, want 0", store.callRecords.listModels)
	}
	if store.callRecords.listModelsByStatus != 1 {
		t.Errorf("ListModelsByStatus called %d times, want 1", store.callRecords.listModelsByStatus)
	}
	if store.lastListedStatus != "active" {
		t.Errorf("status passed to ListModelsByStatus = %q, want %q", store.lastListedStatus, "active")
	}

	if output == nil || len(output.Body.Data) != 1 {
		t.Fatalf("expected 1 row in output, got %v", output)
	}
}

// Test 4: Reference counts store error mid-loop → 500 internal "failed to count references"
func TestListModelsReferenceCountsError(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		models: []types.Model{
			{Name: "gpt-4", DisplayName: "GPT-4", Status: "active"},
		},
		referenceErr: errors.New("failed to query references"),
	}
	s := newModelsServer(t, store)
	input := &ListModelsInput{}

	_, err := s.listModels(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to count references: failed to query references" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to count references: failed to query references")
	}
}

// --- getModel Tests (3) ---

// Test 5: Store error → 500 internal "failed to get model"
func TestGetModelStoreError(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		getErr: errors.New("database connection lost"),
	}
	s := newModelsServer(t, store)
	input := &GetModelInput{Name: "gpt-4"}

	_, err := s.getModel(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to get model" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to get model")
	}
}

// Test 6: Nil model → 404 not_found "model not found"
func TestGetModelNotFound(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		model: nil,
	}
	s := newModelsServer(t, store)
	input := &GetModelInput{Name: "nonexistent"}

	_, err := s.getModel(context.Background(), input)
	assertStatusError(t, err, 404, "not_found")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "model not found" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "model not found")
	}
}

// Test 7: Happy path → 200 with {data: model}
func TestGetModelHappyPath(t *testing.T) {
	t.Parallel()
	store := &fakeModelsStore{
		model: &types.Model{
			Name:        "gpt-4",
			DisplayName: "GPT-4",
			Description: "Advanced model",
			Status:      "active",
			Publisher:   "OpenAI",
		},
	}
	s := newModelsServer(t, store)
	input := &GetModelInput{Name: "gpt-4"}

	output, err := s.getModel(context.Background(), input)
	if err != nil {
		t.Fatalf("getModel() error = %v", err)
	}

	if output == nil {
		t.Fatal("expected output, got nil")
	}
	if output.Body.Data.Name != "gpt-4" {
		t.Errorf("model name = %q, want %q", output.Body.Data.Name, "gpt-4")
	}
	if output.Body.Data.DisplayName != "GPT-4" {
		t.Errorf("model display_name = %q, want %q", output.Body.Data.DisplayName, "GPT-4")
	}
	if output.Body.Data.Description != "Advanced model" {
		t.Errorf("model description = %q, want %q", output.Body.Data.Description, "Advanced model")
	}
	if output.Body.Data.Status != "active" {
		t.Errorf("model status = %q, want %q", output.Body.Data.Status, "active")
	}
	if output.Body.Data.Publisher != "OpenAI" {
		t.Errorf("model publisher = %q, want %q", output.Body.Data.Publisher, "OpenAI")
	}
}

// --- createModel Tests (7) ---

// assertModelError checks status, code, and message on a contract error.
func assertModelError(t *testing.T, err error, wantStatus int, wantCode, wantMsg string) {
	t.Helper()
	assertStatusError(t, err, wantStatus, wantCode)
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != wantMsg {
		t.Errorf("message = %q, want %q", env.Payload.Message, wantMsg)
	}
}

// newCreateModelInput returns a CreateModelInput with the given name and publisher.
func newCreateModelInput(name, publisher string) *CreateModelInput {
	in := &CreateModelInput{}
	in.Body.Name = name
	in.Body.Publisher = publisher
	return in
}

// Test 1: Missing name → 400 "name is required"
func TestCreateModelMissingName(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newCreateModelInput("", "openai")

	_, err := s.createModel(context.Background(), in)
	assertModelError(t, err, 400, "bad_request", "name is required")
}

// Test 2: Uppercase in name → 400 "name must be lowercase"
func TestCreateModelUppercaseName(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newCreateModelInput("GPT-4", "openai")

	_, err := s.createModel(context.Background(), in)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.HTTPStatus != 400 {
		t.Errorf("status = %d, want 400", env.HTTPStatus)
	}
	if env.Payload.Code != "bad_request" {
		t.Errorf("code = %q, want %q", env.Payload.Code, "bad_request")
	}
	if !errors.Is(err, err) { // always true; real check is message prefix
		t.Fatal("unexpected error shape")
	}
	// The message begins with "name must be lowercase"
	msg := env.Payload.Message
	if len(msg) < len("name must be lowercase") || msg[:len("name must be lowercase")] != "name must be lowercase" {
		t.Errorf("message = %q, want prefix %q", msg, "name must be lowercase")
	}
}

// Test 3: Duplicate alias → 400 "duplicate alias"
func TestCreateModelDuplicateAlias(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newCreateModelInput("gpt-4", "openai")
	in.Body.Aliases = []string{"alias-a", "alias-a"}

	_, err := s.createModel(context.Background(), in)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.HTTPStatus != 400 {
		t.Errorf("status = %d, want 400", env.HTTPStatus)
	}
	msg := env.Payload.Message
	if len(msg) < len("duplicate alias") || msg[:len("duplicate alias")] != "duplicate alias" {
		t.Errorf("message = %q, want prefix %q", msg, "duplicate alias")
	}
}

// Test 4: Alias equals canonical → 400 "alias ... cannot equal canonical name"
func TestCreateModelAliasEqualsCanonical(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newCreateModelInput("gpt-4", "openai")
	in.Body.Aliases = []string{"gpt-4"}

	_, err := s.createModel(context.Background(), in)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.HTTPStatus != 400 {
		t.Errorf("status = %d, want 400", env.HTTPStatus)
	}
	msg := env.Payload.Message
	const want = "cannot equal canonical name"
	if len(msg) < len(want) {
		t.Errorf("message = %q, does not contain %q", msg, want)
	} else {
		found := false
		for i := 0; i <= len(msg)-len(want); i++ {
			if msg[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("message = %q, want it to contain %q", msg, want)
		}
	}
}

// Test 5: Missing publisher → 400 "publisher is required"
func TestCreateModelMissingPublisher(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newCreateModelInput("gpt-4", "")

	_, err := s.createModel(context.Background(), in)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.HTTPStatus != 400 {
		t.Errorf("status = %d, want 400", env.HTTPStatus)
	}
	msg := env.Payload.Message
	const want = "publisher is required"
	if len(msg) < len(want) || msg[:len(want)] != want {
		t.Errorf("message = %q, want prefix %q", msg, want)
	}
}

// Test 6: Unique-violation from store → 409 conflict
func TestCreateModelUniqueViolation(t *testing.T) {
	t.Parallel()
	pgErr := &pgconn.PgError{Code: "23505"}
	st := &fakeModelsStore{createErr: pgErr}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newCreateModelInput("gpt-4", "openai")

	_, err := s.createModel(context.Background(), in)
	assertStatusError(t, err, 409, "conflict")
}

// Test 7: Happy path → 201 with {data: model}; DisplayName defaults to Name; catalog.Swap called
func TestCreateModelHappyPath(t *testing.T) {
	t.Parallel()
	freshModels := []types.Model{
		{Name: "gpt-4", DisplayName: "gpt-4", Status: "active", Publisher: "openai"},
	}
	st := &fakeModelsStore{models: freshModels}
	cat := &fakeCatalog{}
	s := newModelsServerWithCatalog(t, st, cat)
	in := newCreateModelInput("gpt-4", "openai")
	in.Body.Status = "active"
	// DisplayName intentionally empty — should default to Name.

	out, err := s.createModel(context.Background(), in)
	if err != nil {
		t.Fatalf("createModel() error = %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	if out.Body.Data.Name != "gpt-4" {
		t.Errorf("Name = %q, want %q", out.Body.Data.Name, "gpt-4")
	}
	if out.Body.Data.DisplayName != "gpt-4" {
		t.Errorf("DisplayName = %q, want %q (should default to Name)", out.Body.Data.DisplayName, "gpt-4")
	}
	if out.Body.Data.Publisher != "openai" {
		t.Errorf("Publisher = %q, want %q", out.Body.Data.Publisher, "openai")
	}

	// refreshCatalog must have been called — Swap should have been called
	// with the fresh model list from ListModels.
	if cat.swappedModels == nil {
		t.Fatal("Catalog.Swap was not called after createModel")
	}
	if len(cat.swappedModels) != 1 || cat.swappedModels[0].Name != "gpt-4" {
		t.Errorf("swappedModels = %v, want [{gpt-4 ...}]", cat.swappedModels)
	}
}
