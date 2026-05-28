package adapter

import (
	"regexp"
	"strconv"
)

// ClassifyPane maps a pane's process liveness to the session lifecycle state.
// We deliberately do not inspect pane content: these agents render full-screen
// TUIs, so a capture is dominated by chrome (footer, status line, input box)
// rather than a clean log, which made any text-scraped state or activity
// summary unreliable. State is grounded only in whether the pane PID is alive.
func ClassifyPane(paneAlive bool) (State, ProcLiveness) {
	if paneAlive {
		return Active, Alive
	}
	return Failed, Exited
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
