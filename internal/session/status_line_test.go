package session

import (
	"bytes"
	"strings"
	"testing"
)

// A notice used to append two newlines, which scrolled the agent's alternate
// screen with nothing to repaint it. With a geometry it now draws on the last
// row and puts the cursor back.
func TestStatusPaintsTheLastRowWithoutScrolling(t *testing.T) {
	got := paintStatus(80, 24, "role controller")

	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("status still moves the screen: %q", got)
	}
	for _, want := range []string{"\x1b7", "\x1b[24;1H", "\x1b[2K", "[uam: role controller]", "\x1b8"} {
		if !strings.Contains(got, want) {
			t.Fatalf("status %q lacks %q", got, want)
		}
	}
	if !strings.HasPrefix(got, "\x1b7") || !strings.HasSuffix(got, "\x1b8") {
		t.Fatalf("status must save and restore the cursor around itself: %q", got)
	}
}

// A notice longer than the terminal keeps all of its content, spread across
// the bottom rows. Cutting it would lose information — `prefix i` alone runs
// past 80 columns — and wrapping it would scroll.
func TestStatusWrapsAcrossBottomRowsWithoutLosingContent(t *testing.T) {
	message := "session uam-fake-11112222; client client-4; role controller; keys: prefix d detach, c interrupt, r request, o transfer, i info, m mouse"
	got := paintStatus(80, 24, message)

	if strings.Contains(got, "\n") || strings.Contains(got, "\r") {
		t.Fatalf("wrapped status still moves the screen: %q", got)
	}
	painted := ""
	for _, row := range strings.Split(got, "\x1b[2K")[1:] {
		painted += strings.TrimSuffix(strings.Split(row, "\x1b[")[0], "\x1b8")
	}
	for _, want := range []string{"[uam: session uam-fake-11112222", "i info, m mouse]"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("wrapped status lost %q: painted %q", want, painted)
		}
	}
	for _, line := range strings.Split(got, "\x1b[2K")[1:] {
		body := strings.TrimSuffix(strings.Split(line, "\x1b[")[0], "\x1b8")
		if runeCells(body) > 80 {
			t.Fatalf("row %q exceeds the terminal width", body)
		}
	}
	// The last painted row is the terminal's last row, so the notice sits at
	// the bottom rather than wherever the agent's cursor happened to be.
	if !strings.Contains(got, "\x1b[24;1H") {
		t.Fatalf("wrapped status does not end on the last row: %q", got)
	}
}

func TestStatusKeepsWideRunesWithinTheWidth(t *testing.T) {
	got := paintStatus(8, 10, "日本語テスト")
	for _, line := range strings.Split(got, "\x1b[2K")[1:] {
		body := strings.TrimSuffix(strings.Split(line, "\x1b[")[0], "\x1b8")
		if width := runeCells(body); width > 8 {
			t.Fatalf("wide-rune row is %d cells, want at most 8: %q", width, body)
		}
	}
}

// Without a geometry there is nothing to address, so the plain form stays.
func TestStatusFallsBackWithoutGeometry(t *testing.T) {
	for _, size := range []struct{ cols, rows int }{{0, 24}, {80, 0}, {0, 0}} {
		got := paintStatus(size.cols, size.rows, "hello")
		if got != "\r\n[uam: hello]\r\n" {
			t.Fatalf("paintStatus(%d,%d) = %q, want the plain form", size.cols, size.rows, got)
		}
	}
}

// The runtime uses its own terminal size when it has one.
func TestRuntimeStatusUsesTheViewerGeometry(t *testing.T) {
	var output bytes.Buffer
	runtime := newAttachRuntime(attachRuntimeConfig{
		output:       &output,
		terminalSize: func() (int, int, bool) { return 100, 30, true },
	})
	if err := runtime.writeStatus("role controller"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("\x1b[30;1H")) {
		t.Fatalf("status did not target the viewer's last row: %q", output.Bytes())
	}
}

func runeCells(s string) int {
	width := 0
	for _, r := range s {
		switch {
		case r >= 0x1100 && r <= 0x115F, r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7A3,
			r >= 0xF900 && r <= 0xFAFF, r >= 0xFF00 && r <= 0xFF60, r >= 0xFFE0 && r <= 0xFFE6:
			width += 2
		default:
			width++
		}
	}
	return width
}
