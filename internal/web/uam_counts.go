package web

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// uamCountsFile keeps counts of uam's own actions beside the store, for
// Settings → Session stats. Names only: never a Thread, path or text.
const uamCountsFile = "uam-counts.json"

// countsDelay is how long a count waits in memory before it is written; a
// crash loses at most that much, which the stats can afford.
var countsDelay = 10 * time.Second

type uamCounts struct {
	// writeMu orders the writes, so an older snapshot never lands last.
	writeMu sync.Mutex
	mu      sync.Mutex
	path    string
	n       map[string]int
	pending bool
}

// count adds one to name. It takes no Manager lock, so any caller may.
func (m *Manager) count(name string) { m.countN(name, 1) }

func (m *Manager) countN(name string, n int) {
	if n <= 0 {
		return
	}
	c := &m.counts
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loadLocked(m.store)
	c.n[name] += n
	if !c.pending && c.path != "" {
		c.pending = true
		time.AfterFunc(countsDelay, c.save)
	}
}

// loadLocked reads the counts file beside st once; a missing or unreadable
// one starts from zero. Without a store the counts stay in memory.
func (c *uamCounts) loadLocked(st *store.Store) {
	if c.n != nil {
		return
	}
	c.n = map[string]int{}
	if st == nil {
		return
	}
	c.path = filepath.Join(filepath.Dir(st.Path()), uamCountsFile)
	data, err := os.ReadFile(c.path)
	if err == nil && json.Unmarshal(data, &c.n) != nil {
		c.n = map[string]int{}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("read the uam counts failed", "error", err)
	}
}

// save writes the counts when any were taken since the file was read.
func (c *uamCounts) save() {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	c.pending = false
	data, err := json.Marshal(c.n)
	path := c.path
	c.mu.Unlock()
	if path == "" || err != nil {
		return
	}
	if err := writeFileAtomic(path, ".uam-counts-*", data); err != nil {
		log.Warn("write the uam counts failed", "error", err)
	}
}

// countTurn counts what the provider reported of an ended turn.
func (m *Manager) countTurn(t agentapi.Turn) {
	if t.ReadOnceHits > 0 {
		m.countN("read_once_hits", t.ReadOnceHits)
	}
	if t.StallGaveUp {
		m.count("stall_gave_up")
	}
}

// autoFallbackTitle is the title of the Copilot adapter's question before a
// rate-limited request switches to Auto.
const autoFallbackTitle = "Auto model fallback"

// countAsked counts a new interaction: the Auto fallback question, and an
// assisted review's outcome.
func (m *Manager) countAsked(ix agentapi.Interaction) {
	switch {
	case ix.Title == autoFallbackTitle && ix.Kind == agentapi.InteractionQuestion:
		m.count("auto_switch_asked")
	case ix.Assisted.Recommendation == "approve":
		m.count("assisted_approved")
	case ix.Assisted.Recommendation != "":
		m.count("assisted_escalated")
	}
}

// countAnswer counts an owner's answer: a permission request's by decision,
// the Auto fallback question's yes or no.
func (m *Manager) countAnswer(ix agentapi.Interaction, a agentapi.Answer) {
	switch {
	case a.Auto:
	case ix.Kind == agentapi.InteractionPermission:
		m.count("permission_answer_" + cmp.Or(a.Decision, "other"))
	case ix.Title == autoFallbackTitle && ix.Kind == agentapi.InteractionQuestion:
		if len(a.Answers) > 0 && slices.Equal(a.Answers[0], []string{"Yes"}) {
			m.count("auto_switch_yes")
		} else {
			m.count("auto_switch_no")
		}
	}
}

// uamStats are uam's counts, its Utility log's calls by purpose and outcome
// over the days it keeps, and its routines' kept runs by outcome.
func (m *Manager) uamStats() []SessionStat {
	c := &m.counts
	c.mu.Lock()
	c.loadLocked(m.store)
	counts := maps.Clone(c.n)
	c.mu.Unlock()
	l := &m.utility
	days := map[string]bool{}
	l.mu.Lock()
	for _, call := range l.calls {
		days[dayOf(call.At)] = true
		counts["utility_"+call.Purpose+"_"+call.Outcome]++
		if call.Answer == "[abstain]" {
			counts["utility_"+call.Purpose+"_abstained"]++
		}
		if call.Valid != nil && !*call.Valid {
			counts["utility_"+call.Purpose+"_invalid"]++
		}
	}
	l.mu.Unlock()
	counts["utility_days_kept"] = len(days)
	routines := m.AllRoutines()
	counts["routines"] = len(routines)
	for _, r := range routines {
		for _, run := range r.Runs {
			counts["routine_runs_"+run.Outcome]++
		}
	}
	out := make([]SessionStat, 0, len(counts))
	for _, k := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, SessionStat{"uam." + k, strconv.Itoa(counts[k])})
	}
	return out
}

// configStats is a snapshot of uam's configuration: counts and switches only.
func (m *Manager) configStats() []SessionStat {
	modes := map[store.Mode]int{}
	total, worktrees, active := 0, 0, 0
	m.mu.Lock()
	for _, s := range m.sessions {
		if s.removed {
			continue
		}
		total++
		modes[s.mode]++
		worktrees += b2i(s.place != nil && s.place.Worktree != "")
		active += b2i(s.stage == StageActive)
	}
	st := m.settings
	limit := strconv.Itoa(m.utilityLimitLocked())
	providers := m.cliProvidersLocked()
	m.mu.Unlock()
	if st.UtilityUnlimited {
		limit = "unlimited"
	}
	onOff := func(on bool) string { return map[bool]string{true: "on", false: "off"}[on] }
	out := []SessionStat{
		{"config.threads", strconv.Itoa(total)},
		{"config.threads_safe", strconv.Itoa(modes[store.ModeSafe])},
		{"config.threads_assisted", strconv.Itoa(modes[store.ModeAssisted])},
		{"config.threads_yolo", strconv.Itoa(modes[store.ModeYolo])},
		{"config.threads_in_worktrees", strconv.Itoa(worktrees)},
		{"config.threads_active", strconv.Itoa(active)},
		{"config.custom_models", strconv.Itoa(len(st.CustomModels))},
		{"config.utility_daily_limit", limit},
		{"config.compaction_threshold", strconv.Itoa(st.compactionThreshold())},
		{"config.suggest_replies", onOff(st.SuggestReplies == nil || *st.SuggestReplies)},
		{"config.gitlab_auto_investigate", onOff(st.GitLabAutoInvestigate)},
	}
	m.glab.mu.Lock()
	ready := 0
	for _, e := range m.glab.dirs {
		ready += b2i(e.ok)
	}
	m.glab.mu.Unlock()
	out = append(out, SessionStat{"config.gitlab_ready_folders", strconv.Itoa(ready)})
	m.cli.mu.Lock()
	for _, name := range providers {
		v := "unknown"
		if cli := m.cli.by[name]; cli != nil {
			v = cmp.Or(cli.Running, cli.Installed, v)
		}
		out = append(out, SessionStat{"config.cli_" + name + "_version", v})
	}
	m.cli.mu.Unlock()
	return out
}
