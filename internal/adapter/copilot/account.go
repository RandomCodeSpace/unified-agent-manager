package copilot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
)

const (
	githubHost      = "https://github.com"
	maxTokenBytes   = 1024
	webAuthTimeout  = 10 * time.Second
	webLoginTimeout = 30 * time.Second
)

// tokenEnvVars are the environment variables the CLI reads a token from, in
// its order of precedence over a stored sign-in.
var tokenEnvVars = []string{"COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"}

// lookupEnv is os.LookupEnv; tests replace it. The CLI inherits the service
// environment (newSDKClient), so the service sees the variables it sees.
var lookupEnv = os.LookupEnv

// accountClient is the account part of the SDK client. A client without it
// (a test fake) reports no account.
type accountClient interface {
	AuthStatus(ctx context.Context) (*copilot.GetAuthStatusResponse, error)
	CurrentAuth(ctx context.Context) (*rpc.AccountGetCurrentAuthResult, error)
	Login(ctx context.Context, req *rpc.AccountLoginRequest) (*rpc.AccountLoginResult, error)
	Logout(ctx context.Context, req *rpc.AccountLogoutRequest) error
}

func (a sdkClientAdapter) AuthStatus(ctx context.Context) (*copilot.GetAuthStatusResponse, error) {
	return a.c.GetAuthStatus(ctx)
}

func (a sdkClientAdapter) CurrentAuth(ctx context.Context) (*rpc.AccountGetCurrentAuthResult, error) {
	return a.c.RPC.Account.GetCurrentAuth(ctx)
}

func (a sdkClientAdapter) Login(ctx context.Context, req *rpc.AccountLoginRequest) (*rpc.AccountLoginResult, error) {
	return a.c.RPC.Account.Login(ctx, req)
}

func (a sdkClientAdapter) Logout(ctx context.Context, req *rpc.AccountLogoutRequest) error {
	_, err := a.c.RPC.Account.Logout(ctx, req)
	return err
}

func (p *webProvider) accountClient(ctx context.Context) (accountClient, error) {
	client, err := p.ensureStarted(ctx)
	if err != nil {
		return nil, err
	}
	ac, ok := client.(accountClient)
	if !ok {
		return nil, agentapi.ErrUnsupported
	}
	return ac, nil
}

// signedOut reports whether the running CLI says it has no sign-in. A CLI
// that cannot start or answer is left to the calls that need it.
func (p *webProvider) signedOut(ctx context.Context) bool {
	ac, err := p.accountClient(ctx)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, webAuthTimeout)
	defer cancel()
	st, err := ac.AuthStatus(ctx)
	return err == nil && st != nil && !st.IsAuthenticated
}

// Account reads the CLI's sign-in. No credential leaves this function.
func (p *webProvider) Account(ctx context.Context) (agentapi.Account, error) {
	ac, err := p.accountClient(ctx)
	if err != nil {
		return agentapi.Account{}, err
	}
	return readAccount(ctx, ac)
}

func readAccount(ctx context.Context, ac accountClient) (agentapi.Account, error) {
	ctx, cancel := context.WithTimeout(ctx, webAuthTimeout)
	defer cancel()
	st, err := ac.AuthStatus(ctx)
	if err != nil {
		return agentapi.Account{}, fmt.Errorf("read the Copilot sign-in: %s", rpcText(err))
	}
	out := agentapi.Account{SignedIn: st.IsAuthenticated, EnvVar: envTokenVar()}
	if st.IsAuthenticated {
		out.Login = cleanText(deref(st.Login))
		out.Host = cleanText(deref(st.Host))
		out.Source = accountSource(deref(st.AuthType))
		out.Message = cleanText(deref(st.StatusMessage))
	}
	// The current auth names the variable a token came from and says why a
	// sign-in failed; its credential fields are never read.
	if cur, err := ac.CurrentAuth(ctx); err == nil && cur != nil {
		if env, ok := cur.AuthInfo.(*rpc.EnvAuthInfo); ok && env.EnvVar != "" {
			out.EnvVar = cleanText(env.EnvVar)
		}
		if !st.IsAuthenticated && len(cur.AuthErrors) > 0 {
			out.Message = cleanText(strings.Join(cur.AuthErrors, "; "))
		}
	}
	if !st.IsAuthenticated && out.Message == "" {
		out.Message = cleanText(deref(st.StatusMessage))
	}
	return out, nil
}

