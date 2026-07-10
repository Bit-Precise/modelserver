package httplog

import "net/http"

// Record holds all data to be logged for a single client request / upstream response pair.
type Record struct {
	RequestID       string      `json:"request_id"`
	ProjectID       string      `json:"project_id"`
	// RequestKind is one of types.Kind* (anthropic_messages, openai_responses,
	// openai_chat_completions, ...). Drives SSE reassemble dispatch — the
	// Anthropic vs OpenAI-Responses vs OpenAI-ChatCompletions SSE event
	// schemas are all different, so the correct reassembler is picked by
	// kind. Non-streaming requests ignore this field.
	RequestKind string `json:"-"`
	RequestHeaders  http.Header `json:"request_headers"`
	RequestBody     []byte      `json:"request_body"`
	ResponseHeaders http.Header `json:"response_headers"`
	ResponseBody    []byte      `json:"response_body"`
	ResponseStatus  int         `json:"response_status_code"`
	Streaming       bool        `json:"-"`
	Truncated       bool        `json:"truncated,omitempty"`
}
