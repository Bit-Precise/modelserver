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
			name: "machine-readable overloaded error code",
			body: `{"error":{"code":"overloaded_error"}}`,
			want: true,
		},
		{
			name: "machine-readable model at capacity code",
			body: `{"error":{"code":"model_at_capacity"}}`,
			want: true,
		},
		{
			name: "machine-readable overloaded alias",
			body: `{"error":{"code":"server_overloaded"}}`,
			want: true,
		},
		{
			name: "codex rollout overloaded info",
			body: `{"codex_error_info":"server_overloaded"}`,
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

func TestProbeCodexCapacityStream_HoldsCodexMetadataUntilFailedEvent(t *testing.T) {
	body := "event: codex.response.metadata\ndata: {\"type\":\"codex.response.metadata\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe did not hold Codex metadata long enough to detect capacity failure")
	}
	if _, err := io.ReadAll(replay); err != nil {
		t.Fatalf("read replay body: %v", err)
	}
}

func TestProbeCodexCapacityStream_HoldsOutputItemAddedUntilFailedEvent(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\",\"message\":\"Selected model is at capacity. Please try a different model.\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe committed the structural output_item.added event before the capacity failure")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_HoldsDoneEventsUntilFailedEvent(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"summary\":[]}}\n\n" +
		"event: response.reasoning_summary_text.done\ndata: {\"type\":\"response.reasoning_summary_text.done\",\"text\":\"\"}\n\n" +
		"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"summary\":[]}}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe committed a buffered *.done event before the capacity failure")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_DoesNotRetryAfterOutputDelta(t *testing.T) {
	body := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"content\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"visible output\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if retry {
		t.Fatal("probe retried after user-visible output had started")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_DoesNotRetryAfterReasoningDelta(t *testing.T) {
	body := "event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"summary\":[]}}\n\n" +
		"event: response.reasoning_summary_text.delta\ndata: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"visible reasoning\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if retry {
		t.Fatal("probe retried after a reasoning delta had been exposed")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestCodexStreamEventAllowsCommit(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		want      bool
	}{
		{name: "created", eventType: "response.created", want: false},
		{name: "output item added", eventType: "response.output_item.added", want: false},
		{name: "output item done", eventType: "response.output_item.done", want: false},
		{name: "reasoning done", eventType: "response.reasoning_summary_text.done", want: false},
		{name: "empty output delta", eventType: "response.output_text.delta", want: false},
		{name: "empty reasoning delta", eventType: "response.reasoning_summary_text.delta", want: false},
		{name: "empty function delta", eventType: "response.function_call_arguments.delta", want: false},
		{name: "empty partial image", eventType: "response.image_generation_call.partial_image", want: false},
		{name: "failed", eventType: "response.failed", want: true},
		{name: "incomplete", eventType: "response.incomplete", want: true},
		{name: "completed", eventType: "response.completed", want: true},
		{name: "error", eventType: "error", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			event := []byte("data: {\"type\":\"" + tt.eventType + "\"}\n\n")
			if got := codexStreamEventAllowsCommit(event); got != tt.want {
				t.Fatalf("codexStreamEventAllowsCommit(%q) = %v, want %v", tt.eventType, got, tt.want)
			}
		})
	}
}

func TestCodexStreamEventAllowsCommit_OnlyWithActualOutput(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  bool
	}{
		{"text delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n", true},
		{"reasoning delta", "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n\n", true},
		{"function delta", "data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"x\\\":1}\"}\n\n", true},
		{"image", "data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"abc\"}\n\n", true},
		{"text done", "data: {\"type\":\"response.output_text.done\",\"text\":\"done\"}\n\n", true},
		{"whitespace text delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":\" \"}\n\n", true},
		{"empty text delta", "data: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\n", false},
		{"empty output item", "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"content\":[]}}\n\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexStreamEventAllowsCommit([]byte(tt.event)); got != tt.want {
				t.Fatalf("codexStreamEventAllowsCommit() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProbeCodexCapacityStream_RetryAfterEmptyDelta(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.in_progress\ndata: {\"type\":\"response.in_progress\"}\n\n" +
		"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"content\":[]}}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"\"}\n\n" +
		"event: response.output_text.done\ndata: {\"type\":\"response.output_text.done\",\"text\":\"\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("empty delta/done events committed the stream before the capacity failure")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
	}
}

func TestProbeCodexCapacityStream_DoesNotRetryTerminalWithOutput(t *testing.T) {
	body := "event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"server_is_overloaded\"},\"output\":[{\"type\":\"message\"}],\"usage\":{\"output_tokens\":1}}}\n\n"
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if retry {
		t.Fatal("terminal carrying output was incorrectly marked replayable")
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

func TestProbeCodexCapacityStream_RetryOnJSONErrorBody(t *testing.T) {
	body := `{"error":{"code":"server_is_overloaded","message":"Selected model is at capacity. Please try a different model."}}`
	retry, replay := probeCodexCapacityStream(io.NopCloser(strings.NewReader(body)))
	if !retry {
		t.Fatal("probe did not recognize a JSON capacity error returned for a stream request")
	}
	got, err := io.ReadAll(replay)
	if err != nil {
		t.Fatalf("read replay body: %v", err)
	}
	if string(got) != body {
		t.Fatalf("replay body = %q, want original body", got)
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
