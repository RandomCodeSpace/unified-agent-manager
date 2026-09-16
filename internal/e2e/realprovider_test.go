package e2e

// Real-provider end-to-end suite: drives the shipped uam binary against the
// real opencode, copilot and codex CLIs through the public CLI surface
// (dispatch, attach, restart, stop, rm, ls, doctor, kill-all). Every other
// E2E test in this repository uses a fake provider; this is the only place a
// provider's actual argv contract, startup, prompt delivery, resume and
// teardown are observed.
//
// Opt-in, because it makes real model calls on the operator's accounts:
//
//	UAM_E2E_BIN=$(pwd)/bin/uam UAM_E2E_REAL_PROVIDERS=opencode,copilot,codex \
//	  go test ./internal/e2e -run TestRealProvider -count=1 -v
//
// Provider state is isolated from the operator's own history: each provider's
// home directory is redirected (CODEX_HOME, COPILOT_HOME, XDG_DATA_HOME and
// XDG_CONFIG_HOME for opencode) to a per-run temp root that borrows only the
// credential files. UAM_E2E_OPENCODE_MODEL picks the opencode model (default
// ollama-cloud/gpt-oss:20b).
//
// Raw viewer captures are written to <root>/captures/ and the root is kept
// when a test fails or UAM_E2E_KEEP is set.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/vterm"
)

const (
	binEnv           = "UAM_E2E_BIN"
	providersEnv     = "UAM_E2E_REAL_PROVIDERS"
	openCodeModelEnv = "UAM_E2E_OPENCODE_MODEL"
	keepEnv          = "UAM_E2E_KEEP"

	detachChord = "\x02d"
	ctrlLeft    = "\x1b[1;5D"
	altScreenOn = "\x1b[?1049h"

	// Prompts ask for a token that does not appear in the prompt itself, so a
	// TUI echoing the prompt cannot satisfy the reply assertion.
	firstPrompt = "Reply with only the uppercase form of the word 'zebra'. No punctuation."
	firstReply  = "ZEBRA"
	// promptFragment identifies the first prompt echoed in a composer.
	promptFragment = "uppercase form of the word 'zebra'"
	secondPrompt   = "Reply with only the uppercase form of the word 'yak'. No punctuation."
	secondReply    = "YAK"

	viewerCols = 120
	viewerRows = 40

	startupTimeout = 90 * time.Second
	replyTimeout   = 120 * time.Second
)

var uuidRE = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// providerSpec is what the suite knows about one provider's contract.
type providerSpec struct {
	name string
	// yoloArgs must all be present in a yolo launch argv and absent in safe.
	yoloArgs []string
	// argvCheck validates provider-specific launch shape beyond the mode args.
	argvCheck func(argv []string, mode string) error
	// resumeCheck validates the argv of a restarted session.
	resumeCheck func(argv []string, uamID, providerID string) error
	// altScreen reports whether the attach client wraps the provider in the
	// alternate screen (OuterScreenUAM) or leaves it on the primary screen.
	altScreen bool
	// backDetach reports whether Ctrl+Left on an empty input box detaches.
	backDetach bool
	// readyMarker returns a byte sequence the provider TUI prints once it has
	// painted its idle screen; the suite then waits for output to go quiet.
	readyMarker func(h *harness) string
	// isolate redirects provider state into the run root and returns the env
	// assignments to apply to launches of this provider.
	isolate func(t *testing.T, h *harness) []string
	// providerSessions counts provider-side session records in the isolated
	// store; resume must not create a second one.
	providerSessions func(h *harness) int
	// providerIDCheck validates the ProviderSessionID uam recorded.
	providerIDCheck func(id, uamID string) error
	// storeCheck validates the provider's own record of a session that has
	// answered a prompt.
	storeCheck func(h *harness, uamID string) error
}

func hasAll(argv []string, want []string) bool {
	for _, w := range want {
		if !slices.Contains(argv, w) {
			return false
		}
	}
	return true
}

