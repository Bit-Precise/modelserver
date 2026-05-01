package proxy

import (
	"io"
	"net/http"
	"time"

	"github.com/modelserver/modelserver/internal/types"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// BedrockOpenAITransformer handles Amazon Bedrock's OpenAI-compatible Chat
// Completions endpoint at /openai/v1/chat/completions. Auth is a static
// Bearer token (the Bedrock API key); body and response are standard OpenAI
// Chat Completions JSON, so the existing chatcompletions parsers apply.
type BedrockOpenAITransformer struct{}

var _ ProviderTransformer = (*BedrockOpenAITransformer)(nil)

// TransformBody ensures stream_options.include_usage is set for streaming
// requests so the upstream emits a final usage event for token accounting.
func (t *BedrockOpenAITransformer) TransformBody(body []byte, _ string, isStream bool, _ http.Header) ([]byte, error) {
	if isStream && !gjson.GetBytes(body, "stream_options.include_usage").Bool() {
		body, _ = sjson.SetBytes(body, "stream_options.include_usage", true)
	}
	return body, nil
}

// SetUpstream configures the outbound request URL and Bearer auth header.
func (t *BedrockOpenAITransformer) SetUpstream(r *http.Request, upstream *types.Upstream, apiKey string) error {
	directorSetBedrockOpenAIUpstream(r, upstream.BaseURL, apiKey)
	return nil
}

// WrapStream reuses the OpenAI Chat Completions SSE stream interceptor.
func (t *BedrockOpenAITransformer) WrapStream(body io.ReadCloser, startTime time.Time, onComplete func(StreamMetrics)) io.ReadCloser {
	return newChatCompletionsStreamInterceptor(body, startTime, onComplete)
}

// ParseResponse reuses the OpenAI Chat Completions non-streaming parser.
func (t *BedrockOpenAITransformer) ParseResponse(body []byte) (*ResponseMetrics, error) {
	return ParseChatCompletionsResponse(body)
}
