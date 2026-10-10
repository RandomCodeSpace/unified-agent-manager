package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	maxAsideQuestionBytes = 16 << 10
	// asideTimeout bounds the wait for one aside answer.
	asideTimeout = 2 * time.Minute
	// asidesFile keeps a Task's answered asides beside its uploads, one
	// Aside a line, oldest first; none is ever dropped.
	asidesFile = "asides.jsonl"
	// maxAsideTurnID bounds the turn anchor kept; a longer item ID keeps none.
	maxAsideTurnID = 256
)

// Aside is one answered aside question, kept with its Task. It is never
// sent to the agent, counts as no turn and changes nothing else on the
// Task: not its activity, updated_at or unread mark.
type Aside struct {
	ID string `json:"id"`
	// Question is what the owner asked, without the earlier asides a
	// follow-up carries as context.
	Question   string    `json:"question"`
	Answer     string    `json:"answer"`
	Truncated  bool      `json:"truncated,omitempty"`
	AskedAt    time.Time `json:"asked_at"`
	AnsweredAt time.Time `json:"answered_at"`
	// Working is set when the agent was working on a turn when it was asked.
	Working bool `json:"working,omitempty"`
	// Turn is the turn the aside belongs to: the ID of the main
	// transcript's latest message the owner sent (not a steer) when it was
	// asked. Empty when there was none.
	Turn string `json:"turn,omitempty"`
}

// AsideReply is the aside route's answer and, once kept, its record.
type AsideReply struct {
	*agentapi.AsideAnswer
	Aside *Aside `json:"aside,omitempty"`
}

type asideEvent struct {
	Seq       uint64 `json:"seq"`
	SessionID string `json:"session_id"`
	Aside     Aside  `json:"aside"`
}

// AskAside answers question from the open conversation's context with its
// current model. The Task's transcript, turns, todos and queue do not
// change, and nothing reopens it; the answered aside is kept with the Task
// (keepAside) under typed, what the owner asked, or question when typed is
// empty. The wait derives from ctx, so a request the browser drops stops
// waiting and keeps nothing; that does not prove the provider stopped the
// model call. s.op is released before the provider call, so Stop, Close and
// model changes never wait for an aside; closing the conversation cancels
// the wait, and an answer from a changed conversation or selection is
// dropped.
func (m *Manager) AskAside(ctx context.Context, id, question, typed string) (*AsideReply, error) {
	question, typed = strings.TrimSpace(question), strings.TrimSpace(typed)
	if typed == "" {
		typed = question
	}
	switch {
	case question == "":
		return nil, newError(http.StatusBadRequest, "question is required")
	case len(question) > maxAsideQuestionBytes || !utf8.ValidString(question) || len(typed) > maxAsideQuestionBytes || !utf8.ValidString(typed):
		return nil, newError(http.StatusBadRequest, "question must be valid text of at most %d bytes", maxAsideQuestionBytes)
	}
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	// An aside is a model call on the account, as a send is.
	if err := m.refuseSignedOut(s); err != nil {
		return nil, err
	}
	s.op.Lock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	if err == nil && (m.closed || s.removed || s.conv == nil || s.state() == StateStarting) {
		err = newError(http.StatusConflict, "ask aside needs an already-open Task")
	}
	m.mu.Unlock()
	if err == nil {
		err = m.checkHolder(s)
	}
	if err != nil {
		s.op.Unlock()
		return nil, err
	}
	m.mu.Lock()
	asker, ok := s.conv.(agentapi.AsideAsker)
	switch {
	case m.closed || s.removed || m.sessions[id] != s || s.conv == nil || s.state() == StateStarting || s.stage != StageActive:
		err = newError(http.StatusConflict, "the Task's conversation is no longer open")
	case !ok:
		err = newError(http.StatusConflict, "this provider does not support aside questions")
	case s.asking:
		err = newError(http.StatusConflict, "an aside question is already waiting for its answer")
	}
	if err != nil {
		m.mu.Unlock()
		s.op.Unlock()
		return nil, err
	}
	s.asking = true
	conv, convID, gen := s.conv, s.convID, s.gen
	model, effort, tier := s.model, s.effort, s.contextSize
	rec := Aside{Question: typed, AskedAt: m.now(), Working: s.base == StateWorking, Turn: asideTurnLocked(s)}
	m.mu.Unlock()
	s.op.Unlock()

	ctx, cancel := context.WithTimeout(ctx, asideTimeout)
	defer cancel()
	out, err := asker.AskAside(ctx, question)
	m.mu.Lock()
	s.asking = false
	current := !m.closed && !s.removed && m.sessions[id] == s && s.conv == conv && s.convID == convID && s.gen == gen && s.stage == StageActive && s.model == model && s.effort == effort && s.contextSize == tier
	m.mu.Unlock()
	switch {
	case !current, errors.Is(err, agentapi.ErrClosed):
		return nil, newError(http.StatusConflict, "the Task or its model changed while the aside was answered; ask again")
	case errors.Is(err, agentapi.ErrUnsupported):
		return nil, newError(http.StatusConflict, "this runtime does not support aside questions")
	case errors.Is(err, context.DeadlineExceeded):
		return nil, newError(http.StatusGatewayTimeout, "the aside answer took too long")
	case err != nil:
		return nil, newError(http.StatusBadGateway, "could not answer the aside: %s", shortError(err))
	case out == nil:
		return nil, newError(http.StatusBadGateway, "the provider returned no aside answer")
	}
	rec.Answer, rec.Truncated, rec.AnsweredAt = clampText(out.Text, maxAnswerBytes), out.Truncated || len(out.Text) > maxAnswerBytes, m.now()
	if err := m.keepAside(s, &rec); err != nil {
		// The answer still reaches the asker; only this page shows it.
		log.Warn("keep the aside failed", "session", id, "error", err)
		return &AsideReply{AsideAnswer: out}, nil
	}
	return &AsideReply{AsideAnswer: out, Aside: &rec}, nil
}

