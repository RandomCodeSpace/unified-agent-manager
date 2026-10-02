package web

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/board"
)

// An existing Task joins a story from its strip: the owner's attach creates
// a subtask named after the Task and holds it for that Task, through the
// confirm step under a proposal, and sends the Task no prompt.
func TestPlannerAttachAnExistingTask(t *testing.T) {
	f := newPlanner(t)
	epic := f.create(board.KindEpic, "", "Epic")
	story := f.create(board.KindStory, epic.ID, "Story")
	task, err := f.m.Create(CreateRequest{Provider: "fake", ProjectID: f.project, Name: "Fix the flaky test"})
	if err != nil {
		t.Fatal(err)
	}
	body := func(extra string) string { return fmt.Sprintf(`{"task_id":%q%s}`, task.ID, extra) }

	var held BoardCard
	f.call(http.MethodPost, "/api/board/cards/"+story.ID+"/attach", body(""), http.StatusOK, &held)
	if held.Kind != board.KindSubtask || held.ParentID == nil || *held.ParentID != story.ID || held.Title != "Fix the flaky test" ||
		held.HeldBy != task.ID || held.WorkedBy != task.ID || held.Status != board.StatusDoing {
		t.Fatalf("attached %+v", held)
	}
	d := f.card(held.ID)
	if len(d.Holds) != 1 || d.Holds[0].TaskID != task.ID || d.Holds[0].BaselineHead != gitOutput(t, f.repo, "rev-parse", "HEAD") {
		t.Fatalf("holds = %+v", d.Holds)
	}
	for _, c := range f.ts.prov.Conversations() {
		if c.Request().SessionID == task.ID && len(c.Sends()) > 0 {
			t.Fatalf("attach sent the task %q", c.Sends())
		}
	}
	// One subtask at a time, and only Tasks of the card's Project.
	f.refused(http.MethodPost, "/api/board/cards/"+story.ID+"/attach", body(""), http.StatusConflict, string(board.CodeLimit))
	other, err := f.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, f.m, branchRepo(t))})
	if err != nil {
		t.Fatal(err)
	}
	f.refused(http.MethodPost, "/api/board/cards/"+story.ID+"/attach", fmt.Sprintf(`{"task_id":%q}`, other.ID), http.StatusBadRequest, string(board.CodeInvalid))
	w := f.do(http.MethodPost, "/api/board/cards/"+story.ID+"/attach", `{"task_id":"missing"}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unknown task = %d %s", w.Code, w.Body)
	}

	// Under a proposal it is refused, naming it, until it says to confirm.
	second, err := f.m.Create(CreateRequest{Provider: "fake", ProjectID: f.project, Name: "Second"})
	if err != nil {
		t.Fatal(err)
	}
	var proposed board.Card
	f.store(func(ctx context.Context, st *board.Store) error {
		if err := st.StartPlanning(ctx, board.Owner(""), epic.ID, "planner"); err != nil {
			return err
		}
		proposed, err = st.Create(ctx, board.Agent("planner", ""), board.NewCard{Kind: board.KindStory, ParentID: epic.ID, Title: "Proposed"})
		return err
	})
	attach := fmt.Sprintf(`{"task_id":%q,"title":"Picked title"%s}`, second.ID, "%s")
	f.refused(http.MethodPost, "/api/board/cards/"+proposed.ID+"/attach", fmt.Sprintf(attach, ""), http.StatusConflict, string(board.CodeUnconfirmed))
	f.call(http.MethodPost, "/api/board/cards/"+proposed.ID+"/attach", fmt.Sprintf(attach, `,"confirm":true`), http.StatusOK, &held)
	if held.Title != "Picked title" || held.HeldBy != second.ID || !f.card(proposed.ID).Card.Confirmed {
		t.Fatalf("confirmed attach = %+v", held)
	}
}
