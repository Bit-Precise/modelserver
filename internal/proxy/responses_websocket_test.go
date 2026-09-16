package proxy

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/collector"
	"github.com/modelserver/modelserver/internal/config"
	"github.com/modelserver/modelserver/internal/ratelimit"
	"github.com/modelserver/modelserver/internal/store"
	"github.com/modelserver/modelserver/internal/types"
	"github.com/tidwall/gjson"
	"nhooyr.io/websocket"
)

// Exercise real HTTP upgrades, masking, compression and close handling without
// opening a listening port (also works in sandboxes which forbid bind()).
type websocketTestListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *websocketTestListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.connections:
		return conn, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *websocketTestListener) Close() error { l.once.Do(func() { close(l.done) }); return nil }
func (l *websocketTestListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 80}
}

func websocketTestHTTPClient(t *testing.T, handler http.Handler) *http.Client {
	t.Helper()
	listener := &websocketTestListener{connections: make(chan net.Conn), done: make(chan struct{})}
	server := &http.Server{Handler: handler}
	go server.Serve(listener)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		select {
		case listener.connections <- server:
			return client, nil
		case <-ctx.Done():
			client.Close()
			server.Close()
			return nil, ctx.Err()
		case <-listener.done:
			client.Close()
			server.Close()
			return nil, net.ErrClosed
		}
	}}
	t.Cleanup(func() { transport.CloseIdleConnections(); server.Close() })
	return &http.Client{Transport: transport}
}

