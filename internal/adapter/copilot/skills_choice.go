package copilot

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// disabledSkills turns req's skill list into the session's disabled skills:
// every skill discovery finds for the working directory that the list does
// not name, except those in req's own skill directories. The CLI describes
// skills within a fixed budget and refills freed room from the rest, so
// only a short list saves context. Nil when req has no list, or when
// discovery fails, which offers every skill rather than failing the open.
func (p *webProvider) disabledSkills(ctx context.Context, req agentapi.OpenRequest) []string {
	if req.Skills == nil {
		return nil
	}
	c, err := p.configurationClient(ctx)
	if err != nil {
		log.Warn("skill list not applied", "session", req.SessionID, "error", err)
		return nil
	}
	res, err := c.DiscoverSkills(ctx, []string{req.Workdir}, req.SkillDirectories)
	if err != nil || res == nil {
		log.Warn("skill list not applied", "session", req.SessionID, "error", configurationDiscoveryError(err, "skills"))
		return nil
	}
	off := []string{}
	for _, skill := range res.Skills {
		if slices.Contains(*req.Skills, skill.Name) || slices.Contains(off, skill.Name) || (skill.Path != nil && inDirs(*skill.Path, req.SkillDirectories)) {
			continue
		}
		off = append(off, skill.Name)
	}
	return off
}

func inDirs(path string, dirs []string) bool {
	return slices.ContainsFunc(dirs, func(dir string) bool {
		return strings.HasPrefix(filepath.Clean(path), filepath.Clean(dir)+string(os.PathSeparator))
	})
}
