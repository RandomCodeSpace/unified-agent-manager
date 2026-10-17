package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// splitLine is one journal line at second s after a fixed start.
func splitLine(s float64, typ, agent, data string) string {
	at := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC).Add(time.Duration(s * float64(time.Second)))
	agentField := ""
	if agent != "" {
		agentField = `,"agentId":"` + agent + `"`
	}
	return fmt.Sprintf(`{"type":%q,"timestamp":%q%s,"data":%s}`, typ, at.Format(time.RFC3339Nano), agentField, data)
}

func toolStart(s float64, agent, id, name string) string {
	return splitLine(s, "tool.execution_start", agent, `{"toolCallId":"`+id+`","toolName":"`+name+`","arguments":{"command":"secret"}}`)
}

func toolDone(s float64, agent, id string) string {
	return splitLine(s, "tool.execution_complete", agent, `{"toolCallId":"`+id+`","success":true,"result":{"content":"secret output"}}`)
}

func modelCall(s float64, agent string, ms int) string {
	return splitLine(s, "session.usage_record", agent, fmt.Sprintf(`{"usage":{"model":"m","duration":%d}}`, ms))
}

func readSplit(t *testing.T, lines ...string) TimeSplit {
	t.Helper()
	got, err := readTimeSplit(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestTimeSplitIntervalMath(t *testing.T) {
	got := readSplit(t,
		splitLine(0, "session.start", "", `{}`),
		modelCall(10, "", 10000), // model [0,10]
		toolStart(10, "", "c1", "bash"),
		toolStart(12, "", "c2", "view"), // parallel with c1: counted once
		toolDone(15, "", "c2"),
		toolDone(20, "", "c1"), // local [10,20]
		toolStart(21, "", "c3", "bash"),
		splitLine(21, "permission.requested", "", `{"requestId":"r1","permissionRequest":{"toolCallId":"c3"}}`),
		splitLine(31, "permission.completed", "", `{"requestId":"r1","toolCallId":"c3","result":{"kind":"approved"}}`),
		toolDone(33, "", "c3"), // local [31,33], waiting [21,31]
		toolStart(34, "", "c4", "ask_user"),
		toolDone(40, "", "c4"), // waiting [34,40]
		toolStart(41, "", "c5", "task"),
		modelCall(50, "a1", 5000), // subagent model [45,50]
		toolStart(50, "a1", "c1", "bash"),
		toolDone(53, "a1", "c1"), // local [50,53]
		toolStart(60, "", "c6", "bash"),
		modelCall(64, "a1", 2000), // model [62,64] wins over c6
		toolDone(66, "", "c6"),    // local [60,62] + [64,66]
		toolDone(100, "", "c5"),   // the task tool is its subagent's time
		toolStart(70, "", "c7", "bash"),
		splitLine(75, "session.shutdown", "", `{}`), // c7 ends here: [70,75]
		splitLine(1000, "session.resume", "", `{}`),
		toolDone(1001, "", "c7"), // already ended
		toolStart(1010, "", "c8", "bash"),
		splitLine(1010, "permission.requested", "", `{"requestId":"r2"}`),
		// The CLI stopped without a shutdown; a new one resumed. The wait
		// lasts until the resume, the tool only to the old CLI's last event.
		splitLine(2010, "session.resume", "", `{}`),
		toolStart(2020, "", "c9", "bash"),
		toolDone(2021, "", "c9"), // local [2020,2021]
		toolStart(2022, "", "c11", "bash"),
		splitLine(2025, "abort", "", `{"reason":"user_initiated"}`), // c11: [2022,2025]
		toolDone(2026, "", "c11"),
		`not json`,
		toolStart(2030, "", "c10", "bash"),
		splitLine(2032, "assistant.message", "", `{"content":"secret"}`), // the journal ends: c10 [2030,2032]
	)
	model := int64(17000)
	want := TimeSplit{
		ModelMS: &model, ModelCalls: 3,
		LocalMS: (10 + 2 + 3 + 4 + 5 + 1 + 3 + 2) * 1000, LocalRuns: 10,
		WaitingMS: (10 + 6 + 1000) * 1000, Waits: 3,
		Tools: []TimeSplitTool{{Name: "bash", MS: (10 + 2 + 3 + 6 + 5 + 0 + 1 + 3 + 2) * 1000, Runs: 9}, {Name: "view", MS: 3000, Runs: 1}},
	}
	if !reflect.DeepEqual(got, want) {
		g, _ := json.Marshal(got)
		w, _ := json.Marshal(want)
		t.Fatalf("got  %s\nwant %s", g, w)
	}
}

func TestTimeSplitWithoutUsageRecordsReportsLocalOnly(t *testing.T) {
	got := readSplit(t,
		toolStart(0, "", "c1", "bash"),
		toolDone(2, "", "c1"),
	)
	if got.ModelMS != nil || got.ModelCalls != 0 || got.LocalMS != 2000 || got.LocalRuns != 1 {
		t.Fatalf("got %+v", got)
	}
	empty := readSplit(t)
	if empty.ModelMS != nil || empty.LocalMS != 0 || empty.Tools == nil {
		t.Fatalf("empty %+v", empty)
	}
}

func TestTaskTimeSplitRouteReadsJournalAndRefreshesWhenItGrows(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".copilot")
	t.Setenv("COPILOT_HOME", home)
	ts := newTestServer(t, ServerConfig{})
	sum, _ := createSession(t, ts.m, ts.prov)
	path := "/api/sessions/" + sum.ID + "/time-split"
	if w := ts.do(http.MethodGet, path, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated=%d", w.Code)
	}
	// No journal yet: nothing recorded.
	if w := ts.do(http.MethodGet, path, "", withCookie(ts)); w.Code != http.StatusOK || w.Body.String() != `{"model_ms":null,"model_calls":0,"local_ms":0,"local_runs":0,"waiting_ms":0,"waits":0,"tools":[]}`+"\n" {
		t.Fatalf("empty=%d %s", w.Code, w.Body)
	}
	dir := filepath.Join(home, "session-state", sum.ConversationID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(dir, "events.jsonl")
	write := func(lines ...string) {
		t.Helper()
		if err := os.WriteFile(journal, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(modelCall(1, "", 1000), toolStart(1, "", "c1", "bash"), toolDone(3, "", "c1"))
	w := ts.do(http.MethodGet, path, "", withCookie(ts))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"model_ms":1000,"model_calls":1,"local_ms":2000`) || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("route=%d %s", w.Code, w.Body)
	}
	write(modelCall(1, "", 1000), toolStart(1, "", "c1", "bash"), toolDone(3, "", "c1"), toolStart(4, "", "c2", "view"), toolDone(9, "", "c2"))
	if w := ts.do(http.MethodGet, path, "", withCookie(ts)); !strings.Contains(w.Body.String(), `"local_ms":7000,"local_runs":2`) {
		t.Fatalf("grown=%s", w.Body)
	}
}
