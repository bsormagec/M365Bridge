package servers

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/client"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/models"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/payload"
	"github.com/google/uuid"
)

const (
	headerHeight          = 2
	footerHeight          = 2
	composerMetaHeight    = 1
	composerDividerHeight = 1
	minComposerHeight     = 3
	maxComposerHeight     = 8
	defaultTUIWidth       = 80
	defaultTUIHeight      = 24
	modelPickerPadding    = 4
)

var (
	colorCyan   = lipgloss.Color("#22d3ee")
	colorBlue   = lipgloss.Color("#60a5fa")
	colorOrange = lipgloss.Color("#f59e0b")
	colorMuted  = lipgloss.Color("#7d8590")
	colorText   = lipgloss.Color("#d7dde5")
	colorPanel  = lipgloss.Color("#151b21")
	colorAgent  = lipgloss.Color("#18252b")
	colorInput  = lipgloss.Color("#171a1f")
)

type chatMessage struct {
	role    string
	content string
}

type chatLayout struct {
	width          int
	height         int
	composerHeight int
	viewportHeight int
}

type streamChunkMsg struct {
	requestID uint64
	chunk     client.StreamChunk
}

type streamDoneMsg struct {
	requestID uint64
}

type streamErrorMsg struct {
	requestID uint64
	err       error
}

type responseMsg struct {
	requestID    uint64
	text         string
	conversation string
	err          error
}

type cloudSessionsMsg struct {
	sessions []cloudSession
	err      error
}

type cloudSession struct {
	id           string
	title        string
	preview      string
	messageCount int
	updatedAt    time.Time
}

type modelItem struct {
	key string
}

type chatTUI struct {
	cli          *CLIServer
	modelKey     string
	noStream     bool
	conversation string
	messages     []chatMessage

	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model

	modelKeys  []string
	modelIndex int
	pickerOpen bool

	sessionStore        sessionStore
	sessionID           string
	sessionTitle        string
	activeSessionSource string
	sessionPreview      string
	sessionMessageCount int
	sessions            []persistedSession
	sessionIndex        int
	sessionPickerOpen   bool
	sessionSource       string
	sessionSearch       string
	sessionSearchActive bool
	cloudSessions       []cloudSession
	cloudIndex          int
	cloudLoading        bool
	conversationClient  *client.ConversationClient

	busy      bool
	requestID uint64
	stream    <-chan client.StreamChunk
	width     int
	height    int
	status    string
	errText   string
}

func runChatTUI(cli *CLIServer, options *CLIOptions) error {
	m := newChatTUI(cli, options.Model, options.NoStream)
	program := tea.NewProgram(&m)
	_, err := program.Run()
	return err
}

func newChatTUI(cli *CLIServer, modelKey string, noStream bool) chatTUI {
	if modelKey == "" {
		modelKey = "auto"
	}

	ta := textarea.New()
	ta.Prompt = ""
	ta.Placeholder = "Ask anything..."
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetKeys("ctrl+j", "shift+enter")
	configureComposerStyles(&ta)

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return chatTUI{
		cli:                 cli,
		modelKey:            modelKey,
		noStream:            noStream,
		textarea:            ta,
		viewport:            viewport.New(),
		spinner:             sp,
		modelKeys:           sortedModelKeys(),
		sessionStore:        defaultSessionStore(),
		sessionID:           uuid.NewString(),
		activeSessionSource: "local",
		width:               defaultTUIWidth,
		height:              defaultTUIHeight,
		conversationClient: func() *client.ConversationClient {
			if cli == nil || cli.tokenManager == nil {
				return nil
			}
			return client.NewConversationClient(cli.tokenManager)
		}(),
	}
}

func configureComposerStyles(ta *textarea.Model) {
	styles := ta.Styles()
	base := lipgloss.NewStyle().Foreground(colorText)
	placeholder := lipgloss.NewStyle().Foreground(colorMuted)
	prompt := lipgloss.NewStyle().Foreground(colorCyan).Bold(true)
	cursorLine := lipgloss.NewStyle().Foreground(colorText)
	endOfBuffer := lipgloss.NewStyle().Foreground(colorMuted)

	styles.Focused.Base = base
	styles.Focused.Text = base
	styles.Focused.Placeholder = placeholder
	styles.Focused.Prompt = prompt
	styles.Focused.CursorLine = cursorLine
	styles.Focused.EndOfBuffer = endOfBuffer

	styles.Blurred.Base = base
	styles.Blurred.Text = base
	styles.Blurred.Placeholder = placeholder
	styles.Blurred.Prompt = prompt
	styles.Blurred.CursorLine = cursorLine
	styles.Blurred.EndOfBuffer = endOfBuffer

	styles.Cursor.Color = colorCyan
	styles.Cursor.Shape = tea.CursorBar
	styles.Cursor.Blink = true
	ta.SetStyles(styles)
}

