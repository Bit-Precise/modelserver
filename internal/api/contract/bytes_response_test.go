package contract

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// TestMarshalBytesResponseNilReaderIsError verifies that marshalBytesResponse
// returns a descriptive error when BytesResponse.Reader is nil, rather than
// silently writing an empty body.
func TestMarshalBytesResponseNilReaderIsError(t *testing.T) {
	err := marshalBytesResponse(io.Discard, BytesResponse{Reader: nil})
	if err == nil {
		t.Fatal("marshalBytesResponse with nil Reader returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error = %q, want message mentioning 'nil'", err.Error())
	}
}

// TestMarshalBytesResponseWrongTypeIsError verifies that marshalBytesResponse
// returns a descriptive error when given a value that is not a BytesResponse,
// rather than silently writing an empty body.
func TestMarshalBytesResponseWrongTypeIsError(t *testing.T) {
	err := marshalBytesResponse(io.Discard, "not a BytesResponse")
	if err == nil {
		t.Fatal("marshalBytesResponse with wrong type returned nil error, want non-nil")
	}
	if !strings.Contains(err.Error(), "BytesResponse") {
		t.Errorf("error = %q, want message mentioning 'BytesResponse'", err.Error())
	}
}
