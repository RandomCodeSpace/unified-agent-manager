package opencode

import (
	"context"
	"fmt"
	"net"
	"net/http"
)

// localHTTPClient checks the connected peer before HTTP can write credentials or
// session metadata. Checking the established connection, rather than the
// listener before dialing, also covers a port stolen between retries.
// Processes under the same UID can already read the child's credential env.
func localHTTPClient(uid int) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if err := verifyLocalPeer(ctx, conn, uid); err != nil {
				_ = conn.Close()
				return nil, fmt.Errorf("verify OpenCode server connection owner: %w", err)
			}
			return conn, nil
		},
	}}
}
