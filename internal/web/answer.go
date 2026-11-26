package web

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Answer forwards the user's answer to a pending interaction. The first
// answer wins. The service answers on the user's behalf only for permission
// requests of a yolo Task or approved by an assisted review, and never for
// questions.
func (m *Manager) Answer(id, interactionID string, answer agentapi.Answer) (agentapi.Interaction, error) {
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	ix := s.ixIdx[interactionID]
	if ix == nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, newError(http.StatusNotFound, "interaction not found")
	}
	if err := interactionOpen(ix); err != nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, err
	}
	if err := validateAnswer(ix.Interaction, answer); err != nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, err
	}
	images, err := answerImagesLocked(s, ix.Interaction, answer.Attachments)
	if err != nil {
		m.mu.Unlock()
		return agentapi.Interaction{}, err
	}
	if ix.reply != nil {
		defer m.mu.Unlock()
		return m.answerOwnLocked(s, ix, answer), nil
	}
	conv := s.conv
	if conv == nil {
		before := m.summaryLocked(s)
		m.expireLocked(s, ix, msgConversationNotOpen)
		m.changedLocked(s, before)
		m.mu.Unlock()
		return agentapi.Interaction{}, newError(http.StatusGone, msgInteractionExpired)
	}
	ix.answering = true
	m.mu.Unlock()
	if len(images) > 0 {
		if answer.Images, err = m.readBlobs(s.id, images); err != nil {
			m.mu.Lock()
			ix.answering = false
			m.mu.Unlock()
			return agentapi.Interaction{}, err
		}
		m.markUsed(s, images)
	}
	return m.respond(s, ix, conv, interactionID, answer)
}

// answerImagesLocked resolves an answer's image uploads against the limits
// the question advertised: each an upload of this Thread, of an allowed type
// and size, and no more of them than the question and a prompt take.
func answerImagesLocked(s *webSession, ix agentapi.Interaction, ids []string) ([]*upload, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	limits := ix.Questions[0].Images // validateAnswer checked it is set
	if n := min(limits.MaxCount, maxAttachments); len(ids) > n {
		return nil, newError(http.StatusBadRequest, "an answer can carry at most %d images", n)
	}
	out := make([]*upload, 0, len(ids))
	for _, id := range ids {
		u := s.uploads[id]
		switch {
		case u == nil || u.Tool:
			return nil, newError(http.StatusBadRequest, "attachment %q is not an upload of this thread", clipRunes(displaytext.Sanitize(id), maxDetailRunes))
		case slices.Contains(out, u):
			return nil, newError(http.StatusBadRequest, "attachment %s is listed twice", u.Name)
		case !slices.Contains(limits.MIMETypes, u.MIME):
			return nil, newError(http.StatusBadRequest, "this question does not take %s files", u.MIME)
		case u.Size > limits.MaxBytes:
			return nil, newError(http.StatusBadRequest, "%s is larger than this question takes (%d bytes)", u.Name, limits.MaxBytes)
		}
		out = append(out, u)
	}
	return out, nil
}

// respond sends answer to an interaction the caller claimed by setting
// ix.answering, and records the outcome. While it is claimed, every other
// answer is refused, so the first answer wins.
func (m *Manager) respond(s *webSession, ix *interaction, conv agentapi.Conversation, interactionID string, answer agentapi.Answer) (agentapi.Interaction, error) {
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	err := conv.Respond(ctx, interactionID, answer)
	cancel()

	m.mu.Lock()
	defer m.mu.Unlock()
	before := m.summaryLocked(s)
	ix.answering = false
	wasYolo := ix.yolo
	if err != nil {
		// Not answered: the request is the user's again.
		ix.yolo, ix.assisted = false, false
	}
	switch {
	case err == nil:
		m.countAnswer(ix.Interaction, answer)
		if ix.Kind == agentapi.InteractionPermission && answer.Decision == "approve_session" {
			s.fuses = nil
		}
		if ix.State == agentapi.InteractionPending {
			ix.State, ix.Resolution = resolution(ix.Interaction, answer)
			ix.Plan = clampPlanReview(ix.Plan, false)
			if ix.yolo {
				ix.Resolution = autoResolution(ix)
			}
			m.publishInteractionLocked(s, ix)
		}
		if !wasYolo {
			m.childAnswerLocked(s, ix, answer)
		}
		m.changedLocked(s, before)
		return ix.Interaction, nil
	case errors.Is(err, agentapi.ErrInteractionGone), errors.Is(err, agentapi.ErrClosed):
		if ix.State == agentapi.InteractionPending {
			m.expireLocked(s, ix, "the provider no longer waits for this answer")
		}
		m.changedLocked(s, before)
		return agentapi.Interaction{}, newError(http.StatusGone, msgInteractionExpired)
	default:
		if wasYolo && ix.State == agentapi.InteractionPending {
			m.publishInteractionLocked(s, ix) // the browser learns it waits for the user
		}
		m.changedLocked(s, before)
		log.Warn("web interaction answer failed", "session", s.id, "error", err)
		return agentapi.Interaction{}, newError(http.StatusBadGateway, "answer failed: %s", shortError(err))
	}
}

func interactionOpen(ix *interaction) error {
	switch ix.State {
	case agentapi.InteractionExpired:
		return newError(http.StatusGone, msgInteractionExpired)
	case agentapi.InteractionPending:
		if ix.answering {
			return newError(http.StatusConflict, "the interaction is already being answered")
		}
		return nil
	default:
		return newError(http.StatusConflict, "the interaction was already resolved")
	}
}
