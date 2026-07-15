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

type errorReader struct {
	err error
}

func (r errorReader) Read([]byte) (int, error) {
	return 0, r.err
}
