package web

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const connectedNoticeFile = "connected-notification-cursors.json"

type noticeSource struct {
	ID, InstanceID, Label string
	Generation            uint64
	Context               context.Context
	Open                  func(context.Context, string) (*http.Response, error)
}

type connectedNotice struct {
	sourceNotice
	ConnectionID string `json:"connection_id"`
	Generation   uint64 `json:"generation"`
	HomeID       string `json:"home_id"`
	Key          string `json:"key"`
}

type connectedAttention struct {
	ConnectionID string `json:"connection_id"`
	InstanceID   string `json:"instance_id"`
	Generation   uint64 `json:"generation"`
	Attention    int    `json:"attention"`
	Fresh        bool   `json:"fresh"`
}

type noticeDelivery struct {
	payload   pushPayload
	emittedAt time.Time
}

type noticeSourceState struct {
	source     noticeSource
	ctx        context.Context
	cancel     context.CancelFunc
	attention  int
	fresh      bool
	deliveries chan noticeDelivery
}

type savedNoticeCursor struct {
	InstanceID string    `json:"instance_id"`
	Generation uint64    `json:"generation"`
	Epoch      string    `json:"epoch"`
	Seq        uint64    `json:"seq"`
	SavedAt    time.Time `json:"saved_at"`
}

type connectedNoticeReader struct {
	page, connection, task string
	ch                     chan []byte
	gone                   chan struct{}
}

// connectedNotices belongs to the hosting daemon, not to browser streams.
// Its only network capability is the registry's per-source Open function.
type connectedNotices struct {
	m            *Manager
	homeID, path string
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	persistMu    sync.Mutex
	closed       bool
	sources      map[string]*noticeSourceState
	cursors      map[string]savedNoticeCursor
	readers      map[*connectedNoticeReader]struct{}
	send         func(context.Context, pushPayload)
}

func newConnectedNotices(m *Manager, homeID, path string) *connectedNotices {
	ctx, cancel := context.WithCancel(m.ctx)
	h := &connectedNotices{m: m, homeID: homeID, path: path, ctx: ctx, cancel: cancel,
		sources: make(map[string]*noticeSourceState), cursors: make(map[string]savedNoticeCursor), readers: make(map[*connectedNoticeReader]struct{}), send: m.push.send}
	if path != "" {
		file, err := os.Open(path) // #nosec G304 -- fixed application-owned cursor file, no credentials.
		var data []byte
		if err == nil {
			data, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
			_ = file.Close()
		}
		if err == nil && len(data) <= 1<<20 {
			if err := json.Unmarshal(data, &h.cursors); err != nil {
				h.cursors = make(map[string]savedNoticeCursor)
			}
		}
	}
	if h.cursors == nil {
		h.cursors = make(map[string]savedNoticeCursor)
	}
	m.mu.Lock()
	m.connectedNotices = h
	m.mu.Unlock()
	return h
}

// reconcile drops removed generations before starting their replacements.
// A disabled source's cursor is forgotten, so enabling starts at a new baseline.
func (h *connectedNotices) reconcile(sources []noticeSource) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	wanted := make(map[string]noticeSource)
	identities := make(map[string]bool)
	for _, source := range sources {
		if source.ID == "" || source.InstanceID == "" || source.InstanceID == h.homeID || source.Open == nil || identities[source.InstanceID] {
			continue
		}
		identities[source.InstanceID] = true
		wanted[source.ID] = source
	}
	for id, state := range h.sources {
		source, exists := wanted[id]
		if !exists || source.Generation != state.source.Generation || source.InstanceID != state.source.InstanceID {
			state.cancel()
			delete(h.sources, id)
			delete(h.cursors, id)
		}
	}
	for id := range h.cursors {
		if _, exists := wanted[id]; !exists {
			delete(h.cursors, id)
		}
	}
	for id, source := range wanted {
		if h.sources[id] != nil {
			h.sources[id].source.Label = source.Label
			continue
		}
		ctx, cancel := context.WithCancel(h.ctx)
		state := &noticeSourceState{source: source, ctx: ctx, cancel: cancel, deliveries: make(chan noticeDelivery, subscriberQueue)}
		h.sources[id] = state
		h.wg.Add(2)
		go h.run(state)
		go h.deliver(state)
	}
	h.publishAttentionLocked()
	h.mu.Unlock()
	if err := h.persist(); err != nil {
		log.Warn("connected notification cursor write failed", "error", err)
	}
}

