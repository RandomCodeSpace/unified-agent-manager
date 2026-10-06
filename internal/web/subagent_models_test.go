package web

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// subagentProvider records the subagent model lists it is given, as the
// Copilot adapter takes them.
type subagentProvider struct {
	*customProvider
	smu   sync.Mutex
	lists [][]string
}

func newSubagentProvider() *subagentProvider {
	return &subagentProvider{customProvider: &customProvider{Provider: agenttest.NewProvider("fake", allCaps)}}
}

func (p *subagentProvider) SetSubagentModels(models []string) {
	p.smu.Lock()
	defer p.smu.Unlock()
	p.lists = append(p.lists, slices.Clone(models))
}

// subagents returns how many lists the provider was given and the latest.
func (p *subagentProvider) subagents() (int, []string) {
	p.smu.Lock()
	defer p.smu.Unlock()
	if len(p.lists) == 0 {
		return 0, nil
	}
	return len(p.lists), p.lists[len(p.lists)-1]
}

func TestSubagentModelsValidatedStoredAndPushed(t *testing.T) {
	t.Setenv("UAM_BYOM_TEST_ACME", "acme-secret")
	st := openTestStore(t)
	prov := newSubagentProvider()
	plain := agenttest.NewProvider("plain", allCaps)
	plain.SetModels(selectionModels(), nil)
	m := startManager(t, st, prov, plain)
	if n, list := prov.subagents(); n != 1 || list != nil {
		t.Fatalf("at startup provider got %d lists, last %v", n, list)
	}
	other := store.WebCustomModel{Name: "acme", BaseURL: acme.BaseURL, ModelID: "other", APIKeyEnv: acme.APIKeyEnv}
	if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(acme, other)}); err != nil {
		t.Fatal(err)
	}
	limit := func(byProvider map[string][]string) (Settings, error) {
		return m.UpdateSettings(SettingsPatch{SubagentModels: byProvider})
	}
	for name, change := range map[string]map[string][]string{
		"unknown provider":       {"nobody": {"own"}},
		"not a subagent user":    {"plain": {"a"}},
		"model not offered":      {"fake": {"own", "missing"}},
		"empty ID":               {"fake": {""}},
		"control character":      {"fake": {"own\x07"}},
		"too many":               {"fake": manyIDs(store.MaxHiddenModels + 1)},
		"other provider refused": {"fake": {"own"}, "plain": {"a"}},
	} {
		if _, err := limit(change); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s = %v, want 400", name, err)
		}
	}
	if n, _ := prov.subagents(); n != 1 || m.Settings().SubagentModels != nil {
		t.Fatalf("refused changes reached the provider (%d lists) or settings %v", n, m.Settings().SubagentModels)
	}

	// Duplicates collapse, keeping the stored order with the fallback first.
	want := []string{"acme/coder", "own", "acme/other"}
	got, err := limit(map[string][]string{"fake": {"acme/coder", "own", "acme/coder", "acme/other", "own"}})
	if err != nil || !slices.Equal(got.SubagentModels["fake"], want) || len(got.SubagentModels) != 1 {
		t.Fatalf("limit = %v, %v", got.SubagentModels, err)
	}
	if n, list := prov.subagents(); n != 2 || !slices.Equal(list, want) {
		t.Fatalf("provider got %d lists, last %v", n, list)
	}
	if cfg, err := st.Load(); err != nil || !slices.Equal(cfg.WebSettings.SubagentModels["fake"], want) {
		t.Fatalf("stored = %v, %v", cfg.WebSettings.SubagentModels, err)
	}
	// The same list again changes nothing.
	if _, err := limit(map[string][]string{"fake": want}); err != nil {
		t.Fatal(err)
	}
	if n, _ := prov.subagents(); n != 2 {
		t.Fatalf("unchanged list pushed again: %d lists", n)
	}

	// Removing a custom model takes it off the list.
	want = []string{"own", "acme/other"}
	got, err = m.UpdateSettings(SettingsPatch{CustomModels: customModels(other)})
	if err != nil || !slices.Equal(got.SubagentModels["fake"], want) {
		t.Fatalf("after removing acme/coder = %v, %v", got.SubagentModels, err)
	}
	if _, list := prov.subagents(); !slices.Equal(list, want) {
		t.Fatalf("provider list after removal = %v", list)
	}
	if cfg, err := st.Load(); err != nil || !slices.Equal(cfg.WebSettings.SubagentModels["fake"], want) {
		t.Fatalf("stored after removal = %v, %v", cfg.WebSettings.SubagentModels, err)
	}

	// A restarted service hands the stored list to the provider.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	prov = newSubagentProvider()
	m = startManager(t, st, prov, plain)
	if _, list := prov.subagents(); !slices.Equal(list, want) || !slices.Equal(m.Settings().SubagentModels["fake"], want) {
		t.Fatalf("after restart provider got %v, settings %v", list, m.Settings().SubagentModels)
	}

	// Removing the last allowed models is refused rather than lifting the limit.
	if _, err := limit(map[string][]string{"fake": {"acme/other"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels()}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("removing the only allowed model = %v, want 400", err)
	}
	if list := m.Settings().SubagentModels["fake"]; !slices.Equal(list, []string{"acme/other"}) {
		t.Fatalf("after refused removal = %v", list)
	}
	if _, err := limit(map[string][]string{"fake": want}); err != nil {
		t.Fatal(err)
	}

	// An empty list lifts the limit.
	got, err = limit(map[string][]string{"fake": {}})
	if err != nil || got.SubagentModels != nil {
		t.Fatalf("lift = %v, %v", got.SubagentModels, err)
	}
	if _, list := prov.subagents(); len(list) != 0 {
		t.Fatalf("provider list after lift = %v", list)
	}
	if cfg, err := st.Load(); err != nil || cfg.WebSettings.SubagentModels != nil {
		t.Fatalf("stored after lift = %v, %v", cfg.WebSettings.SubagentModels, err)
	}
}

func TestSubagentModelsRoute(t *testing.T) {
	prov := newSubagentProvider()
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test", Assets: frameAssets()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m, prov: prov.Provider}
	auth := withCookie(ts)
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH /api/settings %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	limited := `{"send_default":"steer","terminal":false,"planner":false,"subagent_models":{"fake":["own"]}}`
	if got := patch(`{"subagent_models":{"fake":["own","own"]}}`, http.StatusOK); got != limited {
		t.Fatalf("PATCH subagent_models = %s", got)
	}
	for _, body := range []string{
		`{"subagent_models":null}`, `{"subagent_models":["own"]}`, `{"subagent_models":"own"}`, `{"subagent_models":{"fake":null}}`,
		`{"subagent_models":{"fake":"own"}}`, `{"subagent_models":{"fake":[1]}}`, `{"subagent_models":{"nobody":["own"]}}`,
		`{"subagent_models":{"fake":["missing"]}}`,
	} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, `"error"`) {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	if w := ts.do(http.MethodGet, "/api/settings", "", auth); strings.TrimSpace(w.Body.String()) != limited {
		t.Fatalf("GET after refused PATCHes = %s", w.Body)
	}
	if got := patch(`{"subagent_models":{"fake":[]}}`, http.StatusOK); got != `{"send_default":"steer","terminal":false,"planner":false}` {
		t.Fatalf("lift subagent_models = %s", got)
	}
}
