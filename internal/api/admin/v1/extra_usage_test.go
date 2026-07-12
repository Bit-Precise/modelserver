package adminv1

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/config"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// fakeExtraUsageStore records method calls and satisfies extraUsageStore.
type fakeExtraUsageStore struct {
	settings       *types.ExtraUsageSettings
	getSettingsErr error

	monthlySpend    int64
	monthlySpendErr error

	upsertSettings *types.ExtraUsageSettings
	upsertErr      error

	getSettingsCalls []string // projectID
	upsertCalls      []struct {
		projectID       string
		enabled         bool
		monthlyLimitStr string
	} // monthlyLimit is stringified to avoid pointer comparison issues
}

func (s *fakeExtraUsageStore) GetExtraUsageSettings(projectID string) (*types.ExtraUsageSettings, error) {
	s.getSettingsCalls = append(s.getSettingsCalls, projectID)
	if s.getSettingsErr != nil {
		return nil, s.getSettingsErr
	}
	return s.settings, nil
}

func (s *fakeExtraUsageStore) GetMonthlyExtraSpendCredits(projectID string, monthStart time.Time) (int64, error) {
	if s.monthlySpendErr != nil {
		return 0, s.monthlySpendErr
	}
	return s.monthlySpend, nil
}

func (s *fakeExtraUsageStore) UpsertExtraUsageSettings(projectID string, enabled bool, monthlyLimit int64) (*types.ExtraUsageSettings, error) {
	s.upsertCalls = append(s.upsertCalls, struct {
		projectID       string
		enabled         bool
		monthlyLimitStr string
	}{projectID, enabled, formatInt64(monthlyLimit)})
	if s.upsertErr != nil {
		return nil, s.upsertErr
	}
	return s.upsertSettings, nil
}

func (s *fakeExtraUsageStore) ListExtraUsageTransactions(projectID string, p types.PaginationParams, typeFilter string) ([]types.ExtraUsageTransaction, int, error) {
	return nil, 0, nil
}

func (s *fakeExtraUsageStore) SumDailyExtraUsageTopupCredits(projectID string, dayStart time.Time) (int64, error) {
	return 0, nil
}

func (s *fakeExtraUsageStore) CreateOrder(*types.Order) error {
	return nil
}

func (s *fakeExtraUsageStore) UpdateOrderStatus(orderID, status string) error {
	return nil
}

func (s *fakeExtraUsageStore) UpdateOrderPayment(orderID, paymentRef, paymentURL, status string) error {
	return nil
}

func (s *fakeExtraUsageStore) GetOrderByID(id string) (*types.Order, error) {
	return nil, nil
}

func (s *fakeExtraUsageStore) TopUpExtraUsage(req store.TopUpExtraUsageReq) (int64, error) {
	return 0, nil
}

func (s *fakeExtraUsageStore) ListExtraUsageSettings() ([]types.ExtraUsageSettings, error) {
	return nil, nil
}

func (s *fakeExtraUsageStore) SumRecentExtraUsageSpendCredits(projectID string, days int) (int64, error) {
	return 0, nil
}

func (s *fakeExtraUsageStore) SetExtraUsageBypass(projectID string, bypass bool) (*types.ExtraUsageSettings, error) {
	return nil, nil
}

func formatInt64(v int64) string {
	return strconv.FormatInt(v, 10)
}

// Helper function for creating a test configuration
func testExtraUsageConfig() config.ExtraUsageConfig {
	return config.ExtraUsageConfig{
		CreditPriceCNYFen:      5438,
		CreditPriceUSDCents:    907,
		MinTopupCNYFen:         1000,
		MaxTopupCNYFen:         200000,
		MinTopupUSDCents:       167,
		MaxTopupUSDCents:       33333,
		DailyTopupLimitCredits: 91945000,
	}
}

func newExtraUsageServer(store *fakeExtraUsageStore) *Server {
	return &Server{
		ExtraUsage:    store,
		ExtraUsageCfg: testExtraUsageConfig(),
	}
}

// --- G4 (getExtraUsage) Tests ---

