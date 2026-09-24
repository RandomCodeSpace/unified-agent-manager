package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

const fakeWebPassword = "web-password-must-never-leak"

// fakeWebOpenCode is an in-process OpenCode HTTP API with scripted state.
type fakeWebOpenCode struct {
	t         *testing.T
	directory string
	server    *httptest.Server
	done      chan struct{}

	mu          sync.Mutex
	nextSession int
	sessions    map[string]sessionInfo
	messages    map[string][]fakeWebMessage
	statuses    map[string]webSessionStatus
	permissions []webPermissionRequest
	questions   []map[string]any
	diffs       []map[string]any
	requests    []fakeWebRequest
	promptMode  string
	replyStatus int
	streams     []chan string
	streamCount int
	streamDown  bool
}

type fakeWebMessage struct {
	id   string
	role string
	raw  json.RawMessage
}

type fakeWebRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
}

func newFakeWebOpenCode(t *testing.T) *fakeWebOpenCode {
	t.Helper()
	fake := &fakeWebOpenCode{
		t:         t,
		directory: filepath.Clean(t.TempDir()),
		done:      make(chan struct{}),
		sessions:  map[string]sessionInfo{},
		messages:  map[string][]fakeWebMessage{},
		statuses:  map[string]webSessionStatus{},
	}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(func() {
		close(fake.done)
		fake.server.Close()
	})
	return fake
}

func (f *fakeWebOpenCode) serve(w http.ResponseWriter, r *http.Request) {
	user, password, ok := r.BasicAuth()
	if !ok || user != openCodeServerUsername || password != fakeWebPassword || r.Header.Get("X-OpenCode-Directory") != f.directory {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.requests = append(f.requests, fakeWebRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Body: string(body)})
	f.mu.Unlock()
	path := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/event":
		f.serveEvents(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/session":
		f.createSession(w, body)
	case r.Method == http.MethodGet && r.URL.Path == "/session/status":
		f.mu.Lock()
		defer f.mu.Unlock()
		writeFakeJSON(w, f.statuses)
	case r.Method == http.MethodGet && r.URL.Path == "/permission":
		f.mu.Lock()
		defer f.mu.Unlock()
		writeFakeJSON(w, append([]webPermissionRequest{}, f.permissions...))
	case r.Method == http.MethodGet && r.URL.Path == "/question":
		f.mu.Lock()
		defer f.mu.Unlock()
		writeFakeJSON(w, append([]map[string]any{}, f.questions...))
	case r.Method == http.MethodPost && len(path) == 3 && (path[0] == "permission" || path[0] == "question"):
		f.reply(w, path[1])
	case len(path) >= 2 && path[0] == "session":
		f.serveSession(w, r, path[1:], body)
	default:
		http.NotFound(w, r)
	}
}

func writeFakeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func writeFakeNotFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `{"name":"NotFoundError","data":{"message":"not found"}}`)
}

func (f *fakeWebOpenCode) createSession(w http.ResponseWriter, body []byte) {
	var payload struct {
		Title string `json:"title"`
	}
	_ = json.Unmarshal(body, &payload)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSession++
	info := sessionInfo{ID: fmt.Sprintf("ses_web%03d", f.nextSession), Directory: f.directory, Title: payload.Title}
	f.sessions[info.ID] = info
	writeFakeJSON(w, info)
}

func (f *fakeWebOpenCode) serveSession(w http.ResponseWriter, r *http.Request, path []string, body []byte) {
	f.mu.Lock()
	info, exists := f.sessions[path[0]]
	f.mu.Unlock()
	if !exists {
		writeFakeNotFound(w)
		return
	}
	switch {
	case r.Method == http.MethodGet && len(path) == 1:
		writeFakeJSON(w, info)
	case r.Method == http.MethodGet && len(path) == 2 && path[1] == "message":
		f.serveMessages(w, r, info.ID)
	case r.Method == http.MethodGet && len(path) == 3 && path[1] == "message":
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, message := range f.messages[info.ID] {
			if message.id == path[2] {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(message.raw)
				return
			}
		}
		writeFakeNotFound(w)
	case r.Method == http.MethodPost && len(path) == 2 && path[1] == "prompt_async":
		f.prompt(w, info.ID, body)
	case r.Method == http.MethodPost && len(path) == 2 && path[1] == "abort":
		writeFakeJSON(w, true)
	case r.Method == http.MethodGet && len(path) == 2 && path[1] == "diff":
		f.mu.Lock()
		defer f.mu.Unlock()
		writeFakeJSON(w, f.diffs)
	default:
		http.NotFound(w, r)
	}
}

// serveMessages mirrors OpenCode's legacy pagination: newest page first,
// oldest-first inside a page, X-Next-Cursor naming the next older page.
func (f *fakeWebOpenCode) serveMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	f.mu.Lock()
	all := append([]fakeWebMessage{}, f.messages[sessionID]...)
	f.mu.Unlock()
	end := len(all)
	if before := r.URL.Query().Get("before"); before != "" {
		index, err := strconv.Atoi(strings.TrimPrefix(before, "cursor-"))
		if err != nil || index > len(all) {
			http.Error(w, "bad cursor", http.StatusBadRequest)
			return
		}
		end = index
	}
	start := 0
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 && end-limit > 0 {
		start = end - limit
		w.Header().Set("X-Next-Cursor", "cursor-"+strconv.Itoa(start))
	}
	page := make([]json.RawMessage, 0, end-start)
	for _, message := range all[start:end] {
		page = append(page, message.raw)
	}
	writeFakeJSON(w, page)
}

