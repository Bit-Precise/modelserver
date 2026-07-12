package adminv1

import (
	"context"
	"errors"
	"testing"

	"github.com/modelserver/modelserver/internal/api/contract"
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
	lastListedStatus  string
	lastQueriedName   string
	callRecords       struct {
		listModels          int
		listModelsByStatus  int
		getModelByName      int
		modelReferenceCounts int
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

func (s *fakeModelsStore) CreateModel(*types.Model) error {
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
