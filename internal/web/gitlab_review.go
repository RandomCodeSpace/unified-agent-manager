package web

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const gitLabReviewBodyLimit = 16 << 10

type GitLabReviewAuthor struct {
	Name     string `json:"name"`
	Username string `json:"username"`
}

type GitLabReviewPosition struct {
	OldPath  string `json:"old_path"`
	NewPath  string `json:"new_path"`
	OldLine  *int   `json:"old_line,omitempty"`
	NewLine  *int   `json:"new_line,omitempty"`
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

type GitLabReviewNote struct {
	ID         int64                 `json:"id"`
	Body       string                `json:"body"`
	Author     GitLabReviewAuthor    `json:"author"`
	CreatedAt  string                `json:"created_at"`
	UpdatedAt  string                `json:"updated_at"`
	Resolvable bool                  `json:"resolvable"`
	Resolved   bool                  `json:"resolved"`
	System     bool                  `json:"system"`
	Position   *GitLabReviewPosition `json:"position,omitempty"`
}

type GitLabDiscussion struct {
	ID             string             `json:"id"`
	IndividualNote bool               `json:"individual_note"`
	Notes          []GitLabReviewNote `json:"notes"`
}

type GitLabReviewMergeRequest struct {
	GitLabMergeRequest
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	HeadSHA      string `json:"head_sha"`
}

type GitLabReview struct {
	MergeRequest GitLabReviewMergeRequest `json:"merge_request"`
	Discussions  []GitLabDiscussion       `json:"discussions"`
	Truncated    bool                     `json:"truncated"`
	CheckedAt    time.Time                `json:"checked_at"`
	key          string
	gate         glabGate
	projectID    int64
	mrID         int64
	headSHA      string
}

type gitLabReviewMRResponse struct {
	GitLabReviewMergeRequest
	ID              int64  `json:"id"`
	ProjectID       int64  `json:"project_id"`
	SourceProjectID int64  `json:"source_project_id"`
	TargetProjectID int64  `json:"target_project_id"`
	SHA             string `json:"sha"`
}

func (m *Manager) gitLabReview(ctx context.Context, id, sha string, iid int64) (GitLabReview, error) {
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	out, err := m.gitLabReviewLocked(ctx, id, sha, iid)
	if err == nil {
		err = m.gitLabStillSelected(ctx, out.key, id)
	}
	return out, err
}

// The caller holds gitlab.work so all review requests share the host backoff.
func (m *Manager) gitLabReviewLocked(ctx context.Context, id, sha string, iid int64) (out GitLabReview, err error) {
	out.Discussions = []GitLabDiscussion{}
	if iid <= 0 || !validGitLabSHA(sha) {
		return out, newError(http.StatusBadRequest, "invalid merge request or commit")
	}
	key, rec, gate, _, err := m.gitLabSelection(ctx, id)
	if err != nil {
		return out, err
	}
	if key == "" || sha != rec.SHA {
		return out, newError(http.StatusConflict, "the selected UAM commit changed; refresh GitLab")
	}
	if err = m.gitLabBackoff(gate.Host); err != nil {
		return out, err
	}
	defer func() {
		m.gitLabRememberBackoff(gate.Host, err)
		err = gitLabReviewError(err)
	}()
	out.key, out.gate = key, gate
	var project struct {
		ID   int64  `json:"id"`
		Path string `json:"path_with_namespace"`
	}
	if _, err = gitLabGetJSON(ctx, gate, "projects/"+url.PathEscape(gate.Path), &project); err != nil {
		return out, err
	}
	if project.ID <= 0 || !strings.EqualFold(project.Path, gate.Path) {
		return out, newError(http.StatusConflict, "GitLab returned a different project than the repository origin")
	}
	out.projectID = project.ID
	base := fmt.Sprintf("projects/%d", project.ID)
	// IIDs belong to the target project. Match the global ID as well, so an
	// association with a fork's same-numbered MR cannot select a different MR.
	for page := 1; page <= gitLabMaxItems/gitLabPageSize; page++ {
		var batch []gitLabReviewMRResponse
		endpoint := fmt.Sprintf("%s/repository/commits/%s/merge_requests?per_page=%d&page=%d", base, sha, gitLabPageSize, page)
		if _, err = gitLabGetJSON(ctx, gate, endpoint, &batch); err != nil {
			return out, err
		}
		if len(batch) > gitLabPageSize {
			return out, errors.New("GitLab returned an oversized merge request list")
		}
		for _, mr := range batch {
			if mr.IID == iid && mr.ID > 0 && gitLabReviewMatches(mr, project.ID, rec.Branch) {
				out.mrID = mr.ID
				break
			}
		}
		if out.mrID > 0 || len(batch) < gitLabPageSize {
			break
		}
	}
	if out.mrID == 0 {
		return out, newError(http.StatusConflict, "the merge request is not associated with this UAM commit and project")
	}
	var mr gitLabReviewMRResponse
	base += fmt.Sprintf("/merge_requests/%d", iid)
	if _, err = gitLabGetJSON(ctx, gate, base, &mr); err != nil {
		return out, err
	}
	if mr.ID != out.mrID || mr.IID != iid || !gitLabReviewMatches(mr, project.ID, rec.Branch) || !gitLabValidSHA(mr.SHA) {
		return out, newError(http.StatusConflict, "the merge request changed; refresh GitLab")
	}
	out.headSHA = mr.SHA
	out.MergeRequest = mr.GitLabReviewMergeRequest
	out.MergeRequest.HeadSHA = mr.SHA
	out.MergeRequest.Title = displaytext.Sanitize(mr.Title)
	out.MergeRequest.WebURL = gitLabSafeURL(gate.Host, mr.WebURL)
	out.MergeRequest.SourceBranch = displaytext.Sanitize(mr.SourceBranch)
	out.MergeRequest.TargetBranch = displaytext.Sanitize(mr.TargetBranch)
	notes := 0
	seen := make(map[string]bool)
	for page := 1; page <= gitLabMaxItems/gitLabPageSize; page++ {
		var batch []GitLabDiscussion
		var headers http.Header
		headers, err = gitLabGetJSON(ctx, gate, fmt.Sprintf("%s/discussions?per_page=%d&page=%d", base, gitLabPageSize, page), &batch)
		if err != nil {
			return out, err
		}
		if len(batch) > gitLabPageSize {
			return out, errors.New("GitLab returned an oversized discussion list")
		}
		for _, discussion := range batch {
			if !gitLabValidDiscussionID(discussion.ID) || seen[discussion.ID] {
				return out, errors.New("GitLab returned inconsistent discussions")
			}
			seen[discussion.ID] = true
			if notes == gitLabMaxItems {
				out.Truncated = true
				break
			}
			if len(discussion.Notes) > gitLabMaxItems-notes {
				discussion.Notes = discussion.Notes[:gitLabMaxItems-notes]
				out.Truncated = true
			}
			notes += len(discussion.Notes)
			for i := range discussion.Notes {
				note := &discussion.Notes[i]
				if note.ID <= 0 {
					return out, errors.New("GitLab returned an invalid discussion note")
				}
				out.Truncated = gitLabSanitizeReviewNote(note) || out.Truncated
			}
			out.Discussions = append(out.Discussions, discussion)
		}
		if len(batch) < gitLabPageSize {
			break
		}
		if notes == gitLabMaxItems || page == gitLabMaxItems/gitLabPageSize {
			out.Truncated = out.Truncated || headers.Get("X-Next-Page") != "" || len(batch) == gitLabPageSize
			break
		}
	}
	out.CheckedAt = m.now()
	return out, nil
}

func gitLabReviewMatches(mr gitLabReviewMRResponse, projectID int64, branch string) bool {
	return mr.ProjectID == projectID && mr.TargetProjectID == projectID && mr.SourceProjectID == projectID && mr.SourceBranch == branch
}

func gitLabSanitizeReviewNote(note *GitLabReviewNote) bool {
	note.Body = displaytext.SanitizeText(note.Body)
	truncated := len(note.Body) > gitLabReviewBodyLimit
	if truncated {
		note.Body = clampText(note.Body, gitLabReviewBodyLimit-len(truncatedMarker))
	}
	note.Author.Name, note.Author.Username = displaytext.Sanitize(note.Author.Name), displaytext.Sanitize(note.Author.Username)
	note.CreatedAt, note.UpdatedAt = displaytext.Sanitize(note.CreatedAt), displaytext.Sanitize(note.UpdatedAt)
	if p := note.Position; p != nil {
		p.OldPath, p.NewPath = displaytext.Sanitize(p.OldPath), displaytext.Sanitize(p.NewPath)
		p.BaseSHA, p.HeadSHA, p.StartSHA = displaytext.Sanitize(p.BaseSHA), displaytext.Sanitize(p.HeadSHA), displaytext.Sanitize(p.StartSHA)
	}
	return truncated
}

func gitLabValidDiscussionID(id string) bool {
	if len(id) != 40 && len(id) != 64 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func gitLabFindDiscussion(review GitLabReview, id string) (GitLabDiscussion, error) {
	if !gitLabValidDiscussionID(id) {
		return GitLabDiscussion{}, newError(http.StatusBadRequest, "invalid discussion ID")
	}
	for _, discussion := range review.Discussions {
		if discussion.ID == id {
			return discussion, nil
		}
	}
	return GitLabDiscussion{}, newError(http.StatusNotFound, "the discussion is not available in this merge request; refresh GitLab")
}

// Unknown write outcomes must never be retried automatically: GitLab may have
// created the note even when the CLI loses its connection or its response.
type gitLabWriteUncertain struct{}

func (*gitLabWriteUncertain) Error() string {
	return "GitLab may have received this change. Refresh the discussion and check it before trying again."
}

func gitLabWriteJSON(ctx context.Context, gate glabGate, endpoint, method, field string, into any) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// --raw-field treats @file, booleans, and braces literally; --field does not.
	args := []string{"api", endpoint, "--hostname", gate.Host, "--method", method, "--include", "--raw-field", field}
	out, runErr := glabExec(ctx, glabTimeout, gate.Dir, gate.executable, false, args...)
	status, headers, body, parseErr := gitLabResponse(out)
	if parseErr == nil && status >= 400 && status < 500 {
		return gitLabResponseError(status, headers)
	}
	if runErr != nil || parseErr != nil || status < 200 || status >= 300 || json.Unmarshal([]byte(body), into) != nil {
		return &gitLabWriteUncertain{}
	}
	return nil
}

func gitLabReviewError(err error) error {
	var apiErr *gitLabAPIError
	if errors.As(err, &apiErr) {
		status := apiErr.StatusCode
		if status < 400 || status >= 500 {
			status = http.StatusBadGateway
		}
		return newError(status, "%s", apiErr)
	}
	return err
}

func (m *Manager) gitLabReviewStillCurrent(ctx context.Context, id string, review GitLabReview) error {
	if err := m.gitLabStillSelected(ctx, review.key, id); err != nil {
		return err
	}
	var mr gitLabReviewMRResponse
	_, err := gitLabGetJSON(ctx, review.gate, fmt.Sprintf("projects/%d/merge_requests/%d", review.projectID, review.MergeRequest.IID), &mr)
	m.gitLabRememberBackoff(review.gate.Host, err)
	if err != nil {
		return gitLabReviewError(err)
	}
	if mr.ID != review.mrID || mr.IID != review.MergeRequest.IID || mr.SHA != review.headSHA || !gitLabReviewMatches(mr, review.projectID, review.MergeRequest.SourceBranch) {
		return newError(http.StatusConflict, "the merge request changed; refresh GitLab before posting")
	}
	return m.gitLabStillSelected(ctx, review.key, id)
}

// Take the lifecycle guard after remote reads: a Task may have been archived
// while GitLab was responding. Hold op until the write finishes so its stage
// cannot change between the final check and the mutation.
func (m *Manager) gitLabReviewWriteLock(ctx context.Context, id string, review GitLabReview) (func(), error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	s.op.Lock()
	m.mu.Lock()
	err = s.readOnlyLocked()
	if err == nil && (s.removed || m.closed) {
		err = newError(http.StatusConflict, "the Task is no longer available")
	}
	m.mu.Unlock()
	if err == nil {
		err = m.gitLabReviewStillCurrent(ctx, id, review)
	}
	if err != nil {
		s.op.Unlock()
		return nil, err
	}
	return s.op.Unlock, nil
}

func (m *Manager) gitLabComment(ctx context.Context, id, sha string, iid int64, discussionID, body string) (int64, error) {
	if !utf8.ValidString(body) || len(body) > gitLabReviewBodyLimit {
		return 0, newError(http.StatusBadRequest, "comments must be valid text and no larger than 16 KiB")
	}
	body = strings.TrimSpace(displaytext.SanitizeText(body))
	if body == "" {
		return 0, newError(http.StatusBadRequest, "write a comment first")
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 1 && line[0] == '/' && ((line[1] >= 'a' && line[1] <= 'z') || (line[1] >= 'A' && line[1] <= 'Z')) {
			return 0, newError(http.StatusBadRequest, "GitLab quick actions are not supported here; post commands in GitLab")
		}
	}
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	review, err := m.gitLabReviewLocked(ctx, id, sha, iid)
	if err != nil {
		return 0, err
	}
	endpoint := fmt.Sprintf("projects/%d/merge_requests/%d", review.projectID, iid)
	if discussionID != "" {
		discussion, err := gitLabFindDiscussion(review, discussionID)
		if err != nil {
			return 0, err
		}
		if len(discussion.Notes) == 0 || discussion.Notes[0].System {
			return 0, newError(http.StatusConflict, "system notes cannot receive replies")
		}
		endpoint += "/discussions/" + discussionID
	}
	unlock, err := m.gitLabReviewWriteLock(ctx, id, review)
	if err != nil {
		return 0, err
	}
	defer unlock()
	var note struct {
		ID int64 `json:"id"`
	}
	err = gitLabWriteJSON(ctx, review.gate, endpoint+"/notes", "POST", "body="+body, &note)
	m.gitLabRememberBackoff(review.gate.Host, err)
	if err == nil && note.ID <= 0 {
		err = &gitLabWriteUncertain{}
	}
	return note.ID, gitLabReviewError(err)
}

func (m *Manager) gitLabResolve(ctx context.Context, id, sha string, iid int64, discussionID string, resolved bool) error {
	m.gitlab.work.Lock()
	defer m.gitlab.work.Unlock()
	review, err := m.gitLabReviewLocked(ctx, id, sha, iid)
	if err != nil {
		return err
	}
	discussion, err := gitLabFindDiscussion(review, discussionID)
	if err != nil {
		return err
	}
	resolvable := false
	for _, note := range discussion.Notes {
		resolvable = resolvable || (note.Resolvable && !note.System)
	}
	if !resolvable {
		return newError(http.StatusConflict, "this discussion cannot be resolved")
	}
	unlock, err := m.gitLabReviewWriteLock(ctx, id, review)
	if err != nil {
		return err
	}
	defer unlock()
	var updated GitLabDiscussion
	endpoint := fmt.Sprintf("projects/%d/merge_requests/%d/discussions/%s", review.projectID, iid, discussionID)
	err = gitLabWriteJSON(ctx, review.gate, endpoint, "PUT", "resolved="+strconv.FormatBool(resolved), &updated)
	m.gitLabRememberBackoff(review.gate.Host, err)
	if err == nil {
		confirmed := false
		for _, note := range updated.Notes {
			if note.Resolvable && !note.System {
				confirmed = true
				if note.Resolved != resolved {
					return &gitLabWriteUncertain{}
				}
			}
		}
		if updated.ID != discussionID || !confirmed {
			err = &gitLabWriteUncertain{}
		}
	}
	return gitLabReviewError(err)
}

func gitLabReviewIID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	iid, err := strconv.ParseInt(r.PathValue("iid"), 10, 64)
	if err != nil || iid <= 0 {
		writeError(w, http.StatusBadRequest, "invalid merge request ID")
		return 0, false
	}
	return iid, true
}

