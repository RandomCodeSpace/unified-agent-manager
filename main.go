package main

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
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
)

func main() {
	flag.Usage = usage
	flag.Parse()

	closer, err := log.Init()
	if err != nil {
		fmt.Fprintf(os.Stderr, "uam: failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = closer.Close() }()

	ctx := context.Background()
	if err := run(ctx, flag.Args()); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("run exited with error", "err", err)
		fmt.Fprintf(os.Stderr, "uam: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
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
}

func run(ctx context.Context, args []string) error {
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		return err
	}
	svc := newService(st)
	if len(args) == 0 {
		return runTUIFn(ctx, app.NewWithDeps(st, svc.Registry))
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return nil
	case "new":
		return runNew(ctx, svc)
	case "version", "--version":
		fmt.Println(version.String())
		return nil
	case "dispatch":
		return runDispatch(ctx, svc, args[1:])
	case "ls", "list":
		fs := flag.NewFlagSet("ls", flag.ContinueOnError)
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return svc.PrintList(ctx, *asJSON)
	case "peek":
		if len(args) < 2 {
			return errors.New("peek requires <id>")
		}
		p, err := svc.Peek(ctx, args[1])
		if err != nil {
			return err
		}
		fmt.Print(p.TailText)
		return nil
	case "stop":
		if len(args) < 2 {
			return errors.New("stop requires <id>")
		}
		return svc.Stop(ctx, args[1], false)
	case "rm":
		if len(args) < 2 {
			return errors.New("rm requires <id>")
		}
		return svc.Stop(ctx, args[1], true)
	case "attach":
		if len(args) < 2 {
			return errors.New("attach requires <id>")
		}
		return execAttach(ctx, svc, args[1])
	case "last":
		sessions, _, err := svc.LoadSessions(ctx)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			return errors.New("no sessions")
		}
		return execAttach(ctx, svc, sessions[0].ID)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func newService(st *store.Store) *app.Service {
	client := tmux.New("uam")
	reg := adapter.NewRegistry([]adapter.AgentAdapter{claude.New(client), codex.New(client), copilot.New(client), opencode.New(client)})
	return app.NewService(st, reg)
}

func runTUI(ctx context.Context, model tea.Model) error {
	p := tea.NewProgram(model, tea.WithAltScreen(), tea.WithContext(ctx))
	_, err := p.Run()
	return err
}

var runTUIFn = runTUI

func runDispatch(ctx context.Context, svc *app.Service, args []string) error {
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
	mode := string(store.ModeYolo)
	if *safe {
		mode = string(store.ModeSafe)
	}
	name, prompt := parseNameAndPrompt(rem[1:])
	sess, err := svc.DispatchNamed(ctx, rem[0], name, prompt, *cwd, mode)
	if err != nil {
		return err
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
		return err
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

func execAttach(ctx context.Context, svc *app.Service, id string) error {
	spec, err := svc.AttachSpec(ctx, id)
	if err != nil {
		return err
	}
	if len(spec.Argv) == 0 {
		return errors.New("empty attach command")
	}
	cmd := exec.CommandContext(ctx, spec.Argv[0], spec.Argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "uam: session exited: %v\n", err)
	}
	return runTUIFn(ctx, app.NewWithDeps(svc.Store, svc.Registry))
}
