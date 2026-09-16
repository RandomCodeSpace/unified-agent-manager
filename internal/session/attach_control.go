package session

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/mattn/go-runewidth"
	"golang.org/x/sys/unix"
)

type attachOutputConfig struct {
	output  io.Writer
	reader  *bufio.Reader
	version protocolVersion
	frames  *frameWriter
	runtime *attachRuntime
}

func copyAttachOutputConfigured(config attachOutputConfig) error {
	filter := newAttachOutputFilterWithMouse(config.output, config.runtime.mouseEnabled)
	config.runtime.setOutputFilter(filter)
	if config.version == protocolV1 {
		_, err := io.Copy(filter, config.reader)
		if flushErr := filter.Flush(); err == nil {
			err = flushErr
		}
		return err
	}
	for {
		kind, payload, err := readFrame(config.reader)
		if errors.Is(err, io.EOF) {
			return filter.Flush()
		}
		if err != nil {
			return err
		}
		switch kind {
		case serverFramePTY:
			if _, err := filter.Write(payload); err != nil {
				return err
			}
			if damage := filter.consumeDamage(); damage != damageNone {
				if err := config.runtime.paintStatusBar(damage == damageReset); err != nil {
					return err
				}
			}
			// Both the attach banner and a role change are followed by the
			// host's repaint, which opens with a clear-screen. Writing them
			// before that frame meant the user never saw either one.
			if err := config.runtime.flushPendingStatus(); err != nil {
				return err
			}
		case serverFrameControl:
			event, changed, err := config.frames.observeRoleEvent(payload, config.runtime.discardPendingInput)
			if err != nil {
				return err
			}
			if changed {
				config.runtime.setRole(event.Role)
				// A promoted client owns the PTY geometry from here on. The host
				// sized the PTY from whatever this client last reported, which for
				// a standby that never resized is its attach-time size — report
				// the live one so the agent is not repainting into stale bounds.
				if event.Role == roleController {
					if err := config.runtime.reportSize(config.frames); err != nil {
						return err
					}
				}
				config.runtime.queueStatus(fmt.Sprintf("role %s (%s)", event.Role, event.Reason))
			}
		default:
			return fmt.Errorf("unsupported server attach frame type %d", kind)
		}
	}
}

type attachCommand string

const (
	commandNone            attachCommand = ""
	commandRequestControl  attachCommand = "request_control"
	commandTransferControl attachCommand = "transfer_control"
	commandShowInfo        attachCommand = "show_info"
	commandToggleMouse     attachCommand = "toggle_mouse"
)

type attachRuntime struct {
	session       string
	output        io.Writer
	input         *os.File
	inputTerminal bool
	profile       attachProfileSnapshot
	prefix        byte
	mouse         atomic.Bool
	// filter is the host→terminal output filter, registered once the output
	// pump starts. `prefix m` reads the provider's live mouse modes from it.
	filter atomic.Pointer[attachOutputFilter]
	// terminalSize reports the viewer's current size; nil when the attachment
	// has no terminal (tests, pipes).
	terminalSize func() (int, int, bool)
	// pendingStatus holds notices waiting for the repaint that would otherwise
	// erase them; queued from the output pump and from the attach setup.
	statusMu      sync.Mutex
	pendingStatus []string
	// statusRows is the number of terminal rows reserved for the status bar
	// (0 when the client does not own the outer screen). The host is told the
	// terminal is that much shorter; the bar lives on the physical last row.
	statusRows int
	display    string
	cwd        string
	barMu      sync.Mutex
	barRole    clientRole
	barRestore *time.Timer
	barStopped bool
}

// statusNoticeHold is how long a transient notice (role change, prefix i,
// mouse toggle) stays on the reserved row before the status bar is repainted.
const statusNoticeHold = 3 * time.Second

type attachRuntimeConfig struct {
	session       string
	output        io.Writer
	input         *os.File
	inputTerminal bool
	mouseEnabled  bool
	prefix        byte
	profile       attachProfileSnapshot
	terminalSize  func() (int, int, bool)
	statusRows    int
	role          clientRole
	display       string
	cwd           string
}