func writeGitLabReviewFailure(w http.ResponseWriter, err error) {
	var uncertain *gitLabWriteUncertain
	if errors.As(err, &uncertain) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": uncertain.Error(), "outcome_unknown": true})
		return
	}
	writeFailure(w, err)
}

func (s *Server) handleGitLabReview(w http.ResponseWriter, r *http.Request) {
	iid, ok := gitLabReviewIID(w, r)
	if !ok {
		return
	}
	out, err := s.m.gitLabReview(r.Context(), r.PathValue("id"), r.URL.Query().Get("commit_sha"), iid)
	if err != nil {
		writeGitLabReviewFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGitLabComment(w http.ResponseWriter, r *http.Request) {
	iid, ok := gitLabReviewIID(w, r)
	if !ok {
		return
	}
	var body struct {
		SHA          string `json:"commit_sha"`
		DiscussionID string `json:"discussion_id"`
		Body         string `json:"body"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	noteID, err := s.m.gitLabComment(r.Context(), r.PathValue("id"), body.SHA, iid, body.DiscussionID, body.Body)
	if err != nil {
		writeGitLabReviewFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"note_id": noteID, "discussion_id": body.DiscussionID})
}

func (s *Server) handleGitLabResolve(w http.ResponseWriter, r *http.Request) {
	iid, ok := gitLabReviewIID(w, r)
	if !ok {
		return
	}
	var body struct {
		SHA          string `json:"commit_sha"`
		DiscussionID string `json:"discussion_id"`
		Resolved     *bool  `json:"resolved"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Resolved == nil {
		writeError(w, http.StatusBadRequest, "choose whether to resolve or reopen the thread")
		return
	}
	if err := s.m.gitLabResolve(r.Context(), r.PathValue("id"), body.SHA, iid, body.DiscussionID, *body.Resolved); err != nil {
		writeGitLabReviewFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"resolved": *body.Resolved})
}
