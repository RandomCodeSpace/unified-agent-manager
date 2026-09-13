package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestHostReapsAgentWhileGrandchildHoldsPTY(t *testing.T) {
	c := newTestClient(t)
	name := "uam-fake-106a"
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.PutSession("fake:106a", store.SessionRecord{ID: "106a", Agent: "fake", SessionName: name, Status: store.StatusActive})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	pidPath := filepath.Join(t.TempDir(), "grandchild-pid")
	// Ignore terminal hangup so the grandchild really retains the slave
	// after its parent exits. The input gate ensures a viewer sees final output.
	done := startInProcessHost(t, c, name,
		`trap '' HUP; sleep 60 & printf '%s' "$!" > "$UAM_TEST_PID"; printf 'AGENT-READY\n'; read ignored; i=0; while [ "$i" -lt 60 ]; do printf 'FINAL-LINE-%03d\n' "$i"; i=$((i+1)); done; exit 7`,
		"UAM_TEST_PID="+pidPath,
	)
	state, err := readState(c.Dir, name)
	if err != nil {
		t.Fatal(err)
	}
	finished := false
	t.Cleanup(func() {
		_ = syscall.Kill(-state.ChildPID, syscall.SIGKILL)
		if !finished {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("host did not stop after fixture cleanup")
			}
		}
	})
	attached := startQuietAttach(t, c.Dir, name, 80, 24)
	attachFinished := false
	t.Cleanup(func() {
		if !attachFinished {
			_ = syscall.Kill(-state.ChildPID, syscall.SIGKILL)
			select {
			case <-attached.done:
			case <-time.After(5 * time.Second):
				t.Error("attach did not stop after fixture cleanup")
			}
		}
	})
	waitFor(t, "agent input gate", func() bool { return strings.Contains(attached.Snapshot(), "AGENT-READY") })
	pidText, err := os.ReadFile(pidPath)
	if err != nil {
		t.Fatal(err)
	}
	grandchildPID, err := strconv.Atoi(string(pidText))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := attached.ptmx.Write([]byte("exit\r")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		finished = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("host still waits for PTY EOF after direct agent exit")
	}
	if ProcAlive(state.ChildPID) {
		t.Fatal("direct agent was not reaped")
	}
	if !ProcAlive(grandchildPID) {
		t.Fatal("fixture grandchild exited before bounded host shutdown was exercised")
	}
	for _, path := range []string{statePath(c.Dir, name), SocketPath(c.Dir, name)} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("runtime file remains after agent exit: %s: %v", path, err)
		}
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	record := cfg.Sessions["fake:106a"]
	if record.LastExitCode == nil || *record.LastExitCode != 7 {
		t.Fatalf("recorded exit = %+v, want 7", record.LastExitCode)
	}
	select {
	case err := <-attached.done:
		attachFinished = true
		if err != nil {
			t.Fatalf("attach did not end cleanly: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not end with the session")
	}
	waitFor(t, "final output capture", func() bool { return strings.Contains(attached.Snapshot(), "FINAL-LINE-059") })
	for i := range 60 {
		if !strings.Contains(attached.Snapshot(), fmt.Sprintf("FINAL-LINE-%03d", i)) {
			t.Fatalf("final output line %d was lost", i)
		}
	}
}
