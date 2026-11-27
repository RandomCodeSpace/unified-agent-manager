package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const glabTTL = 10 * time.Minute

// glabNegativeTTL is shorter so signing in to glab takes effect quickly.
const glabNegativeTTL = time.Minute
const glabTimeout = 20 * time.Second
const glabOutputLimit = 1 << 20

// glabGate is a checkout's GitLab origin. Web is the project's web address
// glab read (withWeb); empty until read or when glab cannot read it.
type glabGate struct{ Host, Path, Dir, Web, remote, executable string }

// webURL is the project's web address: glab's when it read one, else https
// on the remote's host.
func (g glabGate) webURL() string {
	if g.Web != "" {
		return g.Web
	}
	return (&url.URL{Scheme: "https", Host: g.Host, Path: "/" + g.Path}).String()
}

type glabWeb struct {
	web string
	at  time.Time
}
type glabCachedGate struct {
	gate        glabGate
	at          time.Time
	ok, loading bool
}
type glabAuth struct {
	at   time.Time
	ok   bool
	done chan struct{}
}

// glabGovernor owns its cache independently of the Manager. Cold/expired gates
// are disabled while one background refresh resolves the origin and auth.
type glabGovernor struct {
	mu    sync.Mutex
	dirs  map[string]glabCachedGate
	hosts map[string]*glabAuth
	// webs holds each origin remote URL's project web address (withWeb).
	webs map[string]glabWeb
}

func (g *glabGovernor) gate(ctx context.Context, dir string) (glabGate, bool) {
	if dir == "" {
		return glabGate{}, false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.dirs[dir]
	if !e.at.IsZero() && time.Since(e.at) < glabCacheTTL(e.ok) {
		return e.gate, e.ok
	}
	if !e.loading {
		if g.dirs == nil {
			g.dirs = make(map[string]glabCachedGate)
		}
		e.loading = true
		g.dirs[dir] = e
		go g.refresh(ctx, dir)
	}
	return glabGate{}, false
}

func (g *glabGovernor) refresh(ctx context.Context, dir string) {
	defer log.Recover("glab refresh", &g.mu)
	gate, ok := glabOrigin(ctx, dir)
	if ok {
		ok = g.authenticated(ctx, gate)
	}
	g.mu.Lock()
	g.dirs[dir] = glabCachedGate{gate: gate, ok: ok, at: time.Now()}
	g.mu.Unlock()
}

func glabCacheTTL(ok bool) time.Duration {
	if ok {
		return glabTTL
	}
	return glabNegativeTTL
}

func glabOrigin(ctx context.Context, dir string) (glabGate, bool) {
	bin, err := exec.LookPath("glab")
	if err != nil {
		return glabGate{}, false
	}
	remote, err := glabGit(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return glabGate{}, false
	}
	host, path, ok := parseRemote(remote)
	if !ok || host == "github.com" {
		return glabGate{}, false
	}
	return glabGate{Host: host, Path: path, Dir: dir, remote: strings.TrimSpace(remote), executable: bin}, true
}

// withWeb sets gate.Web to the project's web address, read with glab once per
// origin remote URL, so a new remote reads it again. A failed read, or glab
// signed out of the host, leaves Web empty and is tried again after
// glabNegativeTTL.
func (g *glabGovernor) withWeb(ctx context.Context, gate glabGate) glabGate {
	g.mu.Lock()
	e, cached := g.webs[gate.remote]
	g.mu.Unlock()
	if cached && (e.web != "" || time.Since(e.at) < glabNegativeTTL) {
		gate.Web = e.web
		return gate
	}
	var web string
	if g.authenticated(ctx, gate) {
		if out, err := glabRun(ctx, gate, glabTimeout, "repo view"); err == nil {
			web = glabWebURL(gate, out)
		}
	}
	if ctx.Err() != nil {
		return gate
	}
	g.mu.Lock()
	if g.webs == nil {
		g.webs = make(map[string]glabWeb)
	}
	g.webs[gate.remote] = glabWeb{web: web, at: time.Now()}
	g.mu.Unlock()
	gate.Web = web
	return gate
}

// glabWebURL reads web_url from `glab repo view -F json` output. It must be
// http(s) with a host, no user, query or fragment, and end with the origin's
// project path, so links built from it name the same project; else "".
func glabWebURL(gate glabGate, out string) string {
	start := strings.IndexByte(out, '{')
	if start < 0 {
		return ""
	}
	var project struct {
		WebURL string `json:"web_url"`
	}
	if json.NewDecoder(strings.NewReader(out[start:])).Decode(&project) != nil {
		return ""
	}
	u, err := url.Parse(project.WebURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return ""
	}
	path := strings.TrimSuffix(u.Path, "/")
	suffix := "/" + gate.Path
	if len(path) < len(suffix) || !strings.EqualFold(path[len(path)-len(suffix):], suffix) {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host), Path: path[:len(path)-len(suffix)] + suffix}).String()
}

