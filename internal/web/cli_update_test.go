package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// cliFake is a fake provider whose CLI the tests update. Its update runs
// quiesce, then installs the release, unless a test replaces it.
type cliFake struct {
	*agenttest.Provider
	mu     sync.Mutex
	rel    agentapi.CLIRelease
	relErr error
	update func(ctx context.Context, version string, quiesce func() error) error
	tried  []string
}

func newCLIFake(rel agentapi.CLIRelease) *cliFake {
	caps := allCaps
	caps.CLIUpdate = true
	p := &cliFake{Provider: agenttest.NewProvider("fake", caps), rel: rel}
	p.update = func(_ context.Context, version string, quiesce func() error) error {
		if err := quiesce(); err != nil {
			return err
		}
		p.setRelease(agentapi.CLIRelease{Installed: version, Latest: version}, nil)
		return nil
	}
	return p
}

func (p *cliFake) setRelease(rel agentapi.CLIRelease, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.rel, p.relErr = rel, err
}

func (p *cliFake) setUpdate(update func(ctx context.Context, version string, quiesce func() error) error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.update = update
}

func (p *cliFake) updates() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.tried...)
}

func (p *cliFake) CLIRelease(context.Context) (agentapi.CLIRelease, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.rel, p.relErr
}

func (p *cliFake) UpdateCLI(ctx context.Context, version string, quiesce func() error) error {
	p.mu.Lock()
	p.tried = append(p.tried, version)
	update := p.update
	p.mu.Unlock()
	return update(ctx, version, quiesce)
}

func cliServer(t *testing.T, prov agentapi.Provider) (*Manager, *testServer, reqOpt) {
	t.Helper()
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m}
	return m, ts, withCookie(ts)
}

// cliCall sends method to the provider's CLI route and decodes a 200 answer.
func cliCall(t *testing.T, ts *testServer, auth reqOpt, method, path string) (int, ProviderCLI, string) {
	t.Helper()
	w := ts.do(method, path, map[string]string{http.MethodGet: "", http.MethodPost: "{}"}[method], auth)
	var cli ProviderCLI
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &cli); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return w.Code, cli, w.Body.String()
}

