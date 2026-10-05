package web

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
)

// accountProvider is a fake provider with a sign-in the tests switch.
type accountProvider struct {
	*agenttest.Provider
	mu       sync.Mutex
	signedIn bool
	envVar   string
	tokens   []string
	// login and source are the account signed in; "octo" and stored when empty.
	login, source string
}

func newAccountProvider(signedIn bool) *accountProvider {
	caps := allCaps
	caps.Account = true
	p := &accountProvider{Provider: agenttest.NewProvider("fake", caps), signedIn: signedIn}
	p.SetModels([]agentapi.Model{{ID: "a", Name: "A"}}, nil)
	return p
}

func (p *accountProvider) Check(ctx context.Context) error {
	p.mu.Lock()
	in := p.signedIn
	p.mu.Unlock()
	if !in {
		return fmt.Errorf("not signed in: %w", agentapi.ErrSignedOut)
	}
	return p.Provider.Check(ctx)
}

func (p *accountProvider) set(in bool) {
	p.mu.Lock()
	p.signedIn = in
	p.mu.Unlock()
}

func (p *accountProvider) account() agentapi.Account {
	if !p.signedIn {
		return agentapi.Account{EnvVar: p.envVar, Message: "Not authenticated"}
	}
	return agentapi.Account{SignedIn: true, Login: cmp.Or(p.login, "octo"), Host: "https://github.com", Source: cmp.Or(p.source, agentapi.AccountStored), EnvVar: p.envVar}
}

func (p *accountProvider) Account(context.Context) (agentapi.Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.account(), nil
}

func (p *accountProvider) SignIn(_ context.Context, token string) (agentapi.Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.tokens = append(p.tokens, token)
	if p.envVar != "" {
		return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrEnvAccount, p.envVar)
	}
	switch token {
	case "good":
		p.login = "octo"
	case "other":
		p.login = "mallory"
	default:
		return agentapi.Account{}, fmt.Errorf("%w: Failed to fetch Copilot user info: 401 Unauthorized", agentapi.ErrSignInRejected)
	}
	p.signedIn, p.source = true, agentapi.AccountStored
	return p.account(), nil
}

func (p *accountProvider) SignOut(context.Context) (agentapi.Account, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.signedIn = false
	return p.account(), nil
}

func providerInfo(t *testing.T, m *Manager, name string) ProviderInfo {
	t.Helper()
	for _, info := range m.Providers() {
		if info.Name == name {
			return info
		}
	}
	t.Fatalf("no provider %q", name)
	return ProviderInfo{}
}

func errCode(err error) string {
	var webErr *Error
	if errors.As(err, &webErr) {
		return webErr.Code
	}
	return ""
}

// A provider signed out at start is listed as signed out with the plain
// reason, refuses new Tasks with the signed-out code, and is usable with its
// models once signed in, without a restart.
func TestSignedOutProviderRefusesTasksUntilSignedIn(t *testing.T) {
	prov := newAccountProvider(false)
	m := startManager(t, openTestStore(t), prov)
	info := providerInfo(t, m, "fake")
	if info.Available || !info.SignedOut || info.Reason != "Fake fake is signed out. Sign in in Settings." || len(info.Models) != 0 {
		t.Fatalf("signed out at start: %+v", info)
	}
	project := addProject(t, m, t.TempDir())
	_, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
	if statusOf(err) != http.StatusConflict || errCode(err) != codeSignedOut || !strings.Contains(err.Error(), "Sign in in Settings") {
		t.Fatalf("Create while signed out = %v (code %q)", err, errCode(err))
	}

	if _, err := m.SignIn(context.Background(), "fake", "bad"); statusOf(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "401") {
		t.Fatalf("SignIn with a bad token = %v", err)
	}
	acct, err := m.SignIn(context.Background(), "fake", "good")
	if err != nil || !acct.SignedIn || acct.Login != "octo" {
		t.Fatalf("SignIn = %+v, %v", acct, err)
	}
	info = providerInfo(t, m, "fake")
	if !info.Available || info.SignedOut || info.Reason != "" || len(info.Models) != 1 {
		t.Fatalf("after sign-in: %+v", info)
	}
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "a"}); err != nil {
		t.Fatalf("Create after sign-in: %v", err)
	}
}