func websocketTestDial(t *testing.T, client *http.Client) (*websocket.Conn, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	conn, _, err := websocket.Dial(ctx, "http://modelserver.test/v1/responses", &websocket.DialOptions{
		HTTPClient:      client,
		HTTPHeader:      http.Header{"Authorization": {"Bearer client-secret"}, "Session-Id": {"session-1"}, "Thread-Id": {"thread-1"}, "X-Codex-Turn-State": {"turn-state"}},
		CompressionMode: websocket.CompressionContextTakeover,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn, ctx
}

func websocketTestRead(t *testing.T, ctx context.Context, conn *websocket.Conn) []byte {
	t.Helper()
	kind, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.MessageText {
		t.Fatalf("message kind = %v", kind)
	}
	return data
}

type websocketTestRecords struct{ rows chan types.Request }

func (r *websocketTestRecords) BatchCreateRequests(rows []types.Request) error {
	for _, row := range rows {
		r.rows <- row
	}
	return nil
}

type websocketTestLimiter struct {
	checks  atomic.Int32
	records atomic.Int32
	limit   int32
}

func (l *websocketTestLimiter) PreCheck(context.Context, string, string, string, *types.RateLimitPolicy) (ratelimit.PreCheckResult, error) {
	if n := l.checks.Add(1); l.limit > 0 && n > l.limit {
		return ratelimit.PreCheckResult{Allowed: false, LimitType: ratelimit.LimitTypeClassic, RetryAfter: time.Second}, nil
	}
	return ratelimit.PreCheckResult{Allowed: true}, nil
}
func (l *websocketTestLimiter) PreCheckClassicOnly(ctx context.Context, p, k, m string, policy *types.RateLimitPolicy) (ratelimit.PreCheckResult, error) {
	return l.PreCheck(ctx, p, k, m, policy)
}
func (l *websocketTestLimiter) CheckUserQuota(context.Context, string, string, float64, *types.RateLimitPolicy) (bool, time.Duration, error) {
	return true, 0, nil
}
func (l *websocketTestLimiter) PostRecord(context.Context, string, string, string, string, types.TokenUsage) {
	l.records.Add(1)
}

func websocketTestExecutor(t *testing.T, upstreamClient *http.Client, provider string) (*Executor, *websocketTestRecords, *websocketTestLimiter) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := NewRouter([]types.Upstream{{
		ID: "up", Provider: provider, BaseURL: "http://upstream.test", Status: types.UpstreamStatusActive,
		Weight: 1, MaxConcurrent: 1, SupportedModels: []string{"gpt-5"}, ModelMap: map[string]string{"gpt-5": "upstream-model"},
	}}, []store.UpstreamGroupWithMembers{{
		UpstreamGroup: types.UpstreamGroup{ID: "group", LBPolicy: types.LBPolicyWeightedRandom, Status: "active", RetryPolicy: &types.RetryPolicy{MaxRetries: 2, RetryOn: []string{"connection_error", "5xx"}}},
		Members:       []store.UpstreamGroupMemberDetail{{UpstreamGroupMember: types.UpstreamGroupMember{UpstreamID: "up"}}},
	}}, []types.Route{{
		ID: "route", ModelNames: []string{"gpt-5"}, RequestKinds: []string{types.KindOpenAIResponsesWebsocket}, UpstreamGroupID: "group", Status: "active",
	}}, nil, logger, time.Hour, nil, nil, nil)
	router.decryptedKeys["up"] = "upstream-secret"
	records := &websocketTestRecords{rows: make(chan types.Request, 16)}
	coll := collector.New(collector.Config{BatchSize: 1}, records, logger)
	coll.Start()
	t.Cleanup(coll.Stop)
	factory := NewOutboundClientFactory(nil)
	factory.environment = upstreamClient.Transport.(*http.Transport)
	limiter := &websocketTestLimiter{}
	executor := NewExecutor(router, factory, nil, coll, limiter, newTestCatalog(), logger, 16<<20, 0, config.ExtraUsageConfig{}, time.Second, 0, nil, config.HttpLogConfig{})
	return executor, records, limiter
}

func websocketTestGateway(t *testing.T, executor *Executor) *http.Client {
	t.Helper()
	h := &Handler{executor: executor, catalog: newTestCatalog(), maxBodySize: 16 << 20}
	// Replace only database authentication/pending-row insertion; admission,
	// provider transformation, transport, accounting and completion are real.
	turn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		model, ok := h.resolveModel(w, peekModel(r), IngressOpenAI)
		if !ok || !h.checkModelAllowed(w, r.Context(), APIKeyFromContext(r.Context()), model, writeProxyError) {
			return
		}
		executor.Execute(w, r, &RequestContext{
			ProjectID: "project", APIKeyID: "key", Model: model, ModelRef: ModelFromContext(r.Context()),
			RequestKind: types.KindOpenAIResponsesWebsocket, IsStream: true, SessionID: TraceIDFromContext(r.Context()),
			Project: ProjectFromContext(r.Context()), APIKey: APIKeyFromContext(r.Context()), Policy: PolicyFromContext(r.Context()),
		})
	})
	chain := chi.Chain(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), ctxAPIKey, &types.APIKey{ID: "key"})
			ctx = context.WithValue(ctx, ctxProject, &types.Project{ID: "project"})
			ctx = context.WithValue(ctx, ctxPolicy, &types.RateLimitPolicy{})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}, TraceMiddleware(config.TraceConfig{CodexTraceEnabled: true}, nil, nil),
		ResolveModelMiddleware(newTestCatalog(), 16<<20, 0), SubscriptionEligibilityMiddleware(),
		RateLimitMiddleware(executor.rateLimiter, nil, executor.logger),
		ExtraUsageGuardMiddleware(config.ExtraUsageConfig{}, nil, executor.logger))
	return websocketTestHTTPClient(t, h.responsesWebSocketHandler(chain.Handler(turn)))
}

