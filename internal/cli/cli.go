package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agents"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

// Main is the process entrypoint shared by the root compatibility command and cmd/uam.
func Main() {
	flag.Usage = Usage
	flag.Parse()
	args := flag.Args()

	// Help and version are deliberately independent from both the cache logger
	// and the persistent store. They must remain usable when either location is
	// unavailable (for example during installation diagnostics).
	if handled, err := runBeforeLogger(args); handled {
		if err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "uam: %v\n", err)
			os.Exit(1)
		}
		return
	}

	closer, err := log.Init()
	if err != nil {
		log.UseStderr(os.Stderr)
		fmt.Fprintf(os.Stderr, "uam: failed to initialize logger: %v\n", err)
	} else {
		defer func() { _ = closer.Close() }()
	}

	ctx := context.Background()
	if err := Run(ctx, args); err != nil && !errors.Is(err, context.Canceled) {
		var exitCoder interface{ ExitCode() int }
		if errors.As(err, &exitCoder) {
			os.Exit(exitCoder.ExitCode())
		}
		log.Error("run exited with error", "err", err)
		fmt.Fprintf(os.Stderr, "uam: %v\n", err)
		os.Exit(1)
	}
}

// Usage prints CLI help.
func Usage() {
	fmt.Fprintln(os.Stderr, "uam — unified agent manager")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  uam                              open the TUI")
	fmt.Fprintln(os.Stderr, "  uam new                          guided dispatch wizard")
	fmt.Fprintln(os.Stderr, "  uam new [--profile <name>]       guided dispatch wizard")
	fmt.Fprintln(os.Stderr, "  uam dispatch [--safe] [--alias <name>] [--profile <name>] <agent> [#session-name] [prompt]")
	fmt.Fprintln(os.Stderr, "  uam attach [--allow-latest] <name-or-id>")
	fmt.Fprintln(os.Stderr, "  uam last")
	fmt.Fprintln(os.Stderr, "  uam version")
	fmt.Fprintln(os.Stderr, "  uam ls [--json]")
	fmt.Fprintln(os.Stderr, "  uam doctor [<session-id>] [--json]")
	fmt.Fprintln(os.Stderr, "  uam peek <id>")
	fmt.Fprintln(os.Stderr, "  uam stop <id>")
	fmt.Fprintln(os.Stderr, "  uam restart [--allow-latest] <id> stop the agent and resume it in place")
	fmt.Fprintln(os.Stderr, "  uam rm <id>")
	fmt.Fprintln(os.Stderr, "  uam kill-all                      stop every managed session")
	fmt.Fprintln(os.Stderr, "  uam profile ls [--json]")
	fmt.Fprintln(os.Stderr, "  uam profile show <name> [--json]")
	fmt.Fprintln(os.Stderr, "  uam profile set <name> [profile flags]")
	fmt.Fprintln(os.Stderr, "  uam profile rm <name>")
	fmt.Fprintln(os.Stderr, "  uam profile default <name|none>")
	fmt.Fprintln(os.Stderr, "  uam profile assign <session-id> <name|none>")
	fmt.Fprintln(os.Stderr, "  uam profile override <session-id> [profile flags]")
	fmt.Fprintln(os.Stderr, "  uam profile effective <session-id> [--json]")
	fmt.Fprintln(os.Stderr, "  uam notify-closed <session-name>   (internal: flag a record user-closed)")
}

// Run executes the CLI using the default Bubble Tea TUI runner.
func Run(ctx context.Context, args []string) error {
	return RunWithTUI(ctx, args, RunTUI)
}

// RunWithTUI executes the CLI with an injectable TUI runner for tests.
func RunWithTUI(ctx context.Context, args []string, runTUI func(context.Context, tea.Model) error) error {
	if handled, err := runWithoutStore(ctx, args); handled {
		return err
	}
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	svc := NewService(st)
	if len(args) == 0 {
		// Best-effort startup prune of long-stale dead records so sessions.json
		// does not grow unbounded. Server-down-safe (a no-op when no live session
		// is visible) and advisory: a prune failure must never block launch (F20).
		if err := svc.PruneStartup(ctx); err != nil {
			log.Warn("startup prune failed", "err", err)
		}
		return runTUI(ctx, app.NewWithDeps(st, svc.Registry))
	}
	return runCommand(ctx, svc, args, runTUI)
}