func (f *fakeWebOpenCode) prompt(w http.ResponseWriter, sessionID string, body []byte) {
	var payload struct {
		MessageID string `json:"messageID"`
	}
	_ = json.Unmarshal(body, &payload)
	f.mu.Lock()
	mode := f.promptMode
	f.mu.Unlock()
	switch mode {
	case "reject":
		http.Error(w, "bad prompt "+string(body), http.StatusBadRequest)
		return
	case "drop", "drop-after-save":
		if mode == "drop-after-save" {
			f.addMessage(sessionID, webTestMessage(payload.MessageID, "user", 1000, webTestText("prt_saved", payload.MessageID, "saved")))
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = conn.Close()
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeWebOpenCode) reply(w http.ResponseWriter, id string) {
	f.mu.Lock()
	status := f.replyStatus
	f.mu.Unlock()
	if status == http.StatusNotFound {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = fmt.Fprintf(w, `{"_tag":"PermissionNotFoundError","requestID":%q,"message":"gone"}`, id)
		return
	}
	writeFakeJSON(w, true)
}

func (f *fakeWebOpenCode) serveEvents(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.streamDown {
		f.mu.Unlock()
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	stream := make(chan string, 256)
	f.streams = append(f.streams, stream)
	f.streamCount++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher := w.(http.Flusher)
	flusher.Flush()
	for {
		select {
		case frame, ok := <-stream:
			if !ok {
				return
			}
			_, _ = io.WriteString(w, frame)
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-f.done:
			return
		}
	}
}

func (f *fakeWebOpenCode) emit(eventType string, properties any) {
	f.t.Helper()
	data, err := json.Marshal(map[string]any{"id": "evt_test", "type": eventType, "properties": properties})
	if err != nil {
		f.t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.streams) == 0 {
		f.t.Fatalf("no event stream connected for %s", eventType)
	}
	for _, stream := range f.streams {
		stream <- "data: " + string(data) + "\n\n"
	}
}

func (f *fakeWebOpenCode) dropStreams() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, stream := range f.streams {
		close(stream)
	}
	f.streams = nil
}

func (f *fakeWebOpenCode) waitStreams(t *testing.T, count int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		got := f.streamCount
		f.mu.Unlock()
		if got >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event stream connections did not reach %d", count)
}

func (f *fakeWebOpenCode) addMessage(sessionID string, message fakeWebMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.messages[sessionID] = append(f.messages[sessionID], message)
}

func (f *fakeWebOpenCode) requestsFor(method, path string) []fakeWebRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var result []fakeWebRequest
	for _, request := range f.requests {
		if request.Method == method && request.Path == path {
			result = append(result, request)
		}
	}
	return result
}

func webTestText(id, messageID, text string) map[string]any {
	return map[string]any{"id": id, "sessionID": "ses_x", "messageID": messageID, "type": "text", "text": text, "time": map[string]any{"start": 1000, "end": 2000}}
}

func webTestMessage(id, role string, created int64, parts ...map[string]any) fakeWebMessage {
	info := map[string]any{"id": id, "sessionID": "ses_x", "role": role, "time": map[string]any{"created": created}}
	data, err := json.Marshal(map[string]any{"info": info, "parts": parts})
	if err != nil {
		panic(err)
	}
	return fakeWebMessage{id: id, role: role, raw: data}
}

type webHarness struct {
	fake     *fakeWebOpenCode
	provider *webProvider

	mu     sync.Mutex
	starts int
	stops  int
	done   []chan struct{}
}

func newWebHarness(t *testing.T) *webHarness {
	t.Helper()
	h := &webHarness{fake: newFakeWebOpenCode(t)}
	h.provider = newWebProvider(
		func(context.Context) (providerCommand, error) { return providerCommand{path: "/bin/true"}, nil },
		func(_ context.Context, _ providerCommand, directory string) (*webRuntime, error) {
			client, err := newAPIClient(h.fake.server.URL, openCodeServerUsername, fakeWebPassword, directory, h.fake.server.Client())
			if err != nil {
				return nil, err
			}
			client.allEvents = true
			done := make(chan struct{})
			h.mu.Lock()
			h.starts++
			h.done = append(h.done, done)
			h.mu.Unlock()
			var once sync.Once
			return &webRuntime{
				client: client,
				done:   done,
				stop: func() {
					once.Do(func() {
						h.mu.Lock()
						h.stops++
						h.mu.Unlock()
					})
				},
				exitReason: func() string { return "OpenCode server exited unexpectedly (server output: boom)" },
			}, nil
		},
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := h.provider.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return h
}

func (h *webHarness) counts() (int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.starts, h.stops
}

func (h *webHarness) open(t *testing.T, conversationID string) (*webConversation, *recordingSink) {
	t.Helper()
	sink := &recordingSink{}
	conversation, err := h.provider.Open(testContext(t), agentapi.OpenRequest{SessionID: "11111111-2222-3333-4444-555555555555", ConversationID: conversationID, Workdir: h.fake.directory, Title: "Demo", Events: sink})
	if err != nil {
		t.Fatalf("Open(%q): %v", conversationID, err)
	}
	return conversation.(*webConversation), sink
}

type recordingSink struct {
	mu     sync.Mutex
	events []agentapi.Event
}

func (s *recordingSink) Emit(event agentapi.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingSink) snapshot() []agentapi.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]agentapi.Event{}, s.events...)
}

