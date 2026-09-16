package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/modelserver/modelserver/internal/types"
	"github.com/tidwall/gjson"
	"nhooyr.io/websocket"
)

type responsesWebsocketSession struct {
	ctx          context.Context
	conn         *websocket.Conn
	selected     *SelectedUpstream
	model        string
	headers      http.Header
	fatal        bool // Never replay a request after a frame may have been sent.
	events       chan responsesWebsocketEvent
	readerDone   chan struct{}
	readerCancel context.CancelFunc
}

type responsesWebsocketEvent struct {
	kind websocket.MessageType
	data []byte
	err  error
}

func (e *Executor) doUpstreamRequest(client *http.Client, req *http.Request, candidate *SelectedUpstream, rc *RequestContext, start time.Time) (*http.Response, error) {
	if ws := responsesWebsocketFromContext(req.Context()); ws != nil {
		resp, err := ws.roundTrip(client, req, candidate, rc.Model, start)
		if resp != nil {
			if body, ok := resp.Body.(*responsesWebsocketBody); ok {
				body.idleTimeout = e.streamIdleTimeout
			}
		}
		return resp, err
	}
	return client.Do(req)
}

func (s *responsesWebsocketSession) close() {
	if s.conn != nil {
		if s.readerCancel != nil {
			s.readerCancel()
		}
		s.conn.CloseNow()
		if s.readerDone != nil {
			<-s.readerDone
		}
	}
}

