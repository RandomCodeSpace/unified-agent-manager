package session

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Wire protocol between the client (uam TUI/CLI) and a session host.
//
// Control ops are one JSON request line answered by one JSON response line on
// a fresh connection. The "attach" op upgrades the connection after its
// response. Version 1 then streams raw PTY output to the client while client
// input remains framed. Version 2 frames both PTY output and server controls.

type protocolVersion int

const (
	protocolV1 protocolVersion = 1
	protocolV2 protocolVersion = 2
)

const (
	attachHandshakeTimeout = 2 * time.Second
	maxControlLine         = 64 << 10
)

var (
	errControlLineTooLarge = errors.New("attach control line too large")
	errFrameTooLarge       = errors.New("attach frame too large")
	errOwnershipEpoch      = errors.New("invalid attach ownership epoch")
)

type request struct {
	Op             string          `json:"op"`
	Text           string          `json:"text,omitempty"`
	Lines          int             `json:"lines,omitempty"`
	Cols           int             `json:"cols,omitempty"`
	Rows           int             `json:"rows,omitempty"`
	Label          string          `json:"label,omitempty"`
	Version        protocolVersion `json:"version,omitempty"`
	RequestedRole  clientRole      `json:"requested_role,omitempty"`
	Hello          *clientHello    `json:"hello,omitempty"`
	versionPresent bool
}

type response struct {
	OK             bool               `json:"ok"`
	Err            string             `json:"err,omitempty"`
	ErrorCode      string             `json:"error_code,omitempty"`
	Data           string             `json:"data,omitempty"`
	Version        protocolVersion    `json:"version,omitempty"`
	ClientID       string             `json:"client_id,omitempty"`
	AssignedRole   clientRole         `json:"assigned_role,omitempty"`
	Generation     uint64             `json:"generation,omitempty"`
	Diagnostic     *RuntimeDiagnostic `json:"diagnostic,omitempty"`
	versionPresent bool
}

func (r *request) UnmarshalJSON(data []byte) error {
	type wireRequest request
	var decoded wireRequest
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	present, err := explicitVersionPresent(data)
	if err != nil {
		return err
	}
	*r = request(decoded)
	r.versionPresent = present
	return nil
}

func (r *response) UnmarshalJSON(data []byte) error {
	type wireResponse response
	var decoded wireResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	present, err := explicitVersionPresent(data)
	if err != nil {
		return err
	}
	*r = response(decoded)
	r.versionPresent = present
	return nil
}

func explicitVersionPresent(data []byte) (bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return false, err
	}
	raw, present := fields["version"]
	if !present {
		return false, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, errors.New("attach protocol version cannot be null")
	}
	return true, nil
}

func negotiateAttachRequest(req request) (protocolVersion, error) {
	if !req.versionPresent && req.Version == 0 {
		return protocolV1, nil
	}
	switch req.Version {
	case protocolV1, protocolV2:
		return req.Version, nil
	default:
		return 0, fmt.Errorf("unsupported attach protocol version %d", req.Version)
	}
}

func negotiateAttachResponse(requested protocolVersion, resp response) (protocolVersion, error) {
	if !resp.versionPresent && resp.Version == 0 {
		return protocolV1, nil
	}
	if resp.Version != requested {
		return 0, fmt.Errorf("attach protocol response version %d does not match requested version %d", resp.Version, requested)
	}
	return requested, nil
}

const (
	opPeek   = "peek"
	opSend   = "send"
	opKill   = "kill"
	opLabel  = "label"
	opResize = "resize"
	opAttach = "attach"
	opDoctor = "doctor"
)

// Attach stream frame types (client → host).
const (
	frameStdin  byte = 0
	frameResize byte = 1
	frameDetach byte = 2
	frameRole   byte = 3
)

// Attach stream frame types (host → v2 client).
const (
	serverFramePTY     byte = 0
	serverFrameControl byte = 1
)

// maxFrameLen bounds a single client→host frame so a corrupt or hostile
// length prefix cannot make the host allocate unbounded memory.
const maxFrameLen = 1 << 20

const ownershipEpochLen = 8

func ownedFramePayload(generation uint64, payload []byte) []byte {
	framed := make([]byte, ownershipEpochLen+len(payload))
	binary.BigEndian.PutUint64(framed, generation)
	copy(framed[ownershipEpochLen:], payload)
	return framed
}

