//go:build linux

package opencode

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

var errTCPPeerUnavailable = errors.New("connected server owner is unavailable")

func verifyLocalPeer(_ context.Context, conn net.Conn, uid int) error {
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		file, err := os.Open(path) // #nosec G304 -- fixed kernel TCP table paths.
		if err != nil {
			return err
		}
		err = verifyTCPPeerOwner(file, conn.LocalAddr().(*net.TCPAddr), conn.RemoteAddr().(*net.TCPAddr), uid)
		_ = file.Close()
		if !errors.Is(err, errTCPPeerUnavailable) {
			return err
		}
	}
	return errTCPPeerUnavailable
}

func verifyTCPPeerOwner(table io.Reader, local, remote *net.TCPAddr, uid int) error {
	address := func(addr *net.TCPAddr, mapped bool) string {
		ip := addr.IP.To4()
		if mapped {
			ip = addr.IP.To16()
		}
		var encoded strings.Builder
		for len(ip) > 0 {
			_, _ = fmt.Fprintf(&encoded, "%08X", binary.NativeEndian.Uint32(ip))
			ip = ip[4:]
		}
		return fmt.Sprintf("%s:%04X", encoded.String(), addr.Port)
	}
	localAddress, remoteAddress := address(local, false), address(remote, false)
	localMapped, remoteMapped := address(local, true), address(remote, true)
	scanner := bufio.NewScanner(table)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 {
			continue
		}
		// Match the server's exact reverse tuple, including IPv4-mapped
		// entries when a dual-stack listener accepts our numeric IPv4 dial.
		if (fields[1] != remoteAddress || fields[2] != localAddress) && (fields[1] != remoteMapped || fields[2] != localMapped) {
			continue
		}
		// TCP_DEFER_ACCEPT keeps the server in SYN_RECV after the client's
		// Dial completes, until HTTP sends data. Linux reports the owning
		// listener's UID on that exact pending connection, so it is safe to
		// verify before sending credentials. Never accept a listener alone.
		if fields[3] != "01" && fields[3] != "03" {
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
	return errTCPPeerUnavailable
}
