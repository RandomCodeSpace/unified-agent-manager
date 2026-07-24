//go:build darwin

package session

import "golang.org/x/sys/unix"

func flushTerminalInput(fd int) error {
	return unix.IoctlSetPointerInt(fd, unix.TIOCFLUSH, unix.TCIFLUSH)
}
