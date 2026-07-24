package session

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/x/term"
)

type synchronizedWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (writer *synchronizedWriter) Write(payload []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.writer.Write(payload)
}

type attachTerminalConfig struct {
	input          *os.File
	output         io.Writer
	inputTerminal  bool
	outputTerminal bool
	ownScreen      bool
}

type attachTerminalCleanup struct {
	config attachTerminalConfig
	state  *term.State
	once   sync.Once
	err    error
}

func beginAttachTerminal(config attachTerminalConfig) (*attachTerminalCleanup, error) {
	cleanup := &attachTerminalCleanup{config: config}
	if config.inputTerminal {
		state, err := term.MakeRaw(config.input.Fd())
		if err != nil {
			return nil, fmt.Errorf("set raw mode: %w", err)
		}
		cleanup.state = state
	}
	if config.outputTerminal && config.ownScreen {
		if err := writeAttachBytes(config.output, []byte(screenEnter)); err != nil {
			cleanup.err = fmt.Errorf("enter attach screen: %w", err)
			_ = cleanup.Restore()
			return nil, cleanup.err
		}
	}
	return cleanup, nil
}

func (cleanup *attachTerminalCleanup) Restore() error {
	cleanup.once.Do(func() {
		var screenErr error
		if cleanup.config.outputTerminal {
			reset := screenReset
			if cleanup.config.ownScreen {
				reset = screenExit
			}
			if err := writeAttachBytes(cleanup.config.output, []byte(reset)); err != nil {
				screenErr = fmt.Errorf("reset attach screen: %w", err)
			}
		}
		var termiosErr error
		if cleanup.state != nil {
			if err := term.Restore(cleanup.config.input.Fd(), cleanup.state); err != nil {
				termiosErr = fmt.Errorf("restore terminal state: %w", err)
			}
		}
		cleanup.err = errors.Join(cleanup.err, screenErr, termiosErr)
	})
	return cleanup.err
}
