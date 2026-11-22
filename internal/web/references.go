package web

import (
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// References resolve the numbers a reply cites ("issues 4, 8 and 9", "#12")
// to where the Thread itself defined them: a numbered list, table or line in
// an earlier message, or the output of a gh or glab command. It reads only
// the Thread's own items, with no model and no network, and leaves a number
// unresolved rather than guess.

// referenceScanBytes bounds the text read from one item; a longer tool
// output is read only as far as this.
const referenceScanBytes = 64 << 10

// referenceCacheSize bounds the per-Thread cache of resolved replies.
const referenceCacheSize = 64

// Reference is one number a reply cites and what it resolved to.
type Reference struct {
	Label      string               `json:"label"`
	Noun       string               `json:"noun"`
	Number     string               `json:"number"`
	Status     string               `json:"status"`
	Title      string               `json:"title,omitempty"`
	State      string               `json:"state"`
	Claimed    string               `json:"claimed"`
	Source     *ReferenceSource     `json:"source,omitempty"`
	Candidates []ReferenceCandidate `json:"candidates,omitempty"`
}

// ReferenceSource is the item holding a reference's winning definition.
type ReferenceSource struct {
	ItemID string    `json:"item_id"`
	Time   time.Time `json:"time"`
}

// ReferenceCandidate is one of the definitions an ambiguous reference could
// mean.
type ReferenceCandidate struct {
	Title  string `json:"title"`
	Noun   string `json:"noun"`
	ItemID string `json:"item_id"`
}

const (
	referenceResolved   = "resolved"
	referenceAmbiguous  = "ambiguous"
	referenceUnresolved = "unresolved"
)

type referencesKey struct {
	items int
	seq   uint64
}

type referencesCache struct {
	key  referencesKey
	refs []Reference
}

// References resolves the citations of assistant item reply in Thread id.
func (m *Manager) References(id, reply string) ([]Reference, error) {
	s, err := m.lookup(id)
	if err != nil {
		return nil, err
	}
	if err := m.waitReleasedHistory(m.ctx, s); err != nil {
		return nil, err
	}
	m.mu.Lock()
	if s.removed {
		m.mu.Unlock()
		return nil, newError(http.StatusNotFound, msgSessionNotFound)
	}
	s.historyUsed = m.now()
	items := s.agentItems("")
	end := slices.IndexFunc(items, func(it agentapi.Item) bool { return it.ID == reply && it.Kind == agentapi.ItemAssistant })
	if end < 0 {
		m.mu.Unlock()
		return nil, newError(http.StatusNotFound, "reply not found")
	}
	items = items[:end+1]
	// A replaced item keeps its place but gets a newer mutation sequence, so
	// the count and the newest sequence together notice any change.
	key := referencesKey{items: len(items)}
	for _, it := range items {
		key.seq = max(key.seq, s.itemSeq[itemKey(it.AgentID, it.ID)])
	}
	if c, ok := s.references[reply]; ok && c.key == key {
		m.mu.Unlock()
		return c.refs, nil
	}
	m.mu.Unlock()

	refs := resolveReferences(items)

	m.mu.Lock()
	defer m.mu.Unlock()
	if !s.removed {
		if s.references == nil || len(s.references) >= referenceCacheSize {
			s.references = map[string]referencesCache{}
		}
		s.references[reply] = referencesCache{key: key, refs: refs}
	}
	return refs, nil
}

func (s *Server) handleReferences(w http.ResponseWriter, r *http.Request) {
	reply := r.URL.Query().Get("reply")
	if reply == "" {
		writeError(w, http.StatusBadRequest, "reply is required")
		return
	}
	refs, err := s.m.References(r.PathValue("id"), reply)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		References []Reference `json:"references"`
	}{refs})
}

/* ---------- Definitions ---------- */

// refDef is one definition of a numbered thing. Item and line place it in
// the Thread, so the most recent one before a citation wins.
type refDef struct {
	noun, sigil, number, title, state string
	item, line                        int
	itemID                            string
	time                              time.Time
}

