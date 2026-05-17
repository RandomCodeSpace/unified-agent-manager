package pr

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
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
	if _, err := exec.LookPath("gh"); err != nil {
		return None, err
	}
	cmd := exec.CommandContext(ctx, "gh", "pr", "view", url, "--json", "state,isDraft,mergedAt")
	out, err := cmd.Output()
	if err != nil {
		return None, fmt.Errorf("gh pr view: %w", err)
	}
	return ParseGHState(out)
}
