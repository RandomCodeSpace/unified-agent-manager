package web

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

func clampPlanReview(in *agentapi.PlanReview, body bool) *agentapi.PlanReview {
	if in == nil || !validToolCallID(in.RequestID) || in.RequestID == "" {
		return nil
	}
	plan := *in
	plan.Summary = strings.Clone(clampText(displaytext.Sanitize(plan.Summary), maxLabelText))
	plan.Actions = nil
	for _, action := range in.Actions {
		if action.Valid() && !slices.Contains(plan.Actions, action) {
			plan.Actions = append(plan.Actions, action)
		}
	}
	if !slices.Contains(plan.Actions, plan.Recommended) {
		plan.Recommended = ""
	}
	if body {
		plan.Truncated = plan.Truncated || len(plan.Content) > maxInteractionText
		plan.PreviousTruncated = plan.PreviousTruncated || len(plan.Previous) > maxInteractionText
		plan.Content = strings.Clone(clampText(displaytext.SanitizeText(plan.Content), maxInteractionText))
		plan.Previous = strings.Clone(clampText(displaytext.SanitizeText(plan.Previous), maxInteractionText))
	} else {
		plan.Content, plan.Previous = "", ""
	}
	return &plan
}

// PlanDraft reads only the current open conversation. Closed Tasks retain
// their exact journal reader but are never resumed for today's draft.
func (m *Manager) PlanDraft(ctx context.Context, id string) (agentapi.PlanDraft, error) {
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.PlanDraft{}, err
	}
	m.mu.Lock()
	conv := s.conv
	reader, ok := conv.(agentapi.PlanDraftReader)
	gen := s.gen
	m.mu.Unlock()
	if conv == nil {
		return agentapi.PlanDraft{}, newError(http.StatusConflict, "the current draft is unavailable while this Task is closed")
	}
	if !ok {
		return agentapi.PlanDraft{}, newError(http.StatusConflict, "this provider cannot read the current plan draft")
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	draft, err := reader.ReadPlanDraft(ctx)
	if err != nil {
		return agentapi.PlanDraft{}, newError(http.StatusBadGateway, "read current plan draft: %s", shortError(err))
	}
	m.mu.Lock()
	stale := s.removed || s.gen != gen || s.conv != conv
	m.mu.Unlock()
	if stale {
		return agentapi.PlanDraft{}, newError(http.StatusConflict, "the current plan draft changed conversations; open it again")
	}
	if !draft.Exists {
		draft.Content, draft.Truncated = "", false
	} else {
		draft.Truncated = draft.Truncated || len(draft.Content) > maxInteractionText
		draft.Content = strings.Clone(clampText(displaytext.SanitizeText(draft.Content), maxInteractionText))
	}
	return draft, nil
}

func (s *Server) handlePlanDraft(w http.ResponseWriter, r *http.Request) {
	draft, err := s.m.PlanDraft(r.Context(), r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, draft)
}

// PlanReview reads the exact native review without opening the conversation.
func (m *Manager) PlanReview(ctx context.Context, id, requestID string) (agentapi.PlanReview, error) {
	if requestID == "" || !validToolCallID(requestID) {
		return agentapi.PlanReview{}, newError(http.StatusBadRequest, "invalid plan review ID")
	}
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.PlanReview{}, err
	}
	m.mu.Lock()
	reader, ok := m.providers[s.provider].(agentapi.PlanHistoryReader)
	req := agentapi.ReadRequest{ConversationID: s.convID, Workdir: s.workdir}
	m.mu.Unlock()
	if !ok {
		return agentapi.PlanReview{}, newError(http.StatusConflict, "this provider cannot read native plan reviews")
	}
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	plan, err := reader.ReadPlanReview(ctx, req, requestID)
	if errors.Is(err, agentapi.ErrItemNotFound) {
		return agentapi.PlanReview{}, newError(http.StatusNotFound, "the reviewed plan is not available in the provider's record")
	}
	if err != nil {
		return agentapi.PlanReview{}, newError(http.StatusBadGateway, "read plan review: %s", shortError(err))
	}
	bounded := clampPlanReview(&plan, true)
	if bounded == nil || bounded.RequestID != requestID {
		return agentapi.PlanReview{}, newError(http.StatusBadGateway, "the provider returned a different plan review")
	}
	return *bounded, nil
}

func (s *Server) handlePlanReview(w http.ResponseWriter, r *http.Request) {
	plan, err := s.m.PlanReview(r.Context(), r.PathValue("id"), r.PathValue("rid"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// isPlanPath compares the exact provider-owned identity, never a basename.
func (s *webSession) isPlanPath(path string) bool {
	if s.planPath == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.workdir, path)
	}
	return filepath.Clean(path) == s.planPath
}

func (m *Manager) notePlanPathLocked(s *webSession, path string) {
	if !filepath.IsAbs(path) || len(path) > maxGrantPathBytes || !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		return
	}
	s.planPath = filepath.Clean(path)
	changed := false
	for edit := range s.edits {
		if s.isPlanPath(edit) {
			delete(s.edits, edit)
			changed = true
		}
	}
	if changed {
		m.kickDiffLocked(s)
	}
}
