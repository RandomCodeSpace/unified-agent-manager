package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type cliFakeAdapter struct {
	name     string
	sessions []adapter.Session
	stopped  bool
	resumed  bool
	attached []string
}

func (f *cliFakeAdapter) Name() string {
	if f.name == "" {
		return "fake"
	}
	return f.name
}
func (f *cliFakeAdapter) DisplayName() string { return f.Name() }
func (f *cliFakeAdapter) Available() (bool, string) {
	return true, ""
}
func (f *cliFakeAdapter) TerminalPolicy() adapter.ProviderTerminalPolicy {
	return adapter.ProviderTerminalPolicy{
		Identity:    adapter.ProviderIdentity(f.Name()),
		OuterScreen: adapter.OuterScreenUAM,
		KeyProtocol: adapter.KeyProtocolNative,
	}
}
func (f *cliFakeAdapter) Dispatch(ctx adapter.Context, req adapter.DispatchRequest) (adapter.Session, error) {
	if req.Prompt == "fail" {
		return adapter.Session{}, errors.New("fail")
	}
	sess := adapter.Session{ID: "abc12345", AgentType: f.Name(), CommandAlias: req.CommandAlias, DisplayName: firstNonEmpty(req.Name, req.Prompt, "untitled"), Prompt: req.Prompt, Cwd: req.Cwd, SessionName: "uam-" + f.Name() + "-abc12345", State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	f.sessions = append(f.sessions, sess)
	return sess, nil
}
func (f *cliFakeAdapter) List(ctx adapter.Context) ([]adapter.Session, error) { return f.sessions, nil }
func (f *cliFakeAdapter) Peek(ctx adapter.Context, id string) (adapter.PeekResult, error) {
	return adapter.PeekResult{TailText: "tail for " + id}, nil
}
func (f *cliFakeAdapter) Reply(ctx adapter.Context, id, text string) error { return nil }
func (f *cliFakeAdapter) Attach(id string) (adapter.AttachSpec, error) {
	f.attached = append(f.attached, id)
	return adapter.AttachSpec{Argv: []string{"echo", id}}, nil
}

// Stop drops the live sessions, mirroring the real backend where Kill
// returns only after the host is fully gone.
func (f *cliFakeAdapter) Stop(ctx adapter.Context, id string) error {
	f.stopped = true
	f.sessions = nil
	return nil
}

func (f *cliFakeAdapter) Resume(ctx adapter.Context, req adapter.ResumeRequest) (adapter.Session, error) {
	f.resumed = true
	sess := adapter.Session{ID: req.ID, AgentType: f.Name(), DisplayName: req.Name, Cwd: req.Cwd, SessionName: req.SessionName, State: adapter.Active, ProcAlive: adapter.Alive, CreatedAt: time.Now()}
	f.sessions = append(f.sessions, sess)
	return sess, nil
}

func noopRunTUI(context.Context, tea.Model) error { return nil }

func TestTUIExitCleanupIsMinimalAndOrdered(t *testing.T) {
	var out bytes.Buffer
	if err := writeTUIExitCleanup(&out); err != nil {
		t.Fatal(err)
	}
	want := "\x1b[0m\x1b[?1000;1002;1003;1004;1005;1006;1015l\x1b[?2004l\x1b[?25h\x1b[2K\r"
	if out.String() != want {
		t.Fatalf("cleanup = %q, want %q", out.String(), want)
	}
	for _, forbidden := range []string{"\x1b[2J", "\x1b[3J", "\x1bc", "\x1b[?1049l"} {
		if strings.Contains(out.String(), forbidden) {
			t.Fatalf("cleanup contains destructive sequence %q", forbidden)
		}
	}
}

type resizeSynchronizedQuitModel struct{}

func (resizeSynchronizedQuitModel) Init() tea.Cmd { return nil }
func (m resizeSynchronizedQuitModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return m, tea.Quit
	}
	return m, nil
}
func (resizeSynchronizedQuitModel) View() string { return "dashboard-marker" }

