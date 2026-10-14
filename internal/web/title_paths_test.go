package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// Every way a Thread gets its first message leaves it with a title at once,
// and title_pending is set only while a model's title is really on its way,
// so the list never sits on "Waiting for a title…".
func TestEveryFirstMessageLeavesATitle(t *testing.T) {
	type check struct {
		title   string
		pending bool
	}
	for name, tc := range map[string]struct {
		// hook answers title requests; nil gives "Generated title".
		hook func(context.Context, agentapi.TitleRequest) (string, error)
		// run sends the first message and returns the Thread and what it
		// shows right after; then is what it shows once the job ended.
		run  func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check)
		then check
	}{
		"text": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				return created(t, m, project, "Please add a dark\nmode toggle"), check{"Please add a dark mode toggle", true}
			},
			then: check{"Generated title", false},
		},
		"a long first message is cut at a word": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				return created(t, m, project, "Why do teams write changelogs when the commit history already says what changed?"), check{"Why do teams write changelogs when the commit history…", true}
			},
			then: check{"Generated title", false},
		},
		"slash command": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				prov.SetCommands([]agentapi.Command{{Name: "review", Kind: agentapi.CommandPrompt}}, nil)
				sum := created(t, m, project, "")
				if sub, err := m.Command(sum, CommandRequest{Name: "review", Arguments: "the diff", RequestID: mustUUID(t)}); err != nil || sub.Status != SubmissionAccepted {
					t.Fatalf("command = %+v, %v", sub, err)
				}
				return sum, check{"/review the diff", false}
			},
			then: check{"/review the diff", false},
		},
		"uploads only, titled from the reply": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				id := created(t, m, project, "")
				img := mustUpload(t, m, id, "login-error.png", pngBytes(t))
				submitUploads(t, m, id, img.ID)
				conv := prov.Last()
				// A turn that ends without a reply keeps the wait, behind the file name.
				conv.EmitTurn(agentapi.TurnCompleted, "")
				if got := summaryOf(t, m, id); got.Title != "login-error.png" || !got.TitlePending {
					t.Fatalf("after a turn without a reply: %q pending %v", got.Title, got.TitlePending)
				}
				conv.EmitItem(agentapi.Item{ID: "r1", Kind: agentapi.ItemAssistant, Text: "The login page returns 500."})
				conv.EmitTurn(agentapi.TurnCompleted, "")
				return id, check{"login-error.png", true}
			},
			then: check{"Generated title", false},
		},
		"a file only": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				id := created(t, m, project, "")
				workdir := summaryOf(t, m, id).Workdir
				if err := os.WriteFile(filepath.Join(workdir, "notes.txt"), []byte("notes\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if sub, err := m.Submit(id, PromptRequest{RequestID: mustUUID(t), Files: []string{"notes.txt"}}); err != nil || sub.Status != SubmissionAccepted {
					t.Fatalf("file-only first message = %+v, %v", sub, err)
				}
				return id, check{"notes.txt", true}
			},
			// No reply comes here: the wait stays, behind the file name.
			then: check{"notes.txt", true},
		},
		"every model fails": {
			hook: func(context.Context, agentapi.TitleRequest) (string, error) {
				return "", errors.New("model unavailable")
			},
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				return created(t, m, project, "Fix the build"), check{"Fix the build", true}
			},
			then: check{"Fix the build", false},
		},
		"every model times out": {
			hook: func(context.Context, agentapi.TitleRequest) (string, error) { return "", context.DeadlineExceeded },
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				return created(t, m, project, "Fix the build"), check{"Fix the build", true}
			},
			then: check{"Fix the build", false},
		},
		"the model answers instead of naming": {
			hook: func(context.Context, agentapi.TitleRequest) (string, error) {
				return "Changelogs matter because they help users understand what changed", nil
			},
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				return created(t, m, project, "why do teams write changelogs?"), check{"why do teams write changelogs?", true}
			},
			then: check{"why do teams write changelogs?", false},
		},
		"Background AI off": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				setUtilityLimit(t, m, new(0))
				id := created(t, m, project, "Fix the build")
				// The call is skipped without asking the model.
				waitUntil(t, "the skipped call", func() bool { return !summaryOf(t, m, id).TitlePending })
				return id, check{"Fix the build", false}
			},
			then: check{"Fix the build", false},
		},
		"title model None": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": store.WebTitleModelNone}}); err != nil {
					t.Fatal(err)
				}
				return created(t, m, project, "Fix the build"), check{"Fix the build", false}
			},
			then: check{"Fix the build", false},
		},
		"a refused first message leaves no title": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				id := created(t, m, project, "")
				prov.Last().SetSendHook(func(context.Context, string) error { return errors.New("refused") })
				mustSubmit(t, m, id, "first try", mustUUID(t), ModeSend, SubmissionRejected)
				return id, check{"", false}
			},
			then: check{"", false},
		},
		"Run again": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": store.WebTitleModelNone}}); err != nil {
					t.Fatal(err)
				}
				source := created(t, m, project, "")
				finishTurn(prov.Last(), "Tidy the README", "Done.")
				again, err := m.Rerun(source, RerunRequest{RequestID: mustUUID(t)})
				if err != nil {
					t.Fatal(err)
				}
				return again.ID, check{"Tidy the README", false}
			},
			then: check{"Tidy the README", false},
		},
		"a turn the Thread did not send": {
			run: func(t *testing.T, m *Manager, prov *agenttest.Provider, project string) (string, check) {
				id := created(t, m, project, "")
				finishTurn(prov.Last(), "Tidy the README", "Done.")
				return id, check{"Tidy the README", false}
			},
			then: check{"Tidy the README", false},
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _, project := titleManager(t)
			release := make(chan struct{})
			prov.SetTitleHook(func(ctx context.Context, req agentapi.TitleRequest) (string, error) {
				<-release
				if tc.hook != nil {
					return tc.hook(ctx, req)
				}
				return "Generated title", nil
			})
			id, now := tc.run(t, m, prov, project)
			if got := summaryOf(t, m, id); got.Title != now.title || got.TitlePending != now.pending {
				t.Fatalf("at once: title %q pending %v, want %q %v", got.Title, got.TitlePending, now.title, now.pending)
			}
			close(release)
			waitUntil(t, "the title job", func() bool {
				got := summaryOf(t, m, id)
				return got.Title == tc.then.title && got.TitlePending == tc.then.pending
			})
			if err := m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := summaryOf(t, m, id); got.Title != tc.then.title || got.TitlePending != tc.then.pending {
				t.Fatalf("after the job: title %q pending %v, want %+v", got.Title, got.TitlePending, tc.then)
			}
		})
	}
}

