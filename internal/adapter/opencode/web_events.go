package opencode

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const (
	webMaxDisplayBytes   = 64 << 10
	webExpiredResolution = "No longer pending in OpenCode"
)

type webPartState struct {
	kind agentapi.ItemKind
	// dropDeltas marks parts that are hidden or not text (tools, synthetic).
	dropDeltas bool
	// resynced marks unfinished text read from a snapshot: queued deltas may
	// already be included, so they are dropped until the next full update.
	resynced bool
}

func webInteractionEvent(eventType string) bool {
	switch eventType {
	case "permission.asked", "permission.replied", "question.asked", "question.replied", "question.rejected":
		return true
	}
	return false
}

func (c *webConversation) emitLocked(event agentapi.Event) {
	c.sink.Emit(event)
}

func (c *webConversation) handleEvent(event eventEnvelope) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	switch event.Type {
	case "message.updated":
		var payload struct {
			Info webMessageInfo `json:"info"`
		}
		if json.Unmarshal(event.Properties, &payload) == nil {
			c.noteMessageLocked(payload.Info)
		}
	case "message.part.updated":
		var payload struct {
			Part json.RawMessage `json:"part"`
		}
		if json.Unmarshal(event.Properties, &payload) == nil {
			if item := c.notePartLocked(payload.Part, time.Time{}, false); item != nil {
				c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: item})
			}
		}
	case "message.part.delta":
		c.handleDeltaLocked(event.Properties)
	case "session.status":
		var payload struct {
			Status webSessionStatus `json:"status"`
		}
		if json.Unmarshal(event.Properties, &payload) == nil {
			c.applyStatusLocked(payload.Status)
		}
	case "session.idle":
		c.statusSeq++
		c.finishTurnLocked()
	case "session.error":
		var payload struct {
			Error *webProviderError `json:"error"`
		}
		if json.Unmarshal(event.Properties, &payload) == nil && payload.Error != nil {
			c.sessionErrorLocked(payload.Error)
		}
	case "session.deleted":
		c.closed = true
		c.emitLocked(agentapi.Event{Kind: agentapi.EventExit, Error: "OpenCode deleted this conversation"})
	default:
		c.handleInteractionEventLocked(event)
	}
}

func (c *webConversation) handleDeltaLocked(properties json.RawMessage) {
	var delta struct {
		MessageID string `json:"messageID"`
		PartID    string `json:"partID"`
		Field     string `json:"field"`
		Delta     string `json:"delta"`
	}
	if json.Unmarshal(properties, &delta) != nil || delta.Field != "text" || delta.PartID == "" || delta.Delta == "" {
		return
	}
	state, known := c.parts[delta.PartID]
	if known && (state.dropDeltas || state.resynced) {
		return
	}
	if !known {
		// Deltas only follow a part's first update, but if that was missed,
		// infer the kind from the message role; a later full update with the
		// same ID replaces the item rather than duplicating it.
		state = webPartState{kind: agentapi.ItemAssistant}
		if c.roles[delta.MessageID] == "user" {
			state.kind = agentapi.ItemUser
		}
		c.parts[delta.PartID] = state
	}
	c.emitLocked(agentapi.Event{Kind: agentapi.EventDelta, Delta: &agentapi.Delta{ItemID: delta.PartID, Kind: state.kind, Text: delta.Delta}})
}

func (c *webConversation) handleInteractionEventLocked(event eventEnvelope) {
	switch event.Type {
	case "permission.asked":
		var request webPermissionRequest
		if json.Unmarshal(event.Properties, &request) == nil && request.ID != "" {
			c.askLocked(webPermissionInteraction(request, time.Now()))
		}
	case "question.asked":
		var request webQuestionRequest
		if json.Unmarshal(event.Properties, &request) == nil && request.ID != "" {
			c.askLocked(webQuestionInteraction(request, time.Now()))
		}
	case "permission.replied":
		var reply struct {
			RequestID string `json:"requestID"`
			Reply     string `json:"reply"`
		}
		if json.Unmarshal(event.Properties, &reply) == nil {
			if state, resolution, ok := webPermissionResolution(reply.Reply); ok {
				c.resolveLocked(reply.RequestID, state, resolution)
			}
		}
	case "question.replied":
		var reply struct {
			RequestID string     `json:"requestID"`
			Answers   [][]string `json:"answers"`
		}
		if json.Unmarshal(event.Properties, &reply) == nil {
			c.resolveLocked(reply.RequestID, agentapi.InteractionAnswered, webAnswerResolution(reply.Answers))
		}
	case "question.rejected":
		var reply struct {
			RequestID string `json:"requestID"`
		}
		if json.Unmarshal(event.Properties, &reply) == nil {
			c.resolveLocked(reply.RequestID, agentapi.InteractionRejected, "Dismissed")
		}
	}
}

