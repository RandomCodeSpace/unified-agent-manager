package web

import (
	"slices"
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// maxAskRunes caps an Ask's title and detail: the Task list shows one line.
const maxAskRunes = 200

// Ask is the request a Task waits on, small enough for the Task list to show
// and answer without opening the Task: a permission with its options and the
// first line of what it asks for, or a question's first prompt with its
// choices. The Task's detail still carries every interaction whole.
type Ask struct {
	ID   string                   `json:"id"`
	Kind agentapi.InteractionKind `json:"kind"`
	// Title is a permission's title, or the first line of a question's text
	// (its header, else the interaction title, when the text is empty).
	Title string `json:"title"`
	// Detail is the first line of a permission's detail: the command, path
	// or URL it is for.
	Detail   string            `json:"detail,omitempty"`
	Options  []agentapi.Option `json:"options,omitempty"`
	Choices  []string          `json:"choices,omitempty"`
	Multiple bool              `json:"multiple,omitempty"`
	Custom   bool              `json:"custom,omitempty"`
	// Questions counts a question's prompts; the list answers only a single
	// one in place.
	Questions int `json:"questions,omitempty"`
}

// pendingAsk is the Ask for the request the state reports: the first pending
// permission, else the first pending question. Requests yolo mode is
// answering do not wait for the user and are skipped. The pointer is kept
// while the Ask is unchanged, so an unchanged summary still compares equal
// and publishes nothing.
func (s *webSession) pendingAsk() *Ask {
	var first *interaction
	for _, ix := range s.interactions {
		if ix.State != agentapi.InteractionPending || ix.yolo {
			continue
		}
		if ix.Kind == agentapi.InteractionPermission {
			first = ix
			break
		}
		if first == nil {
			first = ix
		}
	}
	if first == nil {
		s.ask = nil
		return nil
	}
	next := askOf(first.Interaction)
	if s.ask == nil || !sameAsk(*s.ask, next) {
		s.ask = &next
	}
	return s.ask
}

func askOf(ix agentapi.Interaction) Ask {
	a := Ask{ID: ix.ID, Kind: ix.Kind, Title: ix.Title}
	if ix.Kind == agentapi.InteractionPermission {
		a.Detail = clipRunes(firstLine(ix.Detail), maxAskRunes)
		a.Options = ix.Options
	} else if len(ix.Questions) > 0 {
		q := ix.Questions[0]
		a.Title = firstNonEmpty(firstLine(q.Text), strings.TrimSpace(q.Header), ix.Title)
		a.Choices, a.Multiple, a.Custom, a.Questions = q.Choices, q.Multiple, q.Custom, len(ix.Questions)
	}
	a.Title = clipRunes(a.Title, maxAskRunes)
	return a
}

func sameAsk(a, b Ask) bool {
	return a.ID == b.ID && a.Kind == b.Kind && a.Title == b.Title && a.Detail == b.Detail &&
		a.Multiple == b.Multiple && a.Custom == b.Custom && a.Questions == b.Questions &&
		slices.Equal(a.Options, b.Options) && slices.Equal(a.Choices, b.Choices)
}
