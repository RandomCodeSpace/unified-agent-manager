package copilot

import (
	"cmp"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

// secretFiltering lets a CLI accept secrets.addFilterValues; without it the
// CLI refuses the call. Measured on CLI 1.0.108, a registered value is
// masked in the CLI's own process log only: the session journal, session
// events, the tool output the model reads and the OTel export keep it.
const secretFiltering = "COPILOT_ENABLE_SECRET_FILTERING"

// minSecretLen is the shortest value registered: a short one would mask
// ordinary words in the log.
const minSecretLen = 16

// secretTimeout bounds one registration, so a CLI that does not answer
// delays a start by at most this.
const secretTimeout = 5 * time.Second

// SetSecrets gives the provider uam's own secret values. They and the
// custom models' keys are registered with each CLI it starts and with the
// running one; registered is called once per registration the CLI accepted.
func (p *webProvider) SetSecrets(values []string, registered func()) {
	p.customMu.Lock()
	p.secrets, p.secretsRegistered = slices.Clone(values), registered
	p.customMu.Unlock()
	go p.redactRunning()
}

// secretValues lists the values to register: uam's own and each custom
// model's key, saved or from the environment, each long enough.
func (p *webProvider) secretValues() ([]string, func()) {
	p.customMu.Lock()
	values := slices.Clone(p.secrets)
	for _, m := range p.custom {
		values = append(values, cmp.Or(m.APIKey, os.Getenv(m.APIKeyEnv)))
	}
	registered := p.secretsRegistered
	p.customMu.Unlock()
	values = slices.DeleteFunc(values, func(v string) bool { return len(v) < minSecretLen })
	slices.Sort(values)
	return slices.Compact(values), registered
}

// redactSecrets registers the secret values with c. A failure is logged,
// without the values, and never stops the caller.
func (p *webProvider) redactSecrets(ctx context.Context, c sdkClient) {
	r, ok := c.(interface {
		AddSecretFilters(context.Context, []string) error
	})
	values, registered := p.secretValues()
	if !ok || len(values) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), secretTimeout)
	defer cancel()
	if err := r.AddSecretFilters(ctx, values); err != nil {
		msg := err.Error()
		for _, v := range values {
			msg = strings.ReplaceAll(msg, v, "******")
		}
		log.Warn("copilot CLI did not register secret values for redaction", "values", len(values), "error", errText(errors.New(msg)))
		return
	}
	if registered != nil {
		registered()
	}
}

// redactRunning registers the secret values with the running CLI, if any.
func (p *webProvider) redactRunning() {
	defer log.Recover("copilot secret redaction")
	p.mu.Lock()
	c := p.client
	p.mu.Unlock()
	if c != nil {
		p.redactSecrets(context.Background(), c)
	}
}
