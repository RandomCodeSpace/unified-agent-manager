package web

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestToolOutputStreamNegotiationAndPayload(t *testing.T) {
	for _, query := range []string{"", "&tool_output=delta"} {
		t.Run(query, func(t *testing.T) {
			ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("test")}}})
			sum, conv := createSession(t, ts.m, ts.prov)
			conv.EmitSubagent(agentapi.Subagent{ID: "helper", Name: "Helper", Status: agentapi.SubagentRunning})
			srv := httptest.NewServer(ts.srv)
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/events?session="+sum.ID+query, nil)
			req.AddCookie(&http.Cookie{Name: cookieName, Value: validCookie(req.Host)})
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			reader := bufio.NewReader(resp.Body)
			bytes := 0
			next := func() frame {
				t.Helper()
				for {
					var raw strings.Builder
					for {
						line, err := reader.ReadString('\n')
						if err != nil {
							t.Fatal(err)
						}
						if line == "\n" {
							break
						}
						raw.WriteString(line)
					}
					if strings.HasPrefix(raw.String(), "event:") {
						bytes += raw.Len()
						return parseFrame(t, []byte(raw.String()))
					}
				}
			}
			if f := next(); f.event != "snapshot" {
				t.Fatalf("first frame = %s", f.event)
			}
			start := time.Now().UTC()
			emit := func(output string, status agentapi.ToolStatus) {
				conv.EmitItem(agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, AgentID: "helper", Time: start, Tool: &agentapi.ToolCall{Name: "bash", Status: status, Output: output}})
			}
			emit("", agentapi.ToolRunning)
			if f := next(); f.event != "item" {
				t.Fatalf("start = %s", f.event)
			}
			bytes = 0
			chunk := strings.Repeat("x", 1024)
			var lastSeq uint64
			for n := 1; n <= 64; n++ {
				emit(strings.Repeat(chunk, n), agentapi.ToolRunning)
				f := next()
				if f.seq <= lastSeq {
					t.Fatal("sequence did not advance")
				}
				lastSeq = f.seq
				if query == "" {
					if f.event != "item" {
						t.Fatalf("legacy frame = %s", f.event)
					}
					var it agentapi.Item
					if err := json.Unmarshal(f.data["item"], &it); err != nil || len(it.Tool.Output) != n*1024 {
						t.Fatalf("legacy output %d", n)
					}
				} else {
					if f.event != "tool_output" {
						t.Fatalf("frame %d = %s, want tool_output", n, f.event)
					}
					var text, agent string
					_ = json.Unmarshal(f.data["text"], &text)
					_ = json.Unmarshal(f.data["agent_id"], &agent)
					if text != chunk || agent != "helper" {
						t.Fatalf("delta %d: %d bytes, agent %q", n, len(text), agent)
					}
				}
			}
			if query != "" && bytes > 90<<10 {
				t.Fatalf("64 KiB of output used %d wire bytes", bytes)
			}
			t.Logf("query=%q: 64 KiB output, %d SSE bytes", query, bytes)
			// Non-prefix replacements and completion remain full items.
			for _, status := range []agentapi.ToolStatus{agentapi.ToolRunning, agentapi.ToolCompleted} {
				emit("rewritten", status)
				if f := next(); f.event != "item" {
					t.Fatalf("rewrite/final = %s", f.event)
				}
			}
			// A subagent opened later receives its complete retained output.
			d, err := ts.m.Subagent(sum.ID, "helper")
			if err != nil || len(d.Items) != 1 || d.Items[0].Tool.Output != "rewritten" {
				t.Fatalf("retained output: %+v, %v", d, err)
			}
		})
	}
}

func TestToolOutputSnapshotAndMetadata(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, conv := createSession(t, m, prov)
	sub, _, err := m.subscribe(sum.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(sub)
	emit := func(output, title string) {
		conv.EmitItem(agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Title: title, Status: agentapi.ToolRunning, Output: output}})
	}
	emit("a", "first")
	frameOf(t, sub, "item")
	emit("ab", "first")
	delta := frameOf(t, sub, "tool_output")
	// Reconnecting after a delta starts with the full output and covers that sequence.
	reconnected, raw, err := m.subscribe(sum.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(reconnected)
	f := parseFrame(t, raw)
	var detail SessionDetail
	decodeField(t, f, "session", &detail)
	if f.seq < delta.seq || len(detail.Items) != 1 || detail.Items[0].Tool.Output != "ab" {
		t.Fatalf("reconnect = %+v at %d, delta at %d", detail.Items, f.seq, delta.seq)
	}
	// An identical snapshot emits nothing; metadata changes must still be full items.
	emit("ab", "first")
	emit("abc", "second")
	for _, target := range []*Subscriber{sub, reconnected} {
		select {
		case raw := <-target.Frames():
			f := parseFrame(t, raw)
			var item agentapi.Item
			decodeField(t, f, "item", &item)
			if f.event != "item" || item.Tool == nil || item.Tool.Title != "second" || item.Tool.Output != "abc" || f.seq <= detail.Seq {
				t.Fatalf("metadata update = %s %+v", f.event, item)
			}
		case <-time.After(time.Second):
			t.Fatal("metadata update missing")
		}
	}
}