const refNouns = `issue|bug|finding|item|task|step|ticket|pr|mr|problem|question|risk|todo`

var (
	nounRE       = regexp.MustCompile(`(?i)\b(` + refNouns + `)s?\b`)
	headingRE    = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	bulletRE     = regexp.MustCompile(`^[-*+]\s+`)
	checkboxRE   = regexp.MustCompile(`^\[([ xX])\]\s+`)
	numberedRE   = regexp.MustCompile(`^(\d{1,6})[.)]\s+(\S.*)$`)
	explicitRE   = regexp.MustCompile(`(?i)^(` + refNouns + `)s?\s*([#!]?)(\d{1,6})\s*(?::|[—–]|\s-)\s*(\S.*)$`)
	hashDefRE    = regexp.MustCompile(`^([#!])(\d{1,6})\b[\s:—–-]*(\S.*)$`)
	tableSepRE   = regexp.MustCompile(`^\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?$`)
	cellNumRE    = regexp.MustCompile(`^[#!]?(\d{1,6})\.?$`)
	refFenceRE   = regexp.MustCompile("^\\s{0,3}(```|~~~)")
	codeSpanRE   = regexp.MustCompile("`[^`\n]*`")
	ghListState  = regexp.MustCompile(`^(OPEN|CLOSED|MERGED|DRAFT)$`)
	glabRefRE    = regexp.MustCompile(`^\S*[#!]\d+$`)
	viewNumRE    = regexp.MustCompile(`\bview\s+(?:\S*[#!/])?(\d+)\b`)
	headerRE     = regexp.MustCompile(`^(title|state|number):\s*(.*)$`)
	cliRE        = regexp.MustCompile(`\b(gh|glab)\s+(issue|pr|mr)\s+(list|ls|view)\b`)
	refColumnsRE = regexp.MustCompile(`\s{2,}`)
)

// nounIn is the first reference noun s names, singular and lowercase.
func nounIn(s string) string {
	m := nounRE.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1])
}

// refTitle is a definition's first line without Markdown.
func refTitle(s string) string {
	s = mdLinkRE.ReplaceAllString(s, "$1")
	s = strings.NewReplacer("**", "", "__", "", "`", "", "~~", "").Replace(s)
	s = strings.Trim(strings.TrimSpace(s), "*_ ")
	if utf8.RuneCountInString(s) > 160 {
		s = string([]rune(s)[:160]) + "…"
	}
	return s
}

// capText bounds an item's text to referenceScanBytes, dropping the line
// a cut or uam's truncation marker left partial.
func capText(s string) string {
	cut := false
	if i := strings.Index(s, truncatedMarker); i >= 0 {
		s, cut = s[:i], true
	}
	if len(s) > referenceScanBytes {
		s, cut = s[:referenceScanBytes], true
	}
	if cut {
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			s = s[:i]
		} else {
			s = ""
		}
	}
	return s
}

// splitCells splits a Markdown table row into trimmed cells.
func splitCells(row string) []string {
	row = strings.TrimSpace(row)
	row = strings.TrimPrefix(row, "|")
	row = strings.TrimSuffix(row, "|")
	cells := strings.Split(row, "|")
	for i := range cells {
		cells[i] = strings.TrimSpace(cells[i])
	}
	return cells
}

