package web

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// ConfigurationDiscovery reports completeness per native kind. Safe managed
// files remain available when the optional runtime method is absent or fails.
type ConfigurationDiscovery struct {
	Supported bool     `json:"supported"`
	Ready     bool     `json:"ready"`
	Warnings  []string `json:"warnings,omitempty"`
}

// configurationDiscoverer returns the first available provider with native
// discovery, the skill directories Tasks use, and scope's project paths.
func (m *Manager) configurationDiscoverer(scope configurationScope) (agentapi.ConfigurationDiscoverer, []string, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var projectPaths []string
	if !scope.global {
		projectPaths = []string{scope.base}
	}
	for _, name := range m.order {
		if m.infos[name].Available {
			if c, ok := m.providers[name].(agentapi.ConfigurationDiscoverer); ok {
				return c, slices.Clone(m.skillDirs), projectPaths
			}
		}
	}
	return nil, nil, projectPaths
}

func (m *Manager) discoverConfiguration(out *Configuration, scope configurationScope) {
	provider, skillDirs, projectPaths := m.configurationDiscoverer(scope)
	if provider == nil {
		return
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	out.Discovery = map[string]ConfigurationDiscovery{}
	for _, kind := range []string{"skills", "agents", "hooks", "instructions"} {
		var catalog agentapi.ConfigurationCatalog
		var err error
		files := &out.Skills
		switch kind {
		case "skills":
			catalog, err = provider.DiscoverSkills(ctx, projectPaths, skillDirs)
		case "agents":
			catalog, err = provider.DiscoverAgents(ctx, projectPaths)
			files = &out.Agents
		case "hooks":
			catalog, err = provider.DiscoverHooks(ctx, projectPaths)
			files = &out.Hooks
		case "instructions":
			catalog, err = provider.DiscoverInstructions(ctx, projectPaths)
			files = &out.InstructionFiles
		}
		status := ConfigurationDiscovery{Supported: !errors.Is(err, agentapi.ErrUnsupported), Ready: err == nil}
		if err != nil {
			if status.Supported {
				status.Warnings = []string{"Native " + kind + " discovery failed; showing managed files."}
			}
		} else {
			for _, warning := range catalog.Warnings[:min(len(catalog.Warnings), 19)] {
				status.Warnings = append(status.Warnings, shortError(errors.New(warning)))
			}
			if len(catalog.Warnings) > 19 {
				status.Warnings = append(status.Warnings, "Additional native diagnostics were omitted.")
			}
			if len(catalog.Definitions) > maxConfigurationFiles {
				status.Warnings = append(status.Warnings, "Native discovery exceeded the 128-definition limit.")
			}
			mergeConfigurationDefinitions(files, catalog.Definitions[:min(len(catalog.Definitions), maxConfigurationFiles)])
			status.Ready = len(status.Warnings) == 0
		}
		out.Discovery[kind] = status
	}
}

func mergeConfigurationDefinitions(files *[]ConfigurationFile, definitions []agentapi.ConfigurationDefinition) {
	seen := map[string]bool{}
	for _, definition := range definitions {
		// Resolve aliases only to match existing safe reads. No native path is
		// opened for content or allowed to expand the editor's mutation roots.
		path := definition.Path
		if filepath.IsAbs(path) {
			if actual, err := filepath.EvalSymlinks(path); err == nil {
				path = actual
			}
		}
		key := definition.ID + "\x00" + definition.Source + "\x00" + path
		if seen[key] {
			continue
		}
		seen[key] = true
		matched := false
		for i := range *files {
			file := &(*files)[i]
			// Hook and instruction rows keep unresolved project paths.
			if file.MetadataOnly || file.Disabled || path == "" || (file.Path != path && file.Path != definition.Path) || file.Native != nil {
				continue
			}
			file.Native = &definition
			if nativeConfigurationManagedSource(definition.Source) {
				file.Editable = false
				file.ReadOnlyReason = "This definition is managed by Copilot at its source."
			}
			matched = true
			break
		}
		if !matched {
			*files = append(*files, ConfigurationFile{Name: definition.Name, Path: definition.Path, Native: &definition, MetadataOnly: true, ReadOnlyReason: "Native discovery metadata. Manage this definition at its source."})
		}
	}
}

func nativeConfigurationManagedSource(source string) bool {
	return source == "plugin" || source == "builtin" || source == "remote" || source == "sdk" || source == "policy"
}

// SetSkillGloballyDisabled changes Copilot's global disabled-skills setting for
// one name that native discovery lists in scope. It affects every skill with
// that name in every project and never renames or edits a file, so project and
// global file toggles keep their own meaning. Like those toggles it requires
// Terminal on.
func (m *Manager) SetSkillGloballyDisabled(projectID, name string, disabled bool) error {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	scope, err := m.configurationScope(projectID)
	if err != nil {
		return err
	}
	if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || displaytext.Sanitize(name) != name {
		return newError(http.StatusBadRequest, "use the name of a skill Copilot lists")
	}
	m.mu.Lock()
	terminal := m.settings.Terminal
	m.mu.Unlock()
	if !terminal {
		return newError(http.StatusForbidden, "changing skills requires Settings → Shell access → Terminal on because they can run commands or grant tool permissions")
	}
	provider, skillDirs, projectPaths := m.configurationDiscoverer(scope)
	setter, ok := provider.(agentapi.SkillGlobalSetter)
	if !ok {
		return newError(http.StatusConflict, "this Copilot version cannot change the global skill setting")
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	catalog, err := provider.DiscoverSkills(ctx, projectPaths, skillDirs)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return newError(http.StatusConflict, "this Copilot version cannot list skills; the global setting was not changed")
	}
	if err != nil {
		return newError(http.StatusBadGateway, "Copilot skill discovery failed; the global setting was not changed")
	}
	if !slices.ContainsFunc(catalog.Definitions, func(d agentapi.ConfigurationDefinition) bool { return d.Name == name }) {
		return newError(http.StatusNotFound, "Copilot does not list a skill named %q here", name)
	}
	err = setter.SetSkillGloballyDisabled(ctx, name, disabled)
	if errors.Is(err, agentapi.ErrUnsupported) {
		return newError(http.StatusConflict, "this Copilot version cannot change the global skill setting")
	}
	if err != nil {
		return newError(http.StatusBadGateway, "Copilot could not change the global skill setting")
	}
	return nil
}

func (s *Server) handleSkillGlobalSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Disabled *bool  `json:"disabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Name == "" || body.Disabled == nil {
		writeError(w, http.StatusBadRequest, "name and disabled are required")
		return
	}
	if err := s.m.SetSkillGloballyDisabled(r.URL.Query().Get("project_id"), body.Name, *body.Disabled); err != nil {
		writeFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
