package board

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// v7Comment is the start of the comment migration v7 adds to each epic it
// pauses.
const v7Comment = "uam: paused: uam now runs approved epics by itself"

// Migration v7 pauses every approved epic as the owner, so the deploy that
// brings the executor starts nothing on its own, and says so on each epic
// it paused. An epic paused already keeps its pause and gets no comment,
// and nothing else is paused. It is one-way.
func TestMigrateV6ToV7PausesApprovedEpics(t *testing.T) {
	all := migrations
	t.Cleanup(func() { migrations = all })
	migrations = all[:6]
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	approved, story, leaf, _ := f.tree()
	f.approveRun(approved.ID, testRun)
	paused, _, _, _ := f.tree2()
	f.approveRun(paused.ID, testRun)
	_, err := f.s.Edit(f.ctx, owner, paused.ID, Patch{Paused: ptr(true)})
	f.must(err)
	plain := f.create(owner, "", KindEpic, "Plain")
	f.must(f.s.Close())

	migrations = all
	s, err := Open(f.path, Options{})
	f.must(err)
	f.s = s
	var version string
	if err := s.db.QueryRow(`SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&version); err != nil || version != strconv.Itoa(len(migrations)) {
		t.Fatalf("schema_version = %q, %v", version, err)
	}
	for _, tc := range []struct {
		card    Card
		paused  string
		comment bool
	}{
		{approved, PausedOwner, true},
		{paused, PausedOwner, false},
		{plain, "", false},
		{story, "", false},
		{leaf, "", false},
	} {
		d := f.detail(tc.card.ID)
		var notes []Comment
		for _, c := range d.Comments {
			if strings.HasPrefix(c.Author+": "+c.Body, v7Comment) {
				notes = append(notes, c)
			}
		}
		if d.Card.Paused != tc.paused || len(notes) != boolInt(tc.comment) {
			t.Errorf("%s %q: paused %q, notes %+v; want paused %q, a note %v", d.Card.ref(), d.Card.Title, d.Card.Paused, notes, tc.paused, tc.comment)
			continue
		}
		if tc.comment && (!notes[0].Automatic || time.Since(notes[0].CreatedAt) > time.Minute || time.Since(notes[0].CreatedAt) < 0) {
			t.Errorf("%s note = %+v", d.Card.ref(), notes[0])
		}
	}
	// Resume clears the pause as it does any other.
	got, err := s.Edit(f.ctx, owner, approved.ID, Patch{Paused: ptr(false)})
	if err != nil || got.Card.Paused != "" {
		t.Fatalf("resume = %+v, %v", got, err)
	}
	f.must(s.Close())

	migrations = all[:6]
	if s, err := Open(f.path, Options{}); err == nil {
		_ = s.Close()
		t.Fatal("a v6 binary opened a v7 board")
	}
}

// RunFacts reads, in one pass, each approved epic with its ready subtasks
// in outline order and its lane slots in use, every open lane attempt with
// its holder's pending request and landing intent, and the attempt each
// lane Task ended. A root subtask, and work under an epic nobody approved,
// is never ready, and reading writes nothing.
func TestRunFactsReady(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic := f.create(owner, "", KindEpic, "Epic")
	a := f.create(owner, epic.ID, KindStory, "A")
	a1 := f.create(owner, a.ID, KindSubtask, "a1")
	a2 := f.create(owner, a.ID, KindSubtask, "a2")
	a3 := f.create(owner, a.ID, KindSubtask, "a3")
	a4 := f.create(owner, a.ID, KindSubtask, "a4")
	a5 := f.create(owner, a.ID, KindSubtask, "a5")
	b := f.create(owner, epic.ID, KindStory, "B")
	b1 := f.create(owner, b.ID, KindSubtask, "b1")
	b2 := f.create(owner, b.ID, KindSubtask, "b2")
	b3 := f.create(owner, b.ID, KindSubtask, "b3")
	b4 := f.create(owner, b.ID, KindSubtask, "b4")
	c := f.create(owner, epic.ID, KindStory, "C")
	f.create(owner, c.ID, KindSubtask, "c1")
	d := f.create(owner, epic.ID, KindStory, "D")
	f.create(owner, d.ID, KindSubtask, "d1")
	f.create(owner, "", KindSubtask, "Loose")
	unapproved := f.create(owner, "", KindEpic, "Unapproved")
	f.create(owner, f.create(owner, unapproved.ID, KindStory, "U").ID, KindSubtask, "u1")
	for _, l := range [][2]Card{{a1, a2}, {a, d}} {
		f.must(f.s.Link(f.ctx, owner, l[0].ID, l[1].ID))
	}
	f.approveRun(epic.ID, laneRun)
	for _, p := range []struct {
		card Card
		edit Patch
	}{{a4, Patch{Blocked: ptr(true)}}, {a5, Patch{Paused: ptr(true)}}, {c, Patch{Paused: ptr(true)}}} {
		_, err := f.s.Edit(f.ctx, owner, p.card.ID, p.edit)
		f.must(err)
	}
	f.must(f.s.StartPlanning(f.ctx, owner, epic.ID, "planner"))
	f.create(Agent("planner", ""), b.ID, KindSubtask, "b5")

	// a1 waits to land, b1 runs, b2 lands with its intent stored, b3 has a
	// blocked request and a3's attempt was aborted.
	f.lane(a1.ID, "run-a1")
	waiting := f.landDone(a1.ID, "run-a1")
	f.lane(b1.ID, "run-b1")
	f.lane(b2.ID, "run-b2")
	landing := f.landDone(b2.ID, "run-b2")
	f.must(f.s.MarkLanding(f.ctx, landing.Request.ID, landedSHA))
	f.lane(b3.ID, "run-b3")
	blocked, err := f.s.FileRequest(f.ctx, Agent("run-b3", ""), b3.ID, RequestInput{Kind: RequestBlocked, Comment: "needs a key"})
	f.must(err)
	f.lane(a3.ID, "run-x")
	_, err = f.s.AbortRun(f.ctx, a3.ID, "run-x", "the provider failed")
	f.must(err)

	// A second Project's approved epic counts toward the global slots.
	f.must(f.s.SetProjectAcceptCmd(f.ctx, owner, "p2", "go test ./..."))
	other, err := f.s.Create(f.ctx, owner, NewCard{ProjectID: "p2", Kind: KindEpic, Title: "Elsewhere"})
	f.must(err)
	w1, err := f.s.Create(f.ctx, owner, NewCard{Kind: KindSubtask, ParentID: other.ID, Title: "w1"})
	f.must(err)
	w2, err := f.s.Create(f.ctx, owner, NewCard{Kind: KindSubtask, ParentID: other.ID, Title: "w2"})
	f.must(err)
	f.approveRun(other.ID, testRun)
	f.lane(w1.ID, "run-w1")

	before := f.revision()
	facts, err := f.s.RunFacts(f.ctx)
	f.must(err)
	if f.revision() != before {
		t.Fatalf("RunFacts wrote: revision %d → %d", before, f.revision())
	}
	titles := func(cards []Card) []string {
		out := make([]string, len(cards))
		for i, c := range cards {
			out[i] = c.Title
		}
		return out
	}
	if len(facts.Epics) != 2 {
		t.Fatalf("epics = %+v", facts.Epics)
	}
	for i, want := range []struct {
		id    string
		inUse int
		ready []string
	}{
		{epic.ID, 1, []string{"a3", "b4"}},
		{other.ID, 1, []string{"w2"}},
	} {
		e := facts.Epics[i]
		if e.Epic.ID != want.id || e.Epic.Run == nil || e.InUse != want.inUse || !slices.Equal(titles(e.Ready), want.ready) {
			t.Errorf("epic %d = %s run %+v, %d in use, ready %v; want %s, %d, %v", i, e.Epic.Title, e.Epic.Run, e.InUse, titles(e.Ready), want.id, want.inUse, want.ready)
		}
	}
	if facts.Epics[0].Epic.Run.Parallel != laneRun.Parallel || facts.Epics[1].Epic.Run.Provider != testRun.Provider {
		t.Errorf("runs = %+v, %+v", facts.Epics[0].Epic.Run, facts.Epics[1].Epic.Run)
	}
	if facts.InUse != 2 {
		t.Errorf("in use = %d, want 2 (b1 and w1)", facts.InUse)
	}
	wantLanes := []LaneFacts{
		{CardID: a1.ID, Seq: a1.Seq, TaskID: "run-a1", Pending: RequestDone, Request: waiting.Request.ID, Landing: true},
		{CardID: b1.ID, Seq: b1.Seq, TaskID: "run-b1"},
		{CardID: b2.ID, Seq: b2.Seq, TaskID: "run-b2", Pending: RequestDone, Request: landing.Request.ID, Landing: true, Intent: landedSHA},
		{CardID: b3.ID, Seq: b3.Seq, TaskID: "run-b3", Pending: RequestBlocked, Request: blocked.ID},
		{CardID: w1.ID, Seq: w1.Seq, TaskID: "run-w1"},
	}
	if !reflect.DeepEqual(facts.Lanes, wantLanes) {
		t.Errorf("lanes = %+v\nwant %+v", facts.Lanes, wantLanes)
	}
	wantEnded := map[string]LaneEnd{"run-x": {CardID: a3.ID, Seq: a3.Seq, Reason: ReleaseAborted}}
	if !reflect.DeepEqual(facts.Ended, wantEnded) {
		t.Errorf("ended = %+v, want %+v", facts.Ended, wantEnded)
	}
	// StartRun counts the slots RunFacts does: with b1 and w1 running and
	// the cap at four, two more start, and a third is refused.
	f.lane(a3.ID, "run-a3")
	f.lane(b4.ID, "run-b4")
	wantRefusal(t, f.s.CanStart(f.ctx, w2.ID), CodeNotReady, refsOf(a3, b1, b4, w1)...)
	if facts, err := f.s.RunFacts(f.ctx); err != nil || facts.InUse != MaxLanes || facts.Epics[0].InUse != 3 || len(facts.Epics[0].Ready) != 0 {
		t.Fatalf("after two more starts: %+v, %v", facts, err)
	}
}

// nextAt is the clock the Next tests run at.
var nextAt = time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)

// leafCard is a ready subtask in a Next test.
func leafCard(id string, seq int64, prio int) Card {
	return Card{ID: id, Seq: seq, Kind: KindSubtask, Title: id, Prio: prio, Status: StatusTodo}
}

// epicRun is an approved epic in a Next test, on provider, running parallel
// at a time with inUse slots taken and ready waiting.
func epicRun(id, provider string, parallel, inUse int, ready ...Card) EpicFacts {
	return EpicFacts{
		Epic:  Card{ID: id, Kind: KindEpic, Title: id, Run: &Run{RunSettings: RunSettings{Provider: provider, Model: "m", Mode: ModeYolo, Parallel: parallel}}},
		InUse: inUse,
		Ready: ready,
	}
}

// picks lists the subtasks a step starts, as epic/card.
func picks(s Step) []string {
	var out []string
	for _, p := range s.Start {
		out = append(out, p.Epic.ID+"/"+p.Card.ID)
	}
	return out
}

// holder is an open lane attempt at card by task in a Next test.
func holder(card string, seq int64, task string) LaneFacts {
	return LaneFacts{CardID: card, Seq: seq, TaskID: task}
}

// act is what a Next test expects done to task over card.
func act(task, card string, seq int64, provider string, why Why) Act {
	return Act{Task: task, CardID: card, Seq: seq, Provider: provider, Why: why}
}

func idle(provider string) TaskFact {
	return TaskFact{Stage: StageActive, Turn: TurnEnded, Provider: provider}
}

func turn(provider string, tn Turn) TaskFact {
	return TaskFact{Stage: StageActive, Turn: tn, Provider: provider}
}

// TestNext is board.Next's table (ADR 0006 §4.2): what one executor pass
// starts, nudges, cancels, retires, aborts and lands, from the board's
// facts, the lane Tasks' facts and the executor's memory.
func TestNext(t *testing.T) {
	failedAt := nextAt.Add(-time.Minute)
	failed := func(provider string, worked bool) TaskFact {
		return TaskFact{Stage: StageActive, Turn: TurnFailed, Provider: provider, Worked: worked, FailedAt: failedAt}
	}
	for _, tc := range []struct {
		name  string
		facts RunFacts
		tasks map[string]TaskFact
		mem   Memory
		want  Step
	}{
		{
			name: "ready subtasks start up to the epic's free slots",
			facts: RunFacts{InUse: 1, Epics: []EpicFacts{
				epicRun("E", "copilot", 3, 1, leafCard("x1", 1, 3), leafCard("x2", 2, 3), leafCard("x3", 3, 3)),
			}},
			want: Step{Start: []Pick{
				{Epic: epicRun("E", "copilot", 3, 1).Epic, Card: leafCard("x1", 1, 3)},
				{Epic: epicRun("E", "copilot", 3, 1).Epic, Card: leafCard("x2", 2, 3)},
			}},
		},
		{
			name: "starts in progress take slots and are not started again",
			facts: RunFacts{InUse: 1, Epics: []EpicFacts{
				epicRun("E", "copilot", 3, 1, leafCard("x1", 1, 3), leafCard("x2", 2, 3), leafCard("x3", 3, 3)),
			}},
			mem:  Memory{Starting: map[string]string{"x1": "E"}},
			want: Step{Start: []Pick{{Epic: epicRun("E", "copilot", 3, 1).Epic, Card: leafCard("x2", 2, 3)}}},
		},
		{
			name: "a start that took its hold already counts once",
			facts: RunFacts{InUse: 2, Lanes: []LaneFacts{holder("x0", 9, "t0")}, Epics: []EpicFacts{
				epicRun("E", "copilot", 3, 2, leafCard("x1", 1, 3)),
			}},
			tasks: map[string]TaskFact{"t0": turn("copilot", TurnWorking)},
			mem:   Memory{Starting: map[string]string{"x0": "E"}},
			want:  Step{Start: []Pick{{Epic: epicRun("E", "copilot", 3, 2).Epic, Card: leafCard("x1", 1, 3)}}},
		},
		{
			name: "the global cap counts every epic's lanes and starts",
			facts: RunFacts{InUse: 2, Epics: []EpicFacts{
				epicRun("E", "copilot", 4, 1, leafCard("x1", 1, 3), leafCard("x2", 2, 3)),
				epicRun("F", "copilot", 4, 1, leafCard("y1", 5, 3), leafCard("y2", 6, 3)),
			}},
			mem:  Memory{Starting: map[string]string{"z9": "G"}},
			want: Step{Start: []Pick{{Epic: epicRun("E", "copilot", 4, 1).Epic, Card: leafCard("x1", 1, 3)}}},
		},
		{
			name: "highest priority first, then outline order",
			facts: RunFacts{Epics: []EpicFacts{
				epicRun("E", "copilot", 3, 0, leafCard("low", 1, 3), leafCard("high", 2, 1), leafCard("mid1", 3, 2), leafCard("mid2", 4, 2)),
			}},
			want: Step{Start: []Pick{
				{Epic: epicRun("E", "copilot", 3, 0).Epic, Card: leafCard("high", 2, 1)},
				{Epic: epicRun("E", "copilot", 3, 0).Epic, Card: leafCard("mid1", 3, 2)},
				{Epic: epicRun("E", "copilot", 3, 0).Epic, Card: leafCard("mid2", 4, 2)},
			}},
		},
		{
			name: "a paused epic and one backing off start nothing",
			facts: RunFacts{Epics: []EpicFacts{
				func() EpicFacts {
					e := epicRun("P", "copilot", 2, 0, leafCard("p1", 1, 3))
					e.Epic.Paused = PausedUAM
					return e
				}(),
				epicRun("B", "copilot", 2, 0, leafCard("b1", 2, 3)),
				epicRun("D", "copilot", 2, 0, leafCard("d1", 3, 3)),
			}},
			mem:  Memory{Now: nextAt, EpicBackoff: map[string]time.Time{"B": nextAt.Add(time.Second), "D": nextAt}},
			want: Step{Start: []Pick{{Epic: epicRun("D", "copilot", 2, 0).Epic, Card: leafCard("d1", 3, 3)}}},
		},
		{
			name: "a pending request frees a slot and leaves the holder to the owner",
			facts: RunFacts{Lanes: []LaneFacts{
				{CardID: "h1", Seq: 1, TaskID: "t1", Pending: RequestBlocked, Request: "r1"},
				{CardID: "h2", Seq: 2, TaskID: "t2", Pending: RequestDone, Request: "r2"},
				{CardID: "h3", Seq: 3, TaskID: "t3", Pending: RequestSplit, Request: "r3"},
			}, Epics: []EpicFacts{epicRun("E", "copilot", 1, 0, leafCard("x1", 4, 3))}},
			tasks: map[string]TaskFact{"t1": idle("copilot"), "t2": idle("copilot"), "t3": failed("copilot", true)},
			mem:   Memory{Now: nextAt, SeenFailure: map[string]time.Time{"t3": failedAt}},
			want:  Step{Start: []Pick{{Epic: epicRun("E", "copilot", 1, 0).Epic, Card: leafCard("x1", 4, 3)}}},
		},
		{
			name: "a landing intent lands unless a land call is in flight or its retry time has not passed",
			facts: RunFacts{Lanes: []LaneFacts{
				{CardID: "h1", Seq: 1, TaskID: "t1", Pending: RequestDone, Request: "r1", Landing: true, Intent: landedSHA},
				{CardID: "h2", Seq: 2, TaskID: "t2", Pending: RequestDone, Request: "r2", Landing: true, Intent: landedSHA},
				{CardID: "h3", Seq: 3, TaskID: "t3", Pending: RequestDone, Request: "r3", Intent: landedSHA},
			}},
			tasks: map[string]TaskFact{"t1": turn("copilot", TurnWorking), "t2": idle("copilot"), "t3": idle("copilot")},
			mem:   Memory{Now: nextAt, Landing: map[string]bool{"h2": true}, LandRetry: map[string]time.Time{"r1": nextAt, "r3": nextAt.Add(time.Second)}},
			want:  Step{Land: []string{"r1"}},
		},
		{
			name: "a done waiting to land lands once its retry time passed and its holder is not working",
			facts: RunFacts{Lanes: []LaneFacts{
				{CardID: "h1", Seq: 1, TaskID: "t1", Pending: RequestDone, Request: "r1", Landing: true},
				{CardID: "h2", Seq: 2, TaskID: "t2", Pending: RequestDone, Request: "r2", Landing: true},
				{CardID: "h3", Seq: 3, TaskID: "t3", Pending: RequestDone, Request: "r3", Landing: true},
				{CardID: "h4", Seq: 4, TaskID: "t4", Pending: RequestDone, Request: "r4", Landing: true},
				{CardID: "h5", Seq: 5, TaskID: "gone", Pending: RequestDone, Request: "r5", Landing: true},
				{CardID: "h6", Seq: 6, TaskID: "t6", Pending: RequestDone, Request: "r6", Landing: true},
			}},
			tasks: map[string]TaskFact{
				"t1": idle("copilot"), "t2": idle("copilot"), "t3": idle("copilot"),
				"t4": turn("copilot", TurnWorking), "t6": {Stage: StageSettled, Turn: TurnEnded, Provider: "copilot"},
			},
			mem: Memory{Now: nextAt, Landing: map[string]bool{"h2": true},
				LandRetry: map[string]time.Time{"r1": nextAt, "r3": nextAt.Add(time.Second)}},
			want: Step{Land: []string{"r1", "r5", "r6"}},
		},
		{
			// Only a restart interrupts a turn, so one still interrupted
			// after its nudge never took it: it is retired, as an ended one
			// is, instead of keeping its slot.
			name: "interrupted holders are nudged once, one per provider per pass, then retired",
			facts: RunFacts{InUse: 4, Lanes: []LaneFacts{
				holder("h1", 1, "t1"), holder("h2", 2, "t2"), holder("h3", 3, "t3"), holder("h4", 4, "t4"),
			}},
			tasks: map[string]TaskFact{
				"t1": turn("copilot", TurnInterrupted), "t2": turn("copilot", TurnInterrupted),
				"t3": turn("copilot", TurnInterrupted), "t4": turn("other", TurnInterrupted),
			},
			mem: Memory{Now: nextAt, Nudged: map[string]int{"t1": 1}},
			want: Step{
				Nudge: []Act{
					act("t2", "h2", 2, "copilot", WhyRestarted),
					act("t4", "h4", 4, "other", WhyRestarted),
				},
				Retire: []Act{act("t1", "h1", 1, "copilot", "")},
			},
		},
		{
			name: "a holder that ended without a done request is nudged, then retired",
			facts: RunFacts{InUse: 4, Lanes: []LaneFacts{
				holder("h1", 1, "t1"), holder("h2", 2, "t2"), holder("h3", 3, "t3"), holder("h4", 4, "t4"),
			}},
			tasks: map[string]TaskFact{
				"t1": idle("copilot"), "t2": idle("copilot"),
				"t3": turn("other", TurnUAMCancelled), "t4": turn("other", TurnUAMCancelled),
			},
			mem: Memory{Now: nextAt, Nudged: map[string]int{"t2": 1, "t4": 1}},
			want: Step{
				Nudge:  []Act{act("t1", "h1", 1, "copilot", WhyNoDone), act("t3", "h3", 3, "other", WhyNoDone)},
				Retire: []Act{act("t2", "h2", 2, "copilot", ""), act("t4", "h4", 4, "other", "")},
			},
		},
		{
			name:  "a failed turn with nothing in the lane aborts the attempt and reports the provider",
			facts: RunFacts{InUse: 2, Lanes: []LaneFacts{holder("h1", 1, "t1"), holder("h2", 2, "t2")}},
			tasks: map[string]TaskFact{"t1": failed("copilot", false), "t2": failed("copilot", false)},
			mem:   Memory{Now: nextAt, SeenFailure: map[string]time.Time{"t2": failedAt}, Nudged: map[string]int{"t2": 1}},
			want: Step{
				Abort:          []Act{act("t1", "h1", 1, "copilot", ""), act("t2", "h2", 2, "copilot", "")},
				ProviderFailed: []Act{act("t1", "h1", 1, "copilot", "")},
			},
		},
		{
			name:  "a failed turn after work is reported once and nudged nothing while the breaker is open",
			facts: RunFacts{InUse: 1, Lanes: []LaneFacts{holder("h1", 1, "t1")}},
			tasks: map[string]TaskFact{"t1": failed("copilot", true)},
			mem:   Memory{Now: nextAt},
			want:  Step{ProviderFailed: []Act{act("t1", "h1", 1, "copilot", "")}},
		},
		{
			name:  "a reported failure waits for the breaker",
			facts: RunFacts{InUse: 1, Lanes: []LaneFacts{holder("h1", 1, "t1")}},
			tasks: map[string]TaskFact{"t1": failed("copilot", true)},
			mem: Memory{Now: nextAt, SeenFailure: map[string]time.Time{"t1": failedAt},
				Providers: map[string]Breaker{"copilot": {Until: nextAt.Add(time.Minute), Failures: 1}}},
		},
		{
			name:  "a new failure of a holder nudged before is reported again",
			facts: RunFacts{InUse: 1, Lanes: []LaneFacts{holder("h1", 1, "t1")}},
			tasks: map[string]TaskFact{"t1": failed("copilot", true)},
			mem:   Memory{Now: nextAt, SeenFailure: map[string]time.Time{"t1": failedAt.Add(-time.Hour)}, Nudged: map[string]int{"t1": 1}},
			want:  Step{ProviderFailed: []Act{act("t1", "h1", 1, "copilot", "")}},
		},
		{
			name:  "once the breaker allows, one failed holder per provider gets a probe nudge",
			facts: RunFacts{InUse: 3, Lanes: []LaneFacts{holder("h1", 1, "t1"), holder("h2", 2, "t2"), holder("h3", 3, "t3")}},
			tasks: map[string]TaskFact{"t1": failed("copilot", true), "t2": failed("copilot", true), "t3": failed("copilot", true)},
			mem: Memory{Now: nextAt, SeenFailure: map[string]time.Time{"t1": failedAt, "t2": failedAt, "t3": failedAt},
				Nudged:    map[string]int{"t1": 3, "t2": 2},
				Providers: map[string]Breaker{"copilot": {Until: nextAt, Failures: 2}}},
			want: Step{
				Nudge:  []Act{act("t2", "h2", 2, "copilot", WhyProviderFailed)},
				Retire: []Act{act("t1", "h1", 1, "copilot", "")},
			},
		},
		{
			name: "an open breaker starts and nudges nothing on its provider",
			facts: RunFacts{InUse: 2, Lanes: []LaneFacts{holder("h1", 1, "t1"), holder("h2", 2, "t2")}, Epics: []EpicFacts{
				epicRun("E", "copilot", 2, 1, leafCard("x1", 3, 3)),
				epicRun("F", "other", 2, 1, leafCard("y1", 4, 3)),
			}},
			tasks: map[string]TaskFact{"t1": turn("copilot", TurnInterrupted), "t2": turn("other", TurnInterrupted)},
			mem:   Memory{Now: nextAt, Providers: map[string]Breaker{"copilot": {Until: nextAt.Add(time.Second), Failures: 1}}},
			want: Step{
				Nudge: []Act{act("t2", "h2", 2, "other", WhyRestarted)},
				Start: []Pick{{Epic: epicRun("F", "other", 2, 1).Epic, Card: leafCard("y1", 4, 3)}},
			},
		},
		{
			name:  "an owner-cancelled, working or waiting holder is left alone, and so is a deleted one",
			facts: RunFacts{InUse: 4, Lanes: []LaneFacts{holder("h1", 1, "t1"), holder("h2", 2, "t2"), holder("h3", 3, "t3"), holder("h4", 4, "gone")}},
			tasks: map[string]TaskFact{
				"t1": turn("copilot", TurnOwnerCancelled), "t2": turn("copilot", TurnWorking), "t3": turn("copilot", TurnWaiting),
			},
			mem: Memory{Now: nextAt, Nudged: map[string]int{"t1": 1}},
		},
		{
			name:  "a settled or archived holder is retired",
			facts: RunFacts{InUse: 2, Lanes: []LaneFacts{holder("h1", 1, "t1"), holder("h2", 2, "t2")}},
			tasks: map[string]TaskFact{
				"t1": {Stage: StageSettled, Turn: TurnEnded, Provider: "copilot"},
				"t2": {Stage: StageArchived, Turn: TurnInterrupted, Provider: "copilot"},
			},
			mem:  Memory{Now: nextAt},
			want: Step{Retire: []Act{act("t1", "h1", 1, "copilot", ""), act("t2", "h2", 2, "copilot", "")}},
		},
		{
			name: "a landed Task is left to end its turn, then retired",
			facts: RunFacts{Ended: map[string]LaneEnd{
				"t1": {CardID: "c1", Seq: 1, Reason: ReleaseAccepted},
				"t2": {CardID: "c2", Seq: 2, Reason: ReleaseAccepted},
				"t3": {CardID: "c3", Seq: 3, Reason: ReleaseAccepted},
			}},
			tasks: map[string]TaskFact{"t1": turn("copilot", TurnWorking), "t2": turn("copilot", TurnWaiting), "t3": idle("copilot")},
			mem:   Memory{Now: nextAt},
			want:  Step{Retire: []Act{act("t3", "c3", 3, "copilot", "")}},
		},
		{
			name: "a stopped Task still working is cancelled with a reason, and retired once idle",
			facts: RunFacts{Ended: map[string]LaneEnd{
				"t1": {CardID: "c1", Seq: 1, Reason: ReleaseOwner},
				"t2": {CardID: "c2", Seq: 2, Reason: ReleaseBlocked},
				"t3": {CardID: "c3", Seq: 3, Reason: ReleaseSettled},
				"t4": {CardID: "c4", Seq: 4, Reason: ReleaseRejected},
				"t5": {CardID: "c5", Seq: 5, Reason: ReleaseCancelled},
			}},
			tasks: map[string]TaskFact{
				"t1": turn("copilot", TurnWorking), "t2": turn("copilot", TurnWaiting),
				"t3": turn("copilot", TurnUAMCancelled), "t4": turn("copilot", TurnFailed), "t5": idle("copilot"),
			},
			mem: Memory{Now: nextAt, Providers: map[string]Breaker{"copilot": {Until: nextAt.Add(time.Hour), Failures: 1}}},
			want: Step{
				Cancel: []Act{act("t1", "c1", 1, "copilot", WhyStopped), act("t2", "c2", 2, "copilot", WhyStopped)},
				Retire: []Act{act("t3", "c3", 3, "copilot", ""), act("t4", "c4", 4, "copilot", ""), act("t5", "c5", 5, "copilot", "")},
			},
		},
		{
			name: "a settled Task holding nothing is retired, an archived one and one never started are left",
			facts: RunFacts{Ended: map[string]LaneEnd{
				"t1": {CardID: "c1", Seq: 1, Reason: ReleaseAccepted},
				"t2": {CardID: "c2", Seq: 2, Reason: ReleaseEnded},
			}},
			tasks: map[string]TaskFact{
				"t1": {Stage: StageSettled, Turn: TurnEnded, Provider: "copilot"},
				"t2": {Stage: StageArchived, Turn: TurnEnded, Provider: "copilot"},
				"t3": turn("copilot", TurnWorking),
			},
			mem:  Memory{Now: nextAt},
			want: Step{Retire: []Act{act("t1", "c1", 1, "copilot", "")}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.mem.Now.IsZero() {
				tc.mem.Now = nextAt
			}
			got := Next(tc.facts, tc.tasks, tc.mem)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Next =\n%s\nwant\n%s", describeStep(got), describeStep(tc.want))
			}
		})
	}
}

// Next reads its inputs only: the same facts and memory give the same
// step, and the maps it is given are left as they were.
func TestNextIsPure(t *testing.T) {
	facts := RunFacts{InUse: 1, Lanes: []LaneFacts{holder("h1", 1, "t1")}, Epics: []EpicFacts{
		epicRun("E", "copilot", 2, 1, leafCard("x1", 2, 3), leafCard("x2", 3, 1)),
	}}
	tasks := map[string]TaskFact{"t1": turn("copilot", TurnInterrupted)}
	mem := Memory{Now: nextAt, Nudged: map[string]int{}, Starting: map[string]string{}, Landing: map[string]bool{}}
	first := Next(facts, tasks, mem)
	if len(first.Nudge) != 1 || len(first.Start) != 1 || first.Start[0].Card.ID != "x2" {
		t.Fatalf("step = %s", describeStep(first))
	}
	if again := Next(facts, tasks, mem); !reflect.DeepEqual(first, again) {
		t.Fatalf("a second call gave %s", describeStep(again))
	}
	if len(mem.Nudged) != 0 || len(mem.Starting) != 0 || len(mem.Landing) != 0 || facts.Epics[0].Ready[0].ID != "x1" {
		t.Fatalf("Next changed its inputs: %+v, %+v", mem, facts.Epics[0].Ready)
	}
}

// describeStep renders a step for a failure message.
func describeStep(s Step) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  start %v\n  land %v\n", picks(s), s.Land)
	for _, l := range []struct {
		name string
		acts []Act
	}{{"nudge", s.Nudge}, {"cancel", s.Cancel}, {"retire", s.Retire}, {"abort", s.Abort}, {"provider failed", s.ProviderFailed}} {
		fmt.Fprintf(&b, "  %s %+v\n", l.name, l.acts)
	}
	return b.String()
}

// When an approved epic's lane starts keep failing on git or the store, uam
// pauses the epic itself and says why (ADR 0006 §4.5); the owner resumes
// it. An epic paused already, by uam or the owner, keeps its pause and gets
// no second comment, and only an approved epic is paused this way.
func TestPauseRunPausesTheEpicOnce(t *testing.T) {
	f := newFixture(t)
	f.acceptCmd("go test ./...")
	epic, story, _, _ := f.tree()
	f.approveRun(epic.ID, testRun)
	const why = "lanes could not start 3 times: git worktree add failed"
	for range 2 {
		got, err := f.s.PauseRun(f.ctx, epic.ID, why)
		if err != nil || got.Paused != PausedUAM {
			t.Fatalf("pause run = %+v, %v", got, err)
		}
	}
	notes := 0
	for _, c := range f.comments(epic.ID) {
		if c == "uam: paused: "+why {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("comments = %q, want one pause note", f.comments(epic.ID))
	}

	owned, _, _, _ := f.tree2()
	f.approveRun(owned.ID, testRun)
	_, err := f.s.Edit(f.ctx, owner, owned.ID, Patch{Paused: ptr(true)})
	f.must(err)
	if got, err := f.s.PauseRun(f.ctx, owned.ID, why); err != nil || got.Paused != PausedOwner || hasComment(f.comments(owned.ID), why) {
		t.Fatalf("pause run of an epic the owner paused = %+v, %v, comments %q", got, err, f.comments(owned.ID))
	}
	_, err = f.s.PauseRun(f.ctx, story.ID, why)
	wantCode(t, err, CodeInvalid)
	plain := f.create(owner, "", KindEpic, "Plain")
	_, err = f.s.PauseRun(f.ctx, plain.ID, why)
	wantCode(t, err, CodeInvalid)
}
