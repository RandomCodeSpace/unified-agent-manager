package web

import (
	"testing"
	"time"
)

func TestConnectedNoticeReplayAndGap(t *testing.T) {
	m, _, _ := newTestManager(t)
	appendNotice := func() {
		m.mu.Lock()
		m.recordNoticeLocked("same-task", noticeFinished, "Task finished")
		m.mu.Unlock()
	}
	appendNotice()
	reader, baseline, replay := m.subscribeNotices("")
	m.unsubscribeNotices(reader)
	if baseline.Seq != 1 || len(replay) != 0 || baseline.Gap {
		t.Fatalf("fresh attach: %+v %+v", baseline, replay)
	}
	appendNotice()
	reader, baseline, replay = m.subscribeNotices(noticeCursor(m.epoch, 1))
	m.unsubscribeNotices(reader)
	if baseline.Seq != 1 || len(replay) != 1 || replay[0].Seq != 2 {
		t.Fatalf("resume: %+v %+v", baseline, replay)
	}
	for range subscriberQueue {
		appendNotice()
	}
	reader, baseline, replay = m.subscribeNotices(noticeCursor(m.epoch, 1))
	m.unsubscribeNotices(reader)
	if !baseline.Gap || len(replay) != 0 || len(m.noticeJournal.records) != subscriberQueue {
		t.Fatalf("overflow: %+v %d", baseline, len(replay))
	}
	reader, baseline, replay = m.subscribeNotices("previous-epoch:1")
	m.unsubscribeNotices(reader)
	if !baseline.Gap || len(replay) != 0 {
		t.Fatal("epoch mismatch replayed old notices")
	}
}

func TestConnectedNoticeReaderIsBounded(t *testing.T) {
	m, _, _ := newTestManager(t)
	reader, _, _ := m.subscribeNotices("")
	m.mu.Lock()
	for range subscriberQueue + 1 {
		m.recordNoticeLocked("task", noticeQuestion, "Question")
	}
	m.mu.Unlock()
	select {
	case <-reader.gone:
	default:
		t.Fatal("slow source reader was not dropped")
	}
	if len(reader.ch) != subscriberQueue {
		t.Fatalf("queue = %d", len(reader.ch))
	}
	m.unsubscribeNotices(reader)
}

func TestConnectedNoticeExpiry(t *testing.T) {
	m, _, _ := newTestManager(t)
	now := time.Now()
	m.mu.Lock()
	m.now = func() time.Time { return now }
	m.recordNoticeLocked("task", noticeQuestion, "Question")
	now = now.Add(pushTTL*time.Second + time.Second)
	m.mu.Unlock()
	reader, baseline, replay := m.subscribeNotices(noticeCursor(m.epoch, 0))
	m.unsubscribeNotices(reader)
	if !baseline.Gap || len(replay) != 0 {
		t.Fatal("expired notice was replayed")
	}
}
