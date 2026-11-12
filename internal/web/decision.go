package web

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const decisionTimeout = assistTimeout

// maxDecisionOptions is the most options the decision model takes in one
// question.
const maxDecisionOptions = 10

var (
	decisionPurposeID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)
	decisionOptionID  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)
	// ErrInvalidDecision means the model returned an unusable answer. It is
	// distinct from a valid abstention and from a provider or context error.
	ErrInvalidDecision = errors.New("invalid decision answer")
)

var errDecisionUnavailable = &Error{Status: http.StatusConflict, Code: codeUtilityUnavailable,
	Message: "no available provider has a decision model"}

// DecisionRequest asks a provider's decision model to choose among options.
// Purpose is a stable caller-owned name, never a task ID or user input; each
// decision-<purpose> has its own invalid-answer pause. Instructions are trusted
// application instructions. Input is bounded, untrusted evidence, not a prompt.
type DecisionRequest struct {
	Purpose      string
	Instructions string
	Input        string
	Options      []DecisionOption
	// TaskID, when set, is the Thread the decision is for, named in its
	// Utility log entry.
	TaskID string
}

type DecisionOption = agentapi.DecisionOption

// DecisionResult is advisory. On success exactly one of Choice or Abstained is
// set. Callers must recheck current state before using it; a valid choice is not
// proof that the model interpreted the evidence correctly.
type DecisionResult struct {
	Choice    string
	Abstained bool
}

func validateDecisionRequest(req DecisionRequest) error {
	textOK := func(s string, max int) bool {
		return strings.TrimSpace(s) != "" && len(s) <= max && utf8.ValidString(s)
	}
	if !decisionPurposeID.MatchString(req.Purpose) {
		return newError(http.StatusBadRequest, "decision purpose must be 1 to 48 lowercase letters, digits or hyphens, starting with a letter")
	}
	if !textOK(req.Instructions, 4096) || !textOK(req.Input, 16384) {
		return newError(http.StatusBadRequest, "decision requires nonempty UTF-8 instructions (up to 4096 bytes) and evidence (up to 16384 bytes)")
	}
	if len(req.Options) < 2 || len(req.Options) > maxDecisionOptions {
		return newError(http.StatusBadRequest, "decision requires 2 to 10 options")
	}
	seen := make(map[string]bool, len(req.Options))
	for _, option := range req.Options {
		if !decisionOptionID.MatchString(option.ID) || seen[option.ID] || !textOK(option.Description, 512) {
			return newError(http.StatusBadRequest, "decision options require unique 1 to 64 character IDs (letters, digits, underscores or hyphens, starting with a letter or digit) and UTF-8 descriptions of 1 to 512 bytes")
		}
		seen[option.ID] = true
	}
	return nil
}

// checkDecision accepts only an abstention or the ID of an allowed option.
func checkDecision(answer agentapi.DecisionAnswer, options []DecisionOption) (DecisionResult, error) {
	if answer.Abstained {
		if answer.Choice != "" {
			return DecisionResult{}, ErrInvalidDecision
		}
		return DecisionResult{Abstained: true}, nil
	}
	for _, option := range options {
		if answer.Choice == option.ID {
			return DecisionResult{Choice: answer.Choice}, nil
		}
	}
	return DecisionResult{}, ErrInvalidDecision
}

// startDecisionLocked claims the first available provider, in order, that
// has a decision model, counted in titles so Shutdown waits for the call; the
// caller calls titles.Done when it ends. The caller holds mu.
func (m *Manager) startDecisionLocked() (agentapi.DecisionRunner, string, error) {
	if m.closed {
		return nil, "", errShuttingDown
	}
	for _, name := range m.order {
		if runner, ok := m.providers[name].(agentapi.DecisionRunner); ok && m.infos[name].Available {
			m.titles.Add(1)
			return runner, name, nil
		}
	}
	return nil, "", errDecisionUnavailable
}

// Decide makes one call to a provider's decision model, with no retry and no
// fallback to a Utility model: without a decision model it fails. It shares
// Utility slots, the daily limit and logging, and bounds queueing plus
// inference to assistTimeout (or the caller's deadline).
// Provider cleanup may extend beyond that deadline. Empty evidence is an input
// error: callers that know there is no evidence should abstain locally.
// Errors remain unwrap-compatible, including ErrInvalidDecision and ctx.Err().
func (m *Manager) Decide(ctx context.Context, input DecisionRequest) (DecisionResult, error) {
	if err := validateDecisionRequest(input); err != nil {
		return DecisionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return DecisionResult{}, err
	}
	m.mu.Lock()
	runner, provider, err := m.startDecisionLocked()
	call := UtilityCall{Provider: provider, Purpose: "decision-" + input.Purpose}
	if s := m.sessions[input.TaskID]; s != nil {
		call.TaskID, call.ProjectID = s.id, s.projectID
	}
	m.mu.Unlock()
	if err != nil {
		return DecisionResult{}, err
	}
	defer m.titles.Done()
	ctx, stop := m.bound(ctx)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, decisionTimeout)
	defer cancel()
	select {
	case m.titleSlots <- struct{}{}:
		defer func() { <-m.titleSlots }()
	case <-ctx.Done():
		return DecisionResult{}, ctx.Err()
	}
	// A cancellation while waiting for a slot spends no budget.
	if err := ctx.Err(); err != nil {
		return DecisionResult{}, err
	}
	var result DecisionResult
	var answer agentapi.DecisionAnswer
	_, err = m.runUtilityChecked(ctx, call, input.Instructions+input.Input,
		func(ctx context.Context, usage func(agentapi.UtilityUsage)) (string, error) {
			var err error
			answer, err = runner.RunDecision(ctx, agentapi.DecisionRequest{Purpose: input.Purpose, Instructions: input.Instructions, Input: input.Input, Options: input.Options, OnUsage: usage})
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if err == nil {
				result, err = checkDecision(answer, input.Options)
			}
			return "", err
		}, func(call *UtilityCall, _ string, err error) {
			call.Model = answer.Model
			logDecision(call, result, err)
		})
	if err != nil {
		return DecisionResult{}, err
	}
	return result, nil
}

// logDecision fills a Decide call's Utility log entry: provider and context
// errors leave validity unset, an invalid answer is recorded as such.
func logDecision(call *UtilityCall, result DecisionResult, err error) {
	call.Fallback = err != nil
	if err != nil && !errors.Is(err, ErrInvalidDecision) {
		return
	}
	call.Valid = new(err == nil)
	if err != nil {
		call.Reason = "invalid_answer"
	} else if result.Abstained {
		call.Answer = "[abstain]"
	} else {
		call.Answer = result.Choice
	}
}
