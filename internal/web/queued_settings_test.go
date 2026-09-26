package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"testing/fstest"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func queuedSettingsManager(t *testing.T) (*Manager, *agenttest.Provider) {
	t.Helper()
	caps := allCaps
	caps.ContextSize = true
	prov := agenttest.NewProvider("fake", caps)
	prov.SetModels(selectionModels(), nil)
	return startManager(t, openTestStore(t), prov), prov
}

func selectedPrompt(t *testing.T, text, mode, model, effort, size string) PromptRequest {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"text": text, "mode": mode, "request_id": mustUUID(t), "settings": map[string]string{"model": model, "effort": effort, "context_size": size}})
	if err != nil {
		t.Fatal(err)
	}
	var req PromptRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestQueuedSettingsApplyAtDispatchAndKeepRequestIdentity(t *testing.T) {
	m, prov := queuedSettingsManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.SetModel(sum.ID, setting("a"), setting("low"), nil); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, sum.ID, "first", mustUUID(t), ModeSend, SubmissionAccepted)
	first := selectedPrompt(t, "B", ModeQueue, "b", "high", "default")
	second := selectedPrompt(t, "C", ModeQueue, "a", "high", "long_context")
	for _, req := range []PromptRequest{first, second, first} {
		if sub, err := m.Submit(sum.ID, req); err != nil || sub.Status != SubmissionQueued {
			t.Fatalf("queue = %+v, %v", sub, err)
		}
	}
	if d := detail(t, m, sum.ID); d.Model != "a" || d.Effort != "low" || len(d.Queue) != 2 || len(conv.ModelSettings()) != 1 {
		t.Fatalf("active selection changed: %+v settings=%+v", d.SessionSummary, conv.ModelSettings())
	}
	snapshot := detail(t, m, sum.ID)
	snapshot.Queue[0].Settings.Model = "changed by caller"
	if got := detail(t, m, sum.ID).Queue[0].Settings.Model; got != "b" {
		t.Fatalf("returned settings mutated the queue: %q", got)
	}
	first.Settings.Model, first.Settings.Effort = "c", "low"
	conv.SetSendHook(func(_ context.Context, text string) error {
		sets := conv.ModelSettings()
		got := sets[len(sets)-1]
		if (text == "B" && (got.Model != "b" || got.Effort != "high")) || (text == "C" && (got.Model != "a" || got.ContextSize != "long_context")) {
			t.Errorf("settings before Send(%q) = %+v", text, got)
		}
		return nil
	})
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if sub := waitLastSubmission(t, m, sum.ID, first.RequestID); sub.Status != SubmissionAccepted {
		t.Fatalf("B = %+v", sub)
	}
	if d := detail(t, m, sum.ID); d.Model != "b" || d.Effort != "high" {
		t.Fatalf("B settings = %s %s", d.Model, d.Effort)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if sub := waitLastSubmission(t, m, sum.ID, second.RequestID); sub.Status != SubmissionAccepted {
		t.Fatalf("C = %+v", sub)
	}
	if d := detail(t, m, sum.ID); d.Model != "a" || d.Effort != "high" || d.ContextSize != "long_context" {
		t.Fatalf("C settings = %s %s %s", d.Model, d.Effort, d.ContextSize)
	}
	if sub, err := m.Submit(sum.ID, first); err != nil || sub.Status != SubmissionAccepted || len(conv.Sends()) != 3 {
		t.Fatalf("retry = %+v, %v; sends=%v", sub, err, conv.Sends())
	}
}

func TestQueuedSettingsSnapshotSurvivesPauseAndTaskSettingChanges(t *testing.T) {
	m, prov := queuedSettingsManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.SetModel(sum.ID, setting("a"), setting("low"), nil); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, sum.ID, "first", mustUUID(t), ModeSend, SubmissionAccepted)
	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "snapshot", rid, ModeQueue, SubmissionQueued)
	conv.EmitTurn(agentapi.TurnCancelled, "")
	if _, err := m.SetModel(sum.ID, setting("b"), setting("high"), nil); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	if sub := waitLastSubmission(t, m, sum.ID, rid); sub.Status != SubmissionAccepted {
		t.Fatalf("resume = %+v", sub)
	}
	if d := detail(t, m, sum.ID); d.Model != "a" || d.Effort != "low" {
		t.Fatalf("snapshot changed = %s %s", d.Model, d.Effort)
	}
	// Validate again at dispatch even if the current Task selection is identical.
	next := mustUUID(t)
	mustSubmit(t, m, sum.ID, "removed model", next, ModeQueue, SubmissionQueued)
	m.mu.Lock()
	info := m.infos["fake"]
	info.Models = []agentapi.Model{{ID: "b"}}
	m.infos["fake"] = info
	m.mu.Unlock()
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if sub := waitLastSubmission(t, m, sum.ID, next); sub.Status != SubmissionRejected || len(conv.Sends()) != 2 {
		t.Fatalf("removed model = %+v, sends=%v", sub, conv.Sends())
	}
}

