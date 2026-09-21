package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/charmbracelet/x/ansi"
)

func TestLaunchPadProfileSelection(t *testing.T) {
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
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.defaultAgent = "fake"
	m.openLaunchPad()
	m.focusLaunchField(launchFieldProvider)

	// When
	model, _ := m.handleLaunchKey(keyMsg("shift+tab"))
	m = model.(Model)

	// Then
	if view := ansi.Strip(m.View().Content); !strings.Contains(view, "profile") || !strings.Contains(view, "focused") {
		t.Fatalf("profile selection missing from the launch pad:\n%s", view)
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
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 100, Height: 24})
	m.sessions = []adapter.Session{{ID: "exact-session-id", AgentType: "fake", DisplayName: "work", Cwd: "/tmp", CreatedAt: time.Now()}}

	// When
	out := ansi.Strip(m.View().Content)

	// Then
	if !strings.Contains(out, "profile default:focused") {
		t.Fatalf("ledger profile missing:\n%s", out)
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
	m.defaultAgent = "codex"
	m.openLaunchPad()
	m.focusLaunchField(launchFieldProvider)

	// When
	model, _ := m.handleLaunchKey(keyMsg("shift+tab"))
	m = model.(Model)

	// Then
	if m.launchProfile != "claudeprof" || m.launchAgent != "claude" {
		t.Fatalf("launch profile=%q provider=%q", m.launchProfile, m.launchAgent)
	}
}

func TestWizardExplicitProviderSurvivesProfileSelection(t *testing.T) {
	// Given
	m := Model{
		defaultAgent:        "codex",
		launchOpen:          true,
		launchField:         launchFieldProvider,
		launchAgent:         "codex",
		launchAgentExplicit: true,
		profileNames:        []string{"claudeprof"},
		profileProviders:    map[string]string{"claudeprof": "claude"},
	}

	// When
	model, _ := m.handleLaunchKey(keyMsg("shift+tab"))
	m = model.(Model)

	// Then
	if m.launchProfile != "claudeprof" || m.launchAgent != "codex" {
		t.Fatalf("explicit provider lost: profile=%q provider=%q", m.launchProfile, m.launchAgent)
	}
}
