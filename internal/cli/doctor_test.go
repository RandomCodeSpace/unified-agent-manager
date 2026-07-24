package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/app"
	uamlog "github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

func TestDoctorShowsControllerAndProfile(t *testing.T) {
	// Given: a service with a retained profiled session and deterministic live
	// runtime diagnostics.
	svc := diagnosticTestService(t)
	svc.SessionDiagnostics = diagnosticRuntime{
		result: session.RuntimeDiagnostic{
			Protocols: []int{1, 2}, Controller: 1, Standby: 1, Observer: 2,
		},
	}

	// When: the session doctor JSON surface is rendered.
	output := captureStdout(t, func() {
		if err := runDoctor(context.Background(), svc, []string{"a1", "--json"}); err != nil {
			t.Fatal(err)
		}
	})

	// Then: it reports controller counts, effective profile, and provider policy.
	var report app.SessionDoctorReport
	if err := json.Unmarshal(output, &report); err != nil {
		t.Fatalf("decode doctor JSON: %v\n%s", err, output)
	}
	if report.Runtime.Controller != 1 || report.Runtime.Standby != 1 || report.Runtime.Observer != 2 {
		t.Fatalf("role counts = %+v", report.Runtime)
	}
	if report.EffectiveProfile != "stable" || report.ProviderPolicy.OuterScreen != "uam" {
		t.Fatalf("profile/policy = %+v", report)
	}
}

