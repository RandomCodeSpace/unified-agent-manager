package copilot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

// maxAssistedReason bounds the reviewer's reason a permission request shows.
const maxAssistedReason = 1000

type permissionModeSession interface {
	SetPermissionMode(ctx context.Context, req *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error)
	GetPermissionMode(ctx context.Context) (*rpc.PermissionsGetModeResult, error)
}

func (a sdkSessionAdapter) SetPermissionMode(ctx context.Context, req *rpc.PermissionsSetModeRequest) (*rpc.PermissionsSetModeResult, error) {
	return a.s.RPC.Permissions.SetMode(ctx, req)
}

func (a sdkSessionAdapter) GetPermissionMode(ctx context.Context) (*rpc.PermissionsGetModeResult, error) {
	return a.s.RPC.Permissions.GetMode(ctx)
}

// SetAssistedPermissions sets session.permissions.setMode to assisted with
// approvalModel as the explicit reviewer, never the runtime's default, or
// to manual when approvalModel is "". The returned post-mutation mode must
// be the one asked for.
func (c *conversation) SetAssistedPermissions(ctx context.Context, approvalModel string) error {
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	setter, ok := c.sess.(permissionModeSession)
	if !ok {
		return agentapi.ErrUnsupported
	}
	req := &rpc.PermissionsSetModeRequest{Mode: rpc.PermissionModeManual}
	if approvalModel != "" {
		req.Mode, req.AssistedApprovalModel = rpc.PermissionModeAssisted, copilot.String(approvalModel)
	}
	res, err := setter.SetPermissionMode(ctx, req)
	if err != nil {
		var rpcErr *copilot.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return agentapi.ErrUnsupported
		}
		return fmt.Errorf("set permission mode: %s", errText(err))
	}
	if res == nil || !res.Success || res.Mode != req.Mode {
		got := ""
		if res != nil {
			got = string(res.Mode)
		}
		return fmt.Errorf("the runtime did not apply permission mode %s (reports %q)", req.Mode, got)
	}
	return nil
}

// AssistedPermissionsOn reads session.permissions.getMode back: true for
// assisted, false for manual. Any other mode is an error: UAM never sets it.
func (c *conversation) AssistedPermissionsOn(ctx context.Context) (bool, error) {
	if c.isClosed() {
		return false, agentapi.ErrClosed
	}
	getter, ok := c.sess.(permissionModeSession)
	if !ok {
		return false, agentapi.ErrUnsupported
	}
	res, err := getter.GetPermissionMode(ctx)
	if err != nil {
		var rpcErr *copilot.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return false, agentapi.ErrUnsupported
		}
		return false, fmt.Errorf("get permission mode: %s", errText(err))
	}
	switch {
	case res == nil:
		return false, errors.New("the runtime reported no permission mode")
	case res.Mode == rpc.PermissionModeAssisted:
		return true, nil
	case res.Mode == rpc.PermissionModeManual:
		return false, nil
	}
	return false, fmt.Errorf("the runtime reports permission mode %q", res.Mode)
}

// assistedReview returns the assisted review of a permission request the
// runtime evaluated in assisted mode, else the zero review.
func assistedReview(d *rpc.PermissionRequestedData) agentapi.AssistedReview {
	if d.PermissionMode == nil || *d.PermissionMode != rpc.PermissionModeAssisted {
		return agentapi.AssistedReview{}
	}
	var a *rpc.PermissionAssistedApproval
	switch r := d.PromptRequest.(type) {
	case *rpc.PermissionPromptRequestCommands:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestCustomTool:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestExtensionEnvAccess:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestExtensionManagement:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestExtensionPermissionAccess:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestHook:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestMCP:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestMemory:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestPath:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestRead:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestURL:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestWorkflow:
		a = r.AssistedApproval
	case *rpc.PermissionPromptRequestWrite:
		a = r.AssistedApproval
	}
	if a == nil || a.Recommendation == "" {
		return agentapi.AssistedReview{}
	}
	return agentapi.AssistedReview{
		Recommendation: clip(displaytext.Sanitize(string(a.Recommendation)), maxAssistedReason),
		Model:          clip(strings.TrimSpace(displaytext.Sanitize(deref(a.Model))), maxAssistedReason),
		Reason:         clip(strings.TrimSpace(displaytext.Sanitize(deref(a.Reason))), maxAssistedReason),
	}
}
