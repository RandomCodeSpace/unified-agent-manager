//go:build linux

package pty

import "golang.org/x/sys/unix"

// Linux uses TCGETS / TCSETS for termios get/set ioctls.
const (
	tcGetattr = unix.TCGETS
	tcSetattr = unix.TCSETS
)
