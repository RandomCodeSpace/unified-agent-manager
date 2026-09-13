package session

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestCreatePreservesLiveOrphan(t *testing.T) {
	c := newTestClient(t)
	name := "uam-fake-104a"
	child := exec.Command("/bin/sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	state := State{Name: name, ChildPID: child.Process.Pid, ChildStart: procStartTime(child.Process.Pid)}
	if err := writeState(c.Dir, state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(statePath(c.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	err = c.CreateSession(context.Background(), name, t.TempDir(), nil, []string{"/bin/true"})
	if err == nil || !strings.Contains(err.Error(), "stop") {
		t.Fatalf("create with live orphan = %v, want explicit stop required", err)
	}
	after, err := os.ReadFile(statePath(c.Dir, name))
	if err != nil || string(after) != string(before) {
		t.Fatalf("orphan state changed: %q, %v", after, err)
	}
	infos, err := c.List(context.Background())
	if err != nil || len(infos) != 1 || infos[0].ChildPID != child.Process.Pid {
		t.Fatalf("orphan no longer visible: %+v, %v", infos, err)
	}
}

func TestSessionClaimProtectsStartupFromCreateAndSweep(t *testing.T) {
	c := newTestClient(t)
	name := "uam-fake-104b"
	// Another process owns the name but has not yet replaced the old state.
	claim, err := os.Open(c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = claim.Close() }()
	if err := syscall.Flock(int(claim.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := writeState(c.Dir, State{Name: name}); err != nil {
		t.Fatal(err)
	}
	listDone := make(chan error, 1)
	go func() { _, err := c.List(context.Background()); listDone <- err }()
	createDone := make(chan error, 1)
	cwd := t.TempDir()
	go func() { createDone <- c.CreateSession(context.Background(), name, cwd, nil, []string{"/bin/true"}) }()
	select {
	case err := <-listDone:
		t.Fatalf("List completed during another process's startup claim: %v", err)
	case err := <-createDone:
		t.Fatalf("Create completed during another process's startup claim: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := os.Stat(statePath(c.Dir, name)); err != nil {
		t.Errorf("List removed state owned by a starting host: %v", err)
	}
	// The first host publishes its live identity before releasing the claim.
	if err := writeState(c.Dir, State{Name: name, HostPID: os.Getpid(), HostStart: procStartTime(os.Getpid())}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(statePath(c.Dir, name)) })
	_ = claim.Close()
	if err := <-listDone; err != nil {
		t.Fatal(err)
	}
	if err := <-createDone; err == nil {
		t.Error("second host claimed an already reserved name")
	}
	if _, err := os.Stat(statePath(c.Dir, name)); err != nil {
		t.Fatalf("startup state lost after releasing claim: %v", err)
	}
}

func TestConcurrentCreateKeepsSingleReachableHost(t *testing.T) {
	c := newTestClient(t)
	name := "uam-fake-104c"
	cwd := t.TempDir()
	const contenders = 8
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Go(func() {
			<-start
			results <- c.CreateSession(context.Background(), name, cwd, nil, []string{"/bin/sleep", "60"})
		})
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent creates = %d, want 1", successes)
	}
	if _, err := c.Capture(context.Background(), name, 1); err != nil {
		t.Fatalf("winning host is unreachable: %v", err)
	}
	if err := c.Kill(context.Background(), name); err != nil {
		t.Fatal(err)
	}
}
