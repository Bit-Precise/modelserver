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

	transactions       []types.ExtraUsageTransaction
	transactionsTotal  int
	transactionsErr    error

	getSettingsCalls []string // projectID
	upsertCalls      []struct {
		projectID       string
		enabled         bool
		monthlyLimitStr string
	} // monthlyLimit is stringified to avoid pointer comparison issues
	listTransactionsCalls []struct {
		projectID  string
		pageNum    int
		perPage    int
		typeFilter string
	}
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
	s.listTransactionsCalls = append(s.listTransactionsCalls, struct {
		projectID  string
		pageNum    int
		perPage    int
		typeFilter string
	}{projectID, p.Page, p.Limit(), typeFilter})
	if s.transactionsErr != nil {
		return nil, 0, s.transactionsErr
	}
	return s.transactions, s.transactionsTotal, nil
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

// --- G6 (listExtraUsageTransactions) Tests ---

// Test 8: ListExtraUsageTransactions error → 500 internal
func TestListExtraUsageTransactionsStoreError(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		transactionsErr: errors.New("database error"),
	}
	s := newExtraUsageServer(store)
	input := &ListExtraUsageTransactionsInput{ProjectID: "proj-123"}

	_, err := s.listExtraUsageTransactions(context.Background(), input)
	assertStatusError(t, err, 500, "internal")

	env, ok := err.(*contract.ErrorEnvelope)
	if !ok {
		t.Fatalf("expected *contract.ErrorEnvelope, got %T", err)
	}
	if env.Payload.Message != "failed to list transactions" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "failed to list transactions")
	}
}

// Test 9: Type filter passthrough → captured in fake
func TestListExtraUsageTransactionsFilterPassthrough(t *testing.T) {
	t.Parallel()
	store := &fakeExtraUsageStore{
		transactions:      []types.ExtraUsageTransaction{},
		transactionsTotal: 0,
	}
	s := newExtraUsageServer(store)
	input := &ListExtraUsageTransactionsInput{
		ProjectID: "proj-123",
		Page:      1,
		PerPage:   20,
		Type:      "topup",
	}

	output, err := s.listExtraUsageTransactions(context.Background(), input)
	if err != nil {
		t.Fatalf("listExtraUsageTransactions() error = %v", err)
	}

	// Verify the response
	if len(output.Body.Data) != 0 {
		t.Errorf("data length = %d, want 0", len(output.Body.Data))
	}

	// Verify that filter was passed through
	if len(store.listTransactionsCalls) != 1 {
		t.Errorf("listTransactions called %d times, want 1", len(store.listTransactionsCalls))
	}
	call := store.listTransactionsCalls[0]
	if call.projectID != "proj-123" {
		t.Errorf("projectID = %q, want proj-123", call.projectID)
	}
	if call.typeFilter != "topup" {
		t.Errorf("typeFilter = %q, want topup", call.typeFilter)
	}
}

// Test 10: Happy path with paginated results
func TestListExtraUsageTransactionsHappyPath(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	transactions := []types.ExtraUsageTransaction{
		{
			ID:                  "tx-001",
			ProjectID:           "proj-123",
			Type:                "topup",
			AmountCredits:       5000,
			BalanceAfterCredits: 5000,
			OrderID:             "order-001",
			Reason:              "user_topup",
			CreatedAt:           createdAt,
		},
		{
			ID:                  "tx-002",
			ProjectID:           "proj-123",
			Type:                "deduction",
			AmountCredits:       -1000,
			BalanceAfterCredits: 4000,
			RequestID:           "req-001",
			Reason:              "rate_limited",
			CreatedAt:           createdAt.Add(1 * time.Hour),
		},
	}
	store := &fakeExtraUsageStore{
		transactions:      transactions,
		transactionsTotal: 2,
	}
	s := newExtraUsageServer(store)
	input := &ListExtraUsageTransactionsInput{
		ProjectID: "proj-123",
		Page:      1,
		PerPage:   20,
	}

	output, err := s.listExtraUsageTransactions(context.Background(), input)
	if err != nil {
		t.Fatalf("listExtraUsageTransactions() error = %v", err)
	}

	// Verify response data
	if len(output.Body.Data) != 2 {
		t.Errorf("data length = %d, want 2", len(output.Body.Data))
	}
	if output.Body.Data[0].ID != "tx-001" {
		t.Errorf("first transaction ID = %q, want tx-001", output.Body.Data[0].ID)
	}
	if output.Body.Data[1].ID != "tx-002" {
		t.Errorf("second transaction ID = %q, want tx-002", output.Body.Data[1].ID)
	}

	// Verify pagination meta
	if output.Body.Meta.Total != 2 {
		t.Errorf("meta.total = %d, want 2", output.Body.Meta.Total)
	}
	if output.Body.Meta.Page != 1 {
		t.Errorf("meta.page = %d, want 1", output.Body.Meta.Page)
	}
	if output.Body.Meta.PerPage != 20 {
		t.Errorf("meta.per_page = %d, want 20", output.Body.Meta.PerPage)
	}
	if output.Body.Meta.TotalPages != 1 {
		t.Errorf("meta.total_pages = %d, want 1", output.Body.Meta.TotalPages)
	}
}

// Helper functions
func pointerBool(v bool) *bool {
	return &v
}

func pointerInt64(v int64) *int64 {
	return &v
}