func TestResponsesWebsocketCodexWarmupAndContinuation(t *testing.T) {
	var dials atomic.Int32
	requests := make(chan []byte, 2)
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dials.Add(1)
		if r.Method != "GET" || r.URL.Path != "/responses" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		for key, want := range map[string]string{"Authorization": "Bearer upstream-secret", "OpenAI-Beta": responsesWebsocketBeta, "Session-Id": "session-1", "Thread-Id": "thread-1", "X-Codex-Turn-State": "turn-state"} {
			if got := r.Header.Get(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		w.Header().Set("X-Codex-Turn-State", "server-turn-state")
		w.Header().Set("Openai-Model", "server-model")
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.CloseNow()
		for _, event := range []string{
			`{"type":"response.completed","response":{"id":"warmup","model":"upstream-model","usage":{"input_tokens":0,"output_tokens":0}}}`,
			`{"type":"response.completed","response":{"id":"answer","model":"upstream-model","usage":{"input_tokens":15,"output_tokens":7,"input_tokens_details":{"cached_tokens":5}}}}`,
		} {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			requests <- data
			if gjson.GetBytes(data, "previous_response_id").Exists() {
				for _, extra := range []string{
					`{"type":"codex.rate_limits","rate_limits":{"used_percent":3}}`,
					`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call-1","name":"lookup"}}`,
					`{"type":"response.function_call_arguments.delta","delta":"{}"}`,
				} {
					if err := conn.Write(r.Context(), websocket.MessageText, []byte(extra)); err != nil {
						return
					}
				}
			}
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(event)); err != nil {
				return
			}
		}
		_, _, _ = conn.Read(r.Context())
	}))
	executor, records, limiter := websocketTestExecutor(t, upstream, types.ProviderCodex)
	limiter.limit = 2
	conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
	for i, payload := range []string{
		`{"type":"response.create","model":"gpt-5","input":[{"role":"user","content":"hello"}],"store":false,"generate":false,"client_metadata":{"thread_id":"thread-1"}}`,
		`{"type":"response.create","model":"gpt-5","previous_response_id":"warmup","input":[],"store":false}`,
	} {
		if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			metadata := websocketTestRead(t, ctx, conn)
			if gjson.GetBytes(metadata, "type").String() != "response.metadata" || gjson.GetBytes(metadata, "headers.x-codex-turn-state").String() != "server-turn-state" || gjson.GetBytes(metadata, "headers.openai-model").String() != "server-model" {
				t.Fatalf("handshake metadata = %s", metadata)
			}
		} else {
			for _, want := range []string{"codex.rate_limits", "response.output_item.added", "response.function_call_arguments.delta"} {
				if data := websocketTestRead(t, ctx, conn); gjson.GetBytes(data, "type").String() != want {
					t.Fatalf("event = %s, want %s", data, want)
				}
			}
		}
		if event := websocketTestRead(t, ctx, conn); gjson.GetBytes(event, "type").String() != "response.completed" {
			t.Fatalf("event = %s", event)
		}
		select {
		case row := <-records.rows:
			if row.Status != types.RequestStatusSuccess {
				t.Fatalf("row = %+v", row)
			}
			if row.RequestKind != types.KindOpenAIResponsesWebsocket {
				t.Fatalf("request kind = %q, want WebSocket kind", row.RequestKind)
			}
			if i == 1 && (row.InputTokens != 10 || row.OutputTokens != 7 || row.CacheReadTokens != 5 || row.MsgID != "answer") {
				t.Fatalf("usage = %+v", row)
			}
		case <-ctx.Done():
			t.Fatal("missing per-turn accounting")
		}
		wire := <-requests
		if gjson.GetBytes(wire, "model").String() != "upstream-model" || !gjson.GetBytes(wire, "stream").Bool() {
			t.Fatalf("request = %s", wire)
		}
		if i == 0 && (gjson.GetBytes(wire, "generate").Raw != "false" || gjson.GetBytes(wire, "client_metadata.thread_id").String() != "thread-1") {
			t.Fatalf("warmup changed = %s", wire)
		}
		if i == 1 && gjson.GetBytes(wire, "previous_response_id").String() != "warmup" {
			t.Fatalf("continuation changed = %s", wire)
		}
	}
	if got := executor.router.ConnTracker().Count("up"); got != 0 {
		t.Fatalf("connections = %d", got)
	}
	// A later turn must run admission again, even with an established socket.
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
	// Classic limits deliberately use HTTP 400 in modelserver; preserve it.
	if data := websocketTestRead(t, ctx, conn); gjson.GetBytes(data, "status").Int() != 400 || gjson.GetBytes(data, "error.type").String() != "rate_limit_error" {
		t.Fatalf("third turn bypassed rate limit: %s", data)
	}
	if limiter.checks.Load() != 3 || limiter.records.Load() != 2 || dials.Load() != 1 {
		t.Fatal("rejected turn generated upstream usage")
	}
}

