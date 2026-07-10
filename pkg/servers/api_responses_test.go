package servers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/client"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/payload"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/toolcalling"
)

func responsesTestTools() []toolcalling.ToolDef {
	return []toolcalling.ToolDef{
		{Type: "function", Name: "read_nonce"},
		{Type: "function", Name: "write_nonce"},
	}
}

func TestInjectSimulatedPromptResponsesUsesOneCanonicalMessage(t *testing.T) {
	messages := []payload.Message{
		{Role: "user", Content: "old user message"},
		{Role: "assistant", Content: "old assistant message"},
		{Role: "user", Content: "latest user message"},
	}
	requestJSON := `{"input":[{"role":"user","content":"old user message"},{"role":"assistant","content":"old assistant message"},{"role":"user","content":"latest user message"}]}`

	injectSimulatedPromptResponses(&messages, requestJSON, "auto")

	if len(messages) != 1 {
		t.Fatalf("expected one canonical simulation message, got %d: %#v", len(messages), messages)
	}
	if messages[0].Role != "user" {
		t.Fatalf("canonical simulation message role = %q, want user", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content, requestJSON) {
		t.Fatalf("canonical simulation message does not preserve the raw Responses request")
	}
	if strings.Count(messages[0].Content, requestJSON) != 1 {
		t.Fatalf("raw Responses request was embedded more than once")
	}
}

func TestResponsesToolPolicy(t *testing.T) {
	tools := responsesTestTools()
	tests := []struct {
		name         string
		choice       interface{}
		wantSimulate bool
		wantRequired bool
		wantName     string
	}{
		{name: "default", choice: nil, wantSimulate: true},
		{name: "auto", choice: "auto", wantSimulate: true},
		{name: "required", choice: "required", wantSimulate: true, wantRequired: true},
		{name: "none", choice: "none", wantSimulate: false},
		{
			name:         "named",
			choice:       map[string]interface{}{"type": "function", "name": "read_nonce"},
			wantSimulate: true,
			wantRequired: true,
			wantName:     "read_nonce",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy, err := newResponsesToolPolicy(tools, tt.choice)
			if err != nil {
				t.Fatalf("newResponsesToolPolicy returned error: %v", err)
			}
			if policy.simulate != tt.wantSimulate {
				t.Fatalf("simulate = %v, want %v", policy.simulate, tt.wantSimulate)
			}
			if policy.required != tt.wantRequired {
				t.Fatalf("required = %v, want %v", policy.required, tt.wantRequired)
			}
			if policy.requiredName != tt.wantName {
				t.Fatalf("requiredName = %q, want %q", policy.requiredName, tt.wantName)
			}
		})
	}
}

func TestResponsesToolPolicyRejectsUnknownNamedTool(t *testing.T) {
	_, err := newResponsesToolPolicy(
		responsesTestTools(),
		map[string]interface{}{"type": "function", "name": "invented_tool"},
	)
	if err == nil {
		t.Fatal("expected unknown named tool to be rejected")
	}
}

func TestResponsesToolPolicyAcceptsBuiltInToolType(t *testing.T) {
	tools := []toolcalling.ToolDef{{Type: "tool_search"}}

	policy, err := newResponsesToolPolicy(
		tools,
		map[string]interface{}{"type": "tool_search"},
	)
	if err != nil {
		t.Fatalf("built-in Responses tool rejected: %v", err)
	}
	if len(policy.allowedToolNames) != 1 || policy.allowedToolNames[0] != "tool_search" {
		t.Fatalf("unexpected built-in allowlist: %#v", policy.allowedToolNames)
	}
}

func TestParseResponsesSimulationRequiredRejectsPlainContent(t *testing.T) {
	policy, err := newResponsesToolPolicy(responsesTestTools(), "required")
	if err != nil {
		t.Fatal(err)
	}
	text := "```json\n" +
		`{"choices":[{"message":{"role":"assistant","content":"I will not call a tool."},"finish_reason":"stop"}]}` +
		"\n```"

	_, err = parseResponsesSimulation(text, policy)
	if err == nil {
		t.Fatal("required tool choice accepted a plain-content response")
	}
}