func newAttachRuntime(config attachRuntimeConfig) *attachRuntime {
	runtime := &attachRuntime{
		session: config.session, output: config.output, input: config.input, inputTerminal: config.inputTerminal, profile: config.profile, prefix: config.prefix,
		terminalSize: config.terminalSize, statusRows: config.statusRows, barRole: config.role,
		display: config.display, cwd: config.cwd,
	}
	runtime.mouse.Store(config.mouseEnabled)
	return runtime
}

// viewportSize is the geometry reported to the host: the terminal minus the
// rows the client keeps for itself.
func (runtime *attachRuntime) viewportSize() (int, int, bool) {
	if runtime.terminalSize == nil {
		return 0, 0, false
	}
	cols, rows, ok := runtime.terminalSize()
	if !ok {
		return 0, 0, false
	}
	return cols, rows - runtime.statusRows, true
}

func (runtime *attachRuntime) setRole(role clientRole) {
	runtime.barMu.Lock()
	runtime.barRole = role
	runtime.barMu.Unlock()
}

// statusBarText is the persistent bar. Segments in priority order: role,
// session identity ("<name> · <provider> · <id>"), the keys, the working
// directory, the profile, and the mouse state when passthrough is off. Lower
// priority segments are dropped whole when the terminal is too narrow; the
// first two are always kept and cut if they must be. Keys use the tmux-style
// short form. It leads with the same "[uam: role <role>;" as the transient
// banner it replaced so anything that watched for it still matches.
func (runtime *attachRuntime) statusBarText(cols int) string {
	prefix := "C-b"
	if runtime.prefix >= 1 && runtime.prefix <= 26 {
		prefix = fmt.Sprintf("C-%c", 'a'+rune(runtime.prefix)-1)
	}
	display := displaytext.Sanitize(runtime.display)
	if display == "" {
		display = displaytext.Sanitize(runtime.session)
	}
	segments := []string{
		fmt.Sprintf("[uam: role %s", runtime.barRole),
		display,
		fmt.Sprintf("%s d / C-Left detach", prefix),
		fmt.Sprintf("%s i info", prefix),
	}
	if cwd := displaytext.Sanitize(runtime.cwd); cwd != "" {
		segments = append(segments, cwd)
	}
	// "none" is what the launcher reports when no profile applies; it is not
	// worth a slot on the bar.
	if effective := displaytext.Sanitize(runtime.profile.effective); effective != "" && effective != "none" {
		segments = append(segments, "profile "+effective)
	}
	if !runtime.mouse.Load() {
		segments = append(segments, "mouse off ("+prefix+" m)")
	}
	text := segments[0] + "; " + segments[1]
	for _, segment := range segments[2:] {
		candidate := text + "; " + segment
		if cols > 0 && runewidth.StringWidth(candidate+"]") > cols {
			break
		}
		text = candidate
	}
	return text + "]"
}

// paintStatusBar draws the reserved bar, re-pinning the scroll region when the
// caller knows the terminal lost it. It is a no-op without a reservation.
func (runtime *attachRuntime) paintStatusBar(pin bool) error {
	if runtime.statusRows == 0 || runtime.terminalSize == nil {
		return nil
	}
	runtime.barMu.Lock()
	defer runtime.barMu.Unlock()
	return runtime.paintStatusBarLocked(pin)
}

func (runtime *attachRuntime) paintStatusBarLocked(pin bool) error {
	if runtime.barStopped {
		return nil
	}
	cols, rows, ok := runtime.terminalSize()
	if !ok {
		return nil
	}
	if runtime.barRestore != nil {
		runtime.barRestore.Stop()
		runtime.barRestore = nil
	}
	sequence := paintStatusBar(cols, rows, rows-runtime.statusRows, runtime.statusBarText(cols), pin)
	if sequence == "" {
		return nil
	}
	return writeAttachBytes(runtime.output, []byte(sequence))
}

