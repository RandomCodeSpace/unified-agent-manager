package session

import (
	"bufio"
	"bytes"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
)

type queuedFrameReader struct {
	reader  *bytes.Reader
	drained chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (reader *queuedFrameReader) Read(data []byte) (int, error) {
	if reader.reader.Len() > 0 {
		return reader.reader.Read(data)
	}
	reader.once.Do(func() { close(reader.drained) })
	<-reader.release
	return 0, io.EOF
}

func TestQueuedStandbyFramesCannotCrossPromotion(t *testing.T) {
	tests := []struct {
		name    string
		kind    byte
		payload func(uint64) []byte
		assert  func(*testing.T, *host, *os.File)
	}{
		{
			name: "stdin",
			kind: frameStdin,
			payload: func(generation uint64) []byte {
				return ownedFramePayload(generation, []byte("stale-input"))
			},
			assert: func(t *testing.T, _ *host, ptyOutput *os.File) {
				t.Helper()
				if err := ptyOutput.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, len("stale-input"))
				n, err := ptyOutput.Read(buffer)
				if n > 0 || err == nil {
					t.Fatalf("queued standby stdin reached PTY: %q, %v", buffer[:n], err)
				}
			},
		},
		{
			name: "resize",
			kind: frameResize,
			payload: func(generation uint64) []byte {
				return ownedFramePayload(generation, resizePayload(111, 33))
			},
			assert: func(t *testing.T, host *host, _ *os.File) {
				t.Helper()
				host.mu.Lock()
				cols, rows := host.term.Size()
				host.mu.Unlock()
				if cols != 90 || rows != 25 {
					t.Fatalf("queued standby resize changed controller size to %dx%d, want 90x25", cols, rows)
				}
			},
		},
		{
			name: "future stdin",
			kind: frameStdin,
			payload: func(generation uint64) []byte {
				return ownedFramePayload(generation+2, []byte("future-input"))
			},
			assert: func(t *testing.T, _ *host, ptyOutput *os.File) {
				t.Helper()
				if err := ptyOutput.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				buffer := make([]byte, len("future-input"))
				n, err := ptyOutput.Read(buffer)
				if n > 0 || err == nil {
					t.Fatalf("future ownership epoch reached PTY: %q, %v", buffer[:n], err)
				}
			},
		},
		{
			name: "future resize",
			kind: frameResize,
			payload: func(generation uint64) []byte {
				return ownedFramePayload(generation+2, resizePayload(112, 34))
			},
			assert: func(t *testing.T, host *host, _ *os.File) {
				t.Helper()
				host.mu.Lock()
				cols, rows := host.term.Size()
				host.mu.Unlock()
				if cols != 90 || rows != 25 {
					t.Fatalf("future ownership epoch changed controller size to %dx%d, want 90x25", cols, rows)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := newClientRegistry()
			controller := registerTestClient(t, registry, roleController, terminalSize{cols: 80, rows: 24})
			standby := registerTestClient(t, registry, roleController, terminalSize{cols: 90, rows: 25})
			staleGeneration := standby.generation
			var frame bytes.Buffer
			if err := writeFrame(&frame, test.kind, test.payload(staleGeneration)); err != nil {
				t.Fatal(err)
			}
			if changes := registry.transfer(controller); len(changes) != 2 {
				t.Fatalf("transfer changes = %d, want 2", len(changes))
			}
			if standby.generation == staleGeneration {
				t.Fatal("promotion did not advance the ownership generation")
			}

			ptyOutput, ptyInput, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = ptyOutput.Close()
				_ = ptyInput.Close()
			})
			host := &host{registry: registry, term: vterm.New(80, 24, historyLines), ptmx: ptyInput}
			host.term.Resize(90, 25)
			release := make(chan struct{})
			queued := &queuedFrameReader{reader: bytes.NewReader(frame.Bytes()), drained: make(chan struct{}), release: release}
			done := make(chan struct{})
			go func() {
				host.attachReader(standby, bufio.NewReader(queued))
				close(done)
			}()
			t.Cleanup(func() {
				close(release)
				<-done
			})

			select {
			case <-queued.drained:
			case <-time.After(time.Second):
				t.Fatal("queued frame was not processed")
			}
			test.assert(t, host, ptyOutput)
		})
	}
}
