package copilot

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// newSDKClient points the SDK at the installed CLI.
func newSDKClient() (sdkClient, error) {
	path, err := resolveCopilot()
	if err != nil {
		return nil, err
	}
	return sdkClientAt(nativeCopilot(path), true), nil
}

// installedCLI is the CLI binary newSDKClient starts now, and its file.
func installedCLI() (string, os.FileInfo, error) {
	path, err := resolveCopilot()
	if err != nil {
		return "", nil, err
	}
	path = nativeCopilot(path)
	info, err := os.Stat(path)
	return path, info, err
}

// sdkClientAt points the SDK at the CLI at path, retaining the user's login
// and configuration. Disable shell history in the runtime and its children
// and turn on the feature assisted permissions need, without changing the
// service environment or the manual web terminal. export adds the local
// usage export; a client that runs no model leaves it out, so it creates no
// export file.
func sdkClientAt(path string, export bool) sdkClient {
	env := append(withAutoApproval(os.Environ()), "HISTFILE="+os.DevNull, "HISTSIZE=0")
	env = withDefault(env, wsMaxAge, "15")
	if owner := cliOwner(); owner != "" {
		env = append(env, cliOwnerEnv+"="+owner)
	}
	var telemetry *copilot.TelemetryConfig
	if export {
		var err error
		if telemetry, err = usageTelemetry(env, os.UserHomeDir); err != nil {
			log.Warn("copilot local usage export unavailable; SDK usage remains enabled")
		}
	}
	a := sdkClientAdapter{c: copilot.NewClient(&copilot.ClientOptions{Connection: copilot.StdioConnection{Path: path, Env: env}, Telemetry: telemetry})}
	if telemetry != nil && telemetry.FilePath != "" {
		a.export = telemetry.FilePath
	}
	return a
}

// wsMaxAge caps the age (seconds) at which the CLI still reuses its model
// WebSocket. A firewall or proxy that drops an idle tunnel without closing it
// leaves the CLI sending the next request into a dead socket, where it hangs
// with no timeout; GitHub's edge closes idle sockets after about 20 s, so a
// socket older than 15 s at reuse is replaced instead. Undocumented CLI
// setting (1.0.103); the owner's own value wins.
const wsMaxAge = "COPILOT_CLI_WEBSOCKET_MAX_CONNECTION_AGE_SECS"

// withDefault returns env with key=value added unless env already sets key.
func withDefault(env []string, key, value string) []string {
	if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, key+"=") }) {
		return env
	}
	return append(env, key+"="+value)
}

// cliFeatureFlags lists the CLI's enabled feature flags, comma-separated.
const cliFeatureFlags = "COPILOT_CLI_ENABLED_FEATURE_FLAGS"

// withAutoApproval returns a copy of env whose CLI feature flags include
// AUTO_APPROVAL, the CLI's gate for the assisted permission mode, added to
// the flags env sets, if any.
func withAutoApproval(env []string) []string {
	out := slices.Clone(env)
	for i := len(out) - 1; i >= 0; i-- {
		flags, ok := strings.CutPrefix(out[i], cliFeatureFlags+"=")
		if !ok {
			continue
		}
		switch {
		case slices.ContainsFunc(strings.Split(flags, ","), func(f string) bool { return strings.TrimSpace(f) == "AUTO_APPROVAL" }):
		case flags == "":
			out[i] += "AUTO_APPROVAL"
		default:
			out[i] += ",AUTO_APPROVAL"
		}
		return out
	}
	return append(out, cliFeatureFlags+"=AUTO_APPROVAL")
}

func resolveCopilot() (string, error) {
	path, err := exec.LookPath("copilot")
	if err != nil {
		return "", errors.New("GitHub Copilot CLI (copilot) is not installed or not on PATH")
	}
	if nodeScript(path) {
		if _, err := exec.LookPath("node"); err != nil {
			return "", fmt.Errorf("%s is a Node.js script but node is not on PATH", path)
		}
	}
	return path, nil
}

func nodeScript(path string) bool {
	f, err := os.Open(path) // #nosec G304 -- resolveCopilot supplies the service owner's PATH executable; only its bounded shebang is read.
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	line, _ := bufio.NewReader(io.LimitReader(f, 256)).ReadString('\n')
	return strings.HasPrefix(line, "#!") && strings.Contains(line, "node")
}

// nativeCopilot returns the platform binary the npm loader at path runs,
// found the way Node resolves it from the loader, so the process the SDK
// starts, pings and kills is the CLI itself. Through the loader the CLI is
// the loader's child, and killing the loader leaves a hung CLI running. It
// returns path when path is not the npm loader, this Linux's C library
// cannot be told, or no platform package is installed.
func nativeCopilot(path string) string {
	target, err := filepath.EvalSymlinks(path)
	if err != nil || !nodeScript(target) {
		return path
	}
	platforms := []string{runtime.GOOS}
	if runtime.GOOS == "linux" {
		switch lddLibc(lddPath) {
		case "glibc":
		case "musl":
			platforms = []string{"linuxmusl", "linux"}
		default:
			return path
		}
	}
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	for _, platform := range platforms {
		pkg := filepath.Join("node_modules", copilotPackage+"-"+platform+"-"+arch)
		for dir := filepath.Dir(target); ; dir = filepath.Dir(dir) {
			if filepath.Base(dir) != "node_modules" {
				if bin := packageBinary(filepath.Join(dir, pkg)); bin != "" {
					return bin
				}
			}
			if dir == filepath.Dir(dir) {
				break
			}
		}
	}
	return path
}

// lddPath is the ldd nativeCopilot reads the C library from. Tests replace
// it.
var lddPath = "/usr/bin/ldd"

// lddLibc names the C library ldd at path belongs to, as the npm loader's
// detect-libc reads it: "musl", "glibc", or "" when it does not tell.
func lddLibc(path string) string {
	b, err := os.ReadFile(path) // #nosec G304 -- the system ldd, or a test fixture.
	switch {
	case err != nil:
		return ""
	case strings.Contains(string(b), "musl"):
		return "musl"
	case strings.Contains(string(b), "GNU C Library"):
		return "glibc"
	}
	return ""
}

// packageBinary returns the executable the npm package in dir exports as
// its main entry, or "" when there is none.
func packageBinary(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json")) // #nosec G304 -- inside the service owner's copilot install found from PATH.
	if err != nil {
		return ""
	}
	var pkg struct {
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	var entry string
	if json.Unmarshal(pkg.Exports, &entry) != nil {
		var entries map[string]json.RawMessage
		if json.Unmarshal(pkg.Exports, &entries) != nil || json.Unmarshal(entries["."], &entry) != nil {
			return ""
		}
	}
	bin := filepath.Join(dir, filepath.FromSlash(entry))
	if !strings.HasPrefix(bin, dir+string(filepath.Separator)) {
		return ""
	}
	if info, err := os.Stat(bin); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return ""
	}
	return bin
}
