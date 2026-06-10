package session

import (
	"bufio"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/charmbracelet/x/term"
)

// detachPrefix is the attach client's escape key (Ctrl+B, tmux's default
// prefix, kept for muscle memory). Prefix then `d` detaches; prefix twice
// sends a literal Ctrl+B to the agent.
const detachPrefix = 0x02

// ctrlZ is swallowed by the attach client: letting it through would SIGTSTP
// the agent inside its own detached session, where nothing can ever
// foreground it again — the same trap the old tmux config disarmed by
// binding C-z to a warning.
const ctrlZ = 0x1a

// RunAttach is the entry point of `uam __attach`: it puts the terminal in raw
// mode and bridges it to a session host — the native replacement for
// `tmux attach`. It returns when the user detaches (Ctrl+B d) or the agent
// exits.
func RunAttach(args []string) error {
	fs := flag.NewFlagSet("__attach", flag.ContinueOnError)
	dir := fs.String("dir", DefaultDir(), "session runtime directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("attach requires a session name")
	}
	return runAttach(*dir, fs.Arg(0), os.Stdin, os.Stdout)
}

func runAttach(dir, name string, stdin *os.File, stdout *os.File) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	conn, err := net.Dial("unix", SocketPath(dir, name))
	if err != nil {
		return fmt.Errorf("session %s is not running: %w", name, err)
	}
	defer func() { _ = conn.Close() }()

	cols, rows := 0, 0
	if w, h, err := term.GetSize(stdout.Fd()); err == nil {
		cols, rows = w, h
	}
	if err := writeJSONLine(conn, request{Op: opAttach, Cols: cols, Rows: rows}); err != nil {
		return fmt.Errorf("attach %s: %w", name, err)
	}
	br := bufio.NewReader(conn)
	var resp response
	if err := readJSONLine(br, &resp); err != nil {
		return fmt.Errorf("attach %s: %w", name, err)
	}
	if !resp.OK {
		return fmt.Errorf("attach %s: %s", name, resp.Err)
	}

	restore := func() {}
	if term.IsTerminal(stdin.Fd()) {
		state, err := term.MakeRaw(stdin.Fd())
		if err != nil {
			return fmt.Errorf("set raw mode: %w", err)
		}
		var once sync.Once
		restore = func() { once.Do(func() { _ = term.Restore(stdin.Fd(), state) }) }
		defer restore()
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	go func() {
		for range winch {
			if w, h, err := term.GetSize(stdout.Fd()); err == nil {
				_ = writeFrame(conn, frameResize, resizePayload(w, h))
			}
		}
	}()

	// stdin → host. Runs in a goroutine because a blocked terminal read
	// cannot be interrupted; when the session ends the process exits anyway.
	detached := make(chan struct{})
	go func() {
		pumpStdin(stdin, conn)
		close(detached)
	}()

	// host → terminal (the main loop): ends when the host closes the
	// connection (agent exited) or the user detached.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdout, br)
		done <- err
	}()
	var note string
	select {
	case <-detached:
		_ = writeFrame(conn, frameDetach, nil)
		note = "detached"
	case <-done:
		note = "session ended"
	}
	restore()
	_, _ = fmt.Fprintf(stdout, "\r\n[uam: %s]\r\n", note)
	return nil
}

func resizePayload(cols, rows int) []byte {
	// Clamp to uint16 range; the host rejects anything over 1000 anyway.
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:2], uint16(max(0, min(cols, 0xffff)))) // #nosec G115 -- clamped
	binary.BigEndian.PutUint16(out[2:4], uint16(max(0, min(rows, 0xffff)))) // #nosec G115 -- clamped
	return out
}

// pumpStdin forwards terminal input to the host, filtering the detach chord
// and Ctrl+Z. It returns when the user hits the detach chord or stdin/conn
// fails.
func pumpStdin(stdin io.Reader, conn net.Conn) {
	buf := make([]byte, 4096)
	pendingPrefix := false
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			out := make([]byte, 0, n+1)
			for _, b := range buf[:n] {
				if pendingPrefix {
					pendingPrefix = false
					switch b {
					case 'd':
						if len(out) > 0 {
							_ = writeFrame(conn, frameStdin, out)
						}
						return
					case detachPrefix:
						out = append(out, detachPrefix)
					default:
						out = append(out, detachPrefix, b)
					}
					continue
				}
				switch b {
				case detachPrefix:
					pendingPrefix = true
				case ctrlZ:
					// Swallowed; see ctrlZ doc.
				default:
					out = append(out, b)
				}
			}
			if len(out) > 0 {
				if werr := writeFrame(conn, frameStdin, out); werr != nil {
					return
				}
			}
		}
		if err != nil {
			return
		}
	}
}
