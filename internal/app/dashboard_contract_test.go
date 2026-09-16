package app

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/charmbracelet/x/ansi"
)

func TestDashboardGoldenGeometryMatrix(t *testing.T) {
	for _, size := range []struct {
		width, height int
	}{{120, 40}, {96, 20}, {80, 24}, {60, 15}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			m := dashboardFixture(size.width, size.height)
			m.hasLoaded = true
			view := ansi.Strip(m.View().Content)
			assertViewGeometry(t, view, size.width, size.height)
			for _, literal := range []string{"Agents", "Running", "Stopped", "Failed", "Attach", "Resume"} {
				if !strings.Contains(view, literal) {
					t.Fatalf("dashboard missing %q:\n%s", literal, view)
				}
			}
		})
	}
}

func TestDashboardSemanticRegionsAreBoundedAndDisjoint(t *testing.T) {
	for _, width := range []int{40, 60, 80, 96, 120} {
		m := dashboardFixture(width, 20)
		m.hasLoaded = true
		frame := m.buildDashboardFrame()
		regions := make(map[string]image.Rectangle, len(frame.nodes))
		for id := range frame.nodes {
			layer := frame.compositor.GetLayer(id)
			if layer == nil {
				t.Fatalf("width %d: semantic node %q has no Lip Gloss layer", width, id)
			}
			bounds := image.Rect(layer.GetX(), layer.GetY(), layer.GetX()+layer.Width(), layer.GetY()+layer.Height())
			if bounds.Empty() || bounds.Min.X < 0 || bounds.Min.Y < 0 || bounds.Max.X > frame.width || bounds.Max.Y > frame.height {
				t.Fatalf("width %d: node %q out of bounds: %v in %dx%d", width, id, bounds, frame.width, frame.height)
			}
			regions[id] = bounds
		}
		for leftID, left := range regions {
			for rightID, right := range regions {
				if leftID >= rightID {
					continue
				}
				if left.Overlaps(right) {
					t.Fatalf("width %d: actionable regions overlap: %s=%v %s=%v", width, leftID, left, rightID, right)
				}
			}
		}
	}
}

func TestDashboardSemanticNodeIDsUseStableActionGrammar(t *testing.T) {
	identity := sessionIdentity{agent: "codex", id: "session/with/slashes"}
	row := dashboardSessionNodeID(identity, "row")
	attach := dashboardSessionNodeID(identity, "attach")
	if !strings.HasPrefix(row, "session/") || !strings.HasSuffix(row, "/row") || strings.Contains(row, "/action/") {
		t.Fatalf("row node ID = %q, want session/<key>/row", row)
	}
	if !strings.HasPrefix(attach, "session/") || !strings.HasSuffix(attach, "/action/attach") {
		t.Fatalf("action node ID = %q, want session/<key>/action/attach", attach)
	}
	if strings.Contains(row, identity.id) || strings.Contains(attach, identity.id) {
		t.Fatalf("node ID leaked unencoded session identity: row=%q action=%q", row, attach)
	}
}

