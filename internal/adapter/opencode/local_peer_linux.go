//go:build linux

package opencode

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

func verifyLocalPeer(_ context.Context, conn net.Conn, uid int) error {
	file, err := os.Open("/proc/net/tcp")
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return verifyTCPPeerOwner(file, conn.LocalAddr().(*net.TCPAddr), conn.RemoteAddr().(*net.TCPAddr), uid)
}

func verifyTCPPeerOwner(table io.Reader, local, remote *net.TCPAddr, uid int) error {
	address := func(addr *net.TCPAddr) string {
		return fmt.Sprintf("%08X:%04X", binary.NativeEndian.Uint32(addr.IP.To4()), addr.Port)
	}
	localAddress, remoteAddress := address(local), address(remote)
	scanner := bufio.NewScanner(table)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		// Match the server's end of this exact established connection. The
		// client's own entry has the same UID but its addresses are reversed.
		if len(fields) < 8 || fields[1] != remoteAddress || fields[2] != localAddress || fields[3] != "01" {
			continue
		}
		owner, err := strconv.Atoi(fields[7])
		if err != nil || owner != uid {
			return fmt.Errorf("connected server belongs to another user")
		}
		return nil
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("connected server owner is unavailable")
}
