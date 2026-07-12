package servers

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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

	for _, text := range []string{"Start chatting", "New chat", "Model", "Send", "Clear", "Quit"} {
		if !strings.Contains(view, text) {
			t.Fatalf("default UI is missing English text %q", text)
		}
	}
	if !strings.Contains(view, "Ctrl+C Clear") || !strings.Contains(view, "Ctrl+D Quit") {
		t.Fatalf("footer does not describe the current clear/quit shortcuts: %q", view)
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

func TestAssistantMessageIsVisuallyEmphasizedWhileResponding(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	m.busy = true
	m.messages = []chatMessage{
		{role: "You", content: "hello"},
		{role: "Copilot", content: "I am working on it..."},
	}

	rendered := m.renderChatMessages()
	for _, text := range []string{"Copilot · responding", "I am working on it..."} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("assistant render is missing %q: %q", text, rendered)
		}
	}
}

func TestChatMessageCardsDoNotEmitFragmentedBackgrounds(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	m.messages = []chatMessage{
		{role: "You", content: "A short question"},
		{role: "Copilot", content: "A longer answer that wraps across multiple lines without creating nested background blocks."},
	}

	rendered := m.renderChatMessages()
	if strings.Contains(rendered, "48;") || strings.Contains(rendered, "48;2;") {
		t.Fatalf("chat message cards emit background color escapes: %q", rendered)
	}
}

func TestBusyComposerLocksInputAndKeyHandling(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	m.textarea.Focus()
	m.busy = true
	m.textarea.SetValue("should not send")

	model, cmd := m.updateKey(tea.KeyPressMsg{Text: "x"})
	if cmd != nil {
		t.Fatalf("busy input returned a command: %v", cmd)
	}
	updated := model.(*chatTUI)
	if updated.textarea.Value() != "should not send" {
		t.Fatalf("busy input changed draft unexpectedly: %q", updated.textarea.Value())
	}
	if len(updated.messages) != 0 {
		t.Fatalf("busy input sent a message: %#v", updated.messages)
	}

	rendered := updated.renderComposer()
	for _, text := range []string{"Copilot is responding", "input locked"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("busy composer is missing %q: %q", text, rendered)
		}
	}
	footer := updated.renderFooter()
	if !strings.Contains(footer, "Esc Stop response") || strings.Contains(footer, "Ctrl+D Quit") {
		t.Fatalf("busy footer exposes disabled shortcuts: %q", footer)
	}
}

func TestBusyStateAllowsOnlyEscToStopResponse(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	m.busy = true
	m.requestID = 7
	m.textarea.SetValue("draft must remain untouched")
	m.messages = []chatMessage{{role: "Copilot", content: "partial response"}}

	for _, key := range []tea.KeyPressMsg{
		{Code: 'c', Mod: tea.ModCtrl},
		{Code: 'd', Mod: tea.ModCtrl},
		{Code: 'n', Mod: tea.ModCtrl},
		{Code: 'p', Mod: tea.ModCtrl},
		{Code: 'm', Mod: tea.ModCtrl},
		{Code: 'x'},
		{Code: tea.KeyEnter},
	} {
		model, cmd := m.updateKey(key)
		if cmd != nil {
			t.Fatalf("busy key %q returned a command: %v", key.String(), cmd)
		}
		m = *model.(*chatTUI)
		if !m.busy {
			t.Fatalf("busy key %q unexpectedly stopped the response", key.String())
		}
	}

	oldRequestID := m.requestID
	model, cmd := m.updateKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("Esc did not return the focus command")
	}
	m = *model.(*chatTUI)
	if m.busy || m.requestID == oldRequestID || m.status != "Response stopped" {
		t.Fatalf("Esc did not stop the response cleanly: busy=%v requestID=%d status=%q", m.busy, m.requestID, m.status)
	}
	if m.textarea.Value() != "draft must remain untouched" {
		t.Fatalf("Esc changed the draft: %q", m.textarea.Value())
	}
}

func TestCancelRequestDrainsStaleStream(t *testing.T) {
	stream := make(chan client.StreamChunk)
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		stream <- client.StreamChunk{Text: "stale"}
		stream <- client.StreamChunk{IsFinal: true}
		close(stream)
	}()

	m := newChatTUI(nil, "auto", false)
	m.stream = stream
	m.busy = true
	m.cancelRequest()

	select {
	case <-producerDone:
	case <-time.After(time.Second):
		t.Fatal("cancelRequest did not drain the stale stream")
	}
}

