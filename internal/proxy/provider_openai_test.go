package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelserver/modelserver/internal/types"
	"github.com/tidwall/gjson"
)

// TestGetProviderTransformer_OpenAIChatCompletions verifies that the
// (provider, kind) dispatcher returns the chat-completions transformer for
// openai + openai_chat_completions. Without this routing, OpenAITransformer
// (Responses-API only) handles chat-completions streams and never finalizes
// the request row (see issue #57).
func TestGetProviderTransformer_OpenAIChatCompletions(t *testing.T) {
	got := GetProviderTransformer(types.ProviderOpenAI, types.KindOpenAIChatCompletions)
	if _, ok := got.(*OpenAIChatCompletionsTransformer); !ok {
		t.Fatalf("GetProviderTransformer(openai, chat_completions) = %T, want *OpenAIChatCompletionsTransformer", got)
	}
}

// TestGetProviderTransformer_OpenAIResponses verifies that the Responses-API
// path keeps using the existing OpenAITransformer.
func TestGetProviderTransformer_OpenAIResponses(t *testing.T) {
	for _, kind := range []string{types.KindOpenAIResponses, types.KindOpenAIResponsesCompact} {
		got := GetProviderTransformer(types.ProviderOpenAI, kind)
		if _, ok := got.(*OpenAITransformer); !ok {
			t.Errorf("GetProviderTransformer(openai, %s) = %T, want *OpenAITransformer", kind, got)
		}
	}
}

// TestOpenAIChatCompletionsTransformer_WrapStream_FinalizesOnChunkSSE is the
// core regression test for issue #57. Feeds a real-shape chat.completion.chunk
// stream through WrapStream and asserts that onComplete fires with the usage
// metrics from the terminal chunk — proving the row would finalize instead of
// being stuck at status='processing'.
func TestOpenAIChatCompletionsTransformer_WrapStream_FinalizesOnChunkSSE(t *testing.T) {
	transformer := &OpenAIChatCompletionsTransformer{}
	sseData := strings.Join([]string{
		`data: {"id":"chatcmpl-deepseek-1","object":"chat.completion.chunk","model":"deepseek-chat","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-deepseek-1","object":"chat.completion.chunk","model":"deepseek-chat","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl-deepseek-1","object":"chat.completion.chunk","model":"deepseek-chat","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":7,"total_tokens":19}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")

	var gotMetrics StreamMetrics
	done := make(chan struct{})

	wrapped := transformer.WrapStream(
		io.NopCloser(strings.NewReader(sseData)),
		time.Now(),
		func(metrics StreamMetrics) {
			gotMetrics = metrics
			close(done)
		},
	)

	output, err := io.ReadAll(wrapped)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if string(output) != sseData {
		t.Errorf("output differs from input (pass-through must be byte-exact)")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("onComplete not called — request row would be stuck on 'processing'")
	}

	if gotMetrics.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want %q", gotMetrics.Model, "deepseek-chat")
	}
	if gotMetrics.MsgID != "chatcmpl-deepseek-1" {
		t.Errorf("MsgID = %q, want %q", gotMetrics.MsgID, "chatcmpl-deepseek-1")
	}
	if gotMetrics.InputTokens != 12 {
		t.Errorf("InputTokens = %d, want 12", gotMetrics.InputTokens)
	}
	if gotMetrics.OutputTokens != 7 {
		t.Errorf("OutputTokens = %d, want 7", gotMetrics.OutputTokens)
	}
}

// TestOpenAIChatCompletionsTransformer_TransformBody_InjectsStreamOptions
// ensures the transformer forces stream_options.include_usage=true for
// streaming requests so OpenAI-compatible upstreams emit the terminal usage
// chunk needed for token accounting. Mirrors BedrockOpenAITransformer's
// behavior; without it, token counts would be zero even after the row is
// finalized.
func TestOpenAIChatCompletionsTransformer_TransformBody_InjectsStreamOptions(t *testing.T) {
	transformer := &OpenAIChatCompletionsTransformer{}
	input := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	output, err := transformer.TransformBody(input, "deepseek-chat", true, http.Header{})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !gjson.GetBytes(output, "stream_options.include_usage").Bool() {
		t.Errorf("stream_options.include_usage should be true, got %s", output)
	}
}

func TestOpenAIChatCompletionsTransformer_TransformBody_NoOpForNonStreaming(t *testing.T) {
	transformer := &OpenAIChatCompletionsTransformer{}
	input := []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hi"}]}`)
	output, err := transformer.TransformBody(input, "deepseek-chat", false, http.Header{})
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if string(output) != string(input) {
		t.Errorf("non-streaming TransformBody should be no-op:\nin:  %s\nout: %s", input, output)
	}
}

func TestOpenAIChatCompletionsTransformer_TransformBody_PreservesExistingStreamOptions(t *testing.T) {
	transformer := &OpenAIChatCompletionsTransformer{}
	input := []byte(`{"model":"deepseek-chat","stream_options":{"include_usage":true,"foo":"bar"},"messages":[{"role":"user","content":"hi"}]}`)
	output, err := transformer.TransformBody(input, "deepseek-chat", true, http.Header{})
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

// TestOpenAIChatCompletionsTransformer_ParseResponse exercises the non-streaming
// chat completions parser path.
func TestOpenAIChatCompletionsTransformer_ParseResponse(t *testing.T) {
	transformer := &OpenAIChatCompletionsTransformer{}
	body := []byte(`{"id":"chatcmpl-x","model":"deepseek-chat","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)
	got, err := transformer.ParseResponse(body)
	if err != nil {
		t.Fatalf("ParseResponse error: %v", err)
	}
	if got.Model != "deepseek-chat" {
		t.Errorf("Model = %q, want %q", got.Model, "deepseek-chat")
	}
	if got.MsgID != "chatcmpl-x" {
		t.Errorf("MsgID = %q, want %q", got.MsgID, "chatcmpl-x")
	}
	if got.InputTokens != 4 || got.OutputTokens != 2 {
		t.Errorf("tokens = (%d, %d), want (4, 2)", got.InputTokens, got.OutputTokens)
	}
}
