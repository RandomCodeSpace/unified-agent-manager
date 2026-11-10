package copilot

import (
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// transcript maps session events to transcript items. It keeps running tool
// calls by id so start, live output and final results upsert one item.
type transcript struct {
	completions completionTrace
	// planReviews is bounded review metadata, never the full plan bodies.
	planReviews map[string]agentapi.PlanReview
	tools       map[string]*agentapi.ToolCall
	// toolAgents records starts so progress cannot invent a call or attach
	// one agent's status to another agent's call.
	toolAgents map[string]string
	// ended holds the IDs of recently completed tool calls. A shell command
	// a steer moved to the background completes its call at once, then keeps
	// streaming output under the same ID; that must not reopen it.
	ended map[string]struct{}
	// shells holds the live output of running shell calls by ID.
	shells map[string]*shellOutput
	// assets holds recent session.binary_asset events by asset ID. The CLI
	// records one just before the tool completion that names it.
	assets map[string]*rpc.SessionBinaryAssetData
	// reasoned marks each agent that streamed reasoning since its last
	// assistant message, so that message's recorded reasoningText is not
	// shown a second time.
	reasoned map[string]bool
	// stepStart is when each agent's current model call began
	// (assistant.turn_start), the start of the thinking it records.
	stepStart map[string]time.Time
	// whole keeps texts whole; otherwise they are clipped, the item marked
	// Clipped, and clippedInput holds the running tool calls whose input was.
	whole        bool
	clippedInput map[string]bool
	// hidden holds the running skill tool calls, which are not transcript
	// items, each with its agent.
	hidden map[string]string
	// longest is the length of the main agent's longest reply since the
	// owner's last prompt; promoted records, by receipt ID, whether a
	// receipt's summary became a reply.
	longest  int
	promoted map[string]bool
}

func (t *transcript) isHidden(id string) bool {
	_, ok := t.hidden[id]
	return ok
}

// clip bounds s unless t keeps texts whole, and reports whether it cut.
func (t *transcript) clip(s string, n int) (string, bool) {
	if t.whole {
		return s, false
	}
	out := clip(s, n)
	return out, len(out) < len(s)
}

// maxEndedTools bounds transcript.ended. Forgetting older IDs only lets
// shell output that arrives very late reopen its tool call.
const maxEndedTools = 1024

// maxAssets bounds transcript.assets. A tool result that names a forgotten
// asset reports the image by its digest alone.
const maxAssets = 64

func newTranscript() *transcript {
	return &transcript{tools: map[string]*agentapi.ToolCall{}, toolAgents: map[string]string{}, ended: map[string]struct{}{}, shells: map[string]*shellOutput{}, assets: map[string]*rpc.SessionBinaryAssetData{}, reasoned: map[string]bool{}, stepStart: map[string]time.Time{}, clippedInput: map[string]bool{}, hidden: map[string]string{}}
}

// items maps one event to its transcript items. An assistant message whose
// agent streamed no reasoning since its previous message yields the
// message's recorded reasoningText as a reasoning item first: the CLI records
// reasoning only there, so this is how thinking comes back from history. A
// live turn streams assistant.reasoning before the message, and that item
// already holds the text.
func (t *transcript) items(ev copilot.SessionEvent) []agentapi.Item {
	t.completions.observe(ev)
	var out []agentapi.Item
	if _, ok := ev.Data.(*rpc.AssistantTurnStartData); ok {
		t.stepStart[agentOf(ev)] = ev.Timestamp
	}
	if d, ok := ev.Data.(*rpc.AssistantMessageData); ok {
		agentID := agentOf(ev)
		if text := reasoningText(d); text != "" && !t.reasoned[agentID] {
			out = append(out, agentapi.Item{ID: reasoningItemID(d.MessageID), Kind: agentapi.ItemReasoning, Text: text, Time: t.thoughtStart(agentID, ev.Timestamp), EndedAt: ev.Timestamp, AgentID: agentID})
		}
		delete(t.reasoned, agentID)
		if agentID == "" {
			t.longest = max(t.longest, len(d.Content))
		}
	}
	if d, ok := ev.Data.(*rpc.UserMessageData); ok && agentOf(ev) == "" && (d.Delivery == nil || *d.Delivery != rpc.UserMessageDeliverySteering) && (d.IsAutopilotContinuation == nil || !*d.IsAutopilotContinuation) && !stallContinuation(d) {
		t.longest = 0
	}
	if it, ok := t.item(ev); ok {
		if reply, ok := t.completionReply(ev, &it); ok {
			out = append(out, reply)
		}
		out = append(out, it)
	}
	return out
}

// completionReply is an accepted main-agent receipt's summary as the turn's
// reply when it is longer than every reply since the owner's prompt it
// answers: autopilot agents often put their whole answer there, after short
// progress notes at most. It is whole and Markdown like any reply, and the
// receipt's notice then leaves the summary out. A summary of a reply above
// stays in the notice. The same receipt delivered again yields the same
// items.
func (t *transcript) completionReply(ev copilot.SessionEvent, it *agentapi.Item) (agentapi.Item, bool) {
	d, ok := ev.Data.(*rpc.SessionTaskCompleteData)
	if !ok || it.AgentID != "" || it.Completion == nil || it.Completion.Decision != agentapi.CompletionAccepted || d.Summary == nil {
		return agentapi.Item{}, false
	}
	// A late receipt of an earlier turn is not this turn's reply.
	if u := it.Completion.UserItemID; u == "" || u != t.completions.users[""] {
		return agentapi.Item{}, false
	}
	text := strings.TrimSpace(displaytext.SanitizeText(*d.Summary))
	reply, seen := t.promoted[ev.ID]
	if !seen {
		reply = text != "" && len(text) > t.longest
		if ev.ID != "" {
			if t.promoted == nil || len(t.promoted) >= maxCompletionEvents {
				t.promoted = map[string]bool{}
			}
			t.promoted[ev.ID] = reply
		}
	}
	if !reply {
		return agentapi.Item{}, false
	}
	t.longest = max(t.longest, len(text))
	notice := *it.Completion
	notice.Summary = ""
	it.Text = completionNotice(&notice)
	return agentapi.Item{ID: "completion-reply:" + it.ID, Kind: agentapi.ItemAssistant, Text: text, Time: it.Time}, true
}

// thoughtStart is when the agent's current model call began: the CLI
// records thinking only with its end, and that call is where it ran.
func (t *transcript) thoughtStart(agentID string, end time.Time) time.Time {
	if at := t.stepStart[agentID]; !at.IsZero() && !at.After(end) {
		return at
	}
	return end
}

// streamedReasoning notes that an agent's reasoning arrived live, ahead of
// the message it belongs to.
func (t *transcript) streamedReasoning(agentID string) {
	t.reasoned[agentID] = true
}

func reasoningText(d *rpc.AssistantMessageData) string {
	if d.ReasoningText == nil {
		return ""
	}
	return strings.TrimSpace(*d.ReasoningText)
}

func (t *transcript) item(ev copilot.SessionEvent) (agentapi.Item, bool) {
	it := agentapi.Item{Time: ev.Timestamp, AgentID: agentOf(ev)}
	switch d := ev.Data.(type) {
	case *rpc.ExitPlanModeRequestedData, *rpc.ExitPlanModeCompletedData, *rpc.HumanResponseRecordedData:
		return t.planItem(ev)
	case *rpc.UserMessageData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemUser, d.Content
		if d.MessageID != nil && *d.MessageID != "" {
			it.ID = *d.MessageID
		}
		if stallContinuation(d) {
			// uam's continuation of a stalled request reads as its note.
			it.Kind = agentapi.ItemNotice
			return it, true
		}
		it.Attachments = blobAttachments(d.Attachments, d.SupportedNativeDocumentMIMETypes, d.NativeDocumentPathFallbackPaths)
		if d.Delivery != nil && *d.Delivery == rpc.UserMessageDeliverySteering {
			it.Delivery = agentapi.DeliverySteer
		} else if d.IsAutopilotContinuation != nil && *d.IsAutopilotContinuation {
			it.Delivery = agentapi.DeliveryAutopilot
		}
	case *rpc.AssistantMessageData:
		if d.Content == "" { // tool-call-only message
			return it, false
		}
		it.ID, it.Kind, it.Text = d.MessageID, agentapi.ItemAssistant, d.Content
	case *rpc.AssistantReasoningData:
		t.streamedReasoning(it.AgentID)
		it.ID, it.Kind, it.Text = reasoningItemID(d.ReasoningID), agentapi.ItemReasoning, d.Content
		it.Time, it.EndedAt = t.thoughtStart(it.AgentID, ev.Timestamp), ev.Timestamp
	case *rpc.SessionCompactionCompleteData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, "Compacted the conversation."
		if d.TokensRemoved != nil && *d.TokensRemoved > 0 {
			it.Text = "Compacted the conversation · freed " + humanize.Comma(*d.TokensRemoved) + " tokens."
		}
		if !d.Success {
			it.Text = "Compacting the conversation failed."
			if d.Error != nil {
				text, cut := t.clip(displaytext.Sanitize(*d.Error), maxErrorText)
				it.Text, it.Clipped = "Compacting the conversation failed: "+text, cut
			}
		}
	case *rpc.SessionTruncationData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, fmt.Sprintf("Conversation truncated: %d messages and %d tokens removed.", d.MessagesRemovedDuringTruncation, d.TokensRemovedDuringTruncation)
	case *rpc.SessionTaskCompleteData:
		it.Completion = t.completion(ev, d)
		if it.Completion == nil {
			return it, false
		}
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, completionNotice(it.Completion)
	case *rpc.SessionErrorData:
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, "Error: "+displaytext.Sanitize(d.Message)
	case *rpc.ToolExecutionStartData:
		if d.ToolName == skillTool {
			// Loading a skill is bookkeeping, not work: skillLoad reports it.
			t.hidden[d.ToolCallID] = it.AgentID
			return it, false
		}
		input, cut := t.clip(compactJSON(d.Arguments), maxToolText)
		tc := &agentapi.ToolCall{Name: d.ToolName, Status: agentapi.ToolRunning, Input: input}
		t.tools[d.ToolCallID] = tc
		t.toolAgents[d.ToolCallID] = it.AgentID
		delete(t.ended, d.ToolCallID) // a new call reusing an ended ID
		delete(t.shells, d.ToolCallID)
		if cut {
			t.clippedInput[d.ToolCallID] = true
		} else {
			delete(t.clippedInput, d.ToolCallID)
		}
		it.ID, it.Kind, it.Tool, it.Clipped = d.ToolCallID, agentapi.ItemTool, cloneTool(tc), cut
	case *rpc.PermissionRequestedData:
		// Recorded history only (live permissions are handled before this):
		// a call still waiting on permission never ran, so it reads pending.
		if !t.permissionWait(permissionToolCallID(d.PermissionRequest), agentapi.ToolRunning, agentapi.ToolPending, &it) {
			return it, false
		}
	case *rpc.PermissionCompletedData:
		if d.ToolCallID == nil || !t.permissionWait(*d.ToolCallID, agentapi.ToolPending, agentapi.ToolRunning, &it) {
			return it, false
		}
		if d.Result != nil && d.Result.Kind() == rpc.PermissionResultKindDeniedNoApprovalRuleAndCouldNotRequestFromUser {
			// What a client closing mid-prompt (uam stopping) records; the completion keeps it.
			t.tools[*d.ToolCallID].Unasked, it.Tool.Unasked = true, true
		}
	case *rpc.ToolExecutionProgressData:
		if t.isHidden(d.ToolCallID) || !t.toolProgress(d, &it) {
			return it, false
		}
	case *rpc.ToolShellOutputData:
		if t.isHidden(d.ToolCallID) || !t.shellOutput(d, &it) {
			return it, false
		}
	case *rpc.ToolExecutionCompleteData:
		if t.isHidden(d.ToolCallID) {
			delete(t.hidden, d.ToolCallID)
			return it, false
		}
		if _, started := t.tools[d.ToolCallID]; started {
			it.EndedAt = ev.Timestamp
		}
		tc := t.tool(d.ToolCallID)
		delete(t.tools, d.ToolCallID)
		delete(t.toolAgents, d.ToolCallID)
		if len(t.ended) >= maxEndedTools {
			clear(t.ended)
		}
		t.ended[d.ToolCallID] = struct{}{}
		delete(t.shells, d.ToolCallID)
		tc.Status, tc.Tail, tc.Progress = agentapi.ToolCompleted, nil, ""
		tc.EditEventID, tc.FileEdits, tc.FileEditsTruncated = nativeFileEdits(ev.ID, d)
		cut := false
		if d.Result != nil {
			tc.Output, cut = t.clip(nativeConciseResult(d, tc.FileEdits), maxToolText)
			it.Images = t.images(d.Result)
			if d.Success && tc.Name == declarationToolName {
				tc.Declaration = parseDeclarationResult(d.Result.Content)
			}
		}
		if d.ShellExecution != nil {
			code := int(d.ShellExecution.ExitCode)
			tc.ExitCode = &code
		}
		if !d.Success {
			tc.Status = agentapi.ToolFailed
			if d.Error != nil {
				tc.Output, cut = t.clip(d.Error.Message, maxToolText)
			}
		}
		it.ID, it.Kind, it.Tool, it.Clipped = d.ToolCallID, agentapi.ItemTool, tc, cut || t.clippedInput[d.ToolCallID]
		delete(t.clippedInput, d.ToolCallID)
	case *rpc.SessionInfoData, *rpc.SessionWarningData:
		text, shown := noticeText(d)
		if !shown {
			return it, false
		}
		it.ID, it.Kind, it.Text = ev.ID, agentapi.ItemNotice, text
	case *rpc.SessionBinaryAssetData:
		if len(t.assets) >= maxAssets {
			clear(t.assets)
		}
		t.assets[d.AssetID] = d
		return it, false
	default:
		return it, false
	}
	return it, true
}

const (
	// skillTool is the CLI tool the agent loads a skill with.
	skillTool = "skill"
	// maxSkillName and maxSkillDescription bound a load's display text in
	// bytes; 1024 is the most a SKILL.md description may hold.
	maxSkillName        = 256
	maxSkillDescription = 1024
)

// skillLoad reports a skill.invoked event as a load: the name and the
// description, as display metadata, even in a whole-history window. Never
// retain the skill body or resolve a recorded body's reference for it.
func skillLoad(ev copilot.SessionEvent) (agentapi.SkillLoad, bool) {
	var name string
	var description *string
	switch d := ev.Data.(type) {
	case *rpc.SkillInvokedData:
		name, description = d.Name, d.Description
	case *rpc.SkillInvokedRefData:
		name, description = d.Name, d.Description
	default:
		return agentapi.SkillLoad{}, false
	}
	name = clip(strings.TrimSpace(displaytext.Sanitize(name)), maxSkillName)
	if name == "" {
		return agentapi.SkillLoad{}, false
	}
	load := agentapi.SkillLoad{ID: ev.ID, AgentID: agentOf(ev), Name: name, At: ev.Timestamp}
	if description != nil {
		load.Description = clip(strings.TrimSpace(displaytext.Sanitize(*description)), maxSkillDescription)
	}
	return load, true
}

// permissionWait moves the started call id from status from to status to
// and reports the updated item; false when the call is not in from.
func (t *transcript) permissionWait(id string, from, to agentapi.ToolStatus, it *agentapi.Item) bool {
	tc := t.tools[id]
	if tc == nil || tc.Status != from || t.toolAgents[id] != it.AgentID {
		return false
	}
	tc.Status = to
	it.ID, it.Kind, it.Tool, it.Clipped = id, agentapi.ItemTool, cloneTool(tc), t.clippedInput[id]
	return true
}

func (t *transcript) toolProgress(d *rpc.ToolExecutionProgressData, it *agentapi.Item) bool {
	agent, started := t.toolAgents[d.ToolCallID]
	tc := t.tools[d.ToolCallID]
	if !started || agent != it.AgentID || tc == nil || tc.Status != agentapi.ToolRunning {
		return false
	}
	text := clip(strings.TrimSpace(displaytext.Sanitize(d.ProgressMessage)), 512)
	if text == "" || text == tc.Progress {
		return false
	}
	tc.Progress = text
	cut := t.clippedInput[d.ToolCallID]
	if sh := t.shells[d.ToolCallID]; sh != nil {
		cut = cut || sh.clipped
	}
	it.ID, it.Kind, it.Tool, it.Clipped = d.ToolCallID, agentapi.ItemTool, cloneTool(tc), cut
	return true
}

// images returns the images a tool result carried: its image content blocks
// and its model-facing binary results. A live result holds the bytes. A
// recorded one names a session.binary_asset event by its asset ID,
// "sha256:<hex>" of the bytes; without that event only the digest is known.
func (t *transcript) images(r *rpc.ToolExecutionCompleteResult) []agentapi.Image {
	var out []agentapi.Image
	add := func(data, mime string) {
		b, err := base64.StdEncoding.DecodeString(data)
		if err != nil || len(b) == 0 {
			return
		}
		out = append(out, agentapi.Image{MIME: mime, Data: b})
	}
	for _, c := range r.Contents {
		if img, ok := c.(*rpc.ToolExecutionCompleteContentImage); ok {
			add(img.Data, img.MIMEType)
		}
	}
	for _, b := range r.BinaryResultsForLlm {
		if b == nil || b.Type() != rpc.PersistedBinaryResultTypeImage {
			continue
		}
		switch b := b.(type) {
		case *rpc.PersistedBinaryImage:
			add(b.Data, b.MIMEType)
		case *rpc.BinaryAssetReference:
			if a := t.assets[b.AssetID]; a != nil {
				add(a.Data, a.MIMEType)
			} else if digest, ok := strings.CutPrefix(b.AssetID, "sha256:"); ok {
				out = append(out, agentapi.Image{MIME: b.MIMEType, SHA256: strings.ToLower(digest)})
			}
		}
	}
	return out
}

func (t *transcript) tool(id string) *agentapi.ToolCall {
	tc := t.tools[id]
	if tc == nil { // started before this client attached
		tc = &agentapi.ToolCall{Status: agentapi.ToolRunning}
		t.tools[id] = tc
	}
	return tc
}

func cloneTool(tc *agentapi.ToolCall) *agentapi.ToolCall {
	v := *tc
	v.FileEdits = slices.Clone(tc.FileEdits)
	return &v
}

// reasoningItemID keeps reasoning items apart from the message they precede.
func reasoningItemID(id string) string { return "reasoning:" + id }
