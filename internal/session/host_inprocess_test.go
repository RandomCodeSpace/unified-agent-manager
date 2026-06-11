package session

import (
	"bufio"
	"context"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

// startInProcessHost runs runHost in a goroutine inside the test process so
// the host runtime (PTY pump, control ops, attach machinery, shutdown) is
// exercised under the coverage profiler — the normal test path spawns hosts
// as child processes, which Go coverage cannot observe.
func startInProcessHost(t *testing.T, c *Client, name, command string) chan error {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- runHost(c.Dir, name, t.TempDir(), "label0", []string{"UAM_T=1"}, []string{"/bin/sh", "-c", command}, w)
	}()
	line, err := bufio.NewReader(r).ReadString('\n')
	_ = r.Close()
	if err != nil || strings.TrimSpace(line) != "ok" {
		t.Fatalf("host not ready: %q %v", line, err)
	}
	return done
}

func TestInProcessHostLifecycle(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-12121212"
	done := startInProcessHost(t, c, name, `echo hello-cover; while read l; do echo "rt:$l"; done`)

	waitFor(t, "banner", func() bool {
		out, err := c.Capture(ctx, name, 0) // 0 exercises the default-lines branch
		return err == nil && strings.Contains(out, "hello-cover")
	})
	if !c.HasSession(ctx, name) {
		t.Fatal("HasSession should see the in-process host")
	}
	if err := c.SetSessionLabel(ctx, name, "covered · fake"); err != nil {
		t.Fatalf("SetSessionLabel: %v", err)
	}
	if err := c.SendLine(ctx, name, "ping"); err != nil {
		t.Fatalf("SendLine: %v", err)
	}
	waitFor(t, "echo", func() bool {
		out, _ := c.Capture(ctx, name, 50)
		return strings.Contains(out, "rt:ping")
	})

	// Attach over the socket: replay, live output, stdin frames, resize, and
	// detach all flow through the host's attach machinery.
	conn, err := net.Dial("unix", SocketPath(c.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := writeJSONLine(conn, request{Op: opAttach, Cols: 120, Rows: 40}); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(conn)
	var resp response
	if err := readJSONLine(br, &resp); err != nil || !resp.OK {
		t.Fatalf("attach resp: %+v %v", resp, err)
	}
	if resp.Data != "covered · fake" {
		t.Fatalf("attach label = %q", resp.Data)
	}
	if err := writeFrame(conn, frameStdin, []byte("from-attach\r")); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(conn, frameResize, resizePayload(100, 30)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "attach round trip", func() bool {
		out, _ := c.Capture(ctx, name, 50)
		return strings.Contains(out, "rt:from-attach")
	})
	// Live output must reach the attached client (replay or broadcast).
	sawOutput := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		var got strings.Builder
		for {
			n, err := br.Read(buf)
			if n > 0 {
				got.WriteString(string(buf[:n]))
				if strings.Contains(got.String(), "rt:from-attach") {
					close(sawOutput)
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-sawOutput:
	case <-time.After(10 * time.Second):
		t.Fatal("attached client never saw live output")
	}
	if err := writeFrame(conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}

	// A second connection sending an unknown op exercises the error arm.
	conn2, err := net.Dial("unix", SocketPath(c.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONLine(conn2, request{Op: "bogus"}); err != nil {
		t.Fatal(err)
	}
	var resp2 response
	if err := readJSONLine(bufio.NewReader(conn2), &resp2); err != nil || resp2.OK {
		t.Fatalf("unknown op should fail: %+v %v", resp2, err)
	}
	_ = conn2.Close()

	if err := c.Kill(ctx, name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHost: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit after kill")
	}
	if c.HasSession(ctx, name) {
		t.Fatal("session should be gone")
	}
}

func TestInProcessHostAgentExitCleansUp(t *testing.T) {
	c := newTestClient(t)
	name := "uam-fake-34343434"
	done := startInProcessHost(t, c, name, "exit 7")
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runHost: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("host did not exit with its agent")
	}
	if _, err := os.Stat(SocketPath(c.Dir, name)); !os.IsNotExist(err) {
		t.Fatal("socket should be removed after agent exit")
	}
}

func TestRunHostRejectsBadInput(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", t.TempDir())
	if err := RunHost([]string{"--name", "bad name", "--", "/bin/true"}); err == nil {
		t.Fatal("invalid name must fail")
	}
	if err := RunHost([]string{"--name", "uam-fake-56565656"}); err == nil {
		t.Fatal("missing command must fail")
	}
	if err := RunHost([]string{"--bogus-flag"}); err == nil {
		t.Fatal("bad flags must fail")
	}
}

func TestRunAttachArgErrors(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", t.TempDir())
	if err := RunAttach([]string{}); err == nil {
		t.Fatal("missing session name must fail")
	}
	if err := RunAttach([]string{"bad name"}); err == nil {
		t.Fatal("invalid session name must fail")
	}
	if err := RunAttach([]string{"uam-fake-78787878"}); err == nil {
		t.Fatal("attach to nonexistent session must fail")
	}
	if err := RunAttach([]string{"--bogus"}); err == nil {
		t.Fatal("bad flags must fail")
	}
}
