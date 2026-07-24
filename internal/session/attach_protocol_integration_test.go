package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestAttachProtocolCompatibilityMatrix(t *testing.T) {
	t.Run("v2 client consumes unversioned v1 host output as raw bytes", func(t *testing.T) {
		dir := socketTestDir(t)
		if err := EnsureDir(dir); err != nil {
			t.Fatal(err)
		}
		name := "uam-fake-77778888"
		ln, err := net.Listen("unix", SocketPath(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		payload := []byte{0x00, 0xff, 'v', '1', '-', 'r', 'a', 'w', '\r', '\n'}
		serverErr := make(chan error, 1)
		go func() {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				serverErr <- acceptErr
				return
			}
			defer func() { _ = conn.Close() }()
			var req request
			if err := readJSONLine(bufio.NewReader(conn), &req); err != nil {
				serverErr <- err
				return
			}
			if req.Version != protocolV2 {
				serverErr <- errors.New("client did not request v2")
				return
			}
			if err := writeJSONLine(conn, response{OK: true}); err != nil {
				serverErr <- err
				return
			}
			serverErr <- writeAll(conn, payload)
		}()

		stdinR, stdinW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stdinR.Close(); _ = stdinW.Close() }()
		stdoutR, stdoutW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = stdoutR.Close(); _ = stdoutW.Close() }()
		if err := runAttachWithOptions(dir, name, stdinR, stdoutW, attachOptions{quiet: true}); err != nil {
			t.Fatal(err)
		}
		_ = stdoutW.Close()
		got, err := io.ReadAll(stdoutR)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("fallback output = %x, want %x", got, payload)
		}
		if err := <-serverErr; err != nil {
			t.Fatalf("v1 host fixture: %v", err)
		}
	})

	t.Run("v2 client falls back only for an unversioned v1 response", func(t *testing.T) {
		var resp response
		if err := jsonUnmarshalLine(`{"ok":true}`, &resp); err != nil {
			t.Fatal(err)
		}
		got, err := negotiateAttachResponse(protocolV2, resp)
		if err != nil || got != protocolV1 {
			t.Fatalf("negotiation = %d, %v; want v1 fallback", got, err)
		}

		if err := jsonUnmarshalLine(`{"ok":true,"version":1}`, &resp); err != nil {
			t.Fatal(err)
		}
		if _, err := negotiateAttachResponse(protocolV2, resp); err == nil {
			t.Fatal("explicit response downgrade was accepted")
		}
	})

	t.Run("v2 host preserves v1 raw and uses v2 server frames", func(t *testing.T) {
		client := newTestClient(t)
		name := "uam-fake-33334444"
		done := startInProcessHost(t, client, name, `printf 'compat-marker'; while IFS= read -r line; do printf 'in:%s\n' "$line"; done`)
		cleanupProtocolHost(t, client, name, done)

		v1, err := net.Dial("unix", SocketPath(client.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = v1.Close() }()
		if err := writeJSONLine(v1, request{Op: opAttach, Cols: 80, Rows: 24}); err != nil {
			t.Fatal(err)
		}
		v1Reader := bufio.NewReader(v1)
		var v1Resp response
		if err := readJSONLine(v1Reader, &v1Resp); err != nil {
			t.Fatal(err)
		}
		if !v1Resp.OK || v1Resp.versionPresent {
			t.Fatalf("v1 response = %+v, version present = %v", v1Resp, v1Resp.versionPresent)
		}
		v1Raw := readUntilContains(t, v1, v1Reader, []byte("compat-marker"))
		if bytes.HasPrefix(v1Raw, []byte{serverFramePTY}) {
			t.Fatalf("v1 output unexpectedly framed: %x", v1Raw)
		}

		secondV1, err := net.Dial("unix", SocketPath(client.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSONLine(secondV1, request{Op: opAttach, Cols: 80, Rows: 24}); err != nil {
			t.Fatal(err)
		}
		var rejected response
		if err := readJSONLine(bufio.NewReader(secondV1), &rejected); err != nil {
			t.Fatal(err)
		}
		_ = secondV1.Close()
		if rejected.OK || !strings.Contains(rejected.Err, "legacy attach already controlled") {
			t.Fatalf("second v1 response = %+v, want controlled rejection", rejected)
		}
		if err := writeFrame(v1, frameDetach, nil); err != nil {
			t.Fatal(err)
		}
		if err := v1.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, v1Reader); err != nil {
			t.Fatalf("wait for v1 detach: %v", err)
		}
		_ = v1.Close()

		v2, err := net.Dial("unix", SocketPath(client.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = v2.Close() }()
		hello := validTestHello()
		if err := writeJSONLine(v2, request{Op: opAttach, Version: protocolV2, Cols: 80, Rows: 24, RequestedRole: roleController, Hello: &hello}); err != nil {
			t.Fatal(err)
		}
		v2Reader := bufio.NewReader(v2)
		var v2Resp response
		if err := readJSONLine(v2Reader, &v2Resp); err != nil {
			t.Fatal(err)
		}
		if !v2Resp.OK || !v2Resp.versionPresent || v2Resp.Version != protocolV2 {
			t.Fatalf("v2 response = %+v, version present = %v", v2Resp, v2Resp.versionPresent)
		}
		kind, payload, err := readFrame(v2Reader)
		if err != nil {
			t.Fatal(err)
		}
		if kind != serverFrameControl || !bytes.Contains(payload, []byte(`"role":"controller"`)) {
			t.Fatalf("v2 role frame = kind %d payload %x", kind, payload)
		}
		kind, payload, err = readFrame(v2Reader)
		if err != nil {
			t.Fatal(err)
		}
		if kind != serverFramePTY || !bytes.Contains(payload, []byte("compat-marker")) {
			t.Fatalf("v2 first frame = kind %d payload %x", kind, payload)
		}
		if err := writeFrame(v2, frameDetach, nil); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unsupported explicit versions are rejected", func(t *testing.T) {
		var req request
		if err := jsonUnmarshalLine(`{"op":"attach","version":99}`, &req); err != nil {
			t.Fatal(err)
		}
		if _, err := negotiateAttachRequest(req); err == nil {
			t.Fatal("unsupported request version was accepted")
		}
	})
}

func cleanupProtocolHost(t *testing.T, client *Client, name string, done <-chan error) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if client.HasSession(ctx, name) {
			if err := client.Kill(ctx, name); err != nil {
				t.Errorf("kill protocol host: %v", err)
			}
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("protocol host: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("protocol host cleanup timed out")
		}
	})
}

func readUntilContains(t *testing.T, conn net.Conn, reader *bufio.Reader, marker []byte) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var got []byte
	buf := make([]byte, 4096)
	for !bytes.Contains(got, marker) {
		n, err := reader.Read(buf)
		if n > 0 {
			got = append(got, buf[:n]...)
		}
		if err != nil {
			t.Fatalf("read output marker: %v", err)
		}
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	return got
}
