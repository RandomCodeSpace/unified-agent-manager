package web

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// maxBackgroundCursor bounds the cursor a read sends back: the provider's own
// cursor and a byte count.
const maxBackgroundCursor = 256

// BackgroundOutput reads one bounded chunk of the output of a background
// shell the Thread lists: from the start with an empty cursor, else after the
// cursor an earlier read returned. Nothing is read ahead or kept here.
func (m *Manager) BackgroundOutput(id, taskID, cursor string) (agentapi.BackgroundOutput, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.BackgroundOutput{}, err
	}
	if len(cursor) > maxBackgroundCursor {
		return agentapi.BackgroundOutput{}, newError(http.StatusBadRequest, "the cursor is too long")
	}
	m.mu.Lock()
	switch {
	case s.removed || m.closed || s.conv == nil || s.state() == StateStarting:
		err = newError(http.StatusConflict, "the Thread has no active provider conversation")
	case s.backgroundTasks == nil || !slices.ContainsFunc(s.backgroundTasks.Tasks, func(task agentapi.BackgroundTask) bool { return task.ID == taskID }):
		err = newError(http.StatusNotFound, "background shell task not found")
	}
	conv := s.conv
	m.mu.Unlock()
	if err != nil {
		return agentapi.BackgroundOutput{}, err
	}
	reader, ok := conv.(agentapi.BackgroundOutputReader)
	if !ok {
		return agentapi.BackgroundOutput{}, newError(http.StatusConflict, "this provider cannot read background shell output")
	}
	if cursor == "" {
		m.count("background_output_opened")
	}
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	out, err := reader.ReadBackgroundOutput(ctx, taskID, cursor)
	switch {
	case err == nil:
		return out, nil
	case errors.Is(err, agentapi.ErrBackgroundOutputUnavailable):
		return agentapi.BackgroundOutput{}, newError(http.StatusNotFound, "this command's output is not kept")
	case errors.Is(err, agentapi.ErrClosed):
		return agentapi.BackgroundOutput{}, newError(http.StatusConflict, "the Thread has no active provider conversation")
	default:
		return agentapi.BackgroundOutput{}, newError(http.StatusBadGateway, "could not read the background shell output: %s", shortError(err))
	}
}

func (s *Server) handleBackgroundOutput(w http.ResponseWriter, r *http.Request) {
	result, err := s.m.BackgroundOutput(r.PathValue("id"), r.PathValue("task_id"), r.URL.Query().Get("cursor"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
