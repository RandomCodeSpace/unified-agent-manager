package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/mattn/go-runewidth"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
)

// Real-terminal pointer checks for the dashboard (#63): the shipped binary
// runs on a PTY with SGR mouse reporting, against sessions whose provider is
// a fake codex on PATH, and every check reads the rendered screen rather than
// the model. Covered: row click selects only, the Attach button attaches,
// wheel moves the selection, the Stop confirmation's pointer buttons, resize
// to the 40x12 minimum, and the same flow through an ssh session.
//
//	UAM_E2E_BIN=$(pwd)/bin/uam go test ./internal/e2e/ -run TestE2EDashboard -v

const (
	dashCols = 100
	dashRows = 30
)

func TestE2EDashboardMouse(t *testing.T) { runDashboardMouse(t, false) }

func TestE2EDashboardMouseOverSSH(t *testing.T) {
	if err := exec.Command("ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", "localhost", "true").Run(); err != nil {
		t.Skipf("ssh to localhost without a prompt is unavailable: %v", err)
	}
	runDashboardMouse(t, true)
}

func runDashboardMouse(t *testing.T, viaSSH bool) {
	h := newHarness(t)
	h.env = append(h.env, "PATH="+fakeCodexDir(t, h)+":"+os.Getenv("PATH"))
	h.mustRun("dispatch", "codex", "#alpha")
	h.mustRun("dispatch", "codex", "#beta")

	d := startDashboard(t, h, viaSSH)
	d.mustAwait("both rows", func() bool { return d.row("alpha") >= 0 && d.row("beta") >= 0 })

	// A row click selects and does nothing else.
	target := "beta"
	if d.selectedRow() == d.row("beta") {
		target = "alpha"
	}
	col, row := d.locateOnRow(target, target)
	d.click(col, row)
	d.mustAwait(target+" selected by click", func() bool { return d.selectedRow() == d.row(target) })
	if d.seen.contains("[uam: role") {
		t.Fatalf("row click attached; screen:\n%s", d.screen())
	}

	// The explicit Attach button attaches on the first click even after the
	// periodic refresh has run since the last repaint; the prefix detaches.
	time.Sleep(3 * time.Second)
	col, row = d.locateOnRow(target, "Attach")
	d.click(col, row)
	d.mustAwait("attach client from the button", func() bool { return d.seen.contains("[uam: role") && d.seen.contains("FAKE CODEX") })
	d.send("\x02d")
	d.mustAwait("dashboard back after detach", func() bool { return d.row("alpha") >= 0 && d.row("beta") >= 0 && d.row("[uam: role") < 0 })

	// The wheel moves the selection inside the roster, whatever the order.
	first, second := "alpha", "beta"
	if d.row("beta") < d.row("alpha") {
		first, second = "beta", "alpha"
	}
	col, row = d.locateOnRow(first, first)
	d.click(col, row)
	d.mustAwait(first+" selected", func() bool { return d.selectedRow() == d.row(first) })
	d.wheel(col, row, false)
	d.mustAwait("wheel down selects "+second, func() bool { return d.selectedRow() == d.row(second) })
	d.wheel(col, row, true)
	d.mustAwait("wheel up selects "+first, func() bool { return d.selectedRow() == d.row(first) })

	// Stop opens the Huh confirmation; its pointer buttons cancel and confirm.
	col, row = d.locateOnRow("alpha", "alpha")
	d.click(col, row)
	d.mustAwait("alpha selected for stop", func() bool { return d.selectedRow() == d.row("alpha") })
	col, row = d.locate("Stop")
	d.click(col, row)
	d.mustAwait("stop confirmation", func() bool { return d.row("Stop session") >= 0 && d.row("Cancel") >= 0 })
	col, row = d.locate("Cancel")
	d.click(col, row)
	d.mustAwait("cancel closes the confirmation", func() bool { return d.row("Stop session") < 0 })
	if !strings.Contains(d.line(d.row("alpha")), "Running") {
		t.Fatalf("cancel stopped the session; screen:\n%s", d.screen())
	}
	col, row = d.locate("Stop")
	d.click(col, row)
	d.mustAwait("stop confirmation again", func() bool { return d.row("Stop session") >= 0 })
	col, row = d.locate("Stop session")
	d.click(col, row)
	// A stopped session either leaves the roster or stays as resumable; both
	// mean the confirm button reached the existing stop path.
	d.mustAwait("alpha stopped by the confirm button", func() bool {
		alpha := d.row("alpha")
		return d.row("Stop session") < 0 && (alpha < 0 || !strings.Contains(d.line(alpha), "Running"))
	})
	if !strings.Contains(d.line(d.row("beta")), "Running") {
		t.Fatalf("confirm stopped the wrong session; screen:\n%s", d.screen())
	}

	// The minimum geometry keeps identity, state and the primary action.
	d.resize(40, 12)
	d.mustAwait("40x12 layout", func() bool {
		line := d.line(d.row("beta"))
		return d.row("UAM") >= 0 && strings.Contains(line, "codex") && strings.Contains(line, "Running") && strings.Contains(line, "Attach")
	})

	d.send("\x03")
	d.requireExit()
}

// fakeCodexDir puts a codex on PATH that takes the alternate screen, echoes its
// argv and waits, so a dispatched session is a real host with a live child.
func fakeCodexDir(t *testing.T, h *harness) string {
	t.Helper()
	dir := filepath.Join(h.root, "shim")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nprintf '\\033[?1049h\\033[H'\nprintf 'FAKE CODEX %s\\n' \"$*\"\nwhile :; do sleep 1; done\n"
	if err := os.WriteFile(filepath.Join(dir, "codex"), []byte(script), 0o700); err != nil { // #nosec G306 -- executable shim
		t.Fatal(err)
	}
	return dir
}

