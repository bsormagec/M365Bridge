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
		`choices[0].finish_reason`,
		"function.arguments must be a JSON string",
		"requires at least one tool call",
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
