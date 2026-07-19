package adminv1

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelserver/modelserver/internal/api/contract"
	"github.com/modelserver/modelserver/internal/authz"
	"github.com/modelserver/modelserver/internal/billing"
	"github.com/modelserver/modelserver/internal/metrics"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

// CreateExtraUsageTopupInput is the typed input for POST /api/v1/projects/{projectID}/extra-usage/topup.
type CreateExtraUsageTopupInput struct {
	ProjectID string `path:"projectID" format:"uuid" doc:"Project identifier."`
	Body      struct {
		Channel     string `json:"channel" doc:"Payment channel: wechat, alipay, or stripe."`
		AmountFen   *int64 `json:"amount_fen,omitempty" doc:"Amount in CNY fen (required for wechat/alipay)."`
		AmountCents *int64 `json:"amount_cents,omitempty" doc:"Amount in USD cents (required for stripe)."`
	}
}

// CreateExtraUsageTopupResponseData is the typed body for a 201 topup response.
type CreateExtraUsageTopupResponseData struct {
	OrderID    string `json:"order_id"`
	Channel    string `json:"channel"`
	Currency   string `json:"currency"`
	Amount     int64  `json:"amount"`
	Credits    int64  `json:"credits"`
	PaymentURL string `json:"payment_url"`
	PaymentRef string `json:"payment_ref"`
}

// CreateExtraUsageTopupOutput wraps the 201 body in the standard data envelope.
type CreateExtraUsageTopupOutput struct {
	Body DataResponse[CreateExtraUsageTopupResponseData]
}

// GetExtraUsageTopupInput is the typed input for GET /api/v1/projects/{projectID}/extra-usage/topup/{orderID}.
type GetExtraUsageTopupInput struct {
	ProjectID string `path:"projectID" format:"uuid"`
	OrderID   string `path:"orderID" doc:"Topup order identifier."`
}

// GetExtraUsageTopupOutput wraps the order in the standard data envelope.
type GetExtraUsageTopupOutput struct {
	Body DataResponse[types.Order]
}

