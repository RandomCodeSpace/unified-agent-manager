package web

import (
	"maps"
	"os"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// withoutRemovedCustom extends a settings change that sets the custom models
// to list: a custom model it removes is no longer hidden (hidden gains the
// provider's list without it), nor a subagent model (likewise in subagents),
// nor a title model (the returned title change clears it).
func withoutRemovedCustom(current Settings, list []store.WebCustomModel, hidden, subagents map[string][]string, titles map[string]string) map[string]string {
	removed := func(id string) bool {
		return slices.ContainsFunc(current.CustomModels, func(c CustomModel) bool { return c.Name+"/"+c.ModelID == id }) &&
			!slices.ContainsFunc(list, func(c store.WebCustomModel) bool { return c.Name+"/"+c.ModelID == id })
	}
	prune := func(current, change map[string][]string) {
		for provider, ids := range current {
			if c, ok := change[provider]; ok {
				ids = c
			}
			if kept := slices.DeleteFunc(slices.Clone(ids), removed); len(kept) != len(ids) {
				change[provider] = kept
			}
		}
	}
	prune(current.HiddenModels, hidden)
	prune(current.SubagentModels, subagents)
	out := maps.Clone(titles)
	if out == nil {
		out = map[string]string{}
	}
	for provider, model := range current.TitleModel {
		if change, ok := titles[provider]; ok {
			model = change
		}
		if removed(model) {
			out[provider] = ""
		}
	}
	for provider, model := range titles {
		if removed(model) {
			out[provider] = ""
		}
	}
	return out
}

// savedCustomKey reuses a key only for the same provider connection. Changing
// an endpoint or provider name requires entering the key again.
func savedCustomKey(list []store.WebCustomModel, name, baseURL, wireAPI string) string {
	for _, c := range list {
		if c.Name == name && c.BaseURL == baseURL && c.WireAPI == wireAPI && c.APIKeyEnv == "" {
			return c.APIKey
		}
	}
	return ""
}

// customModelsView is the settings view of stored custom models: each says
// whether a saved or environment key is available, never its value.
func customModelsView(list []store.WebCustomModel) []CustomModel {
	if len(list) == 0 {
		return nil
	}
	out := make([]CustomModel, 0, len(list))
	for _, c := range list {
		out = append(out, CustomModel{Name: c.Name, DisplayName: c.DisplayName, BaseURL: c.BaseURL, ModelID: c.ModelID, Vision: c.Vision, WireAPI: c.WireAPI, APIKeyEnv: c.APIKeyEnv, KeyPresent: c.APIKey != "" || os.Getenv(c.APIKeyEnv) != "", KeyError: c.KeyError})
	}
	return out
}

// setCustomModels gives list to every provider that offers custom models.
func (m *Manager) setCustomModels(list []store.WebCustomModel) {
	models := make([]agentapi.CustomModel, 0, len(list))
	for _, c := range list {
		models = append(models, agentapi.CustomModel{Name: c.Name, DisplayName: c.DisplayName, BaseURL: c.BaseURL, ModelID: c.ModelID, Vision: c.Vision, WireAPI: c.WireAPI, APIKeyEnv: c.APIKeyEnv, APIKey: c.APIKey})
	}
	for _, name := range m.order {
		if u, ok := m.providers[name].(agentapi.CustomModelUser); ok {
			u.SetCustomModels(models)
		}
	}
}

// setSubagentModels gives each provider that can limit its subagents' models
// its list; a provider without one is not limited.
func (m *Manager) setSubagentModels(byProvider map[string][]string) {
	for _, name := range m.order {
		if u, ok := m.providers[name].(agentapi.SubagentModelUser); ok {
			u.SetSubagentModels(byProvider[name])
		}
	}
}

// setGitRules gives s's git rules to each provider whose agents commit.
func (m *Manager) setGitRules(s Settings) {
	rules, providerCoAuthor := s.gitRules()
	for _, name := range m.order {
		if u, ok := m.providers[name].(agentapi.GitRulesUser); ok {
			u.SetGitRules(rules, providerCoAuthor)
		}
	}
}

// setGitHubMCP turns the built-in GitHub MCP server on or off in each
// provider that has one.
func (m *Manager) setGitHubMCP(on bool) {
	for _, name := range m.order {
		if u, ok := m.providers[name].(agentapi.GitHubMCPUser); ok {
			u.SetGitHubMCP(on)
		}
	}
}

// reloadCustomCatalogs reloads the catalog of each available provider that
// offers custom models, so a change is selectable at once. A failed load
// keeps the old catalog and marks it stale for the next /api/meta.
func (m *Manager) reloadCustomCatalogs() {
	for _, name := range m.order {
		p := m.providers[name]
		m.mu.Lock()
		available := m.infos[name].Available || m.infos[name].SignedOut
		m.mu.Unlock()
		if _, ok := p.(agentapi.CustomModelUser); !ok || !available {
			continue
		}
		models, err := loadModels(m.ctx, p)
		m.mu.Lock()
		if err != nil {
			log.Warn("reload web provider models failed", "provider", name, "error", err)
			m.modelsAt[name] = time.Time{}
		} else {
			info := m.infos[name]
			info.Models = models
			m.infos[name] = info
			m.modelsAt[name] = m.now()
		}
		m.mu.Unlock()
	}
}

// withProviders returns a copy of current with each provider in change set
// to its value, or removed for an empty one; nil when none is left.
func withProviders[V string | []string](current, change map[string]V) map[string]V {
	out := maps.Clone(current)
	if out == nil {
		out = map[string]V{}
	}
	for provider, ids := range change {
		if len(ids) == 0 {
			delete(out, provider)
		} else {
			out[provider] = ids
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
