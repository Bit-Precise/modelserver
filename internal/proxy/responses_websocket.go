package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"nhooyr.io/websocket"
)

const responsesWebsocketKey contextKey = "responses_websocket"

// Match codex-rs/core/src/client.rs and codex-api/src/common.rs: requests
// are flat response.create objects (including generate:false prewarms).
const responsesWebsocketBeta = "responses_websockets=2026-02-06"

func responsesWebsocketFromContext(ctx context.Context) *responsesWebsocketSession {
	s, _ := ctx.Value(responsesWebsocketKey).(*responsesWebsocketSession)
	return s
}

// The context marks internal POST-shaped turns. Upgrade headers alone must
// never turn an ordinary HTTP POST into a WebSocket request kind.
func isResponsesWebsocketRequest(r *http.Request) bool {
	if r == nil || r.URL.Path != "/v1/responses" {
		return false
	}
	if responsesWebsocketFromContext(r.Context()) != nil {
		return true
	}
	if r.Method != http.MethodGet || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, value := range r.Header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
				return true
			}
		}
	}
	return false
}

// A connection owns exactly one upstream connection. Each response.create
// re-enters the HTTP admission/execution pipeline, with a fresh request row.
// Only the transport differs: JSON frames become SSE internally so billing,
// HTTP logs and provider usage parsing stay shared with POST /responses.
func (h *Handler) responsesWebSocketHandler(turnHandler http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			CompressionMode: websocket.CompressionContextTakeover,
		})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		limit := h.maxBodySize
		if limit <= 0 {
			limit = 16 << 20
		}
		conn.SetReadLimit(limit)
		// Codex rotates connections at the server's 60-minute limit. An idle
		// client must not retain authenticated resources indefinitely either.
		ctx, cancel := context.WithTimeout(r.Context(), time.Hour)
		defer cancel()
		session := &responsesWebsocketSession{ctx: ctx}
		defer session.close()

		// Keep reading while a turn executes, both to process ping/close frames
		// and to cancel upstream work immediately when the client disconnects.
		frames := make(chan []byte, 1)
		readerDone := make(chan struct{})
		go func() {
			defer close(readerDone)
			defer cancel()
			for {
				kind, payload, err := conn.Read(ctx)
				if err != nil {
					return
				}
				if kind != websocket.MessageText {
					_ = writeResponsesWebsocketError(ctx, conn, 400, "invalid_request_error", "expected a JSON text message")
					continue
				}
				select {
				case frames <- payload:
				default:
					_ = writeResponsesWebsocketError(ctx, conn, 400, "invalid_request_error", "only one response may be generated at a time")
				}
			}
		}()
		defer func() {
			cancel()
			conn.CloseNow()
			<-readerDone
		}()
		// HTTP Recoverer cannot write to a hijacked connection. Recover here
		// to close the WebSocket and still expose the panic in server logs.
		defer func() {
			if recovered := recover(); recovered != nil {
				logger := h.logger
				if logger == nil {
					logger = slog.Default()
				}
				logger.Error("Responses WebSocket handler panicked", "panic", recovered)
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case payload := <-frames:
				if ctx.Err() != nil {
					return
				}
				body, err := responsesWebsocketRequestBody(payload)
				if err != nil {
					if writeResponsesWebsocketError(ctx, conn, 400, "invalid_request_error", err.Error()) != nil {
						return
					}
					continue
				}
				// Discard authentication/admission values from the upgrade so
				// revoked keys, changed policies and model switches are checked
				// anew. Keep cancellation and unrelated server context values.
				turnCtx := context.WithValue(responsesWebsocketTurnContext{ctx}, responsesWebsocketKey, session)
				turn := r.Clone(turnCtx)
				turn.Method = http.MethodPost
				turn.Body = io.NopCloser(bytes.NewReader(body))
				turn.ContentLength = int64(len(body))
				turn.TransferEncoding = nil
				turn.Header.Set("Content-Type", "application/json")
				turn.Header.Del("Content-Encoding")
				for key := range turn.Header {
					if isHopByHopHeader(key) || strings.HasPrefix(http.CanonicalHeaderKey(key), "Sec-Websocket-") {
						turn.Header.Del(key)
					}
				}
				turn.Header.Del("Content-Length")
				writer := &responsesWebsocketWriter{ctx: ctx, conn: conn, header: make(http.Header)}
				turnHandler.ServeHTTP(writer, turn)
				if writer.finish() != nil || session.fatal {
					return
				}
			}
		}
	}
}

