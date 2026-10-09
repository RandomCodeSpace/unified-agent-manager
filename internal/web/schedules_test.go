package web

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Native schedules reach the Task's detail and stream only, bounded and
// owned, and leave with the conversation.
func TestNativeSchedulesDetailOnlyBoundedAndReleasedOnClose(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.Subscribe(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	native := agentapi.ScheduleSnapshot{Supported: true, Known: true, Entries: []agentapi.ScheduleEntry{{ID: "7", Recurring: true, Cron: "*/5 * * * *\x1b[31m", Timezone: "Asia/Singapore", NextRunAt: next}}}
	conv.Emit(agentapi.Event{Kind: agentapi.EventSchedules, Schedules: &native})
	var streamed agentapi.ScheduleSnapshot
	decodeField(t, frameOf(t, sub, "schedules"), "schedules", &streamed)
	if !streamed.Known || len(streamed.Entries) != 1 || streamed.Entries[0].ID != "7" || strings.ContainsRune(streamed.Entries[0].Cron, '\x1b') || !streamed.Entries[0].NextRunAt.Equal(next) {
		t.Fatalf("streamed schedules = %+v", streamed)
	}
	d := detail(t, m, sum.ID)
	if d.Schedules == nil || !d.Schedules.Known || len(d.Schedules.Entries) != 1 || d.Schedules.Entries[0].Timezone != "Asia/Singapore" {
		t.Fatalf("detail schedules = %+v", d.Schedules)
	}
	summary, err := m.Summary(sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(summary); strings.Contains(string(raw), "schedule") {
		t.Fatalf("summary carries schedules: %s", raw)
	}
	// Provider and API callers cannot mutate the retained snapshot.
	native.Entries[0].Timezone = "changed by provider"
	d.Schedules.Entries[0].Timezone = "changed by reader"
	if got := detail(t, m, sum.ID).Schedules.Entries[0].Timezone; got != "Asia/Singapore" {
		t.Fatalf("schedules aliased: %s", got)
	}

	over := agentapi.ScheduleSnapshot{Supported: true, Known: true}
	for i := range agentapi.MaxSchedules + 8 {
		over.Entries = append(over.Entries, agentapi.ScheduleEntry{ID: strconv.Itoa(i), Recurring: true, IntervalMs: 60000})
	}
	conv.Emit(agentapi.Event{Kind: agentapi.EventSchedules, Schedules: &over})
	if got := detail(t, m, sum.ID).Schedules; got.Known || !got.Truncated || len(got.Entries) != agentapi.MaxSchedules {
		t.Fatalf("over the cap = known %v truncated %v rows %d", got.Known, got.Truncated, len(got.Entries))
	}
	frameOf(t, sub, "schedules")

	if _, err := m.Close(sum.ID); err != nil {
		t.Fatal(err)
	}
	if f := frameOf(t, sub, "schedules"); string(f.data["schedules"]) != "null" {
		t.Fatalf("close did not release the schedules: %s", f.data["schedules"])
	}
	if got := detail(t, m, sum.ID).Schedules; got != nil {
		t.Fatalf("closed Task kept schedules: %+v", got)
	}
	// The closed conversation's late event does not bring them back.
	conv.Emit(agentapi.Event{Kind: agentapi.EventSchedules, Schedules: &native})
	if got := detail(t, m, sum.ID).Schedules; got != nil {
		t.Fatalf("old conversation repopulated schedules: %+v", got)
	}
}

func TestNativeSchedulesReleasedOnExit(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventSchedules, Schedules: &agentapi.ScheduleSnapshot{Supported: true, Known: true, Entries: []agentapi.ScheduleEntry{{ID: "1", Recurring: true}}}})
	conv.Emit(agentapi.Event{Kind: agentapi.EventExit, Error: "disconnected"})
	if got := detail(t, m, sum.ID).Schedules; got != nil {
		t.Fatalf("exited conversation kept schedules: %+v", got)
	}
}

// Unknown stays unknown: no rows are claimed for an unsupported or failed read.
func TestNativeSchedulesUnknownIsNotEmptyKnown(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	conv.Emit(agentapi.Event{Kind: agentapi.EventSchedules, Schedules: &agentapi.ScheduleSnapshot{Supported: false, Reason: "unsupported"}})
	got := detail(t, m, sum.ID).Schedules
	if got == nil || got.Supported || got.Known || got.Entries == nil || len(got.Entries) != 0 || got.Reason != "unsupported" {
		t.Fatalf("unsupported schedules = %+v", got)
	}
}