func TestRunTUICleansPrimaryLineAfterBubbleTeaExit(t *testing.T) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close() }()
	oldStdout := os.Stdout
	oldStdin := os.Stdin
	os.Stdout = tty
	os.Stdin = tty
	t.Cleanup(func() { os.Stdout, os.Stdin = oldStdout, oldStdin })
	var out bytes.Buffer
	readDone := make(chan struct{})
	go func() { _, _ = io.Copy(&out, ptmx); close(readDone) }()
	// Bubble Tea starts its initial size query in an untracked goroutine. Quit
	// only after its WindowSizeMsg establishes that the PTY Fd query completed;
	// then closing our test-owned slave cannot race that query.
	if err := RunTUI(context.Background(), resizeSynchronizedQuitModel{}); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldStdout
	os.Stdin = oldStdin
	_ = tty.Close()
	<-readDone
	got := out.String()
	cleanupAt := strings.LastIndex(got, tuiExitCleanup)
	altExitAt := strings.LastIndex(got, "\x1b[?1049l")
	if cleanupAt < 0 || altExitAt < 0 || cleanupAt <= altExitAt {
		t.Fatalf("cleanup not after Bubble Tea alt exit: %q", got)
	}
	if got[cleanupAt:] != tuiExitCleanup {
		t.Fatalf("unexpected bytes after cleanup: %q", got[cleanupAt:])
	}
}

func TestRunDispatchListPeekAndStop(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"--cwd", "/tmp", "fake", "#bugfix", "fix", "thing"})
	if id != "abc12345" {
		t.Fatalf("id=%q", id)
	}
	if out := captureCLIStdout(t, func() { must(t, runList(context.Background(), svc, []string{"--json"})) }); !strings.Contains(out, "bugfix") {
		t.Fatalf("list=%q", out)
	}
	if out := captureCLIStdout(t, func() { must(t, runPeek(context.Background(), svc, []string{id})) }); !strings.Contains(out, "tail for") {
		t.Fatalf("peek=%q", out)
	}
	must(t, runStop(context.Background(), svc, "stop", []string{id}))
	if !fake.stopped {
		t.Fatal("fake adapter was not stopped")
	}
}

// restart stops the live agent process and resumes the session in place.
func TestRunRestart(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"--cwd", "/tmp", "fake", "#bugfix", "fix", "thing"})
	must(t, runRestart(context.Background(), svc, []string{id}))
	if !fake.stopped || !fake.resumed {
		t.Fatalf("restart must stop then resume: stopped=%v resumed=%v", fake.stopped, fake.resumed)
	}
	if err := runRestart(context.Background(), svc, nil); err == nil {
		t.Fatal("restart without id should fail")
	}
}

func TestAttachAndRestartAcceptAllowLatest(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"--cwd", "/tmp", "fake", "work"})
	must(t, runRestart(context.Background(), svc, []string{"--allow-latest", id}))
	if !fake.resumed {
		t.Fatal("restart --allow-latest did not resume")
	}
	if err := runCommand(context.Background(), svc, []string{"attach", "--allow-latest", id}, noopRunTUI); err != nil {
		t.Fatalf("attach --allow-latest: %v", err)
	}
}

func TestCLIArgumentValidationAndParsing(t *testing.T) {
	svc, _ := newCLITestService(t)
	if err := RunDispatch(context.Background(), svc, nil); err == nil {
		t.Fatal("dispatch without agent should fail")
	}
	if _, err := requireArg(nil, "missing"); err == nil {
		t.Fatal("empty args should fail")
	}
	name, prompt := parseNameAndPrompt([]string{"#name", "do", "work"})
	if name != "name" || prompt != "do work" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
	name, prompt = parseNameAndPrompt([]string{"do", "work"})
	if name != "" || prompt != "do work" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
	name, prompt = parseNameAndPrompt([]string{"#name", "do   work", "\twith tabs"})
	if name != "name" || prompt != "do   work \twith tabs" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
	name, prompt = parseNameAndPrompt([]string{"do   work", "\twith tabs"})
	if name != "" || prompt != "do   work \twith tabs" {
		t.Fatalf("name=%q prompt=%q", name, prompt)
	}
}

