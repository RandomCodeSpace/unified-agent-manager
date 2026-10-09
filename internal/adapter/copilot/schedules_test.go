package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// scheduleFake adds the native schedule list to a fake session. A read
// returns the entries set when it started; hook runs in between.
type scheduleFake struct {
	*fakeSession
	mu      sync.Mutex
	entries []rpc.ScheduleEntry
	err     error
	reads   int
	hook    func(context.Context)
}

func (s *scheduleFake) ListSchedules(ctx context.Context) ([]rpc.ScheduleEntry, error) {
	s.mu.Lock()
	s.reads++
	entries, err, hook := slices.Clone(s.entries), s.err, s.hook
	s.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return entries, err
}

func (s *scheduleFake) set(entries ...rpc.ScheduleEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = entries
}

func (s *scheduleFake) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

func int64Ptr(v int64) *int64 { return &v }

func scheduleEntry(id int64, next time.Time) rpc.ScheduleEntry {
	return rpc.ScheduleEntry{ID: id, Recurring: true, IntervalMs: int64Ptr(300000), NextRunAt: next, Prompt: "secret scheduled prompt", DisplayPrompt: copilot.String("/secret-skill")}
}

func scheduleSnapshots(sink *recSink) []agentapi.ScheduleSnapshot {
	var out []agentapi.ScheduleSnapshot
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventSchedules {
			out = append(out, *e.Schedules)
		}
	}
	return out
}

// openScheduled opens a conversation whose session lists entries.
func openScheduled(t *testing.T, resume bool, entries ...rpc.ScheduleEntry) (*scheduleFake, *recSink, agentapi.Conversation) {
	t.Helper()
	var sf *scheduleFake
	fc := &fakeClient{}
	fc.wrap = func(s *fakeSession) sdkSession {
		sf = &scheduleFake{fakeSession: s, entries: entries}
		return sf
	}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	sink := &recSink{}
	req := agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Events: sink}
	if resume {
		req.ConversationID = "s-1"
	}
	conv, err := p.Open(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return sf, sink, conv
}

func TestWebSchedulesInitialReadOnCreateAndResume(t *testing.T) {
	next := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, resume := range []bool{false, true} {
		t.Run(fmt.Sprintf("resume=%v", resume), func(t *testing.T) {
			sf, sink, _ := openScheduled(t, resume, scheduleEntry(7, next))
			waitFor(t, "the schedule list", func() bool { return len(scheduleSnapshots(sink)) == 1 })
			got := scheduleSnapshots(sink)[0]
			want := agentapi.ScheduleEntry{ID: "7", Recurring: true, IntervalMs: 300000, NextRunAt: next}
			if !got.Supported || !got.Known || got.Truncated || len(got.Entries) != 1 || got.Entries[0] != want {
				t.Fatalf("snapshot = %+v", got)
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "secret") {
				t.Fatalf("snapshot kept the scheduled prompt: %s", raw)
			}
			time.Sleep(20 * time.Millisecond)
			if n := sf.readCount(); n != 1 {
				t.Fatalf("reads = %d, want one on open and none without events", n)
			}
		})
	}
}

func TestWebSchedulesEmptyListIsKnown(t *testing.T) {
	_, sink, _ := openScheduled(t, false)
	waitFor(t, "the schedule list", func() bool { return len(scheduleSnapshots(sink)) == 1 })
	if got := scheduleSnapshots(sink)[0]; !got.Supported || !got.Known || got.Entries == nil || len(got.Entries) != 0 {
		t.Fatalf("empty snapshot = %+v", got)
	}
}

// A read started before a native event never publishes: the event's own
// read does, once.
func TestWebSchedulesEventDuringReadRejectsStaleResult(t *testing.T) {
	next := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sf, sink, _ := openScheduled(t, false)
	waitFor(t, "the first list", func() bool { return len(scheduleSnapshots(sink)) == 1 })

	entered, release := make(chan struct{}, 4), make(chan struct{})
	sf.mu.Lock()
	sf.hook = func(context.Context) {
		entered <- struct{}{}
		<-release
	}
	sf.mu.Unlock()
	sf.set(scheduleEntry(1, next))
	sf.onEvent(ev("c1", &rpc.SessionScheduleCreatedData{ID: 1, Prompt: "secret scheduled prompt"}))
	<-entered
	sf.set(scheduleEntry(2, next.Add(time.Hour)))
	sf.onEvent(ev("x1", &rpc.SessionScheduleCancelledData{ID: 1}))
	sf.onEvent(ev("r1", &rpc.SessionScheduleRearmedData{ID: 2, NextRunAt: next.Add(time.Hour).UnixMilli()}))
	close(release)
	waitFor(t, "the refreshed list", func() bool { return len(scheduleSnapshots(sink)) == 2 })
	time.Sleep(20 * time.Millisecond)
	got := scheduleSnapshots(sink)
	if len(got) != 2 || len(got[1].Entries) != 1 || got[1].Entries[0].ID != "2" {
		t.Fatalf("snapshots = %+v, want the stale read dropped", got)
	}
	if n := sf.readCount(); n != 3 {
		t.Fatalf("reads = %d, want open, the stale one and one more", n)
	}
}

