package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/daemonruntime"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/web"
)

const (
	bootUnitName    = "uam-web.service"
	bootUnitMarker  = "# Written by uam web; remove it with: uam web service remove\n"
	bootRemovedName = "boot-service-removed"
	bootUnavailable = "unavailable on this host (no systemd user session)"
)

// Tests replace these: where the systemd tools are found, and the file that
// names the cgroup this process runs in.
var (
	bootTool   = execpath.Resolve
	selfCgroup = "/proc/self/cgroup"
)

// bootUnitPath is where the user's systemd manager looks for units by
// default, whatever XDG_CONFIG_HOME the launching shell has.
func bootUnitPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user", bootUnitName), nil
}

// bootRemovedPath marks that uam web service remove ran: while it exists, uam
// web does not install the unit again.
func bootRemovedPath() string {
	return filepath.Join(filepath.Dir(web.TokenPath()), bootRemovedName)
}

// bootRun runs a systemd tool from a fixed directory, with a time limit, and
// returns its trimmed standard output.
func bootRun(name string, args ...string) (string, error) {
	path, err := bootTool(name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...) // #nosec G204 -- a systemd tool from a fixed directory with fixed arguments.
	cmd.Env = append(os.Environ(), web.UserBusEnv()...)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// systemdUser reports whether a systemd user manager answers. Without one
// (WSL, containers, macOS) systemctl prints "offline" or nothing.
func systemdUser() bool {
	state, _ := bootRun("systemctl", "--user", "is-system-running")
	return state != "" && state != "offline" && state != "unknown"
}

func isBootUnit(data []byte) bool { return strings.HasPrefix(string(data), bootUnitMarker) }

// bootUnit renders the unit that runs `uam __web` with the flags this start
// used. Its environment is only HOME, PATH and uam's directory settings,
// like a start from a clean shell; the access token stays in its file.
func bootUnit(exe string, opts webOptions) (string, error) {
	env, err := daemonruntime.WorkEnv()
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(bootUnitMarker + "[Unit]\nDescription=uam web\n\n[Service]\n")
	for _, name := range append([]string{"HOME", "PATH"}, daemonruntime.PathEnv...) {
		i := slices.IndexFunc(env, func(kv string) bool { return strings.HasPrefix(kv, name+"=") })
		if i < 0 || env[i] == name+"=" {
			continue
		}
		v, err := unitQuote(env[i], false)
		if err != nil {
			return "", err
		}
		b.WriteString("Environment=" + v + "\n")
	}
	argv := append([]string{exe, "__web"}, opts.args()...)
	for i, a := range argv {
		if argv[i], err = unitQuote(a, true); err != nil {
			return "", err
		}
	}
	b.WriteString("ExecStart=" + strings.Join(argv, " ") + "\n\n[Install]\nWantedBy=default.target\n")
	return b.String(), nil
}

// unitQuote quotes s for a unit file: specifiers (%) and, in command lines,
// variable expansion ($) are escaped. A line break could add directives, so
// it is refused.
func unitQuote(s string, command bool) (string, error) {
	if strings.ContainsAny(s, "\r\n") {
		return "", errors.New("a line break cannot go in a systemd unit")
	}
	s = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "%", "%%").Replace(s)
	if command {
		s = strings.ReplaceAll(s, "$", "$$")
	}
	return `"` + s + `"`, nil
}