func TestRunWithTUIHelpVersionAndDefault(t *testing.T) {
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	called := false
	runTUI := func(ctx context.Context, model tea.Model) error {
		called = true
		return nil
	}
	if err := RunWithTUI(context.Background(), nil, runTUI); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("default command should run TUI")
	}
	if out := captureCLIStderr(t, Usage); !strings.Contains(out, "uam") {
		t.Fatalf("usage=%q", out)
	}
	versionOut := captureCLIStdout(t, func() { must(t, RunWithTUI(context.Background(), []string{"version"}, runTUI)) })
	if strings.TrimSpace(versionOut) == "" {
		t.Fatal("version output should not be empty")
	}
	if err := RunWithTUI(context.Background(), []string{"unknown"}, runTUI); err == nil {
		t.Fatal("unknown command should fail")
	}
}

func TestRunWithTUIStateFreeCommandsDoNotOpenStore(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_CONFIG_DIR", blocked)
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))

	for _, args := range [][]string{{"help"}, {"version"}, {"kill-all"}} {
		if err := RunWithTUI(context.Background(), args, noopRunTUI); err != nil {
			t.Fatalf("RunWithTUI(%q) opened the store: %v", args, err)
		}
	}
	if err := RunWithTUI(context.Background(), []string{"__host", "--name", "bad name", "--", "/bin/true"}, noopRunTUI); err == nil || !strings.Contains(err.Error(), "invalid session name") {
		t.Fatalf("__host must be routed before the store and return its own validation error, got %v", err)
	}
	if err := RunWithTUI(context.Background(), []string{"__attach"}, noopRunTUI); err == nil || !strings.Contains(err.Error(), "attach requires a session name") {
		t.Fatalf("__attach must be routed before the store and return its own validation error, got %v", err)
	}
	if err := RunWithTUI(context.Background(), []string{"__opencode"}, noopRunTUI); err == nil || !strings.Contains(err.Error(), "OpenCode supervisor") {
		t.Fatalf("__opencode must be routed before the store and return its own validation error, got %v", err)
	}
}

func TestMainStatelessCommandsSkipLoggerAndStore(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"help", "version"} {
		cmd := cliMainSubprocess(t, command, blocked, blocked)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("uam %s: %v\n%s", command, err, output)
		}
		if strings.Contains(string(output), "failed to initialize logger") {
			t.Fatalf("uam %s initialized the logger: %s", command, output)
		}
	}
}

func TestMainFallsBackToStderrWhenFileLoggerFails(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := cliMainSubprocess(t, "kill-all", blocked, filepath.Join(t.TempDir(), "config"))
	cmd.Env = append(cmd.Env, "UAM_SESSION_DIR="+secureSessionDir(t))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("uam kill-all must continue with stderr logging: %v\n%s", err, output)
	}
	text := string(output)
	if strings.Count(text, "failed to initialize logger") != 1 {
		t.Fatalf("logger fallback warning count = %d, want 1: %s", strings.Count(text, "failed to initialize logger"), text)
	}
	if !strings.Contains(text, "all uam sessions stopped") {
		t.Fatalf("kill-all did not run after logger fallback: %s", text)
	}
}

func TestCLIMainHelperProcess(t *testing.T) {
	command := os.Getenv("UAM_TEST_MAIN_COMMAND")
	if command == "" {
		return
	}
	args := []string{command}
	if encoded := os.Getenv("UAM_TEST_MAIN_ARGS"); encoded != "" {
		if err := json.Unmarshal([]byte(encoded), &args); err != nil {
			os.Exit(97)
		}
	}
	flag.CommandLine = flag.NewFlagSet("uam", flag.ContinueOnError)
	os.Args = append([]string{"uam"}, args...)
	Main()
	os.Exit(0)
}

