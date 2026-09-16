package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
	"nhooyr.io/websocket"
)

func executeAvailabilityTestRequest(t *testing.T, executor *Executor) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "http://modelserver.test/v1/responses",
		strings.NewReader(`{"model":"gpt-5","input":[]}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	executor.Execute(w, r, &RequestContext{
		ProjectID: "project", APIKeyID: "key", Model: "gpt-5",
		RequestKind: types.KindOpenAIResponses, SessionID: "session-1",
		Project: &types.Project{ID: "project"}, APIKey: &types.APIKey{ID: "key"},
	})
	return w
}

// More than five consecutive upstream failures used to trip the circuit for
// thirty seconds. The next request must still reach the upstream immediately.
func TestUpstreamFailuresDoNotDisableSubsequentRequests(t *testing.T) {
	var calls atomic.Int32
	client := websocketTestHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 6 {
			writeProxyError(w, http.StatusServiceUnavailable, "upstream busy")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"recovered","model":"gpt-5","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	executor, _, _ := websocketTestExecutor(t, client, types.ProviderOpenAI)
	executor.router.routes[0].RequestKinds = []string{types.KindOpenAIResponses}
	group := executor.router.groups["group"]
	group.group.RetryPolicy = nil
	for i := int32(1); i <= 7; i++ {
		response := executeAvailabilityTestRequest(t, executor)
		if calls.Load() != i {
			t.Fatalf("request %d never reached upstream: %d %s", i, response.Code, response.Body.String())
		}
		wantStatus := http.StatusServiceUnavailable
		if i == 7 {
			wantStatus = http.StatusOK
		}
		if response.Code != wantStatus {
			t.Fatalf("request %d status = %d, want %d", i, response.Code, wantStatus)
		}
		if executor.router.ConnTracker().Count("up") != 0 {
			t.Fatal("completed request leaked capacity")
		}
	}
	if stats := executor.router.Metrics().GetStats("up"); stats == nil || stats.RecentErrors.Load() != 6 {
		t.Fatal("request failures must remain visible in metrics")
	}
}

// An open WebSocket must not become unusable because unrelated HTTP requests
// against its selected upstream failed while the connection was idle.
func TestUpstreamFailuresDoNotBlockWebsocketContinuation(t *testing.T) {
	var dials, frames atomic.Int32
	client := websocketTestHTTPClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeProxyError(w, http.StatusServiceUnavailable, "upstream busy")
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		dials.Add(1)
		defer conn.CloseNow()
		for {
			if _, _, err := conn.Read(r.Context()); err != nil {
				return
			}
			frames.Add(1)
			if err := conn.Write(r.Context(), websocket.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp-ok","usage":{"input_tokens":0,"output_tokens":0}}}`)); err != nil {
				return
			}
		}
	}))
	executor, records, _ := websocketTestExecutor(t, client, types.ProviderOpenAI)
	executor.router.routes[0].RequestKinds = []string{types.KindOpenAIResponses, types.KindOpenAIResponsesWebsocket}
	executor.router.groups["group"].group.RetryPolicy = nil
	conn, ctx := websocketTestDial(t, websocketTestGateway(t, executor))
	sendTurn := func(payload string) {
		t.Helper()
		if err := conn.Write(ctx, websocket.MessageText, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		data := websocketTestRead(t, ctx, conn)
		if !strings.Contains(string(data), `"type":"response.completed"`) {
			t.Fatalf("WebSocket turn rejected: %s", data)
		}
		select {
		case row := <-records.rows:
			if row.Status != types.RequestStatusSuccess {
				t.Fatalf("WebSocket accounting: %+v", row)
			}
		case <-ctx.Done():
			t.Fatal("WebSocket turn did not finish")
		}
	}
	sendTurn(`{"type":"response.create","model":"gpt-5","input":[]}`)
	for i := 0; i < 5; i++ {
		response := executeAvailabilityTestRequest(t, executor)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("failure response = %d", response.Code)
		}
		select {
		case <-records.rows:
		case <-ctx.Done():
			t.Fatal("failed HTTP request did not finish")
		}
	}
	sendTurn(`{"type":"response.create","model":"gpt-5","previous_response_id":"resp-ok","input":[]}`)
	if dials.Load() != 1 || frames.Load() != 2 {
		t.Fatalf("upstream connections/turns = %d/%d", dials.Load(), frames.Load())
	}
}

func TestUpstreamSelectionRetainsOperatorAndConcurrencyLimits(t *testing.T) {
	router, group := newTestRouterForSession(t)
	a, b, c := router.upstreams["up-a"], router.upstreams["up-b"], router.upstreams["up-c"]
	a.Status, b.Status, c.MaxConcurrent = types.UpstreamStatusDisabled, types.UpstreamStatusDraining, 1
	router.ConnTracker().Acquire(c.ID)
	if got := router.SelectWithRetry(context.Background(), group, "", "claude-sonnet"); len(got) != 0 {
		t.Fatal("operator/concurrency limits ignored")
	}
	if got := router.SelectCapacityFallbacks(group, nil); len(got) != 0 {
		t.Fatal("fallback ignored operator/concurrency limits")
	}
	router.ConnTracker().Release(c.ID)
	for i := 0; i < 20; i++ {
		router.Metrics().RecordError(c.ID)
	}
	if got := router.SelectWithRetry(context.Background(), group, "", "claude-sonnet"); len(got) != 1 || got[0].Upstream.ID != c.ID {
		t.Fatalf("available upstream excluded: %v", got)
	}
	if got := router.SelectCapacityFallbacks(group, nil); len(got) != 1 || got[0].Upstream.ID != c.ID {
		t.Fatalf("available fallback excluded: %v", got)
	}
}
