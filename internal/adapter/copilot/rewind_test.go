package copilot

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type rewindFakeSession struct {
	*fakeSession
	points    *rpc.HistoryListRewindPointsResult
	preview   *rpc.HistoryPreviewRewindResult
	result    *rpc.HistoryRewindResult
	err       error
	previewed []string
	rewinds   []rpc.HistoryRewindRequest
}

func (s *rewindFakeSession) ListRewindPoints(context.Context) (*rpc.HistoryListRewindPointsResult, error) {
	return s.points, nil
}

func (s *rewindFakeSession) PreviewRewind(_ context.Context, event string) (*rpc.HistoryPreviewRewindResult, error) {
	s.previewed = append(s.previewed, event)
	return s.preview, nil
}

func (s *rewindFakeSession) Rewind(_ context.Context, req *rpc.HistoryRewindRequest) (*rpc.HistoryRewindResult, error) {
	s.rewinds = append(s.rewinds, *req)
	return s.result, s.err
}

func rewindConversation(t *testing.T) (*conversation, *rewindFakeSession, *webProvider) {
	t.Helper()
	auto := userMessage("auto", rpc.UserMessageDeliveryIdle, "continue")
	auto.IsAutopilotContinuation = copilot.Bool(true)
	f := &forkClient{fakeClient: &fakeClient{pageSize: 2, journal: []copilot.SessionEvent{
		ev("event-1", userMessage("message-1", rpc.UserMessageDeliveryIdle, "first")),
		ev("idle-1", &rpc.SessionIdleData{}),
		ev("event-2", userMessage("message-2", rpc.UserMessageDeliveryIdle, "second")),
		agentEv("child-root", "child", userMessage("child-message", rpc.UserMessageDeliveryIdle, "child")),
		ev("steer-event", userMessage("steer", rpc.UserMessageDeliverySteering, "steer")),
		ev("auto-event", auto),
		ev("idle-2", &rpc.SessionIdleData{}),
		ev("event-3", userMessage("message-3", rpc.UserMessageDeliveryIdle, "third")),
		ev("idle-3", &rpc.SessionIdleData{}),
	}}}
	p := forkProvider(t, f)
	sess := &rewindFakeSession{
		fakeSession: &fakeSession{id: "native-source"},
		points: &rpc.HistoryListRewindPointsResult{FileChangeTrackingEnabled: true, Points: []rpc.HistoryRewindPoint{
			{EventID: "event-1"}, {EventID: "event-2", FileCount: 99}, {EventID: "auto-event", IsAutopilotContinuation: true}, {EventID: "event-3"},
		}},
		preview: &rpc.HistoryPreviewRewindResult{Available: true, FileCount: 2, Files: []rpc.HistoryRewindFilePreview{
			{Path: "/work/a.txt", ChangeType: rpc.HistoryRewindChangeTypeDeleted, LinesRemoved: 458},
			{Path: "/outside/b.txt", ChangeType: rpc.HistoryRewindChangeTypeModified, LinesAdded: 3, LinesRemoved: 1},
		}},
	}
	return &conversation{p: p, client: f, sess: sess, id: "native-source"}, sess, p
}

func TestNativeRewindPreviewBindsExactSuffix(t *testing.T) {
	c, sess, _ := rewindConversation(t)
	got, err := c.PreviewRewind(context.Background(), "message-2")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserEventID != "event-2" || got.TailEventID != "idle-3" || got.Turns != 2 || !reflect.DeepEqual(got.Discarded, []string{"message-2", "message-3"}) {
		t.Fatalf("boundary = %+v", got)
	}
	if !got.FilesAvailable || got.Files.Files != 2 || got.Files.Additions != 3 || got.Files.Deletions != 459 || got.Files.Entries[0].Kind != "deleted" || !reflect.DeepEqual(sess.previewed, []string{"event-2"}) {
		t.Fatalf("files = %+v previewed %v", got.Files, sess.previewed)
	}
	if len(sess.rewinds) != 0 || len(sess.sent) != 0 {
		t.Fatal("preview mutated the conversation")
	}
	for _, user := range []string{"steer", "auto", "child-message", "event-2", "missing"} {
		if _, err := c.PreviewRewind(context.Background(), user); !errors.Is(err, agentapi.ErrItemNotFound) {
			t.Fatalf("%s = %v", user, err)
		}
	}
	sess.points.FileChangeTrackingEnabled = false
	got, err = c.PreviewRewind(context.Background(), "message-3")
	if err != nil || got.FilesAvailable || got.FilesReason != "file-change-tracking-disabled" || got.Turns != 1 {
		t.Fatalf("untracked = %+v, %v", got, err)
	}
	sess.points.FileChangeTrackingEnabled = true
	busy := rpc.HistoryRewindUnavailableReasonSessionBusy
	sess.preview = &rpc.HistoryPreviewRewindResult{Reason: &busy}
	if _, err := c.PreviewRewind(context.Background(), "message-1"); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("busy preview = %v", err)
	}
	sess.points.UnavailableReason = &busy
	if _, err := c.PreviewRewind(context.Background(), "message-1"); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("busy points = %v", err)
	}
	remote := rpc.HistoryRewindUnavailableReasonUnsupportedRemoteSession
	sess.points.UnavailableReason = &remote
	if _, err := c.PreviewRewind(context.Background(), "message-1"); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("remote = %v", err)
	}
	c.turnRunning = true
	if _, err := c.PreviewRewind(context.Background(), "message-1"); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("running = %v", err)
	}
}

