package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type noticeCapture struct {
	mu       sync.Mutex
	payloads []pushPayload
}

func (c *noticeCapture) count() int         { c.mu.Lock(); defer c.mu.Unlock(); return len(c.payloads) }
func (c *noticeCapture) first() pushPayload { c.mu.Lock(); defer c.mu.Unlock(); return c.payloads[0] }

func testNoticeHome(t *testing.T) (*connectedNotices, *noticeSourceState, *noticeCapture) {
	t.Helper()
	m, _, _ := newTestManager(t)
	h := newConnectedNotices(m, "home", filepath.Join(t.TempDir(), connectedNoticeFile))
	t.Cleanup(h.close)
	ctx, cancel := context.WithCancel(h.ctx)
	state := &noticeSourceState{source: noticeSource{ID: "saved-b", InstanceID: "b", Label: "Work", Generation: 3}, ctx: ctx, cancel: cancel, deliveries: make(chan noticeDelivery, subscriberQueue)}
	h.sources[state.source.ID] = state
	sent := &noticeCapture{}
	h.send = func(ctx context.Context, payload pushPayload) {
		if ctx.Err() == nil {
			sent.mu.Lock()
			sent.payloads = append(sent.payloads, payload)
			sent.mu.Unlock()
		}
	}
	h.wg.Add(1)
	go h.deliver(state)
	return h, state, sent
}

func testSourceEvent(seq uint64) sourceNotice {
	return sourceNotice{InstanceID: "b", Epoch: "epoch-b", Seq: seq, Attention: 2, SessionID: "same-task", Kind: noticeFinished, Title: "Build finished", EmittedAt: time.Now().UTC()}
}

func TestConnectedNoticeForwarderDeduplicatesWithoutReexport(t *testing.T) {
	h, state, sent := testNoticeHome(t)
	reader, baseline, _ := h.m.subscribeNotices("")
	defer h.m.unsubscribeNotices(reader)
	for _, name := range []string{"snapshot", "notice", "notice"} {
		seq := uint64(1)
		if name == "snapshot" {
			seq = 0
		}
		if err := h.accept(state, name, testSourceEvent(seq)); err != nil {
			t.Fatal(err)
		}
	}
	waitUntil(t, "a forwarded notice", func() bool { return sent.count() == 1 })
	if sent.count() != 1 {
		t.Fatalf("pushes = %d", sent.count())
	}
	got := sent.first()
	if got.InstanceID != "b" || got.ConnectionID != "saved-b" || got.HomeID != "home" || got.Generation != 3 || got.Key != "b:epoch-b:1" || got.Badge != 2 {
		t.Fatalf("payload: %+v", got)
	}
	if got.Title != "Work: Build finished" {
		t.Fatalf("title = %q", got.Title)
	}
	select {
	case event := <-reader.ch:
		t.Fatalf("import was exported: %+v", event)
	default:
	}
	if h.m.noticeJournal.seq != baseline.Seq {
		t.Fatal("import altered local cursor")
	}
	data, err := osReadCursor(h.path)
	if err != nil || data["saved-b"].Seq != 1 {
		t.Fatalf("durable cursor: %+v %v", data, err)
	}
}

func osReadCursor(path string) (map[string]savedNoticeCursor, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cursors map[string]savedNoticeCursor
	err = json.Unmarshal(data, &cursors)
	return cursors, err
}