// Turn boundaries refresh only a nonempty list, of the main agent.
func TestWebSchedulesTurnBoundariesRefreshNonEmptyList(t *testing.T) {
	sf, sink, _ := openScheduled(t, false)
	waitFor(t, "the first list", func() bool { return len(scheduleSnapshots(sink)) == 1 })
	sf.onEvent(ev("t1", &rpc.AssistantTurnStartData{TurnID: "1"}))
	sf.onEvent(ev("i1", &rpc.SessionIdleData{}))
	time.Sleep(20 * time.Millisecond)
	if n := sf.readCount(); n != 1 {
		t.Fatalf("an empty list was read again at a turn boundary: %d reads", n)
	}

	next := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	sf.set(scheduleEntry(1, next))
	sf.onEvent(ev("c1", &rpc.SessionScheduleCreatedData{ID: 1}))
	waitFor(t, "the created entry", func() bool { return len(scheduleSnapshots(sink)) == 2 })
	sf.onEvent(agentEv("t2", "child", &rpc.AssistantTurnStartData{TurnID: "2"}))
	time.Sleep(20 * time.Millisecond)
	if n := sf.readCount(); n != 2 {
		t.Fatalf("a subagent turn read the list: %d reads", n)
	}
	sf.set() // a one-shot that fired
	sf.onEvent(ev("i2", &rpc.SessionIdleData{}))
	waitFor(t, "the consumed entry", func() bool { return len(scheduleSnapshots(sink)) == 3 })
	if got := scheduleSnapshots(sink)[2]; !got.Known || len(got.Entries) != 0 {
		t.Fatalf("after idle = %+v", got)
	}
}

func TestWebSchedulesCloseCancelsReadAndReleases(t *testing.T) {
	fired := make(chan error, 1)
	sf, sink, conv := openScheduled(t, false)
	waitFor(t, "the first list", func() bool { return len(scheduleSnapshots(sink)) == 1 })
	sf.mu.Lock()
	sf.hook = func(ctx context.Context) {
		<-ctx.Done()
		fired <- ctx.Err()
	}
	sf.mu.Unlock()
	sf.onEvent(ev("c1", &rpc.SessionScheduleCreatedData{ID: 1}))
	waitFor(t, "the read", func() bool { return sf.readCount() == 2 })
	if err := conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fired:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read ended with %v, want cancelled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close did not cancel the read")
	}
	time.Sleep(20 * time.Millisecond)
	if got := scheduleSnapshots(sink); len(got) != 1 {
		t.Fatalf("a read published after close: %+v", got)
	}
	c := conv.(*conversation)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.schedules.snapshot != nil {
		t.Fatal("close kept the schedule rows")
	}
}

func TestScheduleSnapshotUnsupportedUnknownAndPartial(t *testing.T) {
	missing := &copilot.RPCError{Code: -32601, Message: "Method not found"}
	for _, tc := range []struct {
		name      string
		err       error
		supported bool
	}{
		{"method absent", missing, false},
		{"wrapped method absent", fmt.Errorf("list: %w", missing), false},
		{"other RPC error", &copilot.RPCError{Code: -32000, Message: "boom"}, true},
		{"transport", context.DeadlineExceeded, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scheduleSnapshot(nil, tc.err)
			if got.Supported != tc.supported || got.Known || len(got.Entries) != 0 || got.Entries == nil || got.Reason == "" || strings.Contains(got.Reason, "boom") {
				t.Fatalf("snapshot = %+v", got)
			}
		})
	}

	next := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	var many []rpc.ScheduleEntry
	for i := range maxSchedules + 1 {
		many = append(many, scheduleEntry(int64(i), next))
	}
	got := scheduleSnapshot(many, nil)
	if !got.Supported || got.Known || !got.Truncated || len(got.Entries) != maxSchedules || got.Entries[maxSchedules-1].ID != "31" {
		t.Fatalf("over the cap = known %v truncated %v rows %d", got.Known, got.Truncated, len(got.Entries))
	}

	bad := scheduleEntry(2, next)
	bad.IntervalMs = int64Ptr(-1)
	long := scheduleEntry(3, next)
	long.Cron = copilot.String(strings.Repeat("*", 300))
	cron := rpc.ScheduleEntry{ID: 4, Recurring: true, Cron: copilot.String("*/5 * * * *\x1b[31m"), Tz: copilot.String("Asia/Singapore"), NextRunAt: next, SelfPaced: copilot.Bool(false)}
	got = scheduleSnapshot([]rpc.ScheduleEntry{scheduleEntry(1, next), scheduleEntry(1, next), bad, long, cron}, nil)
	if got.Known || !got.Truncated || len(got.Entries) != 2 || got.Entries[0].ID != "1" || got.Entries[1].ID != "4" {
		t.Fatalf("invalid rows = %+v", got)
	}
	if c := got.Entries[1]; strings.ContainsRune(c.Cron, '\x1b') || c.Timezone != "Asia/Singapore" || c.IntervalMs != 0 {
		t.Fatalf("cron row = %+v", c)
	}
}

func TestSDKScheduleListReadsSessionEntries(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	rt.set("session.schedule.list", `{"entries":[{"id":3,"recurring":true,"intervalMs":60000,"nextRunAt":"2026-10-09T10:00:00Z","prompt":"p"}]}`)
	entries, err := s.ListSchedules(context.Background())
	if err != nil || len(entries) != 1 || entries[0].ID != 3 || entries[0].IntervalMs == nil || *entries[0].IntervalMs != 60000 {
		t.Fatalf("ListSchedules = %+v, %v", entries, err)
	}
	if req := rt.last("session.schedule.list"); req["sessionId"] != "s-1" {
		t.Fatalf("schedule list request = %v", req)
	}
	rt.fail("session.schedule.list", "down")
	if _, err := s.ListSchedules(context.Background()); err == nil {
		t.Fatal("ListSchedules hid a failure")
	}
}