func (m *chatTUI) Init() tea.Cmd {
	return tea.Batch(
		m.textarea.Focus(),
		func() tea.Msg { return m.spinner.Tick() },
	)
}

func (m *chatTUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil

	case tea.KeyPressMsg:
		return m.updateKey(msg)

	case tea.MouseMsg:
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		m.refreshViewport(false)
		return m, cmd

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case streamChunkMsg:
		if msg.requestID != m.requestID {
			return m, nil
		}
		m.applyStreamChunk(msg.chunk)
		if msg.chunk.Error != nil {
			m.busy = false
			m.errText = msg.chunk.Error.Error()
			m.status = "Request failed"
			m.stream = nil
			return m, m.restoreComposerFocus()
		}
		if msg.chunk.IsFinal {
			m.busy = false
			m.status = "Ready"
			m.stream = nil
			m.persistCurrentSession()
			return m, m.restoreComposerFocus()
		}
		return m, readStreamChunk(msg.requestID, m.stream)

	case streamDoneMsg:
		if msg.requestID == m.requestID {
			m.busy = false
			m.status = "Ready"
			m.persistCurrentSession()
			return m, m.restoreComposerFocus()
		}
		return m, nil

	case streamErrorMsg:
		if msg.requestID == m.requestID {
			m.busy = false
			m.errText = msg.err.Error()
			m.status = "Request failed"
			m.stream = nil
			return m, m.restoreComposerFocus()
		}
		return m, nil

	case responseMsg:
		if msg.requestID != m.requestID {
			return m, nil
		}
		m.busy = false
		m.stream = nil
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = "Request failed"
			return m, nil
		}
		m.messages[len(m.messages)-1].content = msg.text
		m.conversation = msg.conversation
		m.status = "Ready"
		m.refreshViewport(true)
		m.persistCurrentSession()
		return m, m.restoreComposerFocus()

	case cloudSessionsMsg:
		m.cloudLoading = false
		if msg.err != nil {
			m.errText = "M365 chat list failed: " + msg.err.Error()
			m.status = "M365 chats unavailable"
			return m, nil
		}
		m.cloudSessions = msg.sessions
		m.cloudIndex = 0
		m.sessionSource = "cloud"
		m.status = fmt.Sprintf("%d M365 chats", len(msg.sessions))
		return m, nil
	}

	return m, nil
}

