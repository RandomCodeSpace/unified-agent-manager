package vterm

import (
	"bytes"
	"testing"
)

func TestReplayPreservesProviderModes(t *testing.T) {
	term := New(40, 4, 4000)
	providerModes := []byte("\x1b[?1h\x1b[?1000;1004;1006;2004h\x1b=provider")
	if _, err := term.Write(providerModes); err != nil {
		t.Fatal(err)
	}
	replay := term.Redraw()
	for _, want := range [][]byte{
		[]byte("\x1b[?1h"), []byte("\x1b[?1000h"), []byte("\x1b[?1004h"),
		[]byte("\x1b[?1006h"), []byte("\x1b[?2004h"), []byte("\x1b="),
	} {
		if !bytes.Contains(replay, want) {
			t.Fatalf("replay omitted provider mode %x: %x", want, replay)
		}
	}
}
