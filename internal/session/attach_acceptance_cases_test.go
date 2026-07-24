package session

import (
	"bufio"
	"errors"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/term"
)

func TestTerminalRestoredAfterHandshakeFailure(t *testing.T) {
	t.Run("timeout", testTodo6HandshakeTimeout)
	t.Run("role_rejection", testTodo6HandshakeRoleRejection)
}

func TestTerminalRestoredAfterMalformedServerFrame(t *testing.T) {
	t.Run("malformed_frame", testTodo6MalformedFrameCleanup)
	t.Run("downstream_output_write_failure", testTodo6DownstreamOutputFailure)
	t.Run("termios_equality", testTodo6MalformedFrameCleanup)
	t.Run("cleanup_ordering", testTodo6MalformedFrameCleanup)
}

func TestTerminalRestoredAfterControllerTransfer(t *testing.T) {
	t.Run("controller_transfer", testTodo6ControllerTransferCleanup)
	t.Run("signal", testTodo6SignalCleanup)
}

func TestCodexAttachPreservesPrimaryScreenScrollback(t *testing.T) {
	t.Run("scrollback", testTodo6CodexScrollback)
}

func testTodo6HandshakeTimeout(t *testing.T) {
	// Given
	dir, name, listener := todo6AttachListener(t, "uam-fake-68686868")
	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer func() { _ = conn.Close() }()
		var req request
		if err := readJSONLine(bufio.NewReader(conn), &req); err != nil {
			serverErr <- err
			return
		}
		_, err = io.Copy(io.Discard, conn)
		serverErr <- err
	}()
	_, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}

	// When
	err = runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true})

	// Then
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("attach timeout error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("handshake timeout changed terminal state")
	}
}

func testTodo6DownstreamOutputFailure(t *testing.T) {
	// Given
	dir, name, listener := todo6AttachListener(t, "uam-fake-69696969")
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
		if err := writeJSONLine(conn, response{OK: true, Version: protocolV2, ClientID: "client-output", AssignedRole: roleController, Generation: 1}); err != nil {
			serverErr <- err
			return
		}
		serverErr <- writeFrame(conn, serverFramePTY, []byte("downstream-output"))
	}()
	_, tty := openProtocolPTY(t)
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	output, err := os.CreateTemp(t.TempDir(), "closed-output-")
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}

	// When
	err = runAttachWithOptions(dir, name, tty, output, attachOptions{quiet: true})

	// Then
	if err == nil || !strings.Contains(err.Error(), "attach output") {
		t.Fatalf("downstream output error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("downstream output failure changed terminal state")
	}
}
