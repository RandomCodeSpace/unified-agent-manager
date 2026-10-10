package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Rewind receipt states. pending is saved before the native request; a
// restart reads it as uncertain. applied and uncertain hold the Task until a
// reread: applied clears itself once the record is read again, uncertain only
// through ReconcileRewind.
const (
	rewindPending   = "pending"
	rewindApplied   = "applied"
	rewindUncertain = "uncertain"
	rewindDone      = "done"
)

// RewindPreview is the readonly effect of rewinding to before an owner
// message. Token binds the confirmation to this exact preview.
type RewindPreview struct {
	UserItemID     string                     `json:"user_item_id"`
	Token          string                     `json:"token"`
	Turns          int                        `json:"turns"`
	FilesAvailable bool                       `json:"files_available"`
	FilesReason    string                     `json:"files_reason,omitempty"`
	Files          agentapi.NativeTurnChanges `json:"files"`
}

// RewindRequest confirms one previewed rewind. Mode is conversation or
// conversation-and-files.
type RewindRequest struct {
	UserItemID string `json:"user_item_id"`
	Mode       string `json:"mode"`
	Token      string `json:"token"`
	RequestID  string `json:"request_id"`
}

// RewindReceipt is the recorded state and native result of one request.
type RewindReceipt struct {
	RequestID       string                 `json:"request_id"`
	UserItemID      string                 `json:"user_item_id"`
	Mode            string                 `json:"mode"`
	State           string                 `json:"state"`
	Result          *agentapi.RewindResult `json:"result,omitempty"`
	ReconcileFailed bool                   `json:"reconcile_failed,omitempty"`
	// Released means the owner cleared the hold with the outcome unknown.
	Released bool `json:"released,omitempty"`
}

// RewindStatus is the summary of a receipt that still holds the Task.
type RewindStatus struct {
	RequestID string `json:"request_id"`
	State     string `json:"state"`
	Mode      string `json:"mode"`
	Outcome   string `json:"outcome,omitempty"`
	// ReconcileFailed offers the explicit release.
	ReconcileFailed bool `json:"reconcile_failed,omitempty"`
}

var errRewindUnreconciled = &Error{Status: http.StatusConflict, Code: "rewind_unreconciled", Message: "the last rewind must be reconciled first: reread the conversation and check the files"}
var errRewindStale = &Error{Status: http.StatusConflict, Code: "rewind_stale", Message: "the conversation or its captured files changed; review the rewind again"}

func (s *webSession) rewindHoldLocked() error {
	if s.rewind != nil && s.rewind.State != rewindDone {
		return errRewindUnreconciled
	}
	return nil
}

func (s *webSession) rewindSummary() RewindStatus {
	r := s.rewind
	if r == nil || r.State == rewindDone {
		return RewindStatus{}
	}
	var res agentapi.RewindResult
	_ = json.Unmarshal(r.Result, &res)
	return RewindStatus{RequestID: r.RequestID, State: r.State, Mode: r.Mode, Outcome: res.Outcome, ReconcileFailed: r.ReconcileFailed}
}

func receiptOf(r *store.WebRewind) RewindReceipt {
	out := RewindReceipt{RequestID: r.RequestID, UserItemID: r.UserItemID, Mode: r.Mode, State: r.State, ReconcileFailed: r.ReconcileFailed, Released: r.Released}
	if len(r.Result) > 0 {
		var res agentapi.RewindResult
		if json.Unmarshal(r.Result, &res) == nil {
			out.Result = &res
		}
	}
	return out
}

// loadedRewind reads a request whose native result never landed as uncertain.
func loadedRewind(r *store.WebRewind) *store.WebRewind {
	r = r.Clone()
	if r != nil && r.State != rewindApplied && r.State != rewindUncertain && r.State != rewindDone {
		r.State = rewindUncertain
	}
	return r
}