func registerExtraUsageOperations(api huma.API, server *Server) {
	read := authz.Project(authz.PermissionProjectExtraUsageRead, projectIDPathParam)
	write := authz.Project(authz.PermissionProjectExtraUsageWrite, projectIDPathParam)
	topup := authz.Project(authz.PermissionProjectExtraUsageTopup, projectIDPathParam, authz.RequireProjectMembership())

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "getExtraUsage",
		Method:        http.MethodGet,
		Path:          "/api/v1/projects/{projectID}/extra-usage",
		Summary:       "Get extra-usage settings",
		Tags:          []string{"Projects", "Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        read,
		Authorize:     server.authorizationMiddleware,
	}, server.getExtraUsage)

	contract.RegisterWithLegacyTrailingSlash(api, contract.Operation{
		ID:            "updateExtraUsage",
		Method:        http.MethodPut,
		Path:          "/api/v1/projects/{projectID}/extra-usage",
		Summary:       "Update extra-usage settings",
		Tags:          []string{"Projects", "Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access:        write,
		Authorize:     server.authorizationMiddleware,
	}, server.updateExtraUsage)

	contract.Register(api, contract.Operation{
		ID:            "listExtraUsageTransactions",
		Method:        http.MethodGet,
		Path:          "/api/v1/projects/{projectID}/extra-usage/transactions",
		Summary:       "List extra-usage ledger transactions",
		Tags:          []string{"Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        read,
		Authorize:     server.authorizationMiddleware,
	}, server.listExtraUsageTransactions)

	contract.Register(api, contract.Operation{
		ID:            "createExtraUsageTopup",
		Method:        http.MethodPost,
		Path:          "/api/v1/projects/{projectID}/extra-usage/topup",
		Summary:       "Create extra-usage topup order",
		Description:   "Initiates a payment order to top up the project's extra-usage credit balance.",
		Tags:          []string{"Extra Usage"},
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError, http.StatusServiceUnavailable},
		Access:        topup,
		Authorize:     server.authorizationMiddleware,
	}, server.createExtraUsageTopup)

	contract.Register(api, contract.Operation{
		ID:            "getExtraUsageTopup",
		Method:        http.MethodGet,
		Path:          "/api/v1/projects/{projectID}/extra-usage/topup/{orderID}",
		Summary:       "Get extra-usage topup order status",
		Tags:          []string{"Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError},
		Access: authz.Project(
			authz.PermissionProjectExtraUsageRead,
			projectIDPathParam,
			authz.WithResource("extra-usage-topup", "orderID"),
		),
		Authorize: server.authorizationMiddleware,
	}, server.getExtraUsageTopup)

	contract.Register(api, contract.Operation{
		ID:            "adminExtraUsageOverview",
		Method:        http.MethodGet,
		Path:          "/api/v1/admin/extra-usage/overview",
		Summary:       "Admin extra-usage overview (superadmin)",
		Tags:          []string{"Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authz.System(authz.PermissionSystemExtraUsageRead),
		Authorize:     server.authorizationMiddleware,
	}, server.adminExtraUsageOverview)

	contract.Register(api, contract.Operation{
		ID:            "adminExtraUsageDirectTopup",
		Method:        http.MethodPost,
		Path:          "/api/v1/admin/extra-usage/projects/{projectID}/topup",
		Summary:       "Admin direct top-up (superadmin, bypasses payment provider)",
		Tags:          []string{"Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authz.SystemOnProjectPath(authz.PermissionSystemExtraUsageManage, "projectID"),
		Authorize:     server.authorizationMiddleware,
	}, server.adminExtraUsageDirectTopup)

	contract.Register(api, contract.Operation{
		ID:            "adminExtraUsageSetBypass",
		Method:        http.MethodPut,
		Path:          "/api/v1/admin/extra-usage/projects/{projectID}/bypass",
		Summary:       "Toggle extra-usage balance-check bypass on a project (superadmin)",
		Tags:          []string{"Extra Usage"},
		DefaultStatus: http.StatusOK,
		Errors:        []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError},
		Access:        authz.SystemOnProjectPath(authz.PermissionSystemExtraUsageManage, "projectID"),
		Authorize:     server.authorizationMiddleware,
	}, server.adminExtraUsageSetBypass)
}

// CreditUnitPrices holds the per-million-credit price in each supported
// currency and the implicit exchange rate (for informational display only).
type CreditUnitPrices struct {
	CNYFenPerMillion   int64   `json:"cny_fen_per_million"`
	USDCentsPerMillion int64   `json:"usd_cents_per_million"`
	ImplicitUSDToCNY   float64 `json:"implicit_usd_to_cny_rate"`
}

// TopupAmounts holds the topup bound (min or max) in each supported currency.
type TopupAmounts struct {
	CNYFen   int64 `json:"cny_fen"`
	USDCents int64 `json:"usd_cents"`
}

// ExtraUsageGetResponse packs settings + derived counters for the dashboard.
type ExtraUsageGetResponse struct {
	Enabled             bool             `json:"enabled"`
	BalanceCredits      int64            `json:"balance_credits"`
	MonthlyLimitCredits int64            `json:"monthly_limit_credits"`
	MonthlySpentCredits int64            `json:"monthly_spent_credits"`
	MonthlyWindowStart  string           `json:"monthly_window_start"`
	BypassBalanceCheck  bool             `json:"bypass_balance_check"`
	UpdatedAt           time.Time        `json:"updated_at,omitempty"`
	CreditUnitPrices    CreditUnitPrices `json:"credit_unit_prices"`
	MinTopup            TopupAmounts     `json:"min_topup"`
	MaxTopup            TopupAmounts     `json:"max_topup"`
	DailyTopupLimit     int64            `json:"daily_topup_limit_credits"`
}

type GetExtraUsageInput struct {
	ProjectID string `path:"projectID" format:"uuid" doc:"Project identifier."`
}

type GetExtraUsageOutput struct {
	Body DataResponse[ExtraUsageGetResponse]
}

// AdminExtraUsageOverviewRow is a single row in the admin extra-usage overview.
// It embeds the ExtraUsageSettings and adds the 7-day spend data.
type AdminExtraUsageOverviewRow struct {
	types.ExtraUsageSettings
	Spend7DaysCredits int64 `json:"spend_7d_credits"`
}

// AdminExtraUsageOverviewOutput wraps the overview rows in the standard data envelope.
type AdminExtraUsageOverviewOutput struct {
	Body DataResponse[[]AdminExtraUsageOverviewRow]
}

// AdminDirectTopupInput is the typed input for POST /api/v1/admin/extra-usage/projects/{projectID}/topup.
type AdminDirectTopupInput struct {
	ProjectID string `path:"projectID" format:"uuid"`
	Body      struct {
		AmountCredits int64  `json:"amount_credits"`
		Description   string `json:"description,omitempty"`
	}
}

// AdminDirectTopupResponseData is the response for admin direct topup.
type AdminDirectTopupResponseData struct {
	ProjectID      string `json:"project_id"`
	BalanceCredits int64  `json:"balance_credits"`
}

// AdminDirectTopupOutput wraps the response in the standard data envelope.
type AdminDirectTopupOutput struct {
	Body DataResponse[AdminDirectTopupResponseData]
}

// AdminSetBypassInput is the typed input for PUT /api/v1/admin/extra-usage/projects/{projectID}/bypass.
type AdminSetBypassInput struct {
	ProjectID string `path:"projectID" format:"uuid"`
	Body      struct {
		Bypass *bool `json:"bypass,omitempty"`
	}
}

// AdminSetBypassOutput wraps the settings in the standard data envelope.
type AdminSetBypassOutput struct {
	Body DataResponse[types.ExtraUsageSettings]
}

type UpdateExtraUsageInput struct {
	ProjectID string `path:"projectID" format:"uuid" doc:"Project identifier."`
	Body struct {
		Enabled             *bool  `json:"enabled,omitempty" doc:"Enable or disable extra usage for this project."`
		MonthlyLimitCredits *int64 `json:"monthly_limit_credits,omitempty" doc:"Monthly credit limit. Must be >= 0."`
	}
}

type UpdateExtraUsageOutput struct {
	Body DataResponse[types.ExtraUsageSettings]
}

// getExtraUsage handles GET /api/v1/projects/{projectID}/extra-usage.
// Returns the project's extra-usage state + policy knobs the dashboard needs.
//
// Behavior (matches legacy handleGetExtraUsage):
//  1. GetExtraUsageSettings(projectID) → 500 "failed to load extra usage settings"
//  2. store.MonthWindowStart() to get the month window start
//  3. GetMonthlyExtraSpendCredits(projectID, monthStart) → 500 "failed to sum monthly spend"
//  4. Calculate implicit USD to CNY rate from pricing config
//  5. Build response with all pricing knobs from ExtraUsageCfg
//  6. If settings != nil, populate Enabled, BalanceCredits, MonthlyLimitCredits, BypassBalanceCheck, UpdatedAt
//  7. Populate MonthlySpentCredits from step 3
//  8. Return 200 with {data: response}
func (s *Server) getExtraUsage(ctx context.Context, input *GetExtraUsageInput) (*GetExtraUsageOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	settings, err := s.ExtraUsage.GetExtraUsageSettings(input.ProjectID)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to load extra usage settings", nil)
	}

	monthStart := store.MonthWindowStart()
	spent, err := s.ExtraUsage.GetMonthlyExtraSpendCredits(input.ProjectID, monthStart)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to sum monthly spend", nil)
	}

	implicitUSDToCNY := 0.0
	if s.ExtraUsageCfg.CreditPriceUSDCents > 0 {
		implicitUSDToCNY = float64(s.ExtraUsageCfg.CreditPriceCNYFen) / float64(s.ExtraUsageCfg.CreditPriceUSDCents)
	}

	resp := ExtraUsageGetResponse{
		MonthlyWindowStart: monthStart.Format(time.RFC3339),
		CreditUnitPrices: CreditUnitPrices{
			CNYFenPerMillion:   s.ExtraUsageCfg.CreditPriceCNYFen,
			USDCentsPerMillion: s.ExtraUsageCfg.CreditPriceUSDCents,
			ImplicitUSDToCNY:   implicitUSDToCNY,
		},
		MinTopup: TopupAmounts{
			CNYFen:   s.ExtraUsageCfg.MinTopupCNYFen,
			USDCents: s.ExtraUsageCfg.MinTopupUSDCents,
		},
		MaxTopup: TopupAmounts{
			CNYFen:   s.ExtraUsageCfg.MaxTopupCNYFen,
			USDCents: s.ExtraUsageCfg.MaxTopupUSDCents,
		},
		DailyTopupLimit: s.ExtraUsageCfg.DailyTopupLimitCredits,
	}

	if settings != nil {
		resp.Enabled = settings.Enabled
		resp.BalanceCredits = settings.BalanceCredits
		resp.MonthlyLimitCredits = settings.MonthlyLimitCredits
		resp.BypassBalanceCheck = settings.BypassBalanceCheck
		resp.UpdatedAt = settings.UpdatedAt
	}

	resp.MonthlySpentCredits = spent

	return &GetExtraUsageOutput{
		Body: DataResponse[ExtraUsageGetResponse]{
			Data: resp,
		},
	}, nil
}

