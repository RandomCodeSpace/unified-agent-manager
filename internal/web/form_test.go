package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// Every bound and rule is checked before the form reaches the owner, with a
// message the model can act on.
func TestFormRefusals(t *testing.T) {
	act := `"actions":[{"name":"ok","label":"OK"}]`
	field := func(f string) string { return `{"title":"t",` + act + `,"fields":[` + f + `]}` }
	many := func(n int, item string) string {
		parts := make([]string, n)
		for i := range parts {
			parts[i] = fmt.Sprintf(item, i)
		}
		return strings.Join(parts, ",")
	}
	for _, c := range []struct{ name, args, want string }{
		{"no title", `{"title":" ",` + act + `}`, "title is required"},
		{"long title", `{"title":"` + strings.Repeat("x", maxFormTitle+1) + `",` + act + `}`, "title is longer"},
		{"control in title", `{"title":"a\u0007",` + act + `}`, "control character"},
		{"long body", `{"title":"t","body":"` + strings.Repeat("x", maxFormBody+1) + `",` + act + `}`, "body is longer"},
		{"no actions", `{"title":"t","actions":[]}`, "1 to 4 actions"},
		{"too many actions", `{"title":"t","actions":[` + many(maxFormActions+1, `{"name":"a%d","label":"A"}`) + `]}`, "1 to 4 actions"},
		{"bad action name", `{"title":"t","actions":[{"name":"1x","label":"A"}]}`, "starting with a letter"},
		{"duplicate action", `{"title":"t","actions":[{"name":"a","label":"A"},{"name":"a","label":"B"}]}`, "used twice"},
		{"unknown argument", `{"title":"t",` + act + `,"style":"x"}`, "unknown field"},
		{"too many fields", `{"title":"t",` + act + `,"fields":[` + many(maxFormFields+1, `{"kind":"TextField","name":"f%d","label":"L"}`) + `]}`, "at most 8"},
		{"unknown kind", field(`{"kind":"Image","name":"f","label":"L"}`), "kind \"Image\""},
		{"unknown field key", field(`{"kind":"TextField","name":"f","label":"L","url":"x"}`), "unknown field"},
		{"duplicate name", field(`{"kind":"TextField","name":"f","label":"L"},{"kind":"CheckBox","name":"f","label":"M"}`), "used twice"},
		{"bad field name", field(`{"kind":"TextField","name":"a b","label":"L"}`), "letters, digits"},
		{"no label", field(`{"kind":"TextField","name":"f","label":""}`), "label is required"},
		{"bad variant", field(`{"kind":"TextField","name":"f","label":"L","variant":"multipleSelection"}`), "TextField's variant"},
		{"text with options", field(`{"kind":"TextField","name":"f","label":"L","options":["a","b"]}`), "no options"},
		{"text default too long", field(`{"kind":"TextField","name":"f","label":"L","default":"` + strings.Repeat("x", maxFormShortText+1) + `"}`), "longer than 500"},
		{"one option", field(`{"kind":"ChoicePicker","name":"f","label":"L","options":["a"]}`), "2 to 12 options"},
		{"duplicate option", field(`{"kind":"ChoicePicker","name":"f","label":"L","options":["a","a"]}`), "listed twice"},
		{"default outside options", field(`{"kind":"ChoicePicker","name":"f","label":"L","options":["a","b"],"default":"c"}`), "not one of the options"},
		{"list default for one", field(`{"kind":"ChoicePicker","name":"f","label":"L","options":["a","b"],"default":["a"]}`), "default is one option"},
		{"multi default outside", field(`{"kind":"ChoicePicker","name":"f","label":"L","variant":"multipleSelection","options":["a","b"],"default":["a","z"]}`), "not one of the options"},
		{"required checkbox", field(`{"kind":"CheckBox","name":"f","label":"L","required":true}`), "cannot be required"},
		{"checkbox text default", field(`{"kind":"CheckBox","name":"f","label":"L","default":"yes"}`), "true or false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := formFromArgs(json.RawMessage(c.args))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want %q", err, c.want)
			}
		})
	}
}