func TestParseResponsesSimulationNamedRejectsWrongDeclaredTool(t *testing.T) {
	policy, err := newResponsesToolPolicy(
		responsesTestTools(),
		map[string]interface{}{"type": "function", "name": "read_nonce"},
	)
	if err != nil {
		t.Fatal(err)
	}
	text := simulatedToolCallEnvelope("write_nonce")

	_, err = parseResponsesSimulation(text, policy)
	if err == nil {
		t.Fatal("named tool choice accepted a different declared tool")
	}
}

func TestParseResponsesSimulationDropsInventedTool(t *testing.T) {
	policy, err := newResponsesToolPolicy(responsesTestTools(), "auto")
	if err != nil {
		t.Fatal(err)
	}

	result, err := parseResponsesSimulation(simulatedToolCallEnvelope("code_interpreter"), policy)
	if err != nil {
		t.Fatalf("auto tool choice returned an unexpected error: %v", err)
	}
	if len(result.toolCalls) != 0 {
		t.Fatalf("invented tool was not rejected: %#v", result.toolCalls)
	}
}

func TestParseResponsesSimulationAcceptsFlatResponsesToolDefinition(t *testing.T) {
	policy, err := newResponsesToolPolicy(responsesTestTools(), "required")
	if err != nil {
		t.Fatal(err)
	}

	result, err := parseResponsesSimulation(simulatedToolCallEnvelope("read_nonce"), policy)
	if err != nil {
		t.Fatalf("valid Responses tool call rejected: %v", err)
	}
	if len(result.toolCalls) != 1 || result.toolCalls[0].Function.Name != "read_nonce" {
		t.Fatalf("unexpected parsed tool calls: %#v", result.toolCalls)
	}
}

func TestBuildResponsesToolCallItemUsesDeclaredBuiltInType(t *testing.T) {
	call := client.ToolCall{
		ID:   "call_search",
		Type: "function",
		Function: client.ToolCallFunction{
			Name:      "tool_search",
			Arguments: `{"query":"node repl"}`,
		},
	}

	item := buildResponsesToolCallItem(
		"call_search",
		call,
		map[string]string{"tool_search": "tool_search"},
		"completed",
	)

	if item["type"] != "tool_search_call" {
		t.Fatalf("tool_search item type = %#v", item["type"])
	}
	if item["execution"] != "client" {
		t.Fatalf("tool_search execution = %#v", item["execution"])
	}
	if _, ok := item["arguments"].(map[string]interface{}); !ok {
		t.Fatalf("tool_search arguments are not a JSON object: %#v", item["arguments"])
	}
}

func TestMergeLoadedResponsesTools(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{
			"type": "tool_search_output",
			"tools": []interface{}{
				map[string]interface{}{
					"type": "namespace",
					"tools": []interface{}{
						map[string]interface{}{
							"type": "function",
							"name": "node_repl",
						},
					},
				},
			},
		},
	}

	tools := mergeLoadedResponsesTools(
		input,
		[]toolcalling.ToolDef{{Type: "tool_search"}},
	)
	names := responsesToolNames(tools)
	if strings.Join(names, ",") != "tool_search,node_repl" {
		t.Fatalf("loaded Responses tools not merged: %#v", names)
	}
}

