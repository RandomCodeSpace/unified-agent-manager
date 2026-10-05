package web

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// sourceNotice is the bounded, local-only export. Imported notices never enter
// this journal or the ordinary task event stream.
type sourceNotice struct {
	InstanceID string    `json:"instance_id"`
	Epoch      string    `json:"epoch"`
	Seq        uint64    `json:"seq"`
	Attention  int       `json:"attention"`
	SessionID  string    `json:"session_id,omitempty"`
	Kind       string    `json:"kind,omitempty"`
	Title      string    `json:"title,omitempty"`
	EmittedAt  time.Time `json:"emitted_at"`
	Gap        bool      `json:"gap,omitempty"`
}

type noticeReader struct {
	ch   chan sourceNotice
	gone chan struct{}
}

// noticeJournal is guarded by Manager.mu and bounded independently of browsers.
type noticeJournal struct {
	seq       uint64
	attention int
	records   []sourceNotice
	readers   map[*noticeReader]struct{}
}

func (m *Manager) recordAttentionLocked() {
	n := m.needsYouLocked()
	if n != m.noticeJournal.attention {
		m.noticeJournal.attention = n
		m.appendNoticeLocked(sourceNotice{})
	}
}

func (m *Manager) recordNoticeLocked(task, kind, title string) {
	m.appendNoticeLocked(sourceNotice{SessionID: task, Kind: kind, Title: title})
}

func (m *Manager) appendNoticeLocked(event sourceNotice) {
	j := &m.noticeJournal
	j.seq++
	event.Epoch, event.Seq = m.epoch, j.seq
	event.Attention, event.EmittedAt = m.needsYouLocked(), m.now().UTC()
	j.attention = event.Attention
	j.records = append(j.records, event)
	for len(j.records) > subscriberQueue || len(j.records) > 0 && event.EmittedAt.Sub(j.records[0].EmittedAt) > pushTTL*time.Second {
		j.records = j.records[1:]
	}
	for reader := range j.readers {
		select {
		case reader.ch <- event:
		default:
			delete(j.readers, reader)
			close(reader.gone)
		}
	}
}

func noticeCursor(epoch string, seq uint64) string { return fmt.Sprintf("%s:%d", epoch, seq) }

// subscribeNotices establishes the snapshot and replay under the same lock as
// publication. A missing/expired cursor starts at now, without old alerts.
func (m *Manager) subscribeNotices(cursor string) (*noticeReader, sourceNotice, []sourceNotice) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := &m.noticeJournal
	baseline := sourceNotice{Epoch: m.epoch, Seq: j.seq, Attention: m.needsYouLocked(), EmittedAt: m.now().UTC()}
	var replay []sourceNotice
	if cursor != "" {
		epoch, raw, ok := strings.Cut(cursor, ":")
		seq, err := strconv.ParseUint(raw, 10, 64)
		valid := ok && err == nil && epoch == m.epoch && seq <= j.seq
		if valid && seq < j.seq {
			valid = len(j.records) > 0 && seq >= j.records[0].Seq-1 && m.now().Sub(j.records[0].EmittedAt) <= pushTTL*time.Second
		}
		if valid {
			baseline.Seq = seq
			for _, record := range j.records {
				if record.Seq > seq {
					replay = append(replay, record)
				}
			}
		} else {
			baseline.Gap = true
		}
	}
	reader := &noticeReader{ch: make(chan sourceNotice, subscriberQueue), gone: make(chan struct{})}
	if j.readers == nil {
		j.readers = make(map[*noticeReader]struct{})
	}
	j.readers[reader] = struct{}{}
	return reader, baseline, replay
}

func (m *Manager) unsubscribeNotices(reader *noticeReader) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.noticeJournal.readers[reader]; ok {
		delete(m.noticeJournal.readers, reader)
		close(reader.gone)
	}
}
