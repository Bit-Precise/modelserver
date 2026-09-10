package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
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
	codexCapacityMessage                = "selected model is at capacity. please try a different model."
	codexCapacityProbeMaxBytes          = 64 * 1024
	codexCapacityRetryReason            = "codex_capacity"
	codexCapacityNoFallbackRetryReason  = "codex_capacity_no_fallback"
	codexCapacityAfterCommitRetryReason = "codex_capacity_after_commit"
)

var errCodexCapacity = errors.New(codexCapacityMessage)

// isCodexCapacityErrorBody recognizes both the user-facing Codex error and
// the machine-readable codes used by the ChatGPT Responses backend. The
// latter are present in HTTP 503 bodies and in response.failed SSE events.
func isCodexCapacityErrorBody(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	trimmed := bytes.TrimPrefix(bytes.TrimSpace(body), []byte{0xef, 0xbb, 0xbf})
	if json.Valid(trimmed) {
		var value any
		if json.Unmarshal(trimmed, &value) == nil && isCodexCapacityJSONValue(value, true, false) {
			return true
		}
		// A valid JSON value that is not an error envelope (for example a
		// normal Responses output containing the same sentence) must not be
		// classified by a raw substring search.
		return false
	}
	return containsCodexCapacityText(string(trimmed)) ||
		strings.Contains(strings.ToLower(string(trimmed)), `"server_is_overloaded"`) ||
		strings.Contains(strings.ToLower(string(trimmed)), `"server_overloaded"`) ||
		strings.Contains(strings.ToLower(string(trimmed)), `"slow_down"`)
}

// isCodexCapacityJSONValue walks an error envelope without searching arbitrary
// output text. Codex has used several equivalent shapes over time, including
// {"message": ...}, {"detail": ...}, {"error": "..."}, and nested
// response.error objects. The root message/detail fields are accepted because
// those fields are only used as an error envelope by the Responses endpoint;
// message/text/delta fields nested under output/content are deliberately not.
func isCodexCapacityJSONValue(value any, root, errorContext bool) bool {
	switch v := value.(type) {
	case string:
		return (root || errorContext) && containsCodexCapacityText(v)
	case []any:
		for _, item := range v {
			if isCodexCapacityJSONValue(item, false, errorContext) {
				return true
			}
		}
	case map[string]any:
		for key, item := range v {
			name := strings.ToLower(strings.TrimSpace(key))
			switch name {
			case "code", "error_code", "errorcode":
				if code, ok := item.(string); ok && isCodexCapacityCode(code) {
					return true
				}
			case "codex_error_info":
				// Codex rollout telemetry uses server_overloaded for the same
				// condition represented on the wire as server_is_overloaded.
				if code, ok := item.(string); ok && isCodexCapacityCode(code) {
					return true
				}
			case "message", "detail", "reason", "description":
				if message, ok := item.(string); ok && (root || errorContext) && containsCodexCapacityText(message) {
					return true
				}
			case "error", "errors", "failure", "cause":
				if isCodexCapacityJSONValue(item, false, true) {
					return true
				}
				continue
			}
			if isCodexCapacityJSONValue(item, false, errorContext) {
				return true
			}
		}
	}
	return false
}

func containsCodexCapacityText(value string) bool {
	// Fields may contain line breaks or repeated whitespace after a proxy has
	// formatted the error. Compare a whitespace-normalized, case-insensitive
	// representation rather than only the byte-for-byte sentence.
	normalized := strings.Join(strings.Fields(strings.ToLower(value)), " ")
	return strings.Contains(normalized, codexCapacityMessage)
}

func isCodexCapacityCode(code string) bool {
	return strings.EqualFold(code, "server_is_overloaded") ||
		strings.EqualFold(code, "server_overloaded") ||
		strings.EqualFold(code, "slow_down")
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
// a Codex stream. Metadata, lifecycle, and *.done events are held back because
// none of their bytes have reached the client yet and the next event can still
// be response.failed with server_is_overloaded. Once an actual *.delta starts,
// an image is partially emitted, or a terminal non-capacity event arrives, the
// response must be committed and cannot be replayed transparently. Unknown
// JSON events are held briefly too; the probe has a bounded buffer.
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
			// A non-JSON SSE event is not inspectable as a Codex envelope;
			// preserve existing behavior by allowing it through.
			return true
		}
		switch envelope.Type {
		case "error", "response.failed", "response.incomplete", "response.completed":
			return true
		}
		if strings.HasPrefix(envelope.Type, "response.") &&
			(strings.HasSuffix(envelope.Type, ".delta") ||
				envelope.Type == "response.image_generation_call.partial_image") {
			return true
		}
		// Hold structural, lifecycle, *.done, and unknown JSON events. If no
		// actual delta follows, a capacity failure can still be retried without
		// exposing bytes from two different upstream responses to the client.
		// The bounded probe buffer below prevents indefinite accumulation.
		return false
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
			// A stream request normally returns SSE, but some gateways emit a
			// JSON error envelope with HTTP 200. Check the complete pending body
			// as a fallback after the stream reaches EOF; normal Responses output
			// remains excluded by the envelope-aware matcher.
			if isCodexCapacitySSEEvent(pending.Bytes()) || isCodexCapacityErrorBody(pending.Bytes()) {
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
	eventName, data := parseCodexSSEEvent(event)
	var envelope struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal(data, &envelope)
	if !strings.EqualFold(eventName, "response.failed") &&
		!strings.EqualFold(eventName, "error") &&
		!strings.EqualFold(envelope.Type, "response.failed") &&
		!strings.EqualFold(envelope.Type, "error") {
		return false
	}
	if len(data) > 0 {
		return isCodexCapacityErrorBody(data)
	}
	return isCodexCapacityErrorBody(event)
}

func parseCodexSSEEvent(event []byte) (eventName string, data []byte) {
	var payload bytes.Buffer
	for _, line := range bytes.Split(event, []byte("\n")) {
		line = bytes.TrimSpace(line)
		switch {
		case bytes.HasPrefix(line, []byte("event:")):
			eventName = strings.TrimSpace(string(bytes.TrimPrefix(line, []byte("event:"))))
		case bytes.HasPrefix(line, []byte("data:")):
			if payload.Len() > 0 {
				payload.WriteByte('\n')
			}
			payload.Write(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:"))))
		}
	}
	return eventName, payload.Bytes()
}