func validRewindUser(user string) bool {
	return user != "" && len(user) <= 256 && !strings.ContainsAny(user, "\x00\r\n")
}

// rewindTargetLocked admits only an idle, writable Task whose managed
// conversation is already open. Rewind never resumes a conversation.
func (m *Manager) rewindTargetLocked(s *webSession) (agentapi.HistoryRewinder, uint64, error) {
	switch {
	case m.closed:
		return nil, 0, errShuttingDown
	case s.removed:
		return nil, 0, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if err := s.readOnlyLocked(); err != nil {
		return nil, 0, err
	}
	if err := s.rewindHoldLocked(); err != nil {
		return nil, 0, err
	}
	if s.conv == nil || s.opening != nil {
		return nil, 0, newError(http.StatusConflict, "rewind needs the task's open conversation; open the task first")
	}
	rewinder, ok := s.conv.(agentapi.HistoryRewinder)
	if !ok || !m.infos[s.provider].Capabilities.Rewind {
		return nil, 0, newError(http.StatusConflict, "this provider cannot rewind recorded history")
	}
	if err := s.settleableLocked(); err != nil {
		return nil, 0, err
	}
	if s.runsOrWaitsLocked() {
		return nil, 0, newError(http.StatusConflict, "background work is still running; wait for it to finish first")
	}
	return rewinder, s.gen, nil
}

func rewindReadError(err error) error {
	switch {
	case errors.Is(err, agentapi.ErrItemNotFound), errors.Is(err, agentapi.ErrConversationNotFound):
		return newError(http.StatusNotFound, "the exact recorded owner message is no longer available")
	case errors.Is(err, agentapi.ErrBusy):
		return newError(http.StatusConflict, "the conversation is still working; wait for it to settle")
	case errors.Is(err, agentapi.ErrUnsupported):
		return newError(http.StatusConflict, "this conversation cannot rewind its recorded history")
	case errors.Is(err, agentapi.ErrClosed):
		return newError(http.StatusConflict, "the conversation closed; open the task again")
	default:
		return newError(http.StatusBadGateway, "could not read the rewind preview: %s", shortError(err))
	}
}

func rewindToken(gen uint64, convID, user string, p agentapi.RewindPreview, disk []string) string {
	data, _ := json.Marshal(struct {
		Gen        uint64
		Conv, User string
		Preview    agentapi.RewindPreview
		Discarded  []string
		Disk       []string
	}{gen, convID, user, p, p.Discarded, disk})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// maxRewindDigestBytes bounds the content hashed per previewed file; a
// larger one is bound by its size and modification time only.
const maxRewindDigestBytes = 4 << 20

// rewindDiskState is the current state of each previewed file. The native
// preview reports only the recorded changes, so an outside edit after a
// preview leaves it unchanged; the token binds this state too.
func rewindDiskState(p agentapi.RewindPreview) []string {
	if !p.FilesAvailable {
		return nil
	}
	out := make([]string, 0, len(p.Files.Entries))
	for _, f := range p.Files.Entries {
		out = append(out, fileState(f.Path))
	}
	return out
}

func fileState(path string) string {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "absent"
	}
	if err != nil {
		return "unreadable"
	}
	state := fmt.Sprintf("%s %d %d", info.Mode(), info.Size(), info.ModTime().UnixNano())
	if !info.Mode().IsRegular() || info.Size() > maxRewindDigestBytes {
		return state
	}
	// O_NONBLOCK keeps a FIFO swapped in after the check from blocking.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) // #nosec G304 -- a path the provider previewed; only its digest is kept.
	if err != nil {
		return state + " unreadable"
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxRewindDigestBytes+1)); err != nil {
		return state + " unreadable"
	}
	return state + " " + hex.EncodeToString(h.Sum(nil))
}

