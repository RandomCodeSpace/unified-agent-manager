package web

import (
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// LoadedSkill is a skill the Task's agents loaded. Skill loads stay out of
// the transcript; the Tools panel lists them from here.
type LoadedSkill struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	FirstAt     time.Time `json:"first_at"`
	Count       int       `json:"count"`
}

type skillsLoadedEvent struct {
	Seq          uint64        `json:"seq"`
	SessionID    string        `json:"session_id"`
	SkillsLoaded []LoadedSkill `json:"skills_loaded"`
}

const (
	// maxLoadedSkills bounds the distinct skills a Task lists; later new
	// names are not listed.
	maxLoadedSkills          = 64
	maxSkillDescriptionBytes = 1024
)

// noteSkillsLocked counts loads not seen yet, by agent and provider event
// ID, so a load both streamed and in the record counts once, and tells
// viewers when the list changed.
func (m *Manager) noteSkillsLocked(s *webSession, loads []agentapi.SkillLoad) {
	changed := false
	for _, load := range loads {
		key := [2]string{load.AgentID, load.ID}
		if _, seen := s.skillLoads[key]; seen && load.ID != "" {
			continue
		}
		name := clipRunes(strings.TrimSpace(displaytext.Sanitize(load.Name)), maxNameRunes)
		if name == "" {
			continue
		}
		i := slices.IndexFunc(s.skills, func(k LoadedSkill) bool { return k.Name == name })
		if i < 0 && len(s.skills) >= maxLoadedSkills {
			continue
		}
		if s.skillLoads == nil {
			s.skillLoads = map[[2]string]struct{}{}
		}
		s.skillLoads[key] = struct{}{}
		changed = true
		description := clampText(strings.TrimSpace(displaytext.Sanitize(load.Description)), maxSkillDescriptionBytes)
		if i < 0 {
			s.skills = append(s.skills, LoadedSkill{Name: name, Description: description, FirstAt: load.At, Count: 1})
			continue
		}
		k := &s.skills[i]
		k.Count++
		if load.At.Before(k.FirstAt) {
			k.FirstAt = load.At
		}
		if description != "" {
			k.Description = description
		}
	}
	if !changed {
		return
	}
	// First loaded first; a record read after live loads may hold earlier ones.
	slices.SortStableFunc(s.skills, func(a, b LoadedSkill) int { return a.FirstAt.Compare(b.FirstAt) })
	list := slices.Clone(s.skills)
	m.broadcastLocked("skills_loaded", s.id, func(seq uint64) any {
		return skillsLoadedEvent{Seq: seq, SessionID: s.id, SkillsLoaded: list}
	})
}
