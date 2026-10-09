package web

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func intp(n int) *int { return &n }

func floatp(n float64) *float64 { return &n }

// elicitationForm is a form with a required bounded text, an optional
// integer from 1 to 5 and a titled choice.
func elicitationForm(id string) agentapi.Interaction {
	return agentapi.Interaction{
		ID: id, Kind: agentapi.InteractionQuestion, Title: "Form from github", Detail: "Name the release\nand its size",
		Elicitation: &agentapi.Elicitation{Mode: agentapi.ElicitationForm, Source: "github"},
		Questions: []agentapi.Question{
			{Text: "Name", Custom: true, Field: &agentapi.Field{Name: "name", Type: agentapi.FieldString, Required: true, MinLength: intp(2)}},
			{Text: "Count", Custom: true, Field: &agentapi.Field{Name: "count", Type: agentapi.FieldInteger, Minimum: floatp(1), Maximum: floatp(5)}},
			{Text: "Channel", Choices: []string{"Stable", "Beta"}, Field: &agentapi.Field{Name: "channel", Type: agentapi.FieldString, Values: []string{"stable", "beta"}}},
		},
	}
}

func elicitationLink(id string) agentapi.Interaction {
	return agentapi.Interaction{
		ID: id, Kind: agentapi.InteractionQuestion, Title: "Link from github", Detail: "Sign in to continue",
		Elicitation: &agentapi.Elicitation{Mode: agentapi.ElicitationURL, Source: "github", URL: "https://example.com/auth"},
	}
}

// A form's answer is checked against every field before it reaches the
// provider; cancel is a third answer that only an elicitation takes.
func TestElicitationAnswersAreValidatedBeforeTheProvider(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.EmitInteraction(elicitationForm("f1"))
	conv.EmitInteraction(question("q1"))
	if d := detail(t, m, sum.ID); d.State != StateAwaitingAnswer || d.Ask == nil || d.Ask.Title != "Name the release" {
		t.Fatalf("waiting on a form: state=%s ask=%+v", d.State, d.Ask)
	}
	for _, bad := range []agentapi.Answer{
		{Answers: [][]string{{}, {}, {}}},                     // the name is required
		{Answers: [][]string{{"x"}, {}, {}}},                  // too short
		{Answers: [][]string{{"v1"}, {"2.5"}, {}}},            // not whole
		{Answers: [][]string{{"v1"}, {"9"}, {}}},              // above the maximum
		{Answers: [][]string{{"v1"}, {}, {"stable"}}},         // a value, not a listed choice
		{Answers: [][]string{{"v1"}, {}, {"Stable", "Beta"}}}, // one choice only
		{Answers: [][]string{{"v1"}}},                         // one entry per field
		{Cancel: true, Answers: [][]string{{"v1"}, {}, {}}},
		{Cancel: true, Reject: true},
		{Decision: "accept"},
	} {
		if _, err := m.Answer(sum.ID, "f1", bad); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("answer %+v = %v, want 400", bad, err)
		}
	}
	if _, err := m.Answer(sum.ID, "q1", agentapi.Answer{Cancel: true}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("cancelled ask_user question = %v, want 400", err)
	}
	if n := len(conv.Responds()); n != 0 {
		t.Fatalf("invalid answers reached the provider %d times", n)
	}
	ix, err := m.Answer(sum.ID, "f1", agentapi.Answer{Answers: [][]string{{"v1"}, {"3"}, {"Beta"}}})
	if err != nil || ix.State != agentapi.InteractionAnswered || ix.Resolution != "answered" {
		t.Fatalf("valid answer = %+v %v", ix, err)
	}

	conv.EmitInteraction(elicitationLink("l1"))
	ix, err = m.Answer(sum.ID, "l1", agentapi.Answer{Cancel: true})
	if err != nil || ix.State != agentapi.InteractionRejected || ix.Resolution != "cancelled" {
		t.Fatalf("cancelled link = %+v %v", ix, err)
	}
	conv.EmitInteraction(elicitationLink("l2"))
	ix, err = m.Answer(sum.ID, "l2", agentapi.Answer{})
	if err != nil || ix.State != agentapi.InteractionAnswered || ix.Resolution != "accepted" {
		t.Fatalf("accepted link = %+v %v", ix, err)
	}
	got := conv.Responds()
	if len(got) != 3 || got[1].InteractionID != "l1" || !got[1].Answer.Cancel || got[2].Answer.Cancel || got[2].Answer.Reject {
		t.Fatalf("responds = %+v", got)
	}
}

// The browser's cancel reaches the Manager through the existing answer route.
func TestElicitationCancelRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	w := ts.do(http.MethodPost, "/api/projects", `{"dir":"`+t.TempDir()+`"}`, auth)
	var project Project
	if err := json.Unmarshal(w.Body.Bytes(), &project); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("add project = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPost, "/api/sessions", `{"provider":"fake","project_id":"`+project.ID+`"}`, auth)
	var sum SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body)
	}
	conv := ts.prov.Last()
	conv.EmitInteraction(elicitationForm("f1"))
	path := "/api/sessions/" + sum.ID + "/interactions/f1"
	if w := ts.do(http.MethodPost, path, `{"answers":[["v1"],["0"],[]]}`, auth); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "at least 1") {
		t.Fatalf("out-of-range answer = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, path, `{"cancel":true}`, auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"resolution":"cancelled"`) {
		t.Fatalf("cancel = %d %s", w.Code, w.Body)
	}
	if got := conv.Responds(); len(got) != 1 || !got[0].Answer.Cancel {
		t.Fatalf("responds = %+v", got)
	}
}

// Elicitations are the user's data, not permissions: yolo never answers them.
func TestYoloNeverAnswersElicitations(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createTask(t, m, prov, "yolo")
	mustSubmit(t, m, sum.ID, "work", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnWorking, "")
	conv.EmitInteraction(elicitationForm("f1"))
	conv.EmitInteraction(elicitationLink("l1"))
	conv.EmitInteraction(onceRequest("p1", ""))
	waitAllowed(t, m, sum.ID, "p1")
	time.Sleep(30 * time.Millisecond)
	if got := responded(conv); got != "p1=once" {
		t.Fatalf("yolo answered an elicitation: %s", got)
	}
	for _, id := range []string{"f1", "l1"} {
		if ix := interactionOf(t, m, sum.ID, id); ix.State != agentapi.InteractionPending || ix.Auto {
			t.Fatalf("%s = %+v", id, ix)
		}
	}
}
