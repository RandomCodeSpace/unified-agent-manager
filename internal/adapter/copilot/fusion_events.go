package copilot

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// maxFusionNote caps a reviewer's feedback and defect on its phase.
const maxFusionNote = 600

// syntheticLevels names Copilot Fusion's levels by the synthetic model a route
// reports.
var syntheticLevels = map[string]string{"balance": "standard", "hydrafusion": "high", "hydrafusion-max": "max"}

// observeFusionLocked folds Copilot Fusion's route and phase events into the
// turn's activity and reports whether ev was one. Its phases' progress in
// bytes is left out: the phase's own events say what it does.
func (c *conversation) observeFusionLocked(ev copilot.SessionEvent) bool {
	switch d := ev.Data.(type) {
	case *rpc.SessionFusionRouteStartedData:
		c.fusion, c.standardPick, c.standardPickLive = nil, nil, true
	case *rpc.SessionFusionResolvedData:
		c.fusion, c.standardPick, c.standardPickLive = newFusion(ev, d), nil, true
	case *rpc.SessionAutoModeResolvedData:
		c.standardPick, c.standardPickLive = autoRoute(ev, d), true
		f := *c.standardPick
		c.fusion = &f
	case *rpc.AssistantFusionPhaseStartedData:
		if c.fusion != nil {
			p := fusionPhase(c.fusion, d.PhaseID, string(d.PhaseKind))
			p.Role, p.Model, p.Effort, p.Status, p.StartedAt = d.Role, d.Model, orEmpty(d.ReasoningEffort), "running", ev.Timestamp
		}
	case *rpc.AssistantFusionPhaseCompletedData:
		if c.fusion != nil {
			completePhase(fusionPhase(c.fusion, d.PhaseID, string(d.PhaseKind)), ev, d)
		}
	case *rpc.AssistantFusionPhaseFailedData:
		if c.fusion != nil {
			failPhase(fusionPhase(c.fusion, d.PhaseID, string(d.PhaseKind)), ev, d)
		}
	case *rpc.SessionFusionCompletedData, *rpc.SessionFusionRouteFailedData:
		c.fusion = nil
	case *rpc.AssistantFusionPhaseActivityData:
		return true
	default:
		return false
	}
	c.emitActivityLocked()
	return true
}

// ReadFusion reads the Fusion turns recorded in the persisted journal, oldest
// first. The journal keeps a route and how each phase ended, not when one
// started, so a phase's start is its end less its duration.
func (p *webProvider) ReadFusion(ctx context.Context, req agentapi.ReadRequest) ([]agentapi.Fusion, error) {
	if req.ConversationID == "" {
		return nil, errors.New("copilot: ReadRequest.ConversationID is required")
	}
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		turns := []agentapi.Fusion{}
		find := func(id string) *agentapi.Fusion {
			i := slices.IndexFunc(turns, func(f agentapi.Fusion) bool { return f.ID == id })
			if i < 0 {
				return nil
			}
			return &turns[i]
		}
		err := p.readForward(ctx, client, req.ConversationID, func(ev copilot.SessionEvent) {
			switch d := ev.Data.(type) {
			case *rpc.SessionFusionResolvedData:
				turns = append(turns, *newFusion(ev, d))
			case *rpc.AssistantFusionPhaseCompletedData:
				if f := find(d.FusionID); f != nil {
					completePhase(fusionPhase(f, d.PhaseID, string(d.PhaseKind)), ev, d)
				}
			case *rpc.AssistantFusionPhaseFailedData:
				if f := find(d.FusionID); f != nil {
					failPhase(fusionPhase(f, d.PhaseID, string(d.PhaseKind)), ev, d)
				}
			}
		})
		if err == nil {
			return turns, nil
		}
		if !errors.Is(err, errJournalChanged) || attempt == webWindowAttempts {
			return nil, err
		}
	}
}

// newFusion is a route as Fusion resolved it: its phases planned.
func newFusion(ev copilot.SessionEvent, d *rpc.SessionFusionResolvedData) *agentapi.Fusion {
	f := &agentapi.Fusion{ID: d.FusionID, StartedAt: ev.Timestamp, Level: syntheticLevels[d.SyntheticModel], Pattern: string(d.Pattern), Hint: orEmpty(d.Hint)}
	models := []string{d.PrimaryModel, orEmpty(d.SecondaryModel), orEmpty(d.JudgeModel), orEmpty(d.RepairModel)}
	for _, critic := range d.Critics {
		models = append(models, critic.Model)
	}
	for _, m := range models {
		if m != "" && !slices.Contains(f.Models, m) {
			f.Models = append(f.Models, m)
		}
	}
	for _, step := range d.PhasePlan {
		f.Phases = append(f.Phases, agentapi.FusionPhase{Kind: string(step.Kind), Role: step.Role, Status: "planned", Conditional: step.Conditional})
	}
	return f
}

