package session

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// These exercise the shipped binary rather than the package: a real `uam
// __host` process, a real `uam __attach` process, and a real PTY between the
// attach client and the test. They catch what in-process tests structurally
// cannot — wiring between the two entry points, terminal setup and teardown
// order, and the bytes a viewer actually receives.
//
// Opt-in because they build and run the binary:
//
//	UAM_E2E_BIN=$(pwd)/bin/uam go test ./internal/session/ -run TestE2E
const e2eBinEnv = "UAM_E2E_BIN"

// The agent stand-in: no provider, no API calls. It turns on mouse reporting
// and pushes kitty keyboard flags exactly as a provider TUI does, so the
// filter, the emulator replay and the teardown all see realistic traffic.
const e2eAgentScript = `printf '\033[?1002h\033[?1006h\033[>1u'; printf 'AGENT-READY\n'; while :; do sleep 1; done`

type e2eSession struct {
	t    *testing.T
	bin  string
	dir  string
	name string
	host *exec.Cmd
}

func newE2ESession(t *testing.T, agentScript string) *e2eSession {
	t.Helper()
	bin := os.Getenv(e2eBinEnv)
	if bin == "" {
		t.Skipf("%s is required: point it at a built uam binary", e2eBinEnv)
	}
	if !filepath.IsAbs(bin) {
		t.Fatalf("%s must be absolute: %q", e2eBinEnv, bin)
	}
	// Short path: the socket lives here and sockaddr_un is ~104 bytes.
	dir, err := os.MkdirTemp("", "uam-e2e")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	session := &e2eSession{t: t, bin: bin, dir: dir, name: "uam-fake-11112222"}
	logFile, err := os.Create(filepath.Join(dir, "host.log"))
	if err != nil {
		t.Fatal(err)
	}
	host := exec.Command(bin, "__host", "--dir", dir, "--name", session.name, "--provider", "fake",
		"/bin/sh", "-c", agentScript)
	host.Env = append(os.Environ(), "UAM_SESSION_DIR="+dir, "UAM_CONFIG_DIR="+filepath.Join(dir, "cfg"), "TERM=xterm-256color", "UAM_WIDE=0")
	host.Stdout, host.Stderr = logFile, logFile
	if err := host.Start(); err != nil {
		t.Fatal(err)
	}
	session.host = host
	t.Cleanup(func() {
		_ = host.Process.Kill()
		_, _ = host.Process.Wait()
		_ = logFile.Close()
		_ = os.RemoveAll(dir)
	})
	session.waitForSocket()
	return session
}

func (s *e2eSession) waitForSocket() {
	s.t.Helper()
	socket := SocketPath(s.dir, s.name)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(socket); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.t.Fatalf("host never created %s", socket)
}

type e2eViewer struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	seen *ptyRecorder
}

// ptyRecorder accumulates everything a viewer's terminal receives. Reads run on
// their own goroutine because a pty master here does not honour read deadlines
// — a blocking read on the test goroutine would hang the run instead of failing
// it.
type ptyRecorder struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

func recordPTY(f *os.File) *ptyRecorder {
	recorder := &ptyRecorder{done: make(chan struct{})}
	go func() {
		defer close(recorder.done)
		buf := make([]byte, 4096)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				recorder.mu.Lock()
				recorder.buf.Write(buf[:n])
				recorder.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return recorder
}

func (r *ptyRecorder) contains(needle string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return bytes.Contains(r.buf.Bytes(), []byte(needle))
}

func (r *ptyRecorder) tail() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return tailOf(r.buf.Bytes())
}

// attach starts `uam __attach` on its own PTY and returns the viewer end.
// Cleanup is registered on the caller's t, not the session's: a subtest that
// fails must not leave its client attached and holding the controller role for
// every subtest that follows.
func (s *e2eSession) attach(t *testing.T) *e2eViewer {
	t.Helper()
	cmd := exec.Command(s.bin, "__attach", "--dir", s.dir, s.name)
	cmd.Env = append(os.Environ(), "UAM_SESSION_DIR="+s.dir, "UAM_CONFIG_DIR="+filepath.Join(s.dir, "cfg"), "TERM=xterm-256color", "UAM_WIDE=0")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	viewer := &e2eViewer{t: t, cmd: cmd, ptmx: ptmx, seen: recordPTY(ptmx)}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return viewer
}

