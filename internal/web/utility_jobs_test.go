package web

import (
	"strings"
	"testing"
	"time"
)

func TestUtilityAnswerShapes(t *testing.T) {
	for _, tc := range []struct {
		name, reply string
		valid       bool
	}{
		{"clean", `{"verb":"Fixed","object":"the test"}`, true},
		{"preamble", `Here is the answer: {"verb":"Fixed","object":"the test"}`, true},
		{"think", `<think>{"verb":"Wrong"}</think>` + "\n```json\n" + `{"verb":"Fixed","object":"the test"}` + "\n```", true},
		{"two answers", `{"verb":"Fixed","object":"the test"} {"verb":"Reviewed","object":"the test"}`, false},
		{"refusal", `I cannot help. {"verb":"Fixed","object":"the test"}`, false},
		{"embedded object", `{"answer":{"verb":"Fixed","object":"the test"}}`, false},
		{"unknown field", `{"verb":"Fixed","object":"the test","extra":true}`, false},
		{"oversize", strings.Repeat("x", 2049) + `{"verb":"Fixed","object":"the test"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var answer struct {
				Verb   string `json:"verb"`
				Object string `json:"object"`
			}
			err := parseAnswer(tc.reply, &answer)
			if (err == nil) != tc.valid || tc.valid && answer.Verb != "Fixed" {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
		})
	}
}

func TestUtilityInvalidStreakPausesUntilMidnight(t *testing.T) {
	now := time.Now()
	l := utilityLog{}
	for i := 0; i < 4; i++ {
		if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != "" {
			t.Fatal(reason)
		}
		l.add(UtilityCall{At: now, Purpose: purposeCommitMessage, Outcome: utilityOK, Valid: new(false), Fallback: true})
	}
	if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != skippedInvalid {
		t.Fatalf("reason=%q", reason)
	}
	if reason := l.reserve(now, purposeTitle, 400, false); reason != "" {
		t.Fatal(reason)
	}
	if reason := l.reserve(midnightAfter(now), purposeCommitMessage, 400, false); reason != "" {
		t.Fatal(reason)
	}
}

func TestUtilityInvalidStreakCountsTurnsNotAttempts(t *testing.T) {
	now := time.Now()
	l := utilityLog{}
	answer := func(valid, final bool) {
		t.Helper()
		if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != "" {
			t.Fatal(reason)
		}
		l.add(UtilityCall{At: now, Purpose: purposeCommitMessage, Outcome: utilityOK, Valid: new(valid), Fallback: final && !valid})
	}
	// Four turns whose first answer was invalid and whose retry was valid.
	for range 4 {
		answer(false, false)
		answer(true, true)
	}
	// Three turns with two invalid answers each.
	for range 3 {
		answer(false, false)
		answer(false, true)
	}
	if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != "" {
		t.Fatalf("paused after 3 failed turns: %q", reason)
	}
	answer(false, false)
	answer(false, true)
	if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != skippedInvalid {
		t.Fatalf("4 failed turns: reason=%q", reason)
	}
}

func TestUtilityInvalidWindowDropsOldAnswers(t *testing.T) {
	now := time.Now()
	l := utilityLog{}
	for i := 0; i < 13; i++ {
		if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != "" {
			t.Fatal(reason)
		}
		l.add(UtilityCall{At: now, Purpose: purposeCommitMessage, Outcome: utilityOK, Valid: new(i >= 3 && i < 12)})
	}
	if reason := l.reserve(now, purposeCommitMessage, 400, false); reason != "" {
		t.Fatal(reason)
	}
}

func TestUtilityCommitPromptCap(t *testing.T) {
	prompt := commitDraftPrompt([]string{strings.Repeat("a", 500)}, false, strings.Repeat("界", 6000))
	if len(prompt) > 6<<10 {
		t.Fatalf("prompt bytes=%d", len(prompt))
	}
}
