package web

import (
	"context"
	"errors"
	"net/http"
)

// ResendRequest edits a past owner prompt: a confirmed rewind to before it,
// then the edited prompt as an ordinary send.
type ResendRequest struct {
	Rewind RewindRequest `json:"rewind"`
	Prompt PromptRequest `json:"prompt"`
}

// ResendResult reports both steps. Submission is set only when the prompt
// was sent. SendError says why it was not, once the rewind was applied.
type ResendResult struct {
	Rewind     RewindReceipt `json:"rewind"`
	Submission *Submission   `json:"submission,omitempty"`
	SendError  string        `json:"send_error,omitempty"`
	SendCode   string        `json:"send_code,omitempty"`
}

// rewindTruncated is a rewind whose native history truncation landed.
func rewindTruncated(r RewindReceipt) bool {
	if r.Result == nil || r.State == rewindUncertain || r.State == rewindPending {
		return false
	}
	switch r.Result.Outcome {
	case "success", "checkpoint-cleanup-failed", "snapshot-prune-failed":
		return true
	}
	return false
}

// Resend runs the rewind once, waits for the authoritative reread, then sends
// the edited prompt once through Submit. An uncertain, partial or refused
// rewind sends nothing. Neither step is ever repeated automatically; a
// repeated request uses the same rewind and prompt request IDs.
func (m *Manager) Resend(id string, req ResendRequest) (ResendResult, error) {
	if !validRequestID(req.Prompt.RequestID) || req.Prompt.RequestID == req.Rewind.RequestID {
		return ResendResult{}, newError(http.StatusBadRequest, "the prompt needs its own request_id UUID")
	}
	if req.Prompt.Mode != "" && req.Prompt.Mode != ModeSend {
		return ResendResult{}, newError(http.StatusBadRequest, "an edited prompt is sent as a new turn")
	}
	receipt, err := m.Rewind(id, req.Rewind)
	if err != nil {
		return ResendResult{}, err
	}
	out := ResendResult{Rewind: receipt}
	if !rewindTruncated(receipt) {
		return out, nil
	}
	if err := m.awaitRewindReread(id, receipt.RequestID); err != nil {
		out.SendError = "the conversation is still being read again after the rewind; your edited prompt was not sent: " + err.Error()
		return out, nil
	}
	req.Prompt.Mode = ModeSend
	sub, err := m.Submit(id, req.Prompt)
	if err != nil {
		var webErr *Error
		if errors.As(err, &webErr) {
			out.SendCode = webErr.Code
		}
		_, out.SendError = errorStatus(err)
		return out, nil
	}
	out.Submission = &sub
	return out, nil
}

// awaitRewindReread waits, bounded, until the record read after an applied
// rewind released the Task.
func (m *Manager) awaitRewindReread(id, requestID string) error {
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(m.ctx, openTimeout)
	defer cancel()
	for range 3 {
		m.mu.Lock()
		r := s.rewind
		if s.removed || r == nil || r.RequestID != requestID {
			m.mu.Unlock()
			return newError(http.StatusConflict, msgHistoryChanged)
		}
		if r.State == rewindDone {
			m.mu.Unlock()
			return nil
		}
		wait := s.historyDone
		if s.opening != nil {
			wait = s.opening
		}
		history := s.history
		m.mu.Unlock()
		if wait == nil || history == HistoryUnavailable {
			return newError(http.StatusConflict, "reread the conversation, then send again")
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return newError(http.StatusGatewayTimeout, "reading it took too long")
		}
		m.mu.Lock()
		state, loading := "", s.history == HistoryLoading || s.opening != nil
		if s.rewind != nil {
			state = s.rewind.State
		}
		m.mu.Unlock()
		if state != rewindDone && !loading {
			return newError(http.StatusConflict, "reread the conversation, then send again")
		}
	}
	return newError(http.StatusConflict, "reread the conversation, then send again")
}

func (s *Server) handleResend(w http.ResponseWriter, r *http.Request) {
	var req ResendRequest
	if !decodeBody(w, r, &req) {
		return
	}
	out, err := s.m.Resend(r.PathValue("id"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
