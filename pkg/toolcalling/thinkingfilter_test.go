package toolcalling

import (
	"strings"
	"testing"
)

func TestThinkingStreamFilterKeepsGenuineReasoningDropsTransport(t *testing.T) {
	var f ThinkingStreamFilter
	chunks := []string{
		"I should inspect the repository first.\n",
		"Interpreting payload for the request.\n",
		"The project is a Go module, so I will read go.mod.\n",
		"Simulating assistant response in the required Anthropic JSON format.\n",
	}
	var out string
	for _, c := range chunks {
		out += f.Feed(c)
	}
	out += f.Flush()

	if !strings.Contains(out, "inspect the repository") || !strings.Contains(out, "read go.mod") {
		t.Fatalf("genuine reasoning was dropped: %q", out)
	}
	for _, forbidden := range []string{"payload", "Simulating", "JSON format"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("transport line %q leaked: %q", forbidden, out)
		}
	}
}

func TestThinkingStreamFilterStripsFencedEnvelopeAcrossChunks(t *testing.T) {
	var f ThinkingStreamFilter
	chunks := []string{
		"Here is my plan.\n```",
		"json\n{\"content\":[{\"type\":\"tool_use\"",
		"}]}\n```\nDone planning the read.\n",
	}
	var out string
	for _, c := range chunks {
		out += f.Feed(c)
	}
	out += f.Flush()

	if strings.Contains(out, "tool_use") || strings.Contains(out, "content") || strings.Contains(out, "```") {
		t.Fatalf("fenced envelope leaked: %q", out)
	}
	if !strings.Contains(out, "Here is my plan") || !strings.Contains(out, "Done planning") {
		t.Fatalf("surrounding reasoning was dropped: %q", out)
	}
}

func TestThinkingStreamFilterHoldsPartialLineUntilComplete(t *testing.T) {
	var f ThinkingStreamFilter
	if got := f.Feed("The repo uses "); got != "" {
		t.Fatalf("partial line emitted early: %q", got)
	}
	got := f.Feed("Go modules.\n")
	if strings.TrimSpace(got) != "The repo uses Go modules." {
		t.Fatalf("completed line = %q", got)
	}
}

func TestThinkingStreamFilterFlushEmitsKeptRemainder(t *testing.T) {
	var f ThinkingStreamFilter
	f.Feed("Reading the config file")
	if got := f.Flush(); strings.TrimSpace(got) != "Reading the config file" {
		t.Fatalf("flush remainder = %q", got)
	}
}
