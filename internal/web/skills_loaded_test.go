package web

import (
	"reflect"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestSkillsLoadedCountsEachLoadOnceAcrossLiveAndRecord(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Unsubscribe(sub) })
	at := func(s int) time.Time { return time.Unix(1_800_000_000+int64(s), 0).UTC() }

	conv.Emit(agentapi.Event{Kind: agentapi.EventSkill, Skill: &agentapi.SkillLoad{ID: "e1", Name: "tdd", Description: "Build test-first", At: at(10)}})
	var frame []LoadedSkill
	decodeField(t, frameOf(t, sub, "skills_loaded"), "skills_loaded", &frame)
	if want := []LoadedSkill{{Name: "tdd", Description: "Build test-first", FirstAt: at(10), Count: 1}}; !reflect.DeepEqual(frame, want) {
		t.Fatalf("frame = %+v, want %+v", frame, want)
	}
	// A repeated event is the same load; a later one of the same skill counts.
	conv.Emit(agentapi.Event{Kind: agentapi.EventSkill, Skill: &agentapi.SkillLoad{ID: "e1", Name: "tdd", At: at(11)}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventSkill, Skill: &agentapi.SkillLoad{ID: "e2", Name: "\x1b[1munslop\x1b[0m", At: at(20)}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventSkill, Skill: &agentapi.SkillLoad{ID: "e3", Name: "tdd", At: at(30)}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventSkill, Skill: &agentapi.SkillLoad{ID: "e4", Name: " \x00 ", At: at(31)}})

	// The record holds the loads streamed and an earlier subagent one.
	m.mu.Lock()
	m.applyHistoryLocked(m.sessions[sum.ID], agentapi.History{Skills: []agentapi.SkillLoad{
		{ID: "e0", AgentID: "child", Name: "review", At: at(5)},
		{ID: "e1", Name: "tdd", Description: "Build test-first", At: at(10)},
		{ID: "e2", Name: "unslop", At: at(20)},
	}}, false)
	m.mu.Unlock()

	want := []LoadedSkill{
		{Name: "review", FirstAt: at(5), Count: 1},
		{Name: "tdd", Description: "Build test-first", FirstAt: at(10), Count: 2},
		{Name: "unslop", FirstAt: at(20), Count: 1},
	}
	if got := detail(t, m, sum.ID).SkillsLoaded; !reflect.DeepEqual(got, want) {
		t.Fatalf("skills_loaded = %+v, want %+v", got, want)
	}
}
