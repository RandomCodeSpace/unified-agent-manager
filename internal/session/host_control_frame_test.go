package session

import (
	"bytes"
	"testing"
)

func TestV2ControlFramesRequireOwnershipEpoch(t *testing.T) {
	host := &host{}
	client := testAttachClient(protocolV2)
	if _, _, valid := host.parseControlFrame(client, []byte("stdin")); valid {
		t.Fatal("v2 stdin without an ownership epoch was accepted")
	}
	if _, _, valid := host.parseResizeFrame(client, resizePayload(80, 24)); valid {
		t.Fatal("v2 resize without an ownership epoch was accepted")
	}
	if _, _, valid := host.parseResizeFrame(client, mustOwnedFramePayload(t, 1, []byte{0, 80, 0})); valid {
		t.Fatal("v2 resize with an invalid payload was accepted")
	}
}

func TestV1ControlFramesRemainUnwrapped(t *testing.T) {
	client := testAttachClient(protocolV1)
	client.generation = 3
	host := &host{}
	generation, payload, valid := host.parseControlFrame(client, []byte("legacy"))
	if !valid || generation != 3 || string(payload) != "legacy" {
		t.Fatalf("v1 control = (%d, %q, %t), want raw payload", generation, payload, valid)
	}
}

// `prefix r` used to reach nobody: the host accepted the frame and did
// nothing, while the asker was told "control requested".
func TestControlRequestReachesTheController(t *testing.T) {
	// Given a controller with a known geometry and a standby.
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	controller.ready = true
	standby := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	standby.ready = true

	// When the standby requests control.
	if !h.handleRoleCommand(standby, []byte(`{"action":"request_control"}`)) {
		t.Fatal("request_control was rejected")
	}

	// Then the controller is told, and roles are unchanged.
	select {
	case message := <-controller.out:
		if message.kind != serverFramePTY {
			t.Fatalf("notice frame kind = %d, want %d", message.kind, serverFramePTY)
		}
		if !bytes.Contains(message.payload, []byte("requested control")) ||
			!bytes.Contains(message.payload, []byte(standby.id)) {
			t.Fatalf("notice = %q, want the requester and the reason", message.payload)
		}
		if !bytes.Contains(message.payload, []byte("\x1b[24;1H")) {
			t.Fatalf("notice = %q, want it painted on the controller's last row", message.payload)
		}
	default:
		t.Fatal("the controller was never told about the request")
	}
	if h.registry.controller != controller || standby.assignedRole != roleStandby {
		t.Fatal("a request must not move control on its own")
	}
}

// The controller asking itself is not news.
func TestControlRequestFromTheControllerIsNotDelivered(t *testing.T) {
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	controller := registerTestClient(t, h.registry, roleController, terminalSize{cols: 80, rows: 24})
	controller.ready = true

	if !h.handleRoleCommand(controller, []byte(`{"action":"request_control"}`)) {
		t.Fatal("request_control was rejected")
	}
	select {
	case message := <-controller.out:
		t.Fatalf("controller notified of its own request: %q", message.payload)
	default:
	}
}
