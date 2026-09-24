package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const retainedCommandRequests = 64

// executeCommand serializes command side effects with sends, settings and Stop.
// A durable uncertain reservation prevents a retry after a lost RPC response
// from invoking the same mutating command again.
func (m *Manager) executeCommand(s *webSession, req CommandRequest) (Submission, error) {
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	if sub, ok := s.findRequest(req.RequestID); ok {
		m.mu.Unlock()
		return sub, nil
	}
	err := s.readOnlyLocked()
	if s.removed {
		err = newError(http.StatusNotFound, "session not found")
	}
	if m.closed {
		err = errShuttingDown
	}
	m.mu.Unlock()
	if err != nil {
		return Submission{}, err
	}
	if sub, found, err := m.checkedPrompt(s, req.RequestID); found || err != nil {
		return sub, err
	}
	if err = m.openLocked(s, true); err != nil {
		return Submission{}, err
	}
	m.mu.Lock()
	conv, state, workdir := s.conv, s.state(), s.workdir
	m.mu.Unlock()
	if conv == nil {
		return Submission{}, newError(http.StatusConflict, "the provider conversation is not open")
	}
	executor, ok := conv.(agentapi.CommandExecutor)
	if !ok { // Providers retaining the old prompt-only contract.
		if busy(state) {
			return Submission{}, errTurnRunning
		}
		return m.send(s, turnInput{text: req.Arguments, command: req.Name, files: req.Files, attachments: req.Attachments}, req.RequestID)
	}
	commands, err := m.listCommands(conv)
	if err != nil {
		return Submission{}, err
	}
	var offered *agentapi.Command
	for i := range commands {
		c := &commands[i]
		if c.Name == req.Name || slices.Contains(c.Aliases, req.Name) {
			offered = c
			break
		}
	}
	if offered == nil {
		return Submission{}, newError(http.StatusNotFound, "/%s is not one of this task's commands", req.Name)
	}
	if offered.DisabledReason != "" {
		return Submission{}, newError(http.StatusConflict, "%s", offered.DisabledReason)
	}
	if busy(state) && !offered.AllowDuringTurn {
		return Submission{}, errTurnRunning
	}
	if offered.InputRequired && strings.TrimSpace(req.Arguments) == "" {
		return Submission{}, newError(http.StatusBadRequest, "this command requires arguments")
	}
	req.Name = offered.Name
	local := isTaskCommand(req.Name)
	if local && (len(req.Files) > 0 || len(req.Attachments) > 0) {
		return Submission{}, newError(http.StatusBadRequest, "this command does not accept attachments")
	}
	files, err := checkFiles(workdir, req.Files)
	if err != nil {
		return Submission{}, err
	}
	m.mu.Lock()
	uploads, err := m.checkUploadsLocked(s, req.Attachments)
	m.mu.Unlock()
	if err != nil {
		return Submission{}, err
	}
	blobs, err := m.readBlobs(s.id, uploads)
	if err != nil {
		return Submission{}, err
	}
	// Reserve before either native invocation or local settings mutation.
	reserved := Submission{RequestID: req.RequestID, Status: SubmissionUncertain, Error: "command outcome has not been confirmed; it will not be repeated", Time: m.now()}
	if err = m.saveCommandSubmission(s, reserved, false); err != nil {
		return Submission{}, newError(http.StatusServiceUnavailable, "could not reserve the command request: %s", shortError(err))
	}
	var result *agentapi.CommandResult
	if local {
		result, err = m.taskCommand(s, req.Name, strings.TrimSpace(req.Arguments))
	} else {
		ctx, cancel := context.WithTimeout(m.ctx, sendTimeout)
		result, err = executor.ExecuteCommand(ctx, req.Name, agentapi.Prompt{Text: req.Arguments, Files: files, Attachments: blobs})
		cancel()
	}
	sub := Submission{RequestID: req.RequestID, Status: SubmissionAccepted, Time: m.now(), CommandResult: result}
	if err != nil {
		sub.Status, sub.Error = SubmissionRejected, shortError(err)
		if errors.Is(err, agentapi.ErrSubmissionUncertain) {
			sub.Status = SubmissionUncertain
		}
	} else if result == nil {
		m.markUsed(s, uploads)
	}
	if errors.Is(err, agentapi.ErrSubmissionUncertain) {
		m.markUsed(s, uploads)
	}
	if saveErr := m.saveCommandSubmission(s, sub, true); saveErr != nil {
		// The durable reservation is sufficient to prevent replay, but the exact
		// result could be lost on restart. Do not claim durable acknowledgement.
		return reserved, newError(http.StatusServiceUnavailable, "command ran but its result could not be saved: %s", shortError(saveErr))
	}
	return sub, nil
}

