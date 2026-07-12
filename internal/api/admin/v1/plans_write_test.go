package adminv1

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/types"
)

// fakePlansStore records CreatePlan and UpdatePlan calls and satisfies plansStore.
type fakePlansStore struct {
	created       *types.Plan
	createErr     error
	plan          *types.Plan
	getErr        error
	updateErr     error
	lastUpdatedID string
	lastUpdates   map[string]any
}

func (s *fakePlansStore) ListPlansPaginated(types.PaginationParams) ([]types.Plan, int, error) {
	return nil, 0, nil
}
func (s *fakePlansStore) GetPlanByID(string) (*types.Plan, error) {
	return s.plan, s.getErr
}
func (s *fakePlansStore) CreatePlan(p *types.Plan) error {
	if s.createErr != nil {
		return s.createErr
	}
	s.created = p
	return nil
}
func (s *fakePlansStore) UpdatePlan(id string, updates map[string]any) error {
	s.lastUpdatedID = id
	s.lastUpdates = updates
	return s.updateErr
}
func (s *fakePlansStore) DeletePlan(string) error                 { return nil }

// fakeCatalog implements modelcatalog.Catalog for plan write tests.
// By default NormalizeNames returns names verbatim; set normalizeUnknown
// to simulate an UnknownModelsError, or normalizeErr for a generic error.
type fakeCatalog struct {
	normalizeErr     error
	normalizeUnknown []string
	lastNames        []string
}

func (c *fakeCatalog) NormalizeNames(names []string) ([]string, error) {
	c.lastNames = append([]string(nil), names...)
	if len(c.normalizeUnknown) > 0 {
		return nil, &modelcatalog.UnknownModelsError{Names: c.normalizeUnknown}
	}
	if c.normalizeErr != nil {
		return nil, c.normalizeErr
	}
	out := make([]string, len(names))
	copy(out, names)
	return out, nil
}

func (c *fakeCatalog) Lookup(string) (*types.Model, bool) { return nil, false }
func (c *fakeCatalog) Get(string) (*types.Model, bool)    { return nil, false }
func (c *fakeCatalog) Snapshot() []types.Model            { return nil }
func (c *fakeCatalog) Swap([]types.Model)                 {}
func (c *fakeCatalog) Stats() modelcatalog.Stats          { return modelcatalog.Stats{} }

// newPlansServer returns a Server wired for createPlan tests.
func newPlansServer(_ *testing.T, store *fakePlansStore, catalog modelcatalog.Catalog) *Server {
	return &Server{
		Plans:   store,
		Catalog: catalog,
	}
}

