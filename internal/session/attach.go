package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
)

// detachPrefix is the attach client's escape key (Ctrl+B, tmux's default
// prefix, kept for muscle memory). Prefix then `d` detaches, prefix then `c`
// sends a literal Ctrl+C to the agent, and prefix twice sends a literal Ctrl+B.
const detachPrefix = 0x02

const ctrlC = 0x03

const AttachQuietEnv = "UAM_ATTACH_QUIET"
const AttachMouseEnv = "UAM_ATTACH_MOUSE"
const AttachSelectedProfileEnv = "UAM_ATTACH_SELECTED_PROFILE"
const AttachEffectiveProfileEnv = "UAM_ATTACH_EFFECTIVE_PROFILE"

// attachMouseEnabled resolves the per-viewer mouse policy. Providers keep mouse
// support locally and over SSH by default so wheel and touch scrolling work.
// Explicit off leaves mouse gestures under terminal control for selection/paste.
func attachMouseEnabled(getenv func(string) string) bool {
	return getenv(AttachMouseEnv) != "off"
}

func attachProfileFromEnv(getenv func(string) string) attachProfileSnapshot {
	return attachProfileSnapshot{selected: getenv(AttachSelectedProfileEnv), effective: getenv(AttachEffectiveProfileEnv)}
}

type attachOptions struct {
	quiet         bool
	requestedRole clientRole
	profile       attachProfileSnapshot
	policy        attachPolicySnapshot
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

// mouseTrackingModes are the mutually exclusive tracking levels: a terminal
// keeps one, so setting 1003 replaces 1002 rather than adding to it.
var mouseTrackingModes = []string{"9", "1000", "1001", "1002", "1003"}

// mouseEncodingModes select how a tracked event is encoded. Unlike the tracking
// level these are independent flags.
var mouseEncodingModes = []string{"1005", "1006", "1015", "1016"}

// mouseModes are the DEC private modes that make up provider mouse reporting.
// One list drives every use of the set — the output filter's suppression set,
// the reset a viewer sends when it turns passthrough off, and the teardown in
// screenReset — so no mode can be suppressed on the way in without also being
// reset on the way out. 9 (X10), 1001 (highlight tracking) and 1016 (SGR-pixel)
// are included: the previous per-site literals disagreed about all three, which
// left them alive in the user's terminal after detach.
var mouseModes = append(append([]string{}, mouseTrackingModes...), mouseEncodingModes...)

// mouseReset turns every mode in mouseModes off.
var mouseReset = privateModeSequence(mouseModes, 'l')

// screenExit resets every mode the agent could have toggled mid-attach, then
// leaves the alternate screen. Terminals ignore sequences they don't
// implement, so the suffix is safe to emit unconditionally.
//
// The kitty keyboard pops come first and must precede ?1049l: every push the
// agent made — live or replayed by Redraw — sits on the physical alternate-
// screen stack, which the terminal keeps across screen switches. The client
// cannot learn the real depth from the wire protocol, so it pops the deepest
// stack a terminal keeps (kitty: 7 above the base; popping past empty is
// defined to reset all flags) and then zeroes the base an agent may have set.
var screenReset = "\x1b[<7u" + // empty the kitty keyboard stack
	"\x1b[=0;1u" + // and zero the base (CSI = flags ; 1 u leaves no stack entry)
	mouseReset + // mouse tracking off
	"\x1b[?1004l" + // focus reporting off
	"\x1b[?2004l" + // bracketed paste off
	"\x1b[?2026l" + // synchronized output off
	"\x1b[!p" + // DECSTR: cursor keys, origin, margins, SGR, insert mode
	"\x1b>" + // numeric keypad (DECKPNM; DECSTR leaves keypad mode alone)
	"\x1b(B" + // G0 charset back to ASCII
	"\x1b[?25h" // cursor visible

var screenExit = screenReset +
	"\x1b[?1007r" + // alternate scroll back to the user's saved setting (XTRESTORE)
	"\x1b[?1049l" // leave the alt screen: primary buffer and cursor restored

// privateModeSequence builds a single DEC private set ('h') or reset ('l')
// sequence for modes. It returns "" for an empty set so callers never emit the
// parameterless `CSI ? h`, which means mode 0 to a real terminal.
func privateModeSequence(modes []string, final byte) string {
	if len(modes) == 0 {
		return ""
	}
	return "\x1b[?" + strings.Join(modes, ";") + string(final)
}

// RunAttach is the entry point of `uam __attach`: it puts the terminal in raw
// mode and bridges it to a session host — the native replacement for
// `tmux attach`. It returns when the user detaches (Ctrl+B d, or Ctrl+Left
// while nothing is typed — see stdinFilter) or the agent exits.
func RunAttach(args []string) error {
	fs := flag.NewFlagSet("__attach", flag.ContinueOnError)
	dir := fs.String("dir", DefaultDir(), "session runtime directory")
	requestedRole := fs.String("role", string(roleController), "attach role: controller or observer")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return errors.New("attach requires a session name")
	}
	role := clientRole(*requestedRole)
	if role != roleController && role != roleObserver {
		return fmt.Errorf("attach role must be %q or %q", roleController, roleObserver)
	}
	return runAttachWithOptions(*dir, fs.Arg(0), os.Stdin, os.Stdout, attachOptions{
		quiet: os.Getenv(AttachQuietEnv) == "1", requestedRole: role,
		profile: attachProfileFromEnv(os.Getenv), policy: attachPolicyFromEnv(os.Getenv),
	})
}

