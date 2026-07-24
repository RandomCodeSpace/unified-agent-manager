package session

import (
	"bytes"
	"testing"
)

func TestAttachToggleMouseDisablesActiveTerminalMouseModes(t *testing.T) {
	// Given
	var output bytes.Buffer
	runtime := newAttachRuntime(attachRuntimeConfig{output: &output, mouseEnabled: true})
	frames := newAttachFrameWriter(&bytes.Buffer{}, protocolV2, "client-1", 1)

	// When
	err := runtime.runCommand(commandToggleMouse, frames)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte(mouseReset)) {
		t.Fatalf("toggle output = %q, want active mouse modes disabled", output.Bytes())
	}
	if runtime.mouseEnabled() {
		t.Fatal("mouse passthrough remained enabled after the toggle")
	}
}
