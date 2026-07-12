package adminv1

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	referenceCounts   store.ModelReferenceCounts
	createErr         error
	createdModel      *types.Model
	updateErr         error
	deleteErr         error
	lastDeletedName   string
	lastUpdatedName   string
	lastUpdates       map[string]any
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
	return s.referenceCounts, nil
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
	s.lastUpdatedName = name
	s.lastUpdates = updates
	return s.updateErr
}

func (s *fakeModelsStore) DeleteModel(name string) error {
	s.lastDeletedName = name
	if s.deleteErr != nil {
		return s.deleteErr
	}
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

// --- updateModel Tests (9) ---

// newUpdateModelInput returns an UpdateModelInput for the given path name.
func newUpdateModelInput(pathName string) *UpdateModelInput {
	in := &UpdateModelInput{}
	in.Name = pathName
	return in
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return &s }

// Test U1: GetModelByName returns nil → 404.
func TestUpdateModelNotFound(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: nil}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newUpdateModelInput("no-such-model")
	in.Body.DisplayName = strPtr("New Name")

	_, err := s.updateModel(context.Background(), in)
	assertStatusError(t, err, 404, "not_found")
}

// Test U2: Body has non-nil Name pointer → 400 "canonical name is immutable".
func TestUpdateModelNameImmutable(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"}}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newUpdateModelInput("gpt-4")
	in.Body.Name = strPtr("gpt-5")

	_, err := s.updateModel(context.Background(), in)
	assertModelError(t, err, 400, "bad_request", "canonical name is immutable; create a new model and retire this one instead")
}

// Test U3: Empty body (all nil/zero) → 400 "no valid fields to update".
func TestUpdateModelEmptyBody(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"}}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newUpdateModelInput("gpt-4")
	// All fields are zero/nil — nothing set.

	_, err := s.updateModel(context.Background(), in)
	assertModelError(t, err, 400, "bad_request", "no valid fields to update")
}

// Test U4: Invalid status → 400 "status must be active or disabled".
func TestUpdateModelInvalidStatus(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"}}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newUpdateModelInput("gpt-4")
	in.Body.Status = strPtr("broken")

	_, err := s.updateModel(context.Background(), in)
	assertModelError(t, err, 400, "bad_request", "status must be active or disabled")
}

// Test U5: Duplicate alias → 400 containing "duplicate alias".
func TestUpdateModelDuplicateAlias(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"}}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newUpdateModelInput("gpt-4")
	aliases := []string{"alias-a", "alias-a"}
	in.Body.Aliases = &aliases

	_, err := s.updateModel(context.Background(), in)
	assertStatusError(t, err, 400, "bad_request")
	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if !strings.Contains(env.Payload.Message, "duplicate alias") {
		t.Errorf("message = %q, want it to contain %q", env.Payload.Message, "duplicate alias")
	}
}

// Test U6: default_credit_rate: null → updates["default_credit_rate"] is nil.
func TestUpdateModelCreditRateNull(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{
		model:  &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"},
		models: []types.Model{{Name: "gpt-4"}},
	}
	cat := &fakeCatalog{}
	s := newModelsServerWithCatalog(t, st, cat)
	in := newUpdateModelInput("gpt-4")
	in.Body.DefaultCreditRate = json.RawMessage("null")

	_, err := s.updateModel(context.Background(), in)
	if err != nil {
		t.Fatalf("updateModel() unexpected error: %v", err)
	}
	val, ok := st.lastUpdates["default_credit_rate"]
	if !ok {
		t.Fatal("updates map missing key default_credit_rate")
	}
	if val != nil {
		t.Errorf("updates[default_credit_rate] = %v, want nil", val)
	}
}

