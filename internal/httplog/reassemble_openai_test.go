package httplog

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestReassembleChatCompletions_TextOnly asserts we concatenate text deltas
// across chunks and preserve id/model/usage from the terminal chunk.
func TestReassembleChatCompletions_TextOnly(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"chatcmpl-1","model":"gpt-5.6-sol","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"gpt-5.6-sol","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"gpt-5.6-sol","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":", world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-1","model":"gpt-5.6-sol","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	out, ok := ReassembleOpenAIChatCompletionsSSE([]byte(sse))
	if !ok {
		t.Fatal("reassemble returned ok=false on valid chat.completion.chunk SSE")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("output is not valid JSON: %v; body=%s", err, out)
	}
	if got["id"] != "chatcmpl-1" {
		t.Errorf("id = %v, want chatcmpl-1", got["id"])
	}
	if got["model"] != "gpt-5.6-sol" {
		t.Errorf("model = %v, want gpt-5.6-sol", got["model"])
	}
	choices, _ := got["choices"].([]interface{})
	if len(choices) != 1 {
		t.Fatalf("choices length = %d, want 1", len(choices))
	}
	c0 := choices[0].(map[string]interface{})
	msg := c0["message"].(map[string]interface{})
	if msg["role"] != "assistant" {
		t.Errorf("role = %v, want assistant", msg["role"])
	}
	if msg["content"] != "Hello, world" {
		t.Errorf("content = %q, want %q", msg["content"], "Hello, world")
	}
	if c0["finish_reason"] != "stop" {
		t.Errorf("finish_reason = %v, want stop", c0["finish_reason"])
	}
	usage, _ := got["usage"].(map[string]interface{})
	if usage == nil {
		t.Fatal("usage missing")
	}
	if p, _ := usage["prompt_tokens"].(float64); p != 12 {
		t.Errorf("prompt_tokens = %v, want 12", usage["prompt_tokens"])
	}
}

// TestReassembleChatCompletions_ToolCalls asserts we accumulate tool_call
// argument fragments across chunks and preserve id/name/type.
func TestReassembleChatCompletions_ToolCalls(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c-1","model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"c-1","model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_A","type":"function","function":{"name":"get_weather","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"c-1","model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\""}}]},"finish_reason":null}]}`,
		`data: {"id":"c-1","model":"gpt-5.6-sol","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"Beijing\"}"}}]},"finish_reason":null}]}`,
		`data: {"id":"c-1","model":"gpt-5.6-sol","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		"",
	}, "\n\n")

	out, ok := ReassembleOpenAIChatCompletionsSSE([]byte(sse))
	if !ok {
		t.Fatal("ok=false")
	}
	var got map[string]interface{}
	json.Unmarshal(out, &got)
	choices := got["choices"].([]interface{})
	msg := choices[0].(map[string]interface{})["message"].(map[string]interface{})
	tcs, _ := msg["tool_calls"].([]interface{})
	if len(tcs) != 1 {
		t.Fatalf("tool_calls length = %d, want 1", len(tcs))
	}
	tc := tcs[0].(map[string]interface{})
	if tc["id"] != "call_A" {
		t.Errorf("tool_call.id = %v, want call_A", tc["id"])
	}
	if tc["type"] != "function" {
		t.Errorf("tool_call.type = %v, want function", tc["type"])
	}
	fn := tc["function"].(map[string]interface{})
	if fn["name"] != "get_weather" {
		t.Errorf("function.name = %v, want get_weather", fn["name"])
	}
	if fn["arguments"] != `{"city":"Beijing"}` {
		t.Errorf("function.arguments = %q, want %q", fn["arguments"], `{"city":"Beijing"}`)
	}
	if choices[0].(map[string]interface{})["finish_reason"] != "tool_calls" {
		t.Errorf("finish_reason wrong: %v", choices[0])
	}
}

// TestReassembleChatCompletions_NoEvents returns ok=false when SSE input is
// empty or contains no chat.completion.chunk events (e.g. Anthropic SSE fed
// to it by accident).
func TestReassembleChatCompletions_NoEvents(t *testing.T) {
	for _, in := range []string{
		"",
		"data: [DONE]\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{}}\n\n",
	} {
		_, ok := ReassembleOpenAIChatCompletionsSSE([]byte(in))
		if ok {
			t.Errorf("wanted ok=false for %q", in)
		}
	}
}