func linkCredential(t *testing.T, src, dst string) {
	t.Helper()
	if _, err := os.Stat(src); err != nil {
		t.Skipf("credential file %s unavailable: %v", src, err)
	}
	if err := os.Symlink(src, dst); err != nil {
		t.Fatal(err)
	}
}

func countEntries(dir string, match func(os.DirEntry) bool) int {
	n := 0
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && match(d) {
			n++
		}
		return nil
	})
	return n
}

func flagValue(argv []string, flag string) (string, bool) {
	i := slices.Index(argv, flag)
	if i < 0 || i+1 >= len(argv) {
		return "", false
	}
	return argv[i+1], true
}

func codexRollouts(h *harness) int {
	return countEntries(filepath.Join(h.root, "codex-home", "sessions"), func(d os.DirEntry) bool {
		return !d.IsDir() && strings.HasPrefix(d.Name(), "rollout-")
	})
}

var providers = map[string]providerSpec{
	"codex": {
		name:     "codex",
		yoloArgs: []string{"--sandbox", "danger-full-access"},
		argvCheck: func(argv []string, _ string) error {
			if argv[0] != "codex" || !slices.Contains(argv, "--no-alt-screen") {
				return fmt.Errorf("codex launch argv %q lacks --no-alt-screen", argv)
			}
			return nil
		},
		resumeCheck: func(argv []string, uamID, _ string) error {
			if v, ok := flagValue(argv, "resume"); !ok || v != "--last" {
				return fmt.Errorf("codex resume argv %q lacks `resume --last`", argv)
			}
			if slices.Contains(argv, uamID) {
				return fmt.Errorf("codex resume argv %q leaks the uam id", argv)
			}
			return nil
		},
		altScreen:   false,
		backDetach:  true,
		readyMarker: func(*harness) string { return "OpenAI Codex (v" },
		isolate: func(t *testing.T, h *harness) []string {
			home := filepath.Join(h.root, "codex-home")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			real := filepath.Join(os.Getenv("HOME"), ".codex")
			linkCredential(t, filepath.Join(real, "auth.json"), filepath.Join(home, "auth.json"))
			config, err := os.ReadFile(filepath.Join(real, "config.toml"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			// Pre-trust the run's work directories so codex does not block on
			// its directory-trust dialog (which also swallows typed input).
			var trust strings.Builder
			trust.Write(config)
			for _, dir := range []string{h.work, h.workSafe} {
				fmt.Fprintf(&trust, "\n[projects.%q]\ntrust_level = \"trusted\"\n", dir)
			}
			if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(trust.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"CODEX_HOME=" + home}
		},
		providerSessions: codexRollouts,
		providerIDCheck: func(id, _ string) error {
			if id != "" {
				return fmt.Errorf("codex has no exact resume; recorded provider id %q", id)
			}
			return nil
		},
		storeCheck: func(h *harness, _ string) error {
			if n := codexRollouts(h); n == 0 {
				return errors.New("codex wrote no rollout file for the answered session")
			}
			return nil
		},
	},
	"copilot": {
		name:     "copilot",
		yoloArgs: []string{"--yolo"},
		argvCheck: func(argv []string, _ string) error {
			if argv[0] != "copilot" {
				return fmt.Errorf("copilot launch argv %q", argv)
			}
			id, ok := flagValue(argv, "--session-id")
			if !ok || !uuidRE.MatchString(id) {
				return fmt.Errorf("copilot launch argv %q lacks --session-id <uuid>", argv)
			}
			if name, ok := flagValue(argv, "--name"); !ok || name != id {
				return fmt.Errorf("copilot launch argv %q does not seed --name with the session id", argv)
			}
			return nil
		},
		resumeCheck: func(argv []string, uamID, _ string) error {
			if !slices.Contains(argv, "--resume="+uamID) {
				return fmt.Errorf("copilot resume argv %q lacks --resume=%s", argv, uamID)
			}
			if slices.Contains(argv, "--session-id") {
				return fmt.Errorf("copilot resume argv %q still pins --session-id", argv)
			}
			return nil
		},
		altScreen:   true,
		backDetach:  true,
		readyMarker: func(h *harness) string { return h.root + "/work" },
		isolate: func(t *testing.T, h *harness) []string {
			home := filepath.Join(h.root, "copilot-home")
			if err := os.MkdirAll(home, 0o700); err != nil {
				t.Fatal(err)
			}
			trusted, err := json.Marshal(map[string]any{"trustedFolders": []string{h.work, h.workSafe}, "appTipShown": true})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(home, "config.json"), trusted, 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"COPILOT_HOME=" + home}
		},
		providerSessions: func(h *harness) int {
			entries, _ := os.ReadDir(filepath.Join(h.root, "copilot-home", "session-state"))
			n := 0
			for _, e := range entries {
				if e.IsDir() && uuidRE.MatchString(e.Name()) {
					n++
				}
			}
			return n
		},
		providerIDCheck: func(id, uamID string) error {
			if id != uamID {
				return fmt.Errorf("copilot provider id %q, want the uam id %q", id, uamID)
			}
			return nil
		},
		storeCheck: func(h *harness, uamID string) error {
			dir := filepath.Join(h.root, "copilot-home", "session-state", uamID)
			if _, err := os.Stat(dir); err != nil {
				return fmt.Errorf("copilot did not pin its session to the uam id: %w", err)
			}
			return nil
		},
	},
	"opencode": {
		name:     "opencode",
		yoloArgs: nil,
		argvCheck: func(argv []string, mode string) error {
			if len(argv) < 2 || argv[1] != "__opencode" {
				return fmt.Errorf("opencode launch argv %q is not the uam supervisor", argv)
			}
			if v, ok := flagValue(argv, "--mode"); !ok || v != mode {
				return fmt.Errorf("opencode launch argv %q lacks --mode %s", argv, mode)
			}
			return nil
		},
		resumeCheck: func(argv []string, _, providerID string) error {
			if v, ok := flagValue(argv, "--session"); !ok || v != providerID {
				return fmt.Errorf("opencode resume argv %q lacks --session %s", argv, providerID)
			}
			if slices.Contains(argv, "--prompt-fd") {
				return fmt.Errorf("opencode resume argv %q replays the initial prompt", argv)
			}
			return nil
		},
		altScreen:   true,
		backDetach:  true,
		readyMarker: func(h *harness) string { return h.root + "/work" },
		isolate: func(t *testing.T, h *harness) []string {
			data := filepath.Join(h.root, "xdg-data")
			config := filepath.Join(h.root, "xdg-config")
			if err := os.MkdirAll(filepath.Join(data, "opencode"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(config, "opencode"), 0o700); err != nil {
				t.Fatal(err)
			}
			real := filepath.Join(os.Getenv("HOME"), ".local", "share", "opencode")
			linkCredential(t, filepath.Join(real, "auth.json"), filepath.Join(data, "opencode", "auth.json"))
			for _, optional := range []string{"account.json", "mcp-auth.json"} {
				if _, err := os.Stat(filepath.Join(real, optional)); err == nil {
					_ = os.Symlink(filepath.Join(real, optional), filepath.Join(data, "opencode", optional))
				}
			}
			// A minimal config: the operator's plugins, MCP servers and agent
			// overrides are not what this suite tests.
			model := os.Getenv(openCodeModelEnv)
			if model == "" {
				model = "ollama-cloud/gpt-oss:20b"
			}
			cfg, err := json.Marshal(map[string]any{"$schema": "https://opencode.ai/config.json", "model": model})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "opencode", "opencode.json"), cfg, 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{"XDG_DATA_HOME=" + data, "XDG_CONFIG_HOME=" + config}
		},
		providerSessions: func(h *harness) int {
			// One uam session maps to one identity file; the supervisor
			// creates exactly one root session per dispatch.
			return countEntries(h.sessions, func(d os.DirEntry) bool {
				return strings.HasSuffix(d.Name(), ".provider.json")
			})
		},
		providerIDCheck: func(id, _ string) error {
			if !strings.HasPrefix(id, "ses_") {
				return fmt.Errorf("opencode provider id %q is not a ses_ id", id)
			}
			return nil
		},
		storeCheck: func(h *harness, uamID string) error {
			row := h.mustFind(uamID)
			path := filepath.Join(h.sessions, row.SessionName+".provider.json")
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if !bytes.Contains(data, []byte(row.ProviderSessionID)) {
				return fmt.Errorf("identity file %s does not carry %s", path, row.ProviderSessionID)
			}
			return nil
		},
	},
}

// harness is one isolated uam installation: its own session runtime dir,
// config dir, cache dir, and two git work trees.
type harness struct {
	t        *testing.T
	bin      string
	root     string
	sessions string
	captures string
	work     string
	workSafe string
	env      []string
	// providerEnv holds each isolated provider's extra env, applied only to
	// commands that launch or attach to that provider.
	providerEnv map[string][]string
	captureSeq  int
}

func enabledProviders(t *testing.T) []string {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(providersEnv))
	if raw == "" {
		t.Skipf("%s is required: comma-separated subset of opencode,copilot,codex", providersEnv)
	}
	var out []string
	for name := range strings.SplitSeq(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := providers[name]; !ok {
			t.Fatalf("%s: unknown provider %q", providersEnv, name)
		}
		out = append(out, name)
	}
	return out
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	bin := os.Getenv(binEnv)
	if bin == "" {
		t.Skipf("%s is required: point it at a built uam binary", binEnv)
	}
	if !filepath.IsAbs(bin) {
		t.Fatalf("%s must be absolute: %q", binEnv, bin)
	}
	// Short root: control sockets live under it and sockaddr_un is ~104 bytes.
	root, err := os.MkdirTemp("/tmp", "uam-rp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &harness{
		t: t, bin: bin, root: root,
		sessions: filepath.Join(root, "sessions"), captures: filepath.Join(root, "captures"),
		work: filepath.Join(root, "work"), workSafe: filepath.Join(root, "work-safe"),
		providerEnv: map[string][]string{},
	}
	for _, dir := range []string{h.sessions, h.captures, filepath.Join(root, "cfg"), filepath.Join(root, "cache"), h.work, h.workSafe} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{h.work, h.workSafe} {
		initGit(t, dir)
	}
	h.env = append(filteredEnviron(),
		"UAM_SESSION_DIR="+h.sessions,
		"UAM_CONFIG_DIR="+filepath.Join(root, "cfg"),
		"UAM_CACHE_DIR="+filepath.Join(root, "cache"),
		"TERM=xterm-256color",
		"UAM_WIDE=0",
	)
	t.Cleanup(func() {
		// Best effort: never leave a real provider running after the test.
		cmd := exec.Command(bin, "kill-all")
		cmd.Env = h.env
		_ = cmd.Run()
		if t.Failed() || os.Getenv(keepEnv) != "" {
			t.Logf("run root retained for inspection: %s", root)
			return
		}
		_ = os.RemoveAll(root)
	})
	return h
}