func TestResponsesWebsocketErrorsAndDisconnect(t *testing.T) {
	for _, event := range []string{
		`{"type":"error","status":400,"error":{"code":"previous_response_not_found","message":"missing response"}}`,
		`{"type":"response.failed","response":{"id":"failed","error":{"message":"failed"}}}`,
		`{"type":"response.failed","response":{"id":"capacity","error":{"code":"server_is_overloaded","message":"Selected model is at capacity. Please try a different model."}}}`,
		"disconnect", "idle",
	} {
		t.Run(event, func(t *testing.T) {
			var calls atomic.Int32
			upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				if _, _, err := conn.Read(r.Context()); err != nil {
					return
				}
				if event == "disconnect" {
					return
				}
				if event != "idle" {
					_ = conn.Write(r.Context(), websocket.MessageText, []byte(event))
				}
				_, _, _ = conn.Read(r.Context())
			}))
			provider := types.ProviderOpenAI
			capacity := gjson.Get(event, "response.id").String() == "capacity"
			if capacity {
				provider = types.ProviderCodex
			}
			executor, records, _ := websocketTestExecutor(t, upstream, provider)
			executor.streamIdleTimeout = 50 * time.Millisecond
			conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
			if err := conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`)); err != nil {
				t.Fatal(err)
			}
			if event != "disconnect" && event != "idle" {
				if got := websocketTestRead(t, ctx, conn); string(got) != event {
					t.Fatalf("event changed: %s", got)
				}
			} else if _, _, err := conn.Read(ctx); err == nil {
				t.Fatal("expected connection close")
			}
			select {
			case row := <-records.rows:
				if row.Status != types.RequestStatusError {
					t.Fatalf("row = %+v", row)
				}
				if row.RequestKind != types.KindOpenAIResponsesWebsocket {
					t.Fatalf("error request kind = %q, want WebSocket kind", row.RequestKind)
				}
				if capacity && (row.RetryStatus != types.RequestRetryStatusRetryExhausted || row.RetryReason != codexCapacityAfterCommitRetryReason) {
					t.Fatalf("capacity retry classification = %s/%s", row.RetryStatus, row.RetryReason)
				}
			case <-ctx.Done():
				t.Fatal("missing error accounting")
			}
			if calls.Load() != 1 || executor.router.ConnTracker().Count("up") != 0 {
				t.Fatal("replayed request or leaked connection")
			}
		})
	}
}

func TestResponsesWebsocketValidationAndHTTPError(t *testing.T) {
	h := &Handler{maxBodySize: 1 << 20}
	var calls atomic.Int32
	client := websocketTestHTTPClient(t, h.responsesWebSocketHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "7")
		writeProxyError(w, 429, "rate limited")
	})))
	conn, ctx := websocketTestDial(t, client)
	for _, payload := range []string{`{`, `[]`, `{"type":"response.cancel"}`, `{"type":"response.create"}`} {
		_ = conn.Write(ctx, websocket.MessageText, []byte(payload))
		if got := websocketTestRead(t, ctx, conn); gjson.GetBytes(got, "status").Int() != 400 {
			t.Fatalf("event = %s", got)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid request reached pipeline")
	}
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
	got := websocketTestRead(t, ctx, conn)
	if gjson.GetBytes(got, "type").String() != "error" || gjson.GetBytes(got, "status").Int() != 429 || gjson.GetBytes(got, "headers.Retry-After").String() != "7" {
		t.Fatalf("HTTP error = %s", got)
	}
}

func TestResponsesWebsocketRouteRequiresAuthentication(t *testing.T) {
	r := chi.NewRouter()
	MountRoutes(r, nil, &Handler{}, config.TraceConfig{}, nil, nil, config.ExtraUsageConfig{}, 1<<20, 0, nil, slog.Default(), nil)
	for _, method := range []string{"GET", "POST"} {
		req := httptest.NewRequest(method, "/v1/responses", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("%s status = %d", method, w.Code)
		}
	}
}

func TestResponsesWebsocketTurnClearsAdmissionContext(t *testing.T) {
	ctx := context.WithValue(context.Background(), ctxPolicy, &types.RateLimitPolicy{})
	ctx = context.WithValue(ctx, ctxUserDeniedModels, []string{"old"})
	ctx = context.WithValue(ctx, ctxExtraUsageContext, ExtraUsageContext{})
	ctx = context.WithValue(ctx, "server-key", "preserved")
	turn := responsesWebsocketTurnContext{ctx}
	if PolicyFromContext(turn) != nil || len(UserDeniedModelsFromContext(turn)) != 0 {
		t.Fatal("stale authorization")
	}
	if _, ok := ExtraUsageContextFromContext(turn); ok {
		t.Fatal("stale billing context")
	}
	if turn.Value("server-key") != "preserved" {
		t.Fatal("lost server context")
	}
}

func TestResponsesWebsocketBodyPreservesMultilineEvents(t *testing.T) {
	data := []byte("{\n  \"type\": \"response.completed\",\n  \"response\": {\"id\":\"done\"}\n}")
	session := &responsesWebsocketSession{events: make(chan responsesWebsocketEvent, 1)}
	session.events <- responsesWebsocketEvent{kind: websocket.MessageText, data: data}
	body := &responsesWebsocketBody{session: session, ctx: context.Background()}
	encoded, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(encoded, []byte("data: {\ndata: ")) {
		t.Fatalf("SSE = %s", encoded)
	}
	if body.metrics.MsgID != "done" {
		t.Fatal("missing response ID")
	}
	if _, err := body.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

func TestResponsesWebsocketHandshakeError(t *testing.T) {
	var attempts atomic.Int32
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		if r.URL.Path != "/v1/responses" || r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Errorf("OpenAI handshake: %s %s auth=%t", r.Method, r.URL.Path, r.Header.Get("Authorization") == "Bearer upstream-secret")
		}
		w.Header().Set("Retry-After", "9")
		writeProxyError(w, 429, "upstream rate limit")
	}))
	executor, records, _ := websocketTestExecutor(t, upstream, types.ProviderOpenAI)
	conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
	data := websocketTestRead(t, ctx, conn)
	if gjson.GetBytes(data, "status").Int() != 429 || gjson.GetBytes(data, "headers.Retry-After").String() != "9" {
		t.Fatalf("handshake error = %s", data)
	}
	select {
	case row := <-records.rows:
		if row.Status != types.RequestStatusRateLimited {
			t.Fatalf("request = %+v", row)
		}
	case <-ctx.Done():
		t.Fatal("missing handshake failure record")
	}
	if attempts.Load() != 1 || executor.router.ConnTracker().Count("up") != 0 {
		t.Fatal("retried 429 or leaked connection")
	}
}

func TestResponsesWebsocketClientDisconnectCancelsUpstream(t *testing.T) {
	started, closed := make(chan struct{}), make(chan struct{})
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		if _, _, err := conn.Read(r.Context()); err != nil {
			return
		}
		close(started)
		_, _, _ = conn.Read(r.Context())
		close(closed)
	}))
	executor, records, _ := websocketTestExecutor(t, upstream, types.ProviderOpenAI)
	executor.streamIdleTimeout = 0
	conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("upstream did not receive request")
	}
	conn.CloseNow()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatal("upstream not cancelled")
	}
	select {
	case row := <-records.rows:
		if row.Status != types.RequestStatusError {
			t.Fatalf("request = %+v", row)
		}
	case <-ctx.Done():
		t.Fatal("missing cancellation accounting")
	}
	if executor.router.ConnTracker().Count("up") != 0 {
		t.Fatal("connection leaked")
	}
}

func TestResponsesWebsocketSessionGateAndBodyLimit(t *testing.T) {
	for _, tc := range []struct {
		name    string
		limit   int64
		payload string
	}{
		{"session required", 1024, `{"type":"response.create","model":"gpt-5","input":[]}`},
		{"oversize", 32, `{"type":"response.create","model":"gpt-5","input":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			turn := TraceMiddleware(config.TraceConfig{RequireSession: true}, nil, nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
			h := &Handler{maxBodySize: tc.limit}
			conn, ctx := websocketTestDial(t, websocketTestHTTPClient(t, h.responsesWebSocketHandler(turn)))
			_ = conn.Write(ctx, websocket.MessageText, []byte(tc.payload))
			if tc.limit == 32 {
				if _, _, err := conn.Read(ctx); err == nil {
					t.Fatal("oversize request accepted")
				}
			} else if data := websocketTestRead(t, ctx, conn); gjson.GetBytes(data, "status").Int() != 400 {
				t.Fatalf("event = %s", data)
			}
			if calls.Load() != 0 {
				t.Fatal("request bypassed gate")
			}
		})
	}
}