func runAttach(dir, name string, stdin *os.File, stdout *os.File) error {
	return runAttachWithOptions(dir, name, stdin, stdout, attachOptions{})
}

func runAttachWithOptions(dir, name string, stdin *os.File, stdout *os.File, opts attachOptions) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if err := VerifyDir(dir); err != nil {
		return err
	}
	policy, err := resolveAttachPolicy(opts.policy, os.Getenv)
	if err != nil {
		return err
	}
	conn, err := net.Dial("unix", SocketPath(dir, name))
	if err != nil {
		return fmt.Errorf("session %s is not running: %w", name, err)
	}
	defer func() { _ = conn.Close() }()

	requestedRole := opts.requestedRole
	if requestedRole == "" {
		requestedRole = roleController
	}
	if requestedRole != roleController && requestedRole != roleObserver {
		return fmt.Errorf("unsupported attach role %q", requestedRole)
	}
	cols, rows := 0, 0
	if w, h, err := term.GetSize(stdout.Fd()); err == nil {
		cols, rows = w, h
	}
	inputTerminal := term.IsTerminal(stdin.Fd())
	terminalOutput := term.IsTerminal(stdout.Fd())
	ownScreen := terminalOutput && attachOwnsOuterScreen(dir, name)
	// On uam's own alternate screen the last row is reserved for the status
	// bar: the host sizes the provider one row short and the client pins the
	// scroll region above the bar. Primary-screen providers keep every row —
	// a scroll region there would cut them off from the terminal's scrollback,
	// which is the whole reason they stay on the primary screen.
	statusRows := statusBarReservation(ownScreen, rows)
	display, cwd := statusBarIdentity(dir, name)
	hello := defaultClientHello(inputTerminal && terminalOutput, os.Getenv("TERM"), os.Getenv("COLORTERM"))
	handshake, err := performAttachHandshake(conn, name, request{
		Op: opAttach, Cols: cols, Rows: rows - statusRows, Version: protocolV2, RequestedRole: requestedRole, Hello: &hello,
	})
	if err != nil {
		return err
	}
	frames := newAttachFrameWriter(conn, handshake.version, handshake.clientID, handshake.generation)
	frames.SetAssignedRole(handshake.assignedRole)
	output := &synchronizedWriter{writer: stdout}
	cleanup, err := beginAttachTerminal(attachTerminalConfig{
		input: stdin, output: output, inputTerminal: inputTerminal, outputTerminal: terminalOutput, ownScreen: ownScreen,
	})
	if err != nil {
		return err
	}
	defer func() { _ = cleanup.Restore() }()
	runtime := newAttachRuntime(attachRuntimeConfig{
		session: name, output: output, input: stdin, inputTerminal: inputTerminal, mouseEnabled: policy.mouseEnabled, prefix: policy.controlPrefix, profile: opts.profile,
		statusRows: statusRows, role: handshake.assignedRole, display: display, cwd: cwd,
		terminalSize: func() (int, int, bool) {
			if !terminalOutput {
				return 0, 0, false
			}
			w, h, err := term.GetSize(stdout.Fd())
			return w, h, err == nil
		},
	})
	defer runtime.stopStatusBar()
	if statusRows > 0 {
		// The bar is painted before the host's first frame so a slow provider
		// still shows where the user is; the frame's clear-screen is tracked
		// by the output filter and triggers a repaint.
		if err := runtime.paintStatusBar(true); err != nil {
			return err
		}
	} else if terminalOutput && handshake.version == protocolV2 {
		// Queued, not written: the host's first frame is a full repaint that
		// opens with a clear-screen, so a banner written here is erased before
		// the user can read it.
		runtime.queueStatus(fmt.Sprintf("role %s; %s i for info", handshake.assignedRole, controlPrefixName(policy.controlPrefix)))
	}

	winch := make(chan os.Signal, 1)
	signal.Notify(winch, syscall.SIGWINCH)
	defer signal.Stop(winch)
	stopWinch := make(chan struct{})
	defer close(stopWinch)

	// An external SIGINT/SIGTERM/SIGHUP must restore the screen and termios
	// like a detach would, or the terminal is left raw on the agent's output.
	// Ctrl+C inside the session never lands here: raw mode clears ISIG, so it
	// reaches the agent as a plain byte.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(quit)
	go func() {
		for {
			select {
			case <-stopWinch:
				return
			case <-winch:
				// Standbys report their size too. The host only applies the
				// controller's, but it records everyone's and uses the stored
				// size when it promotes — so a standby that stayed silent since
				// attaching was promoted into a stale geometry.
				if w, h, err := term.GetSize(stdout.Fd()); err == nil {
					_ = frames.WriteFrame(frameResize, resizePayload(w, h-statusRows))
					_ = runtime.paintStatusBar(true)
				}
			}
		}
	}()

	inputDone := make(chan error, 1)
	stopInput := make(chan struct{})
	inputFD, err := attachInputFD(stdin.Fd())
	if err != nil {
		return err
	}
	go func() {
		inputDone <- pumpAttachInput(attachPumpConfig{
			input: stdin, inputFD: inputFD, frames: frames, runtime: runtime, prefix: policy.controlPrefix, backDetach: policy.backDetach, stop: stopInput,
		})
	}()

	// host → terminal (the main loop): ends when the host closes the
	// connection (agent exited) or the user detached. done is closed once the
	// pump has fully drained, so a second receive never blocks.
	outputDone := make(chan error, 1)
	go func() {
		outputDone <- copyAttachOutputConfigured(attachOutputConfig{
			output: output, reader: handshake.reader, version: handshake.version, frames: frames, runtime: runtime,
		})
	}()
	var note string
	var inputErr error
	var outputErr error
	outputFinished := false
	inputFinished := false
	detached := false
	select {
	case inputErr = <-inputDone:
		inputFinished = true
		_ = frames.WriteFrame(frameDetach, nil)
		note = "detached"
		detached = true
	case outputErr = <-outputDone:
		note = "session ended"
		outputFinished = true
	case <-quit:
		_ = frames.WriteFrame(frameDetach, nil)
		note = "detached"
		detached = true
	}
	close(stopInput)
	// Stop the host→terminal pump and drain it before restoring the screen:
	// bytes still buffered from the socket must land inside the alternate
	// screen, not on the primary screen revealed after screenExit. On the
	// session-ended path the pump has already finished and done is closed, so
	// this returns immediately.
	_ = conn.Close()
	if !outputFinished {
		outputErr = <-outputDone
	}
	if !inputFinished {
		inputErr = <-inputDone
	}
	// A pending bar restore must never fire onto the primary screen.
	runtime.stopStatusBar()
	restoreErr := cleanup.Restore()
	if inputErr != nil {
		return errors.Join(inputErr, restoreErr)
	}
	if outputErr != nil && !detached {
		return errors.Join(fmt.Errorf("attach output: %w", outputErr), restoreErr)
	}
	if restoreErr != nil {
		return restoreErr
	}
	if !opts.quiet {
		if _, err := fmt.Fprintf(output, "\r\n[uam: %s]\r\n", note); err != nil {
			return fmt.Errorf("write attach completion: %w", err)
		}
	}
	return nil
}