// readStandardPickLocked reads back, for a resumed conversation on Fusion,
// the model Standard picked before uam opened it: Standard reports its pick
// once, so without the journal a restart would lose it. The same events end a
// pick as live: a Fusion route or a model change.
func (c *conversation) readStandardPickLocked() {
	p, client, id := c.p, c.client, c.id
	go func() {
		defer log.Recover("copilot standard pick read", &c.mu, &c.p.mu)
		ctx, cancel := context.WithTimeout(context.Background(), webStartTimeout)
		defer cancel()
		var pick *agentapi.Fusion
		err := p.readForward(ctx, client, id, func(ev copilot.SessionEvent) {
			switch d := ev.Data.(type) {
			case *rpc.SessionAutoModeResolvedData:
				pick = autoRoute(ev, d)
			case *rpc.SessionFusionResolvedData, *rpc.SessionModelChangeData:
				pick = nil
			}
		})
		if err != nil || pick == nil {
			return
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		if !c.closed && !c.standardPickLive {
			c.standardPick = pick
		}
	}()
}

// autoRoute is the one model Fusion Standard picked and why; Standard reports
// no route or phases.
func autoRoute(ev copilot.SessionEvent, d *rpc.SessionAutoModeResolvedData) *agentapi.Fusion {
	f := &agentapi.Fusion{ID: ev.ID, StartedAt: ev.Timestamp, Level: "standard", Pattern: "single", Models: []string{d.ChosenModel}, Hint: orEmpty(d.SelectionReason)}
	if d.AutoTier != nil {
		f.Level = cmp.Or(syntheticLevels[string(*d.AutoTier)], f.Level)
	}
	return f
}

// fusionPhase is f's phase with id, else the first planned one of its kind
// (which takes id), else a new one.
func fusionPhase(f *agentapi.Fusion, id, kind string) *agentapi.FusionPhase {
	if i := slices.IndexFunc(f.Phases, func(p agentapi.FusionPhase) bool { return p.ID == id }); i >= 0 {
		return &f.Phases[i]
	}
	if i := slices.IndexFunc(f.Phases, func(p agentapi.FusionPhase) bool { return p.ID == "" && p.Kind == kind }); i >= 0 {
		f.Phases[i].ID = id
		return &f.Phases[i]
	}
	f.Phases = append(f.Phases, agentapi.FusionPhase{ID: id, Kind: kind})
	return &f.Phases[len(f.Phases)-1]
}

func completePhase(p *agentapi.FusionPhase, ev copilot.SessionEvent, d *rpc.AssistantFusionPhaseCompletedData) {
	p.Role, p.Model, p.Status = cmp.Or(p.Role, d.Role), d.Model, string(d.Status)
	endPhase(p, ev.Timestamp, d.DurationMs, d.Usage, d.ReasoningEffort)
	if d.ConversationScope != rpc.FusionConversationScopeRoot {
		p.Verdict, p.Note, p.Defect = reviewVerdict(d.Content, orEmpty(d.Verdict))
	}
}

func failPhase(p *agentapi.FusionPhase, ev copilot.SessionEvent, d *rpc.AssistantFusionPhaseFailedData) {
	p.Role, p.Model, p.Status = cmp.Or(p.Role, d.Role), d.Model, "failed"
	endPhase(p, ev.Timestamp, d.DurationMs, d.Usage, d.ReasoningEffort)
	p.Note = clip(displaytext.Sanitize(cmp.Or(orEmpty(d.ErrorMessage), d.Reason)), maxFusionNote)
}

func endPhase(p *agentapi.FusionPhase, at time.Time, durationMs float64, usage rpc.FusionPhaseUsage, effort *string) {
	p.EndedAt = at
	if p.StartedAt.IsZero() && durationMs > 0 {
		p.StartedAt = at.Add(-time.Duration(durationMs * float64(time.Millisecond)))
	}
	p.Tokens, p.NanoAIU = usage.InputTokens+usage.OutputTokens, int64(usage.TotalNanoAiu)
	if effort != nil {
		p.Effort = *effort
	}
}

// reviewVerdict reads a reviewing phase's result: CLI 1.0.112 writes a critic's
// as JSON with its assessment, feedback and defect; otherwise the phase's
// verdict.
func reviewVerdict(content, verdict string) (string, string, string) {
	var review struct {
		Assessment string          `json:"assessment"`
		Feedback   string          `json:"feedback"`
		Defect     json.RawMessage `json:"defect"`
	}
	if json.Unmarshal([]byte(content), &review) == nil && review.Assessment != "" {
		verdict = review.Assessment
	}
	var defect string
	if len(review.Defect) > 0 && string(review.Defect) != "null" && json.Unmarshal(review.Defect, &defect) != nil {
		defect = string(review.Defect) // an object: kept as the JSON it came as
	}
	note := func(s string) string { return clip(strings.TrimSpace(displaytext.Sanitize(s)), maxFusionNote) }
	return verdict, note(review.Feedback), note(defect)
}