func TestToolOutputUnchangedRowNotRepublished(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	main, _, err := m.subscribeView(sum.ID, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(main)
	start := time.Now().UTC()
	upsert := func(it agentapi.Item) {
		it.Time = start
		m.mu.Lock()
		m.upsertItemLocked(m.sessions[sum.ID], it, true)
		m.mu.Unlock()
	}
	tool := func(output string, status agentapi.ToolStatus) agentapi.Item {
		return agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Input: "pwd", Status: status, Output: output}}
	}
	upsert(agentapi.Item{ID: "reply", Kind: agentapi.ItemAssistant, Text: "done"})
	upsert(tool("one", agentapi.ToolCompleted))
	frameOf(t, main, "item")
	frameOf(t, main, "item")
	// Identical items and output the row does not show leave it as it is.
	for _, it := range []agentapi.Item{{ID: "reply", Kind: agentapi.ItemAssistant, Text: "done"}, tool("one", agentapi.ToolCompleted), tool("two", agentapi.ToolCompleted)} {
		upsert(it)
		noFrame(t, main, "unchanged row")
	}
	upsert(tool("two", agentapi.ToolFailed))
	var row compactItem
	decodeField(t, frameOf(t, main, "item"), "item", &row)
	if row.ID != "tool" || row.Tool == nil || row.Tool.Status != agentapi.ToolFailed {
		t.Fatalf("changed row = %+v", row)
	}
}

