package adminv1

import (
	"context"
	"testing"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/modelcatalog"
	"github.com/modelserver/modelserver/internal/types"
)

// fakePlansStore records CreatePlan calls and satisfies plansStore.
type fakePlansStore struct {
	created   *types.Plan
	createErr error
	plan      *types.Plan
	getErr    error
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
func (s *fakePlansStore) UpdatePlan(string, map[string]any) error { return nil }
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
