package web

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi/agenttest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

var titleCaps = func() agentapi.Capabilities { c := allCaps; c.Titles = true; return c }()

// titleManager starts a manager whose provider "fake" titles Tasks with
// model "a", and returns it with a project to create Tasks in.
func titleManager(t *testing.T) (*Manager, *agenttest.Provider, *store.Store, string) {
	t.Helper()
	prov := agenttest.NewProvider("fake", titleCaps)
	prov.SetModels(selectionModels(), nil)
	st := openTestStore(t)
	m := startManager(t, st, prov)
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "a"}}); err != nil {
		t.Fatal(err)
	}
	return m, prov, st, addProject(t, m, t.TempDir())
}

func summaryOf(t *testing.T, m *Manager, id string) SessionSummary {
	t.Helper()
	sum, err := m.Summary(id)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

func TestFirstMessageTitlesTheTask(t *testing.T) {
	m, prov, st, project := titleManager(t)
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) {
		return `"Add dark mode toggle."`, nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	// The provider's own title comes as the prompt is taken, before the job
	// has one.
	conv.SetSendHook(func(_ context.Context, prompt string) error {
		conv.EmitTitle(prompt)
		return nil
	})
	mustSubmit(t, m, sum.ID, "Please add a dark\nmode toggle\x1b[31m", mustUUID(t), ModeSend, SubmissionAccepted)
	waitUntil(t, "the generated title", func() bool { return summaryOf(t, m, sum.ID).Title == "Add dark mode toggle" })
	waitUntil(t, "the provider rename", func() bool { return slices.Equal(conv.Titles(), []string{"Add dark mode toggle"}) })
	reqs := prov.TitleRequests()
	if len(reqs) != 1 || reqs[0].Model != "a" || reqs[0].Workdir != sum.Workdir || reqs[0].Text != "Please add a dark mode toggle" {
		t.Fatalf("title requests = %+v", reqs)
	}
	// The provider echoes the name; later messages never title again.
	conv.SetSendHook(nil)
	conv.EmitTitle("Add dark mode toggle")
	conv.EmitTurn(agentapi.TurnCompleted, "")
	mustSubmit(t, m, sum.ID, "and a light one", mustUUID(t), ModeSend, SubmissionAccepted)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(prov.TitleRequests()); n != 1 {
		t.Fatalf("title requests after a second message = %d", n)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if rec := cfg.Sessions[store.Key("fake", sum.ID)]; rec.Web == nil || rec.Web.Title != "Add dark mode toggle" || rec.Name != "" {
		t.Fatalf("stored record = %+v", rec)
	}
}

func TestTitleTriggerRules(t *testing.T) {
	m, prov, _, project := titleManager(t)
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) { return "Generated", nil })
	prov.SetCommands([]agentapi.Command{{Name: "review", Kind: agentapi.CommandPrompt}}, nil)
	create := func(name string) (SessionSummary, *agenttest.Conversation) {
		t.Helper()
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Name: name})
		if err != nil {
			t.Fatal(err)
		}
		return sum, prov.Last()
	}

	named, _ := create("mine")
	mustSubmit(t, m, named.ID, "a prompt", mustUUID(t), ModeSend, SubmissionAccepted)

	command, _ := create("")
	if sub, err := m.Command(command.ID, CommandRequest{Name: "review", Arguments: "the diff", RequestID: mustUUID(t)}); err != nil || sub.Status != SubmissionAccepted {
		t.Fatalf("command = %+v, %v", sub, err)
	}

	// A rejected first message is not the first accepted one.
	rejected, conv := create("")
	conv.SetSendHook(func(context.Context, string) error { return errors.New("refused") })
	mustSubmit(t, m, rejected.ID, "first try", mustUUID(t), ModeSend, SubmissionRejected)
	conv.SetSendHook(nil)
	mustSubmit(t, m, rejected.ID, "second try", mustUUID(t), ModeSend, SubmissionAccepted)
	waitUntil(t, "the title after a rejected first message", func() bool { return summaryOf(t, m, rejected.ID).Title == "Generated" })

	// An uncertain first message may have started the conversation.
	uncertain, conv := create("")
	conv.SetSendHook(func(context.Context, string) error { return agentapi.ErrSubmissionUncertain })
	mustSubmit(t, m, uncertain.ID, "maybe", mustUUID(t), ModeSend, SubmissionUncertain)
	conv.SetSendHook(nil)
	mustSubmit(t, m, uncertain.ID, "again", mustUUID(t), ModeSend, SubmissionAccepted)

	// Opted out, a first message keeps the provider's title.
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": store.WebTitleModelNone}}); err != nil {
		t.Fatal(err)
	}
	unset, _ := create("")
	mustSubmit(t, m, unset.ID, "no model", mustUUID(t), ModeSend, SubmissionAccepted)

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, req := range prov.TitleRequests() {
		texts = append(texts, req.Text)
	}
	if !slices.Equal(texts, []string{"second try"}) {
		t.Fatalf("titled messages = %q, want only the rejected Task's second try", texts)
	}
}