// updateExtraUsage handles PUT /api/v1/projects/{projectID}/extra-usage.
// Partial update pattern: preserves unspecified fields.
//
// Behavior (matches legacy handleUpdateExtraUsage):
//  1. GetExtraUsageSettings(projectID) → 500 "failed to load settings"
//  2. Initialize enabled := false, monthlyLimit := 0
//  3. If existing != nil: enabled = existing.Enabled, monthlyLimit = existing.MonthlyLimitCredits
//  4. If body.Enabled != nil: override enabled
//  5. If body.MonthlyLimitCredits != nil:
//     - If *value < 0 → 400 bad_request "monthly_limit_credits must be >= 0"
//     - Override monthlyLimit
//  6. UpsertExtraUsageSettings(projectID, enabled, monthlyLimit) → 500 "failed to save settings"
//  7. Return 200 with {data: settings}
func (s *Server) updateExtraUsage(ctx context.Context, input *UpdateExtraUsageInput) (*UpdateExtraUsageOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	existing, err := s.ExtraUsage.GetExtraUsageSettings(input.ProjectID)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to load settings", nil)
	}

	enabled := false
	var monthlyLimit int64
	if existing != nil {
		enabled = existing.Enabled
		monthlyLimit = existing.MonthlyLimitCredits
	}

	if input.Body.Enabled != nil {
		enabled = *input.Body.Enabled
	}

	if input.Body.MonthlyLimitCredits != nil {
		if *input.Body.MonthlyLimitCredits < 0 {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request", "monthly_limit_credits must be >= 0", nil)
		}
		monthlyLimit = *input.Body.MonthlyLimitCredits
	}

	out, err := s.ExtraUsage.UpsertExtraUsageSettings(input.ProjectID, enabled, monthlyLimit)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to save settings", nil)
	}

	return &UpdateExtraUsageOutput{
		Body: DataResponse[types.ExtraUsageSettings]{
			Data: *out,
		},
	}, nil
}