func TestNonStreamErrorRestoresComposerFocus(t *testing.T) {
	m := newChatTUI(nil, "auto", true)
	m.requestID = 3
	m.busy = true
	m.textarea.Blur()
	m.textarea.Placeholder = "Copilot is responding..."

	model, cmd := m.Update(responseMsg{requestID: 3, err: errors.New("request failed")})
	if cmd == nil {
		t.Fatal("response error did not return a focus command")
	}
	updated := model.(*chatTUI)
	cmd()
	if !updated.textarea.Focused() {
		t.Fatal("response error did not restore composer focus")
	}
	if updated.textarea.Placeholder != "Ask anything..." {
		t.Fatalf("placeholder = %q, want restored composer placeholder", updated.textarea.Placeholder)
	}
}

func TestCtrlCClearsComposerWithoutQuitting(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.resize(80, 24)
	m.textarea.SetValue("draft that should be cleared")
	m.textarea.Focus()

	model, cmd := m.updateKey(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatalf("Ctrl+C returned a command: %v", cmd)
	}
	updated := model.(*chatTUI)
	if updated.textarea.Value() != "" {
		t.Fatalf("Ctrl+C left composer content: %q", updated.textarea.Value())
	}
	if !updated.textarea.Focused() {
		t.Fatal("Ctrl+C did not keep the composer focused")
	}
}

func TestCtrlDQuits(t *testing.T) {
	m := newChatTUI(nil, "auto", false)

	_, cmd := m.updateKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("Ctrl+D did not return a quit command")
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
			want:          chatLayout{width: 100, height: 30, composerHeight: 3, viewportHeight: 21},
		},
		{
			name:          "large draft is capped",
			width:         80,
			height:        24,
			composerLines: 20,
			want:          chatLayout{width: 80, height: 24, composerHeight: maxComposerHeight, viewportHeight: 10},
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
		sessionID:    "old-session",
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
	if m.sessionID == "old-session" || m.sessionID == "" {
		t.Fatalf("resetConversation() did not start a new session: %q", m.sessionID)
	}
}