type attachHandshake struct {
	reader       *bufio.Reader
	version      protocolVersion
	clientID     string
	assignedRole clientRole
	generation   uint64
}

func performAttachHandshake(conn net.Conn, name string, req request) (attachHandshake, error) {
	if err := writeJSONLine(conn, req); err != nil {
		return attachHandshake{}, fmt.Errorf("attach %s send handshake: %w", name, err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(attachHandshakeTimeout)); err != nil {
		return attachHandshake{}, fmt.Errorf("attach %s set handshake deadline: %w", name, err)
	}
	br := bufio.NewReader(conn)
	var resp response
	if err := readBoundedJSONLine(br, &resp); err != nil {
		return attachHandshake{}, fmt.Errorf("attach %s read handshake: %w", name, err)
	}
	if !resp.OK {
		return attachHandshake{}, fmt.Errorf("attach %s rejected: %s", name, resp.Err)
	}
	version, err := negotiateAttachResponse(req.Version, resp)
	if err != nil {
		return attachHandshake{}, fmt.Errorf("attach %s negotiate: %w", name, err)
	}
	assignedRole := roleController
	if version == protocolV2 {
		if resp.ClientID == "" {
			return attachHandshake{}, fmt.Errorf("attach %s negotiate: missing client ID", name)
		}
		if err := validateRequestedRole(resp.AssignedRole); err != nil {
			return attachHandshake{}, fmt.Errorf("attach %s negotiate assigned role: %w", name, err)
		}
		assignedRole = resp.AssignedRole
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return attachHandshake{}, fmt.Errorf("attach %s clear handshake deadline: %w", name, err)
	}
	return attachHandshake{reader: br, version: version, clientID: resp.ClientID, assignedRole: assignedRole, generation: resp.Generation}, nil
}

func copyAttachOutput(dst io.Writer, br *bufio.Reader, version protocolVersion, mouse bool) error {
	return copyAttachOutputWithControls(dst, br, version, mouse, nil)
}

func copyAttachOutputWithControls(dst io.Writer, br *bufio.Reader, version protocolVersion, mouse bool, observeControl func([]byte)) error {
	filter := newAttachOutputFilter(dst, mouse)
	if version == protocolV1 {
		_, err := io.Copy(filter, br)
		if flushErr := filter.Flush(); err == nil {
			err = flushErr
		}
		return err
	}
	for {
		kind, payload, err := readFrame(br)
		if errors.Is(err, io.EOF) {
			return filter.Flush()
		}
		if err != nil {
			return err
		}
		if err := consumeAttachServerFrame(filter, kind, payload, observeControl); err != nil {
			return err
		}
	}
}

func consumeAttachServerFrame(filter *attachOutputFilter, kind byte, payload []byte, observeControl func([]byte)) error {
	switch kind {
	case serverFramePTY:
		_, err := filter.Write(payload)
		return err
	case serverFrameControl:
		if observeControl != nil {
			observeControl(payload)
		}
		return nil
	default:
		return fmt.Errorf("unsupported server attach frame type %d", kind)
	}
}

// statusBarIdentity is what the status bar calls the session: the launcher's
// label ("<name> · <provider>") plus the short id from the canonical session
// name, or the canonical name itself for sessions that were never labelled.
// The working directory comes from the same state file, with the home
// directory shortened to "~". The canonical name stays the machine identifier:
// hosts, sockets and listings all parse it.
func statusBarIdentity(dir, name string) (display, cwd string) {
	display = name
	state, err := readState(dir, name)
	if err != nil {
		return display, ""
	}
	if state.Label != "" {
		display = state.Label
		if id := name[strings.LastIndexByte(name, '-')+1:]; id != "" && id != name {
			display += " · " + id
		}
	}
	cwd = state.Cwd
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		if cwd == home {
			cwd = "~"
		} else if strings.HasPrefix(cwd, home+"/") {
			cwd = "~" + cwd[len(home):]
		}
	}
	return display, cwd
}