// runBeforeLogger handles commands that have no cache, store, or session
// dependency. Main invokes it before log.Init; RunWithTUI reaches the same
// behavior through runWithoutStore when called directly by tests/embedders.
func runBeforeLogger(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		Usage()
		return true, nil
	case "version", "--version":
		fmt.Println(version.String())
		return true, nil
	default:
		return false, nil
	}
}

// runWithoutStore handles commands whose behavior does not depend on
// sessions.json. Keeping these ahead of store.Open makes recovery commands
// available even when the config directory is damaged or unwritable.
func runWithoutStore(ctx context.Context, args []string) (bool, error) {
	if handled, err := runBeforeLogger(args); handled {
		return true, err
	}
	if len(args) == 0 {
		return false, nil
	}
	switch args[0] {
	case "kill-all":
		return true, runKillAll(ctx, session.NewClient().KillAll)
	case "__host":
		return true, session.RunHost(args[1:])
	case "__attach":
		return true, session.RunAttach(args[1:])
	case "__opencode":
		return true, opencode.RunSupervisorCommand(args[1:])
	default:
		return false, nil
	}
}

func runCommand(ctx context.Context, svc *app.Service, args []string, runTUI func(context.Context, tea.Model) error) error {
	switch args[0] {
	case "new":
		return runNewWithArgs(ctx, svc, args[1:], runTUI)
	case "dispatch":
		return RunDispatch(ctx, svc, args[1:])
	case "ls", "list":
		return runList(ctx, svc, args[1:])
	case "peek":
		return runPeek(ctx, svc, args[1:])
	case "stop", "rm":
		return runStop(ctx, svc, args[0], args[1:])
	case "restart":
		return runRestart(ctx, svc, args[1:])
	case "notify-closed":
		return runNotifyClosed(svc, args[1:])
	case "profile":
		return runProfile(ctx, svc, args[1:])
	case "doctor":
		return runDoctor(ctx, svc, args[1:])
	case "attach":
		fs := flag.NewFlagSet("attach", flag.ContinueOnError)
		allowLatest := fs.Bool("allow-latest", false, "allow heuristic resume of the provider's latest session")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		id, err := requireArg(fs.Args(), "attach requires <id>")
		if err != nil {
			return err
		}
		return execAttachWithOptions(ctx, svc, id, app.ResumeOptions{AllowLatest: *allowLatest}, runTUI)
	case "last":
		return runLast(ctx, svc, runTUI)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runList(ctx context.Context, svc *app.Service, args []string) error {
	fs := flag.NewFlagSet("ls", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return svc.PrintList(ctx, *asJSON)
}

func runPeek(ctx context.Context, svc *app.Service, args []string) error {
	id, err := requireArg(args, "peek requires <id>")
	if err != nil {
		return err
	}
	p, err := svc.Peek(ctx, id)
	if err != nil {
		return err
	}
	fmt.Print(p.TailText)
	return nil
}

func runStop(ctx context.Context, svc *app.Service, cmd string, args []string) error {
	id, err := requireArg(args, cmd+" requires <id>")
	if err != nil {
		return err
	}
	return svc.Stop(ctx, id, cmd == "rm")
}

// runRestart stops the session's agent process and resumes it in place: same
// session name and record, with the provider's resume args.
func runRestart(ctx context.Context, svc *app.Service, args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	allowLatest := fs.Bool("allow-latest", false, "allow heuristic resume of the provider's latest session")
	if err := fs.Parse(args); err != nil {
		return err
	}
	id, err := requireArg(fs.Args(), "restart requires <id>")
	if err != nil {
		return err
	}
	return svc.RestartWithOptions(ctx, id, app.ResumeOptions{AllowLatest: *allowLatest})
}

// runNotifyClosed flags the matching record as user-closed. Session hosts
// mark records closed in-process when their agent exits, so uam itself no
// longer shells out to this; it stays for scripts and older tmux hooks that
// still call it. Idempotent; safe to run repeatedly.
func runNotifyClosed(svc *app.Service, args []string) error {
	name, err := requireArg(args, "notify-closed requires <session-name>")
	if err != nil {
		return err
	}
	return svc.NotifyClosed(name)
}

// runKillAll tears down every managed session via the injected killer. uam
// never auto-kills on TUI quit — reboot-recovery of dead sessions is
// intentional — so this explicit command is the only teardown path (F24). The
// killer is idempotent when nothing is running.
func runKillAll(ctx context.Context, kill func(context.Context) error) error {
	if err := kill(ctx); err != nil {
		return fmt.Errorf("kill-all: %w", err)
	}
	fmt.Println("all uam sessions stopped")
	return nil
}

func runLast(ctx context.Context, svc *app.Service, runTUI func(context.Context, tea.Model) error) error {
	// LoadSessions returns the persisted config; the "last" session is the one
	// with the newest persisted LastSeenAt, not the top sorted row (whose order
	// is driven by State/Pinned, not recency) (C1-6).
	_, cfg, err := svc.LoadSessions(ctx)
	if err != nil {
		return err
	}
	id := lastSeenID(cfg)
	if id == "" {
		return errors.New("no sessions")
	}
	return execAttach(ctx, svc, id, runTUI)
}

// lastSeenID returns the id of the record with the maximum persisted LastSeenAt.
// Ties are broken by the larger id so repeated `uam last` invocations are
// deterministic. Returns "" when there are no records (C1-6).
func lastSeenID(cfg store.Config) string {
	var best store.SessionRecord
	for _, rec := range cfg.Sessions {
		if best.ID == "" || rec.LastSeenAt.After(best.LastSeenAt) ||
			(rec.LastSeenAt.Equal(best.LastSeenAt) && rec.ID > best.ID) {
			best = rec
		}
	}
	return best.ID
}

func requireArg(args []string, message string) (string, error) {
	if len(args) == 0 {
		return "", errors.New(message)
	}
	return args[0], nil
}

// NewService wires the app service and supported agent adapters.
func NewService(st *store.Store) *app.Service {
	client := session.NewClient()
	// Let migration distinguish reboot-survivors (live session) from
	// user-stopped sessions (dead) so a v1->v2 upgrade does not auto-resume the
	// latter on attach (F07). The store stays backend-free; this only injects
	// the probe.
	st.SetSessionProbe(func(name string) bool {
		return client.HasSession(context.Background(), name)
	})
	// Build the registry from the single shared adapter list so the CLI service
	// and the TUI can never diverge on which providers exist (F14).
	reg := adapter.NewRegistryWithBackend(client, agents.Default(client))
	return app.NewService(st, reg)
}

// RunTUI launches the Bubble Tea TUI.
func RunTUI(ctx context.Context, model tea.Model) error {
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	if term.IsTerminal(os.Stdout.Fd()) {
		_ = writeTUIExitCleanup(os.Stdout)
	}
	return err
}

const tuiExitCleanup = "\x1b[0m" +
	"\x1b[?1000;1002;1003;1004;1005;1006;1015l" +
	"\x1b[?2004l" +
	"\x1b[?25h" +
	"\x1b[2K\r"

func writeTUIExitCleanup(w io.Writer) error {
	_, err := io.WriteString(w, tuiExitCleanup)
	return err
}

// RunDispatch dispatches a prompt to an agent from CLI args.
func RunDispatch(ctx context.Context, svc *app.Service, args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	safe := fs.Bool("safe", false, "use provider default permission mode")
	alias := fs.String("alias", "", "command alias")
	cwd := fs.String("cwd", "", "working directory")
	profile := fs.String("profile", "", "named profile")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rem := fs.Args()
	if len(rem) < 1 {
		return errors.New("dispatch requires <agent> [#session-name] [prompt]")
	}
	// Go's flag parser stops at the first positional, so any flag placed AFTER
	// <agent> lands in the agent or #name slot instead of taking effect — e.g.
	// `dispatch fake --safe prompt` would silently fold --safe into the prompt.
	// Reject a leftover "-"-prefixed token in those two slots. The prompt proper
	// (rem[2:], or rem[1:] when unnamed) is left untouched so it may contain "--"
	// (C2-3).
	if strings.HasPrefix(rem[0], "-") {
		return fmt.Errorf("dispatch: %q looks like a flag; flags must come before <agent>", rem[0])
	}
	if len(rem) > 1 && strings.HasPrefix(rem[1], "-") {
		return fmt.Errorf("dispatch: %q looks like a flag; flags must come before <agent>", rem[1])
	}
	mode := string(store.ModeYolo)
	if *profile != "" {
		mode = ""
	}
	if *safe {
		mode = string(store.ModeSafe)
	}
	name, prompt := parseNameAndPrompt(rem[1:])
	sess, err := svc.DispatchNamedWithAliasProfile(ctx, rem[0], *alias, name, prompt, *cwd, mode, *profile)
	if err != nil {
		// A non-empty session means the agent launched but the record failed to
		// persist (advisory): report the warning, still emit the id, exit 0 (F03).
		if sess.ID == "" {
			return err
		}
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	fmt.Println(sess.ID)
	return nil
}

func runNew(ctx context.Context, svc *app.Service, runTUI func(context.Context, tea.Model) error) error {
	return runNewWithArgs(ctx, svc, nil, runTUI)
}

func runNewWithArgs(ctx context.Context, svc *app.Service, args []string, runTUI func(context.Context, tea.Model) error) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	profile := fs.String("profile", "", "named profile")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("new: unexpected arguments %q", fs.Args())
	}
	reader := bufio.NewReader(os.Stdin)
	cfg, err := svc.Store.Load()
	if err != nil {
		return fmt.Errorf("load new-session config: %w", err)
	}
	agent := cfg.DefaultAgent
	if *profile != "" {
		if err := store.ValidateProfileName(*profile); err != nil {
			return err
		}
		selected, exists := cfg.Profiles[*profile]
		if !exists {
			return fmt.Errorf("profile %q not found", *profile)
		}
		if err := store.ValidateProfile(selected); err != nil {
			return fmt.Errorf("profile %q: %w", *profile, err)
		}
		if selected.Provider != nil {
			agent = *selected.Provider
		}
	}
	if a := svc.Registry.Default(agent); a != nil {
		agent = a.Name()
	}
	fmt.Printf("provider [%s]: ", agent)
	if line, err := readLine(reader); err != nil {
		return fmt.Errorf("read provider: %w", err)
	} else if line != "" {
		agent = line
	}
	// Re-validate the typed provider: if its CLI is not installed, reconcile it
	// to an enabled one rather than failing the dispatch on an "unavailable"
	// name. Registry.Default returns nil only when nothing is enabled, in which
	// case the typed value is kept and the dispatch surfaces the real error
	// (C2-9).
	if a := svc.Registry.Default(agent); a != nil {
		agent = a.Name()
	}
	fmt.Print("command alias [default]: ")
	alias, err := readLine(reader)
	if err != nil {
		return fmt.Errorf("read command alias: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}
	fmt.Printf("workdir [%s]: ", cwd)
	if line, err := readLine(reader); err != nil {
		return fmt.Errorf("read workdir: %w", err)
	} else if line != "" {
		cwd = line
	}
	fmt.Print("prompt [#session-name prompt, optional]: ")
	// Read the raw prompt line; data and io.EOF can co-arrive on the final read,
	// so use the returned bytes even when err == io.EOF (F54).
	raw, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read prompt: %w", err)
	}
	// Split off only a leading #name token; preserve the prompt remainder
	// verbatim so interior whitespace the user typed is not collapsed (C1-3).
	name, prompt := splitNameFromPrompt(strings.TrimRight(raw, "\r\n"))
	if strings.TrimSpace(prompt) == "" {
		prompt = ""
	}
	mode := string(store.ModeYolo)
	if *profile != "" {
		mode = ""
	}
	sess, err := svc.DispatchNamedWithAliasProfile(ctx, agent, alias, name, prompt, cwd, mode, *profile)
	if err != nil {
		if sess.ID == "" {
			return err
		}
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	if sess.ID == "" {
		return errors.New("new: dispatched session has empty id")
	}
	return execAttach(ctx, svc, sess.ID, runTUI)
}

// readLine reads one trimmed input line from r. A trailing io.EOF that arrives
// with the line's bytes is not an error (the line is still returned); any other
// read error is surfaced (F54).
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// splitNameFromPrompt peels a single leading #name token off the front of a
// prompt and returns the remainder verbatim. It does not tokenize the prompt, so
// interior whitespace survives (C1-3). The leading "#name " separator (exactly
// one space) is consumed; everything after it is kept byte-for-byte.
func splitNameFromPrompt(line string) (name, prompt string) {
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "#") {
		return "", line
	}
	rest := trimmed[1:]
	if i := strings.IndexAny(rest, " \t"); i >= 0 {
		return rest[:i], rest[i+1:]
	}
	return rest, ""
}

func parseNameAndPrompt(parts []string) (string, string) {
	if len(parts) == 0 {
		return "", ""
	}
	return splitNameFromPrompt(strings.Join(parts, " "))
}

func execAttach(ctx context.Context, svc *app.Service, id string, runTUI func(context.Context, tea.Model) error) error {
	return execAttachWithOptions(ctx, svc, id, app.ResumeOptions{}, runTUI)
}

func execAttachWithOptions(ctx context.Context, svc *app.Service, id string, opts app.ResumeOptions, runTUI func(context.Context, tea.Model) error) error {
	spec, err := svc.AttachSpecWithOptions(ctx, id, opts)
	if err != nil {
		return err
	}
	if len(spec.Argv) == 0 {
		return errors.New("empty attach command")
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...) // #nosec G204 -- attach argv is generated by trusted agent adapters, no shell expansion.
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = attachEnvironment(os.Environ(), spec)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "uam: session exited: %v\n", err)
	}
	return runTUI(ctx, app.NewWithDeps(svc.Store, svc.Registry))
}

func attachEnvironment(base []string, spec adapter.AttachSpec) []string {
	environment := make([]string, 0, len(base)+6)
	for _, assignment := range base {
		name, _, _ := strings.Cut(assignment, "=")
		if name == session.AttachQuietEnv || name == session.AttachSelectedProfileEnv || name == session.AttachEffectiveProfileEnv ||
			name == session.AttachPolicyMouseEnv || name == session.AttachPolicyPrefixEnv || name == session.AttachPolicyBackDetachEnv {
			continue
		}
		environment = append(environment, assignment)
	}
	environment = append(environment, session.AttachQuietEnv+"=1")
	if spec.Profile.Selected != "" {
		environment = append(environment, session.AttachSelectedProfileEnv+"="+spec.Profile.Selected)
	}
	if spec.Profile.Effective != "" {
		environment = append(environment, session.AttachEffectiveProfileEnv+"="+spec.Profile.Effective)
	}
	if spec.Profile.Mouse != "" || spec.Profile.ControlPrefix != "" {
		environment = append(environment,
			session.AttachPolicyMouseEnv+"="+spec.Profile.Mouse,
			session.AttachPolicyPrefixEnv+"="+spec.Profile.ControlPrefix,
			session.AttachPolicyBackDetachEnv+"="+boolTransport(spec.Profile.BackDetach),
		)
	}
	return environment
}

func boolTransport(value bool) string {
	if value {
		return "1"
	}
	return "0"
}