// holdNoticeThenRestore lets a transient notice sit on the reserved row, then
// brings the bar back. Only one restore is ever pending.
func (runtime *attachRuntime) holdNoticeThenRestore() {
	if runtime.statusRows == 0 {
		return
	}
	runtime.barMu.Lock()
	defer runtime.barMu.Unlock()
	if runtime.barStopped {
		return
	}
	if runtime.barRestore != nil {
		runtime.barRestore.Stop()
	}
	runtime.barRestore = time.AfterFunc(statusNoticeHold, func() {
		runtime.barMu.Lock()
		defer runtime.barMu.Unlock()
		runtime.barRestore = nil
		_ = runtime.paintStatusBarLocked(false)
	})
}

// stopStatusBar cancels any pending restore so nothing is painted after the
// terminal has been handed back. Safe to call more than once.
func (runtime *attachRuntime) stopStatusBar() {
	runtime.barMu.Lock()
	defer runtime.barMu.Unlock()
	runtime.barStopped = true
	if runtime.barRestore != nil {
		runtime.barRestore.Stop()
		runtime.barRestore = nil
	}
}

type attachProfileSnapshot struct {
	selected  string
	effective string
}

func (snapshot attachProfileSnapshot) notice() string {
	parts := make([]string, 0, 2)
	if selected := displaytext.Sanitize(snapshot.selected); selected != "" {
		parts = append(parts, "selected profile "+selected)
	}
	if effective := displaytext.Sanitize(snapshot.effective); effective != "" {
		parts = append(parts, "effective profile "+effective)
	}
	return strings.Join(parts, "; ")
}

func (runtime *attachRuntime) mouseEnabled() bool {
	return runtime.mouse.Load()
}

func (runtime *attachRuntime) discardPendingInput() error {
	if runtime.input == nil || !runtime.inputTerminal {
		return nil
	}
	if err := flushTerminalInput(int(runtime.input.Fd())); err != nil {
		return fmt.Errorf("discard pre-control terminal input: %w", err)
	}
	return nil
}

func (runtime *attachRuntime) runCommand(command attachCommand, frames *frameWriter) error {
	switch command {
	case commandRequestControl:
		if err := frames.WriteRoleCommand(actionRequestControl); err != nil {
			return fmt.Errorf("request attach control: %w", err)
		}
		return runtime.writeStatus("control requested; transfer remains controller-owned")
	case commandTransferControl:
		if err := frames.WriteRoleCommand(actionTransferControl); err != nil {
			return fmt.Errorf("transfer attach control: %w", err)
		}
		return runtime.writeStatus("control transfer requested")
	case commandShowInfo:
		info := fmt.Sprintf("session %s; client %s; role %s", runtime.session, frames.ClientID(), frames.AssignedRole())
		if profile := runtime.profile.notice(); profile != "" {
			info += "; " + profile
		}
		return runtime.writeStatus(info + "; keys: prefix d detach, c interrupt, r request, o transfer, i info, m mouse")
	case commandToggleMouse:
		enabled := runtime.toggleMouse()
		if err := runtime.applyMouseModes(enabled); err != nil {
			return err
		}
		return runtime.writeStatus(fmt.Sprintf("mouse passthrough %t for this attachment", enabled))
	case commandNone:
		return nil
	default:
		return fmt.Errorf("unsupported attach command %q", command)
	}
}

// reportSize sends the viewer's current terminal size to the host. It is a
// no-op when the attachment has no terminal.
func (runtime *attachRuntime) reportSize(frames *frameWriter) error {
	cols, rows, ok := runtime.viewportSize()
	if !ok {
		return nil
	}
	if err := frames.WriteFrame(frameResize, resizePayload(cols, rows)); err != nil {
		return fmt.Errorf("report attach terminal size: %w", err)
	}
	return nil
}

func (runtime *attachRuntime) setOutputFilter(filter *attachOutputFilter) {
	filter.viewportRows = func() int {
		if runtime.statusRows == 0 {
			return 0
		}
		_, rows, ok := runtime.viewportSize()
		if !ok {
			return 0
		}
		return rows
	}
	runtime.filter.Store(filter)
}