// statusBarReservation is how many terminal rows the attach client keeps for
// its status bar: one on uam's own alternate screen when there is room for a
// provider row above it, otherwise none.
func statusBarReservation(ownScreen bool, rows int) int {
	if ownScreen && rows >= 2 {
		return 1
	}
	return 0
}

func resizePayload(cols, rows int) []byte {
	// Clamp to uint16 range; the host rejects anything over 1000 anyway.
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:2], uint16(max(0, min(cols, 0xffff)))) // #nosec G115 -- clamped
	binary.BigEndian.PutUint16(out[2:4], uint16(max(0, min(rows, 0xffff)))) // #nosec G115 -- clamped
	return out
}

// stdinFilter is the attach client's input state machine. Besides the detach
// chord and Ctrl+Z swallowing, it implements the quick detach: pressing
// Ctrl+Left detaches when the agent's input box is (believed) empty. Bare
// arrows are never taken — providers bind them (for example, to switch views
// with left/right, copilot opens its sidebar on left) — while Ctrl+Left is
// word-left in every composer, a no-op on an empty box.
//
// uam is a byte bridge and cannot see the agent's real input box, so "empty"
// is approximated locally: typed counts the runes put into the box, backspace
// deletes one (so deleting the whole draft re-arms the quick detach), and
// unknown latches on anything whose effect cannot be counted (tab completion,
// history/menu navigation via forwarded escape sequences, a literal prefix
// byte) until a key that submits or clears the box (Enter, Esc, Ctrl+U). Plain
// Ctrl+C is swallowed so terminal copy shortcuts cannot cancel the agent;
// Ctrl+B c sends a literal Ctrl+C when an explicit interrupt is needed.
// Ctrl+Left while the box is believed empty detaches; inside a draft it keeps
// moving the cursor by a word. Ctrl+B d always detaches regardless.
//
// Not everything on stdin is a keystroke: agents query the terminal (Ink
// re-requests the cursor position every render) and the replies — CPR, DA1,
// kitty flags, OSC/DCS strings — arrive on the same fd, as do mouse and
// focus events. Terminal-generated traffic never reaches the agent's input
// box, so it is forwarded without touching the estimate (see seqPoisons);
// counting it would wedge the quick detach until the next Enter.
type stdinFilter struct {
	prefix     byte
	backDetach bool
	role       clientRole
	commands   []attachCommand
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
	strActive  bool
	strBel     bool
	strEsc     bool
	strLen     int
	inPaste    bool
	pasteStart int
	pasteEnd   int
	pasteLen   int
	// mouseBody counts the raw bytes still to come in a legacy X10/normal
	// mouse report (CSI M Cb Cx Cy). They are binary, not keystrokes.
	mouseBody int
}

// x10MouseBodyLen is the button-and-coordinate payload that follows CSI M when
// the terminal reports mouse events in the legacy encoding (no ?1006).
const x10MouseBodyLen = 3

var pasteBegin = []byte("\x1b[200~")
var pasteEnd = []byte("\x1b[201~")

