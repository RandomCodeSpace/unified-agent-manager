package web

import (
	"strings"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// maxAskRunes caps an Ask's title: the Task list shows one line.
const maxAskRunes = 200

// Ask is the request a Task waits on, as the Task list's status line and
// the notices name it: its kind and one line. The Task's detail carries
// every interaction whole.
type Ask struct {
	Kind agentapi.InteractionKind `json:"kind"`
	// Title is a permission's title, or the first line of a question's text
	// (its header, else the interaction title, when the text is empty).
	Title string `json:"title"`
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
	if s.ask == nil || *s.ask != next {
		s.ask = &next
	}
	return s.ask
}

func askOf(ix agentapi.Interaction) Ask {
	a := Ask{Kind: ix.Kind, Title: ix.Title}
	if ix.Elicitation != nil {
		// A form's or a link's message says what it is for; its fields do not.
		a.Title = firstNonEmpty(firstLine(ix.Detail), ix.Title)
	} else if ix.Kind != agentapi.InteractionPermission && len(ix.Questions) > 0 {
		q := ix.Questions[0]
		a.Title = firstNonEmpty(firstLine(q.Text), strings.TrimSpace(q.Header), ix.Title)
	}
	a.Title = clipRunes(a.Title, maxAskRunes)
	return a
}
