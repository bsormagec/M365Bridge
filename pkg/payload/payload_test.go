package payload

import (
	"strings"
	"testing"
)

func TestConversationTextForM365IncludesClientHistoryWhenRequested(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Read nonce.txt."},
		{Role: "assistant", Content: "Tool call: read_nonce({})"},
		{Role: "user", Content: "Authoritative tool result: NONCE-EXACT"},
		{Role: "user", Content: "Return the exact tool result now."},
	}

	got := conversationTextForM365(messages, true)

	for _, expected := range []string{
		"CLIENT-PROVIDED CONVERSATION HISTORY",
		"Read nonce.txt.",
		"Tool call: read_nonce({})",
		"Authoritative tool result: NONCE-EXACT",
		"CURRENT USER MESSAGE",
		"Return the exact tool result now.",
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("flattened conversation lost %q:\n%s", expected, got)
		}
	}
}

func TestConversationTextForM365KeepsOnlyCurrentMessageForStickyConversation(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Earlier user message"},
		{Role: "assistant", Content: "Earlier assistant message"},
		{Role: "user", Content: "Current request"},
	}

	got := conversationTextForM365(messages, false)

	if got != "Current request" {
		t.Fatalf("sticky conversation text = %q, want current request only", got)
	}
}