// Test 1: GetExtraUsageSettings error → 500 internal
func TestGetExtraUsageGetSettingsError(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		getSettingsErr: errors.New("database error"),
	}
	s := newExtraUsageServer(store)
	input := &GetExtraUsageInput{ProjectID: "proj-123"}

	_, err := s.getExtraUsage(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to load extra usage settings" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to load extra usage settings")
	}
}

// Test 2: GetMonthlyExtraSpendCredits error → 500 internal
func TestGetExtraUsageMonthlySpendError(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		settings:        &types.ExtraUsageSettings{Enabled: true},
		monthlySpendErr: errors.New("query error"),
	}
	s := newExtraUsageServer(store)
	input := &GetExtraUsageInput{ProjectID: "proj-123"}

	_, err := s.getExtraUsage(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to sum monthly spend" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to sum monthly spend")
	}
}

// Test 3: Happy path with non-nil settings
func TestGetExtraUsageWithSettings(t *testing.T) {
	t.Parallel()
	updatedAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	store := &fakeExtraUsageStore{
		settings: &types.ExtraUsageSettings{
			ProjectID:               "proj-123",
			Enabled:                 true,
			BalanceCredits:          5000,
			MonthlyLimitCredits:     10000,
			BypassBalanceCheck:      false,
			UpdatedAt:               updatedAt,
		},
		monthlySpend: 2500,
	}
	s := newExtraUsageServer(store)
	input := &GetExtraUsageInput{ProjectID: "proj-123"}

	output, err := s.getExtraUsage(context.Background(), input)
	if err != nil {
		t.Fatalf("getExtraUsage() error = %v", err)
	}

	resp := output.Body.Data
	if !resp.Enabled {
		t.Errorf("Enabled = %v, want true", resp.Enabled)
	}
	if resp.BalanceCredits != 5000 {
		t.Errorf("BalanceCredits = %d, want 5000", resp.BalanceCredits)
	}
	if resp.MonthlyLimitCredits != 10000 {
		t.Errorf("MonthlyLimitCredits = %d, want 10000", resp.MonthlyLimitCredits)
	}
	if resp.MonthlySpentCredits != 2500 {
		t.Errorf("MonthlySpentCredits = %d, want 2500", resp.MonthlySpentCredits)
	}
	if !resp.UpdatedAt.Equal(updatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", resp.UpdatedAt, updatedAt)
	}

	// Check pricing knobs are populated
	if resp.CreditUnitPrices.CNYFenPerMillion != 5438 {
		t.Errorf("CreditUnitPrices.CNYFenPerMillion = %d, want 5438", resp.CreditUnitPrices.CNYFenPerMillion)
	}
	if resp.CreditUnitPrices.USDCentsPerMillion != 907 {
		t.Errorf("CreditUnitPrices.USDCentsPerMillion = %d, want 907", resp.CreditUnitPrices.USDCentsPerMillion)
	}
	implicitRate := float64(5438) / float64(907)
	if resp.CreditUnitPrices.ImplicitUSDToCNY != implicitRate {
		t.Errorf("CreditUnitPrices.ImplicitUSDToCNY = %v, want %v", resp.CreditUnitPrices.ImplicitUSDToCNY, implicitRate)
	}

	// Check topup bounds
	if resp.MinTopup.CNYFen != 1000 {
		t.Errorf("MinTopup.CNYFen = %d, want 1000", resp.MinTopup.CNYFen)
	}
	if resp.MinTopup.USDCents != 167 {
		t.Errorf("MinTopup.USDCents = %d, want 167", resp.MinTopup.USDCents)
	}
	if resp.MaxTopup.CNYFen != 200000 {
		t.Errorf("MaxTopup.CNYFen = %d, want 200000", resp.MaxTopup.CNYFen)
	}
	if resp.MaxTopup.USDCents != 33333 {
		t.Errorf("MaxTopup.USDCents = %d, want 33333", resp.MaxTopup.USDCents)
	}
	if resp.DailyTopupLimit != 91945000 {
		t.Errorf("DailyTopupLimit = %d, want 91945000", resp.DailyTopupLimit)
	}
}