// A reopened Task whose conversation already has messages is never titled.
func TestReopenedTaskIsNotTitled(t *testing.T) {
	st := openTestStore(t)
	id := mustUUID(t)
	seedWebRecord(t, st, id, "conv_known", StateCompleted)
	prov := agenttest.NewProvider("fake", titleCaps)
	prov.SetModels(selectionModels(), nil)
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) { return "Generated", nil })
	prov.AddConversation("conv_known", []agentapi.Item{{ID: "u1", Kind: agentapi.ItemUser, Text: "earlier prompt"}})
	m := startManager(t, st, prov)
	if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": "a"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Rename(id, ""); err != nil {
		t.Fatal(err)
	}
	mustSubmit(t, m, id, "a later prompt", mustUUID(t), ModeSend, SubmissionAccepted)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := len(prov.TitleRequests()); n != 0 {
		t.Fatalf("a reopened Task was titled %d times", n)
	}
}

func TestRenameDuringTitleWins(t *testing.T) {
	m, prov, _, project := titleManager(t)
	started, release := make(chan struct{}), make(chan struct{})
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) {
		close(started)
		<-release
		return "Generated", nil
	})
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "do it"})
	if err != nil {
		t.Fatal(err)
	}
	conv := prov.Last()
	conv.EmitTitle("do it")
	<-started
	// Renaming and clearing the name again still counts as the user's choice.
	for _, name := range []string{"mine", ""} {
		if _, err := m.Rename(sum.ID, name); err != nil {
			t.Fatal(err)
		}
	}
	close(release)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := summaryOf(t, m, sum.ID); got.Title != "do it" || len(conv.Titles()) != 0 {
		t.Fatalf("title %q, provider renames %v after a rename during the job", got.Title, conv.Titles())
	}
}

// A failure, the timeout or an unusable reply leaves the provider's title.
func TestTitleFailureKeepsTheProviderTitle(t *testing.T) {
	for name, hook := range map[string]func(context.Context, agentapi.TitleRequest) (string, error){
		"error": func(context.Context, agentapi.TitleRequest) (string, error) {
			return "", errors.New("model unavailable")
		},
		"timeout": func(ctx context.Context, _ agentapi.TitleRequest) (string, error) {
			deadline, ok := ctx.Deadline()
			if left := time.Until(deadline); !ok || left > titleTimeout || left < titleTimeout-5*time.Second {
				return "", errors.New("the job has no 20 s bound")
			}
			return "", context.DeadlineExceeded
		},
		"empty": func(context.Context, agentapi.TitleRequest) (string, error) {
			return " \n <think>hm</think> \"...\" ", nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			m, prov, _, project := titleManager(t)
			prov.SetTitleHook(hook)
			sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "fix the build"})
			if err != nil {
				t.Fatal(err)
			}
			conv := prov.Last()
			conv.EmitTitle("fix the build")
			waitUntil(t, "the title job", func() bool { return len(prov.TitleRequests()) == 1 })
			if err := m.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := summaryOf(t, m, sum.ID); got.Title != "fix the build" || len(conv.Titles()) != 0 {
				t.Fatalf("title %q, provider renames %v", got.Title, conv.Titles())
			}
		})
	}
}

// A failed provider rename keeps the generated title in the web.
func TestTitleKeptWhenTheProviderRenameFails(t *testing.T) {
	m, prov, _, project := titleManager(t)
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) { return "Generated", nil })
	sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
	if err != nil {
		t.Fatal(err)
	}
	prov.Last().SetTitleError(errors.New("rename refused"))
	mustSubmit(t, m, sum.ID, "go", mustUUID(t), ModeSend, SubmissionAccepted)
	waitUntil(t, "the generated title", func() bool { return summaryOf(t, m, sum.ID).Title == "Generated" })
}