func (h *harness) enable(t *testing.T, spec providerSpec) {
	t.Helper()
	h.providerEnv[spec.name] = spec.isolate(t, h)
}

func (h *harness) envFor(provider string) []string {
	return append(slices.Clone(h.env), h.providerEnv[provider]...)
}

// filteredEnviron drops every uam and provider-home variable so the run
// cannot inherit the operator's live installation.
func filteredEnviron() []string {
	var out []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(name, "UAM_"), name == "CODEX_HOME", name == "COPILOT_HOME", name == "XDG_DATA_HOME", name == "XDG_CONFIG_HOME":
			continue
		}
		out = append(out, kv)
	}
	return out
}

func initGit(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("e2e fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "."},
		{"-c", "user.email=e2e@example.invalid", "-c", "user.name=e2e", "commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func (h *harness) runEnv(env []string, args ...string) (string, error) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), fmt.Errorf("uam %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (h *harness) mustRun(args ...string) string {
	h.t.Helper()
	out, err := h.runEnv(h.env, args...)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

func (h *harness) mustRunFor(provider string, args ...string) string {
	h.t.Helper()
	out, err := h.runEnv(h.envFor(provider), args...)
	if err != nil {
		h.t.Fatal(err)
	}
	return out
}

// sessionRow mirrors the fields of adapter.Session the suite reads from
// `uam ls --json` (encoded without tags, so Go field names).
type sessionRow struct {
	ID                string
	AgentType         string
	SessionName       string
	ProviderSessionID string
	State             string
	ProcAlive         string
}

func (h *harness) list() []sessionRow {
	h.t.Helper()
	out := h.mustRun("ls", "--json")
	var rows []sessionRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		h.t.Fatalf("ls --json: %v: %q", err, out)
	}
	return rows
}

func (h *harness) find(id string) (sessionRow, bool) {
	h.t.Helper()
	for _, row := range h.list() {
		if row.ID == id {
			return row, true
		}
	}
	return sessionRow{}, false
}

func (h *harness) mustFind(id string) sessionRow {
	h.t.Helper()
	row, ok := h.find(id)
	if !ok {
		h.t.Fatalf("session %s missing from ls --json", id)
	}
	return row
}

// hostState is the subset of the session host's state file the suite reads.
type hostState struct {
	HostPID  int      `json:"host_pid"`
	ChildPID int      `json:"child_pid"`
	Command  []string `json:"command"`
}

func (h *harness) state(sessionName string) hostState {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join(h.sessions, sessionName+".json"))
	if err != nil {
		h.t.Fatal(err)
	}
	var st hostState
	if err := json.Unmarshal(data, &st); err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *harness) dispatch(spec providerSpec, safe bool, cwd, label, prompt string) (id string, row sessionRow) {
	h.t.Helper()
	args := []string{"dispatch", "--cwd", cwd}
	if safe {
		args = append(args, "--safe")
	}
	args = append(args, spec.name, "#"+label)
	if prompt != "" {
		args = append(args, prompt)
	}
	id = strings.TrimSpace(h.mustRunFor(spec.name, args...))
	if !uuidRE.MatchString(id) {
		h.t.Fatalf("dispatch printed %q, want a session id", id)
	}
	row = h.mustFind(id)
	if row.ProcAlive != "Alive" {
		h.t.Fatalf("freshly dispatched %s session is %s", spec.name, row.ProcAlive)
	}
	return id, row
}

// awaitProviderID polls ls --json until the recorded ProviderSessionID
// satisfies the provider's contract, and returns it.
func (h *harness) awaitProviderID(t *testing.T, spec providerSpec, id string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		row := h.mustFind(id)
		err := spec.providerIDCheck(row.ProviderSessionID, id)
		if err == nil {
			return row.ProviderSessionID
		}
		if time.Now().After(deadline) {
			t.Fatalf("after %s: %v", timeout, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// processesWithUAMID lists every live process whose environment carries
// UAM_ID=<id>: the host, the provider, and any grandchildren it spawned.
func processesWithUAMID(id string) []int {
	needle := []byte("UAM_ID=" + id + "\x00")
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		var pid int
		if _, err := fmt.Sscanf(e.Name(), "%d", &pid); err != nil {
			continue
		}
		environ, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil {
			continue
		}
		if bytes.Contains(environ, needle) {
			pids = append(pids, pid)
		}
	}
	return pids
}

func awaitNoProcesses(t *testing.T, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		pids := processesWithUAMID(id)
		if len(pids) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("processes still carry UAM_ID=%s after %s: %v", id, timeout, pids)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func awaitGone(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s still exists after %s", path, timeout)
}

// viewer is `uam attach <id>` on its own PTY: the real attach client with the
// profile-resolved policy env, exactly as an operator gets it.
type viewer struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File
	seen *recorder
}

type recorder struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

func record(f *os.File) *recorder {
	r := &recorder{done: make(chan struct{})}
	go func() {
		defer close(r.done)
		buf := make([]byte, 8192)
		for {
			n, err := f.Read(buf)
			if n > 0 {
				r.mu.Lock()
				r.buf.Write(buf[:n])
				r.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	return r
}

// contains reports whether needle appears in the raw stream or on the
// rendered screen. A TUI repainting a spinner can interleave cursor moves
// inside a word, so raw bytes alone miss text that is plainly visible.
func (r *recorder) contains(needle string) bool {
	raw := r.bytes()
	if bytes.Contains(raw, []byte(needle)) {
		return true
	}
	term := vterm.New(viewerCols, viewerRows, 4000)
	_, _ = term.Write(raw)
	return strings.Contains(term.Capture(4000), needle)
}

func (r *recorder) size() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Len()
}

func (r *recorder) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.buf.Bytes())
}

func (r *recorder) tail() string {
	b := r.bytes()
	if len(b) > 600 {
		b = b[len(b)-600:]
	}
	return string(b)
}

func (h *harness) attach(t *testing.T, provider, id, label string) *viewer {
	t.Helper()
	cmd := exec.Command(h.bin, "attach", id)
	cmd.Env = h.envFor(provider)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: viewerCols, Rows: viewerRows})
	if err != nil {
		t.Fatal(err)
	}
	v := &viewer{t: t, cmd: cmd, ptmx: ptmx, seen: record(ptmx)}
	h.captureSeq++
	capture := filepath.Join(h.captures, fmt.Sprintf("%02d-%s-%s.raw", h.captureSeq, provider, label))
	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		<-v.seen.done
		_ = os.WriteFile(capture, v.seen.bytes(), 0o600)
	})
	if !v.await("[uam: role", 15*time.Second) {
		t.Fatalf("attach client never joined: %q", v.seen.tail())
	}
	return v
}

