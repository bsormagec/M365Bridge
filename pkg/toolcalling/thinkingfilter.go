package toolcalling

import "strings"

// ThinkingStreamFilter incrementally strips the simulated transport envelope
// from a streamed thinking channel so cleaned reasoning can be streamed live.
// It removes fenced code blocks (the ```json envelope the model emits) and
// lines that describe producing that envelope rather than genuine task
// reasoning. Only complete, kept lines are emitted; a partial trailing line is
// held until it completes or Flush is called.
type ThinkingStreamFilter struct {
	inFence bool
	pending strings.Builder
}

// Feed consumes a thinking chunk and returns cleaned, newline-terminated lines
// ready to stream. The return value may be empty when every completed line was
// dropped or no line boundary was reached yet.
func (f *ThinkingStreamFilter) Feed(chunk string) string {
	f.pending.WriteString(chunk)
	buffered := f.pending.String()
	var out strings.Builder
	for {
		idx := strings.IndexByte(buffered, '\n')
		if idx < 0 {
			break
		}
		line := buffered[:idx]
		buffered = buffered[idx+1:]
		if kept, ok := f.consumeLine(line); ok {
			out.WriteString(kept)
			out.WriteByte('\n')
		}
	}
	f.pending.Reset()
	f.pending.WriteString(buffered)
	return out.String()
}

// Flush returns the cleaned remainder held after the last newline, if kept.
func (f *ThinkingStreamFilter) Flush() string {
	line := f.pending.String()
	f.pending.Reset()
	if kept, ok := f.consumeLine(line); ok {
		return kept
	}
	return ""
}

// consumeLine updates fence state and reports whether the line survives
// filtering. Fence markers, fenced content, blank lines, and transport
// meta-prose are dropped.
func (f *ThinkingStreamFilter) consumeLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "```") {
		f.inFence = !f.inFence
		return "", false
	}
	if f.inFence || trimmed == "" {
		return "", false
	}
	if isTransportThinkingLine(trimmed) {
		return "", false
	}
	return line, true
}

// isTransportThinkingLine reports whether a thinking line describes producing
// the simulated request/response envelope rather than genuine task reasoning.
func isTransportThinkingLine(line string) bool {
	lower := strings.ToLower(line)
	for _, marker := range transportThinkingMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// transportThinkingMarkers are lowercase substrings that identify envelope /
// transport reasoning the model leaks into its thinking under simulated mode.
var transportThinkingMarkers = []string{
	"json format", "json object", "json block", "json code", "valid json",
	"json response", "json envelope", "single json", "in json", "as json",
	"envelope", "payload", "tool_use", "tool_call", "tool call",
	"stop_reason", "finish_reason", "simulat",
	"anthropic format", "anthropic messages", "anthropic response",
	"openai format", "chat.completion", "chat completion",
	"response object", "response format", "required format", "exact format",
	"same format", "code block", "assistant response", "assistant message",
	"content array", "content block",
}