// await polls the recorded output until needle appears or the timeout passes.
func (v *e2eViewer) await(needle string, timeout time.Duration) bool {
	v.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if v.seen.contains(needle) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return v.seen.contains(needle)
}

func (v *e2eViewer) send(keys string) {
	v.t.Helper()
	if _, err := v.ptmx.WriteString(keys); err != nil {
		v.t.Fatalf("write %q to the viewer: %v", keys, err)
	}
	time.Sleep(150 * time.Millisecond)
}

func (v *e2eViewer) requireSeen(what, needle string) {
	v.t.Helper()
	if !v.seen.contains(needle) {
		v.t.Fatalf("%s: %q missing from the viewer output: %q", what, needle, v.seen.tail())
	}
}

func (v *e2eViewer) requireDetached(what string) {
	v.t.Helper()
	if !v.await("detached", 8*time.Second) {
		v.t.Fatalf("%s: no detach notice: %q", what, v.seen.tail())
	}
	done := make(chan error, 1)
	go func() { done <- v.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			v.t.Fatalf("%s: attach client exited with %v", what, err)
		}
	case <-time.After(10 * time.Second):
		v.t.Fatalf("%s: attach client did not exit after detaching", what)
	}
}

func tailOf(b []byte) string {
	if len(b) > 400 {
		b = b[len(b)-400:]
	}
	return string(b)
}

func TestE2EAttachChordsAndMouse(t *testing.T) {
	session := newE2ESession(t, e2eAgentScript)

	t.Run("banner survives the host repaint", func(t *testing.T) {
		viewer := session.attach(t)
		if !viewer.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("agent output never arrived: %q", viewer.seen.tail())
		}
		if !viewer.await("[uam: role", 3*time.Second) {
			t.Fatalf("role banner missing: %q", viewer.seen.tail())
		}
		viewer.send("\x02d")
		viewer.requireDetached("legacy Ctrl+B d")
		viewer.requireSeen("detach teardown", mouseReset)
		viewer.requireSeen("detach teardown", "\x1b[<7u")
	})

	// The chord has to survive the encodings a provider switches the terminal
	// into; a plain 0x02 never arrives once those are on.
	for _, encoded := range []struct{ name, prefix string }{
		{name: "kitty", prefix: "\x1b[98;5u"},
		{name: "modifyOtherKeys", prefix: "\x1b[27;5;98~"},
	} {
		t.Run(encoded.name+" encoded prefix detaches", func(t *testing.T) {
			viewer := session.attach(t)
			if !viewer.await("AGENT-READY", 10*time.Second) {
				t.Fatalf("agent output never arrived: %q", viewer.seen.tail())
			}
			viewer.send(encoded.prefix)
			viewer.send("d")
			viewer.requireDetached(encoded.name + " prefix")
		})
	}

	t.Run("mouse toggle restores the provider's live modes", func(t *testing.T) {
		viewer := session.attach(t)
		if !viewer.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("agent output never arrived: %q", viewer.seen.tail())
		}
		viewer.send("\x02i")
		// The notice is painted across the bottom rows, so assert on fragments
		// that cannot straddle a row break rather than one long run.
		if !viewer.await("session uam-fake-11112222", 5*time.Second) {
			t.Fatalf("info line missing: %q", viewer.seen.tail())
		}
		if !viewer.await("m mouse]", 5*time.Second) {
			t.Fatalf("info line was cut short: %q", viewer.seen.tail())
		}
		viewer.send("\x02m")
		if !viewer.await("mouse passthrough false", 5*time.Second) {
			t.Fatalf("mouse-off notice missing: %q", viewer.seen.tail())
		}
		viewer.requireSeen("mouse off", mouseReset)
		viewer.send("\x02m")
		if !viewer.await("mouse passthrough true", 5*time.Second) {
			t.Fatalf("mouse-on notice missing: %q", viewer.seen.tail())
		}
		viewer.requireSeen("mouse re-enable", "\x1b[?1002;1006h")
		viewer.send("\x02d")
		viewer.requireDetached("mouse toggle")
	})

	t.Run("quick detach survives a legacy mouse report", func(t *testing.T) {
		viewer := session.attach(t)
		if !viewer.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("agent output never arrived: %q", viewer.seen.tail())
		}
		// A click whose column byte falls in the UTF-8 continuation range.
		viewer.send("\x1b[M \x84\x30")
		viewer.send("\x1b[1;5D")
		viewer.requireDetached("Ctrl+Left quick detach")
	})

	t.Run("meta chord does not latch the filter", func(t *testing.T) {
		viewer := session.attach(t)
		if !viewer.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("agent output never arrived: %q", viewer.seen.tail())
		}
		viewer.send("\x1bX")
		viewer.send("\x02d")
		viewer.requireDetached("detach after Alt+X")
	})

	t.Run("second client is a standby and is promoted", func(t *testing.T) {
		first := session.attach(t)
		if !first.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("agent output never arrived: %q", first.seen.tail())
		}
		second := session.attach(t)
		if !second.await("[uam: role standby", 10*time.Second) {
			t.Fatalf("second client was not made a standby: %q", second.seen.tail())
		}
		first.send("\x02d")
		first.requireDetached("controller")
		if !second.await("controller", 10*time.Second) {
			t.Fatalf("standby was not promoted: %q", second.seen.tail())
		}
		second.send("\x02d")
		second.requireDetached("promoted client")
	})

	t.Run("prefix r reaches the controller", func(t *testing.T) {
		controller := session.attach(t)
		if !controller.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("agent output never arrived: %q", controller.seen.tail())
		}
		standby := session.attach(t)
		if !standby.await("[uam: role standby", 10*time.Second) {
			t.Fatalf("second client was not made a standby: %q", standby.seen.tail())
		}
		standby.send("\x02r")
		if !standby.await("control requested", 5*time.Second) {
			t.Fatalf("requester saw no acknowledgement: %q", standby.seen.tail())
		}
		if !controller.await("requested control", 8*time.Second) {
			t.Fatalf("the controller was never told: %q", controller.seen.tail())
		}
		standby.send("\x02d")
		standby.requireDetached("requesting standby")
		controller.send("\x02d")
		controller.requireDetached("notified controller")
	})

	t.Run("session outlives every detach", func(t *testing.T) {
		viewer := session.attach(t)
		if !viewer.await("AGENT-READY", 10*time.Second) {
			t.Fatalf("re-attach after all detaches failed: %q", viewer.seen.tail())
		}
		viewer.send("\x02d")
		viewer.requireDetached("re-attach")
	})
}

