package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const glabTTL = 10 * time.Minute
const glabTimeout = 20 * time.Second
const glabOutputLimit = 1 << 20

type glabGate struct{ Host, Path, Dir, executable string }
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
	if !e.at.IsZero() && time.Since(e.at) < glabTTL {
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
	gate, ok := glabOrigin(ctx, dir)
	if ok {
		ok = g.authenticated(ctx, gate)
	}
	g.mu.Lock()
	g.dirs[dir] = glabCachedGate{gate: gate, ok: ok, at: time.Now()}
	g.mu.Unlock()
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
	raw := strings.TrimSpace(remote)
	if !strings.Contains(raw, "://") {
		// Git's scp spelling requires an explicit user and host.
		_, hostPath, ok := strings.Cut(raw, "@")
		host, path, colon := strings.Cut(hostPath, ":")
		if !ok || !colon {
			return glabGate{}, false
		}
		raw = "ssh://" + host + "/" + path
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh") || u.Hostname() == "" {
		return glabGate{}, false
	}
	host := strings.ToLower(u.Hostname())
	path := strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git")
	if host == "github.com" || !strings.Contains(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
		return glabGate{}, false
	}
	return glabGate{Host: host, Path: path, Dir: dir, executable: bin}, true
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
		if time.Since(e.at) < glabTTL {
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

// glabRun has only the two verbs required by the governor. Authentication output
// is discarded; lint output is bounded before it can enter a tool response.
func glabRun(ctx context.Context, gate glabGate, timeout time.Duration, verb string, args ...string) (string, error) {
	var argv []string
	switch verb {
	case "auth status":
		if len(args) != 0 {
			return "", errors.New("unexpected auth arguments")
		}
		argv = []string{"auth", "status", "--hostname", gate.Host}
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
	cmd := exec.CommandContext(ctx, bin, args...) // #nosec G204 -- resolved glab or fixed git, internal command argv, no shell.
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

func glabGit(ctx context.Context, dir string, args ...string) (string, error) {
	return glabExec(ctx, glabTimeout, dir, "git", false, args...)
}
