package copilot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// npmFake is an npm global install of a fake copilot under a temporary
// prefix, with a fake npm beside it. Only its bin folder is on PATH, so no
// real npm or copilot runs.
type npmFake struct{ dir, bin, root string }

// npmScript logs each call, answers `root -g` and `view` (with the file
// latest), stages a copilot for `install --prefix`, and for `install -g`
// waits while the file hold exists, then records the version installed.
const npmScript = `#!/bin/sh
d='%[1]s'
echo "$*" >> "$d/npm.log"
for a; do last=$a; done
v=${last##*@}
case "$1" in
root) echo "$d/prefix/lib/node_modules" ;;
view) read -r latest < "$d/latest"; echo "$latest" ;;
install)
	if [ "$2" = -g ]; then
		if [ -e "$d/hold" ]; then
			: > "$d/installing"
			while [ -e "$d/hold" ]; do /bin/sleep 0.01; done
		fi
		echo "$v" > "$d/installed"
	else
		/bin/mkdir -p "$3/node_modules/.bin"
		printf '#!/bin/sh\necho "GitHub Copilot CLI %%s."\n' "$v" > "$3/node_modules/.bin/copilot"
		/bin/chmod +x "$3/node_modules/.bin/copilot"
	fi ;;
*) echo "unexpected npm $*" >&2; exit 1 ;;
esac
`

// loaderScript reports the installed version only with --no-auto-update,
// like the npm loader, which may otherwise report a downloaded self-update.
const loaderScript = `#!/bin/sh
read -r v < '%[1]s/installed'
if [ "$1" = --no-auto-update ]; then echo "GitHub Copilot CLI $v."; else echo "GitHub Copilot CLI 9.9.9."; fi
`

func fakeNPMInstall(t *testing.T, installed, latest string) npmFake {
	t.Helper()
	dir := t.TempDir()
	f := npmFake{dir: dir, bin: filepath.Join(dir, "prefix", "bin"), root: filepath.Join(dir, "prefix", "lib", "node_modules")}
	pkg := filepath.Join(f.root, "@github", "copilot")
	for _, d := range []string{pkg, f.bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(pkg, "npm-loader.js"), fmt.Sprintf(loaderScript, dir), 0o755)
	if err := os.Symlink("../lib/node_modules/@github/copilot/npm-loader.js", filepath.Join(f.bin, "copilot")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.bin, "npm"), fmt.Sprintf(npmScript, dir), 0o755)
	writeFile(t, filepath.Join(dir, "installed"), installed+"\n", 0o644)
	writeFile(t, filepath.Join(dir, "latest"), latest+"\n", 0o644)
	t.Setenv("PATH", f.bin)
	return f
}

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// calls returns the npm commands run so far.
func (f npmFake) calls(t *testing.T) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "npm.log"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func (f npmFake) installedGlobally(t *testing.T) bool {
	t.Helper()
	for _, c := range f.calls(t) {
		if strings.HasPrefix(c, "install -g") {
			return true
		}
	}
	return false
}

// fakeProbe replaces the staged-release client with c and records the path
// it was built for.
func fakeProbe(t *testing.T, c *fakeClient) *atomic.Value {
	t.Helper()
	var path atomic.Value
	old := probeClient
	probeClient = func(p string) sdkClient { path.Store(p); return c }
	t.Cleanup(func() { probeClient = old })
	return &path
}

func cliProvider(t *testing.T) (*webProvider, *fakeClient) {
	t.Helper()
	fc := &fakeClient{}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p, fc
}

func (f *fakeClient) lifecycle() (started, stopped, forced int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.stopped, f.forced
}

