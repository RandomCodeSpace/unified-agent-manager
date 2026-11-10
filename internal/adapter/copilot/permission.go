package copilot

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

func (c *conversation) permissionRequestedLocked(d *rpc.PermissionRequestedData, at time.Time, agentID string) {
	if sa := c.subs.byID[agentID]; agentID != "" && (c.stoppedSubagents[agentID] || sa != nil && sa.Status == agentapi.SubagentCancelled) {
		return
	}
	if old := c.pending[d.RequestID]; old != nil && old.answering {
		return
	}
	title, detail := describePermission(d)
	in := &interaction{
		Interaction: agentapi.Interaction{ID: d.RequestID, Kind: agentapi.InteractionPermission, Title: title, Detail: clip(detail, maxToolText), Intention: permissionIntention(d.PromptRequest), State: agentapi.InteractionPending, Time: at, AgentID: agentID, ToolCallID: permissionToolCallID(d.PermissionRequest), Assisted: assistedReview(d)},
		decisions:   map[string]rpc.PermissionDecision{},
	}
	if d.PermissionRequest != nil {
		in.permissionUse.PermissionKind = string(d.PermissionRequest.Kind())
	}
	if tool := c.tr.tools[in.ToolCallID]; tool != nil {
		in.permissionUse.Tool = tool.Name
		_ = json.Unmarshal([]byte(tool.Input), &in.permissionUse.Args)
	}
	if d.ResolvedByHook != nil && *d.ResolvedByHook {
		in.permissionHookResolved = true
		c.pending[d.RequestID] = in
		return
	}
	add := func(opt agentapi.Option, dec rpc.PermissionDecision) {
		in.Options = append(in.Options, opt)
		in.decisions[opt.ID] = dec
	}
	// A request the managed policy says a person must approve, or one this
	// SDK cannot read, is never marked for automatic approval.
	_, raw := d.PermissionRequest.(*rpc.RawPermissionRequest)
	managed := d.PermissionRequest == nil || raw || d.PermissionRequest.RequiresManagedApproval()
	add(agentapi.Option{ID: "approve_once", Label: "Allow once", AllowOnce: !managed}, &rpc.PermissionDecisionApproveOnce{ApprovedInteractively: copilot.Bool(true)})
	if dec := sessionApproval(d.PromptRequest); dec != nil {
		add(agentapi.Option{ID: "approve_session", Label: "Allow for this session"}, dec)
		key, _ := json.Marshal(dec)
		in.onceKey = string(key)
		// After two owner "Allow once" answers for the same scope, the session
		// option comes first, which the UI makes the primary button.
		if c.approvedOnce[in.onceKey] >= 2 {
			in.Options[0], in.Options[1] = in.Options[1], in.Options[0]
		}
	}
	add(agentapi.Option{ID: "reject", Label: "Deny", Reject: true}, &rpc.PermissionDecisionReject{})
	c.pending[d.RequestID] = in
	c.emitInteractionLocked(in)
}

// permissionCompletedLocked reports a permission the CLI resolved without
// this client (a hook, policy, or the turn ending).
func (c *conversation) permissionCompletedLocked(d *rpc.PermissionCompletedData) func() {
	in := c.pending[d.RequestID]
	if in == nil || in.reply != nil || in.answering {
		return nil
	}
	delete(c.pending, d.RequestID)
	in.State = agentapi.InteractionExpired
	if d.Result != nil {
		in.Resolution = kindText(string(d.Result.Kind()))
		switch d.Result.Kind() {
		case rpc.PermissionResultKindApproved, rpc.PermissionResultKindApprovedForSession, rpc.PermissionResultKindApprovedForLocation:
			in.State = agentapi.InteractionAnswered
		case rpc.PermissionResultKindCancelled, rpc.PermissionResultKindDeniedNoApprovalRuleAndCouldNotRequestFromUser:
		default:
			in.State = agentapi.InteractionRejected
		}
	}
	c.emitInteractionLocked(in)
	// Count native automatic denials only. Owner answers, cancelled requests,
	// and our own Pre-hook refusals never feed the fuse back into itself.
	if c.hooks.Failed != nil && d.Result != nil && in.permissionUse.PermissionKind != "" && (d.DecisionSource == nil || *d.DecisionSource != rpc.PermissionDecisionSourceHumanResponse) {
		switch d.Result.Kind() {
		case rpc.PermissionResultKindDeniedByPermissionRequestHook, rpc.PermissionResultKindDeniedByRules, rpc.PermissionResultKindDeniedByContentExclusionPolicy, rpc.PermissionResultKindDeniedNoApprovalRuleAndCouldNotRequestFromUser:
			use := in.permissionUse
			return func() { c.hooks.Failed(context.Background(), use, string(d.Result.Kind())) }
		}
	}
	return nil
}

