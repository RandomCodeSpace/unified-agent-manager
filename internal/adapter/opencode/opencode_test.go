package opencode

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter/adaptertest"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

func TestOpenCodeNew(t *testing.T) {
	agent, ok := New(nil).(*adapter.Agent)
	if !ok {
		t.Fatalf("New() = %T, want *adapter.Agent", New(nil))
	}
	if agent.Name() != "opencode" || agent.DisplayName() == "" {
		t.Fatalf("bad adapter identity: name=%q display=%q", agent.Name(), agent.DisplayName())
	}
	if agent.SessionArgs != nil {
		t.Fatal("OpenCode must not retain legacy session arguments")
	}
	if agent.PrepareLaunch == nil || agent.LiveProviderSessionID == nil || agent.ResumeKindFor == nil {
		t.Fatal("OpenCode supervisor hooks are not fully wired")
	}
	if !agent.SkipPromptOnResume {
		t.Fatal("OpenCode resume must not resend the stored prompt")
	}
}

func TestOpenCodePrepareLaunchRequiresMinimumVersionBeforeCreate(t *testing.T) {
	for _, tt := range []struct {
		version string
		wantErr bool
	}{
		{version: "1.18.0", wantErr: true},
		{version: "1.18.1"},
	} {
		t.Run(tt.version, func(t *testing.T) {
			providerPath := writeVersionedOpenCode(t, tt.version)
			t.Setenv("PATH", filepath.Dir(providerPath))
			t.Setenv("UAM_SESSION_DIR", secureOpenCodeRuntimeDir(t))
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			backend := &adaptertest.Backend{}
			agent := New(backend).(*adapter.Agent)

			_, err := agent.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: t.TempDir(), Mode: "safe"})
			if tt.wantErr {
				if err == nil || !strings.Contains(err.Error(), "required version 1.18.1") {
					t.Fatalf("Dispatch() error = %v, want minimum-version rejection", err)
				}
				if calls := backend.CallsOf("create"); len(calls) != 0 {
					t.Fatalf("unsupported version reached Backend.CreateSession: %+v", calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("Dispatch() with minimum version: %v", err)
			}
			if calls := backend.CallsOf("create"); len(calls) != 1 {
				t.Fatalf("Backend.CreateSession calls = %d, want 1", len(calls))
			}
		})
	}
}

func TestOpenCodePrepareLaunchBuildsSupervisorCommandAndNeutralEnv(t *testing.T) {
	providerPath := writeVersionedOpenCode(t, "1.18.1")
	t.Setenv("PATH", filepath.Dir(providerPath))
	runtimeDir := secureOpenCodeRuntimeDir(t)
	t.Setenv("UAM_SESSION_DIR", runtimeDir)
	rawConfig := " preserve exactly: {not-json} "
	configEnv := "OPENCODE_CONFIG_" + "CONTENT"
	t.Setenv(configEnv, rawConfig)
	backend := &adaptertest.Backend{}
	agent := New(backend).(*adapter.Agent)
	cwd := filepath.Clean(t.TempDir())
	name := "uam-opencode-deadbeef"
	providerID := "ses_exact123"

	gotSession, err := agent.Resume(context.Background(), adapter.ResumeRequest{
		ID: "deadbeef", Cwd: cwd, Mode: "yolo", SessionName: name, ProviderSessionID: providerID,
	})
	if err != nil {
		t.Fatalf("Resume(): %v", err)
	}
	if gotSession.ProviderSessionID != providerID {
		t.Fatalf("ProviderSessionID = %q, want %q", gotSession.ProviderSessionID, providerID)
	}
	calls := backend.CallsOf("create")
	if len(calls) != 1 {
		t.Fatalf("Backend.CreateSession calls = %d, want 1", len(calls))
	}
	uamExecutable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	uamExecutable, err = filepath.Abs(uamExecutable)
	if err != nil {
		t.Fatal(err)
	}
	wantCommand := []string{
		uamExecutable, "__opencode",
		"--path", providerPath,
		"--dir", cwd,
		"--name", name,
		"--runtime-dir", runtimeDir,
		"--mode", "yolo",
		"--session", providerID,
	}
	if !reflect.DeepEqual(calls[0].Command, wantCommand) {
		t.Fatalf("launch command = %#v, want %#v", calls[0].Command, wantCommand)
	}
	identityPath, err := session.ProviderIdentityPath(runtimeDir, name)
	if err != nil {
		t.Fatal(err)
	}
	wantEnv := map[string]string{
		session.ProviderIdentityFileEnv: identityPath,
		"UAM_AGENT":                     "opencode",
		"UAM_ID":                        "deadbeef",
	}
	if !reflect.DeepEqual(calls[0].Env, wantEnv) {
		t.Fatalf("launch env = %#v, want %#v", calls[0].Env, wantEnv)
	}
	if got := os.Getenv(configEnv); got != rawConfig {
		t.Fatalf("OpenCode config env = %q, want unchanged %q", got, rawConfig)
	}
}

