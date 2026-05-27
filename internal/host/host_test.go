package host

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
)

// dialHost waits for the host's socket to appear, then dials it.
func dialHost(t *testing.T, sockPath string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sockPath); err == nil {
			conn, err := net.Dial("unix", sockPath)
			if err == nil {
				return conn
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("socket %q did not appear", sockPath)
	return nil
}

func TestHostRunsEchoAndCaptures(t *testing.T) {
	// Use `cat` instead of `echo` so the child stays alive long enough to
	// dial the socket. We push "hello-host" through the PTY ourselves to
	// drive the journal — exactly the behavior a real agent would have.
	dir := t.TempDir()
	cfg := Config{
		SessionID:   "uam-test-abc12345",
		Argv:        []string{"/bin/cat"},
		Cwd:         "/tmp",
		Env:         nil,
		Cols:        80,
		Rows:        24,
		JournalPath: filepath.Join(dir, "session.log"),
		SocketPath:  filepath.Join(dir, "session.sock"),
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New host: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()

	conn := dialHost(t, cfg.SocketPath)
	defer func() { _ = conn.Close() }()

	// Send a Write RPC so cat echoes "hello-host" back into the PTY which
	// is then captured into the journal.
	wrPayload, _ := json.Marshal(struct {
		Data []byte `json:"data"`
	}{Data: []byte("hello-host\n")})
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindWrite, ID: 1, Payload: wrPayload}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	// Drain the write response.
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := ipc.ReadFrame(conn); err != nil {
		t.Fatalf("ReadFrame write resp: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		content, _ := os.ReadFile(cfg.JournalPath)
		if strings.Contains(string(content), "hello-host") {
			cancel()
			<-done
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("expected 'hello-host' in journal within 3s")
}

func TestHostNewValidatesConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"empty session id", Config{Argv: []string{"x"}, JournalPath: "j", SocketPath: "s"}},
		{"empty argv", Config{SessionID: "s", JournalPath: "j", SocketPath: "s"}},
		{"empty journal", Config{SessionID: "s", Argv: []string{"x"}, SocketPath: "s"}},
		{"empty socket", Config{SessionID: "s", Argv: []string{"x"}, JournalPath: "j"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.cfg); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestHostHandlesCaptureRPC(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SessionID:   "uam-test-capture",
		Argv:        []string{"/bin/cat"},
		Cwd:         "/tmp",
		JournalPath: filepath.Join(dir, "session.log"),
		SocketPath:  filepath.Join(dir, "session.sock"),
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	defer func() { cancel(); <-done }()

	conn := dialHost(t, cfg.SocketPath)
	defer func() { _ = conn.Close() }()

	// Drive output into the PTY via a Write RPC.
	wrPayload, _ := json.Marshal(struct {
		Data []byte `json:"data"`
	}{Data: []byte("rpc-line\n")})
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindWrite, ID: 1, Payload: wrPayload}); err != nil {
		t.Fatalf("WriteFrame write: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, err := ipc.ReadFrame(conn); err != nil {
		t.Fatalf("ReadFrame write resp: %v", err)
	}

	// Wait briefly for the input to flow back into the journal.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		content, _ := os.ReadFile(cfg.JournalPath)
		if strings.Contains(string(content), "rpc-line") {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}

	payload, _ := json.Marshal(struct {
		Bytes int64 `json:"bytes"`
	}{Bytes: 4096})
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindCapture, ID: 42, Payload: payload}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	resp, err := ipc.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if resp.ID != 42 {
		t.Fatalf("expected id=42, got %d", resp.ID)
	}
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		t.Fatalf("Unmarshal capture: %v: %q", err, resp.Payload)
	}
	found := false
	for _, l := range out.Lines {
		if strings.Contains(l, "rpc-line") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("rpc-line not found in capture: %#v", out.Lines)
	}
}

// TestHostKindAttachRoundTrip verifies that sending KindAttach over the
// host socket flips the conn into a raw PTY stream: an ACK frame comes
// back, then bytes written to the conn reach the child agent's stdin
// and bytes the child agent writes to stdout flow back out through the
// same conn (broadcast by pumpPTY via h.attached).
func TestHostKindAttachRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		SessionID:   "uam-test-attach",
		Argv:        []string{"/bin/cat"},
		Cwd:         "/tmp",
		JournalPath: filepath.Join(dir, "session.log"),
		SocketPath:  filepath.Join(dir, "session.sock"),
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	defer func() { cancel(); <-done }()

	conn := dialHost(t, cfg.SocketPath)
	defer func() { _ = conn.Close() }()

	// Handshake.
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindAttach, ID: 7}); err != nil {
		t.Fatalf("WriteFrame attach: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline ack: %v", err)
	}
	ack, err := ipc.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame ack: %v", err)
	}
	if ack.ID != 7 {
		t.Fatalf("expected ack ID=7, got %d", ack.ID)
	}
	if !strings.Contains(string(ack.Payload), `"ok":true`) {
		t.Fatalf("expected ok ack, got %q", string(ack.Payload))
	}

	// From here on the conn is a raw PTY stream. Write "hi\n"; cat
	// echoes it back via the PTY master and pumpPTY broadcasts it to
	// this same conn (since runRawAttach added us to h.attached).
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline raw: %v", err)
	}
	if _, err := conn.Write([]byte("hi\n")); err != nil {
		t.Fatalf("Write raw: %v", err)
	}
	got, ok := readUntilContains(conn, []byte("hi"), 3*time.Second)
	if !ok {
		t.Fatalf("expected echo of 'hi' on raw stream, got %q", string(got))
	}
}

// readUntilContains reads from conn into a growing buffer until either
// pattern appears, the read errors, or timeout elapses. Returns the
// accumulated bytes and whether the pattern was seen.
func readUntilContains(conn net.Conn, pattern []byte, timeout time.Duration) ([]byte, bool) {
	buf := make([]byte, 1024)
	var got []byte
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		n, rerr := conn.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
			if bytes.Contains(got, pattern) {
				return got, true
			}
		}
		if rerr != nil {
			return got, false
		}
	}
	return got, false
}