// applyMouseModes makes the user's terminal match the passthrough state just
// toggled: off tears every mouse mode down so selection and paste return to the
// terminal, on replays the modes the provider still has set, which the filter
// swallowed while passthrough was off.
func (runtime *attachRuntime) applyMouseModes(enabled bool) error {
	if !enabled {
		if err := writeAttachBytes(runtime.output, []byte(mouseReset)); err != nil {
			return fmt.Errorf("disable terminal mouse modes: %w", err)
		}
		return nil
	}
	filter := runtime.filter.Load()
	if filter == nil {
		return nil
	}
	sequence := filter.activeMouseSequence()
	if sequence == "" {
		return nil
	}
	if err := writeAttachBytes(runtime.output, []byte(sequence)); err != nil {
		return fmt.Errorf("restore terminal mouse modes: %w", err)
	}
	return nil
}

func (runtime *attachRuntime) toggleMouse() bool {
	for {
		current := runtime.mouse.Load()
		if runtime.mouse.CompareAndSwap(current, !current) {
			return !current
		}
	}
}

func (runtime *attachRuntime) writeStatus(message string) error {
	cols, rows := 0, 0
	if runtime.terminalSize != nil {
		if width, height, ok := runtime.terminalSize(); ok {
			cols, rows = width, height
		}
	}
	if err := writeAttachBytes(runtime.output, []byte(paintStatus(cols, rows, message))); err != nil {
		return err
	}
	runtime.holdNoticeThenRestore()
	return nil
}

// queueStatus holds a notice until after the next PTY frame. Notices that
// accompany an attach or a role change are always followed by the host's
// repaint, and a repaint starts by clearing the screen.
func (runtime *attachRuntime) queueStatus(message string) {
	runtime.statusMu.Lock()
	defer runtime.statusMu.Unlock()
	runtime.pendingStatus = append(runtime.pendingStatus, message)
}

func (runtime *attachRuntime) flushPendingStatus() error {
	runtime.statusMu.Lock()
	pending := runtime.pendingStatus
	runtime.pendingStatus = nil
	runtime.statusMu.Unlock()
	for _, message := range pending {
		if err := runtime.writeStatus(message); err != nil {
			return err
		}
	}
	return nil
}

type attachPumpConfig struct {
	input      *os.File
	inputFD    int32
	frames     *frameWriter
	runtime    *attachRuntime
	prefix     byte
	backDetach bool
	stop       <-chan struct{}
}

func pumpAttachInput(config attachPumpConfig) error {
	filter := &stdinFilter{prefix: config.prefix, backDetach: config.backDetach, role: config.frames.AssignedRole()}
	buf := make([]byte, 4096)
	for {
		n, role, err := readAttachInput(attachInputReadConfig{
			input: config.input, inputFD: config.inputFD, buf: buf, frames: config.frames, stop: config.stop,
		})
		if n > 0 {
			filter.role = role
			out, detach := filter.filter(buf[:n])
			if len(out) > 0 && role == roleController && config.frames.HasControl() {
				if writeErr := config.frames.WriteFrame(frameStdin, out); writeErr != nil {
					return fmt.Errorf("forward attach input: %w", writeErr)
				}
			}
			for _, command := range filter.drainCommands() {
				if commandErr := config.runtime.runCommand(command, config.frames); commandErr != nil {
					return commandErr
				}
			}
			if detach {
				return nil
			}
		}
		if err != nil {
			return nil
		}
	}
}

type attachInputReadConfig struct {
	input   *os.File
	inputFD int32
	buf     []byte
	frames  *frameWriter
	stop    <-chan struct{}
}

func readAttachInput(config attachInputReadConfig) (int, clientRole, error) {
	for {
		select {
		case <-config.stop:
			return 0, "", io.EOF
		default:
		}
		pollFD := []unix.PollFd{{Fd: config.inputFD, Events: unix.POLLIN | unix.POLLHUP | unix.POLLERR}}
		if _, err := unix.Poll(pollFD, 100); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return 0, "", err
		}

		config.frames.mu.Lock()
		pollFD[0].Revents = 0
		ready, pollErr := unix.Poll(pollFD, 0)
		if pollErr != nil {
			config.frames.mu.Unlock()
			if errors.Is(pollErr, unix.EINTR) {
				continue
			}
			return 0, "", pollErr
		}
		if ready == 0 {
			config.frames.mu.Unlock()
			continue
		}
		n, readErr := config.input.Read(config.buf)
		role := config.frames.role
		config.frames.mu.Unlock()
		return n, role, readErr
	}
}
