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

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
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
}

type attachRuntimeConfig struct {
	session       string
	output        io.Writer
	input         *os.File
	inputTerminal bool
	mouseEnabled  bool
	prefix        byte
	profile       attachProfileSnapshot
	terminalSize  func() (int, int, bool)
}

func newAttachRuntime(config attachRuntimeConfig) *attachRuntime {
	runtime := &attachRuntime{
		session: config.session, output: config.output, input: config.input, inputTerminal: config.inputTerminal, profile: config.profile, prefix: config.prefix,
		terminalSize: config.terminalSize,
	}
	runtime.mouse.Store(config.mouseEnabled)
	return runtime
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
	if runtime.terminalSize == nil {
		return nil
	}
	cols, rows, ok := runtime.terminalSize()
	if !ok {
		return nil
	}
	if err := frames.WriteFrame(frameResize, resizePayload(cols, rows)); err != nil {
		return fmt.Errorf("report attach terminal size: %w", err)
	}
	return nil
}

func (runtime *attachRuntime) setOutputFilter(filter *attachOutputFilter) {
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
	return writeAttachBytes(runtime.output, []byte(paintStatus(cols, rows, message)))
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
