package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Fail store writes deterministically even when the test runs as root.
func blockAcceptanceStore(t *testing.T, st *store.Store) func() {
	t.Helper()
	backup := st.Path() + ".saved"
	if err := os.Rename(st.Path(), backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(st.Path(), 0o700); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() {
		if restored {
			return
		}
		if err := os.Remove(st.Path()); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(backup, st.Path()); err != nil {
			t.Fatal(err)
		}
		restored = true
	}
	t.Cleanup(restore)
	return restore
}

type creationAcceptanceProvider struct {
	*agenttest.Provider
	afterOpen func()
	invalidID bool
}

type invalidAcceptanceConversation struct{ agentapi.Conversation }

func (invalidAcceptanceConversation) ID() string { return "--invalid-conversation" }

func (p *creationAcceptanceProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	c, err := p.Provider.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	if p.afterOpen != nil {
		p.afterOpen()
	}
	if p.invalidID {
		return invalidAcceptanceConversation{c}, nil
	}
	return c, nil
}

func TestFinalAcceptanceCreateFailureDoesNotPublishTask(t *testing.T) {
	for _, failure := range []string{"provider refusal", "invalid identity", "project removed during open", "persistence"} {
		t.Run(failure, func(t *testing.T) {
			st := openTestStore(t)
			p := &creationAcceptanceProvider{Provider: agenttest.NewProvider("fake", allCaps)}
			m := startManager(t, st, p)
			project := addProject(t, m, t.TempDir())
			restore := func() {}
			want := http.StatusBadGateway
			switch failure {
			case "provider refusal":
				p.SetOpenError(errors.New("provider offline"))
			case "invalid identity":
				p.invalidID = true
			case "project removed during open":
				want = http.StatusConflict
				p.afterOpen = func() {
					if err := m.RemoveProject(project); err != nil {
						t.Fatal(err)
					}
				}
			case "persistence":
				want = http.StatusInternalServerError
				p.afterOpen = func() { restore = blockAcceptanceStore(t, st) }
			}
			_, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "must not send", RequestID: mustUUID(t)})
			if statusOf(err) != want {
				t.Fatalf("create = %v, want status %d", err, want)
			}
			restore()
			if len(m.List()) != 0 {
				t.Fatal("failed creation published a task")
			}
			cfg, err := st.Load()
			if err != nil || len(cfg.Sessions) != 0 {
				t.Fatalf("failed creation persisted tasks: %+v, %v", cfg.Sessions, err)
			}
			if conv := p.Last(); conv != nil && (conv.Closes() != 1 || len(conv.Sends()) != 0) {
				t.Fatalf("failed creation cleanup: closes=%d sends=%q", conv.Closes(), conv.Sends())
			}
		})
	}
}

