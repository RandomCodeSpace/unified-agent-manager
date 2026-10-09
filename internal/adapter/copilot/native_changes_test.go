package copilot

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func TestNativeChangesCaptureBeforeCreateAndPreserveOnResume(t *testing.T) {
	h := openWeb(t)
	cfg := h.fc.create[0]
	if cfg.EnableFileChangeTracking == nil || !*cfg.EnableFileChangeTracking {
		t.Fatal("new Task did not request capture before its first prompt")
	}
	if !h.p.Capabilities().SessionDiff {
		t.Fatal("provider did not advertise the implemented session diff")
	}
	_, err := h.p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-2", ConversationID: "recorded", Workdir: "/work", Events: h.sink})
	if err != nil {
		t.Fatal(err)
	}
	resume := h.fc.resume[0]
	if resume.EnableFileChangeTracking == nil || !*resume.EnableFileChangeTracking {
		t.Fatal("resume did not preserve a valid native capture baseline")
	}
}

func TestNativeChangesReadExactSessionPatches(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	rt.set("session.workspaces.diff", `{"requestedMode":"session","mode":"session","isFallback":false,"changes":[{"path":"src/main.go","changeType":"modified","diff":"diff --git a/src/main.go b/src/main.go\n--- a/src/main.go\n+++ b/src/main.go\n@@ -1 +1 @@\n-old\n+new\n"}]}`)
	c := &conversation{sess: s}
	files, err := c.Diff(context.Background())
	if err != nil || len(files) != 1 || files[0].Path != "src/main.go" || files[0].Status != "modified" || files[0].Patch == "" {
		t.Fatalf("native diff = %+v, %v", files, err)
	}
	if req := rt.last("session.workspaces.diff"); req["sessionId"] != "s-1" || req["mode"] != "session" {
		t.Fatalf("native diff request = %v", req)
	}
}

func TestNativeChangesRejectWorkspaceFallbackAndBusyIsTransient(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	c := &conversation{sess: s}
	for _, tc := range []struct {
		name, result string
		target       error
	}{
		{"busy", `{"requestedMode":"session","mode":"unstaged","isFallback":true,"unavailableReason":"session-busy","changes":[{"path":"owner.txt","changeType":"modified","diff":"owner patch"}]}`, agentapi.ErrBusy},
		{"disabled", `{"requestedMode":"session","mode":"unstaged","isFallback":true,"unavailableReason":"file-change-tracking-disabled","changes":[{"path":"owner.txt","changeType":"modified","diff":"owner patch"}]}`, agentapi.ErrUnsupported},
		{"unexpected mode", `{"requestedMode":"session","mode":"unstaged","isFallback":false,"changes":[]}`, agentapi.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt.set("session.workspaces.diff", tc.result)
			files, err := c.Diff(context.Background())
			if len(files) != 0 || !errors.Is(err, tc.target) {
				t.Fatalf("fallback = %+v, %v", files, err)
			}
		})
	}
	rt.set("session.workspaces.diff", `{"requestedMode":"session","mode":"session","isFallback":false,"changes":[]}`)
	files, err := c.Diff(context.Background())
	if err != nil || len(files) != 0 {
		t.Fatalf("settled read = %+v, %v", files, err)
	}
}

func TestNativeChangesPreserveRenameBinaryTruncationAndUnknownDetails(t *testing.T) {
	rt := startFakeRuntime(t)
	s := openFakeSDKSession(t, rt)
	rt.set("session.workspaces.diff", `{"requestedMode":"session","mode":"session","isFallback":false,"changes":[{"path":"new.txt","oldPath":"old.txt","changeType":"renamed","diff":"diff --git a/old.txt b/new.txt\nsimilarity index 100%\nrename from old.txt\nrename to new.txt\n"},{"path":"image.bin","changeType":"modified","diff":"diff --git a/image.bin b/image.bin\nBinary files a/image.bin and b/image.bin differ\n"},{"path":"large.txt","changeType":"modified","isTruncated":true,"diff":""},{"path":"unknown.txt","changeType":"modified","diff":"unrecognized provider detail"}]}`)
	files, err := (&conversation{sess: s}).Diff(context.Background())
	if err != nil || len(files) != 4 {
		t.Fatalf("native metadata = %+v, %v", files, err)
	}
	if files[0].OldPath != "old.txt" || files[0].CountsUnknown {
		t.Fatalf("rename = %+v", files[0])
	}
	if !files[1].Binary || !files[1].CountsUnknown {
		t.Fatalf("binary = %+v", files[1])
	}
	if !files[2].Truncated || !files[2].CountsUnknown || files[2].Patch != "" {
		t.Fatalf("truncated = %+v", files[2])
	}
	if !files[3].CountsUnknown {
		t.Fatalf("unknown detail claimed zero counts: %+v", files[3])
	}
}

