package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type taskForkProvider struct {
	*agenttest.Pager
	mu       sync.Mutex
	reads    []agentapi.ForkBoundaryRequest
	calls    []agentapi.ForkRequest
	readHook func(agentapi.ForkBoundaryRequest) error
	forkHook func(agentapi.ForkRequest) (string, error)
}

func (p *taskForkProvider) ReadForkBoundary(_ context.Context, req agentapi.ForkBoundaryRequest) (agentapi.ForkBoundary, error) {
	p.mu.Lock()
	p.reads = append(p.reads, req)
	hook := p.readHook
	p.mu.Unlock()
	if hook != nil {
		if err := hook(req); err != nil {
			return agentapi.ForkBoundary{}, err
		}
	}
	return agentapi.ForkBoundary{UserEventID: "native-owner", ToEventID: "native-next", TailEventID: "native-tail"}, nil
}

func (p *taskForkProvider) Fork(_ context.Context, req agentapi.ForkRequest) (string, error) {
	p.mu.Lock()
	p.calls = append(p.calls, req)
	n, hook := len(p.calls), p.forkHook
	p.mu.Unlock()
	if hook != nil {
		return hook(req)
	}
	return fmt.Sprintf("native-fork-%d", n), nil
}

func (p *taskForkProvider) forkCount() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) }

