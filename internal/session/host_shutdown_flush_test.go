package session

import (
	"bufio"
	"bytes"
	"net"
	"testing"
	"time"
)

// Output still queued when the host tears down must reach the viewer. The
// distinction the writer has to honour: `done` means stop now, `flush` means
// finish first. Dropping the client outright — as shutdown used to — truncated
// the agent's last screen, and a half-written frame surfaced on the viewer as
// "attach output: unexpected EOF" instead of a clean session end.
func TestWriterFlushesQueuedOutputOnShutdownSignal(t *testing.T) {
	// Given three frames queued and a shutdown already requested.
	server, viewer := net.Pipe()
	t.Cleanup(func() { _ = viewer.Close() })
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	client := newFlushTestClient(t, h, server)
	payloads := [][]byte{[]byte("first\r\n"), []byte("second\r\n"), []byte("agent final line\r\n")}
	for _, payload := range payloads {
		if !h.enqueueClient(client, serverMessage{kind: serverFramePTY, payload: payload}) {
			t.Fatal("queueing the agent's output failed")
		}
	}
	client.requestFlush()

	// When the writer runs.
	go h.attachWriter(client)

	// Then every queued frame arrives before the connection closes.
	if err := viewer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(viewer)
	for i, want := range payloads {
		kind, payload, err := readFrame(reader)
		if err != nil {
			t.Fatalf("frame %d lost on shutdown: %v", i, err)
		}
		if kind != serverFramePTY || !bytes.Equal(payload, want) {
			t.Fatalf("frame %d = %d %q, want %d %q", i, kind, payload, serverFramePTY, want)
		}
	}
	waitClosed(t, client)
}

// The contrast that makes the distinction meaningful: a client told to stop
// now discards what is queued.
func TestWriterStopsImmediatelyOnDone(t *testing.T) {
	server, viewer := net.Pipe()
	t.Cleanup(func() { _ = viewer.Close() })
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	client := newFlushTestClient(t, h, server)
	if err := viewer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	h.enqueueClient(client, serverMessage{kind: serverFramePTY, payload: []byte("discarded")})
	client.drop()

	go h.attachWriter(client)

	if _, _, err := readFrame(bufio.NewReader(viewer)); err == nil {
		t.Fatal("a dropped client still wrote its queue")
	}
}

func newFlushTestClient(t *testing.T, h *host, conn net.Conn) *attachClient {
	t.Helper()
	client := &attachClient{
		conn: conn, out: make(chan serverMessage, attachBufFrames),
		done: make(chan struct{}), flush: make(chan struct{}), version: protocolV2,
	}
	if err := h.registry.register(client, clientRegistration{
		requestedRole: roleController, hello: validTestHello(),
		size: terminalSize{cols: 80, rows: 24},
	}); err != nil {
		t.Fatal(err)
	}
	client.ready = true
	return client
}

func waitClosed(t *testing.T, client *attachClient) {
	t.Helper()
	select {
	case <-client.done:
	case <-time.After(3 * time.Second):
		t.Fatal("client was not closed after the flush")
	}
}

// A viewer that has stopped reading must not hold teardown open.
func TestShutdownDoesNotWaitForeverOnAStalledViewer(t *testing.T) {
	// Given a client whose peer never reads.
	server, viewer := net.Pipe()
	t.Cleanup(func() { _ = viewer.Close() })
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	client := &attachClient{
		conn: server, out: make(chan serverMessage, attachBufFrames),
		done: make(chan struct{}), flush: make(chan struct{}), version: protocolV2,
	}
	if err := h.registry.register(client, clientRegistration{
		requestedRole: roleController, hello: validTestHello(),
		size: terminalSize{cols: 80, rows: 24},
	}); err != nil {
		t.Fatal(err)
	}
	client.ready = true
	go h.attachWriter(client)
	h.enqueueClient(client, serverMessage{kind: serverFramePTY, payload: bytes.Repeat([]byte("x"), 4096)})

	// When the host tears down.
	start := time.Now()
	h.shutdownClients()

	// Then it returns within the flush budget and the client is closed.
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("shutdown blocked for %s on a stalled viewer", elapsed)
	}
	select {
	case <-client.done:
	default:
		t.Fatal("stalled client was not force-closed")
	}
}

// A registered client whose writer never started still has to be closed.
func TestShutdownClosesClientsWithoutAWriter(t *testing.T) {
	server, viewer := net.Pipe()
	t.Cleanup(func() { _ = viewer.Close() })
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	client := &attachClient{
		conn: server, out: make(chan serverMessage, attachBufFrames),
		done: make(chan struct{}), flush: make(chan struct{}), version: protocolV2,
	}
	if err := h.registry.register(client, clientRegistration{
		requestedRole: roleController, hello: validTestHello(),
		size: terminalSize{cols: 80, rows: 24},
	}); err != nil {
		t.Fatal(err)
	}

	h.shutdownClients()

	select {
	case <-client.done:
	default:
		t.Fatal("client with no writer was left open")
	}
}