func TestCLIReleaseReadsInstalledLatestAndManual(t *testing.T) {
	ctx := context.Background()
	f := fakeNPMInstall(t, "1.0.93-1", "1.0.92")
	p, _ := cliProvider(t)
	rel, err := p.CLIRelease(ctx)
	if err != nil || rel != (agentapi.CLIRelease{Installed: "1.0.93-1", Latest: "1.0.92"}) {
		t.Fatalf("CLIRelease with a newer prerelease = %+v, %v", rel, err)
	}
	writeFile(t, filepath.Join(f.dir, "latest"), "1.0.94\n", 0o644)
	if rel, err = p.CLIRelease(ctx); err != nil || rel != (agentapi.CLIRelease{Installed: "1.0.93-1", Latest: "1.0.94", Newer: true}) {
		t.Fatalf("CLIRelease = %+v, %v", rel, err)
	}
	writeFile(t, filepath.Join(f.dir, "latest"), "1.0.95-beta.1\n", 0o644)
	if _, err = p.CLIRelease(ctx); err == nil || !strings.Contains(err.Error(), "no stable release") {
		t.Fatalf("CLIRelease with a prerelease latest = %v", err)
	}
	writeFile(t, filepath.Join(f.dir, "latest"), "1.0.94\n", 0o644)

	// A package in another node_modules, as another package manager places
	// it, is not npm's to update.
	t.Run("a Node.js package npm did not install", func(t *testing.T) {
		other := t.TempDir()
		pkg := filepath.Join(other, "node_modules", "@github", "copilot")
		if err := os.MkdirAll(pkg, 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(pkg, "npm-loader.js"), fmt.Sprintf(loaderScript, f.dir), 0o755)
		if err := os.Symlink(filepath.Join(pkg, "npm-loader.js"), filepath.Join(other, "copilot")); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", other+string(os.PathListSeparator)+f.bin)
		rel, err := p.CLIRelease(ctx)
		if err != nil || rel.Latest != "1.0.94" || !strings.Contains(rel.Manual, "Copilot CLI at "+filepath.Join(other, "copilot")+" is a Node.js package npm did not install") {
			t.Fatalf("CLIRelease = %+v, %v", rel, err)
		}
		if err := p.UpdateCLI(ctx, "1.0.94", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "npm did not install") {
			t.Fatalf("UpdateCLI = %v", err)
		}
		for _, c := range f.calls(t) {
			if strings.HasPrefix(c, "install") {
				t.Fatalf("npm %s ran for a copilot npm does not manage", c)
			}
		}
	})

	t.Run("global folder not writable", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes anywhere")
		}
		if err := os.Chmod(f.root, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(f.root, 0o755) })
		rel, err := p.CLIRelease(ctx)
		if err != nil || rel.Manual != "npm's global folder "+f.root+" is not writable by this user" {
			t.Fatalf("CLIRelease = %+v, %v", rel, err)
		}
	})
}

func TestNewerRelease(t *testing.T) {
	for _, tc := range []struct {
		release, version string
		want             bool
	}{
		{"1.0.92", "1.0.80", true},
		{"1.0.92", "1.0.92", false},
		{"1.0.92", "1.0.93-1", false},
		{"1.0.93", "1.0.93-1", true},
		{"1.0.100", "1.0.99", true},
		{"1.0.9", "1.0.10", false},
		{"2.0.0", "1.9.9", true},
	} {
		if got := newerRelease(tc.release, tc.version); got != tc.want {
			t.Errorf("newerRelease(%s, %s) = %v", tc.release, tc.version, got)
		}
	}
}

// An update stages the release, starts an SDK client on it, then stops the
// running CLI and installs the release globally while starts wait; the next
// start runs the new CLI.
func TestUpdateCLIStagesProbesAndReplacesTheRunningCLI(t *testing.T) {
	ctx := context.Background()
	f := fakeNPMInstall(t, "1.0.80", "1.0.92")
	probe := &fakeClient{}
	probed := fakeProbe(t, probe)
	p, fc := cliProvider(t)
	if _, err := p.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.dir, "hold"), "", 0o644)
	var quiesced atomic.Int32
	done := make(chan error, 1)
	go func() { done <- p.UpdateCLI(ctx, "1.0.92", func() error { quiesced.Add(1); return nil }) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(f.dir, "installing")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the global install did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, stopped, _ := fc.lifecycle(); stopped != 1 || quiesced.Load() != 1 {
		t.Fatalf("before the global install: stopped %d, quiesced %d", stopped, quiesced.Load())
	}
	short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	_, err := p.ensureStarted(short)
	cancel()
	if err == nil || !strings.Contains(err.Error(), "wait for the Copilot CLI update") {
		t.Fatalf("start bounded by its context during the install = %v", err)
	}
	started := make(chan error, 1)
	go func() { _, err := p.ensureStarted(ctx); started <- err }()
	select {
	case err := <-started:
		t.Fatalf("a start did not wait for the install: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if err := os.Remove(filepath.Join(f.dir, "hold")); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("UpdateCLI = %v", err)
	}
	if err := <-started; err != nil {
		t.Fatalf("start after the update = %v", err)
	}
	if started, _, _ := fc.lifecycle(); started != 2 {
		t.Fatalf("CLI started %d times; want the next use to start the new one", started)
	}
	if started, stopped, _ := probe.lifecycle(); started != 1 || stopped != 1 {
		t.Fatalf("staged client started %d, stopped %d times", started, stopped)
	}
	path, _ := probed.Load().(string)
	staging, ok := strings.CutSuffix(path, "/node_modules/.bin/copilot")
	if !ok {
		t.Fatalf("staged client built for %q", path)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging folder %s left behind: %v", staging, err)
	}
	calls := strings.Join(f.calls(t), "\n")
	for _, want := range []string{
		"install --prefix " + staging + " --no-save --no-audit --no-fund @github/copilot@1.0.92",
		"install -g --no-audit --no-fund @github/copilot@1.0.92",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("npm calls %q lack %q", calls, want)
		}
	}
	if rel, err := p.CLIRelease(ctx); err != nil || rel.Installed != "1.0.92" || rel.Newer {
		t.Fatalf("CLIRelease after the update = %+v, %v", rel, err)
	}
}

