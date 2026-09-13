//go:build !linux && !darwin

package opencode

import (
	"context"
	"fmt"
	"net"
)

func verifyLocalPeer(context.Context, net.Conn, int) error {
	return fmt.Errorf("OpenCode connection owner verification requires Linux or macOS")
}