func (s *recordingSink) waitFor(t *testing.T, what string, match func(agentapi.Event) bool) agentapi.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range s.snapshot() {
			if match(event) {
				return event
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s; events=%s", what, describeWebEvents(s.snapshot()))
	return agentapi.Event{}
}

func describeWebEvents(events []agentapi.Event) string {
	var parts []string
	for _, event := range events {
		data, _ := json.Marshal(event)
		parts = append(parts, string(data))
	}
	return strings.Join(parts, "\n")
}

func isTurn(state agentapi.TurnState) func(agentapi.Event) bool {
	return func(event agentapi.Event) bool {
		return event.Kind == agentapi.EventTurn && event.Turn.State == state
	}
}

func isInteraction(id string, state agentapi.InteractionState) func(agentapi.Event) bool {
	return func(event agentapi.Event) bool {
		return event.Kind == agentapi.EventInteraction && event.Interaction.ID == id && event.Interaction.State == state
	}
}

// barrier waits until every earlier event has been dispatched.
func (h *webHarness) barrier(t *testing.T, sink *recordingSink, sessionID string) {
	t.Helper()
	marker := fmt.Sprintf("prt_barrier%d", time.Now().UnixNano())
	h.fake.emit("message.part.updated", map[string]any{"sessionID": sessionID, "part": map[string]any{"id": marker, "messageID": "msg_barrier", "sessionID": sessionID, "type": "text", "text": "."}})
	sink.waitFor(t, "barrier", func(event agentapi.Event) bool { return event.Kind == agentapi.EventItem && event.Item.ID == marker })
}

func TestWebProviderMetadataAndCheck(t *testing.T) {
	provider := NewWebProvider()
	if provider.Name() != agentapi.ProviderOpenCode || provider.DisplayName() != "OpenCode" {
		t.Fatalf("names = %q %q", provider.Name(), provider.DisplayName())
	}
	if got := provider.Capabilities(); got != (agentapi.Capabilities{Cancel: true, Permissions: true, Questions: true, SessionDiff: true, History: true}) {
		t.Fatalf("capabilities = %#v", got)
	}

	t.Setenv("PATH", t.TempDir())
	if err := provider.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "not found on PATH") || !strings.Contains(err.Error(), minimumVersion) {
		t.Fatalf("missing command error = %v", err)
	}
	t.Setenv("PATH", filepath.Dir(writeVersionedOpenCode(t, "1.18.0")))
	if err := provider.Check(t.Context()); err == nil || !strings.Contains(err.Error(), "required version "+minimumVersion) {
		t.Fatalf("old version error = %v", err)
	}
	t.Setenv("PATH", filepath.Dir(writeVersionedOpenCode(t, "1.18.32")))
	if err := provider.Check(t.Context()); err != nil {
		t.Fatalf("supported version: %v", err)
	}
}

func TestWebOpenCreateReopenAndRefusals(t *testing.T) {
	h := newWebHarness(t)
	created, _ := h.open(t, "")
	if created.ID() != "ses_web001" {
		t.Fatalf("created ID = %q", created.ID())
	}
	creates := h.fake.requestsFor(http.MethodPost, "/session")
	if len(creates) != 1 || !strings.Contains(creates[0].Body, `"title":"UAM: Demo"`) || !strings.Contains(creates[0].Body, `"metadata":{"uam":true}`) {
		t.Fatalf("create requests = %#v", creates)
	}

	h.fake.mu.Lock()
	h.fake.sessions["ses_busy01"] = sessionInfo{ID: "ses_busy01", Directory: h.fake.directory}
	h.fake.sessions["ses_child1"] = sessionInfo{ID: "ses_child1", ParentID: "ses_busy01", Directory: h.fake.directory}
	h.fake.sessions["ses_other1"] = sessionInfo{ID: "ses_other1", Directory: "/somewhere/else"}
	h.fake.statuses["ses_busy01"] = webSessionStatus{Type: "busy"}
	h.fake.permissions = []webPermissionRequest{{ID: "per_child1", SessionID: "ses_child1", Permission: "bash", Patterns: []string{"ls"}}}
	h.fake.mu.Unlock()

	reopened, sink := h.open(t, "ses_busy01")
	if reopened.ID() != "ses_busy01" {
		t.Fatalf("reopened ID = %q", reopened.ID())
	}
	sink.waitFor(t, "working turn", isTurn(agentapi.TurnWorking))
	sink.waitFor(t, "subagent permission on the root conversation", isInteraction("per_child1", agentapi.InteractionPending))
	if starts, _ := h.counts(); starts != 1 {
		t.Fatalf("server starts = %d, want one shared server", starts)
	}

	_, err := h.provider.Open(t.Context(), agentapi.OpenRequest{ConversationID: "ses_missing", Workdir: h.fake.directory, Events: &recordingSink{}})
	if !errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("missing reopen error = %v", err)
	}
	for _, id := range []string{"ses_other1", "ses_child1"} {
		if _, err := h.provider.Open(t.Context(), agentapi.OpenRequest{ConversationID: id, Workdir: h.fake.directory, Events: &recordingSink{}}); err == nil || errors.Is(err, agentapi.ErrConversationNotFound) {
			t.Fatalf("Open(%s) error = %v, want refusal", id, err)
		}
	}
	if _, err := h.provider.Open(t.Context(), agentapi.OpenRequest{ConversationID: "ses_busy01", Workdir: h.fake.directory, Events: &recordingSink{}}); err == nil {
		t.Fatal("second Open of an open conversation succeeded")
	}
	if len(h.fake.requestsFor(http.MethodPost, "/session")) != 1 {
		t.Fatal("reopen created a session")
	}
}