// The agent's final output must reach an attached viewer, and the viewer must
// report a clean session end rather than a truncated stream.
func TestE2EAgentExitDeliversFinalOutput(t *testing.T) {
	// The agent waits for a keystroke before printing its last screen and
	// exiting, so the viewer is attached when it happens — no timing race.
	const script = `printf 'AGENT-READY\n'; read _ignored; i=0; while [ $i -lt 60 ]; do printf 'FINAL-LINE-%03d\n' $i; i=$((i+1)); done; printf 'AGENT-EXITING\n'`
	session := newE2ESession(t, script)
	viewer := session.attach(t)
	if !viewer.await("AGENT-READY", 10*time.Second) {
		t.Fatalf("agent output never arrived: %q", viewer.seen.tail())
	}
	viewer.send("\r")

	if !viewer.await("AGENT-EXITING", 25*time.Second) {
		t.Fatalf("the agent's final output never reached the viewer: %q", viewer.seen.tail())
	}
	for i := range 60 {
		viewer.requireSeen("complete final output", fmt.Sprintf("FINAL-LINE-%03d", i))
	}
	if !viewer.await("[uam: session ended]", 10*time.Second) {
		t.Fatalf("no session-ended notice: %q", viewer.seen.tail())
	}
	if viewer.seen.contains("unexpected EOF") {
		t.Fatalf("truncated stream reported to the viewer: %q", viewer.seen.tail())
	}
	if err := viewer.cmd.Wait(); err != nil {
		t.Fatalf("attach client exited with %v after a clean agent exit", err)
	}
}
