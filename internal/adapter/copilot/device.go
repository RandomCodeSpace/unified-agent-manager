package copilot

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const (
	// deviceSignInTimeout is how long GitHub keeps a device code.
	deviceSignInTimeout = 15 * time.Minute
	// maxLoginOutput bounds what is kept of the login command's error output.
	maxLoginOutput = 8 << 10
	// restartReason ends conversations still open when the CLI restarts.
	restartReason = "Copilot restarted for a new sign-in"
)

// devicePrompt is the line `copilot login --device-code` prints:
// "To authenticate, visit <uri> and enter code <code>".
var devicePrompt = regexp.MustCompile(`visit (https://[^\s]+) and enter code ([A-Z0-9]{4,}-[A-Z0-9]{4,})`)

// loginCommand runs `copilot login --device-code` with the CLI and
// environment the runtime uses (newSDKClient), so the sign-in is stored where
// the runtime reads it. Tests replace it.
var loginCommand = func(ctx context.Context) (*exec.Cmd, error) {
	path, err := resolveCopilot()
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, "login", "--device-code") // #nosec G204 -- the service owner's copilot from PATH with fixed arguments.
	cmd.Env = append(os.Environ(), "HISTFILE="+os.DevNull, "HISTSIZE=0")
	return cmd, nil
}

// DeviceSignIn signs the CLI in through GitHub's device flow. show gets the
// code to enter at GitHub; the person approves in their own browser and no
// token passes through uam. The runtime reads its sign-in only when it
// starts, so the CLI is restarted afterwards. That would end open
// conversations, so it is refused while any is open.
func (p *webProvider) DeviceSignIn(ctx context.Context, show func(agentapi.DeviceCode)) (agentapi.Account, error) {
	if name := envTokenVar(); name != "" {
		return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrEnvAccount, name)
	}
	if p.openConversations() > 0 {
		return agentapi.Account{}, fmt.Errorf("%w: close the open Copilot tasks first; signing in restarts Copilot", agentapi.ErrSignInRejected)
	}
	lctx, cancel := context.WithTimeout(ctx, deviceSignInTimeout)
	defer cancel()
	cmd, err := loginCommand(lctx)
	if err != nil {
		return agentapi.Account{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agentapi.Account{}, err
	}
	var stderr boundedBuffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return agentapi.Account{}, fmt.Errorf("start copilot login: %s", errText(err))
	}
	shown := false
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		if m := devicePrompt.FindStringSubmatch(sc.Text()); m != nil && !shown {
			shown = true
			show(agentapi.DeviceCode{URL: m[1], Code: m[2]})
		}
	}
	werr := cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return agentapi.Account{}, ctx.Err()
	case errors.Is(lctx.Err(), context.DeadlineExceeded):
		return agentapi.Account{}, fmt.Errorf("%w: the code expired; start again", agentapi.ErrSignInRejected)
	case werr != nil:
		return agentapi.Account{}, loginFailure(stderr.String(), werr)
	case !shown:
		return agentapi.Account{}, errors.New("copilot login showed no device code")
	}
	if err := p.restartClient(ctx); err != nil {
		log.Warn("restart copilot CLI after sign-in failed", "error", err)
	}
	p.signInChanged()
	ac, err := p.accountClient(ctx)
	if err != nil {
		return agentapi.Account{}, err
	}
	return readAccount(ctx, ac)
}

// loginFailure turns the login command's error output into the reason shown.
func loginFailure(out string, err error) error {
	if strings.Contains(out, "token was not saved") {
		return fmt.Errorf("%w: GitHub approved the sign-in, but Copilot could not store it: this server has no system keychain. Install one, or set \"storeTokenPlaintext\": true in ~/.copilot/settings.json, then sign in again", agentapi.ErrSignInRejected)
	}
	for line := range strings.Lines(out) {
		if msg, ok := strings.CutPrefix(strings.TrimSpace(line), "Login failed: "); ok {
			return fmt.Errorf("%w: %s", agentapi.ErrSignInRejected, cleanText(msg))
		}
	}
	return fmt.Errorf("copilot login: %s", exitText(err))
}

func (p *webProvider) openConversations() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.convs)
}

// restartClient stops the running CLI so the next call starts one that reads
// the current sign-in. Conversations still open end like on a CLI failure.
func (p *webProvider) restartClient(ctx context.Context) error {
	p.mu.Lock()
	c := p.detachLocked()
	if c == nil {
		p.mu.Unlock()
		return nil
	}
	convs := p.takeConvs()
	p.mu.Unlock()
	for _, conv := range convs {
		conv.exit(restartReason)
	}
	return p.stopDetached(ctx, c)
}

// detachLocked clears the running client and its watchdog, so the next call
// starts a new CLI, and returns it; nil when none runs. The caller holds mu.
func (p *webProvider) detachLocked() sdkClient {
	c := p.client
	if c != nil {
		p.client, p.outdated = nil, false
		close(p.stop)
		p.stop = nil
	}
	return c
}

// stopDetached stops a client detachLocked returned.
func (p *webProvider) stopDetached(ctx context.Context, c sdkClient) error {
	err := stopClient(ctx, c)
	if rerr := p.releaseClientUsage(c); rerr != nil {
		log.Warn("release stopped copilot usage ownership failed", "error", rerr)
	}
	return err
}

// boundedBuffer keeps the first maxLoginOutput bytes written to it.
type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if room := maxLoginOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
