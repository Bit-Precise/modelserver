package httplog

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// ReassembleOpenAIChatCompletionsSSE converts OpenAI Chat Completions SSE
// event bytes into the equivalent non-streaming JSON response body. Returns
// (nil, false) if the input contains no recognisable chat.completion.chunk
// events — caller keeps the raw SSE in that case.
//
// Event shape (each `data:` line):
//
//	{"id":"chatcmpl-x", "model":"gpt-5.6-sol", "object":"chat.completion.chunk",
//	 "choices":[{"index":0, "delta":{"role":"assistant"|null, "content":"..."|null,
//	                                  "tool_calls":[{"index":0, "id":"...",
//	                                                 "function":{"name":"...",
//	                                                             "arguments":"..."}}]},
//	             "finish_reason":null|"stop"|"tool_calls"|"length"|...}],
//	 "usage":null|{"prompt_tokens":..., "completion_tokens":..., "total_tokens":...}}
//
// The final chunk carries a non-null `usage` when the client asked for it via
// `stream_options.include_usage`. Terminator marker `data: [DONE]` is skipped.
func ReassembleOpenAIChatCompletionsSSE(sseData []byte) ([]byte, bool) {
	var (
		id, model, systemFingerprint string
		object                       = "chat.completion"
	)
	// Per-choice accumulated state, keyed by index.
	type toolCall struct {
		id       string
		typ      string // usually "function"
		name     string
		argsBuf  strings.Builder
	}
	type choiceState struct {
		role         string
		contentBuf   strings.Builder
		finishReason string
		toolCalls    map[int]*toolCall
	}
	choices := map[int]*choiceState{}
	getChoice := func(idx int) *choiceState {
		c, ok := choices[idx]
		if !ok {
			c = &choiceState{toolCalls: map[int]*toolCall{}}
			choices[idx] = c
		}
		return c
	}
	var usage *chatCompletionsUsagePayload

	scanner := newLineScanner(sseData)
	sawAny := false
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if bytes.Equal(data, []byte("[DONE]")) || len(data) == 0 {
			continue
		}

		var evt chatCompletionsChunkEvent
		if err := json.Unmarshal(data, &evt); err != nil {
			continue
		}
		if evt.Object == "" && len(evt.Choices) == 0 && evt.Usage == nil {
			continue // not a chat.completion.chunk event, skip
		}
		sawAny = true

		if evt.ID != "" {
			id = evt.ID
		}
		if evt.Model != "" {
			model = evt.Model
		}
		if evt.SystemFingerprint != "" {
			systemFingerprint = evt.SystemFingerprint
		}
		if evt.Usage != nil {
			usage = evt.Usage
		}

		for _, ch := range evt.Choices {
			c := getChoice(ch.Index)
			if ch.Delta.Role != "" {
				c.role = ch.Delta.Role
			}
			if ch.Delta.Content != "" {
				c.contentBuf.WriteString(ch.Delta.Content)
			}
			if ch.FinishReason != nil && *ch.FinishReason != "" {
				c.finishReason = *ch.FinishReason
			}
			for _, tcDelta := range ch.Delta.ToolCalls {
				tc := c.toolCalls[tcDelta.Index]
				if tc == nil {
					tc = &toolCall{}
					c.toolCalls[tcDelta.Index] = tc
				}
				if tcDelta.ID != "" {
					tc.id = tcDelta.ID
				}
				if tcDelta.Type != "" {
					tc.typ = tcDelta.Type
				}
				if tcDelta.Function.Name != "" {
					tc.name = tcDelta.Function.Name
				}
				if tcDelta.Function.Arguments != "" {
					tc.argsBuf.WriteString(tcDelta.Function.Arguments)
				}
			}
		}
	}
	if !sawAny {
		return nil, false
	}

	// Sort choice indexes to keep output stable.
	choiceIdxs := make([]int, 0, len(choices))
	for idx := range choices {
		choiceIdxs = append(choiceIdxs, idx)
	}
	sort.Ints(choiceIdxs)

	assembled := map[string]interface{}{
		"id":     id,
		"model":  model,
		"object": object,
	}
	if systemFingerprint != "" {
		assembled["system_fingerprint"] = systemFingerprint
	}
	choicesOut := make([]map[string]interface{}, 0, len(choiceIdxs))
	for _, idx := range choiceIdxs {
		c := choices[idx]
		msg := map[string]interface{}{
			"role":    firstNonEmpty(c.role, "assistant"),
			"content": c.contentBuf.String(),
		}
		if len(c.toolCalls) > 0 {
			tcIdxs := make([]int, 0, len(c.toolCalls))
			for k := range c.toolCalls {
				tcIdxs = append(tcIdxs, k)
			}
			sort.Ints(tcIdxs)
			tcs := make([]map[string]interface{}, 0, len(tcIdxs))
			for _, k := range tcIdxs {
				tc := c.toolCalls[k]
				tcs = append(tcs, map[string]interface{}{
					"id":   tc.id,
					"type": firstNonEmpty(tc.typ, "function"),
					"function": map[string]interface{}{
						"name":      tc.name,
						"arguments": tc.argsBuf.String(),
					},
				})
			}
			msg["tool_calls"] = tcs
		}
		choicesOut = append(choicesOut, map[string]interface{}{
			"index":         idx,
			"message":       msg,
			"finish_reason": c.finishReason,
		})
	}
	assembled["choices"] = choicesOut
	if usage != nil {
		assembled["usage"] = usage
	}
	b, err := json.Marshal(assembled)
	if err != nil {
		return nil, false
	}
	return b, true
}

