package session

import (
	"os"

	"golang.org/x/sys/unix"
)

// pty's setup ioctls call File.Fd, which disables runtime polling. Wrap a
// nonblocking duplicate so reads can be interrupted by Close at shutdown.
func makePTYNonblocking(master *os.File) (*os.File, error) {
	raw, err := master.SyscallConn()
	if err != nil {
		return nil, err
	}
	fd := -1
	var setupErr error
	if err := raw.Control(func(original uintptr) {
		fd, setupErr = unix.FcntlInt(original, unix.F_DUPFD_CLOEXEC, 0)
		if setupErr == nil {
			setupErr = unix.SetNonblock(fd, true)
		}
	}); err != nil {
		return nil, err
	}
	if setupErr != nil {
		if fd >= 0 {
			_ = unix.Close(fd)
		}
		return nil, setupErr
	}
	pollable := os.NewFile(uintptr(fd), master.Name())
	_ = master.Close()
	return pollable, nil
}

func writePTYNonblocking(master *os.File, payload []byte) (int, error) {
	raw, err := master.SyscallConn()
	if err != nil {
		return 0, err
	}
	var written int
	var writeErr error
	if err := raw.Control(func(fd uintptr) {
		// Keep direct test hosts and externally supplied masters safe too.
		if writeErr = unix.SetNonblock(int(fd), true); writeErr == nil {
			written, writeErr = unix.Write(int(fd), payload)
		}
	}); err != nil {
		return 0, err
	}
	return written, writeErr
}
