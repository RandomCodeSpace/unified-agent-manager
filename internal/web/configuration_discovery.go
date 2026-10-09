package web

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// ConfigurationDiscovery reports completeness per native kind. Safe managed
// files remain available when the optional runtime method is absent or fails.
type ConfigurationDiscovery struct {
	Supported bool     `json:"supported"`
	Ready     bool     `json:"ready"`
	Warnings  []string `json:"warnings,omitempty"`
}

func (m *Manager) discoverConfiguration(out *Configuration, scope configurationScope) {
	m.mu.Lock()
	var provider agentapi.ConfigurationDiscoverer
	for _, name := range m.order {
		if m.infos[name].Available {
			if c, ok := m.providers[name].(agentapi.ConfigurationDiscoverer); ok {
				provider = c
				break
			}
		}
	}
	skillDirs := slices.Clone(m.skillDirs)
	m.mu.Unlock()
	if provider == nil {
		return
	}
	var projectPaths []string
	if !scope.global {
		projectPaths = []string{scope.base}
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
