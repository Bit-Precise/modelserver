package adminv1

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/billing"
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

// --- G7 (createExtraUsageTopup) Fake helpers ---

// fakePayClient is a test double for billing.PaymentClient.
type fakePayClient struct {
	createPaymentCalled bool
	createPaymentReq    billing.PaymentRequest
	createPaymentResp   billing.PaymentResponse
	createPaymentErr    error
}

func (c *fakePayClient) CreatePayment(_ context.Context, req billing.PaymentRequest) (*billing.PaymentResponse, error) {
	c.createPaymentCalled = true
	c.createPaymentReq = req
	if c.createPaymentErr != nil {
		return nil, c.createPaymentErr
	}
	return &c.createPaymentResp, nil
}

// extendedFakeExtraUsageStore embeds fakeExtraUsageStore and adds topup-specific
// tracking fields for G7 tests.
type extendedFakeExtraUsageStore struct {
	fakeExtraUsageStore

	sumDailyTopupCredits int64
	sumDailyTopupErr     error

	createOrderCalledWith *types.Order
	createOrderErr        error

	updateOrderStatusCalls []struct {
		orderID string
		status  string
	}

	updateOrderPaymentCalls []struct {
		orderID    string
		paymentRef string
		paymentURL string
		status     string
	}
	updateOrderPaymentErr error
}

// Override SumDailyExtraUsageTopupCredits for topup tests.
func (s *extendedFakeExtraUsageStore) SumDailyExtraUsageTopupCredits(projectID string, dayStart time.Time) (int64, error) {
	return s.sumDailyTopupCredits, s.sumDailyTopupErr
}

// Override CreateOrder for topup tests.
func (s *extendedFakeExtraUsageStore) CreateOrder(order *types.Order) error {
	// Assign a deterministic test ID so tests can assert on it.
	order.ID = "test-order-id"
	s.createOrderCalledWith = order
	return s.createOrderErr
}

// Override UpdateOrderStatus for topup tests.
func (s *extendedFakeExtraUsageStore) UpdateOrderStatus(orderID, status string) error {
	s.updateOrderStatusCalls = append(s.updateOrderStatusCalls, struct {
		orderID string
		status  string
	}{orderID, status})
	return nil
}

// Override UpdateOrderPayment for topup tests.
func (s *extendedFakeExtraUsageStore) UpdateOrderPayment(orderID, paymentRef, paymentURL, status string) error {
	s.updateOrderPaymentCalls = append(s.updateOrderPaymentCalls, struct {
		orderID    string
		paymentRef string
		paymentURL string
		status     string
	}{orderID, paymentRef, paymentURL, status})
	return s.updateOrderPaymentErr
}

func newTopupServer(st *extendedFakeExtraUsageStore, payClient billing.PaymentClient) *Server {
	return &Server{
		ExtraUsage:    st,
		ExtraUsageCfg: testExtraUsageConfig(),
		PayClient:     payClient,
		BillingCfg: config.BillingConfig{
			NotifyURL: "https://example.com/notify",
			ReturnURL: "https://example.com/return",
		},
	}
}

// --- G7 Tests ---

// Test 1: Unknown channel → 400 "channel must be one of: wechat, alipay, stripe"
func TestCreateTopupUnknownChannel(t *testing.T) {
	t.Parallel()
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, nil)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "paypal"

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
	env := err.(*contract.ErrorEnvelope)
	if env.Payload.Message != "channel must be one of: wechat, alipay, stripe" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "channel must be one of: wechat, alipay, stripe")
	}
}

// Test 2: wechat without amount_fen → 400 "amount_fen is required for channel=wechat"
func TestCreateTopupWechatMissingAmountFen(t *testing.T) {
	t.Parallel()
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, nil)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	// AmountFen is nil

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
	env := err.(*contract.ErrorEnvelope)
	if env.Payload.Message != "amount_fen is required for channel=wechat" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "amount_fen is required for channel=wechat")
	}
}

// Test 3: wechat with amount_cents → 400 "amount_cents is not valid for channel=wechat"
func TestCreateTopupWechatInvalidAmountCents(t *testing.T) {
	t.Parallel()
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, nil)
	amt := int64(5000)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	input.Body.AmountFen = &amt
	input.Body.AmountCents = &amt

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
	env := err.(*contract.ErrorEnvelope)
	if env.Payload.Message != "amount_cents is not valid for channel=wechat" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "amount_cents is not valid for channel=wechat")
	}
}

