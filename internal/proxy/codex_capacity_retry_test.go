package proxy

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestIsCodexCapacityErrorBody(t *testing.T) {
	cases := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "codex user-facing message",
			body: `Selected model is at capacity. Please try a different model.`,
			want: true,
		},
		{
			name: "machine-readable overloaded code",
			body: `{"error":{"code":"server_is_overloaded"}}`,
			want: true,
		},
		{
			name: "machine-readable slow down code",
			body: `{"error":{"code":"slow_down"}}`,
			want: true,
		},
		{
			name: "top-level message envelope",
			body: `{"message":"Selected model is at capacity. Please try a different model."}`,
			want: true,
		},
		{
			name: "string error envelope",
			body: `{"error":"Selected model is at capacity. Please try a different model."}`,
			want: true,
		},
		{
			name: "nested detail envelope",
			body: `{"response":{"failure":{"detail":"Selected model is at capacity. Please try a different model."}}}`,
			want: true,
		},
		{
			name: "whitespace in message",
			body: `{"error":{"message":"Selected model is at capacity.\nPlease try a different model."}}`,
			want: true,
		},
		{
			name: "different error",
			body: `{"error":{"code":"invalid_prompt"}}`,
			want: false,
		},
		{
			name: "same text in generated output is not an error",
			body: `{"output":[{"type":"message","content":[{"type":"output_text","text":"Selected model is at capacity. Please try a different model."}]}]}`,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCodexCapacityErrorBody([]byte(tc.body)); got != tc.want {
				t.Fatalf("isCodexCapacityErrorBody() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestInspectCodexCapacityResponse_ReplaysNonStreamingBody(t *testing.T) {
	body := `{"error":{"message":"Selected model is at capacity. Please try a different model."}}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	if !inspectCodexCapacityResponse(resp, nil, false, 0) {
		t.Fatal("inspection did not detect non-streaming capacity error")
	}
	replayed, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(replayed) != body {
		t.Fatalf("replayed body = %q, want %q", replayed, body)
	}
}

func TestInspectCodexCapacityResponse_DoesNotMatchNormalNonStreamingBody(t *testing.T) {
	body := `{"output":[{"type":"message","content":[{"type":"output_text","text":"Selected model is at capacity. Please try a different model."}]}]}`
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	if inspectCodexCapacityResponse(resp, nil, false, 0) {
		t.Fatal("normal response body incorrectly marked as capacity error")
	}
	replayed, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(replayed) != body {
		t.Fatalf("replayed body = %q, want %q", replayed, body)
	}
}

func TestProbeCodexCapacityStream_RetryOnFailedEvent(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe did not mark overloaded stream retryable")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_RetryOnCRLFFailedEvent(t *testing.T) {
	body := "event: response.failed\r\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\r\n\r\n"
	retry, _ := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe did not recognize CRLF-delimited overloaded event")
	}
}

func TestProbeCodexCapacityStream_RetryOnErrorEventMessage(t *testing.T) {
	body := "event: error\n" +
		"data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe did not recognize Codex error event capacity message")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_RetryOnDataOnlyErrorEvent(t *testing.T) {
	body := "data: {\"type\":\"error\",\"message\":\"Selected model is at capacity. Please try a different model.\"}\n\n"
	retry, _ := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe did not recognize data-only Codex error event")
	}
}

func TestProbeCodexCapacityStream_PreservesNormalStream(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if retry {
		t.Fatal("normal stream incorrectly marked retryable")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_DoesNotRetryOnGeneratedText(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Selected model is at capacity. Please try a different model.\"}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if retry {
		t.Fatal("generated text incorrectly marked stream retryable")
	}
	if _, err := io.ReadAll(replay); err != nil {
		t.Fatalf("read replay body: %v", err)
	}
}

func TestProbeCodexCapacityStream_BoundsMetadataBuffer(t *testing.T) {
	body := strings.Repeat(": keepalive\n\n", (codexCapacityProbeMaxBytes/len(": keepalive\n\n"))+10)
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if retry {
		t.Fatal("keepalive-only stream incorrectly marked retryable")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body length = %d, want %d", len(got), len(body))
	}
}