// Test U7: default_credit_rate with input_rate: -1 → 400 "credit rates must be non-negative".
func TestUpdateModelCreditRateNegative(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"}}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	in := newUpdateModelInput("gpt-4")
	in.Body.DefaultCreditRate = json.RawMessage(`{"input_rate": -1, "output_rate": 0, "cache_creation_rate": 0, "cache_read_rate": 0}`)

	_, err := s.updateModel(context.Background(), in)
	assertModelError(t, err, 400, "bad_request", "credit rates must be non-negative")
}

// Test U8: Valid default_image_credit_rate object → updates["default_image_credit_rate"] is []byte.
func TestUpdateModelImageCreditRateObject(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{
		model:  &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"},
		models: []types.Model{{Name: "gpt-4"}},
	}
	cat := &fakeCatalog{}
	s := newModelsServerWithCatalog(t, st, cat)
	in := newUpdateModelInput("gpt-4")
	in.Body.DefaultImageCreditRate = json.RawMessage(`{"text_input_rate":1,"text_cached_input_rate":0,"text_output_rate":2,"image_input_rate":3,"image_cached_input_rate":0,"image_output_rate":4}`)

	_, err := s.updateModel(context.Background(), in)
	if err != nil {
		t.Fatalf("updateModel() unexpected error: %v", err)
	}
	val, ok := st.lastUpdates["default_image_credit_rate"]
	if !ok {
		t.Fatal("updates map missing key default_image_credit_rate")
	}
	if _, isBytes := val.([]byte); !isBytes {
		t.Errorf("updates[default_image_credit_rate] type = %T, want []byte", val)
	}
}

// Test U9: Happy path with several fields → 200; refresh called; refetch via GetModelByName.
func TestUpdateModelHappyPath(t *testing.T) {
	t.Parallel()
	updated := &types.Model{Name: "gpt-4", DisplayName: "GPT-4 Updated", Status: "disabled", Publisher: "openai"}
	st := &fakeModelsStore{
		model:  &types.Model{Name: "gpt-4", Publisher: "openai", Status: "active"},
		models: []types.Model{{Name: "gpt-4"}},
	}
	// After UpdateModel is called, GetModelByName should return the updated model.
	// We simulate this by swapping model after update via a closure approach:
	// The fakeModelsStore always returns st.model for GetModelByName;
	// we set st.model to `updated` before calling so the refetch returns updated.
	// Actually to test refetch after update, we need model to be returned on both
	// the initial fetch and the refetch. Set model = existing initially; the handler
	// will refetch after update, and since st.model stays as updated we pre-set it
	// to the updated version. The first call (existence check) uses it too, so both
	// return the same model — that's fine: the handler only checks nil on the first call.
	st.model = updated
	cat := &fakeCatalog{}
	s := newModelsServerWithCatalog(t, st, cat)
	in := newUpdateModelInput("gpt-4")
	in.Body.DisplayName = strPtr("GPT-4 Updated")
	in.Body.Status = strPtr("disabled")

	out, err := s.updateModel(context.Background(), in)
	if err != nil {
		t.Fatalf("updateModel() unexpected error: %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}

	// Assert updates map contains the expected fields.
	if st.lastUpdatedName != "gpt-4" {
		t.Errorf("lastUpdatedName = %q, want %q", st.lastUpdatedName, "gpt-4")
	}
	if st.lastUpdates["display_name"] != "GPT-4 Updated" {
		t.Errorf("updates[display_name] = %v, want %q", st.lastUpdates["display_name"], "GPT-4 Updated")
	}
	if st.lastUpdates["status"] != "disabled" {
		t.Errorf("updates[status] = %v, want %q", st.lastUpdates["status"], "disabled")
	}

	// refreshCatalog must have been called.
	if cat.swappedModels == nil {
		t.Fatal("Catalog.Swap was not called after updateModel")
	}

	// GetModelByName must have been called at least twice (once for existence check, once for refetch).
	if st.callRecords.getModelByName < 2 {
		t.Errorf("GetModelByName called %d times, want >= 2", st.callRecords.getModelByName)
	}

	// Response carries the refetched model.
	if out.Body.Data.Name != "gpt-4" {
		t.Errorf("response Name = %q, want %q", out.Body.Data.Name, "gpt-4")
	}
	if out.Body.Data.DisplayName != "GPT-4 Updated" {
		t.Errorf("response DisplayName = %q, want %q", out.Body.Data.DisplayName, "GPT-4 Updated")
	}
}

// --- deleteModel Tests (4) ---

// Test D1: GetModelByName returns nil → 404 "model not found"
func TestDeleteModelNotFound(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{model: nil}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	input := &DeleteModelInput{Name: "no-such-model"}

	_, err := s.deleteModel(context.Background(), input)
	assertStatusError(t, err, 404, "not_found")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "model not found" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "model not found")
	}
}

