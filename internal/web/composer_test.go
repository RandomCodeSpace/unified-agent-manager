package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
)

// composerRepo is a Git working tree with tracked, untracked, ignored and
// linked paths.
func composerRepo(t *testing.T) string {
	t.Helper()
	git, err := execpath.Resolve("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	write := func(name, text string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git1 := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git1("init", "-q")
	write("README.md", "# demo\n")
	write("src/main.go", "package main\n")
	write("src/util/helper.go", "package util\n")
	write(".gitignore", "ignored.txt\nbuild/\n")
	write("ignored.txt", "secret\n")
	write("build/out.txt", "built\n")
	write("notes.txt", "untracked\n")
	write("bin.dat", "a\x00b")
	for name, target := range map[string]string{"link.md": "README.md", "outside": "/etc", "linkdir": "src"} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	git1("add", "README.md", "src", ".gitignore", "link.md", "outside", "linkdir")
	return dir
}

func composerTask(t *testing.T, dir string) (*Manager, *agenttest.Provider, SessionSummary, *agenttest.Conversation) {
	t.Helper()
	m, prov, _ := newTestManager(t)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, dir)})
	if err != nil {
		t.Fatal(err)
	}
	return m, prov, sum, prov.Last()
}

func filePaths(list FileList) []string {
	var out []string
	for _, f := range list.Files {
		out = append(out, f.Path+":"+f.Type)
	}
	return out
}

