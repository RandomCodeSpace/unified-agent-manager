package copilot

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// The sweep kills a CLI whose uam is gone, and leaves a live uam's CLI, an
// unmarked one and an agent's command that only inherited the mark.
func TestSweepOrphanCLIsKillsOnlyCLIsOfAGoneUam(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the sweep reads /proc")
	}
	start := func(owner string, args ...string) *exec.Cmd {
		t.Helper()
		cmd := exec.Command("/bin/sh", append([]string{"-c", "while :; do sleep 1; done", "sh"}, args...)...)
		cmd.Env = os.Environ()
		if owner != "" {
			cmd.Env = append(cmd.Env, cliOwnerEnv+"="+owner)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		})
		return cmd
	}
	const gone = "2147483646:1"
	orphan := start(gone, "--headless", "--stdio")
	live := start(cliOwner(), "--headless", "--stdio")
	unmarked := start("", "--headless", "--stdio")
	shell := start(gone)
	if n := sweepOrphanCLIs(); n < 1 {
		t.Fatalf("killed %d", n)
	}
	if err := orphan.Wait(); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("orphaned CLI: %v", err)
	}
	for name, cmd := range map[string]*exec.Cmd{"live uam's CLI": live, "unmarked": unmarked, "agent command": shell} {
		if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
			t.Fatalf("%s was killed: %v", name, err)
		}
	}
}

func TestOwnerRunning(t *testing.T) {
	for owner, want := range map[string]bool{
		cliOwner():     true,
		"2147483646:1": false,
		"":             true,
		"garbage":      true,
		"12:abc":       true,
		"0:5":          true,
	} {
		if got := ownerRunning(owner); got != want {
			t.Errorf("ownerRunning(%q) = %v, want %v", owner, got, want)
		}
	}
}
