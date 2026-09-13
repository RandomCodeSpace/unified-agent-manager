package opencode

import "testing"

func TestLsofPeerOwnerRequiresReverseTupleAndProcessOwner(t *testing.T) {
	const peer = "127.0.0.1:54321->127.0.0.1:12345"
	const client = "p123\nu1001\nn127.0.0.1:12345->127.0.0.1:54321\n"
	for _, tt := range []struct {
		name, output string
		ok           bool
	}{
		{"same user", "p456\nu1001\nn" + peer + "\n", true},
		{"other user", "p456\nu1002\nn" + peer + "\n", false},
		{"client alone", "", false},
		{"missing owner", "p456\nn" + peer + "\n", false},
		{"listener alone", "p456\nu1001\nn127.0.0.1:54321\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyLsofPeerOwner(client+tt.output, peer, 1001)
			if (err == nil) != tt.ok {
				t.Fatalf("verifyLsofPeerOwner = %v, want accepted=%v", err, tt.ok)
			}
		})
	}
}
