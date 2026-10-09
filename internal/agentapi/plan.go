package agentapi

import "context"

const MaxPlanFeedbackBytes = 64 << 10

// PlanAction is an execution action offered by the provider for this review.
// It is independent of the Task's permission policy.
type PlanAction string

const (
	PlanAutopilot      PlanAction = "autopilot"
	PlanAutopilotFleet PlanAction = "autopilot_fleet"
	PlanInteractive    PlanAction = "interactive"
	PlanExitOnly       PlanAction = "exit_only"
)

func (action PlanAction) Valid() bool {
	switch action {
	case PlanAutopilot, PlanAutopilotFleet, PlanInteractive, PlanExitOnly:
		return true
	default:
		return false
	}
}

// PlanReview is a native reviewed snapshot. Pending reviews carry bounded
// content; transcript entries carry only its request identity and revision.
// A PlanHistoryReader reads the exact historical body when its reader opens.
type PlanReview struct {
	RequestID           string       `json:"request_id"`
	Summary             string       `json:"summary,omitempty"`
	Revision            int          `json:"revision,omitempty"`
	Actions             []PlanAction `json:"actions,omitempty"`
	Recommended         PlanAction   `json:"recommended,omitempty"`
	Content             string       `json:"content,omitempty"`
	Previous            string       `json:"previous,omitempty"`
	Truncated           bool         `json:"truncated,omitempty"`
	PreviousTruncated   bool         `json:"previous_truncated,omitempty"`
	PreviousUnavailable bool         `json:"previous_unavailable,omitempty"`
}

// PlanAnswer takes exactly one offered execution action or nonblank feedback.
// Feedback refuses approval so the provider can revise the plan.
type PlanAnswer struct {
	Action   PlanAction `json:"action,omitempty"`
	Feedback string     `json:"feedback,omitempty"`
}

// PlanHistoryReader reads a native reviewed snapshot without opening or
// resuming the provider conversation. RequestID is an exact native boundary.
type PlanHistoryReader interface {
	ReadPlanReview(context.Context, ReadRequest, string) (PlanReview, error)
}

// PlanDraft is the current scratch plan, distinct from the reviewed snapshot.
// Exists distinguishes a missing/deleted draft from an existing empty plan.
type PlanDraft struct {
	Exists    bool   `json:"exists"`
	Content   string `json:"content,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// PlanDraftReader reads only an already open provider conversation.
type PlanDraftReader interface {
	ReadPlanDraft(context.Context) (PlanDraft, error)
}
