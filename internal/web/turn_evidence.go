package web

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Turn evidence: what a Task's turns did, read from its items by fixed
// rules, never a model. The finish card, the outcome line and "Since you
// left" all read it here, so they never disagree. It leans towards
// under-claiming: anything unclear reads "Not verified".
//
// The main agent's items are noted as they arrive live or from the
// provider's record (noteActivity), so the evidence covers the whole turn
// however much of the transcript is held; edits, the main agent's and the
// subagents', are the Task's edits (task_changes.go).

const (
	// maxActivity bounds the items a Task's evidence remembers.
	maxActivity = 20000
	// maxCheckCommand bounds a check's command as kept and shown.
	maxCheckCommand = 4 << 10
	// maxDetailCommand bounds a command quoted in a claim's detail.
	maxDetailCommand = 300
)

// commandTools are the tools that run a shell command.
var commandTools = map[string]bool{"bash": true, "shell": true, "powershell": true}

// activity is what the evidence keeps of one main-agent item.
type activity struct {
	at, ended time.Time
	kind      agentapi.ItemKind
	// text marks an assistant message with text.
	text     bool
	question bool
	shell    bool
	status   agentapi.ToolStatus
	// command is a check's command line; checks are the checks it runs,
	// nil for a command that runs none.
	command string
	checks  []checkRun
	exit    *int
	counts  *testCounts
	// partial is set when the output was cut, so its counts are partial.
	partial bool
	output  bool
}

// startsTurn reports whether it is an ordinary prompt to the main agent,
// which starts a turn: neither a steer nor an automatic continuation, which
// join the running one.
func startsTurn(it agentapi.Item) bool {
	return it.AgentID == "" && it.Kind == agentapi.ItemUser && it.Delivery != agentapi.DeliverySteer && it.Delivery != agentapi.DeliveryAutopilot
}

// noteActivity records what the evidence needs of one main-agent item.
func (s *webSession) noteActivity(it agentapi.Item) {
	if it.AgentID != "" || it.ID == "" {
		return
	}
	a := activity{at: it.Time, ended: it.EndedAt, kind: it.Kind}
	switch it.Kind {
	case agentapi.ItemAssistant:
		a.text = strings.TrimSpace(it.Text) != ""
	case agentapi.ItemTool:
		tc := it.Tool
		if tc == nil {
			break
		}
		name := strings.ToLower(tc.Name)
		a.status, a.question, a.shell = tc.Status, name == "ask_user", commandTools[name]
		if !a.shell {
			break
		}
		command := toolCommand(tc)
		if a.checks = commandChecks(command); a.checks != nil {
			a.command = clampText(command, maxCheckCommand)
		}
		if tc.Status != agentapi.ToolCompleted && tc.Status != agentapi.ToolFailed {
			break
		}
		a.exit, a.output = exitOf(tc), tc.Output != ""
		if a.checks != nil {
			a.counts = countsOf(tc.Output)
			a.partial = it.Clipped || strings.Contains(tc.Output, truncatedMarker)
		}
	}
	// An item keeps the time it began, as the held transcript does.
	if prev, ok := s.activity[it.ID]; ok && !prev.at.IsZero() && prev.at.Before(a.at) {
		a.at = prev.at
	}
	if s.activity == nil {
		s.activity = map[string]activity{}
	}
	s.activity[it.ID] = a
	if len(s.activity) > maxActivity {
		ids := slices.Collect(maps.Keys(s.activity))
		slices.SortFunc(ids, func(x, y string) int { return s.activity[x].at.Compare(s.activity[y].at) })
		for _, id := range ids[:len(ids)-maxActivity*9/10] {
			delete(s.activity, id)
		}
	}
}

// toolCommand is the command line a shell tool call ran.
func toolCommand(tc *agentapi.ToolCall) string {
	var in struct {
		Command string `json:"command"`
	}
	if json.Unmarshal([]byte(tc.Input), &in) != nil {
		return ""
	}
	return in.Command
}

var exitLineRE = regexp.MustCompile(`(?:exited|completed) with exit code (\d+)>?\s*$`)

// exitOf is a shell call's exit code: the provider's, else the one its
// shell tool appends to the output ("<shellId: 0 completed with exit code
// 1>"); nil when neither says.
func exitOf(tc *agentapi.ToolCall) *int {
	if tc.ExitCode != nil {
		return tc.ExitCode
	}
	if m := exitLineRE.FindStringSubmatch(strings.TrimRightFunc(tc.Output, unicode.IsSpace)); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return &n
		}
	}
	return nil
}

/* ---------- Checks ---------- */

// The kinds of check a command can run, in the order a claim names them.
const (
	checkTest      = "test"
	checkBuild     = "build"
	checkLint      = "lint"
	checkVet       = "vet"
	checkTypecheck = "typecheck"
)

const checkPM = `(?:npm|pnpm|yarn|bun)(?:\s+run)?`