// assertPlanError is like assertStatusError but also checks the message text.
func assertPlanError(t *testing.T, err error, wantStatus int, wantCode, wantMsg string) {
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

// --- Test 1: missing name ---

func TestCreatePlanMissingName(t *testing.T) {
	t.Parallel()
	s := newPlansServer(t, &fakePlansStore{}, &fakeCatalog{})
	input := &CreatePlanInput{}
	input.Body.Slug = "my-plan"
	// Name intentionally empty.

	_, err := s.createPlan(context.Background(), input)
	assertPlanError(t, err, 400, "bad_request", "name and slug are required")
}

// --- Test 2: missing slug ---

func TestCreatePlanMissingSlug(t *testing.T) {
	t.Parallel()
	s := newPlansServer(t, &fakePlansStore{}, &fakeCatalog{})
	input := &CreatePlanInput{}
	input.Body.Name = "My Plan"
	// Slug intentionally empty.

	_, err := s.createPlan(context.Background(), input)
	assertPlanError(t, err, 400, "bad_request", "name and slug are required")
}

// --- Test 3: empty body ---

func TestCreatePlanEmptyBody(t *testing.T) {
	t.Parallel()
	s := newPlansServer(t, &fakePlansStore{}, &fakeCatalog{})
	input := &CreatePlanInput{}
	// All fields empty — both name and slug are missing.

	_, err := s.createPlan(context.Background(), input)
	assertPlanError(t, err, 400, "bad_request", "name and slug are required")
}

// --- Test 4: PeriodMonths = 0 → defaults to 1 ---

func TestCreatePlanPeriodMonthsZeroDefaultsToOne(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	s := newPlansServer(t, store, &fakeCatalog{})
	input := &CreatePlanInput{}
	input.Body.Name = "My Plan"
	input.Body.Slug = "my-plan"
	input.Body.PeriodMonths = 0

	_, err := s.createPlan(context.Background(), input)
	if err != nil {
		t.Fatalf("createPlan() error = %v", err)
	}
	if store.created == nil {
		t.Fatal("expected plan to be stored")
	}
	if store.created.PeriodMonths != 1 {
		t.Errorf("PeriodMonths = %d, want 1", store.created.PeriodMonths)
	}
}

// --- Test 5: PeriodMonths = -1 → defaults to 1 ---

func TestCreatePlanPeriodMonthsNegativeDefaultsToOne(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	s := newPlansServer(t, store, &fakeCatalog{})
	input := &CreatePlanInput{}
	input.Body.Name = "My Plan"
	input.Body.Slug = "my-plan"
	input.Body.PeriodMonths = -1

	_, err := s.createPlan(context.Background(), input)
	if err != nil {
		t.Fatalf("createPlan() error = %v", err)
	}
	if store.created == nil {
		t.Fatal("expected plan to be stored")
	}
	if store.created.PeriodMonths != 1 {
		t.Errorf("PeriodMonths = %d, want 1", store.created.PeriodMonths)
	}
}

// --- Test 6: invalid credit rule (fixed + month window) → 400 bad_request ---

func TestCreatePlanInvalidCreditRule(t *testing.T) {
	t.Parallel()
	s := newPlansServer(t, &fakePlansStore{}, &fakeCatalog{})
	input := &CreatePlanInput{}
	input.Body.Name = "My Plan"
	input.Body.Slug = "my-plan"
	input.Body.PeriodMonths = 1
	// Fixed window with month-based window — invalid per validateCreditRules.
	input.Body.CreditRules = []types.CreditRule{
		{Window: "1M", WindowType: types.WindowTypeFixed, MaxCredits: 1000},
	}

	_, err := s.createPlan(context.Background(), input)
	wantMsg := `month-based window "1M" is not supported with window_type "fixed" — use duration-based intervals like "7d"`
	assertPlanError(t, err, 400, "bad_request", wantMsg)
}

// --- Test 7: unknown model in model_credit_rates → 400 unknown_model ---

func TestCreatePlanUnknownModelInRates(t *testing.T) {
	t.Parallel()
	catalog := &fakeCatalog{normalizeUnknown: []string{"gpt-99"}}
	s := newPlansServer(t, &fakePlansStore{}, catalog)
	input := &CreatePlanInput{}
	input.Body.Name = "My Plan"
	input.Body.Slug = "my-plan"
	input.Body.PeriodMonths = 1
	input.Body.ModelCreditRates = map[string]types.CreditRate{
		"gpt-99": {InputRate: 1.0},
	}

	_, err := s.createPlan(context.Background(), input)
	assertStatusError(t, err, 400, "unknown_model")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	details, ok := env.Payload.Details.(map[string]any)
	if !ok {
		t.Fatalf("details is %T, want map[string]any", env.Payload.Details)
	}
	unknown, ok := details["unknown"]
	if !ok {
		t.Fatal("details missing 'unknown' key")
	}
	names, ok := unknown.([]string)
	if !ok {
		t.Fatalf("details['unknown'] is %T, want []string", unknown)
	}
	if len(names) != 1 || names[0] != "gpt-99" {
		t.Errorf("unknown names = %v, want [gpt-99]", names)
	}
}

// --- Test 8: _default sentinel in model_credit_rates is preserved verbatim ---

func TestCreatePlanDefaultSentinelPreserved(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	catalog := &fakeCatalog{}
	s := newPlansServer(t, store, catalog)
	input := &CreatePlanInput{}
	input.Body.Name = "My Plan"
	input.Body.Slug = "my-plan"
	input.Body.PeriodMonths = 1
	input.Body.ModelCreditRates = map[string]types.CreditRate{
		"_default": {InputRate: 2.5, OutputRate: 5.0},
	}

	_, err := s.createPlan(context.Background(), input)
	if err != nil {
		t.Fatalf("createPlan() error = %v", err)
	}
	if store.created == nil {
		t.Fatal("expected plan to be stored")
	}
	rate, ok := store.created.ModelCreditRates["_default"]
	if !ok {
		t.Fatal("_default sentinel not present in stored plan")
	}
	if rate.InputRate != 2.5 || rate.OutputRate != 5.0 {
		t.Errorf("_default rate = %+v, want {InputRate:2.5 OutputRate:5.0}", rate)
	}

	// Verify that _default was excluded from NormalizeNames call.
	for _, n := range catalog.lastNames {
		if n == "_default" {
			t.Errorf("NormalizeNames was called with _default sentinel; sentinel must be skipped: %v", catalog.lastNames)
		}
	}
}

// --- Test 9: happy path all fields → 201 with IsActive: true ---

func TestCreatePlanHappyPath(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	s := newPlansServer(t, store, &fakeCatalog{})
	input := &CreatePlanInput{}
	input.Body.Name = "Pro Plan"
	input.Body.Slug = "pro"
	input.Body.DisplayName = "Pro"
	input.Body.Description = "The pro plan"
	input.Body.TierLevel = 2
	input.Body.GroupTag = "paid"
	input.Body.PriceCNYFen = 9900
	input.Body.PriceUSDCents = 1500
	input.Body.PeriodMonths = 1
	input.Body.CreditRules = []types.CreditRule{
		{Window: "7d", WindowType: types.WindowTypeFixed, MaxCredits: 100000},
	}
	input.Body.ModelCreditRates = map[string]types.CreditRate{
		"claude-3-opus": {InputRate: 1.0, OutputRate: 2.0},
	}
	input.Body.ClassicRules = []types.ClassicRule{
		{Metric: "rpm", Limit: 60},
	}

	out, err := s.createPlan(context.Background(), input)
	if err != nil {
		t.Fatalf("createPlan() error = %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}

	plan := out.Body.Data

	if !plan.IsActive {
		t.Error("IsActive should be true")
	}
	if plan.Name != "Pro Plan" {
		t.Errorf("Name = %q, want %q", plan.Name, "Pro Plan")
	}
	if plan.Slug != "pro" {
		t.Errorf("Slug = %q, want %q", plan.Slug, "pro")
	}
	if plan.DisplayName != "Pro" {
		t.Errorf("DisplayName = %q, want %q", plan.DisplayName, "Pro")
	}
	if plan.Description != "The pro plan" {
		t.Errorf("Description = %q", plan.Description)
	}
	if plan.TierLevel != 2 {
		t.Errorf("TierLevel = %d, want 2", plan.TierLevel)
	}
	if plan.GroupTag != "paid" {
		t.Errorf("GroupTag = %q, want %q", plan.GroupTag, "paid")
	}
	if plan.PriceCNYFen != 9900 {
		t.Errorf("PriceCNYFen = %d, want 9900", plan.PriceCNYFen)
	}
	if plan.PriceUSDCents != 1500 {
		t.Errorf("PriceUSDCents = %d, want 1500", plan.PriceUSDCents)
	}
	if plan.PeriodMonths != 1 {
		t.Errorf("PeriodMonths = %d, want 1", plan.PeriodMonths)
	}
	if len(plan.CreditRules) != 1 {
		t.Errorf("CreditRules len = %d, want 1", len(plan.CreditRules))
	}
	if len(plan.ClassicRules) != 1 {
		t.Errorf("ClassicRules len = %d, want 1", len(plan.ClassicRules))
	}
	if store.created == nil {
		t.Fatal("plan was not stored via CreatePlan")
	}
}

// ---- UpdatePlan tests (Task 4) ----

// newUpdatePlanInput returns an UpdatePlanInput with PlanID pre-set.
func newUpdatePlanInput(planID string) *UpdatePlanInput {
	in := &UpdatePlanInput{}
	in.PlanID = planID
	return in
}

// mustRawJSON marshals v to json.RawMessage for building test inputs.
func mustRawJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("mustRawJSON: %v", err)
	}
	return json.RawMessage(b)
}

// newUpdatePlansServer returns a Server wired with a store that will return
// planToReturn on GetPlanByID calls, used for success-path tests.
func newUpdatePlansServer(t *testing.T, store *fakePlansStore, catalog modelcatalog.Catalog, planToReturn *types.Plan) *Server {
	t.Helper()
	store.plan = planToReturn
	return &Server{Plans: store, Catalog: catalog}
}

// --- UpdatePlan Test 1: Empty body → 400 bad_request "no valid fields to update" ---

func TestUpdatePlanEmptyBody(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	s := newUpdatePlansServer(t, store, &fakeCatalog{}, nil)
	in := newUpdatePlanInput("plan-1")
	// All Body fields are zero/nil — nothing to update.

	_, err := s.updatePlan(context.Background(), in)
	assertPlanError(t, err, 400, "bad_request", "no valid fields to update")
}

// --- UpdatePlan Test 2: model_credit_rates with unknown model → 400 unknown_models ---

func TestUpdatePlanUnknownModel(t *testing.T) {
	t.Parallel()
	catalog := &fakeCatalog{normalizeUnknown: []string{"gpt-99"}}
	store := &fakePlansStore{}
	s := newUpdatePlansServer(t, store, catalog, nil)
	in := newUpdatePlanInput("plan-1")
	in.Body.ModelCreditRates = map[string]json.RawMessage{
		"gpt-99": mustRawJSON(t, map[string]any{"input_rate": 1.0}),
	}

	_, err := s.updatePlan(context.Background(), in)
	assertStatusError(t, err, 400, "unknown_model")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	details, ok := env.Payload.Details.(map[string]any)
	if !ok {
		t.Fatalf("details is %T, want map[string]any", env.Payload.Details)
	}
	unknown, ok := details["unknown"]
	if !ok {
		t.Fatal("details missing 'unknown' key")
	}
	names, ok := unknown.([]string)
	if !ok {
		t.Fatalf("details['unknown'] is %T, want []string", unknown)
	}
	if len(names) != 1 || names[0] != "gpt-99" {
		t.Errorf("unknown names = %v, want [gpt-99]", names)
	}
}

// --- UpdatePlan Test 3: _default sentinel in model_credit_rates preserved ---

func TestUpdatePlanDefaultSentinelPreserved(t *testing.T) {
	t.Parallel()
	catalog := &fakeCatalog{}
	store := &fakePlansStore{}
	returnedPlan := &types.Plan{ID: "plan-1", Name: "Test Plan"}
	s := newUpdatePlansServer(t, store, catalog, returnedPlan)
	in := newUpdatePlanInput("plan-1")
	in.Body.ModelCreditRates = map[string]json.RawMessage{
		"_default": mustRawJSON(t, map[string]any{"input_rate": 2.5}),
	}

	_, err := s.updatePlan(context.Background(), in)
	if err != nil {
		t.Fatalf("updatePlan() error = %v", err)
	}

	// _default must not have been passed to NormalizeNames.
	for _, n := range catalog.lastNames {
		if n == "_default" {
			t.Errorf("NormalizeNames was called with _default sentinel: %v", catalog.lastNames)
		}
	}

	// The store's updates map must contain model_credit_rates as []byte.
	val, ok := store.lastUpdates["model_credit_rates"]
	if !ok {
		t.Fatal("updates map missing 'model_credit_rates'")
	}
	if _, ok := val.([]byte); !ok {
		t.Errorf("model_credit_rates value is %T, want []byte", val)
	}
}

// --- UpdatePlan Test 4: client_model_credit_rates with invalid bucket → 400 bad_request ---

func TestUpdatePlanInvalidClientBucket(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	s := newUpdatePlansServer(t, store, &fakeCatalog{}, nil)
	in := newUpdatePlanInput("plan-1")
	in.Body.ClientModelCreditRates = map[string]json.RawMessage{
		"not-a-valid-bucket": mustRawJSON(t, map[string]any{"input_rate": 1.0}),
	}

	_, err := s.updatePlan(context.Background(), in)
	assertPlanError(t, err, 400, "bad_request", "invalid client bucket in client_model_credit_rates: not-a-valid-bucket")
}

// --- UpdatePlan Test 5: client_model_credit_rates with all valid buckets → success ---

func TestUpdatePlanValidClientBuckets(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	returnedPlan := &types.Plan{ID: "plan-1", Name: "Test Plan"}
	s := newUpdatePlansServer(t, store, &fakeCatalog{}, returnedPlan)
	in := newUpdatePlanInput("plan-1")
	in.Body.ClientModelCreditRates = map[string]json.RawMessage{
		types.ClientBucketClaudeCodeCLI: mustRawJSON(t, map[string]any{"input_rate": 1.0}),
		types.ClientBucketClaudeDesktop: mustRawJSON(t, map[string]any{"input_rate": 2.0}),
		types.ClientBucketOther:         mustRawJSON(t, map[string]any{"input_rate": 0.5}),
	}

	_, err := s.updatePlan(context.Background(), in)
	if err != nil {
		t.Fatalf("updatePlan() error = %v", err)
	}

	// Verify client_model_credit_rates is stored as []byte.
	val, ok := store.lastUpdates["client_model_credit_rates"]
	if !ok {
		t.Fatal("updates map missing 'client_model_credit_rates'")
	}
	if _, ok := val.([]byte); !ok {
		t.Errorf("client_model_credit_rates value is %T, want []byte", val)
	}
}

// --- UpdatePlan Test 6: Invalid credit_rules (fixed window with month) → 400 bad_request ---

func TestUpdatePlanInvalidCreditRules(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	s := newUpdatePlansServer(t, store, &fakeCatalog{}, nil)
	in := newUpdatePlanInput("plan-1")
	in.Body.CreditRules = mustRawJSON(t, []map[string]any{
		{"window": "1M", "window_type": "fixed", "max_credits": 1000},
	})

	_, err := s.updatePlan(context.Background(), in)
	wantMsg := `month-based window "1M" is not supported with window_type "fixed" — use duration-based intervals like "7d"`
	assertPlanError(t, err, 400, "bad_request", wantMsg)
}

// --- UpdatePlan Test 7: Happy path all fields → 200; complex fields stored as []byte ---

func TestUpdatePlanHappyPathAllFields(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	returnedPlan := &types.Plan{ID: "plan-1", Name: "Updated Plan", IsActive: true}
	s := newUpdatePlansServer(t, store, &fakeCatalog{}, returnedPlan)

	name := "Updated Plan"
	slug := "updated-plan"
	displayName := "Updated"
	description := "New description"
	tierLevel := 3
	groupTag := "premium"
	priceCNYFen := int64(19900)
	priceUSDCents := int64(2999)
	periodMonths := 12
	isActive := true

	in := newUpdatePlanInput("plan-1")
	in.Body.Name = &name
	in.Body.Slug = &slug
	in.Body.DisplayName = &displayName
	in.Body.Description = &description
	in.Body.TierLevel = &tierLevel
	in.Body.GroupTag = &groupTag
	in.Body.PriceCNYFen = &priceCNYFen
	in.Body.PriceUSDCents = &priceUSDCents
	in.Body.PeriodMonths = &periodMonths
	in.Body.IsActive = &isActive
	in.Body.CreditRules = mustRawJSON(t, []map[string]any{
		{"window": "7d", "window_type": "fixed", "max_credits": 100000},
	})
	in.Body.ClassicRules = mustRawJSON(t, []map[string]any{
		{"metric": "rpm", "limit": 60},
	})
	in.Body.ModelCreditRates = map[string]json.RawMessage{
		"claude-3-opus": mustRawJSON(t, map[string]any{"input_rate": 1.0, "output_rate": 2.0}),
	}
	in.Body.ClientModelCreditRates = map[string]json.RawMessage{
		types.ClientBucketOther: mustRawJSON(t, map[string]any{"input_rate": 0.5}),
	}

	out, err := s.updatePlan(context.Background(), in)
	if err != nil {
		t.Fatalf("updatePlan() error = %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}
	if out.Body.Data.Name != "Updated Plan" {
		t.Errorf("plan.Name = %q, want %q", out.Body.Data.Name, "Updated Plan")
	}

	// Assert scalar fields are in updates map with correct types.
	scalarFields := []string{"name", "slug", "display_name", "description", "tier_level",
		"group_tag", "price_cny_fen", "price_usd_cents", "period_months", "is_active"}
	for _, f := range scalarFields {
		if _, ok := store.lastUpdates[f]; !ok {
			t.Errorf("updates map missing scalar field %q", f)
		}
	}

	// Assert complex fields are stored as []byte.
	for _, f := range []string{"credit_rules", "classic_rules", "model_credit_rates", "client_model_credit_rates"} {
		val, ok := store.lastUpdates[f]
		if !ok {
			t.Errorf("updates map missing complex field %q", f)
			continue
		}
		if _, ok := val.([]byte); !ok {
			t.Errorf("updates[%q] is %T, want []byte", f, val)
		}
	}
}

// --- UpdatePlan Test 8: Only is_active: false → 200; only is_active in updates map ---

func TestUpdatePlanOnlyIsActiveFalse(t *testing.T) {
	t.Parallel()
	store := &fakePlansStore{}
	returnedPlan := &types.Plan{ID: "plan-1", Name: "My Plan", IsActive: false}
	s := newUpdatePlansServer(t, store, &fakeCatalog{}, returnedPlan)
	in := newUpdatePlanInput("plan-1")
	isActive := false
	in.Body.IsActive = &isActive

	out, err := s.updatePlan(context.Background(), in)
	if err != nil {
		t.Fatalf("updatePlan() error = %v", err)
	}
	if out == nil {
		t.Fatal("expected non-nil output")
	}

	// Only is_active should be in the updates map.
	if len(store.lastUpdates) != 1 {
		t.Errorf("updates map has %d keys, want 1; keys = %v", len(store.lastUpdates), store.lastUpdates)
	}
	if _, ok := store.lastUpdates["is_active"]; !ok {
		t.Error("updates map missing 'is_active'")
	}
}