func TestDiagnosticOutputRedactsTerminalContent(t *testing.T) {
	// Given: secrets in persisted terminal-adjacent fields and a provider error.
	svc := diagnosticTestService(t)
	if err := svc.Store.Update(func(cfg *store.Config) error {
		record := cfg.Sessions["claude:a1"]
		record.Prompt = "secret-input-7f3a provider-output-91bc TOKEN=private-value"
		cfg.Sessions["claude:a1"] = record
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	svc.SessionDiagnostics = diagnosticRuntime{err: errors.New("provider-output-91bc TOKEN=private-value")}
	if err := session.EnsureDir(session.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	malformedStatePath := filepath.Join(session.DefaultDir(), "uam-claude-dead.json")
	if err := os.WriteFile(
		malformedStatePath,
		[]byte(`{"secret":"provider-output-91bc"`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := uamlog.SetLogger(slog.New(slog.NewJSONHandler(&logs, nil)))
	t.Cleanup(func() { uamlog.SetLogger(previous) })
	uamlog.Diagnostic(uamlog.DiagnosticEvent{
		Event: "attach.lifecycle", Session: "secret-input-7f3a", ClientID: "provider-output-91bc",
		Reason: "TOKEN=private-value", Provider: "TOKEN=private-value", Profile: "secret-input-7f3a",
	})

	// When: global and session doctor JSON are rendered.
	output := captureStdout(t, func() {
		if err := runDoctor(context.Background(), svc, []string{"--json"}); err != nil {
			t.Fatal(err)
		}
		if err := runDoctor(context.Background(), svc, []string{"a1", "--json"}); err != nil {
			t.Fatal(err)
		}
		hostile := `{"schema_version":4,"default_agent":"TOKEN=private-value","default_profile":"secret-input-7f3a","profiles":{"TOKEN=private-value":{"provider":"provider-output-91bc"}},"sessions":{"claude:a1":{"id":"a1","agent":"TOKEN=private-value","name":"x","workdir":"/tmp","tmux_session":"secret-input-7f3a","profile":"provider-output-91bc"}},"ui":{}}`
		if err := os.WriteFile(svc.Store.Path(), []byte(hostile), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := runDoctor(context.Background(), svc, []string{"--json"}); err != nil {
			t.Fatal(err)
		}
		if err := runDoctor(context.Background(), svc, nil); err != nil {
			t.Fatal(err)
		}
		if err := runDoctor(context.Background(), svc, []string{"a1", "--json"}); err != nil {
			t.Fatal(err)
		}
		if err := runDoctor(context.Background(), svc, []string{"a1"}); err != nil {
			t.Fatal(err)
		}
	})

	// Then: no retained diagnostic channel contains secret-bearing content.
	retained := string(output) + logs.String()
	for _, secret := range []string{"secret-input-7f3a", "provider-output-91bc", "TOKEN=private-value"} {
		if strings.Contains(retained, secret) {
			t.Fatalf("diagnostics retained sentinel %q:\n%s", secret, retained)
		}
	}
	for _, legitimate := range []string{"stable", "claude", "uam-claude-a1"} {
		if !strings.Contains(retained, legitimate) {
			t.Fatalf("diagnostics hid legitimate identifier %q:\n%s", legitimate, retained)
		}
	}
	if _, err := os.Stat(malformedStatePath); err != nil {
		t.Fatalf("doctor mutated malformed runtime state: %v", err)
	}
	t.Log("redaction_assertions sentinel_count=0 malformed_runtime_safe=true malformed_store_safe=true")
}

type diagnosticRuntime struct {
	result session.RuntimeDiagnostic
	err    error
}

func (runtime diagnosticRuntime) Doctor(context.Context, string) (session.RuntimeDiagnostic, error) {
	return runtime.result, runtime.err
}

func diagnosticTestService(t *testing.T) *app.Service {
	t.Helper()
	isolateDoctorEnvironment(t)
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	provider := "claude"
	cfg := store.DefaultConfig()
	cfg.Profiles["stable"] = store.Profile{Provider: &provider}
	cfg.Sessions["claude:a1"] = store.SessionRecord{
		ID: "a1", Agent: "claude", SessionName: "uam-claude-a1", Profile: "stable",
	}
	if err := st.Save(cfg); err != nil {
		t.Fatal(err)
	}
	agent := &doctorAdapter{name: provider}
	return app.NewService(st, adapter.NewRegistry([]adapter.AgentAdapter{agent}))
}

func TestTodo10DiagnosticEnvironmentIsolation(t *testing.T) {
	isolateDoctorEnvironment(t)
	st, err := store.Open(store.DefaultPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Save(store.DefaultConfig()); err != nil {
		t.Fatal(err)
	}
	if err := session.EnsureDir(session.DefaultDir()); err != nil {
		t.Fatal(err)
	}
}

func isolateDoctorEnvironment(t *testing.T) {
	t.Helper()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	forbidden := filepath.Join(configDir, "uam")
	root := t.TempDir()
	isolatedConfig := filepath.Join(root, "config")
	isolatedRuntime := filepath.Join(root, "runtime")
	t.Setenv("UAM_CONFIG_DIR", isolatedConfig)
	t.Setenv("UAM_SESSION_DIR", isolatedRuntime)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "xdg"))
	if filepath.Clean(filepath.Dir(store.DefaultPath())) == filepath.Clean(forbidden) {
		t.Fatalf("test config path escaped isolation: %s", store.DefaultPath())
	}
	if filepath.Clean(filepath.Dir(store.DefaultPath())) != filepath.Clean(isolatedConfig) {
		t.Fatalf("test config path is not owned temp dir: %s", store.DefaultPath())
	}
	if filepath.Clean(session.DefaultDir()) != filepath.Clean(isolatedRuntime) {
		t.Fatalf("test runtime path escaped isolation: %s", session.DefaultDir())
	}
}

type doctorAdapter struct{ name string }

func (a *doctorAdapter) Name() string              { return a.name }
func (a *doctorAdapter) DisplayName() string       { return a.name }
func (a *doctorAdapter) Available() (bool, string) { return true, "" }
func (a *doctorAdapter) Dispatch(adapter.Context, adapter.DispatchRequest) (adapter.Session, error) {
	return adapter.Session{}, nil
}
func (a *doctorAdapter) List(adapter.Context) ([]adapter.Session, error) { return nil, nil }
func (a *doctorAdapter) Peek(adapter.Context, string) (adapter.PeekResult, error) {
	return adapter.PeekResult{}, nil
}
func (a *doctorAdapter) Reply(adapter.Context, string, string) error { return nil }
func (a *doctorAdapter) Attach(string) (adapter.AttachSpec, error)   { return adapter.AttachSpec{}, nil }
func (a *doctorAdapter) Stop(adapter.Context, string) error          { return nil }
func (a *doctorAdapter) TerminalPolicy() adapter.ProviderTerminalPolicy {
	return adapter.ProviderTerminalPolicy{
		Identity: adapter.ProviderIdentity(a.name), OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative,
	}
}

func captureStdout(t *testing.T, action func()) []byte {
	t.Helper()
	old := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	t.Cleanup(func() { os.Stdout = old })
	action()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return output
}
