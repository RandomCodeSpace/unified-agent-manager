package copilot

import (
	"context"
	"errors"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

type fakePredictSession struct {
	*fakeSession
	res     rpc.SessionLimitPredictionResult
	err     error
	request *rpc.SessionLimitPredictionPredictRequest
}

func (f *fakePredictSession) PredictLimit(_ context.Context, req *rpc.SessionLimitPredictionPredictRequest) (rpc.SessionLimitPredictionResult, error) {
	f.request = req
	return f.res, f.err
}

func TestPredictTurnCreditsReadsThePromptRunsRecommendedTier(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	if _, err := c.PredictTurnCredits(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("session without the RPC = %v", err)
	}
	f := &fakePredictSession{fakeSession: h.fs, res: &rpc.SessionLimitPredictionResultAvailable{Prediction: rpc.SessionLimitPredictionDetails{
		RecommendedCap: 30, Tiers: []rpc.SessionLimitPredictionTierOption{{Cap: 2.5, Tier: rpc.SessionLimitPredictionTierAdditionalHeadroom}, {Cap: 0.9, Tier: rpc.SessionLimitPredictionTierRecommended}},
	}}}
	c.sess = f
	got, err := c.PredictTurnCredits(context.Background())
	if err != nil || got != 0.9 {
		t.Fatalf("prediction = %v, %v", got, err)
	}
	if f.request == nil || f.request.ClientType == nil || *f.request.ClientType != rpc.SessionLimitPredictionClientTypeCLIPrompt || f.request.ModelID != nil {
		t.Fatalf("request = %+v", f.request)
	}
	if len(h.fs.sent) != 0 {
		t.Fatal("a prediction ran a turn")
	}
	f.res = &rpc.SessionLimitPredictionResultAvailable{Prediction: rpc.SessionLimitPredictionDetails{RecommendedCap: 30}}
	if _, err := c.PredictTurnCredits(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("no recommended tier = %v", err)
	}
	f.res = &rpc.SessionLimitPredictionResultUnavailable{Reason: rpc.SessionLimitPredictionUnavailableReasonAutoUnresolved}
	if _, err := c.PredictTurnCredits(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("unavailable = %v", err)
	}
	f.res, f.err = nil, &copilot.RPCError{Code: -32601}
	if _, err := c.PredictTurnCredits(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("old CLI = %v", err)
	}
}