// messageDefs are the definitions in a user or assistant message, each
// with its line. A numbered list or table takes its noun from the nearest
// heading or lead-in line ("Open findings:") before it.
func messageDefs(text string) []refDef {
	var defs []refDef
	lines := strings.Split(capText(text), "\n")
	lead, fenced := "", false
	table, tableNoun, statusCol := false, "", -1
	for i := 0; i < len(lines); i++ {
		raw := lines[i]
		if refFenceRE.MatchString(raw) {
			fenced = !fenced
			continue
		}
		t := strings.TrimSpace(raw)
		if fenced || t == "" {
			continue
		}
		if strings.HasPrefix(t, "|") {
			if !table && i+1 < len(lines) && tableSepRE.MatchString(strings.TrimSpace(lines[i+1])) {
				table, tableNoun, statusCol = true, nounIn(t), -1
				if tableNoun == "" {
					tableNoun = lead
				}
				for j, h := range splitCells(t) {
					if h = strings.ToLower(refTitle(h)); h == "status" || h == "state" {
						statusCol = j
						break
					}
				}
				i++
				continue
			}
			if table && tableNoun != "" {
				if d, ok := tableRowDef(splitCells(t), statusCol); ok {
					d.noun, d.line = tableNoun, i
					defs = append(defs, d)
				}
			}
			continue
		}
		table = false
		indent := len(raw) - len(strings.TrimLeft(raw, " \t"))
		rest, bulleted, box := t, false, ""
		if loc := bulletRE.FindStringIndex(rest); loc != nil {
			rest, bulleted = rest[loc[1]:], true
			if m := checkboxRE.FindStringSubmatch(rest); m != nil {
				rest, box = rest[len(m[0]):], "todo"
				if m[1] != " " {
					box = "done"
				}
			}
		}
		plain := strings.ReplaceAll(rest, "**", "")
		if m := headingRE.FindStringSubmatch(t); m != nil {
			heading := strings.ReplaceAll(m[1], "**", "")
			if m := explicitRE.FindStringSubmatch(heading); m != nil {
				defs = append(defs, refDef{noun: strings.ToLower(m[1]), number: m[3], title: refTitle(m[4]), line: i})
			}
			lead = nounIn(heading)
			continue
		}
		if m := explicitRE.FindStringSubmatch(plain); m != nil {
			defs = append(defs, refDef{noun: strings.ToLower(m[1]), number: m[3], title: refTitle(m[4]), state: box, line: i})
			continue
		}
		// Without a lead-in naming the noun, "#4 is closed." is a citation.
		if m := hashDefRE.FindStringSubmatch(plain); m != nil && lead != "" {
			defs = append(defs, refDef{noun: lead, sigil: m[1], number: m[2], title: refTitle(m[3]), state: box, line: i})
			continue
		}
		if m := numberedRE.FindStringSubmatch(plain); m != nil {
			// A nested list numbers the parts of one entry, not the
			// entries themselves.
			if lead != "" && indent < 2 {
				defs = append(defs, refDef{noun: lead, number: m[1], title: refTitle(m[2]), state: box, line: i})
			}
			continue
		}
		if !bulleted {
			if strings.HasSuffix(plain, ":") {
				lead = nounIn(plain)
			} else if strings.HasPrefix(rest, "**") && strings.HasSuffix(rest, "**") {
				// A bold line alone works as a heading.
				lead = nounIn(plain)
			}
		}
	}
	return defs
}

// tableRowDef reads a row whose first cell is a number: its title is the
// first other column that is neither a number nor the status column.
func tableRowDef(cells []string, statusCol int) (refDef, bool) {
	if len(cells) < 2 {
		return refDef{}, false
	}
	m := cellNumRE.FindStringSubmatch(refTitle(cells[0]))
	if m == nil {
		return refDef{}, false
	}
	d := refDef{number: m[1]}
	for j, c := range cells[1:] {
		c = refTitle(c)
		if j+1 == statusCol {
			d.state = normalizeState(c)
			continue
		}
		if d.title == "" && c != "" && !cellNumRE.MatchString(c) {
			d.title = c
		}
	}
	return d, d.title != ""
}

// normalizeState is a recorded state in lowercase, without the emoji or
// punctuation around it; "" when it is too long to be one.
func normalizeState(s string) string {
	s = strings.ToLower(strings.TrimFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z')
	}))
	s = strings.ReplaceAll(s, "_", " ")
	if len(s) > 24 {
		return ""
	}
	return s
}

