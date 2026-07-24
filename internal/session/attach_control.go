package session

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
		case serverFrameControl:
			event, changed, err := config.frames.observeRoleEvent(payload, config.runtime.discardPendingInput)
			if err != nil {
				return err
			}
			if changed {
				if err := config.runtime.writeStatus(fmt.Sprintf("role %s (%s)", event.Role, event.Reason)); err != nil {
					return err
				}
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
}

type attachRuntimeConfig struct {
	session       string
	output        io.Writer
	input         *os.File
	inputTerminal bool
	mouseEnabled  bool
	prefix        byte
	profile       attachProfileSnapshot
}

func newAttachRuntime(config attachRuntimeConfig) *attachRuntime {
	runtime := &attachRuntime{
		session: config.session, output: config.output, input: config.input, inputTerminal: config.inputTerminal, profile: config.profile, prefix: config.prefix,
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
		if !enabled {
			if err := writeAttachBytes(runtime.output, []byte(mouseReset)); err != nil {
				return fmt.Errorf("disable terminal mouse modes: %w", err)
			}
		}
		return runtime.writeStatus(fmt.Sprintf("mouse passthrough %t for this attachment", enabled))
	case commandNone:
		return nil
	default:
		return fmt.Errorf("unsupported attach command %q", command)
	}
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
	return writeAttachBytes(runtime.output, []byte("\r\n[uam: "+message+"]\r\n"))
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
