package copilot

import (
	"context"
	"errors"
	"maps"
	"math"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
)

const (
	maxUsageModels     = 32
	maxUsageAgents     = 64
	maxUsageTokenTypes = 16
	maxUsageRows       = 128 // model rows shared by the whole snapshot and all agents
	maxUsageLabel      = 256
	maxUsageCount      = 1<<53 - 1 // counters cross JSON into JavaScript without rounding
)

type usageMetricsSession interface {
	UsageMetrics(context.Context) (*rpc.UsageGetMetricsResult, error)
}

func (a sdkSessionAdapter) UsageMetrics(ctx context.Context) (*rpc.UsageGetMetricsResult, error) {
	return a.s.RPC.Usage.GetMetrics(ctx)
}

func (c *conversation) UsageMetrics(ctx context.Context) (*agentapi.TaskUsageMetrics, error) {
	if c.isClosed() {
		return nil, agentapi.ErrClosed
	}
	reader, ok := c.sess.(usageMetricsSession)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	res, err := reader.UsageMetrics(ctx)
	if err != nil {
		var rpcErr *copilot.RPCError
		if errors.As(err, &rpcErr) && rpcErr.Code == -32601 {
			return nil, agentapi.ErrUnsupported
		}
		return nil, err
	}
	// The required map and session boundary distinguish an initialized zero-usage
	// session from a missing/null/partial result decoded into Go zero values.
	if res == nil || res.ModelMetrics == nil || res.SessionStartTime.IsZero() {
		return nil, errors.New("copilot returned no initialized usage metrics")
	}
	if !usageCounts(res.TotalUserRequests, res.TotalAPIDurationMs, res.LastCallInputTokens, res.LastCallOutputTokens, res.CodeChanges.FilesModifiedCount, res.CodeChanges.LinesAdded, res.CodeChanges.LinesRemoved) || !usageCost(res.TotalPremiumRequestCost) || res.TotalNanoAiu != nil && !usageCost(*res.TotalNanoAiu) {
		return nil, errors.New("copilot returned invalid usage metrics")
	}
	out := &agentapi.TaskUsageMetrics{StartedAt: res.SessionStartTime, CurrentModel: usageLabel(deref(res.CurrentModel)), UserRequests: res.TotalUserRequests, PremiumRequestCost: res.TotalPremiumRequestCost, APIDurationMS: res.TotalAPIDurationMs, AIUnits: usageAIUnits(res.TotalNanoAiu), LastInput: res.LastCallInputTokens, LastOutput: res.LastCallOutputTokens, CodeChanges: agentapi.UsageMetricCodeChanges{Files: res.CodeChanges.FilesModifiedCount, Added: res.CodeChanges.LinesAdded, Removed: res.CodeChanges.LinesRemoved}, Models: []agentapi.UsageMetricModel{}, Agents: []agentapi.UsageMetricAgent{}, TokenDetails: []agentapi.UsageTokenDetail{}}
	details := map[string]int64{}
	for key, value := range res.TokenDetails {
		details[key] = value.TokenCount
	}
	out.TokenDetails, err = usageDetails(details, &out.Truncated)
	if err != nil {
		return nil, err
	}
	budget := maxUsageRows
	out.Models, err = usageModels(res.ModelMetrics, &budget, &out.Truncated)
	if err != nil {
		return nil, err
	}
	for _, id := range slices.Sorted(maps.Keys(res.AgentMetrics)) {
		if len(out.Agents) == maxUsageAgents {
			out.Truncated = true
			break
		}
		metric := res.AgentMetrics[id]
		if !usageIdentity(id) {
			out.Truncated = true
			continue
		}
		if !usageCounts(metric.TotalAPIDurationMs) || !usageCost(metric.TotalNanoAiu) {
			return nil, errors.New("copilot returned invalid agent usage metrics")
		}
		models, err := usageModels(metric.ModelMetrics, &budget, &out.Truncated)
		if err != nil {
			return nil, err
		}
		out.Agents = append(out.Agents, agentapi.UsageMetricAgent{ID: strings.Clone(id), Name: usageLabel(deref(metric.AgentName)), DisplayName: usageLabel(deref(metric.AgentDisplayName)), APIDurationMS: metric.TotalAPIDurationMs, AIUnits: metric.TotalNanoAiu / 1e9, Models: models})
	}
	return out, nil
}

func usageModels(metrics map[string]rpc.UsageMetricsModelMetric, budget *int, truncated *bool) ([]agentapi.UsageMetricModel, error) {
	out := []agentapi.UsageMetricModel{}
	for _, model := range slices.Sorted(maps.Keys(metrics)) {
		if len(out) == maxUsageModels || *budget == 0 {
			*truncated = true
			break
		}
		if !usageIdentity(model) {
			*truncated = true
			continue
		}
		m := metrics[model]
		u := m.Usage
		if !usageCounts(m.Requests.Count, u.InputTokens, u.OutputTokens, u.CacheReadTokens, u.CacheWriteTokens) || u.ReasoningTokens != nil && !usageCounts(*u.ReasoningTokens) || !usageCost(m.Requests.Cost) || m.TotalNanoAiu != nil && !usageCost(*m.TotalNanoAiu) {
			return nil, errors.New("copilot returned invalid model usage metrics")
		}
		detail := map[string]int64{}
		for key, value := range m.TokenDetails {
			detail[key] = value.TokenCount
		}
		tokens, err := usageDetails(detail, truncated)
		if err != nil {
			return nil, err
		}
		row := agentapi.UsageMetricModel{Model: strings.Clone(model), Requests: m.Requests.Count, PremiumRequestCost: m.Requests.Cost, AIUnits: usageAIUnits(m.TotalNanoAiu), Input: u.InputTokens, Output: u.OutputTokens, CacheRead: u.CacheReadTokens, CacheWrite: u.CacheWriteTokens, TokenDetails: tokens}
		if u.ReasoningTokens != nil {
			value := *u.ReasoningTokens
			row.Reasoning = &value
		}
		if m.CacheExpiresAt != nil {
			value := *m.CacheExpiresAt
			row.CacheExpiresAt = &value
		}
		out = append(out, row)
		*budget--
	}
	return out, nil
}

func usageDetails(metrics map[string]int64, truncated *bool) ([]agentapi.UsageTokenDetail, error) {
	out := []agentapi.UsageTokenDetail{}
	for _, key := range slices.Sorted(maps.Keys(metrics)) {
		if len(out) == maxUsageTokenTypes {
			*truncated = true
			break
		}
		if !usageIdentity(key) {
			*truncated = true
			continue
		}
		if !usageCounts(metrics[key]) {
			return nil, errors.New("copilot returned invalid token type usage")
		}
		out = append(out, agentapi.UsageTokenDetail{Type: strings.Clone(key), Tokens: metrics[key]})
	}
	return out, nil
}

func usageCounts(values ...int64) bool {
	for _, n := range values {
		if n < 0 || n > maxUsageCount {
			return false
		}
	}
	return true
}
func usageCost(value float64) bool { return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0) }
func usageAIUnits(nano *float64) *float64 {
	if nano == nil {
		return nil
	}
	value := *nano / 1e9
	return &value
}
func usageLabel(value string) string {
	return strings.Clone(clip(strings.TrimSpace(displaytext.Sanitize(value)), maxUsageLabel))
}
func usageIdentity(value string) bool {
	return value != "" && len(value) <= maxUsageLabel && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}
