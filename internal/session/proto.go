package session

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Wire protocol between the client (uam TUI/CLI) and a session host.
//
// Control ops are one JSON request line answered by one JSON response line on
// a fresh connection. The "attach" op upgrades the connection after its
// response: the host then streams raw PTY output bytes to the client, and the
// client sends framed messages (stdin bytes, resizes, detach) to the host.
// Framing is only needed client→host, where three message kinds share the
// stream; host→client carries exactly one kind of data so it stays raw.

type request struct {
	Op    string `json:"op"`
	Text  string `json:"text,omitempty"`
	Lines int    `json:"lines,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
	Label string `json:"label,omitempty"`
}

type response struct {
	OK   bool   `json:"ok"`
	Err  string `json:"err,omitempty"`
	Data string `json:"data,omitempty"`
}

const (
	opPeek   = "peek"
	opSend   = "send"
	opKill   = "kill"
	opLabel  = "label"
	opResize = "resize"
	opAttach = "attach"
)

// Attach stream frame types (client → host).
const (
	frameStdin  byte = 0
	frameResize byte = 1
	frameDetach byte = 2
)

// maxFrameLen bounds a single client→host frame so a corrupt or hostile
// length prefix cannot make the host allocate unbounded memory.
const maxFrameLen = 1 << 20

func writeJSONLine(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

func readJSONLine(r *bufio.Reader, v any) error {
	line, err := r.ReadBytes('\n')
	if err != nil {
		return err
	}
	return json.Unmarshal(line, v)
}

func writeFrame(w io.Writer, kind byte, payload []byte) error {
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
	mu sync.Mutex
	w  io.Writer
}

func newFrameWriter(w io.Writer) *frameWriter { return &frameWriter{w: w} }

func (w *frameWriter) WriteFrame(kind byte, payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeFrame(w.w, kind, payload)
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrameLen {
		return 0, nil, fmt.Errorf("attach frame too large: %d bytes", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return hdr[0], payload, nil
}