func (m *chatTUI) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.busy {
		if key == "esc" {
			m.cancelRequest()
			return m, m.restoreComposerFocus()
		}
		return m, nil
	}

	if key == "ctrl+d" {
		m.persistCurrentSession()
		return m, tea.Quit
	}
	if key == "ctrl+c" {
		m.clearComposer()
		return m, nil
	}

	if m.sessionPickerOpen {
		if m.sessionSearchActive && (key == "backspace" || key == "delete") {
			if m.sessionSearch != "" {
				m.sessionSearch = string([]rune(m.sessionSearch)[:len([]rune(m.sessionSearch))-1])
				m.resetSessionSelection()
			}
			return m, nil
		}
		if m.sessionSearchActive && key != "" && len([]rune(key)) == 1 && []rune(key)[0] >= 32 {
			m.sessionSearch += key
			m.resetSessionSelection()
			return m, nil
		}
		switch key {
		case "esc":
			if m.sessionSearchActive {
				m.sessionSearchActive = false
				return m, nil
			}
			m.sessionPickerOpen = false
		case "/":
			m.sessionSearchActive = true
		case "l":
			m.sessionSource = "local"
			m.resetSessionSelection()
		case "m":
			m.sessionSource = "cloud"
			m.resetSessionSelection()
			return m, m.fetchCloudSessions()
		case "r":
			if m.sessionSource == "cloud" {
				return m, m.fetchCloudSessions()
			}
		case "up", "k":
			if m.sessionSource == "cloud" {
				if m.cloudIndex > 0 {
					m.cloudIndex--
				}
			} else if m.sessionIndex > 0 {
				m.sessionIndex--
			}
		case "down", "j":
			if m.sessionSource == "cloud" {
				if m.cloudIndex < len(m.filteredCloudSessions())-1 {
					m.cloudIndex++
				}
			} else if m.sessionIndex < len(m.filteredLocalSessions())-1 {
				m.sessionIndex++
			}
		case "enter":
			if m.sessionSource == "cloud" {
				m.resumeSelectedCloudSession()
			} else {
				m.resumeSelectedSession()
			}
		case "n":
			m.sessionPickerOpen = false
			m.resetConversation()
		case "d":
			m.deleteSelectedSession()
		}
		return m, nil
	}

	if m.pickerOpen {
		switch key {
		case "esc":
			m.pickerOpen = false
		case "up", "k":
			if m.modelIndex > 0 {
				m.modelIndex--
			}
		case "down", "j":
			if m.modelIndex < len(m.modelKeys)-1 {
				m.modelIndex++
			}
		case "enter":
			if len(m.modelKeys) > 0 {
				m.modelKey = m.modelKeys[m.modelIndex]
			}
			m.pickerOpen = false
		}
		return m, nil
	}

	switch key {
	case "ctrl+n":
		m.resetConversation()
		return m, nil
	case "ctrl+p":
		m.openSessionPicker()
		return m, nil
	case "ctrl+m":
		m.openModelPicker()
		return m, nil
	case "enter":
		if m.busy {
			return m, nil
		}
		text := strings.TrimSpace(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.textarea.Reset()
		m.resize(m.width, m.height)
		m.messages = append(m.messages, chatMessage{role: "You", content: text})
		m.beginAssistantMessage()
		m.busy = true
		m.textarea.Blur()
		m.textarea.Placeholder = "Copilot is responding..."
		m.errText = ""
		m.status = "Copilot is typing " + m.spinner.View()
		m.requestID++
		return m, m.startRequest(text, m.requestID)
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.resize(m.width, m.height)
	return m, cmd
}

func (m *chatTUI) cancelRequest() {
	// Invalidate any in-flight stream messages. The underlying network read may
	// finish later, but stale chunks will be ignored by Update.
	m.requestID++
	m.busy = false
	m.stream = nil
	m.status = "Response stopped"
	m.errText = ""
	m.refreshViewport(true)
}

func (m *chatTUI) clearComposer() {
	m.textarea.Reset()
	m.resize(m.width, m.height)
	if m.busy {
		m.textarea.Blur()
		m.textarea.Placeholder = "Copilot is responding..."
		return
	}
	m.textarea.Placeholder = "Ask anything..."
	m.textarea.Focus()
}

func (m *chatTUI) fetchCloudSessions() tea.Cmd {
	if m.conversationClient == nil {
		m.cloudLoading = false
		return func() tea.Msg {
			return cloudSessionsMsg{err: fmt.Errorf("M365 conversation client is not initialized")}
		}
	}
	m.cloudLoading = true
	m.status = "Loading M365 chats..."
	conversationClient := m.conversationClient
	return func() tea.Msg {
		sessions, err := conversationClient.ListConversations(context.Background())
		if err != nil {
			return cloudSessionsMsg{err: err}
		}
		return cloudSessionsMsg{sessions: normalizeCloudSessions(sessions)}
	}
}

func (m *chatTUI) startRequest(text string, requestID uint64) tea.Cmd {
	if m.cli == nil || m.cli.m365Client == nil {
		return func() tea.Msg {
			return streamErrorMsg{requestID: requestID, err: fmt.Errorf("M365 client is not initialized")}
		}
	}

	cfg := models.LookupModel(m.modelKey)
	if m.noStream {
		return func() tea.Msg {
			result, _, _, _, conversationID, err := m.cli.m365Client.ChatConversation(
				[]payload.Message{{Role: "user", Content: text}},
				cfg.Tone,
				cfg.Override,
				m.conversation,
				m.cli.config.UserOID,
				m.cli.config.TenantID,
				false,
			)
			return responseMsg{requestID: requestID, text: result, conversation: conversationID, err: err}
		}
	}

	stream := m.cli.m365Client.ChatStreamGen(
		text,
		cfg.Tone,
		cfg.Override,
		m.conversation,
		m.cli.config.UserOID,
		m.cli.config.TenantID,
		false,
	)
	m.stream = stream
	return readStreamChunk(requestID, stream)
}

func readStreamChunk(requestID uint64, stream <-chan client.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		chunk, ok := <-stream
		if !ok {
			return streamDoneMsg{requestID: requestID}
		}
		return streamChunkMsg{requestID: requestID, chunk: chunk}
	}
}

func (m *chatTUI) beginAssistantMessage() {
	m.messages = append(m.messages, chatMessage{role: "Copilot"})
	m.refreshViewport(true)
}

