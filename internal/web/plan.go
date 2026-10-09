package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// The provider's journal may not record a review's plan, so each review
// the provider shows keeps its bounded snapshot in an append-only file
// beside the Task's uploads, one planReviewRecord a line, under the review
// ID the browser reads. A transcript receipt names the provider's request
// ID instead; an alias line maps it to the review.
const (
	planReviewsFile = "plan-reviews.jsonl"
	// Past maxPlanReviewsBytes flush rewrites the file with at most
	// keptPlanReviewsBytes of its newest lines.
	maxPlanReviewsBytes  = 8 << 20
	keptPlanReviewsBytes = 4 << 20
)

type planReviewRecord struct {
	ID       string               `json:"id"`
	NativeID string               `json:"native_id,omitempty"`
	Plan     *agentapi.PlanReview `json:"plan,omitempty"`
}

type keptPlanReviews struct {
	s       *webSession
	records []planReviewRecord
}

// keepPlanReviewLocked queues the snapshot of a review that just arrived,
// with its body, for flush to append.
func (m *Manager) keepPlanReviewLocked(s *webSession, ix agentapi.Interaction) {
	if ix.Kind != agentapi.InteractionPlanReview || ix.State != agentapi.InteractionPending || ix.Plan == nil {
		return
	}
	plan := *ix.Plan
	plan.Actions = slices.Clone(plan.Actions)
	s.planRecords = append(s.planRecords, planReviewRecord{ID: ix.ID, Plan: &plan})
	m.dirty[s.id] = struct{}{}
}

// notePlanAliasLocked queues the provider's request ID of a live receipt
// that names the review it decided.
func (m *Manager) notePlanAliasLocked(s *webSession, it agentapi.Item) {
	native, ok := strings.CutPrefix(it.ID, "plan-")
	if !ok || it.Plan == nil || native == "" || it.Plan.RequestID == "" || it.Plan.RequestID == native || !validToolCallID(native) {
		return
	}
	s.planRecords = append(s.planRecords, planReviewRecord{ID: it.Plan.RequestID, NativeID: native})
	m.dirty[s.id] = struct{}{}
}

// appendPlanReviews appends a Task's queued records outside mu, like
// appendTurnTodos.
func (m *Manager) appendPlanReviews(k keptPlanReviews) {
	dir := m.taskUploadDir(k.s.id)
	var data []byte
	for _, r := range k.records {
		if line, err := json.Marshal(r); err == nil {
			data = append(append(data, line...), '\n')
		}
	}
	path := filepath.Join(dir, planReviewsFile)
	size, err := appendPrivate(m.uploadRoot(), dir, path, data)
	if err == nil && size > maxPlanReviewsBytes {
		err = compactPlanReviews(path)
	}
	if err != nil {
		log.Warn("keep the plan review failed", "session", k.s.id, "error", err)
	}
	m.mu.Lock()
	k.s.planRecords = slices.Delete(k.s.planRecords, 0, len(k.records))
	removed := k.s.removed
	m.mu.Unlock()
	if removed {
		removeUploads(dir)
	}
}

func compactPlanReviews(path string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- UAM's own file.
	if err != nil {
		return err
	}
	lines := slices.Collect(bytes.Lines(data))
	size, from := 0, len(lines)
	for from > 0 && size+len(lines[from-1]) <= keptPlanReviewsBytes {
		from--
		size += len(lines[from])
	}
	return writeFileAtomic(path, ".plan-reviews-*", bytes.Join(lines[from:], nil))
}

// keptPlanReview returns the snapshot UAM kept for requestID, a review ID
// or a provider request ID, queued or appended; the last line wins.
func (m *Manager) keptPlanReview(s *webSession, requestID string) (agentapi.PlanReview, bool) {
	m.mu.Lock()
	queued := slices.Clone(s.planRecords)
	path := filepath.Join(m.taskUploadDir(s.id), planReviewsFile)
	m.mu.Unlock()
	var records []planReviewRecord
	if data, err := os.ReadFile(path); err == nil { // #nosec G304 -- UAM's own file.
		for line := range bytes.Lines(data) {
			var r planReviewRecord
			if bytes.HasSuffix(line, []byte("\n")) && json.Unmarshal(line, &r) == nil {
				records = append(records, r)
			}
		}
	}
	plans, aliases := map[string]*agentapi.PlanReview{}, map[string]string{}
	for _, r := range append(records, queued...) {
		if r.Plan != nil {
			plans[r.ID] = r.Plan
		}
		if r.NativeID != "" {
			aliases[r.NativeID] = r.ID
		}
	}
	plan := plans[requestID]
	if plan == nil {
		plan = plans[aliases[requestID]]
	}
	if plan == nil {
		return agentapi.PlanReview{}, false
	}
	bounded := clampPlanReview(plan, true)
	if bounded == nil {
		return agentapi.PlanReview{}, false
	}
	bounded.RequestID = requestID
	return *bounded, true
}

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

// PlanReview reads a reviewed plan snapshot without opening the
// conversation: the one UAM kept for the review, else the provider's record.
func (m *Manager) PlanReview(ctx context.Context, id, requestID string) (agentapi.PlanReview, error) {
	if requestID == "" || !validToolCallID(requestID) {
		return agentapi.PlanReview{}, newError(http.StatusBadRequest, "invalid plan review ID")
	}
	s, err := m.lookup(id)
	if err != nil {
		return agentapi.PlanReview{}, err
	}
	if plan, ok := m.keptPlanReview(s, requestID); ok {
		return plan, nil
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
	if path = filepath.Clean(path); path != s.planPath {
		// The provider filters the scratch file from its own diff, so a
		// total read before this identity is stale.
		s.invalidateNativeDiff()
	}
	s.planPath = path
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