type ListExtraUsageTransactionsInput struct {
	ProjectID string `path:"projectID" format:"uuid" doc:"Project identifier."`
	Page      int    `query:"page" default:"1" minimum:"1"`
	PerPage   int    `query:"per_page" default:"20" minimum:"1" maximum:"100"`
	Sort      string `query:"sort" default:"created_at"`
	Order     string `query:"order" default:"desc" enum:"asc,desc"`
	Type      string `query:"type,omitempty" doc:"Filter by transaction type (topup, deduction, refund, adjust)."`
}

func (input *ListExtraUsageTransactionsInput) pagination() types.PaginationParams {
	return types.PaginationParams{
		Page:    input.Page,
		PerPage: input.PerPage,
		Sort:    input.Sort,
		Order:   input.Order,
	}
}

type ListExtraUsageTransactionsOutput struct {
	Body ListResponse[types.ExtraUsageTransaction]
}

// createExtraUsageTopup handles POST /api/v1/projects/{projectID}/extra-usage/topup.
// Creates a payment order for extra-usage credit topup and returns the payment URL.
//
// Behavior (matches legacy handleCreateExtraUsageTopup):
//  1. Channel dispatch: wechat/alipay require amount_fen, stripe requires amount_cents.
//     Wrong combinations or unknown channels → 400 bad_request.
//  2. Validate amount bounds from ExtraUsageCfg → 400 bad_request.
//  3. Daily-cap check via SumDailyExtraUsageTopupCredits → 409 daily_topup_limit if exceeded.
//  4. CreateOrder (Pending, ExtraUsageTopup type, Metadata "{}") → 500 on store error.
//  5. If PayClient == nil → mark order Failed + 503 payment_not_configured.
//  6. PayClient.CreatePayment → on error, mark order Failed + 503 payment_provider_error.
//  7. UpdateOrderPayment(orderID, PaymentRef, PaymentURL, Paying) → 500 on store error.
//  8. metrics.IncExtraUsageTopupIntent(channel).
//  9. Return 201 with {order_id, channel, currency, amount, credits, payment_url, payment_ref}.
func (s *Server) createExtraUsageTopup(ctx context.Context, input *CreateExtraUsageTopupInput) (*CreateExtraUsageTopupOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	var (
		credits       int64
		currency      string
		paymentAmount int64
	)

	switch input.Body.Channel {
	case "wechat", "alipay":
		if input.Body.AmountFen == nil {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				"amount_fen is required for channel="+input.Body.Channel, nil)
		}
		if input.Body.AmountCents != nil {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				"amount_cents is not valid for channel="+input.Body.Channel, nil)
		}
		amt := *input.Body.AmountFen
		if amt < s.ExtraUsageCfg.MinTopupCNYFen {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				fmt.Sprintf("amount_fen must be >= %d", s.ExtraUsageCfg.MinTopupCNYFen), nil)
		}
		if amt > s.ExtraUsageCfg.MaxTopupCNYFen {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				fmt.Sprintf("amount_fen must be <= %d", s.ExtraUsageCfg.MaxTopupCNYFen), nil)
		}
		credits = (amt * 1_000_000) / s.ExtraUsageCfg.CreditPriceCNYFen
		currency = "CNY"
		paymentAmount = amt

	case "stripe":
		if input.Body.AmountCents == nil {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				"amount_cents is required for channel=stripe", nil)
		}
		if input.Body.AmountFen != nil {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				"amount_fen is not valid for channel=stripe", nil)
		}
		amt := *input.Body.AmountCents
		if amt < s.ExtraUsageCfg.MinTopupUSDCents {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				fmt.Sprintf("amount_cents must be >= %d", s.ExtraUsageCfg.MinTopupUSDCents), nil)
		}
		if amt > s.ExtraUsageCfg.MaxTopupUSDCents {
			return nil, contract.NewError(http.StatusBadRequest, "bad_request",
				fmt.Sprintf("amount_cents must be <= %d", s.ExtraUsageCfg.MaxTopupUSDCents), nil)
		}
		credits = (amt * 1_000_000) / s.ExtraUsageCfg.CreditPriceUSDCents
		currency = "USD"
		paymentAmount = amt

	default:
		return nil, contract.NewError(http.StatusBadRequest, "bad_request",
			"channel must be one of: wechat, alipay, stripe", nil)
	}

	// Daily cap check (currency-agnostic, always in credits).
	dayStart := store.DayWindowStart()
	todayCredits, err := s.ExtraUsage.SumDailyExtraUsageTopupCredits(input.ProjectID, dayStart)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to check daily topup cap", nil)
	}
	if s.ExtraUsageCfg.DailyTopupLimitCredits > 0 && todayCredits+credits > s.ExtraUsageCfg.DailyTopupLimitCredits {
		return nil, contract.NewError(http.StatusConflict, "daily_topup_limit",
			fmt.Sprintf("daily topup limit %d credits reached", s.ExtraUsageCfg.DailyTopupLimitCredits), nil)
	}

	order := &types.Order{
		ProjectID:               input.ProjectID,
		Periods:                 1,
		UnitPrice:               paymentAmount,
		Amount:                  paymentAmount,
		Currency:                currency,
		Status:                  types.OrderStatusPending,
		Channel:                 input.Body.Channel,
		Metadata:                "{}",
		OrderType:               types.OrderTypeExtraUsageTopup,
		ExtraUsageAmountCredits: credits,
	}
	if err := s.ExtraUsage.CreateOrder(order); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to create order: "+err.Error(), nil)
	}

	if s.PayClient == nil {
		_ = s.ExtraUsage.UpdateOrderStatus(order.ID, types.OrderStatusFailed)
		return nil, contract.NewError(http.StatusServiceUnavailable, "payment_not_configured",
			"payment provider is not configured", nil)
	}

	payResp, err := s.PayClient.CreatePayment(ctx, billing.PaymentRequest{
		OrderID:     order.ID,
		ProductName: fmt.Sprintf("extra-usage topup %d credits", credits),
		Channel:     input.Body.Channel,
		Currency:    currency,
		Amount:      paymentAmount,
		NotifyURL:   s.BillingCfg.NotifyURL,
		ReturnURL:   s.BillingCfg.ReturnURL,
	})
	if err != nil {
		slog.Default().Error("payment provider create failed",
			"order_id", order.ID, "channel", input.Body.Channel, "err", err)
		_ = s.ExtraUsage.UpdateOrderStatus(order.ID, types.OrderStatusFailed)
		return nil, contract.NewError(http.StatusServiceUnavailable, "payment_provider_error",
			"payment provider is unavailable", nil)
	}

	if err := s.ExtraUsage.UpdateOrderPayment(order.ID, payResp.PaymentRef, payResp.PaymentURL, types.OrderStatusPaying); err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to update order payment", nil)
	}

	metrics.IncExtraUsageTopupIntent(input.Body.Channel)

	return &CreateExtraUsageTopupOutput{
		Body: DataResponse[CreateExtraUsageTopupResponseData]{
			Data: CreateExtraUsageTopupResponseData{
				OrderID:    order.ID,
				Channel:    input.Body.Channel,
				Currency:   currency,
				Amount:     paymentAmount,
				Credits:    credits,
				PaymentURL: payResp.PaymentURL,
				PaymentRef: payResp.PaymentRef,
			},
		},
	}, nil
}

