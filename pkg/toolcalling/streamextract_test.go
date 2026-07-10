package toolcalling

import "testing"

func TestContentStreamExtractorStreamsEscapedContentAcrossChunks(t *testing.T) {
	var extractor ContentStreamExtractor
	chunks := []string{
		"noise```json\n{\"choices\":[{\"message\":{\"role\":\"assistant\",",
		"\"content\":\"hello\\nwor",
		"ld \\\\ path \\\"quo",
		"ted\\\" \\u263A\"},\"finish_reason\":\"stop\"}]}\n```",
	}

	var streamed string
	for _, chunk := range chunks {
		streamed += extractor.Feed(chunk)
	}

	want := "hello\nworld \\ path \"quoted\" ☺"
	if streamed != want {
		t.Fatalf("streamed content = %q, want %q", streamed, want)
	}
	if extractor.Text() != want {
		t.Fatalf("extractor text = %q, want %q", extractor.Text(), want)
	}
}

func TestContentStreamExtractorHandlesSplitSurrogatePair(t *testing.T) {
	var extractor ContentStreamExtractor

	first := extractor.Feed(
		`{"choices":[{"message":{"content":"emoji \uD83D`,
	)
	second := extractor.Feed(`\uDE80 done"}}]}`)

	if first != "emoji " {
		t.Fatalf("first delta = %q", first)
	}
	if second != "🚀 done" {
		t.Fatalf("second delta = %q", second)
	}
	if extractor.Text() != "emoji 🚀 done" {
		t.Fatalf("full text = %q", extractor.Text())
	}
}

func TestContentStreamExtractorIgnoresRequestContentBeforeChoices(t *testing.T) {
	var extractor ContentStreamExtractor
	payload := `{"input":[{"content":"do not stream me"}],"choices":[{"message":{"content":"stream me"}}]}`

	if got := extractor.Feed(payload); got != "stream me" {
		t.Fatalf("extracted wrong content: %q", got)
	}
}

func TestContentStreamExtractorIgnoresNullToolCallContent(t *testing.T) {
	var extractor ContentStreamExtractor
	payload := `{"choices":[{"message":{"content":null,"tool_calls":[{"function":{"name":"read_nonce"}}]}}]}`

	if got := extractor.Feed(payload); got != "" {
		t.Fatalf("tool-call payload emitted content: %q", got)
	}
	if extractor.Text() != "" {
		t.Fatalf("tool-call payload retained content: %q", extractor.Text())
	}
}
