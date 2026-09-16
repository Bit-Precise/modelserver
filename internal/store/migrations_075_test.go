package store

import (
	"context"
	"math"
	"testing"
)

func TestMigration075_GPTImage25Pricing(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	var displayName, publisher string
	var textInput, textCached, textOutput, imageInput, imageCached, imageOutput float64
	var capabilities []string
	err := st.pool.QueryRow(ctx, `
		SELECT display_name, publisher,
		  (default_image_credit_rate->>'text_input_rate')::float8,
		  (default_image_credit_rate->>'text_cached_input_rate')::float8,
		  (default_image_credit_rate->>'text_output_rate')::float8,
		  (default_image_credit_rate->>'image_input_rate')::float8,
		  (default_image_credit_rate->>'image_cached_input_rate')::float8,
		  (default_image_credit_rate->>'image_output_rate')::float8,
		  ARRAY(SELECT jsonb_array_elements_text(metadata->'capabilities'))
		FROM models WHERE name = 'gpt-image-2.5'`).
		Scan(&displayName, &publisher, &textInput, &textCached, &textOutput,
			&imageInput, &imageCached, &imageOutput, &capabilities)
	if err != nil {
		t.Fatalf("query gpt-image-2.5 catalog row: %v", err)
	}
	if displayName != "GPT Image 2.5" || publisher != "openai" {
		t.Fatalf("metadata = %q/%q, want GPT Image 2.5/openai", displayName, publisher)
	}
	want := []float64{0.667, 0.167, 0, 1.067, 0.267, 4}
	got := []float64{textInput, textCached, textOutput, imageInput, imageCached, imageOutput}
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-9 {
			t.Fatalf("rate %d = %v, want %v", i, got[i], want[i])
		}
	}
	if !containsString(capabilities, "image_generation") || !containsString(capabilities, "image_edit") {
		t.Fatalf("capabilities = %v, want image_generation and image_edit", capabilities)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
