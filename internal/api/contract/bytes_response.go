package contract

import (
	"io"

	"github.com/danielgtaylor/huma/v2"
)

// BytesResponse is an opaque binary body carried by an operation output struct.
// Huma streams it to the response verbatim when the field is named "Body" inside
// the operation's output struct. Content-Type resolves to application/octet-stream
// unless overridden by a Content-Type header field on the same struct.
//
// The Reader is drained but never closed by Huma. Handlers that need to close
// a resource should do so after the handler returns.
//
// Usage:
//
//	type MyOutput struct {
//	    ContentType   string `header:"Content-Type"`
//	    ContentLength int64  `header:"Content-Length,omitempty"`
//	    Body          contract.BytesResponse
//	}
//
//	// In handler:
//	return &MyOutput{
//	    ContentType:   "application/octet-stream",
//	    ContentLength: int64(len(data)),
//	    Body:          contract.BytesResponse{Reader: bytes.NewReader(data)},
//	}, nil
type BytesResponse struct {
	Reader io.Reader
}

// Schema implements huma.SchemaProvider so that the emitted OpenAPI response
// for any operation using BytesResponse as its Body carries a
// string/binary schema instead of a generated struct schema.
func (BytesResponse) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{
		Type:   "string",
		Format: "binary",
	}
}

// ContentType implements huma.ContentTypeFilter so that Huma advertises
// application/octet-stream as the response content type in the generated
// OpenAPI document when no explicit Content-Type header field is set.
func (BytesResponse) ContentType(string) string {
	return "application/octet-stream"
}

// ensure BytesResponse satisfies the Huma interfaces at compile time.
var _ huma.SchemaProvider = BytesResponse{}
var _ huma.ContentTypeFilter = BytesResponse{}
