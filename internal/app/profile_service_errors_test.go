package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestProfileServiceRejectsUnsafeMutations(t *testing.T) {
	t.Run("store unavailable", func(t *testing.T) {
		// Given
		svc := NewService(nil, adapter.NewRegistry(nil))

		// When
		errorsSeen := []error{
			svc.UpdateProfile("focused", func(*store.Profile) error { return nil }),
			svc.RemoveProfile("focused"),
			svc.SetDefaultProfile("focused"),
			svc.AssignProfileExact("session-1", "focused"),
			svc.UpdateProfileOverridesExact("session-1", "", func(*store.SessionProfileOverrides) error { return nil }),
		}
		_, effectiveErr := svc.EffectiveProfileExact(context.Background(), "session-1")
		errorsSeen = append(errorsSeen, effectiveErr)

		// Then
		for _, err := range errorsSeen {
			if err == nil || !strings.Contains(err.Error(), "store unavailable") {
				t.Fatalf("nil-store error = %v", err)
			}
		}
	})

	t.Run("invalid profile updates remain atomic", func(t *testing.T) {
		// Given
		cfg := store.DefaultConfig()
		svc, _ := newProfileLaunchService(t, cfg)
		mutateErr := errors.New("mutation rejected")

		// When
		invalidNameErr := svc.UpdateProfile("../focused", func(*store.Profile) error { return nil })
		callbackErr := svc.UpdateProfile("focused", func(*store.Profile) error { return mutateErr })
		invalidValueErr := svc.UpdateProfile("focused", func(profile *store.Profile) error {
			profile.Mode = pointer(store.Mode("turbo"))
			return nil
		})

		// Then
		if invalidNameErr == nil || !errors.Is(callbackErr, mutateErr) || invalidValueErr == nil {
			t.Fatalf("update errors name=%v callback=%v value=%v", invalidNameErr, callbackErr, invalidValueErr)
		}
		loaded, err := svc.Store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded.Profiles) != 0 {
			t.Fatalf("rejected updates persisted profiles: %+v", loaded.Profiles)
		}
	})

	t.Run("references and missing profiles block mutation", func(t *testing.T) {
		// Given
		cfg := store.DefaultConfig()
		cfg.DefaultProfile = "focused"
		cfg.Profiles["focused"] = store.Profile{}
		cfg.Sessions[store.Key("fake", "session-1")] = store.SessionRecord{
			ID: "session-1", Agent: "fake", Profile: "focused",
		}
		svc, _ := newProfileLaunchService(t, cfg)

		// When
		referencedErr := svc.RemoveProfile("focused")
		missingRemoveErr := svc.RemoveProfile("missing")
		missingDefaultErr := svc.SetDefaultProfile("missing")
		clearDefaultErr := svc.SetDefaultProfile("none")

		// Then
		var referenced *ProfileReferencedError
		if !errors.As(referencedErr, &referenced) || !referenced.Default ||
			len(referenced.SessionIDs) != 1 || referenced.SessionIDs[0] != "session-1" {
			t.Fatalf("referenced error = %#v", referencedErr)
		}
		if missingRemoveErr == nil || missingDefaultErr == nil || clearDefaultErr != nil {
			t.Fatalf("profile errors remove=%v default=%v clear=%v", missingRemoveErr, missingDefaultErr, clearDefaultErr)
		}
	})

	t.Run("assignment and overrides enforce provider identity", func(t *testing.T) {
		// Given
		cfg := store.DefaultConfig()
		cfg.Profiles["wrong-provider"] = store.Profile{Provider: pointer("claude")}
		cfg.Sessions[store.Key("fake", "session-1")] = store.SessionRecord{
			ID: "session-1", Agent: "fake",
		}
		svc, _ := newProfileLaunchService(t, cfg)
		mutateErr := errors.New("override rejected")

		// When
		missingSessionErr := svc.AssignProfileExact("missing", "none")
		missingProfileErr := svc.AssignProfileExact("session-1", "missing")
		providerProfileErr := svc.AssignProfileExact("session-1", "wrong-provider")
		providerOverrideErr := svc.UpdateProfileOverridesExact("session-1", "claude", func(*store.SessionProfileOverrides) error {
			return nil
		})
		callbackErr := svc.UpdateProfileOverridesExact("session-1", "", func(*store.SessionProfileOverrides) error {
			return mutateErr
		})
		invalidOverrideErr := svc.UpdateProfileOverridesExact("session-1", "", func(overrides *store.SessionProfileOverrides) error {
			overrides.Mouse = pointer(store.MousePolicy("sometimes"))
			return nil
		})

		// Then
		if missingSessionErr == nil || missingProfileErr == nil || providerProfileErr == nil ||
			providerOverrideErr == nil || !errors.Is(callbackErr, mutateErr) || invalidOverrideErr == nil {
			t.Fatalf(
				"assignment/override errors missing-session=%v missing-profile=%v profile-provider=%v override-provider=%v callback=%v invalid=%v",
				missingSessionErr, missingProfileErr, providerProfileErr, providerOverrideErr, callbackErr, invalidOverrideErr,
			)
		}
	})

	t.Run("effective profile requires an available provider", func(t *testing.T) {
		// Given
		cfg := store.DefaultConfig()
		cfg.Sessions[store.Key("missing", "session-1")] = store.SessionRecord{
			ID: "session-1", Agent: "missing",
		}
		svc, _ := newProfileLaunchService(t, cfg)

		// When
		_, missingSessionErr := svc.EffectiveProfileExact(context.Background(), "unknown")
		_, unavailableProviderErr := svc.EffectiveProfileExact(context.Background(), "session-1")

		// Then
		if missingSessionErr == nil || unavailableProviderErr == nil ||
			!strings.Contains(unavailableProviderErr.Error(), "unavailable") {
			t.Fatalf("effective errors missing=%v unavailable=%v", missingSessionErr, unavailableProviderErr)
		}
	})
}
