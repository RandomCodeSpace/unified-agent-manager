package session

import (
	"encoding/json"
	"fmt"

	"github.com/creack/pty"

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
		h.dropClientReason(client, "slow_client")
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
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	h.mu.Lock()
	accepted := h.registry.acceptsControl(client, generation)
	h.mu.Unlock()
	if !accepted {
		return nil
	}
	if _, err := h.ptmx.Write(payload); err != nil {
		return fmt.Errorf("write controller input: %w", err)
	}
	return nil
}

func (h *host) writeOutOfBandInput(payload []byte) error {
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	h.mu.Lock()
	busy := h.registry.controller != nil
	h.mu.Unlock()
	if busy {
		return &SessionBusyError{Operation: opSend}
	}
	if _, err := h.ptmx.Write(payload); err != nil {
		return fmt.Errorf("write out-of-band input: %w", err)
	}
	return nil
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

func (h *host) applyPTYSize(size terminalSize) {
	if h.ptmx == nil || !size.valid() {
		return
	}
	_ = pty.Setsize(h.ptmx, &pty.Winsize{Cols: uint16(size.cols), Rows: uint16(size.rows)}) // #nosec G115 -- bounds checked above
}
