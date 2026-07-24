package session

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

type todo6PTYAttach struct {
	master   *os.File
	slave    *os.File
	done     chan error
	snapshot func() string
	before   *term.State
}

type todo6PTYAttachConfig struct {
	dir           string
	name          string
	requestedRole clientRole
	profile       attachProfileSnapshot
}

func TestTodo6AttachControlModesRealPTY(t *testing.T) {
	// Given
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := "uam-fake-66666666"
	command := `stty raw -echo; printf READY; while :; do n=$(dd bs=1 count=1 2>/dev/null | od -An -tu1 | tr -d ' \n'); [ -n "$n" ] || exit; printf '\r\nBYTE:%s\r\n' "$n"; done`
	if err := client.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatal(err)
	}
	state, err := readState(client.Dir, name)
	if err != nil {
		t.Fatal(err)
	}
	profile := attachProfileSnapshot{selected: "focused", effective: "focused+session"}
	controller := startTodo6PTYAttach(t, todo6PTYAttachConfig{dir: client.Dir, name: name, requestedRole: roleController, profile: profile})
	waitFor(t, "Todo 6 controller assignment", func() bool { return strings.Contains(controller.snapshot(), "role controller;") })
	standby := startTodo6PTYAttach(t, todo6PTYAttachConfig{dir: client.Dir, name: name, requestedRole: roleController, profile: profile})
	waitFor(t, "Todo 6 standby assignment", func() bool { return strings.Contains(standby.snapshot(), "role standby;") })
	observer := startTodo6PTYAttach(t, todo6PTYAttachConfig{dir: client.Dir, name: name, requestedRole: roleObserver, profile: profile})
	waitFor(t, "Todo 6 observer assignment", func() bool { return strings.Contains(observer.snapshot(), "role observer;") })
	for _, attached := range []*todo6PTYAttach{controller, standby, observer} {
		waitFor(t, "Todo 6 PTY replay", func() bool { return strings.Contains(attached.snapshot(), "READY") })
	}

	// When
	writeTodo6PTY(t, standby, []byte{detachPrefix, detachPrefix, detachPrefix, 'c', detachPrefix, 'r', 'S', detachPrefix, 'i', detachPrefix, 'm'})
	writeTodo6PTY(t, observer, []byte("O\x1b[6n\x1b[200~prompt-like\x1b[201~"))
	writeTodo6PTY(t, observer, []byte{detachPrefix, detachPrefix, detachPrefix, 'c', detachPrefix, 'r', detachPrefix, 'o', detachPrefix, 'i', detachPrefix, 'm'})
	writeTodo6PTY(t, controller, []byte{'A', detachPrefix, detachPrefix, detachPrefix, 'c', detachPrefix, 'r', detachPrefix, 'i', detachPrefix, 'm', detachPrefix, 'o'})
	waitFor(t, "standby promotion", func() bool { return strings.Count(standby.snapshot(), "role controller") >= 1 })
	writeTodo6PTY(t, standby, []byte{'B', detachPrefix, 'o'})
	waitFor(t, "controller return", func() bool { return strings.Contains(controller.snapshot(), "role controller (transferred)") })
	writeTodo6PTY(t, controller, []byte{'C'})
	providerOutput := ""
	providerTimer := time.NewTimer(20 * time.Second)
	providerTicker := time.NewTicker(20 * time.Millisecond)
	defer providerTimer.Stop()
	defer providerTicker.Stop()
	providerReady := false
	for !providerReady {
		output, captureErr := client.Capture(ctx, name, 100)
		if captureErr == nil {
			providerOutput = output
			providerReady = strings.Contains(output, "BYTE:65") && strings.Contains(output, "BYTE:2") && strings.Contains(output, "BYTE:3") && strings.Contains(output, "BYTE:66") && strings.Contains(output, "BYTE:67")
		}
		if providerReady {
			break
		}
		select {
		case <-providerTimer.C:
			t.Fatalf("timed out waiting for controller command bytes; capture=%q standby=%q", providerOutput, standby.snapshot())
		case <-providerTicker.C:
		}
	}
	writeTodo6PTY(t, observer, []byte{detachPrefix, 'd'})
	waitTodo6Detach(t, observer)
	writeTodo6PTY(t, standby, []byte{detachPrefix, 'd'})
	waitTodo6Detach(t, standby)
	writeTodo6PTY(t, controller, []byte{detachPrefix, 'd'})
	waitTodo6Detach(t, controller)

	// Then
	termiosAfter := make(map[string][]byte, 3)
	termiosEqual := make(map[string]bool, 3)
	attachments := map[string]*todo6PTYAttach{"controller": controller, "standby": standby, "observer": observer}
	for role, attached := range attachments {
		after, stateErr := term.GetState(attached.slave.Fd())
		if stateErr != nil {
			t.Fatal(stateErr)
		}
		termiosEqual[role] = reflect.DeepEqual(attached.before, after)
		termiosAfter[role] = todo6TermStateBytes(after)
	}
	providerOutput, err = client.Capture(ctx, name, 200)
	if err != nil {
		t.Fatal(err)
	}
	for _, rejected := range []string{"BYTE:79", "BYTE:83", "BYTE:112"} {
		if strings.Contains(providerOutput, rejected) {
			t.Fatalf("non-controller input reached provider: %s", rejected)
		}
	}
	for role, equal := range termiosEqual {
		if !equal {
			t.Fatalf("%s attachment changed terminal state", role)
		}
	}
	cleanupOrder := make(map[string]todo6CleanupOrder, 3)
	for role, attached := range attachments {
		cleanupOrder[role] = observeTodo6CleanupOrder(attached.snapshot())
		if !cleanupOrder[role].Valid {
			t.Fatalf("%s attach output was written after screen restoration", role)
		}
	}
	for _, attached := range []*todo6PTYAttach{controller, standby, observer} {
		_ = attached.master.Close()
		_ = attached.slave.Close()
	}
	if err := client.Kill(ctx, name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Todo 6 runtime cleanup", func() bool {
		entries, readErr := os.ReadDir(client.Dir)
		return readErr == nil && len(entries) == 0
	})
	waitFor(t, "Todo 6 process cleanup", func() bool { return !state.hostAlive() && !state.childAlive() })
	entries, err := os.ReadDir(client.Dir)
	if err != nil {
		t.Fatal(err)
	}
	runtimeEntries := make([]string, 0, len(entries))
	for _, entry := range entries {
		runtimeEntries = append(runtimeEntries, entry.Name())
	}
	_, socketErr := os.Lstat(SocketPath(client.Dir, name))
	if socketErr != nil && !errors.Is(socketErr, os.ErrNotExist) {
		t.Fatal(socketErr)
	}
	cleanup := todo6CleanupObservation{
		hostPID: state.HostPID, hostAlive: state.hostAlive(), childPID: state.ChildPID, childAlive: state.childAlive(),
		socketPath: SocketPath(client.Dir, name), socketPresent: socketErr == nil, runtimeEntries: runtimeEntries,
	}
	if err := cleanup.validate(); err != nil {
		t.Fatal(err)
	}
	configDir := os.Getenv("UAM_CONFIG_DIR")
	if err := os.Remove(client.Dir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(configDir); err != nil {
		t.Fatal(err)
	}
	writeTodo6Evidence(t, todo6Evidence{
		controller: controller, standby: standby, observer: observer, providerOutput: providerOutput,
		termiosAfter: termiosAfter, termiosEqual: termiosEqual, cleanupOrder: cleanupOrder, cleanup: cleanup,
	})
}

func startTodo6PTYAttach(t *testing.T, config todo6PTYAttachConfig) *todo6PTYAttach {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := pty.Setsize(master, &pty.Winsize{Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(slave.Fd())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runAttachWithOptions(config.dir, config.name, slave, slave, attachOptions{
			quiet: true, requestedRole: config.requestedRole, profile: config.profile,
		})
	}()
	return &todo6PTYAttach{master: master, slave: slave, done: done, snapshot: capturePTYOutput(master), before: before}
}

func writeTodo6PTY(t *testing.T, attached *todo6PTYAttach, payload []byte) {
	t.Helper()
	if _, err := attached.master.Write(payload); err != nil {
		t.Fatal(err)
	}
}

func waitTodo6Detach(t *testing.T, attached *todo6PTYAttach) {
	t.Helper()
	select {
	case err := <-attached.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Todo 6 attachment did not detach")
	}
}