// Test 4: wechat below min → 400 "amount_fen must be >= <min>"
func TestCreateTopupWechatBelowMin(t *testing.T) {
	t.Parallel()
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, nil)
	// MinTopupCNYFen = 1000, use 500
	amt := int64(500)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	input.Body.AmountFen = &amt

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
	env := err.(*contract.ErrorEnvelope)
	want := fmt.Sprintf("amount_fen must be >= %d", testExtraUsageConfig().MinTopupCNYFen)
	if env.Payload.Message != want {
		t.Errorf("message = %q, want %q", env.Payload.Message, want)
	}
}

// Test 5: stripe below min USD → 400 "amount_cents must be >= <min>"
func TestCreateTopupStripeBelowMin(t *testing.T) {
	t.Parallel()
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, nil)
	// MinTopupUSDCents = 167, use 100
	amt := int64(100)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "stripe"
	input.Body.AmountCents = &amt

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 400, "bad_request")
	env := err.(*contract.ErrorEnvelope)
	want := fmt.Sprintf("amount_cents must be >= %d", testExtraUsageConfig().MinTopupUSDCents)
	if env.Payload.Message != want {
		t.Errorf("message = %q, want %q", env.Payload.Message, want)
	}
}

// Test 6: Daily cap exceeded → 409 daily_topup_limit
func TestCreateTopupDailyCapExceeded(t *testing.T) {
	t.Parallel()
	cfg := testExtraUsageConfig()
	// today = DailyTopupLimitCredits-1, credits = large enough to exceed
	st := &extendedFakeExtraUsageStore{
		sumDailyTopupCredits: cfg.DailyTopupLimitCredits, // already at limit
	}
	s := newTopupServer(st, nil)
	amt := int64(cfg.MinTopupCNYFen) // small amount, but already at cap
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	input.Body.AmountFen = &amt

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 409, "daily_topup_limit")
	env := err.(*contract.ErrorEnvelope)
	want := fmt.Sprintf("daily topup limit %d credits reached", cfg.DailyTopupLimitCredits)
	if env.Payload.Message != want {
		t.Errorf("message = %q, want %q", env.Payload.Message, want)
	}
}

// Test 7: PayClient nil → 503 payment_not_configured; assert order was marked Failed
func TestCreateTopupPayClientNil(t *testing.T) {
	t.Parallel()
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, nil) // nil payClient
	amt := int64(testExtraUsageConfig().MinTopupCNYFen)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	input.Body.AmountFen = &amt

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 503, "payment_not_configured")
	env := err.(*contract.ErrorEnvelope)
	if env.Payload.Message != "payment provider is not configured" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "payment provider is not configured")
	}

	// Assert order was created and then marked Failed
	if st.createOrderCalledWith == nil {
		t.Fatal("expected CreateOrder to be called")
	}
	if len(st.updateOrderStatusCalls) != 1 {
		t.Fatalf("UpdateOrderStatus called %d times, want 1", len(st.updateOrderStatusCalls))
	}
	if st.updateOrderStatusCalls[0].status != types.OrderStatusFailed {
		t.Errorf("status = %q, want %q", st.updateOrderStatusCalls[0].status, types.OrderStatusFailed)
	}
}

// Test 8: PayClient CreatePayment error → 503 payment_provider_error; assert order was marked Failed
func TestCreateTopupPayClientError(t *testing.T) {
	t.Parallel()
	payClient := &fakePayClient{
		createPaymentErr: errors.New("provider down"),
	}
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, payClient)
	amt := int64(testExtraUsageConfig().MinTopupCNYFen)
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	input.Body.AmountFen = &amt

	_, err := s.createExtraUsageTopup(context.Background(), input)
	assertStatusError(t, err, 503, "payment_provider_error")
	env := err.(*contract.ErrorEnvelope)
	if env.Payload.Message != "payment provider is unavailable" {
		t.Errorf("message = %q, want %q", env.Payload.Message, "payment provider is unavailable")
	}

	// Assert order was marked Failed
	if len(st.updateOrderStatusCalls) != 1 {
		t.Fatalf("UpdateOrderStatus called %d times, want 1", len(st.updateOrderStatusCalls))
	}
	if st.updateOrderStatusCalls[0].status != types.OrderStatusFailed {
		t.Errorf("status = %q, want %q", st.updateOrderStatusCalls[0].status, types.OrderStatusFailed)
	}
}