func TestFinalAcceptanceProjectPersistenceFailureKeepsPublishedState(t *testing.T) {
	m, _, st := newTestManager(t)
	id := addProject(t, m, t.TempDir())
	original := m.Projects()[0]
	restore := blockAcceptanceStore(t, st)
	if _, err := m.AddProject(t.TempDir(), "unsaved", nil); err == nil {
		t.Fatal("unsaved project reported success")
	}
	if _, err := m.UpdateProject(id, setting("unsaved rename"), nil); err == nil {
		t.Fatal("unsaved rename reported success")
	}
	if got := m.Projects(); len(got) != 1 || got[0] != original {
		t.Fatalf("failed persistence changed published projects: %+v", got)
	}
	restore()
	if err := st.Update(func(cfg *store.Config) error {
		delete(cfg.WebProjects, id)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateProject(id, setting("must not resurrect"), nil); statusOf(err) != http.StatusNotFound {
		t.Fatalf("rename removed project = %v", err)
	}
	cfg, err := st.Load()
	if err != nil || len(cfg.WebProjects) != 0 || m.Projects()[0] != original {
		t.Fatalf("removed project recreated or renamed: %+v, %v", cfg.WebProjects, err)
	}
}

func TestFinalAcceptanceProjectCreatedByAnotherManagerIsNotDuplicated(t *testing.T) {
	st := openTestStore(t)
	first, second := startManager(t, st), startManager(t, st)
	dir := t.TempDir()
	p, err := first.AddProject(dir, "existing", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = second.AddProject(dir, "duplicate", nil)
	var conflict *Error
	if !errors.As(err, &conflict) || conflict.Status != http.StatusConflict || conflict.ProjectID != p.ID {
		t.Fatalf("concurrent project identity = %v", err)
	}
	cfg, err := st.Load()
	if err != nil || len(cfg.WebProjects) != 1 || cfg.WebProjects[p.ID].Name != "existing" || len(second.Projects()) != 0 {
		t.Fatalf("duplicate project changed state: %+v, %v", cfg.WebProjects, err)
	}
}

func TestFinalAcceptanceRestoredTaskCannotReplaceMissingProviderIdentity(t *testing.T) {
	for _, missing := range []string{"provider", "conversation identity"} {
		t.Run(missing, func(t *testing.T) {
			st := openTestStore(t)
			id, convID := mustUUID(t), "original-conversation"
			if missing == "conversation identity" {
				convID = ""
			}
			seedWebRecord(t, st, id, convID, StateClosed)
			prov := agenttest.NewProvider("fake", allCaps)
			var providers []agentapi.Provider
			if missing != "provider" {
				providers = append(providers, prov)
			}
			m := startManager(t, st, providers...)
			sub, err := m.Submit(id, PromptRequest{Text: "resume", RequestID: mustUUID(t), Mode: ModeSend})
			if err != nil || sub.Status != SubmissionRejected {
				t.Fatalf("resume = %+v, %v", sub, err)
			}
			d := detail(t, m, id)
			if d.State != StateFailed || d.Open || d.ConversationID != convID || len(prov.Opens()) != 0 {
				t.Fatalf("missing %s replaced a conversation: %+v", missing, d)
			}
			if err := m.flush(); err != nil {
				t.Fatal(err)
			}
			rec, ok := loadRecord(t, st, "fake", id)
			if !ok || rec.ProviderSessionID != convID || rec.Web.Turn != StateFailed {
				t.Fatalf("failed resume lost persisted identity: %+v", rec)
			}
		})
	}
}

func TestFinalAcceptanceRecentWorkdirsUsesLatestDistinctHistory(t *testing.T) {
	m, _, st := newTestManager(t)
	root := t.TempDir()
	now := time.Now().UTC()
	want := []string{}
	if err := st.Update(func(cfg *store.Config) error {
		for i := 0; i < recentWorkdirsN+2; i++ {
			dir := filepath.Join(root, fmt.Sprintf("project-%02d", i))
			if i < recentWorkdirsN {
				want = append(want, dir)
			}
			id := mustUUID(t)
			rec := store.SessionRecord{ID: id, Agent: "fake", Workdir: dir, Mode: store.ModeSafe, Status: store.StatusClosedByUser, CreatedAt: now.Add(-time.Duration(i) * time.Hour)}
			// Equal timestamps use the directory name; legacy records may
			// have no LastSeenAt, while newer records supply it.
			if i == 1 {
				rec.LastSeenAt = now
			}
			cfg.Sessions[store.Key("fake", id)] = rec
			if i == 0 {
				rec.ID = mustUUID(t)
				rec.CreatedAt = now.Add(-time.Minute)
				cfg.Sessions[store.Key("fake", rec.ID)] = rec
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got := m.RecentWorkdirs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recent workdirs = %q, want %q", got, want)
	}
	restore := blockAcceptanceStore(t, st)
	if got := m.RecentWorkdirs(); len(got) != 0 {
		t.Fatalf("unreadable history reported paths: %q", got)
	}
	restore()
	if got := m.RecentWorkdirs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("recovered history = %q, want %q", got, want)
	}
}

func TestFinalAcceptanceCreateInputFailureNeverOpensProvider(t *testing.T) {
	m, prov, _ := newTestManager(t)
	project := addProject(t, m, t.TempDir())
	original := m.Projects()[0]
	invalidName := strings.Repeat("n", maxNameRunes+1)
	if _, err := m.AddProject(t.TempDir(), invalidName, nil); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("invalid project name = %v", err)
	}
	if _, err := m.UpdateProject(project, setting(invalidName), nil); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("invalid project rename = %v", err)
	}
	if got := m.Projects(); len(got) != 1 || got[0] != original {
		t.Fatalf("invalid names changed projects: %+v", got)
	}
	for _, tc := range []struct {
		name   string
		req    CreateRequest
		status int
	}{
		{"invalid mode", CreateRequest{Mode: "unsafe"}, http.StatusBadRequest},
		{"oversized prompt", CreateRequest{Prompt: strings.Repeat("x", maxPromptBytes+1)}, http.StatusRequestEntityTooLarge},
		{"invalid request identity", CreateRequest{RequestID: "retry-me"}, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.Provider, tc.req.ProjectID = "fake", project
			if _, err := m.Create(tc.req); statusOf(err) != tc.status {
				t.Fatalf("invalid create = %v, want %d", err, tc.status)
			}
		})
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project}); statusOf(err) != http.StatusServiceUnavailable {
		t.Fatalf("create after shutdown = %v", err)
	}
	if _, err := m.AddProject(t.TempDir(), "", nil); statusOf(err) != http.StatusServiceUnavailable {
		t.Fatalf("add project after shutdown = %v", err)
	}
	if len(prov.Opens()) != 0 || len(m.List()) != 0 {
		t.Fatal("rejected creation reached the provider or published a task")
	}
}