func (c *webConversation) noteMessageLocked(info webMessageInfo) {
	if info.ID == "" {
		return
	}
	c.roles[info.ID] = info.Role
	switch info.Role {
	case "assistant":
		if info.ID >= c.lastAssistantID {
			c.lastAssistantID = info.ID
			c.lastAssistantErr = info.Error
		}
	case "user":
		if info.ID > c.lastUserID {
			c.lastUserID = info.ID
		}
	}
}

// noteSnapshotMessageLocked records a message read through the API and
// returns its visible items.
func (c *webConversation) noteSnapshotMessageLocked(message webMessage) []agentapi.Item {
	c.noteMessageLocked(message.Info)
	fallback := webMillis(message.Info.Time.Created)
	items := make([]agentapi.Item, 0, len(message.Parts))
	for _, raw := range message.Parts {
		if item := c.notePartLocked(raw, fallback, true); item != nil {
			items = append(items, *item)
		}
	}
	return items
}

// notePartLocked records how to treat a part's deltas and returns the item
// to show, or nil when the part is not visible.
func (c *webConversation) notePartLocked(raw json.RawMessage, fallback time.Time, snapshot bool) *agentapi.Item {
	var part webPart
	if json.Unmarshal(raw, &part) != nil || part.ID == "" {
		return nil
	}
	item, state := c.mapPartLocked(part, fallback)
	if snapshot && !state.dropDeltas && (part.Time == nil || part.Time.End == 0) {
		state.resynced = true
	}
	c.parts[part.ID] = state
	return item
}

func (c *webConversation) mapPartLocked(part webPart, fallback time.Time) (*agentapi.Item, webPartState) {
	when := fallback
	if part.Time != nil && part.Time.Start > 0 {
		when = webMillis(part.Time.Start)
	}
	if when.IsZero() {
		when = time.Now()
	}
	switch part.Type {
	case "text", "reasoning":
		state := webPartState{kind: agentapi.ItemReasoning}
		if part.Type == "text" {
			state.kind = agentapi.ItemAssistant
			if c.roles[part.MessageID] == "user" {
				state.kind = agentapi.ItemUser
			}
		}
		if part.Synthetic || part.Ignored {
			state.dropDeltas = true
			return nil, state
		}
		text := part.Text
		if display, ok := c.commandText[part.MessageID]; ok && state.kind == agentapi.ItemUser {
			// One text part shows the command as typed; the template's text
			// stays with OpenCode.
			state.dropDeltas = true
			if shown := c.commandPart[part.MessageID]; shown != "" && shown != part.ID {
				return nil, state
			}
			c.commandPart[part.MessageID] = part.ID
			text = display
		}
		if text == "" {
			return nil, state
		}
		return &agentapi.Item{ID: part.ID, Kind: state.kind, Text: text, Time: when}, state
	case "tool":
		state := webPartState{kind: agentapi.ItemTool, dropDeltas: true}
		if part.State == nil {
			return nil, state
		}
		if part.State.Time != nil && part.State.Time.Start > 0 {
			when = webMillis(part.State.Time.Start)
		}
		call := &agentapi.ToolCall{
			Name:   part.Tool,
			Title:  part.State.Title,
			Status: webToolStatus(part.State.Status),
			Input:  webCompactJSON(part.State.Input),
		}
		switch call.Status {
		case agentapi.ToolCompleted:
			call.Output = webTruncate(part.State.Output)
		case agentapi.ToolFailed:
			call.Output = webTruncate(part.State.Error)
		}
		return &agentapi.Item{ID: part.ID, Kind: agentapi.ItemTool, Tool: call, Time: when}, state
	case "file":
		// An upload a user message carried shows as its own user item,
		// described without the data: URL's bytes. Project file references
		// (file://) stay hidden, as before.
		state := webPartState{kind: agentapi.ItemUser, dropDeltas: true}
		att, ok := webDataAttachment(part)
		if !ok || c.roles[part.MessageID] != "user" {
			return nil, state
		}
		return &agentapi.Item{ID: part.ID, Kind: agentapi.ItemUser, Time: when, Attachments: []agentapi.Attachment{att}}, state
	default:
		return nil, webPartState{dropDeltas: true}
	}
}

// webDataAttachment describes a base64 data: URL file part.
func webDataAttachment(part webPart) (agentapi.Attachment, bool) {
	rest, ok := strings.CutPrefix(part.URL, "data:")
	if !ok {
		return agentapi.Attachment{}, false
	}
	header, payload, ok := strings.Cut(rest, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return agentapi.Attachment{}, false
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return agentapi.Attachment{}, false
	}
	sum := sha256.Sum256(data)
	return agentapi.Attachment{Name: part.Filename, MIME: part.Mime, Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}, true
}

