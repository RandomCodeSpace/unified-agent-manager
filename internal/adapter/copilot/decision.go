package copilot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// decisionTimeoutMS is the longest deadline decisions.ask accepts; the
// caller's ctx ends the call sooner.
const decisionTimeoutMS = 30000

// decisionFitsFloor is the least probability that an option fits for the
// pick to stand.
const decisionFitsFloor = 0.5

// AskDecision is optional on the client seam so the fixtures without a
// decision model keep their ordinary behavior.
func (a sdkClientAdapter) AskDecision(ctx context.Context, req *rpc.DecisionsAskRequest) (*rpc.DecisionsAskResult, error) {
	return a.c.RPC.Decisions.Ask(ctx, req)
}

type decisionClient interface {
	AskDecision(context.Context, *rpc.DecisionsAskRequest) (*rpc.DecisionsAskResult, error)
}

// RunDecision asks the account's default decision model through
// decisions.ask, which the CLI offers behind its DECISIONS_API feature flag.
// A choice question always picks an option, so a second question in the same
// request asks whether any is a reasonable answer: the answer abstains unless
// that is likelier than not and one option leads. req.Input goes as the
// request's context, apart from the questions.
func (p *webProvider) RunDecision(ctx context.Context, req agentapi.DecisionRequest) (agentapi.DecisionAnswer, error) {
	client, done, err := p.startUtility(ctx)
	if err != nil {
		return agentapi.DecisionAnswer{}, err
	}
	defer done()
	asker, ok := client.(decisionClient)
	if !ok {
		return agentapi.DecisionAnswer{}, agentapi.ErrUnsupported
	}
	options := make([]rpc.DecisionOption, len(req.Options))
	var listed strings.Builder
	for i, o := range req.Options {
		options[i] = rpc.DecisionOption{Value: o.ID, Description: o.Description}
		listed.WriteString("\n- " + o.ID + ": " + o.Description)
	}
	res, err := asker.AskDecision(ctx, &rpc.DecisionsAskRequest{
		Context:   &rpc.DecisionContextText{Text: req.Input},
		Purpose:   copilot.String("uam." + req.Purpose),
		TimeoutMs: new(int32(decisionTimeoutMS)),
		Questions: []rpc.DecisionQuestion{
			&rpc.DecisionQuestionChoice{Name: "choice", Instructions: req.Instructions, Options: options},
			&rpc.DecisionQuestionBoolean{Name: "fits",
				// Asking whether the evidence shows which option fits reads as
				// no for a prediction with two plausible answers, such as a
				// reply of yes or no; asking whether any is reasonable does not.
				Instructions: "Is at least one of these options a reasonable answer to the task for this context? Task: " + req.Instructions + "\nOptions:" + listed.String(),
				WhenTrue:     copilot.String("At least one option is a reasonable answer."),
				WhenFalse:    copilot.String("None of the options is a reasonable answer, or the context is unrelated to the task.")},
		},
	})
	if err != nil {
		if rpcErr, ok := errors.AsType[*copilot.RPCError](err); ok && rpcErr.Code == -32601 {
			return agentapi.DecisionAnswer{}, fmt.Errorf("%w: the Copilot CLI has no decisions API; update the Copilot CLI", agentapi.ErrUnsupported)
		}
		return agentapi.DecisionAnswer{}, fmt.Errorf("ask the decision model: %s", errText(err))
	}
	if res == nil {
		return agentapi.DecisionAnswer{}, errors.New("the decision model returned no answers")
	}
	if u := res.Usage; u != nil && req.OnUsage != nil {
		tokens := agentapi.TokenUsage{Model: res.Model, Input: u.InputTokens, Output: u.OutputTokens, DurationMS: res.LatencyMs}
		if u.CachedInputTokens != nil {
			tokens.CacheRead = *u.CachedInputTokens
		}
		if u.CacheWriteTokens != nil {
			tokens.CacheWrite = *u.CacheWriteTokens
		}
		req.OnUsage(agentapi.UtilityUsage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, Tokens: []agentapi.TokenUsage{tokens}})
	}
	// A refused question has neither answer type, so it abstains too.
	answer, fits := agentapi.DecisionAnswer{Model: res.Model}, false
	for _, a := range res.Answers {
		switch a := a.(type) {
		case *rpc.DecisionAnswerChoice:
			if !a.Tied {
				answer.Choice = a.Value
			}
		case *rpc.DecisionAnswerBoolean:
			fits = a.Probability >= decisionFitsFloor
		}
	}
	if !fits || answer.Choice == "" {
		answer.Choice, answer.Abstained = "", true
	}
	return answer, nil
}
