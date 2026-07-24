package session

import (
	"bufio"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func TestAttachHandshakeInterruptionLeavesTerminalUntouched(t *testing.T) {
	dir := socketTestDir(t)
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	name := "uam-fake-99990000"
	ln, err := net.Listen("unix", SocketPath(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	const attempts = 8
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		for range attempts {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			var req request
			_ = readJSONLine(bufio.NewReader(conn), &req)
			_ = conn.Close()
		}
	}()

	_, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	for range attempts {
		if err := runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true}); err == nil {
			t.Fatal("interrupted handshake unexpectedly succeeded")
		}
	}
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("repeated handshake interruption changed terminal state")
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("interrupting server did not exit")
	}
}

func TestAttachNegotiatesBeforeScreenOwnership(t *testing.T) {
	dir := socketTestDir(t)
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	name := "uam-fake-55556666"
	ln, err := net.Listen("unix", SocketPath(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req request
		_ = readJSONLine(bufio.NewReader(conn), &req)
		_ = writeJSONLine(conn, response{Err: "unsupported attach protocol version 99"})
	}()

	ptmx, tty := openProtocolPTY(t)
	err = runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true})
	if err == nil || !strings.Contains(err.Error(), "unsupported attach protocol") {
		t.Fatalf("attach error = %v", err)
	}
	_ = tty.Close()
	_ = ptmx.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	buf := make([]byte, 128)
	n, readErr := ptmx.Read(buf)
	if n != 0 || readErr == nil {
		t.Fatalf("terminal received pre-negotiation output %x, err=%v", buf[:n], readErr)
	}
	select {
	case <-serverDone:
	case <-time.After(time.Second):
		t.Fatal("rejecting peer did not exit")
	}
}

func openProtocolPTY(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	return ptmx, tty
}