// checkKinds recognise a simple command by how it starts.
var checkKinds = []struct {
	kind string
	re   *regexp.Regexp
}{
	{checkVet, regexp.MustCompile(`^go\s+vet\b`)},
	{checkTypecheck, regexp.MustCompile(`^(?:tsc\b|npx\s+tsc\b|mypy\b|pyright\b|cargo\s+check\b|` + checkPM + `\s+(?:typecheck|type-check|tsc|check-types)\b)`)},
	{checkLint, regexp.MustCompile(`^(?:eslint\b|npx\s+eslint\b|golangci-lint\b|staticcheck\b|ruff\b|flake8\b|pylint\b|shellcheck\b|cargo\s+clippy\b|prettier\s+.*--check\b|npx\s+prettier\s+.*--check\b|gofmt\s+-l\b|make\s+lint\b|` + checkPM + `\s+lint\b)`)},
	{checkTest, regexp.MustCompile(`^(?:go\s+test\b|cargo\s+test\b|(?:python3?\s+-m\s+)?pytest\b|npx\s+(?:jest|vitest|playwright\s+test)\b|jest\b|vitest\b|node\s+(?:--\S+\s+)*--test\b|deno\s+test\b|bun\s+test\b|dotnet\s+test\b|mvn\s+(?:\S+\s+)*test\b|(?:\./)?gradlew?\s+test\b|rspec\b|phpunit\b|tox\b|ctest\b|make\s+test\b|` + checkPM + `\s+test(?::\S+)?\b)`)},
	{checkBuild, regexp.MustCompile(`^(?:go\s+build\b|cargo\s+build\b|vite\s+build\b|npx\s+vite\s+build\b|dotnet\s+build\b|mvn\s+(?:\S+\s+)*(?:package|install|verify)\b|(?:\./)?gradlew?\s+build\b|make(?:\s+build)?\s*$|` + checkPM + `\s+build\b)`)},
}

// checkWrapperRE matches a wrapper a command line starts with.
var checkWrapperRE = regexp.MustCompile(`^(?:[A-Za-z_][A-Za-z0-9_]*=\S*\s+|time\s+|sudo\s+|timeout\s+\S+\s+|env\s+)`)

// attribution is how much of a command line's exit status a check's own.
type attribution int

const (
	// statusElsewhere: the status is a later command's (after `;`, a line
	// break, `||` or `&`), so only the check's complete output can tell.
	statusElsewhere attribution = iota
	// statusPiped: the status is a later pipe command's, and the output the
	// shell saw may be cut (`| tail`), so it can only tell a failure.
	statusPiped
	// statusAndThen: only `&&` follows, so exit 0 says the check passed.
	statusAndThen
	// statusOwn: the status is the check's.
	statusOwn
)

// checkRun is one check a command line runs.
type checkRun struct {
	kind string
	attr attribution
}

// commandChecks are the checks a shell command line runs, each with how
// much of the line's exit status is its own; nil when it runs none or is
// not shell the parser reads.
func commandChecks(command string) []checkRun {
	if command == "" {
		return nil
	}
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	w := checkWalker{pipefail: strings.Contains(command, "pipefail")}
	w.stmts(f.Stmts, statusOwn)
	return w.runs
}

type checkWalker struct {
	pipefail bool
	runs     []checkRun
}

func (w *checkWalker) stmts(list []*syntax.Stmt, attr attribution) {
	for i, st := range list {
		if i < len(list)-1 {
			w.stmt(st, statusElsewhere)
		} else {
			w.stmt(st, attr)
		}
	}
}

func (w *checkWalker) stmt(st *syntax.Stmt, attr attribution) {
	if st == nil {
		return
	}
	if st.Negated || st.Background || st.Coprocess || st.Disown {
		attr = statusElsewhere
	}
	switch c := st.Cmd.(type) {
	case *syntax.CallExpr:
		if kind := checkKind(c); kind != "" {
			w.runs = append(w.runs, checkRun{kind, attr})
		}
	case *syntax.BinaryCmd:
		switch c.Op {
		case syntax.AndStmt:
			// `a && b`: exit 0 says both passed; b's status is the line's.
			w.stmt(c.X, min(attr, statusAndThen))
			w.stmt(c.Y, attr)
		case syntax.Pipe, syntax.PipeAll:
			// Without pipefail the status is the last command's, and its
			// output may be cut (`| tail`): only a failure shows.
			left := statusPiped
			if w.pipefail && attr >= statusAndThen {
				left = attr
			}
			w.stmt(c.X, left)
			w.stmt(c.Y, attr)
		default:
			// `a || b`: exit 0 may be a's alone, a failure b's.
			w.stmt(c.X, statusElsewhere)
			w.stmt(c.Y, statusElsewhere)
		}
	case *syntax.Subshell:
		w.stmts(c.Stmts, attr)
	case *syntax.Block:
		w.stmts(c.Stmts, attr)
	case *syntax.TimeClause:
		w.stmt(c.Stmt, attr)
	default:
		// Inside if, while, for, case or a function the status says nothing
		// certain about a check.
		if st.Cmd != nil {
			syntax.Walk(st.Cmd, func(n syntax.Node) bool {
				if call, ok := n.(*syntax.CallExpr); ok {
					if kind := checkKind(call); kind != "" {
						w.runs = append(w.runs, checkRun{kind, statusElsewhere})
					}
				}
				return true
			})
		}
	}
}

