package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func decisionFixture() DecisionRequest {
	return DecisionRequest{Purpose: "reply-kind", Instructions: "Classify what the final reply reports.", Input: "The final reply says it explained how the setting works.",
		Options: []DecisionOption{{ID: "explained", Description: "Explained something"}, {ID: "changed", Description: "Changed something"}}}
}

func TestDecisionAnswerContract(t *testing.T) {
	for _, tt := range []struct {
		name, reply string
		want        DecisionResult
		valid       bool
	}{
		{"first choice", `{"choice":"explained"}`, DecisionResult{Choice: "explained"}, true},
		{"second choice", ` {"choice":"changed"} `, DecisionResult{Choice: "changed"}, true},
		{"abstention", `{"choice":null}`, DecisionResult{Abstained: true}, true},
		{"preamble", `Answer: {"choice":"explained"}`, DecisionResult{Choice: "explained"}, true},
		{"fence", "```json\n{\"choice\":\"explained\"}\n```", DecisionResult{Choice: "explained"}, true},
		{"thinking", `<think>{"choice":"changed"}</think>{"choice":"explained"}`, DecisionResult{Choice: "explained"}, true},
		{"thinking abstention", "<think>hmm</think>\n{\"choice\":null}", DecisionResult{Abstained: true}, true},
		{name: "missing", reply: `{}`},
		{name: "empty", reply: ""},
		{name: "unknown", reply: `{"choice":"complete"}`},
		{name: "empty choice", reply: `{"choice":""}`},
		{name: "unknown field", reply: `{"choice":"explained","reason":"fine"}`},
		{name: "multiple", reply: `{"choice":"explained"}{"choice":"changed"}`},
		{name: "trailing prose", reply: `{"choice":"explained"} done`},
		{name: "refusal", reply: `I cannot choose. {"choice":"explained"}`},
		{name: "number", reply: `{"choice":1}`},
		{name: "boolean", reply: `{"choice":false}`},
		{name: "array", reply: `{"choice":[]}`},
		{name: "object", reply: `{"choice":{}}`},
		{name: "null object", reply: `null`},
		{name: "incomplete", reply: `{"choice":"explained"`},
		{name: "oversized", reply: strings.Repeat(" ", 2048) + `{"choice":null}`},
		{name: "invalid UTF-8", reply: "{\"choice\":\"\xff\"}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDecision(tt.reply, decisionFixture().Options)
			if got != tt.want || (err == nil) != tt.valid || err != nil && !errors.Is(err, ErrInvalidDecision) {
				t.Fatalf("parse = %+v, %v; want %+v, valid=%v", got, err, tt.want, tt.valid)
			}
		})
	}
}

func TestDecisionRejectsInvalidInputBeforeInference(t *testing.T) {
	prov := newAssistProvider()
	m, _ := assistManager(t, openTestStore(t), prov)
	for name, change := range map[string]func(*DecisionRequest){
		"missing purpose":  func(r *DecisionRequest) { r.Purpose = "" },
		"unstable purpose": func(r *DecisionRequest) { r.Purpose = "some/user input" },
		"long purpose":     func(r *DecisionRequest) { r.Purpose = strings.Repeat("p", 49) },
		"no instruction":   func(r *DecisionRequest) { r.Instructions = " " },
		"long instruction": func(r *DecisionRequest) { r.Instructions = strings.Repeat("x", 4097) },
		"empty evidence":   func(r *DecisionRequest) { r.Input = "\n " },
		"long evidence":    func(r *DecisionRequest) { r.Input = strings.Repeat("x", 16385) },
		"invalid UTF-8":    func(r *DecisionRequest) { r.Input = "\xff" },
		"one option":       func(r *DecisionRequest) { r.Options = r.Options[:1] },
		"too many":         func(r *DecisionRequest) { r.Options = make([]DecisionOption, 33) },
		"duplicate ID":     func(r *DecisionRequest) { r.Options[1].ID = r.Options[0].ID },
		"invalid ID":       func(r *DecisionRequest) { r.Options[0].ID = "with space" },
		"no description":   func(r *DecisionRequest) { r.Options[0].Description = "" },
		"long description": func(r *DecisionRequest) { r.Options[0].Description = strings.Repeat("x", 513) },
	} {
		t.Run(name, func(t *testing.T) {
			req := decisionFixture()
			change(&req)
			got, err := m.Decide(context.Background(), req)
			var inputErr *Error
			if got != (DecisionResult{}) || !errors.As(err, &inputErr) || inputErr.Status != http.StatusBadRequest {
				t.Fatalf("invalid request = %+v, %v", got, err)
			}
		})
	}
	if len(prov.UtilityRequests()) != 0 || m.UtilityLog(0, 10).Today.Calls != 0 {
		t.Fatal("invalid input spent a Utility call")
	}
}

