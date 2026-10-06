package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	// cliCommandTimeout bounds each version, release and npm folder read.
	cliCommandTimeout = 30 * time.Second
	// cliUpdateTimeout bounds a whole update, both installs included.
	cliUpdateTimeout = 10 * time.Minute
	copilotPackage   = "@github/copilot"
)

var (
	// installedVersionExpr finds the version `copilot --version` prints,
	// which may be a prerelease such as 1.0.93-1.
	installedVersionExpr = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?`)
	// releaseExpr is a stable release, the only kind offered.
	releaseExpr = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// probeClient builds the SDK client an update starts on a staged release,
// the way the runtime's is built: the SDK's protocol handshake is what says
// whether it can drive that release. Tests replace it.
var probeClient = func(path string) sdkClient { return sdkClientAt(path, false) }

// CLIRelease reads the installed CLI's version and the newest stable release
// on npm, with the npm configuration in effect for the service user.
func (p *webProvider) CLIRelease(ctx context.Context) (agentapi.CLIRelease, error) {
	var rel agentapi.CLIRelease
	path, err := resolveCopilot()
	if err != nil {
		return rel, err
	}
	if rel.Installed, err = cliVersion(ctx, path); err != nil {
		return rel, err
	}
	npm, manual, err := npmFor(ctx, path)
	if err != nil {
		return rel, err
	}
	latest, err := runCLI(ctx, cliCommandTimeout, npm, "view", copilotPackage+"@latest", "version")
	if err != nil {
		return rel, fmt.Errorf("npm view %s: %w", copilotPackage, err)
	}
	if !releaseExpr.MatchString(latest) {
		return rel, fmt.Errorf("npm reported no stable release of %s: %s", copilotPackage, cleanText(latest))
	}
	rel.Latest, rel.Newer, rel.Manual = latest, newerRelease(latest, rel.Installed), manual
	return rel, nil
}

// UpdateCLI installs version with the npm that manages the installed CLI.
// It first installs version into a staging folder and starts an SDK client
// on it, so a release the SDK refuses is never installed for real. Then it
// holds new starts, quiesces, stops the running CLI and installs version
// globally; the next use starts it.
func (p *webProvider) UpdateCLI(ctx context.Context, version string, quiesce func() error) error {
	if !releaseExpr.MatchString(version) {
		return fmt.Errorf("%q is not a Copilot CLI release", version)
	}
	ctx, cancel := context.WithTimeout(ctx, cliUpdateTimeout)
	defer cancel()
	path, err := resolveCopilot()
	if err != nil {
		return err
	}
	npm, manual, err := npmFor(ctx, path)
	if err != nil {
		return err
	}
	if manual != "" {
		return errors.New(manual)
	}
	if err := stageRelease(ctx, npm, version); err != nil {
		return err
	}
	release, err := p.holdStarts()
	if err != nil {
		return err
	}
	defer release()
	if err := quiesce(); err != nil {
		return err
	}
	if err := p.stopForUpdate(ctx); err != nil {
		return err
	}
	pkg := copilotPackage + "@" + version
	if _, err := runCLI(ctx, cliUpdateTimeout, npm, "install", "-g", "--no-audit", "--no-fund", pkg); err != nil {
		return fmt.Errorf("npm install -g %s: %w", pkg, err)
	}
	release()
	installed, err := cliVersion(ctx, path)
	if err != nil {
		return err
	}
	if installed != version {
		return fmt.Errorf("copilot reports %s after installing %s", installed, version)
	}
	return nil
}

// stageRelease installs version into a temporary folder, checks that it runs
// as that version, and starts an SDK client on it, whose protocol handshake
// refuses a release the SDK cannot drive. The folder is removed afterwards.
func stageRelease(ctx context.Context, npm, version string) error {
	dir, err := os.MkdirTemp("", "uam-copilot-")
	if err != nil {
		return fmt.Errorf("create the Copilot CLI staging folder: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Warn("remove the copilot staging folder failed", "dir", dir, "error", err)
		}
	}()
	pkg := copilotPackage + "@" + version
	if _, err := runCLI(ctx, cliUpdateTimeout, npm, "install", "--prefix", dir, "--no-save", "--no-audit", "--no-fund", pkg); err != nil {
		return fmt.Errorf("npm install %s: %w", pkg, err)
	}
	staged := filepath.Join(dir, "node_modules", ".bin", "copilot")
	got, err := cliVersion(ctx, staged)
	if err != nil {
		return err
	}
	if got != version {
		return fmt.Errorf("the staged Copilot CLI reports %s, not %s", got, version)
	}
	c := probeClient(staged)
	sctx, cancel := context.WithTimeout(ctx, webStartTimeout)
	defer cancel()
	if err := c.Start(sctx); err != nil {
		c.ForceStop()
		msg := exitText(err)
		if strings.Contains(msg, "protocol version mismatch") {
			return fmt.Errorf("%w: %s", agentapi.ErrCLIIncompatible, msg)
		}
		return fmt.Errorf("start Copilot CLI %s: %s", version, msg)
	}
	// Stop kills the CLI even when it reports an error.
	if err := stopClient(ctx, c); err != nil {
		log.Warn("stop the staged copilot CLI failed", "error", err)
	}
	return nil
}

// holdStarts makes starts wait until release is called: a CLI started now
// would run the install being replaced. release may be called more than once.
func (p *webProvider) holdStarts() (release func(), err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.shut:
		return nil, agentapi.ErrClosed
	case p.updating != nil:
		return nil, errors.New("the Copilot CLI is already updating")
	}
	held := make(chan struct{})
	p.updating = held
	return sync.OnceFunc(func() {
		p.mu.Lock()
		p.updating = nil
		p.mu.Unlock()
		close(held)
	}), nil
}

// stopForUpdate stops the running CLI so the next call starts the updated
// one. Ending an open conversation would fail its Task, so it refuses while
// any is open; the check and the stop share the lock, so none opens between.
func (p *webProvider) stopForUpdate(ctx context.Context) error {
	p.mu.Lock()
	if n := len(p.convs); n > 0 {
		p.mu.Unlock()
		return fmt.Errorf("%w (%d); try again once they close", agentapi.ErrCLIBusy, n)
	}
	c := p.detachLocked()
	p.mu.Unlock()
	if c == nil {
		return nil
	}
	if err := p.stopDetached(ctx, c); err != nil {
		log.Warn("stop copilot CLI for an update failed", "error", err)
	}
	return nil
}

// npmFor finds the npm that manages the copilot at path: the one next to it,
// since npm links its global commands beside itself, else npm on PATH. manual
// says why that npm cannot update this copilot: it is not npm's global
// install, or npm's global folder is not writable.
func npmFor(ctx context.Context, path string) (npm, manual string, err error) {
	npm, err = exec.LookPath(filepath.Join(filepath.Dir(path), "npm"))
	if err != nil {
		if npm, err = exec.LookPath("npm"); err != nil {
			return "", "", errors.New("npm is not on PATH")
		}
	}
	root, err := runCLI(ctx, cliCommandTimeout, npm, "root", "-g")
	if err != nil {
		return "", "", fmt.Errorf("npm root -g: %w", err)
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", path, err)
	}
	// A global folder that does not exist holds no copilot either.
	realRoot, rootErr := filepath.EvalSymlinks(root)
	switch {
	case rootErr != nil || !strings.HasPrefix(target, filepath.Join(realRoot, "@github", "copilot")+string(filepath.Separator)):
		manual = fmt.Sprintf("Copilot CLI at %s was not installed with npm; update it the way it was installed", path)
	case unix.Access(realRoot, unix.W_OK) != nil:
		manual = fmt.Sprintf("npm's global folder %s is not writable by this user", root)
	}
	return npm, cleanText(manual), nil
}

// cliVersion is the version the CLI at path runs. The SDK runs it with
// --no-auto-update; without it the npm loader may report a newer
// self-update it downloaded instead of the binary the SDK runs.
func cliVersion(ctx context.Context, path string) (string, error) {
	out, err := runCLI(ctx, cliCommandTimeout, path, "--no-auto-update", "--version")
	if err != nil {
		return "", fmt.Errorf("copilot --version: %w", err)
	}
	// "GitHub Copilot CLI 1.0.93-1." ends the sentence with a dot.
	v := strings.TrimRight(installedVersionExpr.FindString(out), ".")
	if v == "" {
		return "", fmt.Errorf("copilot --version printed no version: %s", cleanText(out))
	}
	return v, nil
}

// newerRelease reports whether release (X.Y.Z) is newer than version, which
// may be a prerelease: a prerelease sorts below its release.
func newerRelease(release, version string) bool {
	core, pre, _ := strings.Cut(version, "-")
	a, b := strings.Split(release, "."), strings.Split(core, ".")
	for i := range 3 {
		x, _ := strconv.Atoi(a[i])
		y, _ := strconv.Atoi(b[i])
		if c := cmp.Compare(x, y); c != 0 {
			return c > 0
		}
	}
	return pre != ""
}

// runCLI runs name with args and returns its trimmed output. npm and the
// copilot loader run children that inherit the pipes, so the whole process
// group is killed when the command ends or times out. A failure carries the
// start of the command's error output, without npm's warnings.
func runCLI(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(rctx, name, args...) // #nosec G204 -- the service owner's copilot and npm from PATH, or the copilot staged by uam, with fixed arguments and a validated version; no shell.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var stdout, stderr boundedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		return "", errors.New(exitText(err))
	}
	err := cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	switch {
	case ctx.Err() != nil:
		return "", ctx.Err()
	case rctx.Err() != nil:
		return "", fmt.Errorf("did not finish within %s", timeout)
	case err != nil:
		return "", errors.New(failureText(stderr.String(), err))
	}
	return strings.TrimSpace(stdout.String()), nil
}

// failureText is the start of a command's error output without npm's
// warnings, or its exit status when it printed nothing else.
func failureText(out string, err error) string {
	var lines []string
	for line := range strings.Lines(out) {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(strings.ToLower(line), "npm warn") {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return exitText(err)
	}
	return cleanText(strings.Join(lines, " "))
}
