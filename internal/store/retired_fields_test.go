package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// Older versions saved generated subagent summaries; the next save drops
// them while a newer version's field survives.
func TestRetiredSubagentSummariesDropOnSave(t *testing.T) {
	var web WebState
	old := `{"subagent_summaries":[{"agent_id":"child","item_id":"result","digest":"` + strings.Repeat("a", 64) + `","text":"Verified."}],"future_field":{"keep":true}}`
	if err := json.Unmarshal([]byte(old), &web); err != nil {
		t.Fatal(err)
	}
	web.Update(WebState{Turn: "completed"})
	encoded, err := json.Marshal(web)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "subagent_summaries") || !strings.Contains(string(encoded), `"future_field":{"keep":true}`) {
		t.Fatalf("saved = %s", encoded)
	}
}

// A Task record of an earlier version with the removed planner's retired
// field loads as an ordinary Task, and the next save drops the field.
func TestPlannerRetiredFieldDropsOnSave(t *testing.T) {
	var web WebState
	old := `{"turn":"completed","stage":"settled","retired":"its lane was removed"}`
	if err := json.Unmarshal([]byte(old), &web); err != nil {
		t.Fatal(err)
	}
	if web.Stage != "settled" {
		t.Fatalf("stage = %q", web.Stage)
	}
	encoded, err := json.Marshal(web)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "retired") || !strings.Contains(string(encoded), `"stage":"settled"`) {
		t.Fatalf("saved = %s", encoded)
	}
}