func TestDecisionSendsAClosedSchema(t *testing.T) {
	prov := newAssistProvider()
	m, _ := assistManager(t, openTestStore(t), prov)
	prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return `{"choice":null}`, nil })
	if _, err := m.Decide(context.Background(), decisionFixture()); err != nil {
		t.Fatal(err)
	}
	reqs := prov.UtilityRequests()
	if len(reqs) != 1 {
		t.Fatalf("%d Utility calls", len(reqs))
	}
	got, _ := json.Marshal(reqs[0].ResponseSchema)
	want := `{"additionalProperties":false,"properties":{"choice":{"anyOf":[{"enum":["explained","changed"]},{"type":"null"}]}},"required":["choice"],"type":"object"}`
	if string(got) != want {
		t.Fatalf("schema = %s, want %s", got, want)
	}
}

func TestDecisionUsesConfiguredUtilityAndAccountsOnce(t *testing.T) {
	prov := newAssistProvider()
	m, _ := assistManager(t, openTestStore(t), prov)
	// A different configured model and different options on the next call must
	// not reuse any state from the first call.
	for i, model := range []string{"a", "b"} {
		if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": model}}); err != nil {
			t.Fatal(err)
		}
		input := decisionFixture()
		if i == 1 {
			input.Options[0].ID = "other"
		}
		prov.SetUtilityHook(func(_ context.Context, req agentapi.UtilityRequest) (string, error) {
			if req.Model != model || req.Purpose != "decision-reply-kind" || req.Workdir != "" || len(req.Tools) != 0 || len(req.Attachments) != 0 || req.CallTool != nil || req.Timeout != decisionTimeout {
				t.Errorf("unexpected Utility request: model=%s purpose=%s", req.Model, req.Purpose)
			}
			var prompt struct {
				Input   string           `json:"input"`
				Options []DecisionOption `json:"options"`
			}
			if err := json.Unmarshal([]byte(req.Prompt), &prompt); err != nil || prompt.Input != input.Input || len(prompt.Options) != 2 || prompt.Options[0] != input.Options[0] || !strings.Contains(req.System, input.Instructions) {
				t.Errorf("prompt did not preserve request: %v", err)
			}
			if i == 0 {
				req.OnUsage(agentapi.UtilityUsage{InputTokens: 40, OutputTokens: 6, Credits: 0.002})
			}
			return `{"choice":"` + input.Options[0].ID + `"}`, nil
		})
		got, err := m.Decide(context.Background(), input)
		if err != nil || got.Choice != input.Options[0].ID || got.Abstained {
			t.Fatalf("decision = %+v, %v", got, err)
		}
		calls := m.UtilityLog(0, 10).Calls
		if len(calls) != i+1 || len(prov.UtilityRequests()) != i+1 {
			t.Fatalf("wrong call count: %d logs, %d provider calls", len(calls), len(prov.UtilityRequests()))
		}
		c := calls[0]
		req := prov.UtilityRequests()[i]
		if c.Provider != "fake" || c.Model != model || c.Purpose != req.Purpose || c.Valid == nil || !*c.Valid || c.Answer != got.Choice || c.Outcome != utilityOK || c.Retry || c.Fallback || c.PromptChars != utf8.RuneCountInString(req.System+req.Prompt) {
			t.Fatalf("decision log = %+v", c)
		}
		if i == 0 && (c.Estimated || c.InputTokens != 40 || c.OutputTokens != 6 || c.Credits != 0.002) || i == 1 && (!c.Estimated || c.InputTokens == 0 || c.OutputTokens == 0) {
			t.Fatalf("usage = %+v", c)
		}
	}
}

func TestDecisionFailuresAndPurposePause(t *testing.T) {
	prov := newAssistProvider()
	m, _ := assistManager(t, openTestStore(t), prov)
	providerErr := errors.New("provider unavailable")
	prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return "", providerErr })
	if _, err := m.Decide(context.Background(), decisionFixture()); !errors.Is(err, providerErr) {
		t.Fatalf("provider failure = %v", err)
	}
	if c := m.UtilityLog(0, 10).Calls[0]; c.Valid != nil || c.Outcome != utilityError || !c.Fallback {
		t.Fatalf("provider error counted as invalid: %+v", c)
	}
	prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return `{"choice":"invented"}`, nil })
	for range 4 {
		got, err := m.Decide(context.Background(), decisionFixture())
		if !errors.Is(err, ErrInvalidDecision) || got != (DecisionResult{}) {
			t.Fatalf("invalid result = %+v, %v", got, err)
		}
		if c := m.UtilityLog(0, 10).Calls[0]; c.Valid == nil || *c.Valid || !c.Fallback || c.Retry || c.Reason != "invalid_answer" || c.Outcome != utilityError {
			t.Fatalf("invalid answer log = %+v", c)
		}
	}
	_, err := m.Decide(context.Background(), decisionFixture())
	var paused *Error
	if !errors.As(err, &paused) || paused.Code != codeUtilityPaused || len(prov.UtilityRequests()) != 5 {
		t.Fatalf("purpose did not pause after four invalid jobs: %v, calls=%d", err, len(prov.UtilityRequests()))
	}
	prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return `{"choice":null}`, nil })
	input := decisionFixture()
	input.Purpose = "another-purpose"
	for range 5 {
		got, err := m.Decide(context.Background(), input)
		if err != nil || !got.Abstained || got.Choice != "" {
			t.Fatalf("valid abstention = %+v, %v", got, err)
		}
		if c := m.UtilityLog(0, 20).Calls[0]; c.Valid == nil || !*c.Valid || c.Fallback || c.Answer != "[abstain]" {
			t.Fatalf("abstention log = %+v", c)
		}
	}
	if len(prov.UtilityRequests()) != 10 {
		t.Fatalf("unexpected retries: %d calls", len(prov.UtilityRequests()))
	}
}