func parseOwnedFramePayload(payload []byte) (uint64, []byte, error) {
	if len(payload) < ownershipEpochLen {
		return 0, nil, errOwnershipEpoch
	}
	return binary.BigEndian.Uint64(payload[:ownershipEpochLen]), payload[ownershipEpochLen:], nil
}

func writeJSONLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeAll(w, append(data, '\n'))
}

func readJSONLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

func readBoundedJSONLine(r *bufio.Reader, v any) error {
	line := make([]byte, 0, min(maxControlLine, r.Size()))
	for {
		part, err := r.ReadSlice('\n')
		contentLength := len(line) + len(part)
		if err == nil && len(part) > 0 && part[len(part)-1] == '\n' {
			contentLength--
		}
		if contentLength > maxControlLine {
			return fmt.Errorf("%w: limit is %d bytes", errControlLineTooLarge, maxControlLine)
		}
		line = append(line, part...)
		switch {
		case err == nil:
			return json.Unmarshal(line, v)
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return err
		}
	}
}

func writeFrame(w io.Writer, kind byte, payload []byte) error {
	if len(payload) > maxFrameLen {
		return fmt.Errorf("%w: %d bytes", errFrameTooLarge, len(payload))
	}
	hdr := [5]byte{kind}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload))) // #nosec G115 -- payload length is bounded by callers
	if err := writeAll(w, hdr[:]); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeAll(w, payload)
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

// frameWriter serializes complete attach frames. A frame is two physical
// writes (header then payload), so protecting individual net.Conn.Write calls
// is insufficient when stdin, resize, and detach goroutines share a socket.
type frameWriter struct {
	mu         sync.Mutex
	w          io.Writer
	version    protocolVersion
	clientID   string
	role       clientRole
	generation uint64
}

func newFrameWriter(w io.Writer) *frameWriter { return &frameWriter{w: w} }

func newAttachFrameWriter(w io.Writer, version protocolVersion, clientID string, generation uint64) *frameWriter {
	return &frameWriter{w: w, version: version, clientID: clientID, generation: generation}
}

func (w *frameWriter) SetAssignedRole(role clientRole) {
	w.mu.Lock()
	w.role = role
	w.mu.Unlock()
}

func (w *frameWriter) AssignedRole() clientRole {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.role
}

func (w *frameWriter) Generation() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.generation
}

func (w *frameWriter) ClientID() string {
	return w.clientID
}

func (w *frameWriter) HasControl() bool {
	return w.version == protocolV1 || w.AssignedRole() == roleController
}

func (w *frameWriter) ObserveControl(payload []byte) error {
	_, _, err := w.observeRoleEvent(payload, nil)
	return err
}

func (w *frameWriter) observeRoleEvent(payload []byte, beforePromotion func() error) (roleEvent, bool, error) {
	var event roleEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return roleEvent{}, false, fmt.Errorf("decode attach control event: %w", err)
	}
	if event.Type != "role" {
		return roleEvent{}, false, fmt.Errorf("unsupported attach control event %q", event.Type)
	}
	if event.Role != "" {
		if err := validateRequestedRole(event.Role); err != nil {
			return roleEvent{}, false, fmt.Errorf("invalid attach role event: %w", err)
		}
	}
	if event.ClientID != w.clientID {
		return event, false, nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	changed := event.Role != "" && event.Role != w.role
	if changed && event.Role == roleController && beforePromotion != nil {
		if err := beforePromotion(); err != nil {
			return roleEvent{}, false, err
		}
	}
	if event.Role != "" {
		w.role = event.Role
	}
	if event.Generation > w.generation {
		w.generation = event.Generation
	}
	return event, changed, nil
}

func (w *frameWriter) WriteRoleCommand(action roleAction) error {
	payload, err := json.Marshal(roleCommand{Action: action})
	if err != nil {
		return fmt.Errorf("encode attach role command: %w", err)
	}
	return w.WriteFrame(frameRole, payload)
}

func (w *frameWriter) WriteFrame(kind byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.version == protocolV2 && (kind == frameStdin || kind == frameResize) {
		payload = ownedFramePayload(w.generation, payload)
	}
	return writeFrame(w.w, kind, payload)
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrameLen {
		return 0, nil, fmt.Errorf("%w: %d bytes", errFrameTooLarge, n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}
