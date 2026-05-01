package proxy

import (
	"net/http"
	"testing"

	"github.com/tidwall/gjson"
)

func TestBedrockOpenAITransformerTransformBody_InjectsStreamOptionsForStreaming(t *testing.T) {
	transformer := &BedrockOpenAITransformer{}
	input := []byte(`{"model":"openai.gpt-oss-20b-1:0","messages":[{"role":"user","content":"hi"}]}`)
	output, err := transformer.TransformBody(input, "openai.gpt-oss-20b-1:0", true, http.Header{})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !gjson.GetBytes(output, "stream_options.include_usage").Bool() {
		t.Errorf("stream_options.include_usage should be true, got %s", output)
	}
}

func TestBedrockOpenAITransformerTransformBody_NoOpForNonStreaming(t *testing.T) {
	transformer := &BedrockOpenAITransformer{}
	input := []byte(`{"model":"openai.gpt-oss-20b-1:0","messages":[{"role":"user","content":"hi"}]}`)
	output, err := transformer.TransformBody(input, "openai.gpt-oss-20b-1:0", false, http.Header{})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if string(output) != string(input) {
		t.Errorf("non-streaming TransformBody should be no-op:\nin:  %s\nout: %s", input, output)
	}
}

func TestBedrockOpenAITransformerTransformBody_PreservesExistingStreamOptions(t *testing.T) {
	transformer := &BedrockOpenAITransformer{}
	input := []byte(`{"model":"openai.gpt-oss-20b-1:0","stream_options":{"include_usage":true,"foo":"bar"},"messages":[{"role":"user","content":"hi"}]}`)
	output, err := transformer.TransformBody(input, "openai.gpt-oss-20b-1:0", true, http.Header{})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if gjson.GetBytes(output, "stream_options.foo").String() != "bar" {
		t.Errorf("existing stream_options.foo should be preserved, got %s", output)
	}
	if !gjson.GetBytes(output, "stream_options.include_usage").Bool() {
		t.Errorf("stream_options.include_usage should remain true")
	}
}
