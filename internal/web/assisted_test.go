package web

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var assistedCaps = func() agentapi.Capabilities { c := allCaps; c.AssistedPermissions = true; return c }()

func assistedProvider(models ...string) *agenttest.Provider {
	prov := agenttest.NewProvider("fake", assistedCaps)
	var list []agentapi.Model
	for _, id := range models {
		list = append(list, agentapi.Model{ID: id, Name: id})
	}
	prov.SetModels(list, nil)
	return prov
}

func assistedManager(t *testing.T) (*Manager, *agenttest.Provider, *store.Store) {
	t.Helper()
	prov := assistedProvider("gpt-6-luna", "other")
	st := openTestStore(t)
	return startManager(t, st, prov), prov, st
}

type modeConversation struct {
	*agenttest.Conversation
	mu   sync.Mutex
	sets []string
	err  error
}

func (c *modeConversation) SetAssistedPermissions(_ context.Context, approvalModel string) error {
	c.mu.Lock()
	c.sets = append(c.sets, approvalModel)
	err := c.err
	c.mu.Unlock()
	return err
}

func (c *modeConversation) calls() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sets...)
}

func attachModeSetter(m *Manager, id string, base *agenttest.Conversation) *modeConversation {
	c := &modeConversation{Conversation: base}
	m.mu.Lock()
	m.sessions[id].conv = c
	m.mu.Unlock()
	return c
}

func reviewed(id, recommendation, model string) agentapi.Interaction {
	ix := onceRequest(id, "")
	ix.Assisted = agentapi.AssistedReview{Recommendation: recommendation, Model: model, Reason: "writes one file in the workspace"}
	return ix
}

func TestAssistedModeIsOptInAndOpensWithItsReviewer(t *testing.T) {
	m, prov, _ := assistedManager(t)
	safe, _ := createTask(t, m, prov, "")
	if safe.Mode != "safe" || prov.Opens()[0].AssistedApprovalModel != "" {
		t.Fatalf("default Task = %q, reviewer %q", safe.Mode, prov.Opens()[0].AssistedApprovalModel)
	}
	assisted, _ := createTask(t, m, prov, "assisted")
	if assisted.Mode != "assisted" || prov.Opens()[1].AssistedApprovalModel != "gpt-6-luna" {
		t.Fatalf("assisted Task = %q, reviewer %q", assisted.Mode, prov.Opens()[1].AssistedApprovalModel)
	}
}

func TestAssistedModeRefusesUnsupportedConfigurations(t *testing.T) {
	for name, prov := range map[string]*agenttest.Provider{
		"no capability": func() *agenttest.Provider {
			p := agenttest.NewProvider("fake", allCaps)
			p.SetModels([]agentapi.Model{{ID: "gpt-6-luna"}}, nil)
			return p
		}(),
		"no reviewer model": assistedProvider("gpt-5.5", "other"),
	} {
		t.Run(name, func(t *testing.T) {
			m := startManager(t, openTestStore(t), prov)
			project := addProject(t, m, t.TempDir())
			if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Mode: "assisted"}); statusOf(err) != http.StatusConflict {
				t.Fatalf("create assisted = %v, want 409", err)
			}
			if len(prov.Opens()) != 0 {
				t.Fatal("a refused mode reached the provider")
			}
			sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.SetMode(sum.ID, "assisted"); statusOf(err) != http.StatusConflict {
				t.Fatalf("switch to assisted = %v, want 409", err)
			}
			if d := detail(t, m, sum.ID); d.Mode != "safe" {
				t.Fatalf("refused switch changed the mode to %q", d.Mode)
			}
		})
	}
}

func TestAssistedReviewAllowsOnlyItsOwnApproval(t *testing.T) {
	m, prov, _ := assistedManager(t)
	sum, conv := createTask(t, m, prov, "assisted")
	mustSubmit(t, m, sum.ID, "work", mustUUID(t), ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnWorking, "")

	conv.EmitInteraction(reviewed("ok", "approve", "gpt-6-luna"))
	waitUntil(t, "assisted approval", func() bool {
		ix := interactionOf(t, m, sum.ID, "ok")
		return ix.State == agentapi.InteractionAnswered && ix.Resolution == assistedResolution
	})
	if got := responded(conv); got != "ok=once" || !conv.Responds()[0].Answer.Auto {
		t.Fatalf("responds = %s %+v", got, conv.Responds())
	}
	conv.EmitInteraction(reviewed("other-reviewer", "approve", "gpt-5.5")) // the runtime default, not the chosen reviewer
	conv.EmitInteraction(reviewed("asks", "requireApproval", "gpt-6-luna"))
	conv.EmitInteraction(reviewed("failed", "error", ""))
	conv.EmitInteraction(onceRequest("unreviewed", ""))
	managed := reviewed("managed", "approve", "gpt-6-luna")
	managed.Options = permissionRequest("managed").Options // no allow-once option: a person must decide
	conv.EmitInteraction(managed)
	time.Sleep(30 * time.Millisecond)
	for _, id := range []string{"other-reviewer", "asks", "failed", "unreviewed", "managed"} {
		if ix := interactionOf(t, m, sum.ID, id); ix.State != agentapi.InteractionPending || ix.Auto {
			t.Fatalf("%s = %+v, want waiting for the user", id, ix)
		}
	}
	if ix := interactionOf(t, m, sum.ID, "asks"); ix.Assisted.Recommendation != "requireApproval" || ix.Assisted.Reason == "" {
		t.Fatalf("review not shown with the request: %+v", ix.Assisted)
	}
	if got := responded(conv); got != "ok=once" {
		t.Fatalf("responds = %s", got)
	}

	// A Safe Task never acts on a review.
	safe, safeConv := createTask(t, m, prov, "safe")
	safeConv.EmitInteraction(reviewed("p", "approve", "gpt-6-luna"))
	time.Sleep(30 * time.Millisecond)
	if ix := interactionOf(t, m, safe.ID, "p"); ix.State != agentapi.InteractionPending || len(safeConv.Responds()) != 0 {
		t.Fatalf("safe Task acted on a review: %+v", ix)
	}
}