func TestAtMostTwoTitleJobsRunAtOnce(t *testing.T) {
	m, prov, _, project := titleManager(t)
	var mu sync.Mutex
	running, most := 0, 0
	release := make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	// Runs before the manager's shutdown, so a failure never leaves jobs
	// blocked.
	t.Cleanup(free)
	prov.SetTitleHook(func(_ context.Context, req agentapi.TitleRequest) (string, error) {
		mu.Lock()
		running++
		most = max(most, running)
		mu.Unlock()
		<-release
		mu.Lock()
		running--
		mu.Unlock()
		return "Title " + req.Text, nil
	})
	var ids []string
	for _, text := range []string{"one", "two", "three"} {
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: text})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, sum.ID)
	}
	waitUntil(t, "two running jobs", func() bool { mu.Lock(); defer mu.Unlock(); return running >= 2 })
	time.Sleep(50 * time.Millisecond)
	free()
	for i, text := range []string{"one", "two", "three"} {
		waitUntil(t, "title "+text, func() bool { return summaryOf(t, m, ids[i]).Title == "Title "+text })
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 2 {
		t.Fatalf("at most %d jobs ran at once, want 2", most)
	}
}

// Shutdown ends running title jobs before it stops the providers, so a job
// can still delete its throwaway conversation.
func TestShutdownWaitsForTitleJobsBeforeProviders(t *testing.T) {
	m, prov, _, project := titleManager(t)
	stoppedFirst := make(chan bool, 1)
	prov.SetTitleHook(func(ctx context.Context, _ agentapi.TitleRequest) (string, error) {
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond)
		stoppedFirst <- prov.ShutdownCalls() > 0
		return "", ctx.Err()
	})
	if _, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project, Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, "the title job", func() bool { return len(prov.TitleRequests()) == 1 })
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if <-stoppedFirst {
		t.Fatal("the provider stopped while a title job ran")
	}
}

func TestCleanGeneratedTitle(t *testing.T) {
	long := "Replace Go 1.23 slices.Concat usage for Go 1.21 compatibility now"
	for reply, want := range map[string]string{
		"Add dark mode toggle":                               "Add dark mode toggle",
		"  \n\nFix Go build\nSecond line":                    "Fix Go build",
		`"Add persistent dark mode toggle."`:                 "Add persistent dark mode toggle",
		"Title: `Fix the build`;":                            "Fix the build",
		"TITLE:  **Fix   the\tbuild**":                       "Fix the build",
		"“Smart quotes”":                                     "Smart quotes",
		"<think>a title?\nno</think>\nPlain title":           "Plain title",
		"Sure.\n<session-title>Tagged title</session-title>": "Tagged title",
		"<title>\n  Other tag \n</title>":                    "Other tag",
		"Red\x1b[31m title\x07":                              "Red title",
		"Is it a question?":                                  "Is it a question?",
		long:                                                 "Replace Go 1.23 slices.Concat usage for Go 1.21",
		strings.Repeat("x", 70):                              strings.Repeat("x", 60),
		strings.Repeat("a", 59) + " b":                       strings.Repeat("a", 59),
		strings.Repeat("a", 60) + " b":                       strings.Repeat("a", 60),
		"Fix the build, test, " + strings.Repeat("y", 50):    "Fix the build, test",
		"":                    "",
		"...":                 "",
		`""`:                  "",
		"<think>only</think>": "",
	} {
		if got := cleanGeneratedTitle(reply); got != want {
			t.Errorf("cleanGeneratedTitle(%q) = %q, want %q", reply, got, want)
		}
	}
}

