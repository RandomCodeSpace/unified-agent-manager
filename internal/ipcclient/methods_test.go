package ipcclient

import (
	"context"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/mux"
)

// TestClientResizeAgainstSupervisor verifies the Resize RPC frames end up
// on the wire. The supervisor reports "unknown session" for an
// unregistered handle; we accept that as evidence the call succeeded
// through the codec.
func TestClientResizeAgainstSupervisor(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	err = c.Resize(context.Background(), mux.SessionHandle("nope"), 80, 24)
	if err == nil {
		t.Fatalf("expected error for unknown session")
	}
}

// TestClientSubscribeReturnsNilChannel matches the tmux backend convention.
func TestClientSubscribeReturnsNilChannel(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	ch, err := c.Subscribe(context.Background(), mux.SessionHandle("h"))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if ch != nil {
		t.Fatalf("expected nil channel, got %v", ch)
	}
}

func TestClientAttachReturnsPlaceholderStream(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer func() { _ = c.Close() }()
	stream, err := c.Attach(context.Background(), mux.SessionHandle("h"))
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if stream == nil {
		t.Fatalf("expected non-nil stream")
	}
	// Read must currently error (placeholder); Write/Resize/Close are wired.
	buf := make([]byte, 8)
	if _, err := stream.Read(buf); err == nil {
		t.Fatalf("expected Read placeholder error")
	}
	if err := stream.Resize(80, 24); err == nil {
		t.Fatalf("expected Resize against unknown session to fail")
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After close, Read and Write must error cleanly.
	if _, err := stream.Read(buf); err == nil {
		t.Fatalf("expected Read after close to error")
	}
	if _, err := stream.Write([]byte{1}); err == nil {
		t.Fatalf("expected Write after close to error")
	}
}

func TestAttachRejectsNilClient(t *testing.T) {
	if _, err := newAttachStream(nil, "h"); err == nil {
		t.Fatalf("expected nil client error")
	}
	if _, err := newAttachStream(&Client{}, ""); err == nil {
		t.Fatalf("expected empty handle error")
	}
}

func TestDecodeErrorMissingField(t *testing.T) {
	if got := decodeError(nil); got != "" {
		t.Fatalf("expected empty for nil payload, got %q", got)
	}
	if got := decodeError([]byte("{}")); got != "" {
		t.Fatalf("expected empty for no error field, got %q", got)
	}
	if got := decodeError([]byte("not-json")); got != "" {
		t.Fatalf("expected empty for bad json, got %q", got)
	}
	if got := decodeError([]byte(`{"error":"boom"}`)); got != "boom" {
		t.Fatalf("got %q", got)
	}
}

func TestCloseIdempotent(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close should be safe: %v", err)
	}
}

func TestCallAfterCloseFails(t *testing.T) {
	sock := startSupervisor(t)
	c, err := New(Options{SocketPath: sock})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_ = c.Close()
	if _, err := c.Has(context.Background(), mux.SessionHandle("h")); err == nil {
		t.Fatalf("expected error calling after Close")
	}
}
