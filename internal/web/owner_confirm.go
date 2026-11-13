package web

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// confirmCancel is the declining choice of every question uam asks itself.
const confirmCancel = "Cancel"

// askOwner puts a yes/no question from uam itself on the Thread taskID, on
// the tool call callID, and waits for the owner: true when they choose yes.
func (m *Manager) askOwner(ctx context.Context, taskID, callID, title, text, yes string) (bool, error) {
	answer, err := m.ask(ctx, taskID, "uam-confirm-", agentapi.Interaction{
		Title: title, ToolCallID: callID,
		Questions: []agentapi.Question{{Text: text, Choices: []string{yes, confirmCancel}}},
	})
	if err != nil {
		return false, err
	}
	return !answer.Reject && len(answer.Answers) == 1 && len(answer.Answers[0]) == 1 && answer.Answers[0][0] == yes, nil
}

// ask puts the question ix from uam itself on the Thread taskID, with an ID
// of prefix and a random part, and waits for the owner's answer, checked as
// any answer is. The question is the Thread's like a provider's (the
// composer or its card answers it), but its answer stays here. Stop, a
// rewind, closing the Thread and ctx's end withdraw it; then ask returns an
// error.
func (m *Manager) ask(ctx context.Context, taskID, prefix string, ix agentapi.Interaction) (agentapi.Answer, error) {
	m.mu.Lock()
	s := m.sessions[taskID]
	switch {
	case s == nil || s.removed:
		m.mu.Unlock()
		return agentapi.Answer{}, newError(http.StatusNotFound, msgSessionNotFound)
	case !m.infos[s.provider].Capabilities.Questions:
		m.mu.Unlock()
		return agentapi.Answer{}, errors.New("this thread's provider cannot show the owner a question, so uam cannot ask")
	}
	id := prefix + rand.Text()
	ix.ID, ix.Kind, ix.State, ix.Time = id, agentapi.InteractionQuestion, "", time.Time{}
	reply := make(chan agentapi.Answer, 1)
	before := m.summaryLocked(s)
	m.upsertInteractionLocked(s, ix)
	s.ixIdx[id].reply = reply
	m.changedLocked(s, before)
	m.mu.Unlock()
	select {
	case answer, ok := <-reply:
		if !ok {
			return agentapi.Answer{}, errors.New("the owner did not answer: the question was withdrawn")
		}
		return answer, nil
	case <-ctx.Done():
		m.mu.Lock()
		if ix := s.ixIdx[id]; ix != nil && ix.State == agentapi.InteractionPending {
			before := m.summaryLocked(s)
			m.expireLocked(s, ix, "the tool call ended")
			m.changedLocked(s, before)
		}
		m.mu.Unlock()
		return agentapi.Answer{}, ctx.Err()
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
