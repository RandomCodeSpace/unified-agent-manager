package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestWizardProfileSelection(t *testing.T) {
	// Given
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.DefaultConfig()
	cfg.Profiles["focused"] = store.Profile{Mode: pointer(store.ModeSafe)}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(persistentStore, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
	m = m.handleSessionsLoaded(m.loadSessionsCmd()().(sessionsLoadedMsg))
	m.wizard = true
	m.defaultAgent = "fake"

	// When
	model, _ := m.handleWizardKey(keyMsg("shift+tab"))
	m = model.(Model)
	model, _ = m.handleWizardKey(keyMsg("enter"))
	m = model.(Model)

	// Then
	if view := m.renderWizard(); !strings.Contains(view, "profile") || !strings.Contains(view, "focused") {
		t.Fatalf("profile selection step missing:\n%s", view)
	}
}

func TestSessionDetailsShowEffectiveProfile(t *testing.T) {
	// Given
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "fake"
	cfg.DefaultProfile = "focused"
	cfg.Profiles["focused"] = store.Profile{Mode: pointer(store.ModeSafe)}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m := NewWithDeps(persistentStore, adapter.NewRegistry([]adapter.AgentAdapter{&svcFakeAdapter{name: "fake", available: true}}))
	m = m.handleSessionsLoaded(m.loadSessionsCmd()().(sessionsLoadedMsg))
	m.width = 100
	m.sessions = []adapter.Session{{ID: "exact-session-id", AgentType: "fake", DisplayName: "work", Cwd: "/tmp", CreatedAt: time.Now()}}

	// When
	out := m.renderDetails()

	// Then
	if !strings.Contains(out, "selected: default") || !strings.Contains(out, "effective: focused") {
		t.Fatalf("profile details missing:\n%s", out)
	}
}

func TestAttachSpecCarriesResolvedProfileSnapshot(t *testing.T) {
	// Given
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	id := "profile-attach-id"
	live := adapter.Session{ID: id, AgentType: "fake", SessionName: "uam-fake-abcdef12", ProcAlive: adapter.Alive}
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "fake"
	cfg.DefaultProfile = "focused"
	cfg.Profiles["focused"] = store.Profile{Mode: pointer(store.ModeSafe)}
	cfg.Sessions[store.Key("fake", id)] = RecordFromSession(live, store.ModeYolo)
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{live}}
	svc := NewService(persistentStore, adapter.NewRegistry([]adapter.AgentAdapter{fake}))

	// When
	spec, err := svc.AttachSpecWithOptions(context.Background(), id, ResumeOptions{})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if spec.Profile.Selected != "default" || spec.Profile.Effective != "focused" || spec.Profile.Mouse != "auto" || spec.Profile.ControlPrefix != "C-b" || !spec.Profile.BackDetach {
		t.Fatalf("attach profile = %+v, want selected default, effective focused, and resolved terminal defaults", spec.Profile)
	}
}

func TestWizardProfileProviderBecomesDefault(t *testing.T) {
	// Given: global and profile providers deliberately differ.
	persistentStore, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := store.DefaultConfig()
	cfg.DefaultAgent = "codex"
	cfg.Profiles["claudeprof"] = store.Profile{Provider: pointer("claude")}
	if err := persistentStore.Save(cfg); err != nil {
		t.Fatal(err)
	}
	registry := adapter.NewRegistry([]adapter.AgentAdapter{
		&svcFakeAdapter{name: "codex", available: true},
		&svcFakeAdapter{name: "claude", available: true},
	})
	m := NewWithDeps(persistentStore, registry)
	m = m.handleSessionsLoaded(m.loadSessionsCmd()().(sessionsLoadedMsg))
	m.wizard = true
	m.defaultAgent = "codex"
	m.wizardAgent = "codex"

	// When
	model, _ := m.handleWizardKey(keyMsg("shift+tab"))
	m = model.(Model)

	// Then
	if m.wizardProfile != "claudeprof" || m.wizardAgent != "claude" {
		t.Fatalf("wizard profile=%q provider=%q", m.wizardProfile, m.wizardAgent)
	}
}

func TestWizardExplicitProviderSurvivesProfileSelection(t *testing.T) {
	// Given
	m := Model{
		defaultAgent:        "codex",
		wizard:              true,
		wizardAgent:         "codex",
		wizardAgentExplicit: true,
		profileNames:        []string{"claudeprof"},
		profileProviders:    map[string]string{"claudeprof": "claude"},
	}

	// When
	model, _ := m.handleWizardKey(keyMsg("shift+tab"))
	m = model.(Model)

	// Then
	if m.wizardProfile != "claudeprof" || m.wizardAgent != "codex" {
		t.Fatalf("wizard explicit provider lost: profile=%q provider=%q", m.wizardProfile, m.wizardAgent)
	}
}
