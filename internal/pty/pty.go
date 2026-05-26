// Package pty provides PTY primitives via golang.org/x/sys/unix syscalls.
// Linux and macOS only. No third-party dependencies.
package pty

import (
	"errors"
	"os"
)

// ErrFdClosed signals that an fd has been closed (returned by helpers).
var ErrFdClosed = errors.New("pty: fd closed")

// PTY holds an open master/slave pair.
type PTY struct {
	Master    *os.File
	Slave     *os.File
	SlavePath string
}

// Open allocates a new PTY pair. The slave is opened with O_CLOEXEC; close
// it in the parent immediately after passing to the child to prevent fd
// leakage.
func Open() (*PTY, error) {
	master, slavePath, err := openPTYUnix()
	if err != nil {
		return nil, err
	}
	// #nosec G304 — slavePath is the kernel-allocated /dev/pts/<N> returned
	// by TIOCGPTN on the master we just opened. It is not user input.
	slave, err := os.OpenFile(slavePath, os.O_RDWR|osNoCtty, 0)
	if err != nil {
		_ = master.Close()
		return nil, err
	}
	return &PTY{Master: master, Slave: slave, SlavePath: slavePath}, nil
}

// Close closes both fds. Safe to call multiple times.
func (p *PTY) Close() error {
	var first error
	if p.Master != nil {
		if err := p.Master.Close(); err != nil && first == nil {
			first = err
		}
		p.Master = nil
	}
	if p.Slave != nil {
		if err := p.Slave.Close(); err != nil && first == nil {
			first = err
		}
		p.Slave = nil
	}
	return first
}
