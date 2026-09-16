package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/unix"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func (h *host) enqueueClient(client *attachClient, message serverMessage) bool {
	select {
	case <-client.done:
		return false
	default:
	}
	select {
	case client.out <- message:
		return true
	default:
		log.Diagnostic(log.DiagnosticEvent{
			Event: "slow_client.eviction", Session: h.name, ClientID: client.id,
			Protocol: int(client.version), Role: string(client.assignedRole), Reason: "output_backpressure",
		})
		h.evictSlowClient(client)
		return false
	}
}

func (h *host) roleMessage(client *attachClient, reason string) serverMessage {
	return roleMessageFor(client.id, client.assignedRole, client.generation, reason)
}

func roleMessageFor(clientID string, role clientRole, generation uint64, reason string) serverMessage {
	payload, err := json.Marshal(roleEvent{
		Type: "role", ClientID: clientID, Role: role, Generation: generation, Reason: reason,
	})
	if err != nil {
		return serverMessage{kind: serverFrameControl}
	}
	return serverMessage{kind: serverFrameControl, payload: payload}
}

func (h *host) enqueueRoleChanges(changes []roleChange) {
	for _, change := range changes {
		if change.client.version == protocolV2 {
			h.enqueueClient(change.client, roleMessageFor(change.clientID, change.role, change.generation, change.reason))
		}
	}
}

func (h *host) writeControllerInput(client *attachClient, generation uint64, payload []byte) error {
	if err := h.writeInput(client, generation, payload); err != nil {
		return fmt.Errorf("write controller input: %w", err)
	}
	return nil
}

func (h *host) writeOutOfBandInput(payload []byte) error {
	if err := h.writeInput(nil, 0, payload); err != nil {
		return fmt.Errorf("write out-of-band input: %w", err)
	}
	return nil
}

// submitSettleDelay separates typed text from the Enter that submits it.
// Codex and Copilot treat bytes that arrive in one read as a paste and insert
// a trailing CR as a newline instead of submitting the prompt.
const submitSettleDelay = 200 * time.Millisecond

// splitSubmit separates a line into the text to type and the Enter that
// submits it. A bare Enter or text without one is written as-is.
func splitSubmit(payload []byte) (text, enter []byte) {
	if last := len(payload) - 1; last > 0 && payload[last] == '\r' {
		return payload[:last], payload[last:]
	}
	return payload, nil
}

// rawInputTimeout bounds the wait for a starting provider to take the
// terminal. It must stay under the client's call timeout; a provider that
// never leaves canonical mode receives the line when the wait ends.
const rawInputTimeout = 5 * time.Second

// writeOutOfBandLine types payload into the provider, pausing before a
// trailing Enter so the composer sees a keypress rather than a paste. With
// awaitRaw it first waits for the provider to switch the terminal out of
// canonical mode: until then the line discipline, not the composer, owns the
// input and rewrites the submitting CR into a newline (ICRNL).
func (h *host) writeOutOfBandLine(payload []byte, awaitRaw bool) error {
	if awaitRaw {
		h.awaitRawInput(rawInputTimeout)
	}
	text, enter := splitSubmit(payload)
	if err := h.writeOutOfBandInput(text); err != nil {
		return err
	}
	if enter == nil {
		return nil
	}
	time.Sleep(submitSettleDelay)
	return h.writeOutOfBandInput(enter)
}

const inputWriteTimeout = 250 * time.Millisecond