// Signing out marks the provider so a send to an open Task is refused with
// the signed-out code before anything is sent.
func TestSendAfterSignOutIsRefused(t *testing.T) {
	prov := newAccountProvider(true)
	m := startManager(t, openTestStore(t), prov)
	sum, conv := createSession(t, m, prov.Provider)
	if _, err := m.SignOut(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	_, err := m.Submit(sum.ID, PromptRequest{RequestID: mustUUID(t), Text: "hi"})
	if errCode(err) != codeSignedOut || len(conv.Prompts()) != 0 {
		t.Fatalf("Submit after sign-out = %v, prompts %v", err, conv.Prompts())
	}
}

// A turn that fails because the runtime lost its sign-in says to sign in
// instead of the runtime's own error, and marks the provider signed out.
func TestTurnFailureWhileSignedOutSaysToSignIn(t *testing.T) {
	prov := newAccountProvider(true)
	m := startManager(t, openTestStore(t), prov)
	sum, conv := createSession(t, m, prov.Provider)
	prov.set(false)
	conv.EmitTurn(agentapi.TurnFailed, "Execution failed: InvalidArg, No GitHub OAuth token")
	waitUntil(t, "signed-out failure detail", func() bool {
		return detail(t, m, sum.ID).StateDetail == "Fake fake is signed out. Sign in in Settings."
	})
	if info := providerInfo(t, m, "fake"); info.Available || !info.SignedOut {
		t.Fatalf("provider after the failed turn: %+v", info)
	}
}

// The account routes report the sign-in, take a token without ever
// returning it, and explain an environment token's precedence.
func TestAccountRoutes(t *testing.T) {
	prov := newAccountProvider(false)
	m := startManager(t, openTestStore(t), prov)
	srv, err := NewServer(ServerConfig{Manager: m, Token: testToken, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	ts := &testServer{srv: srv, m: m}
	auth := withCookie(ts)

	if w := ts.do(http.MethodGet, "/api/providers/fake/account", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("GET without a session = %d", w.Code)
	}
	w := ts.do(http.MethodGet, "/api/providers/fake/account", "", auth)
	if w.Code != http.StatusOK || w.Body.String() != `{"signed_in":false,"message":"Not authenticated"}`+"\n" {
		t.Fatalf("GET account = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPost, "/api/providers/fake/account/sign-in", `{"token":"bad"}`, auth)
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), `"bad"`) {
		t.Fatalf("bad token = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodPost, "/api/providers/fake/account/sign-in", `{"token":"good"}`, auth)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "good") || !strings.Contains(w.Body.String(), `"login":"octo"`) {
		t.Fatalf("good token = %d %s", w.Code, w.Body)
	}
	w = ts.do(http.MethodGet, "/api/meta", "", auth)
	if !strings.Contains(w.Body.String(), `"available":true`) || strings.Contains(w.Body.String(), `"signed_out"`) {
		t.Fatalf("meta after sign-in = %s", w.Body)
	}
	if w = ts.do(http.MethodPost, "/api/providers/fake/account/sign-out", "", auth); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"signed_in":false`) {
		t.Fatalf("sign out = %d %s", w.Code, w.Body)
	}
	if w = ts.do(http.MethodGet, "/api/meta", "", auth); !strings.Contains(w.Body.String(), `"signed_out":true`) {
		t.Fatalf("meta after sign-out = %s", w.Body)
	}

	prov.mu.Lock()
	prov.envVar = "GH_TOKEN"
	prov.mu.Unlock()
	w = ts.do(http.MethodPost, "/api/providers/fake/account/sign-in", `{"token":"good"}`, auth)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "GH_TOKEN in the service environment takes precedence") {
		t.Fatalf("sign-in under an environment token = %d %s", w.Code, w.Body)
	}
	if w = ts.do(http.MethodGet, "/api/providers/nope/account", "", auth); w.Code != http.StatusNotFound {
		t.Fatalf("unknown provider = %d", w.Code)
	}
}

// deviceProvider signs in through a device code: it shows the code, then
// waits for the test to approve, deny or cancel.
type deviceProvider struct {
	*accountProvider
	result chan error
}

func newDeviceProvider() *deviceProvider {
	caps := allCaps
	caps.Account, caps.DeviceSignIn = true, true
	ap := &accountProvider{Provider: agenttest.NewProvider("fake", caps)}
	ap.SetModels([]agentapi.Model{{ID: "a", Name: "A"}}, nil)
	return &deviceProvider{accountProvider: ap, result: make(chan error, 1)}
}

func (p *deviceProvider) DeviceSignIn(ctx context.Context, show func(agentapi.DeviceCode)) (agentapi.Account, error) {
	if p.envVar != "" {
		return agentapi.Account{}, fmt.Errorf("%w: %s", agentapi.ErrEnvAccount, p.envVar)
	}
	show(agentapi.DeviceCode{URL: "https://github.com/login/device", Code: "AB12-CD34"})
	select {
	case <-ctx.Done():
		return agentapi.Account{}, ctx.Err()
	case err := <-p.result:
		if err != nil {
			return agentapi.Account{}, err
		}
	}
	p.set(true)
	return p.Account(ctx)
}

func waitDevice(t *testing.T, m *Manager, state string) DeviceSignIn {
	t.Helper()
	var d DeviceSignIn
	waitUntil(t, "device sign-in "+state, func() bool { d = m.DeviceSignIn("fake"); return d.State == state })
	return d
}

// A device sign-in shows its code until approved, then the provider is
// available with its models, without a restart.
func TestDeviceSignInShowsTheCodeUntilApproved(t *testing.T) {
	prov := newDeviceProvider()
	m := startManager(t, openTestStore(t), prov)
	if d := m.DeviceSignIn("fake"); d.State != deviceIdle {
		t.Fatalf("before = %+v", d)
	}
	d, err := m.StartDeviceSignIn(context.Background(), "fake")
	if err != nil || d.State != deviceWaiting || d.Code != "AB12-CD34" || d.URL != "https://github.com/login/device" {
		t.Fatalf("StartDeviceSignIn = %+v, %v", d, err)
	}
	// Starting again joins the sign-in in progress.
	if again, err := m.StartDeviceSignIn(context.Background(), "fake"); err != nil || again != d {
		t.Fatalf("second StartDeviceSignIn = %+v, %v", again, err)
	}
	prov.result <- nil
	d = waitDevice(t, m, deviceSignedIn)
	if d.Account == nil || d.Account.Login != "octo" || d.Code != "" {
		t.Fatalf("signed in = %+v", d)
	}
	if info := providerInfo(t, m, "fake"); !info.Available || info.SignedOut || len(info.Models) != 1 {
		t.Fatalf("after sign-in: %+v", info)
	}
}

func TestDeviceSignInRefusalDeniesAndCancel(t *testing.T) {
	prov := newDeviceProvider()
	m := startManager(t, openTestStore(t), prov)

	prov.envVar = "GH_TOKEN"
	if _, err := m.StartDeviceSignIn(context.Background(), "fake"); statusOf(err) != http.StatusConflict || !strings.Contains(err.Error(), "GH_TOKEN") {
		t.Fatalf("with an environment token: %v", err)
	}
	if d := m.DeviceSignIn("fake"); d.State != deviceIdle {
		t.Fatalf("a refusal left %+v", d)
	}
	prov.envVar = ""

	if _, err := m.StartDeviceSignIn(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	prov.result <- fmt.Errorf("%w: access_denied", agentapi.ErrSignInRejected)
	if d := waitDevice(t, m, deviceFailed); d.Error != "access_denied" || d.Code != "" {
		t.Fatalf("denied = %+v", d)
	}

	if _, err := m.StartDeviceSignIn(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	m.CancelDeviceSignIn("fake")
	waitDevice(t, m, deviceCanceled)
	if info := providerInfo(t, m, "fake"); info.Available {
		t.Fatalf("available after a cancel: %+v", info)
	}
}

func TestDeviceSignInNeedsTheCapability(t *testing.T) {
	m := startManager(t, openTestStore(t), newAccountProvider(false))
	if _, err := m.StartDeviceSignIn(context.Background(), "fake"); statusOf(err) != http.StatusNotFound {
		t.Fatalf("StartDeviceSignIn without the capability = %v", err)
	}
}

// switchAccount signs the fake in as login from source, as a sign-in made
// around uam would (a terminal `copilot login`, an environment token).
func (p *accountProvider) switchAccount(login, source string) {
	p.mu.Lock()
	p.signedIn, p.login, p.source = true, login, source
	p.mu.Unlock()
}

func linkedLogin(t *testing.T, m *Manager) string {
	t.Helper()
	cfg, err := m.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return cfg.WebAccountLinks["fake"].Login
}

// The first sign-in links its account; a sign-in as another account is
// refused and undone, so it never takes effect.
func TestFirstSignInLinksAndAnotherAccountIsRefused(t *testing.T) {
	prov := newAccountProvider(false)
	m := startManager(t, openTestStore(t), prov)
	if _, err := m.SignIn(context.Background(), "fake", "good"); err != nil {
		t.Fatal(err)
	}
	if got := linkedLogin(t, m); got != "octo" {
		t.Fatalf("linked %q after the first sign-in", got)
	}
	_, err := m.SignIn(context.Background(), "fake", "other")
	if statusOf(err) != http.StatusConflict || errCode(err) != codeAccountNotLinked || !strings.Contains(err.Error(), "linked to Fake fake account octo") {
		t.Fatalf("SignIn as another account = %v (code %q)", err, errCode(err))
	}
	if acct, _ := prov.Account(context.Background()); acct.SignedIn {
		t.Fatalf("the refused sign-in stayed: %+v", acct)
	}
	if got := linkedLogin(t, m); got != "octo" {
		t.Fatalf("linked %q after a refused sign-in", got)
	}
	// Signed out but linked, only the linked account may sign in.
	if _, err := m.SignIn(context.Background(), "fake", "other"); errCode(err) != codeAccountNotLinked {
		t.Fatalf("SignIn as another account while signed out = %v", err)
	}
	if _, err := m.SignIn(context.Background(), "fake", "good"); err != nil {
		t.Fatalf("SignIn as the linked account = %v", err)
	}
}

// A sign-in made around uam is caught before the next Task: a stored one is
// signed out again; one uam cannot sign out blocks the provider until fixed.
func TestAnotherAccountSignedInAroundUAMIsUndoneOrBlocks(t *testing.T) {
	prov := newAccountProvider(true)
	m := startManager(t, openTestStore(t), prov)
	if got := linkedLogin(t, m); got != "octo" {
		t.Fatalf("a server signed in at start linked %q", got)
	}
	project := addProject(t, m, t.TempDir())
	later := time.Now()
	advance := func() { later = later.Add(time.Minute); m.now = func() time.Time { return later } }

	prov.switchAccount("mallory", agentapi.AccountStored)
	advance()
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "a"}); errCode(err) != codeSignedOut {
		t.Fatalf("Create after a stored sign-in as another account = %v (code %q)", err, errCode(err))
	}
	if acct, _ := prov.Account(context.Background()); acct.SignedIn {
		t.Fatalf("the other account's stored sign-in stayed: %+v", acct)
	}

	prov.switchAccount("mallory", agentapi.AccountEnv)
	advance()
	_, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "a"})
	if errCode(err) != codeAccountNotLinked || !strings.Contains(err.Error(), "signed in as mallory, but this server is linked to octo") {
		t.Fatalf("Create with another account from the environment = %v (code %q)", err, errCode(err))
	}
	if info := providerInfo(t, m, "fake"); info.Available || !info.AccountMismatch || info.SignedOut {
		t.Fatalf("provider with another account: %+v", info)
	}

	prov.switchAccount("octo", agentapi.AccountStored)
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Model: "a"}); err != nil {
		t.Fatalf("Create once back on the linked account = %v", err)
	}
	if info := providerInfo(t, m, "fake"); !info.Available || info.AccountMismatch {
		t.Fatalf("provider back on the linked account: %+v", info)
	}
}

// Unlinking signs out a stored sign-in and lets the next sign-in link its
// account.
func TestUnlinkLetsAnotherAccountSignIn(t *testing.T) {
	prov := newAccountProvider(true)
	m := startManager(t, openTestStore(t), prov)
	acct, err := m.UnlinkAccount(context.Background(), "fake")
	if err != nil || acct.SignedIn {
		t.Fatalf("UnlinkAccount = %+v, %v", acct, err)
	}
	if got := linkedLogin(t, m); got != "" {
		t.Fatalf("still linked to %q", got)
	}
	if _, err := m.SignIn(context.Background(), "fake", "other"); err != nil {
		t.Fatalf("SignIn after unlinking = %v", err)
	}
	if got := linkedLogin(t, m); got != "mallory" {
		t.Fatalf("linked %q after signing in again", got)
	}
}

func TestDeviceSignInAsAnotherAccountFails(t *testing.T) {
	prov := newDeviceProvider()
	m := startManager(t, openTestStore(t), prov)
	if _, err := m.SignIn(context.Background(), "fake", "good"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SignOut(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	prov.mu.Lock()
	prov.login = "mallory"
	prov.mu.Unlock()
	if _, err := m.StartDeviceSignIn(context.Background(), "fake"); err != nil {
		t.Fatal(err)
	}
	prov.result <- nil
	if d := waitDevice(t, m, deviceFailed); !strings.Contains(d.Error, "linked to Fake fake account octo") {
		t.Fatalf("device sign-in as another account = %+v", d)
	}
	if acct, _ := prov.Account(context.Background()); acct.SignedIn {
		t.Fatalf("the refused device sign-in stayed: %+v", acct)
	}
}
