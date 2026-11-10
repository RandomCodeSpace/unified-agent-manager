package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

// Models lists the models the signed-in account can select: entries with no
// policy or an enabled one. Others are listed by the CLI but not selectable.
func (p *webProvider) Models(ctx context.Context) ([]agentapi.Model, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	models, err := client.ListModels(ctx)
	if err != nil {
		p.poke()
		return nil, fmt.Errorf("list copilot models: %s", errText(err))
	}
	out := []agentapi.Model{}
	for _, m := range models {
		if m.ID == "" || (m.Policy != nil && m.Policy.State != rpc.ModelPolicyStateEnabled) {
			continue
		}
		mo := agentapi.Model{ID: m.ID, Name: m.Name, Efforts: append([]string{}, m.SupportedReasoningEfforts...), ContextSizes: []agentapi.ContextSize{}, Media: media(m.Capabilities), CostTier: costTier(m.ModelPickerPriceCategory)}
		if m.Billing != nil {
			if d := m.Billing.DiscountPercent; d != nil && *d > 0 && *d <= 100 {
				mo.DiscountPercent = int(*d)
			}
			mo.Prices = prices(m.Billing.TokenPrices)
		}
		if m.ID == "auto" {
			mo.Efforts = []string{}
		}
		if m.ID != "auto" && m.Billing != nil && m.Billing.TokenPrices != nil {
			prices := m.Billing.TokenPrices
			if prices.MaxPromptTokens != nil && *prices.MaxPromptTokens > 0 {
				mo.ContextSizes = append(mo.ContextSizes, agentapi.ContextSize{ID: "default", Tokens: *prices.MaxPromptTokens})
			}
			if prices.LongContext != nil && prices.LongContext.MaxPromptTokens != nil && *prices.LongContext.MaxPromptTokens > 0 && len(mo.ContextSizes) > 0 {
				mo.ContextSizes = append(mo.ContextSizes, agentapi.ContextSize{ID: "long_context", Tokens: *prices.LongContext.MaxPromptTokens})
			}
		}
		out = append(out, mo)
	}
	return append(out, customCatalog(p.customModels())...), nil
}

// prices copies the catalog's token prices, preferring cacheReadPrice and
// maxPromptTokens over their deprecated names cachePrice and contextMax.
func prices(tp *rpc.ModelBillingTokenPrices) *agentapi.Prices {
	if tp == nil {
		return nil
	}
	p := &agentapi.Prices{TierPrices: tierPrices(tp.InputPrice, tp.OutputPrice, cmp.Or(tp.CacheReadPrice, tp.CachePrice), tp.CacheWritePrice, cmp.Or(tp.MaxPromptTokens, tp.ContextMax))}
	if tp.BatchSize != nil {
		p.BatchSize = *tp.BatchSize
	}
	if lc := tp.LongContext; lc != nil {
		long := tierPrices(lc.InputPrice, lc.OutputPrice, cmp.Or(lc.CacheReadPrice, lc.CachePrice), lc.CacheWritePrice, cmp.Or(lc.MaxPromptTokens, lc.ContextMax))
		p.LongContext = &long
	}
	return p
}

func tierPrices(input, output, cacheRead, cacheWrite *float64, maxPrompt *int64) agentapi.TierPrices {
	value := func(v *float64) *float64 {
		if v == nil {
			return nil
		}
		c := *v
		return &c
	}
	t := agentapi.TierPrices{Input: value(input), Output: value(output), CacheRead: value(cacheRead), CacheWrite: value(cacheWrite)}
	if maxPrompt != nil {
		t.MaxPromptTokens = *maxPrompt
	}
	return t
}

// costTier maps the catalog's relative cost tier; an unknown one is dropped.
func costTier(c *rpc.ModelPickerPriceCategory) string {
	if c == nil {
		return ""
	}
	switch *c {
	case rpc.ModelPickerPriceCategoryLow:
		return agentapi.CostLow
	case rpc.ModelPickerPriceCategoryMedium:
		return agentapi.CostMedium
	case rpc.ModelPickerPriceCategoryHigh:
		return agentapi.CostHigh
	case rpc.ModelPickerPriceCategoryVeryHigh:
		return agentapi.CostVeryHigh
	}
	return ""
}

// media is a model's upload gate from the catalog: images need
// supports.vision, PDFs application/pdf among the vision media types. A
// model that reports neither (auto) gets no gate.
func media(c rpc.ModelCapabilities) *agentapi.Media {
	var vision *bool
	if c.Supports != nil {
		vision = c.Supports.Vision
	}
	var limits *rpc.ModelCapabilitiesLimitsVision
	if c.Limits != nil {
		limits = c.Limits.Vision
	}
	if vision == nil && limits == nil {
		return nil
	}
	md := &agentapi.Media{Images: vision != nil && *vision}
	if limits != nil {
		md.MaxImages = int(limits.MaxPromptImages)
		md.Types = slices.Clone(limits.SupportedMediaTypes)
		md.PDF = slices.Contains(limits.SupportedMediaTypes, "application/pdf")
	}
	return md
}

