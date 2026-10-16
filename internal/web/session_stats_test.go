package web

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
