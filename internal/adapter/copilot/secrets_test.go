package copilot

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// Made-up values; never a real secret.
const (
	testToken = "uam-test-token-0123456789abcdef"
	testKey   = "uam-test-key-fedcba9876543210"
	testKey2  = "uam-test-key2-00112233445566"
)

type secretClient struct {
	*fakeClient
	smu   sync.Mutex
	calls [][]string
	err   error
}

func (c *secretClient) AddSecretFilters(_ context.Context, values []string) error {
	c.smu.Lock()
	defer c.smu.Unlock()
	c.calls = append(c.calls, slices.Clone(values))
	return c.err
}

func (c *secretClient) registered() [][]string {
	c.smu.Lock()
	defer c.smu.Unlock()
	return slices.Clone(c.calls)
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *lockedBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *lockedBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// secretProvider starts a new secretClient on each start, failing its
// registration with err, and captures the log.
func secretProvider(t *testing.T, err error) (*webProvider, *[]*secretClient, *atomic.Int32, *lockedBuffer) {
	t.Helper()
	var buf lockedBuffer
	old := log.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { log.SetLogger(old) })
	var mu sync.Mutex
	clients := &[]*secretClient{}
	p := newWebProvider(func() (sdkClient, error) {
		c := &secretClient{fakeClient: &fakeClient{}, err: err}
		mu.Lock()
		*clients = append(*clients, c)
		mu.Unlock()
		return c, nil
	}, time.Hour)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	var n atomic.Int32
	// Set directly: SetSecrets also registers with a running CLI, in the
	// background, which these tests check on their own.
	p.secrets, p.secretsRegistered = []string{testToken, "short", ""}, func() { n.Add(1) }
	p.custom = []agentapi.CustomModel{{Name: "a", ModelID: "m1", APIKey: testKey}, {Name: "a", ModelID: "m2", APIKey: testKey}, {Name: "b", ModelID: "m3", APIKey: "tiny"}}
	return p, clients, &n, &buf
}

// Each CLI start, the first and one after a restart, registers uam's token
// and the custom keys, once each, and skips values under minSecretLen.
func TestSecretsRegisterOnStartAndRestart(t *testing.T) {
	p, clients, n, _ := secretProvider(t, nil)
	ctx := context.Background()
	if _, err := p.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	if err := p.RestartRuntime(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ensureStarted(ctx); err != nil {
		t.Fatal(err)
	}
	want := []string{testKey, testToken}
	if len(*clients) != 2 {
		t.Fatalf("started %d CLIs, want 2", len(*clients))
	}
	for i, c := range *clients {
		if got := c.registered(); len(got) != 1 || !slices.Equal(got[0], want) {
			t.Fatalf("CLI %d registered %d calls; want one with the token and the key only", i, len(got))
		}
	}
	if n.Load() != 2 {
		t.Fatalf("counted %d registrations, want 2", n.Load())
	}
}

// A key added while the CLI runs, or uam's secrets given late, reach the
// running CLI.
func TestSecretsRegisterOnKeyChangeWhileRunning(t *testing.T) {
	p, clients, _, _ := secretProvider(t, nil)
	if _, err := p.ensureStarted(context.Background()); err != nil {
		t.Fatal(err)
	}
	c := (*clients)[0]
	p.SetCustomModels([]agentapi.CustomModel{{Name: "a", ModelID: "m1", APIKey: testKey2}})
	waitFor(t, "the new key registered", func() bool {
		calls := c.registered()
		return len(calls) == 2 && slices.Contains(calls[1], testKey2) && !slices.Contains(calls[1], testKey)
	})
	var n atomic.Int32
	p.SetSecrets([]string{testToken}, func() { n.Add(1) })
	waitFor(t, "the secrets registered", func() bool { return len(c.registered()) == 3 && n.Load() == 1 })
}

// A refused registration starts the CLI anyway, counts nothing and logs
// no value, even one the error repeats.
func TestSecretRegistrationFailureDoesNotBlockOrLeak(t *testing.T) {
	p, clients, n, buf := secretProvider(t, errors.New("refused "+testToken+" and "+testKey))
	if _, err := p.ensureStarted(context.Background()); err != nil {
		t.Fatalf("start failed on a refused registration: %v", err)
	}
	if len((*clients)[0].registered()) != 1 || n.Load() != 0 {
		t.Fatalf("calls %d, counted %d; want 1 call, 0 counted", len((*clients)[0].registered()), n.Load())
	}
	out := buf.String()
	if !strings.Contains(out, "did not register secret values") {
		t.Fatal("the refusal was not logged")
	}
	for _, v := range []string{testToken, testKey, "uam-test-"} {
		if strings.Contains(out, v) {
			t.Fatal("the log carries a secret value")
		}
	}
}

// A provider with nothing long enough to register makes no call.
func TestSecretsNothingToRegister(t *testing.T) {
	p, clients, _, _ := secretProvider(t, nil)
	p.secrets, p.custom = []string{"short"}, nil
	if _, err := p.ensureStarted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := (*clients)[0].registered(); len(got) != 0 {
		t.Fatalf("registered %d calls, want none", len(got))
	}
}