// prefixByte is the configured control prefix, defaulting to Ctrl+B.
func (f *stdinFilter) prefixByte() byte {
	if f.prefix == 0 {
		return detachPrefix
	}
	return f.prefix
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

// maxPasteLen bounds bracketed-paste passthrough. Sized far above any
// interactive paste: past the cap the tail is parsed as keystrokes again, which
// is the right trade only once a missing end marker is the likelier
// explanation.
const maxPasteLen = 8 << 20

// filter processes one stdin chunk, returning the bytes to forward and
// whether the user detached. On detach the returned bytes (anything typed
// before the detach key in the same chunk) must still be flushed first.
func (f *stdinFilter) filter(chunk []byte) (out []byte, detach bool) {
	out = make([]byte, 0, len(chunk)+1)
	prefix := f.prefixByte()
	for i, b := range chunk {
		if f.inPaste {
			out = append(out, b)
			f.pasteLen++
			f.pasteEnd = advanceExactMatch(pasteEnd, f.pasteEnd, b)
			if f.pasteEnd == len(pasteEnd) || f.pasteLen > maxPasteLen {
				// A terminal that never sends the end marker (or a stream that
				// is not really a paste) would otherwise leave the filter
				// forwarding everything forever — detach chord included.
				f.inPaste, f.pasteEnd, f.pasteLen = false, 0, 0
				f.unknown = true
			}
			continue
		}
		if f.strActive {
			var consumed bool
			if out, consumed = f.strByte(out, b); consumed {
				continue
			}
		}
		if f.mouseBody > 0 {
			// Legacy mouse report payload: forward verbatim. Counting these
			// bytes as typed runes disarmed the quick detach on every click and
			// wheel tick until the next Enter. The count is in bytes, not
			// runes: a legacy coordinate is 32+value clamped at 223, so columns
			// 97–160 are indistinguishable from a UTF-8 continuation byte and
			// skipping them would overrun the drain into the next keystroke.
			out = append(out, b)
			f.mouseBody--
			continue
		}
		f.pasteStart = advanceExactMatch(pasteBegin, f.pasteStart, b)
		if f.pasteStart == len(pasteBegin) {
			// The marker may have accumulated in esc, or its ESC may already
			// have been forwarded at a read boundary. Emit only what remains.
			if len(f.esc) > 0 {
				out = append(out, f.esc...)
				f.esc = nil
			}
			out = append(out, b)
			f.pasteStart, f.inPaste = 0, true
			continue
		}
		if f.pendingPrefix {
			f.pendingPrefix = false
			switch b {
			case 'd':
				return f.result(out, true)
			case 'c':
				out = append(out, ctrlC)
				f.clearBox()
			case 'r':
				f.commands = append(f.commands, commandRequestControl)
			case 'o':
				f.commands = append(f.commands, commandTransferControl)
			case 'i':
				f.commands = append(f.commands, commandShowInfo)
			case 'm':
				f.commands = append(f.commands, commandToggleMouse)
			case prefix:
				out = append(out, prefix)
				f.unknown = true
			default:
				out = append(out, prefix, b)
				f.unknown = true
			}
			continue
		}
		if len(f.esc) > 0 {
			var fired bool
			out, fired = f.escByte(out, b)
			if fired {
				return f.result(out, true)
			}
			continue
		}
		switch b {
		case prefix:
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
		case ctrlC:
			// Swallowed so terminal copy cannot cancel the agent. Use Ctrl+B c
			// when a literal interrupt needs to be sent through.
			f.clearBox()
		case '\r', '\n', 0x15:
			// Enter submits; Ctrl+U clears the input box.
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
	return f.result(out, false)
}

func (f *stdinFilter) result(out []byte, detach bool) ([]byte, bool) {
	if f.role == roleObserver {
		return nil, detach
	}
	return out, detach
}

func (f *stdinFilter) drainCommands() []attachCommand {
	commands := f.commands
	f.commands = nil
	return commands
}

func advanceExactMatch(pattern []byte, matched int, b byte) int {
	if b == pattern[matched] {
		return matched + 1
	}
	if b == pattern[0] {
		return 1
	}
	return 0
}

const maxAttachCSI = 4096

var attachAltModes = map[string]bool{"47": true, "1047": true, "1049": true}
var attachMouseModes = newModeSet(mouseModes)

func newModeSet(modes []string) map[string]bool {
	set := make(map[string]bool, len(modes))
	for _, mode := range modes {
		set[mode] = true
	}
	return set
}

// attachOutputFilter contains provider-owned alternate-screen toggles inside
// the attach screen and optionally leaves mouse modes under terminal control.
// Only seven-bit DEC private h/l sequences are rewritten.
type attachOutputFilter struct {
	dst          io.Writer
	mouseEnabled func() bool
	pending      []byte
	abortedCSI   bool
	forwardedCSI bool
	// mouseMu guards mouseState: the filter writes it from the host→terminal
	// pump while `prefix m` reads it from the input pump.
	mouseMu sync.Mutex
	// mouseState records which encoding modes the provider currently has on,
	// and mouseTracking its current tracking level (empty when off), both
	// tracked whether or not passthrough is suppressing them.
	mouseState    map[string]bool
	mouseTracking string
	// viewportRows reports the provider's row count when the client reserves
	// a status bar row (0 otherwise). Scroll-region sequences are clamped to
	// it so a provider resetting its margins cannot scroll the bar away.
	viewportRows func() int
	// damage records provider output that clobbered the status bar row
	// (clear-screen) or dropped the pinned scroll region (terminal resets);
	// the runtime repaints after the write.
	damage statusBarDamage
}

// statusBarDamage is what the output filter saw the provider do to the rows
// the attach client owns.
type statusBarDamage uint8

const (
	damageNone  statusBarDamage = iota
	damageClear                 // ED 2/3: the bar row was erased
	damageReset                 // DECSTR or RIS: margins and the bar are gone
)

// consumeDamage returns and clears the damage seen since the last call.
func (f *attachOutputFilter) consumeDamage() statusBarDamage {
	damage := f.damage
	f.damage = damageNone
	return damage
}

func (f *attachOutputFilter) noteDamage(damage statusBarDamage) {
	if damage > f.damage {
		f.damage = damage
	}
}

var attachMouseTrackingModes = newModeSet(mouseTrackingModes)

func newAttachOutputFilter(dst io.Writer, mouse bool) *attachOutputFilter {
	return &attachOutputFilter{dst: dst, mouseEnabled: func() bool { return mouse }}
}

func newAttachOutputFilterWithMouse(dst io.Writer, mouseEnabled func() bool) *attachOutputFilter {
	return &attachOutputFilter{dst: dst, mouseEnabled: mouseEnabled}
}

func (f *attachOutputFilter) Write(p []byte) (int, error) {
	// Ordinary output never exceeds the input length. A split control sequence
	// can add the small pending prefix back, in which case append grows the
	// buffer safely instead of computing a potentially overflowing capacity.
	out := make([]byte, 0, len(p))
	for _, b := range p {
		switch {
		case len(f.pending) == 0:
			if b == 0x1b {
				f.pending = append(f.pending, b)
				f.abortedCSI = f.forwardedCSI
				f.forwardedCSI = false
			} else {
				out = append(out, b)
				if f.forwardedCSI && (b == 0x18 || b == 0x1a || b >= 0x40 && b <= 0x7e) {
					f.forwardedCSI = false
				}
			}
		case len(f.pending) == 1:
			if b == '[' {
				f.pending = append(f.pending, b)
			} else {
				out = append(out, f.pending...)
				f.pending = f.pending[:0]
				f.abortedCSI = false
				if b == 0x1b {
					f.pending = append(f.pending, b)
				} else {
					if b == 'c' { // RIS: full reset drops margins and the bar
						f.noteDamage(damageReset)
					}
					out = append(out, b)
				}
			}
		case b == 0x18 || b == 0x1a:
			// CAN and SUB cancel CSI parsing and return the destination
			// terminal to ground. Preserve the provider's cancellation byte;
			// no synthetic cancellation is needed for a later filtered mode.
			out = append(out, f.pending...)
			out = append(out, b)
			f.pending = f.pending[:0]
			f.abortedCSI = false
			f.forwardedCSI = false
		case b == 0x1b:
			// ESC aborts an in-flight CSI in a real terminal. Flush the old
			// prefix and retain this ESC as the start of a new filterable
			// sequence instead of letting its '[' terminate the old CSI.
			out = append(out, f.pending...)
			f.pending = append(f.pending[:0], b)
			f.abortedCSI = true
		default:
			f.pending = append(f.pending, b)
			if b >= 0x40 && b <= 0x7e {
				rewritten := f.rewriteCSI(f.pending)
				if len(rewritten) == 0 && f.abortedCSI {
					// The filtered ESC would otherwise leave the previously
					// forwarded incomplete CSI active. CAN is the standard CSI
					// cancellation control and cannot begin another sequence.
					out = append(out, 0x18)
				} else {
					out = append(out, rewritten...)
				}
				f.pending = f.pending[:0]
				f.abortedCSI = false
			} else if len(f.pending) > maxAttachCSI {
				out = append(out, f.pending...)
				f.pending = f.pending[:0]
				f.abortedCSI = false
				f.forwardedCSI = true
			}
		}
	}
	if err := writeAttachBytes(f.dst, out); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (f *attachOutputFilter) Flush() error {
	err := writeAttachBytes(f.dst, f.pending)
	f.pending = f.pending[:0]
	return err
}

func (f *attachOutputFilter) rewriteCSI(seq []byte) []byte {
	if rewritten, handled := f.rewriteStatusBarCSI(seq); handled {
		return rewritten
	}
	if len(seq) < 5 || seq[0] != 0x1b || seq[1] != '[' || seq[2] != '?' || (seq[len(seq)-1] != 'h' && seq[len(seq)-1] != 'l') {
		return seq
	}
	params := bytes.Split(seq[3:len(seq)-1], []byte{';'})
	kept := make([][]byte, 0, len(params))
	removed := false
	for _, param := range params {
		if len(param) == 0 {
			return seq
		}
		for _, b := range param {
			if b < '0' || b > '9' {
				return seq
			}
		}
		key := string(param)
		if attachMouseModes[key] {
			f.recordMouseMode(key, seq[len(seq)-1] == 'h')
		}
		if attachAltModes[key] || (!f.mouseEnabled() && attachMouseModes[key]) {
			removed = true
			continue
		}
		kept = append(kept, param)
	}
	if !removed {
		return seq
	}
	if len(kept) == 0 {
		return nil
	}
	out := []byte("\x1b[?")
	out = append(out, bytes.Join(kept, []byte{';'})...)
	out = append(out, seq[len(seq)-1])
	return out
}

// rewriteStatusBarCSI tracks the sequences that matter to a reserved status
// bar and clamps DECSTBM into the provider's viewport. Everything it does not
// recognise is reported unhandled so the private-mode rewrite runs as before.
func (f *attachOutputFilter) rewriteStatusBarCSI(seq []byte) ([]byte, bool) {
	if len(seq) < 3 || seq[0] != 0x1b || seq[1] != '[' {
		return seq, false
	}
	params := seq[2 : len(seq)-1]
	switch seq[len(seq)-1] {
	case 'J':
		if p := string(params); p == "2" || p == "3" {
			f.noteDamage(damageClear)
		}
		return seq, true
	case 'p':
		if string(params) == "!" { // DECSTR
			f.noteDamage(damageReset)
		}
		return seq, true
	case 'r':
		limit := 0
		if f.viewportRows != nil {
			limit = f.viewportRows()
		}
		if limit <= 0 || bytes.IndexByte(params, '?') >= 0 {
			return seq, false
		}
		top, bottom, ok := parseScrollRegion(params, limit)
		if !ok {
			return seq, true
		}
		return []byte("\x1b[" + strconv.Itoa(top) + ";" + strconv.Itoa(bottom) + "r"), true
	}
	return seq, false
}

// parseScrollRegion reads DECSTBM parameters and clamps them to limit rows.
// Defaults follow the terminal: an omitted top is 1, an omitted bottom is the
// last row — which for a provider that thinks it has limit rows is limit.
func parseScrollRegion(params []byte, limit int) (top, bottom int, ok bool) {
	top, bottom = 1, limit
	fields := bytes.Split(params, []byte{';'})
	if len(fields) > 2 {
		return 0, 0, false
	}
	for index, field := range fields {
		if len(field) == 0 {
			continue
		}
		value, err := strconv.Atoi(string(field))
		if err != nil || value < 0 {
			return 0, 0, false
		}
		if value == 0 {
			continue
		}
		if index == 0 {
			top = value
		} else {
			bottom = value
		}
	}
	bottom = min(bottom, limit)
	if top < 1 || top >= bottom {
		return 1, limit, true
	}
	return top, bottom, true
}

func (f *attachOutputFilter) recordMouseMode(mode string, on bool) {
	f.mouseMu.Lock()
	defer f.mouseMu.Unlock()
	if attachMouseTrackingModes[mode] {
		switch {
		case on:
			f.mouseTracking = mode
		case f.mouseTracking == mode:
			f.mouseTracking = ""
		}
		return
	}
	if f.mouseState == nil {
		f.mouseState = make(map[string]bool, len(mouseEncodingModes))
	}
	f.mouseState[mode] = on
}

// activeMouseSequence returns the sequence that re-enables every mouse mode the
// provider currently has on, or "" when none are. Turning passthrough back on
// mid-attach has to replay them: the provider set those modes once, the filter
// swallowed them while passthrough was off, and nothing makes it send them
// again — so without the replay `prefix m` only ever worked in one direction.
func (f *attachOutputFilter) activeMouseSequence() string {
	f.mouseMu.Lock()
	defer f.mouseMu.Unlock()
	active := make([]string, 0, len(mouseModes))
	// Only the live tracking level, never the whole history of levels: the
	// modes are mutually exclusive, so replaying them all would leave the
	// terminal on whichever happens to sort last.
	if f.mouseTracking != "" {
		active = append(active, f.mouseTracking)
	}
	// Iterate the list, not the map, so the emitted order is deterministic.
	for _, mode := range mouseEncodingModes {
		if f.mouseState[mode] {
			active = append(active, mode)
		}
	}
	return privateModeSequence(active, 'h')
}

func writeAttachBytes(dst io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := dst.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// escByte feeds one byte into a pending escape sequence. It returns the
// updated forward buffer and whether the Ctrl+Left quick detach fired.
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
	if f.backDetach && f.boxEmpty() && isQuickDetachKey(seq) {
		return out, true
	}
	if ctrl := decodeEnhancedCtrlKey(seq); ctrl != 0 {
		if f.handleEnhancedCtrlKey(ctrl) {
			// Swallowed exactly like the legacy control byte: the sequence
			// never reaches the agent.
			return out, false
		}
		// Forwarded, but with the legacy byte's effect on the input-box
		// estimate. Falling through to seqPoisons instead would latch on the
		// very keys that clear the box, leaving the quick detach disarmed with
		// no way to re-arm it for the rest of the attachment.
		out = append(out, seq...)
		f.applyKeyToBox(ctrl)
		return out, false
	}
	out = append(out, seq...)
	if len(seq) == 3 && seq[2] == 'M' {
		f.mouseBody = x10MouseBodyLen
		return out, false
	}
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
// It reports whether the byte belonged to the sequence; a byte that cannot
// appear in one ends it and is handed back for normal input handling.
func (f *stdinFilter) strByte(out []byte, b byte) ([]byte, bool) {
	if b < 0x20 && b != 0x1b && b != 0x07 {
		// Not a terminal reply after all: Alt+], Alt+Shift+P and Alt+Shift+X
		// are two-byte meta chords that open the same states, and waiting for a
		// terminator that never comes latched the filter — Ctrl+B d included —
		// for the rest of the attachment. A control byte cannot appear inside a
		// real OSC/DCS payload, so it ends the sequence here.
		f.strActive, f.strEsc = false, false
		f.unknown = true
		return out, false
	}
	out = append(out, b)
	f.strLen++
	switch {
	case f.strEsc:
		if b == '\\' { // ST: sequence complete
			f.strActive, f.strEsc = false, false
			return out, true
		}
		f.strEsc = b == 0x1b
	case b == 0x1b:
		f.strEsc = true
	case b == 0x07 && f.strBel: // BEL terminates OSC
		f.strActive = false
		return out, true
	}
	if f.strLen > maxStrLen {
		f.strActive, f.strEsc = false, false
		f.unknown = true
	}
	return out, true
}

// applyKeyToBox mirrors the input-box bookkeeping the plain control byte would
// have received in filter's switch.
func (f *stdinFilter) applyKeyToBox(ctrl byte) {
	switch ctrl {
	case '\r', '\n', 0x15, 0x1b:
		// Enter submits; Ctrl+U and Esc clear the box.
		f.clearBox()
	case 0x08, 0x7f:
		if f.typed > 0 {
			f.typed--
		}
	default:
		f.unknown = true
	}
}

// handleEnhancedCtrlKey applies the guards that the legacy control byte would
// have triggered. It reports whether the sequence was consumed.
func (f *stdinFilter) handleEnhancedCtrlKey(ctrl byte) bool {
	switch ctrl {
	case f.prefixByte():
		f.pendingPrefix = true
		return true
	case ctrlZ:
		return true // swallowed; see ctrlZ doc
	case ctrlC:
		f.clearBox()
		return true
	}
	return false
}

// decodeEnhancedCtrlKey maps a Ctrl chord encoded by the kitty keyboard
// protocol (CSI unicode ; modifiers u) or by xterm's modifyOtherKeys
// (CSI 27 ; modifiers ; unicode ~) back to the C0 byte the same chord produces
// in the legacy encoding, or 0 when the sequence is not one.
//
// Agents push these protocols on their own output so they can tell Shift+Enter
// from Enter. The terminal then stops sending 0x02 for Ctrl+B, which used to
// make the whole prefix chord — and the Ctrl+Z and Ctrl+C guards with it —
// silently stop working for the rest of the attachment.
func decodeEnhancedCtrlKey(seq []byte) byte {
	if len(seq) < 4 || seq[0] != 0x1b || seq[1] != '[' {
		return 0
	}
	params := strings.Split(string(seq[2:len(seq)-1]), ";")
	var code, modifiers string
	switch seq[len(seq)-1] {
	case 'u':
		if len(params) == 1 {
			// Kitty reports unmodified Enter, Esc and Backspace as CSI code u.
			// They carry no modifier but they do move the input box, so they
			// still have to be recognised; any other bare key is ordinary text.
			key, err := strconv.Atoi(leadingParam(params[0]))
			if err != nil {
				return 0
			}
			switch key {
			case '\r':
				return '\r'
			case 0x1b:
				return 0x1b
			case 0x7f:
				return 0x7f
			}
			return 0
		}
		if len(params) < 2 {
			return 0
		}
		code, modifiers = params[0], params[1]
	case '~':
		if len(params) != 3 || params[0] != "27" {
			return 0
		}
		modifiers, code = params[1], params[2]
	default:
		return 0
	}
	mask, err := strconv.Atoi(leadingParam(modifiers))
	if err != nil || mask < 1 {
		return 0
	}
	const ctrlModifier = 4 // the modifier mask is 1-based: 1 + shift|alt|ctrl…
	if (mask-1)&ctrlModifier == 0 {
		return 0
	}
	key, err := strconv.Atoi(leadingParam(code))
	if err != nil || key < 'a' || key > 'z' {
		return 0
	}
	return ctrlLetterBytes[key-'a']
}

// ctrlLetterBytes maps 'a'–'z' onto the C0 bytes their Ctrl chords produce.
// Indexing a fixed table rather than converting the parsed number keeps the
// result provably in range.
var ctrlLetterBytes = [26]byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d,
	0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a,
}

// leadingParam drops a CSI sub-parameter: kitty and modifyOtherKeys both append
// one after the modifiers (event type) and the key (alternate key).
func leadingParam(param string) string {
	leading, _, _ := strings.Cut(param, ":")
	return leading
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

// isQuickDetachKey matches Ctrl+Left: CSI 1;5 D (xterm, kitty, and every
// terminal following the xterm modifier encoding) or the parameterless CSI 5 D
// some terminals emit. Bare and otherwise-modified arrows are real input.
func isQuickDetachKey(seq []byte) bool {
	return string(seq) == "\x1b[1;5D" || string(seq) == "\x1b[5D"
}