// A form shows all its fields as one question, waits for the owner, and
// returns the chosen action and each field's typed value at once; a
// dismissal and a Stop each return their own result.
func TestFormAsksOnceAndReturnsEveryValue(t *testing.T) {
	m, prov, _ := newTestManager(t)
	task, conv := createSession(t, m, prov)
	show := func(callID, args string) (<-chan agentapi.HostToolResult, agentapi.Interaction) {
		t.Helper()
		done := make(chan agentapi.HostToolResult, 1)
		go func() {
			res, err := conv.CallTool(context.Background(), agentapi.HostToolCall{Name: formToolName, CallID: callID, Arguments: json.RawMessage(args)})
			if err != nil {
				res = agentapi.HostToolResult{Text: err.Error(), Failed: true}
			}
			done <- res
		}()
		var ix agentapi.Interaction
		waitUntil(t, "the form", func() bool {
			d := detail(t, m, task.ID)
			for _, i := range d.Interactions {
				if i.ToolCallID == callID && i.State == agentapi.InteractionPending {
					ix = i
					return d.State == StateAwaitingAnswer
				}
			}
			return false
		})
		return done, ix
	}

	args := `{"title":"Release","body":"Pick the target.","actions":[{"name":"deploy","label":"Deploy"},{"name":"later","label":"Later"}],"fields":[
		{"kind":"TextField","name":"notes","label":"Notes","variant":"longText"},
		{"kind":"ChoicePicker","name":"env","label":"Environment","options":["staging","prod"],"required":true,"default":"staging"},
		{"kind":"ChoicePicker","name":"checks","label":"Checks","variant":"multipleSelection","options":["lint","test","e2e"]},
		{"kind":"CheckBox","name":"tag","label":"Tag the release","default":true}]}`
	done, ix := show("form-1", args)
	if ix.Title != "Release" || ix.Detail != "Pick the target." || len(ix.Questions) != 4 || len(ix.Options) != 2 ||
		ix.Elicitation == nil || ix.Elicitation.Source != formSource || !ix.Questions[0].Field.Multiline || ix.Questions[3].Field.Default[0] != "Yes" {
		t.Fatalf("form = %+v", ix)
	}
	for _, bad := range []agentapi.Answer{
		{Answers: [][]string{{}, {"prod"}, {}, {"Yes"}}},                                   // no action
		{Decision: "ship", Answers: [][]string{{}, {"prod"}, {}, {"Yes"}}},                 // not an action
		{Decision: "deploy", Answers: [][]string{{}, {}, {}, {"Yes"}}},                     // required env empty
		{Decision: "deploy", Answers: [][]string{{}, {"dev"}, {}, {"Yes"}}},                // not an option
		{Decision: "deploy", Answers: [][]string{{}, {"prod"}, {"lint", "lint"}, {"Yes"}}}, // repeated choice
		{Decision: "deploy", Reject: true},
	} {
		if _, err := m.Answer(task.ID, ix.ID, bad); statusOf(err) != 400 {
			t.Fatalf("answer %+v = %v, want 400", bad, err)
		}
	}
	got, err := m.Answer(task.ID, ix.ID, agentapi.Answer{Decision: "deploy", Answers: [][]string{{"line one\nline two"}, {"prod"}, {"lint", "e2e"}, {"No"}}})
	if err != nil || got.State != agentapi.InteractionAnswered || got.Resolution != "Deploy" {
		t.Fatalf("answered = %+v, %v", got, err)
	}
	res := <-done
	var out struct {
		Status string         `json:"status"`
		Action string         `json:"action"`
		Values map[string]any `json:"values"`
	}
	if res.Failed || json.Unmarshal([]byte(res.Text), &out) != nil || out.Status != "submitted" || out.Action != "deploy" ||
		out.Values["notes"] != "line one\nline two" || out.Values["env"] != "prod" || out.Values["tag"] != false ||
		fmt.Sprint(out.Values["checks"]) != "[lint e2e]" {
		t.Fatalf("result = %+v (%s)", res, res.Text)
	}

	// A button card: no fields, an empty answer list and the action.
	done, ix = show("form-2", `{"title":"Next step?","actions":[{"name":"merge","label":"Merge"},{"name":"wait","label":"Wait"}]}`)
	if _, err := m.Answer(task.ID, ix.ID, agentapi.Answer{Decision: "wait"}); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Failed || res.Text != `{"action":"wait","status":"submitted","values":{}}` {
		t.Fatalf("button card = %+v", res)
	}

	done, ix = show("form-3", args)
	if _, err := m.Answer(task.ID, ix.ID, agentapi.Answer{Cancel: true}); err != nil {
		t.Fatal(err)
	}
	if res := <-done; res.Failed || res.Text != `{"status":"dismissed"}` {
		t.Fatalf("dismissed = %+v", res)
	}

	done, _ = show("form-4", args)
	if _, err := m.Cancel(task.ID); err != nil {
		t.Fatal(err)
	}
	if res := <-done; !res.Failed || !strings.Contains(res.Text, "withdrawn") {
		t.Fatalf("stopped = %+v", res)
	}
}
