package pty

import (
	"io"
	"strings"
	"testing"
)

func TestSpawnEchoesBack(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()

	child, err := Spawn(p, SpawnArgs{
		Argv: []string{"/bin/echo", "hello-from-pty"},
		Env:  []string{},
		Cwd:  "/tmp",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	buf := make([]byte, 256)
	var collected []byte
	for {
		n, err := p.Master.Read(buf)
		if n > 0 {
			collected = append(collected, buf[:n]...)
		}
		if err != nil {
			break
		}
		if strings.Contains(string(collected), "hello-from-pty") {
			break
		}
	}
	if !strings.Contains(string(collected), "hello-from-pty") {
		t.Fatalf("expected child's stdout in master, got %q", string(collected))
	}

	if _, err := child.Wait(); err != nil && err != io.EOF {
		t.Fatalf("child.Wait: %v", err)
	}
}

func TestSpawnInheritsEnv(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	child, err := Spawn(p, SpawnArgs{
		Argv: []string{"/usr/bin/env"},
		Env:  []string{"UAM_TEST_KEY=UAM_TEST_VALUE"},
		Cwd:  "/tmp",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	buf := make([]byte, 4096)
	var collected []byte
	for {
		n, err := p.Master.Read(buf)
		if n > 0 {
			collected = append(collected, buf[:n]...)
		}
		if err != nil {
			break
		}
		if strings.Contains(string(collected), "UAM_TEST_VALUE") {
			break
		}
	}
	if !strings.Contains(string(collected), "UAM_TEST_VALUE") {
		t.Fatalf("expected env var visible in child, got %q", string(collected))
	}
	_, _ = child.Wait()
}

func TestSpawnEmptyArgvErrors(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	if _, err := Spawn(p, SpawnArgs{Argv: nil, Cwd: "/tmp"}); err == nil {
		t.Fatalf("expected error for empty argv")
	}
}

func TestChildPidAndTerminate(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	// `sleep 30` gives us a live PID long enough to inspect and signal.
	child, err := Spawn(p, SpawnArgs{Argv: []string{"/bin/sleep", "30"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	pid := child.Pid()
	if pid <= 0 {
		t.Fatalf("expected positive pid, got %d", pid)
	}
	if err := child.Terminate(); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	_, _ = child.Wait() // SIGTERM yields non-nil err; we don't care here.
}

func TestChildKill(t *testing.T) {
	p, err := Open()
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = p.Close() }()
	child, err := Spawn(p, SpawnArgs{Argv: []string{"/bin/sleep", "30"}, Cwd: "/tmp"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := child.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	_, _ = child.Wait()
}

func TestUnstartedChildHelpersAreSafe(t *testing.T) {
	var c Child
	if pid := c.Pid(); pid != 0 {
		t.Fatalf("Pid on unstarted child should be 0, got %d", pid)
	}
	if err := c.Kill(); err != nil {
		t.Fatalf("Kill on unstarted child should be nil, got %v", err)
	}
	if err := c.Terminate(); err != nil {
		t.Fatalf("Terminate on unstarted child should be nil, got %v", err)
	}
	if _, err := c.Wait(); err == nil {
		t.Fatalf("Wait on unstarted child should error")
	}
}
