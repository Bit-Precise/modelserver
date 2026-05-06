package types

const (
	KindAnthropicMessages       = "anthropic_messages"
	KindAnthropicCountTokens    = "anthropic_count_tokens"
	KindOpenAIChatCompletions   = "openai_chat_completions"
	KindOpenAIResponses         = "openai_responses"
	KindOpenAIResponsesCompact  = "openai_responses_compact"
	KindOpenAIImagesGenerations = "openai_images_generations"
	KindOpenAIImagesEdits       = "openai_images_edits"
	KindGoogleGenerateContent   = "google_generate_content"
)

var AllRequestKinds = []string{
	KindAnthropicMessages,
	KindAnthropicCountTokens,
	KindOpenAIChatCompletions,
	KindOpenAIResponses,
	KindOpenAIResponsesCompact,
	KindOpenAIImagesGenerations,
	KindOpenAIImagesEdits,
	KindGoogleGenerateContent,
}

func IsValidRequestKind(s string) bool {
	for _, k := range AllRequestKinds {
		if k == s {
			return true
		}
	}
	return false
}