func TestRefreshFailurePersistsAndRetryUsesExistingLoadPath(t *testing.T) {
	m := dashboardFixture(80, 20)
	m.hasLoaded = true
	m = m.handleSessionsLoaded(sessionsLoadedMsg{refresh: true, err: errors.New("store unavailable")})
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Refresh failed: store unavailable") || !strings.Contains(view, "Retry") || !strings.Contains(view, "release-check") {
		t.Fatalf("refresh failure did not preserve roster and Retry:\n%s", view)
	}
	m.expireMessage(time.Now().Add(24 * time.Hour))
	if m.refreshError == "" {
		t.Fatal("refresh failure expired like a transient message")
	}

	frame := m.buildDashboardFrame()
	intent := pointerIntentFor(t, frame, dashboardRetry, sessionIdentity{})
	next, cmd := m.Update(intent)
	retrying := next.(Model)
	if cmd == nil || !retrying.loading || retrying.refreshError != "" {
		t.Fatalf("pointer Retry did not enter existing load path: loading=%v error=%q cmd=%v", retrying.loading, retrying.refreshError, cmd)
	}
	if got := ansi.Strip(retrying.View().Content); !strings.Contains(got, "Refreshing") || !strings.Contains(got, "release-check") {
		t.Fatalf("Retry displaced last good roster:\n%s", got)
	}

	m = m.handleSessionsLoaded(sessionsLoadedMsg{refresh: true, err: errors.New("again")})
	model, cmd := m.handleKey(keyMsg("r"))
	retrying = model.(Model)
	if cmd == nil || !retrying.loading || retrying.refreshError != "" {
		t.Fatalf("keyboard Retry diverged from pointer path: loading=%v error=%q cmd=%v", retrying.loading, retrying.refreshError, cmd)
	}

	retrying = retrying.handleSessionsLoaded(sessionsLoadedMsg{refresh: true})
	if retrying.refreshError != "" || retrying.loading {
		t.Fatalf("successful refresh did not settle Retry state: loading=%v error=%q", retrying.loading, retrying.refreshError)
	}
}

func TestCurrentPrimaryPointerInvokesExactExistingAttachOnce(t *testing.T) {
	sess := adapter.Session{ID: "exact-id", AgentType: "fake", DisplayName: "exact", ProcAlive: adapter.Alive}
	fake := &svcFakeAdapter{name: "fake", available: true, sessions: []adapter.Session{sess}}
	m := NewWithDeps(nil, adapter.NewRegistry([]adapter.AgentAdapter{fake}))
	m.sessions = []adapter.Session{sess}
	m.loading = false
	m.hasLoaded = true
	m = m.handleWindowSize(tea.WindowSizeMsg{Width: 80, Height: 20})
	intent := pointerIntentFor(t, m.buildDashboardFrame(), dashboardPrimary, sessionIdentity{agent: "fake", id: "exact-id"})
	_, cmd := m.Update(intent)
	if cmd == nil {
		t.Fatal("current primary pointer produced no command")
	}
	msg := cmd()
	if _, ok := msg.(attachSpecMsg); !ok {
		t.Fatalf("primary pointer returned %T, want attachSpecMsg", msg)
	}
	if fake.attachCount != 1 || fake.attachedID != "exact-id" {
		t.Fatalf("attach calls=%d id=%q, want one exact-id", fake.attachCount, fake.attachedID)
	}
}

func dashboardBenchmarkFixture(count int) Model {
	now := time.Date(2026, time.August, 28, 12, 4, 0, 0, time.UTC)
	m := NewWithDeps(nil, nil)
	m.now = func() time.Time { return now }
	m.hasLoaded = true
	m.loading = false
	m.sessions = make([]adapter.Session, count)
	for i := range m.sessions {
		state := adapter.Exited
		if i%3 == 0 {
			state = adapter.Alive
		}
		m.sessions[i] = adapter.Session{
			ID:          fmt.Sprintf("session-%04d", i),
			AgentType:   []string{"codex", "claude", "opencode"}[i%3],
			DisplayName: fmt.Sprintf("agent-task-%04d", i),
			Prompt:      "review the current change without moving the target",
			Cwd:         fmt.Sprintf("/work/project-%03d", i%100),
			ProcAlive:   state,
			CreatedAt:   now.Add(-time.Duration(i) * time.Minute),
		}
	}
	return m.handleWindowSize(tea.WindowSizeMsg{Width: 120, Height: 40})
}

var benchmarkDashboardFrame dashboardFrame

func BenchmarkDashboardFrame1000(b *testing.B) {
	m := dashboardBenchmarkFixture(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		benchmarkDashboardFrame = m.buildDashboardFrame()
	}
}

func BenchmarkDashboardPointer1000(b *testing.B) {
	m := dashboardBenchmarkFixture(1000)
	intent := dashboardPointerIntent{
		frameSignature: m.dashboardSignature(),
		width:          m.width,
		height:         m.height,
		action:         dashboardMove,
		delta:          0,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_, _ = m.handleDashboardPointer(intent)
	}
}
