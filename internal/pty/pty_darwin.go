//go:build darwin

package pty

import (
	"bytes"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/unix"
)

const osNoCtty = unix.O_NOCTTY | unix.O_CLOEXEC

// TIOCPTYGNAME ioctl number on darwin (from sys/ioctl.h).
const tiocPtyGname = 0x40807453

func openPTYUnix() (*os.File, string, error) {
	fd, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, "", fmt.Errorf("open /dev/ptmx: %w", err)
	}
	master := os.NewFile(uintptr(fd), "/dev/ptmx")
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), uintptr(tiocPtyGrant()), 0); errno != 0 {
		_ = master.Close()
		return nil, "", fmt.Errorf("TIOCPTYGRANT: %w", errno)
	}
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), uintptr(tiocPtyUnlk()), 0); errno != 0 {
		_ = master.Close()
		return nil, "", fmt.Errorf("TIOCPTYUNLK: %w", errno)
	}
	var buf [128]byte
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, master.Fd(), uintptr(tiocPtyGname), uintptr(unsafe.Pointer(&buf[0]))); errno != 0 {
		_ = master.Close()
		return nil, "", fmt.Errorf("TIOCPTYGNAME: %w", errno)
	}
	path := string(bytes.TrimRight(buf[:], "\x00"))
	if path == "" {
		_ = master.Close()
		return nil, "", fmt.Errorf("TIOCPTYGNAME returned empty path")
	}
	return master, path, nil
}

// Darwin ioctl numbers (from sys/ttycom.h):
//
//	#define TIOCPTYGRANT _IO('t', 84)
//	#define TIOCPTYUNLK  _IO('t', 82)
//	#define TIOCPTYGNAME _IOR('t', 83, char[128])
//
// _IO(g,n)   = (uint32)(0x20000000 | (g << 8) | n)
// _IOR(g,n,t)= (uint32)(0x40000000 | (sizeof(t) << 16) | (g << 8) | n)
func tiocPtyGrant() uint32 { return 0x20007454 }
func tiocPtyUnlk() uint32  { return 0x20007452 }
