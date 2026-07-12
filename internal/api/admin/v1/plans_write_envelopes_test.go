package adminv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
)

func TestPlanWriteEnvelopeShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	plan := types.Plan{
		ID:               "plan-123",
		Name:             "Pro Plan",
		Slug:             "pro",
		DisplayName:      "Professional",
		Description:      "Professional plan for teams",
		TierLevel:        2,
		GroupTag:         "standard",
		PriceCNYFen:      5000,
		PriceUSDCents:    800,
		PeriodMonths:     1,
		CreditRules:      []types.CreditRule{},
		ModelCreditRates: map[string]types.CreditRate{},
		IsActive:         true,
		CreatedAt:        now,
		UpdatedAt:        now,
	}

	resp := DataResponse[types.Plan]{Data: plan}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal DataResponse[types.Plan]: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify expected plan fields within the data envelope
	expectedFields := []string{
		`"id":"plan-123"`,
		`"name":"Pro Plan"`,
		`"slug":"pro"`,
		`"display_name":"Professional"`,
		`"description":"Professional plan for teams"`,
		`"tier_level":2`,
		`"group_tag":"standard"`,
		`"price_cny_fen":5000`,
		`"price_usd_cents":800`,
		`"period_months":1`,
		`"is_active":true`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}
}

func TestUserWriteEnvelopeShapeAllFields(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 10, 30, 0, 0, time.UTC)
	user := User{
		ID:           "user-456",
		Email:        "john@example.com",
		Nickname:     "John Doe",
		Picture:      "https://example.com/photo.jpg",
		IsSuperadmin: false,
		MaxProjects:  50,
		Status:       "active",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	resp := DataResponse[User]{Data: user}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal DataResponse[User]: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify expected user fields within the data envelope
	expectedFields := []string{
		`"id":"user-456"`,
		`"email":"john@example.com"`,
		`"nickname":"John Doe"`,
		`"picture":"https://example.com/photo.jpg"`,
		`"is_superadmin":false`,
		`"max_projects":50`,
		`"status":"active"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}

	// Verify created_at and updated_at are present (timestamps format check)
	if !strings.Contains(got, `"created_at":"2026-07-11T10:30:00Z"`) {
		t.Errorf("JSON missing or malformed created_at; got: %s", got)
	}
	if !strings.Contains(got, `"updated_at":"2026-07-11T10:30:00Z"`) {
		t.Errorf("JSON missing or malformed updated_at; got: %s", got)
	}
}
