package opencode

import (
	"net"
	"strings"
	"testing"
)

func TestTCPPeerOwnerRequiresEstablishedReverseTuple(t *testing.T) {
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}
	const ownClient = "0: 0100007F:3039 0100007F:D431 01 0:0 0:0 0 1001\n"
	for _, tt := range []struct {
		name, peer string
		ok         bool
	}{
		{"same user", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001\n", true},
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