func TestFilesListGitPathsWithParentsAndNoLinks(t *testing.T) {
	m, _, sum, _ := composerTask(t, composerRepo(t))
	list, err := m.Files(context.Background(), sum.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	got := filePaths(list)
	for _, want := range []string{"README.md:file", "src:directory", "src/util:directory", "src/util/helper.go:file", "notes.txt:file", ".gitignore:file", "bin.dat:file"} {
		if !slices.Contains(got, want) {
			t.Fatalf("listing lacks %s: %v", want, got)
		}
	}
	for _, p := range got {
		name := strings.Split(p, ":")[0]
		if name == "ignored.txt" || strings.HasPrefix(name, "build") || name == "link.md" || name == "outside" || name == "linkdir" || strings.HasPrefix(name, ".git/") {
			t.Fatalf("listing includes %s: %v", p, got)
		}
	}
	if list.Reason != "" {
		t.Fatalf("reason = %q", list.Reason)
	}

	list, _ = m.Files(context.Background(), sum.ID, "HELP", 50)
	if got := filePaths(list); len(got) != 1 || got[0] != "src/util/helper.go:file" {
		t.Fatalf("q=HELP = %v", got)
	}
	list, _ = m.Files(context.Background(), sum.ID, "util", 50)
	if got := filePaths(list); len(got) != 2 || got[0] != "src/util:directory" {
		t.Fatalf("a base-name prefix ranks first: %v", got)
	}
	if list, _ = m.Files(context.Background(), sum.ID, "", 2); len(list.Files) != 2 {
		t.Fatalf("limit 2 = %v", filePaths(list))
	}

	// A project below the top level lists only its own paths, relative to it.
	sub, _, subSum, _ := composerTask(t, filepath.Join(composerRepo(t), "src"))
	list, _ = sub.Files(context.Background(), subSum.ID, "", 50)
	if got := filePaths(list); !slices.Equal(got, []string{"util:directory", "main.go:file", "util/helper.go:file"}) {
		t.Fatalf("subdirectory listing = %v", got)
	}
}

func TestFilesOutsideGitGiveAReason(t *testing.T) {
	if _, err := execpath.Resolve("git"); err != nil {
		t.Skip("git is not installed")
	}
	m, _, sum, _ := composerTask(t, t.TempDir())
	list, err := m.Files(context.Background(), sum.ID, "", 50)
	if err != nil || len(list.Files) != 0 || !strings.Contains(list.Reason, "not in a Git working tree") {
		t.Fatalf("non-git listing = %+v, %v", list, err)
	}
}

func TestPromptFilesAreCheckedInsideTheProject(t *testing.T) {
	dir := composerRepo(t)
	if err := syscall.Mkfifo(filepath.Join(dir, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	m, _, sum, conv := composerTask(t, dir)
	send := func(files ...string) error {
		_, err := m.Submit(sum.ID, PromptRequest{Text: "look", RequestID: mustUUID(t), Files: files})
		return err
	}
	tooMany := make([]string, maxFileRefs+1)
	for i := range tooMany {
		tooMany[i] = "README.md"
	}
	for _, tc := range []struct {
		files []string
		want  string
	}{
		{[]string{"/etc/passwd"}, "is absolute"},
		{[]string{"../x"}, "leaves the project"},
		{[]string{"src/../README.md"}, "leaves the project"},
		{[]string{"./README.md"}, "not a clean relative path"},
		{[]string{"src/"}, "not a clean relative path"},
		{[]string{"link.md"}, "symbolic link"},
		{[]string{"linkdir/main.go"}, "symbolic link"},
		{[]string{"outside/passwd"}, "symbolic link"},
		{[]string{"missing.txt"}, "does not exist"},
		{[]string{"README.md/x"}, "does not exist"},
		{[]string{"bin.dat"}, "binary"},
		{[]string{"pipe"}, "not a regular file"},
		{[]string{"README.md", "missing.txt"}, `"missing.txt"`},
		{tooMany, "at most 20"},
	} {
		err := send(tc.files...)
		if statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("files %v = %v, want 400 %q", tc.files, err, tc.want)
		}
	}
	if len(conv.Sends()) != 0 || detail(t, m, sum.ID).LastSubmission != nil {
		t.Fatal("a refused reference must send and record nothing")
	}

	if err := send("src/main.go", "src/util", "README.md", "src/main.go"); err != nil {
		t.Fatal(err)
	}
	prompts := conv.Prompts()
	want := []agentapi.File{
		{Path: filepath.Join(dir, "src/main.go"), Rel: "src/main.go"},
		{Path: filepath.Join(dir, "src/util"), Rel: "src/util", Dir: true},
		{Path: filepath.Join(dir, "README.md"), Rel: "README.md"},
	}
	if len(prompts) != 1 || prompts[0].Text != "look" || !slices.Equal(prompts[0].Files, want) {
		t.Fatalf("sent %+v, want files %+v", prompts, want)
	}
}

func TestQueuedPromptKeepsItsFilesAndChecksThemAgain(t *testing.T) {
	dir := composerRepo(t)
	m, _, sum, conv := composerTask(t, dir)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "first", RequestID: mustUUID(t)}); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if sub, err := m.Submit(sum.ID, PromptRequest{Text: "steer", RequestID: mustUUID(t), Mode: ModeSteer, Files: []string{"README.md"}}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("steer with files = %+v, %v", sub, err)
	}
	if p := conv.SteerPrompts(); len(p) != 1 || len(p[0].Files) != 1 || p[0].Files[0].Path != filepath.Join(dir, "README.md") {
		t.Fatalf("steered %+v", p)
	}
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "steer", RequestID: mustUUID(t), Mode: ModeSteer, Files: []string{"../outside"}}); statusOf(err) != http.StatusBadRequest || len(conv.SteerPrompts()) != 1 {
		t.Fatalf("steer with a bad file = %v", err)
	}
	queued, err := m.Submit(sum.ID, PromptRequest{Text: "then @README.md", RequestID: mustUUID(t), Mode: ModeQueue, Files: []string{"README.md"}})
	if err != nil || queued.Status != SubmissionQueued {
		t.Fatalf("queue = %+v, %v", queued, err)
	}
	if q := detail(t, m, sum.ID).Queue; len(q) != 1 || !slices.Equal(q[0].Files, []string{"README.md"}) {
		t.Fatalf("queue = %+v", q)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "queued prompt sent", func() bool { return len(conv.Prompts()) == 2 })
	if p := conv.Prompts()[1]; p.Text != "then @README.md" || len(p.Files) != 1 || p.Files[0].Rel != "README.md" {
		t.Fatalf("drained prompt = %+v", p)
	}

	// A file that went away while the prompt waited is a rejected send.
	conv.EmitTurn(agentapi.TurnWorking, "")
	rid := mustUUID(t)
	if _, err := m.Submit(sum.ID, PromptRequest{Text: "gone", RequestID: rid, Mode: ModeQueue, Files: []string{"notes.txt"}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")
	waitUntil(t, "rejected drain", func() bool {
		last := detail(t, m, sum.ID).LastSubmission
		return last != nil && last.RequestID == rid
	})
	if last := detail(t, m, sum.ID).LastSubmission; last.Status != SubmissionRejected || !strings.Contains(last.Error, "does not exist") || len(conv.Prompts()) != 2 {
		t.Fatalf("drain of a removed file = %+v, sends %d", last, len(conv.Prompts()))
	}
}

func TestCommandRunsOnlyListedCommandsLikeASend(t *testing.T) {
	dir := composerRepo(t)
	m, prov, sum, conv := composerTask(t, dir)
	prov.SetCommands([]agentapi.Command{
		{Name: "review", Description: "Review \x1b[31mchanges", Kind: "builtin", InputHint: "what"},
		{Name: "probe-skill", Kind: agentapi.CommandSkill},
		{Name: "has space"},
		{Name: "review"},
	}, nil)
	commands, err := m.Commands(context.Background(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []agentapi.Command{
		{Name: "review", Description: "Review changes", Kind: agentapi.CommandPrompt, InputHint: "what"},
		{Name: "probe-skill", Kind: agentapi.CommandSkill},
	}
	if !reflect.DeepEqual(commands, want) {
		t.Fatalf("commands = %+v, want %+v", commands, want)
	}

	run := func(name, args string, files ...string) (Submission, error) {
		return m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: name, Arguments: args, Files: files})
	}
	for _, tc := range []struct {
		name   string
		status int
	}{{"", http.StatusBadRequest}, {"/review", http.StatusBadRequest}, {"has space", http.StatusBadRequest}, {"help", http.StatusNotFound}} {
		if _, err := run(tc.name, ""); statusOf(err) != tc.status {
			t.Fatalf("command %q = %v, want %d", tc.name, err, tc.status)
		}
	}
	if _, err := m.Command(sum.ID, CommandRequest{RequestID: "nope", Name: "review"}); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("bad request_id = %v", err)
	}
	if _, err := run("review", "x", "../etc"); statusOf(err) != http.StatusBadRequest {
		t.Fatalf("bad file = %v", err)
	}
	if len(conv.CommandRuns()) != 0 || detail(t, m, sum.ID).LastSubmission != nil {
		t.Fatal("a refused command must send and record nothing")
	}

	rid := mustUUID(t)
	sub, err := m.Command(sum.ID, CommandRequest{RequestID: rid, Name: "probe-skill", Arguments: "alpha beta", Files: []string{"README.md"}})
	if err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("command = %+v, %v", sub, err)
	}
	runs := conv.CommandRuns()
	if len(runs) != 1 || runs[0].Name != "probe-skill" || runs[0].Args.Text != "alpha beta" || len(runs[0].Args.Files) != 1 || runs[0].Args.Files[0].Rel != "README.md" {
		t.Fatalf("runs = %+v", runs)
	}
	if d := detail(t, m, sum.ID); d.State != StateWorking || d.LastSubmission.RequestID != rid {
		t.Fatalf("after command: state %s, last %+v", d.State, d.LastSubmission)
	}
	if again, err := m.Command(sum.ID, CommandRequest{RequestID: rid, Name: "probe-skill", Arguments: "alpha beta"}); err != nil || again != sub || len(conv.CommandRuns()) != 1 {
		t.Fatalf("repeat = %+v, %v, runs %d", again, err, len(conv.CommandRuns()))
	}
	conv.EmitTurn(agentapi.TurnWorking, "")
	if _, err := run("review", ""); statusOf(err) != http.StatusConflict {
		t.Fatalf("command during a turn = %v, want 409", err)
	}
	conv.EmitTurn(agentapi.TurnCompleted, "")

	conv.SetSendHook(func(context.Context, string) error { return errors.New("copilot answered /review with text") })
	sub, err = run("review", "")
	if err != nil || sub.Status != SubmissionRejected || !strings.Contains(sub.Error, "with text") {
		t.Fatalf("refused result = %+v, %v", sub, err)
	}
	if d := detail(t, m, sum.ID); d.State == StateWorking {
		t.Fatal("a rejected command must not leave the task working")
	}
}

