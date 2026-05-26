package ipc

import (
	"errors"
	"net"
)

// ErrNilConn is returned when PeerUID is called with a nil connection.
var ErrNilConn = errors.New("ipc: nil unix connection")

// PeerUID returns the UID of the peer process connected on conn.
// Linux uses SO_PEERCRED. macOS uses LOCAL_PEERCRED with Xucred.
//
// Callers use this to enforce same-uid access on the supervisor and host
// control sockets.
func PeerUID(conn *net.UnixConn) (uint32, error) {
	if conn == nil {
		return 0, ErrNilConn
	}
	return peerUID(conn)
}
