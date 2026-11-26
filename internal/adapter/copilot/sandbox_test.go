package copilot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/microsoft/copilot-sdk/go/v2/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// sandboxFake adds the native sandbox state to a fake session; the read
// waits for release when it is set.
type sandboxFake struct {
	*fakeSession
	state   *rpc.SessionSandboxGetStateResult
	err     error
	release chan struct{}
}

func (s *sandboxFake) SandboxState(ctx context.Context) (*rpc.SessionSandboxGetStateResult, error) {
	if s.release != nil {
		<-s.release
	}
	return s.state, s.err
}

func openSandboxed(t *testing.T, fake *sandboxFake) (*sandboxFake, *recSink) {
	t.Helper()
	fc := &fakeClient{}
	fc.wrap = func(s *fakeSession) sdkSession {
		fake.fakeSession = s
		return fake
	}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	sink := &recSink{}
	if _, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Events: sink}); err != nil {
		t.Fatal(err)
	}
	return fake, sink
}

func sandboxEvents(sink *recSink) []*agentapi.SandboxState {
	var out []*agentapi.SandboxState
	for _, e := range sink.all() {
		if e.Kind == agentapi.EventSandbox {
			out = append(out, e.Sandbox)
		}
	}
	return out
}

func sandboxResult(revision int64, enabled bool, source string) *rpc.SessionSandboxGetStateResult {
	return &rpc.SessionSandboxGetStateResult{Config: map[string]any{"enabled": enabled}, ConfigSource: source, Incarnation: 1, Revision: revision}
}

func TestWebSandboxInitialRead(t *testing.T) {
	_, sink := openSandboxed(t, &sandboxFake{state: sandboxResult(1, true, "user_enabled")})
	waitFor(t, "the sandbox state", func() bool { return len(sandboxEvents(sink)) == 1 })
	got := sandboxEvents(sink)[0]
	if got == nil || *got != (agentapi.SandboxState{Enabled: true, Source: "user_enabled"}) {
		t.Fatalf("sandbox = %+v", got)
	}
}

func TestWebSandboxNeverConfiguredIsOff(t *testing.T) {
	// The probe's shape on CLI 1.0.116 with no sandbox configured.
	_, sink := openSandboxed(t, &sandboxFake{state: sandboxResult(1, false, "never_configured")})
	waitFor(t, "the sandbox state", func() bool { return len(sandboxEvents(sink)) == 1 })
	if got := sandboxEvents(sink)[0]; got == nil || got.Enabled || got.OffForThread {
		t.Fatalf("sandbox = %+v", got)
	}
}

func TestWebSandboxReadErrorIsUnknown(t *testing.T) {
	_, sink := openSandboxed(t, &sandboxFake{err: errors.New("method not found")})
	waitFor(t, "the sandbox state", func() bool { return len(sandboxEvents(sink)) == 1 })
	if got := sandboxEvents(sink)[0]; got != nil {
		t.Fatalf("sandbox = %+v, want unknown", got)
	}
}

func TestWebSandboxNewerRevisionWinsAndStaleIsIgnored(t *testing.T) {
	release := make(chan struct{})
	sf, sink := openSandboxed(t, &sandboxFake{state: sandboxResult(1, true, "user_enabled"), release: release})
	off := false
	sf.onEvent(ev("sb2", &rpc.SessionSandboxStateChangedData{Config: map[string]any{"enabled": true}, ConfigSource: "user_enabled", Incarnation: 1, Revision: 2, SessionOverride: &off}))
	// An older change and the read that started before revision 2 never publish.
	sf.onEvent(ev("sb1", &rpc.SessionSandboxStateChangedData{Config: map[string]any{"enabled": true}, Incarnation: 1, Revision: 1}))
	close(release)
	time.Sleep(20 * time.Millisecond)
	got := sandboxEvents(sink)
	if len(got) != 1 || got[0] == nil || !got[0].OffForThread {
		t.Fatalf("sandbox events = %+v, want only revision 2", got)
	}
	sf.onEvent(ev("sb3", &rpc.SessionSandboxStateChangedData{Config: map[string]any{"enabled": true}, ConfigSource: "repository_policy", Incarnation: 1, Revision: 3}))
	got = sandboxEvents(sink)
	if len(got) != 2 || got[1] == nil || got[1].OffForThread || got[1].Source != "repository_policy" {
		t.Fatalf("sandbox events = %+v, want revision 3 last", got)
	}
}