// A new Task's @ picker lists the Project's directory before any
// conversation exists.
func TestProjectFilesRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	base := "/api/projects/" + addProject(t, ts.m, composerRepo(t))

	w := ts.do(http.MethodGet, base+"/files?q=readme&limit=5", "", auth)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"files":[{"path":"README.md","type":"file"}],"reason":""}` {
		t.Fatalf("files = %d %s", w.Code, w.Body)
	}
	var list FileList
	if w := ts.do(http.MethodGet, base+"/files?limit=1", "", auth); w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list.Files) != 1 {
		t.Fatalf("limit 1 = %d %s", w.Code, w.Body)
	}
	for _, limit := range []string{"0", "201", "x"} {
		if w := ts.do(http.MethodGet, base+"/files?limit="+limit, "", auth); w.Code != http.StatusBadRequest {
			t.Fatalf("limit %s = %d", limit, w.Code)
		}
	}
	if w := ts.do(http.MethodGet, "/api/projects/nope/files", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown project = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodGet, base+"/files", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("files without cookie = %d", w.Code)
	}
	if n := len(ts.m.List()); n != 0 {
		t.Fatalf("listing created %d tasks", n)
	}
}

func TestOpenCodeCommandArgumentsRefuseShellExpansion(t *testing.T) {
	prov := agenttest.NewProvider(agentapi.ProviderOpenCode, allCaps)
	m := startManager(t, openTestStore(t), prov)
	sum, err := m.Create(CreateRequest{Provider: prov.Name(), ProjectID: addProject(t, m, t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	prov.SetCommands([]agentapi.Command{{Name: "probe-cmd"}}, nil)
	_, err = m.Command(sum.ID, CommandRequest{RequestID: mustUUID(t), Name: "probe-cmd", Arguments: "!`touch ARG-MARKER`"})
	if statusOf(err) != http.StatusBadRequest || len(prov.Last().CommandRuns()) != 0 {
		t.Fatalf("shell expansion = %v, runs %d", err, len(prov.Last().CommandRuns()))
	}
}

func TestComposerRoutes(t *testing.T) {
	dir := composerRepo(t)
	ts := newTestServer(t, ServerConfig{})
	auth := withCookie(ts)
	sum, err := ts.m.Create(CreateRequest{Provider: "fake", ProjectID: addProject(t, ts.m, dir)})
	if err != nil {
		t.Fatal(err)
	}
	ts.prov.SetCommands([]agentapi.Command{{Name: "init", Kind: agentapi.CommandPrompt}}, nil)
	base := "/api/sessions/" + sum.ID

	w := ts.do(http.MethodGet, base+"/commands", "", auth)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"commands":[{"name":"init","description":"","kind":"command","input_hint":""}]}` {
		t.Fatalf("commands = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodGet, base+"/files?q=readme&limit=5", "", auth)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"files":[{"path":"README.md","type":"file"}],"reason":""}` {
		t.Fatalf("files = %d %s", w.Code, w.Body)
	}
	for _, limit := range []string{"0", "201", "x"} {
		if w := ts.do(http.MethodGet, base+"/files?limit="+limit, "", auth); w.Code != http.StatusBadRequest {
			t.Fatalf("limit %s = %d", limit, w.Code)
		}
	}
	if w := ts.do(http.MethodGet, base+"/files", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("files without cookie = %d", w.Code)
	}
	w = ts.do(http.MethodPost, base+"/prompt", `{"text":"see @README.md","request_id":"`+mustUUID(t)+`","files":["README.md"]}`, auth)
	if w.Code != http.StatusAccepted {
		t.Fatalf("prompt with files = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPost, base+"/prompt", `{"text":"x","request_id":"`+mustUUID(t)+`","files":["../x"]}`, auth)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `\"../x\"`) {
		t.Fatalf("prompt with a bad file = %d %s", w.Code, w.Body)
	}
	ts.prov.Last().EmitTurn(agentapi.TurnCompleted, "")
	w = ts.do(http.MethodPost, base+"/command", `{"request_id":"`+mustUUID(t)+`","name":"init","arguments":""}`, auth)
	var sub Submission
	if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &sub) != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("command = %d %s", w.Code, w.Body)
	}
	if w := ts.do(http.MethodPost, base+"/command", `{"request_id":"`+mustUUID(t)+`","name":"nope"}`, auth); w.Code != http.StatusConflict {
		t.Fatalf("command while working = %d %s", w.Code, w.Body)
	}
}