func taskForkManager(t *testing.T) (*Manager, *taskForkProvider, *store.Store, SessionSummary) {
	t.Helper()
	caps := allCaps
	caps.Fork, caps.Import, caps.ContextSize = true, true, true
	p := &taskForkProvider{Pager: agenttest.NewPager("fake", caps)}
	p.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, p)
	project := addProject(t, m, t.TempDir())
	source, err := m.Create(CreateRequest{ProjectID: project, Provider: "fake", Model: "a", Effort: "high", ContextSize: "long_context", Mode: "yolo"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(p.Last(), "original", "original reply")
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	return m, p, st, source
}

func TestForkRegistersExactNativePrefixWithoutOpeningOrSending(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	ownedFile := filepath.Join(source.Workdir, "source.txt")
	if err := os.WriteFile(ownedFile, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	req := ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}
	p.forkHook = func(native agentapi.ForkRequest) (string, error) {
		cfg, err := st.Load()
		if err != nil {
			return "", err
		}
		reserved, exists := cfg.WebForks[req.RequestID]
		if !exists || reserved.ID == "" || reserved.ProviderSessionID != "" || reserved.Lineage.UserEventID != native.Boundary.UserEventID || reserved.SourceConversationID != source.ConversationID {
			return "", errors.New("native RPC had no exact durable reservation")
		}
		p.SetHistory("native-copy", agentapi.History{Items: []agentapi.Item{{ID: "u-original", Kind: agentapi.ItemUser, Text: "original"}, {ID: "a-original", Kind: agentapi.ItemAssistant, Text: "original reply"}}})
		return "native-copy", nil
	}
	branch, err := m.Fork(source.ID, req)
	if err != nil {
		t.Fatal(err)
	}
	if branch.ID == source.ID || branch.ConversationID != "native-copy" || branch.ForkOf != source.ID || branch.ForkUserItemID != req.UserItemID || branch.RerunOf != "" || branch.Open || branch.Stage != StageActive || branch.ProjectID != source.ProjectID || branch.Workdir != source.Workdir || branch.Model != "a" || branch.Effort != "high" || branch.ContextSize != "long_context" || branch.Mode != "yolo" {
		t.Fatalf("branch = %+v", branch)
	}
	if len(p.Opens()) != 1 || len(p.Last().Prompts()) != 0 {
		t.Fatal("fork opened a conversation or resent a prompt")
	}
	if data, err := os.ReadFile(ownedFile); err != nil || string(data) != "unchanged" {
		t.Fatal("fork changed source file")
	}
	s, _ := m.lookup(source.ID)
	m.mu.Lock()
	sourceCount := len(s.items)
	m.mu.Unlock()
	if sourceCount != 2 {
		t.Fatalf("source item count changed: %d", sourceCount)
	}
	if err := m.flush(); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	stored := cfg.Sessions[store.Key("fake", branch.ID)]
	if stored.Web.Fork == nil || stored.Web.Fork.RequestID != req.RequestID || len(cfg.WebForks) != 0 {
		t.Fatalf("durable receipt = %+v, pending %+v", stored.Web, cfg.WebForks)
	}
	if _, err := m.Detail(branch.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "forked recorded prefix", func() bool { d, _ := m.Detail(branch.ID); return d.History == HistoryLoaded && len(d.Items) == 2 })
	if len(p.Opens()) != 1 {
		t.Fatal("view resumed source or target")
	}
}

func TestForkConcurrentRetryAndRestartCreateOnce(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	req := ForkRequest{UserItemID: "u-original", Model: "b", RequestID: mustUUID(t)}
	const clients = 8
	results := make([]SessionSummary, clients)
	errs := make([]error, clients)
	var wg sync.WaitGroup
	for i := range clients {
		wg.Go(func() { results[i], errs[i] = m.Fork(source.ID, req) })
	}
	wg.Wait()
	for i := range clients {
		if errs[i] != nil || results[i].ID != results[0].ID {
			t.Fatalf("retry %d = %+v %v", i, results[i], errs[i])
		}
	}
	if p.forkCount() != 1 || results[0].Model != "b" || results[0].Effort != "" || results[0].ContextSize != "default" || results[0].Mode != "yolo" {
		t.Fatalf("model/duplicate = %+v calls=%d", results[0], p.forkCount())
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2 := startManager(t, st, p)
	again, err := m2.Fork(source.ID, req)
	if err != nil || again.ID != results[0].ID || p.forkCount() != 1 {
		t.Fatalf("restart retry = %+v %v calls=%d", again, err, p.forkCount())
	}
	req.Model = "c"
	if _, err := m2.Fork(source.ID, req); err == nil || p.forkCount() != 1 {
		t.Fatal("same request accepted another selection")
	}
}

func TestForkUncertainOutcomeNeverBlindlyRetries(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	req := ForkRequest{UserItemID: "u-original", Model: "", RequestID: mustUUID(t)}
	p.forkHook = func(agentapi.ForkRequest) (string, error) { return "", agentapi.ErrForkUncertain }
	for range 2 {
		if _, err := m.Fork(source.ID, req); err == nil {
			t.Fatal("uncertain result reported success")
		}
	}
	if p.forkCount() != 1 {
		t.Fatalf("uncertain RPC retried %d times", p.forkCount())
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	m2 := startManager(t, st, p)
	if _, err := m2.Fork(source.ID, req); err == nil || p.forkCount() != 1 {
		t.Fatal("restart blindly forked again")
	}
	req.RequestID = mustUUID(t)
	if _, err := m2.Fork(source.ID, req); err == nil || p.forkCount() != 1 {
		t.Fatal("new browser request blindly repeated unresolved selection")
	}
}

func TestForkExplicitDefaultClearsModelSpecificSettings(t *testing.T) {
	m, p, _, source := taskForkManager(t)
	branch, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "", RequestID: mustUUID(t)})
	if err != nil || branch.Model != "" || branch.Effort != "" || branch.ContextSize != "default" || branch.Mode != "yolo" || p.forkCount() != 1 {
		t.Fatalf("Default branch = %+v %v", branch, err)
	}
}

func TestForkSavedProviderResultReconcilesRegistrationFailure(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	req := ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}
	m.mu.Lock()
	project := m.projects[source.ProjectID]
	m.mu.Unlock()
	p.forkHook = func(agentapi.ForkRequest) (string, error) {
		m.mu.Lock()
		delete(m.projects, source.ProjectID)
		m.mu.Unlock()
		return "native-preserved", nil
	}
	if _, err := m.Fork(source.ID, req); err == nil {
		t.Fatal("registration succeeded in removed project")
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	reserved := cfg.WebForks[req.RequestID]
	if reserved.ProviderSessionID != "native-preserved" || reserved.ID == "" {
		t.Fatalf("lost native result: %+v", reserved)
	}
	m.mu.Lock()
	m.projects[source.ProjectID] = project
	m.mu.Unlock()
	branch, err := m.Fork(source.ID, req)
	if err != nil || branch.ID != reserved.ID || branch.ConversationID != "native-preserved" || p.forkCount() != 1 || len(p.Opens()) != 1 {
		t.Fatalf("reconciliation = %+v %v", branch, err)
	}
}

func TestForkRegistrationPreservesAnOccupiedReservedTaskIdentity(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	req := ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}
	var targetID string
	p.forkHook = func(agentapi.ForkRequest) (string, error) {
		cfg, err := st.Load()
		if err != nil {
			return "", err
		}
		targetID = cfg.WebForks[req.RequestID].ID
		err = st.Update(func(cfg *store.Config) error {
			other := cfg.Sessions[store.Key("fake", source.ID)]
			other.ID, other.ProviderSessionID = targetID, "other-native-session"
			if !cfg.PutSession(store.Key("fake", targetID), other) {
				return errors.New("fixture collision")
			}
			return nil
		})
		return "preserved-native-fork", err
	}
	if _, err := m.Fork(source.ID, req); err == nil {
		t.Fatal("fork overwrote occupied target identity")
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	other := cfg.Sessions[store.Key("fake", targetID)]
	if other.ProviderSessionID != "other-native-session" || other.Web.Fork != nil || cfg.WebForks[req.RequestID].ProviderSessionID != "preserved-native-fork" {
		t.Fatal("registration lost another Task or its exact native result")
	}
	if p.forkCount() != 1 {
		t.Fatal("collision reforked")
	}
}

func TestForkRejectsInvalidModelBusyHolderAndChangedSourceBeforeRPC(t *testing.T) {
	for _, kind := range []string{"model", "busy", "queue-dispatch", "holder", "changed", "unsupported", "missing-project", "missing-source-record"} {
		t.Run(kind, func(t *testing.T) {
			m, p, st, source := taskForkManager(t)
			req := ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}
			s, _ := m.lookup(source.ID)
			switch kind {
			case "model":
				req.Model = "not-offered"
			case "busy":
				p.Last().EmitTurn(agentapi.TurnWorking, "")
			case "queue-dispatch":
				m.mu.Lock()
				s.queueSending = "sending"
				m.mu.Unlock()
			case "holder":
				if _, err := m.Close(source.ID); err != nil {
					t.Fatal(err)
				}
				p.SetInUse([]string{source.ConversationID}, nil)
			case "changed":
				p.readHook = func(agentapi.ForkBoundaryRequest) error { m.mu.Lock(); s.gen++; m.mu.Unlock(); return nil }
			case "unsupported":
				p.forkHook = func(agentapi.ForkRequest) (string, error) { return "", agentapi.ErrUnsupported }
			case "missing-project":
				m.mu.Lock()
				delete(m.projects, source.ProjectID)
				m.mu.Unlock()
			case "missing-source-record":
				if err := st.Update(func(cfg *store.Config) error { delete(cfg.Sessions, store.Key("fake", source.ID)); return nil }); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.Fork(source.ID, req); err == nil {
				t.Fatal("invalid fork accepted")
			}
			wantCalls := 0
			if kind == "unsupported" {
				wantCalls = 1
			}
			if p.forkCount() != wantCalls {
				t.Fatalf("native calls=%d want%d", p.forkCount(), wantCalls)
			}
			cfg, err := st.Load()
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.WebForks) != 0 {
				t.Fatal("definite refusal left an uncertain reservation")
			}
		})
	}
}

