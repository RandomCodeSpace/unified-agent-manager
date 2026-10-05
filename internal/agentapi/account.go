package agentapi

import (
	"context"
	"errors"
)

var (
	// ErrSignedOut is returned, wrapped, by Provider.Check when the runtime
	// is installed but has no usable account sign-in.
	ErrSignedOut = errors.New("the provider is signed out")
	// ErrEnvAccount refuses a sign-in or sign-out while a token in the
	// service environment takes precedence over any stored sign-in.
	ErrEnvAccount = errors.New("a token in the service environment takes precedence")
	// ErrSignInRejected wraps the runtime's refusal of a sign-in or
	// sign-out, such as a token GitHub did not accept.
	ErrSignInRejected = errors.New("the sign-in was refused")
)

// Account sources: how a signed-in runtime gets its credential.
const (
	// AccountStored is a sign-in the runtime stored, by its own login
	// command or by AccountManager.SignIn.
	AccountStored = "stored"
	// AccountEnv is a token in a service environment variable (EnvVar).
	AccountEnv = "env"
	// AccountGitHubCLI is the GitHub CLI's own sign-in.
	AccountGitHubCLI = "gh-cli"
	// AccountOther is any other source the runtime reports.
	AccountOther = "other"
)

// Account is a provider runtime's sign-in, shared by all its conversations.
// It never carries a credential.
type Account struct {
	SignedIn bool   `json:"signed_in"`
	Login    string `json:"login,omitempty"`
	Host     string `json:"host,omitempty"`
	// Source is one of the Account* constants; "" while signed out.
	Source string `json:"source,omitempty"`
	// EnvVar names the service environment variable whose token takes
	// precedence over a stored sign-in; its value is never reported.
	EnvVar string `json:"env_var,omitempty"`
	// Message is the runtime's sanitized status or error text.
	Message string `json:"message,omitempty"`
	// Stored is false after a SignIn the runtime could not keep: it lasts
	// until the runtime stops.
	Stored *bool `json:"stored,omitempty"`
}

// AccountManager is implemented by a provider whose Capabilities.Account is
// true. A change applies to every conversation of the runtime.
type AccountManager interface {
	Account(ctx context.Context) (Account, error)
	// SignIn validates and stores token as the runtime's sign-in. The token
	// goes to the runtime only; it is never logged or kept.
	SignIn(ctx context.Context, token string) (Account, error)
	// SignOut removes the stored sign-in in effect.
	SignOut(ctx context.Context) (Account, error)
}

// DeviceCode is what a person enters at URL to approve a device sign-in.
type DeviceCode struct {
	URL  string `json:"verification_uri"`
	Code string `json:"user_code"`
}

// DeviceSignInManager is implemented by a provider whose
// Capabilities.DeviceSignIn is true: its runtime signs in through GitHub's
// device flow, so the person approves in their own browser and no token
// passes through here.
type DeviceSignInManager interface {
	// DeviceSignIn starts the flow, calls show once with the code to enter,
	// and returns once the sign-in is approved and in effect, refused,
	// expired, or ctx ends.
	DeviceSignIn(ctx context.Context, show func(DeviceCode)) (Account, error)
}
