package opencode

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTCPPeerOwnerRequiresConnectedReverseTuple(t *testing.T) {
	local := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 12345}
	remote := &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 54321}
	const ownClient = "0: 0100007F:3039 0100007F:D431 01 0:0 0:0 0 1001 0 10\n"
	for _, tt := range []struct {
		name, peer  string
		uid         int
		ok          bool
		unavailable bool
	}{
		{"same user", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001 0 11\n", 1001, true, false},
		{"root owner", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 0 0 11\n", 0, true, false},
		{"unaccepted nonroot", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 0 0 0\n", 1001, false, true},
		{"unaccepted with owner UID", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001 0 0\n", 1001, false, true},
		{"unaccepted root", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 0 0 0\n", 0, false, true},
		{"deferred accept same user", "1: 0100007F:D431 0100007F:3039 03 0:0 0:0 0 1001 0 0\n", 1001, true, false},
		{"deferred accept root", "1: 0100007F:D431 0100007F:3039 03 0:0 0:0 0 0 0 0\n", 0, true, false},
		{"deferred accept other user", "1: 0100007F:D431 0100007F:3039 03 0:0 0:0 0 1002 0 0\n", 1001, false, false},
		{"mapped same user", "1: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:3039 01 0:0 0:0 0 1001 0 11\n", 1001, true, false},
		{"mapped other user", "1: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:3039 01 0:0 0:0 0 1002 0 11\n", 1001, false, false},
		{"mapped deferred same user", "1: 0000000000000000FFFF00000100007F:D431 0000000000000000FFFF00000100007F:3039 03 0:0 0:0 0 1001 0 0\n", 1001, true, false},
		{"other user", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1002 0 11\n", 1001, false, false},
		{"root rejects other user", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001 0 11\n", 0, false, false},
		{"client alone", "", 1001, false, true},
		{"listener alone", "1: 0100007F:D431 00000000:0000 0A 0:0 0:0 0 1001 0 11\n", 1001, false, true},
		{"closing peer", "1: 0100007F:D431 0100007F:3039 08 0:0 0:0 0 1001 0 11\n", 1001, false, true},
		{"invalid owner", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 bad 0 11\n", 1001, false, false},
		{"missing inode", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001\n", 1001, false, true},
		{"invalid inode", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001 0 bad\n", 1001, false, true},
		{"negative inode", "1: 0100007F:D431 0100007F:3039 01 0:0 0:0 0 1001 0 -1\n", 1001, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyTCPPeerOwner(strings.NewReader(ownClient+tt.peer), local, remote, tt.uid)
			if (err == nil) != tt.ok {
				t.Fatalf("verifyTCPPeerOwner = %v, want accepted=%v", err, tt.ok)
			}
			if errors.Is(err, errTCPPeerUnavailable) != tt.unavailable {
				t.Fatalf("verifyTCPPeerOwner = %v, want unavailable=%v", err, tt.unavailable)
			}
		})
	}
}

func TestLocalHTTPClientWaitsForAcceptedOwnerBeforeSendingSecrets(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "uam" || password != "synthetic-credential" {
			t.Error("missing credentials after ownership publication")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	_ = server.Listener.Close()
	server.Listener = listener
	defer server.Close()
	client := localHTTPClient(os.Geteuid())
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+listener.Addr().String(), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.SetBasicAuth("uam", "synthetic-credential")
	done := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			_ = response.Body.Close()
		}
		done <- err
	}()
	// The server has not accepted yet. Inspect its exact local endpoint to
	// prove no HTTP bytes reached the unpublished socket, including for root.
	address := fmt.Sprintf("0100007F:%04X", listener.Addr().(*net.TCPAddr).Port)
	pendingWithoutData := func() bool {
		data, err := os.ReadFile("/proc/net/tcp")
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) < 10 || fields[1] != address || fields[3] != "01" {
				continue
			}
			if fields[9] != "0" || fields[4] != "00000000:00000000" {
				t.Fatalf("unaccepted socket owner/queues = %v", fields[4:10])
			}
			return true
		}
		return false
	}
	deadline := time.Now().Add(time.Second)
	for !pendingWithoutData() {
		select {
		case err := <-done:
			t.Fatalf("request ended before accept: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("request did not establish a pending connection")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("request ended while ownership remained unpublished: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if !pendingWithoutData() {
		t.Fatal("pending connection disappeared before accept")
	}
	server.Start()
	if err := <-done; err != nil {
		t.Fatalf("request after delayed accept: %v", err)
	}
}

func TestLocalPeerUnavailableHonorsCancellation(t *testing.T) {
	for _, mode := range []string{"already canceled", "cancel while waiting", "deadline", "default bound"} {
		t.Run(mode, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close() }()
			conn, err := net.Dial("tcp4", listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			want := error(context.DeadlineExceeded)
			switch mode {
			case "already canceled":
				cancel()
				want = context.Canceled
			case "cancel while waiting":
				timer := time.AfterFunc(10*time.Millisecond, cancel)
				defer timer.Stop()
				want = context.Canceled
			case "deadline":
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 20*time.Millisecond)
				defer stop()
			}
			started := time.Now()
			if err := verifyLocalPeer(ctx, conn, os.Geteuid()); !errors.Is(err, want) {
				t.Fatalf("verifyLocalPeer = %v, want %v", err, want)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("ownership wait took %v, want a bounded wait", elapsed)
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
