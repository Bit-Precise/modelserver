package proxy

import (
	"net/http"
	"net/url"
	"strings"
)

const bedrockOpenAIPath = "/openai/v1/chat/completions"

// directorSetBedrockOpenAIUpstream configures the outbound request for an Amazon
// Bedrock OpenAI-compatible upstream. The base URL should point to the Bedrock
// Runtime regional endpoint (e.g. https://bedrock-runtime.us-west-2.amazonaws.com).
// The /openai/v1/chat/completions path is appended automatically.
func directorSetBedrockOpenAIUpstream(req *http.Request, baseURL, apiKey string) {
	endpoint := strings.TrimRight(baseURL, "/") + bedrockOpenAIPath
	target, err := url.Parse(endpoint)
	if err != nil {
		req.Header.Set("Authorization", "Bearer "+apiKey)
		return
	}

	req.URL.Scheme = target.Scheme
	req.URL.Host = target.Host
	req.URL.Path = target.Path
	req.URL.RawPath = target.RawPath
	req.Host = target.Host

	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Strip headers from other providers so they cannot leak upstream.
	req.Header.Del("x-api-key")
	req.Header.Del("anthropic-version")
	req.Header.Del("anthropic-beta")
	req.Header.Del("x-goog-api-key")
}