func TestSetModeAssistedChangesTheOpenConversationFirst(t *testing.T) {
	m, prov, _ := assistedManager(t)
	sum, base := createTask(t, m, prov, "safe")
	c := attachModeSetter(m, sum.ID, base)
	if got, err := m.SetMode(sum.ID, "assisted"); err != nil || got.Mode != "assisted" {
		t.Fatalf("to assisted = %+v, %v", got.Mode, err)
	}
	if got, err := m.SetMode(sum.ID, "yolo"); err != nil || got.Mode != "yolo" {
		t.Fatalf("to yolo = %+v, %v", got.Mode, err)
	}
	if _, err := m.SetMode(sum.ID, "safe"); err != nil {
		t.Fatal(err)
	}
	if got := c.calls(); len(got) != 2 || got[0] != "gpt-6-luna" || got[1] != "" {
		t.Fatalf("setter calls = %q", got)
	}
	for _, tc := range []struct {
		err    error
		status int
	}{{errors.New("runtime refused"), http.StatusBadGateway}, {agentapi.ErrUnsupported, http.StatusConflict}} {
		c.err = tc.err
		if _, err := m.SetMode(sum.ID, "assisted"); statusOf(err) != tc.status {
			t.Fatalf("refused switch = %v, want %d", err, tc.status)
		}
		if d := detail(t, m, sum.ID); d.Mode != "safe" {
			t.Fatalf("runtime refusal recorded mode %q", d.Mode)
		}
	}
	// Without the setter the open conversation cannot change: refused, not recorded.
	m.mu.Lock()
	m.sessions[sum.ID].conv = base
	m.mu.Unlock()
	if _, err := m.SetMode(sum.ID, "assisted"); statusOf(err) != http.StatusConflict || detail(t, m, sum.ID).Mode != "safe" {
		t.Fatalf("switch without setter = %v", err)
	}
}

func TestAssistedModeOnAClosedTaskAppliesAtReopen(t *testing.T) {
	m, prov, _ := assistedManager(t)
	sum, _ := createTask(t, m, prov, "safe")
	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := m.SetMode(sum.ID, "assisted"); err != nil || got.Mode != "assisted" {
		t.Fatalf("closed switch = %+v %v", got.Mode, err)
	}
	mustSubmit(t, m, sum.ID, "work", mustUUID(t), ModeSend, SubmissionAccepted)
	if opens := prov.Opens(); len(opens) != 2 || opens[1].AssistedApprovalModel != "gpt-6-luna" {
		t.Fatalf("reopen reviewer = %q", opens[len(opens)-1].AssistedApprovalModel)
	}
}

func TestAssistedModeSurvivesARestartAndRefusesAMissingReviewer(t *testing.T) {
	m, prov, st := assistedManager(t)
	sum, _ := createTask(t, m, prov, "assisted")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rec, _ := loadRecord(t, st, "fake", sum.ID); rec.Mode != store.ModeAssisted {
		t.Fatalf("stored mode = %q", rec.Mode)
	}
	prov2 := assistedProvider("gpt-6-luna")
	prov2.AddConversation(sum.ConversationID, nil)
	m2 := startManager(t, st, prov2)
	if d := detail(t, m2, sum.ID); d.Mode != "assisted" {
		t.Fatalf("mode after restart = %q", d.Mode)
	}
	if err := m2.View(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	if opens := prov2.Opens(); len(opens) != 1 || opens[0].AssistedApprovalModel != "gpt-6-luna" {
		t.Fatalf("reopen = %+v", opens)
	}
	if err := m2.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The reviewer is no longer offered: the Task does not open under another one.
	prov3 := assistedProvider("other")
	prov3.AddConversation(sum.ConversationID, nil)
	m3 := startManager(t, st, prov3)
	if err := m3.View(context.Background(), sum.ID); err != nil || len(prov3.Opens()) != 0 {
		t.Fatalf("reopen without the reviewer = %v, opens %d", err, len(prov3.Opens()))
	}
	if d := detail(t, m3, sum.ID); d.Mode != "assisted" || d.State != StateFailed {
		t.Fatalf("after refused reopen = %q %q", d.Mode, d.State)
	}
}
