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

// builtinOutputStyles ship with uam: offered everywhere next to the
// library, read only, and their names are reserved.
var builtinOutputStyles = []store.WebOutputStyle{{
	Name: "Simplified Technical English",
	// ASD-STE100 writing rules plus brevity.
	Text: "Write in ASD-STE100 Simplified Technical English. Be brief.\n" +
		"\n" +
		"Length:\n" +
		"- Give the answer first. No preamble, no restating the question, no closing summary.\n" +
		"- Use only the words the reader needs. Stop when the answer is complete.\n" +
		"- Prefer a short list or table to long paragraphs. Maximum 6 sentences in a paragraph.\n" +
		"\n" +
		"Sentences:\n" +
		"- One topic in each sentence.\n" +
		"- Instructions: maximum 20 words. Descriptions: maximum 25 words.\n" +
		"- Write instructions in the imperative (\"Run the test.\"). Give one instruction in each sentence, unless the actions occur at the same time.\n" +
		"- Use the active voice. Use the simple tenses (present, past, future).\n" +
		"- Keep articles (\"the\", \"a\") and use them where possible.\n" +
		"\n" +
		"Words:\n" +
		"- Use simple, common words. Use each word with one meaning only.\n" +
		"- Do not use phrasal verbs (\"set up\", \"find out\"). Use one verb (\"install\", \"find\").\n" +
		"- Do not use -ing words as verbs or nouns, except in technical names.\n" +
		"- Do not use idioms, slang, humour or filler (\"basically\", \"just\", \"simply\", \"I think\").\n" +
		"- Use the same term for the same thing every time.\n" +
		"\n" +
		"Keep exact: code, commands, file paths, identifiers, error messages and quoted text. Do not simplify them.\n" +
		"\n" +
		"Warnings and cautions: start with a simple command, then give the reason.",
}}

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
		case slices.ContainsFunc(builtinOutputStyles, func(b store.WebOutputStyle) bool { return strings.EqualFold(b.Name, st.Name) }):
			return nil, newError(http.StatusBadRequest, "%q is a built-in style", st.Name)
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

// outputStyleText finds name in the library, then in the built-in styles.
func outputStyleText(lib []store.WebOutputStyle, name string) (string, bool) {
	for _, list := range [][]store.WebOutputStyle{lib, builtinOutputStyles} {
		if i := slices.IndexFunc(list, func(st store.WebOutputStyle) bool { return st.Name == name }); i >= 0 {
			return list[i].Text, true
		}
	}
	return "", false
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