func pendingForks(t *testing.T, st *store.Store) map[string]store.WebFork {
	t.Helper()
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.WebForks
}

func TestForkSourceDeleteClearsOnlyItsReservations(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	other, err := m.Create(CreateRequest{ProjectID: source.ProjectID, Provider: "fake", Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(p.Last(), "second", "second reply")
	p.forkHook = func(agentapi.ForkRequest) (string, error) { return "", agentapi.ErrForkUncertain }
	mine, kept := ForkRequest{UserItemID: "u-original", RequestID: mustUUID(t)}, ForkRequest{UserItemID: "u-second", RequestID: mustUUID(t)}
	if _, err := m.Fork(source.ID, mine); err == nil {
		t.Fatal("uncertain result reported success")
	}
	if _, err := m.Fork(other.ID, kept); err == nil {
		t.Fatal("uncertain result reported success")
	}
	if _, err := m.Archive(source.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(source.ID); err != nil {
		t.Fatal(err)
	}
	pending := pendingForks(t, st)
	if _, ok := pending[mine.RequestID]; ok || len(pending) != 1 || pending[kept.RequestID].Lineage.SourceTaskID != other.ID {
		t.Fatalf("pending after deleting the source = %+v", pending)
	}
}

func TestForkDismissClearsExactlyThatUncertainReservation(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	p.forkHook = func(agentapi.ForkRequest) (string, error) { return "", agentapi.ErrForkUncertain }
	dismissed, kept := ForkRequest{UserItemID: "u-original", Model: "", RequestID: mustUUID(t)}, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}
	for _, req := range []ForkRequest{dismissed, kept} {
		if _, err := m.Fork(source.ID, req); errCode(err) != "fork_uncertain" {
			t.Fatalf("fork = %v, want uncertain", err)
		}
	}
	if err := m.DismissFork(source.ID, ForkRequest{UserItemID: dismissed.UserItemID, Model: dismissed.Model}); err != nil {
		t.Fatal(err)
	}
	if pending := pendingForks(t, st); len(pending) != 1 || pending[kept.RequestID].Lineage.Model != "a" {
		t.Fatalf("pending after dismiss = %+v", pending)
	}
	if err := m.DismissFork(source.ID, dismissed); statusOf(err) != http.StatusNotFound {
		t.Fatalf("second dismiss = %v, want 404", err)
	}
	// Only an explicit new request after Dismiss reaches the provider again.
	p.forkHook = nil
	if _, err := m.Fork(source.ID, ForkRequest{UserItemID: dismissed.UserItemID, Model: dismissed.Model, RequestID: mustUUID(t)}); err != nil || p.forkCount() != 3 {
		t.Fatalf("fork after dismiss = %v, calls %d", err, p.forkCount())
	}
}

func TestForkDismissKeepsAKnownResultAndWaitsForAnInFlightFork(t *testing.T) {
	m, p, st, source := taskForkManager(t)
	known := ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)}
	m.mu.Lock()
	project := m.projects[source.ProjectID]
	m.mu.Unlock()
	p.forkHook = func(agentapi.ForkRequest) (string, error) {
		m.mu.Lock()
		delete(m.projects, source.ProjectID)
		m.mu.Unlock()
		return "native-known", nil
	}
	if _, err := m.Fork(source.ID, known); err == nil {
		t.Fatal("registration succeeded in removed project")
	}
	m.mu.Lock()
	m.projects[source.ProjectID] = project
	m.mu.Unlock()
	if err := m.DismissFork(source.ID, known); statusOf(err) != http.StatusConflict || pendingForks(t, st)[known.RequestID].ProviderSessionID != "native-known" {
		t.Fatalf("dismiss of a known result = %v", err)
	}
	release, started := make(chan struct{}), make(chan struct{})
	p.forkHook = func(agentapi.ForkRequest) (string, error) {
		close(started)
		<-release
		return "", agentapi.ErrForkUncertain
	}
	flying := ForkRequest{UserItemID: "u-original", Model: "", RequestID: mustUUID(t)}
	forked := make(chan error, 1)
	go func() { _, err := m.Fork(source.ID, flying); forked <- err }()
	<-started
	done := make(chan error, 1)
	go func() { done <- m.DismissFork(source.ID, flying) }()
	select {
	case err := <-done:
		t.Fatalf("dismiss returned during the native call: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-forked; errCode(err) != "fork_uncertain" {
		t.Fatalf("fork = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := pendingForks(t, st)[flying.RequestID]; ok {
		t.Fatal("dismiss left the uncertain reservation")
	}
}