// getExtraUsageTopup handles GET /api/v1/projects/{projectID}/extra-usage/topup/{orderID}.
// The resolver already validated that the order exists, is a topup order, and
// belongs to the requested project. The handler re-fetches to return the full
// row. A race between resolver and handler (order deleted, transient store
// error) is reported as 404 for parity with legacy handleGetExtraUsageTopup,
// which treated any err/nil from GetOrderByID as 404 "order not found".
func (s *Server) getExtraUsageTopup(_ context.Context, input *GetExtraUsageTopupInput) (*GetExtraUsageTopupOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	order, err := s.ExtraUsage.GetOrderByID(input.OrderID)
	if err != nil || order == nil {
		return nil, contract.NewError(http.StatusNotFound, "not_found", "order not found", nil)
	}

	return &GetExtraUsageTopupOutput{
		Body: DataResponse[types.Order]{Data: *order},
	}, nil
}

// listExtraUsageTransactions handles GET /api/v1/projects/{projectID}/extra-usage/transactions.
// Returns a paginated ledger of extra-usage transactions with optional type filter.
//
// Behavior (matches legacy handleListExtraUsageTransactions):
//  1. ListExtraUsageTransactions(projectID, pagination, typeFilter) → 500 "failed to list transactions"
//  2. Return 200 with {data: transactions, meta: paginationMeta}
func (s *Server) listExtraUsageTransactions(ctx context.Context, input *ListExtraUsageTransactionsInput) (*ListExtraUsageTransactionsOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	pagination := input.pagination()
	txs, total, err := s.ExtraUsage.ListExtraUsageTransactions(input.ProjectID, pagination, input.Type)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list transactions", nil)
	}

	if txs == nil {
		txs = []types.ExtraUsageTransaction{}
	}

	return &ListExtraUsageTransactionsOutput{
		Body: ListResponse[types.ExtraUsageTransaction]{
			Data: txs,
			Meta: paginationMeta(total, pagination),
		},
	}, nil
}

