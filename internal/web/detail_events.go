package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const maxDetailBodies = 8
const maxDetailWindowItems = 150
const maxDetailWindowBytes = 4 << 20

type bodyRef [2]string

type detailBarrier struct {
	Seq       uint64 `json:"seq"`
	Epoch     string `json:"epoch"`
	SessionID string `json:"session_id"`
}
type itemBody struct {
	detailBarrier
	AgentID string        `json:"agent_id"`
	Item    agentapi.Item `json:"item"`
}
type detailSnapshot struct {
	detailBarrier
	AgentID          string           `json:"agent_id,omitempty"`
	Subagent         *compactSubagent `json:"subagent,omitempty"`
	Items            []compactItem    `json:"items,omitempty"`
	Before           *string          `json:"before,omitempty"`
	HistoryTruncated bool             `json:"history_truncated,omitempty"`
	After            *string          `json:"after,omitempty"`
	Range            bool             `json:"range,omitempty"`
	RangeReset       bool             `json:"range_reset,omitempty"`
}
type detailPage struct {
	detailBarrier
	AgentID string        `json:"agent_id"`
	Items   []compactItem `json:"items"`
	Before  string        `json:"before"`
	After   string        `json:"after"`
	Scope   string        `json:"scope,omitempty"`
}

type initialDetailFrame struct {
	event   string
	payload any
}

func cloneBody(it agentapi.Item) agentapi.Item {
	if it.Tool != nil {
		t := *it.Tool
		it.Tool = &t
	}
	it.Images = slices.Clone(it.Images)
	it.Attachments = slices.Clone(it.Attachments)
	return it
}
func validDetailID(id string, empty bool) bool {
	return (empty || id != "") && len(id) <= 4096 && utf8.ValidString(id) && !strings.ContainsFunc(id, unicode.IsControl)
}
func parseDetailInterest(r *http.Request) (string, string, []bodyRef, error) {
	q := r.URL.Query()
	id, agent := q.Get("session"), q.Get("agent")
	if !validDetailID(id, false) || !validDetailID(agent, true) || len(q["session"]) != 1 || len(q["agent"]) > 1 || len(r.URL.RawQuery) > 65536 {
		return "", "", nil, newError(400, "invalid detail interest")
	}
	raw := q["item"]
	if len(raw) > maxDetailBodies {
		return "", "", nil, newError(400, "too many detail bodies")
	}
	refs := make([]bodyRef, 0, len(raw))
	seen := map[bodyRef]bool{}
	for _, encoded := range raw {
		var tuple []json.RawMessage
		var pair bodyRef
		if json.Unmarshal([]byte(encoded), &tuple) != nil || (len(tuple) != 2 && len(tuple) != 3) {
			return "", "", nil, newError(400, "invalid body reference")
		}
		if len(tuple) == 3 {
			var covered uint64
			if strings.TrimSpace(string(tuple[2])) == "null" || json.Unmarshal(tuple[2], &covered) != nil || covered > 1<<53-1 {
				return "", "", nil, newError(400, "invalid covered sequence")
			}
		}
		if strings.TrimSpace(string(tuple[0])) == "null" || json.Unmarshal(tuple[0], &pair[0]) != nil || json.Unmarshal(tuple[1], &pair[1]) != nil || !validDetailID(pair[0], true) || !validDetailID(pair[1], false) {
			return "", "", nil, newError(400, "invalid body reference")
		}
		ref := bodyRef{pair[0], pair[1]}
		if seen[ref] {
			return "", "", nil, newError(400, "duplicate body reference")
		}
		seen[ref] = true
		refs = append(refs, ref)
	}
	if agent == "" && len(refs) == 0 {
		return "", "", nil, newError(400, "detail interest is empty")
	}
	return id, agent, refs, nil
}

