package copilot

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

var testCustom = []agentapi.CustomModel{
	{Name: "acme", DisplayName: "Acme Coder", BaseURL: "https://llm.example/v1", ModelID: "coder", APIKeyEnv: "UAM_TEST_ACME_KEY"},
	{Name: "acme", BaseURL: "https://llm.example/v1", ModelID: "org/fast", APIKeyEnv: "UAM_TEST_ACME_KEY"},
	{Name: "local", BaseURL: "http://127.0.0.1:9/v1", ModelID: "m", WireAPI: "responses", APIKeyEnv: "UAM_TEST_LOCAL_KEY"},
}

func wantRegistry(t *testing.T, providers []copilot.NamedProviderConfig, models []copilot.ProviderModelConfig) {
	t.Helper()
	got := []string{}
	for _, p := range providers {
		got = append(got, strings.Join([]string{p.Name, p.Type, p.WireAPI, p.BaseURL, p.APIKey}, " "))
	}
	if want := []string{"acme openai  https://llm.example/v1 acme-secret", "local openai responses http://127.0.0.1:9/v1 "}; !slices.Equal(got, want) {
		t.Fatalf("providers = %q, want %q", got, want)
	}
	if len(models) != 3 || models[0] != (copilot.ProviderModelConfig{ID: "coder", Provider: "acme", Name: "Acme Coder"}) || models[1].ID != "org/fast" || models[1].Name != "acme/org/fast" || models[2].Provider != "local" {
		t.Fatalf("models = %+v", models)
	}
}

// Every session the provider opens gets the custom models, with each key
// read from the service environment: new, resumed and title sessions.
func TestWebCustomModelsReachEverySession(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	t.Setenv("UAM_TEST_LOCAL_KEY", "")
	fc := &fakeClient{reply: func(context.Context, copilot.MessageOptions) (string, error) { return "A title", nil }}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	p.SetCustomModels(testCustom)
	ctx := context.Background()
	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Model: "acme/coder", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "s-2", ConversationID: "conv-2", Workdir: "/work", Events: &recSink{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Title(ctx, agentapi.TitleRequest{Model: "acme/coder", Workdir: "/work", Text: "fix it"}); err != nil {
		t.Fatal(err)
	}
	if len(fc.create) != 2 || len(fc.resume) != 1 {
		t.Fatalf("create %d resume %d", len(fc.create), len(fc.resume))
	}
	if fc.create[0].Model != "acme/coder" {
		t.Fatalf("created with model %q", fc.create[0].Model)
	}
	wantRegistry(t, fc.create[0].Providers, fc.create[0].Models)
	wantRegistry(t, fc.resume[0].Providers, fc.resume[0].Models)
	wantRegistry(t, fc.create[1].Providers, fc.create[1].Models)
}

// Models lists the account's models, then the custom ones by selection ID
// with no price, cost tier, effort, context size or uploads.
func TestWebModelsAppendCustomModels(t *testing.T) {
	fc := &fakeClient{models: []rpc.Model{{ID: "gpt-6-luna", Name: "GPT-6 Luna"}}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	p.SetCustomModels(testCustom)
	models, err := p.Models(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, m := range models {
		ids = append(ids, m.ID+"="+m.Name)
	}
	if strings.Join(ids, ",") != "gpt-6-luna=GPT-6 Luna,acme/coder=Acme Coder,acme/org/fast=acme/org/fast,local/m=local/m" {
		t.Fatalf("models = %v", ids)
	}
	c := models[1]
	if c.Prices != nil || c.CostTier != "" || c.DiscountPercent != 0 || len(c.Efforts) != 0 || len(c.ContextSizes) != 0 || c.Media == nil || c.Media.Images || c.Media.PDF {
		t.Fatalf("custom model = %+v", c)
	}
}

// A custom model whose key variable is unset fails its own selection with
// the variable's name; Copilot models are unaffected.
func TestWebCustomModelWithoutKeyFailsClearly(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "")
	h := openWeb(t)
	h.p.SetCustomModels(testCustom)
	err := h.conv.SetModel(context.Background(), "acme/coder", "", "default")
	if err == nil || !strings.Contains(err.Error(), "UAM_TEST_ACME_KEY") || !strings.Contains(err.Error(), "not set") {
		t.Fatalf("SetModel = %v", err)
	}
	if len(h.fs.modelRequests) != 0 || len(h.fs.added) != 0 {
		t.Fatalf("switched %d, added %v", len(h.fs.modelRequests), h.fs.added)
	}
	if err := h.conv.SetModel(context.Background(), "gpt-6-luna", "", "default"); err != nil {
		t.Fatalf("Copilot model: %v", err)
	}
	if _, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", Workdir: "/work", Model: "acme/coder", Events: &recSink{}}); err == nil || !strings.Contains(err.Error(), "UAM_TEST_ACME_KEY") {
		t.Fatalf("Open = %v", err)
	}
	if _, err := h.p.Title(context.Background(), agentapi.TitleRequest{Model: "acme/coder", Workdir: "/work", Text: "x"}); err == nil || !strings.Contains(err.Error(), "UAM_TEST_ACME_KEY") {
		t.Fatalf("Title = %v", err)
	}
}

// A custom model configured after the session opened is registered on it
// before the switch, its provider only once.
func TestWebCustomModelAddedToOpenSession(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	h := openWeb(t)
	h.p.SetCustomModels(testCustom)
	ctx := context.Background()
	for _, id := range []string{"acme/coder", "acme/org/fast", "acme/coder"} {
		if err := h.conv.SetModel(ctx, id, "", "default"); err != nil {
			t.Fatalf("SetModel %s: %v", id, err)
		}
	}
	if want := []string{"provider acme key acme-secret", "model acme/coder", "model acme/org/fast"}; !slices.Equal(h.fs.added, want) {
		t.Fatalf("added = %v, want %v", h.fs.added, want)
	}
	if !slices.Equal(h.fs.models, []string{"acme/coder", "acme/org/fast", "acme/coder"}) {
		t.Fatalf("switched to %v", h.fs.models)
	}
}

// A BYOK model call reports the provider-local model; the turn names the
// selection ID so it does not look routed to another model.
func TestWebCustomModelTurnKeepsSelectionID(t *testing.T) {
	h := openWeb(t)
	h.p.SetCustomModels(testCustom)
	c := h.conv.(*conversation)
	c.onEvent(copilot.SessionEvent{Data: &rpc.AssistantUsageData{Model: "coder", IsByok: copilot.Bool(true)}})
	c.mu.Lock()
	got := c.turnModel
	c.mu.Unlock()
	if got != "acme/coder" {
		t.Fatalf("turn model = %q", got)
	}
}
