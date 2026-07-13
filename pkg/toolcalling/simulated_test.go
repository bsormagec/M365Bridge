package toolcalling

import (
	"strings"
	"testing"
)

func TestBuildSimulatedPromptResponsesDescribesResponsesPayload(t *testing.T) {
	requestJSON := `{"input":"hello","instructions":"be concise","tools":[],"tool_choice":"auto"}`

	prompt := BuildSimulatedPromptResponses(requestJSON, true, "auto")

	for _, want := range []string{
		"OpenAI Responses API",
		"POST /v1/responses",
		`"input"`,
		`"instructions"`,
		`"tools"`,
		`"tool_choice"`,
		"tool_search_output",
		requestJSON,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Responses prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "entire request for the OpenAI chat.completions format") {
		t.Fatalf("Responses prompt incorrectly describes the request as chat.completions:\n%s", prompt)
	}
}

func TestBuildSimulatedPromptResponsesKeepsChatCompletionResultEnvelope(t *testing.T) {
	prompt := BuildSimulatedPromptResponses(`{"input":"hello"}`, true, "required")

	for _, want := range []string{
		"choices[0].message.tool_calls",
		"choices[0].message.content",
		`choices[0].finish_reason`,
		"function.arguments must be a JSON string",
		"requires at least one tool call",
		"namespace",
		"brief user-facing progress update",
		"must not be null",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Responses prompt missing result-envelope instruction %q:\n%s", want, prompt)
		}
	}
}

func TestExistingSimulatedPromptContractsRemainUnchanged(t *testing.T) {
	chatPrompt := BuildSimulatedPrompt(`{"messages":[]}`, true, "auto")
	if !strings.Contains(chatPrompt, "POST /v1/chat/completions") {
		t.Fatalf("Chat Completions prompt lost its endpoint contract:\n%s", chatPrompt)
	}

	anthropicPrompt := BuildSimulatedPromptAnthropic(`{"messages":[]}`, true, "auto")
	if !strings.Contains(anthropicPrompt, "POST /v1/messages") {
		t.Fatalf("Anthropic prompt lost its endpoint contract:\n%s", anthropicPrompt)
	}
	if strings.Contains(anthropicPrompt, "POST /v1/responses") {
		t.Fatalf("Anthropic prompt was changed to Responses semantics:\n%s", anthropicPrompt)
	}
}

func TestParseSimulatedResponseDropsInventedFlatToolAndKeepsSafeContent(t *testing.T) {
	raw := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "Safe plain response",
				"tool_calls": [{
					"id": "call_invented",
					"name": "code_interpreter",
					"arguments": "{}"
				}]
			},
			"finish_reason": "tool_calls"
		}]
	}`

	result := ParseSimulatedResponse(raw, []string{"safe_tool"}, nil)

	if len(result.ToolCalls) != 0 {
		t.Fatalf("invented tool calls were not filtered: %#v", result.ToolCalls)
	}
	if result.Content != "Safe plain response" {
		t.Fatalf("safe content = %q, want %q", result.Content, "Safe plain response")
	}
	if result.FinishReason != "stop" {
		t.Fatalf("finish reason = %q, want stop after filtering all tool calls", result.FinishReason)
	}
}

func TestParseSimulatedResponseAnthropicDropsInventedToolAndKeepsSafeText(t *testing.T) {
	raw := `{
		"type": "message",
		"role": "assistant",
		"content": [
			{"type": "text", "text": "Safe Anthropic response"},
			{
				"type": "tool_use",
				"id": "toolu_invented",
				"name": "code_interpreter",
				"input": {}
			}
		],
		"stop_reason": "tool_use"
	}`

	result := ParseSimulatedResponseAnthropic(raw, []string{"safe_tool"}, nil)

	if len(result.ToolCalls) != 0 {
		t.Fatalf("invented Anthropic tool calls were not filtered: %#v", result.ToolCalls)
	}
	if result.Content != "Safe Anthropic response" {
		t.Fatalf("safe content = %q, want %q", result.Content, "Safe Anthropic response")
	}
	if result.FinishReason != "stop" {
		t.Fatalf("finish reason = %q, want stop after filtering all tool calls", result.FinishReason)
	}
}

func TestParseSimulatedResponseKeepsFlatFunctionCallNamespace(t *testing.T) {
	raw := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"type": "function_call",
					"id": "call_namespaced",
					"name": "ctx_batch_execute",
					"namespace": "mcp__context_mode",
					"arguments": "{\"commands\":[]}"
				}]
			},
			"finish_reason": "tool_calls"
		}]
	}`

	result := ParseSimulatedResponse(raw, []string{"ctx_batch_execute"}, nil)

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Namespace != "mcp__context_mode" {
		t.Fatalf("namespace = %q, want mcp__context_mode", result.ToolCalls[0].Namespace)
	}
}

func agentRequiredByTool() map[string][]string {
	return map[string][]string{"Agent": {"description", "prompt"}}
}

