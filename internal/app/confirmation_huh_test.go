package app

import (
	"image"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestDestructiveConfirmationUsesFailureColor(t *testing.T) {
	styles := confirmationTheme(false, true)
	if got := styles.Focused.Title.GetForeground(); !reflect.DeepEqual(got, failColor) {
		t.Fatalf("destructive title foreground = %#v, want failure color", got)
	}
	if got := styles.Focused.FocusedButton.GetBackground(); !reflect.DeepEqual(got, failColor) {
		t.Fatalf("destructive button background = %#v, want failure color", got)
	}

	styles = confirmationTheme(false, false)
	if got := styles.Focused.FocusedButton.GetBackground(); !reflect.DeepEqual(got, accentColor) {
		t.Fatalf("non-destructive button background = %#v, want accent color", got)
	}
}

func TestConfirmationRoutingRejectsNilAndStaleMessages(t *testing.T) {
	if routeConfirmationCmd(nil) != nil {
		t.Fatal("nil Huh command was wrapped")
	}
	routedNil := routeConfirmationCmd(func() tea.Msg { return nil })
	if routedNil == nil || routedNil() != nil {
		t.Fatal("nil Huh message was not discarded")
	}
	routedBatch := routeConfirmationCmd(func() tea.Msg {
		return tea.BatchMsg{func() tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }, nil}
	})
	batch, ok := routedBatch().(tea.BatchMsg)
	if !ok || len(batch) != 1 {
		t.Fatalf("routed Huh batch = %#v, want one non-nil child", batch)
	}

	m := modelWithTwoSessions()
	model, cmd := m.handleConfirmationFormMsg(confirmationFormMsg{inner: tea.KeyPressMsg{Code: tea.KeyEnter}})
	if cmd != nil || model.(Model).confirmationActive() {
		t.Fatal("inactive confirmation accepted a routed form message")
	}
	model, cmd = m.handleConfirmationPointer(confirmationPointerIntent{generation: 99, key: 'y'})
	if cmd != nil || model.(Model).confirmationActive() {
		t.Fatal("stale confirmation pointer activated an action")
	}

	m.openSessionConfirmation(m.sessions[0])
	m.confirmSignature = "stale"
	if m.confirmationTargetIsCurrent(m.sessions[0].AgentType, m.sessions[0].ID) {
		t.Fatal("changed confirmation target remained current")
	}
	if m.confirmationTargetIsCurrent("missing", "missing") {
		t.Fatal("missing confirmation target was accepted")
	}
}

// settleConfirmation emulates Bubble Tea's command loop only until Huh has
// finished its internal next-field/next-group sequence. The command returned
// after the form closes belongs to the existing application action and is left
// for the caller to inspect or execute.
func settleConfirmation(t *testing.T, m Model, cmd tea.Cmd) (Model, tea.Cmd) {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for step := 0; step < 32; step++ {
		if !m.confirmationActive() {
			if len(pending) == 0 {
				return m, nil
			}
			return m, pending[0]
		}
		if len(pending) == 0 || pending[0] == nil {
			t.Fatal("Huh confirmation remained open without a follow-up command")
		}
		current := pending[0]
		pending = pending[1:]
		msg := current()
		if batch, ok := msg.(tea.BatchMsg); ok {
			pending = append(batch, pending...)
			continue
		}
		model, next := m.Update(msg)
		m = model.(Model)
		if next != nil {
			pending = append(pending, next)
		}
	}
	t.Fatal("Huh confirmation did not settle")
	return m, nil
}

func TestHuhConfirmationDefaultsToCancel(t *testing.T) {
	m := modelWithTwoSessions()
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.openSessionConfirmation(m.sessions[0])
	if m.confirmValue == nil || *m.confirmValue {
		t.Fatal("destructive confirmation did not default to cancel")
	}
	view := m.View().Content
	if !strings.Contains(view, "Stop session") || !strings.Contains(view, "Cancel") {
		t.Fatalf("Huh buttons missing from confirmation overlay: %s", view)
	}
	model, cmd := m.handleKey(keyMsg("enter"))
	m, backend := settleConfirmation(t, model.(Model), cmd)
	if m.confirmationActive() || backend != nil {
		t.Fatal("Enter on the default Cancel choice executed a destructive action")
	}
}