type nativeErrorSession struct {
	sdkSession
	err error
}

func (s nativeErrorSession) WorkspaceDiff(context.Context) (*rpc.WorkspaceDiffResult, error) {
	return nil, s.err
}

func TestNativeChangesMissingMethodIsUnsupportedButTransportFailureIsNot(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{"missing RPC", &copilot.RPCError{Code: -32601, Message: "missing"}, true},
		{"older CLI", &copilot.RPCError{Code: -32000, Message: "Unhandled method session.workspaces.diff"}, true},
		{"transport", errors.New("connection lost: method not found in transport log"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &conversation{sess: nativeErrorSession{err: tc.err}}
			_, err := c.Diff(context.Background())
			if errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported {
				t.Fatalf("native error = %v", err)
			}
		})
	}
}

func TestNativeChangesExcludeOnlyTheExactScratchPlan(t *testing.T) {
	const plan = "/home/u/.copilot/session-state/s-1/plan.md"
	diff := func(paths ...string) string {
		var b strings.Builder
		b.WriteString(`{"requestedMode":"session","mode":"session","isFallback":false,"changes":[`)
		for i, p := range paths {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"path":%q,"changeType":"modified","diff":"--- a/x\n+++ b/x\n@@ -0,0 +1 @@\n+x\n"}`, p)
		}
		b.WriteString(`]}`)
		return b.String()
	}
	workspace := func(cwd, gitRoot string) string {
		ws := `"id":"w"`
		if cwd != "" {
			ws += fmt.Sprintf(`,"cwd":%q`, cwd)
		}
		if gitRoot != "" {
			ws += fmt.Sprintf(`,"git_root":%q`, gitRoot)
		}
		return `{"path":"/home/u/.copilot/session-state/s-1","workspace":{` + ws + `}}`
	}
	ordinary := []string{"docs/plan.md", "plan.md", "/elsewhere/plan.md", "../other/plan.md"}
	for _, tc := range []struct {
		name, plan, workspace string
		failLookup            bool
		paths, want           []string
		lookups               int
	}{
		{"absolute scratch needs no lookup", plan, "", false, []string{plan, "/work/docs/plan.md"}, []string{"/work/docs/plan.md"}, 0},
		{"relative scratch under the working directory", plan, workspace("/work", "/work"), false,
			append([]string{"../home/u/.copilot/session-state/s-1/plan.md"}, ordinary...), ordinary, 1},
		{"no Git root keeps the working directory", plan, workspace("/work", ""), false,
			append([]string{"../home/u/.copilot/session-state/s-1/plan.md"}, ordinary...), ordinary, 1},
		{"unknown working directory preserves", plan, workspace("", "/work"), false,
			[]string{"../home/u/.copilot/session-state/s-1/plan.md", "docs/plan.md"}, []string{"../home/u/.copilot/session-state/s-1/plan.md", "docs/plan.md"}, 1},
		{"ambiguous root preserves", plan, workspace("/work/sub", "/work"), false,
			[]string{"../../home/u/.copilot/session-state/s-1/plan.md", "docs/plan.md"}, []string{"../../home/u/.copilot/session-state/s-1/plan.md", "docs/plan.md"}, 1},
		{"failed lookup preserves", plan, "", true,
			[]string{"../home/u/.copilot/session-state/s-1/plan.md"}, []string{"../home/u/.copilot/session-state/s-1/plan.md"}, 1},
		{"unknown plan keeps everything", "", "", false, []string{plan, "docs/plan.md"}, []string{plan, "docs/plan.md"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := startFakeRuntime(t)
			s := openFakeSDKSession(t, rt)
			rt.set("session.workspaces.diff", diff(tc.paths...))
			if tc.failLookup {
				rt.fail("session.workspaces.getWorkspace", "unavailable")
			} else if tc.workspace != "" {
				rt.set("session.workspaces.getWorkspace", tc.workspace)
			}
			files, err := (&conversation{sess: s, planPath: tc.plan}).Diff(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(files))
			for _, f := range files {
				got = append(got, f.Path)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("native paths = %q, want %q", got, tc.want)
			}
			rt.mu.Lock()
			lookups := len(rt.requests["session.workspaces.getWorkspace"])
			rt.mu.Unlock()
			if lookups != tc.lookups {
				t.Fatalf("workspace lookups = %d, want %d", lookups, tc.lookups)
			}
		})
	}
}
