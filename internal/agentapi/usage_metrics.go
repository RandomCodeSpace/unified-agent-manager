package agentapi

import (
	"context"
	"time"
)

// UsageMetricsReader reads accumulated native metrics from an already-open
// conversation. It does not resume, run a model, or update the host usage ledger.
type UsageMetricsReader interface {
	UsageMetrics(context.Context) (*TaskUsageMetrics, error)
}

// TaskUsageMetrics is a transient native snapshot. Models and Agents are
// overlapping projections, never additive totals. Optional counters remain unknown.
type TaskUsageMetrics struct {
	StartedAt          time.Time              `json:"started_at"`
	CurrentModel       string                 `json:"current_model,omitempty"`
	UserRequests       int64                  `json:"user_requests"`
	PremiumRequestCost float64                `json:"premium_request_cost"`
	APIDurationMS      int64                  `json:"api_duration_ms"`
	AIUnits            *float64               `json:"ai_units,omitempty"`
	LastInput          int64                  `json:"last_input"`
	LastOutput         int64                  `json:"last_output"`
	CodeChanges        UsageMetricCodeChanges `json:"code_changes"`
	TokenDetails       []UsageTokenDetail     `json:"token_details"`
	Models             []UsageMetricModel     `json:"models"`
	Agents             []UsageMetricAgent     `json:"agents"`
	Truncated          bool                   `json:"truncated,omitempty"`
}

type UsageMetricCodeChanges struct {
	Files   int64 `json:"files"`
	Added   int64 `json:"added"`
	Removed int64 `json:"removed"`
}

// Token type rows are provider labels; the host never sums or reprices them.
type UsageTokenDetail struct {
	Type   string `json:"type"`
	Tokens int64  `json:"tokens"`
}

type UsageMetricModel struct {
	Model              string             `json:"model"`
	Requests           int64              `json:"requests"`
	PremiumRequestCost float64            `json:"premium_request_cost"`
	AIUnits            *float64           `json:"ai_units,omitempty"`
	Input              int64              `json:"input"`
	Output             int64              `json:"output"`
	CacheRead          int64              `json:"cache_read"`
	CacheWrite         int64              `json:"cache_write"`
	Reasoning          *int64             `json:"reasoning,omitempty"`
	CacheExpiresAt     *time.Time         `json:"cache_expires_at,omitempty"`
	TokenDetails       []UsageTokenDetail `json:"token_details"`
}

type UsageMetricAgent struct {
	ID            string             `json:"id"`
	Name          string             `json:"name,omitempty"`
	DisplayName   string             `json:"display_name,omitempty"`
	APIDurationMS int64              `json:"api_duration_ms"`
	AIUnits       float64            `json:"ai_units"`
	Models        []UsageMetricModel `json:"models"`
}
