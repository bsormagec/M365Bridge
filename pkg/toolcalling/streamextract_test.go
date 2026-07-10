package toolcalling

import (
	"strings"
	"testing"
)

func TestContentStreamExtractorCommitsEscapedContentAcrossChunks(t *testing.T) {
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
	streamed += extractor.Commit(nil)

	want := "hello\nworld \\ path \"quoted\" ☺"
	if streamed != want {
		t.Fatalf("streamed content = %q, want %q", streamed, want)
	}
	if extractor.Text() != want {
		t.Fatalf("extractor text = %q, want %q", extractor.Text(), want)
	}
}

func TestContentStreamExtractorCommitsSplitSurrogatePair(t *testing.T) {
	var extractor ContentStreamExtractor

	first := extractor.Feed(
		`{"choices":[{"message":{"content":"emoji \uD83D`,
	)
	second := extractor.Feed(`\uDE80 done"}}]}`)
	committed := extractor.Commit(nil)

	if first != "" {
		t.Fatalf("first delta = %q", first)
	}
	if second != "" {
		t.Fatalf("second delta = %q", second)
	}
	if committed != "emoji 🚀 done" {
		t.Fatalf("committed content = %q", committed)
	}
	if extractor.Text() != "emoji 🚀 done" {
		t.Fatalf("full text = %q", extractor.Text())
	}
}

func TestContentStreamExtractorIgnoresRequestContentBeforeChoices(t *testing.T) {
	var extractor ContentStreamExtractor
	payload := `{"input":[{"content":"do not stream me"}],"choices":[{"message":{"content":"stream me"}}]}`

	if got := extractor.Feed(payload); got != "" {
		t.Fatalf("uncommitted content was emitted: %q", got)
	}
	if got := extractor.Commit(nil); got != "stream me" {
		t.Fatalf("committed wrong content: %q", got)
	}
}

func TestContentStreamExtractorIgnoresNullToolCallContent(t *testing.T) {
	var extractor ContentStreamExtractor
	payload := `{"choices":[{"message":{"content":null,"tool_calls":[{"function":{"name":"read_nonce"}}]}}]}`

	if got := extractor.Feed(payload); got != "" {
		t.Fatalf("tool-call payload emitted content: %q", got)
	}
	if got := extractor.Commit([]string{"read_nonce"}); got != "" {
		t.Fatalf("tool-call payload committed content: %q", got)
	}
	if extractor.Text() != "" {
		t.Fatalf("tool-call payload retained content: %q", extractor.Text())
	}
}

func TestContentStreamExtractorCommitsFinalParserCandidate(t *testing.T) {
	var extractor ContentStreamExtractor
	chunks := []string{
		"```json\n" +
			`{"choices":[{"message":{"role":"assistant","content":"wrong early answer"},"finish_reason":"stop"}]}` +
			"\n```\n",
		"```json\n" +
			`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","name":"read_nonce","arguments":"{}"}]},"finish_reason":"tool_calls"}]}` +
			"\n```",
	}

	var streamed string
	for _, chunk := range chunks {
		streamed += extractor.Feed(chunk)
	}
	final := ParseSimulatedResponse(strings.Join(chunks, ""), []string{"read_nonce"})
	streamed += extractor.Commit([]string{"read_nonce"})

	if streamed != final.Content {
		t.Fatalf("streamed content = %q, final parser content = %q", streamed, final.Content)
	}
}