// toolDefs are the definitions a gh or glab issue, pull request or merge
// request command printed. A call with more than one such command is
// skipped: its output cannot be told apart.
func toolDefs(tc *agentapi.ToolCall) []refDef {
	cmd := toolCommand(tc)
	if cmd == "" {
		cmd = tc.Input
	}
	found := cliRE.FindAllStringSubmatchIndex(cmd, -1)
	if len(found) != 1 {
		return nil
	}
	loc := found[0]
	cli, kind, verb := cmd[loc[2]:loc[3]], cmd[loc[4]:loc[5]], cmd[loc[6]:loc[7]]
	if (cli == "gh" && kind == "mr") || (cli == "glab" && kind == "pr") {
		return nil
	}
	args := cmd[loc[0]:]
	if i := strings.IndexAny(args, "|;&\n"); i >= 0 {
		args = args[:i]
	}
	sigil := ""
	if kind == "mr" {
		sigil = "!"
	}
	lines := strings.Split(capText(tc.Output), "\n")
	var defs []refDef
	if verb == "view" {
		d := refDef{noun: kind, sigil: sigil}
		if m := viewNumRE.FindStringSubmatch(args); m != nil {
			d.number = m[1]
		}
		for _, l := range lines {
			if strings.TrimSpace(l) == "--" {
				break
			}
			m := headerRE.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			switch v := strings.TrimSpace(m[2]); m[1] {
			case "title":
				d.title = refTitle(v)
			case "state":
				d.state = normalizeState(v)
			case "number":
				d.number = strings.TrimLeft(v, "#!")
			}
		}
		if d.title != "" && cellNumRE.MatchString(d.number) {
			defs = append(defs, d)
		}
		return defs
	}
	state := "open"
	switch {
	case strings.Contains(args, "--all") || strings.Contains(args, " -A"):
		state = ""
	case strings.Contains(args, "--closed"):
		state = "closed"
	case strings.Contains(args, "--merged"):
		state = "merged"
	}
	for i, l := range lines {
		var d refDef
		var ok bool
		if cli == "gh" {
			d, ok = ghListRow(l)
		} else {
			d, ok = glabListRow(l, state)
		}
		if ok {
			d.noun, d.line = kind, i
			if d.sigil == "" {
				d.sigil = sigil
			}
			defs = append(defs, d)
		}
	}
	return defs
}

// ghListRow reads a tab-separated row of `gh issue list` (number, state,
// title, ...) or `gh pr list` (number, title, branch, state, ...).
func ghListRow(l string) (refDef, bool) {
	f := strings.Split(l, "\t")
	if len(f) < 3 || !cellNumRE.MatchString(f[0]) || strings.Contains(f[0], ".") {
		return refDef{}, false
	}
	d := refDef{number: strings.TrimLeft(f[0], "#")}
	for _, c := range f[1:] {
		c = strings.TrimSpace(c)
		switch {
		case d.state == "" && ghListState.MatchString(c):
			d.state = strings.ToLower(c)
		case d.title == "" && c != "":
			d.title = refTitle(c)
		}
	}
	return d, d.title != ""
}

// glabListRow reads a row of `glab issue list` or `glab mr list`: the
// short reference (#4, !4), the full one, then the title.
func glabListRow(l, state string) (refDef, bool) {
	l = strings.TrimSpace(l)
	if !strings.HasPrefix(l, "#") && !strings.HasPrefix(l, "!") {
		return refDef{}, false
	}
	f := strings.Split(l, "\t")
	if len(f) == 1 {
		f = refColumnsRE.Split(l, -1)
	}
	m := cellNumRE.FindStringSubmatch(f[0])
	if m == nil || len(f) < 2 {
		return refDef{}, false
	}
	d := refDef{sigil: f[0][:1], number: m[1], state: state}
	for _, c := range f[1:] {
		if c = strings.TrimSpace(c); c != "" && !glabRefRE.MatchString(c) {
			d.title = refTitle(c)
			break
		}
	}
	return d, d.title != ""
}

/* ---------- Citations ---------- */

type citation struct {
	noun, sigil, number, claimed string
	line                         int
}

