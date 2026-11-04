package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/execpath"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/web"
)

// fakeSystemd puts fake systemctl, loginctl and busctl in place for one test,
// with a temporary home and config directory. systemctl answers
// is-system-running with state; every call is appended to the returned log.
func fakeSystemd(t *testing.T, state string) (calls func() string, home string) {
	t.Helper()
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "calls")
	script := `#!/bin/sh
echo "$(basename "$0") $*" >> "` + logPath + `"
case "$*" in
  "--user is-system-running") echo "` + state + `" ;;
  "--user is-enabled uam-web.service") echo enabled ;;
  show-user*) echo no ;;
  get-property*) echo "b true" ;;
esac
`
	for _, name := range []string{"systemctl", "loginctl", "busctl"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	prevTool, prevCgroup := bootTool, selfCgroup
	t.Cleanup(func() { bootTool, selfCgroup = prevTool, prevCgroup })
	bootTool = func(name string) (string, error) { return execpath.ResolveInDirs(name, []string{bin}) }
	selfCgroup = filepath.Join(t.TempDir(), "cgroup")
	if err := os.WriteFile(selfCgroup, []byte("0::/user.slice/user-1000.slice/session-1.scope\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("UAM_CONFIG_DIR", t.TempDir())
	t.Setenv("PATH", "/usr/bin:/bin")
	return func() string {
		data, _ := os.ReadFile(logPath)
		return string(data)
	}, home
}

func readUnit(t *testing.T, home string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".config", "systemd", "user", bootUnitName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// uam web writes a unit that runs this binary with the same flags in a clean
// environment, enables it and asks for lingering; a second start with the
// same settings leaves the file and the manager alone.
func TestBootServiceInstallsAndRefreshes(t *testing.T) {
	calls, home := fakeSystemd(t, "degraded")
	token, err := web.LoadOrCreateToken(web.TokenPath())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("UAM_BYOM_SECRET", "not-for-the-unit")
	opts := webOptions{listen: "127.0.0.1:8261", origins: []string{"https://uam.example.com"}, logHeaders: true}
	captureCLIStdout(t, func() { installBootService("/opt/uam dir/uam", opts) })

	unit := readUnit(t, home)
	for _, want := range []string{
		bootUnitMarker,
		"Restart=on-failure\nRestartSec=10\n",
		"StartLimitBurst=5\n",
		`Environment="HOME=` + home + `"`,
		`Environment="PATH=/usr/bin:/bin"`,
		`ExecStart="/opt/uam dir/uam" "__web" "--listen" "127.0.0.1:8261" "--public-origin" "https://uam.example.com" "--log-headers"`,
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, unit)
		}
	}
	if strings.Contains(unit, token) || strings.Contains(unit, "not-for-the-unit") {
		t.Fatalf("unit carries a secret:\n%s", unit)
	}
	first := calls()
	for _, want := range []string{"systemctl --user daemon-reload", "systemctl --user enable uam-web.service", "loginctl --no-ask-password enable-linger"} {
		if !strings.Contains(first, want) {
			t.Fatalf("calls lack %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, " start") {
		t.Fatalf("installing must not start a second service:\n%s", first)
	}

	out := captureCLIStdout(t, func() { installBootService("/opt/uam dir/uam", opts) })
	if readUnit(t, home) != unit || out != "" || strings.Count(calls(), "daemon-reload") != 1 {
		t.Fatalf("an unchanged start rewrote the unit (out %q):\n%s", out, calls())
	}
	opts.logHeaders = false
	captureCLIStdout(t, func() { installBootService("/opt/uam dir/uam", opts) })
	if strings.Contains(readUnit(t, home), "--log-headers") || strings.Count(calls(), "daemon-reload") != 2 {
		t.Fatalf("changed flags did not refresh the unit:\n%s", calls())
	}

	lines := strings.Join(bootServiceStatus(), "\n")
	if !strings.Contains(lines, "enabled; starts at login") || !strings.Contains(lines, "KillUserProcesses=yes") || strings.Contains(lines, "UAM_BYOM_") {
		t.Fatalf("status = %q", lines)
	}

	// A custom model whose key comes from the environment is named, by
	// variable only; one with a saved key is not.
	t.Setenv("UAM_BYOM_LAB", "lab-key-value")
	cfg := `{"web_settings":{"custom_models":[` +
		`{"name":"lab","base_url":"https://lab.example","model_id":"m","api_key_env":"UAM_BYOM_LAB"},` +
		`{"name":"saved","base_url":"https://saved.example","model_id":"m","api_key_env":"","api_key":"sealed"}]}}`
	if err := os.WriteFile(store.DefaultPath(), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	lines = strings.Join(bootServiceStatus(), "\n")
	if !strings.Contains(lines, "environment has no UAM_BYOM_LAB;") || strings.Contains(lines, "lab-key-value") || strings.Contains(lines, "sealed") {
		t.Fatalf("status = %q", lines)
	}
}

// Nothing is written or run where no systemd user manager answers, inside the
// unit's own service, or over a uam-web.service uam did not write.
func TestBootServiceSkips(t *testing.T) {
	opts := webOptions{listen: "127.0.0.1:8260"}
	unitPath := func(home string) string { return filepath.Join(home, ".config", "systemd", "user", bootUnitName) }

	t.Run("no systemd", func(t *testing.T) {
		calls, home := fakeSystemd(t, "offline")
		installBootService("/usr/bin/uam", opts)
		if _, err := os.Stat(unitPath(home)); !os.IsNotExist(err) {
			t.Fatalf("unit written without systemd: %v", err)
		}
		if strings.TrimSpace(calls()) != "systemctl --user is-system-running" {
			t.Fatalf("calls = %q", calls())
		}
		if got := bootServiceStatus(); len(got) != 1 || got[0] != "Boot service: "+bootUnavailable {
			t.Fatalf("status = %q", got)
		}
	})
	t.Run("no systemctl", func(t *testing.T) {
		_, home := fakeSystemd(t, "running")
		bootTool = func(string) (string, error) { return "", os.ErrNotExist }
		installBootService("/usr/bin/uam", opts)
		if _, err := os.Stat(unitPath(home)); !os.IsNotExist(err) {
			t.Fatalf("unit written without systemctl: %v", err)
		}
	})
	t.Run("inside the unit", func(t *testing.T) {
		calls, home := fakeSystemd(t, "running")
		if err := os.WriteFile(selfCgroup, []byte("0::/user.slice/user-1000.slice/user@1000.service/app.slice/uam-web.service\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		installBootService("/usr/bin/uam", opts)
		if _, err := os.Stat(unitPath(home)); !os.IsNotExist(err) || calls() != "" {
			t.Fatalf("installed from inside its own unit: %v, calls %q", err, calls())
		}
	})
	t.Run("unit uam did not write", func(t *testing.T) {
		calls, home := fakeSystemd(t, "running")
		own := "[Service]\nExecStart=/bin/true\n"
		if err := os.MkdirAll(filepath.Dir(unitPath(home)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(unitPath(home), []byte(own), 0o644); err != nil {
			t.Fatal(err)
		}
		installBootService("/usr/bin/uam", opts)
		if readUnit(t, home) != own || strings.Contains(calls(), "enable") {
			t.Fatalf("replaced or enabled a unit uam did not write:\n%s", calls())
		}
		if err := Run(context.Background(), []string{"web", "service", "remove"}); err == nil || readUnit(t, home) != own {
			t.Fatalf("remove = %v; it must leave a foreign unit alone", err)
		}
	})
}

// uam web service remove disables and deletes the unit, and uam web does not
// install it again afterwards.
func TestWebServiceRemove(t *testing.T) {
	calls, home := fakeSystemd(t, "running")
	opts := webOptions{listen: "127.0.0.1:8260"}
	captureCLIStdout(t, func() { installBootService("/usr/bin/uam", opts) })
	readUnit(t, home)

	out := captureCLIStdout(t, func() { must(t, Run(context.Background(), []string{"web", "service", "remove"})) })
	if !strings.Contains(out, "Boot service removed") || !strings.Contains(calls(), "systemctl --user disable uam-web.service") {
		t.Fatalf("remove out %q, calls:\n%s", out, calls())
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", bootUnitName)
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatalf("unit still present: %v", err)
	}
	installBootService("/usr/bin/uam", opts)
	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Fatal("uam web installed the unit again after remove")
	}
	if got := strings.Join(bootServiceStatus(), "\n"); !strings.Contains(got, "removed with uam web service remove") {
		t.Fatalf("status = %q", got)
	}
	if err := Run(context.Background(), []string{"web", "service"}); err == nil {
		t.Fatal("uam web service without remove must fail")
	}
}
