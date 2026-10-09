package web

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"testing"
	"time"
)

func queuedStream(frames [][]byte) *Subscriber {
	sub := &Subscriber{ch: make(chan []byte, subscriberQueue), gone: make(chan struct{})}
	for _, frame := range frames {
		sub.ch <- frame
		sub.queued.Add(int64(len(frame)))
	}
	return sub
}

func TestStreamBatchPreservesQueuedFramesAndSharedBytes(t *testing.T) {
	frames := [][]byte{
		[]byte("event: delta\ndata: {\"seq\":1,\"text\":\"a\"}\n\n"),
		[]byte("event: item\ndata: {\"seq\":2}\n\n"),
		[]byte("event: ready\ndata: {\"seq\":3}\n\n"),
	}
	// Deliberate spare capacity: encoded frames may be shared by subscribers.
	first := make([]byte, len(frames[0]), 1024)
	copy(first, frames[0])
	for i := len(first); i < cap(first); i++ {
		first[:cap(first)][i] = 0x7f
	}
	frames[0] = first
	before := bytes.Clone(first[:cap(first)])
	sub := queuedStream(frames)
	server := &Server{heartbeat: time.Hour}
	var got []byte
	server.streamEvents(context.Background(), sub, func(frame []byte) bool {
		got = bytes.Clone(frame)
		return false
	})
	if !bytes.Equal(got, bytes.Join(frames, nil)) || sub.queued.Load() != 0 {
		t.Fatalf("first write = %q, queued=%d", got, sub.queued.Load())
	}
	if !bytes.Equal(before, first[:cap(first)]) {
		t.Fatal("batch mutated a shared frame's backing array")
	}
}

func TestStreamBatchBoundsAndQueueAccounting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames int
		bytes  int
		want   []int
	}{
		{"frame bound", 40, 128, []int{32 * 128, 8 * 128}},
		{"byte bound", 17, 4096, []int{64 << 10, 4096}},
		{"pending boundary", 3, 25 << 10, []int{50 << 10, 25 << 10}},
		{"large frames alone", 2, (64 << 10) + 1, []int{(64 << 10) + 1, (64 << 10) + 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := make([][]byte, tc.frames)
			for i := range frames {
				frames[i] = bytes.Repeat([]byte{byte(i)}, tc.bytes)
			}
			sub := queuedStream(frames)
			server := &Server{heartbeat: time.Hour}
			var got []byte
			var sizes []int
			server.streamEvents(context.Background(), sub, func(frame []byte) bool {
				got = append(got, frame...)
				sizes = append(sizes, len(frame))
				return len(got) < tc.frames*tc.bytes
			})
			if fmt.Sprint(sizes) != fmt.Sprint(tc.want) || !bytes.Equal(got, bytes.Join(frames, nil)) || sub.queued.Load() != 0 {
				t.Fatalf("sizes=%v want=%v queued=%d", sizes, tc.want, sub.queued.Load())
			}
		})
	}
}

func TestStreamBatchCancellationRetainsUnsentQueue(t *testing.T) {
	frames := make([][]byte, 40)
	for i := range frames {
		frames[i] = []byte("frame")
	}
	sub := queuedStream(frames)
	server := &Server{heartbeat: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	server.streamEvents(ctx, sub, func(frame []byte) bool {
		writes++
		cancel()
		return true
	})
	if writes != 1 || sub.queued.Load() != 8*int64(len(frames[0])) {
		t.Fatalf("writes=%d queued=%d", writes, sub.queued.Load())
	}
}

func TestStreamBatchPendingFrameRemainsAccountedOnCancellation(t *testing.T) {
	frame := bytes.Repeat([]byte("p"), 25<<10)
	sub := queuedStream([][]byte{frame, frame, frame})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := 0
	(&Server{heartbeat: time.Hour}).streamEvents(ctx, sub, func(batch []byte) bool {
		writes++
		if len(batch) != 50<<10 {
			t.Errorf("batch bytes=%d", len(batch))
		}
		cancel()
		return true
	})
	if writes != 1 || sub.queued.Load() != 25<<10 || len(sub.ch) != 0 {
		t.Fatalf("writes=%d queued=%d channel=%d", writes, sub.queued.Load(), len(sub.ch))
	}
}

func TestStreamBatchSingletonDoesNotWait(t *testing.T) {
	sub := queuedStream([][]byte{[]byte("single")})
	server := &Server{heartbeat: time.Hour}
	done := make(chan struct{})
	go func() {
		server.streamEvents(context.Background(), sub, func(frame []byte) bool {
			if string(frame) != "single" {
				t.Errorf("frame=%q", frame)
			}
			return false
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("singleton waited for a timer or another frame")
	}
}

func BenchmarkStreamQueuedFlush(b *testing.B) {
	for _, count := range []int{1, 64} {
		b.Run(fmt.Sprintf("frames=%d", count), func(b *testing.B) {
			frames := make([][]byte, count)
			for i := range frames {
				frames[i] = []byte(fmt.Sprintf("event: delta\ndata: {\"seq\":%d,\"session_id\":\"owned\",\"item_id\":\"reply\",\"text\":\"A small streamed fragment of the reply.\"}\n\n", i))
			}
			server := &Server{heartbeat: time.Hour}
			compressor, err := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
			if err != nil {
				b.Fatal(err)
			}
			defer compressor.Close()
			bytesPerRun := len(bytes.Join(frames, nil))
			var flushes int
			b.ReportAllocs()
			b.SetBytes(int64(bytesPerRun))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sub := queuedStream(frames)
				compressor.Reset(io.Discard)
				written := 0
				server.streamEvents(context.Background(), sub, func(frame []byte) bool {
					if _, err := compressor.Write(frame); err != nil {
						b.Fatal(err)
					}
					if err := compressor.Flush(); err != nil {
						b.Fatal(err)
					}
					flushes++
					written += len(frame)
					return written < bytesPerRun
				})
			}
			b.ReportMetric(float64(flushes)/float64(b.N), "flushes/op")
		})
	}
}