var (
	citeRE      = regexp.MustCompile(`(?i)\b(` + refNouns + `)s?((?:\s+[#!]?|\s*[#!])\d{1,6}\b(?:(?:\s*,\s*(?:and\s+|or\s+)?|\s+(?:and|or)\s+|\s*[&/]\s*)[#!]?\d{1,6}\b)*)`)
	citeNumRE   = regexp.MustCompile(`\d+`)
	bareRE      = regexp.MustCompile(`(?:^|[^\w#!&/])([#!])(\d{1,6})\b`)
	stateWordRE = regexp.MustCompile(`(?i)\b(still\s+open|open|closed|done|fixed|merged|blocked)\b`)
	sentenceEnd = regexp.MustCompile(`[.!?;](\s|$)`)
	// contRE is a later number list in the same sentence ("…; 5 and 6 are
	// closed"); it takes the noun of the list before it.
	contRE = regexp.MustCompile(`(?i)(?:[;,]|\band\b|\bbut\b|\bwhile\b|\bwhereas\b)\s*(\d{1,6}\b(?:(?:\s*,\s*(?:and\s+|or\s+)?|\s+(?:and|or)\s+|\s*[&/]\s*)[#!]?\d{1,6}\b)*)`)
	stopRE = regexp.MustCompile(`[.!?](\s|$)`)
)

// claimedState is the first state word in the rest of a sentence, skipping
// negated ones ("not fixed").
func claimedState(rest string) string {
	if loc := sentenceEnd.FindStringIndex(rest); loc != nil {
		rest = rest[:loc[0]]
	}
	for _, loc := range stateWordRE.FindAllStringIndex(rest, -1) {
		before := strings.ToLower(strings.TrimSpace(rest[:loc[0]]))
		if strings.HasSuffix(before, "not") || strings.HasSuffix(before, "n't") || strings.HasSuffix(before, "never") {
			continue
		}
		w := strings.ToLower(strings.Join(strings.Fields(rest[loc[0]:loc[1]]), " "))
		switch w {
		case "still open":
			return "open"
		case "fixed":
			return "closed"
		}
		return w
	}
	return ""
}

// stateClauseRE is a verb that ties a number list to its state word: "are
// closed", "is still open", "as closed".
var stateClauseRE = regexp.MustCompile(`(?i)^\s*(?:is|are|was|were|as|remain|remains|stay|stays|got|have\s+been|has\s+been)\b`)

func startsWithStateClause(s string) bool { return stateClauseRE.MatchString(s) }

// citations are the numbers a reply cites, in order, outside code and the
// reply's own definition lines.
func citations(text string, skip map[int]bool) []citation {
	var out []citation
	fenced := false
	for i, line := range strings.Split(capText(text), "\n") {
		if refFenceRE.MatchString(line) {
			fenced = !fenced
			continue
		}
		if fenced || skip[i] {
			continue
		}
		line = codeSpanRE.ReplaceAllStringFunc(line, func(s string) string { return strings.Repeat(" ", len(s)) })
		type hit struct {
			start int
			cs    []citation
		}
		var hits []hit
		var spans [][]int
		for _, m := range citeRE.FindAllStringSubmatchIndex(line, -1) {
			spans = append(spans, m[:2])
			noun, claimed := strings.ToLower(line[m[2]:m[3]]), claimedState(line[m[1]:])
			h := hit{start: m[0]}
			for _, n := range citeNumRE.FindAllString(line[m[4]:m[5]], -1) {
				h.cs = append(h.cs, citation{noun: noun, number: n, claimed: claimed, line: i})
			}
			// Only a later list with a state word of its own counts, so "and 3
			// tests failed" is not read as issue 3.
			rest := line[m[1]:]
			if loc := stopRE.FindStringIndex(rest); loc != nil {
				rest = rest[:loc[0]]
			}
			for _, c := range contRE.FindAllStringSubmatchIndex(rest, -1) {
				st := claimedState(rest[c[3]:])
				if st == "" || !startsWithStateClause(rest[c[3]:]) {
					continue
				}
				spans = append(spans, []int{m[1] + c[2], m[1] + c[3]})
				for _, n := range citeNumRE.FindAllString(rest[c[2]:c[3]], -1) {
					h.cs = append(h.cs, citation{noun: noun, number: n, claimed: st, line: i})
				}
			}
			hits = append(hits, h)
		}
		for _, m := range bareRE.FindAllStringSubmatchIndex(line, -1) {
			if slices.ContainsFunc(spans, func(s []int) bool { return m[2] >= s[0] && m[2] < s[1] }) {
				continue
			}
			hits = append(hits, hit{start: m[2], cs: []citation{{sigil: line[m[2]:m[3]], number: line[m[4]:m[5]], claimed: claimedState(line[m[1]:]), line: i}}})
		}
		slices.SortStableFunc(hits, func(a, b hit) int { return a.start - b.start })
		for _, h := range hits {
			out = append(out, h.cs...)
		}
	}
	return out
}

