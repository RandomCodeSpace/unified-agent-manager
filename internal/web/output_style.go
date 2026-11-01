package web

import (
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Output styles are named texts that replace the provider's own response
// style instructions. A Thread's style is fixed when it starts: its own
// choice, else its Project's, else the default.
const (
	// maxOutputStyleRunes keeps a style short: the system message carries it
	// on every model call.
	maxOutputStyleRunes  = 2000
	maxOutputStyleName   = 40
	maxOutputStyles      = 20
	providerOutputStyle  = "Copilot" // a Thread or Project choosing the provider's own style
	msgUnknownOutputName = "output style %q is not in the library"
)

// cleanOutputStyles checks a library: trimmed, non-empty unique names and
// texts within their limits.
func cleanOutputStyles(in []store.WebOutputStyle) ([]store.WebOutputStyle, error) {
	if len(in) > maxOutputStyles {
		return nil, newError(http.StatusBadRequest, "at most %d output styles", maxOutputStyles)
	}
	out := make([]store.WebOutputStyle, 0, len(in))
	for _, st := range in {
		st.Name, st.Text = strings.TrimSpace(st.Name), strings.TrimSpace(st.Text)
		switch {
		case st.Name == "" || utf8.RuneCountInString(st.Name) > maxOutputStyleName || strings.ContainsFunc(st.Name, isControl):
			return nil, newError(http.StatusBadRequest, "an output style name must be 1 to %d characters on one line", maxOutputStyleName)
		case strings.EqualFold(st.Name, providerOutputStyle):
			return nil, newError(http.StatusBadRequest, "%q is reserved for the provider's own style", providerOutputStyle)
		case slices.ContainsFunc(out, func(o store.WebOutputStyle) bool { return strings.EqualFold(o.Name, st.Name) }):
			return nil, newError(http.StatusBadRequest, "output style %q is listed twice", st.Name)
		case st.Text == "" || utf8.RuneCountInString(st.Text) > maxOutputStyleRunes:
			return nil, newError(http.StatusBadRequest, "output style %q must be 1 to %d characters", st.Name, maxOutputStyleRunes)
		}
		out = append(out, st)
	}
	return out, nil
}

func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

func hasOutputStyle(lib []store.WebOutputStyle, name string) bool {
	_, ok := outputStyleText(lib, name)
	return ok
}

func outputStyleText(lib []store.WebOutputStyle, name string) (string, bool) {
	i := slices.IndexFunc(lib, func(st store.WebOutputStyle) bool { return st.Name == name })
	if i < 0 {
		return "", false
	}
	return lib[i].Text, true
}

// checkOutputStyleChoice checks a Thread's or Project's choice: "" to
// inherit, the provider's own, or a style in the library.
func checkOutputStyleChoice(lib []store.WebOutputStyle, name string) error {
	if name == "" || name == providerOutputStyle || hasOutputStyle(lib, name) {
		return nil
	}
	return newError(http.StatusBadRequest, msgUnknownOutputName, name)
}

// pinOutputStyleLocked fixes a new Thread's style name: its own choice,
// else its Project's, else the default, with the provider's own named
// explicitly. Later Project or default changes leave the Thread as it is.
func (m *Manager) pinOutputStyleLocked(s *webSession, projectID string) {
	choices := []string{s.style}
	if p := m.projects[projectID]; p != nil {
		choices = append(choices, p.OutputStyle)
	}
	s.style = providerOutputStyle
	for _, name := range append(choices, m.settings.OutputStyle) {
		if name == providerOutputStyle || hasOutputStyle(m.settings.OutputStyles, name) {
			s.style = name
			return
		}
	}
}

// outputStyleLocked is the style text s opens with: its pinned style's
// current text, or the provider's own when that name left the library.
// A Thread from before styles existed has none pinned and keeps the
// provider's own.
func (m *Manager) outputStyleLocked(s *webSession) string {
	text, _ := outputStyleText(m.settings.OutputStyles, s.style)
	return text
}
