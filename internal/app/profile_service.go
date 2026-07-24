package app

import (
	"context"
	"fmt"
	"sort"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func (s *Service) UpdateProfile(name string, mutate func(*store.Profile) error) error {
	if s.Store == nil {
		return fmt.Errorf("profile store unavailable")
	}
	if err := store.ValidateProfileName(name); err != nil {
		return err
	}
	return s.Store.Update(func(cfg *store.Config) error {
		profile := cfg.Profiles[name]
		if err := mutate(&profile); err != nil {
			return err
		}
		if err := store.ValidateProfile(profile); err != nil {
			return err
		}
		cfg.Profiles[name] = profile
		return nil
	})
}

func (s *Service) RemoveProfile(name string) error {
	if s.Store == nil {
		return fmt.Errorf("profile store unavailable")
	}
	if err := store.ValidateProfileName(name); err != nil {
		return err
	}
	return s.Store.Update(func(cfg *store.Config) error {
		if _, exists := cfg.Profiles[name]; !exists {
			return fmt.Errorf("profile %q not found", name)
		}
		referenced := &ProfileReferencedError{Name: name, Default: cfg.DefaultProfile == name}
		for _, record := range cfg.Sessions {
			if record.Profile == name {
				referenced.SessionIDs = append(referenced.SessionIDs, record.ID)
			}
		}
		sort.Strings(referenced.SessionIDs)
		if referenced.Default || len(referenced.SessionIDs) > 0 {
			return referenced
		}
		delete(cfg.Profiles, name)
		return nil
	})
}

func (s *Service) SetDefaultProfile(name string) error {
	if s.Store == nil {
		return fmt.Errorf("profile store unavailable")
	}
	return s.Store.Update(func(cfg *store.Config) error {
		if name == "" || name == "none" {
			cfg.DefaultProfile = ""
			return nil
		}
		if err := store.ValidateProfileName(name); err != nil {
			return err
		}
		if _, exists := cfg.Profiles[name]; !exists {
			return fmt.Errorf("profile %q not found", name)
		}
		cfg.DefaultProfile = name
		return nil
	})
}

func exactSessionRecord(cfg *store.Config, id string) (string, store.SessionRecord, error) {
	var foundKey string
	var found store.SessionRecord
	for key, record := range cfg.Sessions {
		if record.ID != id && record.SessionName != id {
			continue
		}
		if foundKey != "" {
			return "", store.SessionRecord{}, fmt.Errorf("session %q is ambiguous", id)
		}
		foundKey, found = key, record
	}
	if foundKey == "" {
		return "", store.SessionRecord{}, fmt.Errorf("session %q not found", id)
	}
	return foundKey, found, nil
}

func (s *Service) AssignProfileExact(id, name string) error {
	if s.Store == nil {
		return fmt.Errorf("profile store unavailable")
	}
	return s.Store.Update(func(cfg *store.Config) error {
		key, record, err := exactSessionRecord(cfg, id)
		if err != nil {
			return err
		}
		if name == "" || name == "none" {
			record.Profile = ""
			cfg.Sessions[key] = record
			return nil
		}
		if err := store.ValidateProfileName(name); err != nil {
			return err
		}
		profile, exists := cfg.Profiles[name]
		if !exists {
			return fmt.Errorf("profile %q not found", name)
		}
		if profile.Provider != nil && *profile.Provider != record.Agent {
			return fmt.Errorf("profile %q provider %q conflicts with session provider %q", name, *profile.Provider, record.Agent)
		}
		record.Profile = name
		cfg.Sessions[key] = record
		return nil
	})
}

func (s *Service) UpdateProfileOverridesExact(id, provider string, mutate func(*store.SessionProfileOverrides) error) error {
	if s.Store == nil {
		return fmt.Errorf("profile store unavailable")
	}
	return s.Store.Update(func(cfg *store.Config) error {
		key, record, err := exactSessionRecord(cfg, id)
		if err != nil {
			return err
		}
		if provider != "" && provider != record.Agent {
			return fmt.Errorf("provider %q conflicts with session provider %q", provider, record.Agent)
		}
		overrides := store.SessionProfileOverrides{}
		if record.ProfileOverrides != nil {
			overrides = *record.ProfileOverrides
		}
		if err := mutate(&overrides); err != nil {
			return err
		}
		if err := store.ValidateSessionProfileOverrides(overrides); err != nil {
			return err
		}
		if emptyProfileOverrides(overrides) {
			record.ProfileOverrides = nil
		} else {
			record.ProfileOverrides = &overrides
		}
		cfg.Sessions[key] = record
		return nil
	})
}

func (s *Service) EffectiveProfileExact(ctx context.Context, id string) (EffectiveProfileView, error) {
	if s.Store == nil {
		return EffectiveProfileView{}, fmt.Errorf("profile store unavailable")
	}
	cfg, err := s.Store.Load()
	if err != nil {
		return EffectiveProfileView{}, err
	}
	_, record, err := exactSessionRecord(&cfg, id)
	if err != nil {
		return EffectiveProfileView{}, err
	}
	provider, ok := s.Registry.Get(record.Agent)
	if !ok {
		return EffectiveProfileView{}, fmt.Errorf("agent %q unavailable", record.Agent)
	}
	launch, err := resolveLaunchPolicy(cfg, provider, record)
	if err != nil {
		return EffectiveProfileView{}, err
	}
	effective, err := ResolveProfilePolicy(ResolutionInput{Config: cfg, Session: record, ProviderPolicy: launch.ProviderPolicy()})
	if err != nil {
		return EffectiveProfileView{}, err
	}
	attachment, err := effective.NewAttachment(ClientTemporaryOverride{}, ClientCapabilities{})
	if err != nil {
		return EffectiveProfileView{}, err
	}
	selected := record.Profile
	if selected == "" {
		selected = "default"
	}
	profile := effective.SelectedProfile()
	if profile == "" {
		profile = "none"
	}
	_ = ctx
	return EffectiveProfileView{
		SessionID: record.ID, Provider: launch.Provider(), SelectedProfile: selected, EffectiveProfile: profile,
		Mode: launch.Mode(), CommandAlias: launch.CommandAlias(), Mouse: attachment.Mouse(),
		ControlPrefix: attachment.ControlPrefix(), BackDetach: attachment.BackDetach(), ScrollbackLines: launch.ScrollbackLines(),
	}, nil
}

func emptyProfileOverrides(overrides store.SessionProfileOverrides) bool {
	return overrides.Mode == nil && overrides.CommandAlias == nil && overrides.Mouse == nil &&
		overrides.ControlPrefix == nil && overrides.BackDetach == nil && overrides.ScrollbackLines == nil
}