func TestResponsesWebsocketWriterFragmentedSSE(t *testing.T) {
	payload := []byte("{\n \"type\":\"response.completed\",\n \"response\":{\"id\":\"x\"}\n}")
	client := websocketTestHTTPClient(t, (&Handler{}).responsesWebSocketHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		encoded := &responsesWebsocketBody{}
		encoded.appendEvent(payload)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(sseHeartbeat))
		for _, b := range encoded.buf.Bytes() {
			_, _ = w.Write([]byte{b})
		}
	})))
	conn, ctx := websocketTestDial(t, client)
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5"}`))
	if got := websocketTestRead(t, ctx, conn); !bytes.Equal(got, payload) {
		t.Fatalf("event changed = %s", got)
	}
}

func TestResponsesWebsocketPinnedRouteRevalidation(t *testing.T) {
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected dial") }))
	executor, _, _ := websocketTestExecutor(t, upstream, types.ProviderOpenAI)
	group, err := executor.router.Match("project", "gpt-5", types.KindOpenAIResponsesWebsocket, types.ClientBucketOther)
	if err != nil {
		t.Fatal(err)
	}
	selected := executor.router.SelectWithRetry(context.Background(), group, "session", "gpt-5")[0]
	session := &responsesWebsocketSession{selected: selected, model: "gpt-5"}
	if candidates, err := session.candidates(executor.router, group, "gpt-5"); err != nil || len(candidates) != 1 {
		t.Fatalf("candidates = %v, %v", candidates, err)
	}
	other := &resolvedGroup{group: group.group}
	if _, err := session.candidates(executor.router, other, "gpt-5"); err == nil {
		t.Fatal("route removal ignored")
	}
	if _, err := session.candidates(executor.router, group, "other-model"); err == nil {
		t.Fatal("model switch ignored")
	}
}