func (h *connectedNotices) close() {
	if h == nil {
		return
	}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.cancel()
	for reader := range h.readers {
		close(reader.gone)
		delete(h.readers, reader)
	}
	h.mu.Unlock()
	h.wg.Wait()
	h.m.mu.Lock()
	if h.m.connectedNotices == h {
		h.m.connectedNotices = nil
	}
	h.m.mu.Unlock()
}

func (h *connectedNotices) run(state *noticeSourceState) {
	defer h.wg.Done()
	if state.source.Context != nil {
		stop := context.AfterFunc(state.source.Context, state.cancel)
		defer stop()
	}
	delay := time.Second
	for state.ctx.Err() == nil {
		h.mu.Lock()
		cursor := h.cursors[state.source.ID]
		h.mu.Unlock()
		resume := ""
		if cursor.InstanceID == state.source.InstanceID && cursor.Generation == state.source.Generation && time.Since(cursor.SavedAt) < pushTTL*time.Second {
			resume = noticeCursor(cursor.Epoch, cursor.Seq)
		}
		err := h.read(state, resume)
		h.mu.Lock()
		if h.sources[state.source.ID] == state {
			state.fresh = false
			h.publishAttentionLocked()
		}
		h.mu.Unlock()
		if state.ctx.Err() != nil {
			return
		}
		// Do not log response bodies or URLs, which can contain credentials.
		if err != nil {
			log.Debug("connected notification stream ended", "connection", state.source.ID)
		}
		timer := time.NewTimer(delay)
		select {
		case <-state.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay = min(delay*2, 30*time.Second)
		}
	}
}