func metaCLIUpdate(t *testing.T, ts *testServer, auth reqOpt) string {
	t.Helper()
	var meta Meta
	if err := json.Unmarshal(ts.do(http.MethodGet, "/api/meta", "", auth).Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	return meta.Providers[0].CLIUpdate
}

func waitCLI(t *testing.T, m *Manager, state string) ProviderCLI {
	t.Helper()
	var cli ProviderCLI
	waitUntil(t, "CLI update "+state, func() bool {
		cli, _ = m.ProviderCLI(context.Background(), "fake", false)
		return cli.State == state
	})
	return cli
}

// An update offered in meta runs in the background, closes the idle Task's
// conversation without changing its state, and leaves the new release
// installed and no update offered.
func TestCLIUpdateRunsAndClosesIdleConversations(t *testing.T) {
	prov := newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.92", Newer: true})
	m, ts, auth := cliServer(t, prov)
	code, cli, body := cliCall(t, ts, auth, http.MethodGet, "/api/providers/fake/cli?refresh=1")
	if code != http.StatusOK || cli.Installed != "1.0.80" || cli.Latest != "1.0.92" || !cli.UpdateAvailable || cli.State != cliIdle || cli.CheckedAt.IsZero() {
		t.Fatalf("GET = %d %s", code, body)
	}
	if got := metaCLIUpdate(t, ts, auth); got != "1.0.92" {
		t.Fatalf("meta cli_update = %q", got)
	}
	sum, conv := createSession(t, m, prov.Provider)
	before := mustSummary(t, m, sum.ID)

	release := make(chan struct{})
	update := prov.update
	prov.setUpdate(func(ctx context.Context, version string, quiesce func() error) error {
		<-release
		return update(ctx, version, quiesce)
	})
	code, cli, body = cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update")
	if code != http.StatusOK || cli.State != cliUpdating || cli.Target != "1.0.92" {
		t.Fatalf("POST = %d %s", code, body)
	}
	// Starting again joins the update in progress.
	if code, cli, body = cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusOK || cli.State != cliUpdating {
		t.Fatalf("second POST = %d %s", code, body)
	}
	close(release)
	waitCLI(t, m, cliUpdated)
	if got := prov.updates(); len(got) != 1 || got[0] != "1.0.92" {
		t.Fatalf("updates run = %q", got)
	}
	after := mustSummary(t, m, sum.ID)
	if conv.Closes() != 1 || after.Open || after.State != before.State {
		t.Fatalf("idle Task after the update: closes %d, %+v (was %s)", conv.Closes(), after, before.State)
	}
	code, cli, body = cliCall(t, ts, auth, http.MethodGet, "/api/providers/fake/cli")
	if code != http.StatusOK || cli.Installed != "1.0.92" || cli.UpdateAvailable || cli.State != cliUpdated || cli.Target != "1.0.92" || cli.Error != "" {
		t.Fatalf("GET after the update = %d %s", code, body)
	}
	if got := metaCLIUpdate(t, ts, auth); got != "" {
		t.Fatalf("meta cli_update after the update = %q", got)
	}
	if code, _, body = cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusConflict {
		t.Fatalf("POST with nothing newer = %d %s", code, body)
	}
}

func TestCLIUpdateRefusals(t *testing.T) {
	t.Run("no capability", func(t *testing.T) {
		_, ts, auth := cliServer(t, agenttest.NewProvider("fake", allCaps))
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := map[string]string{http.MethodGet: "/api/providers/fake/cli", http.MethodPost: "/api/providers/fake/cli/update"}[method]
			if code, _, body := cliCall(t, ts, auth, method, path); code != http.StatusNotFound {
				t.Fatalf("%s without the capability = %d %s", method, code, body)
			}
		}
	})
	t.Run("manual", func(t *testing.T) {
		manual := "npm's global folder /opt/node is not writable by this user"
		_, ts, auth := cliServer(t, newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.92", Newer: true, Manual: manual}))
		if code, cli, body := cliCall(t, ts, auth, http.MethodGet, "/api/providers/fake/cli"); code != http.StatusOK || cli.UpdateAvailable || cli.Manual != manual {
			t.Fatalf("GET = %d %s", code, body)
		}
		if code, _, body := cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusConflict || !strings.Contains(body, manual) {
			t.Fatalf("POST = %d %s", code, body)
		}
	})
	t.Run("task working", func(t *testing.T) {
		prov := newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.92", Newer: true})
		m, ts, auth := cliServer(t, prov)
		_, conv := createSession(t, m, prov.Provider)
		conv.EmitTurn(agentapi.TurnWorking, "")
		code, _, body := cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update")
		if code != http.StatusConflict || !strings.Contains(body, "1 Fake fake task is working or waiting for an answer; update when it finishes") || len(prov.updates()) != 0 {
			t.Fatalf("POST while working = %d %s, updates %q", code, body, prov.updates())
		}
	})
	t.Run("check failed", func(t *testing.T) {
		prov := newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.92", Newer: true})
		_, ts, auth := cliServer(t, prov)
		cliCall(t, ts, auth, http.MethodGet, "/api/providers/fake/cli?refresh=1")
		prov.setRelease(agentapi.CLIRelease{Installed: "1.0.80"}, errors.New("npm view @github/copilot: network unreachable"))
		code, cli, body := cliCall(t, ts, auth, http.MethodGet, "/api/providers/fake/cli?refresh=1")
		if code != http.StatusOK || cli.CheckError != "npm view @github/copilot: network unreachable" || cli.Latest != "1.0.92" || !cli.UpdateAvailable {
			t.Fatalf("GET after a failed check = %d %s", code, body)
		}
	})
}

// A read that fails after a successful update still shows the release now in
// place and offers no update.
func TestCLIUpdateFailedReadAfterInstall(t *testing.T) {
	prov := newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.92", Newer: true})
	prov.setUpdate(func(context.Context, string, func() error) error {
		prov.setRelease(agentapi.CLIRelease{}, errors.New("npm view @github/copilot: network unreachable"))
		return nil
	})
	m, ts, auth := cliServer(t, prov)
	if code, _, body := cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	if cli := waitCLI(t, m, cliUpdated); cli.Installed != "1.0.92" || cli.UpdateAvailable || cli.CheckError == "" {
		t.Fatalf("after the update = %+v", cli)
	}
	if got := metaCLIUpdate(t, ts, auth); got != "" {
		t.Fatalf("meta cli_update after the update = %q", got)
	}
}

// A Task that starts working once the update runs keeps its conversation and
// fails the update; an idle one is closed all the same.
func TestCLIUpdateQuiesceRefusesAWorkingTask(t *testing.T) {
	prov := newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.92", Newer: true})
	m, ts, auth := cliServer(t, prov)
	_, idle := createSession(t, m, prov.Provider)
	working, busy := createSession(t, m, prov.Provider)
	ready := make(chan struct{})
	update := prov.update
	prov.setUpdate(func(ctx context.Context, version string, quiesce func() error) error {
		<-ready
		return update(ctx, version, quiesce)
	})
	if code, _, body := cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	busy.EmitTurn(agentapi.TurnWorking, "")
	close(ready)
	cli := waitCLI(t, m, cliFailed)
	if cli.Error != "1 Fake fake task is working or waiting for an answer; update when it finishes" || cli.Installed != "1.0.80" || !cli.UpdateAvailable {
		t.Fatalf("refused update = %+v", cli)
	}
	if idle.Closes() != 1 || busy.Closes() != 0 || !mustSummary(t, m, working.ID).Open {
		t.Fatalf("closes: idle %d, working %d", idle.Closes(), busy.Closes())
	}
}

// A release the SDK refuses fails the update with the SDK's reason and is
// not offered again; a newer one is.
func TestCLIUpdateRemembersAnIncompatibleRelease(t *testing.T) {
	prov := newCLIFake(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.95", Newer: true})
	prov.setUpdate(func(context.Context, string, func() error) error {
		return fmt.Errorf("%w: SDK protocol version mismatch: SDK supports versions 3-3, but server reports version 4", agentapi.ErrCLIIncompatible)
	})
	m, ts, auth := cliServer(t, prov)
	if code, _, body := cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusOK {
		t.Fatalf("POST = %d %s", code, body)
	}
	cli := waitCLI(t, m, cliFailed)
	if cli.Error != "Fake fake CLI 1.0.95 needs a newer uam: SDK protocol version mismatch: SDK supports versions 3-3, but server reports version 4" ||
		cli.Incompatible != "1.0.95" || cli.UpdateAvailable || cli.Target != "1.0.95" {
		t.Fatalf("incompatible update = %+v", cli)
	}
	if got := metaCLIUpdate(t, ts, auth); got != "" {
		t.Fatalf("meta offers the refused release: %q", got)
	}
	if code, _, body := cliCall(t, ts, auth, http.MethodPost, "/api/providers/fake/cli/update"); code != http.StatusConflict {
		t.Fatalf("POST for the refused release = %d %s", code, body)
	}
	prov.setRelease(agentapi.CLIRelease{Installed: "1.0.80", Latest: "1.0.96", Newer: true}, nil)
	if code, cli, body := cliCall(t, ts, auth, http.MethodGet, "/api/providers/fake/cli?refresh=1"); code != http.StatusOK || !cli.UpdateAvailable || cli.Incompatible != "1.0.95" {
		t.Fatalf("GET with a newer release = %d %s", code, body)
	}
}
