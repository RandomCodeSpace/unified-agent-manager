package web

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand/v2"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func seeded(seed uint64) func(int) int { return rand.New(rand.NewPCG(seed, seed)).IntN }

// addBadged records a Project named name with the badge newBadge picks for
// it, as AddProject does.
func addBadged(projects map[string]store.WebProject, name string, pick func(int) int) store.WebBadge {
	b := newBadge(name, projects, pick)
	id := fmt.Sprint(len(projects))
	projects[id] = store.WebProject{ID: id, Name: name, Badge: b}
	return b
}

func TestBadgeTextComesFromTheName(t *testing.T) {
	for seed := range uint64(50) {
		projects, pick := map[string]store.WebProject{}, seeded(seed)
		config := addBadged(projects, "config", pick)
		configuration := addBadged(projects, "configuration", pick)
		digit := addBadged(projects, "-9 lives", pick)
		switch {
		case config.Text != "CG":
			t.Fatalf("seed %d: config = %+v", seed, config)
		case configuration.Text != "CN":
			t.Fatalf("seed %d: configuration = %+v next to %+v", seed, configuration, config)
		case digit.Text != "9S":
			t.Fatalf("seed %d: -9 lives = %+v", seed, digit)
		}
		if got := addBadged(projects, "cog", pick); got.Text != "CO" {
			t.Fatalf("seed %d: cog next to CG = %+v, want CO", seed, got)
		}
		for _, b := range []store.WebBadge{config, configuration, digit} {
			if !validBadge(b) {
				t.Fatalf("seed %d: invalid badge %+v", seed, b)
			}
		}
	}
}

func TestExhaustedBadgesDoNotNeedReassignment(t *testing.T) {
	projects := map[string]store.WebProject{}
	for _, first := range badgeChars {
		for _, last := range badgeChars {
			text := string(first) + string(last)
			projects[text] = store.WebProject{Badge: store.WebBadge{Text: text, Color: "red"}}
		}
	}
	projects["overflow"] = store.WebProject{Badge: newBadge("config", projects, seeded(1))}
	if ids := badgeless(projects); len(ids) != 0 {
		t.Fatalf("exhausted badge space needs reassignment: %v", ids)
	}
}