func TestOpenCodePrepareLaunchBuildsValidatedAliasCommand(t *testing.T) {
	shell := writeOpenCodeAliasShell(t, "1.18.1")
	t.Setenv("SHELL", shell)
	t.Setenv("UAM_SESSION_DIR", secureOpenCodeRuntimeDir(t))
	cwd := filepath.Clean(t.TempDir())
	name := "uam-opencode-a1b2c3d4"

	prep, err := prepareLaunch(context.Background(), adapter.ResumeRequest{
		CommandAlias: "custom-opencode", Mode: "safe",
	}, "dispatched", name, cwd)
	if err != nil {
		t.Fatalf("prepareLaunch(): %v", err)
	}
	wantPrefix := []string{
		"__opencode",
		"--shell", shell,
		"--alias", "custom-opencode",
		"--dir", cwd,
		"--name", name,
		"--runtime-dir", session.DefaultDir(),
		"--mode", "safe",
	}
	if len(prep.Command) != len(wantPrefix)+1 || !filepath.IsAbs(prep.Command[0]) || !reflect.DeepEqual(prep.Command[1:], wantPrefix) {
		t.Fatalf("alias supervisor command = %#v, want absolute uam executable followed by %#v", prep.Command, wantPrefix)
	}
	if prep.ProviderSessionID != "" {
		t.Fatalf("dispatch ProviderSessionID = %q, want empty", prep.ProviderSessionID)
	}
}

func TestOpenCodeLiveProviderSessionIDReadsRuntimeIdentity(t *testing.T) {
	runtimeDir := secureOpenCodeRuntimeDir(t)
	t.Setenv("UAM_SESSION_DIR", runtimeDir)
	name := "uam-opencode-a1b2c3d4"
	if err := session.WriteProviderIdentity(runtimeDir, name, "ses_live123"); err != nil {
		t.Fatal(err)
	}
	agent := New(nil).(*adapter.Agent)
	got, err := agent.LiveProviderSessionID(name)
	if err != nil || got != "ses_live123" {
		t.Fatalf("LiveProviderSessionID() = (%q, %v), want (ses_live123, nil)", got, err)
	}
}

