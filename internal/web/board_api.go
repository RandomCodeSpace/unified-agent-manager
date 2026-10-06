package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// boardRoutes registers the planner's routes (ADR 0005 §14).
func (s *Server) boardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/board", s.handleBoard)
	mux.HandleFunc("GET /api/board/projects/{id}", s.handleBoardProject)
	mux.HandleFunc("PATCH /api/board/projects/{id}", s.handleUpdateBoardProject)
	mux.HandleFunc("POST /api/board/cards", s.handleCreateCard)
	mux.HandleFunc("GET /api/board/cards/{ref}", s.handleCard)
	mux.HandleFunc("PATCH /api/board/cards/{ref}", s.handleEditCard)
	mux.HandleFunc("POST /api/board/cards/{ref}/confirm", cardAction(s, func(ctx context.Context, st *board.Store, a board.Actor, ref string, _ struct{}) (board.Card, error) {
		return st.Confirm(ctx, a, ref)
	}))
	mux.HandleFunc("POST /api/board/cards/{ref}/approve", s.handleApprove)
	mux.HandleFunc("POST /api/board/cards/{ref}/dismiss", cardAction(s, func(ctx context.Context, st *board.Store, a board.Actor, ref string, _ struct{}) (board.Card, error) {
		return st.Dismiss(ctx, a, ref)
	}))
	mux.HandleFunc("POST /api/board/cards/{ref}/move", cardAction(s, func(ctx context.Context, st *board.Store, a board.Actor, ref string, body moveBody) (board.Card, error) {
		p := board.Patch{Rank: body.Rank}
		if body.ParentID != nil {
			parent, err := optionalRef("parent_id", body.ParentID)
			if err != nil {
				return board.Card{}, err
			}
			p.ParentID = &parent
		}
		res, err := st.Edit(ctx, a, ref, p)
		return res.Card, err
	}))
	mux.HandleFunc("POST /api/board/cards/{ref}/status", cardAction(s, func(ctx context.Context, st *board.Store, a board.Actor, ref string, body statusBody) (board.Card, error) {
		return st.SetStatus(ctx, a, ref, body.Status, body.Comment, body.Force)
	}))
	mux.HandleFunc("POST /api/board/cards/{ref}/restore", cardAction(s, func(ctx context.Context, st *board.Store, a board.Actor, ref string, body commentBody) (board.Card, error) {
		return st.Restore(ctx, a, ref, body.Comment)
	}))
	mux.HandleFunc("POST /api/board/cards/{ref}/split", cardAction(s, func(ctx context.Context, st *board.Store, a board.Actor, ref string, body splitBody) (board.Card, error) {
		res, err := st.Split(ctx, a, ref, body.Children)
		return res.Card, err
	}))
	mux.HandleFunc("POST /api/board/cards/{ref}/release", s.handleRelease)
	mux.HandleFunc("POST /api/board/cards/{ref}/comments", s.handleCommentCard)
	mux.HandleFunc("POST /api/board/cards/{ref}/launch", s.handleLaunch)
	mux.HandleFunc("POST /api/board/cards/{ref}/attach", s.handleAttach)
	mux.HandleFunc("POST /api/board/cards/{ref}/plan", s.handlePlan)
	mux.HandleFunc("POST /api/board/cards/{ref}/check", s.handleCheckCard)
	mux.HandleFunc("POST /api/board/cards/{ref}/triage", s.handleTriageCard)
	mux.HandleFunc("POST /api/board/cards/{ref}/suggest", s.handleSuggest)
	mux.HandleFunc("POST /api/board/links", s.handleLink)
	mux.HandleFunc("DELETE /api/board/links", s.handleUnlink)
	mux.HandleFunc("POST /api/board/requests/{id}/accept", s.handleAcceptRequest)
	mux.HandleFunc("POST /api/board/requests/{id}/reject", s.handleRejectRequest)
	mux.HandleFunc("POST /api/board/purge", s.handlePurge)
	mux.HandleFunc("POST /api/board/import", s.handleImportBoard)
}

type commentBody struct {
	Comment string `json:"comment"`
}

type moveBody struct {
	// ParentID is a card ref; null or "" is the root.
	ParentID json.RawMessage `json:"parent_id"`
	Rank     *int            `json:"rank"`
}

type statusBody struct {
	Status  board.Status `json:"status"`
	Comment string       `json:"comment"`
	Force   bool         `json:"force"`
}

type splitBody struct {
	Children []board.SplitChild `json:"children"`
}