type dashboard struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	seen *recorder
	mu   sync.Mutex
	term *vterm.Terminal
	fed  int
}

func startDashboard(t *testing.T, h *harness, viaSSH bool) *dashboard {
	t.Helper()
	var cmd *exec.Cmd
	if viaSSH {
		var remote []string
		for _, kv := range h.env {
			name, _, _ := strings.Cut(kv, "=")
			switch name {
			case "UAM_SESSION_DIR", "UAM_CONFIG_DIR", "UAM_CACHE_DIR", "TERM", "UAM_WIDE", "PATH":
				remote = append(remote, "'"+strings.ReplaceAll(kv, "'", `'\''`)+"'")
			}
		}
		cmd = exec.Command("ssh", "-tt", "-o", "BatchMode=yes", "localhost", "env "+strings.Join(remote, " ")+" "+h.bin) // #nosec G204 -- test fixture
	} else {
		cmd = exec.Command(h.bin) // #nosec G204 -- test fixture
		cmd.Env = h.env
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: dashCols, Rows: dashRows})
	if err != nil {
		t.Fatal(err)
	}
	d := &dashboard{t: t, cmd: cmd, ptmx: ptmx, seen: record(ptmx), term: vterm.New(dashCols, dashRows, 0)}
	h.captureSeq++
	capture := filepath.Join(h.captures, fmt.Sprintf("%02d-dashboard-ssh-%v.raw", h.captureSeq, viaSSH))
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		<-d.seen.done
		_ = os.WriteFile(capture, d.seen.bytes(), 0o600)
	})
	d.mustAwait("dashboard header", func() bool { return d.row("Agents") >= 0 })
	if !d.seen.contains("\x1b[?1006h") {
		t.Fatalf("dashboard did not enable SGR mouse reporting: %q", d.seen.tail())
	}
	return d
}

// lines renders everything seen so far through the emulator and returns the
// active screen, one string per row.
func (d *dashboard) lines() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw := d.seen.bytes()
	if len(raw) > d.fed {
		_, _ = d.term.Write(raw[d.fed:])
		d.fed = len(raw)
	}
	_, rows := d.term.Size()
	out := strings.Split(strings.TrimSuffix(d.term.Capture(rows), "\n"), "\n")
	for len(out) < rows {
		out = append(out, "")
	}
	return out
}

func (d *dashboard) screen() string { return strings.Join(d.lines(), "\n") }

func (d *dashboard) line(row int) string {
	if row < 0 {
		return ""
	}
	return d.lines()[row]
}

func (d *dashboard) row(text string) int {
	for i, line := range d.lines() {
		if strings.Contains(line, text) {
			return i
		}
	}
	return -1
}

// selectedRow is the row carrying the selection rail (glyph or ASCII fallback).
func (d *dashboard) selectedRow() int {
	for i, line := range d.lines() {
		if strings.HasPrefix(line, "|") || strings.HasPrefix(line, "▌") {
			return i
		}
	}
	return -1
}

// locate returns 1-based terminal coordinates of the first cell of text.
func (d *dashboard) locate(text string) (col, row int) {
	d.t.Helper()
	for i, line := range d.lines() {
		if before, _, found := strings.Cut(line, text); found {
			return runewidth.StringWidth(before) + 1, i + 1
		}
	}
	d.t.Fatalf("%q not on screen:\n%s", text, d.screen())
	return 0, 0
}

func (d *dashboard) locateOnRow(rowText, text string) (col, row int) {
	d.t.Helper()
	i := d.row(rowText)
	if i < 0 {
		d.t.Fatalf("row %q not on screen:\n%s", rowText, d.screen())
	}
	line := d.line(i)
	before, _, found := strings.Cut(line, text)
	if !found {
		d.t.Fatalf("%q not on row %q: %q", text, rowText, line)
	}
	return runewidth.StringWidth(before) + 1, i + 1
}

func (d *dashboard) send(keys string) {
	d.t.Helper()
	if _, err := d.ptmx.WriteString(keys); err != nil {
		d.t.Fatalf("write %q: %v", keys, err)
	}
}

// click sends an SGR left press and release at 1-based coordinates.
func (d *dashboard) click(col, row int) {
	d.send(fmt.Sprintf("\x1b[<0;%d;%dM\x1b[<0;%d;%dm", col, row, col, row))
}

func (d *dashboard) wheel(col, row int, up bool) {
	button := 65
	if up {
		button = 64
	}
	d.send(fmt.Sprintf("\x1b[<%d;%d;%dM", button, col, row))
}

func (d *dashboard) resize(cols, rows int) {
	d.t.Helper()
	if err := pty.Setsize(d.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil { // #nosec G115 -- test geometry
		d.t.Fatal(err)
	}
	d.mu.Lock()
	d.term.Resize(cols, rows)
	d.mu.Unlock()
}

func (d *dashboard) mustAwait(what string, cond func() bool) {
	d.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	d.t.Fatalf("%s: not reached within 15s; screen:\n%s", what, d.screen())
}

func (d *dashboard) requireExit() {
	d.t.Helper()
	done := make(chan error, 1)
	go func() { done <- d.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			d.t.Fatalf("dashboard exited with %v: %q", err, d.seen.tail())
		}
	case <-time.After(10 * time.Second):
		d.t.Fatalf("dashboard did not exit on Ctrl+C: %q", d.seen.tail())
	}
}
