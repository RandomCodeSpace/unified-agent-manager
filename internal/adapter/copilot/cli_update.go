package copilot

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
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
	// cliUpdateTimeout bounds a whole update, download and installs included.
	cliUpdateTimeout = 10 * time.Minute
	copilotPackage   = "@github/copilot"
	// maxReleaseBytes bounds a release archive download; maxSumsBytes its
	// checksum list.
	maxReleaseBytes = 512 << 20
	maxSumsBytes    = 64 << 10
)

var (
	// installedVersionExpr finds the version `copilot --version` prints,
	// which may be a prerelease such as 1.0.93-1.
	installedVersionExpr = regexp.MustCompile(`\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?`)
	// releaseExpr is a stable release, the only kind offered.
	releaseExpr = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	// releaseBase is where Copilot CLI releases are published: /latest
	// redirects to the newest stable tag, and each tag holds one archive
	// per platform with a SHA256SUMS.txt. Tests point it at a local server.
	releaseBase = "https://github.com/github/copilot-cli/releases"
)

// probeClient builds the SDK client an update starts on a staged release,
// the way the runtime's is built: the SDK's protocol handshake is what says
// whether it can drive that release. Tests replace it.
var probeClient = func(path string) sdkClient { return sdkClientAt(path, false) }

// cliInstall is how the copilot on PATH was put there, which decides how
// it is updated: with the npm that installed it, or by replacing the
// standalone binary the install script, a Homebrew cask or a person placed.
type cliInstall struct {
	path   string // copilot on PATH
	target string // the file it resolves to
	npm    string // the npm that manages it; empty for a standalone binary
	manual string // why it cannot be updated here; empty when it can
}

// locateCLI finds the installed copilot and how it is managed.
func locateCLI(ctx context.Context) (cliInstall, error) {
	path, err := resolveCopilot()
	if err != nil {
		return cliInstall{}, err
	}
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return cliInstall{}, fmt.Errorf("resolve %s: %w", path, err)
	}
	inst := cliInstall{path: path, target: target}
	// npm's global command resolves to the loader in its package folder.
	if strings.Contains(filepath.ToSlash(target), "/node_modules/"+copilotPackage+"/") {
		inst.npm, inst.manual, err = npmFor(ctx, path, target)
		return inst, err
	}
	switch {
	case releaseAsset() == "":
		inst.manual = "Copilot CLI updates here support Linux and macOS on x64 and arm64; update it the way it was installed"
	case unix.Access(filepath.Dir(target), unix.W_OK) != nil:
		inst.manual = fmt.Sprintf("Copilot CLI at %s is in a folder this user cannot write; update it the way it was installed", target)
	}
	inst.manual = cleanText(inst.manual)
	return inst, nil
}

// CLIRelease reads the installed CLI's version and the newest stable
// release: from npm, with the npm configuration in effect for the service
// user, when npm installed it; else from the published releases.
func (p *webProvider) CLIRelease(ctx context.Context) (agentapi.CLIRelease, error) {
	var rel agentapi.CLIRelease
	inst, err := locateCLI(ctx)
	if err != nil {
		return rel, err
	}
	if rel.Installed, err = cliVersion(ctx, inst.path); err != nil {
		return rel, err
	}
	latest, err := latestRelease(ctx, inst.npm)
	if err != nil {
		return rel, err
	}
	rel.Latest, rel.Newer, rel.Manual = latest, newerRelease(latest, rel.Installed), inst.manual
	return rel, nil
}

func latestRelease(ctx context.Context, npm string) (string, error) {
	if npm == "" {
		return latestPublished(ctx)
	}
	latest, err := runCLI(ctx, cliCommandTimeout, npm, "view", copilotPackage+"@latest", "version")
	if err != nil {
		return "", fmt.Errorf("npm view %s: %w", copilotPackage, err)
	}
	if !releaseExpr.MatchString(latest) {
		return "", fmt.Errorf("npm reported no stable release of %s: %s", copilotPackage, cleanText(latest))
	}
	return latest, nil
}

