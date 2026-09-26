package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSubagentSummaryWebStateRoundTripPreservesUnknownFields(t *testing.T) {
	var web WebState
	if err := json.Unmarshal([]byte(`{"future_field":{"keep":true}}`), &web); err != nil {
		t.Fatal(err)
	}
	record := SubagentSummary{AgentID: "child", ItemID: "result", ItemAgentID: "child", Digest: strings.Repeat("a", 64), LastAssistantID: "result", Text: "Verified the result."}
	web.Update(WebState{SubagentSummaries: []SubagentSummary{record}})
	encoded, err := json.Marshal(web)
	if err != nil {
		t.Fatal(err)
	}
	var restored WebState
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if len(restored.SubagentSummaries) != 1 || restored.SubagentSummaries[0] != record {
		t.Fatalf("round trip = %+v", restored.SubagentSummaries)
	}
	if !strings.Contains(string(encoded), `"future_field":{"keep":true}`) {
		t.Fatalf("unknown field lost: %s", encoded)
	}
	restored.Update(WebState{})
	encoded, err = json.Marshal(restored)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "subagent_summaries") || !strings.Contains(string(encoded), "future_field") {
		t.Fatalf("clear = %s", encoded)
	}
}
