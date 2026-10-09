package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

func textSession() (*Manager, *webSession) {
	return &Manager{now: time.Now}, &webSession{id: "owned", itemIdx: map[string]int{}}
}

func appendText(m *Manager, s *webSession, agent, id string, kind agentapi.ItemKind, text string) {
	m.applyDeltaLocked(s, agentapi.Delta{AgentID: agent, ItemID: id, Kind: kind, Text: text})
}

func TestDeltaTextSnapshotsRemainImmutableAndAgentsSeparate(t *testing.T) {
	m, s := textSession()
	for _, agent := range []string{"", "helper"} {
		for _, kind := range []agentapi.ItemKind{agentapi.ItemAssistant, agentapi.ItemReasoning} {
			id := string(kind)
			appendText(m, s, agent, id, kind, agent+id)
			appendText(m, s, agent, id, kind, " first")
		}
	}
	beforeMain, beforeHelper := s.agentItems(""), s.agentItems("helper")
	for _, agent := range []string{"", "helper"} {
		for _, kind := range []agentapi.ItemKind{agentapi.ItemAssistant, agentapi.ItemReasoning} {
			appendText(m, s, agent, string(kind), kind, " within capacity")
			appendText(m, s, agent, string(kind), kind, strings.Repeat("界", 32<<10))
		}
	}
	for _, snapshot := range [][]agentapi.Item{beforeMain, beforeHelper} {
		for _, it := range snapshot {
			if it.Text != it.AgentID+it.ID+" first" {
				t.Fatalf("old snapshot changed for %q: %q", itemKey(it.AgentID, it.ID), it.Text)
			}
		}
	}
	for _, it := range s.items {
		if it.Text != it.AgentID+it.ID+" first within capacity"+strings.Repeat("界", 32<<10) {
			t.Fatalf("wrong current text for %q", itemKey(it.AgentID, it.ID))
		}
	}
	if len(s.textBuffers) != 4 {
		t.Fatalf("buffers=%d for four agent-qualified items", len(s.textBuffers))
	}
}

func TestDeltaTextReplacementAndHistoryDiscardOldAccumulation(t *testing.T) {
	m, s := textSession()
	for _, agent := range []string{"", "helper"} {
		appendText(m, s, agent, "same", agentapi.ItemAssistant, "live")
		appendText(m, s, agent, "same", agentapi.ItemAssistant, " suffix")
	}
	before := s.agentItems("")[0]
	m.upsertItemLocked(s, agentapi.Item{ID: "same", Kind: agentapi.ItemAssistant, Text: "authoritative", Time: time.Now()}, false)
	if s.textBuffers["same"] != nil {
		t.Fatal("replacement retained the previous accumulation buffer")
	}
	appendText(m, s, "", "same", agentapi.ItemAssistant, " next")
	if got := s.agentItems("")[0].Text; got != "authoritative next" || before.Text != "live suffix" {
		t.Fatalf("replacement text=%q old snapshot=%q", got, before.Text)
	}
	m.applyHistoryLocked(s, agentapi.History{Items: []agentapi.Item{{ID: "same", Kind: agentapi.ItemAssistant, Text: "record"}}}, false)
	if s.textBuffers["same"] != nil || s.textBuffers[itemKey("helper", "same")] == nil {
		t.Fatal("history did not reset its covered item and preserve the omitted live item")
	}
	appendText(m, s, "", "same", agentapi.ItemAssistant, " after read")
	appendText(m, s, "helper", "same", agentapi.ItemAssistant, " after read")
	if got := s.agentItems("")[0].Text; got != "record after read" {
		t.Fatalf("record text=%q", got)
	}
	if got := s.agentItems("helper")[0].Text; got != "live suffix after read" {
		t.Fatalf("omitted live text=%q", got)
	}
}