// readRewind previews under s.op and rechecks that the same conversation is
// still current and idle afterwards.
func (m *Manager) readRewind(s *webSession, user string) (agentapi.HistoryRewinder, agentapi.RewindPreview, string, error) {
	m.mu.Lock()
	rewinder, gen, err := m.rewindTargetLocked(s)
	conv, convID := s.conv, s.convID
	m.mu.Unlock()
	if err != nil {
		return nil, agentapi.RewindPreview{}, "", err
	}
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	preview, err := rewinder.PreviewRewind(ctx, user)
	if err != nil {
		return nil, agentapi.RewindPreview{}, "", rewindReadError(err)
	}
	m.mu.Lock()
	_, now, err := m.rewindTargetLocked(s)
	changed := now != gen || s.conv != conv || s.convID != convID
	m.mu.Unlock()
	if err != nil {
		return nil, agentapi.RewindPreview{}, "", err
	}
	if changed {
		return nil, agentapi.RewindPreview{}, "", errRewindStale
	}
	return rewinder, preview, rewindToken(gen, convID, user, preview, rewindDiskState(preview)), nil
}

// PreviewRewind reads what rewinding to before the owner message user would
// remove and restore. It changes nothing.
func (m *Manager) PreviewRewind(id, user string) (RewindPreview, error) {
	if !validRewindUser(user) {
		return RewindPreview{}, newError(http.StatusBadRequest, "choose an exact recorded owner message")
	}
	s, err := m.lookup(id)
	if err != nil {
		return RewindPreview{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	_, p, token, err := m.readRewind(s, user)
	if err != nil {
		return RewindPreview{}, err
	}
	return RewindPreview{UserItemID: user, Token: token, Turns: p.Turns, FilesAvailable: p.FilesAvailable, FilesReason: p.FilesReason, Files: p.Files}, nil
}

// Rewind executes a confirmed preview once. The receipt is saved before the
// native request; a repeated request ID returns it without another request.
func (m *Manager) Rewind(id string, req RewindRequest) (RewindReceipt, error) {
	if !validRequestID(req.RequestID) {
		return RewindReceipt{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
	}
	if !validRewindUser(req.UserItemID) {
		return RewindReceipt{}, newError(http.StatusBadRequest, "choose an exact recorded owner message")
	}
	if req.Mode != agentapi.RewindConversation && req.Mode != agentapi.RewindConversationAndFiles {
		return RewindReceipt{}, newError(http.StatusBadRequest, "mode must be conversation or conversation-and-files")
	}
	s, err := m.lookup(id)
	if err != nil {
		return RewindReceipt{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	if out, found, err := m.recordedRewind(s, req); found {
		return out, err
	}
	// Like a send, never under another process holding the conversation. The
	// check releases s.op, so a request that finished meanwhile is replayed.
	holderErr := m.checkHolder(s)
	if out, found, err := m.recordedRewind(s, req); found {
		return out, err
	}
	if holderErr != nil {
		return RewindReceipt{}, holderErr
	}
	rewinder, preview, token, err := m.readRewind(s, req.UserItemID)
	if err != nil {
		return RewindReceipt{}, err
	}
	if token != req.Token {
		return RewindReceipt{}, errRewindStale
	}
	if req.Mode == agentapi.RewindConversationAndFiles && !preview.FilesAvailable {
		return RewindReceipt{}, newError(http.StatusConflict, "file restore is unavailable for this conversation; choose conversation only")
	}

	m.mu.Lock()
	_, gen, err := m.rewindTargetLocked(s)
	if err != nil {
		m.mu.Unlock()
		return RewindReceipt{}, err
	}
	conv, previous := s.conv, s.rewind
	gone := make(map[string]bool, len(preview.Discarded))
	for _, user := range preview.Discarded {
		gone[user] = true
	}
	var discarded []string
	for _, t := range s.turnTimings {
		if t.UserItemID != "" && gone[t.UserItemID] && !slices.Contains(discarded, t.UserItemID) {
			discarded = append(discarded, t.UserItemID)
		}
	}
	s.rewind = &store.WebRewind{RequestID: req.RequestID, UserItemID: req.UserItemID, UserEventID: preview.UserEventID, Mode: req.Mode, State: rewindPending, Discarded: discarded, CreatedAt: m.now()}
	m.dirty[s.id] = struct{}{}
	m.mu.Unlock()
	// The durable receipt precedes the native request.
	if err := m.flush(); err != nil {
		m.mu.Lock()
		s.rewind = previous
		m.dirty[s.id] = struct{}{}
		m.mu.Unlock()
		return RewindReceipt{}, newError(http.StatusServiceUnavailable, "could not save the rewind request; nothing was changed: %s", shortError(err))
	}
	m.mu.Lock()
	if s.removed || s.conv != conv || s.gen != gen {
		s.rewind = previous
		m.dirty[s.id] = struct{}{}
		m.mu.Unlock()
		_ = m.flush()
		return RewindReceipt{}, errRewindStale
	}
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	result, err := rewinder.Rewind(ctx, preview.UserEventID, req.Mode)
	state := rewindUncertain
	switch {
	case err == nil:
		switch result.Outcome {
		case "success", "checkpoint-cleanup-failed", "snapshot-prune-failed", "truncation-failed", "rollback-incomplete":
			state = rewindApplied
		default:
			state = rewindDone
		}
	case !errors.Is(err, agentapi.ErrRewindUncertain):
		// Refused before the native request was sent: nothing changed.
		m.mu.Lock()
		s.rewind = previous
		m.dirty[s.id] = struct{}{}
		if errors.Is(err, agentapi.ErrUnsupported) {
			before := m.summaryLocked(s)
			info := m.infos[s.provider]
			info.Capabilities.Rewind = false
			m.infos[s.provider] = info
			m.changedLocked(s, before)
		}
		m.mu.Unlock()
		if saveErr := m.flush(); saveErr != nil {
			log.Warn("release refused rewind receipt failed", "session", id, "error", saveErr)
		}
		return RewindReceipt{}, rewindReadError(err)
	default:
		log.Warn("native rewind result is unknown", "session", id, "error", err)
	}
	truncated := err == nil && (result.Outcome == "success" || result.Outcome == "checkpoint-cleanup-failed" || result.Outcome == "snapshot-prune-failed")
	m.mu.Lock()
	before := m.summaryLocked(s)
	rec := s.rewind.Clone()
	rec.State = state
	if err == nil {
		rec.Result, _ = json.Marshal(result)
	}
	if truncated {
		m.pruneRewoundLocked(s, rec.Discarded)
	}
	if state != rewindUncertain {
		rec.Discarded = nil
	}
	s.rewind = rec
	var closeConv agentapi.Conversation
	if state != rewindDone {
		closeConv = m.resetRewoundLocked(s)
	}
	m.dirty[s.id] = struct{}{}
	m.changedLocked(s, before)
	out := receiptOf(rec)
	m.mu.Unlock()
	if closeConv != nil {
		m.closeConversation(closeConv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist rewind result failed", "session", id, "error", err)
	}
	return out, nil
}

// recordedRewind returns the receipt of a request ID already used on s.
func (m *Manager) recordedRewind(s *webSession, req RewindRequest) (RewindReceipt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := s.rewind
	if r == nil || r.RequestID != req.RequestID {
		return RewindReceipt{}, false, nil
	}
	if r.UserItemID != req.UserItemID || r.Mode != req.Mode {
		return RewindReceipt{}, true, newError(http.StatusConflict, "this rewind request ID was already used for a different selection")
	}
	return receiptOf(r), true, nil
}

// pruneRewoundLocked drops the turn timings, and their pending snapshot rows,
// of the owner turns the provider confirmed it removed.
func (m *Manager) pruneRewoundLocked(s *webSession, discarded []string) {
	if len(discarded) == 0 {
		return
	}
	gone := make(map[string]bool, len(discarded))
	for _, user := range discarded {
		gone[user] = true
	}
	kept := map[string]bool{}
	s.turnTimings = slices.DeleteFunc(s.turnTimings, func(t TurnTiming) bool {
		if gone[t.UserItemID] {
			return true
		}
		kept[t.ID] = true
		return false
	})
	s.turnTodos = slices.DeleteFunc(s.turnTodos, func(r TurnTodos) bool { return !kept[r.TimingID] })
	s.turnChanges = slices.DeleteFunc(s.turnChanges, func(r TurnChanges) bool { return !kept[r.TimingID] })
	s.timingRevision++
}

// resetRewoundLocked detaches the conversation, forgets everything derived
// from the old record and starts an authoritative reread of it. Billed usage
// is kept: a rewind is no refund. The caller closes the conversation.
func (m *Manager) resetRewoundLocked(s *webSession) agentapi.Conversation {
	var conv agentapi.Conversation
	if s.conv != nil {
		conv = m.suspendLocked(s, false)
	}
	m.cancelHistoryLocked(s)
	m.dropHistoryLocked(s)
	s.interactions, s.ixIdx, s.ask = nil, map[string]*interaction{}, nil
	s.outcome, s.suggestions = "", nil
	m.clearDoneLocked(s)
	s.outcomeRun++
	s.turnActivity, s.todoBase, s.turnIntent = TurnActivity{}, TodoView{}, ""
	s.edits, s.editsKnown, s.turnStart, s.activity, s.diff = nil, false, time.Time{}, nil, nil
	s.invalidateNativeDiff()
	s.context = nil
	m.viewHistoryLocked(s)
	m.publishHistoryLocked(s)
	return conv
}

// rereadRewindLocked clears an applied hold once the provider's record of
// the conversation was read again after the rewind.
func (m *Manager) rereadRewindLocked(s *webSession) {
	if s.rewind == nil || s.rewind.State != rewindApplied {
		return
	}
	rec := s.rewind.Clone()
	rec.State, rec.Discarded = rewindDone, nil
	s.rewind = rec
	m.dirty[s.id] = struct{}{}
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// ReconcileRewind is the owner's explicit path out of an applied or uncertain
// rewind: it checks whether the boundary is still recorded, rereads the
// record and only then releases the Task. It never repeats the rewind.
func (m *Manager) ReconcileRewind(id, requestID string) (RewindReceipt, error) {
	out, err := m.reconcileRewind(id, requestID)
	if err != nil && !errors.Is(err, errNoRewindRequest) {
		m.reconcileFailed(id, requestID)
	}
	return out, err
}

var errNoRewindRequest = newError(http.StatusNotFound, "no such rewind request on this task")

// reconcileFailed records that the record could not establish the outcome,
// which offers the owner's explicit release.
func (m *Manager) reconcileFailed(id, requestID string) {
	s, err := m.lookup(id)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.rewind == nil || s.rewind.RequestID != requestID || s.rewind.State == rewindDone || s.rewind.ReconcileFailed {
		return
	}
	before := m.summaryLocked(s)
	rec := s.rewind.Clone()
	rec.ReconcileFailed = true
	s.rewind = rec
	m.dirty[s.id] = struct{}{}
	m.changedLocked(s, before)
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// ReleaseRewind clears a hold whose reconcile failed, without any native
// history request. The outcome stays unknown and is recorded so; nothing is
// pruned. The record is read again afterwards.
func (m *Manager) ReleaseRewind(id, requestID string) (RewindReceipt, error) {
	s, err := m.lookup(id)
	if err != nil {
		return RewindReceipt{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	r := s.rewind
	switch {
	case s.removed:
		m.mu.Unlock()
		return RewindReceipt{}, newError(http.StatusNotFound, msgSessionNotFound)
	case r == nil || r.RequestID != requestID:
		m.mu.Unlock()
		return RewindReceipt{}, errNoRewindRequest
	case r.State == rewindDone:
		defer m.mu.Unlock()
		return receiptOf(r), nil
	case !r.ReconcileFailed:
		m.mu.Unlock()
		return RewindReceipt{}, newError(http.StatusConflict, "reread the conversation first; release is offered only when that fails")
	}
	before := m.summaryLocked(s)
	rec := r.Clone()
	rec.State, rec.Released, rec.Discarded = rewindDone, true, nil
	s.rewind = rec
	conv := m.resetRewoundLocked(s)
	m.dirty[s.id] = struct{}{}
	m.changedLocked(s, before)
	out := receiptOf(rec)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	if err := m.flush(); err != nil {
		log.Warn("persist released rewind failed", "session", id, "error", err)
	}
	return out, nil
}

func (m *Manager) reconcileRewind(id, requestID string) (RewindReceipt, error) {
	s, err := m.lookup(id)
	if err != nil {
		return RewindReceipt{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	rec := s.rewind.Clone()
	prov, convID := m.providers[s.provider], s.convID
	m.mu.Unlock()
	if rec == nil || rec.RequestID != requestID {
		return RewindReceipt{}, errNoRewindRequest
	}
	if rec.State == rewindDone {
		return receiptOf(rec), nil
	}
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	truncated := false
	if rec.State == rewindUncertain && len(rec.Discarded) > 0 {
		forker, ok := prov.(agentapi.Forker)
		if !ok {
			return RewindReceipt{}, newError(http.StatusConflict, "this provider cannot check the recorded boundary")
		}
		_, err := forker.ReadForkBoundary(ctx, agentapi.ForkBoundaryRequest{ConversationID: convID, UserItemID: rec.UserItemID})
		switch {
		case errors.Is(err, agentapi.ErrItemNotFound):
			truncated = true
		case err != nil && !errors.Is(err, agentapi.ErrBusy):
			return RewindReceipt{}, newError(http.StatusBadGateway, "could not check the recorded conversation: %s", shortError(err))
		}
	}
	m.mu.Lock()
	if s.removed || s.rewind == nil || s.rewind.RequestID != requestID {
		m.mu.Unlock()
		return RewindReceipt{}, newError(http.StatusConflict, msgHistoryChanged)
	}
	before := m.summaryLocked(s)
	if truncated {
		m.pruneRewoundLocked(s, rec.Discarded)
	}
	conv := m.resetRewoundLocked(s)
	done, gen := s.historyDone, s.historyGen
	m.changedLocked(s, before)
	m.mu.Unlock()
	if conv != nil {
		m.closeConversation(conv)
	}
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return RewindReceipt{}, newError(http.StatusGatewayTimeout, "rereading the conversation took too long; try again")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.removed || s.historyGen != gen || s.history != HistoryLoaded || s.rewind == nil || s.rewind.RequestID != requestID {
		reason := "could not reread the conversation; try again"
		if s.history == HistoryUnavailable && s.historyReason != "" {
			reason = s.historyReason
		}
		return RewindReceipt{}, newError(http.StatusConflict, "%s", reason)
	}
	before = m.summaryLocked(s)
	out := s.rewind.Clone()
	out.State, out.Discarded = rewindDone, nil
	s.rewind = out
	m.dirty[s.id] = struct{}{}
	m.changedLocked(s, before)
	return receiptOf(out), nil
}

func (s *Server) handleRewindRelease(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string `json:"request_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := s.m.ReleaseRewind(r.PathValue("id"), req.RequestID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRewindPreview(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	p, err := s.m.PreviewRewind(r.PathValue("id"), r.URL.Query().Get("user_item_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleRewind(w http.ResponseWriter, r *http.Request) {
	var req RewindRequest
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := s.m.Rewind(r.PathValue("id"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleRewindReconcile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RequestID string `json:"request_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := s.m.ReconcileRewind(r.PathValue("id"), req.RequestID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
