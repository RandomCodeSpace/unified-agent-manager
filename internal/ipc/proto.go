// Package ipc defines the wire types for uam's supervisor RPC.
//
// Frame format: 4-byte big-endian length prefix + 4-byte big-endian RequestID
// + 1-byte Kind + N-byte JSON payload. The length covers everything after
// itself (ID + Kind + Payload).
package ipc

// Kind enumerates RPC verbs.
type Kind uint8

// RPC verbs. Kept stable across versions; new kinds append.
const (
	KindHello     Kind = 1
	KindSpawn     Kind = 2
	KindHas       Kind = 3
	KindList      Kind = 4
	KindCapture   Kind = 5
	KindWrite     Kind = 6
	KindResize    Kind = 7
	KindKill      Kind = 8
	KindAttach    Kind = 9
	KindSubscribe Kind = 10
	KindStatus    Kind = 11
	KindShutdown  Kind = 12
	KindGoodbye   Kind = 13
)

// Request is a single RPC frame on the wire.
type Request struct {
	Kind    Kind
	ID      uint32 // correlation id; echoed by the matching Response
	Payload []byte // Kind-specific body (typically JSON)
}

// Response is a single RPC reply frame. The framing on the wire is the
// same Request shape (the receiver demuxes by ID), but the higher level
// pairs an outgoing Request with this struct.
type Response struct {
	ID      uint32
	Error   string
	Payload []byte
}

// MaxFrameSize caps a single frame's wire length at 8 MiB. Defends
// against malicious or buggy peers from forcing huge allocations.
const MaxFrameSize = 8 * 1024 * 1024