// checkKind is the kind of check a simple command runs, "" for none.
func checkKind(call *syntax.CallExpr) string {
	words := make([]string, 0, len(call.Args))
	for _, w := range call.Args {
		if lit := w.Lit(); lit != "" {
			words = append(words, lit)
			continue
		}
		var b strings.Builder
		if syntax.NewPrinter().Print(&b, w) != nil {
			return ""
		}
		words = append(words, b.String())
	}
	s := strings.Join(words, " ")
	for next := checkWrapperRE.ReplaceAllString(s, ""); next != s; next = checkWrapperRE.ReplaceAllString(s, "") {
		s = next
	}
	for _, c := range checkKinds {
		if c.re.MatchString(s) {
			return c.kind
		}
	}
	return ""
}

// testCounts is what a test output says it counted.
type testCounts struct {
	passed, failed int
	// label is "2 packages ok", "14 passed, 1 failed".
	label string
}

var (
	cargoResultRE = regexp.MustCompile(`test result: (?:ok|FAILED)\.`)
	cargoPassedRE = regexp.MustCompile(`test result: \w+\. (\d+) passed`)
	cargoFailedRE = regexp.MustCompile(`test result: \w+\. \d+ passed; (\d+) failed`)
	jsTestsRE     = regexp.MustCompile(`(?m)^[ \t]*Tests:?[ \t]+(.*)$`)
	countedRE     = regexp.MustCompile(`\d+ (?:passed|failed)`)
	passedRE      = regexp.MustCompile(`(\d+) passed`)
	failedRE      = regexp.MustCompile(`(\d+) failed`)
	pytestRE      = regexp.MustCompile(`(?m)^=+ (.*\b(?:passed|failed)\b.*) in [\d.]+s(?: \([^)]*\))? =+$`)
	tapPassRE     = regexp.MustCompile(`(?m)^# pass (\d+)`)
	tapFailRE     = regexp.MustCompile(`(?m)^# fail (\d+)`)
	goOKRE        = regexp.MustCompile(`(?m)^ok[ \t]+\S+`)
	goFailPkgRE   = regexp.MustCompile(`(?m)^FAIL[ \t]+\S+[ \t]`)
	goFailTestRE  = regexp.MustCompile(`(?m)^[ \t]*--- FAIL:`)
)

func sumOf(re *regexp.Regexp, s string) int {
	n := 0
	for _, m := range re.FindAllStringSubmatch(s, -1) {
		v, _ := strconv.Atoi(m[1])
		n += v
	}
	return n
}

func firstOf(re *regexp.Regexp, s string) int {
	if m := re.FindStringSubmatch(s); m != nil {
		v, _ := strconv.Atoi(m[1])
		return v
	}
	return 0
}

func passFail(passed, failed int) *testCounts {
	label := fmt.Sprintf("%d passed", passed)
	if failed > 0 {
		label += fmt.Sprintf(", %d failed", failed)
	}
	return &testCounts{passed, failed, label}
}

// countsOf reads the counts of Go, Jest, Vitest, pytest, cargo and TAP
// (node --test) outputs; nil when nothing is recognisable.
func countsOf(output string) *testCounts {
	if output == "" {
		return nil
	}
	if cargoResultRE.MatchString(output) {
		return passFail(sumOf(cargoPassedRE, output), sumOf(cargoFailedRE, output))
	}
	if m := jsTestsRE.FindStringSubmatch(output); m != nil && countedRE.MatchString(m[1]) {
		return passFail(firstOf(passedRE, m[1]), firstOf(failedRE, m[1]))
	}
	if m := pytestRE.FindStringSubmatch(output); m != nil {
		return passFail(firstOf(passedRE, m[1]), firstOf(failedRE, m[1]))
	}
	if tapPassRE.MatchString(output) {
		return passFail(sumOf(tapPassRE, output), sumOf(tapFailRE, output))
	}
	ok, failedPkgs := len(goOKRE.FindAllString(output, -1)), len(goFailPkgRE.FindAllString(output, -1))
	if ok == 0 && failedPkgs == 0 {
		return nil
	}
	failedTests := len(goFailTestRE.FindAllString(output, -1))
	var parts []string
	if ok > 0 {
		parts = append(parts, fmt.Sprintf("%d %s ok", ok, plural(ok, "package", "packages")))
	}
	if failedPkgs > 0 {
		parts = append(parts, fmt.Sprintf("%d %s failed", failedPkgs, plural(failedPkgs, "package", "packages")))
	}
	if failedTests > 0 {
		parts = append(parts, fmt.Sprintf("%d %s failed", failedTests, plural(failedTests, "test", "tests")))
	}
	return &testCounts{ok, failedPkgs + failedTests, strings.Join(parts, ", ")}
}

