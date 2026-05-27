package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/ipc"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/pty"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/supervisor"
)

// detachKey is the byte that, when read from local stdin, ends the
// attach session without killing the underlying agent. Chosen as Ctrl-\
// (0x1c, ASCII FS) because it is rarely produced by interactive shells
// or any of the supported agent CLIs.
const detachKey byte = 0x1c

// RunAttachRaw implements `uam attach --raw <handle>`. It dials the
// per-session host socket, sends KindAttach to flip the conn into a raw
// PTY byte stream, then bidirectionally pipes between the local
// terminal and the host's PTY until the agent exits, the host shuts
// down, or the user presses Ctrl-\.
func RunAttachRaw(args []string) {
	fs := flag.NewFlagSet("attach --raw", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	rem := fs.Args()
	if len(rem) < 1 {
		fmt.Fprintln(os.Stderr, "uam attach --raw: requires <session-handle>")
		os.Exit(2)
	}
	if err := attachToHost(rem[0]); err != nil {
		fmt.Fprintf(os.Stderr, "uam attach --raw: %v\n", err)
		os.Exit(1)
	}
}

// attachToHost orchestrates one attach session. Each non-trivial step
// (handshake, resize relay, bidirectional pipe) lives in its own
// helper so this orchestrator stays straightforward.
func attachToHost(handle string) error {
	sockPath := hostSocketPath(handle)

	streamConn, err := dialAndHandshake(sockPath)
	if err != nil {
		return err
	}
	defer func() { _ = streamConn.Close() }()

	controlConn, _ := net.DialTimeout("unix", sockPath, 2*time.Second)
	if controlConn != nil {
		defer func() { _ = controlConn.Close() }()
	}

	restore, err := pty.MakeRaw(os.Stdin)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() { _ = restore() }()

	if controlConn != nil {
		stop := startResizeWatch(controlConn, os.Stdin)
		defer stop()
	}

	pipeAttach(streamConn, os.Stdin, os.Stdout)
	return nil
}

// hostSocketPath computes the per-session host socket path for a given
// session handle. Factored out so tests can exercise the derivation
// without dialing.
func hostSocketPath(handle string) string {
	return filepath.Join(supervisor.DefaultRuntimeDir(), "hosts", handle+".sock")
}

// dialAndHandshake dials the host UDS, sends the KindAttach handshake,
// waits up to 2s for the ACK frame, then clears the read deadline so
// the subsequent raw streaming loop blocks naturally on conn.Read.
func dialAndHandshake(sockPath string) (net.Conn, error) {
	conn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial host socket %s: %w", sockPath, err)
	}
	if err := ipc.WriteFrame(conn, ipc.Request{Kind: ipc.KindAttach, ID: 1}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("send handshake: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("set handshake deadline: %w", err)
	}
	if _, err := ipc.ReadFrame(conn); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake ack: %w", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clear read deadline: %w", err)
	}
	return conn, nil
}

// startResizeWatch subscribes to SIGWINCH, sends an immediate
// KindResize matching the current tty winsize, and forwards subsequent
// SIGWINCH signals as further KindResize frames over the control
// conn. Returns a stop function that unsubscribes the handler.
func startResizeWatch(controlConn net.Conn, tty *os.File) func() {
	sigCh := make(chan os.Signal, 8)
	signal.Notify(sigCh, syscall.SIGWINCH)
	sendResize(controlConn, tty)
	go func() {
		for range sigCh {
			sendResize(controlConn, tty)
		}
	}()
	return func() { signal.Stop(sigCh) }
}

// pipeAttach runs the bidirectional copy between the raw stream conn
// and local stdin/stdout. Returns once either direction's goroutine
// completes (peer close, EOF, or detach key fired from stdin).
func pipeAttach(streamConn net.Conn, stdin io.Reader, stdout io.Writer) {
	var closeOnce sync.Once
	closer := func() { _ = streamConn.Close() }
	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(stdout, streamConn)
		closeOnce.Do(closer)
		done <- struct{}{}
	}()
	go func() {
		pumpStdinToStream(stdin, streamConn)
		closeOnce.Do(closer)
		done <- struct{}{}
	}()
	<-done
}

// pumpStdinToStream copies bytes from stdin to streamConn until either
// side errors or the detach key (Ctrl-\) is read. Bytes preceding the
// detach key in the same read buffer are forwarded so a key sequence
// ending in the detach byte does not silently drop its preamble.
func pumpStdinToStream(stdin io.Reader, streamConn net.Conn) {
	buf := make([]byte, 4096)
	for {
		n, rerr := stdin.Read(buf)
		if n > 0 {
			if i := bytes.IndexByte(buf[:n], detachKey); i >= 0 {
				if i > 0 {
					_, _ = streamConn.Write(buf[:i])
				}
				return
			}
			if _, werr := streamConn.Write(buf[:n]); werr != nil {
				return
			}
		}
		if rerr != nil {
			return
		}
	}
}

// sendResize reads the current terminal winsize and sends a KindResize
// frame over the control conn. Errors are swallowed because a missing
// or transient resize is non-fatal — the agent will render with stale
// dimensions until the next SIGWINCH.
func sendResize(conn net.Conn, tty *os.File) {
	cols, rows, err := pty.GetWinsize(tty)
	if err != nil {
		return
	}
	payload, err := json.Marshal(struct {
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}{Cols: cols, Rows: rows})
	if err != nil {
		return
	}
	// #nosec G115 -- UnixNano fits in uint32 for correlation purposes;
	// truncation here is acceptable since the resize verb does not need
	// a unique request ID.
	_ = ipc.WriteFrame(conn, ipc.Request{
		Kind:    ipc.KindResize,
		ID:      uint32(time.Now().UnixNano()),
		Payload: payload,
	})
}
