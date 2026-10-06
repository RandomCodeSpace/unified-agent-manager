package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func setUtilityLimit(t *testing.T, m *Manager, limit *int) {
	t.Helper()
	if _, err := m.UpdateSettings(SettingsPatch{UtilityLimit: &limit}); err != nil {
		t.Fatal(err)
	}
}

// Every title goes through the Utility log: real usage when the provider
// reports it, estimates when not; past the day's limit the call is skipped
// and logged, the provider's title stays, and a raised limit lets calls run
// again. The log and today's count survive a restart.
func TestUtilityCallsAreLoggedAndCapped(t *testing.T) {
	m, prov, st, project := titleManager(t)
	reported := true
	prov.SetTitleHook(func(_ context.Context, req agentapi.TitleRequest) (string, error) {
		if reported && req.OnUsage != nil {
			req.OnUsage(agentapi.UtilityUsage{InputTokens: 90, OutputTokens: 5, Credits: 0.002})
		}
		return "Generated title", nil
	})
	setUtilityLimit(t, m, new(1))
	titled := func(text string) SessionSummary {
		t.Helper()
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
		if err != nil {
			t.Fatal(err)
		}
		mustSubmit(t, m, sum.ID, text, mustUUID(t), ModeSend, SubmissionAccepted)
		return sum
	}
	logged := func(n int) UtilityLog {
		t.Helper()
		waitUntil(t, "the logged calls", func() bool { return len(m.UtilityLog(0, utilityPage).Calls) == n })
		return m.UtilityLog(0, utilityPage)
	}

	first := titled("Add a dark mode toggle")
	waitUntil(t, "the title", func() bool { return summaryOf(t, m, first.ID).Title == "Generated title" })
	l := logged(1)
	c := l.Calls[0]
	if c.Purpose != purposeTitle || c.Provider != "fake" || c.Model != "a" || c.TaskID != first.ID || c.ProjectID != project || c.Outcome != utilityOK ||
		c.PromptChars != len("Add a dark mode toggle") || c.ReplyChars != len("Generated title") || c.InputTokens != 90 || c.OutputTokens != 5 || c.Estimated || c.Credits != 0.002 || c.Day != dayOf(time.Now()) {
		t.Fatalf("logged call = %+v", c)
	}
	if today := l.Today; today.Calls != 1 || today.Limit != 1 || !today.Paused || !today.ResetsAt.Equal(midnightAfter(time.Now())) {
		t.Fatalf("today = %+v", today)
	}

	second := titled("Fix the login form")
	l = logged(2)
	if c := l.Calls[0]; c.Outcome != utilitySkipped || c.Reason != skippedLimit || c.TaskID != second.ID || c.PromptChars != 0 || c.InputTokens != 0 {
		t.Fatalf("skipped call = %+v", c)
	}
	if n := len(prov.TitleRequests()); n != 1 || summaryOf(t, m, second.ID).Title != "" {
		t.Fatalf("a call past the limit ran: %d requests, title %q", n, summaryOf(t, m, second.ID).Title)
	}

	setUtilityLimit(t, m, new(5))
	reported = false
	third := titled("Write the release notes")
	waitUntil(t, "the title after the limit was raised", func() bool { return summaryOf(t, m, third.ID).Title == "Generated title" })
	l = logged(3)
	if c := l.Calls[0]; c.Outcome != utilityOK || !c.Estimated || c.InputTokens != 6 || c.OutputTokens != 4 || c.Credits != 0 {
		t.Fatalf("estimated call = %+v", c)
	}
	if today := l.Today; today.Calls != 2 || today.Paused {
		t.Fatalf("today after raising the limit = %+v", today)
	}
	if d := l.Days; len(d) != 1 || d[0].Calls != 2 || d[0].Skipped != 1 || d[0].InputTokens != 96 || !d[0].Estimated || d[0].Credits != 0.002 {
		t.Fatalf("days = %+v", d)
	}

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := startManager(t, st, prov)
	if l := again.UtilityLog(0, utilityPage); len(l.Calls) != 3 || l.Calls[0].ID != 3 || l.Today.Calls != 2 || l.Today.Limit != 5 {
		t.Fatalf("log after a restart = %+v", l)
	}
	setUtilityLimit(t, again, new(0))
	if l := again.UtilityLog(0, 1); !l.Today.Paused || l.Today.Limit != 0 {
		t.Fatalf("today with the limit at 0 = %+v", l.Today)
	}
	_, err := again.runUtility(context.Background(), UtilityCall{Purpose: purposePlannerTriage}, "p", nil)
	if e, ok := errors.AsType[*Error](err); !ok || e.Code != codeUtilityPaused || !strings.Contains(e.Message, "Background AI is off") || utilityFailed("the triage", err) != e {
		t.Fatalf("call with the limit at 0 = %v", err)
	}
	if c := again.UtilityLog(0, 1).Calls[0]; c.Outcome != utilitySkipped || c.Reason != skippedOff {
		t.Fatalf("call with the limit at 0 logged as %+v", c)
	}
}

