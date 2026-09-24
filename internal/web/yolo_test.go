package web

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// onceRequest is a permission request whose "once" option allows it once.
func onceRequest(id, agentID string) agentapi.Interaction {
	return agentapi.Interaction{
		ID: id, Kind: agentapi.InteractionPermission, Title: "Run ls", AgentID: agentID, State: agentapi.InteractionPending,
		Options: []agentapi.Option{
			{ID: "always", Label: "Always allow"},
			{ID: "once", Label: "Allow once", AllowOnce: true},
			{ID: "deny", Label: "Deny", Reject: true},
		},
	}
}

func question(id string) agentapi.Interaction {
	return agentapi.Interaction{ID: id, Kind: agentapi.InteractionQuestion, Title: "Pick", Questions: []agentapi.Question{{Text: "Color?", Custom: true}}}
}

func createTask(t *testing.T, m *Manager, prov *agenttest.Provider, mode string) (SessionSummary, *agenttest.Conversation) {
	t.Helper()
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir()), Mode: mode})
	if err != nil {
		t.Fatalf("Create(mode %q): %v", mode, err)
	}
	return sum, prov.Last()
}

func interactionOf(t *testing.T, m *Manager, id, ixID string) agentapi.Interaction {
	t.Helper()
	for _, ix := range detail(t, m, id).Interactions {
		if ix.ID == ixID {
			return ix
		}
	}
	t.Fatalf("interaction %s not found", ixID)
	return agentapi.Interaction{}
}

func waitAllowed(t *testing.T, m *Manager, id, ixID string) {
	t.Helper()
	waitUntil(t, ixID+" allowed", func() bool {
		ix := interactionOf(t, m, id, ixID)
		return ix.State == agentapi.InteractionAnswered && ix.Resolution == yoloResolution
	})
}

