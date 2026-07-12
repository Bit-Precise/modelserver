package adminv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/ratelimit"
)

func TestSubscriptionsOverviewEnvelopeShape(t *testing.T) {
	t.Parallel()

	// Create a sample project subscription overview
	overview := ProjectSubscriptionOverview{
		ProjectID:   "proj-123",
		PlanID:      "plan-456",
		PlanName:    "pro",
		DisplayName: "Professional",
		Windows: []ratelimit.CreditWindowStatus{
			{
				Window:     "monthly",
				Percentage: 45.5,
				ResetsAt:   "2026-08-11T00:00:00Z",
			},
		},
		Owner: &ProjectOwnerSnapshot{
			ID:       "user-789",
			Email:    "owner@example.com",
			Nickname: "Owner Name",
			Picture:  "https://example.com/pic.jpg",
		},
		PeriodCreditsK: ptrInt64(100),
	}

	// Create the output response
	output := &AdminProjectsSubscriptionsOverviewOutput{}
	output.Body.Data = []ProjectSubscriptionOverview{overview}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal AdminProjectsSubscriptionsOverviewOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data array envelope exists
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("JSON missing 'data' array envelope: %s", got)
	}

	// Verify expected subscription overview fields
	expectedFields := []string{
		`"project_id":"proj-123"`,
		`"plan_id":"plan-456"`,
		`"plan_name":"pro"`,
		`"display_name":"Professional"`,
		`"windows":[`,
		`"window":"monthly"`,
		`"percentage":45.5`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}
}

func TestListAllProjectsEnvelopeShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)

	project := Project{
		ID:        "proj-123",
		Name:      "Test Project",
		Description: "A test project",
		CreatedBy: "user-456",
		Status:    "active",
		CreatedAt: now,
		UpdatedAt: now,
	}

	output := &ListAllProjectsOutput{Body: ListResponse[Project]{
		Data: []Project{project},
		Meta: Meta{
			Total:      1,
			Page:       1,
			PerPage:    20,
			TotalPages: 1,
		},
	}}

	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("marshal ListAllProjectsOutput: %v", err)
	}

	got := string(encoded)

	// Verify the outer data array envelope exists
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("JSON missing 'data' array envelope: %s", got)
	}

	// Verify expected project fields
	expectedFields := []string{
		`"id":"proj-123"`,
		`"name":"Test Project"`,
		`"description":"A test project"`,
		`"created_by":"user-456"`,
		`"status":"active"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}

	// Verify meta fields exist
	if !strings.Contains(got, `"meta":{`) {
		t.Errorf("JSON missing 'meta' envelope: %s", got)
	}
	if !strings.Contains(got, `"total":1`) {
		t.Errorf("JSON missing 'total' field: %s", got)
	}
}

func ptrInt64(v int64) *int64 {
	return &v
}
