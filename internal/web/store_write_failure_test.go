package web

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// freezeStore makes st refuse writes, as the store of a newer uam does.
func freezeStore(t *testing.T, st *store.Store) []byte {
	t.Helper()
	if err := st.Update(func(cfg *store.Config) error {
		cfg.SchemaVersion = store.CurrentSchemaVersion + 1
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	frozen, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	return frozen
}

func wantUnchanged(t *testing.T, st *store.Store, frozen []byte) {
	t.Helper()
	if now, err := os.ReadFile(st.Path()); err != nil || !bytes.Equal(now, frozen) {
		t.Fatalf("a store that refuses writes changed: %v", err)
	}
}

// A change that must be saved before it counts is refused when the store
// refuses writes, and the service keeps its previous state.
func TestChangesThatMustBeSavedFailOnAStoreRefusingWrites(t *testing.T) {
	m, prov, st := newTestManager(t)
	empty := addProject(t, m, t.TempDir())
	archived, _ := createSession(t, m, prov)
	if _, err := m.Archive(archived.ID); err != nil {
		t.Fatal(err)
	}
	frozen := freezeStore(t, st)
	queue := store.WebSendQueue
	for _, tc := range []struct {
		name   string
		change func() error
		prefix string
	}{
		{"settings", func() error { _, err := m.UpdateSettings(SettingsPatch{SendDefault: &queue}); return err }, "save web settings: "},
		{"delete task", func() error { return m.Delete(archived.ID) }, "delete web session: "},
		{"remove project", func() error { return m.RemoveProject(empty) }, "remove web project: "},
	} {
		if err := tc.change(); !errors.Is(err, store.ErrReadOnly) || !strings.HasPrefix(err.Error(), tc.prefix) {
			t.Fatalf("%s = %v, want %q wrapping %v", tc.name, err, tc.prefix, store.ErrReadOnly)
		}
	}
	if got := m.Settings().SendDefault; got != store.WebSendSteer {
		t.Fatalf("send default = %q", got)
	}
	if _, err := m.Summary(archived.ID); err != nil {
		t.Fatalf("task after a failed delete: %v", err)
	}
	projects := map[string]bool{}
	for _, p := range m.Projects() {
		projects[p.ID] = true
	}
	if !projects[empty] || !projects[archived.ProjectID] {
		t.Fatalf("projects after a failed removal = %v", projects)
	}
	wantUnchanged(t, st, frozen)
}

// Prompts, closing and settling go on when the store refuses writes; the
// failed writes are logged and retried with the next change.
func TestTasksKeepWorkingOnAStoreRefusingWrites(t *testing.T) {
	var logs lockedBuffer
	previous := uamlog.SetLogger(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	m, prov, st := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	frozen := freezeStore(t, st)

	rid := mustUUID(t)
	mustSubmit(t, m, sum.ID, "work", rid, ModeSend, SubmissionAccepted)
	conv.EmitTurn(agentapi.TurnCompleted, "")
	if closed, err := m.Close(sum.ID); err != nil || closed.State != StateClosed || conv.Closes() != 1 {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	if settled, err := m.Settle(sum.ID); err != nil || settled.Stage != StageSettled {
		t.Fatalf("settle = %+v, %v", settled, err)
	}
	// The request ID is still known to this run: a retry is not sent again.
	mustSubmit(t, m, sum.ID, "work", rid, ModeSend, SubmissionAccepted)
	if sends := conv.Sends(); len(sends) != 1 {
		t.Fatalf("sends = %v", sends)
	}
	waitUntil(t, "the background write to fail", func() bool { return strings.Contains(logs.String(), "persist web sessions failed") })
	for _, want := range []string{"persist web submission failed", "persist closed web session failed", "persist web task stage failed"} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("log lacks %q:\n%s", want, logs.String())
		}
	}
	wantUnchanged(t, st, frozen)
}

// The service starts on a store that refuses writes, reports running turns as
// interrupted for this run, and says on shutdown that it could not save them.
func TestStoreRefusingWritesAtStartAndShutdown(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_a", StateWorking)
	frozen := freezeStore(t, st)
	m := NewManager(st, []agentapi.Provider{agenttest.NewProvider("fake", allCaps)})
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start = %v", err)
	}
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	if s := mustSummary(t, m, id); s.State != StateInterrupted || s.StateDetail != interruptedDetail {
		t.Fatalf("summary = %+v", s)
	}
	err := m.Shutdown(context.Background())
	if !errors.Is(err, store.ErrReadOnly) || !strings.HasPrefix(err.Error(), "persist web sessions: ") {
		t.Fatalf("shutdown = %v", err)
	}
	wantUnchanged(t, st, frozen)
}
