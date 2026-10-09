package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

const maxPendingForks = 64

// ForkRequest selects a completed owner message, including its whole turn.
// Model is the selected target model; empty deliberately selects Default.
type ForkRequest struct {
	UserItemID string `json:"user_item_id"`
	Model      string `json:"model"`
	RequestID  string `json:"request_id"`
}

var errForkUncertain = &Error{Status: http.StatusConflict, Code: "fork_uncertain", Message: "the provider may have created this branch, but its exact result is unknown; this request will not create another branch automatically"}
var errForkRegistered = errors.New("branch request is already registered")

func forkMatches(lineage store.ForkLineage, source string, req ForkRequest) bool {
	return lineage.SourceTaskID == source && lineage.UserItemID == req.UserItemID && lineage.Model == req.Model
}

// Fork copies recorded history without opening the source or submitting a
// turn. The native RPC has no idempotency key; the durable reservation is
// therefore written first, and an unresolved request never reissues it.
func (m *Manager) Fork(id string, req ForkRequest) (SessionSummary, error) {
	if !validRequestID(req.RequestID) {
		return SessionSummary{}, newError(http.StatusBadRequest, msgRequestIDNotUUID)
	}
	if req.UserItemID == "" || len(req.UserItemID) > 256 || strings.ContainsAny(req.UserItemID, "\x00\r\n") {
		return SessionSummary{}, newError(http.StatusBadRequest, "choose an exact recorded owner message")
	}
	s, err := m.lookup(id)
	if err != nil {
		return SessionSummary{}, err
	}
	s.op.Lock()
	locked := true
	defer func() {
		if locked {
			s.op.Unlock()
		}
	}()
	if result, pending, err := m.recordedFork(id, req); err != nil || result.ID != "" {
		return result, err
	} else if pending != nil {
		if pending.ProviderSessionID == "" {
			return SessionSummary{}, errForkUncertain
		}
		s.op.Unlock()
		locked = false
		return savedFork(m.registerFork(*pending))
	}
	m.mu.Lock()
	if err := m.forkAllowedLocked(s); err != nil {
		m.mu.Unlock()
		return SessionSummary{}, err
	}
	create := CreateRequest{ProjectID: s.projectID, Provider: s.provider, Model: req.Model, Effort: s.effort, ContextSize: s.contextSize, Mode: string(s.mode)}
	if req.Model != s.model {
		create.Effort, create.ContextSize = "", "default"
	}
	key, gen, stop, sequence := s.key(), s.gen, s.stopSeq, s.turnSeq
	convID, external := s.convID, s.conv == nil || s.imported || s.terminalID != ""
	m.mu.Unlock()
	prov, workdir, mode, err := m.checkCreate(&create)
	if err != nil {
		return SessionSummary{}, err
	}
	forker, ok := prov.(agentapi.Forker)
	if !ok || !prov.Capabilities().Fork {
		return SessionSummary{}, newError(http.StatusConflict, "this provider does not support recorded-history branches; Run again repeats the last message")
	}
	if external {
		s.op.Unlock()
		held, checkErr := m.holders(m.ctx, prov, []string{convID})
		s.op.Lock()
		m.mu.Lock()
		changed := s.key() != key || s.gen != gen || s.stopSeq != stop || s.turnSeq != sequence
		allowed := m.forkAllowedLocked(s)
		m.mu.Unlock()
		if allowed != nil {
			return SessionSummary{}, allowed
		}
		if changed {
			return SessionSummary{}, newError(http.StatusConflict, "the task changed while checking its holder; choose the turn again")
		}
		if checkErr != nil {
			return SessionSummary{}, checkErr
		}
		if slices.Contains(held, convID) {
			return SessionSummary{}, errHeldElsewhere
		}
	}
	ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
	defer cancel()
	boundary, err := forker.ReadForkBoundary(ctx, agentapi.ForkBoundaryRequest{ConversationID: convID, UserItemID: req.UserItemID})
	if err != nil {
		return SessionSummary{}, forkReadError(err)
	}
	m.mu.Lock()
	changed := s.key() != key || s.gen != gen || s.stopSeq != stop || s.turnSeq != sequence
	allowed := m.forkAllowedLocked(s)
	m.mu.Unlock()
	if allowed != nil {
		return SessionSummary{}, allowed
	}
	if changed {
		return SessionSummary{}, newError(http.StatusConflict, "the task changed while reading its record; choose the turn again")
	}
	targetID, err := newUUID()
	if err != nil {
		return SessionSummary{}, err
	}
	reservation := store.WebFork{
		ID: targetID, Provider: prov.Name(), SourceConversationID: convID, ProjectID: create.ProjectID, Workdir: workdir,
		Effort: create.Effort, ContextSize: create.ContextSize, Mode: mode, CreatedAt: m.now(), TailEventID: boundary.TailEventID,
		Lineage: store.ForkLineage{SourceTaskID: id, UserItemID: req.UserItemID, UserEventID: boundary.UserEventID, ToEventID: boundary.ToEventID, RequestID: req.RequestID, Model: req.Model},
	}
	if err := m.store.Update(func(cfg *store.Config) error {
		if m.isClosed() {
			return errShuttingDown
		}
		if !forkSourcePresent(cfg, reservation) {
			return newError(http.StatusConflict, "the source task or project changed before branching")
		}
		if _, exists := cfg.Sessions[store.Key(reservation.Provider, reservation.ID)]; exists {
			return errors.New("the reserved task identity is already in use")
		}
		if cfg.WebForks == nil {
			cfg.WebForks = map[string]store.WebFork{}
		}
		if _, exists := cfg.WebForks[req.RequestID]; exists {
			return errForkUncertain
		}
		for _, pending := range cfg.WebForks {
			if forkMatches(pending.Lineage, id, req) {
				return errForkUncertain
			}
		}
		if len(cfg.WebForks) >= maxPendingForks {
			return newError(http.StatusConflict, "unresolved branch requests must be reconciled before creating more branches")
		}
		cfg.WebForks[req.RequestID] = reservation
		return nil
	}); err != nil {
		return SessionSummary{}, fmt.Errorf("reserve task branch: %w", err)
	}
	providerID, err := forker.Fork(ctx, agentapi.ForkRequest{ForkBoundaryRequest: agentapi.ForkBoundaryRequest{ConversationID: convID, UserItemID: req.UserItemID}, Boundary: boundary})
	if err != nil {
		if errors.Is(err, agentapi.ErrForkUncertain) {
			return SessionSummary{}, errForkUncertain
		}
		if errors.Is(err, agentapi.ErrUnsupported) {
			m.mu.Lock()
			before := m.summaryLocked(s)
			info := m.infos[prov.Name()]
			info.Capabilities.Fork = false
			m.infos[prov.Name()] = info
			m.changedLocked(s, before)
			m.mu.Unlock()
		}
		// A definite pre-RPC/unsupported refusal can release the reservation.
		if saveErr := m.store.Update(func(cfg *store.Config) error {
			pending, exists := cfg.WebForks[req.RequestID]
			if !exists || pending.ID != reservation.ID || pending.ProviderSessionID != "" {
				return errForkUncertain
			}
			delete(cfg.WebForks, req.RequestID)
			return nil
		}); saveErr != nil {
			return SessionSummary{}, fmt.Errorf("branch refused, but its reservation could not be released: %w", saveErr)
		}
		return SessionSummary{}, forkReadError(err)
	}
	if !store.ValidProviderSessionID(providerID) || providerID == convID {
		return SessionSummary{}, errForkUncertain
	}
	reservation.ProviderSessionID = providerID
	if err := m.store.Update(func(cfg *store.Config) error {
		pending, exists := cfg.WebForks[req.RequestID]
		if !exists || pending.ID != reservation.ID {
			return errors.New("the reserved branch identity changed")
		}
		cfg.WebForks[req.RequestID] = reservation
		return nil
	}); err != nil {
		return SessionSummary{}, fmt.Errorf("the provider created the branch, but its exact result could not be saved; no retry will create another: %w", err)
	}
	// register takes projectMu before any Task operation lock, matching
	// RemoveProject. The source no longer needs to be held after the copy.
	s.op.Unlock()
	locked = false
	return savedFork(m.registerFork(reservation))
}