func TestConnectedNoticeViewingUsesSourceAndStreamLifetime(t *testing.T) {
	h, state, sent := testNoticeHome(t)
	reader := &connectedNoticeReader{page: "page", connection: "other-source", task: "same-task", ch: make(chan []byte, subscriberQueue), gone: make(chan struct{})}
	h.readers[reader] = struct{}{}
	if err := h.accept(state, "snapshot", testSourceEvent(0)); err != nil {
		t.Fatal(err)
	}
	if err := h.accept(state, "notice", testSourceEvent(1)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the other-source notice", func() bool { return sent.count() == 1 })
	h.mu.Lock()
	reader.connection = "saved-b"
	h.mu.Unlock()
	if err := h.accept(state, "notice", testSourceEvent(2)); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	delete(h.readers, reader)
	h.mu.Unlock()
	if err := h.accept(state, "notice", testSourceEvent(3)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "two scoped notices", func() bool { return sent.count() == 2 })
	if sent.count() != 2 {
		t.Fatalf("pushes = %d, want other-source + closed-page", sent.count())
	}
}

func TestConnectedNoticeDisableDropsCursorAndQueuedGeneration(t *testing.T) {
	h, state, sent := testNoticeHome(t)
	if err := h.accept(state, "snapshot", testSourceEvent(0)); err != nil {
		t.Fatal(err)
	}
	h.reconcile(nil)
	if err := h.accept(state, "notice", testSourceEvent(1)); err == nil {
		t.Fatal("removed generation accepted")
	}
	if sent.count() != 0 || len(h.cursors) != 0 {
		t.Fatal("removed source retained push or cursor")
	}
	if count, partial := h.badge(4); count != 4 || partial {
		t.Fatalf("disabled badge = %d, %v", count, partial)
	}
}

func TestConnectedNoticeHeadlessReaderAndRevocation(t *testing.T) {
	m, _, _ := newTestManager(t)
	h := newConnectedNotices(m, "home", "")
	defer h.close()
	delivered := make(chan pushPayload, 2)
	h.send = func(ctx context.Context, p pushPayload) {
		select {
		case delivered <- p:
		case <-ctx.Done():
		}
	}
	lifetime, revoke := context.WithCancel(context.Background())
	opened := make(chan struct{}, 1)
	source := noticeSource{ID: "saved-b", InstanceID: "b", Generation: 1, Label: "Work", Context: lifetime,
		Open: func(ctx context.Context, _ string) (*http.Response, error) {
			reader, writer := io.Pipe()
			go func() {
				defer func() { _ = writer.Close() }()
				for _, item := range []struct {
					name string
					seq  uint64
				}{{"snapshot", 0}, {"notice", 1}} {
					frame, _ := encodeFrame(item.name, testSourceEvent(item.seq))
					if _, err := writer.Write(frame); err != nil {
						return
					}
				}
				<-ctx.Done()
			}()
			opened <- struct{}{}
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: reader}, nil
		}}
	h.reconcile([]noticeSource{source})
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("daemon never opened source without a page")
	}
	select {
	case <-delivered:
	case <-time.After(time.Second):
		t.Fatal("headless notice was not pushed")
	}
	revoke()
	h.wg.Wait()
	if _, partial := h.badge(0); !partial {
		t.Fatal("revoked source still reported fresh coverage")
	}
}

func TestConnectedNoticeRejectsForeignIdentityAndOversizedFrame(t *testing.T) {
	for _, body := range []string{
		"event: snapshot\ndata: {\"instance_id\":\"wrong\",\"epoch\":\"e\"}\n\n",
		"event: snapshot\ndata: " + strings.Repeat("x", 16<<10) + "\n\n",
	} {
		h, state, sent := testNoticeHome(t)
		state.source.Open = func(context.Context, string) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		}
		if err := h.read(state, ""); err == nil {
			t.Fatal("invalid source accepted")
		}
		if sent.count() != 0 {
			t.Fatal("invalid source pushed")
		}
	}
}

func TestConnectedNoticeSlowPushDoesNotBlockSourceAndCancelStopsQueue(t *testing.T) {
	h, state, _ := testNoticeHome(t)
	started := make(chan struct{}, 1)
	h.send = func(ctx context.Context, _ pushPayload) {
		started <- struct{}{}
		<-ctx.Done()
	}
	if err := h.accept(state, "snapshot", testSourceEvent(0)); err != nil {
		t.Fatal(err)
	}
	if err := h.accept(state, "notice", testSourceEvent(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("push did not start")
	}
	consumed := make(chan error, 1)
	go func() { consumed <- h.accept(state, "notice", testSourceEvent(2)) }()
	select {
	case err := <-consumed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow push blocked the source cursor")
	}
	h.reconcile(nil)
	h.wg.Wait()
	select {
	case <-started:
		t.Fatal("canceled source sent a queued push")
	default:
	}
}

func TestConnectedNoticeRejectsBackwardsBaselineAndCursorGap(t *testing.T) {
	h, state, _ := testNoticeHome(t)
	if err := h.accept(state, "snapshot", testSourceEvent(4)); err != nil {
		t.Fatal(err)
	}
	if err := h.accept(state, "snapshot", testSourceEvent(3)); err == nil {
		t.Fatal("backwards baseline could repeat an alert")
	}
	if err := h.accept(state, "notice", testSourceEvent(6)); err == nil {
		t.Fatal("missing source event was accepted as continuous")
	}
}
