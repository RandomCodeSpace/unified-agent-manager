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
