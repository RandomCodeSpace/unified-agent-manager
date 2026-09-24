package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

var acme = store.WebCustomModel{Name: "acme", DisplayName: "Acme Coder", BaseURL: "https://llm.example/v1", ModelID: "coder", APIKeyEnv: "UAM_BYOM_TEST_ACME"}

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
	t.Setenv("UAM_BYOM_TEST_ACME", "acme-secret")
	st := openTestStore(t)
	prov := &customProvider{Provider: agenttest.NewProvider("fake", allCaps)}
	m := startManager(t, st, prov)
	if got := modelIDs(m, "fake"); !slices.Equal(got, []string{"own"}) {
		t.Fatalf("models before = %v", got)
	}
	for name, bad := range map[string]store.WebCustomModel{
		"slash name": {Name: "a/b", BaseURL: acme.BaseURL, ModelID: "m", APIKeyEnv: "UAM_BYOM_K"},
		"file url":   {Name: "a", BaseURL: "file:///etc/passwd", ModelID: "m", APIKeyEnv: "UAM_BYOM_K"},
		"env syntax": {Name: "a", BaseURL: acme.BaseURL, ModelID: "m", APIKeyEnv: "$HOME"},
		"env prefix": {Name: "a", BaseURL: acme.BaseURL, ModelID: "m", APIKeyEnv: "GITHUB_TOKEN"},
	} {
		if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(bad)}); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s: %v, want 400", name, err)
		}
	}
	if _, err := os.Stat(st.Path()); !os.IsNotExist(err) {
		t.Fatalf("a refused change wrote the store: %v", err)
	}
	unset := store.WebCustomModel{Name: "local", BaseURL: "http://127.0.0.1:9/v1", ModelID: "m", WireAPI: "responses", APIKeyEnv: "UAM_BYOM_TEST_UNSET"}
	got, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(acme, unset)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.CustomModels) != 2 || !got.CustomModels[0].KeyPresent || got.CustomModels[1].KeyPresent || got.CustomModels[0].APIKeyEnv != "UAM_BYOM_TEST_ACME" {
		t.Fatalf("settings = %+v", got.CustomModels)
	}
	if ids := modelIDs(m, "fake"); !slices.Equal(ids, []string{"own", "acme/coder", "local/m"}) {
		t.Fatalf("models after add = %v", ids)
	}
	if last := prov.last(); len(last) != 2 || last[0].APIKeyEnv != "UAM_BYOM_TEST_ACME" || last[1].WireAPI != "responses" {
		t.Fatalf("provider got %+v", last)
	}
	data, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "acme-secret") || !strings.Contains(string(data), `"UAM_BYOM_TEST_ACME"`) {
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
	t.Setenv("UAM_BYOM_TEST_ACME", "acme-secret")
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
	for _, body := range []string{`{"custom_models":null}`, `{"custom_models":{}}`, `{"custom_models":[{"name":"a","base_url":"https://x/v1?k=1","model_id":"m","api_key_env":"UAM_BYOM_K"}]}`} {
		patch(body, http.StatusBadRequest)
	}
	if got := patch(`{"custom_models":[{"name":"a","base_url":"https://x/v1","model_id":"m","api_key_env":"HOME"}]}`, http.StatusBadRequest); !strings.Contains(got, "UAM_BYOM_") {
		t.Fatalf("unprefixed key variable = %s", got)
	}
	// key_present is output only: a browser sending it back changes nothing.
	body := `{"custom_models":[{"name":"acme","display_name":"Acme Coder","base_url":"https://llm.example/v1","model_id":"coder","api_key_env":"UAM_BYOM_TEST_ACME","key_present":false}]}`
	want := `{"send_default":"steer","custom_models":[{"name":"acme","display_name":"Acme Coder","base_url":"https://llm.example/v1","model_id":"coder","api_key_env":"UAM_BYOM_TEST_ACME","key_present":true}]}`
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

// Removing a custom model also stops hiding it and stops it titling Tasks,
// in the same change; other hidden models and title models stay.
func TestRemovedCustomModelLeavesHiddenAndTitle(t *testing.T) {
	st := openTestStore(t)
	caps := allCaps
	caps.Titles = true
	prov := &customProvider{Provider: agenttest.NewProvider("fake", caps)}
	m := startManager(t, st, prov)
	other := store.WebCustomModel{Name: "acme", BaseURL: acme.BaseURL, ModelID: "other", APIKeyEnv: acme.APIKeyEnv}
	if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(acme, other)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSettings(SettingsPatch{HiddenModels: map[string][]string{"fake": {"acme/coder", "acme/other"}}, TitleModel: map[string]string{"fake": "acme/coder"}}); err != nil {
		t.Fatal(err)
	}
	got, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(other)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.HiddenModels["fake"], []string{"acme/other"}) || got.TitleModel != nil {
		t.Fatalf("after removing acme/coder: hidden %v title %v", got.HiddenModels, got.TitleModel)
	}
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "acme/other"}}); err != nil {
		t.Fatal(err)
	}
	got, err = m.UpdateSettings(SettingsPatch{CustomModels: customModels()})
	if err != nil || got.HiddenModels != nil || got.TitleModel != nil || got.CustomModels != nil {
		t.Fatalf("after removing all = %+v, %v", got, err)
	}
	cfg, err := st.Load()
	if err != nil || cfg.WebSettings.HiddenModels != nil || cfg.WebSettings.TitleModel != nil || cfg.WebSettings.CustomModels != nil {
		t.Fatalf("stored = %+v, %v", cfg.WebSettings, err)
	}
}

