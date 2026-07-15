package admin

import (
	"errors"
	"strings"
	"testing"
)

func TestReadUpstreamTestResponseBody(t *testing.T) {
	t.Run("complete body", func(t *testing.T) {
		body, truncated, err := readUpstreamTestResponseBody(strings.NewReader(`{"ok":true}`))
		if err != nil {
			t.Fatal(err)
		}
		if truncated {
			t.Fatal("short response was marked truncated")
		}
		if got := string(body); got != `{"ok":true}` {
			t.Fatalf("body = %q", got)
		}
	})

	t.Run("truncated body", func(t *testing.T) {
		body, truncated, err := readUpstreamTestResponseBody(strings.NewReader(strings.Repeat("x", upstreamTestResponseBodyLimit+1)))
		if err != nil {
			t.Fatal(err)
		}
		if !truncated {
			t.Fatal("oversized response was not marked truncated")
		}
		if len(body) != upstreamTestResponseBodyLimit {
			t.Fatalf("body length = %d", len(body))
		}
	})

	t.Run("read error", func(t *testing.T) {
		wantErr := errors.New("read failed")
		body, truncated, err := readUpstreamTestResponseBody(errorReader{err: wantErr})
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v", err)
		}
		if truncated || len(body) != 0 {
			t.Fatalf("body = %q, truncated = %v", body, truncated)
		}
	})
}

func TestBedrockResponseErrorType(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "coral output envelope",
			body: `{"Output":{"__type":"com.amazon.coral.service#UnknownOperationException"},"Version":"1.0"}`,
			want: "com.amazon.coral.service#UnknownOperationException",
		},
		{
			name: "top-level AWS error",
			body: `{"__type":"ValidationException","message":"bad model"}`,
			want: "ValidationException",
		},
		{
			name: "normal Anthropic response",
			body: `{"type":"message","content":[]}`,
		},
		{
			name: "non-JSON response",
			body: `not-json`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bedrockResponseErrorType([]byte(tt.body)); got != tt.want {
				t.Fatalf("bedrockResponseErrorType() = %q, want %q", got, tt.want)
			}
		})
	}
}

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
