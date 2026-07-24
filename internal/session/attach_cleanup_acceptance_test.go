package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

func testTodo6HandshakeRoleRejection(t *testing.T) {
	// Given
	dir, name, listener := todo6AttachListener(t, "uam-fake-61616161")
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req request
		_ = readJSONLine(bufio.NewReader(conn), &req)
		_ = writeJSONLine(conn, response{Err: "role rejected"})
	}()
	_, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true})

	// Then
	if err == nil || !strings.Contains(err.Error(), "role rejected") {
		t.Fatalf("attach error = %v", err)
	}
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("handshake rejection changed terminal state")
	}
	<-serverDone
}

func testTodo6MalformedFrameCleanup(t *testing.T) {
	// Given
	dir, name, listener := todo6AttachListener(t, "uam-fake-62626262")
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req request
		_ = readJSONLine(bufio.NewReader(conn), &req)
		_ = writeJSONLine(conn, response{OK: true, Version: protocolV2, ClientID: "client-1", AssignedRole: roleController, Generation: 1})
		header := [5]byte{serverFramePTY}
		binary.BigEndian.PutUint32(header[1:], uint32(maxFrameLen+1))
		_ = writeAll(conn, header[:])
	}()
	ptmx, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := capturePTYOutput(ptmx)

	// When
	err = runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true})

	// Then
	if !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("attach error = %v, want oversized frame", err)
	}
	after, stateErr := term.GetState(tty.Fd())
	if stateErr != nil {
		t.Fatal(stateErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("malformed frame cleanup did not restore terminal state")
	}
	output := snapshot()
	if strings.Index(output, screenReset) > strings.Index(output, screenExit) {
		t.Fatalf("cleanup order = %q", output)
	}
}

func testTodo6ControllerTransferCleanup(t *testing.T) {
	// Given
	dir, name, listener := todo6AttachListener(t, "uam-fake-63636363")
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		reader := bufio.NewReader(conn)
		var req request
		if err := readJSONLine(reader, &req); err != nil {
			serverErr <- err
			return
		}
		if err := writeJSONLine(conn, response{OK: true, Version: protocolV2, ClientID: "client-1", AssignedRole: roleController, Generation: 1}); err != nil {
			serverErr <- err
			return
		}
		_ = writeFrame(conn, serverFrameControl, []byte(`{"type":"role","client_id":"client-1","role":"controller","generation":1,"reason":"assigned"}`))
		_ = writeFrame(conn, serverFramePTY, []byte("transfer-ready"))
		kind, payload, err := readFrame(reader)
		if err != nil {
			serverErr <- err
			return
		}
		if kind != frameRole || !bytes.Contains(payload, []byte(actionTransferControl)) {
			serverErr <- errors.New("transfer command was not sent as a role frame")
			return
		}
		_ = writeFrame(conn, serverFrameControl, []byte(`{"type":"role","client_id":"client-1","role":"standby","generation":2,"reason":"transferred"}`))
		kind, _, err = readFrame(reader)
		if err == nil && kind != frameDetach {
			err = errors.New("detach frame not received after transfer")
		}
		serverErr <- err
	}()
	ptmx, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := capturePTYOutput(ptmx)
	done := make(chan error, 1)
	go func() { done <- runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true}) }()
	waitFor(t, "controller transfer fixture", func() bool { return strings.Contains(snapshot(), "transfer-ready") })

	// When
	_, _ = ptmx.Write([]byte{detachPrefix, 'o'})
	_, _ = ptmx.Write([]byte{detachPrefix, 'd'})
	err = <-done

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("controller transfer cleanup did not restore terminal state")
	}
}

func testTodo6CodexScrollback(t *testing.T) {
	// Given
	client := newTestClient(t)
	name := "uam-codex-64646464"
	if err := client.CreateSession(t.Context(), name, t.TempDir(), nil, []string{"/bin/sh", "-c", "echo codex-scrollback-pin; sleep 60"}); err != nil {
		t.Fatal(err)
	}
	attached := startQuietAttach(t, client.Dir, name, 80, 24)
	waitFor(t, "Codex scrollback", func() bool { return strings.Contains(attached.Snapshot(), "codex-scrollback-pin") })

	// When
	attached.Detach(t)

	// Then
	if output := attached.Snapshot(); strings.Contains(output, screenEnter) || strings.Contains(output, screenExit) {
		t.Fatalf("Codex attach used an outer alternate screen: %q", output)
	}
}

func todo6AttachListener(t *testing.T, name string) (string, string, net.Listener) {
	t.Helper()
	dir := socketTestDir(t)
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", SocketPath(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return dir, name, listener
}
