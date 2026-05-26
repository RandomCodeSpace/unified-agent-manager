//go:build darwin

package pty

import "golang.org/x/sys/unix"

// Darwin (and other BSDs) use TIOCGETA / TIOCSETA instead of TCGETS / TCSETS.
const (
	tcGetattr = unix.TIOCGETA
	tcSetattr = unix.TIOCSETA
)
