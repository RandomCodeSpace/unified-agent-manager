package opencode

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "opencode" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

func testOpenCodeAgent(t *testing.T, help string) (*adapter.Agent, *adaptertest.Backend) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = --help ]; then printf '%s\\n' '" + help + "'; fi\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("OPENCODE_CONFIG_CONTENT", "")
	resetAutoCacheForTest()
	be := &adaptertest.Backend{}
	return New(be).(*adapter.Agent), be
}

func TestAutoFlagIsVersionAwareAndSafeModeNeverUsesIt(t *testing.T) {
	for _, tt := range []struct {
		name, help, mode string
		want             bool
	}{
		{"current yolo", "Usage: opencode [--auto]", "yolo", true},
		{"old yolo", "Usage: opencode [--continue]", "yolo", false},
		{"current safe", "Usage: opencode [--auto]", "safe", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ag, be := testOpenCodeAgent(t, tt.help)
			if _, err := ag.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: tt.mode}); err != nil {
				t.Fatal(err)
			}
			got := strings.Contains(strings.Join(be.CallsOf("create")[0].Command, " "), "--auto")
			if got != tt.want {
				t.Fatalf("auto=%v want=%v argv=%v", got, tt.want, be.CallsOf("create")[0].Command)
			}
		})
	}
}