func TestResponsesWebsocketModelPermissionsEveryTurn(t *testing.T) {
	h := &Handler{catalog: newTestCatalog()}
	var calls int
	turn := ResolveModelMiddleware(h.catalog, 16<<20, 0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		canonical, ok := h.resolveModel(w, peekModel(r), IngressOpenAI)
		if !ok {
			return
		}
		ctx := r.Context()
		if calls == 2 {
			ctx = context.WithValue(ctx, ctxUserDeniedModels, []string{"gpt-5"})
		}
		if !h.checkModelAllowed(w, ctx, &types.APIKey{AllowedModels: []string{"gpt-5"}}, canonical, writeProxyError) {
			return
		}
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"ok\"}}\n\n"))
	}))
	conn, ctx := websocketTestDial(t, websocketTestHTTPClient(t, h.responsesWebSocketHandler(turn)))
	for i := 0; i < 2; i++ {
		_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
		data := websocketTestRead(t, ctx, conn)
		if i == 0 && gjson.GetBytes(data, "type").String() != "response.completed" {
			t.Fatalf("first turn = %s", data)
		}
		if i == 1 && gjson.GetBytes(data, "status").Int() != 403 {
			t.Fatalf("revoked model permission bypassed: %s", data)
		}
	}
}

func TestResponsesWebsocketHTTPOnlyRoute(t *testing.T) {
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("HTTP-only upstream received a WebSocket request")
	}))
	executor, _, _ := websocketTestExecutor(t, upstream, types.ProviderBedrockOpenAI)
	conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
	if data := websocketTestRead(t, ctx, conn); gjson.GetBytes(data, "status").Int() != 426 {
		t.Fatalf("unsupported transport error = %s", data)
	}
}