func isTaskCommand(name string) bool {
	switch name {
	case "allow-all", "permissions", "model", "rename":
		return true
	}
	return false
}

// Native permission/model commands mutate a different settings owner. Handle
// their web equivalents here so Safe/Yolo and Task model settings stay exact.
func (m *Manager) taskCommand(s *webSession, name, args string) (*agentapi.CommandResult, error) {
	completed := &agentapi.CommandResult{Kind: "completed"}
	switch name {
	case "allow-all", "permissions":
		m.mu.Lock()
		mode := string(s.mode)
		m.mu.Unlock()
		if name == "permissions" && args == "" {
			return &agentapi.CommandResult{Kind: "action", Action: "permissions"}, nil
		}
		switch args {
		case "show":
			return &agentapi.CommandResult{Kind: "text", Text: "Permission mode: " + mode}, nil
		case "":
			if mode == "yolo" {
				mode = "safe"
			} else {
				mode = "yolo"
			}
		case "on", "allow-all":
			mode = "yolo"
		case "off", "default":
			mode = "safe"
		default:
			return nil, errors.New("supported permission arguments: on, off, show")
		}
		_, err := m.SetMode(s.id, mode)
		completed.Text = "Permission mode: " + mode
		return completed, err
	case "model":
		if args == "" {
			return &agentapi.CommandResult{Kind: "action", Action: "model"}, nil
		}
		if strings.ContainsAny(args, " \t\r\n") {
			return nil, errors.New("use /model with one offered model ID or the model settings control")
		}
		_, err := m.setModelLocked(s, &args, nil, nil)
		completed.Text = "Model: " + args
		return completed, err
	case "rename":
		if args == "" {
			return &agentapi.CommandResult{Kind: "action", Action: "rename"}, nil
		}
		_, err := m.Rename(s.id, args)
		completed.Text = "Task renamed"
		return completed, err
	}
	return nil, agentapi.ErrUnsupported
}

func (m *Manager) saveCommandSubmission(s *webSession, sub Submission, publish bool) error {
	if sub.CommandResult != nil {
		sub.CommandResult = cleanCommandResult(*sub.CommandResult)
	}
	m.mu.Lock()
	before := m.summaryLocked(s)
	s.commandSubmissions = slices.DeleteFunc(s.commandSubmissions, func(old Submission) bool { return old.RequestID == sub.RequestID })
	s.commandSubmissions = append(s.commandSubmissions, sub)
	if len(s.commandSubmissions) > retainedCommandRequests {
		s.commandSubmissions = s.commandSubmissions[len(s.commandSubmissions)-retainedCommandRequests:]
	}
	encoded, _ := json.Marshal(s.commandSubmissions)
	s.commandLedger = string(encoded)
	s.remember(sub)
	last := sub
	s.last = &last
	if publish && (sub.Status == SubmissionUncertain || sub.Status == SubmissionRejected) {
		m.pauseQueueLocked(s)
	}
	if publish {
		m.broadcastLocked("submission", s.id, func(seq uint64) any { return submissionEvent{Seq: seq, SessionID: s.id, Submission: sub} })
	}
	m.changedLocked(s, before)
	m.mu.Unlock()
	return m.flush()
}

func cleanCommandResult(value agentapi.CommandResult) *agentapi.CommandResult {
	value.Text = clipRunes(displaytext.Sanitize(value.Text), maxPromptBytes)
	value.Title = clipRunes(displaytext.Sanitize(value.Title), maxDetailRunes)
	value.PrefillInput = clipRunes(displaytext.Sanitize(value.PrefillInput), maxPromptBytes)
	return &value
}
