//go:build !linux

package session

import "golang.org/x/sys/unix"

// ioctlReadTermios reads the terminal attributes shared by a pty pair.
const ioctlReadTermios = unix.TIOCGETA
