package copilot

import (
	"crypto/sha256"
	"fmt"
	"maps"

	copilot "github.com/github/copilot-sdk/go"
)

// viewTool is the CLI's file read tool.
const viewTool = "view"

// readKey is one agent's view of one file range; agent is "" for the main
// agent.
type readKey struct{ agent, path, viewRange string }

// readOnceLocked returns the result that replaces an agent's repeat view
// whose text is identical to the last one it received for the same path and
// range; nil otherwise. Hashing the text itself lets any change to the file,
// by anyone, pass through in full. Nothing is replaced during a compaction,
// which may drop the earlier view.
func (c *conversation) readOnceLocked(in copilot.PostToolUseHookInput) any {
	if in.ToolName != viewTool || c.compaction != compactionNone {
		return nil
	}
	args, _ := in.ToolArgs.(map[string]any)
	path, _ := args["path"].(string)
	result, _ := in.ToolResult.(map[string]any)
	text, ok := result["textResultForLlm"].(string)
	if path == "" || !ok {
		return nil
	}
	key := readKey{in.AgentID, path, fmt.Sprint(args["view_range"])}
	sum := sha256.Sum256([]byte(text))
	if prev, seen := c.views[key]; seen && prev == sum {
		c.viewRepeats++
		out := maps.Clone(result)
		out["textResultForLlm"] = "uam read-once: " + path + " is unchanged since your earlier view in this conversation; use that result."
		delete(out, "contents")
		return out
	}
	if c.views == nil {
		c.views = map[readKey][sha256.Size]byte{}
	}
	c.views[key] = sum
	return nil
}

// forgetViewsLocked forgets the views of agent ("" for the main agent),
// whose context no longer holds them after a compaction or its end.
func (c *conversation) forgetViewsLocked(agent string) {
	maps.DeleteFunc(c.views, func(k readKey, _ [sha256.Size]byte) bool { return k.agent == agent })
}
