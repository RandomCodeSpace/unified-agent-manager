//go:build darwin

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
	var xucred *unix.Xucred
	var inner error
	ctlErr := raw.Control(func(fd uintptr) {
		xucred, inner = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if ctlErr != nil {
		return 0, fmt.Errorf("ipc: PeerUID Control: %w", ctlErr)
	}
	if inner != nil {
		return 0, fmt.Errorf("ipc: LOCAL_PEERCRED: %w", inner)
	}
	return xucred.Uid, nil
}
