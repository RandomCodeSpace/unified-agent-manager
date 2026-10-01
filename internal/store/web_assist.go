package store

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// WebSavedPrompt is a message the owner saved to insert again from any
// Task's composer: in every Project when ProjectID is empty, else only in
// that Project's Tasks.
type WebSavedPrompt struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Text      string    `json:"text"`
	ProjectID string    `json:"project_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Saved prompt limits: prompts, runes per name and bytes per text.
const (
	MaxSavedPrompts         = 200
	MaxSavedPromptNameRunes = 80
	MaxSavedPromptBytes     = 16 << 10
)

var savedPromptID = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// ValidSavedPrompt reports why p cannot be stored, or nil: an ID of letters,
// digits and '-', a name of 1 to MaxSavedPromptNameRunes runes without
// control characters, and a text that is not blank, at most
// MaxSavedPromptBytes and without control characters other than newlines
// and tabs.
func ValidSavedPrompt(p WebSavedPrompt) error {
	name := strings.TrimSpace(p.Name)
	switch {
	case !savedPromptID.MatchString(p.ID):
		return errors.New("prompt ID must be 1 to 64 letters, digits or '-'")
	case name == "" || name != p.Name || utf8.RuneCountInString(name) > MaxSavedPromptNameRunes || !utf8.ValidString(name) || hasControlChar(name):
		return fmt.Errorf("prompt name must be 1 to %d characters without control characters or surrounding spaces", MaxSavedPromptNameRunes)
	case strings.TrimSpace(p.Text) == "":
		return errors.New("prompt text is required")
	case len(p.Text) > MaxSavedPromptBytes || !utf8.ValidString(p.Text):
		return fmt.Errorf("prompt text must be valid UTF-8 of at most %d bytes", MaxSavedPromptBytes)
	case strings.ContainsFunc(p.Text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }):
		return errors.New("prompt text may not hold control characters other than newlines and tabs")
	case len(p.ProjectID) > maxWebModelBytes || hasControlChar(p.ProjectID):
		return errors.New("prompt project_id is invalid")
	}
	return nil
}

// cleanSavedPrompts drops loaded saved prompts that could not be stored now,
// a repeated ID and those past MaxSavedPrompts.
func cleanSavedPrompts(w *WebSettings) {
	var keep []WebSavedPrompt
	seen := map[string]bool{}
	for _, p := range w.SavedPrompts {
		if ValidSavedPrompt(p) != nil || seen[p.ID] || len(keep) == MaxSavedPrompts {
			log.Warn("dropping invalid stored saved prompt", "id", p.ID)
			continue
		}
		seen[p.ID] = true
		keep = append(keep, p)
	}
	w.SavedPrompts = keep
}

// WebSuggestions are the replies suggested to the owner once the Task's
// main transcript ended with the item ItemID; Replies may be empty when
// none came. They are kept so the same state is never asked for again.
type WebSuggestions struct {
	ItemID  string   `json:"item_id"`
	Replies []string `json:"replies"`
}