// Test D2: ModelReferenceCountsFor error → 500 internal "failed to count references"
func TestDeleteModelReferenceCountsError(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{
		model:        &types.Model{Name: "gpt-4", Status: "active", Publisher: "openai"},
		referenceErr: errors.New("database error"),
	}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	input := &DeleteModelInput{Name: "gpt-4"}

	_, err := s.deleteModel(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to count references" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to count references")
	}
}

// Test D3: counts.Total() > 0 → 409 conflict with counts as details
func TestDeleteModelReferencesExist(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{
		model: &types.Model{Name: "gpt-4", Status: "active", Publisher: "openai"},
		referenceCounts: store.ModelReferenceCounts{
			Upstreams: 1,
			Routes:    2,
			Plans:     0,
			Policies:  0,
			APIKeys:   0,
		},
	}
	s := newModelsServerWithCatalog(t, st, &fakeCatalog{})
	input := &DeleteModelInput{Name: "gpt-4"}

	_, err := s.deleteModel(context.Background(), input)
	assertStatusError(t, err, 409, "conflict")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "model is referenced; set status=disabled or clear references first" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "model is referenced; set status=disabled or clear references first")
	}

	// Check that details contains the exact ModelReferenceCounts struct
	if env.Payload.Details == nil {
		t.Fatal("expected details to be non-nil")
	}

	// Cast details to ModelReferenceCounts and verify
	details, ok := env.Payload.Details.(store.ModelReferenceCounts)
	if !ok {
		t.Fatalf("expected Details to be store.ModelReferenceCounts, got %T", env.Payload.Details)
	}
	if details.Upstreams != 1 || details.Routes != 2 {
		t.Errorf("details = %+v, want {Upstreams: 1, Routes: 2, ...}", details)
	}
}

// Test D4: Happy path (counts.Total() == 0) → 204; refresh called; DeleteModel called
func TestDeleteModelHappyPath(t *testing.T) {
	t.Parallel()
	st := &fakeModelsStore{
		model:  &types.Model{Name: "gpt-4", Status: "active", Publisher: "openai"},
		models: []types.Model{{Name: "gpt-4"}},
		referenceCounts: store.ModelReferenceCounts{
			Upstreams: 0,
			Routes:    0,
			Plans:     0,
			Policies:  0,
			APIKeys:   0,
		},
	}
	cat := &fakeCatalog{}
	s := newModelsServerWithCatalog(t, st, cat)
	input := &DeleteModelInput{Name: "gpt-4"}

	out, err := s.deleteModel(context.Background(), input)
	if err != nil {
		t.Fatalf("deleteModel() error = %v", err)
	}

	if out == nil {
		t.Fatal("expected non-nil output")
	}

	// Assert DeleteModel was called with correct name
	if st.lastDeletedName != "gpt-4" {
		t.Errorf("lastDeletedName = %q, want %q", st.lastDeletedName, "gpt-4")
	}

	// Assert refreshCatalog was called (Swap should have been called)
	if cat.swappedModels == nil {
		t.Fatal("Catalog.Swap was not called after deleteModel")
	}
}
