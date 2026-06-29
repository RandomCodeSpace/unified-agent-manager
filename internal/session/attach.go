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

const AttachQuietEnv = "UAM_ATTACH_QUIET"

type attachOptions struct {
	quiet bool
}

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
// primary screen and cursor underneath, then disables alternate scroll mode
// (?1007, default-on in VTE terminals): on the alt screen with mouse
// reporting off it turns wheel motion into arrow keys typed into the agent.
// The user's setting is saved first (XTSAVE) and restored by screenExit;
// terminals without ?1007 ignore all three sequences.
const screenEnter = "\x1b[?1049h" + "\x1b[?1007s" + "\x1b[?1007l"

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
	"\x1b[?1007r" + // alternate scroll back to the user's saved setting (XTRESTORE)
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
	return runAttachWithOptions(*dir, fs.Arg(0), os.Stdin, os.Stdout, attachOptions{quiet: os.Getenv(AttachQuietEnv) == "1"})
}

func runAttach(dir, name string, stdin *os.File, stdout *os.File) error {
	return runAttachWithOptions(dir, name, stdin, stdout, attachOptions{})
}

func runAttachWithOptions(dir, name string, stdin *os.File, stdout *os.File, opts attachOptions) error {
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
	if !opts.quiet {
		_, _ = fmt.Fprintf(stdout, "\r\n[uam: %s]\r\n", note)
	}
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
// is approximated locally: typed counts the runes put into the box, backspace
// deletes one (so deleting the whole draft re-arms the quick detach), and
// unknown latches on anything whose effect cannot be counted (tab completion,
// history/menu navigation via forwarded escape sequences, a literal prefix
// byte) until a key that submits or clears the box (Enter, Esc, Ctrl+C,
// Ctrl+U). A bare left arrow while the box is believed empty detaches; inside
// a draft it keeps moving the cursor. Ctrl+B d always detaches regardless.
//
// Not everything on stdin is a keystroke: agents query the terminal (Ink
// re-requests the cursor position every render) and the replies — CPR, DA1,
// kitty flags, OSC/DCS strings — arrive on the same fd, as do mouse and
// focus events. Terminal-generated traffic never reaches the agent's input
// box, so it is forwarded without touching the estimate (see seqPoisons);
// counting it would wedge the quick detach until the next Enter.
type stdinFilter struct {
	backDetach bool
	// pendingPrefix is set after Ctrl+B, waiting for the chord's second key.
	pendingPrefix bool
	// esc accumulates a partial escape sequence (possibly across reads).
	esc []byte
	// typed approximates the number of runes in the agent's input box.
	typed int
	// unknown latches when the box may hold text typed cannot account for.
	unknown bool
	// strActive marks an OSC/DCS/SOS/PM/APC string sequence being consumed
	// verbatim (a terminal reply to an agent query); strBel allows the OSC
	// BEL terminator, strEsc tracks a possible ST (ESC \), and strLen caps
	// runaway sequences at maxStrLen.
	strActive bool
	strBel    bool
	strEsc    bool
	strLen    int
}

// boxEmpty reports whether the agent's input box is believed empty.
func (f *stdinFilter) boxEmpty() bool { return !f.unknown && f.typed == 0 }

// clearBox resets the estimate on keys that submit or clear the input box.
func (f *stdinFilter) clearBox() { f.typed, f.unknown = 0, false }

// maxEscLen bounds escape-sequence accumulation; anything longer is flushed
// through verbatim rather than parsed. Sized for terminal replies, not just
// keystrokes — a DA1 attribute list runs ~40 bytes.
const maxEscLen = 64

// maxStrLen bounds string-sequence (OSC/DCS) consumption the same way;
// color-query and XTGETTCAP replies stay well under it.
const maxStrLen = 4096

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
		if f.strActive {
			out = f.strByte(out, b)
			continue
		}
		if f.pendingPrefix {
			f.pendingPrefix = false
			switch b {
			case 'd':
				return out, true
			case detachPrefix:
				out = append(out, detachPrefix)
				f.unknown = true
			default:
				out = append(out, detachPrefix, b)
				f.unknown = true
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
				f.clearBox()
			}
		case '\r', '\n', 0x03, 0x15:
			// Enter submits; Ctrl+C and Ctrl+U clear the input box.
			out = append(out, b)
			f.clearBox()
		case 0x08, 0x7f:
			// Backspace deletes one rune; deleting the whole draft re-arms
			// the quick detach. On an empty box it is a no-op.
			out = append(out, b)
			if f.typed > 0 {
				f.typed--
			}
		case '\t':
			// Tab completion can insert text uam cannot count; disarm until
			// the next submit/clear.
			out = append(out, b)
			f.unknown = true
		default:
			out = append(out, b)
			// Count one per rune: skip UTF-8 continuation bytes.
			if b >= 0x20 && b&0xc0 != 0x80 {
				f.typed++
			}
		}
	}
	return out, false
}

