package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/charmbracelet/x/ansi"
)

const unknownWorkspaceKey = "(unknown workspace)"

// workspaceKey returns the stable presentation key for a working directory.
// It deliberately does not resolve symlinks: rendering must not perform I/O or
// unexpectedly merge two paths that the user chose to keep distinct.
func workspaceKey(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return unknownWorkspaceKey
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(abs)
}

// projectSessions keeps the service's canonical order byte-for-byte when
// grouping is disabled. When enabled, it groups only inside each canonical
// lifecycle/pin partition, ordering workspaces by their first occurrence and
// retaining canonical order inside each workspace.
func projectSessions(canonical []adapter.Session, grouped bool) []adapter.Session {
	out := append([]adapter.Session(nil), canonical...)
	if !grouped || len(out) < 2 {
		return out
	}
	projected := make([]adapter.Session, 0, len(out))
	for start := 0; start < len(out); {
		end := start + 1
		for end < len(out) && samePartition(out[start], out[end]) {
			end++
		}
		order := make([]string, 0, end-start)
		groups := make(map[string][]adapter.Session, end-start)
		for _, sess := range out[start:end] {
			key := workspaceKey(sess.Cwd)
			if _, seen := groups[key]; !seen {
				order = append(order, key)
			}
			groups[key] = append(groups[key], sess)
		}
		for _, key := range order {
			projected = append(projected, groups[key]...)
		}
		start = end
	}
	return projected
}

func workspaceDisplayName(key string) string {
	if key == unknownWorkspaceKey {
		return "Unknown workspace"
	}
	name := filepath.Base(key)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return key
	}
	return name
}

func workspaceHeadingLine(key string, count, width int) string {
	label := " WORKSPACE " + displaytext.Sanitize(workspaceDisplayName(key))
	if key != unknownWorkspaceKey {
		label += " · " + displaytext.Sanitize(key)
	}
	right := fmt.Sprintf("%d", count)
	available := max(1, width-ansi.StringWidth(right)-2)
	return ansi.Truncate(label, available, "…") + "  " + right
}

type groupedRenderLine struct {
	text         string
	sessionIndex int
	workspace    bool
}

type groupedRenderContext struct {
	width, nameWidth, taskWidth int
	showTask                    bool
	live                        map[string]int
	warned                      map[string]bool
}

func (m Model) groupedSessionListLines(width, budget int, class LayoutClass) []string {
	if budget <= 0 {
		return nil
	}
	if len(m.sessions) == 0 {
		return takeLines([]string{m.renderSectionAtWidth("SESSIONS", "0", width), "  " + hintStyle.Render("no sessions")}, budget)
	}
	entries := m.groupedRenderEntries(width, class)
	return windowGroupedEntries(entries, m.selected, budget)
}

func (m Model) groupedRenderEntries(width int, class LayoutClass) []groupedRenderLine {
	closed := countClosedSessions(m.sessions)
	right := fmt.Sprintf("%d", len(m.sessions))
	if class == LayoutCompact && closed > 0 {
		right = fmt.Sprintf("%d · %d closed", len(m.sessions), closed)
	}
	entries := []groupedRenderLine{{text: m.renderSectionAtWidth("SESSIONS", right, width), sessionIndex: -1}}
	liveByWorkspace := liveWorkspaceCounts(m.sessions)
	warnedWorkspaces := make(map[string]bool)
	nameWidth, taskWidth, showTask := tableWidthsFor(width, class)
	ctx := groupedRenderContext{width: width, nameWidth: nameWidth, taskWidth: taskWidth, showTask: showTask, live: liveByWorkspace, warned: warnedWorkspaces}
	for start := 0; start < len(m.sessions); {
		partitionEnd := sessionPartitionEnd(m.sessions, start)
		if class != LayoutCompact {
			entries = append(entries, groupedRenderLine{text: m.renderSectionAtWidth(lifecycleLabel(m.sessions[start]), "", width), sessionIndex: -1})
		}
		entries = append(entries, m.workspacePartitionEntries(start, partitionEnd, ctx)...)
		start = partitionEnd
	}
	return entries
}

func windowGroupedEntries(entries []groupedRenderLine, selected, budget int) []string {
	selectedLine := selectedGroupedLine(entries, selected)
	start, end := visibleWindow(len(entries), selectedLine, budget)
	lines := make([]string, 0, end-start)
	for _, entry := range entries[start:end] {
		lines = append(lines, entry.text)
	}
	keepSelectedWorkspaceHeading(entries, lines, selectedLine, start, end)
	return lines
}

func selectedGroupedLine(entries []groupedRenderLine, selected int) int {
	for i, entry := range entries {
		if entry.sessionIndex == selected {
			return i
		}
	}
	return 0
}

func keepSelectedWorkspaceHeading(entries []groupedRenderLine, lines []string, selectedLine, start, end int) {
	if selectedLine < start || selectedLine >= end || len(lines) == 0 {
		return
	}
	for i := selectedLine - 1; i >= 0; i-- {
		if entries[i].workspace {
			if i < start {
				lines[0] = entries[i].text
			}
			return
		}
	}
}

func (m Model) workspacePartitionEntries(start, end int, ctx groupedRenderContext) []groupedRenderLine {
	var entries []groupedRenderLine
	for groupStart := start; groupStart < end; {
		key := workspaceKey(m.sessions[groupStart].Cwd)
		groupEnd := workspaceGroupEnd(m.sessions, groupStart, end, key)
		entries = append(entries, groupedRenderLine{text: workspaceHeadingLine(key, groupEnd-groupStart, ctx.width), sessionIndex: -1, workspace: true})
		if ctx.live[key] > 1 && !ctx.warned[key] {
			warning := fmt.Sprintf("  ⚠ %d sessions share this workspace", ctx.live[key])
			entries = append(entries, groupedRenderLine{text: ansi.Truncate(warnStyle.Render(warning), ctx.width, "…"), sessionIndex: -1})
			ctx.warned[key] = true
		}
		for i := groupStart; i < groupEnd; i++ {
			row := ansi.Truncate(renderRow(m.sessions[i], i == m.selected, ctx.nameWidth, ctx.taskWidth, ctx.showTask), ctx.width, "…")
			entries = append(entries, groupedRenderLine{text: row, sessionIndex: i})
		}
		groupStart = groupEnd
	}
	return entries
}

func countClosedSessions(sessions []adapter.Session) int {
	count := 0
	for _, sess := range sessions {
		if sess.Closed {
			count++
		}
	}
	return count
}

func liveWorkspaceCounts(sessions []adapter.Session) map[string]int {
	counts := make(map[string]int)
	for _, sess := range sessions {
		key := workspaceKey(sess.Cwd)
		if key != unknownWorkspaceKey && sess.ProcAlive == adapter.Alive && !sess.Closed {
			counts[key]++
		}
	}
	return counts
}

func sessionPartitionEnd(sessions []adapter.Session, start int) int {
	end := start + 1
	for end < len(sessions) && samePartition(sessions[start], sessions[end]) {
		end++
	}
	return end
}

func workspaceGroupEnd(sessions []adapter.Session, start, limit int, key string) int {
	end := start + 1
	for end < limit && workspaceKey(sessions[end].Cwd) == key {
		end++
	}
	return end
}

func lifecycleLabel(sess adapter.Session) string {
	if sess.Closed {
		return "CLOSED"
	}
	return "ACTIVE"
}
