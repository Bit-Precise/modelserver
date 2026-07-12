package adminv1

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
)

func TestModelListRowEnvelopeShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	model := types.Model{
		Name:        "gpt-4",
		DisplayName: "GPT-4",
		Description: "A large language model",
		Aliases:     []string{"gpt4"},
		Status:      types.ModelStatusActive,
		Publisher:   "OpenAI",
		Metadata:    types.ModelMetadata{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	row := ModelListRow{
		Model: model,
		ReferenceCounts: store.ModelReferenceCounts{
			Plans: 1,
			Routes: 2,
		},
	}

	resp := DataResponse[[]ModelListRow]{Data: []ModelListRow{row}}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal DataResponse[[]ModelListRow]: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":[`) {
		t.Errorf("JSON missing 'data' array envelope: %s", got)
	}

	// Verify reference_counts is present at the row level
	if !strings.Contains(got, `"reference_counts":{`) {
		t.Errorf("JSON missing 'reference_counts' object: %s", got)
	}

	// Verify expected model fields within each row
	expectedFields := []string{
		`"name":"gpt-4"`,
		`"display_name":"GPT-4"`,
		`"description":"A large language model"`,
		`"status":"active"`,
		`"publisher":"OpenAI"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}
}

func TestModelEnvelopeShape(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 11, 0, 0, 0, 0, time.UTC)
	model := types.Model{
		Name:        "gpt-3.5-turbo",
		DisplayName: "GPT-3.5 Turbo",
		Description: "A fast language model",
		Aliases:     []string{"gpt35"},
		Status:      types.ModelStatusActive,
		Publisher:   "OpenAI",
		Metadata:    types.ModelMetadata{},
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	resp := DataResponse[types.Model]{Data: model}
	encoded, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal DataResponse[types.Model]: %v", err)
	}

	got := string(encoded)

	// Verify the outer data envelope exists
	if !strings.Contains(got, `"data":{`) {
		t.Errorf("JSON missing 'data' envelope: %s", got)
	}

	// Verify expected model fields within the data envelope
	expectedFields := []string{
		`"name":"gpt-3.5-turbo"`,
		`"display_name":"GPT-3.5 Turbo"`,
		`"description":"A fast language model"`,
		`"status":"active"`,
		`"publisher":"OpenAI"`,
	}

	for _, field := range expectedFields {
		if !strings.Contains(got, field) {
			t.Errorf("JSON missing field %s; got: %s", field, got)
		}
	}
}
