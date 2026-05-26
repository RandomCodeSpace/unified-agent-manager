//go:build linux

package ipc

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("ipc: SyscallConn: %w", err)
	}
	var ucred *unix.Ucred
	var inner error
	ctlErr := raw.Control(func(fd uintptr) {
		ucred, inner = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if ctlErr != nil {
		return 0, fmt.Errorf("ipc: PeerUID Control: %w", ctlErr)
	}
	if inner != nil {
		return 0, fmt.Errorf("ipc: SO_PEERCRED: %w", inner)
	}
	return ucred.Uid, nil
}
