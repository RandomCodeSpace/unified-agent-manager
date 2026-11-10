package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// yoloResolution and assistedResolution are the recorded resolutions of a
// permission request that yolo mode or an approving assisted review allowed.
const (
	yoloResolution     = "allowed (yolo)"
	assistedResolution = "allowed (assisted review)"
)

// assistedApprovalModel is the only reviewer assisted Tasks use. A
// configuration without it is refused, never left to the runtime default.
const assistedApprovalModel = "gpt-6-luna"

func autoResolution(ix *interaction) string {
	if ix.assisted {
		return assistedResolution
	}
	return yoloResolution
}

// approvalModel is the OpenRequest.AssistedApprovalModel of a Task mode.
func approvalModel(mode store.Mode) string {
	if mode == store.ModeAssisted {
		return assistedApprovalModel
	}
	return ""
}

// assistedSupportLocked refuses assisted mode unless the provider supports
// it and offers assistedApprovalModel.
func (m *Manager) assistedSupportLocked(provider string) error {
	if why := m.infos[provider].Capabilities.AssistedUnavailable; why != "" {
		return newError(http.StatusConflict, "Assisted is unavailable: %s", why)
	}
	if !m.infos[provider].Capabilities.AssistedPermissions {
		return newError(http.StatusConflict, "this provider does not support assisted permissions")
	}
	if m.modelLocked(provider, assistedApprovalModel).ID == "" {
		return newError(http.StatusConflict, "assisted permissions need the %s reviewer model, which this account does not offer", assistedApprovalModel)
	}
	return nil
}

// syncAssisted copies provider's current assisted-permission support into
// its info: a runtime that refused assisted withdraws it until it changes.
// The provider is asked without mu held.
func (m *Manager) syncAssisted(provider string) {
	m.mu.Lock()
	prov := m.providers[provider]
	m.mu.Unlock()
	if prov == nil {
		return
	}
	caps := prov.Capabilities()
	m.mu.Lock()
	defer m.mu.Unlock()
	info, ok := m.infos[provider]
	if !ok {
		return
	}
	info.Capabilities.AssistedPermissions, info.Capabilities.AssistedUnavailable = caps.AssistedPermissions, caps.AssistedUnavailable
	m.infos[provider] = info
}

// autoAllowLocked answers a pending permission request with the provider's
// single-use allow option, through the same claim as a browser's answer:
// any request of a yolo Task, and a request of an assisted Task that
// assistedApprovalModel reviewed and approved. Questions, and requests
// without that option (the provider's policy says a person must decide),
// stay with the user. It reports whether it claimed ix.
func (m *Manager) autoAllowLocked(s *webSession, ix *interaction) bool {
	assisted := s.mode == store.ModeAssisted && !s.leavingAssisted && ix.Assisted.Recommendation == "approve" && ix.Assisted.Model == assistedApprovalModel
	if s.mode != store.ModeYolo && !assisted || s.modeUnknown || m.closed || s.removed || s.conv == nil ||
		ix.Kind != agentapi.InteractionPermission || ix.State != agentapi.InteractionPending || ix.answering {
		return false
	}
	i := slices.IndexFunc(ix.Options, func(o agentapi.Option) bool { return o.AllowOnce && !o.Reject })
	if i < 0 {
		return false
	}
	conv, id, answer := s.conv, ix.ID, agentapi.Answer{Decision: ix.Options[i].ID, Auto: true}
	ix.answering, ix.yolo, ix.assisted = true, true, assisted
	m.wg.Add(1)
	go func() {
		defer log.Recover("automatic approval", &m.mu)
		defer m.wg.Done()
		if _, err := m.respond(s, ix, conv, id, answer); err != nil {
			log.Warn("web automatic approval failed", "session", s.id, "interaction", id, "error", err)
		}
	}()
	return true
}

// autoAllowPendingLocked applies yolo mode to every pending permission
// request of s; the browser learns each claimed one no longer waits for the
// user.
func (m *Manager) autoAllowPendingLocked(s *webSession) {
	for _, ix := range s.interactions {
		if m.autoAllowLocked(s, ix) {
			m.publishInteractionLocked(s, ix)
		}
	}
}

// parseMode validates a Task mode.
func parseMode(mode string) (store.Mode, error) {
	switch store.Mode(mode) {
	case store.ModeSafe, store.ModeYolo, store.ModeAssisted:
		return store.Mode(mode), nil
	}
	return "", newError(http.StatusBadRequest, "mode must be safe, yolo or assisted")
}

// parseDefaultMode validates the mode of Task defaults and routines:
// assisted is a per-Task opt-in, and the store keeps only safe or yolo there.
func parseDefaultMode(mode string) (store.Mode, error) {
	switch store.Mode(mode) {
	case store.ModeSafe, store.ModeYolo:
		return store.Mode(mode), nil
	}
	return "", newError(http.StatusBadRequest, "mode must be safe or yolo")
}

// SetMode sets the Task's permission mode at any time, even while a turn
// runs. It applies to permission requests raised afterwards; switching to yolo
// also answers the ones already pending. Switching into or out of assisted
// changes the open conversation's mode first (setAssistedMode).
func (m *Manager) SetMode(id, mode string) (SessionSummary, error) {
	return m.setMode(id, mode, false)
}