// savedFork marks a failed registration of a saved native result, which the
// picker can add again or dismiss; the Copilot session is never deleted.
func savedFork(sum SessionSummary, err error) (SessionSummary, error) {
	if err == nil || errors.Is(err, errForkUncertain) {
		return sum, err
	}
	status, msg := errorStatus(err)
	return SessionSummary{}, &Error{Status: status, Code: "fork_saved", Message: "could not add the existing branch: " + msg}
}

func (m *Manager) forkAllowedLocked(s *webSession) error {
	if m.closed {
		return errShuttingDown
	}
	if s.removed {
		return newError(http.StatusNotFound, msgSessionNotFound)
	}
	if s.convID == "" {
		return newError(http.StatusConflict, "this task has no recorded provider conversation")
	}
	if s.opening != nil {
		return newError(http.StatusConflict, "wait for the task to finish opening before branching")
	}
	if err := s.rewindHoldLocked(); err != nil {
		return err
	}
	return s.settleableLocked()
}

func forkReadError(err error) error {
	switch {
	case errors.Is(err, agentapi.ErrItemNotFound), errors.Is(err, agentapi.ErrConversationNotFound):
		return newError(http.StatusNotFound, "the exact recorded owner turn is no longer available")
	case errors.Is(err, agentapi.ErrBusy):
		return newError(http.StatusConflict, "wait for the selected owner turn to finish before branching")
	case errors.Is(err, agentapi.ErrUnsupported):
		return newError(http.StatusConflict, "this provider does not support recorded-history branches; Run again repeats the last message")
	default:
		return newError(http.StatusBadGateway, "could not verify the branch: %s", shortError(err))
	}
}