// The log pages newest first, totals every day kept, and drops lines past
// retention or unreadable when it loads.
// A Utility request's logged prompt size is what it sent the model: its
// system message and its prompt, for every purpose alike.
func TestUtilityPromptSizeIsSystemAndPrompt(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t", Model: "a"})
	if err != nil {
		t.Fatal(err)
	}
	finishTurn(prov.Last(), "fix the test", "Fixed it.")
	if _, err := m.SuggestReplies(context.Background(), sum.ID); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the suggested replies call", func() bool { return utilityCalls(prov, purposeSuggestReplies) == 1 })
	var req agentapi.UtilityRequest
	for _, r := range prov.UtilityRequests() {
		if r.Purpose == purposeSuggestReplies {
			req = r
		}
	}
	if req.System == "" {
		t.Fatal("the request has no system message")
	}
	want := utf8.RuneCountInString(req.System + req.Prompt)
	waitUntil(t, "the logged call", func() bool {
		for _, c := range m.UtilityLog(0, utilityPage).Calls {
			if c.Purpose == purposeSuggestReplies && c.TaskID == sum.ID {
				return c.PromptChars == want
			}
		}
		return false
	})
}

// Rewriting the log never writes through a file planted where a fixed
// temporary name would be: each write uses a new temporary file.
func TestUtilityLogRewriteUsesAFreshTemporaryFile(t *testing.T) {
	st := openTestStore(t)
	dir := filepath.Dir(st.Path())
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, utilityLogFile+".tmp")); err != nil {
		t.Fatal(err)
	}
	m := startManager(t, st)
	m.utility.mu.Lock()
	m.utility.calls = []UtilityCall{{ID: 1, At: time.Now(), Purpose: purposeTitle, Outcome: utilityOK}}
	m.utility.rewriteLocked()
	m.utility.mu.Unlock()
	if data, err := os.ReadFile(victim); err != nil || string(data) != "keep" {
		t.Fatalf("victim = %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dir, utilityLogFile)); err != nil || !strings.Contains(string(data), `"id":1`) {
		t.Fatalf("log = %q, %v", data, err)
	}
}

func TestUtilityLogPagesAndRetention(t *testing.T) {
	st := openTestStore(t)
	now := time.Now()
	old := UtilityCall{ID: 1, At: now.AddDate(0, 0, -utilityRetentionDays-1), Purpose: purposeTitle, Outcome: utilityOK}
	yesterday := UtilityCall{ID: 2, At: now.AddDate(0, 0, -1), Purpose: purposePlannerTriage, Outcome: utilityError, Reason: "boom", PromptChars: 40, InputTokens: 10}
	var lines []string
	for _, c := range []UtilityCall{old, yesterday} {
		b, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(b))
	}
	lines = append(lines, "not json")
	path := filepath.Join(filepath.Dir(st.Path()), utilityLogFile)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := startManager(t, st)
	for i := range 5 {
		m.utility.add(UtilityCall{At: now, Purpose: purposePlannerSuggest, Outcome: utilityOK, PromptChars: i})
	}
	l := m.UtilityLog(0, 2)
	if len(l.Calls) != 2 || l.Calls[0].ID != 7 || l.Calls[1].ID != 6 || l.Next != 6 {
		t.Fatalf("first page = %+v", l)
	}
	if len(l.Days) != 2 || l.Days[0].Day != dayOf(now) || l.Days[0].Calls != 5 || l.Days[1].Calls != 1 || l.Days[1].Errors != 1 || l.Days[1].PromptChars != 40 {
		t.Fatalf("days = %+v", l.Days)
	}
	l = m.UtilityLog(l.Next, 4)
	if len(l.Calls) != 4 || l.Calls[0].ID != 5 || l.Calls[3].ID != 2 || l.Next != 0 {
		t.Fatalf("last page = %+v", l.Calls)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(data); strings.Count(text, "\n") != 6 || strings.Contains(text, "not json") || strings.Contains(text, `"id":1,`) {
		t.Fatalf("log file after load =\n%s", text)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log file mode = %v, %v", info, err)
	}
}

func TestUtilityLimitSettingAndRoutes(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	for _, body := range []string{`{"utility_daily_limit":-1}`, `{"utility_daily_limit":1001}`, `{"utility_daily_limit":"5"}`, `{"utility_daily_limit":1.5}`} {
		patch(body, http.StatusBadRequest)
	}
	if got := patch(`{"utility_daily_limit":0}`, http.StatusOK); !strings.Contains(got, `"utility_daily_limit":0`) {
		t.Fatalf("limit 0 = %s", got)
	}
	cfg, err := ts.m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if l := cfg.WebSettings.UtilityDailyLimit; l == nil || *l != 0 {
		t.Fatalf("stored limit = %v", l)
	}
	if got := patch(`{"utility_daily_limit":null}`, http.StatusOK); strings.Contains(got, "utility_daily_limit") {
		t.Fatalf("limit reset = %s", got)
	}
	if w := ts.do(http.MethodGet, "/api/utility", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET /api/utility without sign-in = %d", w.Code)
	}
	for _, q := range []string{"?before=x", "?before=0", "?limit=0", "?limit=201"} {
		if w := ts.do(http.MethodGet, "/api/utility"+q, "", auth); w.Code != http.StatusBadRequest {
			t.Fatalf("GET /api/utility%s = %d", q, w.Code)
		}
	}
	w := ts.do(http.MethodGet, "/api/utility?limit=10", "", auth)
	var l UtilityLog
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &l) != nil || l.Today.Limit != store.DefaultUtilityDailyLimit || l.Today.Paused || l.Calls == nil || l.Days == nil {
		t.Fatalf("GET /api/utility = %d %s", w.Code, w.Body)
	}
}
