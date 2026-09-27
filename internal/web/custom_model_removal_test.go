package web

import (
	"slices"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// A change that removes a custom model while it also hides that model and
// makes it the title model neither hides it nor titles with it; the rest of
// the change applies.
func TestCustomModelRemovedInTheChangeThatUsesIt(t *testing.T) {
	st := openTestStore(t)
	caps := allCaps
	caps.Titles = true
	prov := &customProvider{Provider: agenttest.NewProvider("fake", caps)}
	m := startManager(t, st, prov)
	other := store.WebCustomModel{Name: "acme", BaseURL: acme.BaseURL, ModelID: "other", APIKeyEnv: acme.APIKeyEnv}
	if _, err := m.UpdateSettings(SettingsPatch{CustomModels: customModels(acme, other)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UpdateSettings(SettingsPatch{HiddenModels: map[string][]string{"fake": {"acme/other"}}, TitleModel: map[string]string{"fake": "acme/other"}}); err != nil {
		t.Fatal(err)
	}
	got, err := m.UpdateSettings(SettingsPatch{
		CustomModels: customModels(other),
		HiddenModels: map[string][]string{"fake": {"own", "acme/coder"}},
		TitleModel:   map[string]string{"fake": "acme/coder"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.HiddenModels["fake"], []string{"own"}) || got.TitleModel != nil || len(got.CustomModels) != 1 || got.CustomModels[0].ModelID != "other" {
		t.Fatalf("settings = hidden %v, title %v, custom %+v", got.HiddenModels, got.TitleModel, got.CustomModels)
	}
	cfg, err := st.Load()
	if err != nil || !slices.Equal(cfg.WebSettings.HiddenModels["fake"], []string{"own"}) || cfg.WebSettings.TitleModel != nil {
		t.Fatalf("stored = %+v, %v", cfg.WebSettings, err)
	}
}