// SetModel changes all three settings between turns and checks the runtime's
// result before the manager records success.
func (c *conversation) SetModel(ctx context.Context, model, effort, contextSize string) error {
	if c.isClosed() {
		return agentapi.ErrClosed
	}
	// With a custom agent selected, a tool proof can select it again and
	// restore the selection; it must restore this one.
	if c.tools != nil && c.agentSelected() {
		c.tools.proof.Lock()
		defer c.tools.proof.Unlock()
	}
	return c.applyModel(ctx, model, effort, contextSize)
}

// applyModel switches to the complete selection and records it as the one
// agent changes keep.
func (c *conversation) applyModel(ctx context.Context, model, effort, contextSize string) error {
	if err := c.p.customKeyErr(model); err != nil {
		return err
	}
	if err := c.registerCustom(ctx, model); err != nil {
		return err
	}
	tier := rpc.ContextTier(contextSize)
	req := &rpc.ModelSwitchToRequest{ModelID: model, ContextTier: &tier, RunCompactionPreflight: copilot.Bool(true)}
	custom, isCustom := c.p.customModel(model)
	if isCustom {
		req.ModelCapabilities = &rpc.ModelCapabilitiesOverride{Supports: &rpc.ModelCapabilitiesOverrideSupports{Vision: copilot.Bool(custom.Vision)}}
	}
	if effort != "" {
		req.ReasoningEffort = &effort
	}
	res, err := c.sess.SwitchModel(ctx, req)
	if err != nil {
		c.p.poke()
		// Only a JSON-RPC rejection proves that nothing changed.
		if !isRPCError(err) {
			return c.selectionUncertain(ctx, fmt.Errorf("model switch outcome is unknown: %s", errText(err)))
		}
		return fmt.Errorf("copilot model switch: %s", errText(err))
	}
	if res == nil {
		return c.selectionUncertain(ctx, errors.New("model switch returned no result"))
	}
	status := ""
	if res.Status != nil {
		status = *res.Status
	}
	if status == "confirmation_required" || status == "cancelled" {
		return fmt.Errorf("copilot model switch %s; the selection was not applied", status)
	}
	if (res.Deferred != nil && *res.Deferred) || status != "applied" || res.PersistenceError != nil {
		return c.selectionUncertain(ctx, fmt.Errorf("model switch did not confirm a durable immediate selection (status %q)", status))
	}
	if res.ModelID != nil && *res.ModelID != model {
		return c.selectionUncertain(ctx, errors.New("model switch returned a different model"))
	}
	if state := res.ModelState; state != nil {
		if (state.ModelID != nil && *state.ModelID != model) || (state.ContextTier != nil && *state.ContextTier != tier) || (effort != "" && (state.ReasoningEffort == nil || *state.ReasoningEffort != effort)) {
			return c.selectionUncertain(ctx, errors.New("model switch returned different settings"))
		}
		if contextSize == "long_context" && state.ContextTier == nil {
			return c.selectionUncertain(ctx, errors.New("model switch did not confirm the context size"))
		}
	} else if contextSize == "long_context" || effort != "" {
		return c.selectionUncertain(ctx, errors.New("model switch did not report the selected settings"))
	}
	if effort == "" {
		// switchTo rejects an empty effort, while omitting it preserves the
		// current effort on a same-model switch. This RPC explicitly resets it.
		if err := c.sess.SetEffort(ctx, ""); err != nil {
			return c.selectionUncertain(ctx, fmt.Errorf("model changed but resetting effort failed: %s", errText(err)))
		}
	}
	// The old report describes the old selection; wait for a fresh one.
	c.mu.Lock()
	c.usage = agentapi.Context{}
	c.selected = model
	c.selection = &modelSelection{model: model, effort: effort, contextSize: contextSize}
	if isCustom {
		c.byom.models[model] = custom.Vision
	}
	c.mu.Unlock()
	return nil
}

// An uncertain or partial switch must not let the next prompt run with the
// old displayed settings. Reopening reapplies the Task's durable selection.
func (c *conversation) selectionUncertain(ctx context.Context, err error) error {
	reason := clip(displaytext.Sanitize(err.Error()), maxErrorText)
	c.mu.Lock()
	if !c.closed {
		c.emitLocked(agentapi.Event{Kind: agentapi.EventExit, Error: reason})
	}
	c.mu.Unlock()
	_ = c.Close(ctx)
	return fmt.Errorf("copilot model settings: %s", reason)
}
