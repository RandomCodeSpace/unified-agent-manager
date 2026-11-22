package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

func refMsg(kind agentapi.ItemKind, text string) agentapi.Item {
	return agentapi.Item{Kind: kind, Text: text}
}

func refTool(command, output string) agentapi.Item {
	in, _ := json.Marshal(map[string]string{"command": command})
	return agentapi.Item{Kind: agentapi.ItemTool, Tool: &agentapi.ToolCall{Name: "bash", Status: agentapi.ToolCompleted, Input: string(in), Output: output}}
}

// refRow is a reference in one line: label|status|title|state|claimed@item,
// or label|ambiguous|noun:title,noun:title.
func refRow(r Reference) string {
	if r.Status == referenceAmbiguous {
		var c []string
		for _, x := range r.Candidates {
			c = append(c, x.Noun+":"+x.Title+"@"+x.ItemID)
		}
		return fmt.Sprintf("%s|%s|%s|%s", r.Label, r.Status, strings.Join(c, ","), r.Claimed)
	}
	src := ""
	if r.Source != nil {
		src = "@" + r.Source.ItemID
	}
	return fmt.Sprintf("%s|%s|%s|%s|%s%s", r.Label, r.Status, r.Title, r.State, r.Claimed, src)
}

func TestResolveReferences(t *testing.T) {
	user, reply := agentapi.ItemUser, agentapi.ItemAssistant
	cases := []struct {
		name  string
		items []agentapi.Item
		want  []string
	}{
		{
			name: "list under a heading, cited as a list with claims",
			items: []agentapi.Item{
				refMsg(reply, "## Issues found\n\n1. **Login times out** after the SSO redirect\n2. Crash on save\n\n3. Slow search"),
				refMsg(reply, "Issues 3, 1 and 2 are still open; the rest is fine."),
			},
			want: []string{"issue 3|resolved|Slow search||open@i0", "issue 1|resolved|Login times out after the SSO redirect||open@i0", "issue 2|resolved|Crash on save||open@i0"},
		},
		{
			name: "a later list in the sentence takes the noun when it has its own state",
			items: []agentapi.Item{
				refMsg(reply, "## Issues\n\n| # | Title | Status |\n|---|---|---|\n| 4 | Login | Open |\n| 5 | Crash | Open |\n| 6 | Copy | Closed |"),
				refMsg(reply, "issue 4,8 is still open and 5,6 as closed; 9 tests failed. Issue 4 is open and 3 tests failed."),
			},
			want: []string{"issue 4|resolved|Login|open|open@i0", "issue 8|unresolved|||open", "issue 5|resolved|Crash|open|closed@i0", "issue 6|resolved|Copy|closed|closed@i0"},
		},
		{
			name: "restarted numbering: the most recent list wins",
			items: []agentapi.Item{
				refMsg(reply, "Open findings:\n1. Old one\n2. Old two"),
				refMsg(user, "thanks"),
				refMsg(reply, "## Findings\n1) New one"),
				refMsg(reply, "Finding #1 is done. Finding 2 is not fixed."),
			},
			want: []string{"finding 1|resolved|New one||done@i2", "finding 2|resolved|Old two||@i0"},
		},
		{
			name: "a bare number of two nouns is ambiguous",
			items: []agentapi.Item{
				refMsg(reply, "## Steps\n1. Build the image"),
				refMsg(user, "## Bugs\n1. Image is too large"),
				refMsg(reply, "That is #1, closed."),
			},
			want: []string{"#1|ambiguous|bug:Image is too large@i1,step:Build the image@i0|closed"},
		},
		{
			name: "a noun-led number without a definition is unresolved; a bare one is dropped",
			items: []agentapi.Item{
				refMsg(user, "## Issues\n1. Something"),
				refMsg(reply, "Issue 9 is closed and #7 is open."),
			},
			want: []string{"issue 9|unresolved|||closed"},
		},
		{
			name: "numbers in code spans and fences are not citations",
			items: []agentapi.Item{
				refMsg(user, "## Issues\n1. A\n2. B"),
				refMsg(reply, "Run `grep issue 1` first.\n\n```\nissue 2\n```\nDone."),
			},
			want: []string{},
		},
		{
			name: "the reply's own definitions are not citations but define later lines",
			items: []agentapi.Item{
				refMsg(reply, "## Issues\n1. Foo is slow\n2. Bar crashes (see issue 1)\n\nIssue 2 is fixed."),
			},
			want: []string{"issue 2|resolved|Bar crashes (see issue 1)||closed@i0"},
		},
		{
			name: "a definition later in the reply does not count",
			items: []agentapi.Item{
				refMsg(reply, "Issue 1 first.\n\n## Issues\n1. Foo"),
			},
			want: []string{"issue 1|unresolved|||"},
		},
		{
			name: "gh issue list rows, with a claim that disagrees",
			items: []agentapi.Item{
				refTool("gh issue list --limit 5", "4\tOPEN\tLogin times out\tbug\t2026-11-01T00:00:00Z\n5\tCLOSED\tCrash on save\t\t2026-11-02T00:00:00Z\n"),
				refMsg(reply, "Issue 4 is open and issue 5 is still open."),
			},
			want: []string{"issue 4|resolved|Login times out|open|open@i0", "issue 5|resolved|Crash on save|closed|open@i0"},
		},
		{
			name: "gh pr list puts the state after the branch",
			items: []agentapi.Item{
				refTool("gh pr list", "12\tAdd retries\tfeat/retries\tOPEN\t2026-11-01T00:00:00Z\n"),
				refMsg(reply, "PR #12 merged."),
			},
			want: []string{"PR 12|resolved|Add retries|open|merged@i0"},
		},
		{
			name: "gh issue view headers",
			items: []agentapi.Item{
				refTool("gh issue view 4", "title:\tLogin times out\nstate:\tCLOSED\nauthor:\tsomeone\nnumber:\t4\n--\nissue 9 body"),
				refMsg(reply, "#4 is closed."),
			},
			want: []string{"#4|resolved|Login times out|closed|closed@i0"},
		},
		{
			name: "glab issue list --closed and glab mr list",
			items: []agentapi.Item{
				refTool("glab issue list --closed", "Showing 1 closed issue in group/proj\n\n#4\tgroup/proj#4\tLogin times out\t(bug)\tabout 1 day ago\n"),
				refTool("glab mr list", "!12\tgroup/proj!12\tAdd retries\t(main) ← (feat)\n"),
				refMsg(reply, "#4 is fixed and MR !12 is blocked; !12 again."),
			},
			want: []string{"#4|resolved|Login times out|closed|closed@i0", "MR 12|resolved|Add retries|open|blocked@i1", "!12|resolved|Add retries|open|@i1"},
		},
		{
			name: "glab list --all records no state; glab issue view takes the number from the command",
			items: []agentapi.Item{
				refTool("glab issue list --all", "#3\tgroup/proj#3\tOld thing\n"),
				refTool("glab issue view 7 --comments", "title:\tFlaky test\nstate:\topen\n"),
				refMsg(reply, "Issues 3 and 7."),
			},
			want: []string{"issue 3|resolved|Old thing||@i0", "issue 7|resolved|Flaky test|open|@i1"},
		},
		{
			name: "a truncated tool output drops its partial line",
			items: []agentapi.Item{
				refTool("gh issue list", "4\tOPEN\tLogin\n5\tOPEN\tCra"+truncatedMarker),
				refMsg(reply, "Issues 4 and 5."),
			},
			want: []string{"issue 4|resolved|Login|open|@i0", "issue 5|unresolved|||"},
		},
		{
			name: "two commands in one call are skipped",
			items: []agentapi.Item{
				refTool("gh issue list; gh pr list", "4\tOPEN\tLogin\n"),
				refMsg(reply, "Issue 4."),
			},
			want: []string{"issue 4|unresolved|||"},
		},
		{
			name: "a table under a lead-in with a status column",
			items: []agentapi.Item{
				refMsg(reply, "Open findings:\n\n| # | Title | Status |\n|---|---|---|\n| 3 | SQL injection | ✅ Done |\n| 4 | XSS | open |"),
				refMsg(reply, "Finding #3 is done but finding 4 is blocked."),
			},
			want: []string{"finding 3|resolved|SQL injection|done|done@i0", "finding 4|resolved|XSS|open|blocked@i0"},
		},
		{
			name: "a table whose header names the noun",
			items: []agentapi.Item{
				refMsg(user, "| Issue | Summary | Owner |\n|:--|:--|--|\n| #4 | Login loop | me |"),
				refMsg(reply, "Issue 4."),
			},
			want: []string{"issue 4|resolved|Login loop||@i0"},
		},
		{
			name: "task-list items record done or todo",
			items: []agentapi.Item{
				refMsg(reply, "## Tasks\n- [x] 1. Write tests\n- [ ] 2. Ship it"),
				refMsg(reply, "Task 1 done, task 2 blocked."),
			},
			want: []string{"task 1|resolved|Write tests|done|done@i0", "task 2|resolved|Ship it|todo|blocked@i0"},
		},
		{
			name: "explicit lines, with the noun from the line or the lead-in",
			items: []agentapi.Item{
				refMsg(user, "Issue #4 — Login loop\n**Step 2:** Run the tests\n\n## Bugs\n**#5** Crash on save"),
				refMsg(reply, "Issue 4, step 2 and bug 5."),
			},
			want: []string{"issue 4|resolved|Login loop||@i0", "step 2|resolved|Run the tests||@i0", "bug 5|resolved|Crash on save||@i0"},
		},
		{
			name: "a list without a noun, a nested list and a reasoning item define nothing",
			items: []agentapi.Item{
				refMsg(reply, "Here is what I did:\n1. Built it\n\n## Issues\n1. Real issue\n   2. A sub-step"),
				refMsg(agentapi.ItemKind("thought"), "## Steps\n1. Hidden"),
				refMsg(reply, "Issue 2, step 1 and issue 1."),
			},
			want: []string{"issue 2|unresolved|||", "step 1|unresolved|||", "issue 1|resolved|Real issue||@i0"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for i := range tc.items {
				tc.items[i].ID = fmt.Sprintf("i%d", i)
			}
			got := []string{}
			for _, r := range resolveReferences(tc.items) {
				got = append(got, refRow(r))
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestReferencesRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	sum, conv := createSession(t, ts.m, ts.prov)
	finishTurn(conv, "review", "## Issues\n1. Login times out\n2. Crash on save")
	finishTurn(conv, "status", "Issue 1 is still open.")
	auth := withCookie(ts)
	path := "/api/sessions/" + sum.ID + "/references"
	for range 2 {
		w := ts.do(http.MethodGet, path+"?reply=a-status", "", auth)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body)
		}
		var body struct {
			References []Reference `json:"references"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.References) != 1 || refRow(body.References[0]) != "issue 1|resolved|Login times out||open@a-review" || body.References[0].Source.Time.IsZero() {
			t.Fatalf("references = %+v", body.References)
		}
	}
	if w := ts.do(http.MethodGet, path+"?reply=a-review", "", auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"references":[]`) {
		t.Fatalf("no citations: %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, path, "", auth); w.Code != http.StatusBadRequest {
		t.Fatalf("missing reply: %d", w.Code)
	}
	if w := ts.do(http.MethodGet, path+"?reply=u-status", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("not a reply: %d", w.Code)
	}
	if w := ts.do(http.MethodGet, "/api/sessions/nope/references?reply=a-status", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown thread: %d", w.Code)
	}
}