func TestTitleModelSetting(t *testing.T) {
	st := openTestStore(t)
	fake := agenttest.NewProvider("fake", titleCaps)
	fake.SetModels(selectionModels(), nil)
	plain := agenttest.NewProvider("plain", allCaps)
	plain.SetModels(selectionModels(), nil)
	m := startManager(t, st, fake, plain)
	sub, _, err := m.Subscribe("")
	if err != nil {
		t.Fatal(err)
	}
	set := func(change map[string]string) (Settings, error) {
		return m.UpdateSettings(SettingsPatch{TitleModel: change})
	}
	for name, change := range map[string]map[string]string{
		"unknown provider":   {"nobody": "a"},
		"no titles":          {"plain": "a"},
		"unlisted model":     {"fake": "gone"},
		"one bad of two":     {"fake": "a", "plain": "a"},
		"control characters": {"fake": "a\x07"},
	} {
		if _, err := set(change); statusOf(err) != http.StatusBadRequest {
			t.Fatalf("%s = %v, want 400", name, err)
		}
	}
	if got := m.Settings(); got.TitleModel != nil {
		t.Fatalf("refused changes set %v", got.TitleModel)
	}
	// The opt-out needs no titles capability and is kept as such.
	if got, err := set(map[string]string{"plain": store.WebTitleModelNone}); err != nil || got.TitleModel["plain"] != store.WebTitleModelNone {
		t.Fatalf("opt out plain = %+v, %v", got, err)
	}
	if f := frameOf(t, sub, "settings"); string(f.data["settings"]) != `{"send_default":"steer","title_model":{"plain":"none"}}` {
		t.Fatalf("opt-out settings frame = %s", f.data["settings"])
	}
	got, err := set(map[string]string{"fake": "auto"})
	if err != nil || got.TitleModel["fake"] != "auto" || len(got.TitleModel) != 2 {
		t.Fatalf("set auto = %+v, %v", got, err)
	}
	if f := frameOf(t, sub, "settings"); string(f.data["settings"]) != `{"send_default":"steer","title_model":{"fake":"auto","plain":"none"}}` {
		t.Fatalf("settings frame = %s", f.data["settings"])
	}
	// A clear needs no titles capability; it clears only what it names.
	if got, err := set(map[string]string{"plain": ""}); err != nil || got.TitleModel["fake"] != "auto" {
		t.Fatalf("clear plain = %+v, %v", got, err)
	}
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cfg, err := st.Load(); err != nil || cfg.WebSettings.TitleModel["fake"] != "auto" {
		t.Fatalf("stored title model = %v, %v", cfg.WebSettings.TitleModel, err)
	}
	m = startManager(t, st, fake, plain)
	if got := m.Settings(); got.TitleModel["fake"] != "auto" {
		t.Fatalf("title model after restart = %v", got.TitleModel)
	}
	if _, snap, err := m.Subscribe(""); err != nil || !strings.Contains(string(parseFrame(t, snap).data["settings"]), `"title_model":{"fake":"auto"}`) {
		t.Fatalf("snapshot settings: %v", err)
	}
	if got, err := set(map[string]string{"fake": ""}); err != nil || got.TitleModel != nil {
		t.Fatalf("clear fake = %+v, %v", got, err)
	}
}

func TestTitleModelRoute(t *testing.T) {
	ts := newTestServer(t, ServerConfig{})
	ts.prov.SetModels(selectionModels(), nil)
	setNow(ts.m, time.Now().Add(time.Hour))
	ts.m.RefreshModels()
	auth := withCookie(ts)
	patch := func(body string, want int) string {
		t.Helper()
		w := ts.do(http.MethodPatch, "/api/settings", body, auth)
		if w.Code != want {
			t.Fatalf("PATCH /api/settings %s = %d %s, want %d", body, w.Code, w.Body, want)
		}
		return strings.TrimSpace(w.Body.String())
	}
	for _, body := range []string{
		`{"title_model":null}`, `{"title_model":"a"}`, `{"title_model":["a"]}`, `{"title_model":{"fake":1}}`,
		`{"title_model":{"nobody":"a"}}`, `{"title_model":{"fake":"a"}}`,
	} {
		if got := patch(body, http.StatusBadRequest); !strings.Contains(got, `"error"`) {
			t.Fatalf("PATCH %s = %s", body, got)
		}
	}
	if w := ts.do(http.MethodGet, "/api/settings", "", auth); strings.TrimSpace(w.Body.String()) != `{"send_default":"steer"}` {
		t.Fatalf("GET after refused PATCHes = %s", w.Body)
	}
	if got := patch(`{"title_model":{"fake":"none"}}`, http.StatusOK); got != `{"send_default":"steer","title_model":{"fake":"none"}}` {
		t.Fatalf("PATCH opt-out = %s", got)
	}
	if got := patch(`{"title_model":{"fake":""}}`, http.StatusOK); got != `{"send_default":"steer"}` {
		t.Fatalf("PATCH unset = %s", got)
	}
	ts.prov.SetModels([]agentapi.Model{pricedModel("dear", 100, 500, 1e6), pricedModel("cheap", 10, 50, 1e6)}, nil)
	setNow(ts.m, time.Now().Add(2*time.Hour))
	ts.m.RefreshModels()
	if w := ts.do(http.MethodGet, "/api/meta", "", auth); !strings.Contains(w.Body.String(), `"cheapest_model":"cheap"`) {
		t.Fatalf("GET /api/meta = %s", w.Body)
	}
}

// pricedModel is a model with input and output prices per batch tokens; a
// zero batch reports none.
func pricedModel(id string, input, output float64, batch int64) agentapi.Model {
	return agentapi.Model{ID: id, Prices: &agentapi.Prices{BatchSize: batch, TierPrices: agentapi.TierPrices{Input: &input, Output: &output}}}
}

