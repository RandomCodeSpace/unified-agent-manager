package ipc

import (
	"encoding/binary"
	"fmt"
	"io"
)

// frameHeaderLen is the on-wire length of ID (4) + Kind (1).
const frameHeaderLen = 4 + 1

// WriteFrame writes a single request frame to w.
//
// Wire layout (big endian):
//
//	[length:4][id:4][kind:1][payload:N]
//
// where length = 4 + 1 + N.
func WriteFrame(w io.Writer, r Request) error {
	if len(r.Payload) > MaxFrameSize {
		return fmt.Errorf("ipc: frame payload exceeds %d bytes", MaxFrameSize)
	}
	body := frameHeaderLen + len(r.Payload)
	hdr := make([]byte, 4+frameHeaderLen)
	binary.BigEndian.PutUint32(hdr[:4], uint32(body))
	binary.BigEndian.PutUint32(hdr[4:8], r.ID)
	hdr[8] = byte(r.Kind)
	if _, err := w.Write(hdr); err != nil {
		return fmt.Errorf("ipc: write hdr: %w", err)
	}
	if len(r.Payload) > 0 {
		if _, err := w.Write(r.Payload); err != nil {
			return fmt.Errorf("ipc: write payload: %w", err)
		}
	}
	return nil
}

// ReadFrame reads a single request frame from r. Returns io.EOF cleanly
// when r is drained between frames.
func ReadFrame(r io.Reader) (Request, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return Request{}, err
	}
	length := binary.BigEndian.Uint32(hdr[:])
	if length > MaxFrameSize {
		return Request{}, fmt.Errorf("ipc: oversize frame (%d > %d)", length, MaxFrameSize)
	}
	if length < frameHeaderLen {
		return Request{}, fmt.Errorf("ipc: undersize frame (%d < %d)", length, frameHeaderLen)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return Request{}, err
	}
	id := binary.BigEndian.Uint32(body[:4])
	kind := Kind(body[4])
	payload := body[frameHeaderLen:]
	// Normalize zero-length payload to nil so reflect.DeepEqual round-trips
	// match callers that pass nil rather than []byte{}.
	if len(payload) == 0 {
		payload = nil
	}
	return Request{Kind: kind, ID: id, Payload: payload}, nil
}
