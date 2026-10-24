package web

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// TestUtilitySchemaLiveProbe sends Decide's prompt with a response schema to
// the installed Copilot CLI on gpt-6-luna through the provider's RunUtility,
// in isolated throwaway sessions under a private COPILOT_HOME. Per schema
// shape it passes with no errors and every reply accepted by parseDecision.
func TestUtilitySchemaLiveProbe(t *testing.T) {
	if os.Getenv("UAM_SCHEMA_LIVE_PROBE") != "1" {
		t.Skip("set UAM_SCHEMA_LIVE_PROBE=1 for the paid gpt-6-luna schema probe")
	}
	t.Setenv("COPILOT_HOME", t.TempDir())
	p := copilot.NewWebProvider()
	defer func() { _ = p.Shutdown(context.Background()) }()
	runner := p.(agentapi.UtilityRunner)
	options := []DecisionOption{
		{ID: "a", Description: "The CI job failed because the code did not compile"},
		{ID: "b", Description: "The CI job failed because a test timed out"},
		{ID: "c", Description: "The CI job failed because the runner ran out of disk space"},
	}
	ids := []any{"a", "b", "c"}
	shapes := map[string]map[string]any{
		"anyOf": {"type": "object", "properties": map[string]any{"choice": map[string]any{"anyOf": []any{map[string]any{"enum": ids}, map[string]any{"type": "null"}}}},
			"required": []any{"choice"}, "additionalProperties": false},
		"type-list": {"type": "object", "properties": map[string]any{"choice": map[string]any{"type": []any{"string", "null"}, "enum": append(ids[:len(ids):len(ids)], nil)}},
			"required": []any{"choice"}, "additionalProperties": false},
	}
	evidence := []struct{ input, want string }{
		{"--- FAIL: TestUpload (600.00s)\npanic: test timed out after 10m0s", "b"},
		{"go test ./... \n*** Test killed: ran too long (10m0s).\nFAIL\tpkg/api\t600.012s", "b"},
		{"vitest: Test timed out in 5000ms. If this is a long-running test, pass a timeout value.", "b"},
		{"pytest: Failed: Timeout >300.0s in test_sync_large_repo", "b"},
		{"jest: thrown: \"Exceeded timeout of 5000 ms for a test.\" at src/queue.test.ts:41", "b"},
		{"The cafeteria menu for Friday is tomato soup and bread.", ""},
		{"Release notes: the logo colour changed to a darker blue.", ""},
	}
	for _, shape := range []string{"anyOf", "type-list"} {
		t.Run(shape, func(t *testing.T) {
			for i, ev := range evidence {
				prompt, _ := json.Marshal(struct {
					Input   string           `json:"input"`
					Options []DecisionOption `json:"options"`
				}{ev.input, options})
				start := time.Now()
				reply, err := runner.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "gpt-6-luna", Workdir: t.TempDir(), Purpose: "decision-probe",
					System: decisionSystem + "Pick the cause of the CI failure the evidence shows.", Prompt: string(prompt), Timeout: decisionTimeout, ResponseSchema: shapes[shape]})
				got, perr := parseDecision(reply, options)
				t.Logf("shape=%s case=%d want=%q err=%v reply=%q parsed=%v choice=%q abstained=%v latency_ms=%d",
					shape, i, ev.want, err, reply, perr == nil, got.Choice, got.Abstained, time.Since(start).Milliseconds())
				if err != nil || perr != nil {
					t.Errorf("case %d: provider error %v, parse error %v", i, err, perr)
				}
			}
		})
	}
}