// parseRemote reads a git remote URL (https, http, ssh or git's scp
// spelling) as its lower-case host and owner/repository path, without any
// user, password or port.
func parseRemote(remote string) (host, path string, ok bool) {
	raw := strings.TrimSpace(remote)
	if !strings.Contains(raw, "://") {
		// Git's scp spelling requires an explicit user and host.
		_, hostPath, ok := strings.Cut(raw, "@")
		host, path, colon := strings.Cut(hostPath, ":")
		if !ok || !colon {
			return "", "", false
		}
		raw = "ssh://" + host + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh") || u.Hostname() == "" {
		return "", "", false
	}
	host = strings.ToLower(u.Hostname())
	path = strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if !strings.Contains(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
		return "", "", false
	}
	return host, path, true
}

func (g *glabGovernor) authenticated(ctx context.Context, gate glabGate) bool {
	g.mu.Lock()
	if g.hosts == nil {
		g.hosts = make(map[string]*glabAuth)
	}
	if e := g.hosts[gate.Host]; e != nil {
		if e.done != nil {
			done := e.done
			g.mu.Unlock()
			select {
			case <-done:
				return g.authenticated(ctx, gate)
			case <-ctx.Done():
				return false
			}
		}
		if time.Since(e.at) < glabCacheTTL(e.ok) {
			g.mu.Unlock()
			return e.ok
		}
	}
	e := &glabAuth{done: make(chan struct{})}
	g.hosts[gate.Host] = e
	g.mu.Unlock()
	_, err := glabRun(ctx, gate, glabTimeout, "auth status")
	g.mu.Lock()
	e.ok, e.at = err == nil, time.Now()
	close(e.done)
	e.done = nil
	g.mu.Unlock()
	return err == nil
}

// glabRun has only the verbs required by the governor. Authentication output
// is discarded; lint output is bounded before it can enter a tool response.
func glabRun(ctx context.Context, gate glabGate, timeout time.Duration, verb string, args ...string) (string, error) {
	var argv []string
	switch verb {
	case "auth status":
		if len(args) != 0 {
			return "", errors.New("unexpected auth arguments")
		}
		argv = []string{"auth", "status", "--hostname", gate.Host}
	case "repo view":
		if len(args) != 0 {
			return "", errors.New("unexpected repo arguments")
		}
		argv = []string{"repo", "view", "-F", "json"}
	case "ci lint":
		argv = append([]string{"ci", "lint", "-R", "https://" + gate.Host + "/" + gate.Path}, args...)
	default:
		return "", errors.New("unsupported glab verb")
	}
	return glabExec(ctx, timeout, gate.Dir, gate.executable, verb == "auth status", argv...)
}

type glabOutput struct {
	buf      bytes.Buffer
	exceeded bool
}

func (b *glabOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := glabOutputLimit - b.buf.Len()
	if n > remaining {
		b.exceeded = true
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

func glabExec(ctx context.Context, timeout time.Duration, dir, bin string, discard bool, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 G702 -- resolved glab, structured argv; API data uses escaped endpoints and literal --raw-field values, no shell.
	cmd.Dir, cmd.Env = dir, append(os.Environ(), "NO_COLOR=1")
	cmd.WaitDelay = time.Second
	var out glabOutput
	if discard {
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	} else {
		cmd.Stdout, cmd.Stderr = &out, &out
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if out.exceeded {
		err = errors.New("glab output limit exceeded")
	}
	return out.buf.String(), err
}

// glabGit runs a read-only git command through runGit, so it gets the same
// resolved binary and read-only flags as every other git call. A non-zero
// exit is an error.
func glabGit(ctx context.Context, dir string, args ...string) (string, error) {
	git, err := execpath.Resolve("git")
	if err != nil {
		return "", err
	}
	out, code, stderr, err := runGit(ctx, git, dir, glabOutputLimit+1, args...)
	switch {
	case err != nil:
		return "", err
	case len(out) > glabOutputLimit:
		return "", errors.New("git output limit exceeded")
	case code != 0:
		return string(out), fmt.Errorf("git %s exited %d: %s", args[0], code, gitMessage(stderr))
	}
	return string(out), nil
}