// created makes a Thread in project, sending prompt as its first message
// when there is one.
func created(t *testing.T, m *Manager, project, prompt string) string {
	t.Helper()
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	return sum.ID
}

// A model's reply is a title only when it names the request: replies that
// answer it, or that are too long to keep whole, are refused rather than cut.
func TestGeneratedTitleRefusesAnswersAndCuts(t *testing.T) {
	for reply, want := range map[string]string{
		"Add dark mode toggle":                  "Add dark mode toggle",
		`"Why teams write changelogs."`:         "Why teams write changelogs",
		"Title: Fix the build\nSecond line":     "Fix the build",
		"Is it a question?":                     "Is it a question?",
		"Fix v1.2 parser":                       "Fix v1.2 parser",
		"Add dark mode toggle to settings page": "Add dark mode toggle to settings page",
		"":                                      "",
		// Observed answers: a sentence of eight words, and one cut mid-phrase.
		"Explaining updates helps teams communicate changes to users":       "",
		"Changelogs matter because they help users understand what changed": "",
		"Sure, here is a title":                      "",
		"I will fix the build":                       "",
		"It's a way to record changes":               "",
		"Changelogs record changes. Users read them": "",
		strings.Repeat("a", 61):                      "",
	} {
		got, err := generatedTitle(reply)
		if got != want || (err == nil) != (want != "") {
			t.Errorf("generatedTitle(%q) = %q, %v; want %q", reply, got, err, want)
		}
	}
}

func TestFallbackTitle(t *testing.T) {
	for text, want := range map[string]string{
		"  Fix the\nbuild\x1b[31m  ":                "Fix the build",
		strings.Repeat("word ", 20):                 "word word word word word word word word word word word word…",
		strings.Repeat("x", 70):                     strings.Repeat("x", 59) + "…",
		"Is this a bug? " + strings.Repeat("y", 50): "Is this a bug?…",
	} {
		got := fallbackTitle(text)
		if got != want || len([]rune(got)) > maxTitleRunes {
			t.Errorf("fallbackTitle(%q) = %q, want %q", text, got, want)
		}
	}
}

// A forked Thread starts with its source's name or title, marked as the
// branch, and a Thread whose turn ended untitled takes its first message.
func TestForkTakesTheSourceTitle(t *testing.T) {
	m, p, _, source := taskForkManager(t)
	if got := summaryOf(t, m, source.ID); got.Title != "original" {
		t.Fatalf("source title = %q, want its first message", got.Title)
	}
	if _, err := m.Rename(source.ID, "Parser fix"); err != nil {
		t.Fatal(err)
	}
	p.forkHook = func(agentapi.ForkRequest) (string, error) { return "native-copy", nil }
	branch, err := m.Fork(source.ID, ForkRequest{UserItemID: "u-original", Model: "a", RequestID: mustUUID(t)})
	if err != nil {
		t.Fatal(err)
	}
	if branch.Title != "Parser fix (branch)" || branch.Name != "" {
		t.Fatalf("branch name %q title %q", branch.Name, branch.Title)
	}
}

// A new branch waits for the model's title, not the fallback, before it
// takes the Thread's title as its name.
func TestWorktreeBranchWaitsForTheModelTitle(t *testing.T) {
	m, prov, _, _ := titleManager(t)
	release := make(chan struct{})
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) {
		<-release
		return "Fix the parser", nil
	})
	m.worktreeRoot = t.TempDir()
	project := addProject(t, m, worktreeRepo(t))
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "please look at the parser crash", Place: &PlaceRequest{Mode: placeWorktree}})
	if err != nil {
		t.Fatal(err)
	}
	if got := summaryOf(t, m, sum.ID); got.Title != "please look at the parser crash" || !got.TitlePending {
		t.Fatalf("before the model's title: %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	if got := summaryOf(t, m, sum.ID); got.Worktree.Branch != sum.Worktree.Branch {
		t.Fatalf("the branch took the fallback: %q", got.Worktree.Branch)
	}
	close(release)
	waitUntil(t, "the branch name", func() bool { return summaryOf(t, m, sum.ID).Worktree.Branch == "uam/fix-the-parser" })
}