func (v *viewer) await(needle string, timeout time.Duration) bool {
	v.t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if v.seen.contains(needle) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return v.seen.contains(needle)
}

func (v *viewer) mustAwait(what, needle string, timeout time.Duration) {
	v.t.Helper()
	if !v.await(needle, timeout) {
		v.t.Fatalf("%s: %q never appeared; tail: %q", what, needle, v.seen.tail())
	}
}

// awaitQuiet waits until the viewer has received output and then seen none
// for idle, or gives up after timeout. A provider TUI that has finished
// painting its idle screen goes quiet; one still starting up does not.
func (v *viewer) awaitQuiet(idle, timeout time.Duration) {
	v.t.Helper()
	deadline := time.Now().Add(timeout)
	last, lastChange := v.seen.size(), time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		if n := v.seen.size(); n != last {
			last, lastChange = n, time.Now()
			continue
		}
		if time.Since(lastChange) >= idle {
			return
		}
	}
	v.t.Fatalf("output never went quiet for %s within %s; tail: %q", idle, timeout, v.seen.tail())
}

func (v *viewer) send(keys string) {
	v.t.Helper()
	if _, err := v.ptmx.WriteString(keys); err != nil {
		v.t.Fatalf("write %q to the viewer: %v", keys, err)
	}
	time.Sleep(150 * time.Millisecond)
}