func TestHuhConfirmationMouseButtonsAndShield(t *testing.T) {
	m := modelWithTwoSessions()
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.openSessionConfirmation(m.sessions[0])
	frame := m.buildConfirmationOverlay()
	var affirmative, negative *confirmationButtonSpan
	for i := range frame.spans {
		span := &frame.spans[i]
		switch span.key {
		case 'y':
			affirmative = span
		case 'n':
			negative = span
		}
	}
	if affirmative == nil || negative == nil {
		t.Fatalf("derived Huh button spans = %+v", frame.spans)
	}
	if cmd := frame.mouseCommand(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown}); cmd != nil {
		t.Fatal("modal wheel event was not swallowed")
	}
	if cmd := frame.mouseCommand(tea.MouseClickMsg{X: frame.modalBounds.Min.X, Y: frame.modalBounds.Min.Y, Button: tea.MouseLeft}); cmd != nil {
		t.Fatal("click inside the modal but outside its buttons was not swallowed")
	}
	cancelCmd := frame.mouseCommand(tea.MouseClickMsg{X: negative.minX, Y: negative.y, Button: tea.MouseLeft})
	model, follow := m.Update(cancelCmd())
	m, backend := settleConfirmation(t, model.(Model), follow)
	if m.confirmationActive() || backend != nil {
		t.Fatal("clicking Huh Cancel executed an action")
	}

	m.openSessionConfirmation(m.sessions[0])
	frame = m.buildConfirmationOverlay()
	for i := range frame.spans {
		if frame.spans[i].key == 'y' {
			affirmative = &frame.spans[i]
		}
	}
	acceptCmd := frame.mouseCommand(tea.MouseClickMsg{X: affirmative.minX, Y: affirmative.y, Button: tea.MouseLeft})
	model, follow = m.Update(acceptCmd())
	m, backend = settleConfirmation(t, model.(Model), follow)
	if m.confirmationActive() || backend == nil {
		t.Fatal("clicking the Huh affirmative button did not produce the existing stop command")
	}
}

func TestHuhConfirmationBackdropCancelsThroughForm(t *testing.T) {
	m := modelWithTwoSessions()
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.openSessionConfirmation(m.sessions[0])
	frame := m.buildConfirmationOverlay()
	outside := tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft}
	if image.Pt(outside.X, outside.Y).In(frame.modalBounds) {
		t.Fatalf("test backdrop point unexpectedly intersects modal bounds %v", frame.modalBounds)
	}
	cancelCmd := frame.mouseCommand(outside)
	if cancelCmd == nil {
		t.Fatal("left-clicking the backdrop did not emit semantic cancel")
	}
	intent, ok := cancelCmd().(confirmationPointerIntent)
	if !ok || intent.key != 'n' || intent.generation != m.confirmGeneration {
		t.Fatalf("backdrop intent = %#v, want current semantic n", intent)
	}
	model, follow := m.Update(intent)
	m, backend := settleConfirmation(t, model.(Model), follow)
	if m.confirmationActive() || backend != nil {
		t.Fatal("backdrop cancel did not close through Huh without a backend action")
	}
}

func TestHuhConfirmationResizeHidesDestructiveChoice(t *testing.T) {
	m := modelWithTwoSessions()
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	m.openSessionConfirmation(m.sessions[0])
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 39, Height: 11})
	if strings.Contains(m.View().Content, "Stop session") {
		t.Fatal("unsafe terminal still exposed the destructive submit target")
	}
	model, cmd := m.handleKey(keyMsg("y"))
	m = model.(Model)
	if cmd != nil || !m.confirmationActive() {
		t.Fatal("unsafe terminal accepted the destructive shortcut")
	}
	model, cmd = m.handleKey(keyMsg("esc"))
	if cmd != nil || model.(Model).confirmationActive() {
		t.Fatal("unsafe confirmation was not cancelable with Escape")
	}
}

func TestStoppedSessionConfirmationSaysRemove(t *testing.T) {
	m := modelWithTwoSessions()
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	stopped := adapter.Session{ID: "stopped", AgentType: "fake", DisplayName: "done", ProcAlive: adapter.Exited}
	m.sessions = []adapter.Session{stopped}
	m.openSessionConfirmation(stopped)
	view := m.View().Content
	if !strings.Contains(view, "Remove session") || strings.Contains(view, "Stop session") {
		t.Fatalf("stopped-session confirmation used the wrong action: %s", view)
	}
}
