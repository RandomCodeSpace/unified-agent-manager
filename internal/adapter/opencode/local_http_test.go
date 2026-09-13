//go:build linux || darwin

package opencode

import (
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLocalHTTPClientRejectsWrongOwnerBeforeSendingSecrets(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	received := make(chan []byte, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			received <- nil
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		data, _ := io.ReadAll(conn)
		received <- data
	}()
	// The live kernel socket belongs to this process; require a different UID
	// to reproduce the cross-user boundary without requiring root in CI.
	client, err := newAPIClient("http://"+listener.Addr().String(), "uam", "synthetic-credential", "/private/workspace", localHTTPClient(os.Geteuid()+1))
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	_, err = client.createSession(t.Context(), "private title")
	if err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("request error = %v, want peer ownership rejection", err)
	}
	if data := <-received; len(data) != 0 {
		t.Fatalf("untrusted listener received %d bytes before rejection", len(data))
	}
}