// sessionApproval builds the "allow for this session" decision the same way
// the CLI's own prompt does, for the prompt kinds where that is unambiguous.
// URL (origin pattern computed natively), path, hook, workflow and extension
// prompts get no session option.
func sessionApproval(pr rpc.PermissionPromptRequest) rpc.PermissionDecision {
	var approval rpc.PermissionDecisionApproveForSessionApproval
	switch r := pr.(type) {
	case *rpc.PermissionPromptRequestCommands:
		if r.CanOfferSessionApproval && len(r.CommandIdentifiers) > 0 {
			approval = &rpc.PermissionDecisionApproveForSessionApprovalCommands{CommandIdentifiers: r.CommandIdentifiers}
		}
	case *rpc.PermissionPromptRequestWrite:
		if r.CanOfferSessionApproval {
			approval = &rpc.PermissionDecisionApproveForSessionApprovalWrite{}
		}
	case *rpc.PermissionPromptRequestRead:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalRead{}
	case *rpc.PermissionPromptRequestMCP:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalMCP{ServerName: r.ServerName, ToolName: &r.ToolName}
	case *rpc.PermissionPromptRequestMemory:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalMemory{}
	case *rpc.PermissionPromptRequestCustomTool:
		approval = &rpc.PermissionDecisionApproveForSessionApprovalCustomTool{ToolName: r.ToolName}
	}
	if approval == nil {
		return nil
	}
	return &rpc.PermissionDecisionApproveForSession{Approval: approval}
}

func describePermission(d *rpc.PermissionRequestedData) (title, detail string) {
	switch r := d.PromptRequest.(type) {
	case *rpc.PermissionPromptRequestCommands:
		detail = r.FullCommandText
		if r.Warning != nil {
			detail += "\n\nWarning: " + *r.Warning
		}
		return "Run shell command", detail + sandboxBypass(r.RequestSandboxBypass, r.RequestSandboxBypassReason)
	case *rpc.PermissionPromptRequestWrite:
		return "Write file", r.FileName + "\n\n" + r.Diff
	case *rpc.PermissionPromptRequestRead:
		return "Read file", r.Path
	case *rpc.PermissionPromptRequestPath:
		return "Access paths outside the workspace", strings.Join(r.Paths, "\n")
	case *rpc.PermissionPromptRequestURL:
		return "Fetch URL", r.URL + sandboxBypass(r.RequestSandboxBypass, r.RequestSandboxBypassReason)
	case *rpc.PermissionPromptRequestMCP:
		// The display name only labels; approvals keep the server name.
		return "Run MCP tool " + cmp.Or(strings.TrimSpace(deref(r.ServerDisplayName)), r.ServerName) + "/" + r.ToolName, compactJSON(r.Args)
	case *rpc.PermissionPromptRequestCustomTool:
		return "Run tool " + r.ToolName, compactJSON(r.Args)
	case *rpc.PermissionPromptRequestMemory:
		return "Store memory", r.Fact
	case *rpc.PermissionPromptRequestHook:
		detail = compactJSON(r.ToolArgs)
		if r.HookMessage != nil {
			detail = *r.HookMessage + "\n\n" + detail
		}
		return "Confirm " + r.ToolName, detail
	}
	kind := "unknown"
	if d.PermissionRequest != nil {
		kind = string(d.PermissionRequest.Kind())
	}
	return "Permission request: " + kind, compactJSON(d.PermissionRequest)
}

// permissionIntention is the model's stated reason for a shell, write, read
// or URL request, bounded for one line in the permission card.
func permissionIntention(pr rpc.PermissionPromptRequest) string {
	var s string
	switch r := pr.(type) {
	case *rpc.PermissionPromptRequestCommands:
		s = r.Intention
	case *rpc.PermissionPromptRequestWrite:
		s = r.Intention
	case *rpc.PermissionPromptRequestRead:
		s = r.Intention
	case *rpc.PermissionPromptRequestURL:
		s = r.Intention
	}
	return clip(strings.TrimSpace(displaytext.Sanitize(s)), maxIntention)
}

// permissionToolCallID returns the tool call a permission request is for,
// or "" when the request does not say. It is the ToolCallID of the tool
// execution events, which the transcript uses as the tool item's ID. Every
// request kind in SDK 1.0.17 has an optional toolCallId; a kind the SDK
// cannot read keeps its raw JSON, which is read for the same field.
func permissionToolCallID(pr rpc.PermissionRequest) string {
	var id *string
	switch r := pr.(type) {
	case *rpc.PermissionRequestShell:
		id = r.ToolCallID
	case *rpc.PermissionRequestWrite:
		id = r.ToolCallID
	case *rpc.PermissionRequestRead:
		id = r.ToolCallID
	case *rpc.PermissionRequestURL:
		id = r.ToolCallID
	case *rpc.PermissionRequestMCP:
		id = r.ToolCallID
	case *rpc.PermissionRequestCustomTool:
		id = r.ToolCallID
	case *rpc.PermissionRequestMemory:
		id = r.ToolCallID
	case *rpc.PermissionRequestHook:
		id = r.ToolCallID
	case *rpc.PermissionRequestWorkflow:
		id = r.ToolCallID
	case *rpc.PermissionRequestExtensionEnvAccess:
		id = r.ToolCallID
	case *rpc.PermissionRequestExtensionManagement:
		id = r.ToolCallID
	case *rpc.PermissionRequestExtensionPermissionAccess:
		id = r.ToolCallID
	case *rpc.RawPermissionRequest:
		var v struct {
			ToolCallID *string `json:"toolCallId"`
		}
		if json.Unmarshal(r.Raw, &v) == nil {
			id = v.ToolCallID
		}
	}
	if id == nil {
		return ""
	}
	return strings.TrimSpace(*id)
}

func sandboxBypass(requested *bool, reason *string) string {
	if requested == nil || !*requested {
		return ""
	}
	s := "\n\nRequests to run outside the sandbox."
	if reason != nil {
		s += " " + *reason
	}
	return s
}

// kindText turns a CLI result kind such as "denied-interactively-by-user"
// into display text.
func kindText(kind string) string {
	text := strings.ReplaceAll(kind, "-", " ")
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
