package servers

import (
	"strings"
	"testing"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/toolcalling"
)

func TestToolNamesFromDefsSupportsProviderShapes(t *testing.T) {
	tools := []toolcalling.ToolDef{
		{Type: "function", Function: toolcalling.ToolDefFunc{Name: "openai_tool"}},
		{Name: "anthropic_tool"},
	}

	names := toolNamesFromDefs(tools)
	if len(names) != 2 || names[0] != "openai_tool" || names[1] != "anthropic_tool" {
		t.Fatalf("toolNamesFromDefs() = %v, want both provider tool names", names)
	}
}

func TestResponsesInputToMessagesPreservesFunctionCallOutputID(t *testing.T) {
	input := []any{
		map[string]any{
			"type":    "function_call_output",
			"call_id": "call_123",
			"output":  "result text",
		},
	}

	messages := responsesInputToMessages(input)
	if len(messages) != 1 {
		t.Fatalf("responsesInputToMessages() returned %d messages, want 1", len(messages))
	}
	if messages[0].Role != "tool" {
		t.Fatalf("message role = %q, want tool", messages[0].Role)
	}
	if messages[0].ToolCallID != "call_123" {
		t.Fatalf("tool call ID = %q, want call_123", messages[0].ToolCallID)
	}
	if !strings.Contains(messages[0].Content, "call_id: call_123") || !strings.Contains(messages[0].Content, "result text") {
		t.Fatalf("message content = %q, want call ID and output", messages[0].Content)
	}
}
