package proxy

import (
	"io"
	"net/http"
	"time"

	"github.com/modelserver/modelserver/internal/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAIChatCompletionsTransformer handles the openai provider's
// /v1/chat/completions endpoint. The OpenAI provider can serve two distinct
// wire formats — Responses API (handled by OpenAITransformer) and Chat
// Completions — and they need different SSE interceptors. Folding both into
// one transformer (as we used to) stranded chat-completions stream rows on
// status='processing' because the Responses-API interceptor doesn't match
// chat.completion.chunk events, leaving onComplete unfired (see issue #57).
//
// The body/parser/stream-interceptor wiring mirrors BedrockOpenAITransformer
// and VertexOpenAITransformer; only the auth path differs (Bearer with the
// OpenAI API key against the configured base URL).
type OpenAIChatCompletionsTransformer struct{}

var _ ProviderTransformer = (*OpenAIChatCompletionsTransformer)(nil)

// TransformBody injects stream_options.include_usage=true for streaming
// requests so the upstream emits a terminal usage chunk for token accounting.
// Without this, OpenAI-compatible Chat Completions upstreams skip the usage
// event and token counts would be zero even after the row is finalized.
func (t *OpenAIChatCompletionsTransformer) TransformBody(body []byte, _ string, isStream bool, _ http.Header) ([]byte, error) {
	if isStream && !gjson.GetBytes(body, "stream_options.include_usage").Bool() {
		body, _ = sjson.SetBytes(body, "stream_options.include_usage", true)
	}
	return body, nil
}

// SetUpstream configures the outbound request for an OpenAI Chat Completions
// upstream. Auth and URL handling are identical to OpenAITransformer — only
// the response wire format differs.
func (t *OpenAIChatCompletionsTransformer) SetUpstream(r *http.Request, upstream *types.Upstream, apiKey string) error {
	directorSetOpenAIUpstream(r, upstream.BaseURL, apiKey)
	return nil
}

// WrapStream wraps the response body with the Chat Completions SSE interceptor,
// which unconditionally fires onComplete on EOF so the request row finalizes
// even when no usage chunk arrives (matches Anthropic stream interceptor
// behavior — see stream.go:117-125).
func (t *OpenAIChatCompletionsTransformer) WrapStream(body io.ReadCloser, startTime time.Time, onComplete func(StreamMetrics)) io.ReadCloser {
	return newChatCompletionsStreamInterceptor(body, startTime, onComplete)
}

// ParseResponse extracts metrics from a non-streaming Chat Completions response.
func (t *OpenAIChatCompletionsTransformer) ParseResponse(body []byte) (*ResponseMetrics, error) {
	return ParseChatCompletionsResponse(body)
}
