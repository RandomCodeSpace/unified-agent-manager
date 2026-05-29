package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/claude"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/codex"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/hermes"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

// Main is the process entrypoint shared by the root compatibility command and cmd/uam.
func Main() {
	flag.Usage = Usage
	flag.Parse()

	closer, err := log.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uam: failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = closer.Close() }()

	ctx := context.Background()
	if err := Run(ctx, flag.Args()); err != nil && !errors.Is(err, context.Canceled) {
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
	fmt.Fprintln(os.Stderr, "  uam dispatch [--safe] <agent> [#session-name] [prompt]")
	fmt.Fprintln(os.Stderr, "  uam attach <name-or-id>")
	fmt.Fprintln(os.Stderr, "  uam last")
	fmt.Fprintln(os.Stderr, "  uam version")
	fmt.Fprintln(os.Stderr, "  uam ls [--json]")
	fmt.Fprintln(os.Stderr, "  uam peek <id>")
	fmt.Fprintln(os.Stderr, "  uam stop <id>")
	fmt.Fprintln(os.Stderr, "  uam rm <id>")
	fmt.Fprintln(os.Stderr, "  uam notify-closed <tmux-session>   (internal: tmux session-closed hook)")
}

// Run executes the CLI using the default Bubble Tea TUI runner.
func Run(ctx context.Context, args []string) error {
	return RunWithTUI(ctx, args, RunTUI)
}

// RunWithTUI executes the CLI with an injectable TUI runner for tests.
func RunWithTUI(ctx context.Context, args []string, runTUI func(context.Context, tea.Model) error) error {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	svc := NewService(st)
	if len(args) == 0 {
		return runTUI(ctx, app.NewWithDeps(st, svc.Registry))
	}
	return runCommand(ctx, svc, args, runTUI)
}

func runCommand(ctx context.Context, svc *app.Service, args []string, runTUI func(context.Context, tea.Model) error) error {
	switch args[0] {
	case "-h", "--help", "help":
		Usage()
		return nil
	case "new":
		return runNew(ctx, svc)
	case "version", "--version":
		fmt.Println(version.String())
		return nil
	case "dispatch":
		return RunDispatch(ctx, svc, args[1:])
	case "ls", "list":
		return runList(ctx, svc, args[1:])
	case "peek":
		return runPeek(ctx, svc, args[1:])
	case "stop", "rm":
		return runStop(ctx, svc, args[0], args[1:])
	case "notify-closed":
		return runNotifyClosed(svc, args[1:])
	case "attach":
		id, err := requireArg(args[1:], "attach requires <id>")
		if err != nil {
			return err
		}
		return execAttach(ctx, svc, id, runTUI)
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

// runNotifyClosed is invoked from tmux's session-closed hook via:
//
//	tmux set-hook -g session-closed 'run-shell "<uam-bin> notify-closed #{hook_session_name}"'
//
// It flags the matching record as user-closed so exit-in-session and external
// `tmux kill-session` calls aren't mistaken for reboot-killed sessions on
// the next uam launch. Idempotent; safe to run repeatedly.
func runNotifyClosed(svc *app.Service, args []string) error {
	tmuxName, err := requireArg(args, "notify-closed requires <tmux-session>")
	if err != nil {
		return err
	}
	return svc.NotifyClosed(tmuxName)
}

func runLast(ctx context.Context, svc *app.Service, runTUI func(context.Context, tea.Model) error) error {
	sessions, _, err := svc.LoadSessions(ctx)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return errors.New("no sessions")
	}
	return execAttach(ctx, svc, sessions[0].ID, runTUI)
}

func requireArg(args []string, message string) (string, error) {
	if len(args) == 0 {
		return "", errors.New(message)
	}
	return args[0], nil
}

// NewService wires the app service and supported agent adapters.
func NewService(st *store.Store) *app.Service {
	client := tmux.New("uam")
	// Let migration distinguish reboot-survivors (live pane) from user-stopped
	// sessions (dead pane) so a v1->v2 upgrade does not auto-resume the latter
	// on attach (F07). The store stays tmux-free; this only injects the probe.
	st.SetSessionProbe(func(name string) bool {
		return client.HasSession(context.Background(), name)
	})
	reg := adapter.NewRegistry([]adapter.AgentAdapter{claude.New(client), codex.New(client), copilot.New(client), hermes.New(client), opencode.New(client)})
	return app.NewService(st, reg)
}

// RunTUI launches the Bubble Tea TUI.
func RunTUI(ctx context.Context, model tea.Model) error {
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

// RunDispatch dispatches a prompt to an agent from CLI args.
func RunDispatch(ctx context.Context, svc *app.Service, args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	safe := fs.Bool("safe", false, "use provider default permission mode")
	cwd := fs.String("cwd", "", "working directory")
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
	if *safe {
		mode = string(store.ModeSafe)
	}
	name, prompt := parseNameAndPrompt(rem[1:])
	sess, err := svc.DispatchNamed(ctx, rem[0], name, prompt, *cwd, mode)
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

func runNew(ctx context.Context, svc *app.Service) error {
	reader := bufio.NewReader(os.Stdin)
	cfg, _ := svc.Store.Load()
	agent := cfg.DefaultAgent
	if a := svc.Registry.Default(agent); a != nil {
		agent = a.Name()
	}
	fmt.Printf("provider [%s]: ", agent)
	if line, _ := reader.ReadString('\n'); strings.TrimSpace(line) != "" {
		agent = strings.TrimSpace(line)
	}
	cwd, _ := os.Getwd()
	fmt.Printf("workdir [%s]: ", cwd)
	if line, _ := reader.ReadString('\n'); strings.TrimSpace(line) != "" {
		cwd = strings.TrimSpace(line)
	}
	fmt.Print("prompt [#session-name prompt, optional]: ")
	prompt, _ := reader.ReadString('\n')
	prompt = strings.TrimSpace(prompt)
	name, prompt := parseNameAndPrompt(strings.Fields(prompt))
	sess, err := svc.DispatchNamed(ctx, agent, name, prompt, cwd, string(store.ModeYolo))
	if err != nil {
		if sess.ID == "" {
			return err
		}
		fmt.Fprintln(os.Stderr, "warning:", err)
	}
	fmt.Printf("dispatched %s (%s)\n", sess.ID, sess.TmuxSession)
	return nil
}

func parseNameAndPrompt(parts []string) (string, string) {
	if len(parts) == 0 {
		return "", ""
	}
	if strings.HasPrefix(parts[0], "#") {
		return strings.TrimPrefix(parts[0], "#"), strings.Join(parts[1:], " ")
	}
	return "", strings.Join(parts, " ")
}

func execAttach(ctx context.Context, svc *app.Service, id string, runTUI func(context.Context, tea.Model) error) error {
	spec, err := svc.AttachSpec(ctx, id)
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
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "uam: session exited: %v\n", err)
	}
	return runTUI(ctx, app.NewWithDeps(svc.Store, svc.Registry))
}
