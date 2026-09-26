package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestAcceptanceSpawnReadinessProtocol(t *testing.T) {
	for _, tc := range []struct {
		name, script, want string
		cancel             bool
	}{
		{"ready", "printf 'ok\\n' >&3", "", false},
		{"refused", "printf 'error: address in use\\n' >&3", "address in use", false},
		{"early exit", "exit 0", "exited before it was ready", false},
		{"invalid reply", "printf 'almost ready\\n' >&3", "unexpected response", false},
		{"cancelled", "exec sleep 30", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "ready-helper")
			if err := os.WriteFile(exe, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if tc.cancel {
				cancel()
			}
			err := Spawn(ctx, exe, nil)
			switch {
			case tc.cancel:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled startup = %v", err)
				}
			case tc.want == "":
				if err != nil {
					t.Fatal(err)
				}
			case err == nil || !strings.Contains(err.Error(), tc.want):
				t.Fatalf("startup = %v, want %q", err, tc.want)
			}
		})
	}
	if err := Spawn(context.Background(), filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("missing executable reported readiness")
	}
}

func TestAcceptanceDaemonStartupFailureLeavesNoRunningState(t *testing.T) {
	for _, failure := range []string{"listen syntax", "public origin", "token", "token read", "token creation", "store", "occupied port", "state publication"} {
		t.Run(failure, func(t *testing.T) {
			runtimeDir, configDir := t.TempDir(), t.TempDir()
			t.Setenv("UAM_SESSION_DIR", runtimeDir)
			t.Setenv("UAM_CONFIG_DIR", configDir)
			t.Setenv(readyEnv, "")
			prov := agenttest.NewProvider("fake", allCaps)
			cfg := DaemonConfig{Listen: "127.0.0.1:0", Providers: []agentapi.Provider{prov}}
			want := ""
			switch failure {
			case "listen syntax":
				cfg.Listen = "not-an-address"
				want = "invalid --listen"
			case "public origin":
				cfg.PublicOrigins = []string{"ftp://example.com"}
				want = "invalid public origin"
			case "token":
				want = "malformed"
				if err := os.WriteFile(filepath.Join(configDir, tokenFileName), []byte("bad token"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "token read":
				want = "not a regular file"
				if err := os.Mkdir(filepath.Join(configDir, tokenFileName), 0o700); err != nil {
					t.Fatal(err)
				}
			case "token creation":
				want = "create token directory"
				blocked := filepath.Join(configDir, "not-a-directory")
				if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("UAM_CONFIG_DIR", blocked)
			case "store":
				want = "load web sessions"
				if err := os.Mkdir(filepath.Join(configDir, "sessions.json"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "occupied port":
				want = "listen on"
				ln, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = ln.Close() }()
				cfg.Listen = ln.Addr().String()
			case "state publication":
				want = "write web.json"
				if err := os.Mkdir(statePath(runtimeDir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := RunDaemon(cfg); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("startup failure = %v, want %q", err, want)
			}
			if _, running := ReadRunning(runtimeDir); running {
				t.Fatal("failed startup published a running service")
			}
			if !lockFree(runtimeDir) {
				t.Fatal("failed startup retained the daemon lock")
			}
			if (failure == "occupied port" || failure == "state publication") && prov.ShutdownCalls() != 1 {
				t.Fatalf("provider cleanup calls = %d", prov.ShutdownCalls())
			}
		})
	}
}

func TestAcceptanceStaleDaemonIdentityDoesNotStopAnotherProcess(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	start := session.ProcStartTime(child.Process.Pid)
	if start == 0 {
		t.Skip("process start identity is unavailable")
	}
	dir := t.TempDir()
	if err := writeStateFile(dir, DaemonState{PID: child.Process.Pid, StartTime: start + 1, Listen: "127.0.0.1:8260"}); err != nil {
		t.Fatal(err)
	}
	if _, running := ReadRunning(dir); running {
		t.Fatal("stale start identity was trusted")
	}
	if running, err := Stop(context.Background(), dir); running || err != nil {
		t.Fatalf("stale service stop = %v, %v", running, err)
	}
	if !session.ProcAlive(child.Process.Pid) {
		t.Fatal("stale service state stopped an unrelated process")
	}
	if _, err := os.Stat(statePath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale state was not removed: %v", err)
	}
}

// --listen takes any IP literal, binds exactly its family, and this host's
// clients reach a wildcard bind over loopback on the same port.
func TestListenAddressesAndLocalURL(t *testing.T) {
	for _, tc := range []struct{ in, listen, network, url string }{
		{"127.0.0.1:8260", "127.0.0.1:8260", "tcp4", "http://127.0.0.1:8260/"},
		{"localhost:8260", "127.0.0.1:8260", "tcp4", "http://127.0.0.1:8260/"},
		{"[::1]:8260", "[::1]:8260", "tcp6", "http://[::1]:8260/"},
		{"0.0.0.0:8275", "0.0.0.0:8275", "tcp4", "http://127.0.0.1:8275/"},
		{"[::]:8275", "[::]:8275", "tcp6", "http://[::1]:8275/"},
		{"192.0.2.10:8275", "192.0.2.10:8275", "tcp4", "http://192.0.2.10:8275/"},
		{"[2001:db8::1]:8275", "[2001:db8::1]:8275", "tcp6", "http://[2001:db8::1]:8275/"},
	} {
		got, err := ValidateListen(tc.in)
		if err != nil || got != tc.listen {
			t.Fatalf("ValidateListen(%q) = %q, %v; want %q", tc.in, got, err, tc.listen)
		}
		if n := listenNetwork(got); n != tc.network {
			t.Fatalf("listenNetwork(%q) = %q, want %q", got, n, tc.network)
		}
		if u := (DaemonState{Listen: got}).URL(); u != tc.url {
			t.Fatalf("URL for %q = %q, want %q", got, u, tc.url)
		}
		if loopback := strings.HasPrefix(tc.in, "127.") || strings.HasPrefix(tc.in, "localhost") || strings.HasPrefix(tc.in, "[::1]"); BeyondLoopback(got) == loopback {
			t.Fatalf("BeyondLoopback(%q) = %v", got, !loopback)
		}
	}
	for _, in := range []string{"example.com:8260", "uam.local:8260", ":8260", "127.0.0.1", "0.0.0.0:99999", "[fe80::1%eth0]:8260"} {
		if got, err := ValidateListen(in); err == nil {
			t.Fatalf("ValidateListen(%q) = %q, want an error", in, got)
		}
	}
	if _, err := ValidateListen("example.com:8260"); err == nil || !strings.Contains(err.Error(), "not an IP address") {
		t.Fatalf("host name error = %v", err)
	}
}

// uam web token set: an owner-chosen token replaces the file atomically, is
// what the service loads, and signs out every browser once the service runs
// with it (cookies are derived from the token, not held server-side).
func TestSetTokenValidatesAndReplacesTheFile(t *testing.T) {
	for _, bad := range []string{"", strings.Repeat("a", 23), strings.Repeat("a", 257), strings.Repeat("a", 12) + " " + strings.Repeat("a", 12),
		strings.Repeat("a", 24) + "\t", strings.Repeat("a", 24) + "\x01", strings.Repeat("a", 24) + "\x7f", strings.Repeat("a", 24) + "é"} {
		if err := ValidateToken(bad); err == nil {
			t.Fatalf("ValidateToken(%q) accepted", bad)
		} else if len(bad) >= 12 && strings.Contains(err.Error(), bad) {
			t.Fatalf("the error must not repeat the token: %v", err)
		}
	}
	for _, good := range []string{strings.Repeat("a", 24), strings.Repeat("~", 256), "Correct-Horse_Battery!Staple#42", testToken} {
		if err := ValidateToken(good); err != nil {
			t.Fatalf("ValidateToken(%q) = %v", good, err)
		}
	}
	dir := filepath.Join(t.TempDir(), "config")
	path := filepath.Join(dir, tokenFileName)
	old, err := LoadOrCreateToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetToken(path, "too-short"); err == nil {
		t.Fatal("a short token was set")
	}
	if got, err := LoadOrCreateToken(path); err != nil || got != old {
		t.Fatalf("a refused token changed the file: %q, %v", got, err)
	}
	const chosen = "Correct-Horse_Battery!Staple#42"
	if err := SetToken(path, chosen); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("token file = %v, %v; want a regular 0600 file", info, err)
	}
	if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("token directory = %v, %v; want 0700", info, err)
	}
	if got, err := LoadOrCreateToken(path); err != nil || got != chosen {
		t.Fatalf("LoadOrCreateToken after set = %q, %v", got, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}

	m, _, _ := newTestManager(t)
	before, err := NewServer(ServerConfig{Manager: m, Token: old, Assets: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewServer(ServerConfig{Manager: m, Token: chosen, Assets: fstest.MapFS{"index.html": {Data: []byte("x")}}})
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: cookieName, Value: sessionCookie(old, "127.0.0.1:8260", time.Now().Add(time.Hour).Unix())}
	for _, tc := range []struct {
		srv  *Server
		want int
	}{{before, http.StatusOK}, {after, http.StatusUnauthorized}} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8260/api/sessions", nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		tc.srv.ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("old browser login = %d, want %d", w.Code, tc.want)
		}
	}
}

func TestAcceptanceTokenFileRefusesReplacementAndRepairsPermissions(t *testing.T) {
	for _, tc := range []struct{ name, data string }{{"short", "bad"}, {"inner whitespace", strings.Repeat("z", 32) + " " + strings.Repeat("z", 32)}, {"control character", strings.Repeat("z", 32) + "\x01"}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tokenFileName)
			if err := os.WriteFile(path, []byte(tc.data), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreateToken(path); err == nil {
				t.Fatal("malformed token accepted")
			}
			data, err := os.ReadFile(path)
			if err != nil || string(data) != tc.data {
				t.Fatalf("malformed token silently replaced: %q, %v", data, err)
			}
		})
	}
	dir := t.TempDir()
	path := filepath.Join(dir, tokenFileName)
	if err := os.WriteFile(path, []byte(testToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadOrCreateToken(path); err != nil || got != testToken {
		t.Fatalf("existing token = %q, %v", got, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("token permissions = %v, %v", info, err)
	}
	link := filepath.Join(dir, "token-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(link); err == nil {
		t.Fatal("token symlink accepted")
	}
	if _, err := LoadOrCreateToken(filepath.Join(path, "token")); err == nil {
		t.Fatal("token with a non-directory parent accepted")
	}
}
