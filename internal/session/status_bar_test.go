package session

import (
	"bytes"
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPaintStatusBarShape(t *testing.T) {
	got := paintStatusBar(30, 10, 9, "[uam: role controller]", true)
	wantOrder := []string{"\x1b7", "\x1b[1;9r", "\x1b[10;1H", "\x1b[0m\x1b[7m", "[uam: role controller]", "\x1b[0m", "\x1b8"}
	at := 0
	for _, part := range wantOrder {
		index := strings.Index(got[at:], part)
		if index < 0 {
			t.Fatalf("status bar %q lacks %q after offset %d", got, part, at)
		}
		at += index + len(part)
	}
	if cut := paintStatusBar(8, 10, 9, "[uam: role controller]", false); !strings.Contains(cut, "\x1b[7m[uam: ro\x1b[0m") {
		t.Fatalf("bar text longer than the row must be cut, not wrapped: %q", cut)
	}
	if strings.Contains(paintStatusBar(30, 10, 9, "short", false), "\x1b[1;9r") {
		t.Fatal("repaint without pin must not touch the scroll region")
	}
	if padded := paintStatusBar(10, 5, 4, "abc", false); !strings.Contains(padded, "abc       \x1b[0m") {
		t.Fatalf("bar must be padded to the full width: %q", padded)
	}
	for _, bad := range [][3]int{{0, 10, 9}, {20, 1, 0}, {20, 10, 10}} {
		if paintStatusBar(bad[0], bad[1], bad[2], "x", true) != "" {
			t.Fatalf("paintStatusBar(%v) must paint nothing without a usable reservation", bad)
		}
	}
}

func TestStatusBarTextSegmentsAndPriority(t *testing.T) {
	full := newAttachRuntime(attachRuntimeConfig{
		session: "uam-copilot-5b1e30f2", display: "smoke · copilot · 5b1e30f2", cwd: "~/projects/x", statusRows: 1,
		role: roleController, prefix: 0x02, mouseEnabled: true, profile: attachProfileSnapshot{effective: "focused"},
	})
	want := "[uam: role controller; smoke · copilot · 5b1e30f2; C-b d / C-Left detach; C-b i info; ~/projects/x; profile focused]"
	if got := full.statusBarText(200); got != want {
		t.Fatalf("bar = %q\nwant %q", got, want)
	}
	// Narrow terminals drop trailing segments whole rather than cutting one.
	if got := full.statusBarText(80); got != "[uam: role controller; smoke · copilot · 5b1e30f2; C-b d / C-Left detach]" {
		t.Fatalf("80-column bar = %q", got)
	}
	if got := full.statusBarText(40); got != "[uam: role controller; smoke · copilot · 5b1e30f2]" {
		t.Fatalf("40-column bar must keep role and identity = %q", got)
	}
	full.mouse.Store(false)
	if got := full.statusBarText(200); !strings.HasSuffix(got, "; profile focused; mouse off (C-b m)]") {
		t.Fatalf("mouse-off bar = %q", got)
	}
	none := newAttachRuntime(attachRuntimeConfig{session: "s", statusRows: 1, role: roleController, prefix: 0x01, mouseEnabled: true, profile: attachProfileSnapshot{effective: "none"}})
	if got := none.statusBarText(200); got != "[uam: role controller; s; C-a d / C-Left detach; C-a i info]" {
		t.Fatalf("unlabelled bar = %q", got)
	}
}

func TestStatusBarIdentityUsesLabelAndShortensHome(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	if err := writeState(dir, State{Name: "uam-copilot-5b1e30f2", HostPID: 1, ChildPID: 1, Label: "smoke · copilot", Cwd: home + "/projects/x", Command: []string{"copilot"}}); err != nil {
		t.Fatal(err)
	}
	display, cwd := statusBarIdentity(dir, "uam-copilot-5b1e30f2")
	if display != "smoke · copilot · 5b1e30f2" || cwd != "~/projects/x" {
		t.Fatalf("identity = %q cwd = %q", display, cwd)
	}
	if err := writeState(dir, State{Name: "uam-fake-11112222", HostPID: 1, ChildPID: 1, Cwd: "/srv/work", Command: []string{"sh"}}); err != nil {
		t.Fatal(err)
	}
	if display, cwd := statusBarIdentity(dir, "uam-fake-11112222"); display != "uam-fake-11112222" || cwd != "/srv/work" {
		t.Fatalf("unlabelled identity = %q cwd = %q", display, cwd)
	}
	if display, cwd := statusBarIdentity(dir, "uam-fake-missing"); display != "uam-fake-missing" || cwd != "" {
		t.Fatalf("missing state identity = %q cwd = %q", display, cwd)
	}
}

func TestOutputFilterClampsScrollRegionAndTracksDamage(t *testing.T) {
	var dst bytes.Buffer
	filter := newAttachOutputFilter(&dst, true)
	filter.viewportRows = func() int { return 23 }
	for _, test := range []struct {
		in, want string
		damage   statusBarDamage
	}{
		{in: "\x1b[r", want: "\x1b[1;23r"},
		{in: "\x1b[5;20r", want: "\x1b[5;20r"},
		{in: "\x1b[5;40r", want: "\x1b[5;23r"},
		{in: "\x1b[30;40r", want: "\x1b[1;23r"},
		{in: "\x1b[;23r", want: "\x1b[1;23r"},
		{in: "\x1b[2J", want: "\x1b[2J", damage: damageClear},
		{in: "\x1b[3J", want: "\x1b[3J", damage: damageClear},
		{in: "\x1b[J", want: "\x1b[J"},
		{in: "\x1b[!p", want: "\x1b[!p", damage: damageReset},
		{in: "\x1bc", want: "\x1bc", damage: damageReset},
		{in: "\x1b[?1049h", want: ""},
	} {
		dst.Reset()
		if _, err := filter.Write([]byte(test.in)); err != nil {
			t.Fatal(err)
		}
		if got := dst.String(); got != test.want {
			t.Fatalf("filter(%q) wrote %q, want %q", test.in, got, test.want)
		}
		if damage := filter.consumeDamage(); damage != test.damage {
			t.Fatalf("filter(%q) damage = %d, want %d", test.in, damage, test.damage)
		}
	}
	// Without a reservation the provider's margins pass through untouched.
	plain := newAttachOutputFilter(&dst, true)
	dst.Reset()
	if _, err := plain.Write([]byte("\x1b[r")); err != nil {
		t.Fatal(err)
	}
	if dst.String() != "\x1b[r" {
		t.Fatalf("unreserved filter rewrote margins: %q", dst.String())
	}
}

func TestReportSizeSubtractsTheStatusBar(t *testing.T) {
	var wire bytes.Buffer
	frames := newAttachFrameWriter(&wire, protocolV2, "client-3", 4)
	frames.SetAssignedRole(roleController)
	runtime := newAttachRuntime(attachRuntimeConfig{
		output: &bytes.Buffer{}, statusRows: 1,
		terminalSize: func() (int, int, bool) { return 120, 40, true },
	})
	if err := runtime.reportSize(frames); err != nil {
		t.Fatal(err)
	}
	_, payload, err := readFrame(bytes.NewReader(wire.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	_, size, ok := (&host{}).parseResizeFrame(&attachClient{version: protocolV2}, payload)
	if !ok || size != (terminalSize{cols: 120, rows: 39}) {
		t.Fatalf("resize payload = %+v ok=%v, want 120x39", size, ok)
	}
}

// lockedBuffer stands in for the attach client's synchronized writer: the bar
// restore fires from a timer goroutine while the test reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

func (b *lockedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func TestStatusBarRestoresAfterNoticeAndStopsOnExit(t *testing.T) {
	var out lockedBuffer
	runtime := newAttachRuntime(attachRuntimeConfig{
		session: "uam-fake-bar", output: &out, statusRows: 1, role: roleController, prefix: 0x02, mouseEnabled: true,
		terminalSize: func() (int, int, bool) { return 100, 12, true },
	})
	if err := runtime.paintStatusBar(true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "\x1b[1;11r") || !strings.Contains(out.String(), "[uam: role controller; uam-fake-bar; C-b d / C-Left detach; C-b i info]") {
		t.Fatalf("initial paint = %q", out.String())
	}
	out.Reset()
	if err := runtime.writeStatus("mouse passthrough false"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "[uam: mouse passthrough false]") {
		t.Fatalf("notice missing: %q", out.String())
	}
	runtime.setRole(roleStandby)
	deadline := time.Now().Add(statusNoticeHold + 2*time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "[uam: role standby; ") {
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(out.String(), "[uam: role standby; ") {
		t.Fatalf("bar was not restored with the new role: %q", out.String())
	}
	// A notice queued right before exit must not paint after the stop.
	out.Reset()
	if err := runtime.writeStatus("late"); err != nil {
		t.Fatal(err)
	}
	runtime.stopStatusBar()
	out.Reset()
	time.Sleep(statusNoticeHold + 500*time.Millisecond)
	if out.Len() != 0 {
		t.Fatalf("painted %q after stop", out.String())
	}
}

// The real client keeps one row for the bar on uam's own screen and reports
// the rest to the host; a primary-screen provider keeps every row and no bar.
func TestAttachReservesStatusRowOnlyOnOwnScreen(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	const probe = `while :; do stty size; sleep 0.2; done`
	for _, test := range []struct {
		name     string
		wantSize string
		wantPin  bool
	}{
		{name: "uam-fake-5a7a7a7a", wantSize: "23 80", wantPin: true},
		{name: "uam-codex-5b7b7b7b", wantSize: "24 80", wantPin: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := c.CreateSession(ctx, test.name, t.TempDir(), nil, []string{"/bin/sh", "-c", probe}); err != nil {
				t.Fatalf("CreateSession: %v", err)
			}
			attached := startQuietAttach(t, c.Dir, test.name, 80, 24)
			waitFor(t, "provider sees the viewport", func() bool { return strings.Contains(attached.Snapshot(), test.wantSize) })
			output := attached.Snapshot()
			if strings.Contains(output, "\x1b[1;23r") != test.wantPin {
				t.Fatalf("scroll region pinned = %v, want %v: %q", !test.wantPin, test.wantPin, output)
			}
			if strings.Contains(output, "[uam: role controller; "+test.name+"; C-b d / C-Left detach") != test.wantPin {
				t.Fatalf("status bar present = %v, want %v: %q", !test.wantPin, test.wantPin, output)
			}
			attached.Detach(t)
		})
	}
}