// A release the SDK refuses at the protocol handshake is never installed
// globally, and the running CLI is left alone.
func TestUpdateCLIRefusesAReleaseTheSDKCannotDrive(t *testing.T) {
	ctx := context.Background()
	f := fakeNPMInstall(t, "1.0.80", "1.0.95")
	probe := &fakeClient{startErr: errors.New("SDK protocol version mismatch: SDK supports versions 3-3, but server reports version 4. Please update your SDK or server to ensure compatibility")}
	fakeProbe(t, probe)
	p, fc := cliProvider(t)
	if _, err := p.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	quiesced := 0
	err := p.UpdateCLI(ctx, "1.0.95", func() error { quiesced++; return nil })
	if !errors.Is(err, agentapi.ErrCLIIncompatible) || !strings.Contains(err.Error(), "server reports version 4") {
		t.Fatalf("UpdateCLI = %v", err)
	}
	if f.installedGlobally(t) || quiesced != 0 {
		t.Fatalf("refused release: npm %q, quiesced %d", f.calls(t), quiesced)
	}
	if _, stopped, _ := fc.lifecycle(); stopped != 0 {
		t.Fatal("the running CLI was stopped for a refused release")
	}
	if _, _, forced := probe.lifecycle(); forced != 1 {
		t.Fatalf("staged client force-stopped %d times", forced)
	}
}

// An update stops before anything is installed when quiesce refuses or a
// conversation is still open; starts are released again.
func TestUpdateCLIStopsWhileTasksAreBusy(t *testing.T) {
	ctx := context.Background()
	t.Run("quiesce refuses", func(t *testing.T) {
		f := fakeNPMInstall(t, "1.0.80", "1.0.92")
		fakeProbe(t, &fakeClient{})
		p, fc := cliProvider(t)
		if _, err := p.ensureStarted(ctx); err != nil {
			t.Fatal(err)
		}
		busy := errors.New("1 task is working")
		if err := p.UpdateCLI(ctx, "1.0.92", func() error { return busy }); !errors.Is(err, busy) {
			t.Fatalf("UpdateCLI = %v", err)
		}
		if f.installedGlobally(t) {
			t.Fatal("installed while busy")
		}
		if _, stopped, _ := fc.lifecycle(); stopped != 0 {
			t.Fatal("the running CLI was stopped while busy")
		}
		short, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		if _, err := p.ensureStarted(short); err != nil {
			t.Fatalf("start after a refused update = %v", err)
		}
	})
	t.Run("conversation open", func(t *testing.T) {
		f := fakeNPMInstall(t, "1.0.80", "1.0.92")
		fakeProbe(t, &fakeClient{})
		p, fc := cliProvider(t)
		if _, err := p.Open(ctx, agentapi.OpenRequest{SessionID: "s-1", Workdir: "/work", Events: &recSink{}}); err != nil {
			t.Fatal(err)
		}
		if err := p.UpdateCLI(ctx, "1.0.92", func() error { return nil }); !errors.Is(err, agentapi.ErrCLIBusy) {
			t.Fatalf("UpdateCLI = %v", err)
		}
		if f.installedGlobally(t) || p.openConversations() != 1 {
			t.Fatalf("busy update: npm %q, open %d", f.calls(t), p.openConversations())
		}
		if _, stopped, _ := fc.lifecycle(); stopped != 0 {
			t.Fatal("the running CLI was stopped under an open conversation")
		}
	})
}

// binaryScript is a standalone copilot: it reports its version however it
// is asked, unlike the npm loader.
const binaryScript = "#!/bin/sh\necho \"GitHub Copilot CLI %s.\"\n"

// fakeBinaryInstall places a standalone copilot, as the install script or a
// Homebrew cask does, in a temporary bin folder that alone is on PATH, so
// neither npm nor a real copilot runs. It returns the binary's path.
func fakeBinaryInstall(t *testing.T, installed string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "copilot")
	writeFile(t, path, fmt.Sprintf(binaryScript, installed), 0o755)
	t.Setenv("PATH", bin)
	return path
}

