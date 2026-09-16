package admin

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/modelserver/modelserver/internal/types"
)

func TestListRequestKindsIncludesResponsesWebsocket(t *testing.T) {
	w := httptest.NewRecorder()
	handleListRequestKinds()(w, httptest.NewRequest("GET", "/request-kinds", nil))
	var response struct {
		Data []string `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{types.KindOpenAIResponses, types.KindOpenAIResponsesWebsocket} {
		if !slices.Contains(response.Data, kind) {
			t.Errorf("request kind catalog missing %s: %v", kind, response.Data)
		}
	}
}