// adminExtraUsageOverview handles GET /api/v1/admin/extra-usage/overview.
// Returns an overview of all projects' extra-usage settings with 7-day spend aggregation.
// This is a superadmin-only endpoint.
//
// Behavior (matches legacy handleAdminExtraUsageOverview):
//  1. ListExtraUsageSettings() → 500 "failed to list settings"
//  2. For each setting, SumRecentExtraUsageSpendCredits(projectID, 7) → 500 "failed to sum recent spend"
//  3. Return 200 with {data: [rows with embedded settings + spend]}
//  NOTE: This preserves the N+1 query pattern; optimization is not in scope for this batch.
func (s *Server) adminExtraUsageOverview(_ context.Context, _ *struct{}) (*AdminExtraUsageOverviewOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	rows, err := s.ExtraUsage.ListExtraUsageSettings()
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to list settings", nil)
	}

	out := make([]AdminExtraUsageOverviewRow, 0, len(rows))
	for _, row := range rows {
		spend, err := s.ExtraUsage.SumRecentExtraUsageSpendCredits(row.ProjectID, 7)
		if err != nil {
			return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to sum recent spend", nil)
		}
		out = append(out, AdminExtraUsageOverviewRow{
			ExtraUsageSettings: row,
			Spend7DaysCredits:  spend,
		})
	}

	return &AdminExtraUsageOverviewOutput{
		Body: DataResponse[[]AdminExtraUsageOverviewRow]{Data: out},
	}, nil
}

