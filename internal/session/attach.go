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

const mouseReset = "\x1b[?1000;1002;1003;1005;1006;1015l"

// screenExit resets every mode the agent could have toggled mid-attach, then
// leaves the alternate screen. Terminals ignore sequences they don't
// implement, so the suffix is safe to emit unconditionally.
const screenReset = "\x1b[<u" + // pop the kitty keyboard flags agents push
	"\x1b[=0;1u" + // and zero them in case the agent pushed more than once
	"\x1b[?1000;1002;1003;1004;1005;1006;1015l" + // mouse tracking + focus reporting off
	"\x1b[?2004l" + // bracketed paste off
	"\x1b[?2026l" + // synchronized output off
	"\x1b[!p" + // DECSTR: cursor keys, origin, margins, SGR, insert mode
	"\x1b>" + // numeric keypad (DECKPNM; DECSTR leaves keypad mode alone)
	"\x1b(B" + // G0 charset back to ASCII
	"\x1b[?25h" // cursor visible

const screenExit = screenReset +
	"\x1b[?1007r" + // alternate scroll back to the user's saved setting (XTRESTORE)
	"\x1b[?1049l" // leave the alt screen: primary buffer and cursor restored

// RunAttach is the entry point of `uam __attach`: it puts the terminal in raw
// mode and bridges it to a session host — the native replacement for
// `tmux attach`. It returns when the user detaches (Ctrl+B d, or a bare left
// arrow while nothing is typed — see stdinFilter) or the agent exits.
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
	hello := defaultClientHello(term.IsTerminal(stdin.Fd()) && term.IsTerminal(stdout.Fd()), os.Getenv("TERM"), os.Getenv("COLORTERM"))
	handshake, err := performAttachHandshake(conn, name, request{
		Op: opAttach, Cols: cols, Rows: rows, Version: protocolV2, RequestedRole: requestedRole, Hello: &hello,
	})
	if err != nil {
		return err
	}
	frames := newAttachFrameWriter(conn, handshake.version, handshake.clientID, handshake.generation)
	frames.SetAssignedRole(handshake.assignedRole)
	output := &synchronizedWriter{writer: stdout}
	inputTerminal := term.IsTerminal(stdin.Fd())
	terminalOutput := term.IsTerminal(stdout.Fd())
	ownScreen := terminalOutput && attachOwnsOuterScreen(dir, name)
	cleanup, err := beginAttachTerminal(attachTerminalConfig{
		input: stdin, output: output, inputTerminal: inputTerminal, outputTerminal: terminalOutput, ownScreen: ownScreen,
	})
	if err != nil {
		return err
	}
	defer func() { _ = cleanup.Restore() }()
	runtime := newAttachRuntime(attachRuntimeConfig{
		session: name, output: output, input: stdin, inputTerminal: inputTerminal, mouseEnabled: policy.mouseEnabled, prefix: policy.controlPrefix, profile: opts.profile,
	})
	if terminalOutput && handshake.version == protocolV2 {
		if err := runtime.writeStatus(fmt.Sprintf("role %s; %s i for info", handshake.assignedRole, controlPrefixName(policy.controlPrefix))); err != nil {
			return errors.Join(fmt.Errorf("show assigned attach role: %w", err), cleanup.Restore())
		}
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
				if frames.HasControl() {
					if w, h, err := term.GetSize(stdout.Fd()); err == nil {
						_ = frames.WriteFrame(frameResize, resizePayload(w, h))
					}
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

func resizePayload(cols, rows int) []byte {
	// Clamp to uint16 range; the host rejects anything over 1000 anyway.
	out := make([]byte, 4)
	binary.BigEndian.PutUint16(out[0:2], uint16(max(0, min(cols, 0xffff)))) // #nosec G115 -- clamped
	binary.BigEndian.PutUint16(out[2:4], uint16(max(0, min(rows, 0xffff)))) // #nosec G115 -- clamped
	return out
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
// byte) until a key that submits or clears the box (Enter, Esc, Ctrl+U). Plain
// Ctrl+C is swallowed so terminal copy shortcuts cannot cancel the agent;
// Ctrl+B c sends a literal Ctrl+C when an explicit interrupt is needed. A bare
// left arrow while the box is believed empty detaches; inside a draft it keeps
// moving the cursor. Ctrl+B d always detaches regardless.
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
}

var pasteBegin = []byte("\x1b[200~")
var pasteEnd = []byte("\x1b[201~")

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

// filter processes one stdin chunk, returning the bytes to forward and
// whether the user detached. On detach the returned bytes (anything typed
// before the detach key in the same chunk) must still be flushed first.
func (f *stdinFilter) filter(chunk []byte) (out []byte, detach bool) {
	out = make([]byte, 0, len(chunk)+1)
	prefix := f.prefix
	if prefix == 0 {
		prefix = detachPrefix
	}
	for i, b := range chunk {
		if f.inPaste {
			out = append(out, b)
			f.pasteEnd = advanceExactMatch(pasteEnd, f.pasteEnd, b)
			if f.pasteEnd == len(pasteEnd) {
				f.inPaste, f.pasteEnd = false, 0
				f.unknown = true
			}
			continue
		}
		if f.strActive {
			out = f.strByte(out, b)
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
var attachMouseModes = map[string]bool{"1000": true, "1002": true, "1003": true, "1005": true, "1006": true, "1015": true}

// attachOutputFilter contains provider-owned alternate-screen toggles inside
// the attach screen and optionally leaves mouse modes under terminal control.
// Only seven-bit DEC private h/l sequences are rewritten.
type attachOutputFilter struct {
	dst          io.Writer
	mouseEnabled func() bool
	pending      []byte
	abortedCSI   bool
	forwardedCSI bool
}

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
