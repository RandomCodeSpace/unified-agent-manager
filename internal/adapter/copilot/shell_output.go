package copilot

import (
	"slices"
	"strings"

	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// A running shell call's tail holds at most maxTailLines lines of at most
// maxTailLine bytes each.
const (
	maxTailLines = 10
	maxTailLine  = 512
)

// shellOutput is what a running shell call streamed so far. tool.shell_output
// appends to one of its streams in chunks that need not end on a line.
type shellOutput struct {
	next    int64 // the lowest Sequence not yet applied
	clipped bool  // the output reached maxToolText and stays its head
	// lines are the newest complete lines, oldest first; partial holds each
	// stream's unfinished line, in the order they began.
	lines   []agentapi.OutputLine
	partial []partialLine
}

type partialLine struct {
	stream rpc.ToolShellOutputStream
	text   string
	full   bool // text reached maxTailLine; the rest of the line is dropped
}

// shellOutput applies a chunk of a running shell call's output to it and
// reports whether that changed the call: the output grows until maxToolText,
// past which it stays the head, as the completion's clip does, and the tail
// follows the newest lines. A chunk for an ended call or one already applied
// changes nothing.
func (t *transcript) shellOutput(d *rpc.ToolShellOutputData, it *agentapi.Item) bool {
	if _, ended := t.ended[d.ToolCallID]; ended {
		return false
	}
	sh := t.shells[d.ToolCallID]
	if sh == nil {
		sh = &shellOutput{}
		t.shells[d.ToolCallID] = sh
	}
	if d.Sequence < sh.next {
		return false
	}
	sh.next = d.Sequence + 1
	tc := t.tool(d.ToolCallID)
	output := tc.Output
	if !sh.clipped {
		output, sh.clipped = t.clip(tc.Output+d.Text, maxToolText)
	}
	stream := rpc.ToolShellOutputStreamStdout
	if d.Stream != nil {
		stream = *d.Stream
	}
	sh.feed(stream, d.Text)
	tail := sh.tail()
	if output == tc.Output && slices.Equal(tail, tc.Tail) {
		return false
	}
	tc.Output, tc.Tail = output, tail
	it.ID, it.Kind, it.Tool, it.Clipped = d.ToolCallID, agentapi.ItemTool, cloneTool(tc), sh.clipped || t.clippedInput[d.ToolCallID]
	return true
}

// feed adds a chunk of one stream: each line it ends joins the complete
// lines, and the rest stays that stream's unfinished line.
func (sh *shellOutput) feed(stream rpc.ToolShellOutputStream, text string) {
	for text != "" {
		line, rest, ended := strings.Cut(text, "\n")
		i := slices.IndexFunc(sh.partial, func(p partialLine) bool { return p.stream == stream })
		if i < 0 {
			i = len(sh.partial)
			sh.partial = append(sh.partial, partialLine{stream: stream})
		}
		if p := &sh.partial[i]; !p.full {
			if room := maxTailLine - len(p.text); len(line) > room {
				line, p.full = clip(line, room), true
			}
			p.text += line
		}
		if !ended {
			return
		}
		sh.lines = append(sh.lines, agentapi.OutputLine{Text: sh.partial[i].text, Err: stream == rpc.ToolShellOutputStreamStderr})
		if len(sh.lines) > maxTailLines {
			sh.lines = slices.Delete(sh.lines, 0, len(sh.lines)-maxTailLines)
		}
		sh.partial = slices.Delete(sh.partial, i, i+1)
		text = rest
	}
}

// tail is the newest maxTailLines of the complete lines then the unfinished
// ones, as a new slice; nil without any.
func (sh *shellOutput) tail() []agentapi.OutputLine {
	lines := slices.Clone(sh.lines)
	for _, p := range sh.partial {
		lines = append(lines, agentapi.OutputLine{Text: p.text, Err: p.stream == rpc.ToolShellOutputStreamStderr})
	}
	if len(lines) > maxTailLines {
		lines = lines[len(lines)-maxTailLines:]
	}
	return lines
}
