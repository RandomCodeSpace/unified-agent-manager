package session

import (
	"errors"
	"net"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func attachRegistration(req request, version protocolVersion) (clientRegistration, error) {
	registration := clientRegistration{
		requestedRole: roleController,
		size:          terminalSize{cols: req.Cols, rows: req.Rows},
	}
	if version == protocolV1 {
		return registration, nil
	}
	if req.Hello == nil {
		return clientRegistration{}, errors.New("invalid client hello: missing hello")
	}
	registration.requestedRole = req.RequestedRole
	registration.hello = *req.Hello
	return registration, nil
}

func (h *host) registerAttachClient(client *attachClient, registration clientRegistration) (response, error) {
	h.mu.Lock()
	if err := h.registry.register(client, registration); err != nil {
		h.mu.Unlock()
		return response{}, err
	}
	attachResponse := response{OK: true, Data: h.label}
	if client.version == protocolV2 {
		attachResponse.Version = protocolV2
		attachResponse.ClientID = client.id
		attachResponse.AssignedRole = client.assignedRole
		attachResponse.Generation = client.generation
	}
	clientID := client.id
	protocol := int(client.version)
	role := string(client.assignedRole)
	fallback := client.fallback
	termHint := client.hello.TermHint
	h.mu.Unlock()
	reason := "selected"
	if fallback {
		reason = "fallback"
	}
	log.Diagnostic(log.DiagnosticEvent{
		Event: "attach.negotiation", Session: h.name, ClientID: clientID,
		Protocol: protocol, Role: role, Reason: reason, TermHint: termHint,
	})
	log.Diagnostic(log.DiagnosticEvent{
		Event: "attach.lifecycle", Session: h.name, ClientID: clientID,
		Protocol: protocol, Role: role, Reason: "attached",
	})
	log.Diagnostic(log.DiagnosticEvent{
		Event: "role.assignment", Session: h.name, ClientID: clientID,
		Protocol: protocol, Role: role, Reason: "assigned",
	})
	return attachResponse, nil
}

func writeAttachResponse(conn net.Conn, attachResponse response) error {
	return writeJSONLine(conn, attachResponse)
}

func (h *host) initializeAttachClient(client *attachClient, registration clientRegistration, label string) {
	h.controlMu.Lock()
	defer h.controlMu.Unlock()
	h.mu.Lock()
	controls := h.registry.acceptsControl(client, client.generation)
	nudgeSize := h.prepareInitialGeometry(registration.size, controls)
	if client.version == protocolV2 {
		client.out <- h.roleMessage(client, "assigned")
	}
	client.out <- serverMessage{kind: serverFramePTY, payload: append([]byte(titleSequence(label)), h.term.Redraw()...)}
	client.ready = true
	focusGained := controls && h.term.FocusReporting() && !h.providerFocused
	if focusGained {
		h.providerFocused = true
	}
	h.mu.Unlock()
	if focusGained {
		// The agent asked for focus events (?1004) but runs under a detached
		// host, so no terminal ever tells it a window gained focus. A
		// controller attaching is that event; without it agents that dim or
		// lock their input box while unfocused stay that way. Client
		// terminals that implement ?1004 may also send their own focus-in
		// once the replay enables the mode — a duplicate is harmless.
		h.writeFocusEvent(focusIn)
	}
	if !controls || !registration.size.valid() {
		return
	}
	if nudgeSize.valid() {
		h.applyPTYSize(nudgeSize)
	}
	h.applyPTYSize(registration.size)
}

func (h *host) prepareInitialGeometry(size terminalSize, controls bool) terminalSize {
	if !controls || !size.valid() {
		return terminalSize{}
	}
	currentCols, currentRows := h.term.Size()
	var nudgeSize terminalSize
	if size.cols == currentCols && size.rows == currentRows {
		if cols, rows, ok := resizeNudge(size.cols, size.rows); ok {
			nudgeSize = terminalSize{cols: cols, rows: rows}
		}
	}
	h.term.Resize(size.cols, size.rows)
	return nudgeSize
}
