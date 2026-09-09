package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type codexReplayBody struct {
	prefix     *bytes.Reader
	tail       io.Reader
	closer     io.Closer
	pendingErr error
}

func (b *codexReplayBody) Read(p []byte) (int, error) {
	if b.prefix.Len() > 0 {
		return b.prefix.Read(p)
	}
	if b.pendingErr != nil {
		err := b.pendingErr
		b.pendingErr = nil
		return 0, err
	}
	return b.tail.Read(p)
}

func (b *codexReplayBody) Close() error { return b.closer.Close() }

func (b *codexReplayBody) streamIdleTimeoutApplied() {}

func newCodexReplayBody(prefix []byte, tail io.Reader, closer io.Closer, pendingErr error) io.ReadCloser {
	return &codexReplayBody{
		prefix:     bytes.NewReader(prefix),
		tail:       tail,
		closer:     closer,
		pendingErr: pendingErr,
	}
}

const (
	codexCapacityMessage       = "selected model is at capacity. please try a different model."
	codexCapacityProbeMaxBytes = 64 * 1024
)

var errCodexCapacity = errors.New(codexCapacityMessage)

// isCodexCapacityErrorBody recognizes both the user-facing Codex error and
// the machine-readable codes used by the ChatGPT Responses backend. The
// latter are present in HTTP 503 bodies and in response.failed SSE events.
func isCodexCapacityErrorBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	lower := strings.ToLower(string(body))
	if gjson.ValidBytes(body) {
		for _, path := range []string{"error.code", "response.error.code"} {
			if isCodexCapacityCode(gjson.GetBytes(body, path).String()) {
				return true
			}
		}
		for _, path := range []string{"error.message", "response.error.message"} {
			if strings.Contains(strings.ToLower(gjson.GetBytes(body, path).String()), codexCapacityMessage) {
				return true
			}
		}
		return false
	}
	return strings.Contains(lower, codexCapacityMessage) ||
		strings.Contains(lower, `"server_is_overloaded"`) ||
		strings.Contains(lower, `"slow_down"`)
}

func isCodexCapacityCode(code string) bool {
	return strings.EqualFold(code, "server_is_overloaded") || strings.EqualFold(code, "slow_down")
}

// inspectCodexCapacityResponse checks a Codex response without consuming the
// body seen by the normal commit path. For successful streams it performs a
// bounded SSE look-ahead; non-streaming bodies are replay-buffered so the
// normal commit path still receives the exact bytes after inspection.
func inspectCodexCapacityResponse(resp *http.Response, doErr error, isStream bool, idleTimeout time.Duration) bool {
	if doErr != nil || resp == nil || resp.Body == nil {
		return false
	}
	if isStream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if idleTimeout > 0 {
			resp.Body = newIdleTimeoutReader(resp.Body, idleTimeout)
		}
		retry, replay := probeCodexCapacityStream(resp.Body)
		resp.Body = replay
		return retry
	}

	var body []byte
	var readErr error
	if !isStream && resp.StatusCode < 400 {
		body, readErr = io.ReadAll(resp.Body)
	} else if resp.StatusCode >= 400 {
		body, readErr = io.ReadAll(io.LimitReader(resp.Body, 4096))
	} else {
		return false
	}
	_ = resp.Body.Close()
	empty := io.NopCloser(bytes.NewReader(nil))
	resp.Body = newCodexReplayBody(body, empty, empty, readErr)
	return (readErr == nil || len(body) > 0) && isCodexCapacityErrorBody(body)
}

// codexStreamEventAllowsCommit reports whether it is safe to start forwarding
// a Codex stream. Metadata events (created/in_progress) are held back because
// the next event can still be response.failed with server_is_overloaded. Once
// output starts, or a terminal non-capacity event arrives, the response must
// be committed and cannot be replayed transparently.
func codexStreamEventAllowsCommit(event []byte) bool {
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		var envelope struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(data, &envelope) != nil {
			// A non-JSON SSE event is not a known metadata event; preserve
			// existing behavior by allowing it through.
			return true
		}
		if envelope.Type == "response.created" || envelope.Type == "response.in_progress" {
			return false
		}
		if envelope.Type == "response.failed" || envelope.Type == "response.completed" ||
			strings.HasPrefix(envelope.Type, "response.output") ||
			strings.HasPrefix(envelope.Type, "response.reasoning") ||
			strings.HasPrefix(envelope.Type, "response.function_call") ||
			strings.HasPrefix(envelope.Type, "response.custom_tool_call") {
			return true
		}
		// Unknown response events are safer to forward than to hold forever.
		return true
	}
	return false
}

// probeCodexCapacityStream reads just enough of a successful Codex SSE body
// to distinguish an immediate model-capacity failure from a real response.
// The consumed prefix is replayed through the returned body, so normal
// streams retain their exact bytes and streaming behavior. On a capacity
// failure the original body is closed and the prefix is returned for logging.
func probeCodexCapacityStream(body io.ReadCloser) (retry bool, replay io.ReadCloser) {
	var prefix bytes.Buffer
	var pending bytes.Buffer
	chunk := make([]byte, 4096)

	for {
		n, readErr := body.Read(chunk)
		if n > 0 {
			prefix.Write(chunk[:n])
			pending.Write(chunk[:n])

			for {
				raw := pending.Bytes()
				end, delimiterLen := sseEventEnd(raw)
				if end < 0 {
					break
				}
				event := append([]byte(nil), raw[:end+delimiterLen]...)
				pending.Next(end + delimiterLen)
				if isCodexCapacitySSEEvent(event) {
					_ = body.Close()
					return true, io.NopCloser(bytes.NewReader(prefix.Bytes()))
				}
				if codexStreamEventAllowsCommit(event) {
					return false, newCodexReplayBody(prefix.Bytes(), body, body, readErr)
				}
			}
			if prefix.Len() >= codexCapacityProbeMaxBytes {
				// Do not buffer an unbounded run of comments/metadata before
				// handing the stream to the normal forwarding path.
				return false, newCodexReplayBody(prefix.Bytes(), body, body, nil)
			}
		}

		if readErr != nil {
			if isCodexCapacitySSEEvent(pending.Bytes()) {
				_ = body.Close()
				return true, io.NopCloser(bytes.NewReader(prefix.Bytes()))
			}
			if readErr != io.EOF {
				// Preserve a transport/read error for the normal streaming
				// interceptor. Closing here would turn a timed-out or reset
				// stream into a misleading clean EOF after the consumed prefix.
				return false, newCodexReplayBody(prefix.Bytes(), body, body, readErr)
			}
			_ = body.Close()
			return false, io.NopCloser(bytes.NewReader(prefix.Bytes()))
		}
	}
}

func sseEventEnd(raw []byte) (int, int) {
	lf := bytes.Index(raw, []byte("\n\n"))
	crlf := bytes.Index(raw, []byte("\r\n\r\n"))
	if crlf >= 0 && (lf < 0 || crlf < lf) {
		return crlf, 4
	}
	if lf >= 0 {
		return lf, 2
	}
	return -1, 0
}

func isCodexCapacitySSEEvent(event []byte) bool {
	lower := strings.ToLower(string(event))
	if !strings.Contains(lower, "response.failed") {
		return false
	}
	return isCodexCapacityErrorBody(event)
}
