package contract

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/modelserver/modelserver/internal/authz"
)

type bytesInput struct{}
type bytesOutput struct {
	ContentType   string `header:"Content-Type"`
	ContentLength int64  `header:"Content-Length,omitempty"`
	Body          BytesResponse
}

func TestBytesResponseStreamsBinaryPayload(t *testing.T) {
	router := chi.NewRouter()
	api := NewAdminAPI(router, APIOptions{})

	payload := []byte{0x00, 0x01, 0x02, 0x03}
	Register(api, Operation{
		ID:            "downloadBlob",
		Method:        http.MethodGet,
		Path:          "/blob",
		DefaultStatus: http.StatusOK,
		Access:        authz.Public(),
	}, func(context.Context, *bytesInput) (*bytesOutput, error) {
		return &bytesOutput{
			ContentType:   "application/octet-stream",
			ContentLength: int64(len(payload)),
			Body:          BytesResponse{Reader: bytes.NewReader(payload)},
		}, nil
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/blob", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q", got)
	}
	got, _ := io.ReadAll(recorder.Body)
	if !bytes.Equal(got, payload) {
		t.Fatalf("body = %v, want %v", got, payload)
	}
}