// Check outcomes.
const (
	outcomePass    = "pass"
	outcomeFail    = "fail"
	outcomeUnclear = "unclear"
	outcomeRunning = "running"
)

// result is how one check of a command came out, and why when unclear.
func (a activity) result(run checkRun) (outcome, note string) {
	if a.status != agentapi.ToolCompleted && a.status != agentapi.ToolFailed {
		return outcomeRunning, "still running"
	}
	// Counts are test summaries; they say nothing of a build or a linter.
	var counts *testCounts
	if run.kind == checkTest {
		counts = a.counts
	}
	exited := a.exit != nil || a.status == agentapi.ToolFailed
	failed := a.status == agentapi.ToolFailed || a.exit != nil && *a.exit != 0
	switch run.attr {
	case statusOwn:
		switch {
		case !exited:
			return outcomeUnclear, "no exit status recorded"
		case failed:
			return outcomeFail, ""
		}
		return outcomePass, ""
	case statusAndThen:
		switch {
		case !exited:
			return outcomeUnclear, "no exit status recorded"
		case !failed:
			return outcomePass, ""
		case counts != nil && counts.failed > 0:
			return outcomeFail, ""
		}
		return outcomeUnclear, "a later command may have failed"
	case statusPiped:
		if counts != nil && counts.failed > 0 {
			return outcomeFail, ""
		}
		return outcomeUnclear, "piped, so the exit status is the last command’s"
	}
	switch {
	case counts != nil && counts.failed > 0:
		return outcomeFail, ""
	case counts != nil && counts.passed > 0 && !a.partial:
		return outcomePass, "passed by its output"
	}
	return outcomeUnclear, "the exit status is a later command’s"
}

/* ---------- One turn ---------- */

// EvidenceCheck is one command of a turn that runs checks.
type EvidenceCheck struct {
	ItemID string `json:"item_id"`
	// Kinds are the checks it runs: test, build, lint, vet, typecheck.
	Kinds   []string `json:"kinds"`
	Command string   `json:"command"`
	// Outcome is pass, fail, unclear or running: the worst of its checks.
	Outcome string `json:"outcome"`
	Exit    *int   `json:"exit,omitempty"`
	// Counts is what its output counted ("2 packages ok").
	Counts string `json:"counts,omitempty"`
	TookMS *int64 `json:"took_ms,omitempty"`
	// Note says why the outcome is unclear, or how it passed.
	Note      string `json:"note,omitempty"`
	HasOutput bool   `json:"has_output"`

	at       time.Time
	outcomes map[string]string
}

// EvidenceClaim is one sentence of a turn's final message that claims what
// evidence should back.
type EvidenceClaim struct {
	Text     string `json:"text"`
	Verified bool   `json:"verified"`
	// Detail is what backs it ("go test ./... · exit 0"), or "Not verified ·
	// why".
	Detail string `json:"detail"`
}

// TurnFile is one file the turn's edit tools changed, relative to the
// repository as Changes lists it, with its line counts when Changes lists
// it.
type TurnFile struct {
	Path      string `json:"path"`
	Additions *int   `json:"additions,omitempty"`
	Deletions *int   `json:"deletions,omitempty"`
}

// SinceSummary says what changed in a Task between two looks.
type SinceSummary struct {
	// Text is "the agent finished, ran the tests, and changed 3 files".
	Text string `json:"text"`
	// IDs are the main agent's items after the mark, oldest first.
	IDs []string `json:"ids"`
}

// TurnEvidence is GET /api/sessions/{id}/evidence: the evidence of the
// Task's latest turn and, when asked, what changed since a look.
type TurnEvidence struct {
	Checks []EvidenceCheck `json:"checks"`
	Claims []EvidenceClaim `json:"claims"`
	Files  []TurnFile      `json:"files"`
	Since  *SinceSummary   `json:"since,omitempty"`
}

// turnFacts is a turn's evidence as the locked session holds it.
type turnFacts struct {
	start  time.Time
	checks []EvidenceCheck
	// final is the turn's last main-agent message with text.
	final string
	// edits are the paths the turn's edit tools touched, as they gave them,
	// with when each was last touched, in that order.
	edits  []turnEdit
	failed int
}

type turnEdit struct {
	path string
	at   time.Time
}

