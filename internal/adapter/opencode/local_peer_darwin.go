//go:build darwin

package opencode

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

func verifyLocalPeer(ctx context.Context, conn net.Conn, uid int) error {
	remote := conn.RemoteAddr().(*net.TCPAddr)
	// macOS ships lsof; use its absolute system path and numeric field output.
	// Restrict the query to this loopback port and established TCP connections.
	command := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-a", "-iTCP@"+remote.String(), "-sTCP:ESTABLISHED", "-F", "un")
	output := newByteRing(serverLogCapacity)
	command.Stdout = output
	if err := command.Run(); err != nil {
		return fmt.Errorf("system lsof could not verify connected server owner: %w", err)
	}
	data := output.Bytes()
	if len(data) == serverLogCapacity {
		return fmt.Errorf("system lsof connection-owner output exceeded its limit")
	}
	return verifyLsofPeerOwner(string(data), remote.String()+"->"+conn.LocalAddr().String(), uid)
}

func verifyLsofPeerOwner(output, peer string, uid int) error {
	owner := ""
	for line := range strings.SplitSeq(output, "\n") {
		if strings.HasPrefix(line, "p") {
			owner = ""
		} else if strings.HasPrefix(line, "u") {
			owner = line[1:]
		} else if line == "n"+peer {
			if owner != strconv.Itoa(uid) {
				return fmt.Errorf("connected server belongs to another user")
			}
			return nil
		}
	}
	return fmt.Errorf("connected server owner is unavailable")
}
