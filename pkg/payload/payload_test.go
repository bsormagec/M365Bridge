package payload

import (
	"net/url"
	"strings"
	"testing"
)

func TestBuildURLUsesEduStarterRoute(t *testing.T) {
	raw, _, _, err := BuildURL(
		"token",
		"0123456789abcdef0123456789abcdef",
		"",
		"user",
		"tenant",
	)
	if err != nil {
		t.Fatalf("BuildURL returned error: %v", err)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("BuildURL returned invalid URL: %v", err)
	}
	query := parsed.Query()
	if got := query.Get("licenseType"); got != "Starter" {
		t.Fatalf("licenseType = %q, want Starter", got)
	}
	if got := query.Get("isEdu"); got != "true" {
		t.Fatalf("isEdu = %q, want true", got)
	}
	if got := query.Get("scenario"); got != "OfficeWebIncludedCopilot" {
		t.Fatalf("scenario = %q, want OfficeWebIncludedCopilot", got)
	}
}

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