func TestDeltaTextLimitKeepsUTF8AndDropsSpareCapacity(t *testing.T) {
	for _, tc := range []struct{ name, add, suffix string }{
		{"rune crossing", "界tail", truncatedMarker},
		{"exact limit", "x", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, s := textSession()
			prefix := strings.Repeat("a", maxItemText-1)
			m.upsertItemLocked(s, agentapi.Item{ID: "reply", Kind: agentapi.ItemAssistant, Text: prefix}, false)
			appendText(m, s, "", "reply", agentapi.ItemAssistant, tc.add)
			text := s.items[0].Text
			if text != prefix+tc.suffix || !utf8.ValidString(text) || s.itemBytes != itemSize(s.items[0]) {
				t.Fatalf("bounded text length=%d bytes=%d", len(text), s.itemBytes)
			}
			if len(s.textBuffers) != 0 {
				t.Fatal("an item that cannot grow retained a builder's spare capacity")
			}
			appendText(m, s, "", "reply", agentapi.ItemAssistant, "ignored")
			if s.items[0].Text != text {
				t.Fatal("text grew after reaching the limit")
			}
		})
	}
}

func TestDeltaTextTrimAndHistoryBoundsRemoveBuffers(t *testing.T) {
	t.Run("count trim", func(t *testing.T) {
		m, s := textSession()
		appendText(m, s, "", "old", agentapi.ItemAssistant, "old")
		appendText(m, s, "", "old", agentapi.ItemAssistant, " suffix")
		for i := 0; i < maxItems-1; i++ {
			m.upsertItemLocked(s, agentapi.Item{ID: fmt.Sprint(i), Kind: agentapi.ItemAssistant}, false)
		}
		appendText(m, s, "helper", "kept", agentapi.ItemReasoning, "new")
		appendText(m, s, "helper", "kept", agentapi.ItemReasoning, " suffix")
		if s.textBuffers["old"] != nil || len(s.textBuffers) != 1 || s.textBuffers[itemKey("helper", "kept")] == nil || !s.truncated {
			t.Fatalf("buffers after trim=%d truncated=%v", len(s.textBuffers), s.truncated)
		}
	})
	t.Run("history bound", func(t *testing.T) {
		m, s := textSession()
		appendText(m, s, "", "old", agentapi.ItemAssistant, "old")
		appendText(m, s, "", "old", agentapi.ItemAssistant, " suffix")
		// JSON escaping makes the newest item exceed the history-frame budget.
		m.upsertItemLocked(s, agentapi.Item{ID: "large", Kind: agentapi.ItemAssistant, Text: strings.Repeat("\x00", maxHistoryBytes/6+1)}, false)
		m.boundHistoryLocked(s)
		if len(s.textBuffers) != 0 || !s.truncated {
			t.Fatalf("buffers after history bound=%d truncated=%v", len(s.textBuffers), s.truncated)
		}
	})
}

func TestDeltaTextClosedReleaseAndDeletionFreeBuffers(t *testing.T) {
	for _, action := range []string{"last viewer", "delete"} {
		t.Run(action, func(t *testing.T) {
			prov := agenttest.NewPager("fake", allCaps)
			m := startManager(t, openTestStore(t), prov)
			sum, conv := createSession(t, m, prov.Provider)
			conv.EmitDelta("reply", agentapi.ItemAssistant, "recorded")
			conv.EmitDelta("reply", agentapi.ItemAssistant, " suffix")
			sub, _, err := m.Subscribe(sum.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Unsubscribe(sub)
			m.mu.Lock()
			s := m.sessions[sum.ID]
			buffered := len(s.textBuffers)
			m.mu.Unlock()
			if buffered != 1 {
				t.Fatalf("before close buffers=%d", buffered)
			}
			prov.SetReadHook(func(context.Context, agentapi.ReadRequest) (agentapi.History, error) {
				return agentapi.History{Items: []agentapi.Item{{ID: "reply", Kind: agentapi.ItemAssistant, Text: "recorded suffix"}}}, nil
			})
			if _, err := m.Archive(sum.ID); err != nil {
				t.Fatal(err)
			}
			if action == "delete" {
				if err := m.Delete(sum.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				m.Unsubscribe(sub)
			}
			m.mu.Lock()
			remaining := len(s.textBuffers)
			m.mu.Unlock()
			if remaining != 0 {
				t.Fatalf("after %s buffers=%d", action, remaining)
			}
			if action == "last viewer" {
				if err := m.View(context.Background(), sum.ID); err != nil {
					t.Fatal(err)
				}
				if got := waitHistory(t, m, sum.ID, HistoryLoaded); len(got.Items) != 1 || got.Items[0].Text != "recorded suffix" {
					t.Fatalf("reread=%+v", got)
				}
			}
		})
	}
}