// keyedProvider refuses custom model selections while its key is missing,
// as the Copilot adapter does; nothing reaches a model.
type keyedProvider struct {
	*customProvider
	missing atomic.Bool
}

func (p *keyedProvider) Open(ctx context.Context, req agentapi.OpenRequest) (agentapi.Conversation, error) {
	c, err := p.customProvider.Open(ctx, req)
	if err != nil {
		return nil, err
	}
	return keyedConv{Conversation: c, p: p}, nil
}

type keyedConv struct {
	agentapi.Conversation
	p *keyedProvider
}

func (c keyedConv) SetModel(ctx context.Context, model, effort, size string) error {
	if strings.Contains(model, "/") && c.p.missing.Load() {
		return errors.New("custom model " + model + " needs its API key in the environment variable UAM_BYOM_TEST_ACME, which is not set")
	}
	return c.Conversation.SetModel(ctx, model, effort, size)
}

// A Task whose custom model's key is missing fails to open and says why. It
// recovers on the next prompt, either after a switch to another model or
// once the key is set and the service restarted: that prompt reopens it
// explicitly, the failure clears, and the prompt is sent once.
func TestTaskRecoversFromMissingCustomModelKey(t *testing.T) {
	st := openTestStore(t)
	switched, restarted := mustUUID(t), mustUUID(t)
	now := time.Now().UTC()
	for _, id := range []string{switched, restarted} {
		seedLegacyWebRecord(t, st, id, t.TempDir(), &store.WebState{Turn: StateCompleted, UpdatedAt: now, Model: "acme/coder"})
	}
	newProv := func(missing bool) *keyedProvider {
		p := &keyedProvider{customProvider: &customProvider{Provider: agenttest.NewProvider("fake", allCaps)}}
		p.AddConversation("conv_"+switched[:8], nil)
		p.AddConversation("conv_"+restarted[:8], nil)
		p.missing.Store(missing)
		return p
	}
	prov := newProv(true)
	m := NewManager(st, []agentapi.Provider{prov})
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{switched, restarted} {
		if _, err := m.Submit(id, PromptRequest{Text: "hello", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
			t.Fatal(err)
		}
		if d := detail(t, m, id); d.State != StateFailed || !strings.Contains(d.StateDetail, "UAM_BYOM_TEST_ACME") {
			t.Fatalf("open without the key: %s %q", d.State, d.StateDetail)
		}
	}

	// Switching to an account model is stored while the Task is closed; the
	// old failure shows until the next prompt reopens it.
	if _, err := m.SetModel(switched, setting("own"), nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Submit(switched, PromptRequest{Text: "again", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	if d := detail(t, m, switched); d.State == StateFailed || d.StateDetail != "" || d.Model != "own" || len(conv.Sends()) != 1 || !slices.Equal(conv.ModelSets(), []string{"own"}) {
		t.Fatalf("after the switch: %s %q model %s sends %d sets %q", d.State, d.StateDetail, d.Model, len(conv.Sends()), conv.ModelSets())
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	// With the key set and the service restarted, the failed Task stays
	// failed until the next prompt, which reopens it on its custom model.
	prov = newProv(false)
	m = startManager(t, st, prov)
	if err := m.View(context.Background(), restarted); err != nil {
		t.Fatal(err)
	}
	if d := detail(t, m, restarted); d.State != StateFailed {
		t.Fatalf("a failed Task reopened for a viewer: %s", d.State)
	}
	if _, err := m.Submit(restarted, PromptRequest{Text: "again", RequestID: mustUUID(t), Mode: ModeSend}); err != nil {
		t.Fatal(err)
	}
	conv = prov.Last()
	if d := detail(t, m, restarted); d.State == StateFailed || d.StateDetail != "" || len(conv.Sends()) != 1 || !slices.Equal(conv.ModelSets(), []string{"acme/coder"}) {
		t.Fatalf("after restart with the key: %s %q sends %d sets %q", d.State, d.StateDetail, len(conv.Sends()), conv.ModelSets())
	}
}