func TestWebDeltasAndToolUpserts(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	h.fake.emit("message.updated", map[string]any{"sessionID": id, "info": map[string]any{"id": "msg_a1", "sessionID": id, "role": "assistant", "time": map[string]any{"created": 1}}})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_text", "messageID": "msg_a1", "sessionID": id, "type": "text", "text": "", "time": map[string]any{"start": 5}}})
	for _, text := range []string{"Hel", "lo"} {
		h.fake.emit("message.part.delta", map[string]any{"sessionID": id, "messageID": "msg_a1", "partID": "prt_text", "field": "text", "delta": text})
	}
	h.fake.emit("message.part.delta", map[string]any{"sessionID": id, "messageID": "msg_a1", "partID": "prt_think", "field": "text", "delta": "hmm"})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_think", "messageID": "msg_a1", "sessionID": id, "type": "reasoning", "text": "hmm", "time": map[string]any{"start": 5}}})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_syn", "messageID": "msg_a1", "sessionID": id, "type": "text", "text": "hidden", "synthetic": true}})
	h.fake.emit("message.part.delta", map[string]any{"sessionID": id, "messageID": "msg_a1", "partID": "prt_syn", "field": "text", "delta": "more hidden"})
	input := map[string]any{"command": "ls -la"}
	for _, state := range []map[string]any{
		{"status": "pending", "input": input, "raw": ""},
		{"status": "running", "input": input, "title": "List files", "time": map[string]any{"start": 7}},
		{"status": "completed", "input": input, "title": "List files", "output": strings.Repeat("x", webMaxDisplayBytes+10), "metadata": map[string]any{}, "time": map[string]any{"start": 7, "end": 9}},
	} {
		h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_tool", "messageID": "msg_a1", "sessionID": id, "type": "tool", "callID": "c1", "tool": "bash", "state": state}})
	}
	h.fake.emit("message.part.updated", map[string]any{"sessionID": "ses_unknown", "part": map[string]any{"id": "prt_foreign", "messageID": "msg_z", "sessionID": "ses_unknown", "type": "text", "text": "not ours"}})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_text", "messageID": "msg_a1", "sessionID": id, "type": "text", "text": "Hello", "time": map[string]any{"start": 5, "end": 8}}})
	h.barrier(t, sink, id)

	var deltas []string
	var toolStatuses []agentapi.ToolStatus
	for _, event := range sink.snapshot() {
		switch event.Kind {
		case agentapi.EventDelta:
			deltas = append(deltas, string(event.Delta.Kind)+":"+event.Delta.ItemID+":"+event.Delta.Text)
		case agentapi.EventItem:
			switch event.Item.ID {
			case "prt_tool":
				toolStatuses = append(toolStatuses, event.Item.Tool.Status)
				if event.Item.Tool.Name != "bash" || event.Item.Tool.Input != `{"command":"ls -la"}` {
					t.Errorf("tool item = %#v", event.Item.Tool)
				}
				if event.Item.Tool.Status == agentapi.ToolCompleted && (len(event.Item.Tool.Output) > webMaxDisplayBytes || event.Item.Tool.Title != "List files") {
					t.Errorf("completed tool output length %d title %q", len(event.Item.Tool.Output), event.Item.Tool.Title)
				}
			case "prt_syn", "prt_foreign":
				t.Errorf("unexpected item %#v", event.Item)
			case "prt_text":
				if event.Item.Kind != agentapi.ItemAssistant || event.Item.Text != "Hello" {
					t.Errorf("text item = %#v", event.Item)
				}
			case "prt_think":
				if event.Item.Kind != agentapi.ItemReasoning {
					t.Errorf("reasoning item = %#v", event.Item)
				}
			}
		}
	}
	wantDeltas := []string{"assistant:prt_text:Hel", "assistant:prt_text:lo", "assistant:prt_think:hmm"}
	if strings.Join(deltas, "|") != strings.Join(wantDeltas, "|") {
		t.Fatalf("deltas = %q, want %q", deltas, wantDeltas)
	}
	if fmt.Sprint(toolStatuses) != "[pending running completed]" {
		t.Fatalf("tool statuses = %v", toolStatuses)
	}
}

func TestWebTurnStates(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	turns := func() []string {
		var result []string
		for _, event := range sink.snapshot() {
			if event.Kind == agentapi.EventTurn {
				result = append(result, string(event.Turn.State)+"|"+event.Turn.Error)
			}
		}
		return result
	}
	busy := func() {
		h.fake.emit("session.status", map[string]any{"sessionID": id, "status": map[string]any{"type": "busy"}})
	}
	idle := func() {
		h.fake.emit("session.status", map[string]any{"sessionID": id, "status": map[string]any{"type": "idle"}})
		h.fake.emit("session.idle", map[string]any{"sessionID": id})
	}

	busy()
	idle()
	busy()
	h.fake.emit("message.updated", map[string]any{"sessionID": id, "info": map[string]any{"id": "msg_b1", "sessionID": id, "role": "assistant", "error": map[string]any{"name": "MessageAbortedError", "data": map[string]any{"message": "aborted"}}}})
	h.fake.emit("session.error", map[string]any{"sessionID": id, "error": map[string]any{"name": "MessageAbortedError", "data": map[string]any{"message": "aborted"}}})
	idle()
	busy()
	h.fake.emit("session.status", map[string]any{"sessionID": id, "status": map[string]any{"type": "retry", "attempt": 2, "message": "rate limited", "next": 5}})
	h.fake.emit("session.error", map[string]any{"error": map[string]any{"name": "UnknownError", "data": map[string]any{"message": "broken plugin"}}})
	h.fake.emit("session.error", map[string]any{"sessionID": id, "error": map[string]any{"name": "APIError", "data": map[string]any{"message": "quota \x1b[31mexceeded", "isRetryable": false}}})
	idle()
	h.fake.emit("session.error", map[string]any{"sessionID": id, "error": map[string]any{"name": "UnknownError", "data": map[string]any{"message": "agent missing"}}})
	h.barrier(t, sink, id)

	want := []string{
		"working|", "completed|",
		"working|", "cancelled|",
		"working|", "failed|APIError: quota exceeded",
		"failed|UnknownError: agent missing",
	}
	if got := turns(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("turns = %q, want %q", got, want)
	}
	sink.waitFor(t, "retry notice", func(event agentapi.Event) bool {
		return event.Kind == agentapi.EventItem && event.Item.Kind == agentapi.ItemNotice && strings.Contains(event.Item.Text, "rate limited")
	})
}

