package copilot

import (
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// compactionState is where the main conversation's compaction stands.
type compactionState uint8

const (
	compactionNone compactionState = iota
	// compactionBlocking runs from its start until the main agent goes on:
	// /compact, and compaction a turn waits for, hold the conversation.
	compactionBlocking
	// compactionBackground is automatic compaction the turn goes on beside;
	// the CLI reports its end only when the turn ends.
	compactionBackground
)

// observeCompactionLocked marks the main conversation's compaction as
// running in the background at the main agent's first progress during it:
// a step starting, a message or a tool call, none of which a compaction that
// holds the conversation lets through. The Thread then shows as working, not
// compacting, and the compaction's model call leaves the phase, where it
// would hide the turn's own work: when it ends is not reported until the
// turn's end.
func (c *conversation) observeCompactionLocked(ev copilot.SessionEvent, agentID string) {
	if c.compaction != compactionBlocking || agentID != "" {
		return
	}
	switch ev.Data.(type) {
	case *rpc.AssistantTurnStartData, *rpc.AssistantMessageData, *rpc.ToolExecutionStartData:
	default:
		return
	}
	c.compaction = compactionBackground
	c.emitLocked(agentapi.Event{Kind: agentapi.EventCompaction, CompactingBackground: true})
	if c.phase.endCall(compactionCall) {
		c.emitPhaseLocked()
	}
}

// endBackgroundCompactionLocked ends, with the main turn, a compaction in
// the background: the CLI reports its end as the turn ends, and one never
// reported must not mark the next turn.
func (c *conversation) endBackgroundCompactionLocked() {
	if c.compaction == compactionBackground {
		c.compaction = compactionNone
		c.emitLocked(agentapi.Event{Kind: agentapi.EventCompaction})
	}
}

// maxBackgroundCompaction is the highest point where compaction may start:
// below the CLI's default 0.95, where a turn waits for compaction, which
// stays as it is.
const maxBackgroundCompaction = 0.90

// infiniteSessions sets where the CLI starts compacting (threshold, 0 to 1,
// at most maxBackgroundCompaction); nil keeps its defaults.
func infiniteSessions(threshold float64) *copilot.InfiniteSessionConfig {
	if threshold <= 0 {
		return nil
	}
	return &copilot.InfiniteSessionConfig{BackgroundCompactionThreshold: copilot.Float64(min(threshold, maxBackgroundCompaction))}
}
