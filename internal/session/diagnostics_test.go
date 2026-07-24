package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

func TestAttachLifecycleStructuredLogs(t *testing.T) {
	// Given: a host registry with a controller and standby using protocol v2.
	var output bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	h := &host{name: "uam-codex-a1", registry: newClientRegistry()}
	controller := &attachClient{version: protocolV2, done: make(chan struct{}), out: make(chan serverMessage, 8)}
	standby := &attachClient{version: protocolV2, done: make(chan struct{}), out: make(chan serverMessage, 8)}

	// When: clients attach, control transfers, an invalid resize is ignored, and
	// the controller drops.
	if _, err := h.registerAttachClient(controller, clientRegistration{requestedRole: roleController, hello: validDiagnosticHello()}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.registerAttachClient(standby, clientRegistration{requestedRole: roleController, hello: validDiagnosticHello()}); err != nil {
		t.Fatal(err)
	}
	h.handleRoleCommand(controller, mustRoleCommand(t, actionTransferControl))
	h.resizeClient(controller, controller.generation, terminalSize{})
	h.dropClient(standby)
	fallbackHost := &host{name: "uam-codex-a2", registry: newClientRegistry()}
	fallbackClient := &attachClient{
		version: protocolV1, fallback: true, done: make(chan struct{}), out: make(chan serverMessage, 1),
	}
	if _, err := fallbackHost.registerAttachClient(fallbackClient, clientRegistration{requestedRole: roleController}); err != nil {
		t.Fatal(err)
	}
	uamlog.Diagnostic(uamlog.DiagnosticEvent{Event: "attach.negotiation", Session: h.name, Reason: "rejected"})
	uamlog.Diagnostic(uamlog.DiagnosticEvent{Event: "attach.negotiation", Session: h.name, Reason: "timeout"})

	// Then: retained events contain only stable lifecycle fields.
	logs := output.String()
	for _, event := range []string{"attach.negotiation", "attach.lifecycle", "role.assignment", "role.transfer", "resize.ignored", "role.promotion"} {
		if !strings.Contains(logs, `"event":"`+event+`"`) {
			t.Fatalf("missing %s in logs:\n%s", event, logs)
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(logs))
	for scanner.Scan() {
		var event map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"session", "client_id", "protocol", "role", "reason", "provider", "profile"} {
			if _, exists := event[field]; !exists {
				t.Fatalf("diagnostic field %q missing from %s", field, scanner.Text())
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
}

func validDiagnosticHello() clientHello {
	return clientHello{
		TTY: true,
		Capabilities: []clientCapability{
			capabilityFramedOutput,
			capabilityRoleEvents,
			capabilityLocalMouseFilter,
			capabilityOwnedScreen,
		},
	}
}

func mustRoleCommand(t *testing.T, action roleAction) []byte {
	t.Helper()
	payload, err := json.Marshal(roleCommand{Action: action})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
