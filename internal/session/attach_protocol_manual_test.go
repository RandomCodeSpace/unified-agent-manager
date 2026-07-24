package session

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAttachProtocolRealPTYFixture(t *testing.T) {
	client := newTestClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	name := "uam-fake-a1b2c3d4"
	command := `stty raw -echo; printf 'TASK1-READY'; cat`
	if err := client.CreateSession(ctx, name, t.TempDir(), nil, []string{"/bin/sh", "-c", command}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task 1 PTY fixture readiness", func() bool {
		out, err := client.Capture(ctx, name, 20)
		return err == nil && bytes.Contains([]byte(out), []byte("TASK1-READY"))
	})

	v1Conn, err := net.Dial("unix", SocketPath(client.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONLine(v1Conn, request{Op: opAttach, Cols: 80, Rows: 24}); err != nil {
		t.Fatal(err)
	}
	v1Reader := bufio.NewReader(v1Conn)
	var v1Resp response
	if err := readBoundedJSONLine(v1Reader, &v1Resp); err != nil || !v1Resp.OK || v1Resp.versionPresent {
		t.Fatalf("v1 handshake = %+v, %v", v1Resp, err)
	}
	v1Payload := []byte{0x00, 0xff, 'V', '1', '\r', '\n'}
	if err := writeFrame(v1Conn, frameStdin, v1Payload); err != nil {
		t.Fatal(err)
	}
	v1Bytes := readUntilContains(t, v1Conn, v1Reader, v1Payload)
	if !bytes.Contains(v1Bytes, v1Payload) {
		t.Fatalf("v1 payload missing from raw stream: %x", v1Bytes)
	}
	if err := writeFrame(v1Conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	_ = v1Conn.Close()

	v2Conn, err := net.Dial("unix", SocketPath(client.Dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = v2Conn.Close() }()
	hello := validTestHello()
	if err := writeJSONLine(v2Conn, request{Op: opAttach, Version: protocolV2, Cols: 80, Rows: 24, RequestedRole: roleController, Hello: &hello}); err != nil {
		t.Fatal(err)
	}
	v2Reader := bufio.NewReader(v2Conn)
	var v2Resp response
	if err := readBoundedJSONLine(v2Reader, &v2Resp); err != nil || !v2Resp.OK || v2Resp.Version != protocolV2 {
		t.Fatalf("v2 handshake = %+v, %v", v2Resp, err)
	}
	v2Payload := []byte{0x00, 0xff, 'V', '2', '\r', '\n'}
	if err := writeFrame(v2Conn, frameStdin, ownedFramePayload(v2Resp.Generation, v2Payload)); err != nil {
		t.Fatal(err)
	}
	if err := v2Conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var v2PTY []byte
	var v2Controls [][]byte
	for !bytes.Contains(v2PTY, v2Payload) {
		kind, payload, err := readFrame(v2Reader)
		if err != nil {
			t.Fatal(err)
		}
		switch kind {
		case serverFramePTY:
			v2PTY = append(v2PTY, payload...)
		case serverFrameControl:
			v2Controls = append(v2Controls, append([]byte{}, payload...))
		default:
			t.Fatalf("unexpected server frame type %d", kind)
		}
	}
	if !bytes.Contains(v2PTY, v2Payload) {
		t.Fatalf("v2 payload missing from PTY frames: %x", v2PTY)
	}
	for _, control := range v2Controls {
		if bytes.Contains(v2PTY, control) {
			t.Fatalf("control payload leaked into PTY bytes: %x", control)
		}
	}
	if err := writeFrame(v2Conn, frameDetach, nil); err != nil {
		t.Fatal(err)
	}
	_ = v2Conn.Close()

	if err := client.Kill(ctx, name); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task 1 PTY fixture cleanup", func() bool {
		_, err := os.Stat(SocketPath(client.Dir, name))
		return os.IsNotExist(err)
	})
	writeTask1ManualEvidence(t, v1Bytes, v2PTY, v2Controls)
}

func writeTask1ManualEvidence(t *testing.T, v1, v2PTY []byte, controls [][]byte) {
	t.Helper()
	dir := os.Getenv("UAM_TASK1_EVIDENCE_DIR")
	if dir == "" {
		return
	}
	var transcript bytes.Buffer
	transcript.WriteString("UAM-TASK1\x00V1\x00")
	writeEvidenceChunk(&transcript, v1)
	transcript.WriteString("V2-PTY\x00")
	writeEvidenceChunk(&transcript, v2PTY)
	transcript.WriteString("V2-CONTROL\x00")
	_ = binary.Write(&transcript, binary.BigEndian, uint32(len(controls)))
	for _, control := range controls {
		writeEvidenceChunk(&transcript, control)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-1-protocol.bin"), transcript.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	assertions := struct {
		V1PayloadExact         bool `json:"v1_payload_exact"`
		V2PayloadExact         bool `json:"v2_payload_exact"`
		ControlFrames          int  `json:"control_frames"`
		ControlInPTY           bool `json:"control_in_pty"`
		SocketRemovedOnCleanup bool `json:"socket_removed_on_cleanup"`
	}{true, true, len(controls), false, true}
	data, err := json.MarshalIndent(assertions, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(dir, "task-1-pty-assertions.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeEvidenceChunk(dst *bytes.Buffer, data []byte) {
	_ = binary.Write(dst, binary.BigEndian, uint32(len(data)))
	dst.Write(data)
}
