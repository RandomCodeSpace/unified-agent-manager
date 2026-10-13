package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const turnChangesFile = "turn-changes.jsonl"

// TurnChanges is a recorded native preview of exactly this finalized owner
// turn, read without resuming the conversation or consulting current files.
type TurnChanges struct {
	TimingID string                    `json:"timing_id"`
	EndedAt  time.Time                 `json:"ended_at"`
	Counts   store.TurnChangeCounts    `json:"counts"`
	Files    []agentapi.NativeTurnFile `json:"files"`
}

func cloneTurnChanges(records []TurnChanges) []TurnChanges {
	out := slices.Clone(records)
	for i := range out {
		out[i].Files = slices.Clone(out[i].Files)
	}
	return out
}

// A read the provider answers busy, that lands while it runs a turn of its
// own (a background subagent still working when the owner's turn ended, then
// the turn delivering its result), or that a tool result landing while idle
// may postdate, is repeated after these waits. After the last, what the read
// says is kept.
var turnChangeRetryWaits = []time.Duration{2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second, time.Minute, time.Minute, time.Minute}

// turnChangeScope is the conversation a capture belongs to: any change ends it.
// idleEdits is the count of idle tool results the next read must postdate.
type turnChangeScope struct {
	conv      agentapi.Conversation
	convID    string
	gen       uint64
	history   uint64
	idleEdits uint64
}

func (m *Manager) kickTurnChangesLocked(s *webSession, timing TurnTiming) {
	reader, ok := s.conv.(agentapi.TurnChangeReader)
	if !ok || !s.nativeChanges || m.closed || s.removed || timing.UserItemID == "" || timing.EndedAt.IsZero() {
		return
	}
	scope := turnChangeScope{conv: s.conv, convID: s.convID, gen: s.gen, history: s.historyGen, idleEdits: s.idleToolEdits}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		for attempt := 0; ; attempt++ {
			if !m.captureTurnChanges(s, reader, timing, &scope, attempt < len(turnChangeRetryWaits)) {
				return
			}
			select {
			case <-m.ctx.Done():
				return
			case <-time.After(turnChangeRetryWaits[attempt]):
			}
		}
	}()
}

// captureTurnChanges reads the turn's native changes once and keeps them on
// its timing. True asks for another read later: the provider was busy, a turn
// it began on its own was running, or a tool result landed while idle
// (`retry` permitting).
func (m *Manager) captureTurnChanges(s *webSession, reader agentapi.TurnChangeReader, timing TurnTiming, scope *turnChangeScope, retry bool) bool {
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	release, err := m.readSlot(ctx)
	if err != nil {
		return false
	}
	defer release()
	facts, err := reader.TurnChanges(ctx, timing.UserItemID)
	if err != nil {
		facts = agentapi.NativeTurnChanges{Status: "unknown"}
	}
	rec := checkedTurnChanges(timing, facts)
	m.mu.Lock()
	defer m.mu.Unlock()
	i := slices.IndexFunc(s.turnTimings, func(t TurnTiming) bool {
		return t.ID == timing.ID && t.UserItemID == timing.UserItemID && t.EndedAt.Equal(timing.EndedAt)
	})
	// Even a native result that raced a later turn is never recomputed for this
	// older timing. A submission marks the session working before its SDK
	// events start a timing; its initial unknown remains the honest immutable
	// result. Later turns the provider began on its own (no owner prompt) are
	// this prompt's work: the provider's own proof decides whether the counts
	// are still this turn's.
	if m.closed || s.removed || m.sessions[s.id] != s || s.conv != scope.conv || s.convID != scope.convID || s.gen != scope.gen || s.historyGen != scope.history || i < 0 {
		return false
	}
	submitted := s.activeTiming < 0 && (s.base == StateWorking || s.base == StateStarting)
	if submitted || s.pendingTimingUser != "" || slices.ContainsFunc(s.turnTimings[i+1:], func(t TurnTiming) bool { return t.UserItemID != "" }) {
		return false
	}
	// A tool result that landed while no turn ran (the CLI hands over a
	// background subagent's result before its own turn begins) may postdate
	// this read: read again, or keep the initial unknown after the last read.
	if s.idleToolEdits != scope.idleEdits {
		scope.idleEdits = s.idleToolEdits
		return retry
	}
	if retry && (s.activeTiming >= 0 || rec.Counts.Status == "busy") {
		return true
	}
	before := m.summaryLocked(s)
	current := s.turnTimings[i]
	current.Changes = &rec.Counts
	s.turnTimings[i] = current
	if rec.Counts.Status == "available" {
		s.turnChanges = append(s.turnChanges, rec)
	}
	m.publishTurnTimingLocked(s, current)
	m.changedLocked(s, before)
	return false
}

