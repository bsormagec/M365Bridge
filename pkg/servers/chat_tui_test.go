package servers

import (
	"reflect"
	"strings"
	"testing"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/client"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/models"
)

func TestInitFocusesComposer(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.Init()

	if !m.textarea.Focused() {
		t.Fatal("Init() did not focus the composer")
	}
}

func TestDefaultUIStringsAreEnglish(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	view := m.View().Content

	for _, text := range []string{"Start chatting", "New chat", "Model", "Send", "Quit"} {
		if !strings.Contains(view, text) {
			t.Fatalf("default UI is missing English text %q", text)
		}
	}
	if !strings.Contains(m.textarea.Placeholder, "Ask anything") {
		t.Fatalf("default composer placeholder is not English: %q", m.textarea.Placeholder)
	}
	for _, text := range []string{"Mesaj", "Yeni sohbet", "Gönder", "Çıkış", "Hazır"} {
		if strings.Contains(view, text) {
			t.Fatalf("default UI still contains Turkish-only text %q", text)
		}
	}
}

func TestChatMessagesRenderAsDistinctStyledBlocks(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	m.messages = []chatMessage{
		{role: "You", content: "hello"},
		{role: "Copilot", content: "Hello! How can I help?"},
	}

	rendered := m.renderChatMessages()
	for _, text := range []string{"You", "hello", "Copilot", "Hello! How can I help?"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("styled chat render is missing %q: %q", text, rendered)
		}
	}
}

func TestComposerUsesCardLayoutAndMetadata(t *testing.T) {
	m := newChatTUI(nil, "claude-sonnet", false)
	m.resize(80, 24)
	rendered := m.renderComposer()

	for _, text := range []string{"claude-sonnet", "Enter Send", "Shift+Enter New line", "Ctrl+J New line"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("composer render is missing %q: %q", text, rendered)
		}
	}
}

func TestComposerSupportsShiftEnterForNewLine(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	keys := m.textarea.KeyMap.InsertNewline.Keys()

	if !containsString(keys, "shift+enter") {
		t.Fatalf("newline keys = %v, want shift+enter", keys)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestComposerTextareaStylesHaveReadableContrast(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	styles := m.textarea.Styles()

	if !reflect.DeepEqual(styles.Focused.Placeholder.GetForeground(), colorMuted) {
		t.Fatalf("focused placeholder foreground = %v, want %v", styles.Focused.Placeholder.GetForeground(), colorMuted)
	}
	if !reflect.DeepEqual(styles.Focused.Text.GetForeground(), colorText) {
		t.Fatalf("focused text foreground = %v, want %v", styles.Focused.Text.GetForeground(), colorText)
	}
	m.textarea.Focus()
	m.textarea.SetWidth(40)
	m.textarea.SetHeight(3)
	if rendered := m.textarea.View(); strings.Contains(rendered, "48;") {
		t.Fatalf("textarea emits an internal background color: %q", rendered)
	}
}

func TestComputeChatLayoutClampsComposerAndViewport(t *testing.T) {
	tests := []struct {
		name          string
		width         int
		height        int
		composerLines int
		want          chatLayout
	}{
		{
			name:          "normal terminal",
			width:         100,
			height:        30,
			composerLines: 2,
			want:          chatLayout{width: 100, height: 30, composerHeight: 3, viewportHeight: 22},
		},
		{
			name:          "large draft is capped",
			width:         80,
			height:        24,
			composerLines: 20,
			want:          chatLayout{width: 80, height: 24, composerHeight: maxComposerHeight, viewportHeight: 11},
		},
		{
			name:          "small terminal keeps viewport positive",
			width:         40,
			height:        4,
			composerLines: 1,
			want:          chatLayout{width: 40, height: 4, composerHeight: 3, viewportHeight: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeChatLayout(tt.width, tt.height, tt.composerLines); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("computeChatLayout() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestResetConversationClearsVisibleState(t *testing.T) {
	m := chatTUI{
		modelKey:     "gpt5.5",
		conversation: "conversation-1",
		messages: []chatMessage{
			{role: "You", content: "Hello"},
			{role: "Copilot", content: "Hi"},
		},
		status:  "done",
		errText: "old error",
	}

	m.resetConversation()

	if m.conversation != "" || len(m.messages) != 0 || m.status != "" || m.errText != "" {
		t.Fatalf("resetConversation() left stale state: %#v", m)
	}
	if m.modelKey != "gpt5.5" {
		t.Fatalf("resetConversation() changed selected model: %q", m.modelKey)
	}
}

func TestApplyStreamChunkAppendsToAssistantMessage(t *testing.T) {
	m := chatTUI{messages: []chatMessage{{role: "You", content: "Hello"}}}
	m.beginAssistantMessage()

	m.applyStreamChunk(client.StreamChunk{Text: "Hi"})
	m.applyStreamChunk(client.StreamChunk{Text: " there"})
	m.applyStreamChunk(client.StreamChunk{IsFinal: true, ConversationID: "conversation-2"})

	if got := m.messages[len(m.messages)-1].content; got != "Hi there" {
		t.Fatalf("assistant content = %q, want %q", got, "Hi there")
	}
	if m.conversation != "conversation-2" {
		t.Fatalf("conversation = %q, want %q", m.conversation, "conversation-2")
	}
}

func TestModelKeysAreSortedAndMatchRegistry(t *testing.T) {
	keys := sortedModelKeys()
	if len(keys) != len(models.ModelRegistry) {
		t.Fatalf("sortedModelKeys() returned %d keys, registry has %d", len(keys), len(models.ModelRegistry))
	}
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Fatalf("model keys are not strictly sorted: %v", keys)
		}
	}
}
