package proxy

import (
	"testing"

	"github.com/modelserver/modelserver/internal/config"
)

// TestHandlerHttpLogAllowsPublisher table-tests every allowlist rule
// documented in the spec:
//   - default anthropic+openai allows both, denies others
//   - explicit [anthropic] excludes openai
//   - nil / empty slice deny all publishers
//   - empty-string publisher denied by default, allowed only when "" is
//     explicitly listed
//   - operator yaml typos "OpenAI" (case) or " anthropic " (whitespace)
//     match the corresponding catalog rows after normalization
func TestHandlerHttpLogAllowsPublisher(t *testing.T) {
	cases := []struct {
		name       string
		publishers []string
		input      string
		want       bool
	}{
		{"default allows anthropic", []string{"anthropic", "openai"}, "anthropic", true},
		{"default allows openai", []string{"anthropic", "openai"}, "openai", true},
		{"default denies gemini", []string{"anthropic", "openai"}, "gemini", false},
		{"default denies empty publisher", []string{"anthropic", "openai"}, "", false},
		{"anthropic-only denies openai", []string{"anthropic"}, "openai", false},
		{"anthropic-only allows anthropic", []string{"anthropic"}, "anthropic", true},
		{"nil slice denies anthropic", nil, "anthropic", false},
		{"empty slice denies anthropic", []string{}, "anthropic", false},
		{"empty in list allows empty", []string{""}, "", true},
		{"case-insensitive: OpenAI matches openai", []string{"OpenAI"}, "openai", true},
		{"case-insensitive: OpenAI matches OPENAI catalog row", []string{"openai"}, "OPENAI", true},
		{"whitespace trimmed on both sides", []string{" anthropic "}, "anthropic ", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{}
			h.httpLogAllowlist = buildHttpLogAllowlist(config.HttpLogConfig{Publishers: tc.publishers})
			if got := h.httpLogAllowsPublisher(tc.input); got != tc.want {
				t.Errorf("httpLogAllowsPublisher(%q) with publishers=%v = %v, want %v",
					tc.input, tc.publishers, got, tc.want)
			}
		})
	}
}