// Check the optional provider seam before storing it. Counts are authoritative
// only when its bounded rows plus explicit omissions match its unique total.
func checkedTurnChanges(t TurnTiming, f agentapi.NativeTurnChanges) TurnChanges {
	unknown := TurnChanges{TimingID: t.ID, EndedAt: t.EndedAt, Counts: store.TurnChangeCounts{Status: "unknown"}, Files: []agentapi.NativeTurnFile{}}
	if f.Status == "busy" || f.Status == "unsupported" {
		unknown.Counts.Status = f.Status
		return unknown
	}
	if f.Status != "available" || f.EventID == "" || len(f.EventID) > 256 || len(f.Entries) > agentapi.MaxTurnChangeFiles || f.Files < 0 || f.Files > agentapi.MaxTurnChangeCount || f.Additions < 0 || f.Additions > agentapi.MaxTurnChangeCount || f.Deletions < 0 || f.Deletions > agentapi.MaxTurnChangeCount || f.Omitted < 0 || f.Files != int64(len(f.Entries))+f.Omitted {
		return unknown
	}
	bytes := 0
	seen := map[string]bool{}
	additions, deletions := int64(0), int64(0)
	files := make([]agentapi.NativeTurnFile, 0, len(f.Entries))
	for _, file := range f.Entries {
		bytes += len(file.Path)
		if !filepath.IsAbs(file.Path) || strings.ContainsRune(file.Path, 0) || len(file.Path) > 4096 || bytes > agentapi.MaxTurnChangePathBytes || len(file.Kind) > 32 || seen[file.Path] || file.Additions < 0 || file.Deletions < 0 || file.Additions > f.Additions-additions || file.Deletions > f.Deletions-deletions {
			return unknown
		}
		additions += file.Additions
		deletions += file.Deletions
		seen[file.Path] = true
		file.Path = strings.Clone(file.Path)
		file.Kind = strings.Clone(file.Kind)
		files = append(files, file)
	}
	if f.Omitted == 0 && (additions != f.Additions || deletions != f.Deletions) {
		return unknown
	}
	return TurnChanges{TimingID: t.ID, EndedAt: t.EndedAt, Counts: store.TurnChangeCounts{Status: "available", EventID: strings.Clone(f.EventID), Files: f.Files, Additions: f.Additions, Deletions: f.Deletions, Omitted: f.Omitted}, Files: files}
}

type keptTurnChanges struct {
	s       *webSession
	records []TurnChanges
	timings []TurnTiming
}

func (m *Manager) appendTurnChanges(k keptTurnChanges) {
	dir := m.taskUploadDir(k.s.id)
	var data []byte
	for _, r := range k.records {
		line, err := json.Marshal(r)
		if err == nil {
			data = append(append(data, line...), '\n')
		}
	}
	path := filepath.Join(dir, turnChangesFile)
	size, err := appendPrivate(m.uploadRoot(), dir, path, data)
	if err == nil && size > maxTurnTodosBytes {
		err = compactTurnRecords(path, k.timings, keptTurnTodosBytes, ".turn-changes-*")
	}
	if err != nil {
		log.Warn("keep native turn changes failed", "session", k.s.id, "error", err)
	}
	m.mu.Lock()
	k.s.turnChanges = slices.Delete(k.s.turnChanges, 0, len(k.records))
	removed := k.s.removed
	m.mu.Unlock()
	if removed {
		removeUploads(dir)
	}
}

func (m *Manager) TurnChanges(id, timingID string) (TurnChanges, error) {
	s, err := m.lookup(id)
	if err != nil {
		return TurnChanges{}, err
	}
	m.mu.Lock()
	i := slices.IndexFunc(s.turnTimings, func(t TurnTiming) bool { return t.ID == timingID })
	var timing TurnTiming
	if i >= 0 {
		timing = store.CloneTurnTimings(s.turnTimings[i : i+1])[0]
	}
	queued := slices.IndexFunc(s.turnChanges, func(r TurnChanges) bool { return r.TimingID == timingID })
	var record TurnChanges
	if queued >= 0 {
		record = cloneTurnChanges(s.turnChanges[queued : queued+1])[0]
	}
	path := filepath.Join(m.taskUploadDir(s.id), turnChangesFile)
	m.mu.Unlock()
	if i < 0 || timing.Changes == nil {
		return TurnChanges{}, newError(http.StatusNotFound, "no native changes were kept for that turn")
	}
	if timing.Changes.Status != "available" {
		return TurnChanges{TimingID: timingID, EndedAt: timing.EndedAt, Counts: *timing.Changes, Files: []agentapi.NativeTurnFile{}}, nil
	}
	if queued < 0 {
		var ok bool
		record, ok = readTurnChanges(path, timingID)
		if !ok {
			return TurnChanges{}, newError(http.StatusNotFound, "this turn's native file rows are no longer kept")
		}
	}
	f := agentapi.NativeTurnChanges{Status: record.Counts.Status, EventID: record.Counts.EventID, Files: record.Counts.Files, Additions: record.Counts.Additions, Deletions: record.Counts.Deletions, Omitted: record.Counts.Omitted, Entries: record.Files}
	checked := checkedTurnChanges(timing, f)
	if checked.Counts != *timing.Changes || !record.EndedAt.Equal(timing.EndedAt) {
		return TurnChanges{}, newError(http.StatusNotFound, "this turn's native file rows are unavailable")
	}
	return checked, nil
}

func readTurnChanges(path, id string) (TurnChanges, bool) {
	f, err := os.Open(path) // #nosec G304 -- UAM's own snapshot path.
	if err != nil {
		return TurnChanges{}, false
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 128<<10)
	var rec TurnChanges
	found := false
	for sc.Scan() {
		var r TurnChanges
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.TimingID == id {
			rec = r
			found = true
		}
	}
	return rec, found
}
func (s *Server) handleTurnChanges(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	rec, err := s.m.TurnChanges(r.PathValue("id"), r.PathValue("timing_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
