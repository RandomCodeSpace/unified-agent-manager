package copilot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

type reloadSession struct {
	*fakeSession
	result *rpc.CustomizationsReloadResult
	err    error
	calls  int
}

func (s *reloadSession) ReloadCustomizations(context.Context) (*rpc.CustomizationsReloadResult, error) {
	s.calls++
	return s.result, s.err
}

func TestMCPReloadCustomizationsReportsFailuresWithoutDisconnect(t *testing.T) {
	for _, tc := range []struct {
		name        string
		result      *rpc.CustomizationsReloadResult
		err         error
		want        string
		unsupported bool
	}{
		{name: "success", result: &rpc.CustomizationsReloadResult{}},
		{name: "warnings", result: &rpc.CustomizationsReloadResult{Warnings: []string{"optional skill omitted"}}},
		{name: "partial component errors", result: &rpc.CustomizationsReloadResult{Errors: []string{"MCP could not reload"}}, want: "MCP could not reload"},
		{name: "failed outcome", result: &rpc.CustomizationsReloadResult{Outcomes: []rpc.CustomizationReloadOutcome{{Subsystem: "mcp", Status: "failed"}}}, want: "mcp"},
		{name: "method absent", err: &copilot.RPCError{Code: -32601, Message: "Method not found"}, unsupported: true},
		{name: "wrapped method absent", err: fmt.Errorf("RPC: %w", &copilot.RPCError{Code: -32601, Message: "Method not found"}), unsupported: true},
		{name: "ordinary RPC failure", err: &copilot.RPCError{Code: -32000, Message: "Method not found in MCP config"}, want: "Method not found in MCP config"},
		{name: "untyped lookalike", err: errors.New("Method not found"), want: "Method not found"},
		{name: "nil result", want: "empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := openWeb(t)
			c := h.conv.(*conversation)
			fs := &reloadSession{fakeSession: h.fs, result: tc.result, err: tc.err}
			c.sess = fs
			r, ok := any(c).(interface{ ReloadCustomizations(context.Context) error })
			if !ok {
				t.Fatal("conversation does not support customization reload")
			}
			err := r.ReloadCustomizations(context.Background())
			if errors.Is(err, agentapi.ErrUnsupported) != tc.unsupported || (tc.want == "" && !tc.unsupported && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
				t.Fatalf("reload=%v want=%q unsupported=%t", err, tc.want, tc.unsupported)
			}
			if fs.calls != 1 || fs.disconnected || len(h.fc.resume) != 0 {
				t.Fatalf("calls=%d disconnected=%t resumes=%d", fs.calls, fs.disconnected, len(h.fc.resume))
			}
		})
	}
}

func TestMCPReloadCustomizationsReprovesHostToolsOnTheNextTurn(t *testing.T) {
	for _, tc := range []struct {
		name        string
		err         error
		unsupported bool
	}{
		{name: "success"},
		{name: "partial failure", err: errors.New("MCP failed after skills reloaded")},
		{name: "unsupported", err: &copilot.RPCError{Code: -32601, Message: "Method not found"}, unsupported: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, fs, tools := openProved(t, &fakeClient{})
			if !toolsAnswer(t, tools, "before") {
				t.Fatal("initial host tools are not proven")
			}
			c := conv.(*conversation)
			c.sess = &reloadSession{fakeSession: fs, result: &rpc.CustomizationsReloadResult{}, err: tc.err}
			_ = c.ReloadCustomizations(context.Background())
			if ready := toolsAnswer(t, tools, "after reload"); ready != tc.unsupported {
				t.Fatalf("host tools ready=%t after reload, unsupported=%t", ready, tc.unsupported)
			}
			if err := c.Send(context.Background(), agentapi.Prompt{Text: "next turn"}); err != nil || !toolsAnswer(t, tools, "after next turn") {
				t.Fatalf("next turn did not reprove host tools: %v", err)
			}
		})
	}
}

func TestMCPReloadCustomizationsRefusesClosedAndUnsupportedSessions(t *testing.T) {
	h := openWeb(t)
	c := h.conv.(*conversation)
	if err := c.ReloadCustomizations(context.Background()); !errors.Is(err, agentapi.ErrUnsupported) {
		t.Fatalf("missing session capability = %v", err)
	}
	fs := &reloadSession{fakeSession: h.fs, result: &rpc.CustomizationsReloadResult{}}
	c.sess = fs
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.ReloadCustomizations(context.Background()); !errors.Is(err, agentapi.ErrClosed) || fs.calls != 0 {
		t.Fatalf("closed reload=%v calls=%d", err, fs.calls)
	}
}