func TestNativeRewindKeepsAllNineOutcomesAndFields(t *testing.T) {
	text := func(s string) *string { return &s }
	count := func(n int64) *int64 { return &n }
	for _, outcome := range []rpc.HistoryRewindOutcome{
		rpc.HistoryRewindOutcomeSuccess, rpc.HistoryRewindOutcomeCheckpointCleanupFailed, rpc.HistoryRewindOutcomeSnapshotPruneFailed,
		rpc.HistoryRewindOutcomeTruncationFailed, rpc.HistoryRewindOutcomeFilesRolledBack, rpc.HistoryRewindOutcomeRollbackIncomplete,
		rpc.HistoryRewindOutcomeSessionBusy, rpc.HistoryRewindOutcomeFileChangeTrackingDisabled, rpc.HistoryRewindOutcomeUnsupportedRemoteSession,
	} {
		c, sess, _ := rewindConversation(t)
		native := &rpc.HistoryRewindResult{Outcome: outcome, RestoredFiles: []string{"/work/a.txt"}, SkippedFiles: []rpc.HistorySkippedFileRestore{{Path: "/work/b.txt", Reason: rpc.HistoryFileRestoreSkipReasonUserModified}}}
		if outcome != rpc.HistoryRewindOutcomeSuccess {
			native.Error = text("detail " + string(outcome))
		}
		if outcome == rpc.HistoryRewindOutcomeSuccess || outcome == rpc.HistoryRewindOutcomeSnapshotPruneFailed {
			native.EventsRemoved = count(0)
		}
		sess.result = native
		got, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversationAndFiles)
		if err != nil || got.Outcome != string(outcome) || (got.Error == nil) != (native.Error == nil) || (got.EventsRemoved == nil) != (native.EventsRemoved == nil) ||
			!reflect.DeepEqual(got.RestoredFiles, []string{"/work/a.txt"}) || !reflect.DeepEqual(got.SkippedFiles, []agentapi.RewindSkip{{Path: "/work/b.txt", Reason: "user-modified"}}) {
			t.Fatalf("%s = %+v, %v", outcome, got, err)
		}
		if len(sess.rewinds) != 1 || sess.rewinds[0].EventID != "event-2" || sess.rewinds[0].Mode != rpc.HistoryRewindModeConversationAndFiles {
			t.Fatalf("request = %+v", sess.rewinds)
		}
	}
}

func TestNativeRewindLostResultIsUncertainAndNeverRepeated(t *testing.T) {
	c, sess, p := rewindConversation(t)
	sess.err = context.DeadlineExceeded
	if _, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversation); !errors.Is(err, agentapi.ErrRewindUncertain) || len(sess.rewinds) != 1 {
		t.Fatalf("lost = %v, requests %d", err, len(sess.rewinds))
	}
	sess.err, sess.result = nil, nil
	if _, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversation); !errors.Is(err, agentapi.ErrRewindUncertain) {
		t.Fatalf("empty = %v", err)
	}
	sess.result = &rpc.HistoryRewindResult{Outcome: "brand-new"}
	if _, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversation); !errors.Is(err, agentapi.ErrRewindUncertain) {
		t.Fatalf("unknown outcome = %v", err)
	}
	many := make([]string, agentapi.MaxRewindResultFiles+3)
	for i := range many {
		many[i] = "/work/" + strings.Repeat("x", i%7+1)
	}
	sess.result = &rpc.HistoryRewindResult{Outcome: rpc.HistoryRewindOutcomeSuccess, RestoredFiles: append(many, "relative")}
	got, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversation)
	if err != nil || len(got.RestoredFiles) != agentapi.MaxRewindResultFiles || got.RestoredOmitted != 4 {
		t.Fatalf("bounded = %d omitted %d, %v", len(got.RestoredFiles), got.RestoredOmitted, err)
	}
	sess.err = &copilot.RPCError{Code: -32601, Message: "Method not found"}
	if _, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversation); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("missing method = %v", err)
	}
	if p.Capabilities().Rewind {
		t.Fatal("unsupported rewind still advertised")
	}
	before := len(sess.rewinds)
	for _, tc := range []struct {
		name string
		set  func()
		want error
	}{
		{"running", func() { c.turnRunning = true }, agentapi.ErrBusy},
		{"closed", func() { c.turnRunning, c.closed = false, true }, agentapi.ErrClosed},
	} {
		tc.set()
		if _, err := c.Rewind(context.Background(), "event-2", agentapi.RewindConversation); !errors.Is(err, tc.want) {
			t.Fatalf("%s = %v", tc.name, err)
		}
	}
	if _, err := c.Rewind(context.Background(), "event-2", "force"); err == nil || len(sess.rewinds) != before {
		t.Fatalf("refusals reached the provider: %v", err)
	}
}

func TestSDKRewindSendsBoundaryAndMode(t *testing.T) {
	rt := startFakeRuntime(t)
	sess := openFakeSDKSession(t, rt)
	rt.set("session.history.rewind", `{"outcome":"truncation-failed","error":"disk","restoredFiles":["/w/a"],"skippedFiles":[{"path":"/w/b","reason":"user-modified"}]}`)
	res, err := sess.Rewind(context.Background(), &rpc.HistoryRewindRequest{EventID: "native-owner", Mode: rpc.HistoryRewindModeConversation})
	if err != nil || res.Outcome != rpc.HistoryRewindOutcomeTruncationFailed || res.EventsRemoved != nil || len(res.SkippedFiles) != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if got := rt.last("session.history.rewind"); got["eventId"] != "native-owner" || got["mode"] != "conversation" || got["sessionId"] != "s-1" {
		t.Fatalf("params = %v", got)
	}
}
