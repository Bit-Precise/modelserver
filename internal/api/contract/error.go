package contract

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

var configureErrorsOnce sync.Once

// ErrorEnvelope matches the management API's existing error wire format.
// HTTPStatus is intentionally omitted from JSON but implements huma.StatusError.
type ErrorEnvelope struct {
	HTTPStatus int         `json:"-"`
	Payload    ErrorDetail `json:"error"`
}

// ErrorDetail is a stable machine-readable management API error.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details any    `json:"details,omitempty"`
}

func (e *ErrorEnvelope) Error() string { return e.Payload.Message }

func (e *ErrorEnvelope) GetStatus() int { return e.HTTPStatus }

// NewError creates an error which Huma serializes using the existing
// {"error": {"code", "message", "details"}} envelope.
func NewError(status int, code, message string, details any) *ErrorEnvelope {
	return &ErrorEnvelope{
		HTTPStatus: status,
		Payload: ErrorDetail{
			Code:    code,
			Message: message,
			Details: details,
		},
	}
}

// WriteError is used by operation middleware, which runs before Huma's typed
// handler and therefore cannot return a StatusError in the normal way.
func WriteError(ctx huma.Context, apiError *ErrorEnvelope) {
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(apiError.HTTPStatus)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(apiError)
}

func configureErrors() {
	configureErrorsOnce.Do(func() {
		huma.NewError = func(status int, message string, errs ...error) huma.StatusError {
			var details any
			if len(errs) > 0 {
				details = errs
			}
			return NewError(status, errorCodeForStatus(status), message, details)
		}
		huma.NewErrorWithContext = func(_ huma.Context, status int, message string, errs ...error) huma.StatusError {
			return huma.NewError(status, message, errs...)
		}
	})
}

func errorCodeForStatus(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return "bad_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limited"
	default:
		if status >= 500 {
			return "internal"
		}
		return "request_failed"
	}
}
