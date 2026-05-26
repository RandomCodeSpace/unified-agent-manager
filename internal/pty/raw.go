//go:build unix

package pty

import (
	"os"

	"golang.org/x/sys/unix"
)

// RestoreFunc returns the terminal to its original state.
type RestoreFunc func() error

// MakeRaw puts a tty fd into raw mode and returns a restore function. The
// caller MUST defer restore() to avoid leaving the user's terminal cooked.
func MakeRaw(f *os.File) (RestoreFunc, error) {
	fd := int(f.Fd())
	orig, err := unix.IoctlGetTermios(fd, tcGetattr)
	if err != nil {
		return nil, err
	}
	raw := *orig
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, tcSetattr, &raw); err != nil {
		return nil, err
	}
	restore := func() error {
		return unix.IoctlSetTermios(fd, tcSetattr, orig)
	}
	return restore, nil
}