func (m *Manager) ItemBody(id, agent, item string) (itemBody, error) {
	if !validDetailID(agent, true) || !validDetailID(item, false) {
		return itemBody{}, newError(400, "invalid body reference")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return itemBody{}, newError(404, "session not found")
	}
	i, ok := s.itemIdx[itemKey(agent, item)]
	if !ok {
		return itemBody{}, newError(404, "item is no longer retained")
	}
	s.historyUsed = m.now()
	return itemBody{detailBarrier{m.seq, m.epoch, id}, agent, cloneBody(s.items[i])}, nil
}
func (s *Server) handleItemBody(w http.ResponseWriter, r *http.Request) {
	body, err := s.m.ItemBody(r.PathValue("id"), r.URL.Query().Get("agent_id"), r.PathValue("item_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func decodeAgentBoundary(cursor string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || !validDetailID(string(raw), false) {
		return "", newError(400, "invalid agent boundary")
	}
	return string(raw), nil
}

// Match the browser historyWindow accounting; wire escaping is not retained
// string memory. One indivisible oversized item still makes forward progress.
func compactWindowFits(items []agentapi.Item) bool {
	if len(items) > maxDetailWindowItems {
		return false
	}
	if len(items) <= 1 {
		return true
	}
	size := 0
	for _, it := range items {
		size += compactItemBytes(projectItem(it))
		if size > maxDetailWindowBytes {
			return false
		}
	}
	return true
}

// Keep these charges aligned with web/src/lib/historyWindow.ts. Count typed
// wire fields directly so admission does not serialize/copy large chat bodies.
func compactItemBytes(it compactItem) int {
	field := func(key string, bytes int) int { return 32 + 2*len(key) + bytes }
	str := func(key, value string, optional bool) int {
		if optional && value == "" {
			return 0
		}
		units := 0
		for _, r := range value {
			units++
			if r > 0xffff {
				units++
			}
		}
		return field(key, 24+2*units)
	}
	size := 64 + str("id", it.ID, false) + str("kind", string(it.Kind), false) + str("time", it.Time.Format(time.RFC3339Nano), false)
	size += str("text", it.Text, true) + str("agent_id", it.AgentID, true) + str("delivery", it.Delivery, true) + str("images_note", it.ImagesNote, true)
	if !it.EndedAt.IsZero() {
		size += str("ended_at", it.EndedAt.Format(time.RFC3339Nano), false)
	}
	if it.Compact != nil {
		size += field("compact", 64+field("has_reasoning", 4)+field("has_text", 4))
	}
	if t := it.Tool; t != nil {
		tool := 64 + str("name", t.Name, false) + str("status", string(t.Status), false)
		tool += str("title", t.Title, true) + str("input", t.Input, true) + str("output", t.Output, true) + str("display_arg", t.DisplayArg, true) + str("path", t.Path, true)
		tool += field("has_input", 4) + field("has_output", 4)
		if len(t.FilePaths) > 0 {
			list := 32
			for _, path := range t.FilePaths {
				list += 8 + str("", path, false) - field("", 0)
			}
			tool += field("file_paths", list)
		}
		size += field("tool", tool)
	}
	if len(it.Attachments) > 0 {
		list := 32
		for _, a := range it.Attachments {
			attachment := 64 + str("id", a.ID, true) + str("name", a.Name, false) + str("mime", a.MIME, false)
			if a.Size != 0 {
				attachment += field("size", 8)
			}
			if a.NotNative {
				attachment += field("not_native", 4)
			}
			list += 8 + attachment
		}
		size += field("attachments", list)
	}
	if len(it.Images) > 0 {
		list := 32
		for _, image := range it.Images {
			list += 8 + 64 + str("id", image.ID, false) + str("mime", image.MIME, false) + field("size", 8) + str("name", image.Name, true)
		}
		size += field("images", list)
	}
	return size
}

// Capture immutable resource values and register at one barrier. The handler
// serializes one resource at a time outside the lock; live frames queue after it.
func (m *Manager) subscribeDetail(id, agent string, refs []bodyRef) (*Subscriber, []initialDetailFrame, error) {
	return m.subscribeDetailRange(id, agent, refs, "")
}

func (m *Manager) subscribeDetailRange(id, agent string, refs []bodyRef, before string) (*Subscriber, []initialDetailFrame, error) {
	return m.subscribeDetailKnown(id, agent, refs, before, "", nil)
}

func (m *Manager) subscribeDetailKnown(id, agent string, refs []bodyRef, before, epoch string, known map[bodyRef]uint64) (*Subscriber, []initialDetailFrame, error) {
	return m.subscribeDetailWindow(id, agent, refs, before, "", epoch, known)
}

func (m *Manager) subscribeDetailWindow(id, agent string, refs []bodyRef, before, until, epoch string, known map[bodyRef]uint64) (*Subscriber, []initialDetailFrame, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, nil, errShuttingDown
	}
	s := m.sessions[id]
	if s == nil {
		return nil, nil, newError(404, "session not found")
	}
	barrier := detailBarrier{m.seq, m.epoch, id}
	snapshot := detailSnapshot{detailBarrier: barrier, AgentID: agent}
	var older []initialDetailFrame
	if agent != "" {
		sa := s.subIdx[agent]
		if sa == nil {
			return nil, nil, newError(404, "subagent not found")
		}
		projected := s.compactSubagent(*sa)
		snapshot.Subagent = &projected
		items := s.agentItems(agent)
		page := compactPage(items, len(items))
		snapshot.Items = page.Items
		snapshot.Before = &page.Before
		snapshot.After = &page.After
		snapshot.HistoryTruncated = s.truncated
		if until != "" {
			if before == "" {
				return nil, nil, newError(400, "agent_until requires agent_before")
			}
			lower, err := decodeAgentBoundary(before)
			if err != nil {
				return nil, nil, err
			}
			upper, err := decodeAgentBoundary(until)
			if err != nil {
				return nil, nil, err
			}
			snapshot.Range = true
			first := slices.IndexFunc(items, func(it agentapi.Item) bool { return it.ID == lower })
			last := slices.IndexFunc(items, func(it agentapi.Item) bool { return it.ID == upper })
			if first >= 0 && last >= 0 && first > last {
				return nil, nil, newError(400, "agent window bounds are reversed")
			}
			if first < 0 || last < 0 || !compactWindowFits(items[first:last+1]) {
				snapshot.RangeReset = true
				snapshot.HistoryTruncated = true
			} else {
				for end := last + 1; end > first; {
					part := compactPage(items[first:end], end-first)
					begin := end - len(part.Items)
					part.Before = ""
					part.After = ""
					if begin > 0 {
						part.Before = base64.RawURLEncoding.EncodeToString([]byte(items[begin].ID))
					}
					if end < len(items) {
						part.After = base64.RawURLEncoding.EncodeToString([]byte(items[end-1].ID))
					}
					older = append(older, initialDetailFrame{"detail_page", detailPage{detailBarrier: barrier, AgentID: agent, Items: part.Items, Before: part.Before, After: part.After, Scope: "window"}})
					end = begin
				}
			}
		} else if before != "" {
			boundary, err := decodeAgentBoundary(before)
			if err != nil {
				return nil, nil, err
			}
			start := slices.IndexFunc(items, func(it agentapi.Item) bool { return it.ID == boundary })
			if start < 0 {
				start = 0
				snapshot.HistoryTruncated = true
			}
			for end := len(items) - len(page.Items); end > start; {
				prior := compactPage(items, end)
				older = append(older, initialDetailFrame{"detail_page", detailPage{detailBarrier: barrier, AgentID: agent, Items: prior.Items, Before: prior.Before, After: prior.After}})
				end -= len(prior.Items)
			}
		}
	} else if before != "" || until != "" {
		return nil, nil, newError(400, "agent boundary requires an agent")
	}

	frames := []initialDetailFrame{{"detail_snapshot", snapshot}}
	frames = append(frames, older...)
	bodies := make(map[string]bool, len(refs))
	for _, ref := range refs {
		key := itemKey(ref[0], ref[1])
		i, ok := s.itemIdx[key]
		if !ok {
			frames = append(frames, initialDetailFrame{"body_unavailable", struct {
				detailBarrier
				AgentID string `json:"agent_id"`
				ItemID  string `json:"item_id"`
			}{barrier, ref[0], ref[1]}})
			continue
		}
		bodies[key] = true
		if covered, ok := known[ref]; ok && epoch == m.epoch && covered <= m.seq {
			if changed, tracked := s.itemSeq[key]; tracked && changed <= covered {
				frames = append(frames, initialDetailFrame{"body_current", struct {
					detailBarrier
					AgentID string `json:"agent_id"`
					ItemID  string `json:"item_id"`
				}{barrier, ref[0], ref[1]}})
				continue
			}
		}
		frames = append(frames, initialDetailFrame{"body", itemBody{barrier, ref[0], cloneBody(s.items[i])}})
	}
	frames = append(frames, initialDetailFrame{"detail_ready", barrier})
	sub := &Subscriber{session: id, compact: true, detail: true, agent: agent, bodies: bodies, ch: make(chan []byte, subscriberQueue), gone: make(chan struct{})}
	s.historyUsed = m.now()
	m.subs[sub] = struct{}{}
	return sub, frames, nil
}
func (s *Server) handleDetailEvents(w http.ResponseWriter, r *http.Request) {
	id, agent, refs, err := parseDetailInterest(r)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if len(r.URL.Query()["agent_before"]) > 1 || len(r.URL.Query()["agent_until"]) > 1 || (r.URL.Query().Has("agent_until") && r.URL.Query().Get("agent_until") == "") || len(r.URL.Query()["epoch"]) > 1 {
		writeFailure(w, newError(400, "invalid agent boundary"))
		return
	}
	known := map[bodyRef]uint64{}
	for i, encoded := range r.URL.Query()["item"] {
		var tuple []json.RawMessage
		_ = json.Unmarshal([]byte(encoded), &tuple)
		if len(tuple) == 3 {
			var seq uint64
			_ = json.Unmarshal(tuple[2], &seq)
			known[refs[i]] = seq
		}
	}
	sub, initial, err := s.m.subscribeDetailWindow(id, agent, refs, r.URL.Query().Get("agent_before"), r.URL.Query().Get("agent_until"), r.URL.Query().Get("epoch"), known)
	if err != nil {
		writeFailure(w, err)
		return
	}
	defer s.m.Unsubscribe(sub)
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(frame []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteWait))
		if _, err := w.Write(frame); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write([]byte("retry: 2000\n\n")) {
		return
	}
	for _, part := range initial {
		select {
		case <-sub.Gone():
			return
		case <-r.Context().Done():
			return
		default:
		}
		frame, err := encodeFrame(part.event, part.payload)
		if err != nil || len(frame) > subscriberBytes || !write(frame) {
			return
		}
	}
	ticker := time.NewTicker(s.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-sub.Gone():
			return
		case frame := <-sub.Frames():
			sub.Sent(frame)
			if !write(frame) {
				return
			}
		case <-ticker.C:
			if !write([]byte(": keep-alive\n\n")) {
				return
			}
		}
	}
}

