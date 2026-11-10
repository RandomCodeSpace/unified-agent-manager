package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type outputConversation struct {
	agentapi.Conversation
	read func(id, cursor string) (agentapi.BackgroundOutput, error)
}

func (c *outputConversation) ReadBackgroundOutput(_ context.Context, id, cursor string) (agentapi.BackgroundOutput, error) {
	return c.read(id, cursor)
}

func TestBackgroundOutputRouteScopesAndBoundsReads(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	sum, conv := createSession(t, ts.m, ts.prov)
	other, otherConv := createSession(t, ts.m, ts.prov)
	auth := withCookie(ts)
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{{ID: "server", Status: "completed", Command: "server"}}}})
	var reads []string
	var readErr error
	read := func(id, cursor string) (agentapi.BackgroundOutput, error) {
		reads = append(reads, id+"@"+cursor)
		return agentapi.BackgroundOutput{Text: "line\n", Cursor: "5:", More: true}, readErr
	}
	ts.m.mu.Lock()
	ts.m.sessions[sum.ID].conv = &outputConversation{Conversation: conv, read: read}
	ts.m.sessions[other.ID].conv = &outputConversation{Conversation: otherConv, read: read}
	ts.m.mu.Unlock()

	path := "/api/sessions/" + sum.ID + "/background-tasks/server/output"
	if w := ts.do(http.MethodGet, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
	w := ts.do(http.MethodGet, path, "", auth)
	var out agentapi.BackgroundOutput
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK || out.Text != "line\n" || out.Cursor != "5:" || !out.More || out.Done {
		t.Fatalf("first read=%d %s %v", w.Code, w.Body, err)
	}
	// A finished shell stays readable, and only an open from the start is counted.
	if w := ts.do(http.MethodGet, path+"?cursor=5:", "", auth); w.Code != http.StatusOK {
		t.Fatalf("continued read=%d %s", w.Code, w.Body)
	}
	// A jump to the newest output reaches the provider as asked and is not an open.
	if w := ts.do(http.MethodGet, path+"?cursor="+agentapi.BackgroundOutputLatest+"5:", "", auth); w.Code != http.StatusOK {
		t.Fatalf("jump read=%d %s", w.Code, w.Body)
	}
	if got := strings.Join(reads, " "); got != "server@ server@5: server@latest:5:" || countOf(ts.m, "background_output_opened") != 1 {
		t.Fatalf("reads=%q opened=%d", got, countOf(ts.m, "background_output_opened"))
	}
	// Only a shell this Thread lists is read: not an unlisted ID, not another Thread's shell.
	for _, p := range []string{"/api/sessions/" + sum.ID + "/background-tasks/agent/output", "/api/sessions/" + other.ID + "/background-tasks/server/output", "/api/sessions/none/background-tasks/server/output"} {
		if w := ts.do(http.MethodGet, p, "", auth); w.Code != http.StatusNotFound {
			t.Fatalf("%s=%d", p, w.Code)
		}
	}
	if w := ts.do(http.MethodGet, path+"?cursor="+strings.Repeat("9", maxBackgroundCursor+1), "", auth); w.Code != http.StatusBadRequest {
		t.Fatalf("long cursor=%d", w.Code)
	}
	if len(reads) != 3 {
		t.Fatalf("a refused request reached the provider: %q", reads)
	}
	readErr = agentapi.ErrBackgroundOutputUnavailable
	if w := ts.do(http.MethodGet, path, "", auth); w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not kept") {
		t.Fatalf("unavailable=%d %s", w.Code, w.Body)
	}
	readErr = errors.New("offline")
	if w := ts.do(http.MethodGet, path, "", auth); w.Code != http.StatusBadGateway {
		t.Fatalf("failed read=%d %s", w.Code, w.Body)
	}
	// A provider without the call is refused, not guessed at.
	ts.m.mu.Lock()
	ts.m.sessions[sum.ID].conv = conv
	ts.m.mu.Unlock()
	if w := ts.do(http.MethodGet, path, "", auth); w.Code != http.StatusConflict {
		t.Fatalf("provider without output=%d %s", w.Code, w.Body)
	}
}

// The snapshot tells the client which shells keep no output to read; a
// readable shell carries no field, as from a server without it.
func TestBackgroundTasksSnapshotMarksUnreadableOutput(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	sum, conv := createSession(t, ts.m, ts.prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventBackgroundTasks, BackgroundTasks: &agentapi.BackgroundTasks{Known: true, Tasks: []agentapi.BackgroundTask{
		{ID: "attached", Status: "running", Command: "make test", OutputUnreadable: true},
		{ID: "detached", Status: "running", Command: "sleep 120"},
	}}})
	w := ts.do(http.MethodGet, "/api/sessions/"+sum.ID, "", withCookie(ts))
	var detail struct {
		BackgroundTasks struct {
			Tasks []map[string]any `json:"tasks"`
		} `json:"background_tasks"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil || w.Code != http.StatusOK || len(detail.BackgroundTasks.Tasks) != 2 {
		t.Fatalf("detail=%d %s %v", w.Code, w.Body, err)
	}
	_, hasField := detail.BackgroundTasks.Tasks[1]["output_unreadable"]
	if detail.BackgroundTasks.Tasks[0]["output_unreadable"] != true || hasField {
		t.Fatalf("tasks=%v", detail.BackgroundTasks.Tasks)
	}
}
