package adminv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

func TestGetExtraUsageEnvelope(t *testing.T) {
	t.Parallel()
	output := &GetExtraUsageOutput{
		Body: DataResponse[ExtraUsageGetResponse]{
			Data: ExtraUsageGetResponse{
				Enabled:             true,
				BalanceCredits:      1000,
				MonthlyLimitCredits: 5000,
				BypassBalanceCheck:  false,
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal GetExtraUsageOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"balance_credits":1000`) {
		t.Errorf("JSON missing 'balance_credits' field: %s", got)
	}
	if !strings.Contains(got, `"enabled":true`) {
		t.Errorf("JSON missing 'enabled' field: %s", got)
	}
}

func TestUpdateExtraUsageEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	output := &UpdateExtraUsageOutput{
		Body: DataResponse[types.ExtraUsageSettings]{
			Data: types.ExtraUsageSettings{
				ProjectID:           "proj-123",
				Enabled:             true,
				BalanceCredits:      5000,
				MonthlyLimitCredits: 10000,
				BypassBalanceCheck:  true,
				CreatedAt:           now,
				UpdatedAt:           now,
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal UpdateExtraUsageOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"balance_credits":5000`) {
		t.Errorf("JSON missing 'balance_credits' field: %s", got)
	}
	if !strings.Contains(got, `"bypass_balance_check":true`) {
		t.Errorf("JSON missing 'bypass_balance_check' field: %s", got)
	}
}

func TestListExtraUsageTransactionsEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	output := &ListExtraUsageTransactionsOutput{
		Body: ListResponse[types.ExtraUsageTransaction]{
			Data: []types.ExtraUsageTransaction{
				{
					ID:                  "tx-001",
					ProjectID:           "proj-123",
					Type:                "topup",
					AmountCredits:       1000,
					BalanceAfterCredits: 5000,
					CreatedAt:           now,
				},
			},
			Meta: Meta{
				Total:      1,
				Page:       1,
				PerPage:    20,
				TotalPages: 1,
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal ListExtraUsageTransactionsOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("JSON missing 'data' array envelope: %s", got)
	}

	// Verify meta envelope and fields
	if !strings.Contains(got, `"meta":{`) {
		t.Errorf("JSON missing 'meta' envelope: %s", got)
	}
	if !strings.Contains(got, `"total":1`) {
		t.Errorf("JSON missing 'total' field: %s", got)
	}
	if !strings.Contains(got, `"page":1`) {
		t.Errorf("JSON missing 'page' field: %s", got)
	}
}

func TestCreateExtraUsageTopupEnvelope(t *testing.T) {
	t.Parallel()
	output := &CreateExtraUsageTopupOutput{
		Body: DataResponse[CreateExtraUsageTopupResponseData]{
			Data: CreateExtraUsageTopupResponseData{
				OrderID:    "ord-123",
				Channel:    "stripe",
				Currency:   "USD",
				Amount:     1000,
				Credits:    100000000,
				PaymentURL: "https://payment.example.com",
				PaymentRef: "ref-123",
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal CreateExtraUsageTopupOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"order_id":"ord-123"`) {
		t.Errorf("JSON missing 'order_id' field: %s", got)
	}
	if !strings.Contains(got, `"credits":100000000`) {
		t.Errorf("JSON missing 'credits' field: %s", got)
	}
}

func TestGetExtraUsageTopupEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	output := &GetExtraUsageTopupOutput{
		Body: DataResponse[types.Order]{
			Data: types.Order{
				ID:                     "ord-123",
				ProjectID:              "proj-123",
				Periods:                1,
				UnitPrice:              1000,
				Amount:                 1000,
				Currency:               "USD",
				Status:                 "paying",
				Channel:                "stripe",
				PaymentRef:             "ref-123",
				PaymentURL:             "https://payment.example.com",
				OrderType:              "extra_usage_topup",
				ExtraUsageAmountCredits: 100000000,
				CreatedAt:              now,
				UpdatedAt:              now,
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal GetExtraUsageTopupOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"id":"ord-123"`) {
		t.Errorf("JSON missing 'id' field: %s", got)
	}
	if !strings.Contains(got, `"amount":1000`) {
		t.Errorf("JSON missing 'amount' field: %s", got)
	}
}

func TestAdminExtraUsageOverviewEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	output := &AdminExtraUsageOverviewOutput{
		Body: DataResponse[[]AdminExtraUsageOverviewRow]{
			Data: []AdminExtraUsageOverviewRow{
				{
					ExtraUsageSettings: types.ExtraUsageSettings{
						ProjectID:           "proj-123",
						Enabled:             true,
						BalanceCredits:      5000,
						MonthlyLimitCredits: 10000,
						BypassBalanceCheck:  false,
						CreatedAt:           now,
						UpdatedAt:           now,
					},
					Spend7DaysCredits: 500,
				},
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal AdminExtraUsageOverviewOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("JSON missing 'data' array envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"spend_7d_credits":500`) {
		t.Errorf("JSON missing 'spend_7d_credits' field: %s", got)
	}
	if !strings.Contains(got, `"balance_credits":5000`) {
		t.Errorf("JSON missing 'balance_credits' field: %s", got)
	}
}

func TestAdminDirectTopupEnvelope(t *testing.T) {
	t.Parallel()
	output := &AdminDirectTopupOutput{
		Body: DataResponse[AdminDirectTopupResponseData]{
			Data: AdminDirectTopupResponseData{
				ProjectID:      "proj-123",
				BalanceCredits: 7500,
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal AdminDirectTopupOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"project_id":"proj-123"`) {
		t.Errorf("JSON missing 'project_id' field: %s", got)
	}
	if !strings.Contains(got, `"balance_credits":7500`) {
		t.Errorf("JSON missing 'balance_credits' field: %s", got)
	}
}

func TestAdminSetBypassEnvelope(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 19, 12, 0, 0, 0, time.UTC)
	output := &AdminSetBypassOutput{
		Body: DataResponse[types.ExtraUsageSettings]{
			Data: types.ExtraUsageSettings{
				ProjectID:           "proj-123",
				Enabled:             true,
				BalanceCredits:      5000,
				MonthlyLimitCredits: 10000,
				BypassBalanceCheck:  true,
				CreatedAt:           now,
				UpdatedAt:           now,
			},
		},
	}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal AdminSetBypassOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify representative fields
	if !strings.Contains(got, `"bypass_balance_check":true`) {
		t.Errorf("JSON missing 'bypass_balance_check' field: %s", got)
	}
	if !strings.Contains(got, `"project_id":"proj-123"`) {
		t.Errorf("JSON missing 'project_id' field: %s", got)
	}
}