// fakeReleases stands in for the published releases: /latest redirects to
// version's tag, whose archive for this platform holds a copilot script
// reporting version, listed in a checksum list that corrupt makes wrong.
func fakeReleases(t *testing.T, version string, corrupt bool) {
	t.Helper()
	asset := releaseAsset()
	if asset == "" {
		t.Skip("no release archive for this platform")
	}
	archive := tarGz(t, "copilot", fmt.Sprintf(binaryScript, version))
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:])
	if corrupt {
		sums = strings.Repeat("0", 64)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/v"+version, http.StatusFound)
	})
	mux.HandleFunc("GET /releases/download/v"+version+"/SHA256SUMS.txt", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, "%s  copilot-other.tar.gz\n%s  %s\n", strings.Repeat("1", 64), sums, asset)
	})
	mux.HandleFunc("GET /releases/download/v"+version+"/"+asset, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := releaseBase
	releaseBase = srv.URL + "/releases"
	t.Cleanup(func() { releaseBase = old })
}

// tarGz is a gzipped tar holding one executable file.
func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A standalone binary reads the newest release from the published releases,
// since npm need not exist, and can be updated while its folder is writable.
func TestCLIReleaseForAStandaloneBinary(t *testing.T) {
	path := fakeBinaryInstall(t, "1.0.89")
	fakeReleases(t, "1.0.92", false)
	p, _ := cliProvider(t)
	rel, err := p.CLIRelease(context.Background())
	if err != nil || rel != (agentapi.CLIRelease{Installed: "1.0.89", Latest: "1.0.92", Newer: true}) {
		t.Fatalf("CLIRelease = %+v, %v", rel, err)
	}
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	if err := os.Chmod(filepath.Dir(path), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(path), 0o755) })
	rel, err = p.CLIRelease(context.Background())
	if err != nil || !strings.Contains(rel.Manual, "cannot write") {
		t.Fatalf("CLIRelease in a read-only folder = %+v, %v", rel, err)
	}
	if err := p.UpdateCLI(context.Background(), "1.0.92", func() error { return nil }); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Fatalf("UpdateCLI in a read-only folder = %v", err)
	}
}

// A standalone binary is replaced in place by the release's binary once it
// passed the checksum and the SDK handshake on a staged copy; the running
// CLI is stopped after the idle conversations closed, and the next start
// runs the new one. Nothing is left beside it.
func TestUpdateCLIReplacesAStandaloneBinary(t *testing.T) {
	path := fakeBinaryInstall(t, "1.0.89")
	fakeReleases(t, "1.0.92", false)
	p, fc := cliProvider(t)
	probed := fakeProbe(t, &fakeClient{})
	if _, err := p.ensureStarted(context.Background()); err != nil {
		t.Fatal(err)
	}
	quiesced := false
	if err := p.UpdateCLI(context.Background(), "1.0.92", func() error { quiesced = true; return nil }); err != nil {
		t.Fatalf("UpdateCLI: %v", err)
	}
	staged, _ := probed.Load().(string)
	if !quiesced || staged == "" || strings.HasPrefix(staged, filepath.Dir(path)) {
		t.Fatalf("quiesced %v, probed %q", quiesced, staged)
	}
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging folder kept: %v", err)
	}
	if v, err := cliVersion(context.Background(), path); err != nil || v != "1.0.92" {
		t.Fatalf("installed copilot reports %q, %v", v, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("replaced binary not executable: %v, %v", info, err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Fatalf("files beside copilot: %v", entries)
	}
	if started, stopped, _ := fc.lifecycle(); started != 1 || stopped != 1 {
		t.Fatalf("running client started %d, stopped %d", started, stopped)
	}
	if _, err := p.ensureStarted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if started, _, _ := fc.lifecycle(); started != 2 {
		t.Fatalf("starts after the update = %d", started)
	}
}

// An archive that does not match the release's checksum list is never
// unpacked, probed or installed.
func TestUpdateCLIRefusesAnArchiveWithAWrongChecksum(t *testing.T) {
	path := fakeBinaryInstall(t, "1.0.89")
	fakeReleases(t, "1.0.92", true)
	p, _ := cliProvider(t)
	probed := fakeProbe(t, &fakeClient{})
	err := p.UpdateCLI(context.Background(), "1.0.92", func() error { t.Fatal("quiesced"); return nil })
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("UpdateCLI = %v", err)
	}
	if probed.Load() != nil {
		t.Fatal("probed a rejected archive")
	}
	if v, _ := cliVersion(context.Background(), path); v != "1.0.89" {
		t.Fatalf("installed copilot changed to %q", v)
	}
}