// Test 4: Happy path with nil settings (defaults)
func TestGetExtraUsageNilSettings(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		settings:     nil,
		monthlySpend: 0,
	}
	s := newExtraUsageServer(store)
	input := &GetExtraUsageInput{ProjectID: "proj-123"}

	output, err := s.getExtraUsage(context.Background(), input)
	if err != nil {
		t.Fatalf("getExtraUsage() error = %v", err)
	}

	resp := output.Body.Data
	if resp.Enabled {
		t.Errorf("Enabled = %v, want false", resp.Enabled)
	}
	if resp.BalanceCredits != 0 {
		t.Errorf("BalanceCredits = %d, want 0", resp.BalanceCredits)
	}
	if resp.MonthlySpentCredits != 0 {
		t.Errorf("MonthlySpentCredits = %d, want 0", resp.MonthlySpentCredits)
	}

	// Pricing knobs should still be populated
	if resp.CreditUnitPrices.CNYFenPerMillion != 5438 {
		t.Errorf("CreditUnitPrices.CNYFenPerMillion = %d, want 5438", resp.CreditUnitPrices.CNYFenPerMillion)
	}
	if resp.DailyTopupLimit != 91945000 {
		t.Errorf("DailyTopupLimit = %d, want 91945000", resp.DailyTopupLimit)
	}
}

// --- G5 (updateExtraUsage) Tests ---

// Test 5: Negative monthly_limit_credits → 400 bad_request
func TestUpdateExtraUsageNegativeLimit(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		settings: nil,
	}
	s := newExtraUsageServer(store)
	input := &UpdateExtraUsageInput{
		ProjectID: "proj-123",
	}
	input.Body.MonthlyLimitCredits = pointerInt64(-100)

	_, err := s.updateExtraUsage(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "monthly_limit_credits must be >= 0" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "monthly_limit_credits must be >= 0")
	}
}

// Test 6: Store upsert error → 500 internal
func TestUpdateExtraUsageUpsertError(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		settings:  nil,
		upsertErr: errors.New("database error"),
	}
	s := newExtraUsageServer(store)
	input := &UpdateExtraUsageInput{
		ProjectID: "proj-123",
	}
	input.Body.Enabled = pointerBool(true)

	_, err := s.updateExtraUsage(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to save settings" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to save settings")
	}
}

// Test 7: Happy path preserving existing enabled
func TestUpdateExtraUsagePreserveExisting(t *testing.T) {
	t.Parallel()
	existingSettings := &types.ExtraUsageSettings{
		ProjectID:               "proj-123",
		Enabled:                 true,
		BalanceCredits:          5000,
		MonthlyLimitCredits:     5000,
		BypassBalanceCheck:      false,
	}
	updatedSettings := &types.ExtraUsageSettings{
		ProjectID:               "proj-123",
		Enabled:                 true,
		BalanceCredits:          5000,
		MonthlyLimitCredits:     20000,
		BypassBalanceCheck:      false,
	}
	store := &fakeExtraUsageStore{
		settings:       existingSettings,
		upsertSettings: updatedSettings,
	}
	s := newExtraUsageServer(store)
	input := &UpdateExtraUsageInput{
		ProjectID: "proj-123",
	}
	input.Body.MonthlyLimitCredits = pointerInt64(20000)

	output, err := s.updateExtraUsage(context.Background(), input)
	if err != nil {
		t.Fatalf("updateExtraUsage() error = %v", err)
	}

	// Verify the response
	if output.Body.Data.MonthlyLimitCredits != 20000 {
		t.Errorf("MonthlyLimitCredits = %d, want 20000", output.Body.Data.MonthlyLimitCredits)
	}

	// Verify that upsert was called with preserved enabled flag
	if len(store.upsertCalls) != 1 {
		t.Errorf("upsert called %d times, want 1", len(store.upsertCalls))
	}
	call := store.upsertCalls[0]
	if call.projectID != "proj-123" {
		t.Errorf("projectID = %q, want proj-123", call.projectID)
	}
	if !call.enabled {
		t.Errorf("enabled = %v, want true (preserved from existing)", call.enabled)
	}
	if call.monthlyLimitStr != "20000" {
		t.Errorf("monthlyLimit = %q, want 20000", call.monthlyLimitStr)
	}
}

// Helper functions
func pointerBool(v bool) *bool {
	return &v
}

func pointerInt64(v int64) *int64 {
	return &v
}
