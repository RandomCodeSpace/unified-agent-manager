package app

import (
	"fmt"
	"image"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

type confirmationIntent uint8

const (
	confirmationNone confirmationIntent = iota
	confirmationStop
	confirmationRemove
	confirmationRestart
	confirmationLatest
)

type confirmationFormMsg struct{ inner tea.Msg }

type confirmationPointerIntent struct {
	generation uint64
	key        rune
}

type confirmationButtonSpan struct {
	minX, maxX int
	y          int
	key        rune
}

type confirmationOverlayFrame struct {
	content     string
	generation  uint64
	modalBounds image.Rectangle
	spans       []confirmationButtonSpan
}

func (m Model) confirmationActive() bool {
	return m.confirmStop || m.confirmLatest
}

func (m *Model) openSessionConfirmation(sess adapter.Session) {
	m.confirmStop = true
	m.confirmStopAgent = sess.AgentType
	m.confirmStopID = sess.ID
	m.confirmLatest = false
	m.confirmLatestAction = ""
	m.confirmLatestAgent = ""
	m.confirmLatestID = ""
	m.confirmLatestName = ""
	m.confirmName = displaytext.Sanitize(firstNonEmpty(sess.DisplayName, sess.ID, "session"))
	m.confirmSignature = sessionActionSignature(sess)
	if sessionIsRunning(sess) {
		m.confirmIntent = confirmationStop
		m.confirmAffirmative = "Stop session"
	} else {
		m.confirmIntent = confirmationRemove
		m.confirmAffirmative = "Remove session"
	}
	m.buildConfirmationForm()
}

func (m *Model) openLatestConfirmation(action latestAction, agentName, id, name string) {
	m.confirmLatest = true
	m.confirmLatestAction = action
	m.confirmLatestAgent = agentName
	m.confirmLatestID = id
	m.confirmLatestName = name
	m.confirmStop = false
	m.confirmStopAgent = ""
	m.confirmStopID = ""
	m.confirmIntent = confirmationLatest
	m.confirmName = displaytext.Sanitize(firstNonEmpty(name, id, "session"))
	m.confirmAffirmative = "Continue"
	if sess, ok := m.sessionByIdentity(agentName, id); ok {
		m.confirmSignature = sessionActionSignature(sess)
	} else {
		m.confirmSignature = ""
	}
	m.message = ""
	m.messageSetAt = time.Time{}
	m.buildConfirmationForm()
}

func sessionIsRunning(sess adapter.Session) bool {
	return sess.ProcAlive == adapter.Alive || (sess.ProcAlive == "" && sess.State == adapter.Active)
}

func (m *Model) ensureConfirmationForm() {
	if m.confirmForm != nil || !m.confirmationActive() {
		return
	}
	if m.confirmStop {
		sess, ok := m.sessionByIdentity(m.confirmStopAgent, m.confirmStopID)
		if !ok {
			sess = adapter.Session{AgentType: m.confirmStopAgent, ID: m.confirmStopID, DisplayName: m.confirmName, ProcAlive: adapter.Alive}
		}
		m.openSessionConfirmation(sess)
		return
	}
	m.openLatestConfirmation(m.confirmLatestAction, m.confirmLatestAgent, m.confirmLatestID, m.confirmLatestName)
}

func (m *Model) buildConfirmationForm() {
	value := new(bool)
	*value = false
	title, description := m.confirmationCopy()
	field := huh.NewConfirm().
		Title(title).
		Description(description).
		Affirmative(m.confirmAffirmative).
		Negative("Cancel").
		Value(value)
	keys := huh.NewDefaultKeyMap()
	keys.Quit.SetKeys("esc")
	destructive := m.confirmIntent == confirmationStop || m.confirmIntent == confirmationRemove
	form := huh.NewForm(huh.NewGroup(field)).
		WithKeyMap(keys).
		WithTheme(huh.ThemeFunc(func(isDark bool) *huh.Styles {
			return confirmationTheme(isDark, destructive)
		})).
		WithShowHelp(false).
		WithShowErrors(true)
	m.confirmValue = value
	m.confirmField = field
	m.confirmForm = form
	m.confirmGeneration++
	// Init establishes Huh's field focus and viewport synchronously. Its
	// returned commands only request dynamic values and terminal size; both are
	// already supplied by this static embedded form.
	_ = form.Init()
	background := lipgloss.Color("#FFFFFF")
	if m.darkBackground {
		background = lipgloss.Color("#000000")
	}
	updated, _ := form.Update(tea.BackgroundColorMsg{Color: background})
	m.confirmForm = updated.(*huh.Form)
	m.resizeConfirmation()
}

func confirmationTheme(isDark, destructive bool) *huh.Styles {
	styles := huh.ThemeBase(isDark)
	base := lipgloss.NewStyle().
		BorderStyle(lipgloss.ThickBorder()).
		BorderLeft(true).
		BorderForeground(accentColor).
		PaddingLeft(1)
	button := lipgloss.NewStyle().Padding(0, 2).MarginRight(1)
	styles.Focused.Base = base
	buttonColor := accentColor
	styles.Focused.Title = titleStyle
	if destructive {
		buttonColor = failColor
		styles.Focused.Title = titleStyle.Foreground(failColor)
	}
	styles.Focused.Description = hintStyle
	styles.Focused.ErrorIndicator = warnStyle
	styles.Focused.ErrorMessage = warnStyle
	styles.Focused.FocusedButton = button.Bold(true).Foreground(actionTextColor).Background(buttonColor)
	styles.Focused.BlurredButton = button.Foreground(mutedColor)
	styles.Blurred = styles.Focused
	styles.Blurred.Base = base.BorderStyle(lipgloss.HiddenBorder())
	styles.Group.Title = titleStyle
	styles.Group.Description = hintStyle
	return styles
}

func (m Model) confirmationCopy() (string, string) {
	warning := "⚠ "
	if asciiGlyphs() {
		warning = "! "
	}
	name := firstNonEmpty(m.confirmName, m.confirmStopID, m.confirmLatestID, "session")
	switch m.confirmIntent {
	case confirmationRemove:
		return fmt.Sprintf("%sRemove %q?", warning, name), "This removes the managed session record. Press r to restart instead."
	case confirmationRestart:
		return fmt.Sprintf("Restart %q?", name), "This restarts the same managed session."
	case confirmationLatest:
		provider := displaytext.Sanitize(firstNonEmpty(m.confirmLatestAgent, "provider"))
		return "Continue with latest conversation?", fmt.Sprintf("Several retained conversations share %s and this workspace. Continuing %s for %q may select the provider's latest conversation.", provider, m.confirmLatestAction, name)
	default:
		return fmt.Sprintf("%sStop %q?", warning, name), "This stops the process and removes the managed session. Press r to restart instead."
	}
}

func (m Model) confirmationSafe() bool {
	return !m.sizeKnown || (m.width >= dashboardMinWidth && m.height >= dashboardMinHeight)
}

func (m *Model) resizeConfirmation() {
	if m.confirmForm == nil || m.confirmField == nil {
		return
	}
	label := m.confirmAffirmative
	if !m.confirmationSafe() {
		label = ""
	}
	m.confirmField.Affirmative(label)
	width := m.width - 4
	if !m.sizeKnown {
		width = 60
	}
	width = max(20, min(64, width))
	m.confirmForm.WithWidth(width)
	var resizeMsg tea.Msg = struct{}{}
	if m.sizeKnown {
		resizeMsg = tea.WindowSizeMsg{Width: width, Height: max(1, m.height-2)}
	}
	updated, _ := m.confirmForm.Update(resizeMsg)
	m.confirmForm = updated.(*huh.Form)
}

func (m Model) handleConfirmationKey(msg tea.KeyPressMsg, key string) (bool, tea.Model, tea.Cmd) {
	m.ensureConfirmationForm()
	if m.confirmForm == nil {
		return true, m, nil
	}
	if key == "r" && m.confirmStop {
		if !m.confirmationSafe() {
			return true, m, nil
		}
		m.confirmIntent = confirmationRestart
		m.confirmAffirmative = "Restart session"
		m.confirmField.Affirmative(m.confirmAffirmative)
		*m.confirmValue = false
		title, description := m.confirmationCopy()
		m.confirmField.Title(title).Description(description)
		model, cmd := m.updateConfirmationForm(tea.KeyPressMsg{Code: 'y', Text: "y"})
		return true, model, cmd
	}
	if !m.confirmationSafe() && (key == "y" || key == "Y") {
		return true, m, nil
	}
	model, cmd := m.updateConfirmationForm(msg)
	return true, model, cmd
}

func (m Model) handleConfirmationFormMsg(msg confirmationFormMsg) (tea.Model, tea.Cmd) {
	if !m.confirmationActive() || m.confirmForm == nil {
		return m, nil
	}
	return m.updateConfirmationForm(msg.inner)
}

func (m Model) handleConfirmationPointer(msg confirmationPointerIntent) (tea.Model, tea.Cmd) {
	if msg.generation != m.confirmGeneration || !m.confirmationActive() || m.confirmForm == nil {
		return m, nil
	}
	if msg.key == 'y' && !m.confirmationSafe() {
		return m, nil
	}
	return m.updateConfirmationForm(tea.KeyPressMsg{Code: msg.key, Text: string(msg.key)})
}

func (m Model) updateConfirmationForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.confirmForm.Update(msg)
	m.confirmForm = updated.(*huh.Form)
	switch m.confirmForm.State {
	case huh.StateAborted:
		m.clearConfirmation()
		return m, nil
	case huh.StateCompleted:
		accepted := m.confirmValue != nil && *m.confirmValue
		return m.completeConfirmation(accepted)
	default:
		return m, routeConfirmationCmd(cmd)
	}
}

