package opencode

import (
	"context"
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

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const minimumVersion = "1.18.1"

const versionProbeTimeout = 2500 * time.Millisecond

type providerCommand struct {
	path  string
	shell string
	alias string
}

func providerCommandFor(req adapter.ResumeRequest) (providerCommand, error) {
	if req.ExecutablePath != "" {
		return providerCommandFromFlags(req.ExecutablePath, "", "")
	}
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return providerCommandFromFlags("", shell, req.CommandAlias)
}

func providerCommandFromFlags(path, shell, alias string) (providerCommand, error) {
	if path != "" {
		if alias != "" || shell != "" {
			return providerCommand{}, fmt.Errorf("OpenCode provider command cannot combine a direct path with shell alias fields")
		}
		if !filepath.IsAbs(path) {
			return providerCommand{}, fmt.Errorf("OpenCode executable path %q must be absolute", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return providerCommand{}, fmt.Errorf("stat OpenCode executable %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return providerCommand{}, fmt.Errorf("OpenCode executable path %q is not a regular file", path)
		}
		return providerCommand{path: path}, nil
	}

	if alias == "" {
		return providerCommand{}, fmt.Errorf("OpenCode provider command requires an executable path or alias")
	}
	if err := validateProviderAlias(alias); err != nil {
		return providerCommand{}, err
	}
	if !filepath.IsAbs(shell) {
		return providerCommand{}, fmt.Errorf("OpenCode alias shell %q must be absolute", shell)
	}
	return providerCommand{shell: shell, alias: alias}, nil
}

func validateProviderAlias(alias string) error {
	if alias == "" || strings.HasPrefix(alias, "-") {
		return fmt.Errorf("invalid OpenCode command alias %q", alias)
	}
	for _, r := range alias {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return fmt.Errorf("invalid OpenCode command alias %q", alias)
	}
	return nil
}

func (c providerCommand) argv(args ...string) []string {
	if c.path != "" {
		return append([]string{c.path}, args...)
	}
	aliasArgv := append([]string{c.alias}, args...)
	return []string{c.shell, "-ic", "exec " + adapter.ShellJoin(aliasArgv)}
}

func (c providerCommand) command(ctx context.Context, args ...string) *exec.Cmd {
	argv := c.argv(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv comes from a validated absolute executable or absolute shell plus safely quoted alias.
}

type semanticVersion struct {
	major, minor, patch int
	prerelease          bool
}

var semanticVersionRE = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func parseSemanticVersion(value string) (semanticVersion, error) {
	match := semanticVersionRE.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return semanticVersion{}, fmt.Errorf("malformed semantic version")
	}
	if match[4] != "" {
		for _, identifier := range strings.Split(match[4], ".") {
			if len(identifier) > 1 && identifier[0] == '0' && allDecimal(identifier) {
				return semanticVersion{}, fmt.Errorf("malformed semantic version prerelease")
			}
		}
	}
	major, err := strconv.Atoi(match[1])
	if err != nil {
		return semanticVersion{}, fmt.Errorf("parse semantic version major: %w", err)
	}
	minor, err := strconv.Atoi(match[2])
	if err != nil {
		return semanticVersion{}, fmt.Errorf("parse semantic version minor: %w", err)
	}
	patch, err := strconv.Atoi(match[3])
	if err != nil {
		return semanticVersion{}, fmt.Errorf("parse semantic version patch: %w", err)
	}
	return semanticVersion{major: major, minor: minor, patch: patch, prerelease: match[4] != ""}, nil
}

func allDecimal(value string) bool {
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (v semanticVersion) compare(other semanticVersion) int {
	for _, pair := range [][2]int{{v.major, other.major}, {v.minor, other.minor}, {v.patch, other.patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if v.prerelease == other.prerelease {
		return 0
	}
	if v.prerelease {
		return -1
	}
	return 1
}

type versionExecutableIdentity struct {
	path          string
	size, mtime   int64
	device, inode uint64
}

var minimumVersionCache = struct {
	sync.Mutex
	values map[versionExecutableIdentity]error
}{values: map[versionExecutableIdentity]error{}}

func requireMinimumVersion(ctx context.Context, command providerCommand) error {
	if command.path == "" {
		err, _ := probeMinimumVersion(ctx, command)
		return err
	}

	key, err := versionIdentity(command.path)
	if err != nil {
		return minimumVersionError(command, nil, "cannot inspect executable")
	}
	minimumVersionCache.Lock()
	defer minimumVersionCache.Unlock()
	if cached, ok := minimumVersionCache.values[key]; ok {
		return cached
	}
	err, deterministic := probeMinimumVersion(ctx, command)
	if deterministic {
		minimumVersionCache.values[key] = err
	}
	return err
}

func versionIdentity(path string) (versionExecutableIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		return versionExecutableIdentity{}, err
	}
	if !info.Mode().IsRegular() {
		return versionExecutableIdentity{}, fmt.Errorf("not a regular file")
	}
	key := versionExecutableIdentity{path: path, size: info.Size(), mtime: info.ModTime().UnixNano()}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		key.device, key.inode = uint64(stat.Dev), stat.Ino
	}
	return key, nil
}

// probeMinimumVersion reports whether the outcome is deterministic: only a
// parsed version (pass, or too low) is; timeouts and other exec failures are
// transient and must be retried on the next launch.
func probeMinimumVersion(ctx context.Context, command providerCommand) (err error, deterministic bool) {
	probeCtx, cancel := context.WithTimeout(ctx, versionProbeTimeout)
	defer cancel()
	out, err := command.command(probeCtx, "--version").CombinedOutput()
	if err != nil {
		if probeCtx.Err() != nil {
			return fmt.Errorf("OpenCode command %q version probe timed out after %s (detected %q); it will be retried on the next launch", commandIdentity(command), versionProbeTimeout, sanitizedOutput(out)), false
		}
		return minimumVersionError(command, out, "version probe exited unsuccessfully"), false
	}
	detected, err := parseSemanticVersion(string(out))
	if err != nil {
		return minimumVersionError(command, out, "unrecognized version output"), false
	}
	required := semanticVersion{major: 1, minor: 18, patch: 1}
	if detected.prerelease || detected.compare(required) < 0 {
		return minimumVersionError(command, out, "unsupported version"), true
	}
	return nil, true
}

func minimumVersionError(command providerCommand, output []byte, reason string) error {
	return fmt.Errorf("OpenCode command %q version check failed: detected %q (%s); required version %s; run `opencode upgrade %s`", commandIdentity(command), sanitizedOutput(output), reason, minimumVersion, minimumVersion)
}

func sanitizedOutput(output []byte) string {
	detected := strings.TrimSpace(displaytext.Sanitize(string(output)))
	if detected == "" {
		detected = "<no output>"
	}
	return detected
}

func commandIdentity(command providerCommand) string {
	identity := command.path
	if identity == "" {
		identity = command.alias
	}
	return displaytext.Sanitize(identity)
}