func TestAutoProbeCachesByExecutableStatIdentity(t *testing.T) {
	dir := t.TempDir()
	path, count := filepath.Join(dir, "opencode"), filepath.Join(dir, "count")
	t.Setenv("COUNT", count)
	write := func(help string) {
		t.Helper()
		script := "#!/bin/sh\nprintf x >> \"$COUNT\"\nprintf '%s\\n' '" + help + "'\n"
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	resetAutoCacheForTest()
	write("Usage [--auto]")
	if !supportsAuto(context.Background(), path) || !supportsAuto(context.Background(), path) {
		t.Fatal("current executable not detected")
	}
	data, _ := os.ReadFile(count)
	if string(data) != "x" {
		t.Fatalf("probe count=%q", data)
	}
	write("Usage [--continue] changed-size")
	if supportsAuto(context.Background(), path) {
		t.Fatal("changed old executable inherited cached support")
	}
	data, _ = os.ReadFile(count)
	if string(data) != "xx" {
		t.Fatalf("re-probe count=%q", data)
	}
}

func TestInvalidCommandAliasCannotExecuteDuringPreparation(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	alias := filepath.Join(t.TempDir(), "not-an-alias")
	if err := os.WriteFile(alias, []byte("#!/bin/sh\nprintf ran > "+marker+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	ag := New(&adaptertest.Backend{}).(*adapter.Agent)
	_, err := ag.Dispatch(context.Background(), adapter.DispatchRequest{CommandAlias: alias, Cwd: "/tmp", Mode: "yolo"})
	if err == nil {
		t.Fatal("invalid path alias accepted")
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("invalid alias executed during preparation: %v", statErr)
	}
}

func TestMergeInlineConfigPreservesKeysAndPlugins(t *testing.T) {
	got, err := mergeConfigContent(`{"model":"x","nested":{"keep":true},"plugin":["file:///user.js"]}`, "file:///uam.js")
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	plugins := cfg["plugin"].([]any)
	if cfg["model"] != "x" || len(plugins) != 2 || plugins[0] != "file:///user.js" || plugins[1] != "file:///uam.js" {
		t.Fatalf("config=%v", cfg)
	}
	if _, err := mergeConfigContent(`{"plugin":"wrong"}`, "file:///uam.js"); err == nil {
		t.Fatal("incompatible plugin shape accepted")
	}
	if _, err := mergeConfigContent(`{broken`, "file:///uam.js"); err == nil {
		t.Fatal("malformed config accepted")
	}
}

func TestIdentityHandoffRejectsSymlinksModesBoundsAndMalformedIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.json")
	write := func(value string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"provider_session_id":"ses_abc123"}`, 0o600)
	if got, err := readIdentity(path); err != nil || got != "ses_abc123" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentity(path); err == nil {
		t.Fatal("unsafe mode accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	write(`{"provider_session_id":"bad"}`, 0o600)
	if _, err := readIdentity(path); err == nil {
		t.Fatal("malformed id accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	write(strings.Repeat("x", maxIdentityBytes+1), 0o600)
	if _, err := readIdentity(path); err == nil {
		t.Fatal("oversized handoff accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte(`{"provider_session_id":"ses_ok"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentity(path); err == nil {
		t.Fatal("symlink accepted")
	}
}

func TestIdentityBoundaryMatchesSchemaV3AndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.json")
	accepted := "ses_" + strings.Repeat("a", 60)
	if len(accepted) != 64 {
		t.Fatal("bad test boundary")
	}
	if err := os.WriteFile(path, []byte(`{"provider_session_id":"`+accepted+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := readIdentity(path)
	if err != nil || id != accepted {
		t.Fatalf("accepted boundary id=%q err=%v", id, err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(cfg *store.Config) error {
		cfg.Sessions["opencode:abc12345"] = store.SessionRecord{ID: "abc12345", Agent: "opencode", SessionName: "uam-opencode-abc12345", Workdir: "/tmp", ProviderSessionID: id}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := st.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sessions["opencode:abc12345"].ProviderSessionID != accepted {
		t.Fatalf("boundary identity did not survive reload: %+v", cfg.Sessions)
	}
	overlong := "ses_" + strings.Repeat("a", 61)
	if err := os.WriteFile(path, []byte(`{"provider_session_id":"`+overlong+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readIdentity(path); err == nil {
		t.Fatal("65-byte provider identity accepted")
	}
}

func TestProviderFilesRejectIntermediateSymlink(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	if err := os.Symlink(t.TempDir(), filepath.Join(base, "uam")); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureProviderFiles(); err == nil {
		t.Fatal("intermediate provider-state symlink accepted")
	}
}

func TestProviderFilesRejectHostileStateBase(t *testing.T) {
	t.Run("group writable", func(t *testing.T) {
		base := t.TempDir()
		if err := os.Chmod(base, 0o770); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_STATE_HOME", base)
		if _, err := ensureProviderFiles(); err == nil {
			t.Fatal("group-writable XDG_STATE_HOME accepted")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		parent, target := t.TempDir(), t.TempDir()
		base := filepath.Join(parent, "state")
		if err := os.Symlink(target, base); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_STATE_HOME", base)
		if _, err := ensureProviderFiles(); err == nil {
			t.Fatal("symlinked XDG_STATE_HOME accepted")
		}
	})
	t.Run("hostile parent", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "shared")
		if err := os.Mkdir(parent, 0o777); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(parent, 0o777); err != nil {
			t.Fatal(err)
		}
		t.Setenv("XDG_STATE_HOME", filepath.Join(parent, "state"))
		if _, err := ensureProviderFiles(); err == nil {
			t.Fatal("state base under writable non-sticky parent accepted")
		}
	})
}

func TestLiveIdentityProducesExactResumeAndMissingKeepsFallback(t *testing.T) {
	ag, _ := testOpenCodeAgent(t, "Usage: opencode [--auto]")
	if got := ag.ResumeKind(adapter.ResumeRequest{ProviderSessionID: "ses_known"}); got != adapter.ResumeExact {
		t.Fatalf("known kind=%q", got)
	}
	if got := ag.ResumeKind(adapter.ResumeRequest{}); got != adapter.ResumeHeuristic {
		t.Fatalf("missing kind=%q", got)
	}
	path, err := identityPath("uam-opencode-abc12345")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ensureProviderFiles(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"provider_session_id":"ses_live"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ag.ListFromSnapshot(context.Background(), []session.Info{{Name: "uam-opencode-abc12345", Cwd: "/tmp", Alive: true}})
	if err != nil || got[0].ProviderSessionID != "ses_live" {
		t.Fatalf("sessions=%+v err=%v", got, err)
	}
}

func TestPluginRootEventsWinAndChildCannotOverwrite(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	plugin, err := ensureProviderFiles()
	if err != nil {
		t.Fatal(err)
	}
	handoff, err := identityPath("uam-opencode-abc12345")
	if err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(t.TempDir(), "runner.mjs")
	source := `import { promises as fs } from "node:fs";
import { UAMIdentityPlugin } from ` + strconv.Quote((&url.URL{Scheme: "file", Path: plugin}).String()) + `;
const hooks = await UAMIdentityPlugin();
await hooks.event({event:{type:"session.created",properties:{info:{id:"ses_root123"}}}});
await fs.unlink(process.env.UAM_OPENCODE_IDENTITY_FILE);
await hooks.event({event:{type:"message.updated",properties:{info:{id:"msg_not_session"},sessionID:"ses_root123"}}});
if (JSON.parse(await fs.readFile(process.env.UAM_OPENCODE_IDENTITY_FILE)).provider_session_id !== "ses_root123") throw new Error("root activity not captured");
await hooks.event({event:{type:"session.updated",properties:{info:{id:"ses_child123",parentID:"ses_root123"}}}});
await hooks.event({event:{type:"command.executed",properties:{sessionID:"ses_child123"}}});
if (JSON.parse(await fs.readFile(process.env.UAM_OPENCODE_IDENTITY_FILE)).provider_session_id !== "ses_root123") throw new Error("child overwrote root");
await hooks.event({event:{type:"session.updated",properties:{info:{id:"ses_switched123"}}}});
`
	if err := os.WriteFile(runner, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, runner)
	cmd.Env = append(os.Environ(), "UAM_OPENCODE_IDENTITY_FILE="+handoff)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("plugin: %v: %s", err, out)
	}
	if got, err := readIdentity(handoff); err != nil || got != "ses_switched123" {
		t.Fatalf("id=%q err=%v", got, err)
	}
}

func TestPluginActivityUsesOwningSessionRatherThanMessageID(t *testing.T) {
	for _, eventType := range []string{"message.updated", "command.executed"} {
		if !strings.Contains(pluginSource, `type === "`+eventType+`"`) {
			t.Fatalf("plugin missing %s", eventType)
		}
	}
	if !strings.Contains(pluginSource, "const activityID = event?.properties?.sessionID") || !strings.Contains(pluginSource, "activityID === rootID") {
		t.Fatal("activity events do not select and guard the owning root session")
	}
	if !strings.Contains(pluginSource, "catch (_)") || !strings.Contains(pluginSource, "finally") {
		t.Fatal("identity writes are not wrapped as best-effort event handling")
	}
}

// Static yolo args stay empty because --auto is added only after a
// version-aware executable probe in PrepareLaunch.
func TestNoYoloArgs(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent, got %T", got)
	}
	if len(ta.YoloArgs) != 0 {
		t.Fatalf("opencode static YoloArgs must be empty, got %v", ta.YoloArgs)
	}
}

// TestSessionArgsAppendsContinueOnResume asserts the SessionArgs
// hook returns opencode's `-c` (continue) on resume and nothing on
// dispatch. Identity is learned asynchronously, so records observed before
// discovery still resume the most recent session in the current cwd.
func TestSessionArgsAppendsContinueOnResume(t *testing.T) {
	if got := sessionArgs(adapter.ResumeRequest{ID: "x"}, "dispatched"); got != nil {
		t.Fatalf("dispatched should add no flags, got %v", got)
	}
	if got, want := sessionArgs(adapter.ResumeRequest{ID: "x"}, "resumed"), []string{"-c"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resumed got %v want %v", got, want)
	}
}

// TestNewWiresSessionArgs asserts New installs the SessionArgs hook
// and SkipPromptOnResume. Without this wiring, picking "Resume" on
// an opencode row would re-launch opencode with no continuation
// flag, starting a fresh TUI instead of resuming the prior session.
func TestNewWiresSessionArgs(t *testing.T) {
	got := New(nil)
	ta, ok := got.(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent, got %T", got)
	}
	if ta.SessionArgs == nil {
		t.Fatal("expected SessionArgs to be wired")
	}
	if !ta.SkipPromptOnResume {
		t.Fatal("expected SkipPromptOnResume to be true")
	}
}

// A recorded provider session id must resume that exact opencode session
// (--session ses_...) instead of the project's most recent (-c).
func TestResumeTargetsExactSessionWhenIDKnown(t *testing.T) {
	ag, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("expected *adapter.Agent")
	}
	got := ag.SessionArgs(adapter.ResumeRequest{ProviderSessionID: "ses_2132323b6ffe"}, "resumed")
	if len(got) != 2 || got[0] != "--session" || got[1] != "ses_2132323b6ffe" {
		t.Fatalf("resume args = %v, want exact --session", got)
	}
	if got := ag.SessionArgs(adapter.ResumeRequest{}, "resumed"); len(got) != 1 || got[0] != "-c" {
		t.Fatalf("resume args without id = %v, want -c fallback", got)
	}
}