func TestDecisionRespectsUtilitySettings(t *testing.T) {
	for _, mode := range []string{"disabled model", "off", "daily limit"} {
		t.Run(mode, func(t *testing.T) {
			prov := newAssistProvider()
			m, _ := assistManager(t, openTestStore(t), prov)
			wantCode := codeUtilityPaused
			switch mode {
			case "disabled model":
				wantCode = codeUtilityUnavailable
				if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "none"}}); err != nil {
					t.Fatal(err)
				}
			case "off":
				setUtilityLimit(t, m, new(0))
			case "daily limit":
				setUtilityLimit(t, m, new(1))
				prov.SetUtilityHook(func(context.Context, agentapi.UtilityRequest) (string, error) { return `{"choice":null}`, nil })
				if _, err := m.Decide(context.Background(), decisionFixture()); err != nil {
					t.Fatal(err)
				}
			}
			before := len(prov.UtilityRequests())
			got, err := m.Decide(context.Background(), decisionFixture())
			var blocked *Error
			if !errors.As(err, &blocked) || blocked.Code != wantCode || got != (DecisionResult{}) || len(prov.UtilityRequests()) != before {
				t.Fatalf("blocked call = %+v, %v; calls=%d", got, err, len(prov.UtilityRequests())-before)
			}
		})
	}
}

func TestDecisionCancellationAndShutdown(t *testing.T) {
	for _, source := range []string{"caller", "shutdown", "deadline"} {
		t.Run(source, func(t *testing.T) {
			prov := newAssistProvider()
			m, _ := assistManager(t, openTestStore(t), prov)
			started, canceled, cleanup := make(chan struct{}), make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-cleanup:
				default:
					close(cleanup)
				}
			}()
			prov.SetUtilityHook(func(ctx context.Context, _ agentapi.UtilityRequest) (string, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-cleanup
				// Even a provider returning success after cancellation cannot
				// turn the canceled job into a valid decision.
				return `{"choice":"explained"}`, nil
			})
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := context.Canceled
			if source == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), time.Second)
				wantErr = context.DeadlineExceeded
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				got, err := m.Decide(ctx, decisionFixture())
				if got != (DecisionResult{}) {
					t.Errorf("accepted late decision: %+v", got)
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("decision did not start")
			}
			var shutdown chan error
			switch source {
			case "caller":
				cancel()
			case "shutdown":
				shutdown = make(chan error, 1)
				go func() { shutdown <- m.Shutdown(context.Background()) }()
			}
			select {
			case <-canceled:
			case <-time.After(5 * time.Second):
				t.Fatal("provider was not canceled")
			}
			select {
			case err := <-done:
				t.Fatalf("decision returned before provider cleanup: %v", err)
			default:
			}
			if shutdown != nil {
				select {
				case err := <-shutdown:
					t.Fatalf("shutdown returned before cleanup: %v", err)
				default:
				}
			}
			close(cleanup)
			select {
			case err := <-done:
				if !errors.Is(err, wantErr) {
					t.Fatalf("canceled decision = %v, want %v", err, wantErr)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("decision did not finish")
			}
			if shutdown != nil {
				if err := <-shutdown; err != nil {
					t.Fatal(err)
				}
			}
			if c := m.UtilityLog(0, 10).Calls[0]; c.Valid != nil || c.Outcome != utilityError || len(m.titleSlots) != 0 {
				t.Fatalf("canceled call = %+v; slots=%d", c, len(m.titleSlots))
			}
		})
	}
}

func TestDecisionCancellationBeforeInference(t *testing.T) {
	for _, queued := range []bool{false, true} {
		t.Run(map[bool]string{false: "already canceled", true: "slots full"}[queued], func(t *testing.T) {
			prov := newAssistProvider()
			m, _ := assistManager(t, openTestStore(t), prov)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantErr := context.Canceled
			if queued {
				for range cap(m.titleSlots) {
					m.titleSlots <- struct{}{}
				}
				defer func() {
					for range cap(m.titleSlots) {
						<-m.titleSlots
					}
				}()
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				wantErr = context.DeadlineExceeded
			} else {
				cancel()
			}
			_, err := m.Decide(ctx, decisionFixture())
			if !errors.Is(err, wantErr) || len(prov.UtilityRequests()) != 0 || m.UtilityLog(0, 10).Today.Calls != 0 {
				t.Fatalf("canceled before inference = %v, calls=%d", err, len(prov.UtilityRequests()))
			}
		})
	}
}