func TestMainPropagatesOpenCodeAttachExitCodeWithoutPrintingError(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	provider := filepath.Join(t.TempDir(), "opencode")
	script := `#!/bin/sh
case "$1" in
  --version) printf '1.18.1\n'; exit 0 ;;
  serve) shift; exec "$UAM_CLI_TEST_EXE" -test.run=^TestCLIOpenCodeProviderHelper$ -- serve "$@" ;;
  attach) exit 23 ;;
esac
exit 97
`
	if err := os.WriteFile(provider, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Clean(t.TempDir())
	runtimeDir := secureSessionDir(t)
	args := []string{
		"__opencode",
		"--path", provider,
		"--dir", cwd,
		"--name", "uam-opencode-a1b2c3d4",
		"--runtime-dir", runtimeDir,
		"--mode", "safe",
	}
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestCLIMainHelperProcess$")
	cmd.Env = append(os.Environ(),
		"UAM_TEST_MAIN_COMMAND=__opencode",
		"UAM_TEST_MAIN_ARGS="+string(encoded),
		"UAM_CLI_TEST_EXE="+executable,
		"UAM_CLI_PROVIDER_HELPER=1",
		"UAM_CACHE_DIR="+t.TempDir(),
		"UAM_CONFIG_DIR="+filepath.Join(t.TempDir(), "unused-config"),
		"OPENCODE_SERVER_PASSWORD=credential-must-never-print",
	)
	output, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 23 {
		t.Fatalf("Main() subprocess = (%v, %q), want exit code 23", err, output)
	}
	if len(output) != 0 {
		t.Fatalf("Main() printed an attach/credential error: %q", output)
	}
}

func TestCLIOpenCodeProviderHelper(t *testing.T) {
	if os.Getenv("UAM_CLI_PROVIDER_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) || os.Args[separator+1] != "serve" {
		t.Fatalf("provider helper argv = %q", os.Args)
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	hostname := fs.String("hostname", "", "")
	port := fs.Int("port", 0, "")
	if err := fs.Parse(os.Args[separator+2:]); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/global/health":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"healthy":true,"version":"1.18.1"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
		case request.Method == http.MethodPost && request.URL.Path == "/session":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"id": "ses_cli123", "directory": request.Header.Get("X-OpenCode-Directory"), "title": "CLI helper",
			})
		default:
			http.NotFound(w, request)
		}
	})
	server := &http.Server{
		Addr:              net.JoinHostPort(*hostname, strconv.Itoa(*port)),
		Handler:           handler,
		ReadHeaderTimeout: time.Second,
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
}

func cliMainSubprocess(t *testing.T, command, cacheDir, configDir string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCLIMainHelperProcess$")
	cmd.Env = append(os.Environ(),
		"UAM_TEST_MAIN_COMMAND="+command,
		"UAM_CACHE_DIR="+cacheDir,
		"UAM_CONFIG_DIR="+configDir,
	)
	return cmd
}

func TestRunCommandAttachLastAndNew(t *testing.T) {
	svc, fake := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"fake", "attachable"})
	var tuiCalls int
	runTUI := func(ctx context.Context, model tea.Model) error {
		tuiCalls++
		return nil
	}
	if err := runCommand(context.Background(), svc, []string{"attach", id}, runTUI); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(context.Background(), svc, []string{"last"}, runTUI); err != nil {
		t.Fatal(err)
	}
	if tuiCalls != 2 {
		t.Fatalf("TUI calls=%d", tuiCalls)
	}
	out := captureCLIStdout(t, func() {
		withCLIStdin(t, "fake\n\n/tmp\n#from-new prompt\n", func() { must(t, runNew(context.Background(), svc, runTUI)) })
	})
	if !strings.Contains(out, "abc12345") {
		t.Fatalf("new should attach the created session, output=%q", out)
	}
	if tuiCalls != 3 {
		t.Fatalf("TUI calls=%d, want 3", tuiCalls)
	}
	if len(fake.attached) == 0 || fake.attached[len(fake.attached)-1] != "abc12345" {
		t.Fatalf("new did not attach created session, attached=%v", fake.attached)
	}
}

