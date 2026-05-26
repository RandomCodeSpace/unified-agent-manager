package supervisor

import (
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
)

// startFakeHost serves a minimal host socket that replies with a canned
// payload. It mimics the parts of internal/host needed to drive
// supervisor.proxyToHost in tests without forking a real subprocess.
func startFakeHost(t *testing.T, dir, id string, reply []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, id+".sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				req, err := ipc.ReadFrame(c)
				if err == io.EOF || err != nil {
					return
				}
				_ = ipc.WriteFrame(c, ipc.Request{ID: req.ID, Payload: reply})
			}(conn)
		}
	}()
	return sock
}

func TestProxyToHostRoundTrip(t *testing.T) {
	s := supForHandlers(t)
	sock := startFakeHost(t, s.HostsDir(), "fake-1", []byte(`{"lines":["row-1","row-2"]}`))
	s.sessions["fake-1"] = SessionRecord{ID: "fake-1", SocketPath: sock}

	payload, _ := json.Marshal(struct {
		Bytes int64 `json:"bytes"`
	}{Bytes: 4096})
	resp, err := s.proxyToHost("fake-1", ipc.KindCapture, payload)
	if err != nil {
		t.Fatalf("proxyToHost: %v", err)
	}
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("Unmarshal: %v: %q", err, resp)
	}
	if len(out.Lines) != 2 || out.Lines[0] != "row-1" {
		t.Fatalf("unexpected reply: %+v", out)
	}
}

func TestProxyToHostUnknownSession(t *testing.T) {
	s := supForHandlers(t)
	if _, err := s.proxyToHost("nope", ipc.KindCapture, nil); err == nil {
		t.Fatalf("expected unknown-session error")
	}
}

func TestProxyToHostDialFailure(t *testing.T) {
	s := supForHandlers(t)
	s.sessions["dead"] = SessionRecord{ID: "dead", SocketPath: "/nonexistent.sock"}
	if _, err := s.proxyToHost("dead", ipc.KindCapture, nil); err == nil {
		t.Fatalf("expected dial error")
	}
}

func TestHandleCaptureProxiesAndReturnsLines(t *testing.T) {
	s := supForHandlers(t)
	sock := startFakeHost(t, s.HostsDir(), "k", []byte(`{"lines":["line-A"]}`))
	s.sessions["k"] = SessionRecord{ID: "k", SocketPath: sock}

	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Bytes  int64  `json:"bytes"`
	}{Handle: "k", Bytes: 4096})
	resp := s.handleCapture(payload)
	var out struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("Unmarshal: %v: %q", err, resp)
	}
	if len(out.Lines) != 1 || out.Lines[0] != "line-A" {
		t.Fatalf("got %+v", out)
	}
}

func TestHandleCaptureUsesLinesAsByteHint(t *testing.T) {
	s := supForHandlers(t)
	sock := startFakeHost(t, s.HostsDir(), "k2", []byte(`{"lines":[]}`))
	s.sessions["k2"] = SessionRecord{ID: "k2", SocketPath: sock}

	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Lines  int    `json:"lines"`
	}{Handle: "k2", Lines: 10})
	resp := s.handleCapture(payload)
	if hasErr(resp) {
		t.Fatalf("expected non-error response: %q", resp)
	}
}

func TestHandleWriteForwardsAndConfirms(t *testing.T) {
	s := supForHandlers(t)
	sock := startFakeHost(t, s.HostsDir(), "w", nil)
	s.sessions["w"] = SessionRecord{ID: "w", SocketPath: sock}

	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Data   []byte `json:"data"`
	}{Handle: "w", Data: []byte("hello")})
	resp := s.handleWrite(payload)
	if hasErr(resp) {
		t.Fatalf("expected non-error response: %q", resp)
	}
}

func TestHandleResizeForwardsAndConfirms(t *testing.T) {
	s := supForHandlers(t)
	sock := startFakeHost(t, s.HostsDir(), "r", nil)
	s.sessions["r"] = SessionRecord{ID: "r", SocketPath: sock}

	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
		Cols   uint16 `json:"cols"`
		Rows   uint16 `json:"rows"`
	}{Handle: "r", Cols: 100, Rows: 30})
	resp := s.handleResize(payload)
	if hasErr(resp) {
		t.Fatalf("expected non-error response: %q", resp)
	}
}

func TestHandleKillRemovesSessionAndForwards(t *testing.T) {
	s := supForHandlers(t)
	sock := startFakeHost(t, s.HostsDir(), "kk", nil)
	s.sessions["kk"] = SessionRecord{ID: "kk", SocketPath: sock}

	payload, _ := json.Marshal(struct {
		Handle string `json:"handle"`
	}{Handle: "kk"})
	resp := s.handleKill(payload)
	if hasErr(resp) {
		t.Fatalf("unexpected error: %q", resp)
	}
	s.mu.Lock()
	_, ok := s.sessions["kk"]
	s.mu.Unlock()
	if ok {
		t.Fatalf("session should be removed after Kill")
	}
}
