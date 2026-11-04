package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// A Project's skill list is checked and stored; a new Thread pins its own
// list, else its Project's, else none (every skill), opens with it, keeps it
// when the Project changes and across a restart.
func TestThreadSkillList(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	pid := addProject(t, ts.m, t.TempDir())
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/projects/"+pid, body, withCookie(ts))
		if w.Code != want {
			t.Fatalf("PATCH %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return w.Body.String()
	}
	for _, body := range []string{`{"skills":"review"}`, `{"skills":[""]}`, `{"skills":[" review"]}`, `{"skills":["a\nb"]}`} {
		patch(body, http.StatusBadRequest)
	}
	if got := patch(`{"skills":["tdd","review","tdd"]}`, http.StatusOK); !strings.Contains(got, `"skills":["review","tdd"]`) {
		t.Fatalf("set = %s", got)
	}
	create := func(skills *[]string) SessionSummary {
		t.Helper()
		sum, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: pid, Name: "task", Skills: skills})
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	opened := func() *[]string {
		t.Helper()
		opens := ts.prov.Opens()
		return opens[len(opens)-1].Skills
	}
	equal := func(got *[]string, want ...string) bool { return got != nil && slices.Equal(*got, want) }

	project := create(nil)
	if !equal(project.Skills, "review", "tdd") || !equal(opened(), "review", "tdd") {
		t.Fatalf("project list: pinned %v, opened with %v", project.Skills, opened())
	}
	if own := create(&[]string{}); !equal(own.Skills) || !equal(opened()) {
		t.Fatalf("own empty list: pinned %v, opened with %v", own.Skills, opened())
	}
	if _, err := ts.m.Create(CreateRequest{Provider: ts.prov.Name(), ProjectID: pid, Name: "task", Skills: &[]string{"bad\tname"}}); err == nil {
		t.Fatal("created with an invalid skill name")
	}
	if got := patch(`{"skills":null}`, http.StatusOK); strings.Contains(got, `"skills"`) {
		t.Fatalf("cleared = %s", got)
	}
	if every := create(nil); every.Skills != nil || opened() != nil {
		t.Fatalf("no list: pinned %v, opened with %v", every.Skills, opened())
	}

	// The Thread keeps its pinned list after the Project changed and across
	// a restart; the Project keeps its own.
	patch(`{"skills":["grilling"]}`, http.StatusOK)
	if err := ts.m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, ts.m.store, ts.prov)
	sum, err := again.Summary(project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !equal(sum.Skills, "review", "tdd") {
		t.Fatalf("after restart pinned %v", sum.Skills)
	}
	projects := again.Projects()
	if i := slices.IndexFunc(projects, func(p Project) bool { return p.ID == pid }); i < 0 || !equal(projects[i].Skills, "grilling") {
		t.Fatalf("projects after restart = %+v", projects)
	}
}

// The skills a list can name are what discovery finds for the Project, by
// name, without uam's own skills.
func TestSkillOptions(t *testing.T) {
	m := configurationManager(t)
	p := &configurationProvider{Provider: agenttest.NewProvider("fake", agentapi.Capabilities{}), skills: agentapi.ConfigurationCatalog{Definitions: []agentapi.ConfigurationDefinition{
		{Name: "tdd", Description: "Test first", Path: "/home/u/.agents/skills/tdd/SKILL.md"}, {Name: "uam", Path: "/uam/skills/uam/SKILL.md"},
		{Name: "review", Source: "plugin"}, {Name: "tdd", Path: "/project/.agents/skills/tdd/SKILL.md"},
	}}}
	attachConfigurationProvider(m, p)
	m.skillDirs = []string{"/uam/skills"}
	got, err := m.SkillOptions("project")
	if err != nil {
		t.Fatal(err)
	}
	if want := []SkillOption{{Name: "review"}, {Name: "tdd", Description: "Test first"}}; !slices.Equal(got, want) {
		t.Fatalf("options = %+v", got)
	}
	if _, err := m.SkillOptions("missing"); err == nil {
		t.Fatal("listed skills for a missing Project")
	}
	p.skillsErr = errors.New("down")
	if _, err := m.SkillOptions("project"); err == nil {
		t.Fatal("listed skills while discovery failed")
	}
}
