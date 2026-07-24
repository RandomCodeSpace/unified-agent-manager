package session

import (
	"net"
	"strings"
	"testing"
	"unicode/utf8"
)

func testAttachClient(version protocolVersion) *attachClient {
	return &attachClient{version: version, out: make(chan serverMessage, 1), done: make(chan struct{})}
}

func validTestHello() clientHello {
	return clientHello{
		TTY:       true,
		TermHint:  "xterm-256color",
		ColorHint: "truecolor",
		Capabilities: []clientCapability{
			capabilityFramedOutput,
			capabilityRoleEvents,
			capabilityLocalMouseFilter,
			capabilityOwnedScreen,
		},
	}
}

func registerTestClient(t *testing.T, registry *clientRegistry, role clientRole, size terminalSize) *attachClient {
	t.Helper()
	client := testAttachClient(protocolV2)
	if err := registry.register(client, clientRegistration{requestedRole: role, hello: validTestHello(), size: size}); err != nil {
		t.Fatalf("register client: %v", err)
	}
	return client
}

func TestControllerAssignmentSingleWriter(t *testing.T) {
	registry := newClientRegistry()
	controller := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	standby := registerTestClient(t, registry, roleController, terminalSize{cols: 100, rows: 30})
	observer := registerTestClient(t, registry, roleObserver, terminalSize{cols: 120, rows: 40})

	if controller.id != "client-1" || controller.order != 1 || controller.assignedRole != roleController {
		t.Fatalf("first client = id %q order %d role %q", controller.id, controller.order, controller.assignedRole)
	}
	if standby.id != "client-2" || standby.order != 2 || standby.assignedRole != roleStandby {
		t.Fatalf("second client = id %q order %d role %q", standby.id, standby.order, standby.assignedRole)
	}
	if observer.id != "client-3" || observer.order != 3 || observer.assignedRole != roleObserver {
		t.Fatalf("observer = id %q order %d role %q", observer.id, observer.order, observer.assignedRole)
	}
	if !registry.acceptsControl(controller, controller.generation) || registry.acceptsControl(standby, standby.generation) || registry.acceptsControl(observer, observer.generation) {
		t.Fatal("exactly the assigned controller must own input and resize")
	}
}

func TestControllerTransferAtomic(t *testing.T) {
	registry := newClientRegistry()
	first := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	second := registerTestClient(t, registry, roleController, terminalSize{cols: 100, rows: 30})
	staleGeneration := first.generation

	changes := registry.transfer(first)

	if len(changes) != 2 || first.assignedRole != roleStandby || second.assignedRole != roleController {
		t.Fatalf("transfer changes = %#v, roles = %q/%q", changes, first.assignedRole, second.assignedRole)
	}
	if registry.acceptsControl(first, staleGeneration) || !registry.acceptsControl(second, second.generation) {
		t.Fatal("transfer accepted a stale owner generation or rejected the new owner")
	}
}

func TestControllerDisconnectPromotesOldestEligible(t *testing.T) {
	registry := newClientRegistry()
	first := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	second := registerTestClient(t, registry, roleController, terminalSize{cols: 90, rows: 25})
	third := registerTestClient(t, registry, roleStandby, terminalSize{cols: 100, rows: 30})

	registry.remove(first)
	if registry.controller != second {
		t.Fatalf("first promotion = %v, want oldest standby", registry.controller)
	}
	registry.remove(second)
	if registry.controller != third {
		t.Fatalf("second promotion = %v, want next standby", registry.controller)
	}
	registry.remove(third)
	if registry.controller != nil {
		t.Fatalf("controller after final drop = %v, want vacancy", registry.controller)
	}
}

func TestExplicitObserverNeverPromotes(t *testing.T) {
	registry := newClientRegistry()
	controller := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	observer := registerTestClient(t, registry, roleObserver, terminalSize{cols: 120, rows: 40})

	registry.remove(controller)

	if registry.controller != nil || observer.assignedRole != roleObserver {
		t.Fatalf("observer promoted: controller=%v role=%q", registry.controller, observer.assignedRole)
	}
}

func TestObserverInputAndTerminalRepliesAreIgnored(t *testing.T) {
	registry := newClientRegistry()
	registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	observer := registerTestClient(t, registry, roleObserver, terminalSize{cols: 120, rows: 40})

	if registry.acceptsControl(observer, observer.generation) {
		t.Fatal("observer stdin or terminal replies were accepted")
	}
}