func (m *Manager) recordedFork(source string, req ForkRequest) (SessionSummary, *store.WebFork, error) {
	cfg, err := m.store.Load()
	if err != nil {
		return SessionSummary{}, nil, err
	}
	for _, rec := range cfg.Sessions {
		if rec.Surface != store.SurfaceWeb || rec.Web == nil || rec.Web.Fork == nil || rec.Web.Fork.RequestID != req.RequestID {
			continue
		}
		if !forkMatches(*rec.Web.Fork, source, req) {
			return SessionSummary{}, nil, newError(http.StatusConflict, "this branch request ID was already used for a different selection")
		}
		sum, err := m.Summary(rec.ID)
		return sum, nil, err
	}
	if pending, exists := cfg.WebForks[req.RequestID]; exists {
		if !forkMatches(pending.Lineage, source, req) {
			return SessionSummary{}, nil, newError(http.StatusConflict, "this branch request ID was already used for a different selection")
		}
		return SessionSummary{}, &pending, nil
	}
	for _, pending := range cfg.WebForks {
		if pending.ProviderSessionID != "" && forkMatches(pending.Lineage, source, req) {
			return SessionSummary{}, &pending, nil
		}
	}
	return SessionSummary{}, nil, nil
}

func forkSourcePresent(cfg *store.Config, fork store.WebFork) bool {
	source, exists := cfg.Sessions[store.Key(fork.Provider, fork.Lineage.SourceTaskID)]
	project, projectExists := cfg.WebProjects[fork.ProjectID]
	return exists && source.ID == fork.Lineage.SourceTaskID && source.Agent == fork.Provider && source.Surface == store.SurfaceWeb && source.ProviderSessionID == fork.SourceConversationID && source.Workdir == fork.Workdir && source.Web != nil && source.Web.ProjectID == fork.ProjectID && projectExists && project.Dir == fork.Workdir
}

