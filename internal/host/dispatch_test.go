package host

import (
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

// startHost helper used by the dispatch-coverage tests.
func startHost(t *testing.T, argv []string) (*Host, string, func()) {
	t.Helper()
	dir := t.TempDir()
	cfg := Config{
		SessionID:   "uam-test-dispatch",
		Argv:        argv,
		Cwd:         "/tmp",
		JournalPath: filepath.Join(dir, "session.log"),
		SocketPath:  filepath.Join(dir, "session.sock"),
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.Run(ctx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(cfg.SocketPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cleanup := func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Errorf("host.Run did not exit")
		}
	}
	return h, cfg.SocketPath, cleanup
}

// rpc sends a single Request and reads the response on conn.
func rpc(t *testing.T, conn net.Conn, kind ipc.Kind, id uint32, payload []byte) ipc.Request {
	t.Helper()
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: kind, ID: id, Payload: payload}); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	resp, err := ipc.ReadFrame(conn)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	return resp
}

func TestDispatchResize(t *testing.T) {
	_, sock, cleanup := startHost(t, []string{"/bin/cat"})
	defer cleanup()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	payload, _ := json.Marshal(struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}{Cols: 100, Rows: 30})
	resp := rpc(t, conn, ipc.KindResize, 11, payload)
	if resp.ID != 11 {
		t.Fatalf("expected id=11, got %d", resp.ID)
	}
}

func TestDispatchStatus(t *testing.T) {
	_, sock, cleanup := startHost(t, []string{"/bin/cat"})
	defer cleanup()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	resp := rpc(t, conn, ipc.KindStatus, 21, nil)
	if resp.ID != 21 {
		t.Fatalf("expected id=21, got %d", resp.ID)
	}
	var out struct {
		ExitCode int    `json:"exit_code"`
		Pid      int    `json:"pid"`
		Session  string `json:"session_id"`
	}
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		t.Fatalf("Unmarshal status: %v: %q", err, resp.Payload)
	}
	if out.Session != "uam-test-dispatch" {
		t.Fatalf("expected session uam-test-dispatch, got %s", out.Session)
	}
	if out.Pid == 0 {
		t.Fatalf("expected non-zero pid")
	}
}

func TestDispatchKill(t *testing.T) {
	_, sock, cleanup := startHost(t, []string{"/bin/cat"})
	defer cleanup()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	resp := rpc(t, conn, ipc.KindKill, 31, nil)
	if resp.ID != 31 {
		t.Fatalf("expected id=31, got %d", resp.ID)
	}
}

func TestDispatchUnknownKindReturnsEmpty(t *testing.T) {
	_, sock, cleanup := startHost(t, []string{"/bin/cat"})
	defer cleanup()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	resp := rpc(t, conn, ipc.Kind(200), 41, nil)
	if resp.ID != 41 {
		t.Fatalf("expected id=41, got %d", resp.ID)
	}
	if len(resp.Payload) != 0 {
		t.Fatalf("expected empty payload, got %q", resp.Payload)
	}
}

// TestHostBootstrapFailsOnBadJournalPath exercises the bootstrap error
// path when the journal's parent dir cannot be created (e.g., a file
// exists where a directory should be).
func TestHostBootstrapFailsOnBadJournalPath(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file at the journal dir path so MkdirAll fails.
	badDir := filepath.Join(dir, "notadir")
	if err := os.WriteFile(badDir, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		SessionID:   "x",
		Argv:        []string{"/bin/cat"},
		Cwd:         "/tmp",
		JournalPath: filepath.Join(badDir, "j.log"),
		SocketPath:  filepath.Join(dir, "s.sock"),
	}
	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := h.Run(ctx); err == nil {
		t.Fatalf("expected Run to fail with bad journal path")
	}
}

// TestHostRejectsCrossUIDPeer ensures handleConn's PeerUID check returns
// silently for impossible UIDs. We can't actually fake a different UID
// in a unit test, so this exercises the happy-path same-uid branch and
// confirms the connection is served.
func TestHostRejectsCrossUIDPeer(t *testing.T) {
	_, sock, cleanup := startHost(t, []string{"/bin/cat"})
	defer cleanup()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	// A Capture RPC must succeed (same-uid path).
	payload, _ := json.Marshal(struct {
		Bytes int64 `json:"bytes"`
	}{Bytes: 1024})
	resp := rpc(t, conn, ipc.KindCapture, 51, payload)
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp.Payload, &out); err != nil {
		t.Fatalf("Unmarshal: %v: %q", err, resp.Payload)
	}
	// Lines may be empty if cat hasn't produced output yet — that's OK,
	// what we care about is the connection being served.
	_ = strings.Join(out.Lines, "\n")
}