// escByte feeds one byte into a pending escape sequence. It returns the
// updated forward buffer and whether the left-arrow quick detach fired.
func (f *stdinFilter) escByte(out []byte, b byte) ([]byte, bool) {
	f.esc = append(f.esc, b)
	if len(f.esc) == 2 {
		switch b {
		case ']', 'P', 'X', '^', '_':
			// OSC/DCS/SOS/PM/APC: a string sequence — in practice a terminal
			// reply to an agent query (OSC 10/11 colors, DCS XTGETTCAP).
			// Hand off to strByte, which consumes it through its terminator.
			out = append(out, f.esc...)
			f.strActive, f.strBel = true, b == ']'
			f.strEsc, f.strLen = false, len(f.esc)
			f.esc = nil
			return out, false
		}
	}
	if !escComplete(f.esc) {
		if len(f.esc) > maxEscLen {
			out = append(out, f.esc...)
			f.esc = nil
			f.unknown = true
		}
		return out, false
	}
	seq := f.esc
	f.esc = nil
	if f.backDetach && f.boxEmpty() && isLeftArrow(seq) {
		return out, true
	}
	out = append(out, seq...)
	if seqPoisons(seq) {
		// Navigation may recall history or move through a menu, either of
		// which can leave text in the input box — be conservative and require
		// a fresh submit/clear before the quick detach re-arms.
		f.unknown = true
	}
	return out, false
}

// strByte consumes one byte of an in-flight string sequence, forwarding it
// verbatim. The sequence ends at ST (ESC \) or, for OSC only, BEL. Reply
// payloads are not keystrokes, so the input-box estimate stays untouched; a
// sequence exceeding maxStrLen is assumed malformed and poisons it instead.
func (f *stdinFilter) strByte(out []byte, b byte) []byte {
	out = append(out, b)
	f.strLen++
	switch {
	case f.strEsc:
		if b == '\\' { // ST: sequence complete
			f.strActive, f.strEsc = false, false
			return out
		}
		f.strEsc = b == 0x1b
	case b == 0x1b:
		f.strEsc = true
	case b == 0x07 && f.strBel: // BEL terminates OSC
		f.strActive = false
		return out
	}
	if f.strLen > maxStrLen {
		f.strActive, f.strEsc = false, false
		f.unknown = true
	}
	return out
}

// seqPoisons reports whether a completed escape sequence may change the
// agent's input box. Keystrokes (arrows, function keys, alt/meta chords) can
// recall history or navigate menus, so they poison the empty-box estimate;
// terminal replies (cursor position, device attributes, kitty flags, mode
// reports) and terminal events (mouse, focus) never reach the input box and
// stay neutral.
func seqPoisons(seq []byte) bool {
	if len(seq) < 3 || seq[1] != '[' {
		return true // SS3 keys and alt/meta chords are real input
	}
	switch seq[2] {
	case '<', '?', '>':
		// Private-parameter CSI: SGR mouse (CSI < ... M/m), DEC replies
		// (CSI ? ... c/u/n, CSI ? ... $y) — none are keystrokes.
		return false
	}
	switch seq[len(seq)-1] {
	case 'R', 'c', 'n', 'y', 't', 'I', 'O', 'M':
		// CPR, device attributes, status reports, mode/window reports,
		// focus in/out, legacy mouse. Known xterm grammar collision: a
		// modified F3 (CSI 1;2R) is indistinguishable from a CPR at row 1
		// col 2, and either parameter heuristic misreads common cursor
		// positions. CPR wins — Ink agents request it on every render,
		// while no supported agent binds modified F3 to text entry, and a
		// misfired quick detach leaves the draft intact in the agent.
		return false
	}
	return true
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
