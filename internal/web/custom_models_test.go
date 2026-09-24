package web

import (
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// customProvider lists its custom models after its own, as the Copilot
// adapter does.
type customProvider struct {
	*agenttest.Provider
	mu  sync.Mutex
	got [][]agentapi.CustomModel
}

func (p *customProvider) SetCustomModels(models []agentapi.CustomModel) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.got = append(p.got, models)
	list := []agentapi.Model{{ID: "own"}}
	for _, m := range models {
		list = append(list, agentapi.Model{ID: m.SelectionID(), Name: m.DisplayName})
	}
	p.SetModels(list, nil)
}

func (p *customProvider) last() []agentapi.CustomModel {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.got) == 0 {
		return nil
	}
	return p.got[len(p.got)-1]
}

var acme = store.WebCustomModel{Name: "acme", DisplayName: "Acme Coder", BaseURL: "https://llm.example/v1", ModelID: "coder", APIKeyEnv: "UAM_TEST_ACME_KEY"}

func customModels(list ...store.WebCustomModel) *[]store.WebCustomModel { return &list }

func modelIDs(m *Manager, provider string) []string {
	var ids []string
	for _, info := range m.Providers() {
		if info.Name == provider {
			for _, mo := range info.Models {
				ids = append(ids, mo.ID)
			}
		}
	}
	return ids
}

// Custom models are validated, stored, handed to the provider, listed at
// once, and shown with whether the key variable is set, never its value.
func TestCustomModelsStoredListedAndRedacted(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	st := openTestStore(t)
	prov := &customProvider{Provider: agenttest.NewProvider("fake", allCaps)}
	m := startManager(t, st, prov)
	if got := modelIDs(m, "fake"); !slices.Equal(got, []string{"own"}) {
		t.Fatalf("models before = %v", got)
	}
	for name, bad := range map[string]store.WebCustomModel{
		"slash name": {Name: "a/b", BaseURL: acme.BaseURL, ModelID: "m", APIKeyEnv: "K"},
		"file url":   {Name: "a", BaseURL: "file:///etc/passwd", ModelID: "m", APIKeyEnv: "K"},
		"env syntax": {Name: "a", BaseURL: acme.BaseURL, ModelID: "m", APIKeyEnv: "$HOME"},
	} {
		if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(bad)}); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s: %v, want 400", name, err)
		}
	}
	if _, err := os.Stat(st.Path()); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote the store: %v", err)
	}
	unset := store.WebCustomModel{Name: "local", BaseURL: "http://127.0.0.1:9/v1", ModelID: "m", WireAPI: "responses", APIKeyEnv: "UAM_TEST_UNSET_KEY"}
	got, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(acme, unset)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CustomModels) != 2 || !got.CustomModels[0].KeyPresent || got.CustomModels[1].KeyPresent || got.CustomModels[0].APIKeyEnv != "UAM_TEST_ACME_KEY" {
		t.Fatalf("settings = %+v", got.CustomModels)
	}
	if ids := modelIDs(m, "fake"); !slices.Equal(ids, []string{"own", "acme/coder", "local/m"}) {
		t.Fatalf("models after add = %v", ids)
	}
	if last := prov.last(); len(last) != 2 || last[0].APIKeyEnv != "UAM_TEST_ACME_KEY" || last[1].WireAPI != "responses" {
		t.Fatalf("provider got %+v", last)
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "acme-secret") || !strings.Contains(string(data), `"UAM_TEST_ACME_KEY"`) {
		t.Fatalf("stored = %s", data)
	}

	// A restart hands the stored models to the provider before its catalog loads.
	if err := m.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	prov2 := &customProvider{Provider: agenttest.NewProvider("fake", allCaps)}
	m2 := startManager(t, st, prov2)
	if ids := modelIDs(m2, "fake"); !slices.Equal(ids, []string{"own", "acme/coder", "local/m"}) {
		t.Fatalf("models after restart = %v", ids)
	}
	if got, err := m2.UpdateSettings(SettingsPatch{CustomModels: customModels()}); err != nil || got.CustomModels != nil || len(prov2.last()) != 0 {
		t.Fatalf("remove all = %+v, %v", got, err)
	}
	if ids := modelIDs(m2, "fake"); !slices.Equal(ids, []string{"own"}) {
		t.Fatalf("models after remove = %v", ids)
	}
}

func TestCustomModelsRoute(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	for _, body := range []string{`{"custom_models":null}`, `{"custom_models":{}}`, `{"custom_models":[{"name":"a","base_url":"https://x/v1?k=1","model_id":"m","api_key_env":"K"}]}`} {
		patch(body, http.StatusBadRequest)
	}
	// key_present is output only: a browser sending it back changes nothing.
	body := `{"custom_models":[{"name":"acme","display_name":"Acme Coder","base_url":"https://llm.example/v1","model_id":"coder","api_key_env":"UAM_TEST_ACME_KEY","key_present":false}]}`
	want := `{"send_default":"steer","custom_models":[{"name":"acme","display_name":"Acme Coder","base_url":"https://llm.example/v1","model_id":"coder","api_key_env":"UAM_TEST_ACME_KEY","key_present":true}]}`
	if got := patch(body, http.StatusOK); got != want {
		t.Fatalf("PATCH = %s", got)
	}
	w := ts.do(http.MethodGet, "/api/settings", "", auth)
	if got := strings.TrimSpace(w.Body.String()); got != want || strings.Contains(got, "acme-secret") {
		t.Fatalf("GET = %s", got)
	}
	if got := patch(`{"custom_models":[]}`, http.StatusOK); got != `{"send_default":"steer"}` {
		t.Fatalf("remove = %s", got)
	}
}
