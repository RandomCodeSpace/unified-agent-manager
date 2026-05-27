package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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
	handle := rem[0]
	if err := attachToHost(handle); err != nil {
		fmt.Fprintf(os.Stderr, "uam attach --raw: %v\n", err)
		os.Exit(1)
	}
}

// attachToHost is the core of RunAttachRaw, factored out so tests and
// callers that want to handle errors structurally (rather than via
// os.Exit) can reuse it.
func attachToHost(handle string) error {
	sockPath := filepath.Join(supervisor.DefaultRuntimeDir(), "hosts", handle+".sock")

	streamConn, err := net.DialTimeout("unix", sockPath, 2*time.Second)
	if err != nil {
		return fmt.Errorf("dial host socket %s: %w", sockPath, err)
	}
	defer func() { _ = streamConn.Close() }()

	if err := ipc.WriteFrame(streamConn, ipc.Request{Kind: ipc.KindAttach, ID: 1}); err != nil {
		return fmt.Errorf("send handshake: %w", err)
	}
	if err := streamConn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return fmt.Errorf("set handshake deadline: %w", err)
	}
	if _, err := ipc.ReadFrame(streamConn); err != nil {
		return fmt.Errorf("handshake ack: %w", err)
	}
	// Clear the read deadline so the raw streaming loop blocks naturally
	// on conn.Read.
	if err := streamConn.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear read deadline: %w", err)
	}

	// Open a separate conn for control RPCs (resize). The stream conn
	// can no longer carry IPC frames after the handshake.
	controlConn, _ := net.DialTimeout("unix", sockPath, 2*time.Second)
	if controlConn != nil {
		defer func() { _ = controlConn.Close() }()
	}

	// Switch local stdin to raw mode so each keystroke flows to the
	// remote PTY without local line buffering or echo. The restore
	// func is deferred so the terminal returns to cooked mode on every
	// exit path.
	restore, err := pty.MakeRaw(os.Stdin)
	if err != nil {
		return fmt.Errorf("set raw mode: %w", err)
	}
	defer func() { _ = restore() }()

	// SIGWINCH handler: forward terminal resize to the host so the
	// remote PTY matches the local viewport. Sends an initial resize
	// immediately so the agent renders correctly even if the size has
	// not changed since the session was spawned.
	if controlConn != nil {
		sigCh := make(chan os.Signal, 8)
		signal.Notify(sigCh, syscall.SIGWINCH)
		defer signal.Stop(sigCh)
		sendResize(controlConn, os.Stdin)
		go func() {
			for range sigCh {
				sendResize(controlConn, os.Stdin)
			}
		}()
	}

	// Bidirectional pipe. Either side returning ends the session.
	done := make(chan struct{}, 2)
	var closeOnce sync.Once
	closer := func() { _ = streamConn.Close() }

	// Host PTY output → local stdout. io.Copy returns when the conn
	// closes (host shutdown or detach key fired below).
	go func() {
		_, _ = io.Copy(os.Stdout, streamConn)
		closeOnce.Do(closer)
		done <- struct{}{}
	}()

	// Local stdin → host PTY, intercepting the detach key. Reading
	// directly from os.Stdin (instead of an io.Reader wrapper) keeps
	// raw-mode latency to a single syscall per keystroke.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, rerr := os.Stdin.Read(buf)
			if n > 0 {
				if i := bytes.IndexByte(buf[:n], detachKey); i >= 0 {
					if i > 0 {
						_, _ = streamConn.Write(buf[:i])
					}
					closeOnce.Do(closer)
					done <- struct{}{}
					return
				}
				if _, werr := streamConn.Write(buf[:n]); werr != nil {
					closeOnce.Do(closer)
					done <- struct{}{}
					return
				}
			}
			if rerr != nil {
				if !errors.Is(rerr, io.EOF) {
					// Non-EOF read errors are surfaced via the close so
					// the io.Copy goroutine wakes up; we don't print
					// here because the terminal is still in raw mode.
					_ = rerr
				}
				closeOnce.Do(closer)
				done <- struct{}{}
				return
			}
		}
	}()

	<-done
	return nil
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
