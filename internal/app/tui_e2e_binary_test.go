package app_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Drives the shipped binary's dashboard on a real PTY with an isolated store.
// Bubbletea key routing only exists once a terminal is attached, so the modal
// key contract cannot be checked any other way.
//
//	UAM_E2E_BIN=$(pwd)/bin/uam go test ./internal/app/ -run TestE2ETUI
const e2eBinEnv = "UAM_E2E_BIN"

type tuiSession struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	seen *ptyRecorder
}

// ptyRecorder accumulates everything the dashboard paints. Reads run on their
// own goroutine because a pty master here does not honour read deadlines — a
// blocking read on the test goroutine would hang the run instead of failing it.
type ptyRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func recordPTY(f *os.File) *ptyRecorder {
	recorder := &ptyRecorder{}
	go func() {
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
	b := r.buf.Bytes()
	if len(b) > 400 {
		b = b[len(b)-400:]
	}
	return string(b)
}

func startTUI(t *testing.T) *tuiSession {
	t.Helper()
	bin := os.Getenv(e2eBinEnv)
	if bin == "" {
		t.Skipf("%s is required: point it at a built uam binary", e2eBinEnv)
	}
	root := t.TempDir()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"UAM_SESSION_DIR="+filepath.Join(root, "run"),
		"UAM_CONFIG_DIR="+filepath.Join(root, "cfg"),
		"TERM=xterm-256color", "UAM_WIDE=0",
	)
	if err := os.MkdirAll(filepath.Join(root, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	session := &tuiSession{t: t, cmd: cmd, ptmx: ptmx, seen: recordPTY(ptmx)}
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	})
	return session
}

func (s *tuiSession) await(needle string, timeout time.Duration) bool {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if s.seen.contains(needle) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return s.seen.contains(needle)
}

func (s *tuiSession) send(keys string) {
	s.t.Helper()
	if _, err := s.ptmx.WriteString(keys); err != nil {
		s.t.Fatalf("write %q: %v", keys, err)
	}
	time.Sleep(200 * time.Millisecond)
}

// requireExit reaps the dashboard. The recorder keeps draining the pty, so a
// quitting bubbletea program is never blocked writing its screen-restore
// sequence.
func (s *tuiSession) requireExit(what string, timeout time.Duration) {
	s.t.Helper()
	done := make(chan error, 1)
	go func() { done <- s.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			s.t.Fatalf("%s: dashboard exited with %v: %q", what, err, s.seen.tail())
		}
	case <-time.After(timeout):
		s.t.Fatalf("%s: the dashboard did not exit: %q", what, s.seen.tail())
	}
}

// Ctrl+C is the universal quit. Every modal used to claim it and do nothing,
// which left no way out for a user who does not reach for Esc.
func TestE2ETUICtrlCQuitsFromEveryModal(t *testing.T) {
	tests := []struct {
		name  string
		open  string
		ready string
	}{
		{name: "base dashboard", open: "", ready: ""},
		{name: "help", open: "?", ready: "Keys:"},
		{name: "wizard", open: "e", ready: ""},
		{name: "rename", open: "\x12", ready: ""},
		{name: "filter", open: "/abc", ready: "abc"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := startTUI(t)
			if !session.await("UAM", 15*time.Second) {
				t.Fatalf("dashboard never rendered: %q", session.seen.tail())
			}
			if test.open != "" {
				session.send(test.open)
			}
			if test.ready != "" && !session.await(test.ready, 5*time.Second) {
				t.Fatalf("modal did not open: %q", session.seen.tail())
			}
			session.send("\x03")
			session.requireExit("Ctrl+C in "+test.name, 10*time.Second)
		})
	}
}

// '?' opens help on an empty composer and is ordinary text otherwise — the
// rule every other letter-shaped binding already followed.
func TestE2ETUIQuestionMarkGuard(t *testing.T) {
	session := startTUI(t)
	if !session.await("UAM", 15*time.Second) {
		t.Fatalf("dashboard never rendered: %q", session.seen.tail())
	}
	session.send("why")
	session.send("?")
	if !session.await("why?", 5*time.Second) {
		t.Fatalf("'?' did not type into the composer: %q", session.seen.tail())
	}
	session.send("\x03")
	session.requireExit("Ctrl+C after typing", 10*time.Second)
}
