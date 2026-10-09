package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const (
	maxAsideQuestionBytes = 16 << 10
	// asideTimeout bounds the wait for one aside answer.
	asideTimeout = 2 * time.Minute
)

// AskAside answers question from the open conversation's context with its
// current model. Nothing is stored: no transcript item, turn, todo or queue
// change, and no reopen. The wait derives from ctx, so a request the browser
// drops stops waiting; that does not prove the provider stopped the model
// call. s.op is released before the provider call, so Stop, Close and model
// changes never wait for an aside; closing the conversation cancels the wait,
// and an answer from a changed conversation or selection is dropped.
func (m *Manager) AskAside(ctx context.Context, id, question string) (*agentapi.AsideAnswer, error) {
	question = strings.TrimSpace(question)
	switch {
	case question == "":
		return nil, newError(http.StatusBadRequest, "question is required")
	case len(question) > maxAsideQuestionBytes || !utf8.ValidString(question):
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
	return out, nil
}

func (s *Server) handleAside(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Question string `json:"question"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := s.m.AskAside(r.Context(), r.PathValue("id"), req.Question)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
