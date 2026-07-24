package session

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestTodo6PINPreservesV1CodexAndCleanupContracts(t *testing.T) {
	// Given: an unversioned v1 host behind a legacy Codex session name.
	dir := t.TempDir()
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	name := "uam-codex-60606060"
	if err := writeState(dir, State{Name: name}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", SocketPath(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	serverDone := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		var req request
		if readErr := readJSONLine(reader, &req); readErr != nil {
			serverDone <- readErr
			return
		}
		if writeErr := writeJSONLine(conn, response{OK: true}); writeErr != nil {
			serverDone <- writeErr
			return
		}
		if writeErr := writeAll(conn, []byte("todo6-v1-pin")); writeErr != nil {
			serverDone <- writeErr
			return
		}
		kind, _, readErr := readFrame(reader)
		if readErr == nil && kind != frameDetach {
			readErr = fmt.Errorf("attach frame = %d, want %d", kind, frameDetach)
		}
		serverDone <- readErr
	}()

	ptmx, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := capturePTYOutput(ptmx)
	attachDone := make(chan error, 1)

	// When: the v2-capable client falls back to v1 and detaches twice-safe.
	go func() {
		attachDone <- runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true})
	}()
	waitFor(t, "v1 characterization output", func() bool {
		return bytes.Contains([]byte(snapshot()), []byte("todo6-v1-pin"))
	})
	if _, err := ptmx.Write([]byte{detachPrefix, 'd'}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-attachDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("characterization attach did not detach")
	}

	// Then: v1 output stayed raw, Codex stayed on the primary screen, and termios returned exactly.
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("attach cleanup did not restore the original terminal state")
	}
	output := snapshot()
	if bytes.Contains([]byte(output), []byte(screenEnter)) || bytes.Contains([]byte(output), []byte(screenExit)) {
		t.Fatalf("legacy Codex attach changed screen ownership: %q", output)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("v1 characterization server did not exit")
	}
}
