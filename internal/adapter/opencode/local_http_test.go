//go:build linux || darwin

package opencode

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
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
	assertLocalHTTPClientRejectsWrongOwner(t, listener)
}

func TestLocalHTTPClientDualStackPeer(t *testing.T) {
	for _, sameUser := range []bool{true, false} {
		t.Run(map[bool]string{true: "same user", false: "other user"}[sameUser], func(t *testing.T) {
			listener, err := net.Listen("tcp", "[::]:0")
			if err != nil {
				t.Fatal(err)
			}
			if listener.Addr().(*net.TCPAddr).IP.To4() != nil {
				_ = listener.Close()
				t.Fatal("fixture requires an IPv6 dual-stack listener")
			}
			if sameUser {
				assertLocalHTTPClientAcceptsOwner(t, listener)
			} else {
				assertLocalHTTPClientRejectsWrongOwner(t, listener)
			}
		})
	}
}

func assertLocalHTTPClientAcceptsOwner(t *testing.T, listener net.Listener) {
	t.Helper()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "uam" || password != "synthetic-credential" {
			t.Error("missing synthetic credentials after owner verification")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"healthy":true,"version":"1.18.1"}`)
	}))
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	// Keep the production numeric IPv4 endpoint even for a dual-stack server.
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + net.JoinHostPort("127.0.0.1", port)
	client, err := newAPIClient(baseURL, "uam", "synthetic-credential", "/private/workspace", localHTTPClient(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	health, err := client.health(t.Context())
	if err != nil || !health.Healthy {
		t.Fatalf("same-user peer health = %+v, %v", health, err)
	}
}

func assertLocalHTTPClientRejectsWrongOwner(t *testing.T, listener net.Listener) {
	t.Helper()
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
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	client, err := newAPIClient("http://"+net.JoinHostPort("127.0.0.1", port), "uam", "synthetic-credential", "/private/workspace", localHTTPClient(os.Geteuid()+1))
	if err != nil {
		t.Fatal(err)
	}
	defer client.http.CloseIdleConnections()
	_, err = client.createSession(t.Context(), "private title")
	if err == nil || !strings.Contains(err.Error(), "another user") {
		t.Fatalf("request error = %v, want peer ownership rejection", err)
	}
	_ = listener.Close() // Wake Accept when TCP_DEFER_ACCEPT received no bytes.
	if data := <-received; len(data) != 0 {
		t.Fatalf("untrusted listener received %d bytes before rejection", len(data))
	}
}
