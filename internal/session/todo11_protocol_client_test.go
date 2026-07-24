package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net"
	"testing"
	"time"
)

type todo11ProtocolAttach struct {
	conn       net.Conn
	reader     *bufio.Reader
	clientID   string
	generation uint64
	role       clientRole
	replay     []byte
}

func (harness *todo11HostHarness) attach(t *testing.T, termHint string, role clientRole) *todo11ProtocolAttach {
	t.Helper()
	conn, err := net.Dial("unix", SocketPath(harness.runtimeDir, harness.name))
	if err != nil {
		t.Fatal(err)
	}
	hello := validTestHello()
	hello.TermHint = termHint
	req := request{
		Op: opAttach, Version: protocolV2, Cols: 110, Rows: 32,
		RequestedRole: role, Hello: &hello,
	}
	requestData, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidenceChunk(&harness.transcript, requestData)
	if err := writeJSONLine(conn, req); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	var response response
	if err := readBoundedJSONLine(reader, &response); err != nil {
		t.Fatal(err)
	}
	responseData, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	writeEvidenceChunk(&harness.transcript, responseData)
	roleAccepted := response.AssignedRole == role ||
		(role == roleController && response.AssignedRole == roleStandby)
	if !response.OK || response.Version != protocolV2 || !roleAccepted {
		t.Fatalf("attach response = %+v, want v2 %s", response, role)
	}
	attached := &todo11ProtocolAttach{
		conn: conn, reader: reader, clientID: response.ClientID,
		generation: response.Generation, role: response.AssignedRole,
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	roleFrameObserved := false
	for attempts := 0; attempts < 8 && (!roleFrameObserved || len(attached.replay) == 0); attempts++ {
		kind, payload, frameErr := readFrame(reader)
		if frameErr != nil {
			t.Fatal(frameErr)
		}
		harness.transcript.WriteByte(kind)
		writeEvidenceChunk(&harness.transcript, payload)
		switch kind {
		case serverFramePTY:
			attached.replay = append(attached.replay, payload...)
		case serverFrameControl:
			roleFrameObserved = attached.observeRole(t, payload) || roleFrameObserved
		}
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if len(attached.replay) == 0 {
		t.Fatal("attach replay was empty")
	}
	if !roleFrameObserved {
		t.Fatal("attach role frame was not observed")
	}
	return attached
}

func (attached *todo11ProtocolAttach) observeRole(t *testing.T, payload []byte) bool {
	t.Helper()
	var event roleEvent
	if err := json.Unmarshal(payload, &event); err != nil || event.Type != "role" {
		return false
	}
	attached.role = event.Role
	attached.generation = event.Generation
	return true
}

func (attached *todo11ProtocolAttach) awaitController(t *testing.T, transcript *bytes.Buffer) {
	t.Helper()
	if attached.role == roleController {
		return
	}
	if err := attached.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := attached.conn.SetReadDeadline(time.Time{}); err != nil {
			t.Fatal(err)
		}
	}()
	for attached.role != roleController {
		kind, payload, err := readFrame(attached.reader)
		if err != nil {
			t.Fatalf("wait for controller promotion: %v", err)
		}
		transcript.WriteByte(kind)
		writeEvidenceChunk(transcript, payload)
		switch kind {
		case serverFramePTY:
			attached.replay = append(attached.replay, payload...)
		case serverFrameControl:
			attached.observeRole(t, payload)
		}
	}
}

func (attached *todo11ProtocolAttach) write(kind byte, payload []byte) error {
	framed, err := ownedFramePayload(attached.generation, payload)
	if err != nil {
		return err
	}
	return writeFrame(attached.conn, kind, framed)
}

func (attached *todo11ProtocolAttach) close() {
	_ = attached.conn.Close()
}

func todo11WriteInput(t *testing.T, attached *todo11ProtocolAttach, data []byte) {
	t.Helper()
	framed := append(append([]byte{}, data...), todo11InputDelimiter)
	if err := attached.write(frameStdin, framed); err != nil {
		t.Fatal(err)
	}
}

func todo11DropMalformed(t *testing.T, attached *todo11ProtocolAttach, truncated bool) {
	t.Helper()
	if truncated {
		header := [5]byte{frameStdin}
		binary.BigEndian.PutUint32(header[1:], 4)
		_, _ = attached.conn.Write(append(header[:], 'x'))
		attached.close()
		return
	}
	_, err := attached.conn.Write([]byte{frameStdin, 0xff, 0xff, 0xff, 0xff})
	if err != nil {
		t.Fatal(err)
	}
	attached.close()
}
