package pr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

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
	exe, err := ghExecutable()
	if err != nil {
		return None, err
	}
	cmd := exec.CommandContext(ctx, exe, "pr", "view", url, "--json", "state,isDraft,mergedAt") // #nosec G204 -- gh path is resolved from fixed system directories; URL is passed as an argv argument with no shell expansion.
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
