package copilot

import (
	"context"
	"fmt"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestNativeTurnChangesExactOwnerAndAutopilotSuffix(t *testing.T) {
	rt := startFakeRuntime(t)
	sess := openFakeSDKSession(t, rt)
	c := &conversation{sess: sess, turnChangeUser: "visible-user", turnChangeEvent: "native-owner"}
	rt.set("session.history.listRewindPoints", `{"fileChangeTrackingEnabled":true,"points":[{"eventId":"old-turn","fileCount":999},{"eventId":"native-owner","fileCount":999,"linesAdded":17,"linesRemoved":9},{"eventId":"auto","isAutopilotContinuation":true,"fileCount":999}]}`)
	rt.set("session.history.previewRewind", `{"available":true,"fileCount":1,"files":[{"path":"/work/a.txt","changeType":"modified","linesAdded":3,"linesRemoved":2}]}`)
	got, err := c.TurnChanges(context.Background(), "visible-user")
	if err != nil || got.Status != "available" || got.EventID != "native-owner" || got.Files != 1 || got.Additions != 3 || got.Deletions != 2 {
		t.Fatalf("owner snapshot=%+v,%v", got, err)
	}
	if rt.last("session.history.previewRewind")["eventId"] != "native-owner" {
		t.Fatal("preview guessed exposed item or turn number")
	}
	// A later ordinary owner makes the same native preview ambiguous. Never use
	// current suffix data to manufacture historical counts.
	rt.set("session.history.listRewindPoints", `{"fileChangeTrackingEnabled":true,"points":[{"eventId":"native-owner"},{"eventId":"later-owner"}]}`)
	got, err = c.TurnChanges(context.Background(), "visible-user")
	if err != nil || got.Status != "unknown" || got.Files != 0 || len(got.Entries) != 0 {
		t.Fatalf("historical suffix=%+v,%v", got, err)
	}
}

func TestNativeTurnChangesMissingWrongBusyUnsupportedAndClosed(t *testing.T) {
	rt := startFakeRuntime(t)
	sess := openFakeSDKSession(t, rt)
	c := &conversation{sess: sess, turnChangeUser: "visible", turnChangeEvent: "native"}
	for _, tc := range []struct{ name, points, status string }{
		{"missing", `{"fileChangeTrackingEnabled":true,"points":[]}`, "unknown"},
		{"autopilot boundary", `{"fileChangeTrackingEnabled":true,"points":[{"eventId":"native","isAutopilotContinuation":true}]}`, "unknown"},
		{"duplicate boundary", `{"fileChangeTrackingEnabled":true,"points":[{"eventId":"native"},{"eventId":"native"}]}`, "unknown"},
		{"busy", `{"fileChangeTrackingEnabled":true,"unavailableReason":"session-busy","points":[]}`, "busy"},
		{"untracked", `{"fileChangeTrackingEnabled":false,"points":[{"eventId":"native"}]}`, "unsupported"},
		{"remote", `{"fileChangeTrackingEnabled":false,"unavailableReason":"unsupported-remote-session","points":[]}`, "unsupported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt.set("session.history.listRewindPoints", tc.points)
			got, err := c.TurnChanges(context.Background(), "visible")
			if err != nil || got.Status != tc.status {
				t.Fatalf("facts=%+v,%v", got, err)
			}
		})
	}
	for _, user := range []string{"", "wrong"} {
		got, err := c.TurnChanges(context.Background(), user)
		if err != nil || got.Status != "unknown" {
			t.Fatalf("wrong owner=%+v,%v", got, err)
		}
	}
	c.closed = true
	got, err := c.TurnChanges(context.Background(), "visible")
	if err != nil || got.Status != "unknown" {
		t.Fatalf("closed=%+v,%v", got, err)
	}
}

func TestNativeTurnPreviewBoundsAndMalformedCounts(t *testing.T) {
	files := make([]rpc.HistoryRewindFilePreview, 35)
	for i := range files {
		files[i] = rpc.HistoryRewindFilePreview{Path: fmt.Sprintf("/work/file-%02d", i), ChangeType: "modified", LinesAdded: 1, LinesRemoved: 2}
	}
	got := nativeTurnPreview("event", &rpc.HistoryPreviewRewindResult{Available: true, FileCount: 35, Files: files})
	if got.Status != "available" || got.Files != 35 || got.Additions != 35 || got.Deletions != 70 || got.Omitted != 3 || len(got.Entries) != agentapi.MaxTurnChangeFiles {
		t.Fatalf("bounded=%+v", got)
	}
	for _, tc := range []struct {
		name  string
		count int64
		files []rpc.HistoryRewindFilePreview
	}{
		{"file count mismatch", 2, files[:1]},
		{"duplicate", 2, []rpc.HistoryRewindFilePreview{files[0], files[0]}},
		{"negative", 1, []rpc.HistoryRewindFilePreview{{Path: "/a", LinesAdded: -1}}},
		{"overflow", 1, []rpc.HistoryRewindFilePreview{{Path: "/a", LinesAdded: agentapi.MaxTurnChangeCount + 1}}},
		{"relative", 1, []rpc.HistoryRewindFilePreview{{Path: "a"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := nativeTurnPreview("e", &rpc.HistoryPreviewRewindResult{Available: true, FileCount: tc.count, Files: tc.files}); got.Status != "unknown" {
				t.Fatalf("malformed=%+v", got)
			}
		})
	}
	if got := nativeTurnPreview("e", &rpc.HistoryPreviewRewindResult{Available: true, Files: []rpc.HistoryRewindFilePreview{}}); got.Status != "available" || got.Files != 0 {
		t.Fatalf("proved empty=%+v", got)
	}
}

func TestNativeTurnBoundaryDoesNotFollowSteerAutopilotOrSubagent(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	visible := "visible"
	h.fs.onEvent(ev("owner", &rpc.UserMessageData{Content: "owned", MessageID: &visible}))
	steering, yes := rpc.UserMessageDeliverySteering, true
	for _, e := range []rpc.SessionEvent{
		ev("steer", &rpc.UserMessageData{Content: "steer", Delivery: &steering}),
		ev("auto", &rpc.UserMessageData{Content: "auto", IsAutopilotContinuation: &yes}),
	} {
		h.fs.onEvent(e)
	}
	sub := ev("child", &rpc.UserMessageData{Content: "child"})
	child := "child-agent"
	sub.AgentID = &child
	h.fs.onEvent(sub)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.turnChangeUser != "visible" || c.turnChangeEvent != "owner" {
		t.Fatalf("owner replaced: %s/%s", c.turnChangeUser, c.turnChangeEvent)
	}
}
