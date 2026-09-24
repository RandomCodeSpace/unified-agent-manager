package web

import (
	"encoding/json"
	"net/http"
	"sync/atomic"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Subscriber bounds. A browser that cannot keep up is disconnected and
// resynchronizes from a fresh snapshot when it reconnects; the provider side
// never waits for it and its backlog never grows past these limits.
const (
	subscriberQueue = 256
	subscriberBytes = 32 << 20
)

// Subscriber is one event-stream connection. It only observes: dropping it
// never affects providers.
type Subscriber struct {
	session string
	ch      chan []byte
	queued  atomic.Int64
	gone    chan struct{}
	dropped bool // guarded by Manager.mu
}

// Frames returns the queue of encoded events for this subscriber.
func (s *Subscriber) Frames() <-chan []byte { return s.ch }

// Gone is closed when the subscriber was dropped (queue overflow or service
// shutdown).
func (s *Subscriber) Gone() <-chan struct{} { return s.gone }

// Sent records that one queued frame was written.
func (s *Subscriber) Sent(frame []byte) { s.queued.Add(-int64(len(frame))) }

type snapshotEvent struct {
	Seq      uint64           `json:"seq"`
	Projects []Project        `json:"projects"`
	Sessions []SessionSummary `json:"sessions"`
	Session  *SessionDetail   `json:"session"`
}

type sessionEvent struct {
	Seq     uint64         `json:"seq"`
	Session SessionSummary `json:"session"`
}

type sessionRemovedEvent struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
}

type projectEvent struct {
	Seq     uint64  `json:"seq"`
	Project Project `json:"project"`
}

type projectRemovedEvent struct {
	Seq       uint64 `json:"seq"`
	ProjectID string `json:"project_id"`
}

// itemEvent and deltaEvent carry agent_id for subagent items so a browser
// can route them without looking inside the item.
type itemEvent struct {
	Seq       uint64        `json:"seq"`
	SessionID string        `json:"session_id"`
	AgentID   string        `json:"agent_id,omitempty"`
	Item      agentapi.Item `json:"item"`
}

type deltaEvent struct {
	Seq       uint64            `json:"seq"`
	SessionID string            `json:"session_id"`
	AgentID   string            `json:"agent_id,omitempty"`
	ItemID    string            `json:"item_id"`
	Kind      agentapi.ItemKind `json:"kind"`
	Text      string            `json:"text"`
}

type subagentEvent struct {
	Seq       uint64            `json:"seq"`
	SessionID string            `json:"session_id"`
	Subagent  agentapi.Subagent `json:"subagent"`
}

type interactionEvent struct {
	Seq         uint64               `json:"seq"`
	SessionID   string               `json:"session_id"`
	Interaction agentapi.Interaction `json:"interaction"`
}

type submissionEvent struct {
	Seq        uint64     `json:"seq"`
	SessionID  string     `json:"session_id"`
	Submission Submission `json:"submission"`
}

type queueEvent struct {
	Seq       uint64         `json:"seq"`
	SessionID string         `json:"session_id"`
	Queue     []QueuedPrompt `json:"queue"`
	Paused    bool           `json:"paused"`
}

func encodeFrame(event string, payload any) ([]byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	frame := make([]byte, 0, len(event)+len(data)+16)
	frame = append(frame, "event: "...)
	frame = append(frame, event...)
	frame = append(frame, "\ndata: "...)
	frame = append(frame, data...)
	frame = append(frame, "\n\n"...)
	return frame, nil
}

// Subscribe registers a subscriber and returns it with its snapshot frame.
// Registration and snapshot happen under one lock, so the subscriber sees
// every later event exactly once and nothing between the two.
func (m *Manager) Subscribe(sessionID string) (*Subscriber, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, errShuttingDown
	}
	var detail *SessionDetail
	if sessionID != "" {
		s := m.sessions[sessionID]
		if s == nil {
			return nil, nil, newError(http.StatusNotFound, "session not found")
		}
		d := m.detailLocked(s)
		detail = &d
	}
	frame, err := encodeFrame("snapshot", snapshotEvent{Seq: m.seq, Projects: m.projectsLocked(), Sessions: m.summariesLocked(), Session: detail})
	if err != nil {
		return nil, nil, err
	}
	sub := &Subscriber{session: sessionID, ch: make(chan []byte, subscriberQueue), gone: make(chan struct{})}
	m.subs[sub] = struct{}{}
	return sub, frame, nil
}

// Unsubscribe removes a subscriber (its viewer left). It has no effect on
// the provider side.
func (m *Manager) Unsubscribe(sub *Subscriber) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropLocked(sub)
}

// DropSubscribers disconnects every event stream, for server shutdown.
func (m *Manager) DropSubscribers() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for sub := range m.subs {
		m.dropLocked(sub)
	}
}

func (m *Manager) dropLocked(sub *Subscriber) {
	if sub == nil || sub.dropped {
		return
	}
	sub.dropped = true
	delete(m.subs, sub)
	close(sub.gone)
}

// broadcastLocked assigns the next sequence number and queues the event for
// matching subscribers without blocking. sessionID "" reaches everyone;
// otherwise only subscribers of that session. The payload is built and
// encoded only when someone will receive it.
func (m *Manager) broadcastLocked(event, sessionID string, build func(seq uint64) any) {
	m.seq++
	targets := 0
	for sub := range m.subs {
		if sessionID == "" || sub.session == sessionID {
			targets++
		}
	}
	if targets == 0 {
		return
	}
	frame, err := encodeFrame(event, build(m.seq))
	if err != nil {
		log.Warn("encode web event failed", "event", event, "error", err)
		return
	}
	for sub := range m.subs {
		if sessionID != "" && sub.session != sessionID {
			continue
		}
		if sub.queued.Load()+int64(len(frame)) > subscriberBytes {
			m.dropLocked(sub)
			continue
		}
		select {
		case sub.ch <- frame:
			sub.queued.Add(int64(len(frame)))
		default:
			m.dropLocked(sub)
		}
	}
}