// asideTurnLocked is the turn an aside asked now belongs to: the main
// transcript's latest message the owner sent, as the browser keys turns.
func asideTurnLocked(s *webSession) string {
	for _, it := range slices.Backward(s.items) {
		if it.AgentID == "" && it.Kind == agentapi.ItemUser && it.Delivery == "" {
			if len(it.ID) > maxAsideTurnID {
				return ""
			}
			return it.ID
		}
	}
	return ""
}

// keepAside appends a to the Task's asides file, owner-only and synced, and
// sends it to the Task's other pages. A Task deleted meanwhile keeps
// nothing.
func (m *Manager) keepAside(s *webSession, a *Aside) error {
	var err error
	if a.ID, err = newUUID(); err != nil {
		return err
	}
	line, err := json.Marshal(a)
	if err != nil {
		return err
	}
	dir := m.taskUploadDir(s.id)
	_, err = appendPrivate(m.uploadRoot(), dir, filepath.Join(dir, asidesFile), append(line, '\n'))
	m.mu.Lock()
	removed := s.removed
	if err == nil && !removed {
		kept := *a
		m.broadcastLocked("aside", s.id, func(seq uint64) any { return asideEvent{Seq: seq, SessionID: s.id, Aside: kept} })
	}
	m.mu.Unlock()
	if removed {
		removeUploads(dir)
		return errors.New("the Task was deleted")
	}
	return err
}

// Asides returns every aside kept with Task id, oldest first. It reads
// settled and archived Tasks alike, and changes nothing. A line a crash
// cut short, or one this version cannot read, is skipped.
func (m *Manager) Asides(id string) ([]Aside, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(m.taskUploadDir(s.id), asidesFile)) // #nosec G304 -- UAM's own directory and file name.
	out := []Aside{}
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for line := range bytes.Lines(data) {
		var a Aside
		if bytes.HasSuffix(line, []byte("\n")) && json.Unmarshal(line, &a) == nil && a.ID != "" {
			out = append(out, a)
		}
	}
	slices.SortStableFunc(out, func(a, b Aside) int { return a.AskedAt.Compare(b.AskedAt) })
	return out, nil
}

func (s *Server) handleAside(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
		// Typed is the question as the owner asked it, kept and shown;
		// Question may add earlier asides as context.
		Typed string `json:"typed"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := s.m.AskAside(r.Context(), r.PathValue("id"), req.Question, req.Typed)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleAsides(w http.ResponseWriter, r *http.Request) {
	out, err := s.m.Asides(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Asides []Aside `json:"asides"`
	}{out})
}