func (m *chatTUI) restoreComposerFocus() tea.Cmd {
	m.textarea.Placeholder = "Ask anything..."
	return m.textarea.Focus()
}

func (m *chatTUI) applyStreamChunk(chunk client.StreamChunk) {
	if len(m.messages) == 0 || m.messages[len(m.messages)-1].role != "Copilot" {
		m.beginAssistantMessage()
	}
	if chunk.Text != "" {
		m.messages[len(m.messages)-1].content += chunk.Text
	}
	if chunk.ConversationID != "" {
		m.conversation = chunk.ConversationID
	}
	m.refreshViewport(true)
}

func (m *chatTUI) resetConversation() {
	m.requestID++
	m.sessionID = uuid.NewString()
	m.sessionTitle = ""
	m.activeSessionSource = "local"
	m.sessionPreview = ""
	m.sessionMessageCount = 0
	m.conversation = ""
	m.messages = nil
	m.busy = false
	m.status = ""
	m.errText = ""
	m.stream = nil
	m.refreshViewport(false)
}

func (m *chatTUI) persistCurrentSession() {
	if len(m.messages) == 0 {
		return
	}
	if m.sessionTitle == "" {
		m.sessionTitle = deriveSessionTitle(m.messages)
	}
	session := persistedSession{
		ID:             m.sessionID,
		Title:          m.sessionTitle,
		Model:          m.modelKey,
		Source:         m.activeSessionSource,
		ConversationID: m.conversation,
		MessageCount:   len(m.messages),
		Messages:       toPersistedMessages(m.messages),
		UpdatedAt:      time.Now().UTC(),
	}
	if session.Source == "m365" {
		if last := lastCopilotMessage(m.messages); last != "" {
			m.sessionPreview = last
		}
		session.Preview = m.sessionPreview
		session.MessageCount = m.sessionMessageCount
		session.Messages = nil
	}
	if err := m.sessionStore.Save(session); err != nil {
		m.errText = "session save failed: " + err.Error()
		return
	}
	m.sessions, _ = m.sessionStore.List()
}

func deriveSessionTitle(messages []chatMessage) string {
	for _, message := range messages {
		if message.role != "You" {
			continue
		}
		title := strings.Join(strings.Fields(message.content), " ")
		if len([]rune(title)) > 60 {
			return string([]rune(title)[:57]) + "..."
		}
		if title != "" {
			return title
		}
	}
	return "Untitled session"
}

func (m *chatTUI) openSessionPicker() {
	sessions, err := m.sessionStore.List()
	if err != nil {
		m.errText = "session list failed: " + err.Error()
		return
	}
	m.sessions = sessions
	m.sessionIndex = 0
	m.sessionSearch = ""
	m.sessionSearchActive = false
	m.sessionSource = "local"
	m.sessionPickerOpen = true
}

func (m *chatTUI) resumeSelectedSession() {
	sessions := m.filteredLocalSessions()
	if len(sessions) == 0 || m.sessionIndex >= len(sessions) {
		return
	}
	session, err := m.sessionStore.Load(sessions[m.sessionIndex].ID)
	if err != nil {
		m.errText = "session load failed: " + err.Error()
		return
	}
	m.sessionID = session.ID
	m.sessionTitle = session.Title
	m.activeSessionSource = session.Source
	m.sessionPreview = session.Preview
	m.sessionMessageCount = session.MessageCount
	m.modelKey = session.Model
	if m.modelKey == "" {
		m.modelKey = "auto"
	}
	m.conversation = session.ConversationID
	if session.Source == "m365" {
		m.messages = cloudPreviewMessages(cloudSession{title: session.Title, preview: session.Preview})
	} else {
		m.messages = fromPersistedMessages(session.Messages)
	}
	m.busy = false
	m.stream = nil
	m.errText = ""
	m.status = "Session resumed"
	m.sessionPickerOpen = false
	m.refreshViewport(true)
}

func (m *chatTUI) resumeSelectedCloudSession() {
	sessions := m.filteredCloudSessions()
	if len(sessions) == 0 || m.cloudIndex >= len(sessions) {
		return
	}
	selected := sessions[m.cloudIndex]
	m.sessionID = m.localSessionIDForConversation(selected.id)
	m.sessionTitle = selected.title
	m.activeSessionSource = "m365"
	m.sessionPreview = selected.preview
	m.sessionMessageCount = selected.messageCount
	m.conversation = selected.id
	m.messages = cloudPreviewMessages(selected)
	m.busy = false
	m.stream = nil
	m.errText = ""
	m.status = "M365 chat selected"
	m.sessionPickerOpen = false
	m.sessionSource = "local"
	m.refreshViewport(true)
	m.persistCloudSession(selected)
}

