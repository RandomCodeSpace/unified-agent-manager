package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func TestTranscriptRetentionReachesSubscribers(t *testing.T) {
	for _, mode := range []string{"item-count", "byte-budget-delta"} {
		t.Run(mode, func(t *testing.T) {
			m, prov, _ := newTestManager(t)
			sum, conv := createSession(t, m, prov)
			sub, _, err := m.Subscribe(sum.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Unsubscribe(sub)
			retained := map[string]agentapi.Item{}
			trims := 0
			var lastSeq uint64
			drain := func() {
				t.Helper()
				for {
					select {
					case raw := <-sub.Frames():
						sub.Sent(raw)
						f := parseFrame(t, raw)
						if f.seq <= lastSeq {
							t.Fatal("events out of order")
						}
						lastSeq = f.seq
						switch f.event {
						case "item":
							var it agentapi.Item
							decodeField(t, f, "item", &it)
							retained[itemKey(it.AgentID, it.ID)] = it
						case "delta":
							var id, agent, text string
							decodeField(t, f, "item_id", &id)
							decodeField(t, f, "text", &text)
							if _, ok := f.data["agent_id"]; ok {
								decodeField(t, f, "agent_id", &agent)
							}
							key := itemKey(agent, id)
							it := retained[key]
							it.Text += text
							retained[key] = it
						case "items_trimmed":
							var ids []struct {
								ID      string `json:"id"`
								AgentID string `json:"agent_id"`
							}
							decodeField(t, f, "items", &ids)
							for _, id := range ids {
								delete(retained, itemKey(id.AgentID, id.ID))
							}
							trims++
						}
					default:
						return
					}
				}
			}
			count, text := 6000, "small output"
			if mode == "byte-budget-delta" {
				count, text = 21, strings.Repeat("x", 3<<20)
			}
			for i := 0; i < count; i++ {
				agent := ""
				if i%2 != 0 {
					agent = "helper"
				}
				conv.EmitItem(agentapi.Item{ID: fmt.Sprint(i / 2), AgentID: agent, Kind: agentapi.ItemAssistant, Text: text})
				drain()
			}
			if mode == "byte-budget-delta" {
				// Growing the oldest item evicts that very item. The trim must
				// follow the delta so the browser cannot recreate it afterwards.
				conv.EmitDelta("0", agentapi.ItemAssistant, strings.Repeat("y", 1<<20))
				drain()
			}
			m.mu.Lock()
			s := m.sessions[sum.ID]
			want := map[string]agentapi.Item{}
			for _, it := range s.items {
				want[itemKey(it.AgentID, it.ID)] = it
			}
			truncated := s.truncated
			m.mu.Unlock()
			if !truncated || trims == 0 || len(retained) != len(want) {
				t.Fatalf("browser retained %d, server %d, trims %d, truncated %v", len(retained), len(want), trims, truncated)
			}
			for key, it := range want {
				got := retained[key]
				if got.ID != it.ID || got.AgentID != it.AgentID || got.Kind != it.Kind || got.Text != it.Text || !got.Time.Equal(it.Time) {
					t.Fatalf("browser differs from server for %q", key)
				}
			}
			t.Logf("%s: %d input items, %d retained, %d trim events", mode, count, len(retained), trims)
		})
	}
}
