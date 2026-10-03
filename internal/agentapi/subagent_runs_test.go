package agentapi

import (
	"testing"
	"time"
)

func TestSubagentRunsKeepTheFirstAndTheNewest(t *testing.T) {
	t0 := time.Unix(1000, 0)
	sa := Subagent{ID: "a"}
	sa.StartRun(t0, SubagentTriggerSpawn)
	sa.Status, sa.StartedAt = SubagentRunning, t0
	for i := 1; i <= 2*MaxSubagentRuns; i++ {
		sa.Status, sa.EndedAt = SubagentCompleted, t0.Add(time.Duration(2*i-1)*time.Second)
		shared := sa.Snapshot()
		sa.StartRun(t0.Add(time.Duration(2*i)*time.Second), SubagentTriggerAgent)
		sa.Status, sa.EndedAt = SubagentRunning, time.Time{}
		if shared.Runs[len(shared.Runs)-1].Status != SubagentCompleted {
			t.Fatal("StartRun changed a snapshot's runs")
		}
	}
	got := sa.Snapshot().Runs
	if len(got) != MaxSubagentRuns || got[0].Trigger != SubagentTriggerSpawn || !got[0].StartedAt.Equal(t0) || got[0].Status != SubagentCompleted {
		t.Fatalf("%d runs, first %+v", len(got), got[0])
	}
	last, beforeLast := got[len(got)-1], got[len(got)-2]
	if last.Status != SubagentRunning || !last.EndedAt.IsZero() || !last.StartedAt.Equal(t0.Add(200*time.Second)) ||
		beforeLast.Status != SubagentCompleted || !beforeLast.EndedAt.Equal(t0.Add(199*time.Second)) {
		t.Fatalf("newest runs = %+v, %+v", beforeLast, last)
	}
}

func TestSubagentStartRunRestoresAMissingSpawnRun(t *testing.T) {
	t0 := time.Unix(1000, 0)
	sa := Subagent{ID: "a", Status: SubagentCompleted, StartedAt: t0, EndedAt: t0.Add(time.Second)}
	sa.StartRun(t0.Add(2*time.Second), SubagentTriggerUser)
	want := []SubagentRun{
		{StartedAt: t0, EndedAt: t0.Add(time.Second), Status: SubagentCompleted, Trigger: SubagentTriggerSpawn},
		{StartedAt: t0.Add(2 * time.Second), Status: SubagentRunning, Trigger: SubagentTriggerUser},
	}
	if len(sa.Runs) != 2 || sa.Runs[0] != want[0] || sa.Runs[1] != want[1] {
		t.Fatalf("runs = %+v", sa.Runs)
	}
}