func TestOpenCodeResumeKindIsExactOnly(t *testing.T) {
	agent := New(nil).(*adapter.Agent)
	for _, tt := range []struct {
		name string
		id   string
		want adapter.ResumeKind
	}{
		{name: "valid", id: "ses_exact123", want: adapter.ResumeExact},
		{name: "maximum length", id: "ses_" + strings.Repeat("a", 60), want: adapter.ResumeExact},
		{name: "one byte over maximum", id: "ses_" + strings.Repeat("a", 61), want: adapter.ResumeUnsupported},
		{name: "missing", want: adapter.ResumeUnsupported},
		{name: "wrong prefix", id: "abc_exact123", want: adapter.ResumeUnsupported},
		{name: "too short", id: "ses_ab", want: adapter.ResumeUnsupported},
		{name: "flag-shaped", id: "ses_bad value", want: adapter.ResumeUnsupported},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := agent.ResumeKind(adapter.ResumeRequest{ProviderSessionID: tt.id}); got != tt.want {
				t.Fatalf("ResumeKind(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestOpenCodePromptDeliveryAndResumeNoReplay(t *testing.T) {
	newAgent := func(t *testing.T) (*adapter.Agent, *adaptertest.Backend, string) {
		t.Helper()
		providerPath := writeVersionedOpenCode(t, "1.18.1")
		t.Setenv("PATH", filepath.Dir(providerPath))
		t.Setenv("UAM_SESSION_DIR", secureOpenCodeRuntimeDir(t))
		backend := &adaptertest.Backend{}
		return New(backend).(*adapter.Agent), backend, filepath.Clean(t.TempDir())
	}

	t.Run("dispatch preserves Unicode and multiline prompt", func(t *testing.T) {
		agent, backend, cwd := newAgent(t)
		const prompt = "Unicode π 你好\nsecond line\tkept"
		if _, err := agent.Dispatch(context.Background(), adapter.DispatchRequest{Prompt: prompt, Cwd: cwd, Mode: "yolo"}); err != nil {
			t.Fatalf("Dispatch(): %v", err)
		}
		sends := backend.CallsOf("send")
		if len(sends) != 1 || sends[0].Text != prompt {
			t.Fatalf("dispatch sends = %#v, want one byte-preserved prompt %q", sends, prompt)
		}
	})

	t.Run("empty dispatch sends no line", func(t *testing.T) {
		agent, backend, cwd := newAgent(t)
		if _, err := agent.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: cwd, Mode: "safe"}); err != nil {
			t.Fatalf("Dispatch(): %v", err)
		}
		if sends := backend.CallsOf("send"); len(sends) != 0 {
			t.Fatalf("empty dispatch sends = %#v, want none", sends)
		}
	})

	t.Run("exact resume does not replay stored prompt", func(t *testing.T) {
		agent, backend, cwd := newAgent(t)
		if _, err := agent.Resume(context.Background(), adapter.ResumeRequest{
			ID: "deadbeef", Cwd: cwd, Mode: "yolo", SessionName: "uam-opencode-deadbeef",
			ProviderSessionID: "ses_exact123", Prompt: "stored prompt must stay inert",
		}); err != nil {
			t.Fatalf("Resume(): %v", err)
		}
		if sends := backend.CallsOf("send"); len(sends) != 0 {
			t.Fatalf("exact resume replayed prompt: %#v", sends)
		}
	})
}

func TestOpenCodeLegacyPluginAndProviderStateAreInert(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(t *testing.T, root string) string
	}{
		{
			name: "stale plugin",
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, legacyOpenCodePluginName())
				writeOpenCodeLegacyFile(t, path, "stale plugin", 0o600)
				return path
			},
		},
		{
			name: "permissive plugin",
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, legacyOpenCodePluginName())
				writeOpenCodeLegacyFile(t, path, "permissive plugin", 0o666)
				return path
			},
		},
		{
			name: "symlinked plugin",
			setup: func(t *testing.T, root string) string {
				target := filepath.Join(t.TempDir(), "target.mjs")
				writeOpenCodeLegacyFile(t, target, "symlink target", 0o600)
				path := filepath.Join(root, legacyOpenCodePluginName())
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
		{
			name: "malformed provider identity",
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, "identity", "uam-opencode-a1b2c3d4.json")
				writeOpenCodeLegacyFile(t, path, "{malformed", 0o600)
				return path
			},
		},
		{
			name: "directory-shaped plugin",
			setup: func(t *testing.T, root string) string {
				path := filepath.Join(root, legacyOpenCodePluginName())
				if err := os.MkdirAll(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			providerPath := writeVersionedOpenCode(t, "1.18.1")
			t.Setenv("PATH", filepath.Dir(providerPath))
			t.Setenv("UAM_SESSION_DIR", secureOpenCodeRuntimeDir(t))
			stateHome := t.TempDir()
			t.Setenv("XDG_STATE_HOME", stateHome)
			legacyRoot := filepath.Join(stateHome, "uam", "providers", "opencode")
			if err := os.MkdirAll(legacyRoot, 0o700); err != nil {
				t.Fatal(err)
			}
			observedPath := tt.setup(t, legacyRoot)
			before, beforeErr := os.Lstat(observedPath)
			beforeData, _ := os.ReadFile(observedPath)
			backend := &adaptertest.Backend{}

			if _, err := New(backend).Dispatch(context.Background(), adapter.DispatchRequest{Cwd: t.TempDir(), Mode: "safe"}); err != nil {
				t.Fatalf("legacy state affected dispatch: %v", err)
			}
			if len(backend.CallsOf("create")) != 1 {
				t.Fatal("dispatch did not reach Backend.CreateSession")
			}
			after, afterErr := os.Lstat(observedPath)
			afterData, _ := os.ReadFile(observedPath)
			if beforeErr != nil || afterErr != nil || before.Mode() != after.Mode() || !reflect.DeepEqual(beforeData, afterData) {
				t.Fatalf("legacy path was inspected or modified: before=(%v,%v,%q) after=(%v,%v,%q)", before, beforeErr, beforeData, after, afterErr, afterData)
			}
		})
	}
}

func TestOpenCodeProductionSourceHasNoLegacyPluginPath(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"uam-identity-plugin" + ".mjs", "plugin" + "Source", "ensureProvider" + "Files", "OPENCODE_CONFIG_" + "CONTENT", `[]string{` + `"-c"}`}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(data), token) {
				t.Errorf("production source %s contains removed token %q", entry.Name(), token)
			}
		}
	}
}

func writeVersionedOpenCode(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	script := "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '%s\\n' '" + version + "'; exit 0; fi\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeOpenCodeAliasShell(t *testing.T, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "shell")
	script := "#!/bin/sh\ncase \"$2\" in *--version*) printf '%s\\n' '" + version + "';; esac\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func secureOpenCodeRuntimeDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Clean(t.TempDir())
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeOpenCodeLegacyFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func legacyOpenCodePluginName() string {
	return "uam-identity-plugin" + ".mjs"
}
