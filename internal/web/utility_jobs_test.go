package web

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A suggestion's retry is not made once its Thread settled or its folder
// went away during the first attempt, as a worktree Thread's does.
func TestSuggestionRetryStopsForASettledThreadOrAMissingFolder(t *testing.T) {
	for name, gone := range map[string]func(m *Manager, id, workdir string){
		"settled": func(m *Manager, id, _ string) {
			if _, err := m.Settle(id); err != nil {
				t.Error(err)
			}
		},
		"folder removed": func(_ *Manager, _, workdir string) {
			if err := os.RemoveAll(workdir); err != nil {
				t.Error(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			prov := newAssistProvider()
			m, project := assistManager(t, openTestStore(t), prov)
			sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: "t"})
			if err != nil {
				t.Fatal(err)
			}
			finishTurn(prov.Last(), "make the test pass", "Fixed it.")
			prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
				if req.Purpose != purposeSuggestReplies {
					return "", errors.New("not here")
				}
				gone(m, sum.ID, sum.Workdir)
				return "not\na valid answer", nil
			})
			if got, err := m.SuggestReplies(context.Background(), sum.ID); err != nil || len(got.Replies) != 0 {
				t.Fatalf("suggestions = %+v, %v", got, err)
			}
			if n := utilityCalls(prov, purposeSuggestReplies); n != 1 {
				t.Fatalf("%d suggestion calls, want the first only", n)
			}
		})
	}
}

func TestUtilityAnswerShapes(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"clean", `{"verb":"Fixed","object":"the test"}`, true},
		{"preamble", `Here is the answer: {"verb":"Fixed","object":"the test"}`, true},
		{"think", `<think>{"verb":"Wrong"}</think>` + "\n```json\n" + `{"verb":"Fixed","object":"the test"}` + "\n```", true},
		{"casing", `{"verb":"fixed","object":"the test"}`, false},
		{"enum", `{"verb":"Deleted","object":"the test"}`, false},
		{"range", `{"verb":"Fixed","object":"one two three four five six seven"}`, false},
		{"two answers", `{"verb":"Fixed","object":"the test"} {"verb":"Reviewed","object":"the test"}`, false},
		{"refusal", `I cannot help. {"verb":"Fixed","object":"the test"}`, false},
		{"embedded object", `{"answer":{"verb":"Fixed","object":"the test"}}`, false},
		{"invented object", `{"verb":"Fixed","object":"a production outage"}`, false},
		{"missing object", `{"verb":"Fixed"}`, false},
		{"null object", `{"verb":"Fixed","object":null}`, false},
		{"unknown field", `{"verb":"Fixed","object":"the test","extra":true}`, false},
		{"oversize", strings.Repeat("x", 2049) + `{"verb":"Fixed","object":"the test"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateOutcome(tc.reply, "Fixed the test, one two three four five six seven")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
	for _, reply := range []string{"Run tests\nCommit this", "<think>" + strings.Repeat("x", 321) + "</think>Commit this", "Open https://example.com", "Visit www.example.com", "Visit example.com", "Send mailto:dev@example.com", "foo | bar", strings.Repeat("a", 81), "I fixed the test.", "Run tests. Commit this."} {
		if _, err := validateSuggestion(reply, "I fixed the test."); err == nil {
			t.Errorf("accepted %q", reply)
		}
	}
	if got, err := validateSuggestion("Commit this", "I fixed the test."); err != nil || got != "Commit this" {
		t.Fatalf("valid suggestion = %q, %v", got, err)
	}
}

func TestUtilityJobRetryAndFallback(t *testing.T) {
	for _, tc := range []struct {
		name    string
		replies []string
		failure bool
		want    string
		calls   int
	}{
		{"retry valid", []string{"wrong", `{"verb":"Fixed","object":"the test"}`}, false, "Fixed the test", 2},
		{"retry invalid", []string{"wrong", "wrong again"}, false, "", 2},
		{"runner error", nil, true, "", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := newAssistProvider()
			m, _ := assistManager(t, openTestStore(t), prov)
			n := 0
			prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
				n++
				if n == 2 && (req.Timeout != 20*time.Second || !strings.Contains(req.System, "previous answer")) {
					t.Error("retry did not strengthen request")
				}
				if tc.failure {
					return "", errors.New("runner failed")
				}
				return tc.replies[n-1], nil
			})
			got, err := m.runJob(context.Background(), outcomeJob, UtilityCall{Provider: "fake"}, prov, agentapi.UtilityRequest{Model: "a", Prompt: "Fixed the test"})
			if got != tc.want || n != tc.calls || (err != nil) != tc.failure {
				t.Fatalf("got %q, %v, %d calls", got, err, n)
			}
			log := m.UtilityLog(0, 10)
			if log.Today.Calls != n || len(log.Calls) != n || log.Calls[0].Fallback != (tc.want == "") {
				t.Fatalf("log=%+v", log)
			}
			if n == 2 && !log.Calls[0].Retry {
				t.Fatal("retry not logged")
			}
		})
	}
}

func TestUtilityInvalidStreakPausesUntilMidnight(t *testing.T) {
	now := time.Now()
	l := utilityLog{}
	for i := 0; i < 4; i++ {
		if reason := l.reserve(now, purposeOutcome, 400, false); reason != "" {
			t.Fatal(reason)
		}
		l.add(UtilityCall{At: now, Purpose: purposeOutcome, Outcome: utilityOK, Valid: new(false)})
	}
	if reason := l.reserve(now, purposeOutcome, 400, false); reason != skippedInvalid {
		t.Fatalf("reason=%q", reason)
	}
	if reason := l.reserve(now, purposeTitle, 400, false); reason != "" {
		t.Fatal(reason)
	}
	if reason := l.reserve(midnightAfter(now), purposeOutcome, 400, false); reason != "" {
		t.Fatal(reason)
	}
}

func TestUtilityInvalidWindowDropsOldAnswers(t *testing.T) {
	now := time.Now()
	l := utilityLog{}
	for i := 0; i < 13; i++ {
		if reason := l.reserve(now, purposeOutcome, 400, false); reason != "" {
			t.Fatal(reason)
		}
		l.add(UtilityCall{At: now, Purpose: purposeOutcome, Outcome: utilityOK, Valid: new(i >= 3 && i < 12)})
	}
	if reason := l.reserve(now, purposeOutcome, 400, false); reason != "" {
		t.Fatal(reason)
	}
}

func TestUtilityCommitPromptCap(t *testing.T) {
	prompt := commitDraftPrompt([]string{strings.Repeat("a", 500)}, false, strings.Repeat("界", 6000))
	if len(prompt) > 6<<10 {
		t.Fatalf("prompt bytes=%d", len(prompt))
	}
}
