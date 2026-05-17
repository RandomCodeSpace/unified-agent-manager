package adapter

import (
	"regexp"
	"strconv"
	"strings"
)

type Patterns struct {
	NeedsInputRegex []*regexp.Regexp
	WorkingRegex    []*regexp.Regexp
	IdleRegex       []*regexp.Regexp
	FailedRegex     []*regexp.Regexp
	SpinnerRunes    string
}

func DefaultPatterns(agent string) Patterns {
	commonNeeds := []*regexp.Regexp{
		re(`(?i)do you want to|continue\?|approve|permission|allow|confirm|proceed|run this command|accept`),
		re(`(?i)\b(y/n|yes/no)\b`),
		re(`(?m)^\s*[0-9]+[.)]\s+`),
	}
	commonWork := []*regexp.Regexp{re(`(?i)thinking|generating|working|running|analyzing|searching|editing|tool use|executing`)}
	commonIdle := []*regexp.Regexp{re(`^\s*[>❯]\s*$`), re(`(?i)try .* or type`)}
	commonFail := []*regexp.Regexp{re(`(?i)\berror\b|failed|panic|traceback|fatal`)}
	p := Patterns{NeedsInputRegex: commonNeeds, WorkingRegex: commonWork, IdleRegex: commonIdle, FailedRegex: commonFail, SpinnerRunes: "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏✻✽◐◓◑◒"}
	switch agent {
	case "claude":
		p.WorkingRegex = append(p.WorkingRegex, re(`✻|✽|Bash\(|Read\(|Edit\(|Write\(`))
	case "codex":
		p.IdleRegex = append(p.IdleRegex, re(`^\s*codex\s*>`))
	case "copilot":
		p.NeedsInputRegex = append(p.NeedsInputRegex, re(`(?i)Run|Explain|Revise`))
	case "opencode":
		p.NeedsInputRegex = append(p.NeedsInputRegex, re(`(?i)approve file|allow tool|confirm edit`))
	}
	return p
}

func re(s string) *regexp.Regexp { return regexp.MustCompile(s) }

func ClassifyPane(lines []string, paneCommand string, paneAlive bool, changedRecently bool, patterns Patterns) (State, ProcLiveness, string) {
	live := Exited
	if paneAlive {
		live = Alive
	}
	summary := summarize(lines)
	if !paneAlive || paneCommand == "bash" || paneCommand == "sh" || paneCommand == "zsh" {
		return Completed, live, summary
	}
	joinedTail := tail(lines, 12)
	if ExtractPR(joinedTail) != nil {
		return ReadyForReview, live, summary
	}
	if anyMatch(patterns.NeedsInputRegex, joinedTail) {
		return NeedsInput, live, summary
	}
	if anyMatch(patterns.FailedRegex, joinedTail) {
		return Failed, live, summary
	}
	if anyMatch(patterns.WorkingRegex, joinedTail) || strings.ContainsAny(joinedTail, patterns.SpinnerRunes) {
		return Working, live, summary
	}
	if anyMatch(patterns.IdleRegex, strings.TrimSpace(lastNonBlank(lines))) {
		return Completed, live, summary
	}
	if changedRecently {
		return Working, live, summary
	}
	return Completed, live, summary
}

func anyMatch(regexps []*regexp.Regexp, text string) bool {
	for _, r := range regexps {
		if r.MatchString(text) {
			return true
		}
	}
	return false
}

func tail(lines []string, n int) string {
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func summarize(lines []string) string {
	start := 0
	if len(lines) > 20 {
		start = len(lines) - 20
	}
	best := ""
	for _, line := range lines[start:] {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsAny(line, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏✻✽") {
			continue
		}
		if len(line) > len(best) {
			best = line
		}
	}
	return best
}

func lastNonBlank(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
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
