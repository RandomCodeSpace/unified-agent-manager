package copilot

import (
	"context"
	"errors"
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
	{Name: "local", BaseURL: "http://127.0.0.1:9/v1", ModelID: "m", Vision: true, WireAPI: "responses", APIKeyEnv: "UAM_TEST_LOCAL_KEY"},
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
	if len(models) != 3 || models[0].ID != "coder" || models[0].Provider != "acme" || models[0].Name != "Acme Coder" || models[1].ID != "org/fast" || models[1].Name != "acme/org/fast" || models[2].Provider != "local" {
		t.Fatalf("models = %+v", models)
	}
	for i, model := range models {
		if model.Capabilities == nil || model.Capabilities.Supports == nil || model.Capabilities.Supports.Vision == nil || *model.Capabilities.Supports.Vision != testCustom[i].Vision {
			t.Fatalf("model %s vision override = %+v", model.ID, model.Capabilities)
		}
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
// with no price, cost tier, effort or context size; images are opt-in.
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
	if c := models[3]; c.Media == nil || !c.Media.Images || c.Media.PDF {
		t.Fatalf("vision model = %+v", c)
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

func TestWebCustomModelVisionEditsReachTheNextPrompt(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	h := openWeb(t)
	models := slices.Clone(testCustom)
	h.p.SetCustomModels(models)
	ctx := context.Background()
	if err := h.conv.SetModel(ctx, "acme/coder", "", "default"); err != nil {
		t.Fatal(err)
	}
	for i, vision := range []bool{true, false, false} {
		models[0].Vision = vision
		h.p.SetCustomModels(models)
		if err := h.conv.Send(ctx, agentapi.Prompt{Text: "hello"}); err != nil {
			t.Fatal(err)
		}
		request := h.fs.modelRequests[len(h.fs.modelRequests)-1]
		if caps := request.ModelCapabilities; caps == nil || caps.Supports == nil || caps.Supports.Vision == nil || *caps.Supports.Vision != vision {
			t.Fatalf("vision %t: switch = %+v", vision, request)
		}
		if len(h.fs.msgs) != i+1 || len(h.fs.added) != 2 {
			t.Fatalf("messages %d, registry calls %v", len(h.fs.msgs), h.fs.added)
		}
		h.fs.onEvent(ev("idle", &rpc.SessionIdleData{}))
	}
	if len(h.fs.modelRequests) != 3 {
		t.Fatalf("unchanged vision caused another switch: %d requests", len(h.fs.modelRequests))
	}
}

func TestWebCustomModelVisionEditRefusesImagesDuringTurn(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	h := openWeb(t)
	models := slices.Clone(testCustom)
	h.p.SetCustomModels(models)
	ctx := context.Background()
	if err := h.conv.SetModel(ctx, "acme/coder", "", "default"); err != nil {
		t.Fatal(err)
	}
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	models[0].Vision = true
	h.p.SetCustomModels(models)
	image := agentapi.Prompt{Attachments: []agentapi.Blob{{MIME: "image/png", Data: []byte("png")}}}
	if err := h.conv.Steer(ctx, image); err == nil || !strings.Contains(err.Error(), "wait for the current turn") {
		t.Fatalf("image steer after vision edit: %v", err)
	}
	if len(h.fs.msgs) != 1 || len(h.fs.modelRequests) != 1 {
		t.Fatal("vision edit switched models or sent an image during a turn")
	}
	h.fs.onEvent(ev("idle", &rpc.SessionIdleData{}))
	if err := h.conv.Send(ctx, image); err != nil || len(h.fs.msgs) != 2 || len(h.fs.msgs[1].Attachments) != 1 {
		t.Fatalf("image after idle: %v, messages %+v", err, h.fs.msgs)
	}
}

func TestWebCustomModelVisionEditFailureDoesNotSend(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	h := openWeb(t)
	models := slices.Clone(testCustom)
	h.p.SetCustomModels(models)
	ctx := context.Background()
	if err := h.conv.SetModel(ctx, "acme/coder", "", "default"); err != nil {
		t.Fatal(err)
	}
	models[0].Vision = true
	h.p.SetCustomModels(models)
	h.fs.modelErr = errors.New("JSON-RPC Error: vision override refused")
	if err := h.conv.Send(ctx, agentapi.Prompt{Text: "hello"}); err == nil || len(h.fs.msgs) != 0 {
		t.Fatalf("failed vision override: %v, sent %d messages", err, len(h.fs.msgs))
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
	// A model no custom model serves keeps its own name.
	c.onEvent(copilot.SessionEvent{Data: &rpc.AssistantUsageData{Model: "elsewhere", IsByok: copilot.Bool(true)}})
	c.mu.Lock()
	got = c.turnModel
	c.mu.Unlock()
	if got != "elsewhere" {
		t.Fatalf("unmatched turn model = %q", got)
	}
}

// A custom model the open session refuses is not switched to, and is
// registered on the next try.
func TestWebCustomModelRegistrationFailure(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	h := openWeb(t)
	h.p.SetCustomModels(testCustom)
	ctx := context.Background()
	h.fs.addErr = errors.New("provider refused")
	if err := h.conv.SetModel(ctx, "acme/coder", "", "default"); err == nil || !strings.Contains(err.Error(), "register custom model acme/coder: provider refused") || len(h.fs.models) != 0 {
		t.Fatalf("SetModel = %v, switched to %v", err, h.fs.models)
	}
	h.fs.addErr = nil
	if err := h.conv.SetModel(ctx, "acme/coder", "", "default"); err != nil || !slices.Equal(h.fs.models, []string{"acme/coder"}) {
		t.Fatalf("retry = %v, switched to %v", err, h.fs.models)
	}
}

// Two providers serving one model ID: the turn names the one the
// conversation selected, not the first configured.
func TestWebCustomModelTurnPrefersSelectedProvider(t *testing.T) {
	t.Setenv("UAM_TEST_ACME_KEY", "acme-secret")
	t.Setenv("UAM_TEST_LOCAL_KEY", "local-secret")
	h := openWeb(t)
	h.p.SetCustomModels(append(slices.Clone(testCustom), agentapi.CustomModel{Name: "mirror", BaseURL: "https://mirror.example/v1", ModelID: "coder", APIKeyEnv: "UAM_TEST_LOCAL_KEY"}))
	c := h.conv.(*conversation)
	turn := func() string {
		c.onEvent(copilot.SessionEvent{Data: &rpc.AssistantUsageData{Model: "coder", IsByok: copilot.Bool(true)}})
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.turnModel
	}
	if got := turn(); got != "acme/coder" {
		t.Fatalf("with nothing selected: %q", got)
	}
	for _, id := range []string{"mirror/coder", "acme/coder", "mirror/coder"} {
		if err := h.conv.SetModel(context.Background(), id, "", "default"); err != nil {
			t.Fatal(err)
		}
		if got := turn(); got != id {
			t.Fatalf("selected %s, turn names %q", id, got)
		}
	}
}

// Saving a direct key recovers the missing-environment failure without a
// service restart, for new, resumed and utility conversations.
func TestBYOMDirectKey(t *testing.T) {
	t.Setenv("UAM_TEST_DIRECT_KEY", "")
	fc := &fakeClient{reply: func(context.Context, copilot.MessageOptions) (string, error) { return "A title", nil }}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	model := agentapi.CustomModel{Name: "direct", BaseURL: "https://llm.example/v1", ModelID: "coder", APIKeyEnv: "UAM_TEST_DIRECT_KEY"}
	p.SetCustomModels([]agentapi.CustomModel{model})
	req := agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Model: "direct/coder", Events: &recSink{}}
	if _, err := p.Open(t.Context(), req); err == nil {
		t.Fatal("missing environment key accepted")
	}
	model.APIKeyEnv, model.APIKey = "", "fixture-key"
	p.SetCustomModels([]agentapi.CustomModel{model})
	if _, err := p.Open(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	req.SessionID, req.ConversationID = "s-2", "conv-2"
	if _, err := p.Open(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Title(t.Context(), agentapi.TitleRequest{Model: "direct/coder", Workdir: "/work", Text: "fix it"}); err != nil {
		t.Fatal(err)
	}
	if len(fc.create) != 2 || len(fc.resume) != 1 {
		t.Fatal("expected new, resumed and utility conversations")
	}
	for _, providers := range [][]copilot.NamedProviderConfig{fc.create[0].Providers, fc.resume[0].Providers, fc.create[1].Providers} {
		if len(providers) != 1 || providers[0].APIKey != "fixture-key" {
			t.Fatal("direct key did not reach SDK conversation")
		}
	}
}
