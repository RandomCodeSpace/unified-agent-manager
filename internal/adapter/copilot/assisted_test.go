package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

func (s *fakeSession) SetPermissionMode(_ context.Context, req *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
	s.mu.Lock()
	if len(s.permModes) == 0 {
		s.sentAtPermMode = len(s.sent)
	}
	s.permModes = append(s.permModes, req)
	hook := s.permMode
	s.mu.Unlock()
	if hook != nil {
		return hook(req)
	}
	return &rpc.PermissionsSetModeResult{Mode: req.Mode, Success: true}, nil
}

func (s *fakeSession) GetPermissionMode(context.Context) (*rpc.PermissionsGetModeResult, error) {
	s.mu.Lock()
	hook := s.getMode
	mode := rpc.PermissionModeManual
	if n := len(s.permModes); n > 0 {
		mode = s.permModes[n-1].Mode
	}
	s.mu.Unlock()
	if hook != nil {
		return hook()
	}
	return &rpc.PermissionsGetModeResult{Mode: mode}, nil
}

func openAssisted(t *testing.T, fc *fakeClient, model string) (agentapi.Conversation, error) {
	t.Helper()
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p.Open(context.Background(), agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Events: &recSink{}, AssistedApprovalModel: model})
}

func TestOpenAppliesAssistedPermissionsWithTheExplicitReviewer(t *testing.T) {
	fc := &fakeClient{}
	conv, err := openAssisted(t, fc, "gpt-6-luna")
	if err != nil {
		t.Fatal(err)
	}
	fs := fc.sessions[0]
	if len(fs.permModes) != 1 || fs.permModes[0].Mode != rpc.PermissionModeAssisted || fs.permModes[0].AssistedApprovalModel == nil || *fs.permModes[0].AssistedApprovalModel != "gpt-6-luna" || fs.sentAtPermMode != 0 {
		t.Fatalf("set mode = %+v", fs.permModes)
	}
	if !newWebProvider(nil, time.Hour).Capabilities().AssistedPermissions {
		t.Fatal("assisted permissions capability absent")
	}
	if err := conv.(agentapi.AssistedPermissionSetter).SetAssistedPermissions(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if last := fs.permModes[1]; last.Mode != rpc.PermissionModeManual || last.AssistedApprovalModel != nil {
		t.Fatalf("off = %+v", last)
	}

	// Safe and yolo Tasks never touch the runtime's mode.
	plain := &fakeClient{}
	if _, err := openAssisted(t, plain, ""); err != nil || len(plain.sessions[0].permModes) != 0 {
		t.Fatalf("plain open = %v, modes %d", err, len(plain.sessions[0].permModes))
	}
}

func TestOpenFailsWhenTheRuntimeDoesNotApplyAssistedPermissions(t *testing.T) {
	for name, hook := range map[string]func(*rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error){
		"error": func(*rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
			return nil, errors.New("unknown model")
		},
		"unsupported": func(*rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
			return nil, &copilot.RPCError{Code: -32601}
		},
		"other mode": func(*rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
			return &rpc.PermissionsSetModeResult{Mode: rpc.PermissionModeManual, Success: true}, nil
		},
		"not applied": func(req *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
			return &rpc.PermissionsSetModeResult{Mode: req.Mode}, nil
		},
		"no result": func(*rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) { return nil, nil },
	} {
		t.Run(name, func(t *testing.T) {
			fc := &fakeClient{permMode: hook}
			conv, err := openAssisted(t, fc, "gpt-6-luna")
			if err == nil || conv != nil || !strings.Contains(err.Error(), "assisted permissions") {
				t.Fatalf("open = %v, %v", conv, err)
			}
			if fs := fc.sessions[0]; !fs.disconnected || len(fs.sent) != 0 {
				t.Fatalf("refused session stayed open or was used: disconnected %v", fs.disconnected)
			}
		})
	}
}

func TestPermissionRequestCarriesOnlyAnAssistedModeReview(t *testing.T) {
	h := openWeb(t)
	assisted, manual := rpc.PermissionModeAssisted, rpc.PermissionModeManual
	review := &rpc.PermissionAssistedApproval{Recommendation: rpc.AssistedApprovalRecommendationApprove, Model: copilot.String("gpt-6-luna"), Reason: copilot.String("  writes \x1b[31mone file  ")}
	write := func(id string, mode *rpc.PermissionMode) *rpc.PermissionRequestedData {
		return &rpc.PermissionRequestedData{RequestID: id, PermissionMode: mode, PermissionRequest: &rpc.PermissionRequestWrite{FileName: "a.txt"}, PromptRequest: &rpc.PermissionPromptRequestWrite{FileName: "a.txt", AssistedApproval: review}}
	}
	h.fs.onEvent(ev("e1", write("p1", &assisted)))
	h.fs.onEvent(ev("e2", write("p2", &manual)))
	h.fs.onEvent(ev("e3", write("p3", nil)))
	got := map[string]agentapi.AssistedReview{}
	for _, e := range h.sink.all() {
		if e.Kind == agentapi.EventInteraction {
			got[e.Interaction.ID] = e.Interaction.Assisted
		}
	}
	if r := got["p1"]; r.Recommendation != "approve" || r.Model != "gpt-6-luna" || r.Reason != "writes one file" {
		t.Fatalf("assisted review = %+v", r)
	}
	if got["p2"] != (agentapi.AssistedReview{}) || got["p3"] != (agentapi.AssistedReview{}) {
		t.Fatalf("review outside assisted mode = %+v %+v", got["p2"], got["p3"])
	}
}

func TestAssistedPermissionsOnReadsTheRuntimeModeBack(t *testing.T) {
	fc := &fakeClient{}
	conv, err := openAssisted(t, fc, "gpt-6-luna")
	if err != nil {
		t.Fatal(err)
	}
	reader := conv.(agentapi.AssistedPermissionSetter)
	fs := fc.sessions[0]
	for name, tc := range map[string]struct {
		res     *rpc.PermissionsGetModeResult
		err     error
		on      bool
		failure string
	}{
		"assisted":    {res: &rpc.PermissionsGetModeResult{Mode: rpc.PermissionModeAssisted}, on: true},
		"manual":      {res: &rpc.PermissionsGetModeResult{Mode: rpc.PermissionModeManual}},
		"allow-all":   {res: &rpc.PermissionsGetModeResult{Mode: rpc.PermissionModeAllowAll}, failure: "allow-all"},
		"no result":   {failure: "no permission mode"},
		"error":       {err: errors.New("down"), failure: "get permission mode"},
		"unsupported": {err: &copilot.RPCError{Code: -32601}, failure: agentapi.ErrUnsupported.Error()},
	} {
		t.Run(name, func(t *testing.T) {
			fs.mu.Lock()
			fs.getMode = func() (*rpc.PermissionsGetModeResult, error) { return tc.res, tc.err }
			fs.mu.Unlock()
			on, err := reader.AssistedPermissionsOn(context.Background())
			if on != tc.on || (err == nil) != (tc.failure == "") || err != nil && !strings.Contains(err.Error(), tc.failure) {
				t.Fatalf("read back = %v, %v", on, err)
			}
		})
	}
	if err := conv.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.AssistedPermissionsOn(context.Background()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("closed read back = %v", err)
	}
}