func routeConfirmationCmd(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			routed := make(tea.BatchMsg, 0, len(batch))
			for _, child := range batch {
				if wrapped := routeConfirmationCmd(child); wrapped != nil {
					routed = append(routed, wrapped)
				}
			}
			return routed
		}
		return confirmationFormMsg{inner: msg}
	}
}

func (m Model) completeConfirmation(accepted bool) (tea.Model, tea.Cmd) {
	intent := m.confirmIntent
	agentName, id := m.confirmStopAgent, m.confirmStopID
	if intent == confirmationLatest {
		agentName, id = m.confirmLatestAgent, m.confirmLatestID
	}
	latest := m.confirmLatestAction
	if !accepted {
		m.clearConfirmation()
		return m, nil
	}
	if !m.confirmationSafe() || !m.confirmationTargetIsCurrent(agentName, id) {
		m.clearConfirmation()
		m.setMessage("Session changed; select it again.")
		return m, nil
	}
	m.clearConfirmation()
	switch intent {
	case confirmationRestart:
		return m, m.restartTargetExactCmd(agentName, id)
	case confirmationLatest:
		return m, m.retryLatestCmd(latest, agentName, id)
	default:
		return m, m.stopTargetExactCmd(agentName, id, true)
	}
}

func (m Model) confirmationTargetIsCurrent(agentName, id string) bool {
	sess, ok := m.sessionByIdentity(agentName, id)
	if !ok {
		return false
	}
	return m.confirmSignature == "" || sessionActionSignature(sess) == m.confirmSignature
}

