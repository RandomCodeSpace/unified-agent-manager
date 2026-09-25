package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
