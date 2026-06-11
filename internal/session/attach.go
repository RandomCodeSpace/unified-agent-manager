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

// The attach client is a verbatim byte bridge: the host replay opens with a
// clear-screen and every escape sequence the agent emits while a client is
// attached lands on the user's real terminal. `tmux attach` confined all of
// that by running the session inside its own alternate screen and resetting
// modes on detach; without the same ownership the session scribbles over the
// user's primary screen (still visible after uam exits) and leaked modes
// corrupt the TUI that resumes after detach.

// screenEnter opens the attach client's own alternate screen, saving the
// primary screen and cursor underneath.
const screenEnter = "\x1b[?1049h"

// screenExit resets every mode the agent could have toggled mid-attach, then
// leaves the alternate screen. Terminals ignore sequences they don't
// implement, so the suffix is safe to emit unconditionally.
const screenExit = "\x1b[<u" + // pop the kitty keyboard flags agents push
	"\x1b[=0;1u" + // and zero them in case the agent pushed more than once
	"\x1b[?1000;1002;1003;1004;1005;1006;1015l" + // mouse tracking + focus reporting off
	"\x1b[?2004l" + // bracketed paste off
	"\x1b[?2026l" + // synchronized output off
	"\x1b[!p" + // DECSTR: cursor keys, origin, margins, SGR, insert mode
	"\x1b>" + // numeric keypad (DECKPNM; DECSTR leaves keypad mode alone)
	"\x1b(B" + // G0 charset back to ASCII
	"\x1b[?25h" + // cursor visible
	"\x1b[?1049l" // leave the alt screen: primary buffer and cursor restored

// RunAttach is the entry point of `uam __attach`: it puts the terminal in raw
// mode and bridges it to a session host — the native replacement for
// `tmux attach`. It returns when the user detaches (Ctrl+B d, or a bare left
// arrow while nothing is typed — see stdinFilter) or the agent exits.
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

	var ttyState *term.State
	if term.IsTerminal(stdin.Fd()) {
		state, err := term.MakeRaw(stdin.Fd())
		if err != nil {
			return fmt.Errorf("set raw mode: %w", err)
		}
		ttyState = state
	}
	ownScreen := term.IsTerminal(stdout.Fd())
	if ownScreen {
		_, _ = stdout.WriteString(screenEnter)
	}
	var once sync.Once
	restore := func() {
		once.Do(func() {
			if ownScreen {
				_, _ = stdout.WriteString(screenExit)
			}
			if ttyState != nil {
				_ = term.Restore(stdin.Fd(), ttyState)
			}
		})
	}
	defer restore()

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)

	// An external SIGINT/SIGTERM/SIGHUP must restore the screen and termios
	// like a detach would, or the terminal is left raw on the agent's output.
	// Ctrl+C inside the session never lands here: raw mode clears ISIG, so it
	// reaches the agent as a plain byte.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(quit)
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
		pumpStdin(stdin, conn, backDetachEnabled())
		close(detached)
	}()

	// host → terminal (the main loop): ends when the host closes the
	// connection (agent exited) or the user detached. done is closed once the
	// pump has fully drained, so a second receive never blocks.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(stdout, br)
		done <- err
		close(done)
	}()
	var note string
	select {
	case <-detached:
		_ = writeFrame(conn, frameDetach, nil)
		note = "detached"
	case <-done:
		note = "session ended"
	case <-quit:
		_ = writeFrame(conn, frameDetach, nil)
		note = "detached"
	}
	// Stop the host→terminal pump and drain it before restoring the screen:
	// bytes still buffered from the socket must land inside the alternate
	// screen, not on the primary screen revealed after screenExit. On the
	// session-ended path the pump has already finished and done is closed, so
	// this returns immediately.
	_ = conn.Close()
	<-done
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

// backDetachEnabled reports whether the left-arrow quick detach is on. It is
// the default; UAM_ATTACH_BACK_DETACH=0 restores pure passthrough for agents
// that bind a bare left arrow themselves.
func backDetachEnabled() bool {
	return os.Getenv("UAM_ATTACH_BACK_DETACH") != "0"
}

// stdinFilter is the attach client's input state machine. Besides the detach
// chord and Ctrl+Z swallowing, it implements the Claude-Code-style quick
// detach: pressing the left arrow detaches when the agent's input box is
// (believed) empty.
//
// uam is a byte bridge and cannot see the agent's real input box, so "empty"
// is approximated locally: typedSinceClear flips on anything that could put
// text in the box (printables, tab, history/menu navigation via forwarded
// escape sequences) and resets on the keys that submit or clear it (Enter,
// Esc, Ctrl+C, Ctrl+U). A bare left arrow while clear detaches; inside a
// draft it keeps moving the cursor. Ctrl+B d always detaches regardless.
type stdinFilter struct {
	backDetach bool
	// pendingPrefix is set after Ctrl+B, waiting for the chord's second key.
	pendingPrefix bool
	// esc accumulates a partial escape sequence (possibly across reads).
	esc []byte
	// typedSinceClear approximates "the agent's input box is non-empty".
	typedSinceClear bool
}

