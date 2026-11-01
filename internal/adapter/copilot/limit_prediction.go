package copilot

import (
	"context"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/github/copilot-sdk/go/rpc"
)

// limitPredictionSession is the experimental session.limitPrediction.predict
// (SDK v1.0.28); a rename of it breaks only this file.
type limitPredictionSession interface {
	PredictLimit(context.Context, *rpc.SessionLimitPredictionPredictRequest) (rpc.SessionLimitPredictionResult, error)
}

func (a sdkSessionAdapter) PredictLimit(ctx context.Context, req *rpc.SessionLimitPredictionPredictRequest) (rpc.SessionLimitPredictionResult, error) {
	return a.s.RPC.LimitPrediction.Predict(ctx, req)
}

// PredictTurnCredits is the "recommended" usage tier of a non-interactive
// prompt run (one prompt carried to its end) on the session's current model,
// from the CLI's baseline usage data. No model runs. RecommendedCap is not
// used: it is raised to the CLI's minimum session limit (30 credits with CLI
// 1.0.105, where the gpt-6-luna tier reads 0.9).
func (c *conversation) PredictTurnCredits(ctx context.Context) (float64, error) {
	if c.isClosed() {
		return 0, agentapi.ErrClosed
	}
	p, ok := c.sess.(limitPredictionSession)
	if !ok {
		return 0, agentapi.ErrUnsupported
	}
	client := rpc.SessionLimitPredictionClientTypeCLIPrompt
	res, err := p.PredictLimit(ctx, &rpc.SessionLimitPredictionPredictRequest{ClientType: &client})
	if err != nil {
		return 0, contextReadError(err)
	}
	got, ok := res.(*rpc.SessionLimitPredictionResultAvailable)
	if !ok {
		return 0, agentapi.ErrUnsupported
	}
	for _, t := range got.Prediction.Tiers {
		if t.Tier == rpc.SessionLimitPredictionTierRecommended {
			return t.Cap, nil
		}
	}
	return 0, agentapi.ErrUnsupported
}
