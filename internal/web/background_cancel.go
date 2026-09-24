package web

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type BackgroundTaskCancellation struct {
	Accepted        bool                     `json:"accepted"`
	BackgroundTasks agentapi.BackgroundTasks `json:"background_tasks"`
}

func (m *Manager) CancelBackgroundTask(id, taskID string) (BackgroundTaskCancellation, error) {
	s, err := m.lookup(id)
	if err != nil {
		return BackgroundTaskCancellation{}, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if err = s.readOnlyLocked(); err == nil && (s.removed || m.closed || s.conv == nil || s.state() == StateStarting) {
		err = newError(http.StatusConflict, "the Task has no active provider conversation")
	}
	if err == nil && (s.backgroundTasks == nil || !s.backgroundTasks.Known) {
		err = newError(http.StatusConflict, "background shell state is unknown; refresh before stopping")
	}
	if err == nil {
		index := slices.IndexFunc(s.backgroundTasks.Tasks, func(task agentapi.BackgroundTask) bool { return task.ID == taskID })
		if index < 0 {
			err = newError(http.StatusNotFound, "background shell task not found")
		} else if s.backgroundTasks.Tasks[index].Status != "running" {
			err = newError(http.StatusConflict, "background shell task is not running")
		}
	}
	if err == nil && !m.infos[s.provider].Capabilities.Cancel {
		err = newError(http.StatusConflict, "this provider cannot stop background shell tasks")
	}
	conv := s.conv
	m.mu.Unlock()
	if err != nil {
		return BackgroundTaskCancellation{}, err
	}
	controller, ok := conv.(agentapi.BackgroundTaskController)
	if !ok {
		return BackgroundTaskCancellation{}, newError(http.StatusConflict, "this provider cannot stop background shell tasks")
	}
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	snapshot, err := controller.CancelBackgroundTask(ctx, taskID)
	cancel()
	m.mu.Lock()
	if s.conv == conv {
		m.backgroundTasksLocked(s, snapshot)
	}
	m.mu.Unlock()
	switch {
	case err == nil:
		return BackgroundTaskCancellation{Accepted: true, BackgroundTasks: snapshot}, nil
	case errors.Is(err, agentapi.ErrBackgroundTaskNotFound):
		return BackgroundTaskCancellation{}, newError(http.StatusNotFound, "background shell task not found")
	case errors.Is(err, agentapi.ErrBackgroundTaskInactive), errors.Is(err, agentapi.ErrClosed):
		return BackgroundTaskCancellation{}, newError(http.StatusConflict, "the provider did not accept the shell stop: %s", shortError(err))
	default:
		return BackgroundTaskCancellation{}, newError(http.StatusBadGateway, "could not stop the background shell task: %s", shortError(err))
	}
}

func (s *Server) handleCancelBackgroundTask(w http.ResponseWriter, r *http.Request) {
	result, err := s.m.CancelBackgroundTask(r.PathValue("id"), r.PathValue("task_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