func TestSessionResumeRestoresConversation(t *testing.T) {
	store := newSessionStore(t.TempDir())
	if err := store.Save(persistedSession{
		ID:             "session-1",
		Title:          "Deploy the bridge",
		Model:          "claude-sonnet",
		ConversationID: "conversation-1",
		Messages: []persistedMessage{
			{Role: "You", Content: "Deploy the bridge"},
			{Role: "Copilot", Content: "Sure."},
		},
		UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	m := newChatTUI(nil, "auto", false)
	m.sessionStore = store
	m.openSessionPicker()
	if !m.sessionPickerOpen || len(m.sessions) != 1 {
		t.Fatalf("openSessionPicker() state = open:%v sessions:%d", m.sessionPickerOpen, len(m.sessions))
	}
	m.resumeSelectedSession()

	if m.sessionPickerOpen || m.sessionID != "session-1" || m.modelKey != "claude-sonnet" || m.conversation != "conversation-1" {
		t.Fatalf("resumeSelectedSession() state = %#v", m)
	}
	if len(m.messages) != 2 || m.messages[1].content != "Sure." {
		t.Fatalf("resumeSelectedSession() messages = %#v", m.messages)
	}
}

func TestSessionTitleAndPickerRender(t *testing.T) {
	if got := deriveSessionTitle([]chatMessage{{role: "You", content: "  fix   the   deployment  "}}); got != "fix the deployment" {
		t.Fatalf("deriveSessionTitle() = %q", got)
	}

	m := newChatTUI(nil, "auto", false)
	m.width, m.height = 80, 24
	m.sessions = []persistedSession{{Title: "Fix the deployment", Model: "auto", UpdatedAt: time.Now()}}
	rendered := m.renderSessionPicker()
	for _, text := range []string{"Sessions", "Fix the deployment", "Enter resume", "d delete"} {
		if !strings.Contains(rendered, text) {
			t.Fatalf("session picker is missing %q: %q", text, rendered)
		}
	}
}

func TestSessionPickerFiltersLoadedSessionsLocally(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.sessions = []persistedSession{
		{ID: "local-1", Title: "Deploy bridge", ConversationID: "conv-1"},
		{ID: "local-2", Title: "Review auth", ConversationID: "conv-2"},
	}
	m.cloudSessions = []cloudSession{
		{id: "cloud-1", title: "Project chat", preview: "Deploy bridge status"},
		{id: "cloud-2", title: "Weekend", preview: "Travel plans"},
	}
	m.sessionSearch = "DEPLOY"

	local := m.filteredLocalSessions()
	if len(local) != 1 || local[0].ID != "local-1" {
		t.Fatalf("filteredLocalSessions() = %#v", local)
	}
	cloud := m.filteredCloudSessions()
	if len(cloud) != 1 || cloud[0].id != "cloud-1" {
		t.Fatalf("filteredCloudSessions() = %#v", cloud)
	}
}

func TestSessionPickerSearchDoesNotChangeLoadedData(t *testing.T) {
	m := newChatTUI(nil, "auto", false)
	m.sessionPickerOpen = true
	m.sessionSource = "local"
	m.sessions = []persistedSession{{ID: "local-1", Title: "Deploy bridge"}}
	model, cmd := m.updateKey(tea.KeyPressMsg{Text: "/"})
	if cmd != nil || model.(*chatTUI).sessionSearchActive != true {
		t.Fatalf("search activation state = %#v, cmd=%v", model.(*chatTUI), cmd)
	}
	model, cmd = model.(*chatTUI).updateKey(tea.KeyPressMsg{Text: "D"})
	if cmd != nil || model.(*chatTUI).sessionSearch != "D" || len(model.(*chatTUI).sessions) != 1 {
		t.Fatalf("search state = %#v, cmd=%v", model.(*chatTUI), cmd)
	}
}

func TestNormalizeCloudSessions(t *testing.T) {
	items := normalizeCloudSessions([]map[string]any{
		{"conversation": map[string]any{"conversationId": "cloud-1", "chatName": "Project chat", "createTimeUtc": "2026-07-12T12:00:00Z"}},
		{"chat": map[string]any{"id": "cloud-2", "displayName": "Older chat", "updatedAt": "2026-07-11T12:00:00Z"}},
		{"title": "Missing ID"},
	})

	if len(items) != 2 || items[0].id != "cloud-1" || items[0].title != "Project chat" || items[1].id != "cloud-2" {
		t.Fatalf("normalizeCloudSessions() = %#v", items)
	}
}

func TestVisibleSessionRangeBoundsPickerContent(t *testing.T) {
	if start, end := visibleSessionRange(9, 20, 5); start != 7 || end != 12 {
		t.Fatalf("visibleSessionRange() = %d:%d, want 7:12", start, end)
	}
	if start, end := visibleSessionRange(0, 2, 5); start != 0 || end != 2 {
		t.Fatalf("visibleSessionRange() small = %d:%d, want 0:2", start, end)
	}
}

func TestResumeSelectedCloudSessionUsesConversationID(t *testing.T) {
	m := newChatTUI(nil, "claude-sonnet", false)
	m.width, m.height = 80, 24
	m.sessionPickerOpen = true
	m.sessionSource = "cloud"
	m.cloudSessions = []cloudSession{{id: "cloud-1", title: "Project chat", preview: "Previous Copilot answer"}}

	m.resumeSelectedCloudSession()

	if m.sessionPickerOpen || m.conversation != "cloud-1" || m.sessionTitle != "Project chat" || m.sessionSource != "local" {
		t.Fatalf("resumeSelectedCloudSession() state = %#v", m)
	}
	if len(m.messages) != 1 || m.messages[0].role != "Copilot" || m.messages[0].content != "Previous Copilot answer" {
		t.Fatalf("cloud resume did not restore preview message: %#v", m.messages)
	}
}

func TestPersistCloudSessionStoresMetadataOnly(t *testing.T) {
	store := newSessionStore(t.TempDir())
	m := newChatTUI(nil, "auto", false)
	m.sessionStore = store
	m.sessionID = "local-cloud-1"
	m.modelKey = "auto"
	m.persistCloudSession(cloudSession{
		id:           "conversation-1",
		title:        "Imported chat",
		preview:      "Recent answer",
		messageCount: 12,
	})

	session, err := store.Load("local-cloud-1")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if session.Source != "m365" || session.ConversationID != "conversation-1" || session.Preview != "Recent answer" || session.MessageCount != 12 {
		t.Fatalf("cloud metadata = %#v", session)
	}
	if session.Messages != nil {
		t.Fatalf("cloud session stored messages: %#v", session.Messages)
	}
}

func TestCloudPreviewMessagesEmptyPreview(t *testing.T) {
	if messages := cloudPreviewMessages(cloudSession{preview: "  "}); messages != nil {
		t.Fatalf("cloudPreviewMessages() = %#v, want nil", messages)
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