func TestCheapestModel(t *testing.T) {
	onlyInput := 1.0
	for name, tc := range map[string]struct {
		models []agentapi.Model
		hidden []string
		want   string
	}{
		"lowest input+output":      {[]agentapi.Model{pricedModel("mini", 25, 200, 1e6), pricedModel("luna", 10, 50, 1e6), pricedModel("haiku", 100, 500, 1e6)}, nil, "luna"},
		"unpriced skipped":         {[]agentapi.Model{{ID: "free"}, {ID: "half", Prices: &agentapi.Prices{TierPrices: agentapi.TierPrices{Input: &onlyInput}}}, pricedModel("paid", 5, 5, 1e6)}, nil, "paid"},
		"auto skipped":             {[]agentapi.Model{pricedModel("auto", 1, 1, 1e6), pricedModel("b", 5, 5, 1e6)}, nil, "b"},
		"hidden skipped":           {[]agentapi.Model{pricedModel("a", 1, 1, 1e6), pricedModel("b", 5, 5, 1e6)}, []string{"a"}, "b"},
		"tie takes lower input":    {[]agentapi.Model{pricedModel("a", 6, 4, 1e6), pricedModel("b", 4, 6, 1e6)}, nil, "b"},
		"tie takes lower ID":       {[]agentapi.Model{pricedModel("b", 5, 5, 1e6), pricedModel("a", 5, 5, 1e6)}, nil, "a"},
		"per token, not per batch": {[]agentapi.Model{pricedModel("a", 10, 10, 1e6), pricedModel("b", 15, 15, 2e6)}, nil, "b"},
		"no batch means a million": {[]agentapi.Model{pricedModel("a", 10, 10, 0), pricedModel("b", 15, 15, 2e6)}, nil, "b"},
		"none priced":              {[]agentapi.Model{{ID: "a"}, pricedModel("auto", 1, 1, 1e6)}, nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			prov := agenttest.NewProvider("fake", titleCaps)
			prov.SetModels(append(tc.models, agentapi.Model{ID: "visible"}), nil)
			m := startManager(t, openTestStore(t), prov)
			if tc.hidden != nil {
				if _, err := m.UpdateSettings(SettingsPatch{HiddenModels: map[string][]string{"fake": tc.hidden}}); err != nil {
					t.Fatal(err)
				}
			}
			if got := m.Providers()[0].CheapestModel; got != tc.want {
				t.Fatalf("cheapest = %q, want %q", got, tc.want)
			}
		})
	}
}

// Unset, the Utility model is the cheapest priced model; a chosen model
// replaces it, and the opt-out makes no title call and survives a restart.
func TestUtilityModelDefaultsToCheapest(t *testing.T) {
	prov := agenttest.NewProvider("fake", titleCaps)
	prov.SetModels([]agentapi.Model{pricedModel("dear", 100, 500, 1e6), pricedModel("cheap", 10, 50, 1e6), {ID: "auto"}}, nil)
	prov.SetTitleHook(func(context.Context, agentapi.TitleRequest) (string, error) { return "Generated", nil })
	st := openTestStore(t)
	m := startManager(t, st, prov)
	project := addProject(t, m, t.TempDir())
	titled := func(prompt string) {
		t.Helper()
		sum, err := m.Create(CreateRequest{Provider: "fake", ProjectID: project})
		if err != nil {
			t.Fatal(err)
		}
		mustSubmit(t, m, sum.ID, prompt, mustUUID(t), ModeSend, SubmissionAccepted)
	}
	set := func(model string) {
		t.Helper()
		if _, err := m.UpdateSettings(SettingsPatch{TitleModel: map[string]string{"fake": model}}); err != nil {
			t.Fatal(err)
		}
	}
	titled("unset")
	set("dear")
	titled("chosen")
	set(store.WebTitleModelNone)
	titled("opted out")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cfg, err := st.Load(); err != nil || cfg.WebSettings.TitleModel["fake"] != store.WebTitleModelNone {
		t.Fatalf("stored opt-out = %v, %v", cfg.WebSettings.TitleModel, err)
	}
	m = startManager(t, st, prov)
	if got := m.Settings().TitleModel["fake"]; got != store.WebTitleModelNone {
		t.Fatalf("opt-out after restart = %q", got)
	}
	titled("opted out after restart")
	set("")
	if got := m.Settings().TitleModel; got != nil {
		t.Fatalf("unset left %v", got)
	}
	titled("unset again")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, req := range prov.TitleRequests() {
		got = append(got, req.Text+"="+req.Model)
	}
	if want := []string{"unset=cheap", "chosen=dear", "unset again=cheap"}; !slices.Equal(got, want) {
		t.Fatalf("title requests = %q, want %q", got, want)
	}
}