func responded(conv *agenttest.Conversation) string {
	var out []string
	for _, r := range conv.Responds() {
		out = append(out, r.InteractionID+"="+r.Answer.Decision)
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

func TestYoloAllowsPermissionsOnceButNeverQuestionsOrPolicyRequests(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createTask(t, m, prov, "yolo")
	if sum.Mode != "yolo" {
		t.Fatalf("created mode = %q", sum.Mode)
	}
	mustSubmit(t, m, sum.ID, "work", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnWorking, "")

	conv.EmitInteraction(onceRequest("p1", ""))
	conv.EmitInteraction(onceRequest("p2", "agent-1")) // a subagent's request arrives on the Task
	waitAllowed(t, m, sum.ID, "p1")
	waitAllowed(t, m, sum.ID, "p2")
	if got := responded(conv); got != "p1=once,p2=once" {
		t.Fatalf("responds = %s", got)
	}

	conv.EmitInteraction(question("q1"))
	conv.EmitInteraction(permissionRequest("managed")) // no allow-once option: a person must decide
	time.Sleep(30 * time.Millisecond)
	if got := responded(conv); got != "p1=once,p2=once" {
		t.Fatalf("yolo answered a question or a policy request: %s", got)
	}
	d := detail(t, m, sum.ID)
	if d.State != StateAwaitingPermission || d.Pending != 2 {
		t.Fatalf("after question and policy request: state=%s pending=%d", d.State, d.Pending)
	}
	for _, id := range []string{"q1", "managed"} {
		if ix := interactionOf(t, m, sum.ID, id); ix.State != agentapi.InteractionPending {
			t.Fatalf("%s = %+v", id, ix)
		}
	}
}

// Copilot reports its own "Allow once" resolution while Respond runs; the
// record still says yolo allowed it.
func TestYoloResolutionSurvivesTheProviderReport(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createTask(t, m, prov, "yolo")
	conv.SetRespondHook(func(_ context.Context, id string, _ agentapi.Answer) error {
		ix := onceRequest(id, "")
		ix.State, ix.Resolution = agentapi.InteractionAnswered, "Allow once"
		conv.EmitInteraction(ix)
		return nil
	})
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	conv.EmitInteraction(onceRequest("p1", ""))
	var states []string
	for len(states) < 2 {
		var ix agentapi.Interaction
		decodeField(t, frameOf(t, sub, "interaction"), "interaction", &ix)
		states = append(states, string(ix.State)+":"+ix.Resolution)
	}
	if strings.Join(states, ",") != "pending:,answered:"+yoloResolution {
		t.Fatalf("interaction frames = %v", states)
	}
}

func TestYoloAndABrowserNeverBothAnswer(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createTask(t, m, prov, "yolo")
	release := make(chan struct{})
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { <-release; return nil })
	conv.EmitInteraction(onceRequest("p1", ""))
	waitUntil(t, "yolo answer at the provider", func() bool { return len(conv.Responds()) == 1 })
	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "deny"}); statusOf(err) != http.StatusConflict {
		t.Fatalf("browser answer while yolo answers = %v, want 409", err)
	}
	close(release)
	waitAllowed(t, m, sum.ID, "p1")
	if _, err := m.Answer(sum.ID, "p1", agentapi.Answer{Decision: "deny"}); statusOf(err) != http.StatusConflict {
		t.Fatalf("browser answer after yolo = %v, want 409", err)
	}

	// The other way round: a browser answers first, then the Task turns yolo.
	block := make(chan struct{})
	conv.SetRespondHook(func(context.Context, string, agentapi.Answer) error { <-block; return nil })
	if _, err := m.SetMode(sum.ID, "safe"); err != nil {
		t.Fatal(err)
	}
	conv.EmitInteraction(onceRequest("p2", ""))
	done := make(chan error, 1)
	go func() {
		_, err := m.Answer(sum.ID, "p2", agentapi.Answer{Decision: "deny"})
		done <- err
	}()
	waitUntil(t, "browser answer at the provider", func() bool { return len(conv.Responds()) == 2 })
	if _, err := m.SetMode(sum.ID, "yolo"); err != nil {
		t.Fatal(err)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	if got := responded(conv); got != "p1=once,p2=deny" {
		t.Fatalf("responds = %s", got)
	}
	if ix := interactionOf(t, m, sum.ID, "p2"); ix.State != agentapi.InteractionRejected || ix.Resolution != "Deny" {
		t.Fatalf("p2 = %+v", ix)
	}
}

func TestModeSwitchAnswersPendingAndAppliesToLaterRequests(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createTask(t, m, prov, "")
	if sum.Mode != "safe" {
		t.Fatalf("default mode = %q", sum.Mode)
	}
	conv.EmitInteraction(onceRequest("p1", ""))
	time.Sleep(30 * time.Millisecond)
	if len(conv.Responds()) != 0 {
		t.Fatal("a safe Task answered a permission")
	}

	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	m.mu.Lock()
	m.now = func() time.Time { return later }
	m.mu.Unlock()
	got, err := m.SetMode(sum.ID, "yolo")
	if err != nil || got.Mode != "yolo" || !got.UpdatedAt.Equal(later) {
		t.Fatalf("SetMode = %+v, %v", got, err)
	}
	var frame SessionSummary
	decodeField(t, frameOf(t, sub, "session"), "session", &frame)
	if frame.Mode != "yolo" || !frame.UpdatedAt.Equal(later) {
		t.Fatalf("session frame = %+v", frame)
	}
	waitAllowed(t, m, sum.ID, "p1") // pending when the switch happened
	waitUntil(t, "mode stored", func() bool {
		rec, _ := loadRecord(t, st, "fake", sum.ID)
		return rec.Mode == store.ModeYolo && rec.Web.UpdatedAt.Equal(later)
	})

	if _, err := m.SetMode(sum.ID, "safe"); err != nil {
		t.Fatal(err)
	}
	conv.EmitInteraction(onceRequest("p2", ""))
	time.Sleep(30 * time.Millisecond)
	if got := responded(conv); got != "p1=once" {
		t.Fatalf("after switching back to safe: %s", got)
	}
	if d := detail(t, m, sum.ID); d.State != StateAwaitingPermission || d.Mode != "safe" {
		t.Fatalf("after switching back: state=%s mode=%s", d.State, d.Mode)
	}
}