func (h *host) writeInput(client *attachClient, generation uint64, payload []byte) error {
	deadline := time.Now().Add(inputWriteTimeout)
	// Preserve frame ordering without making controls wait on provider input.
	for !h.inputMu.TryLock() {
		if time.Now().After(deadline) {
			return errors.New("provider input backpressure")
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer h.inputMu.Unlock()
	for {
		h.controlMu.Lock()
		h.mu.Lock()
		accepted := client == nil && h.registry.controller == nil || client != nil && h.registry.acceptsControl(client, generation)
		if !accepted {
			h.mu.Unlock()
			h.controlMu.Unlock()
			if client == nil {
				return &SessionBusyError{Operation: opSend}
			}
			return nil
		}
		// The nonblocking syscall and ownership check share the control lock;
		// no pending bytes can be committed after a control transfer.
		n, err := writePTYNonblocking(h.ptmx, payload)
		h.mu.Unlock()
		h.controlMu.Unlock()
		if n > 0 {
			payload = payload[n:]
			// Bound a stalled provider, not a paste that is still progressing.
			deadline = time.Now().Add(inputWriteTimeout)
		}
		if len(payload) == 0 {
			return err
		}
		if err != nil && !errors.Is(err, unix.EAGAIN) && !errors.Is(err, unix.EINTR) {
			return err
		}
		if time.Now().After(deadline) {
			return errors.New("provider input backpressure")
		}
		if n <= 0 {
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func (h *host) resizeOutOfBand(size terminalSize) error {
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.registry.controller != nil {
		return &SessionBusyError{Operation: opResize}
	}
	h.applyResizeLocked(size.cols, size.rows)
	return nil
}

func (h *host) resizeClient(client *attachClient, generation uint64, size terminalSize) {
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	h.mu.Lock()
	reason := h.registry.resizeReason(client, generation, size)
	accepted := h.registry.updateSize(client, generation, size)
	if accepted && h.term != nil {
		h.term.Resize(size.cols, size.rows)
	}
	h.mu.Unlock()
	event := "resize.ignored"
	if accepted {
		event = "resize.accepted"
	}
	log.Diagnostic(log.DiagnosticEvent{
		Event: event, Session: h.name, ClientID: client.id, Protocol: int(client.version),
		Role: string(client.assignedRole), Reason: reason,
	})
	if accepted {
		h.applyPTYSize(size)
	}
}

func (h *host) handleRoleCommand(client *attachClient, payload []byte) bool {
	var command roleCommand
	if err := json.Unmarshal(payload, &command); err != nil || command.validate() != nil {
		return false
	}
	if command.Action == actionRequestControl {
		h.notifyControlRequest(client)
		return true
	}

	h.controlMu.Lock()
	h.mu.Lock()
	changes := h.registry.transfer(client)
	var controllerSize terminalSize
	var controller *attachClient
	var repaint []byte
	if len(changes) > 0 && h.registry.controller != nil {
		controller = h.registry.controller
		controllerSize = controller.latestSize
		if controllerSize.valid() && h.term != nil {
			h.term.Resize(controllerSize.cols, controllerSize.rows)
		}
		if controller.ready && h.term != nil {
			repaint = h.term.Redraw()
		}
	}
	h.mu.Unlock()
	if len(changes) > 0 && controllerSize.valid() {
		h.applyPTYSize(controllerSize)
	}
	h.controlMu.Unlock()
	h.enqueueRoleChanges(changes)
	for _, change := range changes {
		log.Diagnostic(log.DiagnosticEvent{
			Event: "role.transfer", Session: h.name, ClientID: change.clientID,
			Protocol: int(change.client.version), Role: string(change.role), Reason: change.reason,
		})
	}
	if controller != nil && len(repaint) > 0 {
		h.enqueueClient(controller, serverMessage{kind: serverFramePTY, payload: repaint})
	}
	return true
}

// notifyControlRequest tells the current controller that another client is
// waiting for the session. Transfer stays controller-owned, so the request is
// only ever a notice — but until it was delivered, `prefix r` printed
// "control requested" to the asker and reached nobody at all.
//
// It is delivered as terminal output rather than a control event: the client
// rejects control events it does not know, and a viewer old enough not to know
// this one would drop its connection instead of ignoring it.
func (h *host) notifyControlRequest(client *attachClient) {
	h.mu.Lock()
	controller := h.registry.controller
	requesterID := client.id
	var size terminalSize
	if controller != nil {
		size = controller.latestSize
	}
	h.mu.Unlock()
	if controller == nil || controller == client || !controller.ready {
		return
	}
	notice := paintStatus(size.cols, size.rows, requesterID+" requested control; prefix o transfers it")
	h.enqueueClient(controller, serverMessage{kind: serverFramePTY, payload: []byte(notice)})
	log.Diagnostic(log.DiagnosticEvent{
		Event: "control.requested", Session: h.name, ClientID: requesterID,
		Protocol: int(client.version), Role: string(client.assignedRole), Reason: "control_requested",
	})
}

func (h *host) awaitRawInput(timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for h.inputIsCanonical() && time.Now().Before(deadline) {
		select {
		case <-h.exited:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// inputIsCanonical reports whether the child's terminal still cooks input.
// The master shares the slave's termios, so no slave descriptor is kept.
func (h *host) inputIsCanonical() bool {
	if h.ptmx == nil {
		return false
	}
	raw, err := h.ptmx.SyscallConn()
	if err != nil {
		return false
	}
	canonical := false
	_ = raw.Control(func(fd uintptr) {
		attrs, err := unix.IoctlGetTermios(int(fd), ioctlReadTermios) // #nosec G115 -- descriptor from the runtime
		canonical = err == nil && attrs.Lflag&unix.ICANON != 0
	})
	return canonical
}

func (h *host) applyPTYSize(size terminalSize) {
	if h.ptmx == nil || !size.valid() {
		return
	}
	raw, err := h.ptmx.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(size.cols), Row: uint16(size.rows)}) // #nosec G115 -- bounds checked above
	})
}
