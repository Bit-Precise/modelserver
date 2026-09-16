package httplog

import (
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestReassembleSSEResponsesWebsocket(t *testing.T) {
	const sse = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-ws\",\"model\":\"gpt-5\",\"status\":\"completed\",\"usage\":{\"input_tokens\":12,\"output_tokens\":3}}}\n\n"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	body := reassembleSSE(&Record{RequestKind: types.KindOpenAIResponsesWebsocket, ResponseBody: []byte(sse)}, logger)
	var response struct {
		ID    string `json:"id"`
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("WebSocket log was not reassembled: %s (%v)", body, err)
	}
	if response.ID != "resp-ws" || response.Usage.InputTokens != 12 {
		t.Fatalf("reassembled log = %s", body)
	}
}