func (m *chatTUI) localSessionIDForConversation(conversationID string) string {
	for _, session := range m.sessions {
		legacyM365 := session.Source == "" && len(session.Messages) <= 1 && session.ConversationID != ""
		if (session.Source == "m365" || legacyM365) && session.ConversationID == conversationID {
			return session.ID
		}
	}
	return uuid.NewString()
}

func (m *chatTUI) persistCloudSession(selected cloudSession) {
	if strings.TrimSpace(selected.id) == "" {
		return
	}
	session := persistedSession{
		ID:             m.sessionID,
		Title:          selected.title,
		Model:          m.modelKey,
		Source:         "m365",
		ConversationID: selected.id,
		Preview:        selected.preview,
		MessageCount:   selected.messageCount,
		UpdatedAt:      time.Now().UTC(),
	}
	if err := m.sessionStore.Save(session); err != nil {
		m.errText = "session save failed: " + err.Error()
		return
	}
	m.sessions, _ = m.sessionStore.List()
}

func cloudPreviewMessages(session cloudSession) []chatMessage {
	preview := strings.TrimSpace(session.preview)
	if preview == "" {
		return nil
	}
	return []chatMessage{{role: "Copilot", content: preview}}
}

func lastCopilotMessage(messages []chatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].role == "Copilot" && strings.TrimSpace(messages[i].content) != "" {
			return strings.TrimSpace(messages[i].content)
		}
	}
	return ""
}

func normalizeCloudSessions(items []map[string]any) []cloudSession {
	result := make([]cloudSession, 0, len(items))
	for _, item := range items {
		id := findNestedString(item, "conversationid", "conversation_id", "id")
		if id == "" {
			continue
		}
		title := findNestedString(item, "chatname", "title", "displayname", "name", "subject")
		if title == "" {
			title = "Untitled M365 chat"
		}
		preview := findNestedString(item, "preview", "lastmessage", "lastmessagetext")
		result = append(result, cloudSession{
			id:           id,
			title:        title,
			preview:      preview,
			messageCount: findNestedInt(item, "messagecount", "messagescount", "turncount"),
			updatedAt:    findNestedTime(item, "updatetimeutc", "createtimeutc", "updatedat", "lastupdatedtime", "lastmodifiedtime", "timestamp"),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].updatedAt.IsZero() || result[j].updatedAt.IsZero() {
			return false
		}
		return result[i].updatedAt.After(result[j].updatedAt)
	})
	return result
}

func findNestedString(value any, keys ...string) string {
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[normalizeFieldName(key)] = struct{}{}
	}
	return findNestedStringByKeys(value, wanted, 0)
}

func findNestedStringByKeys(value any, wanted map[string]struct{}, depth int) string {
	if depth > 8 {
		return ""
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, ok := wanted[normalizeFieldName(key)]; ok {
				if text, ok := child.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
		for _, child := range typed {
			if found := findNestedStringByKeys(child, wanted, depth+1); found != "" {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findNestedStringByKeys(child, wanted, depth+1); found != "" {
				return found
			}
		}
	}
	return ""
}

func findNestedInt(value any, keys ...string) int {
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[normalizeFieldName(key)] = struct{}{}
	}
	return findNestedIntByKeys(value, wanted, 0)
}

func findNestedIntByKeys(value any, wanted map[string]struct{}, depth int) int {
	if depth > 8 {
		return 0
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, ok := wanted[normalizeFieldName(key)]; !ok {
				continue
			}
			switch number := child.(type) {
			case float64:
				return int(number)
			case json.Number:
				if value, err := number.Int64(); err == nil {
					return int(value)
				}
			}
		}
		for _, child := range typed {
			if found := findNestedIntByKeys(child, wanted, depth+1); found > 0 {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findNestedIntByKeys(child, wanted, depth+1); found > 0 {
				return found
			}
		}
	}
	return 0
}

func findNestedTime(value any, keys ...string) time.Time {
	wanted := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		wanted[normalizeFieldName(key)] = struct{}{}
	}
	return findNestedTimeByKeys(value, wanted, 0)
}