func TestToolOutputSlidingWindowPaced(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	clock := time.Now()
	m.mu.Lock()
	m.now = func() time.Time { return clock }
	s := m.sessions[sum.ID]
	m.mu.Unlock()
	start := clock.UTC()
	emit := func(id, output string, status agentapi.ToolStatus) {
		m.mu.Lock()
		m.upsertItemLocked(s, agentapi.Item{ID: id, Kind: agentapi.ItemTool, Time: start, Tool: &agentapi.ToolCall{Name: "bash", Input: "seq 9", Status: status, Output: output}}, true)
		m.mu.Unlock()
	}
	emit("tool", "", agentapi.ToolRunning)
	main, _, err := m.subscribeView(sum.ID, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(main)
	detail, _, err := m.subscribeDetail(sum.ID, "", []bodyRef{{"", "tool"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(detail)
	delta := func(want string) {
		t.Helper()
		var text string
		decodeField(t, frameOf(t, detail, "body_output"), "text", &text)
		if text != want {
			t.Fatalf("delta %q, want %q", text, want)
		}
	}
	body := func(want string, status agentapi.ToolStatus) {
		t.Helper()
		var it agentapi.Item
		decodeField(t, frameOf(t, detail, "body"), "item", &it)
		if it.Tool.Output != want || it.Tool.Status != status {
			t.Fatalf("body %q %s, want %q %s", it.Tool.Output, it.Tool.Status, want, status)
		}
	}
	// Growing output is a delta.
	emit("tool", "row 1\n", agentapi.ToolRunning)
	frameOf(t, main, "item")
	delta("row 1\n")
	// The first window that replaces it is sent at once, later ones within
	// the interval wait, growth included, and the timer sends the latest.
	emit("tool", "row 2\n", agentapi.ToolRunning)
	body("row 2\n", agentapi.ToolRunning)
	for _, window := range []string{"row 3\n", "row 4\n", "row 4\nrow 5\n"} {
		emit("tool", window, agentapi.ToolRunning)
		noFrame(t, detail, "held window")
	}
	body("row 4\nrow 5\n", agentapi.ToolRunning)
	noFrame(t, main, "unchanged row")
	// Then growth is a delta again, and a window after the interval is sent at once.
	emit("tool", "row 4\nrow 5\nrow 6\n", agentapi.ToolRunning)
	delta("row 6\n")
	m.mu.Lock()
	clock = clock.Add(previewInterval)
	m.mu.Unlock()
	emit("tool", "row 7\n", agentapi.ToolRunning)
	body("row 7\n", agentapi.ToolRunning)
	// Completion sends the final output at once and stops the pending timer.
	emit("tool", "row 8\n", agentapi.ToolRunning)
	noFrame(t, detail, "held window")
	m.mu.Lock()
	state := s.outputs[itemKey("", "tool")]
	m.mu.Unlock()
	if state == nil || state.timer == nil {
		t.Fatal("window not held")
	}
	emit("tool", "row 8\nexit 0", agentapi.ToolCompleted)
	body("row 8\nexit 0", agentapi.ToolCompleted)
	frameOf(t, main, "item")
	m.mu.Lock()
	if len(s.outputs) != 0 || state.timer.Stop() {
		t.Fatal("completion left pacing state/timer")
	}
	m.mu.Unlock()
	time.Sleep(2 * previewInterval)
	noFrame(t, detail, "stopped timer")
	// Dropping the Task's history stops a pending timer too.
	emit("other", "a", agentapi.ToolRunning)
	emit("other", "b", agentapi.ToolRunning)
	emit("other", "c", agentapi.ToolRunning)
	m.mu.Lock()
	defer m.mu.Unlock()
	state = s.outputs[itemKey("", "other")]
	if state == nil || state.timer == nil {
		t.Fatal("second window not held")
	}
	m.dropHistoryLocked(s)
	if len(s.outputs) != 0 || state.timer.Stop() {
		t.Fatal("history drop left pacing state/timer")
	}
}

// A running shell's row shows its tail, which changes with every chunk: the
// row goes out at most once per previewInterval, the latest on a trailing
// timer, while the output's deltas stay immediate. The completion's row has
// no tail.
func TestToolOutputTailRowPaced(t *testing.T) {
	m, prov, _ := newTestManager(t)
	sum, _ := createSession(t, m, prov)
	base := time.Now()
	clock := base
	m.mu.Lock()
	m.now = func() time.Time { return clock }
	s := m.sessions[sum.ID]
	m.mu.Unlock()
	start := base.UTC()
	output, tail := "", []agentapi.OutputLine(nil)
	emit := func(status agentapi.ToolStatus) {
		m.mu.Lock()
		m.upsertItemLocked(s, agentapi.Item{ID: "tool", Kind: agentapi.ItemTool, Time: start, Tool: &agentapi.ToolCall{Name: "bash", Input: "make", Status: status, Output: output, Tail: tail}}, true)
		m.mu.Unlock()
	}
	emit(agentapi.ToolRunning)
	main, _, err := m.subscribeView(sum.ID, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(main)
	detail, _, err := m.subscribeDetail(sum.ID, "", []bodyRef{{"", "tool"}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Unsubscribe(detail)
	key := itemKey("", "tool")
	// due reports whether a held row is due on the clock; settle waits for
	// its timer, which runs on real time.
	due := func() bool {
		m.mu.Lock()
		defer m.mu.Unlock()
		state := s.outputs[key]
		return state != nil && state.timer != nil && !clock.Before(state.rowAt.Add(previewInterval))
	}
	settle := func() {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			m.mu.Lock()
			state := s.outputs[key]
			pending := state != nil && state.timer != nil
			m.mu.Unlock()
			if !pending {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("the held row was never sent")
			}
		}
	}
	// 20 chunks in a second.
	for i := range 20 {
		m.mu.Lock()
		clock = base.Add(time.Duration(i) * 50 * time.Millisecond)
		m.mu.Unlock()
		if due() {
			settle()
		}
		line := fmt.Sprintf("line %d", i)
		output += line + "\n"
		tail = append(slices.Clone(tail[max(0, len(tail)-9):]), agentapi.OutputLine{Text: line, Err: i%2 == 1})
		emit(agentapi.ToolRunning)
		var text string
		decodeField(t, frameOf(t, detail, "body_output"), "text", &text)
		if text != line+"\n" {
			t.Fatalf("delta %d = %q", i, text)
		}
	}
	settle()
	var rows []frame
	for {
		select {
		case raw := <-main.Frames():
			if f := parseFrame(t, raw); f.event == "item" {
				rows = append(rows, f)
			}
			continue
		default:
		}
		break
	}
	if len(rows) < 2 || len(rows) > 5 {
		t.Fatalf("20 chunks in 1s sent %d rows, want 2 to 5", len(rows))
	}
	t.Logf("20 chunks in 1s: %d rows", len(rows))
	var row compactItem
	decodeField(t, rows[len(rows)-1], "item", &row)
	if row.Tool == nil || !row.Tool.HasOutput || row.Tool.Output != "" || !reflect.DeepEqual(row.Tool.Tail, tail) {
		t.Fatalf("last row = %+v, want the latest tail %+v", row.Tool, tail)
	}
	// The trailing row raised the sequence the browser's body waits for; the
	// body it holds from the deltas is current.
	if current := frameOf(t, detail, "body_current"); current.seq <= rows[len(rows)-1].seq {
		t.Fatalf("body_current at %d, row at %d", current.seq, rows[len(rows)-1].seq)
	}
	// Past the output cap only the tail moves: a row sent at once still
	// leaves the browser's body current.
	m.mu.Lock()
	clock = clock.Add(time.Second)
	m.mu.Unlock()
	tail = append(slices.Clone(tail[1:]), agentapi.OutputLine{Text: "capped"})
	emit(agentapi.ToolRunning)
	capped := frameOf(t, main, "item")
	if current := frameOf(t, detail, "body_current"); current.seq <= capped.seq {
		t.Fatalf("tail-only row at %d, body_current at %d", capped.seq, current.seq)
	}
	output, tail = output+"exit 0", nil
	emit(agentapi.ToolCompleted)
	row = compactItem{}
	decodeField(t, frameOf(t, main, "item"), "item", &row)
	if row.Tool == nil || row.Tool.Status != agentapi.ToolCompleted || row.Tool.Tail != nil {
		t.Fatalf("completed row = %+v", row.Tool)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(s.outputs) != 0 {
		t.Fatal("completion left pacing state")
	}
}
