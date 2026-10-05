package copilot

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// fakeLogin replaces the login command with script, run by sh, and counts runs.
func fakeLogin(t *testing.T, script string) *int {
	t.Helper()
	runs := 0
	old := loginCommand
	loginCommand = func(ctx context.Context) (*exec.Cmd, error) {
		runs++
		return exec.CommandContext(ctx, "sh", "-c", script), nil
	}
	t.Cleanup(func() { loginCommand = old })
	return &runs
}

const devicePromptScript = `echo "To authenticate, visit https://github.com/login/device and enter code AB12-CD34"; echo "Waiting for authorization..."; `

func TestDeviceSignInShowsTheCodeAndRestartsTheCLI(t *testing.T) {
	p, fc := accountProvider(t, nil)
	fc.status = copilot.GetAuthStatusResponse{StatusMessage: copilot.String("Not authenticated")}
	if _, err := p.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
	fakeLogin(t, devicePromptScript+`echo "Signed in successfully as octo."`)
	var shown []agentapi.DeviceCode
	// The CLI stores the sign-in; the restarted runtime reads it.
	fc.status = copilot.GetAuthStatusResponse{IsAuthenticated: true, AuthType: copilot.String("user"), Login: copilot.String("octo"), Host: copilot.String(githubHost)}
	acct, err := p.DeviceSignIn(context.Background(), func(c agentapi.DeviceCode) { shown = append(shown, c) })
	if err != nil || !acct.SignedIn || acct.Login != "octo" || acct.Source != agentapi.AccountStored {
		t.Fatalf("DeviceSignIn = %+v, %v", acct, err)
	}
	if len(shown) != 1 || shown[0] != (agentapi.DeviceCode{URL: "https://github.com/login/device", Code: "AB12-CD34"}) {
		t.Fatalf("shown = %+v", shown)
	}
	fc.mu.Lock()
	started, stopped := fc.started, fc.stopped
	fc.mu.Unlock()
	if started != 2 || stopped != 1 {
		t.Fatalf("CLI started %d, stopped %d times; want a restart", started, stopped)
	}
}

func TestDeviceSignInFailuresSayWhy(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"not stored", devicePromptScript + `echo "Login succeeded, but the token was not saved. Install a system keychain or rerun login and accept plaintext storage." >&2; exit 1`, "no system keychain"},
		{"refused", devicePromptScript + `echo "Login failed: access_denied" >&2; exit 1`, "access_denied"},
		{"no code", `echo "something else"; exit 0`, "showed no device code"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := accountProvider(t, nil)
			fakeLogin(t, tc.script)
			_, err := p.DeviceSignIn(context.Background(), func(agentapi.DeviceCode) {})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DeviceSignIn error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDeviceSignInIsRefusedBeforeTheCLIRuns(t *testing.T) {
	p, _ := accountProvider(t, map[string]string{"GH_TOKEN": "secret-value"})
	runs := fakeLogin(t, devicePromptScript)
	if _, err := p.DeviceSignIn(context.Background(), func(agentapi.DeviceCode) {}); !errors.Is(err, agentapi.ErrEnvAccount) {
		t.Fatalf("with an environment token: %v", err)
	}
	lookupEnv = func(string) (string, bool) { return "", false }
	p.mu.Lock()
	p.convs[&conversation{}] = struct{}{}
	p.mu.Unlock()
	if _, err := p.DeviceSignIn(context.Background(), func(agentapi.DeviceCode) {}); !errors.Is(err, agentapi.ErrSignInRejected) || !strings.Contains(err.Error(), "close the open Copilot tasks") {
		t.Fatalf("with an open conversation: %v", err)
	}
	p.mu.Lock()
	clear(p.convs)
	p.mu.Unlock()
	if *runs != 0 {
		t.Fatalf("login ran %d times", *runs)
	}
}

func TestDeviceSignInCanceledStopsTheCLI(t *testing.T) {
	p, _ := accountProvider(t, nil)
	fakeLogin(t, devicePromptScript+`exec sleep 60`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.DeviceSignIn(ctx, func(agentapi.DeviceCode) { cancel() })
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled DeviceSignIn = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("DeviceSignIn kept waiting after cancel")
	}
}