func (s *responsesWebsocketSession) readEvents(ctx context.Context) {
	defer close(s.readerDone)
	for {
		kind, data, err := s.conn.Read(ctx)
		select {
		case s.events <- responsesWebsocketEvent{kind, data, err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// Revalidate the pinned upstream against the current route on every turn.
// Rebalancing a continuation would lose connection-local response IDs (even
// store:false responses are cached on a Codex WebSocket connection).
func (s *responsesWebsocketSession) candidates(router *Router, group *resolvedGroup, model string) ([]*SelectedUpstream, error) {
	if s.selected == nil {
		return nil, nil
	}
	if model != s.model {
		// Codex reconnects when its connection key (including model) changes.
		// Close this connection so other clients also know they must reconnect.
		s.fatal = true
		return nil, errors.New("model changed; open a new WebSocket connection")
	}
	router.mu.RLock()
	defer router.mu.RUnlock()
	for _, member := range group.members {
		u := member.upstream
		if u.ID != s.selected.Upstream.ID {
			continue
		}
		if u.Status != types.UpstreamStatusActive ||
			u.BaseURL != s.selected.Upstream.BaseURL || u.Provider != s.selected.Upstream.Provider ||
			u.EffectiveProxyMode() != s.selected.Upstream.EffectiveProxyMode() ||
			u.SocksProxyURL != s.selected.Upstream.SocksProxyURL || u.SocksProxyUsername != s.selected.Upstream.SocksProxyUsername ||
			!bytes.Equal(u.SocksProxyPasswordEncrypted, s.selected.Upstream.SocksProxyPasswordEncrypted) ||
			u.ResolveModel(model) != s.selected.Upstream.ResolveModel(model) ||
			router.decryptedKeys[u.ID] != s.selected.APIKey {
			break
		}
		if u.MaxConcurrent > 0 && router.ConnTracker().Count(u.ID) >= int64(u.MaxConcurrent) {
			return nil, errors.New("WebSocket upstream is temporarily unavailable")
		}
		return []*SelectedUpstream{{Upstream: u, APIKey: s.selected.APIKey}}, nil
	}
	s.fatal = true
	return nil, errors.New("WebSocket upstream configuration changed; reconnect")
}

func (s *responsesWebsocketSession) roundTrip(client *http.Client, req *http.Request, candidate *SelectedUpstream, model string, start time.Time) (*http.Response, error) {
	connected := false
	if s.conn == nil {
		headers := req.Header.Clone()
		headers.Del("Content-Type")
		headers.Del("Accept")
		headers.Set("OpenAI-Beta", responsesWebsocketBeta)
		// Reuse the exact HTTP transport, including environment/direct/SOCKS5
		// proxy selection, credentials and TLS settings.
		dialClient := *client
		dialClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		dialCtx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		conn, resp, err := websocket.Dial(dialCtx, req.URL.String(), &websocket.DialOptions{
			HTTPClient: &dialClient, HTTPHeader: headers, Host: req.Host,
			CompressionMode: websocket.CompressionContextTakeover,
		})
		cancel()
		if err != nil {
			// Handshake HTTP failures use the existing retry/OAuth recovery
			// policy. No response.create has been sent yet.
			if resp != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				if resp.StatusCode < 400 {
					resp.StatusCode = http.StatusBadGateway
				}
				return resp, nil
			}
			return nil, err
		}
		conn.SetReadLimit(64 << 20)
		s.conn, s.selected, s.model, s.headers = conn, candidate, model, resp.Header.Clone()
		connected = true
		s.events = make(chan responsesWebsocketEvent, 1)
		s.readerDone = make(chan struct{})
		// Read control frames even between turns, so upstream ping/close
		// handling doesn't depend on the client issuing another request.
		readerCtx, readerCancel := context.WithCancel(s.ctx)
		s.readerCancel = readerCancel
		go s.readEvents(readerCtx)
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	// From this point on the request may have reached the model. Surface
	// transport failures as an interrupted stream, never as a retryable dial.
	stream := &responsesWebsocketBody{
		session: s, ctx: req.Context(), start: start,
	}
	if connected {
		// The downstream upgrade precedes routing (the model is in the first
		// frame), so carry handshake hints in the metadata event supported by
		// codex-api/src/sse/responses.rs instead of losing turn-state/model.
		hints := make(map[string]string)
		for _, key := range []string{"x-codex-turn-state", "openai-model", "x-reasoning-included", "x-models-etag"} {
			if value := s.headers.Get(key); value != "" {
				hints[key] = value
			}
		}
		if len(hints) > 0 {
			data, _ := json.Marshal(map[string]any{"type": "response.metadata", "headers": hints})
			stream.appendEvent(data)
		}
	}
	if err := writeResponsesWebsocketFrame(req.Context(), s.conn, body); err != nil {
		stream.metrics.InterruptErr = errors.New("upstream WebSocket write failed")
		stream.done = true
		s.fatal = true
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       stream,
	}, nil
}

// responsesWebsocketBody exposes one upstream response as an SSE body. EOF
// ends a turn, not the connection. Opaque events, reasoning and tool outputs
// pass through without reconstruction or loss of unknown JSON fields.
type responsesWebsocketBody struct {
	session     *responsesWebsocketSession
	ctx         context.Context
	start       time.Time
	idleTimeout time.Duration
	buf         bytes.Buffer
	done        bool
	gotTTFT     bool
	metrics     StreamMetrics
}

func (b *responsesWebsocketBody) Read(p []byte) (int, error) {
	if b.buf.Len() > 0 {
		return b.buf.Read(p)
	}
	if b.done {
		if b.metrics.InterruptErr != nil && b.session.fatal {
			return 0, b.metrics.InterruptErr
		}
		return 0, io.EOF
	}
	readCtx := b.ctx
	var cancel context.CancelFunc
	if b.idleTimeout > 0 {
		readCtx, cancel = context.WithTimeout(readCtx, b.idleTimeout)
		defer cancel()
	}
	var event responsesWebsocketEvent
	select {
	case event = <-b.session.events:
	case <-readCtx.Done():
		event.err = readCtx.Err()
	}
	kind, data, err := event.kind, event.data, event.err
	if err != nil {
		b.metrics.InterruptErr = errors.New("upstream WebSocket closed before a terminal response")
		if errors.Is(readCtx.Err(), context.DeadlineExceeded) {
			b.metrics.InterruptErr = ErrStreamIdleTimeout
		}
		b.session.fatal = true
		b.done = true
		return 0, b.metrics.InterruptErr
	}
	if kind != websocket.MessageText || !gjson.ValidBytes(data) {
		b.metrics.InterruptErr = errors.New("invalid upstream WebSocket event")
		b.session.fatal = true
		b.done = true
		return 0, b.metrics.InterruptErr
	}
	eventType, model, id, usage, terminal := ParseOpenAIStreamEvent(data)
	if model != "" {
		b.metrics.Model = model
	}
	if id != "" {
		b.metrics.MsgID = id
	}
	if !b.gotTTFT && (eventType == "response.output_text.delta" || eventType == "response.function_call_arguments.delta") {
		b.metrics.TTFTMs = time.Since(b.start).Milliseconds()
		b.gotTTFT = true
	}
	if terminal {
		b.done = true
		b.metrics.InputTokens = max(0, usage.InputTokens-usage.InputTokensDetails.CachedTokens)
		b.metrics.OutputTokens = usage.OutputTokens
		b.metrics.CacheReadTokens = usage.InputTokensDetails.CachedTokens
		if eventType == "response.failed" {
			b.metrics.InterruptErr = fmt.Errorf("response.failed: %s", gjson.GetBytes(data, "response.error.message").String())
		}
	}
	if eventType == "error" {
		b.done = true
		b.metrics.InterruptErr = fmt.Errorf("upstream error: %s", gjson.GetBytes(data, "error.message").String())
	}
	if (eventType == "response.failed" || eventType == "error") &&
		b.session.selected.Upstream.Provider == types.ProviderCodex {
		b.metrics.CodexCapacityError = isCodexCapacityErrorBody(data)
	}
	// JSON text frames may contain formatting newlines. Encode each as an
	// SSE data field; the downstream adapter rejoins them before forwarding.
	b.appendEvent(data)
	return b.buf.Read(p)
}

func (b *responsesWebsocketBody) appendEvent(data []byte) {
	for _, line := range bytes.Split(data, []byte("\n")) {
		b.buf.WriteString("data: ")
		b.buf.Write(line)
		b.buf.WriteByte('\n')
	}
	b.buf.WriteByte('\n')
}

func (b *responsesWebsocketBody) Close() error {
	if !b.done || b.session.fatal {
		b.session.conn.CloseNow()
		b.session.fatal = true
	}
	return nil
}

// Finalize even on a pre-usage error/disconnect. The ordinary OpenAI SSE
// parser only finalizes after usage; WebSocket prewarms/errors may omit it.
type responsesWebsocketStream struct {
	io.ReadCloser
	body       *responsesWebsocketBody
	onComplete func(StreamMetrics)
	once       sync.Once
}

func (s *responsesWebsocketStream) Close() error {
	err := s.ReadCloser.Close()
	s.once.Do(func() { s.onComplete(s.body.metrics) })
	return err
}
