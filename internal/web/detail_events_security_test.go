package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestDetailEventsKeepUntrustedBodiesInsideJSON(t *testing.T) {
	ts := newTestServer(t, ServerConfig{Assets: fstest.MapFS{"index.html": {Data: []byte("test")}}})
	sum, conv := createSession(t, ts.m, ts.prov)
	const text = "<script>alert(1)</script>\r\n\nevent: forged\ndata: {}"
	const itemID = "body<script>"
	conv.EmitItem(agentapi.Item{ID: itemID, Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Input: text, Output: text, Status: agentapi.ToolCompleted}})
	host := httptest.NewServer(ts.srv)
	defer host.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	interest, _ := json.Marshal([]string{"", itemID})
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, host.URL+"/api/events/detail?session="+sum.ID+"&item="+url.QueryEscape(string(interest)), nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: validCookie(req.Host)})
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("detail stream response: status=%d headers=%v", response.StatusCode, response.Header)
	}
	scanner := bufio.NewScanner(response.Body)
	event := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "<script>") || line == "event: forged" {
			t.Fatalf("unescaped event data: %q", line)
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
		}
		if event != "body" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload struct {
			Item agentapi.Item `json:"item"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Item.ID != itemID || payload.Item.Tool == nil || payload.Item.Tool.Input != text || payload.Item.Tool.Output != text {
			t.Fatalf("JSON changed original body: %+v", payload.Item)
		}
		return
	}
	t.Fatalf("detail body was not streamed: %v", scanner.Err())
}