// sortedActivity lists the IDs of s's activity that keep, in time order.
func (s *webSession) sortedActivity(keep func(activity) bool) []string {
	var ids []string
	for id, a := range s.activity {
		if keep(a) {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(x, y string) int {
		return cmp.Or(s.activity[x].at.Compare(s.activity[y].at), strings.Compare(x, y))
	})
	return ids
}

// turnFacts gathers the evidence of s's latest turn; ok is false before
// any turn began.
func (s *webSession) turnFacts() (f turnFacts, ok bool) {
	if s.turnStart.IsZero() {
		return f, false
	}
	f.start = s.turnStart
	inTurn := func(a activity) bool { return !a.at.Before(s.turnStart) }
	finalID := ""
	for _, id := range s.sortedActivity(inTurn) {
		a := s.activity[id]
		if a.text {
			finalID = id
		}
		if !a.shell {
			continue
		}
		if a.status == agentapi.ToolFailed || a.exit != nil && *a.exit != 0 {
			f.failed++
		}
		if a.checks == nil {
			continue
		}
		c := EvidenceCheck{ItemID: id, Command: a.command, Exit: a.exit, HasOutput: a.output, at: a.at, outcomes: map[string]string{}}
		if a.counts != nil && slices.ContainsFunc(a.checks, func(r checkRun) bool { return r.kind == checkTest }) {
			c.Counts = a.counts.label
		}
		if !a.ended.IsZero() && !a.ended.Before(a.at) {
			ms := a.ended.Sub(a.at).Milliseconds()
			c.TookMS = &ms
		}
		rank := map[string]int{outcomePass: 0, outcomeUnclear: 1, outcomeRunning: 2, outcomeFail: 3}
		c.Outcome = outcomePass
		for _, run := range a.checks {
			outcome, note := a.result(run)
			if prev, seen := c.outcomes[run.kind]; !seen || rank[outcome] > rank[prev] {
				c.outcomes[run.kind] = outcome
			}
			if !slices.Contains(c.Kinds, run.kind) {
				c.Kinds = append(c.Kinds, run.kind)
			}
			if rank[outcome] > rank[c.Outcome] || c.Note == "" && outcome == c.Outcome {
				c.Outcome, c.Note = outcome, note
			}
		}
		f.checks = append(f.checks, c)
	}
	if i, held := s.itemIdx[itemKey("", finalID)]; finalID != "" && held {
		f.final = s.items[i].Text
	}
	for p, at := range s.edits {
		if !at.Before(s.turnStart) {
			f.edits = append(f.edits, turnEdit{p, at})
		}
	}
	slices.SortFunc(f.edits, func(x, y turnEdit) int { return cmp.Or(x.at.Compare(y.at), strings.Compare(x.path, y.path)) })
	return f, true
}

// files counts the distinct files of the turn's edits in workdir.
func (f turnFacts) files(workdir string) int {
	seen := map[string]bool{}
	for _, e := range f.edits {
		p := e.path
		if !filepath.IsAbs(p) {
			p = filepath.Join(workdir, p)
		}
		seen[filepath.Clean(p)] = true
	}
	return len(seen)
}

// tests is the result of the turn's last test run: pass, fail, or "" when
// it ran none or that run's result is unclear.
func (f turnFacts) tests() string {
	for i := len(f.checks) - 1; i >= 0; i-- {
		if outcome, ok := f.checks[i].outcomes[checkTest]; ok {
			if outcome == outcomePass || outcome == outcomeFail {
				return outcome
			}
			return ""
		}
	}
	return ""
}

/* ---------- Claims ---------- */

var (
	// docPathRE matches a doc file: README, CHANGELOG, Markdown or docs/.
	docPathRE = regexp.MustCompile(`(?i)(?:^|/)(?:README|CHANGELOG|CONTRIBUTING)[^/]*$|\.(?:md|mdx|rst|adoc)$|(?:^|/)docs?/`)
	// noProblemRE are positive phrases built from negative words.
	noProblemRE = regexp.MustCompile(`\b(?:no|without|zero) (?:\w+ )?(?:issues|warnings|errors|problems|findings|failures|regressions)\b`)
	noFailedRE  = regexp.MustCompile(`\b(?:no|none of the|zero) (tests?|checks?|specs?) (?:failed|fail|failing)\b`)
	negationRE  = regexp.MustCompile(`\b(?:not|no|never|none|didn['’]t|don['’]t|doesn['’]t|isn['’]t|aren['’]t|wasn['’]t|weren['’]t|won['’]t|couldn['’]t|can['’]t|cannot|unable|fail(?:s|ed|ing|ure|ures)?|errors?|broken|broke|skipp(?:ed|ing))\b`)
	goodRE      = regexp.MustCompile(`\b(?:pass(?:es|ed|ing)?|green|succeed(?:s|ed)?|successful(?:ly)?|clean(?:ly)?|ok)\b`)
	clauseRE    = regexp.MustCompile(`[;:,—–]|\b(?:and|but|although|though|while|whereas|except)\b`)
	testWordRE  = regexp.MustCompile(`\b(?:tests?|test suite|specs?)\b`)
	buildWordRE = regexp.MustCompile(`\b(?:build|builds|built)\b`)
	compilesRE  = regexp.MustCompile(`\bcompiles?\b|\bcompiled (?:successfully|cleanly)\b`)
	vetWordRE   = regexp.MustCompile(`\b(?:go vet|vet)\b`)
	lintWordRE  = regexp.MustCompile(`\b(?:lint|linter|linters|linting|eslint|golangci-lint|clippy|ruff)\b`)
	typeWordRE  = regexp.MustCompile(`\b(?:type[- ]?check(?:s|ed|ing)?|types? check|tsc|mypy|pyright)\b`)
	docWordRE   = regexp.MustCompile(`\b(?:readme|changelog|docs?|documentation|[\w-]+\.(?:md|mdx|rst))\b`)
	docVerbRE   = regexp.MustCompile(`\b(?:updat(?:e|es|ed)|add(?:s|ed)?|document(?:s|ed)?|describ(?:e|es|ed)|mention(?:s|ed)?|not(?:e|es|ed)|wrote|written|rewr(?:ote|itten)|fix(?:es|ed)?|chang(?:e|es|ed)|explain(?:s|ed)?|cover(?:s|ed)?)\b`)
	docNameRE   = regexp.MustCompile(`(?i)\b(README|CHANGELOG|CONTRIBUTING|[\w./-]+\.(?:md|mdx|rst))\b`)
)

const claimDocs = "docs"

// claimTopics are what a sentence claims went well: tests, the build,
// linting, vetting, type checks or docs. A topic is claimed only where a
// clause naming it says nothing negative ("I did not run the tests, but the
// build passes" claims the build alone).
func claimTopics(sentence string) []string {
	s := strings.ToLower(sentence)
	s = noFailedRE.ReplaceAllString(s, "$1 ok")
	s = noProblemRE.ReplaceAllString(s, "ok")
	clauses := clauseRE.Split(s, -1)
	// A topic is claimed when a clause naming it is not negative.
	named := func(re *regexp.Regexp) bool {
		for _, c := range clauses {
			if re.MatchString(c) && !negationRE.MatchString(c) {
				return true
			}
		}
		return false
	}
	good := goodRE.MatchString(s)
	var out []string
	if good && named(testWordRE) {
		out = append(out, checkTest)
	}
	if good && named(buildWordRE) || named(compilesRE) {
		out = append(out, checkBuild)
	}
	if good && named(vetWordRE) {
		out = append(out, checkVet)
	} else if good && named(lintWordRE) {
		out = append(out, checkLint)
	}
	if good && named(typeWordRE) {
		out = append(out, checkTypecheck)
	}
	if docVerbRE.MatchString(s) && named(docWordRE) {
		out = append(out, claimDocs)
	}
	return out
}

var (
	codeFenceRE  = regexp.MustCompile("(?s)```.*?(?:```|$)")
	inlineCodeRE = regexp.MustCompile("`([^`\n]*)`")
	lineMarkRE   = regexp.MustCompile(`^\s*(?:#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)`)
	boldRE       = regexp.MustCompile(`\*\*|__`)
)

// sentences are a final message's sentences: prose only (code blocks out,
// inline code marks dropped), list marks and headings stripped.
func sentences(text string) []string {
	prose := inlineCodeRE.ReplaceAllString(codeFenceRE.ReplaceAllString(text, "\n"), "$1")
	var out []string
	for _, line := range strings.Split(prose, "\n") {
		line = strings.TrimSpace(boldRE.ReplaceAllString(lineMarkRE.ReplaceAllString(line, ""), ""))
		for _, s := range splitSentences(line) {
			if n := utf8.RuneCountInString(s); n > 3 && n <= 400 {
				out = append(out, s)
			}
		}
	}
	return out
}

// splitSentences cuts a line after `.`, `!` or `?` followed by space and
// a word, a quote or a parenthesis, except after "e.g.", "i.e.", "etc.",
// "vs." and "...".
func splitSentences(line string) []string {
	var out []string
	start := 0
	for i := 0; i < len(line); i++ {
		if c := line[i]; c != '.' && c != '!' && c != '?' {
			continue
		}
		j := i + 1
		for j < len(line) && (line[j] == ' ' || line[j] == '\t') {
			j++
		}
		if j == i+1 || j == len(line) {
			continue
		}
		next, _ := utf8.DecodeRuneInString(line[j:])
		if !unicode.IsLetter(next) && !unicode.IsDigit(next) && !strings.ContainsRune("_`\"'(", next) {
			continue
		}
		head := strings.ToLower(line[start : i+1])
		if strings.HasSuffix(head, "...") || slices.ContainsFunc([]string{"e.g.", "i.e.", "etc.", "vs."}, func(abbr string) bool {
			return strings.HasSuffix(head, abbr) && (len(head) == len(abbr) || !isWordByte(head[len(head)-len(abbr)-1]))
		}) {
			continue
		}
		out = append(out, strings.TrimSpace(line[start:i+1]))
		start = j
	}
	if rest := strings.TrimSpace(line[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

func isWordByte(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// backers are the checks that back each claim topic.
var backers = map[string][]string{
	checkTest:      {checkTest},
	checkBuild:     {checkBuild, checkTypecheck},
	checkLint:      {checkLint, checkVet},
	checkVet:       {checkVet},
	checkTypecheck: {checkTypecheck, checkBuild},
}

var checkNoun = map[string]string{checkTest: "test run", checkBuild: "build", checkLint: "lint run", checkVet: "vet run", checkTypecheck: "type check"}

// judgeClaims judges each claim of the turn's final message against its
// evidence. A check topic is verified by the turn's last check of a backing
// kind passing, with no file other than a doc edited after it began, by the
// main agent or a subagent; a docs topic by an edit to a doc file, the named
// one when the sentence names one. A sentence with several topics is
// verified only when every topic is. Paths are relative to workdir.
func (f turnFacts) judgeClaims(workdir string) []EvidenceClaim {
	out := []EvidenceClaim{}
	var lastEdit time.Time
	var docs []string
	for _, e := range f.edits {
		p := relativeTo(e.path, workdir)
		if docPathRE.MatchString(p) {
			if !slices.Contains(docs, p) {
				docs = append(docs, p)
			}
		} else if e.at.After(lastEdit) {
			lastEdit = e.at
		}
	}
	for _, sentence := range sentences(f.final) {
		topics := claimTopics(sentence)
		if len(topics) == 0 {
			continue
		}
		var backs []string
		reason := ""
		fail := func(why string) {
			if reason == "" {
				reason = why
			}
		}
		for _, topic := range topics {
			if topic == claimDocs {
				var stems []string
				named := docNameRE.FindAllStringSubmatch(sentence, -1)
				for _, n := range named {
					stem := strings.ToLower(n[1][strings.LastIndex(n[1], "/")+1:])
					stems = append(stems, strings.TrimSuffix(strings.TrimSuffix(strings.TrimSuffix(stem, ".md"), ".mdx"), ".rst"))
				}
				hit := slices.IndexFunc(docs, func(p string) bool {
					base := strings.ToLower(p[strings.LastIndex(p, "/")+1:])
					return len(stems) == 0 || slices.ContainsFunc(stems, func(stem string) bool { return strings.HasPrefix(base, stem) })
				})
				switch {
				case hit >= 0:
					backs = append(backs, "edited "+docs[hit])
				case len(named) > 0:
					fail("no edit to " + named[0][1] + " in this turn")
				default:
					fail("no doc file edited in this turn")
				}
				continue
			}
			last := -1
			for i, c := range f.checks {
				if slices.ContainsFunc(backers[topic], func(k string) bool { _, ok := c.outcomes[k]; return ok }) {
					last = i
				}
			}
			if last < 0 {
				fail("no " + checkNoun[topic] + " in this turn")
				continue
			}
			c := f.checks[last]
			outcome := outcomePass
			for _, k := range backers[topic] {
				if o, ok := c.outcomes[k]; ok && o != outcomePass {
					outcome = o
				}
			}
			switch {
			case outcome == outcomeFail:
				if c.Exit != nil && *c.Exit != 0 {
					fail(fmt.Sprintf("the last run failed (exit %d)", *c.Exit))
				} else {
					fail("the last run failed")
				}
			case outcome != outcomePass:
				fail("the last run’s result is unclear")
			case !lastEdit.Before(c.at):
				fail("files changed after the last run")
			default:
				how := c.Note
				if c.Exit != nil && c.Note == "" {
					how = fmt.Sprintf("exit %d", *c.Exit)
				}
				backs = append(backs, oneLine(c.Command, maxDetailCommand)+" · "+how)
			}
		}
		if reason != "" {
			out = append(out, EvidenceClaim{Text: sentence, Detail: "Not verified · " + reason})
		} else {
			out = append(out, EvidenceClaim{Text: sentence, Verified: true, Detail: strings.Join(backs, " · ")})
		}
	}
	return out
}

// relativeTo is p relative to dir when it is inside it.
func relativeTo(p, dir string) string {
	if !filepath.IsAbs(p) {
		return filepath.ToSlash(filepath.Clean(p))
	}
	if rel, err := filepath.Rel(dir, p); err == nil && filepath.IsLocal(rel) {
		return filepath.ToSlash(rel)
	}
	return p
}

/* ---------- Since you left ---------- */

// series joins "a", "a and b", "a, b, and c".
func series(parts []string) string {
	if len(parts) < 3 {
		return strings.Join(parts, " and ")
	}
	return strings.Join(parts[:len(parts)-1], ", ") + ", and " + parts[len(parts)-1]
}

func count(n int, word string) string {
	return fmt.Sprintf("%d %s", n, plural(n, word, word+"s"))
}

// sinceYouLeft says what changed in s after mark (the owner's last look)
// and up to until (when they came back; what happens while they watch is
// seen): turns that finished, failed or stopped, the main agent's commands
// (the tests named), the files the Task's edit tools changed and the
// questions asked. Nil when the main agent did nothing in between.
func (s *webSession) sinceYouLeft(mark, until time.Time) *SinceSummary {
	after := func(t time.Time) bool { return t.After(mark) && !t.After(until) }
	fresh := s.sortedActivity(func(a activity) bool { return after(a.at) })
	if len(fresh) == 0 {
		return nil
	}
	var parts []string
	completed, last := 0, ""
	for _, t := range s.turnTimings {
		if !t.EndedAt.IsZero() && after(t.EndedAt) {
			last = t.State
			if t.State == StateCompleted {
				completed++
			}
		}
	}
	switch state := s.state(); {
	case state == StateWorking || state == StateStarting:
		parts = append(parts, "the agent is still working")
	case last == StateCompleted && completed > 1:
		parts = append(parts, fmt.Sprintf("the agent finished %d turns", completed))
	case last == StateCompleted:
		parts = append(parts, "the agent finished")
	case last == StateFailed:
		parts = append(parts, "the turn failed")
	case last == StateCancelled:
		parts = append(parts, "the turn was stopped")
	}
	commands, tests := 0, 0
	asked := map[string]bool{}
	for _, id := range fresh {
		a := s.activity[id]
		if a.shell {
			commands++
			if slices.ContainsFunc(a.checks, func(r checkRun) bool { return r.kind == checkTest }) {
				tests++
			}
		}
		if a.question {
			asked[id] = true
		}
	}
	switch other := commands - tests; {
	case tests > 0 && other > 0:
		parts = append(parts, "ran the tests plus "+count(other, "other command"))
	case tests > 0:
		parts = append(parts, "ran the tests")
	case other > 0:
		parts = append(parts, "ran "+count(other, "command"))
	}
	files := map[string]bool{}
	for p, at := range s.edits {
		if after(at) {
			if !filepath.IsAbs(p) {
				p = filepath.Join(s.workdir, p)
			}
			files[filepath.Clean(p)] = true
		}
	}
	if len(files) > 0 {
		parts = append(parts, "changed "+count(len(files), "file"))
	}
	for _, ix := range s.interactions {
		if ix.Kind == agentapi.InteractionQuestion && after(ix.Time) {
			asked[cmp.Or(ix.ToolCallID, ix.ID)] = true
		}
	}
	switch n := len(asked); {
	case n == 1:
		parts = append(parts, "asked you a question")
	case n > 1:
		parts = append(parts, fmt.Sprintf("asked you %d questions", n))
	}
	if len(parts) == 0 {
		return nil
	}
	return &SinceSummary{Text: series(parts), IDs: fresh}
}

/* ---------- The route ---------- */

// TurnEvidence is the evidence of Task id's latest turn and, when since is
// set, what changed in it between since and until (zero: now).
func (m *Manager) TurnEvidence(ctx context.Context, id string, since, until time.Time) (TurnEvidence, error) {
	s, err := m.lookup(id)
	if err != nil {
		return TurnEvidence{}, err
	}
	out := TurnEvidence{Checks: []EvidenceCheck{}, Claims: []EvidenceClaim{}, Files: []TurnFile{}}
	m.mu.Lock()
	if until.IsZero() {
		until = m.now()
	}
	if !since.IsZero() {
		out.Since = s.sinceYouLeft(since, until)
	}
	facts, ok := s.turnFacts()
	workdir, edits := s.workdir, maps.Clone(s.edits)
	m.mu.Unlock()
	if !ok {
		return out, nil
	}
	out.Checks = append(out.Checks, facts.checks...)
	for i := range out.Checks {
		out.Checks[i].Command = strings.TrimSpace(out.Checks[i].Command)
	}
	out.Claims = facts.judgeClaims(workdir)
	out.Files = turnFiles(ctx, workdir, edits, facts)
	return out, nil
}

// turnFiles lists the turn's edited files as Changes names them, relative
// to the repository, with the line counts of those it lists; relative to
// workdir without git.
func turnFiles(ctx context.Context, workdir string, edits map[string]time.Time, facts turnFacts) []TurnFile {
	l, err := listTaskChanges(ctx, workdir, edits, facts.start)
	resolve := func(p string) (string, bool) { return relativeTo(p, workdir), true }
	if err == nil && l.repo != nil {
		inRepo := repoPaths(l.repo.top, workdir)
		resolve = func(p string) (string, bool) {
			if rel, ok := inRepo(p); ok {
				return rel, true
			}
			return filepath.Clean(p), true
		}
	}
	listed := map[string]ChangedFile{}
	for _, f := range l.turn {
		listed[f.Path] = f
	}
	out := []TurnFile{}
	seen := map[string]bool{}
	for _, e := range facts.edits {
		rel, _ := resolve(e.path)
		if seen[rel] {
			continue
		}
		seen[rel] = true
		f := TurnFile{Path: rel}
		if c, ok := listed[rel]; ok {
			f.Additions, f.Deletions = &c.Additions, &c.Deletions
		}
		out = append(out, f)
	}
	return out
}

func (s *Server) handleTurnEvidence(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var since, until time.Time
	for _, p := range []struct {
		name string
		at   *time.Time
	}{{"since", &since}, {"until", &until}} {
		if v := q.Get(p.name); v != "" {
			t, err := time.Parse(time.RFC3339Nano, v)
			if err != nil {
				writeError(w, http.StatusBadRequest, p.name+" must be an RFC 3339 time")
				return
			}
			*p.at = t
		}
	}
	ev, err := s.m.TurnEvidence(r.Context(), r.PathValue("id"), since, until)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, ev)
}
