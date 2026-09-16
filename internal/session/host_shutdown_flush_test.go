package session

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestNaturalExitFlushesQueuedOutputFIFO(t *testing.T) {
	payloads := [][]byte{
		[]byte("first\r\n"),
		[]byte("second\r\n"),
		[]byte("agent final line\r\n"),
	}
	for _, test := range []struct {
		name    string
		version protocolVersion
	}{
		{name: "protocol_v1", version: protocolV1},
		{name: "protocol_v2", version: protocolV2},
	} {
		t.Run(test.name, func(t *testing.T) {
			server, viewer := net.Pipe()
			t.Cleanup(func() { _ = viewer.Close() })
			h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
			client := newShutdownFlushTestClient(server, test.version)
			for _, payload := range payloads {
				client.out <- serverMessage{kind: serverFramePTY, payload: payload}
			}

			client.requestFlush()
			go h.attachWriter(client)

			if err := viewer.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if test.version == protocolV1 {
				got, err := io.ReadAll(viewer)
				if err != nil {
					t.Fatalf("read raw output: %v", err)
				}
				if want := bytes.Join(payloads, nil); !bytes.Equal(got, want) {
					t.Fatalf("raw output = %q, want %q", got, want)
				}
			} else {
				reader := bufio.NewReader(viewer)
				for index, want := range payloads {
					kind, got, err := readFrame(reader)
					if err != nil {
						t.Fatalf("frame %d: %v", index, err)
					}
					if kind != serverFramePTY || !bytes.Equal(got, want) {
						t.Fatalf("frame %d = %d %q, want %d %q", index, kind, got, serverFramePTY, want)
					}
				}
			}
			waitForShutdownClientClose(t, client)
		})
	}
}

func TestNaturalExitFlushesResponsiveClientBesideStalledClient(t *testing.T) {
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	responsiveServer, responsiveViewer := net.Pipe()
	stalledServer, stalledViewer := net.Pipe()
	t.Cleanup(func() {
		_ = responsiveViewer.Close()
		_ = stalledViewer.Close()
	})
	responsive := newShutdownFlushTestClient(responsiveServer, protocolV2)
	stalled := newShutdownFlushTestClient(stalledServer, protocolV2)
	registerShutdownFlushTestClient(t, h, responsive)
	registerShutdownFlushTestClient(t, h, stalled)
	responsivePayload := []byte("responsive final output")
	responsive.out <- serverMessage{kind: serverFramePTY, payload: responsivePayload}
	stalled.out <- serverMessage{kind: serverFramePTY, payload: bytes.Repeat([]byte("x"), 4096)}

	started := time.Now()
	shutdownDone := make(chan struct{})
	go func() {
		h.shutdownClients()
		close(shutdownDone)
	}()

	waitForWholeCohortFlush(t, responsive, stalled)
	responsiveResult := make(chan struct {
		kind    byte
		payload []byte
		err     error
	}, 1)
	go func() {
		kind, payload, err := readFrame(bufio.NewReader(responsiveViewer))
		responsiveResult <- struct {
			kind    byte
			payload []byte
			err     error
		}{kind: kind, payload: payload, err: err}
	}()
	go h.attachWriter(responsive)
	go h.attachWriter(stalled)

	select {
	case result := <-responsiveResult:
		if result.err != nil {
			t.Fatalf("read responsive output: %v", result.err)
		}
		if result.kind != serverFramePTY || !bytes.Equal(result.payload, responsivePayload) {
			t.Fatalf("responsive output = %d %q, want %d %q", result.kind, result.payload, serverFramePTY, responsivePayload)
		}
	case <-time.After(time.Second):
		t.Fatal("responsive client did not receive final output")
	}
	select {
	case <-shutdownDone:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("one stalled client extended the shared shutdown deadline")
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("cohort shutdown took %s, want no more than 500ms", elapsed)
	}
	waitForShutdownClientClose(t, responsive)
	waitForShutdownClientClose(t, stalled)
}

func TestImmediateDropStillDiscardsQueuedOutput(t *testing.T) {
	server, viewer := net.Pipe()
	t.Cleanup(func() { _ = viewer.Close() })
	client := newShutdownFlushTestClient(server, protocolV2)
	client.out <- serverMessage{kind: serverFramePTY, payload: []byte("discarded")}

	started := time.Now()
	client.drop()
	if elapsed := time.Since(started); elapsed > 100*time.Millisecond {
		t.Fatalf("immediate drop took %s", elapsed)
	}

	if _, _, err := readFrame(bufio.NewReader(viewer)); err == nil {
		t.Fatal("dropped client received queued output")
	}
}

func TestNaturalExitClosesClientWithoutWriter(t *testing.T) {
	h := &host{name: "uam-fake-11112222", registry: newClientRegistry()}
	server, viewer := net.Pipe()
	t.Cleanup(func() { _ = viewer.Close() })
	client := newShutdownFlushTestClient(server, protocolV2)
	registerShutdownFlushTestClient(t, h, client)

	started := time.Now()
	h.shutdownClients()
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("client without writer extended shutdown to %s", elapsed)
	}
	waitForShutdownClientClose(t, client)
}

func registerShutdownFlushTestClient(t *testing.T, h *host, client *attachClient) {
	t.Helper()
	if err := h.registry.register(client, clientRegistration{
		requestedRole: roleController,
		hello:         validTestHello(),
		size:          terminalSize{cols: 80, rows: 24},
	}); err != nil {
		t.Fatalf("register client: %v", err)
	}
}

func waitForWholeCohortFlush(t *testing.T, clients ...*attachClient) {
	t.Helper()
	for _, client := range clients {
		select {
		case <-client.flush:
		case <-time.After(time.Second):
			t.Fatal("host did not signal the whole client cohort to flush")
		}
	}
	for _, client := range clients {
		select {
		case <-client.done:
			t.Fatal("host closed a client before signaling the whole cohort")
		default:
		}
	}
}

func newShutdownFlushTestClient(conn net.Conn, version protocolVersion) *attachClient {
	return &attachClient{
		conn: conn, out: make(chan serverMessage, attachBufFrames),
		done: make(chan struct{}), flush: make(chan struct{}), version: version,
	}
}

func waitForShutdownClientClose(t *testing.T, client *attachClient) {
	t.Helper()
	select {
	case <-client.done:
	case <-time.After(3 * time.Second):
		t.Fatal("client was not closed after shutdown flush")
	}
}