// setMode is SetMode; opHeld says the caller already holds s.op (a
// permission command), which a change into or out of assisted needs.
func (m *Manager) setMode(id, mode string, opHeld bool) (SessionSummary, error) {
	md, err := parseMode(mode)
	if err != nil {
		return SessionSummary{}, err
	}
	m.mu.Lock()
	s := m.sessions[id]
	if s == nil {
		m.mu.Unlock()
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	if md == store.ModeAssisted || s.mode == store.ModeAssisted || s.modeUnknown {
		m.mu.Unlock()
		if !opHeld {
			s.op.Lock()
			defer s.op.Unlock()
		}
		return m.setAssistedMode(s, md)
	}
	defer m.mu.Unlock()
	if err := s.readOnlyLocked(); err != nil {
		return SessionSummary{}, err
	}
	before := m.summaryLocked(s)
	if s.mode != md {
		s.fuses = nil
	}
	s.mode = md
	m.autoAllowPendingLocked(s)
	m.changedLocked(s, before)
	return m.summaryLocked(s), nil
}

// setAssistedMode is SetMode into or out of assisted, or out of an unknown
// mode; the caller holds s.op, which keeps opens out, so the conversation it
// changes stays the open one. The mode is recorded only after the runtime
// applied it; a closed conversation gets it at its next open. While the
// runtime leaves assisted, its approving reviews no longer allow requests on
// their own. When the change fails or times out, the runtime's mode is read
// back and adopted; if that fails too, the mode is unknown and nothing is
// allowed automatically until a later change sets it.
func (m *Manager) setAssistedMode(s *webSession, md store.Mode) (SessionSummary, error) {
	if md == store.ModeAssisted {
		m.syncAssisted(s.provider)
	}
	m.mu.Lock()
	err := s.readOnlyLocked()
	switch {
	case s.removed:
		err = newError(http.StatusNotFound, msgSessionNotFound)
	case err == nil && md == store.ModeAssisted && s.mode != store.ModeAssisted:
		err = m.assistedSupportLocked(s.provider)
	}
	conv, toggle := s.conv, (md == store.ModeAssisted) != (s.mode == store.ModeAssisted) || s.modeUnknown
	if err == nil && conv != nil && toggle && s.mode == store.ModeAssisted {
		s.leavingAssisted = true
	}
	m.mu.Unlock()
	if err != nil {
		return SessionSummary{}, err
	}
	if conv != nil && toggle {
		var uncertain bool
		ctx, cancel := context.WithTimeout(m.ctx, controlTimeout)
		uncertain, err = setAssistedPermissions(ctx, s.id, conv, approvalModel(md))
		cancel()
		if err != nil && md == store.ModeAssisted {
			m.syncAssisted(s.provider)
		}
		if err != nil {
			return m.assistedModeFailed(s, conv, md, err, uncertain)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.leavingAssisted = false
	if s.removed {
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	before := m.summaryLocked(s)
	if s.mode != md || s.modeUnknown {
		s.fuses = nil
	}
	s.mode, s.modeUnknown = md, false
	m.autoAllowPendingLocked(s)
	m.changedLocked(s, before)
	return m.summaryLocked(s), nil
}

// modeReadTimeout bounds reading the runtime's permission mode back.
const modeReadTimeout = 10 * time.Second

// assistedModeFailed records the outcome of a refused or uncertain change of
// conv's mode to md. A refusal changed nothing. After an uncertain one the
// runtime's mode is read back: the mode it reports is adopted, and the change
// succeeded if that is md; an unreadable mode is unknown.
func (m *Manager) assistedModeFailed(s *webSession, conv agentapi.Conversation, md store.Mode, setErr error, uncertain bool) (SessionSummary, error) {
	on, readErr := false, error(nil)
	if uncertain {
		ctx, cancel := context.WithTimeout(m.ctx, modeReadTimeout)
		on, readErr = conv.(agentapi.AssistedPermissionSetter).AssistedPermissionsOn(ctx)
		cancel()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s.leavingAssisted = false
	if s.removed {
		return SessionSummary{}, newError(http.StatusNotFound, msgSessionNotFound)
	}
	before := m.summaryLocked(s)
	switch {
	case !uncertain, errors.Is(readErr, agentapi.ErrClosed):
		// Unchanged, or the next open applies the recorded mode.
	case readErr != nil:
		log.Warn("web permission mode read-back failed", "session", s.id, "error", readErr)
		s.modeUnknown = true
	default:
		s.modeUnknown = false
		switch {
		case on:
			s.mode = store.ModeAssisted
		case md != store.ModeAssisted:
			s.mode = md
		case s.mode == store.ModeAssisted:
			// Asked for assisted with the recorded mode unknown and found
			// it off: Safe, never Yolo, is what the runtime then does.
			s.mode = store.ModeSafe
		}
	}
	if string(s.mode) != before.Mode {
		s.fuses = nil
	}
	m.autoAllowPendingLocked(s)
	m.changedLocked(s, before)
	if uncertain && readErr == nil && on == (md == store.ModeAssisted) {
		return m.summaryLocked(s), nil
	}
	return SessionSummary{}, setErr
}

// setAssistedPermissions changes conv's runtime permission mode; a closed
// conversation gets the recorded mode at its next open. Uncertain reports
// that the runtime may have applied the change despite the error.
func setAssistedPermissions(ctx context.Context, id string, conv agentapi.Conversation, approvalModel string) (uncertain bool, err error) {
	setter, ok := conv.(agentapi.AssistedPermissionSetter)
	if !ok {
		return false, newError(http.StatusConflict, "this provider does not support assisted permissions")
	}
	err = setter.SetAssistedPermissions(ctx, approvalModel)
	switch {
	case err == nil, errors.Is(err, agentapi.ErrClosed):
		return false, nil
	case errors.Is(err, agentapi.ErrUnsupported):
		return false, newError(http.StatusConflict, "this runtime does not support assisted permissions")
	default:
		log.Warn("web permission mode change failed", "session", id, "error", err)
		return true, newError(http.StatusBadGateway, "could not change the permission mode: %s", shortError(err))
	}
}