func legacySubscriber(sub *Subscriber) bool { return !sub.compact && !sub.detail }
func compactMain(sub *Subscriber) bool      { return sub.compact && !sub.detail }
func compactAgent(agent string) func(*Subscriber) bool {
	return func(sub *Subscriber) bool {
		return sub.compact && ((!sub.detail && agent == "") || (sub.detail && sub.agent != "" && sub.agent == agent))
	}
}
func bodySubscriber(it agentapi.Item) func(*Subscriber) bool {
	key := itemKey(it.AgentID, it.ID)
	return func(sub *Subscriber) bool { return sub.detail && sub.bodies[key] }
}
func (m *Manager) publishCompactItemLocked(s *webSession, it agentapi.Item, appendItem bool) {
	m.broadcastFilteredLocked("item", s.id, compactAgent(it.AgentID), func(seq uint64) any {
		return struct {
			detailBarrier
			AgentID string      `json:"agent_id,omitempty"`
			Item    compactItem `json:"item"`
			Append  bool        `json:"append,omitempty"`
		}{detailBarrier{seq, m.epoch, s.id}, it.AgentID, projectItem(it), appendItem}
	})
}
func (m *Manager) publishBodyLocked(s *webSession, it agentapi.Item) {
	m.broadcastFilteredLocked("body", s.id, bodySubscriber(it), func(seq uint64) any { return itemBody{detailBarrier{seq, m.epoch, s.id}, it.AgentID, it} })
}
func (m *Manager) publishBodyDeltaLocked(s *webSession, it agentapi.Item, text string, output bool) {
	name := "body_delta"
	if output {
		name = "body_output"
	}
	m.broadcastFilteredLocked(name, s.id, bodySubscriber(it), func(seq uint64) any {
		return struct {
			detailBarrier
			AgentID string            `json:"agent_id"`
			ItemID  string            `json:"item_id"`
			Kind    agentapi.ItemKind `json:"kind"`
			Text    string            `json:"text"`
		}{detailBarrier{seq, m.epoch, s.id}, it.AgentID, it.ID, it.Kind, text}
	})
}
