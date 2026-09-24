package copilot

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// SetCustomModels replaces the models brought by the owner. The CLI registers
// them per session as named OpenAI-compatible providers, additive to the
// account's own models (experimental SDK surface, measured on CLI 1.0.88).
func (p *webProvider) SetCustomModels(models []agentapi.CustomModel) {
	p.customMu.Lock()
	defer p.customMu.Unlock()
	p.custom = slices.Clone(models)
}

func (p *webProvider) customModels() []agentapi.CustomModel {
	p.customMu.Lock()
	defer p.customMu.Unlock()
	return slices.Clone(p.custom)
}

// customModel returns the custom model whose selection ID is id.
func (p *webProvider) customModel(id string) (agentapi.CustomModel, bool) {
	for _, m := range p.customModels() {
		if m.SelectionID() == id {
			return m, true
		}
	}
	return agentapi.CustomModel{}, false
}

// customKeyErr refuses a custom model whose key variable is unset or empty
// in the service environment, so the model fails with a reason instead of
// an unauthenticated request. It is nil for any other model.
func (p *webProvider) customKeyErr(id string) error {
	m, ok := p.customModel(id)
	if !ok || os.Getenv(m.APIKeyEnv) != "" {
		return nil
	}
	return fmt.Errorf("custom model %s needs its API key in the environment variable %s, which is not set in the uam web service's environment; export it where the service starts and restart the service", id, m.APIKeyEnv)
}

// byom builds the session registry for models: one provider per Name,
// with the key read from the service environment now, and one model each.
func byom(models []agentapi.CustomModel) ([]copilot.NamedProviderConfig, []copilot.ProviderModelConfig) {
	var providers []copilot.NamedProviderConfig
	var out []copilot.ProviderModelConfig
	for _, m := range models {
		if !slices.ContainsFunc(providers, func(pc copilot.NamedProviderConfig) bool { return pc.Name == m.Name }) {
			providers = append(providers, copilot.NamedProviderConfig{Name: m.Name, Type: "openai", WireAPI: m.WireAPI, BaseURL: m.BaseURL, APIKey: os.Getenv(m.APIKeyEnv)})
		}
		out = append(out, copilot.ProviderModelConfig{ID: m.ModelID, Provider: m.Name, Name: cmp.Or(m.DisplayName, m.SelectionID())})
	}
	return providers, out
}

// customCatalog lists the custom models as selectable models: no prices,
// cost tier, efforts or context sizes, and no uploads, as the CLI gives a
// custom model no vision.
func customCatalog(models []agentapi.CustomModel) []agentapi.Model {
	out := make([]agentapi.Model, 0, len(models))
	for _, m := range models {
		out = append(out, agentapi.Model{ID: m.SelectionID(), Name: cmp.Or(m.DisplayName, m.SelectionID()), Efforts: []string{}, ContextSizes: []agentapi.ContextSize{}, Media: &agentapi.Media{}})
	}
	return out
}

// registered records the custom models a session was given.
type registered struct {
	providers, models map[string]bool
}

func newRegistered(models []agentapi.CustomModel) registered {
	r := registered{providers: map[string]bool{}, models: map[string]bool{}}
	for _, m := range models {
		r.providers[m.Name], r.models[m.SelectionID()] = true, true
	}
	return r
}

// registerCustom adds custom model id to the open session when it was
// configured after the session opened, with its provider unless the session
// already has one of that name.
func (c *conversation) registerCustom(ctx context.Context, id string) error {
	m, ok := c.p.customModel(id)
	c.mu.Lock()
	done := !ok || c.byom.models[id]
	hasProvider := c.byom.providers[m.Name]
	c.mu.Unlock()
	if done {
		return nil
	}
	providers, models := byom([]agentapi.CustomModel{m})
	if hasProvider {
		providers = nil
	}
	if err := c.sess.AddProviders(ctx, providers, models); err != nil {
		c.p.poke()
		return fmt.Errorf("register custom model %s: %s", id, errText(err))
	}
	c.mu.Lock()
	c.byom.providers[m.Name], c.byom.models[id] = true, true
	c.mu.Unlock()
	return nil
}

// addProviders converts the SDK's session config types to the
// session.provider.add request.
func addProviders(providers []copilot.NamedProviderConfig, models []copilot.ProviderModelConfig) *rpc.ProviderAddRequest {
	req := &rpc.ProviderAddRequest{}
	for _, pc := range providers {
		typ := rpc.ProviderConfigType(pc.Type)
		np := rpc.NamedProviderConfig{Name: pc.Name, Type: &typ, BaseURL: pc.BaseURL}
		if pc.WireAPI != "" {
			wire := rpc.ProviderConfigWireAPI(pc.WireAPI)
			np.WireAPI = &wire
		}
		if pc.APIKey != "" {
			key := pc.APIKey
			np.APIKey = &key
		}
		req.Providers = append(req.Providers, np)
	}
	for _, m := range models {
		name := m.Name
		req.Models = append(req.Models, rpc.ProviderModelConfig{ID: m.ID, Provider: m.Provider, Name: &name})
	}
	return req
}

// customSelection maps a BYOK model call's provider-local model back to its
// selection ID, so the turn is not shown as routed to another model.
func (p *webProvider) customSelection(model string) string {
	for _, m := range p.customModels() {
		if m.ModelID == model {
			return m.SelectionID()
		}
	}
	return model
}
