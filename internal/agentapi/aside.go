package agentapi

import "context"

// AsideAsker answers a transient question from an already-open
// conversation's context with its current model. The answer is not added to
// the conversation's history and starts no turn or tool. Cancelling ctx only
// stops waiting; it does not prove the provider stopped the model call.
type AsideAsker interface {
	AskAside(ctx context.Context, question string) (*AsideAnswer, error)
}

// AsideAnswer is the bounded result of one aside question.
type AsideAnswer struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated,omitempty"`
}