func webToolStatus(status string) agentapi.ToolStatus {
	switch status {
	case "running":
		return agentapi.ToolRunning
	case "completed":
		return agentapi.ToolCompleted
	case "error":
		return agentapi.ToolFailed
	default:
		return agentapi.ToolPending
	}
}

func (c *webConversation) applyStatusLocked(status webSessionStatus) {
	c.statusSeq++
	switch status.Type {
	case "busy", "retry":
		c.startTurnLocked()
		if status.Type == "retry" {
			key := c.lastUserID
			if key == "" {
				key = c.id
			}
			c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &agentapi.Item{
				ID:   fmt.Sprintf("retry_%s_%d", key, status.Attempt),
				Kind: agentapi.ItemNotice,
				Text: fmt.Sprintf("Retrying (attempt %d): %s", status.Attempt, status.Message),
				Time: time.Now(),
			}})
		}
	case "idle":
		c.finishTurnLocked()
	}
}

func (c *webConversation) startTurnLocked() {
	if c.busy {
		return
	}
	c.busy = true
	c.sessionErr = nil
	c.lastAssistantErr = nil
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: &agentapi.Turn{State: agentapi.TurnWorking}})
}

func (c *webConversation) finishTurnLocked() {
	if !c.busy {
		return
	}
	c.busy = false
	primary := c.lastAssistantErr
	if primary == nil {
		primary = c.sessionErr
	}
	c.sessionErr = nil
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: c.turnOutcomeLocked(primary)})
}

func (c *webConversation) sessionErrorLocked(err *webProviderError) {
	if c.busy {
		c.sessionErr = err
		return
	}
	// A prompt can fail before the session ever turns busy; OpenCode then
	// reports only session.error.
	c.emitLocked(agentapi.Event{Kind: agentapi.EventTurn, Turn: c.turnOutcomeLocked(err)})
}

func (c *webConversation) turnOutcomeLocked(err *webProviderError) *agentapi.Turn {
	switch {
	case err == nil:
		return &agentapi.Turn{State: agentapi.TurnCompleted}
	case err.Name == "MessageAbortedError":
		return &agentapi.Turn{State: agentapi.TurnCancelled}
	}
	text := err.Name
	if message := err.message(); message != "" {
		if text == "" {
			text = message
		} else {
			text += ": " + message
		}
	}
	if text == "" {
		text = "OpenCode reported an error"
	}
	return &agentapi.Turn{State: agentapi.TurnFailed, Error: c.server.runtime.client.safeText(text)}
}

func (c *webConversation) askLocked(interaction agentapi.Interaction) {
	if _, done := c.resolved[interaction.ID]; done {
		return
	}
	if existing, ok := c.pending[interaction.ID]; ok {
		interaction.Time = existing.Time
	}
	c.pending[interaction.ID] = interaction
	copied := interaction
	c.emitLocked(agentapi.Event{Kind: agentapi.EventInteraction, Interaction: &copied})
}

func (c *webConversation) resolveLocked(id string, state agentapi.InteractionState, resolution string) {
	if id == "" {
		return
	}
	c.resolved[id] = struct{}{}
	interaction, ok := c.pending[id]
	if !ok {
		return
	}
	delete(c.pending, id)
	interaction.State = state
	interaction.Resolution = resolution
	c.emitLocked(agentapi.Event{Kind: agentapi.EventInteraction, Interaction: &interaction})
}

// reconcile applies a snapshot read after an event-stream gap.
func (c *webConversation) reconcile(messages []webMessage, messagesOK bool, snapshot webServerSnapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if messagesOK {
		for _, message := range messages {
			for _, item := range c.noteSnapshotMessageLocked(message) {
				copied := item
				c.emitLocked(agentapi.Event{Kind: agentapi.EventItem, Item: &copied})
			}
		}
	}
	if snapshot.statuses != nil {
		if webBusy(snapshot.statuses, c.id) {
			c.startTurnLocked()
		} else {
			c.finishTurnLocked()
		}
	}
	if snapshot.pending == nil {
		return
	}
	present := c.askPendingLocked(snapshot.pending[c])
	var vanished []string
	for id := range c.pending {
		if _, ok := present[id]; !ok {
			vanished = append(vanished, id)
		}
	}
	sort.Strings(vanished)
	for _, id := range vanished {
		c.resolveLocked(id, agentapi.InteractionExpired, webExpiredResolution)
	}
}

// applyOpenSnapshot reports the state of a reopened conversation. Live
// status events seen since sequence win over the snapshot.
func (c *webConversation) applyOpenSnapshot(snapshot webServerSnapshot, sequence uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.askPendingLocked(snapshot.pending[c])
	if c.statusSeq == sequence && webBusy(snapshot.statuses, c.id) {
		c.startTurnLocked()
	}
}

