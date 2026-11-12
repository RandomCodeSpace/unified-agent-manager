package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type decisionFake struct {
	*fakeClient
	asked []*rpc.DecisionsAskRequest
	res   *rpc.DecisionsAskResult
	err   error
}

func (f *decisionFake) AskDecision(_ context.Context, req *rpc.DecisionsAskRequest) (*rpc.DecisionsAskResult, error) {
	f.asked = append(f.asked, req)
	return f.res, f.err
}

func decisionProvider(t *testing.T, client sdkClient) *webProvider {
	t.Helper()
	p := newWebProvider(func() (sdkClient, error) { return client, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p
}

func decisionRequest() agentapi.DecisionRequest {
	return agentapi.DecisionRequest{Purpose: "reply-kind", Instructions: "Classify the reply.", Input: "It explained the setting.",
		Options: []agentapi.DecisionOption{{ID: "explained", Description: "Explained something"}, {ID: "changed", Description: "Changed something"}}}
}

// The pick stands only when one option leads and the model finds it likelier
// than not that an option fits; every other answer abstains.
func TestRunDecisionAbstainsUnlessAnOptionFits(t *testing.T) {
	choice := func(value string, tied bool) rpc.DecisionAnswer {
		return &rpc.DecisionAnswerChoice{Name: "choice", Value: value, Tied: tied}
	}
	fits := func(p float64) rpc.DecisionAnswer { return &rpc.DecisionAnswerBoolean{Name: "fits", Probability: p} }
	for name, tc := range map[string]struct {
		answers []rpc.DecisionAnswer
		want    string
	}{
		"fits":              {[]rpc.DecisionAnswer{choice("changed", false), fits(0.9)}, "changed"},
		"at the floor":      {[]rpc.DecisionAnswer{choice("changed", false), fits(decisionFitsFloor)}, "changed"},
		"does not fit":      {[]rpc.DecisionAnswer{choice("changed", false), fits(0.49)}, ""},
		"tied":              {[]rpc.DecisionAnswer{choice("explained", true), fits(1)}, ""},
		"choice refused":    {[]rpc.DecisionAnswer{&rpc.DecisionAnswerRefused{Name: "choice"}, fits(1)}, ""},
		"fits refused":      {[]rpc.DecisionAnswer{choice("changed", false), &rpc.DecisionAnswerRefused{Name: "fits"}}, ""},
		"fits not answered": {[]rpc.DecisionAnswer{choice("changed", false)}, ""},
		"no answers":        {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := &decisionFake{fakeClient: &fakeClient{}, res: &rpc.DecisionsAskResult{Model: "decider", Answers: tc.answers}}
			got, err := decisionProvider(t, f).RunDecision(context.Background(), decisionRequest())
			if want := (agentapi.DecisionAnswer{Choice: tc.want, Abstained: tc.want == "", Model: "decider"}); err != nil || got != want {
				t.Fatalf("answer = %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

// One request carries the evidence as context, the choice question and the
// question whether any option fits; the usage it reports is passed on.
func TestRunDecisionRequestAndUsage(t *testing.T) {
	cached := int64(12)
	f := &decisionFake{fakeClient: &fakeClient{}, res: &rpc.DecisionsAskResult{Model: "decider", LatencyMs: 640,
		Usage:   &rpc.DecisionUsage{InputTokens: 420, CachedInputTokens: &cached},
		Answers: []rpc.DecisionAnswer{&rpc.DecisionAnswerChoice{Name: "choice", Value: "explained"}, &rpc.DecisionAnswerBoolean{Name: "fits", Probability: 1}}}}
	var usage agentapi.UtilityUsage
	req := decisionRequest()
	req.OnUsage = func(u agentapi.UtilityUsage) { usage = u }
	if got, err := decisionProvider(t, f).RunDecision(context.Background(), req); err != nil || got.Choice != "explained" {
		t.Fatalf("answer = %+v, %v", got, err)
	}
	if len(f.asked) != 1 || len(f.asked[0].Questions) != 2 {
		t.Fatalf("asked = %+v", f.asked)
	}
	asked := f.asked[0]
	text, _ := asked.Context.(*rpc.DecisionContextText)
	pick, _ := asked.Questions[0].(*rpc.DecisionQuestionChoice)
	fit, _ := asked.Questions[1].(*rpc.DecisionQuestionBoolean)
	if text == nil || text.Text != req.Input || asked.Model != nil || deref(asked.Purpose) != "uam.reply-kind" || asked.TimeoutMs == nil || *asked.TimeoutMs != decisionTimeoutMS {
		t.Fatalf("request = %+v", asked)
	}
	if pick == nil || pick.Instructions != req.Instructions || len(pick.Options) != 2 || pick.Options[1] != (rpc.DecisionOption{Value: "changed", Description: "Changed something"}) {
		t.Fatalf("choice question = %+v", pick)
	}
	if fit == nil || !strings.Contains(fit.Instructions, req.Instructions) || !strings.Contains(fit.Instructions, "- changed: Changed something") || strings.Contains(fit.Instructions, req.Input) {
		t.Fatalf("fits question = %+v", fit)
	}
	want := agentapi.TokenUsage{Model: "decider", Input: 420, CacheRead: 12, DurationMS: 640}
	if usage.InputTokens != 420 || usage.OutputTokens != 0 || len(usage.Tokens) != 1 || usage.Tokens[0] != want {
		t.Fatalf("usage = %+v", usage)
	}
}

func TestRunDecisionFailures(t *testing.T) {
	// A client with no decisions API, and a CLI that does not know the method.
	if _, err := decisionProvider(t, &fakeClient{}).RunDecision(context.Background(), decisionRequest()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("no decision client = %v", err)
	}
	f := &decisionFake{fakeClient: &fakeClient{}, err: &copilot.RPCError{Code: -32601, Message: "method not found"}}
	if _, err := decisionProvider(t, f).RunDecision(context.Background(), decisionRequest()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("old CLI = %v", err)
	}
	f.err = &copilot.RPCError{Code: -32603, Message: "decisions are not enabled"}
	if got, err := decisionProvider(t, f).RunDecision(context.Background(), decisionRequest()); err == nil || errors.Is(err, agentapi.ErrUnsupported) || !strings.Contains(err.Error(), "decisions are not enabled") || got != (agentapi.DecisionAnswer{}) {
		t.Fatalf("provider error = %+v, %v", got, err)
	}
	f.err = nil
	if _, err := decisionProvider(t, f).RunDecision(context.Background(), decisionRequest()); err == nil {
		t.Fatal("a missing result was accepted")
	}
}