func TestRunLastWithoutSessionsFails(t *testing.T) {
	svc, _ := newCLITestService(t)
	if err := runLast(context.Background(), svc, func(context.Context, tea.Model) error { return nil }); err == nil {
		t.Fatal("last without sessions should fail")
	}
}

func TestRunNotifyClosedFlagsRecord(t *testing.T) {
	svc, _ := newCLITestService(t)
	id := dispatchAndCaptureID(t, svc, []string{"fake", "to-close"})
	if id == "" {
		t.Fatal("dispatch did not return an id")
	}
	// The notify payload is the backend session name, not the agent id.
	if err := runNotifyClosed(svc, []string{"uam-fake-" + id}); err != nil {
		t.Fatalf("notify-closed: %v", err)
	}
	cfg, err := svc.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := cfg.Sessions[store.Key("fake", id)]
	if !ok {
		t.Fatalf("record missing: %+v", cfg.Sessions)
	}
	if rec.Status != store.StatusClosedByUser {
		t.Fatalf("status = %q, want %q", rec.Status, store.StatusClosedByUser)
	}
}

func TestRunNotifyClosedRequiresName(t *testing.T) {
	svc, _ := newCLITestService(t)
	if err := runNotifyClosed(svc, nil); err == nil {
		t.Fatal("notify-closed without a session name should fail")
	}
}

func TestNewServiceRegistersHermesWhenAvailable(t *testing.T) {
	dir := t.TempDir()
	writeCLIExecutable(t, filepath.Join(dir, "hermes"))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(st)
	if a, ok := svc.Registry.Get("hermes"); !ok || a.DisplayName() != "Hermes Agent" {
		t.Fatalf("hermes adapter missing: %v %v", a, ok)
	}
}

func newCLITestService(t *testing.T) (*app.Service, *cliFakeAdapter) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &cliFakeAdapter{}
	return app.NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{fake})), fake
}

func dispatchAndCaptureID(t *testing.T, svc *app.Service, args []string) string {
	t.Helper()
	out := captureCLIStdout(t, func() { must(t, RunDispatch(context.Background(), svc, args)) })
	return strings.TrimSpace(out)
}

func captureCLIStdout(t *testing.T, fn func()) string {
	t.Helper()
	return captureCLIFile(t, &os.Stdout, fn)
}

func captureCLIStderr(t *testing.T, fn func()) string {
	t.Helper()
	return captureCLIFile(t, &os.Stderr, fn)
}

func captureCLIFile(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	old := *target
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	*target = w
	defer func() {
		_ = w.Close()
		*target = old
	}()
	fn()
	_ = w.Close()
	*target = old
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func withCLIStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	old := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.WriteString(input)
	_ = w.Close()
	os.Stdin = r
	defer func() { os.Stdin = old }()
	fn()
}

func writeCLIExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func secureSessionDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// The internal __host/__attach subcommands are routed before store access;
// invalid input must surface as errors rather than silently doing nothing.
func TestRunWithoutStoreInternalSubcommands(t *testing.T) {
	t.Setenv("UAM_SESSION_DIR", secureSessionDir(t))
	if handled, err := runWithoutStore(context.Background(), []string{"__host", "--name", "bad name", "--", "/bin/true"}); !handled || err == nil {
		t.Fatal("__host with an invalid name must fail")
	}
	if handled, err := runWithoutStore(context.Background(), []string{"__attach"}); !handled || err == nil {
		t.Fatal("__attach without a session must fail")
	}
	if handled, err := runWithoutStore(context.Background(), []string{"__opencode"}); !handled || err == nil {
		t.Fatal("__opencode without supervisor flags must fail before store access")
	}
}
