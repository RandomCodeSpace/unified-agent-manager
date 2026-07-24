package session

import (
	"bufio"
	"fmt"
	"net"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func testTodo6SignalCleanup(t *testing.T) {
	for _, assignedRole := range []clientRole{roleController, roleStandby, roleObserver} {
		t.Run(string(assignedRole), func(t *testing.T) {
			// Given
			dir := t.TempDir()
			if err := EnsureDir(dir); err != nil {
				t.Fatal(err)
			}
			name := "uam-fake-67676767"
			listener, err := net.Listen("unix", SocketPath(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			serverErr := make(chan error, 1)
			go serveTodo6SignalAttach(listener, assignedRole, serverErr)
			ptmx, tty := openProtocolPTY(t)
			before, err := term.GetState(tty.Fd())
			if err != nil {
				t.Fatal(err)
			}
			snapshot := capturePTYOutput(ptmx)
			done := make(chan error, 1)
			go func() { done <- runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true}) }()
			waitFor(t, "signal fixture readiness", func() bool { return strings.Contains(snapshot(), "signal-ready") })

			// When
			if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}

			// Then
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("signal did not stop attachment")
			}
			if err := <-serverErr; err != nil {
				t.Fatal(err)
			}
			after, err := term.GetState(tty.Fd())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("signal cleanup changed terminal state for role %q", assignedRole)
			}
		})
	}
}

func serveTodo6SignalAttach(listener net.Listener, assignedRole clientRole, result chan<- error) {
	conn, err := listener.Accept()
	if err != nil {
		result <- err
		return
	}
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	var req request
	if err := readJSONLine(reader, &req); err != nil {
		result <- err
		return
	}
	if err := writeJSONLine(conn, response{OK: true, Version: protocolV2, ClientID: "signal-client", AssignedRole: assignedRole, Generation: 1}); err != nil {
		result <- err
		return
	}
	if err := writeFrame(conn, serverFrameControl, []byte(`{"type":"role","client_id":"signal-client","role":"`+assignedRole+`","generation":1,"reason":"assigned"}`)); err != nil {
		result <- err
		return
	}
	if err := writeFrame(conn, serverFramePTY, []byte("signal-ready")); err != nil {
		result <- err
		return
	}
	kind, _, err := readFrame(reader)
	if err == nil && kind != frameDetach {
		result <- fmt.Errorf("signal cleanup frame = %d, want %d", kind, frameDetach)
		return
	}
	result <- err
}
