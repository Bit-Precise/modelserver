package types

import "testing"

func TestIsValidRequestKind_KnownConstants(t *testing.T) {
	for _, k := range AllRequestKinds {
		if !IsValidRequestKind(k) {
			t.Errorf("IsValidRequestKind(%q) = false, want true", k)
		}
	}
}

func TestIsValidRequestKind_RejectsUnknown(t *testing.T) {
	for _, k := range []string{"", "anthropic_complete", "OPENAI_RESPONSES", "openai-responses"} {
		if IsValidRequestKind(k) {
			t.Errorf("IsValidRequestKind(%q) = true, want false", k)
		}
	}
}

func TestAllRequestKinds_ContainsExactlyEight(t *testing.T) {
	if got := len(AllRequestKinds); got != 8 {
		t.Errorf("len(AllRequestKinds) = %d, want 8", got)
	}
}

func TestIsValidRequestKind_OpenAIResponsesCompact(t *testing.T) {
	if !IsValidRequestKind(KindOpenAIResponsesCompact) {
		t.Errorf("IsValidRequestKind(%q) = false, want true", KindOpenAIResponsesCompact)
	}
}
