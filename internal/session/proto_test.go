package session

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
)

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