type responsesWebsocketTurnContext struct{ context.Context }

func (c responsesWebsocketTurnContext) Value(key any) any {
	// All proxy-owned context values are per request, never per connection.
	if _, ok := key.(contextKey); ok {
		return nil
	}
	return c.Context.Value(key)
}

func responsesWebsocketRequestBody(payload []byte) ([]byte, error) {
	if !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
		return nil, errors.New("expected a JSON object")
	}
	if gjson.GetBytes(payload, "type").String() != "response.create" {
		return nil, errors.New("unsupported event type; expected response.create")
	}
	model := gjson.GetBytes(payload, "model")
	if model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
		return nil, errors.New("model is required")
	}
	// A WebSocket response is always streamed, even if stream is omitted.
	return sjson.SetBytes(payload, "stream", true)
}

func writeResponsesWebsocketError(ctx context.Context, conn *websocket.Conn, status int, code, message string) error {
	data, _ := json.Marshal(map[string]any{
		"type": "error", "status": status,
		"error": map[string]string{"type": code, "code": code, "message": message},
	})
	return writeResponsesWebsocketFrame(ctx, conn, data)
}

func writeResponsesWebsocketFrame(ctx context.Context, conn *websocket.Conn, data []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

// responsesWebsocketWriter turns internal SSE into one JSON frame per event,
// and HTTP admission/upstream errors into the envelope Codex understands.
type responsesWebsocketWriter struct {
	ctx    context.Context
	conn   *websocket.Conn
	header http.Header
	status int
	buf    bytes.Buffer
	err    error
}

func (w *responsesWebsocketWriter) Header() http.Header { return w.header }
func (w *responsesWebsocketWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *responsesWebsocketWriter) Flush() {}

func (w *responsesWebsocketWriter) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.buf.Write(p)
	if w.status >= 400 {
		return len(p), nil
	}
	for {
		data := w.buf.Bytes()
		end := bytes.Index(data, []byte("\n\n"))
		if end < 0 {
			break
		}
		event := append([]byte(nil), data[:end]...)
		w.buf.Next(end + 2)
		var fields [][]byte
		for _, line := range bytes.Split(event, []byte("\n")) {
			if bytes.HasPrefix(line, []byte("data:")) {
				fields = append(fields, bytes.TrimPrefix(line[5:], []byte(" ")))
			}
		}
		payload := bytes.Join(fields, []byte("\n"))
		if len(payload) == 0 || bytes.Equal(payload, []byte("[DONE]")) {
			continue // SSE heartbeats are not Responses events.
		}
		w.err = writeResponsesWebsocketFrame(w.ctx, w.conn, payload)
		if w.err != nil {
			return 0, w.err
		}
	}
	return len(p), nil
}

func (w *responsesWebsocketWriter) finish() error {
	if w.err != nil {
		return w.err
	}
	if w.status >= 400 {
		var envelope map[string]any
		if json.Unmarshal(w.buf.Bytes(), &envelope) != nil || envelope == nil {
			envelope = map[string]any{"error": map[string]string{
				"type": "server_error", "message": http.StatusText(w.status),
			}}
		}
		envelope["type"] = "error"
		envelope["status"] = w.status
		// Includes Retry-After, which Codex reads from wrapped HTTP errors.
		headers := make(map[string]string)
		for key := range w.header {
			if !isHopByHopHeader(key) {
				headers[key] = w.header.Get(key)
			}
		}
		envelope["headers"] = headers
		data, _ := json.Marshal(envelope)
		return writeResponsesWebsocketFrame(w.ctx, w.conn, data)
	}
	return nil
}