// installBootService writes or refreshes the systemd user unit that starts
// the service at boot and enables it (it does not start a second service),
// then asks logind to keep the user's manager running without a login.
// It does nothing without a systemd user manager, inside the unit's own
// service, after uam web service remove, or when a uam-web.service that uam
// did not write is there. Failures are logged only: the service already runs.
func installBootService(exe string, opts webOptions) {
	if cgroup, err := os.ReadFile(selfCgroup); err == nil && strings.Contains(string(cgroup), "/"+bootUnitName) {
		return
	}
	if _, err := os.Stat(bootRemovedPath()); err == nil || !systemdUser() {
		return
	}
	path, err := bootUnitPath()
	if err != nil {
		log.Warn("boot service", "err", err)
		return
	}
	old, err := os.ReadFile(path) // #nosec G304 -- fixed name under the user's home.
	if err == nil && !isBootUnit(old) {
		return
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Warn("boot service", "err", err)
		return
	}
	unit, err := bootUnit(exe, opts)
	if err != nil {
		log.Warn("boot service", "err", err)
		return
	}
	if string(old) != unit {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- systemd's standard user unit directory.
			log.Warn("boot service", "err", err)
			return
		}
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil { // #nosec G306 -- a unit file holds no secret.
			log.Warn("boot service", "err", err)
			return
		}
		if _, err := bootRun("systemctl", "--user", "daemon-reload"); err != nil {
			log.Warn("boot service: systemctl --user daemon-reload", "err", err)
		}
		fmt.Printf("Boot service: %s starts uam web at boot; uam web service remove takes it out.\n", path)
	}
	if _, err := bootRun("systemctl", "--user", "enable", bootUnitName); err != nil {
		log.Warn("boot service: systemctl --user enable", "err", err)
	}
	// Without lingering the manager, and the unit with it, starts at login.
	if _, err := bootRun("loginctl", "--no-ask-password", "enable-linger"); err != nil {
		log.Info("boot service: loginctl enable-linger refused; it starts at login", "err", err)
	}
}

// bootServiceStatus describes the boot service for uam web status.
func bootServiceStatus() []string {
	if !systemdUser() {
		return []string{"Boot service: " + bootUnavailable}
	}
	path, err := bootUnitPath()
	if err != nil {
		return []string{"Boot service: " + err.Error()}
	}
	data, readErr := os.ReadFile(path) // #nosec G304 -- fixed name under the user's home.
	_, removedErr := os.Stat(bootRemovedPath())
	var line string
	switch {
	case readErr == nil && !isBootUnit(data):
		line = path + " was not written by uam; uam leaves it as is"
	case removedErr == nil:
		line = "removed with uam web service remove; delete " + bootRemovedPath() + " to let uam web install it again"
	case readErr != nil:
		line = "not installed; uam web installs it when it starts the service"
	default:
		enabled, _ := bootRun("systemctl", "--user", "is-enabled", bootUnitName)
		linger, _ := bootRun("loginctl", "show-user", strconv.Itoa(os.Getuid()), "-p", "Linger", "--value")
		switch {
		case enabled != "enabled":
			line = "installed but not enabled; uam web enables it when it starts the service"
		case linger == "yes":
			line = "enabled; starts at boot"
		default:
			line = "enabled; starts at login, not at boot, and stops at logout (lingering is off: loginctl enable-linger was refused)"
		}
	}
	lines := []string{"Boot service: " + line}
	if kill, _ := bootRun("busctl", "get-property", "org.freedesktop.login1", "/org/freedesktop/login1", "org.freedesktop.login1.Manager", "KillUserProcesses"); kill == "b true" {
		lines = append(lines, "Warning: logind ends a user's processes at logout (KillUserProcesses=yes), so a uam web started from a login shell stops then")
	}
	return lines
}

// runWebService is `uam web service remove`: it disables and deletes the
// unit uam wrote, and keeps uam web from installing it again.
func runWebService(args []string) error {
	if len(args) != 1 || args[0] != "remove" {
		return errors.New("usage: uam web service remove")
	}
	path, err := bootUnitPath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- fixed name under the user's home.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	exists := err == nil
	if exists && !isBootUnit(data) {
		return fmt.Errorf("%s was not written by uam; left as is", path)
	}
	removed := bootRemovedPath()
	if err := os.MkdirAll(filepath.Dir(removed), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(removed, []byte("uam web does not install its boot service while this file exists.\n"), 0o600); err != nil {
		return err
	}
	if exists {
		available := systemdUser()
		if available {
			if _, err := bootRun("systemctl", "--user", "disable", bootUnitName); err != nil {
				return fmt.Errorf("systemctl --user disable %s: %w", bootUnitName, err)
			}
		}
		if err := os.Remove(path); err != nil {
			return err
		}
		if available {
			_, _ = bootRun("systemctl", "--user", "daemon-reload")
		}
	}
	fmt.Println("Boot service removed; uam web does not install it again. A running service keeps running: uam web stop ends it.")
	return nil
}
