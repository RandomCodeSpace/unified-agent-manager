package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func TestAttachHandshakeTimeout(t *testing.T) {
	dir := socketTestDir(t)
	if err := EnsureDir(dir); err != nil {
		t.Fatal(err)
	}
	name := "uam-fake-11112222"
	ln, err := net.Listen("unix", SocketPath(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	peerDone := make(chan struct{})
	go func() {
		defer close(peerDone)
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		var req request
		_ = readJSONLine(bufio.NewReader(conn), &req)
		_, _ = io.Copy(io.Discard, conn)
	}()

	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ptmx.Close(); _ = tty.Close() })
	before, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = runAttachWithOptions(dir, name, tty, tty, attachOptions{quiet: true})
	elapsed := time.Since(started)
	if err == nil || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("attach error = %v, want handshake timeout", err)
	}
	if elapsed < 1500*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("handshake elapsed = %s, want approximately 2s", elapsed)
	}
	after, err := term.GetState(tty.Fd())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("terminal state changed before negotiation completed")
	}
	select {
	case <-peerDone:
	case <-time.After(time.Second):
		t.Fatal("silent peer did not exit after handshake timeout")
	}
}

func TestAttachHandshakeOversizedLine(t *testing.T) {
	valid := []byte(`{"op":"attach"}`)
	exact := append(append([]byte{}, valid...), bytes.Repeat([]byte{' '}, maxControlLine-len(valid))...)
	exact = append(exact, '\n')
	var exactReq request
	if err := readBoundedJSONLine(bufio.NewReader(bytes.NewReader(exact)), &exactReq); err != nil {
		t.Fatalf("exact-limit line rejected: %v", err)
	}

	line := append(bytes.Repeat([]byte{'x'}, maxControlLine+1), '\n')
	var req request
	err := readBoundedJSONLine(bufio.NewReader(bytes.NewReader(line)), &req)
	if !errors.Is(err, errControlLineTooLarge) {
		t.Fatalf("oversized line error = %v, want %v", err, errControlLineTooLarge)
	}
}

func TestAttachServerFrameLimit(t *testing.T) {
	maximum := bytes.Repeat([]byte{'m'}, maxFrameLen)
	var maximumWire bytes.Buffer
	if err := writeFrame(&maximumWire, serverFramePTY, maximum); err != nil {
		t.Fatalf("maximum frame write: %v", err)
	}
	_, maximumRead, err := readFrame(&maximumWire)
	if err != nil || !bytes.Equal(maximumRead, maximum) {
		t.Fatalf("maximum frame round trip: len=%d err=%v", len(maximumRead), err)
	}

	payload := bytes.Repeat([]byte{'x'}, maxFrameLen+1)
	if err := writeFrame(io.Discard, serverFramePTY, payload); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("oversized write error = %v, want %v", err, errFrameTooLarge)
	}

	var header [5]byte
	header[0] = serverFramePTY
	binary.BigEndian.PutUint32(header[1:], uint32(maxFrameLen+1))
	if _, _, err := readFrame(bytes.NewReader(header[:])); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("oversized read error = %v, want %v", err, errFrameTooLarge)
	}
}

func TestAttachV2OutputIsByteExact(t *testing.T) {
	first := []byte{0x00, 0xff, 'p', 't', 'y', '\r', '\n'}
	second := []byte{serverFramePTY, 0, 0, 0, 3, 'e', 'n', 'd'}
	var wire bytes.Buffer
	if err := writeFrame(&wire, serverFramePTY, first); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(&wire, serverFrameControl, []byte(`{"type":"role","role":"observer"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(&wire, serverFramePTY, second); err != nil {
		t.Fatal(err)
	}

	var terminal bytes.Buffer
	if err := copyAttachOutput(&terminal, bufio.NewReader(&wire), protocolV2, true); err != nil {
		t.Fatalf("copy v2 output: %v", err)
	}
	want := append(append([]byte{}, first...), second...)
	if !bytes.Equal(terminal.Bytes(), want) {
		t.Fatalf("terminal bytes = %x, want %x", terminal.Bytes(), want)
	}
}

func TestAttachMalformedExplicitVersionsNeverDowngrade(t *testing.T) {
	for _, raw := range []string{
		`{"op":"attach","version":null}`,
		`{"op":"attach","version":"2"}`,
		`{"op":"attach","version":2.5}`,
		`{"ok":true,"version":null}`,
		`{"ok":true,"version":"2"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			if strings.Contains(raw, `"op"`) {
				var req request
				if err := jsonUnmarshalLine(raw, &req); err == nil {
					t.Fatal("malformed request version was accepted")
				}
				return
			}
			var resp response
			if err := jsonUnmarshalLine(raw, &resp); err == nil {
				t.Fatal("malformed response version was accepted")
			}
		})
	}
}

func TestAttachExplicitVersionWireRejection(t *testing.T) {
	for _, raw := range []string{
		`{"op":"attach","version":99}`,
		`{"op":"attach","version":null}`,
		`{"op":"attach","version":"2"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
			done := make(chan struct{})
			go func() {
				defer close(done)
				(&host{}).handleConn(server)
			}()
			if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := writeAll(client, []byte(raw+"\n")); err != nil {
				t.Fatal(err)
			}
			var resp response
			if err := readBoundedJSONLine(bufio.NewReader(client), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.OK || resp.Err == "" {
				t.Fatalf("explicit invalid version response = %+v", resp)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("rejecting host did not close")
			}
		})
	}
}

func TestAttachHostHandshakeTimeout(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	done := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(done)
		(&host{}).handleConn(server)
	}()
	if err := client.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := readBoundedJSONLine(bufio.NewReader(client), &resp); err != nil {
		t.Fatalf("read host timeout response: %v", err)
	}
	if resp.OK || !strings.Contains(resp.Err, "i/o timeout") {
		t.Fatalf("host timeout response = %+v", resp)
	}
	select {
	case <-done:
		elapsed := time.Since(started)
		if elapsed < 1500*time.Millisecond || elapsed > 3*time.Second {
			t.Fatalf("host handshake elapsed = %s, want approximately 2s", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("host did not time out a silent handshake")
	}
}

func jsonUnmarshalLine(raw string, dst any) error {
	return readBoundedJSONLine(bufio.NewReader(strings.NewReader(raw+"\n")), dst)
}