func TestResponsesInputPreservesToolSearchAndCompactionHistory(t *testing.T) {
	input := []interface{}{
		map[string]interface{}{
			"type":      "tool_search_call",
			"arguments": map[string]interface{}{"query": "node repl"},
		},
		map[string]interface{}{
			"type": "tool_search_output",
			"tools": []interface{}{
				map[string]interface{}{
					"type": "function",
					"name": "node_repl",
				},
			},
		},
		map[string]interface{}{
			"type":              "compaction",
			"encrypted_content": "Earlier work summary",
		},
	}

	messages := responsesInputToMessages(input)
	combined := ""
	for _, message := range messages {
		combined += message.Content + "\n"
	}
	for _, expected := range []string{
		"Tool search call",
		"node_repl",
		"Earlier work summary",
	} {
		if !strings.Contains(combined, expected) {
			t.Fatalf("Responses history lost %q: %#v", expected, messages)
		}
	}
}

func TestContextCacheDeleteRemovesMemoryAndDisk(t *testing.T) {
	cacheDir := t.TempDir()
	cache := NewContextCache(cacheDir)
	cache.Set("session:test", "conv-poisoned")
	cache.Delete("session:test")

	if got := cache.Get("session:test"); got != "" {
		t.Fatalf("deleted context cache returned %q", got)
	}
	matches, err := filepath.Glob(filepath.Join(cacheDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("deleted context cache left disk entries: %#v", matches)
	}
}

func TestShouldResetResponsesSession(t *testing.T) {
	call := client.ToolCall{
		Function: client.ToolCallFunction{Name: "read_nonce"},
	}
	tests := []struct {
		name    string
		content string
		calls   []client.ToolCall
		err     error
		want    bool
	}{
		{name: "error", err: errors.New("failed"), want: true},
		{name: "empty", want: true},
		{name: "content", content: "ok", want: false},
		{name: "tool call", calls: []client.ToolCall{call}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldResetResponsesSession(tt.content, tt.calls, tt.err); got != tt.want {
				t.Fatalf("shouldResetResponsesSession = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResponsesCompactionAlwaysUsesFreshConversation(t *testing.T) {
	if got := responsesCompactionConversationID("conv-poisoned"); got != "" {
		t.Fatalf("compaction reused sticky conversation %q", got)
	}
}

func TestResponsesReasoningIsSuppressedDuringToolSimulation(t *testing.T) {
	const leaked = `{"id":"chatcmpl-1234567890","object":"chat.completion"}`

	if got := responsesReasoningForOutput(leaked, true); got != "" {
		t.Fatalf("tool simulation leaked reasoning payload: %q", got)
	}
	if got := responsesReasoningForOutput("normal reasoning", false); got != "normal reasoning" {
		t.Fatalf("non-simulated reasoning changed: %q", got)
	}
}

func TestWriteResponsesSimulationErrorNonStreaming(t *testing.T) {
	rec := httptest.NewRecorder()

	writeResponsesSimulationError(rec, false, "resp_test", "gpt-test", errSimulatedToolCallRequired)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body: %v", err)
	}
	errorObject, _ := body["error"].(map[string]interface{})
	if errorObject["code"] != simulatedToolCallRequiredCode {
		t.Fatalf("error code = %#v, want %q", errorObject["code"], simulatedToolCallRequiredCode)
	}
}

func TestWriteResponsesSimulationErrorStreaming(t *testing.T) {
	rec := httptest.NewRecorder()

	writeResponsesSimulationError(rec, true, "resp_test", "gpt-test", errSimulatedToolCallRequired)

	body := rec.Body.String()
	if !strings.Contains(body, `"type":"response.failed"`) {
		t.Fatalf("stream missing response.failed event:\n%s", body)
	}
	if !strings.Contains(body, `"code":"`+simulatedToolCallRequiredCode+`"`) {
		t.Fatalf("stream missing stable error code:\n%s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("stream missing terminal marker:\n%s", body)
	}
}

func simulatedToolCallEnvelope(name string) string {
	return "```json\n" +
		`{"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_test","type":"function","function":{"name":"` +
		name +
		`","arguments":"{\"path\":\"nonce.txt\"}"}}]},"finish_reason":"tool_calls"}]}` +
		"\n```"
}