func findNestedTimeByKeys(value any, wanted map[string]struct{}, depth int) time.Time {
	if depth > 8 {
		return time.Time{}
	}
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if _, ok := wanted[normalizeFieldName(key)]; !ok {
				continue
			}
			switch item := child.(type) {
			case string:
				for _, layout := range []string{time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04:05.000Z"} {
					if parsed, err := time.Parse(layout, item); err == nil {
						return parsed
					}
				}
			case float64:
				return unixTimestamp(item)
			case json.Number:
				if value, err := item.Float64(); err == nil {
					return unixTimestamp(value)
				}
			}
		}
		for _, child := range typed {
			if found := findNestedTimeByKeys(child, wanted, depth+1); !found.IsZero() {
				return found
			}
		}
	case []any:
		for _, child := range typed {
			if found := findNestedTimeByKeys(child, wanted, depth+1); !found.IsZero() {
				return found
			}
		}
	}
	return time.Time{}
}

func unixTimestamp(value float64) time.Time {
	if value > 1e12 {
		return time.UnixMilli(int64(value))
	}
	return time.Unix(int64(value), 0)
}

func normalizeFieldName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "")
	name = strings.ReplaceAll(name, "-", "")
	return name
}

func (m *chatTUI) deleteSelectedSession() {
	sessions := m.filteredLocalSessions()
	if len(sessions) == 0 || m.sessionIndex >= len(sessions) {
		return
	}
	if err := m.sessionStore.Delete(sessions[m.sessionIndex].ID); err != nil {
		m.errText = "session delete failed: " + err.Error()
		return
	}
	m.sessions, _ = m.sessionStore.List()
	if len(m.sessions) == 0 {
		m.sessionIndex = 0
		return
	}
	if m.sessionIndex >= len(m.sessions) {
		m.sessionIndex = len(m.sessions) - 1
	}
}

func (m *chatTUI) resetSessionSelection() {
	m.sessionIndex = 0
	m.cloudIndex = 0
}

