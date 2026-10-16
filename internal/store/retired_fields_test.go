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

// A Task record with the removed done-check settle's reviewed item loads,
// and the next save drops the field.
func TestReviewedItemDropsOnSave(t *testing.T) {
	var web WebState
	old := `{"turn":"completed","stage":"settled","settled_by":"reviewed","reviewed_item":"a1"}`
	if err := json.Unmarshal([]byte(old), &web); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(web)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "reviewed_item") || !strings.Contains(string(encoded), `"settled_by":"reviewed"`) {
		t.Fatalf("saved = %s", encoded)
	}
}

// A Task record with the removed done check's verdict loads, and the next
// save drops the field.
func TestDoneVerdictDropsOnSave(t *testing.T) {
	var web WebState
	old := `{"turn":"completed","stage":"active","done":{"item_id":"a1","verdict":"done","by":"model","line":"All tests pass.","at":"2026-10-10T10:00:00Z"}}`
	if err := json.Unmarshal([]byte(old), &web); err != nil {
		t.Fatal(err)
	}
	if web.Turn != "completed" || len(web.unknown) != 0 {
		t.Fatalf("loaded = %+v", web)
	}
	encoded, err := json.Marshal(web)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), `"done"`) || !strings.Contains(string(encoded), `"stage":"active"`) {
		t.Fatalf("saved = %s", encoded)
	}
}

// A saved budget loads as the field, not as a newer version's: a later
// save writes the current budget, also when it was removed or reached.
func TestBudgetSavesItsCurrentValue(t *testing.T) {
	var web WebState
	if err := json.Unmarshal([]byte(`{"turn":"working","budget":{"credits":2,"from":5}}`), &web); err != nil {
		t.Fatal(err)
	}
	if web.Budget == nil || web.Budget.Credits != 2 || web.Budget.From != 5 {
		t.Fatalf("loaded budget = %+v", web.Budget)
	}
	reached := *web.Budget
	reached.Reached = true
	web.Update(WebState{Turn: "cancelled", Budget: &reached})
	if encoded, _ := json.Marshal(web); !strings.Contains(string(encoded), `"reached":true`) {
		t.Fatalf("reached budget saved as %s", encoded)
	}
	web.Update(WebState{Turn: "cancelled"})
	if encoded, _ := json.Marshal(web); strings.Contains(string(encoded), "budget") {
		t.Fatalf("removed budget saved as %s", encoded)
	}
}