// typeLine types text into the provider's composer and submits it.
func (v *viewer) typeLine(text string) {
	v.t.Helper()
	v.send(text)
	time.Sleep(300 * time.Millisecond)
	v.send("\r")
}

// attachClientAlive reports whether `uam attach` still has its `__attach`
// child: the client runs quiet (no detach notice is printed), so the child's
// exit is the detach signal.
func (v *viewer) attachClientAlive() bool {
	// Go forks from any thread, so the child is listed under whichever task
	// spawned it.
	tasks, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", v.cmd.Process.Pid))
	if err != nil {
		return false
	}
	for _, task := range tasks {
		data, err := os.ReadFile(task)
		if err != nil {
			continue
		}
		for child := range strings.FieldsSeq(string(data)) {
			cmdline, err := os.ReadFile(filepath.Join("/proc", child, "cmdline"))
			if err == nil && bytes.Contains(cmdline, []byte("__attach")) {
				return true
			}
		}
	}
	return false
}

func (v *viewer) awaitDetached(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !v.attachClientAlive() {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return !v.attachClientAlive()
}

// detach sends the control chord and waits for the attach client to exit.
// `uam attach` then opens the dashboard; the viewer is killed rather than
// driven through it.
func (v *viewer) detach(what string) {
	v.t.Helper()
	if !v.attachClientAlive() {
		v.t.Fatalf("%s: attach client already gone before detach: %q", what, v.seen.tail())
	}
	v.send(detachChord)
	if !v.awaitDetached(10 * time.Second) {
		v.t.Fatalf("%s: attach client did not exit after the detach chord: %q", what, v.seen.tail())
	}
	_ = v.cmd.Process.Kill()
}

func (v *viewer) ready(h *harness, spec providerSpec) {
	v.t.Helper()
	v.mustAwait(spec.name+" startup", spec.readyMarker(h), startupTimeout)
	v.awaitQuiet(2*time.Second, startupTimeout)
}

func TestRealProviderLifecycle(t *testing.T) {
	names := enabledProviders(t)
	for _, name := range names {
		spec := providers[name]
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s is not installed: %v", name, err)
			}
			h := newHarness(t)
			h.enable(t, spec)

			t.Run("safe dispatch omits full-access args", func(t *testing.T) {
				id, row := h.dispatch(spec, true, h.workSafe, "safe-"+name, "")
				st := h.state(row.SessionName)
				if err := spec.argvCheck(st.Command, "safe"); err != nil {
					t.Fatal(err)
				}
				for _, arg := range spec.yoloArgs {
					if slices.Contains(st.Command, arg) {
						t.Fatalf("safe launch argv %q carries full-access arg %q", st.Command, arg)
					}
				}
				v := h.attach(t, name, id, "safe")
				v.ready(h, spec)
				v.detach("safe session")
				h.mustRun("rm", id)
				if _, ok := h.find(id); ok {
					t.Fatalf("session %s still listed after rm", id)
				}
				awaitNoProcesses(t, id, 20*time.Second)
			})

			t.Run("yolo lifecycle", func(t *testing.T) {
				id, row := h.dispatch(spec, false, h.work, "yolo-"+name, firstPrompt)
				st := h.state(row.SessionName)
				if err := spec.argvCheck(st.Command, "yolo"); err != nil {
					t.Fatal(err)
				}
				if !hasAll(st.Command, spec.yoloArgs) {
					t.Fatalf("yolo launch argv %q lacks %q", st.Command, spec.yoloArgs)
				}
				// Providers that discover their id live (opencode) publish it a
				// few seconds after launch.
				providerID := h.awaitProviderID(t, spec, id, 30*time.Second)

				// Initial prompt reaches the model; the reply reaches a viewer.
				v := h.attach(t, name, id, "first")
				v.ready(h, spec)
				if !v.await(firstReply, replyTimeout) {
					if !v.seen.contains(promptFragment) {
						t.Fatalf("initial prompt neither answered nor visible in the composer; tail: %q", v.seen.tail())
					}
					// Recorded as a failure, then recovered so the rest of the
					// lifecycle still produces evidence.
					t.Errorf("initial prompt reached the composer but was never submitted; submitting manually")
					v.send("\r")
					v.mustAwait("initial prompt reply after manual Enter", firstReply, replyTimeout)
				}
				if got := v.seen.contains(altScreenOn); got != spec.altScreen {
					t.Fatalf("alternate screen used = %v, want %v (outer-screen policy)", got, spec.altScreen)
				}
				doctor := h.mustRun("doctor", id, "--json")
				if !json.Valid([]byte(doctor)) {
					t.Fatalf("doctor %s --json is not JSON: %q", id, doctor)
				}
				v.detach("first viewer")

				// Reattach replays the transcript; back-detach follows policy.
				v = h.attach(t, name, id, "reattach")
				v.mustAwait("replayed transcript", firstReply, 15*time.Second)
				v.send(ctrlLeft)
				detached := v.awaitDetached(3 * time.Second)
				if detached != spec.backDetach {
					t.Fatalf("Ctrl+Left detached = %v, want %v (back-detach policy)", detached, spec.backDetach)
				}
				if detached {
					_ = v.cmd.Process.Kill()
				} else {
					v.detach("second viewer")
				}

				if err := spec.storeCheck(h, id); err != nil {
					t.Fatal(err)
				}
				before := spec.providerSessions(h)
				oldChild := st.ChildPID

				// Restart resumes the same provider session in place.
				h.mustRunFor(name, "restart", id)
				row = h.mustFind(id)
				if row.ProcAlive != "Alive" {
					t.Fatalf("restarted session is %s", row.ProcAlive)
				}
				st = h.state(row.SessionName)
				if st.ChildPID == oldChild {
					t.Fatalf("restart kept child pid %d", oldChild)
				}
				if err := spec.resumeCheck(st.Command, id, providerID); err != nil {
					t.Fatal(err)
				}
				if !hasAll(st.Command, spec.yoloArgs) {
					t.Fatalf("resume argv %q dropped the stored yolo mode", st.Command)
				}
				v = h.attach(t, name, id, "resumed")
				v.ready(h, spec)
				v.typeLine(secondPrompt)
				v.mustAwait("reply after resume", secondReply, replyTimeout)
				v.detach("resumed viewer")
				if after := spec.providerSessions(h); after != before {
					t.Fatalf("resume changed provider session count %d -> %d", before, after)
				}

				// Stop keeps the record; rm drops it; nothing lingers.
				h.mustRun("stop", id)
				row = h.mustFind(id)
				if row.ProcAlive != "Exited" {
					t.Fatalf("stopped session is %s", row.ProcAlive)
				}
				awaitNoProcesses(t, id, 20*time.Second)
				awaitGone(t, filepath.Join(h.sessions, row.SessionName+".sock"), 10*time.Second)
				h.mustRun("rm", id)
				if _, ok := h.find(id); ok {
					t.Fatalf("session %s still listed after rm", id)
				}
			})
		})
	}
}