func TestQueuedSettingsLegacyDefaultNeedsNoModelSwitch(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := busySession(t, m, prov)
	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "default", rid, ModeQueue, SubmissionQueued)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if sub := waitLastSubmission(t, m, sum.ID, rid); sub.Status != SubmissionAccepted || len(conv.ModelSettings()) != 0 {
		t.Fatalf("default = %+v, settings=%v", sub, conv.ModelSettings())
	}
}

func TestQueuedSettingsUploadHintAndAttachmentValidation(t *testing.T) {
	m, prov, sum, conv, _ := uploadTask(t, "blind")
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m, prov: prov}
	mustSubmit(t, ts.m, sum.ID, "first", mustUUID(t), ModeSend, SubmissionAccepted)
	img := pngBytes(t)
	base := "/api/sessions/" + sum.ID + "/attachments?name=queued.png"
	for _, query := range []string{"", "&model=", "&model=unknown"} {
		w := ts.do(http.MethodPost, base+query, string(img), withCookie(ts), withHeader("Content-Type", "application/octet-stream"))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("upload %q = %d %s", query, w.Code, w.Body)
		}
	}
	w := ts.do(http.MethodPost, base+"&model=vision", string(img), withCookie(ts), withHeader("Content-Type", "application/octet-stream"))
	var att agentapi.Attachment
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &att) != nil {
		t.Fatalf("draft upload = %d %s", w.Code, w.Body)
	}
	if d := detail(t, ts.m, sum.ID); d.Model != "blind" || len(conv.ModelSettings()) != 0 {
		t.Fatal("upload changed the active model")
	}
	steer := selectedPrompt(t, "steer image", ModeSteer, "blind", "", "default")
	steer.Attachments = []string{att.ID}
	if _, err := ts.m.Submit(sum.ID, steer); statusOf(err) != http.StatusBadRequest || len(conv.Steers()) != 0 {
		t.Fatalf("blind steer = %v", err)
	}
	req := selectedPrompt(t, "queued image", ModeQueue, "vision", "", "default")
	req.Attachments = []string{att.ID}
	if sub, err := ts.m.Submit(sum.ID, req); err != nil || sub.Status != SubmissionQueued {
		t.Fatalf("queue image = %+v, %v", sub, err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if sub := waitLastSubmission(t, ts.m, sum.ID, req.RequestID); sub.Status != SubmissionAccepted {
		t.Fatalf("image dispatch = %+v", sub)
	}
	if prompts := conv.Prompts(); len(prompts) != 2 || len(prompts[1].Attachments) != 1 || !bytes.Equal(prompts[1].Attachments[0].Data, img) {
		t.Fatalf("image content changed: %+v", prompts)
	}
}

func TestQueuedSettingsValidationSteerAndFailedApply(t *testing.T) {
	m, prov := queuedSettingsManager(t)
	sum, conv := createSession(t, m, prov)
	if _, err := m.SetModel(sum.ID, setting("a"), setting("low"), nil); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, sum.ID, "first", mustUUID(t), ModeSend, SubmissionAccepted)
	bad := selectedPrompt(t, "invalid", ModeQueue, "c", "high", "default")
	if _, err := m.Submit(sum.ID, bad); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("invalid settings = %v", err)
	}
	different := selectedPrompt(t, "different steer", ModeSteer, "b", "high", "default")
	if _, err := m.Submit(sum.ID, different); statusOf(err) != http.StatusConflict {
		t.Fatalf("different steer = %v", err)
	}
	same := selectedPrompt(t, "same steer", ModeSteer, "a", "low", "default")
	if sub, err := m.Submit(sum.ID, same); err != nil || sub.Status != SubmissionAccepted || len(conv.Steers()) != 1 {
		t.Fatalf("same steer = %+v, %v", sub, err)
	}
	first := selectedPrompt(t, "B", ModeQueue, "b", "high", "default")
	if _, err := m.Submit(sum.ID, first); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, sum.ID, "C", mustUUID(t), ModeQueue, SubmissionQueued)
	conv.SetModelError(errors.New("selection rejected"))
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if sub := waitLastSubmission(t, m, sum.ID, first.RequestID); sub.Status != SubmissionRejected {
		t.Fatalf("failed selection = %+v", sub)
	}
	if d := detail(t, m, sum.ID); !d.QueuePaused || queueTexts(d) != "C" || d.Model != "a" || len(conv.Sends()) != 1 {
		t.Fatalf("failed selection sent or changed state: %+v", d.SessionSummary)
	}
	conv.SetModelError(nil)
	if err := m.ResumeQueue(sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "remaining prompt", func() bool { return len(conv.Sends()) == 2 })
	if d := detail(t, m, sum.ID); d.Model != "a" || d.Effort != "low" {
		t.Fatalf("remaining snapshot = %s %s", d.Model, d.Effort)
	}
}