func (c *webConversation) askPendingLocked(pending *webPendingRequests) map[string]struct{} {
	present := map[string]struct{}{}
	if pending == nil {
		return present
	}
	now := time.Now()
	for _, request := range pending.permissions {
		present[request.ID] = struct{}{}
		c.askLocked(webPermissionInteraction(request, now))
	}
	for _, request := range pending.questions {
		present[request.ID] = struct{}{}
		c.askLocked(webQuestionInteraction(request, now))
	}
	return present
}

func webBusy(statuses map[string]webSessionStatus, id string) bool {
	status, ok := statuses[id]
	return ok && (status.Type == "busy" || status.Type == "retry")
}

var webPermissionTitles = map[string]string{
	"bash":               "Run a shell command",
	"edit":               "Edit a file",
	"read":               "Read a file",
	"glob":               "Find files",
	"grep":               "Search file contents",
	"list":               "List a directory",
	"webfetch":           "Fetch a URL",
	"websearch":          "Search the web",
	"external_directory": "Access a directory outside the project",
	"task":               "Start a subagent",
	"skill":              "Load a skill",
	"todowrite":          "Update the todo list",
	"lsp":                "Query the language server",
	"doom_loop":          "Repeat the same tool call",
}

var webPermissionDetailKeys = []string{"command", "description", "filepath", "filePath", "path", "url", "pattern", "include", "query", "tool"}

func webPermissionInteraction(request webPermissionRequest, now time.Time) agentapi.Interaction {
	title := webPermissionTitles[request.Permission]
	if title == "" {
		title = "Permission: " + request.Permission
	}
	var lines []string
	if len(request.Patterns) > 0 {
		lines = append(lines, "patterns: "+strings.Join(request.Patterns, ", "))
	}
	for _, key := range webPermissionDetailKeys {
		if value, ok := request.Metadata[key].(string); ok && value != "" {
			lines = append(lines, key+": "+value)
		}
	}
	if directories, ok := request.Metadata["directories"].([]any); ok {
		var values []string
		for _, directory := range directories {
			if value, ok := directory.(string); ok {
				values = append(values, value)
			}
		}
		if len(values) > 0 {
			lines = append(lines, "directories: "+strings.Join(values, ", "))
		}
	}
	if diff, ok := request.Metadata["diff"].(string); ok && diff != "" {
		lines = append(lines, "diff:\n"+diff)
	}
	return agentapi.Interaction{
		ID:     request.ID,
		Kind:   agentapi.InteractionPermission,
		Title:  title,
		Detail: webTruncate(strings.Join(lines, "\n")),
		Options: []agentapi.Option{
			{ID: "once", Label: "Allow once", AllowOnce: true},
			{ID: "always", Label: "Always allow"},
			{ID: "reject", Label: "Deny", Reject: true},
		},
		State: agentapi.InteractionPending,
		Time:  now,
	}
}

func webQuestionInteraction(request webQuestionRequest, now time.Time) agentapi.Interaction {
	questions := make([]agentapi.Question, 0, len(request.Questions))
	for _, question := range request.Questions {
		choices := make([]string, 0, len(question.Options))
		for _, option := range question.Options {
			choices = append(choices, option.Label)
		}
		questions = append(questions, agentapi.Question{
			Text:     question.Question,
			Header:   question.Header,
			Choices:  choices,
			Multiple: question.Multiple != nil && *question.Multiple,
			// OpenCode documents custom answers as allowed unless false.
			Custom: question.Custom == nil || *question.Custom,
		})
	}
	return agentapi.Interaction{
		ID:        request.ID,
		Kind:      agentapi.InteractionQuestion,
		Title:     "Question",
		Questions: questions,
		State:     agentapi.InteractionPending,
		Time:      now,
	}
}

func webPermissionResolution(reply string) (agentapi.InteractionState, string, bool) {
	switch reply {
	case "once":
		return agentapi.InteractionAnswered, "Allowed once", true
	case "always":
		return agentapi.InteractionAnswered, "Always allowed", true
	case "reject":
		return agentapi.InteractionRejected, "Denied", true
	}
	return "", "", false
}

func webAnswerResolution(answers [][]string) string {
	parts := make([]string, 0, len(answers))
	for _, answer := range answers {
		parts = append(parts, strings.Join(answer, ", "))
	}
	if text := strings.Join(parts, "; "); strings.TrimSpace(text) != "" {
		return "Answered: " + webTruncate(text)
	}
	return "Answered"
}

func webCompactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return webTruncate(string(raw))
	}
	return webTruncate(compact.String())
}

// webTruncate bounds display text at a UTF-8 boundary.
func webTruncate(value string) string {
	if len(value) <= webMaxDisplayBytes {
		return value
	}
	const marker = "…"
	cut := webMaxDisplayBytes - len(marker)
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + marker
}

func webMillis(value float64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(value))
}