func (h *connectedNotices) read(state *noticeSourceState, cursor string) error {
	ctx, cancel := context.WithCancel(state.ctx)
	defer cancel()
	idle := time.AfterFunc(45*time.Second, cancel)
	defer idle.Stop()
	response, err := state.source.Open(ctx, cursor)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = response.Body.Close() })
	defer stop()
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusConflict {
		state.cancel()
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		return errors.New("notice stream unavailable")
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 1024), 16<<10)
	name, baseline := "", false
	for scanner.Scan() {
		idle.Reset(45 * time.Second)
		line := scanner.Text()
		if line == "" {
			name = ""
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			name = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if name != "snapshot" && name != "notice" && name != "attention" {
			return errors.New("unexpected notice event")
		}
		if !baseline && name != "snapshot" {
			return errors.New("notice stream has no baseline")
		}
		var event sourceNotice
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event); err != nil {
			return errors.New("invalid notice event")
		}
		if event.InstanceID != state.source.InstanceID || event.Epoch == "" || len(event.Epoch) > 128 || event.Attention < 0 || event.Attention > 1_000_000 {
			return errors.New("notice source identity or count mismatch")
		}
		if name == "notice" && (event.SessionID == "" || len(event.SessionID) > 128 || !validNoticeKind(event.Kind)) {
			return errors.New("invalid task notice")
		}
		if name == "snapshot" {
			if baseline {
				return errors.New("repeated notice baseline")
			}
			baseline = true
		}
		if err := h.accept(state, name, event); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

func validNoticeKind(kind string) bool {
	return kind == noticeQuestion || kind == noticePermission || kind == noticeFailed || kind == noticeFinished
}

func (h *connectedNotices) accept(state *noticeSourceState, name string, event sourceNotice) error {
	h.mu.Lock()
	if h.closed || h.sources[state.source.ID] != state || state.ctx.Err() != nil {
		h.mu.Unlock()
		return context.Canceled
	}
	previous := h.cursors[state.source.ID]
	if previous.InstanceID == event.InstanceID && previous.Generation == state.source.Generation {
		if previous.Epoch == event.Epoch && event.Seq < previous.Seq && name == "snapshot" {
			h.mu.Unlock()
			return errors.New("notice baseline moved backwards")
		}
		if name != "snapshot" {
			if previous.Epoch != event.Epoch || event.Seq > previous.Seq+1 {
				h.mu.Unlock()
				return errors.New("notice stream lost its cursor")
			}
			if event.Seq <= previous.Seq {
				h.mu.Unlock()
				return nil
			}
		}
	}
	state.attention, state.fresh = event.Attention, true
	h.cursors[state.source.ID] = savedNoticeCursor{InstanceID: event.InstanceID, Generation: state.source.Generation, Epoch: event.Epoch, Seq: event.Seq, SavedAt: time.Now().UTC()}
	notice := connectedNotice{sourceNotice: event, ConnectionID: state.source.ID, Generation: state.source.Generation, HomeID: h.homeID,
		Key: fmt.Sprintf("%s:%s:%d", event.InstanceID, event.Epoch, event.Seq)}
	notice.Title = clipNotice(state.source.Label) + ": " + clipNotice(event.Title)
	visible := false
	for reader := range h.readers {
		if reader.connection == state.source.ID && reader.task == event.SessionID {
			visible = true
		}
	}
	h.publishAttentionLocked()
	h.mu.Unlock()
	// Save the consumed cursor before delivery. A crash can lose an alert, but
	// cannot reannounce a replayed alert on every daemon restart.
	if err := h.persist(); err != nil {
		return err
	}
	if name != "notice" || time.Since(event.EmittedAt) > pushTTL*time.Second || event.EmittedAt.After(time.Now().Add(time.Minute)) {
		return nil
	}
	h.mu.Lock()
	if h.sources[state.source.ID] != state || state.ctx.Err() != nil {
		h.mu.Unlock()
		return context.Canceled
	}
	h.publishLocked("notify", notice)
	h.mu.Unlock()
	if !visible {
		payload := pushPayload{Title: notice.Title, Task: event.SessionID, Kind: event.Kind, Key: notice.Key,
			HomeID: h.homeID, InstanceID: event.InstanceID, ConnectionID: state.source.ID, Generation: state.source.Generation}
		select {
		case state.deliveries <- noticeDelivery{payload, event.EmittedAt}:
		case <-state.ctx.Done():
			return context.Canceled
		default:
			return errors.New("notification delivery queue full")
		}
	}
	return nil
}

// Sending to a slow push service must not stall the upstream event reader.
// Each source has one bounded delivery queue, canceled with its generation.
func (h *connectedNotices) deliver(state *noticeSourceState) {
	defer h.wg.Done()
	for {
		select {
		case <-state.ctx.Done():
			return
		case delivery := <-state.deliveries:
			if state.ctx.Err() != nil {
				return
			}
			if time.Since(delivery.emittedAt) > pushTTL*time.Second {
				continue
			}
			h.mu.Lock()
			visible := h.sources[state.source.ID] != state
			for reader := range h.readers {
				if reader.connection == state.source.ID && reader.task == delivery.payload.Task {
					visible = true
				}
			}
			h.mu.Unlock()
			if visible {
				continue
			}
			h.m.mu.Lock()
			local := h.m.needsYouLocked()
			h.m.mu.Unlock()
			delivery.payload.Badge, delivery.payload.BadgePartial = h.badge(local)
			h.send(state.ctx, delivery.payload)
		}
	}
}

func (h *connectedNotices) badge(local int) (int, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	partial := false
	for _, source := range h.sources {
		if source.fresh {
			local += source.attention
		} else {
			partial = true
		}
	}
	return local, partial
}

func (h *connectedNotices) attentionLocked() []connectedAttention {
	attention := make([]connectedAttention, 0, len(h.sources))
	for id, source := range h.sources {
		attention = append(attention, connectedAttention{id, source.source.InstanceID, source.source.Generation, source.attention, source.fresh})
	}
	return attention
}

func (h *connectedNotices) publishAttentionLocked() {
	h.publishLocked("attention", h.attentionLocked())
}

func (h *connectedNotices) publishLocked(name string, payload any) {
	frame, err := encodeFrame(name, payload)
	if err != nil {
		return
	}
	for reader := range h.readers {
		select {
		case reader.ch <- frame:
		default:
			close(reader.gone)
			delete(h.readers, reader)
		}
	}
}

func (h *connectedNotices) persist() error {
	if h.path == "" {
		return nil
	}
	h.persistMu.Lock()
	defer h.persistMu.Unlock()
	h.mu.Lock()
	data, err := json.Marshal(h.cursors)
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(h.path, connectedNoticeFile+".tmp.*", data)
}
