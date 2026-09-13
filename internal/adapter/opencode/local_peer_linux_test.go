package opencode

import (
	"net"
	"strings"
	"syscall"
	"testing"
)

func TestTCPPeerOwnerRequiresConnectedReverseTuple(t *testing.T) {
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}
	const ownClient = "0: 0100007F:3039 0100007F:D431 01 0:0 0:0 0 1001\n"
	for _, tt := range []struct {
		name, peer string
		ok         bool
	}{
		{"same user", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001\n", true},
		{"deferred accept same user", "1: 0100007F:D431 0100007F:3039 03 0:0 0:0 0 1001\n", true},
		{"deferred accept other user", "1: 0100007F:D431 0100007F:3039 03 0:0 0:0 0 1002\n", false},
		{"mapped same user", "1: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:3039 01 0:0 0:0 0 1001\n", true},
		{"mapped other user", "1: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:3039 01 0:0 0:0 0 1002\n", false},
		{"mapped deferred same user", "1: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:3039 03 0:0 0:0 0 1001\n", true},
		{"other user", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1002\n", false},
		{"client alone", "", false},
		{"listener alone", "1: 0100007F:D431 00000000:0000 0A 0:0 0:0 0 1001\n", false},
		{"closing peer", "1: 0100007F:D431 0100007F:3039 08 0:0 0:0 0 1001\n", false},
		{"invalid owner", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 bad\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyTCPPeerOwner(strings.NewReader(ownClient+tt.peer), local, remote, 1001)
			if (err == nil) != tt.ok {
				t.Fatalf("verifyTCPPeerOwner = %v, want accepted=%v", err, tt.ok)
			}
		})
	}
}

func TestLocalHTTPClientDeferredAcceptPeer(t *testing.T) {
	for _, sameUser := range []bool{true, false} {
		t.Run(map[bool]string{true: "same user", false: "other user"}[sameUser], func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			raw, err := listener.(*net.TCPListener).SyscallConn()
			if err != nil {
				t.Fatal(err)
			}
			var optionErr error
			if err := raw.Control(func(fd uintptr) {
				optionErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_TCP, syscall.TCP_DEFER_ACCEPT, 30)
			}); err != nil {
				t.Fatal(err)
			}
			if optionErr != nil {
				t.Fatal(optionErr)
			}
			if sameUser {
				assertLocalHTTPClientAcceptsOwner(t, listener)
			} else {
				assertLocalHTTPClientRejectsWrongOwner(t, listener)
			}
		})
	}
}
