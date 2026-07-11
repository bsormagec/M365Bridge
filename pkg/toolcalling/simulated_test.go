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

	result := ParseSimulatedResponse(raw, []string{"safe_tool"})

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

	result := ParseSimulatedResponseAnthropic(raw, []string{"safe_tool"})

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

	result := ParseSimulatedResponse(raw, []string{"ctx_batch_execute"})

	if len(result.ToolCalls) != 1 {
		t.Fatalf("tool call count = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Namespace != "mcp__context_mode" {
		t.Fatalf("namespace = %q, want mcp__context_mode", result.ToolCalls[0].Namespace)
	}
}
