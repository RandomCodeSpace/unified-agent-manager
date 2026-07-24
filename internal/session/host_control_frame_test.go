package session

import "testing"

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