// SignIn sends token to the CLI's account.login, which validates it with
// GitHub and stores it. The token is never logged, kept or echoed: error
// text has it removed before it leaves.
func (p *webProvider) SignIn(ctx context.Context, token string) (agentapi.Account, error) {
	token = strings.TrimSpace(token)
	if err := checkToken(token); err != nil {
		return agentapi.Account{}, err
	}
	if name := envTokenVar(); name != "" {
		return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrEnvAccount, name)
	}
	ac, err := p.accountClient(ctx)
	if err != nil {
		return agentapi.Account{}, err
	}
	lctx, cancel := context.WithTimeout(ctx, webLoginTimeout)
	res, err := ac.Login(lctx, &rpc.AccountLoginRequest{Host: githubHost, Token: token})
	cancel()
	if err != nil {
		msg := strings.ReplaceAll(err.Error(), token, "[token]")
		if isRPCError(err) {
			return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrSignInRejected, rpcText(errors.New(msg)))
		}
		return agentapi.Account{}, fmt.Errorf("sign in to Copilot: %s", rpcText(errors.New(msg)))
	}
	p.signInChanged()
	out, err := readAccount(ctx, ac)
	if err == nil && res != nil {
		stored := res.StoredInVault
		out.Stored = &stored
	}
	return out, err
}

// SignOut removes the stored sign-in in effect. Only a stored sign-in can be
// removed here: an environment token or the GitHub CLI's sign-in belong to
// the server's owner.
func (p *webProvider) SignOut(ctx context.Context) (agentapi.Account, error) {
	if name := envTokenVar(); name != "" {
		return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrEnvAccount, name)
	}
	ac, err := p.accountClient(ctx)
	if err != nil {
		return agentapi.Account{}, err
	}
	cur, err := readAccount(ctx, ac)
	if err != nil || !cur.SignedIn {
		return cur, err
	}
	if cur.Source != agentapi.AccountStored {
		return agentapi.Account{}, fmt.Errorf("%w: only a sign-in stored by Copilot can be signed out here", agentapi.ErrSignInRejected)
	}
	lctx, cancel := context.WithTimeout(ctx, webLoginTimeout)
	err = ac.Logout(lctx, &rpc.AccountLogoutRequest{AuthInfo: &rpc.UserAuthInfo{Host: cmp.Or(cur.Host, githubHost), Login: cur.Login}})
	cancel()
	if err != nil {
		if isRPCError(err) {
			return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrSignInRejected, rpcText(err))
		}
		return agentapi.Account{}, fmt.Errorf("sign out of Copilot: %s", rpcText(err))
	}
	p.signInChanged()
	return readAccount(ctx, ac)
}

// signInChanged drops the quota snapshots of the account before.
func (p *webProvider) signInChanged() {
	p.quotaMu.Lock()
	p.live = nil
	p.quotaMu.Unlock()
}

func checkToken(token string) error {
	if token == "" {
		return fmt.Errorf("%w: enter a token", agentapi.ErrSignInRejected)
	}
	if len(token) > maxTokenBytes || strings.IndexFunc(token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("%w: that does not look like a GitHub token", agentapi.ErrSignInRejected)
	}
	return nil
}

// envTokenVar names the first token variable set in the service
// environment, in the CLI's order; "" when none is.
func envTokenVar() string {
	for _, name := range tokenEnvVars {
		if v, ok := lookupEnv(name); ok && v != "" {
			return name
		}
	}
	return ""
}

func accountSource(authType string) string {
	switch authType {
	case string(rpc.AuthInfoTypeUser):
		return agentapi.AccountStored
	case string(rpc.AuthInfoTypeEnv):
		return agentapi.AccountEnv
	case string(rpc.AuthInfoTypeGhCLI):
		return agentapi.AccountGitHubCLI
	default:
		return agentapi.AccountOther
	}
}

// rpcText is an RPC error's message without the JSON-RPC wrapping.
func rpcText(err error) string {
	msg := err.Error()
	if _, after, ok := strings.Cut(msg, "failed with message: "); ok {
		msg = after
	}
	return cleanText(msg)
}

func cleanText(s string) string {
	return clip(strings.TrimSpace(displaytext.Sanitize(s)), maxErrorText)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