func TestWebPermissionRespond(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	h.fake.emit("permission.asked", map[string]any{"id": "per_one", "sessionID": id, "permission": "bash", "patterns": []string{"git status"}, "metadata": map[string]any{"command": "git status"}, "always": []string{"git *"}})
	event := sink.waitFor(t, "permission", isInteraction("per_one", agentapi.InteractionPending))
	got := event.Interaction
	if got.Kind != agentapi.InteractionPermission || got.Title != "Run a shell command" || !strings.Contains(got.Detail, "command: git status") || !strings.Contains(got.Detail, "patterns: git status") {
		t.Fatalf("permission interaction = %#v", got)
	}
	if fmt.Sprint(got.Options) != "[{once Allow once false} {always Always allow false} {reject Deny true}]" {
		t.Fatalf("options = %v", got.Options)
	}
	if err := conversation.Respond(t.Context(), "per_one", agentapi.Answer{Decision: "maybe"}); err == nil || errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("invalid decision error = %v", err)
	}
	if err := conversation.Respond(t.Context(), "per_one", agentapi.Answer{Decision: "once"}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	replies := h.fake.requestsFor(http.MethodPost, "/permission/per_one/reply")
	if len(replies) != 1 || replies[0].Body != `{"reply":"once"}` {
		t.Fatalf("reply requests = %#v", replies)
	}
	resolved := sink.waitFor(t, "answered permission", isInteraction("per_one", agentapi.InteractionAnswered))
	if resolved.Interaction.Resolution != "Allowed once" {
		t.Fatalf("resolution = %q", resolved.Interaction.Resolution)
	}
	if err := conversation.Respond(t.Context(), "per_one", agentapi.Answer{Decision: "once"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("second Respond = %v", err)
	}

	h.fake.emit("permission.asked", map[string]any{"id": "per_two", "sessionID": id, "permission": "edit", "patterns": []string{"a.go"}, "metadata": map[string]any{"filepath": "/p/a.go", "diff": "-a\n+b"}, "always": []string{"*"}})
	sink.waitFor(t, "second permission", isInteraction("per_two", agentapi.InteractionPending))
	h.fake.mu.Lock()
	h.fake.replyStatus = http.StatusNotFound
	h.fake.mu.Unlock()
	if err := conversation.Respond(t.Context(), "per_two", agentapi.Answer{Decision: "reject"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("404 Respond = %v", err)
	}
	sink.waitFor(t, "expired permission", isInteraction("per_two", agentapi.InteractionExpired))
}

func TestWebQuestionRespondAndReject(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	questions := []map[string]any{
		{"question": "Pick one", "header": "Pick", "options": []map[string]any{{"label": "A", "description": "a"}, {"label": "B", "description": "b"}}},
		{"question": "Pick many", "header": "Many", "options": []map[string]any{{"label": "X", "description": "x"}}, "multiple": true, "custom": false},
	}
	h.fake.emit("question.asked", map[string]any{"id": "que_one", "sessionID": id, "questions": questions})
	event := sink.waitFor(t, "question", isInteraction("que_one", agentapi.InteractionPending))
	got := event.Interaction.Questions
	if event.Interaction.Title != "Question" || len(got) != 2 || fmt.Sprint(got[0].Choices) != "[A B]" || !got[0].Custom || got[0].Multiple || got[1].Custom || !got[1].Multiple {
		t.Fatalf("question interaction = %#v", event.Interaction)
	}
	if err := conversation.Respond(t.Context(), "que_one", agentapi.Answer{Answers: [][]string{{"A"}}}); err == nil {
		t.Fatal("short answers accepted")
	}
	if err := conversation.Respond(t.Context(), "que_one", agentapi.Answer{Answers: [][]string{{"A"}, nil}}); err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if replies := h.fake.requestsFor(http.MethodPost, "/question/que_one/reply"); len(replies) != 1 || replies[0].Body != `{"answers":[["A"],[]]}` {
		t.Fatalf("question replies = %#v", replies)
	}
	sink.waitFor(t, "answered question", isInteraction("que_one", agentapi.InteractionAnswered))

	h.fake.emit("question.asked", map[string]any{"id": "que_two", "sessionID": id, "questions": questions[:1]})
	sink.waitFor(t, "second question", isInteraction("que_two", agentapi.InteractionPending))
	if err := conversation.Respond(t.Context(), "que_two", agentapi.Answer{Reject: true}); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rejects := h.fake.requestsFor(http.MethodPost, "/question/que_two/reject"); len(rejects) != 1 {
		t.Fatalf("reject requests = %#v", rejects)
	}
	sink.waitFor(t, "rejected question", isInteraction("que_two", agentapi.InteractionRejected))
}

func TestWebRepliedEventsResolveInteractions(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	h.fake.emit("permission.asked", map[string]any{"id": "per_x", "sessionID": id, "permission": "read", "patterns": []string{"a"}, "metadata": map[string]any{}, "always": []string{}})
	h.fake.emit("question.asked", map[string]any{"id": "que_x", "sessionID": id, "questions": []map[string]any{{"question": "q", "header": "h", "options": []map[string]any{}}}})
	h.fake.emit("question.asked", map[string]any{"id": "que_y", "sessionID": id, "questions": []map[string]any{{"question": "q", "header": "h", "options": []map[string]any{}}}})
	h.fake.emit("permission.replied", map[string]any{"sessionID": id, "requestID": "per_x", "reply": "reject"})
	h.fake.emit("question.replied", map[string]any{"sessionID": id, "requestID": "que_x", "answers": [][]string{{"yes"}}})
	h.fake.emit("question.rejected", map[string]any{"sessionID": id, "requestID": "que_y"})
	if event := sink.waitFor(t, "denied permission", isInteraction("per_x", agentapi.InteractionRejected)); event.Interaction.Resolution != "Denied" {
		t.Fatalf("resolution = %q", event.Interaction.Resolution)
	}
	if event := sink.waitFor(t, "answered question", isInteraction("que_x", agentapi.InteractionAnswered)); event.Interaction.Resolution != "Answered: yes" {
		t.Fatalf("resolution = %q", event.Interaction.Resolution)
	}
	sink.waitFor(t, "rejected question", isInteraction("que_y", agentapi.InteractionRejected))
	if err := conversation.Respond(t.Context(), "per_x", agentapi.Answer{Decision: "once"}); !errors.Is(err, agentapi.ErrInteractionGone) {
		t.Fatalf("Respond after reply = %v", err)
	}
}

func TestWebReconnectReconciles(t *testing.T) {
	h := newWebHarness(t)
	conversation, sink := h.open(t, "")
	id := conversation.ID()
	h.fake.emit("session.status", map[string]any{"sessionID": id, "status": map[string]any{"type": "busy"}})
	h.fake.emit("permission.asked", map[string]any{"id": "per_gone", "sessionID": id, "permission": "bash", "patterns": []string{"x"}, "metadata": map[string]any{}, "always": []string{}})
	h.fake.emit("permission.asked", map[string]any{"id": "per_kept", "sessionID": id, "permission": "bash", "patterns": []string{"y"}, "metadata": map[string]any{}, "always": []string{}})
	sink.waitFor(t, "kept permission", isInteraction("per_kept", agentapi.InteractionPending))
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_live", "messageID": "msg_r2", "sessionID": id, "type": "text", "text": "", "time": map[string]any{"start": 3}}})
	h.fake.emit("message.part.delta", map[string]any{"sessionID": id, "messageID": "msg_r2", "partID": "prt_live", "field": "text", "delta": "par"})
	h.barrier(t, sink, id)

	h.fake.mu.Lock()
	h.fake.permissions = []webPermissionRequest{{ID: "per_kept", SessionID: id, Permission: "bash", Patterns: []string{"y"}}}
	h.fake.questions = []map[string]any{{"id": "que_new", "sessionID": id, "questions": []map[string]any{{"question": "q", "header": "h", "options": []map[string]any{}}}}}
	h.fake.messages[id] = []fakeWebMessage{
		webTestMessage("msg_r1", "user", 1000, webTestText("prt_prompt", "msg_r1", "do it")),
		webTestMessage("msg_r2", "assistant", 2000, map[string]any{"id": "prt_live", "messageID": "msg_r2", "type": "text", "text": "partial answer", "time": map[string]any{"start": 3}}),
	}
	h.fake.mu.Unlock()
	h.fake.dropStreams()
	h.fake.waitStreams(t, 2)

	sink.waitFor(t, "missed user item", func(event agentapi.Event) bool {
		return event.Kind == agentapi.EventItem && event.Item.ID == "prt_prompt" && event.Item.Kind == agentapi.ItemUser && event.Item.Text == "do it"
	})
	sink.waitFor(t, "resynced live text", func(event agentapi.Event) bool {
		return event.Kind == agentapi.EventItem && event.Item.ID == "prt_live" && event.Item.Text == "partial answer"
	})
	sink.waitFor(t, "vanished permission expired", isInteraction("per_gone", agentapi.InteractionExpired))
	sink.waitFor(t, "new question", isInteraction("que_new", agentapi.InteractionPending))
	sink.waitFor(t, "turn completed during gap", isTurn(agentapi.TurnCompleted))
	for _, event := range sink.snapshot() {
		if isInteraction("per_kept", agentapi.InteractionExpired)(event) {
			t.Fatal("still-pending permission was expired")
		}
	}
	// Deltas already covered by the snapshot are dropped until the part's
	// next full update, so text is never duplicated.
	h.fake.emit("message.part.delta", map[string]any{"sessionID": id, "messageID": "msg_r2", "partID": "prt_live", "field": "text", "delta": " answer"})
	h.fake.emit("message.part.updated", map[string]any{"sessionID": id, "part": map[string]any{"id": "prt_live", "messageID": "msg_r2", "sessionID": id, "type": "text", "text": "partial answer done", "time": map[string]any{"start": 3, "end": 4}}})
	h.barrier(t, sink, id)
	deltas := 0
	for _, event := range sink.snapshot() {
		if event.Kind == agentapi.EventDelta && event.Delta.ItemID == "prt_live" {
			deltas++
		}
	}
	if deltas != 1 {
		t.Fatalf("prt_live deltas = %d, want only the pre-gap delta", deltas)
	}
}

func TestWebStreamLossExitsAndLaterOpenStartsFresh(t *testing.T) {
	h := newWebHarness(t)
	h.provider.streamWindow = 300 * time.Millisecond
	conversation, sink := h.open(t, "")
	h.fake.mu.Lock()
	h.fake.streamDown = true
	h.fake.mu.Unlock()
	h.fake.dropStreams()
	event := sink.waitFor(t, "exit", func(event agentapi.Event) bool { return event.Kind == agentapi.EventExit })
	if !strings.Contains(event.Error, "event stream") || strings.Contains(event.Error, fakeWebPassword) {
		t.Fatalf("exit error = %q", event.Error)
	}
	if err := conversation.Send(t.Context(), "hello"); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("Send after exit = %v", err)
	}
	h.fake.mu.Lock()
	h.fake.streamDown = false
	h.fake.mu.Unlock()
	h.open(t, "")
	if starts, stops := h.counts(); starts != 2 || stops != 1 {
		t.Fatalf("starts/stops = %d/%d, want a fresh server after the failed one stopped", starts, stops)
	}
}

func TestWebServerExitEmitsExit(t *testing.T) {
	h := newWebHarness(t)
	first, firstSink := h.open(t, "")
	_, secondSink := h.open(t, "")
	h.mu.Lock()
	close(h.done[0])
	h.mu.Unlock()
	for _, sink := range []*recordingSink{firstSink, secondSink} {
		event := sink.waitFor(t, "exit", func(event agentapi.Event) bool { return event.Kind == agentapi.EventExit })
		if !strings.Contains(event.Error, "exited unexpectedly") {
			t.Fatalf("exit error = %q", event.Error)
		}
	}
	if _, err := first.History(t.Context()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("History after exit = %v", err)
	}
	if err := first.Close(t.Context()); err != nil {
		t.Fatalf("Close after exit: %v", err)
	}
	h.open(t, "")
	if starts, _ := h.counts(); starts != 2 {
		t.Fatalf("starts = %d, want a new server for a later Open", starts)
	}
	if len(h.fake.requestsFor(http.MethodPost, "/session/ses_web001/prompt_async")) != 0 {
		t.Fatal("server exit resent a prompt")
	}
}

func TestWebSend(t *testing.T) {
	h := newWebHarness(t)
	conversation, _ := h.open(t, "")
	id := conversation.ID()
	promptPath := "/session/" + id + "/prompt_async"
	messageIDRE := regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`)
	var sentIDs []string
	lastPrompt := func() map[string]any {
		t.Helper()
		requests := h.fake.requestsFor(http.MethodPost, promptPath)
		var body map[string]any
		if err := json.Unmarshal([]byte(requests[len(requests)-1].Body), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	for range 2 {
		if err := conversation.Send(t.Context(), "hello"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		body := lastPrompt()
		messageID, _ := body["messageID"].(string)
		if !messageIDRE.MatchString(messageID) || fmt.Sprint(body["parts"]) != "[map[text:hello type:text]]" || len(body) != 2 {
			t.Fatalf("prompt body = %#v", body)
		}
		sentIDs = append(sentIDs, messageID)
	}
	if sentIDs[0] >= sentIDs[1] {
		t.Fatalf("message IDs not ascending: %q", sentIDs)
	}

	h.fake.mu.Lock()
	h.fake.promptMode = "reject"
	h.fake.mu.Unlock()
	err := conversation.Send(t.Context(), "secret prompt")
	if err == nil || errors.Is(err, agentapi.ErrSubmissionUncertain) || !strings.Contains(err.Error(), "400") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("rejected Send = %v", err)
	}

	h.fake.mu.Lock()
	h.fake.promptMode = ""
	h.fake.statuses[id] = webSessionStatus{Type: "busy"}
	h.fake.mu.Unlock()
	before := len(h.fake.requestsFor(http.MethodPost, promptPath))
	if err := conversation.Send(t.Context(), "hello"); !errors.Is(err, agentapi.ErrBusy) {
		t.Fatalf("busy Send = %v", err)
	}
	if len(h.fake.requestsFor(http.MethodPost, promptPath)) != before {
		t.Fatal("busy Send posted a prompt")
	}

	h.fake.mu.Lock()
	delete(h.fake.statuses, id)
	h.fake.promptMode = "drop"
	h.fake.mu.Unlock()
	if err := conversation.Send(t.Context(), "hello"); !errors.Is(err, agentapi.ErrSubmissionUncertain) {
		t.Fatalf("dropped Send = %v", err)
	}
	if got := len(h.fake.requestsFor(http.MethodPost, promptPath)); got != before+1 {
		t.Fatalf("prompt posts = %d, want exactly one more (never retried)", got-before)
	}

	h.fake.mu.Lock()
	h.fake.promptMode = "drop-after-save"
	h.fake.mu.Unlock()
	if err := conversation.Send(t.Context(), "hello"); err != nil {
		t.Fatalf("dropped-but-saved Send = %v", err)
	}
	saved := lastPrompt()["messageID"].(string)
	if checks := h.fake.requestsFor(http.MethodGet, "/session/"+id+"/message/"+saved); len(checks) != 1 {
		t.Fatalf("reconciliation reads = %d, want one", len(checks))
	}

	h.fake.mu.Lock()
	h.fake.promptMode = ""
	delete(h.fake.sessions, id)
	h.fake.mu.Unlock()
	if err := conversation.Send(t.Context(), "hello"); !errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("missing-session Send = %v", err)
	}
}

func TestWebHistoryPaginatesLargeSessions(t *testing.T) {
	h := newWebHarness(t)
	conversation, _ := h.open(t, "")
	id := conversation.ID()
	large := strings.Repeat("y", 2<<20)
	var want []string
	for index := range 60 {
		messageID := fmt.Sprintf("msg_%04d", index)
		role := "user"
		if index%2 == 1 {
			role = "assistant"
		}
		text := webTestText(fmt.Sprintf("prt_%04d", index), messageID, fmt.Sprintf("text %d", index))
		parts := []map[string]any{text}
		want = append(want, text["id"].(string))
		if index == 31 {
			parts = append(parts, map[string]any{"id": "prt_big", "messageID": messageID, "type": "tool", "callID": "c", "tool": "read", "state": map[string]any{"status": "completed", "input": map[string]any{}, "output": large, "title": "big", "metadata": map[string]any{}, "time": map[string]any{"start": 1, "end": 2}}})
			parts = append(parts, map[string]any{"id": "prt_step", "messageID": messageID, "type": "step-start"})
			want = append(want, "prt_big")
		}
		h.fake.addMessage(id, webTestMessage(messageID, role, int64(1000+index), parts...))
	}
	recorded, err := conversation.History(t.Context())
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	var got []string
	for _, item := range recorded.Items {
		got = append(got, item.ID)
		if item.ID == "prt_big" && len(item.Tool.Output) > webMaxDisplayBytes {
			t.Fatalf("tool output not truncated: %d", len(item.Tool.Output))
		}
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("history order = %v", got)
	}
	if items := recorded.Items; items[0].Kind != agentapi.ItemUser || items[1].Kind != agentapi.ItemAssistant {
		t.Fatalf("history roles = %s %s", items[0].Kind, items[1].Kind)
	}
	reads := h.fake.requestsFor(http.MethodGet, "/session/"+id+"/message")
	if len(reads) != 3 || reads[0].Query != "limit=25" || reads[1].Query != "before=cursor-35&limit=25" {
		t.Fatalf("history reads = %#v", reads)
	}

	h.fake.mu.Lock()
	delete(h.fake.sessions, id)
	h.fake.mu.Unlock()
	if _, err := conversation.History(t.Context()); !errors.Is(err, agentapi.ErrConversationNotFound) {
		t.Fatalf("History of deleted session = %v", err)
	}
}

func TestWebDiffCancelAndClose(t *testing.T) {
	h := newWebHarness(t)
	first, _ := h.open(t, "")
	second, _ := h.open(t, "")
	h.fake.mu.Lock()
	h.fake.diffs = []map[string]any{{"file": "a.go", "patch": "@@ -1 +1 @@", "additions": 3, "deletions": 1, "status": "modified"}}
	h.fake.mu.Unlock()
	diffs, err := first.Diff(t.Context())
	if err != nil || len(diffs) != 1 || diffs[0] != (agentapi.FileDiff{Path: "a.go", Status: "modified", Additions: 3, Deletions: 1, Patch: "@@ -1 +1 @@"}) {
		t.Fatalf("Diff = %#v, %v", diffs, err)
	}
	if err := first.Cancel(t.Context()); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if aborts := h.fake.requestsFor(http.MethodPost, "/session/"+first.ID()+"/abort"); len(aborts) != 1 {
		t.Fatalf("abort requests = %d", len(aborts))
	}

	if err := first.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, stops := h.counts(); stops != 0 {
		t.Fatal("server stopped while another conversation is open")
	}
	if err := first.Cancel(t.Context()); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("Cancel after Close = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := h.provider.Open(canceled, agentapi.OpenRequest{Workdir: h.fake.directory, Events: &recordingSink{}}); err == nil {
		t.Fatal("canceled Open succeeded")
	}
	if _, stops := h.counts(); stops != 0 {
		t.Fatal("canceled Open stopped the shared server")
	}
	if err := second.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, stops := h.counts(); stops == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server kept running after its last conversation closed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(h.fake.requestsFor(http.MethodPost, "/session/"+second.ID()+"/abort")) != 0 {
		t.Fatal("Close aborted the turn")
	}
	h.fake.mu.Lock()
	defer h.fake.mu.Unlock()
	for _, request := range h.fake.requests {
		if request.Method == http.MethodDelete {
			t.Fatalf("Close deleted provider state: %#v", request)
		}
	}
}

func TestNewAscendingMessageIDMatchesOpenCodeFormat(t *testing.T) {
	before := time.Now().UnixMilli()
	first, err := newAscendingMessageID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := newAscendingMessageID()
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now().UnixMilli()
	if !regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString(first) || first >= second {
		t.Fatalf("IDs = %q, %q", first, second)
	}
	// OpenCode's Identifier.timestamp: hex prefix / 4096, low 48 bits.
	value, err := strconv.ParseUint(first[4:16], 16, 64)
	if err != nil {
		t.Fatal(err)
	}
	mask := uint64(1)<<48 - 1
	low := (uint64(before) * 4096) & mask
	high := (uint64(after)*4096 + 4095) & mask
	if low <= high && (value < low || value > high) {
		t.Fatalf("encoded time %d outside [%d, %d]", value, low, high)
	}
}

func TestSSEForwardsAllEventsOnlyWhenRequested(t *testing.T) {
	stream := "data: {\"type\":\"message.updated\",\"properties\":{}}\n\ndata: {\"type\":\"session.created\",\"properties\":{}}\n\n"
	for _, all := range []bool{false, true} {
		client := &apiClient{allEvents: all}
		events := make(chan eventEnvelope, 4)
		err := client.readSSEEvents(t.Context(), bufio.NewReader(strings.NewReader(stream)), events)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("readSSEEvents: %v", err)
		}
		close(events)
		var types []string
		for event := range events {
			types = append(types, event.Type)
		}
		want := "[session.created]"
		if all {
			want = "[message.updated session.created]"
		}
		if fmt.Sprint(types) != want {
			t.Fatalf("allEvents=%v forwarded %v", all, types)
		}
	}
}
