package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/opencode"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/web"
)

// webProviders lists the structured provider integrations the web service
// drives. A provider whose Check fails is shown as unavailable, not fatal.
func webProviders() []agentapi.Provider {
	return []agentapi.Provider{copilot.NewWebProvider(), opencode.NewWebProvider()}
}

type originList []string

func (o *originList) String() string     { return strings.Join(*o, ",") }
func (o *originList) Set(v string) error { *o = append(*o, v); return nil }

// noAuthNotice is printed wherever the service's settings are shown while
// authentication is disabled.
const noAuthNotice = "Authentication: disabled — anyone who can reach this service can use it"

// webOptions are the settings shared by `uam web` and `uam __web`.
type webOptions struct {
	listen  string
	origins []string
	noAuth  bool
}

// args renders o as `uam __web` arguments.
func (o webOptions) args() []string {
	args := []string{"--listen", o.listen}
	for _, origin := range o.origins {
		args = append(args, "--public-origin", origin)
	}
	if o.noAuth {
		args = append(args, "--no-auth")
	}
	return args
}

// webFlags parses the flags shared by `uam web` and `uam __web`.
func webFlags(name string, args []string) (webOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	listen := fs.String("listen", web.DefaultListen, "loopback address to serve on")
	var origins originList
	fs.Var(&origins, "public-origin", "origin of a same-host reverse proxy, e.g. https://host (repeatable)")
	noAuth := fs.Bool("no-auth", false, "disable authentication: anyone who can reach the service can use it")
	if err := fs.Parse(args); err != nil {
		return webOptions{}, err
	}
	if fs.NArg() > 0 {
		return webOptions{}, fmt.Errorf("%s: unexpected arguments %q", name, fs.Args())
	}
	addr, err := web.ValidateListen(*listen)
	if err != nil {
		return webOptions{}, err
	}
	normalized := make([]string, 0, len(origins))
	for _, o := range origins {
		n, err := web.NormalizePublicOrigin(o)
		if err != nil {
			return webOptions{}, err
		}
		normalized = append(normalized, n)
	}
	return webOptions{listen: addr, origins: normalized, noAuth: *noAuth}, nil
}

// runWeb is `uam web [--listen addr] [--public-origin url]... [--no-auth]`,
// `uam web status [--json]` and `uam web stop`.
func runWeb(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runWebStatus(args[1:])
		case "stop":
			return runWebStop(ctx, args[1:])
		}
	}
	opts, err := webFlags("web", args)
	if err != nil {
		return ignoreHelp(err)
	}
	token, err := web.LoadOrCreateToken(web.TokenPath())
	if err != nil {
		return err
	}
	dir := session.DefaultDir()
	if st, running := web.ReadRunning(dir); running {
		fmt.Printf("uam web is already running (pid %d); stop it first to change its settings\n", st.PID)
		printWebAccess(st, token)
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve uam binary: %w", err)
	}
	if err := execpath.ValidateAbsoluteExecutable(exe); err != nil {
		return fmt.Errorf("invalid uam binary for the web service: %w", err)
	}
	if err := web.Spawn(ctx, exe, opts.args()); err != nil {
		return err
	}
	st, running := web.ReadRunning(dir)
	if !running {
		return errors.New("uam web reported ready but is not running")
	}
	fmt.Printf("uam web started (pid %d)\n", st.PID)
	printWebAccess(st, token)
	return nil
}

func printWebAccess(st web.DaemonState, token string) {
	host, port, err := net.SplitHostPort(st.Listen)
	if err != nil {
		host, port = "127.0.0.1", "8260"
	}
	fmt.Printf("  URL:           %s\n", st.URL())
	fmt.Printf("  Listen:        %s\n", st.Listen)
	if st.NoAuth {
		fmt.Printf("  %s\n", noAuthNotice)
	} else {
		fmt.Printf("  Access token:  %s\n", token)
	}
	for _, o := range st.PublicOrigins {
		fmt.Printf("  Public origin: %s\n", o)
	}
	fmt.Println()
	fmt.Println("From another computer, forward the port over SSH (for example in PowerShell):")
	fmt.Printf("  ssh -N -L 127.0.0.1:%s:%s <user>@<host>\n", port, net.JoinHostPort(host, port))
	if st.NoAuth {
		fmt.Printf("then open http://127.0.0.1:%s/.\n", port)
		return
	}
	fmt.Printf("then open http://127.0.0.1:%s/ and sign in with the access token.\n", port)
}

type webStatus struct {
	Running       bool      `json:"running"`
	PID           int       `json:"pid,omitempty"`
	URL           string    `json:"url,omitempty"`
	Listen        string    `json:"listen,omitempty"`
	PublicOrigins []string  `json:"public_origins,omitempty"`
	NoAuth        *bool     `json:"no_auth,omitempty"`
	Version       string    `json:"version,omitempty"`
	StartedAt     time.Time `json:"started_at,omitzero"`
}

func runWebStatus(args []string) error {
	fs := flag.NewFlagSet("web status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return ignoreHelp(err)
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("web status: unexpected arguments %q", fs.Args())
	}
	st, running := web.ReadRunning(session.DefaultDir())
	status := webStatus{Running: running}
	if running {
		status = webStatus{Running: true, PID: st.PID, URL: st.URL(), Listen: st.Listen, PublicOrigins: st.PublicOrigins, NoAuth: &st.NoAuth, Version: st.Version, StartedAt: st.StartedAt}
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(status)
	}
	if !running {
		fmt.Println("uam web is not running")
		return nil
	}
	fmt.Printf("uam web is running (pid %d)\n", st.PID)
	fmt.Printf("  URL:      %s\n", st.URL())
	fmt.Printf("  Listen:   %s\n", st.Listen)
	for _, o := range st.PublicOrigins {
		fmt.Printf("  Public origin: %s\n", o)
	}
	if st.NoAuth {
		fmt.Printf("  %s\n", noAuthNotice)
	}
	fmt.Printf("  Version:  %s\n", st.Version)
	fmt.Printf("  Started:  %s\n", st.StartedAt.Local().Format(time.RFC3339))
	return nil
}

func runWebStop(ctx context.Context, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("web stop: unexpected arguments %q", args)
	}
	stopped, err := web.Stop(ctx, session.DefaultDir())
	if err != nil {
		return err
	}
	if !stopped {
		fmt.Println("uam web is not running")
		return nil
	}
	fmt.Println("uam web stopped")
	return nil
}

// runWebDaemon is the internal `uam __web` service entry point.
func runWebDaemon(args []string) error {
	opts, err := webFlags("__web", args)
	if err != nil {
		return err
	}
	return web.RunDaemon(web.DaemonConfig{Listen: opts.listen, PublicOrigins: opts.origins, NoAuth: opts.noAuth, Providers: webProviders(), Version: version.String()})
}
