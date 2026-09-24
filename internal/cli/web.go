package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/x/term"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/copilot"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/version"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/web"
)

// webProviders lists the structured provider integrations the web service
// drives. A provider whose Check fails is shown as unavailable, not fatal.
// Only Copilot is offered: a provider is added once it supports the web
// features the same way (docs/adr/0004-web-interface.md).
func webProviders() []agentapi.Provider {
	return []agentapi.Provider{copilot.NewWebProvider()}
}

type originList []string

func (o *originList) String() string     { return strings.Join(*o, ",") }
func (o *originList) Set(v string) error { *o = append(*o, v); return nil }

// noAuthNotice is printed wherever the service's settings are shown while
// authentication is disabled.
const noAuthNotice = "Authentication: disabled — anyone who can reach this service can use it"

// logHeadersNotice is printed wherever the service's settings are shown while
// --log-headers is on.
const logHeadersNotice = "Header logging: on — every request's headers go to the uam log (credential headers redacted)"

// webOptions are the settings shared by `uam web` and `uam __web`.
type webOptions struct {
	listen     string
	origins    []string
	noAuth     bool
	logHeaders bool
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
	if o.logHeaders {
		args = append(args, "--log-headers")
	}
	return args
}

// webFlags parses the flags shared by `uam web` and `uam __web`.
func webFlags(name string, args []string) (webOptions, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	listen := fs.String("listen", web.DefaultListen, "IP address and port to serve on; beyond loopback, other machines can reach it")
	var origins originList
	fs.Var(&origins, "public-origin", "origin of a same-host reverse proxy, e.g. https://host (repeatable)")
	noAuth := fs.Bool("no-auth", false, "disable authentication: anyone who can reach the service can use it")
	logHeaders := fs.Bool("log-headers", false, "debug: log each request's method, path, remote address and headers (credential headers redacted) to the uam log")
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
	return webOptions{listen: addr, origins: normalized, noAuth: *noAuth, logHeaders: *logHeaders}, nil
}

// runWeb is `uam web [--listen addr] [--public-origin url]... [--no-auth]
// [--log-headers]`, `uam web status [--json]`, `uam web stop` and
// `uam web token set`.
func runWeb(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runWebStatus(args[1:])
		case "stop":
			return runWebStop(ctx, args[1:])
		case "token":
			if len(args) < 2 || args[1] != "set" {
				// Never echo the arguments: they may be a token.
				return errors.New("usage: uam web token set (reads the token from stdin)")
			}
			return runWebTokenSet(args[2:])
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
	host, port, err := net.SplitHostPort(st.LocalAddr())
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
	if st.LogHeaders {
		fmt.Printf("  %s\n", logHeadersNotice)
	}
	printExposure(st)
	fmt.Println()
	fmt.Println("From another computer, forward the port over SSH (for example in PowerShell):")
	fmt.Printf("  ssh -N -L 127.0.0.1:%s:%s <user>@<host>\n", port, net.JoinHostPort(host, port))
	if st.NoAuth {
		fmt.Printf("then open http://127.0.0.1:%s/.\n", port)
		return
	}
	fmt.Printf("then open http://127.0.0.1:%s/ and sign in with the access token.\n", port)
}

// printExposure warns wherever the service's settings are shown while it
// listens beyond loopback, with one more line when --no-auth is in use.
func printExposure(st web.DaemonState) {
	if !web.BeyondLoopback(st.Listen) {
		return
	}
	exposed := "Warning: listening on " + st.Listen + ", so other machines can reach this service"
	if !st.NoAuth {
		fmt.Printf("  %s; sign-in is required (--no-auth is not in use)\n", exposed)
		return
	}
	fmt.Printf("  %s\n", exposed)
	fmt.Printf("  Warning: --no-auth is in use: anyone who can reach %s can run agents on this host with your credentials\n", st.Listen)
}

type webStatus struct {
	Running       bool      `json:"running"`
	PID           int       `json:"pid,omitempty"`
	URL           string    `json:"url,omitempty"`
	Listen        string    `json:"listen,omitempty"`
	PublicOrigins []string  `json:"public_origins,omitempty"`
	NoAuth        *bool     `json:"no_auth,omitempty"`
	LogHeaders    bool      `json:"log_headers,omitempty"`
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
		status = webStatus{Running: true, PID: st.PID, URL: st.URL(), Listen: st.Listen, PublicOrigins: st.PublicOrigins, NoAuth: &st.NoAuth, LogHeaders: st.LogHeaders, Version: st.Version, StartedAt: st.StartedAt}
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
	if st.LogHeaders {
		fmt.Printf("  %s\n", logHeadersNotice)
	}
	printExposure(st)
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

// runWebTokenSet is `uam web token set`: it replaces the access token with
// one read from stdin and never prints it.
func runWebTokenSet(args []string) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(os.Stderr, `usage: uam web token set

Sets the access token browsers sign in with. The token is read from stdin, up
to the first newline, with surrounding whitespace trimmed; at a terminal the
prompt does not echo it. It must be 24 to 256 printable ASCII characters with
no whitespace. There is no argument, flag or environment variable for it:
arguments show up in ps and shell history, and the environment is inherited
by the agents the service starts.

It refuses while uam web is running: run uam web stop first, then uam web
again. The new token signs out every browser.`)
		return nil
	}
	if len(args) > 0 {
		// Never echo the arguments: they may be a token.
		return errors.New("web token set takes no arguments or flags; it reads the token from stdin")
	}
	// A running service keeps the token it started with; replacing the file
	// under it would leave no copy of the token it accepts.
	if st, running := web.ReadRunning(session.DefaultDir()); running {
		return fmt.Errorf("uam web is running (pid %d); run uam web stop first, then set the token and start uam web again", st.PID)
	}
	token, err := readTokenInput(os.Stdin)
	if err != nil {
		return err
	}
	path := web.TokenPath()
	if err := web.SetToken(path, token); err != nil {
		return err
	}
	fmt.Printf("Access token set in %s\n", path)
	return nil
}

// readTokenInput reads one line from in, without echo at a terminal.
func readTokenInput(in *os.File) (string, error) {
	if term.IsTerminal(in.Fd()) {
		fmt.Fprint(os.Stderr, "New access token (not shown): ")
		line, err := term.ReadPassword(in.Fd())
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read access token: %w", err)
		}
		return strings.TrimSpace(string(line)), nil
	}
	// Anything longer than a token is refused anyway; bound the read.
	line, err := bufio.NewReader(io.LimitReader(in, 4<<10)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read access token: %w", err)
	}
	return strings.TrimSpace(line), nil
}

// runWebDaemon is the internal `uam __web` service entry point.
func runWebDaemon(args []string) error {
	opts, err := webFlags("__web", args)
	if err != nil {
		return err
	}
	return web.RunDaemon(web.DaemonConfig{Listen: opts.listen, PublicOrigins: opts.origins, NoAuth: opts.noAuth, LogHeaders: opts.logHeaders, Providers: webProviders(), Version: version.String()})
}
