package copilot

import (
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func modelTokens(ev copilot.SessionEvent, d *rpc.AssistantUsageData) *agentapi.TokenUsage {
	if d.InputTokens == nil && d.OutputTokens == nil && d.CacheReadTokens == nil && d.CacheWriteTokens == nil {
		return nil
	}
	return &agentapi.TokenUsage{
		Model: d.Model, Time: ev.Timestamp,
		Input: max(orZero(d.InputTokens), 0), Output: max(orZero(d.OutputTokens), 0),
		CacheRead: max(orZero(d.CacheReadTokens), 0), CacheWrite: max(orZero(d.CacheWriteTokens), 0),
	}
}
