package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
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

// decides makes prov's decision model pick choice, or abstain when choice
// is "".
func decides(prov interface {
	SetDecisionHook(func(context.Context, agentapi.DecisionRequest) (agentapi.DecisionAnswer, error))
}, choice string) {
	prov.SetDecisionHook(func(context.Context, agentapi.DecisionRequest) (agentapi.DecisionAnswer, error) {
		return agentapi.DecisionAnswer{Choice: choice, Abstained: choice == "", Model: "decider"}, nil
	})
}

func TestDecisionAnswerContract(t *testing.T) {
	for _, tt := range []struct {
		name   string
		answer agentapi.DecisionAnswer
		want   DecisionResult
		valid  bool
	}{
		{"first choice", agentapi.DecisionAnswer{Choice: "explained"}, DecisionResult{Choice: "explained"}, true},
		{"second choice", agentapi.DecisionAnswer{Choice: "changed"}, DecisionResult{Choice: "changed"}, true},
		{"abstention", agentapi.DecisionAnswer{Abstained: true}, DecisionResult{Abstained: true}, true},
		{name: "empty", answer: agentapi.DecisionAnswer{}},
		{name: "unknown", answer: agentapi.DecisionAnswer{Choice: "complete"}},
		{name: "padded", answer: agentapi.DecisionAnswer{Choice: " explained"}},
		{name: "choice and abstention", answer: agentapi.DecisionAnswer{Choice: "explained", Abstained: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkDecision(tt.answer, decisionFixture().Options)
			if got != tt.want || (err == nil) != tt.valid || err != nil && !errors.Is(err, ErrInvalidDecision) {
				t.Fatalf("check = %+v, %v; want %+v, valid=%v", got, err, tt.want, tt.valid)
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
		"too many": func(r *DecisionRequest) {
			for i := range maxDecisionOptions - 1 {
				r.Options = append(r.Options, DecisionOption{ID: "more-" + strconv.Itoa(i), Description: "More"})
			}
		},
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
	if len(prov.DecisionRequests()) != 0 || m.UtilityLog(0, 10).Today.Calls != 0 {
		t.Fatal("invalid input spent a decision call")
	}
}

// A decision goes to the decision model whole, whatever the Utility model
// is, and is logged and counted once under the model that answered.
func TestDecisionAsksTheDecisionModelAndAccountsOnce(t *testing.T) {
	prov := newAssistProvider()
	m, _ := assistManager(t, openTestStore(t), prov)
	// Different options on the next call must not reuse any state from the
	// first call.
	for i, utility := range []string{"a", "none"} {
		if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": utility}}); err != nil {
			t.Fatal(err)
		}
		input := decisionFixture()
		if i == 1 {
			input.Options[0].ID = "other"
		}
		prov.SetDecisionHook(func(_ context.Context, req agentapi.DecisionRequest) (agentapi.DecisionAnswer, error) {
			if req.Purpose != "reply-kind" || req.Instructions != input.Instructions || req.Input != input.Input || !slices.Equal(req.Options, input.Options) {
				t.Errorf("request did not preserve the decision: %+v", req)
			}
			if i == 0 {
				req.OnUsage(agentapi.UtilityUsage{InputTokens: 40, Tokens: []agentapi.TokenUsage{{Model: "decider", Input: 40}}})
			}
			return agentapi.DecisionAnswer{Choice: input.Options[0].ID, Model: "decider"}, nil
		})
		got, err := m.Decide(context.Background(), input)
		if err != nil || got.Choice != input.Options[0].ID || got.Abstained {
			t.Fatalf("decision = %+v, %v", got, err)
		}
		calls := m.UtilityLog(0, 10).Calls
		if len(calls) != i+1 || len(prov.DecisionRequests()) != i+1 || len(prov.UtilityRequests()) != 0 {
			t.Fatalf("wrong call count: %d logs, %d decisions, %d Utility calls", len(calls), len(prov.DecisionRequests()), len(prov.UtilityRequests()))
		}
		c := calls[0]
		if c.Provider != "fake" || c.Model != "decider" || c.Purpose != "decision-reply-kind" || c.Valid == nil || !*c.Valid || c.Answer != got.Choice || c.Outcome != utilityOK || c.Retry || c.Fallback || c.PromptChars != utf8.RuneCountInString(input.Instructions+input.Input) {
			t.Fatalf("decision log = %+v", c)
		}
		if i == 0 && (c.Estimated || c.InputTokens != 40 || c.OutputTokens != 0) || i == 1 && (!c.Estimated || c.InputTokens == 0 || c.OutputTokens != 0) {
			t.Fatalf("usage = %+v", c)
		}
	}
}

func TestDecisionFailuresAndPurposePause(t *testing.T) {
	prov := newAssistProvider()
	m, _ := assistManager(t, openTestStore(t), prov)
	providerErr := errors.New("provider unavailable")
	prov.SetDecisionHook(func(context.Context, agentapi.DecisionRequest) (agentapi.DecisionAnswer, error) {
		return agentapi.DecisionAnswer{}, providerErr
	})
	if _, err := m.Decide(context.Background(), decisionFixture()); !errors.Is(err, providerErr) {
		t.Fatalf("provider failure = %v", err)
	}
	if c := m.UtilityLog(0, 10).Calls[0]; c.Valid != nil || c.Outcome != utilityError || !c.Fallback {
		t.Fatalf("provider error counted as invalid: %+v", c)
	}
	decides(prov, "invented")
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
	if !errors.As(err, &paused) || paused.Code != codeUtilityPaused || len(prov.DecisionRequests()) != 5 {
		t.Fatalf("purpose did not pause after four invalid jobs: %v, calls=%d", err, len(prov.DecisionRequests()))
	}
	decides(prov, "")
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
	if len(prov.DecisionRequests()) != 10 {
		t.Fatalf("unexpected retries: %d calls", len(prov.DecisionRequests()))
	}
}

func TestDecisionRespectsUtilitySettings(t *testing.T) {
	for _, mode := range []string{"no provider", "off", "daily limit"} {
		t.Run(mode, func(t *testing.T) {
			prov := newAssistProvider()
			m, _ := assistManager(t, openTestStore(t), prov)
			wantCode := codeUtilityPaused
			switch mode {
			case "no provider":
				wantCode = codeUtilityUnavailable
				m.mu.Lock()
				info := m.infos["fake"]
				info.Available = false
				m.infos["fake"] = info
				m.mu.Unlock()
			case "off":
				setUtilityLimit(t, m, new(0))
			case "daily limit":
				setUtilityLimit(t, m, new(1))
				decides(prov, "")
				if _, err := m.Decide(context.Background(), decisionFixture()); err != nil {
					t.Fatal(err)
				}
			}
			before := len(prov.DecisionRequests())
			got, err := m.Decide(context.Background(), decisionFixture())
			var blocked *Error
			if !errors.As(err, &blocked) || blocked.Code != wantCode || got != (DecisionResult{}) || len(prov.DecisionRequests()) != before {
				t.Fatalf("blocked call = %+v, %v; calls=%d", got, err, len(prov.DecisionRequests())-before)
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
			prov.SetDecisionHook(func(ctx context.Context, _ agentapi.DecisionRequest) (agentapi.DecisionAnswer, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-cleanup
				// Even a provider returning success after cancellation cannot
				// turn the canceled job into a valid decision.
				return agentapi.DecisionAnswer{Choice: "explained"}, nil
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
			if !errors.Is(err, wantErr) || len(prov.DecisionRequests()) != 0 || m.UtilityLog(0, 10).Today.Calls != 0 {
				t.Fatalf("canceled before inference = %v, calls=%d", err, len(prov.DecisionRequests()))
			}
		})
	}
}

// The Utility model is the Settings model while it can be used, else the
// cheapest priced model, else the Thread's own model for a Thread's job;
// None opts out and a job with no Thread has nothing to run on.
func TestUtilityModelResolution(t *testing.T) {
	priced := []agentapi.Model{pricedModel("dear", 100, 500, 1e6), pricedModel("cheap", 10, 50, 1e6), {ID: "free"}, {ID: "byom/x"}}
	thread := &webSession{provider: "fake", model: "own"}
	for name, tc := range map[string]struct {
		models  []agentapi.Model
		setting string
		hidden  []string
		s       *webSession
		want    string
	}{
		"settings usable":             {priced, "dear", nil, thread, "dear"},
		"settings hidden stays":       {priced, "dear", []string{"dear"}, thread, "dear"},
		"settings no longer listed":   {priced, "gone", nil, thread, "cheap"},
		"settings custom without key": {priced, "byom/x", nil, thread, "cheap"},
		"unset takes cheapest":        {priced, "", nil, thread, "cheap"},
		"none priced, Thread job":     {selectionModels(), "", nil, thread, "own"},
		"none priced, no Thread":      {selectionModels(), "", nil, nil, ""},
		"settings unusable, Thread":   {selectionModels(), "gone", nil, thread, "own"},
		"none opts out":               {priced, "none", nil, thread, ""},
		"list not loaded yet":         {nil, "dear", nil, thread, "dear"},
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := assistManager(t, openTestStore(t), newAssistProvider())
			m.mu.Lock()
			info := m.infos["fake"]
			info.Models = tc.models
			m.infos["fake"] = info
			m.settings.TitleModel = map[string]string{"fake": tc.setting}
			m.settings.HiddenModels = map[string][]string{"fake": tc.hidden}
			m.settings.CustomModels = []CustomModel{{Name: "byom", ModelID: "x", APIKeyEnv: "UAM_TEST_UNSET_KEY"}}
			_, model, err := m.startUtilityLocked(tc.s)
			m.mu.Unlock()
			if err == nil {
				m.titles.Done()
			}
			if tc.want == "" {
				if !errors.Is(err, errUtilityUnavailable) {
					t.Fatalf("model = %+v, err = %v; want unavailable", model, err)
				}
				return
			}
			if err != nil || model != (UtilityModel{Provider: "fake", Model: tc.want}) {
				t.Fatalf("model = %+v, err = %v; want %s", model, err, tc.want)
			}
		})
	}
}

// A Thread's decision names the Thread in the log; neither it nor one with
// no Thread needs a Utility model.
func TestDecisionNamesItsThread(t *testing.T) {
	prov := newAssistProvider()
	m, project := assistManager(t, openTestStore(t), prov)
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "none"}}); err != nil {
		t.Fatal(err)
	}
	decides(prov, "")
	if got, err := m.Decide(context.Background(), decisionFixture()); err != nil || !got.Abstained {
		t.Fatalf("decision with no Thread = %+v, %v", got, err)
	}
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "b"})
	if err != nil {
		t.Fatal(err)
	}
	input := decisionFixture()
	input.TaskID = sum.ID
	if got, err := m.Decide(context.Background(), input); err != nil || !got.Abstained {
		t.Fatalf("Thread decision = %+v, %v", got, err)
	}
	if calls := m.UtilityLog(0, 10).Calls; len(calls) != 2 || calls[0].Model != "decider" || calls[0].TaskID != sum.ID || calls[1].TaskID != "" {
		t.Fatalf("logged calls = %+v", calls)
	}
}