// maxEscLen bounds escape-sequence accumulation; anything longer is flushed
// through verbatim rather than parsed.
const maxEscLen = 8

// pumpStdin forwards terminal input to the host, filtering the detach chord,
// Ctrl+Z, and (when enabled) the left-arrow quick detach. It returns when the
// user detaches or stdin/conn fails.
func pumpStdin(stdin io.Reader, conn net.Conn, backDetach bool) {
	f := &stdinFilter{backDetach: backDetach}
	buf := make([]byte, 4096)
	for {
		n, err := stdin.Read(buf)
		if n > 0 {
			out, detach := f.filter(buf[:n])
			if len(out) > 0 {
				if werr := writeFrame(conn, frameStdin, out); werr != nil {
					return
				}
			}
			if detach {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// filter processes one stdin chunk, returning the bytes to forward and
// whether the user detached. On detach the returned bytes (anything typed
// before the detach key in the same chunk) must still be flushed first.
func (f *stdinFilter) filter(chunk []byte) (out []byte, detach bool) {
	out = make([]byte, 0, len(chunk)+1)
	for i, b := range chunk {
		if f.pendingPrefix {
			f.pendingPrefix = false
			switch b {
			case 'd':
				return out, true
			case detachPrefix:
				out = append(out, detachPrefix)
				f.typedSinceClear = true
			default:
				out = append(out, detachPrefix, b)
				f.typedSinceClear = true
			}
			continue
		}
		if len(f.esc) > 0 {
			var fired bool
			out, fired = f.escByte(out, b)
			if fired {
				return out, true
			}
			continue
		}
		switch b {
		case detachPrefix:
			f.pendingPrefix = true
		case ctrlZ:
			// Swallowed; see ctrlZ doc.
		case 0x1b:
			f.esc = append(f.esc, b)
			// Terminals write a full key's sequence atomically, so an ESC
			// that ends the chunk is a bare Esc press, not a sequence start.
			// Forward it immediately — delaying Esc would lag interrupts —
			// and treat it as clearing the input box (Claude Code semantics).
			if i == len(chunk)-1 {
				out = append(out, 0x1b)
				f.esc = nil
				f.typedSinceClear = false
			}
		case '\r', '\n', 0x03, 0x15:
			// Enter submits; Ctrl+C and Ctrl+U clear the input box.
			out = append(out, b)
			f.typedSinceClear = false
		default:
			out = append(out, b)
			if b >= 0x20 || b == '\t' {
				f.typedSinceClear = true
			}
		}
	}
	return out, false
}

// escByte feeds one byte into a pending escape sequence. It returns the
// updated forward buffer and whether the left-arrow quick detach fired.
func (f *stdinFilter) escByte(out []byte, b byte) ([]byte, bool) {
	f.esc = append(f.esc, b)
	if !escComplete(f.esc) {
		if len(f.esc) > maxEscLen {
			out = append(out, f.esc...)
			f.esc = nil
			f.typedSinceClear = true
		}
		return out, false
	}
	seq := f.esc
	f.esc = nil
	if f.backDetach && !f.typedSinceClear && isLeftArrow(seq) {
		return out, true
	}
	// Any other navigation may recall history or move through a menu, either
	// of which can leave text in the input box — be conservative and require
	// a fresh submit/clear before the quick detach re-arms.
	out = append(out, seq...)
	f.typedSinceClear = true
	return out, false
}

// escComplete reports whether esc (starting with ESC, len >= 2) is a full
// sequence: CSI (ESC [ ... final 0x40–0x7e), SS3 (ESC O x), or a two-byte
// alt/meta escape.
func escComplete(esc []byte) bool {
	if len(esc) < 2 {
		return false
	}
	switch esc[1] {
	case '[':
		return len(esc) > 2 && esc[len(esc)-1] >= 0x40 && esc[len(esc)-1] <= 0x7e
	case 'O':
		return len(esc) == 3
	default:
		return true
	}
}

// isLeftArrow matches an unmodified left arrow: CSI D (normal) or SS3 D
// (application cursor mode). Modified arrows (e.g. shift-left, ESC[1;2D) are
// real edits and pass through.
func isLeftArrow(seq []byte) bool {
	return string(seq) == "\x1b[D" || string(seq) == "\x1bOD"
}
