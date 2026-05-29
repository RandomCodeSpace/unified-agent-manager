package pr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

var errInvalidURL = errors.New("not a github pull-request url")

// prURLRE anchors the GitHub PR URL shape. It mirrors adapter.prRE (the regex
// used to scrape a PR URL out of pane text) but is anchored end-to-end so the
// whole string must be a well-formed PR URL, leaving no room for a flag-shaped
// argument to slip through to gh.
var prURLRE = regexp.MustCompile(`^https://github\.com/[^/\s]+/[^/\s]+/pull/\d+$`)

// ValidURL reports whether url is a well-formed GitHub pull-request URL.
func ValidURL(url string) bool {
	return prURLRE.MatchString(url)
}

type Status string

const (
	None   Status = "none"
	Open   Status = "open"
	Merged Status = "merged"
	Closed Status = "closed"
	Draft  Status = "draft"
)

type ghState struct {
	State    string  `json:"state"`
	IsDraft  bool    `json:"isDraft"`
	MergedAt *string `json:"mergedAt"`
}

func ParseGHState(data []byte) (Status, error) {
	var s ghState
	if err := json.Unmarshal(data, &s); err != nil {
		return None, err
	}
	if s.IsDraft {
		return Draft, nil
	}
	if s.MergedAt != nil || strings.EqualFold(s.State, "MERGED") {
		return Merged, nil
	}
	switch strings.ToUpper(s.State) {
	case "OPEN":
		return Open, nil
	case "CLOSED":
		return Closed, nil
	default:
		return None, nil
	}
}

func Check(ctx context.Context, url string) (Status, error) {
	if !ValidURL(url) {
		return None, fmt.Errorf("invalid PR URL %q: %w", url, errInvalidURL)
	}
	exe, err := ghExecutable()
	if err != nil {
		return None, err
	}
	// #nosec G204 -- exe is resolved from fixed system dirs (or a validated
	// absolute UAM_GH_BIN); url is validated against prURLRE above and passed
	// after the `--` end-of-options separator as the final argv argument, so it
	// cannot be interpreted as a flag and there is no shell expansion.
	cmd := exec.CommandContext(ctx, exe, "pr", "view", "--json", "state,isDraft,mergedAt", "--", url)
	out, err := cmd.Output()
	if err != nil {
		return None, fmt.Errorf("gh pr view: %w", err)
	}
	return ParseGHState(out)
}

func ghExecutable() (string, error) {
	if v := os.Getenv("UAM_GH_BIN"); v != "" {
		if err := execpath.ValidateAbsoluteExecutable(v); err != nil {
			return "", fmt.Errorf("invalid UAM_GH_BIN: %w", err)
		}
		return v, nil
	}
	return execpath.Resolve("gh")
}