// TestReassembleResponses_TextOnly asserts we accumulate output_text deltas
// on the message item and pick up usage from response.completed.
func TestReassembleResponses_TextOnly(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.created",
		`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.6-sol","status":"in_progress"}}`,
		"",
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"delta":", world"}`,
		"",
		"event: response.completed",
		`data: {"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello, world"}]}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15}}}`,
		"",
	}, "\n")

	out, ok := ReassembleOpenAIResponsesSSE([]byte(sse))
	if !ok {
		t.Fatal("ok=false")
	}
	var got map[string]interface{}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("bad JSON: %v; body=%s", err, out)
	}
	if got["id"] != "resp_1" {
		t.Errorf("id = %v, want resp_1", got["id"])
	}
	if got["model"] != "gpt-5.6-sol" {
		t.Errorf("model = %v, want gpt-5.6-sol", got["model"])
	}
	if got["status"] != "completed" {
		t.Errorf("status = %v, want completed", got["status"])
	}
	outputs, _ := got["output"].([]interface{})
	if len(outputs) != 1 {
		t.Fatalf("output length = %d, want 1", len(outputs))
	}
	msg := outputs[0].(map[string]interface{})
	if msg["type"] != "message" {
		t.Errorf("output[0].type = %v, want message", msg["type"])
	}
	content := msg["content"].([]interface{})
	if len(content) != 1 {
		t.Fatalf("message content length = %d, want 1", len(content))
	}
	first := content[0].(map[string]interface{})
	if first["type"] != "output_text" {
		t.Errorf("content[0].type = %v, want output_text", first["type"])
	}
	if first["text"] != "Hello, world" {
		t.Errorf("content[0].text = %q, want %q", first["text"], "Hello, world")
	}
	usage := got["usage"].(map[string]interface{})
	if v, _ := usage["input_tokens"].(float64); v != 12 {
		t.Errorf("usage.input_tokens = %v, want 12", usage["input_tokens"])
	}
}

// TestReassembleResponses_WithoutTerminalEnvelope covers the case where the
// stream is truncated before response.completed — our accumulator's own
// text builder should still surface in the output.
func TestReassembleResponses_WithoutTerminalEnvelope(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"message","role":"assistant","content":[]}}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"partial "}`,
		"",
		"event: response.output_text.delta",
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"answer"}`,
		"",
	}, "\n")
	out, ok := ReassembleOpenAIResponsesSSE([]byte(sse))
	if !ok {
		t.Fatal("ok=false")
	}
	var got map[string]interface{}
	json.Unmarshal(out, &got)
	outputs := got["output"].([]interface{})
	msg := outputs[0].(map[string]interface{})
	content := msg["content"].([]interface{})
	first := content[0].(map[string]interface{})
	if first["text"] != "partial answer" {
		t.Errorf("text = %q, want %q", first["text"], "partial answer")
	}
}

// TestReassembleResponses_FunctionCall covers arguments-delta accumulation.
func TestReassembleResponses_FunctionCall(t *testing.T) {
	sse := strings.Join([]string{
		"event: response.output_item.added",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"get_weather","call_id":"call_A"}}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":\""}`,
		"",
		"event: response.function_call_arguments.delta",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"Beijing\"}"}`,
		"",
	}, "\n")
	out, ok := ReassembleOpenAIResponsesSSE([]byte(sse))
	if !ok {
		t.Fatal("ok=false")
	}
	var got map[string]interface{}
	json.Unmarshal(out, &got)
	outputs := got["output"].([]interface{})
	fc := outputs[0].(map[string]interface{})
	if fc["type"] != "function_call" {
		t.Errorf("type = %v, want function_call", fc["type"])
	}
	if fc["arguments"] != `{"city":"Beijing"}` {
		t.Errorf("arguments = %q, want %q", fc["arguments"], `{"city":"Beijing"}`)
	}
	if fc["name"] != "get_weather" {
		t.Errorf("name = %v, want get_weather", fc["name"])
	}
}

// TestReassembleResponses_NoEvents returns ok=false when no response.*
// events are found (e.g. someone hands Anthropic SSE to this function).
func TestReassembleResponses_NoEvents(t *testing.T) {
	for _, in := range []string{
		"",
		"data: [DONE]\n\n",
		"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
	} {
		_, ok := ReassembleOpenAIResponsesSSE([]byte(in))
		if ok {
			t.Errorf("wanted ok=false for %q", in)
		}
	}
}