// ReassembleOpenAIResponsesSSE converts OpenAI Responses API SSE event bytes
// into the equivalent non-streaming Response JSON. Returns (nil, false) when
// the input contains no recognisable response.* events.
//
// Responses API events are named on the `event:` line and carry structured
// JSON on the `data:` line. Key event types we handle:
//
//	response.created                                 → header (id, model, created_at)
//	response.output_item.added                       → new output entry (message/reasoning/function_call/…)
//	response.output_text.delta                       → append text to a message output item's first output_text content
//	response.reasoning_text.delta / .summary_text.delta → append to a reasoning item's text/summary
//	response.function_call_arguments.delta           → append to a function_call item's arguments string
//	response.output_item.done                        → replace our accumulator with the authoritative final item if present
//	response.completed / .incomplete / .failed       → capture usage + final response envelope
//
// The output shape mirrors the non-streaming response body: {id, model, output:[…], usage:{…}, status}.
func ReassembleOpenAIResponsesSSE(sseData []byte) ([]byte, bool) {
	type outputItem struct {
		// raw carries the last-seen full JSON for this item (from
		// output_item.added and later overwritten by output_item.done).
		raw map[string]interface{}
		// textBuf accumulates output_text deltas for the first
		// "output_text" content entry within a message item.
		textBuf strings.Builder
		// reasoningTextBuf and reasoningSummaryBuf accumulate reasoning deltas.
		reasoningTextBuf    strings.Builder
		reasoningSummaryBuf strings.Builder
		// argsBuf accumulates function_call arguments deltas.
		argsBuf strings.Builder
		typ     string
	}
	items := map[int]*outputItem{}
	order := []int{}
	get := func(idx int) *outputItem {
		it, ok := items[idx]
		if !ok {
			it = &outputItem{}
			items[idx] = it
			order = append(order, idx)
		}
		return it
	}

	var (
		respID, model, status string
		usage                 map[string]interface{}
		responseEnvelope      map[string]interface{}
	)

	scanner := newLineScanner(sseData)
	sawAny := false

	var currentEvent string
	for scanner.Scan() {
		line := scanner.Bytes()
		// SSE record separator = blank line; reset event name.
		if len(bytes.TrimSpace(line)) == 0 {
			currentEvent = ""
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			currentEvent = string(bytes.TrimSpace(bytes.TrimPrefix(line, []byte("event:"))))
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
		if len(data) == 0 || bytes.Equal(data, []byte("[DONE]")) {
			continue
		}

		// Some deployments omit the `event:` line; then the type lives
		// inside the JSON as `"type"`. Peek for that.
		if currentEvent == "" {
			var peek struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(data, &peek)
			currentEvent = peek.Type
		}
		if currentEvent == "" {
			continue
		}

		// Only flip sawAny for events we actually recognise. This lets us
		// return ok=false when handed a non-Responses SSE stream (e.g.
		// Anthropic's message_start / content_block_delta).
		switch currentEvent {
		case "response.created", "response.in_progress",
			"response.output_item.added", "response.output_item.done",
			"response.output_text.delta",
			"response.reasoning_text.delta", "response.reasoning_summary_text.delta",
			"response.function_call_arguments.delta",
			"response.completed", "response.incomplete", "response.failed":
			sawAny = true
		}

		switch currentEvent {
		case "response.created", "response.in_progress":
			var evt struct {
				Response map[string]interface{} `json:"response"`
			}
			if err := json.Unmarshal(data, &evt); err == nil && evt.Response != nil {
				if v, ok := evt.Response["id"].(string); ok {
					respID = v
				}
				if v, ok := evt.Response["model"].(string); ok {
					model = v
				}
				responseEnvelope = evt.Response
			}
		case "response.output_item.added":
			var evt struct {
				OutputIndex int                    `json:"output_index"`
				Item        map[string]interface{} `json:"item"`
			}
			if err := json.Unmarshal(data, &evt); err == nil && evt.Item != nil {
				it := get(evt.OutputIndex)
				it.raw = evt.Item
				if t, ok := evt.Item["type"].(string); ok {
					it.typ = t
				}
			}
		case "response.output_text.delta":
			var evt struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal(data, &evt); err == nil {
				get(evt.OutputIndex).textBuf.WriteString(evt.Delta)
			}
		case "response.reasoning_text.delta":
			var evt struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal(data, &evt); err == nil {
				get(evt.OutputIndex).reasoningTextBuf.WriteString(evt.Delta)
			}
		case "response.reasoning_summary_text.delta":
			var evt struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal(data, &evt); err == nil {
				get(evt.OutputIndex).reasoningSummaryBuf.WriteString(evt.Delta)
			}
		case "response.function_call_arguments.delta":
			var evt struct {
				OutputIndex int    `json:"output_index"`
				Delta       string `json:"delta"`
			}
			if err := json.Unmarshal(data, &evt); err == nil {
				get(evt.OutputIndex).argsBuf.WriteString(evt.Delta)
			}
		case "response.output_item.done":
			var evt struct {
				OutputIndex int                    `json:"output_index"`
				Item        map[string]interface{} `json:"item"`
			}
			if err := json.Unmarshal(data, &evt); err == nil && evt.Item != nil {
				it := get(evt.OutputIndex)
				// output_item.done carries the authoritative final item —
				// trust it over our accumulators when present.
				it.raw = evt.Item
				if t, ok := evt.Item["type"].(string); ok {
					it.typ = t
				}
				// Zero out the accumulators to signal "use raw as-is".
				it.textBuf.Reset()
				it.reasoningTextBuf.Reset()
				it.reasoningSummaryBuf.Reset()
				it.argsBuf.Reset()
			}
		case "response.completed", "response.incomplete", "response.failed":
			var evt struct {
				Response map[string]interface{} `json:"response"`
			}
			if err := json.Unmarshal(data, &evt); err == nil && evt.Response != nil {
				if v, ok := evt.Response["id"].(string); ok {
					respID = v
				}
				if v, ok := evt.Response["model"].(string); ok {
					model = v
				}
				if v, ok := evt.Response["status"].(string); ok {
					status = v
				}
				if u, ok := evt.Response["usage"].(map[string]interface{}); ok {
					usage = u
				}
				// Terminal envelope: prefer its `output` array wholesale when
				// present — the server has already assembled the definitive
				// list of items and their content.
				if out, ok := evt.Response["output"].([]interface{}); ok && len(out) > 0 {
					items = map[int]*outputItem{}
					order = order[:0]
					for i, raw := range out {
						if m, ok := raw.(map[string]interface{}); ok {
							it := get(i)
							it.raw = m
							if t, ok := m["type"].(string); ok {
								it.typ = t
							}
						}
					}
				}
				responseEnvelope = evt.Response
			}
		}
		currentEvent = ""
	}
	if !sawAny {
		return nil, false
	}

	// Materialise the output array in original order.
	sort.Ints(order)
	outputArr := make([]map[string]interface{}, 0, len(order))
	for _, idx := range order {
		it := items[idx]
		if it.raw == nil {
			// Skeleton item type-only (unusual but possible)
			outputArr = append(outputArr, map[string]interface{}{"type": it.typ})
			continue
		}
		merged := cloneMap(it.raw)
		switch it.typ {
		case "message":
			if it.textBuf.Len() > 0 {
				// Find or create the first output_text content entry.
				content, _ := merged["content"].([]interface{})
				text := it.textBuf.String()
				injected := false
				for i, c := range content {
					if cm, ok := c.(map[string]interface{}); ok {
						if t, _ := cm["type"].(string); t == "output_text" {
							cm["text"] = text
							content[i] = cm
							injected = true
							break
						}
					}
				}
				if !injected {
					content = append(content, map[string]interface{}{
						"type": "output_text",
						"text": text,
					})
				}
				merged["content"] = content
			}
		case "reasoning":
			if it.reasoningTextBuf.Len() > 0 {
				merged["text"] = it.reasoningTextBuf.String()
			}
			if it.reasoningSummaryBuf.Len() > 0 {
				merged["summary"] = []interface{}{
					map[string]interface{}{
						"type": "summary_text",
						"text": it.reasoningSummaryBuf.String(),
					},
				}
			}
		case "function_call":
			if it.argsBuf.Len() > 0 {
				merged["arguments"] = it.argsBuf.String()
			}
		}
		outputArr = append(outputArr, merged)
	}

	assembled := map[string]interface{}{}
	// Start from the terminal response envelope if we have it — preserves
	// fields like created_at / object / model_settings the client set.
	if responseEnvelope != nil {
		assembled = cloneMap(responseEnvelope)
	}
	if respID != "" {
		assembled["id"] = respID
	}
	if model != "" {
		assembled["model"] = model
	}
	if status != "" {
		assembled["status"] = status
	}
	if usage != nil {
		assembled["usage"] = usage
	}
	assembled["output"] = outputArr

	b, err := json.Marshal(assembled)
	if err != nil {
		return nil, false
	}
	return b, true
}

// ---- Helpers ----

type chatCompletionsUsagePayload struct {
	PromptTokens     int64                  `json:"prompt_tokens"`
	CompletionTokens int64                  `json:"completion_tokens"`
	TotalTokens      int64                  `json:"total_tokens"`
	// Preserve nested details when present (e.g. reasoning_tokens, cached_tokens)
	PromptTokensDetails     map[string]interface{} `json:"prompt_tokens_details,omitempty"`
	CompletionTokensDetails map[string]interface{} `json:"completion_tokens_details,omitempty"`
}

type chatCompletionsChunkEvent struct {
	ID                string                       `json:"id"`
	Model             string                       `json:"model"`
	Object            string                       `json:"object"`
	SystemFingerprint string                       `json:"system_fingerprint,omitempty"`
	Choices           []chatCompletionsChunkChoice `json:"choices"`
	Usage             *chatCompletionsUsagePayload `json:"usage,omitempty"`
}

type chatCompletionsChunkChoice struct {
	Index        int                       `json:"index"`
	Delta        chatCompletionsChunkDelta `json:"delta"`
	FinishReason *string                   `json:"finish_reason"`
}

type chatCompletionsChunkDelta struct {
	Role      string                     `json:"role,omitempty"`
	Content   string                     `json:"content,omitempty"`
	ToolCalls []chatCompletionsToolCall  `json:"tool_calls,omitempty"`
}

type chatCompletionsToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func cloneMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
