package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

// fleetProvider is the provider whose linked account every connected
// instance shares: the owner's rule is one Copilot account for the fleet.
const fleetProvider = "copilot"

// codeAccountMismatch marks a pairing refused, or a connection marked,
// because the two instances are linked to different accounts.
const codeAccountMismatch = "account_mismatch"

// fleetCheckTimeout bounds reading the connected instances' accounts.
const fleetCheckTimeout = 5 * time.Second

// fleetAccount is a linked account as pairing and the registry exchange it.
type fleetAccount struct {
	Login string `json:"login"`
	Host  string `json:"host,omitempty"`
}

func (a fleetAccount) link() store.AccountLink {
	return store.AccountLink{Login: a.Login, Host: a.Host}
}

func (a fleetAccount) same(link store.AccountLink) bool {
	return sameAccount(link, agentapi.Account{Login: a.Login, Host: a.Host})
}

// valid bounds an account another instance reports before it is linked or
// shown here.
func (a fleetAccount) valid() bool {
	if a.Login == "" || len(a.Login) > 100 || strings.IndexFunc(a.Login, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return false
	}
	if a.Host == "" {
		return true
	}
	u, err := url.Parse(a.Host)
	return err == nil && len(a.Host) <= 256 && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// linkedFleetAccount is this instance's linked Copilot account, or nil.
func (s *Server) linkedFleetAccount() *fleetAccount {
	link, ok := s.m.accountLink(fleetProvider)
	if !ok {
		return nil
	}
	return &fleetAccount{Login: link.Login, Host: link.Host}
}

// adoptFleetAccount links this instance to the account of an instance it
// paired with, when this one has none.
func (s *Server) adoptFleetAccount(peer *fleetAccount, from string) {
	if peer != nil && peer.valid() {
		s.m.adoptAccount(fleetProvider, peer.link(), from)
	}
}

func fleetMismatchMessage(label string, theirs fleetAccount, ours store.AccountLink) string {
	return fmt.Sprintf("%s is linked to Copilot account %s; this instance is linked to %s. Both must use the same account.", label, theirs.Login, ours.Login)
}

// pairAccountRefusal reads a 409 pairing refusal. A target linked to another
// Copilot account names it; any other conflict is not an account refusal.
func (s *Server) pairAccountRefusal(body io.Reader, label string) error {
	raw, err := io.ReadAll(io.LimitReader(body, 64<<10))
	var refusal struct {
		Code    string        `json:"code"`
		Account *fleetAccount `json:"account"`
	}
	if err != nil || json.Unmarshal(raw, &refusal) != nil || refusal.Code != codeAccountMismatch {
		return nil
	}
	home, linked := s.m.accountLink(fleetProvider)
	if refusal.Account == nil || !refusal.Account.valid() || !linked {
		return connectionError(http.StatusConflict, codeAccountMismatch, label+" is linked to another Copilot account than this instance. Both must use the same account.")
	}
	return connectionError(http.StatusConflict, codeAccountMismatch, fleetMismatchMessage(label, *refusal.Account, home))
}

// fleetRemote is what a connection last reported of its Copilot account:
// the account it is linked to, and the runtime's account when it is signed
// in as another one.
type fleetRemote struct {
	generation uint64
	at         time.Time
	linked     *fleetAccount
	signedIn   *fleetAccount
}

type fleetAccounts struct {
	mu sync.Mutex
	by map[string]fleetRemote
}

// readFleetAccount reads the connection's Copilot account through its
// workload grant. It is false when the account could not be read; an
// instance without Copilot accounts reports nothing to compare.
func (s *Server) readFleetAccount(ctx context.Context, target connectionTarget) (fleetRemote, bool) {
	transport, err := s.connectionTransport(target)
	if err != nil {
		return fleetRemote{}, false
	}
	if idle, ok := transport.(interface{ CloseIdleConnections() }); ok {
		defer idle.CloseIdleConnections()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.BaseURL+federationWorkloadPrefix+"api/providers/"+fleetProvider+"/account", nil)
	if err != nil {
		return fleetRemote{}, false
	}
	req.Header.Set("Authorization", "Bearer "+target.Credential)
	req.Header.Set(headerUAMInstance, target.InstanceID)
	response, err := transport.RoundTrip(req)
	if err != nil {
		return fleetRemote{}, false
	}
	defer func() { _ = response.Body.Close() }()
	out := fleetRemote{generation: target.Generation, at: time.Now()}
	if response.StatusCode != http.StatusOK {
		return out, response.StatusCode == http.StatusNotFound
	}
	var view struct {
		agentapi.Account
		Linked *fleetAccount `json:"linked"`
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil || json.Unmarshal(raw, &view) != nil {
		return fleetRemote{}, false
	}
	if view.Linked != nil && view.Linked.valid() {
		out.linked = view.Linked
		signed := fleetAccount{Login: view.Login, Host: view.Host}
		if view.SignedIn && signed.valid() && !signed.same(view.Linked.link()) {
			out.signedIn = &signed
		}
	}
	return out, true
}

// refreshFleetAccount reads the connection's account unless it was read for
// its generation within maxAge. A failed read keeps what was known.
func (s *Server) refreshFleetAccount(ctx context.Context, target connectionTarget, maxAge time.Duration) {
	s.fleet.mu.Lock()
	known, ok := s.fleet.by[target.ID]
	s.fleet.mu.Unlock()
	if ok && known.generation == target.Generation && time.Since(known.at) < maxAge {
		return
	}
	got, ok := s.readFleetAccount(ctx, target)
	if !ok {
		return
	}
	s.fleet.mu.Lock()
	if s.fleet.by == nil {
		s.fleet.by = map[string]fleetRemote{}
	}
	s.fleet.by[target.ID] = got
	s.fleet.mu.Unlock()
}

// checkFleetAccounts reads every enabled connection's account at once, and
// forgets those of removed connections.
func (s *Server) checkFleetAccounts(ctx context.Context, list []Connection) {
	ctx, cancel := context.WithTimeout(ctx, fleetCheckTimeout)
	defer cancel()
	var wg sync.WaitGroup
	for _, c := range list {
		if !c.Enabled {
			continue
		}
		target, err := s.connections.lookup(c.ID)
		if err != nil {
			continue
		}
		wg.Go(func() { s.refreshFleetAccount(ctx, target, 0) })
	}
	wg.Wait()
	s.fleet.mu.Lock()
	for id := range s.fleet.by {
		if !slices.ContainsFunc(list, func(c Connection) bool { return c.ID == id }) {
			delete(s.fleet.by, id)
		}
	}
	s.fleet.mu.Unlock()
}

// fleetAccountReason says why the connection breaks the one-account rule:
// it is linked to another account than this instance, or its runtime is
// signed in as another account than its link. Empty when it does not, or
// when its account is not known for its generation.
func (s *Server) fleetAccountReason(c Connection) string {
	s.fleet.mu.Lock()
	known, ok := s.fleet.by[c.ID]
	s.fleet.mu.Unlock()
	if !ok || known.generation != c.Generation || known.linked == nil {
		return ""
	}
	if home, linked := s.m.accountLink(fleetProvider); linked && !known.linked.same(home) {
		return fleetMismatchMessage(c.Label, *known.linked, home)
	}
	if known.signedIn != nil {
		return fmt.Sprintf("%s is signed in to Copilot as %s, but is linked to %s. Sign in there as %s.", c.Label, known.signedIn.Login, known.linked.Login, known.linked.Login)
	}
	return ""
}

// refuseFleetAccount refuses a gated workload route to a connection whose
// account breaks the one-account rule. The account is read again when this
// instance is linked and the last read is older than accountCheckEvery.
func (s *Server) refuseFleetAccount(ctx context.Context, target connectionTarget) error {
	if _, linked := s.m.accountLink(fleetProvider); linked {
		ctx, cancel := context.WithTimeout(ctx, fleetCheckTimeout)
		s.refreshFleetAccount(ctx, target, accountCheckEvery)
		cancel()
	}
	if reason := s.fleetAccountReason(target.Connection); reason != "" {
		return &Error{Status: http.StatusConflict, Code: codeAccountNotLinked, Message: reason}
	}
	return nil
}