func TestReadOnlyStoreBadgeFallbackIsStableAcrossRestarts(t *testing.T) {
	st := openTestStore(t)
	if err := st.Update(func(cfg *store.Config) error {
		cfg.SchemaVersion = store.CurrentSchemaVersion + 1
		cfg.WebProjects = map[string]store.WebProject{
			"first":  {ID: "first", Name: "config", Dir: t.TempDir()},
			"second": {ID: "second", Name: "config", Dir: t.TempDir()},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	m := startManager(t, st)
	first := m.Projects()
	if len(first) != 2 || !validBadge(store.WebBadge(first[0].Badge)) || first[0].Badge.Text == first[1].Badge.Text {
		t.Fatalf("fallback badges = %+v", first)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	second := startManager(t, st).Projects()
	for i := range first {
		if first[i].ID != second[i].ID || first[i].Badge != second[i].Badge {
			t.Fatalf("restart changed fallback badges: %+v -> %+v", first, second)
		}
	}
	after, err := os.ReadFile(st.Path())
	if err != nil || string(before) != string(after) {
		t.Fatalf("read-only store changed: %v", err)
	}
}

func TestBadgeForANameWithoutASCIILettersOrDigits(t *testing.T) {
	projects, pick := map[string]store.WebProject{}, seeded(1)
	seen := map[string]bool{}
	// Dotless i and long s upper-case to I and S; they must not count.
	for _, name := range []string{"日本語", "", "ıſ", "---", "P"} {
		b := addBadged(projects, name, pick)
		if !validBadge(b) || b.Text[0] != 'P' || b.Text[1] < 'A' || b.Text[1] > 'Z' || seen[b.Text] {
			t.Fatalf("%q = %+v", name, b)
		}
		seen[b.Text] = true
	}
}

func TestBadgeFallbacksWhenPairsRunOut(t *testing.T) {
	projects, pick := map[string]store.WebProject{}, seeded(2)
	seen := map[string]bool{}
	for i := range 40 {
		b := addBadged(projects, "aa", pick)
		switch {
		case !validBadge(b) || seen[b.Text]:
			t.Fatalf("badge %d = %+v, invalid or taken", i, b)
		case i == 0 && b.Text != "AA":
			t.Fatalf("first badge = %s, want AA", b.Text)
		case i < 26 && (b.Text[0] != 'A' || b.Text[1] < 'A' || b.Text[1] > 'Z'):
			t.Fatalf("badge %d = %s, want A and a free letter", i, b.Text)
		}
		seen[b.Text] = true
	}
	// With one free pair left, that is the one.
	full := map[string]store.WebProject{}
	for i := range len(badgeChars) {
		for j := range len(badgeChars) {
			if text := badgeChars[i:i+1] + badgeChars[j:j+1]; text != "7Q" {
				full[text] = store.WebProject{Badge: store.WebBadge{Text: text, Color: "red"}}
			}
		}
	}
	if b := newBadge("config", full, pick); b.Text != "7Q" {
		t.Fatalf("last free pair = %+v, want 7Q", b)
	}
}

func TestBadgesStayUniqueAcrossManyProjects(t *testing.T) {
	projects, pick := map[string]store.WebProject{}, seeded(3)
	names := []string{"config", "configuration", "api", "app", "a", "", "日本", "uam web", "x1"}
	seen := map[string]bool{}
	for i := range 600 {
		b := addBadged(projects, names[i%len(names)], pick)
		if !validBadge(b) || seen[b.Text] {
			t.Fatalf("badge %d for %q = %+v, invalid or taken", i, names[i%len(names)], b)
		}
		seen[b.Text] = true
	}
}

func TestBadgeColourPrefersUnusedTones(t *testing.T) {
	for seed := range uint64(20) {
		projects, pick := map[string]store.WebProject{}, seeded(seed)
		var tones []string
		for range badgeColors {
			tones = append(tones, addBadged(projects, "x", pick).Color)
		}
		if !slices.Equal(slices.Sorted(slices.Values(tones)), slices.Sorted(slices.Values(badgeColors))) {
			t.Fatalf("seed %d: first %d tones = %v, want each once", seed, len(badgeColors), tones)
		}
		if b := addBadged(projects, "x", pick); !slices.Contains(badgeColors, b.Color) {
			t.Fatalf("seed %d: tone once all are used = %q", seed, b.Color)
		}
	}
}

func TestProjectBadgeAssignedAndKeptOnRenameAndRestart(t *testing.T) {
	st := openTestStore(t)
	m := startManager(t, st)
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.AddProject(t.TempDir(), "config", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !validBadge(store.WebBadge(p.Badge)) || p.Badge.Text[0] != 'C' {
		t.Fatalf("added project badge = %+v", p.Badge)
	}
	f := frameOf(t, sub, "project")
	var framed map[string]json.RawMessage
	decodeField(t, f, "project", &framed)
	if want, _ := json.Marshal(p.Badge); string(framed["badge"]) != string(want) {
		t.Fatalf("project frame badge = %s, want %s", framed["badge"], want)
	}
	renamed, err := m.UpdateProject(p.ID, setting("zz top"), nil)
	if err != nil || renamed.Badge != p.Badge {
		t.Fatalf("renamed badge = %+v, %v; want %+v", renamed.Badge, err, p.Badge)
	}
	cfg, err := st.Load()
	if err != nil || cfg.WebProjects[p.ID].Badge != store.WebBadge(p.Badge) {
		t.Fatalf("stored badge = %+v, %v", cfg.WebProjects[p.ID].Badge, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := startManager(t, st).Projects(); len(got) != 1 || got[0].Badge != p.Badge || got[0].Name != "zz top" {
		t.Fatalf("projects after restart = %+v", got)
	}
}

func TestStartReplacesMissingAndInvalidBadgesOnce(t *testing.T) {
	st := openTestStore(t)
	now := time.Now().UTC()
	legacyDir := t.TempDir()
	seedLegacyWebRecord(t, st, mustUUID(t), legacyDir, &store.WebState{Turn: StateIdle, UpdatedAt: now})
	project := func(id string, age int, badge store.WebBadge) store.WebProject {
		return store.WebProject{ID: id, Name: "config", Dir: "/tmp/" + id, CreatedAt: now.Add(time.Duration(age) * time.Minute), Badge: badge}
	}
	kept := store.WebBadge{Text: "CG", Color: "teal"}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.WebProjects = map[string]store.WebProject{
			"kept":      project("kept", 0, kept),
			"duplicate": project("duplicate", 1, store.WebBadge{Text: "CG", Color: "blue"}),
			"none":      project("none", 2, store.WebBadge{}),
			"lower":     project("lower", 3, store.WebBadge{Text: "co", Color: "red"}),
			"long":      project("long", 4, store.WebBadge{Text: "CON", Color: "red"}),
			"markup":    project("markup", 5, store.WebBadge{Text: "<b", Color: "red"}),
			"tone":      project("tone", 6, store.WebBadge{Text: "CO", Color: "magenta"}),
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	m := startManager(t, st)
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	projects := m.Projects()
	if len(projects) != 8 || len(cfg.WebProjects) != 8 {
		t.Fatalf("projects = %+v, stored %+v", projects, cfg.WebProjects)
	}
	seen := map[string]bool{}
	for _, p := range projects {
		b := store.WebBadge(p.Badge)
		if !validBadge(b) || seen[b.Text] || cfg.WebProjects[p.ID].Badge != b {
			t.Fatalf("project %s badge = %+v, stored %+v", p.ID, b, cfg.WebProjects[p.ID].Badge)
		}
		seen[b.Text] = true
		switch {
		case p.ID == "kept" && b != kept:
			t.Fatalf("a valid badge was replaced: %+v", b)
		case p.Dir != legacyDir && b.Text[0] != 'C':
			t.Fatalf("project %s badge = %+v, want C first", p.ID, b)
		}
	}

	// Nothing is left to assign, so a second start does not write the store.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	again := startManager(t, st).Projects()
	after, err := os.Stat(st.Path())
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("second start rewrote the store")
	}
	badges := func(ps []Project) map[string]Badge {
		out := map[string]Badge{}
		for _, p := range ps {
			out[p.ID] = p.Badge
		}
		return out
	}
	if got, want := badges(again), badges(projects); !maps.Equal(got, want) {
		t.Fatalf("badges after restart = %+v, want %+v", got, want)
	}
}