func TestControllerResizeOwnership(t *testing.T) {
	registry := newClientRegistry()
	controller := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	standby := registerTestClient(t, registry, roleController, terminalSize{cols: 100, rows: 30})

	if !registry.updateSize(controller, controller.generation, terminalSize{cols: 81, rows: 25}) {
		t.Fatal("controller resize rejected")
	}
	if registry.updateSize(standby, standby.generation, terminalSize{cols: 101, rows: 31}) {
		t.Fatal("standby resize accepted")
	}
	if standby.latestSize != (terminalSize{cols: 101, rows: 31}) {
		t.Fatalf("standby latest valid size = %+v", standby.latestSize)
	}
	observer := registerTestClient(t, registry, roleObserver, terminalSize{cols: 120, rows: 40})
	if registry.updateSize(observer, observer.generation, terminalSize{cols: 121, rows: 41}) {
		t.Fatal("observer resize accepted")
	}
	if observer.latestSize != (terminalSize{cols: 120, rows: 40}) {
		t.Fatalf("observer resize was not discarded: %+v", observer.latestSize)
	}
}

func TestResizeDuringTransfer(t *testing.T) {
	registry := newClientRegistry()
	first := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	second := registerTestClient(t, registry, roleController, terminalSize{cols: 100, rows: 30})
	staleGeneration := first.generation
	registry.transfer(first)

	if registry.updateSize(first, staleGeneration, terminalSize{cols: 81, rows: 25}) {
		t.Fatal("stale controller resize crossed transfer generation")
	}
	if first.latestSize != (terminalSize{cols: 80, rows: 24}) {
		t.Fatalf("stale resize changed standby size: %+v", first.latestSize)
	}
	if !registry.updateSize(second, second.generation, terminalSize{cols: 101, rows: 31}) {
		t.Fatal("new controller resize rejected")
	}
}

func TestSlowControllerDropTriggersFailover(t *testing.T) {
	registry := newClientRegistry()
	controller := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
	standby := registerTestClient(t, registry, roleController, terminalSize{cols: 100, rows: 30})
	host := &host{registry: registry}
	controller.out <- serverMessage{kind: serverFramePTY, payload: []byte("blocked")}

	if host.enqueueClient(controller, serverMessage{kind: serverFramePTY, payload: []byte("overflow")}) {
		t.Fatal("full controller queue accepted output")
	}
	if registry.controller != standby {
		t.Fatalf("controller after backpressure = %v, want standby", registry.controller)
	}
}

func TestClientHelloValidation(t *testing.T) {
	valid := validTestHello()
	if err := validateClientHello(valid); err != nil {
		t.Fatalf("valid hello: %v", err)
	}
	tests := []clientHello{
		{TTY: true, Capabilities: []clientCapability{"unknown"}},
		{TTY: true, Capabilities: []clientCapability{capabilityRoleEvents, capabilityRoleEvents}},
		{TTY: true, TermHint: string(make([]byte, maxClientHintLen+1))},
		{TTY: true, ColorHint: string(make([]byte, maxClientHintLen+1))},
	}
	for _, hello := range tests {
		if err := validateClientHello(hello); err == nil {
			t.Fatalf("invalid hello accepted: %#v", hello)
		}
	}
}

func TestBoundedClientHintPreservesUTF8(t *testing.T) {
	hint := strings.Repeat("a", maxClientHintLen-1) + "é"
	bounded := boundedClientHint(hint)
	if len(bounded) > maxClientHintLen || !utf8.ValidString(bounded) {
		t.Fatalf("bounded hint is invalid: %q", bounded)
	}
}

func TestLegacyAttachFirstControllerAndExtraBusy(t *testing.T) {
	registry := newClientRegistry()
	first := testAttachClient(protocolV1)
	if err := registry.register(first, clientRegistration{requestedRole: roleController, size: terminalSize{cols: 80, rows: 24}}); err != nil {
		t.Fatalf("first v1 register: %v", err)
	}
	second := testAttachClient(protocolV1)
	if err := registry.register(second, clientRegistration{requestedRole: roleController}); err == nil {
		t.Fatal("extra v1 controller was not busy")
	}
}

func TestClientRegistryRejectsMalformedRoleAndSize(t *testing.T) {
	registry := newClientRegistry()
	if err := registry.register(testAttachClient(protocolV2), clientRegistration{requestedRole: clientRole("invalid"), hello: validTestHello()}); err == nil {
		t.Fatal("malformed role accepted")
	}
	client := testAttachClient(protocolV2)
	if err := registry.register(client, clientRegistration{requestedRole: roleController, hello: validTestHello(), size: terminalSize{cols: 1001, rows: 24}}); err != nil {
		t.Fatalf("invalid diagnostic size rejected attach: %v", err)
	}
	if client.latestSize.valid() {
		t.Fatalf("invalid size retained: %+v", client.latestSize)
	}
}

func TestDroppedClientConnectionClosesOnce(t *testing.T) {
	left, right := net.Pipe()
	client := testAttachClient(protocolV2)
	client.conn = left
	client.drop()
	client.drop()
	_ = right.Close()
}
