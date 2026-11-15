package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// breakStore makes every save fail: the store's folder takes no new files,
// so no save can write its replacement file. The returned func repairs it.
// Both are atomic, so a background save never sees a missing store.
func breakStore(t *testing.T, st *store.Store) func() {
	t.Helper()
	dir := filepath.Dir(st.Path())
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	repaired := false
	repair := func() {
		if !repaired {
			repaired = true
			if err := os.Chmod(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	t.Cleanup(repair)
	return repair
}

func wantUnavailable(t *testing.T, sub Submission, err error) {
	t.Helper()
	if status, _ := errorStatus(err); err == nil || status != http.StatusServiceUnavailable || sub.Status != "" {
		t.Fatalf("submission = %+v, %v; want a 503 and no outcome", sub, err)
	}
}

// A prompt is reported queued only once the queue holding it is saved: the
// browser drops its draft on that answer.
func TestQueuedPromptIsAcknowledgedOnlyOnceSaved(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, _ := busySession(t, m, prov)
	repair := breakStore(t, st)
	rid := mustUUID(t)
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "next", RequestID: rid, Mode: ModeQueue})
	wantUnavailable(t, sub, err)
	if d := detail(t, m, sum.ID); len(d.Queue) != 0 {
		t.Fatalf("queue after a failed save = %+v", d.Queue)
	}
	// The same request after the store recovers is queued once, and saved.
	repair()
	mustSubmit(t, m, sum.ID, "next", rid, ModeQueue, SubmissionQueued)
	if d := detail(t, m, sum.ID); len(d.Queue) != 1 {
		t.Fatalf("queue = %+v", d.Queue)
	}
	rec, _ := loadRecord(t, st, prov.Name(), sum.ID)
	if rec.Web == nil || !strings.Contains(string(rec.Web.Queue), rid) {
		t.Fatalf("saved record = %+v", rec)
	}
}

// A prompt whose request cannot be reserved never reaches the provider, and
// the same request is sent once the store recovers.
func TestUnreservedPromptIsNotSent(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	repair := breakStore(t, st)
	rid := mustUUID(t)
	sub, err := m.Submit(sum.ID, PromptRequest{Text: "hello", RequestID: rid})
	wantUnavailable(t, sub, err)
	if sends := conv.Sends(); len(sends) != 0 {
		t.Fatalf("sent without a reservation: %q", sends)
	}
	if d := detail(t, m, sum.ID); d.State == StateWorking {
		t.Fatalf("state after a refused send = %s", d.State)
	}
	repair()
	mustSubmit(t, m, sum.ID, "hello", rid, ModeSend, SubmissionAccepted)
	if sends := conv.Sends(); len(sends) != 1 {
		t.Fatalf("sends = %q", sends)
	}
}

// A service that stops after the provider took a prompt but before its
// outcome was saved finds the reservation, and a retry is not sent again.
func TestReservedPromptIsNotResentAfterACrash(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	// The crash: what is on disk at the moment the provider gets the prompt.
	crashed := filepath.Join(t.TempDir(), "crashed")
	conv.SetSendHook(func(context.Context, string) error {
		return os.CopyFS(crashed, os.DirFS(filepath.Dir(st.Path())))
	})
	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "hello", rid, ModeSend, SubmissionAccepted)

	st2, err := store.Open(filepath.Join(crashed, filepath.Base(st.Path())))
	if err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(sum.ConversationID, nil)
	m2 := startManager(t, st2, prov2)
	sub, err := m2.Submit(sum.ID, PromptRequest{Text: "hello", RequestID: rid})
	if err != nil || sub.Status != SubmissionUncertain || !strings.Contains(sub.Error, "will not be repeated") {
		t.Fatalf("retry after the crash = %+v, %v", sub, err)
	}
	for _, c := range prov2.Conversations() {
		if sends := c.Sends(); len(sends) != 0 {
			t.Fatalf("the reserved prompt was sent again: %q", sends)
		}
	}
}

// An accepted prompt is answered from its record, in the service and after a
// restart, even when later prompts followed it.
func TestAcceptedPromptIsAnsweredFromItsRecord(t *testing.T) {
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	first, second := mustUUID(t), mustUUID(t)
	mustSubmit(t, m, sum.ID, "one", first, ModeSend, SubmissionAccepted)
	mustSubmit(t, m, sum.ID, "one", first, ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "the first turn ends", func() bool { return !busy(detail(t, m, sum.ID).State) })
	mustSubmit(t, m, sum.ID, "two", second, ModeSend, SubmissionAccepted)
	if sends := conv.Sends(); len(sends) != 2 {
		t.Fatalf("sends = %q", sends)
	}
	rec, _ := loadRecord(t, st, prov.Name(), sum.ID)
	if ledger := string(rec.Web.CommandSubmissions); !strings.Contains(ledger, first) || !strings.Contains(ledger, second) || strings.Contains(ledger, SubmissionUncertain) {
		t.Fatalf("saved ledger = %s", ledger)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov2 := agenttest.NewProvider("fake", allCaps)
	prov2.AddConversation(sum.ConversationID, nil)
	m2 := startManager(t, st, prov2)
	mustSubmit(t, m2, sum.ID, "one", first, ModeSend, SubmissionAccepted)
	for _, c := range prov2.Conversations() {
		if sends := c.Sends(); len(sends) != 0 {
			t.Fatalf("the accepted prompt was sent again: %q", sends)
		}
	}
}