// Test 9: Happy path (wechat) → 201 with all 7 body fields populated
func TestCreateTopupHappyPathWechat(t *testing.T) {
	t.Parallel()
	payClient := &fakePayClient{
		createPaymentResp: billing.PaymentResponse{
			PaymentRef: "ref-wechat-001",
			PaymentURL: "https://pay.wechat.example/order/001",
			Status:     "pending",
		},
	}
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, payClient)

	cfg := testExtraUsageConfig()
	amt := int64(cfg.MinTopupCNYFen) // 1000 fen
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "wechat"
	input.Body.AmountFen = &amt

	output, err := s.createExtraUsageTopup(context.Background(), input)
	if err != nil {
		t.Fatalf("createExtraUsageTopup() error = %v", err)
	}

	data := output.Body.Data
	if data.OrderID != "test-order-id" {
		t.Errorf("order_id = %q, want test-order-id", data.OrderID)
	}
	if data.Channel != "wechat" {
		t.Errorf("channel = %q, want wechat", data.Channel)
	}
	if data.Currency != "CNY" {
		t.Errorf("currency = %q, want CNY", data.Currency)
	}
	if data.Amount != amt {
		t.Errorf("amount = %d, want %d", data.Amount, amt)
	}
	// credits = (1000 * 1_000_000) / 5438
	expectedCredits := (amt * 1_000_000) / cfg.CreditPriceCNYFen
	if data.Credits != expectedCredits {
		t.Errorf("credits = %d, want %d", data.Credits, expectedCredits)
	}
	if data.PaymentURL != "https://pay.wechat.example/order/001" {
		t.Errorf("payment_url = %q, want %q", data.PaymentURL, "https://pay.wechat.example/order/001")
	}
	if data.PaymentRef != "ref-wechat-001" {
		t.Errorf("payment_ref = %q, want %q", data.PaymentRef, "ref-wechat-001")
	}

	// Assert UpdateOrderPayment was called with Paying status
	if len(st.updateOrderPaymentCalls) != 1 {
		t.Fatalf("UpdateOrderPayment called %d times, want 1", len(st.updateOrderPaymentCalls))
	}
	call := st.updateOrderPaymentCalls[0]
	if call.status != types.OrderStatusPaying {
		t.Errorf("order payment status = %q, want %q", call.status, types.OrderStatusPaying)
	}
}

// Test 10: Happy path (stripe with USD) → 201 with correct currency + credits calculation
func TestCreateTopupHappyPathStripe(t *testing.T) {
	t.Parallel()
	payClient := &fakePayClient{
		createPaymentResp: billing.PaymentResponse{
			PaymentRef: "ref-stripe-002",
			PaymentURL: "https://checkout.stripe.com/pay/002",
			Status:     "pending",
		},
	}
	st := &extendedFakeExtraUsageStore{}
	s := newTopupServer(st, payClient)

	cfg := testExtraUsageConfig()
	amt := int64(cfg.MinTopupUSDCents) // 167 cents
	input := &CreateExtraUsageTopupInput{ProjectID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}
	input.Body.Channel = "stripe"
	input.Body.AmountCents = &amt

	output, err := s.createExtraUsageTopup(context.Background(), input)
	if err != nil {
		t.Fatalf("createExtraUsageTopup() error = %v", err)
	}

	data := output.Body.Data
	if data.Channel != "stripe" {
		t.Errorf("channel = %q, want stripe", data.Channel)
	}
	if data.Currency != "USD" {
		t.Errorf("currency = %q, want USD", data.Currency)
	}
	if data.Amount != amt {
		t.Errorf("amount = %d, want %d", data.Amount, amt)
	}
	// credits = (167 * 1_000_000) / 907
	expectedCredits := (amt * 1_000_000) / cfg.CreditPriceUSDCents
	if data.Credits != expectedCredits {
		t.Errorf("credits = %d, want %d", data.Credits, expectedCredits)
	}
	if data.PaymentURL != "https://checkout.stripe.com/pay/002" {
		t.Errorf("payment_url = %q, want %q", data.PaymentURL, "https://checkout.stripe.com/pay/002")
	}
	if data.PaymentRef != "ref-stripe-002" {
		t.Errorf("payment_ref = %q, want %q", data.PaymentRef, "ref-stripe-002")
	}

	// Assert CreatePayment was called with correct currency
	if payClient.createPaymentReq.Currency != "USD" {
		t.Errorf("PaymentRequest.Currency = %q, want USD", payClient.createPaymentReq.Currency)
	}
}
