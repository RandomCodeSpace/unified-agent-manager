package copilot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func modelTokens(ev copilot.SessionEvent, d *rpc.AssistantUsageData) *agentapi.TokenUsage {
	if d.InputTokens == nil && d.OutputTokens == nil && d.CacheReadTokens == nil && d.CacheWriteTokens == nil {
		return nil
	}
	return &agentapi.TokenUsage{
		Model: d.Model, Time: ev.Timestamp,
		Input: max(orZero(d.InputTokens), 0), Output: max(orZero(d.OutputTokens), 0),
		CacheRead: max(orZero(d.CacheReadTokens), 0), CacheWrite: max(orZero(d.CacheWriteTokens), 0),
	}
}

// SetUsageSessionRecorder installs the host's durable ownership recorder.
func (p *webProvider) SetUsageSessionRecorder(record func(string, bool) error) {
	p.mu.Lock()
	p.usageSessionRecorder = record
	p.mu.Unlock()
}

// usageMu serializes callbacks with process-stop cleanup, including utility
// sessions and conversations already removed from the live conversation map.
func (p *webProvider) recordUsageSession(client sdkClient, id string, active bool) error {
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	p.mu.Lock()
	record := p.usageSessionRecorder
	current := p.client == client
	p.mu.Unlock()
	if record == nil {
		return nil
	}
	if active && !current {
		return agentapi.ErrClosed
	}
	if !active && p.usageOwned[id] != client {
		return nil
	}
	if err := record(id, active); err != nil {
		return err
	}
	if active {
		if p.usageOwned == nil {
			p.usageOwned = map[string]sdkClient{}
		}
		p.usageOwned[id] = client
	} else {
		delete(p.usageOwned, id)
	}
	return nil
}

// Called only after Stop or ForceStop has terminated this client's runtime.
func (p *webProvider) releaseClientUsage(client sdkClient) error {
	p.usageMu.Lock()
	defer p.usageMu.Unlock()
	p.mu.Lock()
	record := p.usageSessionRecorder
	p.mu.Unlock()
	if record == nil {
		return nil
	}
	var errs []error
	for id, owner := range p.usageOwned {
		if owner != client {
			continue
		}
		if err := record(id, false); err != nil {
			errs = append(errs, fmt.Errorf("release copilot usage ownership: %w", err))
			continue
		}
		delete(p.usageOwned, id)
	}
	return errors.Join(errs...)
}

// Any explicit telemetry environment belongs to the owner, including empty
// values and disable flags. A nonnil SDK Telemetry would override enablement.
func usageTelemetry(env []string, homeDir func() (string, error)) (*copilot.TelemetryConfig, error) {
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "COPILOT_OTEL_") {
			return nil, nil
		}
	}
	home, err := homeDir()
	if err != nil {
		return nil, fmt.Errorf("locate copilot usage directory: %w", err)
	}
	dir := filepath.Join(home, ".copilot", "otel")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create copilot usage directory: %w", err)
	}
	file, err := os.CreateTemp(dir, "uam-*.jsonl")
	if err != nil {
		return nil, fmt.Errorf("create copilot usage export: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return nil, fmt.Errorf("close copilot usage export: %w", err)
	}
	return &copilot.TelemetryConfig{FilePath: file.Name(), ExporterType: "file", SourceName: "uam", CaptureContent: copilot.Bool(false)}, nil
}
