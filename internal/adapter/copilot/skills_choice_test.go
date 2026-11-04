package copilot

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A Thread's skill list disables every other skill discovery finds for its
// folder, on create and on resume, but never uam's own skills; no list, or
// failed discovery, disables none.
func TestSkillListDisablesTheRest(t *testing.T) {
	uamSkill, other := "/uam/skills/uam/SKILL.md", "/home/u/.agents/skills/drop/SKILL.md"
	client := &configurationFakeClient{fakeClient: &fakeClient{}, skills: &rpc.ServerSkillList{Skills: []rpc.ServerSkill{
		{Name: "keep"}, {Name: "drop", Path: &other}, {Name: "drop"}, {Name: "uam", Path: &uamSkill}, {Name: "plugin:tool"},
	}}}
	p := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	open := func(convID string, skills *[]string) {
		t.Helper()
		workdir := t.TempDir()
		if _, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-" + convID, ConversationID: convID, Workdir: workdir, SkillDirectories: []string{"/uam/skills"}, Skills: skills, Events: &recSink{}}); err != nil {
			t.Fatal(err)
		}
		if skills != nil && (!slices.Equal(client.projects, []string{workdir}) || !slices.Equal(client.directories, []string{"/uam/skills"})) {
			t.Fatalf("discovered for %v %v", client.projects, client.directories)
		}
	}
	open("", &[]string{"keep"})
	if got := client.create[0].DisabledSkills; !slices.Equal(got, []string{"drop", "plugin:tool"}) {
		t.Fatalf("create disabled %v", got)
	}
	open("conv", &[]string{})
	if got := client.resume[0].DisabledSkills; !slices.Equal(got, []string{"keep", "drop", "plugin:tool"}) {
		t.Fatalf("resume with an empty list disabled %v", got)
	}
	open("", nil)
	if got := client.create[1].DisabledSkills; got != nil {
		t.Fatalf("no list disabled %v", got)
	}
	client.err = errors.New("discovery down")
	open("", &[]string{"keep"})
	if got := client.create[2].DisabledSkills; got != nil {
		t.Fatalf("failed discovery disabled %v", got)
	}
}