func (m *Manager) registerFork(fork store.WebFork) (SessionSummary, error) {
	if !validRequestID(fork.ID) || !validRequestID(fork.Lineage.RequestID) || !store.ValidProviderSessionID(fork.ProviderSessionID) || fork.ProviderSessionID == fork.SourceConversationID {
		return SessionSummary{}, errForkUncertain
	}
	create := CreateRequest{ProjectID: fork.ProjectID, Provider: fork.Provider, Model: fork.Lineage.Model, Effort: fork.Effort, ContextSize: fork.ContextSize, Mode: string(fork.Mode)}
	_, workdir, _, err := m.checkCreate(&create)
	if err != nil {
		return SessionSummary{}, err
	}
	if workdir != fork.Workdir {
		return SessionSummary{}, newError(http.StatusConflict, "the branch project directory changed")
	}
	s := newSession(fork.ID, fork.Provider, "", fork.Workdir, fork.ProviderSessionID, fork.CreatedAt)
	s.projectID, s.model, s.effort, s.contextSize, s.mode = fork.ProjectID, fork.Lineage.Model, fork.Effort, fork.ContextSize, fork.Mode
	s.base, s.fork = StateClosed, &fork.Lineage
	rec := store.SessionRecord{ID: s.id, Agent: s.provider, Workdir: s.workdir, Mode: s.mode, CreatedAt: s.createdAt, LastSeenAt: s.createdAt, Status: store.StatusActive, Surface: store.SurfaceWeb, ProviderSessionID: s.convID,
		Web: &store.WebState{Turn: StateClosed, UpdatedAt: s.updatedAt, ProjectID: s.projectID, Model: s.model, Effort: s.effort, ContextSize: s.contextSize, Fork: s.fork}}
	check := func(cfg *store.Config) error {
		if existing, exists := cfg.Sessions[store.Key(fork.Provider, fork.ID)]; exists {
			if existing.ID == fork.ID && existing.ProviderSessionID == fork.ProviderSessionID && existing.Web != nil && existing.Web.Fork != nil && existing.Web.Fork.RequestID == fork.Lineage.RequestID {
				return errForkRegistered
			}
			return errors.New("the reserved task identity is already in use; its native branch result is preserved")
		}
		if !forkSourcePresent(cfg, fork) {
			return newError(http.StatusConflict, "the provider created the branch, but the source task or project changed; its exact result is saved for reconciliation")
		}
		pending, exists := cfg.WebForks[fork.Lineage.RequestID]
		if !exists || pending.ID != fork.ID || pending.ProviderSessionID != fork.ProviderSessionID {
			return errors.New("the reserved branch result changed")
		}
		for _, existing := range cfg.Sessions {
			if existing.ProviderSessionID == fork.ProviderSessionID {
				return errors.New("the forked provider conversation is already registered")
			}
		}
		delete(cfg.WebForks, fork.Lineage.RequestID)
		return nil
	}
	if err := m.register(s, nil, rec, check); err != nil {
		if errors.Is(err, errForkRegistered) {
			return m.Summary(fork.ID)
		}
		return SessionSummary{}, err
	}
	return m.Summary(s.id)
}

// DismissFork clears the one unresolved reservation for this source, owner
// message and model, uncertain or saved but unregistered, so a later explicit
// request may fork again. It deletes no Copilot session and waits for an
// in-flight fork.
func (m *Manager) DismissFork(id string, req ForkRequest) error {
	if req.UserItemID == "" || len(req.UserItemID) > 256 || strings.ContainsAny(req.UserItemID, "\x00\r\n") {
		return newError(http.StatusBadRequest, "choose an exact recorded owner message")
	}
	s, err := m.lookup(id)
	if err != nil {
		return err
	}
	s.op.Lock()
	defer s.op.Unlock()
	return m.store.Update(func(cfg *store.Config) error {
		for requestID, pending := range cfg.WebForks {
			if !forkMatches(pending.Lineage, id, req) {
				continue
			}
			delete(cfg.WebForks, requestID)
			return nil
		}
		return newError(http.StatusNotFound, "no unresolved branch request for that reply and model")
	})
}

func (s *Server) handleDismissFork(w http.ResponseWriter, r *http.Request) {
	var req ForkRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.m.DismissFork(r.PathValue("id"), req); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleFork(w http.ResponseWriter, r *http.Request) {
	var req ForkRequest
	if !decodeBody(w, r, &req) {
		return
	}
	result, err := s.m.Fork(r.PathValue("id"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
