package opencode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestWebSendAddsFilePartsAfterTheText(t *testing.T) {
	h := newWebHarness(t)
	conversation, _ := h.open(t, "")
	files := []agentapi.File{{Path: "/work/src/a b.go", Rel: "src/a b.go"}, {Path: "/work/docs", Rel: "docs", Dir: true}}
	if err := conversation.Send(t.Context(), agentapi.Prompt{Text: "é @src/a b.go", Files: files}); err != nil {
		t.Fatal(err)
	}
	requests := h.fake.requestsFor(http.MethodPost, "/session/"+conversation.ID()+"/prompt_async")
	var body struct {
		Parts []map[string]any `json:"parts"`
	}
	if err := json.Unmarshal([]byte(requests[0].Body), &body); err != nil {
		t.Fatal(err)
	}
	want := []map[string]any{
		{"type": "text", "text": "é @src/a b.go"},
		{"type": "file", "mime": "text/plain", "filename": "src/a b.go", "url": "file:///work/src/a%20b.go",
			"source": map[string]any{"type": "file", "path": "src/a b.go", "text": map[string]any{"value": "@src/a b.go", "start": 2.0, "end": 13.0}}},
		{"type": "file", "mime": "application/x-directory", "filename": "docs", "url": "file:///work/docs"},
	}
	if !reflect.DeepEqual(body.Parts, want) {
		t.Fatalf("parts = %#v", body.Parts)
	}
}

func TestWebCommandsAndRunCommand(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	h.fake.mu.Lock()
	h.fake.commands = []webCommand{{Name: "init", Description: "create AGENTS.md", Source: "command"}, {Name: "probe-skill", Source: "skill", Hints: []string{"$ARGUMENTS"}}}
	h.fake.mu.Unlock()
	commands, err := conversation.Commands(t.Context())
	want := []agentapi.Command{{Name: "init", Description: "create AGENTS.md", Kind: agentapi.CommandPrompt}, {Name: "probe-skill", Kind: agentapi.CommandSkill, InputHint: "$ARGUMENTS"}}
	if err != nil || !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %+v, %v", commands, err)
	}

	commandPath := "/session/" + id + "/command"
	if err := conversation.RunCommand(t.Context(), "probe-skill", agentapi.Prompt{Text: "!`touch ARG-MARKER`"}); err == nil || len(h.fake.requestsFor(http.MethodPost, commandPath)) != 0 {
		t.Fatalf("shell expansion = %v", err)
	}

	// OpenCode answers only when the turn ends; the user message is enough.
	hold := make(chan struct{})
	defer close(hold)
	h.fake.mu.Lock()
	h.fake.commandHold = hold
	h.fake.mu.Unlock()
	files := []agentapi.File{{Path: "/work/a.go", Rel: "a.go"}}
	if err := conversation.RunCommand(t.Context(), "probe-skill", agentapi.Prompt{Text: "alpha @a.go", Files: files}); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	requests := h.fake.requestsFor(http.MethodPost, commandPath)
	var body map[string]any
	if len(requests) != 1 || json.Unmarshal([]byte(requests[0].Body), &body) != nil {
		t.Fatalf("command posts = %+v", requests)
	}
	parts, _ := body["parts"].([]any)
	if body["command"] != "probe-skill" || body["arguments"] != "alpha @a.go" || !webMessageIDRE.MatchString(body["messageID"].(string)) || len(parts) != 1 {
		t.Fatalf("command body = %#v", body)
	}
	item := sink.waitFor(t, "command user item", func(e agentapi.Event) bool { return e.Kind == agentapi.EventItem && e.Item.ID == "prt_cmd" })
	if item.Item.Kind != agentapi.ItemUser || item.Item.Text != "/probe-skill alpha @a.go" {
		t.Fatalf("user item = %+v", item.Item)
	}

	h.fake.mu.Lock()
	h.fake.commandHold, h.fake.commandCode = nil, http.StatusInternalServerError
	h.fake.mu.Unlock()
	err = conversation.RunCommand(t.Context(), "init", agentapi.Prompt{})
	if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) || !strings.Contains(err.Error(), "HTTP 500") {
		t.Fatalf("refused command = %v", err)
	}
	h.fake.mu.Lock()
	h.fake.commandCode = 0
	h.fake.statuses[id] = webSessionStatus{Type: "busy"}
	h.fake.mu.Unlock()
	before := len(h.fake.requestsFor(http.MethodPost, commandPath))
	if err := conversation.RunCommand(t.Context(), "init", agentapi.Prompt{}); !errors.Is(err, agentapi.ErrBusy) || len(h.fake.requestsFor(http.MethodPost, commandPath)) != before {
		t.Fatalf("busy command = %v", err)
	}
}

func TestWebUploadsGoAsDataURLsAndShowAsUserItems(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	prompt := agentapi.Prompt{Text: "look", Files: []agentapi.File{{Path: "/work/a.go", Rel: "a.go"}}, Attachments: []agentapi.Blob{{Name: "note.txt", MIME: "text/plain", Data: []byte("hi")}}}
	if err := conversation.Send(t.Context(), prompt); err != nil {
		t.Fatal(err)
	}
	requests := h.fake.requestsFor(http.MethodPost, "/session/"+id+"/prompt_async")
	var body struct {
		Parts []map[string]any `json:"parts"`
	}
	if err := json.Unmarshal([]byte(requests[0].Body), &body); err != nil || len(body.Parts) != 3 {
		t.Fatalf("parts = %+v, %v", body.Parts, err)
	}
	if want := map[string]any{"type": "file", "mime": "text/plain", "filename": "note.txt", "url": "data:text/plain;base64,aGk="}; !reflect.DeepEqual(body.Parts[2], want) {
		t.Fatalf("upload part = %#v", body.Parts[2])
	}

	h.fake.emit("message.updated", map[string]any{"sessionID": id, "info": map[string]any{"id": "msg_u1", "sessionID": id, "role": "user", "time": map[string]any{"created": 1}}})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_ref", "messageID": "msg_u1", "sessionID": id, "type": "file", "mime": "text/plain", "filename": "a.go", "url": "file:///work/a.go"}})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_up", "messageID": "msg_u1", "sessionID": id, "type": "file", "mime": "text/plain", "filename": "note.txt", "url": "data:text/plain;base64,aGk="}})
	item := sink.waitFor(t, "upload item", func(e agentapi.Event) bool { return e.Kind == agentapi.EventItem && e.Item.ID == "prt_up" })
	sum := sha256.Sum256([]byte("hi"))
	want := []agentapi.Attachment{{Name: "note.txt", MIME: "text/plain", Size: 2, SHA256: hex.EncodeToString(sum[:])}}
	if item.Item.Kind != agentapi.ItemUser || item.Item.Text != "" || !reflect.DeepEqual(item.Item.Attachments, want) {
		t.Fatalf("upload item = %+v", item.Item)
	}
	for _, e := range sink.snapshot() {
		if e.Kind == agentapi.EventItem && e.Item.ID == "prt_ref" {
			t.Fatalf("a file reference showed as an item: %+v", e.Item)
		}
	}
}