// Only yolo marks its answer automatic; a browser cannot claim to be yolo.
func TestOnlyYoloAnswersAreMarkedAutomatic(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	yolo, yconv := createTask(t, ts.m, ts.prov, "yolo")
	safe, sconv := createTask(t, ts.m, ts.prov, "safe")
	yconv.EmitInteraction(onceRequest("p1", ""))
	waitAllowed(t, ts.m, yolo.ID, "p1")
	sconv.EmitInteraction(onceRequest("p2", ""))
	if w := ts.do(http.MethodPost, "/api/sessions/"+safe.ID+"/interactions/p2", `{"decision":"once","auto":true}`, auth); w.Code != http.StatusOK {
		t.Fatalf("browser answer = %d %s", w.Code, w.Body)
	}
	if r := yconv.Responds(); len(r) != 1 || !r[0].Answer.Auto {
		t.Fatalf("yolo responds = %+v", r)
	}
	if r := sconv.Responds(); len(r) != 1 || r[0].Answer.Auto {
		t.Fatalf("browser responds = %+v", r)
	}
}

func TestModeValidation(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	project := addProject(t, ts.m, t.TempDir())
	if _, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: project, Mode: "turbo"}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("create with mode turbo = %v, want 400", err)
	}
	if len(ts.prov.Opens()) != 0 {
		t.Fatal("an invalid mode reached the provider")
	}
	w := ts.do(http.MethodPost, "/api/sessions", `{"provider":"fake","project_id":"`+project+`","mode":"yolo"}`, auth)
	var sum SessionSummary
	if err := json.Unmarshal(w.Body.Bytes(), &sum); err != nil || w.Code != http.StatusCreated || sum.Mode != "yolo" {
		t.Fatalf("create yolo = %d %s", w.Code, w.Body)
	}
	base := "/api/sessions/" + sum.ID
	for _, body := range []string{`{"mode":"turbo","name":"x"}`, `{"mode":""}`, `{"mode":"YOLO"}`} {
		if w := ts.do(http.MethodPatch, base, body, auth); w.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s = %d, want 400", body, w.Code)
		}
	}
	if d := detail(t, ts.m, sum.ID); d.Mode != "yolo" || d.Name != "" {
		t.Fatalf("a refused PATCH changed the Task: %+v", d.SessionSummary)
	}
	w = ts.do(http.MethodPatch, base, `{"mode":"safe","name":"careful"}`, auth)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"mode":"safe"`) || !strings.Contains(w.Body.String(), `"name":"careful"`) {
		t.Fatalf("PATCH mode and name = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPatch, "/api/sessions/missing", `{"mode":"yolo"}`, auth); w.Code != http.StatusNotFound {
		t.Fatalf("PATCH missing = %d, want 404", w.Code)
	}
}

func TestYoloModeSurvivesARestart(t *testing.T) {
	st := openTestStore(t)
	prov := agenttest.NewProvider("fake", allCaps)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	yolo, _ := createTask(t, m, prov, "yolo")
	safe, _ := createTask(t, m, prov, "safe")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]store.Mode{yolo.ID: store.ModeYolo, safe.ID: store.ModeSafe} {
		if rec, _ := loadRecord(t, st, "fake", id); rec.Mode != want {
			t.Fatalf("stored mode of %s = %q, want %q", id, rec.Mode, want)
		}
	}

	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(yolo.ConversationID, nil)
	m2 := startManager(t, st, prov2)
	if d := detail(t, m2, yolo.ID); d.Mode != "yolo" {
		t.Fatalf("mode after restart = %q", d.Mode)
	}
	if err := m2.View(context.Background(), yolo.ID); err != nil {
		t.Fatal(err)
	}
	conv := prov2.Last()
	conv.EmitInteraction(onceRequest("p1", ""))
	waitAllowed(t, m2, yolo.ID, "p1")
}