// TestRealProviderKillAll dispatches one safe session per enabled provider
// into a single installation and tears them all down with kill-all.
func TestRealProviderKillAll(t *testing.T) {
	names := enabledProviders(t)
	h := newHarness(t)
	var ids []string
	for _, name := range names {
		if _, err := exec.LookPath(name); err != nil {
			t.Logf("%s is not installed, skipped: %v", name, err)
			continue
		}
		spec := providers[name]
		h.enable(t, spec)
		id, _ := h.dispatch(spec, true, h.workSafe, "killall-"+name, "")
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		t.Skip("no enabled provider installed")
	}
	for _, id := range ids {
		row := h.mustFind(id)
		v := h.attach(t, row.AgentType, id, "killall")
		v.ready(h, providers[row.AgentType])
		v.detach("pre-kill viewer")
	}
	out := h.mustRun("kill-all")
	if !strings.Contains(out, "all uam sessions stopped") {
		t.Fatalf("kill-all output %q", out)
	}
	for _, id := range ids {
		awaitNoProcesses(t, id, 20*time.Second)
		if row := h.mustFind(id); row.ProcAlive != "Exited" {
			t.Fatalf("session %s is %s after kill-all", id, row.ProcAlive)
		}
	}
	entries, err := os.ReadDir(h.sessions)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sock") {
			t.Fatalf("control socket %s survives kill-all", e.Name())
		}
	}
}