func TestResponsesWebsocketDoesNotMatchHTTPRoute(t *testing.T) {
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("WebSocket request reached an HTTP-only route")
	}))
	executor, _, _ := websocketTestExecutor(t, upstream, types.ProviderOpenAI)
	executor.router.routes[0].RequestKinds = []string{types.KindOpenAIResponses}
	conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
	_ = conn.Write(ctx, websocket.MessageText, []byte(`{"type":"response.create","model":"gpt-5","input":[]}`))
	data := websocketTestRead(t, ctx, conn)
	if gjson.GetBytes(data, "status").Int() != http.StatusNotFound {
		t.Fatalf("WebSocket fell back to HTTP route: %s", data)
	}
}

func TestResponsesWebsocketRoutesAndMatrixAreIndependent(t *testing.T) {
	upstream := websocketTestHTTPClient(t, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("unexpected upstream call")
	}))
	executor, _, _ := websocketTestExecutor(t, upstream, types.ProviderOpenAI)
	router := executor.router
	if _, err := router.Match("project", "gpt-5", types.KindOpenAIResponses, types.ClientBucketCodexCLI); err == nil {
		t.Fatal("HTTP request matched a WebSocket-only route")
	}
	router.groups["http-group"] = &resolvedGroup{group: types.UpstreamGroup{ID: "http-group"}}
	router.routes = append(router.routes, types.Route{
		ID: "http-route", ModelNames: []string{"gpt-5"}, RequestKinds: []string{types.KindOpenAIResponses},
		UpstreamGroupID: "http-group", MatchPriority: 100, Status: "active",
	})
	for kind, want := range map[string]string{
		types.KindOpenAIResponses:          "http-group",
		types.KindOpenAIResponsesWebsocket: "group",
	} {
		group, err := router.Match("project", "gpt-5", kind, types.ClientBucketCodexCLI)
		if err != nil || group.group.ID != want {
			t.Fatalf("Match(%s) = %v, %v; want group %s", kind, group, err, want)
		}
		found := false
		for _, cell := range router.MatrixGlobal([]string{"gpt-5"}, types.ClientBucketCodexCLI) {
			if cell.Kind == kind {
				found = true
				if cell.UpstreamGroupID != want {
					t.Errorf("matrix %s group = %s, want %s", kind, cell.UpstreamGroupID, want)
				}
			}
		}
		if !found {
			t.Errorf("matrix missing kind %s", kind)
		}
	}
}
