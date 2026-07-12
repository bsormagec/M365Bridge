package servers

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/KilimcininKorOglu/M365Bridge/pkg/client"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/models"
	"github.com/KilimcininKorOglu/M365Bridge/pkg/payload"
)

const (
	headerHeight       = 2
	footerHeight       = 2
	composerMetaHeight = 1
	minComposerHeight  = 3
	maxComposerHeight  = 8
	defaultTUIWidth    = 80
	defaultTUIHeight   = 24
	modelPickerPadding = 4
)

var (
	colorCyan   = lipgloss.Color("#22d3ee")
	colorBlue   = lipgloss.Color("#60a5fa")
	colorOrange = lipgloss.Color("#f59e0b")
	colorMuted  = lipgloss.Color("#7d8590")
	colorText   = lipgloss.Color("#d7dde5")
	colorPanel  = lipgloss.Color("#151b21")
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
		cli:       cli,
		modelKey:  modelKey,
		noStream:  noStream,
		textarea:  ta,
		viewport:  viewport.New(),
		spinner:   sp,
		modelKeys: sortedModelKeys(),
		width:     defaultTUIWidth,
		height:    defaultTUIHeight,
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
			return m, nil
		}
		if msg.chunk.IsFinal {
			m.busy = false
			m.status = "Ready"
			m.stream = nil
			return m, nil
		}
		return m, readStreamChunk(msg.requestID, m.stream)

	case streamDoneMsg:
		if msg.requestID == m.requestID {
			m.busy = false
			m.status = "Ready"
		}
		return m, nil

	case streamErrorMsg:
		if msg.requestID == m.requestID {
			m.busy = false
			m.errText = msg.err.Error()
			m.status = "Request failed"
			m.stream = nil
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
		return m, nil
	}

	return m, nil
}

func (m *chatTUI) updateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if key == "ctrl+c" {
		return m, tea.Quit
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
	m.conversation = ""
	m.messages = nil
	m.busy = false
	m.status = ""
	m.errText = ""
	m.stream = nil
	m.refreshViewport(false)
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
	viewportHeight := height - headerHeight - footerHeight - composerMetaHeight - composerHeight
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	return chatLayout{width: width, height: height, composerHeight: composerHeight, viewportHeight: viewportHeight}
}

func (m *chatTUI) resize(width, height int) {
	layout := computeChatLayout(width, height, m.textarea.LineCount())
	m.width, m.height = layout.width, layout.height
	m.viewport.SetWidth(layout.width)
	m.viewport.SetHeight(layout.viewportHeight)
	m.viewport.SoftWrap = true
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
		}

		labelView := lipgloss.NewStyle().Foreground(labelColor).Bold(true).Render(label)
		bodyView := lipgloss.NewStyle().Foreground(colorText).Render(message.content)
		block := labelView + "\n" + bodyView
		if message.role == "You" {
			block = lipgloss.NewStyle().
				Foreground(colorText).
				Background(colorPanel).
				BorderLeft(true).
				BorderLeftForeground(colorCyan).
				Padding(0, 1).
				Width(maxInt(1, m.width-2)).
				Render(block)
		} else {
			block = lipgloss.NewStyle().PaddingLeft(2).Render(block)
		}
		b.WriteString(block)
	}
	return b.String()
}

func (m chatTUI) View() tea.View {
	if m.pickerOpen {
		v := tea.NewView(m.renderModelPicker())
		v.AltScreen = true
		return v
	}

	content := lipgloss.JoinVertical(
		lipgloss.Top,
		m.renderHeader(),
		m.viewport.View(),
		m.renderComposer(),
		m.renderFooter(),
	)
	v := tea.NewView(content)
	v.AltScreen = true
	v.WindowTitle = "M365 Copilot"
	return v
}

func (m chatTUI) renderHeader() string {
	title := lipgloss.NewStyle().Foreground(colorCyan).Bold(true).Render("M365 Copilot")
	version := lipgloss.NewStyle().Foreground(colorMuted).Render(" v" + models.Version)
	model := lipgloss.NewStyle().Foreground(lipgloss.Color("#07131a")).Background(colorCyan).Bold(true).Padding(0, 1).Render(m.modelKey)
	text := title + version + "    " + lipgloss.NewStyle().Foreground(colorMuted).Render("Model") + " " + model
	return lipgloss.NewStyle().Width(m.width).Render(text) + "\n" + lipgloss.NewStyle().Foreground(colorCyan).Render(strings.Repeat("─", m.width))
}

func (m chatTUI) renderComposer() string {
	input := lipgloss.NewStyle().
		Foreground(colorText).
		Background(colorInput).
		BorderLeft(true).
		BorderLeftForeground(colorCyan).
		Padding(0, 1).
		Width(maxInt(1, m.width-2)).
		Render(m.textarea.View())
	meta := lipgloss.NewStyle().Foreground(colorMuted).Render(fmt.Sprintf("%s  ·  Enter Send  ·  Shift+Enter New line  ·  Ctrl+J New line", m.modelKey))
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
	shortcuts := lipgloss.NewStyle().Foreground(colorMuted).Render("Ctrl+N New chat   Ctrl+M Model   Ctrl+C Quit")
	statusView := lipgloss.NewStyle().Foreground(colorBlue).Render(status)
	return lipgloss.NewStyle().Foreground(colorMuted).Render(strings.Repeat("─", m.width)) + "\n" + shortcuts + "    " + statusView
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
