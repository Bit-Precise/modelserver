package proxy

import (
	"testing"
)

func TestDirectorSetBedrockOpenAIUpstream(t *testing.T) {
	req := mustNewRequest(t, "POST", "http://localhost/v1/chat/completions", nil)

	directorSetBedrockOpenAIUpstream(req,
		"https://bedrock-runtime.us-west-2.amazonaws.com",
		"aws-bearer-token",
	)

	if req.URL.Scheme != "https" {
		t.Errorf("scheme = %s, want https", req.URL.Scheme)
	}
	if req.URL.Host != "bedrock-runtime.us-west-2.amazonaws.com" {
		t.Errorf("host = %s", req.URL.Host)
	}
	if req.URL.Path != "/openai/v1/chat/completions" {
		t.Errorf("path = %s, want /openai/v1/chat/completions", req.URL.Path)
	}
	if req.Host != "bedrock-runtime.us-west-2.amazonaws.com" {
		t.Errorf("Host header = %s", req.Host)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer aws-bearer-token" {
		t.Errorf("Authorization = %s, want Bearer aws-bearer-token", got)
	}
}

func TestDirectorSetBedrockOpenAIUpstream_TrailingSlash(t *testing.T) {
	req := mustNewRequest(t, "POST", "http://localhost/v1/chat/completions", nil)

	directorSetBedrockOpenAIUpstream(req,
		"https://bedrock-runtime.us-east-1.amazonaws.com/",
		"tok",
	)

	if req.URL.Path != "/openai/v1/chat/completions" {
		t.Errorf("path = %s, want /openai/v1/chat/completions", req.URL.Path)
	}
	if req.URL.Host != "bedrock-runtime.us-east-1.amazonaws.com" {
		t.Errorf("host = %s", req.URL.Host)
	}
}

func TestDirectorSetBedrockOpenAIUpstream_StripsConflictingHeaders(t *testing.T) {
	req := mustNewRequest(t, "POST", "http://localhost/v1/chat/completions", nil)
	req.Header.Set("x-api-key", "client-key")
	req.Header.Set("anthropic-version", "2023-06-01")
	req.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	req.Header.Set("x-goog-api-key", "stale")

	directorSetBedrockOpenAIUpstream(req,
		"https://bedrock-runtime.us-west-2.amazonaws.com",
		"tok",
	)

	for _, h := range []string{"x-api-key", "anthropic-version", "anthropic-beta", "x-goog-api-key"} {
		if v := req.Header.Get(h); v != "" {
			t.Errorf("%s should be removed, got %q", h, v)
		}
	}
}