func TestParseSimulatedResponseDropsToolCallMissingRequiredArgs(t *testing.T) {
	raw := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "Let me delegate this.",
				"tool_calls": [{
					"id": "call_bad",
					"type": "function",
					"function": {"name": "Agent", "arguments": "{\"description\":\"analyze\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}]
	}`

	result := ParseSimulatedResponse(raw, []string{"Agent"}, agentRequiredByTool())

	if len(result.ToolCalls) != 0 {
		t.Fatalf("tool call with missing required arg was not dropped: %#v", result.ToolCalls)
	}
	if result.FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop after dropping call", result.FinishReason)
	}
	if result.Content != "Let me delegate this." {
		t.Fatalf("assistant content lost after drop: %q", result.Content)
	}
}

func TestParseSimulatedResponseKeepsToolCallWithAllRequiredArgs(t *testing.T) {
	raw := `{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_ok",
					"type": "function",
					"function": {"name": "Agent", "arguments": "{\"description\":\"analyze\",\"prompt\":\"do it\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}]
	}`

	result := ParseSimulatedResponse(raw, []string{"Agent"}, agentRequiredByTool())

	if len(result.ToolCalls) != 1 {
		t.Fatalf("valid tool call was dropped: %#v", result.ToolCalls)
	}
}

func TestParseSimulatedResponseAnthropicDropsToolUseMissingRequiredArgs(t *testing.T) {
	raw := `{
		"type": "message",
		"role": "assistant",
		"stop_reason": "tool_use",
		"content": [
			{"type": "text", "text": "delegating"},
			{"type": "tool_use", "id": "toolu_bad", "name": "Agent", "input": {"prompt": "do it"}}
		]
	}`

	result := ParseSimulatedResponseAnthropic(raw, []string{"Agent"}, agentRequiredByTool())

	if len(result.ToolCalls) != 0 {
		t.Fatalf("Anthropic tool_use with missing required arg was not dropped: %#v", result.ToolCalls)
	}
	if result.Content != "delegating" {
		t.Fatalf("text content lost after drop: %q", result.Content)
	}
}

func TestParseSimulatedResponseAnthropicDropsToolUseWithEmptyRequiredArg(t *testing.T) {
	raw := `{
		"type": "message",
		"role": "assistant",
		"stop_reason": "tool_use",
		"content": [
			{"type": "tool_use", "id": "toolu_empty", "name": "Agent", "input": {"description": "", "prompt": "x"}}
		]
	}`

	result := ParseSimulatedResponseAnthropic(raw, []string{"Agent"}, agentRequiredByTool())

	if len(result.ToolCalls) != 0 {
		t.Fatalf("tool_use with empty required arg was not dropped: %#v", result.ToolCalls)
	}
}

func TestRequiredArgsByToolReadsBothSchemaShapes(t *testing.T) {
	tools := []ToolDef{
		{Name: "Agent", InputSchema: map[string]any{"required": []any{"description", "prompt"}}},
		{Function: ToolDefFunc{Name: "Run", Parameters: map[string]any{"required": []any{"cmd"}}}},
		{Name: "NoSchema"},
	}

	got := RequiredArgsByTool(tools)

	if want := []string{"description", "prompt"}; strings.Join(got["Agent"], ",") != strings.Join(want, ",") {
		t.Fatalf("Agent required = %v, want %v", got["Agent"], want)
	}
	if strings.Join(got["Run"], ",") != "cmd" {
		t.Fatalf("Run required = %v, want [cmd]", got["Run"])
	}
	if len(got["NoSchema"]) != 0 {
		t.Fatalf("NoSchema required = %v, want empty", got["NoSchema"])
	}
}

func TestParseSimulatedResponseAnthropicReportsDroppedMissingArgs(t *testing.T) {
	raw := `{
		"type": "message",
		"role": "assistant",
		"stop_reason": "tool_use",
		"content": [
			{"type": "tool_use", "id": "toolu_bad", "name": "Agent", "input": {"prompt": "do it"}}
		]
	}`

	result := ParseSimulatedResponseAnthropic(raw, []string{"Agent"}, agentRequiredByTool())

	if len(result.ToolCalls) != 0 {
		t.Fatalf("expected the malformed tool_use to be dropped, got %#v", result.ToolCalls)
	}
	if want := []string{"Agent"}; strings.Join(result.DroppedMissingArgs, ",") != strings.Join(want, ",") {
		t.Fatalf("DroppedMissingArgs = %v, want %v", result.DroppedMissingArgs, want)
	}
}

func TestParseSimulatedResponseReportsDroppedMissingArgs(t *testing.T) {
	raw := "```json\n" +
		`{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"Agent","arguments":"{\"description\":\"only one\"}"}}]}}]}` +
		"\n```"

	result := ParseSimulatedResponse(raw, []string{"Agent"}, agentRequiredByTool())

	if len(result.ToolCalls) != 0 {
		t.Fatalf("expected the malformed tool call to be dropped, got %#v", result.ToolCalls)
	}
	if want := []string{"Agent"}; strings.Join(result.DroppedMissingArgs, ",") != strings.Join(want, ",") {
		t.Fatalf("DroppedMissingArgs = %v, want %v", result.DroppedMissingArgs, want)
	}
}

func TestBuildRepairNoteNamesToolsAndRequiredFields(t *testing.T) {
	note := BuildRepairNote([]string{"Agent", "Agent", ""}, agentRequiredByTool())

	for _, want := range []string{"RETRY", "Agent", "description", "prompt"} {
		if !strings.Contains(note, want) {
			t.Fatalf("repair note missing %q: %s", want, note)
		}
	}
}
