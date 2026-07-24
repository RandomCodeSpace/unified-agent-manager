package app

import (
	"fmt"
	"sort"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type NamedProfile struct {
	Name    string        `json:"name"`
	Default bool          `json:"default"`
	Profile store.Profile `json:"profile"`
}

type ProfileList struct {
	DefaultProfile string         `json:"default_profile"`
	Profiles       []NamedProfile `json:"profiles"`
}

type EffectiveProfileView struct {
	SessionID        string            `json:"session_id"`
	Provider         string            `json:"provider"`
	SelectedProfile  string            `json:"selected_profile"`
	EffectiveProfile string            `json:"effective_profile"`
	Mode             store.Mode        `json:"mode"`
	CommandAlias     string            `json:"command_alias"`
	Mouse            store.MousePolicy `json:"mouse"`
	ControlPrefix    string            `json:"control_prefix"`
	BackDetach       bool              `json:"back_detach"`
	ScrollbackLines  int               `json:"scrollback_lines"`
}

type ProfileReferencedError struct {
	Name       string
	Default    bool
	SessionIDs []string
}

func (e *ProfileReferencedError) Error() string {
	if len(e.SessionIDs) > 0 {
		return fmt.Sprintf("profile %q is referenced by sessions: %v", e.Name, e.SessionIDs)
	}
	return fmt.Sprintf("profile %q is the default profile", e.Name)
}

func (s *Service) ListProfiles() (ProfileList, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return ProfileList{}, err
	}
	names := make([]string, 0, len(cfg.Profiles))
	for name := range cfg.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	profiles := make([]NamedProfile, 0, len(names))
	for _, name := range names {
		profiles = append(profiles, NamedProfile{Name: name, Default: name == cfg.DefaultProfile, Profile: cfg.Profiles[name]})
	}
	return ProfileList{DefaultProfile: cfg.DefaultProfile, Profiles: profiles}, nil
}

func (s *Service) Profile(name string) (NamedProfile, error) {
	if err := store.ValidateProfileName(name); err != nil {
		return NamedProfile{}, err
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return NamedProfile{}, err
	}
	profile, exists := cfg.Profiles[name]
	if !exists {
		return NamedProfile{}, fmt.Errorf("profile %q not found", name)
	}
	return NamedProfile{Name: name, Default: cfg.DefaultProfile == name, Profile: profile}, nil
}
