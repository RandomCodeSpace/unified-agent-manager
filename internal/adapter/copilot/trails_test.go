package copilot

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestPreToolUseTrailContext(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	var got agentapi.ToolUse
	c.hooks.Pre = func(_ context.Context, use agentapi.ToolUse) agentapi.ToolVerdict {
		got = use
		return agentapi.ToolVerdict{Context: "uam trail: sibling"}
	}
	for _, tc := range []struct {
		tool string
		args any
		want map[string]any
	}{
		{"edit", map[string]any{"path": "auth.go"}, map[string]any{"path": "auth.go"}},
		{"apply_patch", "*** Update File: auth.go", map[string]any{"patch": "*** Update File: auth.go"}},
	} {
		out, err := c.preToolUse(copilot.PreToolUseHookInput{ToolName: tc.tool, ToolArgs: tc.args, WorkingDirectory: "/repo"}, copilot.HookInvocation{})
		if err != nil || out == nil || out.AdditionalContext != "uam trail: sibling" || out.ModifiedArgs != nil || out.PermissionDecision != "" {
			t.Fatalf("hook=%+v error=%v", out, err)
		}
		if got.Tool != tc.tool || got.Workdir != "/repo" || !reflect.DeepEqual(got.Args, tc.want) {
			t.Fatalf("use=%+v", got)
		}
	}
	if len(h.fs.events) != 2 {
		t.Fatalf("recorded notices=%d", len(h.fs.events))
	}
	for _, event := range h.fs.events {
		info, ok := event.Data.(*rpc.SessionInfoData)
		if !ok || info.Message != "uam trail: sibling" {
			t.Fatalf("recorded event=%+v", event)
		}
	}
}

func TestTrailNoticeReplaysFromJournal(t *testing.T) {
	const note = `uam trail: Task "Fix login" edited auth.go 1 min ago and is still running. Re-read it first.`
	event := ev("trail-notice", &rpc.SessionInfoData{InfoType: "notification", Message: note})
	live, ok := newTranscript().item(event)
	if !ok || live.Kind != agentapi.ItemNotice || live.Text != note {
		t.Fatalf("live=%+v", live)
	}
	h := openWeb(t)
	h.fs.onEvent(event)
	found := false
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventItem && e.Item.ID == event.ID {
			found = true
			if !reflect.DeepEqual(*e.Item, live) {
				t.Fatalf("live event=%+v", e.Item)
			}
		}
	}
	if !found {
		t.Fatal("live notification did not emit notice")
	}
	fc := &fakeClient{journal: []copilot.SessionEvent{event}}
	p := readerProvider(fc)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	history, err := p.ReadHistory(t.Context(), agentapi.ReadRequest{ConversationID: "trail"})
	if err != nil || len(history.Items) != 1 || !reflect.DeepEqual(history.Items[0], live) {
		t.Fatalf("replay=%+v error=%v", history, err)
	}
}

type failedTrailLog struct{ sdkSession }

func (failedTrailLog) Log(context.Context, string) error {
	return errors.New("notification unavailable")
}

func TestTrailContextSurvivesLogFailure(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	c.sess = failedTrailLog{c.sess}
	c.hooks.Pre = func(context.Context, agentapi.ToolUse) agentapi.ToolVerdict {
		return agentapi.ToolVerdict{Context: "uam trail: sibling"}
	}
	out, err := c.preToolUse(copilot.PreToolUseHookInput{ToolName: "edit"}, copilot.HookInvocation{})
	if err != nil || out == nil || out.AdditionalContext != "uam trail: sibling" {
		t.Fatalf("output=%+v error=%v", out, err)
	}
}
