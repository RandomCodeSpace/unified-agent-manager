package web

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// confirmCancel is the declining choice of every question uam asks itself.
const confirmCancel = "Cancel"

// askOwner puts a yes/no question from uam itself on the Thread taskID, on
// the tool call callID, and waits for the owner: true when they choose yes.
// The question is the Thread's like a provider's (the composer answers it),
// but its answer stays here. Stop, a rewind, closing the Thread and ctx's end
// withdraw it; then askOwner returns an error.
func (m *Manager) askOwner(ctx context.Context, taskID, callID, title, text, yes string) (bool, error) {
	m.mu.Lock()
	s := m.sessions[taskID]
	switch {
	case s == nil || s.removed:
		m.mu.Unlock()
		return false, newError(http.StatusNotFound, msgSessionNotFound)
	case !m.infos[s.provider].Capabilities.Questions:
		m.mu.Unlock()
		return false, errors.New("this thread's provider cannot show the owner a question, so uam cannot ask for the confirmation")
	}
	id := "uam-confirm-" + rand.Text()
	reply := make(chan agentapi.Answer, 1)
	before := m.summaryLocked(s)
	m.upsertInteractionLocked(s, agentapi.Interaction{
		ID: id, Kind: agentapi.InteractionQuestion, Title: title, ToolCallID: callID,
		Questions: []agentapi.Question{{Text: text, Choices: []string{yes, confirmCancel}}},
	})
	s.ixIdx[id].reply = reply
	m.changedLocked(s, before)
	m.mu.Unlock()
	select {
	case answer, ok := <-reply:
		if !ok {
			return false, errors.New("the owner did not answer: the question was withdrawn")
		}
		return !answer.Reject && len(answer.Answers) == 1 && len(answer.Answers[0]) == 1 && answer.Answers[0][0] == yes, nil
	case <-ctx.Done():
		m.mu.Lock()
		if ix := s.ixIdx[id]; ix != nil && ix.State == agentapi.InteractionPending {
			before := m.summaryLocked(s)
			m.expireLocked(s, ix, "the tool call ended")
			m.changedLocked(s, before)
		}
		m.mu.Unlock()
		return false, ctx.Err()
	}
}

// answerOwnLocked records the owner's answer to a question uam asked and
// hands it to the waiting askOwner. The caller holds m.mu and checked the
// answer.
func (m *Manager) answerOwnLocked(s *webSession, ix *interaction, answer agentapi.Answer) agentapi.Interaction {
	before := m.summaryLocked(s)
	ix.State, ix.Resolution = resolution(ix.Interaction, answer)
	ix.reply <- answer
	ix.reply = nil
	m.publishInteractionLocked(s, ix)
	m.changedLocked(s, before)
	return ix.Interaction
}

// expireOwnLocked withdraws the questions uam itself asked on s.
func (m *Manager) expireOwnLocked(s *webSession, reason string) {
	for _, ix := range s.interactions {
		if ix.reply != nil && ix.State == agentapi.InteractionPending {
			m.expireLocked(s, ix, reason)
		}
	}
}
