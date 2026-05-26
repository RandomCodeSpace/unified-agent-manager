//go:build unix

package pty

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// Resize sets the PTY window size on a master fd (TIOCSWINSZ).
func Resize(f *os.File, cols, rows uint16) error {
	ws := &unix.Winsize{Col: cols, Row: rows}
	if err := unix.IoctlSetWinsize(int(f.Fd()), unix.TIOCSWINSZ, ws); err != nil {
		return fmt.Errorf("TIOCSWINSZ: %w", err)
	}
	return nil
}

// GetWinsize reads current window size (TIOCGWINSZ).
func GetWinsize(f *os.File) (cols, rows uint16, err error) {
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, fmt.Errorf("TIOCGWINSZ: %w", err)
	}
	return ws.Col, ws.Row, nil
}
