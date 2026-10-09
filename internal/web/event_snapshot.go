package web

import (
	"maps"
	"slices"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func snapshotValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func snapshotSettings(settings Settings) Settings {
	settings.TokenPrices = maps.Clone(settings.TokenPrices)
	for provider, prices := range settings.TokenPrices {
		prices = maps.Clone(prices)
		for model, price := range prices {
			price.Input = snapshotValue(price.Input)
			price.Output = snapshotValue(price.Output)
			price.CacheRead = snapshotValue(price.CacheRead)
			price.CacheWrite = snapshotValue(price.CacheWrite)
			prices[model] = price
		}
		settings.TokenPrices[provider] = prices
	}
	settings.HiddenModels = maps.Clone(settings.HiddenModels)
	for provider, models := range settings.HiddenModels {
		settings.HiddenModels[provider] = slices.Clone(models)
	}
	settings.SubagentModels = maps.Clone(settings.SubagentModels)
	for provider, models := range settings.SubagentModels {
		settings.SubagentModels[provider] = slices.Clone(models)
	}
	settings.TitleModel = maps.Clone(settings.TitleModel)
	settings.CustomModels = slices.Clone(settings.CustomModels)
	settings.UtilityDailyLimit = snapshotValue(settings.UtilityDailyLimit)
	settings.SuggestReplies = snapshotValue(settings.SuggestReplies)
	settings.CompactionThreshold = snapshotValue(settings.CompactionThreshold)
	return settings
}

func snapshotSummary(summary SessionSummary) SessionSummary {
	summary.Context = snapshotValue(summary.Context)
	summary.Usage = snapshotValue(summary.Usage)
	summary.Ask = snapshotValue(summary.Ask)
	summary.Diff = snapshotValue(summary.Diff)
	summary.Execution = snapshotValue(summary.Execution)
	if summary.Execution != nil {
		summary.Execution.Objective = snapshotValue(summary.Execution.Objective)
		if objective := summary.Execution.Objective; objective != nil {
			objective.CreditsUsed = snapshotValue(objective.CreditsUsed)
			objective.CreditLimit = snapshotValue(objective.CreditLimit)
		}
	}
	return summary
}

func snapshotPlan(plan *agentapi.PlanReview) *agentapi.PlanReview {
	plan = snapshotValue(plan)
	if plan != nil {
		plan.Actions = slices.Clone(plan.Actions)
	}
	return plan
}

func snapshotBody(item agentapi.Item) agentapi.Item {
	item = cloneBody(item)
	item.Plan = snapshotPlan(item.Plan)
	if tool := item.Tool; tool != nil {
		tool.ExitCode = snapshotValue(tool.ExitCode)
		tool.Declaration = snapshotValue(tool.Declaration)
		tool.Tail = slices.Clone(tool.Tail)
	}
	return item
}

func snapshotSubagent(agent agentapi.Subagent) agentapi.Subagent {
	agent.Runs = slices.Clone(agent.Runs)
	agent.Retry = snapshotValue(agent.Retry)
	return agent
}

// detailLocked already owns the outer lists; own their nested values too.
func snapshotDetail(detail SessionDetail) SessionDetail {
	detail.SessionSummary = snapshotSummary(detail.SessionSummary)
	for i := range detail.Items {
		detail.Items[i] = snapshotBody(detail.Items[i])
	}
	for i := range detail.Interactions {
		interaction := &detail.Interactions[i]
		interaction.Plan = snapshotPlan(interaction.Plan)
		interaction.Options = slices.Clone(interaction.Options)
		interaction.Questions = slices.Clone(interaction.Questions)
		for j := range interaction.Questions {
			interaction.Questions[j].Choices = slices.Clone(interaction.Questions[j].Choices)
		}
	}
	for i := range detail.Subagents {
		detail.Subagents[i] = snapshotSubagent(detail.Subagents[i])
	}
	for i := range detail.Queue {
		detail.Queue[i].Files = slices.Clone(detail.Queue[i].Files)
		detail.Queue[i].Attachments = slices.Clone(detail.Queue[i].Attachments)
	}
	detail.LastSubmission = snapshotValue(detail.LastSubmission)
	if last := detail.LastSubmission; last != nil {
		last.CommandResult = snapshotValue(last.CommandResult)
		if last.CommandResult != nil {
			last.CommandResult.Options = slices.Clone(last.CommandResult.Options)
		}
	}
	detail.HistoryBefore = snapshotValue(detail.HistoryBefore)
	detail.Schedules = snapshotValue(detail.Schedules)
	if schedules := detail.Schedules; schedules != nil {
		schedules.Entries = slices.Clone(schedules.Entries)
	}
	detail.TurnActivity = snapshotValue(detail.TurnActivity)
	if activity := detail.TurnActivity; activity != nil {
		activity.Retry = snapshotValue(activity.Retry)
		activity.Todos.Todos = slices.Clone(activity.Todos.Todos)
	}
	return detail
}

func snapshotCompactDetail(detail compactSessionDetail) compactSessionDetail {
	// These embedded lists are shadowed in compact JSON; retain only its rows.
	detail.SessionDetail.Items, detail.SessionDetail.Subagents = nil, nil
	detail.SessionDetail = snapshotDetail(detail.SessionDetail)
	detail.Items = slices.Clone(detail.Items)
	for i := range detail.Items {
		item := &detail.Items[i]
		item.Item = snapshotBody(item.Item)
		item.Compact = snapshotValue(item.Compact)
		item.Tool = snapshotValue(item.Tool)
		if tool := item.Tool; tool != nil {
			tool.ExitCode = snapshotValue(tool.ExitCode)
			tool.Declaration = snapshotValue(tool.Declaration)
			tool.Tail = slices.Clone(tool.Tail)
			tool.FilePaths = slices.Clone(tool.FilePaths)
			tool.FileEdits = slices.Clone(tool.FileEdits)
		}
	}
	detail.Subagents = slices.Clone(detail.Subagents)
	for i := range detail.Subagents {
		detail.Subagents[i].Subagent = snapshotSubagent(detail.Subagents[i].Subagent)
	}
	return detail
}