func (m chatTUI) filteredLocalSessions() []persistedSession {
	if strings.TrimSpace(m.sessionSearch) == "" {
		return m.sessions
	}
	query := strings.ToLower(strings.TrimSpace(m.sessionSearch))
	filtered := make([]persistedSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		if strings.Contains(strings.ToLower(session.Title), query) ||
			strings.Contains(strings.ToLower(session.ConversationID), query) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (m chatTUI) filteredCloudSessions() []cloudSession {
	if strings.TrimSpace(m.sessionSearch) == "" {
		return m.cloudSessions
	}
	query := strings.ToLower(strings.TrimSpace(m.sessionSearch))
	filtered := make([]cloudSession, 0, len(m.cloudSessions))
	for _, session := range m.cloudSessions {
		if strings.Contains(strings.ToLower(session.title), query) ||
			strings.Contains(strings.ToLower(session.preview), query) ||
			strings.Contains(strings.ToLower(session.id), query) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

func (m *chatTUI) openModelPicker() {
	for i, key := range m.modelKeys {
		if key == m.modelKey {
			m.modelIndex = i
			break
		}
	}
	m.pickerOpen = true
}

func sortedModelKeys() []string {
	keys := make([]string, 0, len(models.ModelRegistry))
	for key := range models.ModelRegistry {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func computeChatLayout(width, height, composerLines int) chatLayout {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	composerHeight := minComposerHeight
	if composerLines+1 > composerHeight {
		composerHeight = composerLines + 1
	}
	if composerHeight > maxComposerHeight {
		composerHeight = maxComposerHeight
	}
	viewportHeight := height - headerHeight - footerHeight - composerMetaHeight - composerDividerHeight - composerHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	return chatLayout{width: width, height: height, composerHeight: composerHeight, viewportHeight: viewportHeight}
}

func (m *chatTUI) resize(width, height int) {
	layout := computeChatLayout(width, height, m.textarea.LineCount())
	m.width, m.height = layout.width, layout.height
	m.viewport.SetWidth(maxInt(1, layout.width-1))
	m.viewport.SetHeight(layout.viewportHeight)
	m.viewport.SoftWrap = true
	m.viewport.MouseWheelEnabled = true
	m.viewport.MouseWheelDelta = 3
	m.textarea.SetWidth(maxInt(1, layout.width-4))
	m.textarea.SetHeight(layout.composerHeight)
	m.refreshViewport(true)
}

func (m *chatTUI) refreshViewport(gotoBottom bool) {
	wasBottom := m.viewport.AtBottom()
	m.viewport.SetContent(m.renderChatMessages())
	if gotoBottom || wasBottom {
		m.viewport.GotoBottom()
	}
}

func (m chatTUI) renderChatMessages() string {
	if len(m.messages) == 0 {
		return lipgloss.NewStyle().Foreground(colorMuted).Italic(true).Render("Start chatting by sending a message.")
	}

	var b strings.Builder
	for i, message := range m.messages {
		if i > 0 {
			b.WriteString("\n\n")
		}

		label := "Copilot"
		labelColor := colorOrange
		if message.role == "You" {
			label = "You"
			labelColor = colorCyan
		} else if m.busy && i == len(m.messages)-1 {
			label += " · responding"
		}

		labelView := lipgloss.NewStyle().Foreground(labelColor).Bold(true).Render(label)
		bodyView := lipgloss.NewStyle().Foreground(colorText).Render(message.content)
		accent := colorOrange
		if message.role == "You" {
			accent = colorCyan
		}
		block := labelView + "\n" + bodyView
		block = lipgloss.NewStyle().
			BorderLeft(true).
			BorderLeftForeground(accent).
			PaddingLeft(1).
			Width(maxInt(1, m.width-2)).
			Render(block)
		b.WriteString(block)
	}
	return b.String()
}

func (m chatTUI) renderChatScrollbar() string {
	height := maxInt(1, m.viewport.Height())
	thumbHeight := maxInt(1, height/5)
	start := int(m.viewport.ScrollPercent() * float64(height-thumbHeight))
	var b strings.Builder
	for row := 0; row < height; row++ {
		if row > 0 {
			b.WriteByte('\n')
		}
		if row >= start && row < start+thumbHeight {
			b.WriteString(lipgloss.NewStyle().Foreground(colorCyan).Render("┃"))
		} else {
			b.WriteString(lipgloss.NewStyle().Foreground(colorMuted).Render("│"))
		}
	}
	return b.String()
}

func (m chatTUI) View() tea.View {
	if m.sessionPickerOpen {
		v := tea.NewView(m.renderSessionPicker())
		v.AltScreen = true
		v.MouseMode = tea.MouseModeAllMotion
		return v
	}
	if m.pickerOpen {
		v := tea.NewView(m.renderModelPicker())
		v.AltScreen = true
		v.MouseMode = tea.MouseModeAllMotion
		return v
	}

	content := lipgloss.JoinVertical(
		lipgloss.Top,
		m.renderHeader(),
		lipgloss.JoinHorizontal(lipgloss.Top, m.viewport.View(), m.renderChatScrollbar()),
		m.renderComposerDivider(),
		m.renderComposer(),
		m.renderFooter(),
	)
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.WindowTitle = "M365 Copilot"
	return v
}

func (m chatTUI) renderComposerDivider() string {
	return lipgloss.NewStyle().
		Foreground(colorCyan).
		Bold(true).
		Render(strings.Repeat("━", maxInt(1, m.width)))
}

func (m chatTUI) renderHeader() string {
	title := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("M365 Copilot")
	version := lipgloss.NewStyle().Foreground(colorMuted).Render(" v" + models.Version)
	model := lipgloss.NewStyle().Foreground(lipgloss.Color("#07131a")).Background(colorCyan).Bold(true).Padding(0, 1).Render(m.modelKey)
	text := title + version + "    " + lipgloss.NewStyle().Foreground(colorMuted).Render("Model") + " " + model
	return lipgloss.NewStyle().Width(m.width).Render(text) + "\n" + lipgloss.NewStyle().Foreground(colorCyan).Render(strings.Repeat("─", m.width))
}

func (m chatTUI) renderComposer() string {
	inputColor := colorInput
	borderColor := colorCyan
	if m.busy {
		inputColor = colorAgent
		borderColor = colorOrange
	}
	input := lipgloss.NewStyle().
		Foreground(colorText).
		Background(inputColor).
		BorderLeft(true).
		BorderLeftForeground(borderColor).
		Padding(0, 1).
		Width(maxInt(1, m.width-2)).
		Render(m.textarea.View())
	metaText := fmt.Sprintf("%s  ·  Enter Send  ·  Shift+Enter New line  ·  Ctrl+J New line", m.modelKey)
	if m.busy {
		metaText = "Copilot is responding  ·  input locked"
	}
	meta := lipgloss.NewStyle().Foreground(colorMuted).Render(metaText)
	return input + "\n" + meta
}

func (m chatTUI) renderFooter() string {
	status := m.status
	if status == "" {
		status = "Ready"
	}
	if m.errText != "" {
		status += ": " + m.errText
	}
	shortcutsText := "Ctrl+N New chat   Ctrl+P Sessions   Ctrl+M Model   Ctrl+C Clear   Ctrl+D Quit"
	if m.busy {
		shortcutsText = "Esc Stop response"
	}
	shortcuts := lipgloss.NewStyle().Foreground(colorMuted).Render(shortcutsText)
	statusView := lipgloss.NewStyle().Foreground(colorBlue).Render(status)
	return lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", m.width)) + "\n" + shortcuts + "    " + statusView
}

func (m chatTUI) renderSessionPicker() string {
	width := 72
	if m.width-modelPickerPadding < width {
		width = m.width - modelPickerPadding
	}
	if width < 28 {
		width = 28
	}

	source := m.sessionSource
	if source == "" {
		source = "local"
	}
	lines := []string{"Sessions  [l] Local  [m] M365", ""}
	searchLabel := "Search: " + m.sessionSearch
	if m.sessionSearchActive {
		searchLabel = "Search▌: " + m.sessionSearch
	}
	if m.sessionSearch == "" {
		if m.sessionSearchActive {
			searchLabel += "type to filter"
		} else {
			searchLabel += "press / to search"
		}
	}
	lines = append(lines, searchLabel, "")
	if source == "cloud" {
		sessions := m.filteredCloudSessions()
		if m.cloudLoading {
			lines = append(lines, "Loading M365 chats...")
		} else if len(sessions) == 0 {
			lines = append(lines, "No M365 chats found.")
		} else {
			start, end := visibleSessionRange(m.cloudIndex, len(sessions), maxInt(1, m.height/3))
			if start > 0 {
				lines = append(lines, "  ↑ more chats above")
			}
			for i := start; i < end; i++ {
				session := sessions[i]
				prefix := "  "
				if i == m.cloudIndex {
					prefix = "▸ "
				}
				lines = append(lines, prefix+session.title, "    M365 · "+formatSessionAge(session.updatedAt))
			}
			if end < len(sessions) {
				lines = append(lines, "  ↓ more chats below")
			}
		}
	} else {
		sessions := m.filteredLocalSessions()
		if len(sessions) == 0 {
			lines = append(lines, "No saved local sessions yet.")
		} else {
			start, end := visibleSessionRange(m.sessionIndex, len(sessions), maxInt(1, m.height/3))
			if start > 0 {
				lines = append(lines, "  ↑ more sessions above")
			}
			for i := start; i < end; i++ {
				session := sessions[i]
				prefix := "  "
				if i == m.sessionIndex {
					prefix = "▸ "
				}
				title := session.Title
				if title == "" {
					title = "Untitled session"
				}
				model := session.Model
				if model == "" {
					model = "auto"
				}
				lines = append(lines, prefix+title, "    "+model+" · "+formatSessionAge(session.UpdatedAt))
			}
			if end < len(sessions) {
				lines = append(lines, "  ↓ more sessions below")
			}
		}
	}
	if source == "cloud" {
		lines = append(lines, "", "↑/↓ select   Enter continue   / search   r refresh   Esc close")
	} else {
		lines = append(lines, "", "↑/↓ select   Enter resume   / search   n new   d delete   Esc close")
	}
	box := lipgloss.NewStyle().Width(width).Padding(1, 2).Border(lipgloss.RoundedBorder()).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func visibleSessionRange(selected, total, maxItems int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if maxItems < 1 {
		maxItems = 1
	}
	if maxItems > total {
		maxItems = total
	}
	start := selected - maxItems/2
	if start < 0 {
		start = 0
	}
	if start+maxItems > total {
		start = total - maxItems
	}
	return start, start + maxItems
}

func formatSessionAge(updatedAt time.Time) string {
	if updatedAt.IsZero() {
		return "unknown time"
	}
	age := time.Since(updatedAt)
	if age < time.Minute {
		return "just now"
	}
	if age < time.Hour {
		return fmt.Sprintf("%d min ago", int(age/time.Minute))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%d hr ago", int(age/time.Hour))
	}
	if age < 48*time.Hour {
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", int(age/(24*time.Hour)))
}

func (m chatTUI) renderModelPicker() string {
	width := 48
	if m.width-modelPickerPadding < width {
		width = m.width - modelPickerPadding
	}
	if width < 20 {
		width = 20
	}

	lines := []string{"Select model", ""}
	for i, key := range m.modelKeys {
		prefix := "  "
		if i == m.modelIndex {
			prefix = "▸ "
		}
		lines = append(lines, prefix+key)
	}
	lines = append(lines, "", "↑/↓ select   Enter apply   Esc cancel")
	box := lipgloss.NewStyle().Width(width).Padding(1, 2).Border(lipgloss.RoundedBorder()).Render(strings.Join(lines, "\n"))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
