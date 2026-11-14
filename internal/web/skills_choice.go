package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// maxSkillChoices bounds a Thread's or Project's skill list, the same bound
// native discovery lists.
const maxSkillChoices = 128

// cleanSkillChoice checks a Thread's or Project's skill list: nil offers
// every skill, otherwise unique skill names, sorted so equal lists compare
// equal. The names need not exist: a skill installed later under a listed
// name is offered.
func cleanSkillChoice(in *[]string) (*[]string, error) {
	if in == nil {
		return nil, nil
	}
	if len(*in) > maxSkillChoices {
		return nil, newError(http.StatusBadRequest, "at most %d skills", maxSkillChoices)
	}
	out := make([]string, 0, len(*in))
	for _, name := range *in {
		if name == "" || len(name) > 256 || strings.TrimSpace(name) != name || displaytext.Sanitize(name) != name {
			return nil, newError(http.StatusBadRequest, "use the name of a skill Copilot lists")
		}
		out = append(out, name)
	}
	slices.Sort(out)
	out = slices.Compact(out)
	return &out, nil
}

// skillsPatch changes a Project's skill list to list; nil offers every
// skill.
type skillsPatch struct{ list *[]string }

// skillsKey is skills in a comparable form for persistKey; keySkills
// reverses it.
func skillsKey(skills *[]string) string {
	if skills == nil {
		return ""
	}
	data, _ := json.Marshal(*skills)
	return string(data)
}

func keySkills(key string) *[]string {
	if key == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(key), &out) != nil {
		return nil
	}
	return &out
}

// pinSkillsLocked fixes a new Thread's skill list: its own choice, else
// its Project's, else nil for every skill. Later Project changes leave the
// Thread as it is.
func (m *Manager) pinSkillsLocked(s *webSession, projectID string) {
	if s.skillChoice != nil {
		return
	}
	if p := m.projects[projectID]; p != nil && p.Skills != nil {
		list := slices.Clone(*p.Skills)
		s.skillChoice = &list
	}
}

// cloneSkills copies a skill list so two Threads never share one.
func cloneSkills(list *[]string) *[]string {
	if list == nil {
		return nil
	}
	c := slices.Clone(*list)
	return &c
}

// SkillOption is one skill a Thread's list can name.
type SkillOption struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SkillOptions lists the skills a Thread of the Project can be offered, by
// name: what native discovery finds for its folder, without uam's own,
// which every Thread is offered.
func (m *Manager) SkillOptions(projectID string) ([]SkillOption, error) {
	scope, err := m.configurationScope(projectID)
	if err != nil {
		return nil, err
	}
	provider, skillDirs, projectPaths := m.configurationDiscoverer(scope)
	if provider == nil {
		return nil, newError(http.StatusConflict, "no provider can list skills")
	}
	ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
	defer cancel()
	catalog, err := provider.DiscoverSkills(ctx, projectPaths, skillDirs)
	if err != nil {
		return nil, newError(http.StatusBadGateway, "skills could not be listed: %s", shortError(err))
	}
	out := []SkillOption{}
	for _, d := range catalog.Definitions {
		builtin := slices.ContainsFunc(skillDirs, func(dir string) bool {
			return d.Path != "" && strings.HasPrefix(filepath.Clean(d.Path), filepath.Clean(dir)+string(os.PathSeparator))
		})
		if builtin || slices.ContainsFunc(out, func(o SkillOption) bool { return o.Name == d.Name }) {
			continue
		}
		out = append(out, SkillOption{Name: d.Name, Description: d.Description})
	}
	slices.SortFunc(out, func(a, b SkillOption) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// handleSkillOptions answers the skills a list can name and the Project's
// own list, absent when it offers every skill.
func (s *Server) handleSkillOptions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	skills, err := s.m.SkillOptions(id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	s.m.mu.Lock()
	var chosen *[]string
	if p := s.m.projects[id]; p != nil {
		chosen = p.Skills
	}
	s.m.mu.Unlock()
	writeJSON(w, http.StatusOK, struct {
		Skills        []SkillOption `json:"skills"`
		ProjectSkills *[]string     `json:"project_skills,omitempty"`
	}{skills, chosen})
}
