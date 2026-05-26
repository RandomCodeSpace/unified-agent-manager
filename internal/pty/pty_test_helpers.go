//go:build unix

package pty

import (
	"errors"

	"golang.org/x/sys/unix"
)

type fdFlags struct {
	CloseOnExec bool
}

func getFdFlags(fd int) (fdFlags, error) {
	v, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		if errors.Is(err, unix.EBADF) {
			return fdFlags{}, ErrFdClosed
		}
		return fdFlags{}, err
	}
	return fdFlags{CloseOnExec: v&unix.FD_CLOEXEC != 0}, nil
}