/* ---------- Resolution ---------- */

// resolveReferences resolves the citations of the last item, the reply,
// against the definitions in every item before it and its own earlier
// lines.
func resolveReferences(items []agentapi.Item) []Reference {
	refs := []Reference{}
	if len(items) == 0 {
		return refs
	}
	r := len(items) - 1
	var defs []refDef
	var replyDefs []refDef
	for i, it := range items {
		var found []refDef
		switch {
		case it.Kind == agentapi.ItemUser || it.Kind == agentapi.ItemAssistant:
			found = messageDefs(it.Text)
		case it.Kind == agentapi.ItemTool && it.Tool != nil:
			found = toolDefs(it.Tool)
		}
		for j := range found {
			found[j].item, found[j].itemID, found[j].time = i, it.ID, it.Time
		}
		if i == r {
			replyDefs = found
		}
		defs = append(defs, found...)
	}
	skip := map[int]bool{}
	for _, d := range replyDefs {
		skip[d.line] = true
	}
	seen := map[string]int{}
	for _, c := range citations(items[r].Text, skip) {
		label := c.sigil + c.number
		if c.noun != "" {
			label = c.noun + " " + c.number
			if c.noun == "pr" || c.noun == "mr" {
				label = strings.ToUpper(c.noun) + " " + c.number
			}
		}
		if at, ok := seen[label]; ok {
			if refs[at].Claimed == "" {
				refs[at].Claimed = c.claimed
			}
			continue
		}
		ref, ok := resolveCitation(c, defs, r)
		if !ok {
			continue
		}
		ref.Label = label
		seen[label] = len(refs)
		refs = append(refs, ref)
	}
	return refs
}

// resolveCitation picks the most recent definition before c. A bare #N
// matches any noun but a merge request's, a bare !N only a merge request,
// and it is dropped when nothing defines it.
func resolveCitation(c citation, defs []refDef, reply int) (Reference, bool) {
	ref := Reference{Noun: c.noun, Number: c.number, Claimed: c.claimed}
	latest := map[string]refDef{}
	var order []string
	for _, d := range defs {
		if d.item > reply || (d.item == reply && d.line >= c.line) || d.number != c.number {
			continue
		}
		switch {
		case c.noun != "":
			if d.noun != c.noun {
				continue
			}
		case c.sigil == "!":
			if d.noun != "mr" && d.sigil != "!" {
				continue
			}
		default:
			if d.noun == "mr" || d.sigil == "!" {
				continue
			}
		}
		if _, ok := latest[d.noun]; ok {
			order = slices.DeleteFunc(order, func(n string) bool { return n == d.noun })
		}
		latest[d.noun] = d
		order = append(order, d.noun)
	}
	switch len(order) {
	case 0:
		if c.noun == "" {
			return ref, false
		}
		ref.Status = referenceUnresolved
	case 1:
		d := latest[order[0]]
		ref.Status, ref.Title, ref.State = referenceResolved, d.title, d.state
		ref.Source = &ReferenceSource{ItemID: d.itemID, Time: d.time}
	default:
		ref.Status = referenceAmbiguous
		for _, n := range slices.Backward(order) {
			d := latest[n]
			ref.Candidates = append(ref.Candidates, ReferenceCandidate{Title: d.title, Noun: d.noun, ItemID: d.itemID})
		}
	}
	return ref, true
}