// cardAction serves an owner write to the card {ref} that answers with the
// card. Its body decodes into a B and may be empty.
func cardAction[B any](s *Server, act func(context.Context, *board.Store, board.Actor, string, B) (board.Card, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body B
		if !decodeOptionalBody(w, r, &body) {
			return
		}
		ref := r.PathValue("ref")
		var c board.Card
		err := s.m.boardWrite(ref, func(ctx context.Context, st *board.Store, a board.Actor) error {
			var err error
			c, err = act(ctx, st, a, ref, body)
			return err
		})
		if err != nil {
			writeFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, boardCard(c))
	}
}

// decodeOptionalBody is decodeBody for a body that may be empty, which
// leaves v as it is.
func decodeOptionalBody(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body is too large")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return true
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return decodeBody(w, r, v)
}

// optionalRef reads a card ref that may be null, which is "".
func optionalRef(name string, raw json.RawMessage) (string, error) {
	if string(raw) == "null" {
		return "", nil
	}
	var ref string
	if json.Unmarshal(raw, &ref) != nil {
		return "", invalidBoard("%s must be a card id, a #number or null", name)
	}
	return strings.TrimSpace(ref), nil
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	project := r.URL.Query().Get("project_id")
	if project == "" {
		writeFailure(w, invalidBoard("project_id is required: a project id or %q", unassignedBoard))
		return
	}
	snap, err := s.m.Board(project)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleBoardProject(w http.ResponseWriter, r *http.Request) {
	p, err := s.m.BoardProject(r.PathValue("id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// handleUpdateBoardProject changes a Project's planner settings: any of
// accept_cmd ("" means none), base_ref and accept_parallel (1 to 4).
func (s *Server) handleUpdateBoardProject(w http.ResponseWriter, r *http.Request) {
	var body BoardProjectPatch
	if !decodeBody(w, r, &body) {
		return
	}
	p, err := s.m.SetBoardProject(r.PathValue("id"), body)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleCreateCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID    string        `json:"project_id"`
		Kind         board.Kind    `json:"kind"`
		ParentID     *string       `json:"parent_id"`
		Title        string        `json:"title"`
		Desc         string        `json:"desc"`
		WinCondition string        `json:"win_condition"`
		Prio         int           `json:"prio"`
		Effort       string        `json:"effort"`
		Due          string        `json:"due"`
		Labels       []string      `json:"labels"`
		Checklist    []board.Check `json:"checklist"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	in := board.NewCard{ProjectID: body.ProjectID, Kind: body.Kind, Title: body.Title, Desc: body.Desc, WinCondition: body.WinCondition,
		Prio: body.Prio, Effort: body.Effort, Due: body.Due, Labels: body.Labels, Checklist: body.Checklist}
	if body.ParentID != nil {
		in.ParentID = strings.TrimSpace(*body.ParentID)
	}
	c, err := s.m.CreateCard(in)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleCard(w http.ResponseWriter, r *http.Request) {
	d, err := s.m.CardDetail(r.PathValue("ref"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// cardPatchBody is the owner's edit. accept_cmd is null to inherit the
// Project default, "" for none, or a command; parent_id is null or "" for
// the root; paused pauses or resumes a card under an approved epic.
type cardPatchBody struct {
	Title        *string         `json:"title"`
	Desc         *string         `json:"desc"`
	WinCondition *string         `json:"win_condition"`
	Prio         *int            `json:"prio"`
	Due          *string         `json:"due"`
	Effort       *string         `json:"effort"`
	Labels       *[]string       `json:"labels"`
	Checklist    *[]board.Check  `json:"checklist"`
	ParentID     json.RawMessage `json:"parent_id"`
	Rank         *int            `json:"rank"`
	Blocked      *bool           `json:"blocked"`
	Paused       *bool           `json:"paused"`
	AcceptCmd    json.RawMessage `json:"accept_cmd"`
	Paths        *[]string       `json:"paths"`
	ProjectID    *string         `json:"project_id"`
}

func (b cardPatchBody) patch() (board.Patch, error) {
	p := board.Patch{Title: b.Title, Desc: b.Desc, WinCondition: b.WinCondition, Prio: b.Prio, Due: b.Due, Effort: b.Effort,
		Labels: b.Labels, Checklist: b.Checklist, Rank: b.Rank, Blocked: b.Blocked, Paused: b.Paused, Paths: b.Paths, ProjectID: b.ProjectID}
	if b.ParentID != nil {
		parent, err := optionalRef("parent_id", b.ParentID)
		if err != nil {
			return p, err
		}
		p.ParentID = &parent
	}
	if b.AcceptCmd != nil {
		var cmd sql.NullString
		if string(b.AcceptCmd) != "null" {
			if json.Unmarshal(b.AcceptCmd, &cmd.String) != nil {
				return p, invalidBoard("accept_cmd must be null, \"\" or a command")
			}
			cmd.Valid = true
		}
		p.AcceptCmd = &cmd
	}
	return p, nil
}

func (s *Server) handleEditCard(w http.ResponseWriter, r *http.Request) {
	var body cardPatchBody
	if !decodeBody(w, r, &body) {
		return
	}
	p, err := body.patch()
	if err == nil {
		var c BoardCard
		if c, err = s.m.EditCard(r.PathValue("ref"), p); err == nil {
			writeJSON(w, http.StatusOK, c)
			return
		}
	}
	writeFailure(w, err)
}

// handleApprove approves an epic (ADR 0006 §7): 200 with the epic and its
// run.
func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	var req ApproveRequest
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := s.m.ApproveCard(r.PathValue("ref"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleCommentCard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Body string `json:"body"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	c, err := s.m.CommentCard(r.PathValue("ref"), body.Body)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, c)
}

func (s *Server) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req LaunchRequest
	if !decodeOptionalBody(w, r, &req) {
		return
	}
	c, summary, err := s.m.Launch(r.PathValue("ref"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		Card    BoardCard      `json:"card"`
		Session SessionSummary `json:"session"`
	}{c, summary})
}

// handleAttach makes an existing Task work on the card: 200 with the held subtask.
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	var req AttachRequest
	if !decodeBody(w, r, &req) {
		return
	}
	c, err := s.m.AttachTask(r.PathValue("ref"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	var req LaunchRequest
	if !decodeOptionalBody(w, r, &req) {
		return
	}
	summary, err := s.m.Plan(r.PathValue("ref"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]SessionSummary{"session": summary})
}

// handleCheckCard starts Check at HEAD: 202 {job_id}, then board_job frames,
// the last carrying the run.
func (s *Server) handleCheckCard(w http.ResponseWriter, r *http.Request) {
	id, err := s.m.CheckCard(r.PathValue("ref"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

func (s *Server) handleTriageCard(w http.ResponseWriter, r *http.Request) {
	t, err := s.m.TriageCard(r.Context(), r.PathValue("ref"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleSuggest starts a suggestion job: 202 {job_id}, then board_job frames.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request) {
	var req SuggestRequest
	if !decodeOptionalBody(w, r, &req) {
		return
	}
	id, err := s.m.Suggest(r.PathValue("ref"), req)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job_id": id})
}

func (s *Server) handleImportBoard(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Dir string `json:"dir"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	report, err := s.m.ImportBoard(r.Context(), body.Dir)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) handleLink(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Blocker string `json:"blocker"`
		Blocked string `json:"blocked"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.link(w, body.Blocker, body.Blocked, true)
}

func (s *Server) handleUnlink(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.link(w, q.Get("blocker"), q.Get("blocked"), false)
}

func (s *Server) link(w http.ResponseWriter, blocker, blocked string, link bool) {
	if strings.TrimSpace(blocker) == "" || strings.TrimSpace(blocked) == "" {
		writeFailure(w, invalidBoard("blocker and blocked are required"))
		return
	}
	writeNoContent(w, s.m.LinkCards(blocker, blocked, link))
}

// handleAcceptRequest accepts a request: 200 with it, or, for a lane's
// done request, 202 {job_id}, then board_job frames as it lands.
func (s *Server) handleAcceptRequest(w http.ResponseWriter, r *http.Request) {
	var body commentBody
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	req, job, err := s.m.AcceptRequest(r.PathValue("id"), body.Comment)
	switch {
	case err != nil:
		writeFailure(w, err)
	case job != "":
		writeJSON(w, http.StatusAccepted, map[string]string{"job_id": job})
	default:
		writeJSON(w, http.StatusOK, req)
	}
}

// handleRelease is the owner's Release of a held subtask; on a lane's it is
// Stop. It answers with the card.
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	var body commentBody
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	c, err := s.m.ReleaseCard(r.PathValue("ref"), body.Comment)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) handleRejectRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	req, err := s.m.RejectRequest(r.PathValue("id"), body.Reason)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, req)
}

func (s *Server) handlePurge(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectID string `json:"project_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.ProjectID == "" {
		writeFailure(w, invalidBoard("project_id is required"))
		return
	}
	n, err := s.m.PurgeBoard(body.ProjectID)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"purged": n})
}

// handleSettle settles a Task. Its optional body decides the subtasks the
// Task holds: {"holds": {"<card id>": {"action": "keep|release|cancel",
// "comment": ""}}}.
func (s *Server) handleSettle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Holds map[string]HoldDecision `json:"holds"`
	}
	if !decodeOptionalBody(w, r, &body) {
		return
	}
	summary, err := s.m.SettleHolds(r.PathValue("id"), body.Holds)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}
