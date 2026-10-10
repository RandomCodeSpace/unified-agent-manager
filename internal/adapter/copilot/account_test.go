package copilot

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
)

// accountFake adds the account RPCs to fakeClient.
type accountFake struct {
	*fakeClient
	status   copilot.GetAuthStatusResponse
	current  rpc.AccountGetCurrentAuthResult
	loginErr error
	logins   []rpc.AccountLoginRequest
	logouts  []rpc.AccountLogoutRequest
}

func (f *accountFake) AuthStatus(context.Context) (*copilot.GetAuthStatusResponse, error) {
	st := f.status
	return &st, nil
}

func (f *accountFake) CurrentAuth(context.Context) (*rpc.AccountGetCurrentAuthResult, error) {
	cur := f.current
	return &cur, nil
}

func (f *accountFake) Login(_ context.Context, req *rpc.AccountLoginRequest) (*rpc.AccountLoginResult, error) {
	f.logins = append(f.logins, *req)
	if f.loginErr != nil {
		return nil, f.loginErr
	}
	f.status = copilot.GetAuthStatusResponse{IsAuthenticated: true, AuthType: copilot.String("user"), Login: copilot.String("octo"), Host: copilot.String(githubHost)}
	return &rpc.AccountLoginResult{StoredInVault: true}, nil
}

func (f *accountFake) Logout(_ context.Context, req *rpc.AccountLogoutRequest) error {
	f.logouts = append(f.logouts, *req)
	f.status = copilot.GetAuthStatusResponse{IsAuthenticated: false, StatusMessage: copilot.String("Not authenticated")}
	return nil
}

func accountProvider(t *testing.T, env map[string]string) (*webProvider, *accountFake) {
	t.Helper()
	old := lookupEnv
	lookupEnv = func(name string) (string, bool) { v, ok := env[name]; return v, ok }
	t.Cleanup(func() { lookupEnv = old })
	fc := &accountFake{fakeClient: &fakeClient{}}
	p := newWebProvider(func() (sdkClient, error) { return fc, nil }, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	return p, fc
}

func TestAccountReportsSourceAndEnvVarWithoutCredentials(t *testing.T) {
	p, fc := accountProvider(t, map[string]string{"GITHUB_TOKEN": "secret-value", "GH_TOKEN": ""})
	fc.status = copilot.GetAuthStatusResponse{IsAuthenticated: true, AuthType: copilot.String("gh-cli"), Login: copilot.String("octo"), Host: copilot.String(githubHost), StatusMessage: copilot.String("octo (via gh)")}
	acct, err := p.Account(context.Background())
	if err != nil || !acct.SignedIn || acct.Source != agentapi.AccountGitHubCLI || acct.Login != "octo" || acct.EnvVar != "GITHUB_TOKEN" {
		t.Fatalf("Account = %+v, %v", acct, err)
	}

	// Signed out by a bad environment token: the runtime's reason is shown,
	// and the variable is named by the runtime's current auth.
	fc.status = copilot.GetAuthStatusResponse{StatusMessage: copilot.String("Not authenticated")}
	fc.current = rpc.AccountGetCurrentAuthResult{AuthErrors: []string{"Failed to fetch PAT user login (401): Bad credentials"}}
	acct, err = p.Account(context.Background())
	if err != nil || acct.SignedIn || acct.Source != "" || acct.Message != "Failed to fetch PAT user login (401): Bad credentials" {
		t.Fatalf("signed out Account = %+v, %v", acct, err)
	}
	if !p.signedOut(context.Background()) {
		t.Fatal("signedOut = false while signed out")
	}
}

func TestSignInSendsTheTokenOnlyToTheRuntime(t *testing.T) {
	p, fc := accountProvider(t, nil)
	fc.status = copilot.GetAuthStatusResponse{StatusMessage: copilot.String("Not authenticated")}
	const token = "github_pat_example"
	fc.loginErr = errors.New("Request account.login failed with message: token " + token + " was refused: 401 Unauthorized")
	_, err := p.SignIn(context.Background(), "  "+token+"\n")
	if err == nil || strings.Contains(err.Error(), token) || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("refused SignIn error = %v", err)
	}
	if len(fc.logins) != 1 || fc.logins[0].Token != token || fc.logins[0].Host != githubHost || fc.logins[0].Login != nil {
		t.Fatalf("login requests = %+v", fc.logins)
	}

	fc.loginErr = nil
	acct, err := p.SignIn(context.Background(), token)
	if err != nil || !acct.SignedIn || acct.Source != agentapi.AccountStored || acct.Login != "octo" || acct.Stored == nil || !*acct.Stored {
		t.Fatalf("SignIn = %+v, %v", acct, err)
	}

	for _, bad := range []string{"", "two words", strings.Repeat("x", maxTokenBytes+1)} {
		if _, err := p.SignIn(context.Background(), bad); !errors.Is(err, agentapi.ErrSignInRejected) {
			t.Fatalf("SignIn(%q) = %v", bad, err)
		}
	}
	if len(fc.logins) != 2 {
		t.Fatalf("malformed tokens reached the runtime: %d logins", len(fc.logins))
	}
}

