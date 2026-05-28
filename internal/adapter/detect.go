package adapter

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// ClassifyPane reduces a pane's observable state to {Active, Failed}, based
// solely on whether the pane process is alive. We used to text-scrape the
// capture for richer states (Working / NeedsInput / Completed / etc.), but
// those signals were keyword guesses on prose and produced more false
// positives than real signal. The activity summary line still comes from
// the captured lines via summarize(); only the lifecycle state stops
// depending on pattern matching.
func ClassifyPane(lines []string, paneAlive bool) (State, ProcLiveness, string) {
	live := Exited
	state := Failed
	if paneAlive {
		live = Alive
		state = Active
	}
	return state, live, summarize(lines)
}

func summarize(lines []string) string {
	start := 0
	if len(lines) > 20 {
		start = len(lines) - 20
	}
	best := ""
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if line == "" || isDecorativeLine(line) || strings.ContainsAny(line, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏✻✽") {
			continue
		}
		if len(line) > len(best) {
			best = line
		}
	}
	return best
}

func isDecorativeLine(line string) bool {
	line = strings.TrimSpace(line)
	if len([]rune(line)) < 6 {
		return false
	}
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		if strings.ContainsRune("?!$%#@/\\", r) {
			return false
		}
	}
	return true
}

var prRE = regexp.MustCompile(`https://github\.com/([^/\s]+)/([^/\s]+)/pull/(\d+)`)

func ExtractPR(text string) *PRRef {
	m := prRE.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	n, _ := strconv.Atoi(m[3])
	return &PRRef{URL: m[0], Owner: m[1], Repo: m[2], Number: n, Status: PROpen}
}
