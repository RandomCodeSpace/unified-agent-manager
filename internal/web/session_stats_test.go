package web

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSessionStatsCountsJournalsAndWritesKeyValueFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".copilot")
	t.Setenv("COPILOT_HOME", home)
	writeJournal := func(id string, lines ...string) {
		t.Helper()
		dir := filepath.Join(home, "session-state", id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeJournal("a",
		`{"type":"session.start","timestamp":"2026-10-01T10:00:00Z","data":{}}`,
		`{"type":"user.message","timestamp":"2026-10-01T10:00:01Z","data":{"content":"secret prompt"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-10-01T10:00:02Z","data":{}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-01T10:00:03Z","data":{"toolCallId":"c1","toolName":"bash"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-10-01T10:00:04Z","data":{"toolCallId":"c1","success":false,"result":{"content":"boom"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-01T10:00:05Z","data":{"toolCallId":"c2","toolName":"view"}}`,
		`{"type":"tool.execution_complete","timestamp":"2026-10-01T10:00:06Z","data":{"toolCallId":"c2","success":true,"result":{"content":"ok"}}}`,
		`{"type":"permission.requested","timestamp":"2026-10-01T10:00:07Z","data":{}}`,
		`{"type":"permission.completed","timestamp":"2026-10-01T10:00:08Z","data":{"result":{"kind":"denied-interactively-by-user"}}}`,
		`{"type":"abort","timestamp":"2026-10-01T10:00:09Z","data":{"reason":"user_initiated"}}`,
		`not json`,
	)
	writeJournal("b",
		`{"type":"session.start","timestamp":"2026-10-03T09:00:00Z","data":{}}`,
		`{"type":"session.error","timestamp":"2026-10-03T09:00:01Z","data":{"errorType":"query","message":"secret error"}}`,
	)
	m, _, _ := newTestManager(t)

	got, err := m.SessionStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stats := map[string]string{}
	for _, s := range got.Stats {
		stats[s.Key] = s.Value
	}
	for key, want := range map[string]string{
		"scan.skipped":                             "1",
		"sessions.total":                           "2",
		"sessions.with_prompts":                    "1",
		"sessions.first":                           "2026-10-01",
		"sessions.last":                            "2026-10-03",
		"work.prompts":                             "1",
		"work.tool_calls":                          "2",
		"friction.turns_unfinished":                "1",
		"friction.tool_failures":                   "1",
		"friction.tool_failure_pct":                "50",
		"friction.permission_prompts":              "1",
		"aborts.user_initiated":                    "1",
		"errors.query":                             "1",
		"permissions.denied-interactively-by-user": "1",
		"tools.bash":                               "1",
		"tool_failures.bash":                       "1",
	} {
		if stats[key] != want {
			t.Errorf("%s = %q, want %q", key, stats[key], want)
		}
	}
	data, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(got.Path) != filepath.Dir(m.store.Path()) || !strings.Contains(string(data), "\nsessions.total=2\n") {
		t.Fatalf("file %s:\n%s", got.Path, data)
	}
	if strings.Contains(string(data), "secret") {
		t.Fatalf("file holds journal text:\n%s", data)
	}
}

func TestSessionStatsRecentCountsTheLastSevenDays(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".copilot")
	t.Setenv("COPILOT_HOME", home)
	recent := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	for id, lines := range map[string][]string{
		"old": {
			`{"type":"user.message","timestamp":"2026-01-01T10:00:00Z","data":{}}`,
			`{"type":"tool.execution_start","timestamp":"2026-01-01T10:00:01Z","data":{"toolCallId":"c1","toolName":"bash"}}`,
		},
		"both": {
			`{"type":"tool.execution_start","timestamp":"2026-01-02T10:00:01Z","data":{"toolCallId":"c1","toolName":"view"}}`,
			`{"type":"user.message","timestamp":"` + recent + `","data":{}}`,
			`{"type":"tool.execution_start","timestamp":"` + recent + `","data":{"toolCallId":"c2","toolName":"bash"}}`,
		},
	} {
		dir := filepath.Join(home, "session-state", id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	m, _, _ := newTestManager(t)

	got, err := m.SessionStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values := func(list []SessionStat) map[string]string {
		out := map[string]string{}
		for _, s := range list {
			out[s.Key] = s.Value
		}
		return out
	}
	all, last := values(got.Stats), values(got.Recent)
	for key, want := range map[string][2]string{
		"sessions.total":  {"2", "1"},
		"work.prompts":    {"2", "1"},
		"work.tool_calls": {"3", "1"},
		"tools.bash":      {"2", "1"},
		"tools.view":      {"1", ""},
	} {
		if all[key] != want[0] || last[key] != want[1] {
			t.Errorf("%s = %q all, %q recent; want %q, %q", key, all[key], last[key], want[0], want[1])
		}
	}
	if _, ok := last["uam.settle"]; ok || last["config.threads"] != "" {
		t.Errorf("recent holds uam or config keys: %v", last)
	}
	data, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\nwork.prompts=2\n") || !strings.Contains(string(data), "\n7d.work.prompts=1\n") || strings.Contains(string(data), "7d.scan.") {
		t.Fatalf("file:\n%s", data)
	}
}

func TestSessionStatsRoadmapCounts(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".copilot")
	t.Setenv("COPILOT_HOME", home)
	dir := filepath.Join(home, "session-state", "c")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user.message","timestamp":"2026-10-05T10:00:00Z","data":{"content":"secret prompt"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-10-05T10:00:00Z","data":{"turnId":"0"}}`,
		`{"type":"permission.requested","timestamp":"2026-10-05T10:00:01Z","data":{"requestId":"r1","permissionRequest":{"kind":"shell","fullCommandText":"rm secret-file","intention":"secret intent","canOfferSessionApproval":true}}}`,
		`{"type":"permission.completed","timestamp":"2026-10-05T10:00:03Z","data":{"requestId":"r1","result":{"kind":"approved"}}}`,
		`{"type":"permission.requested","timestamp":"2026-10-05T10:00:04Z","data":{"requestId":"r2","permissionRequest":{"kind":"shell","fullCommandText":"rm secret-file"}}}`,
		`{"type":"permission.completed","timestamp":"2026-10-05T10:00:08Z","data":{"requestId":"r2","result":{"kind":"approved"}}}`,
		`{"type":"permission.requested","timestamp":"2026-10-05T10:00:08Z","data":{"requestId":"r3","permissionRequest":{"kind":"shell","fullCommandText":"rm secret-file"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:02Z","data":{"toolCallId":"t1","toolName":"bash","arguments":{"command":"gh run view secret"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:03Z","data":{"toolCallId":"t2","toolName":"bash","arguments":{"command":"gh run view secret"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:04Z","data":{"toolCallId":"t3","toolName":"bash","arguments":{"command":"sleep 30"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:05Z","data":{"toolCallId":"t4","toolName":"bash","arguments":{"command":"while true; do date; done"}}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:06Z","data":{"toolCallId":"t5","toolName":"view","arguments":"secret"}}`,
		`{"type":"session.usage_record","timestamp":"2026-10-05T10:00:07Z","data":{"usage":{"model":"m1","initiator":"user","inputTokens":100,"outputTokens":10,"cacheReadTokens":50,"cacheWriteTokens":5,"reasoningTokens":3,"cost":1,"timeToFirstTokenMs":200}}}`,
		`{"type":"session.usage_record","timestamp":"2026-10-05T10:00:08Z","data":{"usage":{"model":"m2","initiator":"sub-agent","inputTokens":40,"outputTokens":4,"cost":0.5,"timeToFirstTokenMs":400}}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-10-05T10:00:10Z","data":{"turnId":"0"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-10-05T10:00:10Z","data":{"turnId":"1"}}`,
		// The same command again in the next model step of the same prompt.
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:11Z","data":{"toolCallId":"t7","toolName":"bash","arguments":{"command":"gh run view secret"}}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-10-05T10:00:12Z","data":{"turnId":"1"}}`,
		`{"type":"user.message","timestamp":"2026-10-05T10:00:12Z","data":{"content":"continue","source":"uam-stall-retry"}}`,
		`{"type":"subagent.started","timestamp":"2026-10-05T10:00:13Z","data":{}}`,
		`{"type":"assistant.turn_start","agentId":"a1","timestamp":"2026-10-05T10:00:13Z","data":{"turnId":"0"}}`,
		// A subagent's command is not a repeat of the main agent's.
		`{"type":"tool.execution_start","agentId":"a1","timestamp":"2026-10-05T10:00:13Z","data":{"toolCallId":"t6","toolName":"bash","arguments":{"command":"sleep 30"}}}`,
		`{"type":"assistant.turn_end","agentId":"a1","timestamp":"2026-10-05T10:00:14Z","data":{"turnId":"0"}}`,
		`{"type":"subagent.completed","timestamp":"2026-10-05T10:00:14Z","data":{}}`,
		`{"type":"session.model_change","timestamp":"2026-10-05T10:00:15Z","data":{"source":"automatic","cause":"rate_limit_auto_switch","newModel":"m3"}}`,
		// A new prompt starts afresh.
		`{"type":"user.message","timestamp":"2026-10-05T10:00:20Z","data":{"content":"secret again"}}`,
		`{"type":"assistant.turn_start","timestamp":"2026-10-05T10:00:20Z","data":{"turnId":"0"}}`,
		`{"type":"tool.execution_start","timestamp":"2026-10-05T10:00:20Z","data":{"toolCallId":"t8","toolName":"bash","arguments":{"command":"gh run view secret"}}}`,
		`{"type":"assistant.turn_end","timestamp":"2026-10-05T10:00:21Z","data":{"turnId":"0"}}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, _ := newTestManager(t)
	got, err := m.SessionStats(context.Background(), SessionStat{"config.uam_version", "test"})
	if err != nil {
		t.Fatal(err)
	}
	stats := map[string]string{}
	for _, s := range got.Stats {
		stats[s.Key] = s.Value
	}
	for key, want := range map[string]string{
		"approvals.wait_median_s":        "2.0",
		"approvals.wait_p90_s":           "4.0",
		"approvals.wait_total_s":         "6",
		"approvals.session_offer_pct":    "33",
		"approvals.with_intention_pct":   "33",
		"approvals.repeats":              "2",
		"approvals.repeat_pct":           "66",
		"approvals.commands_asked_3x":    "1",
		"polling.shell_calls":            "7",
		"polling.sleep_or_loop":          "3",
		"polling.sleep_or_loop_pct":      "42",
		"polling.same_command_in_prompt": "2",
		"turns.completed":                "4",
		"turns.median_s":                 "1.0",
		"turns.p90_s":                    "10.0",
		"turns.tool_calls_median":        "1",
		"turns.no_tools_pct":             "0",
		"turns.per_prompt_median":        "1",
		"tokens.total_calls":             "2",
		"tokens.total_input":             "140",
		"tokens.total_cost":              "1.50",
		"tokens.user_input":              "100",
		"tokens.user_cache_read":         "50",
		"tokens.user_cache_write":        "5",
		"tokens.user_reasoning":          "3",
		"tokens.subagent_calls":          "1",
		"tokens.subagent_output":         "4",
		"tokens.other_calls":             "0",
		"models.ttfb_median_ms":          "200",
		"models.ttfb_p90_ms":             "400",
		"models.rate_limit_switches":     "1",
		"model_calls.m1":                 "1",
		"model_changes.automatic":        "1",
		"activity.owner_prompts":         "2",
		"activity.active_days":           "1",
		"activity.prompts_per_day":       "2.0",
		"activity.subagents_completed":   "1",
		"activity.stall_retries":         "1",
		"config.uam_version":             "test",
		"config.threads":                 "0",
		"config.suggest_replies":         "on",
		"uam.routines":                   "0",
	} {
		if stats[key] != want {
			t.Errorf("%s = %q, want %q", key, stats[key], want)
		}
	}
	data, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret") || strings.Contains(string(data), "gh run") {
		t.Fatalf("file holds journal text:\n%s", data)
	}
}

func TestUAMCountsRoundTrip(t *testing.T) {
	t.Setenv("COPILOT_HOME", filepath.Join(t.TempDir(), ".copilot"))
	m, _, _ := newTestManager(t)
	m.count("forks")
	m.count("forks")
	m.countN("trail_notes", 3)
	m.countN("nothing", 0)
	m.counts.save()

	var back uamCounts
	back.loadLocked(m.store)
	if len(back.n) != 2 || back.n["forks"] != 2 || back.n["trail_notes"] != 3 {
		t.Fatalf("counts read back = %v", back.n)
	}
	got, err := m.SessionStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got.Stats, SessionStat{"uam.forks", "2"}) || !slices.Contains(got.Stats, SessionStat{"uam.trail_notes", "3"}) {
		t.Fatalf("stats = %v", got.Stats)
	}
}
