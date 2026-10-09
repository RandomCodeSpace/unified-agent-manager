package web

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func BenchmarkDeltaTextAccumulation(b *testing.B) {
	for _, tc := range []struct{ total, chunk int }{
		{256 << 10, 64},
		{4 << 20, 1024},
		{4 << 20, 16 << 10},
	} {
		b.Run(fmt.Sprintf("bytes=%d/chunk=%d", tc.total, tc.chunk), func(b *testing.B) {
			fragment := strings.Repeat("x", tc.chunk)
			m := &Manager{now: time.Now}
			b.ReportAllocs()
			b.SetBytes(int64(tc.total))
			for i := 0; i < b.N; i++ {
				s := &webSession{id: "owned", itemIdx: map[string]int{}}
				for sent := 0; sent < tc.total; sent += len(fragment) {
					m.applyDeltaLocked(s, agentapi.Delta{ItemID: "reply", Kind: agentapi.ItemAssistant, Text: fragment})
				}
				if len(s.items) != 1 || len(s.items[0].Text) != tc.total || s.itemBytes != itemSize(s.items[0]) {
					b.Fatalf("items=%d bytes=%d", len(s.items), s.itemBytes)
				}
			}
		})
	}
}

func BenchmarkDeltaTextHeapRelease(b *testing.B) {
	const total, chunk = 3 << 20, 1024
	fragment := strings.Repeat("x", chunk)
	var liveHeap, releasedHeap, capacity int64
	for i := 0; i < b.N; i++ {
		m, s := textSession()
		var before, live, released runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		for sent := 0; sent < total; sent += chunk {
			appendText(m, s, "", "reply", agentapi.ItemAssistant, fragment)
		}
		for _, buffer := range s.textBuffers {
			capacity += int64(buffer.Cap())
		}
		runtime.GC()
		runtime.ReadMemStats(&live)
		m.dropHistoryLocked(s)
		runtime.GC()
		runtime.ReadMemStats(&released)
		runtime.KeepAlive(s)
		liveHeap += int64(live.HeapAlloc) - int64(before.HeapAlloc)
		releasedHeap += int64(released.HeapAlloc) - int64(before.HeapAlloc)
	}
	b.ReportMetric(float64(capacity)/float64(b.N), "buffer-cap-B/op")
	b.ReportMetric(float64(liveHeap)/float64(b.N), "live-heap-B/op")
	b.ReportMetric(float64(releasedHeap)/float64(b.N), "released-heap-B/op")
}
