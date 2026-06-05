package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelserver/modelserver/internal/config"
	"github.com/modelserver/modelserver/internal/types"
)

// These tests exercise deriveClientKind independently of extractTraceID so
// we pin the invariant spec §3.2 cares about: classification is decoupled
// from trace-id source precedence AND from the trace_*_enabled config flags.

func TestDeriveClientKind_ClaudeCodeDetectedEvenWithTraceHeader(t *testing.T) {
	body := `{"metadata":{"user_id":"{\"session_id\":\"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\"}"}}`
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", io.NopCloser(bytes.NewReader([]byte(body))))
	// Even with an explicit trace header that would win extractTraceID, the
	// client-kind detection must still classify as claude-code.
	r.Header.Set("X-Trace-Id", "externally-provided-trace")

	got := deriveClientKind(r, config.TraceConfig{ClaudeCodeTraceEnabled: true})
	if got != types.ClientKindClaudeCode {
		t.Errorf("with X-Trace-Id present: got %q, want %q", got, types.ClientKindClaudeCode)
	}
}

func TestDeriveClientKind_IgnoresClaudeCodeDisabledFlag(t *testing.T) {
	body := `{"metadata":{"user_id":"{\"session_id\":\"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee\"}"}}`
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", io.NopCloser(bytes.NewReader([]byte(body))))

	// Operator disabled trace body inspection for trace-id purposes, but
	// subscription-eligibility must still classify correctly — otherwise
	// every Claude Code request would be pushed into extra usage.
	got := deriveClientKind(r, config.TraceConfig{ClaudeCodeTraceEnabled: false})
	if got != types.ClientKindClaudeCode {
		t.Errorf("with ClaudeCodeTraceEnabled=false: got %q, want %q", got, types.ClientKindClaudeCode)
	}
}

func TestDeriveClientKind_OpenCodeViaUserAgent(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "opencode/1.2.3 (darwin)")
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindOpenCode {
		t.Errorf("opencode UA → %q, want %q", got, types.ClientKindOpenCode)
	}
}

func TestDeriveClientKind_OpenClawViaUserAgent(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "openclaw/2.0")
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindOpenClaw {
		t.Errorf("openclaw UA → %q, want %q", got, types.ClientKindOpenClaw)
	}
}

func TestDeriveClientKind_CodexViaSessionHeader(t *testing.T) {
	// Modern codex CLI (≥0.135.0) sends hyphenated session-id.
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("Session-Id", "codex-1234")
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindCodex {
		t.Errorf("Session-Id header → %q, want %q", got, types.ClientKindCodex)
	}

	// Legacy codex CLI (≤0.124.x) sent underscored session_id; we still
	// recognize it so older clients keep getting trace correlation.
	r = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("Session_id", "codex-legacy")
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindCodex {
		t.Errorf("legacy Session_id header → %q, want %q", got, types.ClientKindCodex)
	}
}

// Real Claude Desktop UA captured from a production rejection. The Electron
// shell concatenates Chromium's UA with the product `Claude/<version>` and
// `Electron/<version>` segments; CLI's UA is the unrelated
// `claude-cli/<version> (external, cli)` string set in normalize_identity.go.
const claudeDesktopRealUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Claude/1.11187.1 Chrome/146.0.7680.216 Electron/41.6.1 Safari/537.36"

func TestDeriveClientKind_ClaudeDesktopViaUserAgent(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", claudeDesktopRealUA)
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindClaudeDesktop {
		t.Errorf("real Claude Desktop UA → %q, want %q", got, types.ClientKindClaudeDesktop)
	}
}

// The CLI's UA contains "claude-cli/" not "claude/". The Electron substring
// gate prevents collision either way, but pin it as a regression test in case
// somebody simplifies the rule to a single substring match.
func TestDeriveClientKind_ClaudeCLIIsNotMisclassifiedAsDesktop(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "claude-cli/2.1.116 (external, cli)")
	// No metadata.user_id in body — body classifier returns nothing, so this
	// should fall through to ClientKindUnknown (and definitely NOT to desktop).
	if got := deriveClientKind(r, config.TraceConfig{}); got == types.ClientKindClaudeDesktop {
		t.Errorf("claude-cli UA must not classify as desktop; got %q", got)
	}
}

// If somebody hand-fakes a UA with just "Claude/" but no Electron segment
// (e.g. some unrelated tool that uses the substring), don't promote them to
// desktop. The Electron gate is the meaningful signal.
func TestDeriveClientKind_ClaudeUASubstringWithoutElectronStaysUnknown(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	r.Header.Set("User-Agent", "claude/9.9.9 (some-other-tool)")
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindUnknown {
		t.Errorf("Claude/ UA without Electron/ → %q, want unknown", got)
	}
}

func TestDeriveClientKind_UnknownByDefault(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	if got := deriveClientKind(r, config.TraceConfig{}); got != types.ClientKindUnknown {
		t.Errorf("default → %q, want empty", got)
	}
}
