package session

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
)

func mustOwnedFramePayload(t *testing.T, generation uint64, payload []byte) []byte {
	t.Helper()
	framed, err := ownedFramePayload(generation, payload)
	if err != nil {
		t.Fatal(err)
	}
	return framed
}

func TestOwnedFramePayloadBoundsAllocation(t *testing.T) {
	maximum := make([]byte, maxFrameLen-ownershipEpochLen)
	framed, err := ownedFramePayload(1, maximum)
	if err != nil {
		t.Fatalf("maximum owned payload: %v", err)
	}
	if len(framed) != maxFrameLen {
		t.Fatalf("framed payload length = %d, want %d", len(framed), maxFrameLen)
	}

	oversized := make([]byte, maxFrameLen-ownershipEpochLen+1)
	if _, err := ownedFramePayload(1, oversized); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("oversized owned payload error = %v, want %v", err, errFrameTooLarge)
	}
	writer := newAttachFrameWriter(io.Discard, protocolV2, "client-1", 1)
	if err := writer.WriteFrame(frameStdin, oversized); !errors.Is(err, errFrameTooLarge) {
		t.Fatalf("oversized writer payload error = %v, want %v", err, errFrameTooLarge)
	}
}

func TestAttachV1CharacterizationHandshakeAndRawOutput(t *testing.T) {
	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	payload := []byte{'v', '1', ':', 0x00, 0xff, '\r', '\n'}
	serverErr := make(chan error, 1)
	go func() {
		br := bufio.NewReader(server)
		var req request
		if err := readJSONLine(br, &req); err != nil {
			serverErr <- err
			return
		}
		if req.Op != opAttach || req.Cols != 80 || req.Rows != 24 {
			serverErr <- errors.New("unexpected v1 attach request")
			return
		}
		if err := writeJSONLine(server, response{OK: true, Data: "v1-label"}); err != nil {
			serverErr <- err
			return
		}
		if err := writeAll(server, payload); err != nil {
			serverErr <- err
			return
		}
		serverErr <- server.Close()
	}()

	if err := writeJSONLine(client, request{Op: opAttach, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("write v1 handshake: %v", err)
	}
	br := bufio.NewReader(client)
	var resp response
	if err := readJSONLine(br, &resp); err != nil {
		t.Fatalf("read v1 handshake: %v", err)
	}
	if !resp.OK || resp.Data != "v1-label" {
		t.Fatalf("v1 response = %+v", resp)
	}
	got, err := io.ReadAll(br)
	if err != nil {
		t.Fatalf("read v1 raw output: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("v1 raw output = %x, want %x", got, payload)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("v1 fixture: %v", err)
	}
}

type shortWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (w *shortWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

func TestWriteFrameHandlesShortWrites(t *testing.T) {
	w := &shortWriter{max: 2}
	if err := writeFrame(w, frameStdin, []byte("payload")); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	kind, payload, err := readFrame(bytes.NewReader(w.buf.Bytes()))
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if kind != frameStdin || string(payload) != "payload" {
		t.Fatalf("frame = (%d, %q), want stdin payload", kind, payload)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) { return 0, nil }

func TestWriteFrameRejectsNoProgressWriter(t *testing.T) {
	if err := writeFrame(zeroWriter{}, frameDetach, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeFrame error = %v, want io.ErrShortWrite", err)
	}
}

func TestFrameWriterSerializesConcurrentFrames(t *testing.T) {
	underlying := &shortWriter{max: 1}
	w := newFrameWriter(underlying)
	const frames = 100

	var wg sync.WaitGroup
	for i := 0; i < frames; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := bytes.Repeat([]byte{byte(i)}, 32)
			if err := w.WriteFrame(frameStdin, payload); err != nil {
				t.Errorf("WriteFrame(%d): %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	r := bytes.NewReader(underlying.buf.Bytes())
	for i := 0; i < frames; i++ {
		kind, payload, err := readFrame(r)
		if err != nil {
			t.Fatalf("read frame %d: %v", i, err)
		}
		if kind != frameStdin || len(payload) != 32 {
			t.Fatalf("frame %d = kind %d len %d", i, kind, len(payload))
		}
		for _, b := range payload[1:] {
			if b != payload[0] {
				t.Fatalf("frame %d contains interleaved payload: %v", i, payload)
			}
		}
	}
	if _, _, err := readFrame(r); !errors.Is(err, io.EOF) {
		t.Fatalf("trailing read error = %v, want EOF", err)
	}
}

func TestAttachFrameWriterStampsV2OwnershipGeneration(t *testing.T) {
	var wire bytes.Buffer
	writer := newAttachFrameWriter(&wire, protocolV2, "client-1", 7)
	if err := writer.WriteFrame(frameStdin, []byte("before")); err != nil {
		t.Fatal(err)
	}
	if err := writer.ObserveControl([]byte(`{"type":"role","client_id":"client-1","generation":8}`)); err != nil {
		t.Fatalf("observe control: %v", err)
	}
	if err := writer.WriteFrame(frameResize, []byte{0, 100, 0, 30}); err != nil {
		t.Fatal(err)
	}
	if err := writer.ObserveControl([]byte(`{"type":"role","client_id":"client-2","generation":9}`)); err != nil {
		t.Fatalf("observe control: %v", err)
	}
	if err := writer.WriteFrame(frameStdin, []byte("after")); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		kind       byte
		generation uint64
		payload    []byte
	}{
		{kind: frameStdin, generation: 7, payload: []byte("before")},
		{kind: frameResize, generation: 8, payload: []byte{0, 100, 0, 30}},
		{kind: frameStdin, generation: 8, payload: []byte("after")},
	}
	for _, expected := range want {
		kind, payload, err := readFrame(&wire)
		if err != nil {
			t.Fatal(err)
		}
		generation, control, err := parseOwnedFramePayload(payload)
		if err != nil {
			t.Fatal(err)
		}
		if kind != expected.kind || generation != expected.generation || !bytes.Equal(control, expected.payload) {
			t.Fatalf("frame = (%d, %d, %q), want (%d, %d, %q)", kind, generation, control, expected.kind, expected.generation, expected.payload)
		}
	}
}

func TestAttachFrameWriterDoesNotRegressMatchingClientGeneration(t *testing.T) {
	var wire bytes.Buffer
	writer := newAttachFrameWriter(&wire, protocolV2, "client-1", 1)
	if err := writer.ObserveControl([]byte(`{"type":"role","client_id":"client-1","generation":3}`)); err != nil {
		t.Fatalf("observe control: %v", err)
	}
	if err := writer.ObserveControl([]byte(`{"type":"role","client_id":"client-1","generation":2}`)); err != nil {
		t.Fatalf("observe control: %v", err)
	}
	if err := writer.WriteFrame(frameStdin, []byte("stdin")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteFrame(frameResize, []byte{0, 100, 0, 30}); err != nil {
		t.Fatal(err)
	}

	for _, expectedKind := range []byte{frameStdin, frameResize} {
		kind, payload, err := readFrame(&wire)
		if err != nil {
			t.Fatal(err)
		}
		generation, _, err := parseOwnedFramePayload(payload)
		if err != nil {
			t.Fatal(err)
		}
		if kind != expectedKind || generation != 3 {
			t.Fatalf("frame = (%d, generation %d), want (%d, generation 3)", kind, generation, expectedKind)
		}
	}
}

func TestAttachFrameWriterPreservesV1ControlPayloads(t *testing.T) {
	var wire bytes.Buffer
	writer := newAttachFrameWriter(&wire, protocolV1, "", 7)
	if err := writer.WriteFrame(frameStdin, []byte("legacy")); err != nil {
		t.Fatal(err)
	}
	kind, payload, err := readFrame(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if kind != frameStdin || !bytes.Equal(payload, []byte("legacy")) {
		t.Fatalf("v1 frame = (%d, %q), want raw stdin", kind, payload)
	}
}

func FuzzFrameDecoding(f *testing.F) {
	for _, seed := range [][]byte{
		{frameDetach, 0, 0, 0, 0},
		{frameStdin, 0, 0, 0, 3, 'a', 'b', 'c'},
		{frameResize, 0, 0, 0, 4, 0, 80, 0, 24},
		{frameStdin, 0xff, 0xff, 0xff, 0xff},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		kind, payload, err := readFrame(bytes.NewReader(data))
		if err != nil {
			return
		}
		var roundTrip bytes.Buffer
		if err := writeFrame(&roundTrip, kind, payload); err != nil {
			t.Fatal(err)
		}
		gotKind, gotPayload, err := readFrame(&roundTrip)
		if err != nil || gotKind != kind || !bytes.Equal(gotPayload, payload) {
			t.Fatalf("frame round trip = (%d, %x, %v), want (%d, %x)", gotKind, gotPayload, err, kind, payload)
		}
	})
}