// latestPublished reads the newest stable release from where /latest
// redirects, which prereleases never are.
func latestPublished(ctx context.Context) (string, error) {
	rctx, cancel := context.WithTimeout(ctx, cliCommandTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, releaseBase+"/latest", nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("read the newest Copilot CLI release: %w", err)
	}
	_ = resp.Body.Close()
	loc := resp.Header.Get("Location")
	version := strings.TrimPrefix(path.Base(loc), "v")
	if resp.StatusCode/100 != 3 || !releaseExpr.MatchString(version) {
		return "", fmt.Errorf("the newest Copilot CLI release is not published where expected (HTTP %d)", resp.StatusCode)
	}
	return version, nil
}

// UpdateCLI installs version the way the installed CLI was installed. It
// first stages version and starts an SDK client on it, so a release the SDK
// refuses is never installed for real. Then it holds new starts, quiesces,
// stops the running CLI and puts version in place; the next use starts it.
func (p *webProvider) UpdateCLI(ctx context.Context, version string, quiesce func() error) error {
	if !releaseExpr.MatchString(version) {
		return fmt.Errorf("%q is not a Copilot CLI release", version)
	}
	ctx, cancel := context.WithTimeout(ctx, cliUpdateTimeout)
	defer cancel()
	inst, err := locateCLI(ctx)
	if err != nil {
		return err
	}
	if inst.manual != "" {
		return errors.New(inst.manual)
	}
	dir, err := os.MkdirTemp("", "uam-copilot-")
	if err != nil {
		return fmt.Errorf("create the Copilot CLI staging folder: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Warn("remove the copilot staging folder failed", "dir", dir, "error", err)
		}
	}()
	var staged string
	if inst.npm != "" {
		staged, err = stageNPM(ctx, inst.npm, dir, version)
	} else {
		staged, err = stageBinary(ctx, dir, version)
	}
	if err != nil {
		return err
	}
	if err := probeRelease(ctx, staged, version); err != nil {
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
	if inst.npm != "" {
		pkg := copilotPackage + "@" + version
		if _, err := runCLI(ctx, cliUpdateTimeout, inst.npm, "install", "-g", "--no-audit", "--no-fund", pkg); err != nil {
			return fmt.Errorf("npm install -g %s: %w", pkg, err)
		}
	} else if err := replaceBinary(staged, inst.target); err != nil {
		return err
	}
	release()
	installed, err := cliVersion(ctx, inst.path)
	if err != nil {
		return err
	}
	if installed != version {
		return fmt.Errorf("copilot reports %s after installing %s", installed, version)
	}
	return nil
}

// stageNPM installs version into dir with npm and returns the copilot in it.
func stageNPM(ctx context.Context, npm, dir, version string) (string, error) {
	pkg := copilotPackage + "@" + version
	if _, err := runCLI(ctx, cliUpdateTimeout, npm, "install", "--prefix", dir, "--no-save", "--no-audit", "--no-fund", pkg); err != nil {
		return "", fmt.Errorf("npm install %s: %w", pkg, err)
	}
	return filepath.Join(dir, "node_modules", ".bin", "copilot"), nil
}

// stageBinary downloads version's archive for this platform into dir,
// checks it against the release's checksum list, and returns the copilot
// binary unpacked from it.
func stageBinary(ctx context.Context, dir, version string) (string, error) {
	asset := releaseAsset()
	base := releaseBase + "/download/v" + version + "/"
	sums, err := fetchBytes(ctx, base+"SHA256SUMS.txt", maxSumsBytes)
	if err != nil {
		return "", fmt.Errorf("download the Copilot CLI %s checksums: %w", version, err)
	}
	want := ""
	for line := range strings.Lines(string(sums)) {
		if sum, name, ok := strings.Cut(strings.TrimSpace(line), " "); ok && strings.TrimSpace(name) == asset {
			want = sum
		}
	}
	if want == "" {
		return "", fmt.Errorf("the Copilot CLI %s release lists no checksum for %s", version, asset)
	}
	archive := filepath.Join(dir, asset)
	got, err := fetchFile(ctx, base+asset, archive, maxReleaseBytes)
	if err != nil {
		return "", fmt.Errorf("download the Copilot CLI %s archive: %w", version, err)
	}
	if got != want {
		return "", fmt.Errorf("the Copilot CLI %s archive does not match its published checksum", version)
	}
	staged := filepath.Join(dir, "copilot")
	if err := unpackBinary(archive, staged); err != nil {
		return "", fmt.Errorf("unpack the Copilot CLI %s archive: %w", version, err)
	}
	return staged, nil
}

// probeRelease checks that the staged copilot runs as version and that an
// SDK client starts on it: the protocol handshake refuses a release the SDK
// cannot drive.
func probeRelease(ctx context.Context, staged, version string) error {
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

// releaseAsset names this platform's archive in a release, as the install
// script picks it; empty on a platform without one.
func releaseAsset() string {
	os, ok := map[string]string{"linux": "linux", "darwin": "darwin"}[runtime.GOOS]
	arch, ok2 := map[string]string{"amd64": "x64", "arm64": "arm64"}[runtime.GOARCH]
	if !ok || !ok2 {
		return ""
	}
	return "copilot-" + os + "-" + arch + ".tar.gz"
}

func fetchBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	resp, err := fetch(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("larger than %d bytes", limit)
	}
	return b, nil
}

// fetchFile downloads url to path and returns the SHA-256 of what it wrote.
func fetchFile(ctx context.Context, url, path string, limit int64) (string, error) {
	resp, err := fetch(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- path is inside the staging folder uam just created.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", err
	}
	if n > limit {
		return "", fmt.Errorf("larger than %d bytes", limit)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func fetch(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

// unpackBinary writes the copilot file of the gzipped tar at archive to
// path, executable. Nothing else in the archive is written.
func unpackBinary(archive, path string) error {
	f, err := os.Open(archive) // #nosec G304 -- the archive uam downloaded into its staging folder.
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return errors.New("no copilot file in the archive")
			}
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "copilot" {
			continue
		}
		out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) // #nosec G302,G304 -- the staged binary must be executable; path is inside the staging folder.
		if err != nil {
			return err
		}
		_, err = io.Copy(out, io.LimitReader(tr, maxReleaseBytes)) // #nosec G110 -- bounded by maxReleaseBytes.
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	}
}

// replaceBinary puts staged in place of target: a copy beside target, then
// a rename, so the file is never half-written where the runtime starts it.
func replaceBinary(staged, target string) error {
	in, err := os.Open(staged) // #nosec G304 -- the binary uam staged and probed.
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	next := filepath.Join(filepath.Dir(target), ".copilot-uam-update")
	out, err := os.OpenFile(next, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755) // #nosec G302,G304 -- a binary beside the one it replaces; path from the installed copilot.
	if err != nil {
		return fmt.Errorf("write the new Copilot CLI beside %s: %w", target, err)
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(next, target)
	}
	if err != nil {
		_ = os.Remove(next)
		return fmt.Errorf("replace %s: %w", target, err)
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

// npmFor finds the npm that manages the Node.js copilot at path, resolving
// to target: the one next to it, since npm links its global commands beside
// itself, else npm on PATH. manual says why that npm cannot update this
// copilot: it is not npm's global install, or npm's global folder is not
// writable.
func npmFor(ctx context.Context, path, target string) (npm, manual string, err error) {
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
	// A global folder that does not exist holds no copilot either.
	realRoot, rootErr := filepath.EvalSymlinks(root)
	switch {
	case rootErr != nil || !strings.HasPrefix(target, filepath.Join(realRoot, "@github", "copilot")+string(filepath.Separator)):
		manual = fmt.Sprintf("Copilot CLI at %s is a Node.js package npm did not install; update it the way it was installed", path)
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