// adminExtraUsageDirectTopup handles POST /api/v1/admin/extra-usage/projects/{projectID}/topup.
// Superadmin direct credit injection without going through payment provider.
//
// Behavior:
//  1. If body.AmountCredits <= 0 → 400 bad_request "amount_credits must be > 0"
//  2. TopUpExtraUsage(TopUpExtraUsageReq{ProjectID, AmountCredits, Reason: ExtraUsageReasonAdminAdjust, Description}) → 500 internal "failed to top up: "+err.Error()
//  3. metrics.SetExtraUsageBalance(projectID, bal) — preserve side effect
//  4. Return 200 with {project_id, balance_credits}
func (s *Server) adminExtraUsageDirectTopup(_ context.Context, input *AdminDirectTopupInput) (*AdminDirectTopupOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	if input.Body.AmountCredits <= 0 {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "amount_credits must be > 0", nil)
	}

	bal, err := s.ExtraUsage.TopUpExtraUsage(store.TopUpExtraUsageReq{
		ProjectID:     input.ProjectID,
		AmountCredits: input.Body.AmountCredits,
		Reason:        types.ExtraUsageReasonAdminAdjust,
		Description:   input.Body.Description,
	})
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to top up: "+err.Error(), nil)
	}

	metrics.SetExtraUsageBalance(input.ProjectID, bal)

	return &AdminDirectTopupOutput{
		Body: DataResponse[AdminDirectTopupResponseData]{
			Data: AdminDirectTopupResponseData{
				ProjectID:      input.ProjectID,
				BalanceCredits: bal,
			},
		},
	}, nil
}

// adminExtraUsageSetBypass handles PUT /api/v1/admin/extra-usage/projects/{projectID}/bypass.
// Toggle the balance-check bypass flag on a project's extra-usage settings.
//
// Behavior:
//  1. If body.Bypass == nil → 400 bad_request "bypass field required"
//  2. SetExtraUsageBypass(projectID, *body.Bypass) → 500 internal "failed to set bypass"
//  3. Log actor via authorizationFromContext(ctx).Principal.UserID
//  4. Return 200 with {data: settings}
func (s *Server) adminExtraUsageSetBypass(ctx context.Context, input *AdminSetBypassInput) (*AdminSetBypassOutput, error) {
	if s == nil || s.ExtraUsage == nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "extra usage store is not configured", nil)
	}

	if input.Body.Bypass == nil {
		return nil, contract.NewError(http.StatusBadRequest, "bad_request", "bypass field required", nil)
	}

	settings, err := s.ExtraUsage.SetExtraUsageBypass(input.ProjectID, *input.Body.Bypass)
	if err != nil {
		return nil, contract.NewError(http.StatusInternalServerError, "internal", "failed to set bypass", nil)
	}

	actorID := ""
	if authorization, ok := authorizationFromContext(ctx); ok {
		actorID = authorization.Principal.UserID
	}
	slog.Default().Info("extra_usage_bypass_toggled",
		"project_id", input.ProjectID,
		"bypass", *input.Body.Bypass,
		"actor_user_id", actorID)

	return &AdminSetBypassOutput{
		Body: DataResponse[types.ExtraUsageSettings]{
			Data: *settings,
		},
	}, nil
}
