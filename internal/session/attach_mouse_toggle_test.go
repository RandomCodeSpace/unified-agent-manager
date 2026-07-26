package session

import (
	"bytes"
	"strings"
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

// Turning passthrough back on has to re-emit the modes the provider set while
// they were being suppressed: the provider sent them once and never repeats
// them, so without the replay the toggle only ever worked one way.
func TestAttachToggleMouseRestoresProviderModes(t *testing.T) {
	// Given
	var output bytes.Buffer
	runtime := newAttachRuntime(attachRuntimeConfig{output: &output, mouseEnabled: true})
	frames := newAttachFrameWriter(&bytes.Buffer{}, protocolV2, "client-1", 1)
	filter := newAttachOutputFilterWithMouse(&output, runtime.mouseEnabled)
	runtime.setOutputFilter(filter)
	if _, err := filter.Write([]byte("\x1b[?1002h\x1b[?1006h")); err != nil {
		t.Fatal(err)
	}

	// When
	if err := runtime.runCommand(commandToggleMouse, frames); err != nil { // off
		t.Fatal(err)
	}
	if _, err := filter.Write([]byte("\x1b[?1003h")); err != nil { // suppressed upgrade
		t.Fatal(err)
	}
	output.Reset()
	if err := runtime.runCommand(commandToggleMouse, frames); err != nil { // on
		t.Fatal(err)
	}

	// Then
	if !runtime.mouseEnabled() {
		t.Fatal("second toggle did not re-enable mouse passthrough")
	}
	if want := "\x1b[?1003;1006h"; !strings.Contains(output.String(), want) {
		t.Fatalf("re-enable output = %q, want the live provider modes %q", output.String(), want)
	}
}

// The tracking levels are mutually exclusive, so a provider that turned mouse
// reporting off must not have it restored by a later toggle.
func TestAttachToggleMouseSkipsModesTheProviderDisabled(t *testing.T) {
	// Given
	var output bytes.Buffer
	runtime := newAttachRuntime(attachRuntimeConfig{output: &output, mouseEnabled: true})
	frames := newAttachFrameWriter(&bytes.Buffer{}, protocolV2, "client-1", 1)
	filter := newAttachOutputFilterWithMouse(&output, runtime.mouseEnabled)
	runtime.setOutputFilter(filter)
	if _, err := filter.Write([]byte("\x1b[?1002h\x1b[?1002l")); err != nil {
		t.Fatal(err)
	}
	output.Reset() // drop the forwarded provider bytes; only the toggle matters

	// When
	for range 2 {
		if err := runtime.runCommand(commandToggleMouse, frames); err != nil {
			t.Fatal(err)
		}
	}

	// Then
	if strings.Contains(output.String(), "1002h") {
		t.Fatalf("re-enable output = %q, want no restore of a mode the provider turned off", output.String())
	}
}

// Every list that suppresses, resets or replays mouse modes must agree, or a
// mode gets suppressed on the way in and left alive in the terminal on the way
// out.
func TestMouseModeSetsAgree(t *testing.T) {
	for _, mode := range mouseModes {
		if !attachMouseModes[mode] {
			t.Fatalf("mode %s is not suppressed by the output filter", mode)
		}
		if !strings.Contains(mouseReset, mode) {
			t.Fatalf("mode %s is not reset by mouseReset", mode)
		}
		if !strings.Contains(screenReset, mode) {
			t.Fatalf("mode %s is not torn down on detach", mode)
		}
	}
}