func (m *Model) clearConfirmation() {
	m.confirmStop = false
	m.confirmStopAgent = ""
	m.confirmStopID = ""
	m.clearLatestConfirmation()
	m.confirmForm = nil
	m.confirmField = nil
	m.confirmValue = nil
	m.confirmIntent = confirmationNone
	m.confirmName = ""
	m.confirmSignature = ""
	m.confirmAffirmative = ""
}

func (m Model) buildConfirmationOverlay() confirmationOverlayFrame {
	m.ensureConfirmationForm()
	base := m.buildDashboardFrame().content
	if m.confirmForm == nil {
		return confirmationOverlayFrame{content: base}
	}
	modal := m.confirmForm.View()
	x := max(0, (m.width-lipgloss.Width(modal))/2)
	y := max(0, (m.height-lipgloss.Height(modal))/2)
	compositor := lipgloss.NewCompositor(
		lipgloss.NewLayer(base).Z(0),
		lipgloss.NewLayer(modal).ID("confirmation-form").X(x).Y(y).Z(100),
	)
	affirmative := m.confirmAffirmative
	if !m.confirmationSafe() {
		affirmative = ""
	}
	spans := confirmationButtonSpans(modal, affirmative, x, y)
	return confirmationOverlayFrame{
		content:     fitScreen(strings.Split(compositor.Render(), "\n"), max(1, m.width), max(0, m.height)),
		generation:  m.confirmGeneration,
		modalBounds: image.Rect(x, y, x+lipgloss.Width(modal), y+lipgloss.Height(modal)),
		spans:       spans,
	}
}

func confirmationButtonSpans(rendered, affirmativeLabel string, offsetX, offsetY int) []confirmationButtonSpan {
	plain := ansi.Strip(rendered)
	lines := strings.Split(plain, "\n")
	negativeLabel := "Cancel"
	// Huh deliberately does not expose labels. The affirmative label is one of
	// the application-owned values supplied to Huh; recover it from the rendered
	// button line rather than duplicating Huh's padding or alignment geometry.
	for lineIndex := len(lines) - 1; lineIndex >= 0; lineIndex-- {
		line := lines[lineIndex]
		negativeByte := strings.Index(line, negativeLabel)
		if negativeByte < 0 {
			continue
		}
		negativeStart := ansi.StringWidth(line[:negativeByte])
		negativeEnd := negativeStart + ansi.StringWidth(negativeLabel)
		negativeMin := max(0, negativeStart-2)
		spans := []confirmationButtonSpan{{minX: offsetX + negativeMin, maxX: offsetX + negativeEnd + 2, y: offsetY + lineIndex, key: 'n'}}
		if affirmativeLabel == "" {
			return spans
		}
		affirmativeByte := strings.Index(line, affirmativeLabel)
		if affirmativeByte < 0 {
			return spans
		}
		affirmativeStart := ansi.StringWidth(line[:affirmativeByte])
		affirmativeEnd := affirmativeStart + ansi.StringWidth(affirmativeLabel)
		midpoint := (affirmativeEnd + negativeStart) / 2
		spans = append(spans, confirmationButtonSpan{minX: offsetX + max(0, affirmativeStart-2), maxX: offsetX + midpoint, y: offsetY + lineIndex, key: 'y'})
		return spans
	}
	return nil
}

func (frame confirmationOverlayFrame) mouseCommand(msg tea.MouseMsg) tea.Cmd {
	click, ok := msg.(tea.MouseClickMsg)
	if !ok || click.Button != tea.MouseLeft {
		return nil
	}
	for _, span := range frame.spans {
		if click.Y == span.y && click.X >= span.minX && click.X < span.maxX {
			intent := confirmationPointerIntent{generation: frame.generation, key: span.key}
			return func() tea.Msg { return intent }
		}
	}
	if !image.Pt(click.X, click.Y).In(frame.modalBounds) {
		intent := confirmationPointerIntent{generation: frame.generation, key: 'n'}
		return func() tea.Msg { return intent }
	}
	return nil
}
