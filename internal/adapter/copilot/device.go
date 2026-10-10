package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
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

// errTokenNotSaved is the CLI approving a sign-in it could not store: the
// system has no keychain and plaintext storage is off.
var errTokenNotSaved = fmt.Errorf("%w: GitHub approved the sign-in, but Copilot could not store it: this server has no system keychain", agentapi.ErrSignInRejected)

// noKeychainNotice comes with the second code after errTokenNotSaved.
const noKeychainNotice = "This server has no system keychain, so Copilot will keep the sign-in in its own settings folder, readable only by this user. Approve this new code to finish."

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
// token passes through uam. With no system keychain the CLI cannot store an
// approved sign-in: its plaintext storage is then turned on and show gets a
// second code. The runtime reads its sign-in only when it starts, so the CLI
// is restarted afterwards. That would end open conversations, so it is
// refused while any is open.
func (p *webProvider) DeviceSignIn(ctx context.Context, show func(agentapi.DeviceCode)) (agentapi.Account, error) {
	if name := envTokenVar(); name != "" {
		return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrEnvAccount, name)
	}
	if p.openConversations() > 0 {
		return agentapi.Account{}, fmt.Errorf("%w: close the open Copilot tasks first; signing in restarts Copilot", agentapi.ErrSignInRejected)
	}
	err := deviceLogin(ctx, show)
	if errors.Is(err, errTokenNotSaved) {
		if serr := allowPlaintextToken(); serr != nil {
			return agentapi.Account{}, fmt.Errorf("%w, and %s. Set \"storeTokenPlaintext\": true in it, then sign in again", errTokenNotSaved, serr)
		}
		log.Info("copilot sign-in: no system keychain, turned on plaintext token storage")
		err = deviceLogin(ctx, func(c agentapi.DeviceCode) {
			c.Notice = noKeychainNotice
			show(c)
		})
	}
	if err != nil {
		return agentapi.Account{}, err
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

// deviceLogin runs the login command once, calling show with its code.
func deviceLogin(ctx context.Context, show func(agentapi.DeviceCode)) error {
	lctx, cancel := context.WithTimeout(ctx, deviceSignInTimeout)
	defer cancel()
	cmd, err := loginCommand(lctx)
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr boundedBuffer
	cmd.Stderr = &stderr
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start copilot login: %s", errText(err))
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
		return ctx.Err()
	case errors.Is(lctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("%w: the code expired; start again", agentapi.ErrSignInRejected)
	case werr != nil:
		return loginFailure(stderr.String(), werr)
	case !shown:
		return errors.New("copilot login showed no device code")
	}
	return nil
}

// copilotSettings is the CLI's settings file: settings.json in COPILOT_HOME,
// else in ~/.copilot.
func copilotSettings() (string, error) {
	home, _ := lookupEnv("COPILOT_HOME")
	if home == "" {
		dir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(dir, ".copilot")
	}
	return filepath.Join(home, "settings.json"), nil
}

// allowPlaintextToken turns on the CLI's storeTokenPlaintext setting and
// keeps its others, so a sign-in on a system with no keychain is stored in
// the CLI's own config file, which only its user can read.
func allowPlaintextToken() error {
	path, err := copilotSettings()
	if err != nil {
		return fmt.Errorf("its settings file could not be found: %s", errText(err))
	}
	settings := map[string]json.RawMessage{}
	mode := fs.FileMode(0o600)
	data, err := os.ReadFile(path) // #nosec G304 -- the CLI's own settings file in the service user's Copilot home.
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("%s could not be read", path)
	default:
		if json.Unmarshal(data, &settings) != nil || settings == nil {
			return fmt.Errorf("%s is not plain JSON", path)
		}
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	}
	settings["storeTokenPlaintext"] = json.RawMessage("true")
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("%s could not be written", path)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "settings.json.tmp.*")
	if err != nil {
		return fmt.Errorf("%s could not be written", path)
	}
	defer func() { _ = os.Remove(tmp.Name()) }() // gone after the rename; left only by a failure
	_, werr := tmp.Write(append(out, '\n'))
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Chmod(tmp.Name(), mode) != nil || os.Rename(tmp.Name(), path) != nil {
		return fmt.Errorf("%s could not be written", path)
	}
	return nil
}

// loginFailure turns the login command's error output into the reason shown.
func loginFailure(out string, err error) error {
	if strings.Contains(out, "token was not saved") {
		return errTokenNotSaved
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
