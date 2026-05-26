//go:build linux

package pty

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

const osNoCtty = unix.O_NOCTTY | unix.O_CLOEXEC

func openPTYUnix() (*os.File, string, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	master := os.NewFile(uintptr(fd), "/dev/ptmx")
	// Unlock the slave side.
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		_ = master.Close()
		return nil, "", fmt.Errorf("TIOCSPTLCK: %w", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		_ = master.Close()
		return nil, "", fmt.Errorf("TIOCGPTN: %w", err)
	}
	return master, fmt.Sprintf("/dev/pts/%d", n), nil
}
