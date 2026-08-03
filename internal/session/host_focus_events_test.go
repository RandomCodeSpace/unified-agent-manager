package session

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
)

// attachControllerConn dials the host socket and completes a v1 controller
// attach, returning the open connection. The caller owns closing it.
func attachControllerConn(t *testing.T, c *Client, name string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", SocketPath(c.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONLine(conn, request{Op: opAttach, Cols: 120, Rows: 40}); err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := readJSONLine(bufio.NewReader(conn), &resp); err != nil || !resp.OK {
		t.Fatalf("attach resp: %+v %v", resp, err)
	}
	return conn
}

// A provider with focus reporting (?1004) enabled runs under a detached host,
// so no terminal ever tells it about focus changes. The host must synthesize
// focus-in when a controller attaches and focus-out when the last controller
// detaches. The fake agent enables ?1004 before echoing stdin via cat -v, so
// the synthesized events become visible in the capture as ^[[I / ^[[O once a
// newline flushes the canonical input buffer.
func TestHostSynthesizesFocusEventsAtAttachBoundaries(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-56565656"
	done := startInProcessHost(t, c, name, `printf '\033[?1004h'; echo armed; cat -v`)
	defer func() {
		_ = c.Kill(ctx, name)
		<-done
	}()
	waitFor(t, "focus reporting armed", func() bool {
		out, err := c.Capture(ctx, name, 50)
		return err == nil && strings.Contains(out, "armed")
	})

	conn := attachControllerConn(t, c, name)
	defer func() { _ = conn.Close() }()
	// The synthetic focus-in has no newline; a carriage return flushes the
	// agent's canonical input buffer so cat -v echoes what preceded it. Sent
	// inside the poll because the attach response can race the host's
	// initialization of the new client.
	waitFor(t, "synthetic focus-in", func() bool {
		_ = writeFrame(conn, frameStdin, []byte("\r"))
		out, _ := c.Capture(ctx, name, 50)
		return strings.Contains(out, "^[[I")
	})

	if err := writeFrame(conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "synthetic focus-out", func() bool {
		// Flushes out-of-band; rejected with SessionBusy until the host has
		// actually dropped the controller, which the poll absorbs.
		_ = c.SendLine(ctx, name, "")
		captured, err := c.Capture(ctx, name, 50)
		return err == nil && strings.Contains(captured, "^[[O")
	})
}

// The resume path attaches the controller while the replacement provider
// process is still starting: ?1004 comes on only after the attach. The host
// must synthesize the missed focus-in the moment the mode turns on.
func TestHostSynthesizesFocusInWhenModeArmsAfterAttach(t *testing.T) {
	c := newTestClient(t)
	ctx := context.Background()
	name := "uam-fake-78787878"
	done := startInProcessHost(t, c, name, `echo waiting; read go; printf '\033[?1004h'; echo armed; cat -v`)
	defer func() {
		_ = c.Kill(ctx, name)
		<-done
	}()
	waitFor(t, "agent waiting", func() bool {
		out, err := c.Capture(ctx, name, 50)
		return err == nil && strings.Contains(out, "waiting")
	})

	conn := attachControllerConn(t, c, name)
	defer func() { _ = conn.Close() }()
	if err := writeFrame(conn, frameStdin, []byte("go\r")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "focus reporting armed after attach", func() bool {
		out, _ := c.Capture(ctx, name, 50)
		return strings.Contains(out, "armed")
	})
	waitFor(t, "late synthetic focus-in", func() bool {
		_ = writeFrame(conn, frameStdin, []byte("\r"))
		out, _ := c.Capture(ctx, name, 50)
		return strings.Contains(out, "^[[I")
	})
}
