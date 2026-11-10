package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Command runs one of the provider's listed commands. It follows the rules
// of a send: a repeated request ID returns the recorded outcome, a running
// turn refuses it, and there is no queue or steer.
func (m *Manager) Command(id string, req CommandRequest) (Submission, error) {
	if !validRequestID(req.RequestID) {
		return Submission{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
	}
	if !validCommandName(req.Name) {
		return Submission{}, newError(http.StatusBadRequest, "name must be a command name without the slash or spaces")
	}
	if len(req.Arguments) > maxPromptBytes {
		return Submission{}, newError(http.StatusRequestEntityTooLarge, "arguments are too large")
	}
	s, err := m.lookup(id)
	if err != nil {
		return Submission{}, err
	}
	m.mu.Lock()
	provider := s.provider
	m.mu.Unlock()
	if provider == agentapi.ProviderCopilot {
		return m.executeCommand(s, req)
	}
	return m.submit(s, turnInput{text: req.Arguments, command: req.Name, files: req.Files, attachments: req.Attachments}, req.RequestID, ModeSend)
}

const maxCommandName = 200

func validCommandName(name string) bool {
	if name == "" || len(name) > maxCommandName || strings.HasPrefix(name, "/") || !utf8.ValidString(name) {
		return false
	}
	return !strings.ContainsFunc(name, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) })
}

// Commands lists the live catalogue, reopening only the exact conversation
// of an active Task. Discovery never submits a prompt.
func (m *Manager) Commands(ctx context.Context, id string) ([]agentapi.Command, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	s.op.Lock()
	defer s.op.Unlock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if err = m.checkHolder(s); err != nil {
		return nil, err
	}
	if err = m.openLocked(s, true); err != nil {
		return nil, err
	}
	m.mu.Lock()
	conv, state := s.conv, s.state()
	m.mu.Unlock()
	if conv == nil {
		return nil, newError(http.StatusConflict, msgConversationNotOpen)
	}
	commands, err := m.listCommands(conv)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return []agentapi.Command{}, nil
	}
	for i := range commands {
		if turnRunning(state) && !commands[i].AllowDuringTurn && commands[i].DisabledReason == "" {
			commands[i].DisabledReason = "Wait for the active turn to finish"
		}
	}
	return commands, err
}

// listCommands reads conv's commands, made safe to show. A name a user
// could not type after a slash is left out.
func (m *Manager) listCommands(conv agentapi.Conversation) ([]agentapi.Command, error) {
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	listed, err := conv.Commands(ctx)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return nil, err
	}
	if err != nil {
		return nil, newError(http.StatusBadGateway, "could not list the provider's commands: %s", shortError(err))
	}
	out := make([]agentapi.Command, 0, len(listed))
	for _, c := range listed {
		if !validCommandName(c.Name) || slices.ContainsFunc(out, func(o agentapi.Command) bool { return o.Name == c.Name }) {
			continue
		}
		if c.Kind != agentapi.CommandSkill {
			c.Kind = agentapi.CommandPrompt
		}
		c.Description = clipRunes(strings.TrimSpace(displaytext.Sanitize(c.Description)), maxDetailRunes)
		c.InputHint = clipRunes(strings.TrimSpace(displaytext.Sanitize(c.InputHint)), maxDetailRunes)
		c.Aliases = slices.DeleteFunc(slices.Clone(c.Aliases), func(alias string) bool { return !validCommandName(alias) })
		c.DisabledReason = clipRunes(displaytext.Sanitize(c.DisabledReason), maxDetailRunes)
		out = append(out, c)
	}
	return out, nil
}

// commandOffered checks name against conv's listed commands.
func (m *Manager) commandOffered(conv agentapi.Conversation, name string) error {
	commands, err := m.listCommands(conv)
	switch {
	case errors.Is(err, agentapi.ErrUnsupported):
		return newError(http.StatusConflict, "this provider has no commands")
	case err != nil:
		return err
	case !slices.ContainsFunc(commands, func(c agentapi.Command) bool { return c.Name == name }):
		return newError(http.StatusNotFound, "/%s is not one of this thread's commands", name)
	}
	return nil
}