func TestEnvironmentTokenBlocksSignInAndSignOut(t *testing.T) {
	p, fc := accountProvider(t, map[string]string{"COPILOT_GITHUB_TOKEN": "x"})
	if _, err := p.SignIn(context.Background(), "github_pat_example"); !errors.Is(err, agentapi.ErrEnvAccount) || !strings.HasSuffix(err.Error(), "COPILOT_GITHUB_TOKEN") {
		t.Fatalf("SignIn under an environment token = %v", err)
	}
	if _, err := p.SignOut(context.Background()); !errors.Is(err, agentapi.ErrEnvAccount) {
		t.Fatalf("SignOut under an environment token = %v", err)
	}
	if len(fc.logins) != 0 || len(fc.logouts) != 0 {
		t.Fatalf("runtime called: logins %d, logouts %d", len(fc.logins), len(fc.logouts))
	}
}

// Only a stored sign-in is signed out; the GitHub CLI's own sign-in is left
// alone.
func TestSignOutRemovesOnlyAStoredSignIn(t *testing.T) {
	p, fc := accountProvider(t, nil)
	fc.status = copilot.GetAuthStatusResponse{IsAuthenticated: true, AuthType: copilot.String("gh-cli"), Login: copilot.String("octo")}
	if _, err := p.SignOut(context.Background()); !errors.Is(err, agentapi.ErrSignInRejected) || len(fc.logouts) != 0 {
		t.Fatalf("SignOut of a gh sign-in = %v, logouts %d", err, len(fc.logouts))
	}
	fc.status = copilot.GetAuthStatusResponse{IsAuthenticated: true, AuthType: copilot.String("user"), Login: copilot.String("octo"), Host: copilot.String(githubHost)}
	acct, err := p.SignOut(context.Background())
	if err != nil || acct.SignedIn || len(fc.logouts) != 1 {
		t.Fatalf("SignOut = %+v, %v, logouts %d", acct, err, len(fc.logouts))
	}
	user, ok := fc.logouts[0].AuthInfo.(*rpc.UserAuthInfo)
	if !ok || user.Login != "octo" || user.Host != githubHost {
		t.Fatalf("logout request = %#v", fc.logouts[0].AuthInfo)
	}
}

func TestAccountRestartsASignedOutCLIToFindALoginMadeElsewhere(t *testing.T) {
	p, fc := accountProvider(t, nil)
	started := func() int {
		fc.mu.Lock()
		defer fc.mu.Unlock()
		return fc.started
	}
	fc.status = copilot.GetAuthStatusResponse{StatusMessage: copilot.String("Not authenticated")}
	if acct, err := p.Account(context.Background()); err != nil || acct.SignedIn {
		t.Fatalf("Account = %+v, %v", acct, err)
	}
	// The signed-out CLI was restarted to read a sign-in made outside UAM.
	if n := started(); n != 2 {
		t.Fatalf("CLI started %d times, want 2", n)
	}
	// Within signInRecheck, or with a conversation open, it is not restarted again.
	if _, err := p.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.signInChecked = time.Time{}
	p.convs[&conversation{}] = struct{}{}
	p.mu.Unlock()
	if _, err := p.Account(context.Background()); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	clear(p.convs)
	p.mu.Unlock()
	if n := started(); n != 2 {
		t.Fatalf("CLI started %d times, want no more restarts", n)
	}
	// Signed in on the server: the next read reports it.
	fc.status = copilot.GetAuthStatusResponse{IsAuthenticated: true, AuthType: copilot.String("user"), Login: copilot.String("octo"), Host: copilot.String(githubHost)}
	if acct, err := p.Account(context.Background()); err != nil || !acct.SignedIn || acct.Login != "octo" {
		t.Fatalf("Account = %+v, %v", acct, err)
	}
	// A signed-in CLI is not restarted.
	if n := started(); n != 2 {
		t.Fatalf("CLI started %d times, want 2", n)
	}
}
