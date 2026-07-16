package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

const testSupervisorPassword = "supervisor-password-must-never-leak"

const (
	fakeConfigEnv = "UAM_FAKE_OPENCODE_CONFIG"
	fakeRecordEnv = "UAM_FAKE_OPENCODE_RECORD"
)

type fakeOpenCodeConfig struct {
	Directory         string          `json:"directory"`
	CreatedID         string          `json:"created_id"`
	ExistingID        string          `json:"existing_id"`
	ResponseID        string          `json:"response_id"`
	ResponseDirectory string          `json:"response_directory"`
	ResponseParentID  string          `json:"response_parent_id"`
	ResumeMissing     bool            `json:"resume_missing"`
	Events            []eventEnvelope `json:"events"`
	DisconnectSSE     bool            `json:"disconnect_sse"`
	AttachExit        int             `json:"attach_exit"`
	AttachSignal      bool            `json:"attach_signal"`
	AttachDelayMillis int             `json:"attach_delay_millis"`
	ReadStdin         bool            `json:"read_stdin"`
	FailServeAttempts int             `json:"fail_serve_attempts"`
	HealthNeverReady  bool            `json:"health_never_ready"`
	HealthLeaksSecret bool            `json:"health_leaks_secret"`
	ServerExitMillis  int             `json:"server_exit_millis"`
	CreateDelayMillis int             `json:"create_delay_millis"`
	IgnoreTerminate   bool            `json:"ignore_terminate"`
	NoisyServerLog    bool            `json:"noisy_server_log"`
}

type fakeOpenCodeRecord struct {
	Kind             string   `json:"kind"`
	PID              int      `json:"pid,omitempty"`
	Args             []string `json:"args,omitempty"`
	ID               string   `json:"id,omitempty"`
	Body             string   `json:"body,omitempty"`
	Input            string   `json:"input,omitempty"`
	Port             int      `json:"port,omitempty"`
	PasswordSet      bool     `json:"password_set,omitempty"`
	PasswordReplaced bool     `json:"password_replaced,omitempty"`
}

func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "__supervisor_test":
			err := RunSupervisorCommand(os.Args[2:])
			if err == nil || errors.Is(err, context.Canceled) {
				os.Exit(0)
			}
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(89)
		case "serve":
			os.Exit(fakeOpenCodeServe(os.Args[2:]))
		case "attach":
			os.Exit(fakeOpenCodeAttach(os.Args[2:]))
		}
	}
	os.Exit(m.Run())
}

func TestSupervisorCommandParser(t *testing.T) {
	t.Setenv("OPENCODE_SERVER_PASSWORD", testSupervisorPassword)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Clean(t.TempDir())
	runtimeDir := filepath.Clean(t.TempDir())
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	base := []string{
		"--path", executable,
		"--dir", directory,
		"--name", "uam-opencode-a1b2c3d4",
		"--runtime-dir", runtimeDir,
		"--mode", "safe",
	}

	t.Run("direct executable safe new", func(t *testing.T) {
		got, err := parseSupervisorOptions(base)
		if err != nil {
			t.Fatal(err)
		}
		want := supervisorOptions{
			Command:     providerCommand{path: executable},
			Directory:   directory,
			SessionName: "uam-opencode-a1b2c3d4",
			RuntimeDir:  runtimeDir,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("options = %#v, want %#v", got, want)
		}
	})

	t.Run("shell alias yolo exact resume", func(t *testing.T) {
		args := []string{
			"--shell", "/bin/sh", "--alias", "custom-opencode",
			"--dir", directory,
			"--name", "uam-opencode-deadbeef",
			"--runtime-dir", runtimeDir,
			"--mode", "yolo",
			"--session", "ses_exact123",
		}
		got, err := parseSupervisorOptions(args)
		if err != nil {
			t.Fatal(err)
		}
		want := supervisorOptions{
			Command:           providerCommand{shell: "/bin/sh", alias: "custom-opencode"},
			Directory:         directory,
			SessionName:       "uam-opencode-deadbeef",
			ProviderSessionID: "ses_exact123",
			Yolo:              true,
			RuntimeDir:        runtimeDir,
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("options = %#v, want %#v", got, want)
		}
	})

	tests := []struct {
		name string
		args []string
	}{
		{name: "missing command", args: withoutFlag(base, "--path")},
		{name: "missing directory", args: withoutFlag(base, "--dir")},
		{name: "missing name", args: withoutFlag(base, "--name")},
		{name: "missing runtime directory", args: withoutFlag(base, "--runtime-dir")},
		{name: "missing mode", args: withoutFlag(base, "--mode")},
		{name: "duplicate flag", args: append(append([]string(nil), base...), "--mode", "yolo")},
		{name: "direct and shell conflict", args: append(append([]string(nil), base...), "--shell", "/bin/sh", "--alias", "opencode")},
		{name: "shell without alias", args: replaceFlag(base, "--path", "", "--shell", "/bin/sh")},
		{name: "alias without shell", args: replaceFlag(base, "--path", "", "--alias", "opencode")},
		{name: "invalid session name", args: replaceFlag(base, "--name", "../escape")},
		{name: "invalid provider id", args: append(append([]string(nil), base...), "--session", "ses_bad/value")},
		{name: "invalid mode", args: replaceFlag(base, "--mode", "unsafe")},
		{name: "relative directory", args: replaceFlag(base, "--dir", "relative")},
		{name: "noncanonical directory", args: replaceFlag(base, "--dir", directory+string(os.PathSeparator)+".")},
		{name: "noncanonical runtime directory", args: replaceFlag(base, "--runtime-dir", runtimeDir+string(os.PathSeparator)+".")},
		{name: "positional argument", args: append(append([]string(nil), base...), "unexpected")},
		{name: "unknown flag", args: append(append([]string(nil), base...), "--bogus")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSupervisorOptions(tt.args)
			if err == nil {
				t.Fatal("parseSupervisorOptions succeeded")
			}
			if strings.Contains(err.Error(), testSupervisorPassword) {
				t.Fatalf("parser error leaked password: %q", err)
			}
		})
	}
}

func TestSupervisorExitError(t *testing.T) {
	err := error(&ExitError{Code: 23})
	if err.Error() != "OpenCode attach exited with code 23" {
		t.Fatalf("Error() = %q", err)
	}
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatal("errors.As did not find *ExitError")
	}
	if exitErr.ExitCode() != 23 {
		t.Fatalf("ExitCode() = %d, want 23", exitErr.ExitCode())
	}
}

func withoutFlag(args []string, flag string) []string {
	result := make([]string, 0, len(args)-2)
	for index := 0; index < len(args); index += 2 {
		if args[index] != flag {
			result = append(result, args[index], args[index+1])
		}
	}
	return result
}

func replaceFlag(args []string, flag, value string, replacement ...string) []string {
	result := make([]string, 0, len(args)+len(replacement))
	for index := 0; index < len(args); index += 2 {
		if args[index] == flag {
			if value != "" {
				result = append(result, flag, value)
			}
			result = append(result, replacement...)
			continue
		}
		result = append(result, args[index], args[index+1])
	}
	return result
}

type supervisorFixture struct {
	configPath string
	recordPath string
	options    supervisorOptions
}

func newSupervisorFixture(t *testing.T, config fakeOpenCodeConfig) supervisorFixture {
	t.Helper()
	directory := filepath.Clean(t.TempDir())
	runtimeDir := filepath.Clean(t.TempDir())
	if err := os.Chmod(runtimeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if config.Directory == "" {
		config.Directory = directory
	}
	if config.CreatedID == "" {
		config.CreatedID = "ses_created123"
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fixtureDir := t.TempDir()
	configPath := filepath.Join(fixtureDir, "config.json")
	recordPath := filepath.Join(fixtureDir, "records.jsonl")
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(fakeConfigEnv, configPath)
	t.Setenv(fakeRecordEnv, recordPath)
	t.Setenv("OPENCODE_SERVER_USERNAME", "attacker")
	t.Setenv("OPENCODE_SERVER_PASSWORD", testSupervisorPassword)
	return supervisorFixture{
		configPath: configPath,
		recordPath: recordPath,
		options: supervisorOptions{
			Command:     providerCommand{path: executable},
			Directory:   config.Directory,
			SessionName: "uam-opencode-a1b2c3d4",
			RuntimeDir:  runtimeDir,
		},
	}
}

func (f supervisorFixture) records(t *testing.T) []fakeOpenCodeRecord {
	t.Helper()
	data, err := os.ReadFile(f.recordPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var records []fakeOpenCodeRecord
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var record fakeOpenCodeRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode fake record %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func (f supervisorFixture) recordsOfKind(t *testing.T, kind string) []fakeOpenCodeRecord {
	t.Helper()
	var result []fakeOpenCodeRecord
	for _, record := range f.records(t) {
		if record.Kind == kind {
			result = append(result, record)
		}
	}
	return result
}

func (f supervisorFixture) commandArgs() []string {
	args := []string{
		"--path", f.options.Command.path,
		"--dir", f.options.Directory,
		"--name", f.options.SessionName,
		"--runtime-dir", f.options.RuntimeDir,
		"--mode", "safe",
	}
	if f.options.Yolo {
		args[len(args)-1] = "yolo"
	}
	if f.options.ProviderSessionID != "" {
		args = append(args, "--session", f.options.ProviderSessionID)
	}
	return args
}

func TestSupervisorNewAndExactResume(t *testing.T) {
	t.Run("new creates persists and attaches exact root", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{CreatedID: "ses_new123"})
		if err := runSupervisor(t.Context(), fixture.options); err != nil {
			t.Fatalf("runSupervisor: %v", err)
		}
		assertProviderIdentity(t, fixture.options, "ses_new123")
		if got := len(fixture.recordsOfKind(t, "create")); got != 1 {
			t.Fatalf("create requests = %d, want 1", got)
		}
		if got := len(fixture.recordsOfKind(t, "get")); got != 0 {
			t.Fatalf("get requests = %d, want 0", got)
		}
		assertServeAndAttachRecords(t, fixture, "ses_new123")
		assertNoSupervisorSecret(t, fixture)
		assertFakeChildrenReaped(t, fixture)
	})

	t.Run("resume validates and attaches retained exact root", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{ExistingID: "ses_resume123"})
		fixture.options.ProviderSessionID = "ses_resume123"
		if err := runSupervisor(t.Context(), fixture.options); err != nil {
			t.Fatalf("runSupervisor: %v", err)
		}
		assertProviderIdentity(t, fixture.options, "ses_resume123")
		if got := len(fixture.recordsOfKind(t, "create")); got != 0 {
			t.Fatalf("create requests = %d, want 0", got)
		}
		gets := fixture.recordsOfKind(t, "get")
		if len(gets) != 1 || gets[0].ID != "ses_resume123" {
			t.Fatalf("get records = %#v", gets)
		}
		assertServeAndAttachRecords(t, fixture, "ses_resume123")
		assertNoSupervisorSecret(t, fixture)
		assertFakeChildrenReaped(t, fixture)
	})

	t.Run("missing exact resume is actionable and never falls back", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{ResumeMissing: true})
		fixture.options.ProviderSessionID = "ses_missing123"
		err := runSupervisor(t.Context(), fixture.options)
		if err == nil || !strings.Contains(err.Error(), "ses_missing123") || !strings.Contains(strings.ToLower(err.Error()), "not found") {
			t.Fatalf("missing resume error = %v", err)
		}
		if got := fixture.recordsOfKind(t, "attach"); len(got) != 0 {
			t.Fatalf("missing resume started attach: %#v", got)
		}
		for _, record := range fixture.records(t) {
			joined := strings.Join(record.Args, " ")
			if strings.Contains(joined, " -c") || strings.Contains(joined, "--continue") {
				t.Fatalf("fallback flag found in %#v", record)
			}
		}
		assertNoSupervisorSecret(t, fixture)
		assertFakeChildrenReaped(t, fixture)
	})
}

func TestSupervisorExactRootValidation(t *testing.T) {
	tests := []struct {
		name   string
		config func(string) fakeOpenCodeConfig
		resume bool
	}{
		{
			name: "new invalid provider ID",
			config: func(directory string) fakeOpenCodeConfig {
				return fakeOpenCodeConfig{Directory: directory, CreatedID: "bad/id"}
			},
		},
		{
			name: "new child session",
			config: func(directory string) fakeOpenCodeConfig {
				return fakeOpenCodeConfig{Directory: directory, CreatedID: "ses_child123", ResponseParentID: "ses_parent123"}
			},
		},
		{
			name: "new wrong directory",
			config: func(directory string) fakeOpenCodeConfig {
				return fakeOpenCodeConfig{Directory: directory, CreatedID: "ses_wrong123", ResponseDirectory: directory + "-other"}
			},
		},
		{
			name: "resume returns different ID",
			config: func(directory string) fakeOpenCodeConfig {
				return fakeOpenCodeConfig{Directory: directory, ExistingID: "ses_exact123", ResponseID: "ses_other123"}
			},
			resume: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			directory := filepath.Clean(t.TempDir())
			fixture := newSupervisorFixture(t, tt.config(directory))
			if tt.resume {
				fixture.options.ProviderSessionID = "ses_exact123"
			}
			err := runSupervisor(t.Context(), fixture.options)
			if err == nil {
				t.Fatal("runSupervisor accepted a non-exact root session")
			}
			if got := len(fixture.recordsOfKind(t, "attach_start")); got != 0 {
				t.Fatalf("invalid root started attach: %#v", fixture.recordsOfKind(t, "attach_start"))
			}
			assertLifecycleClean(t, fixture, err)
		})
	}
}

func TestSupervisorPromptOrder(t *testing.T) {
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
		CreatedID:         "ses_prompt123",
		AttachDelayMillis: 75,
		ReadStdin:         true,
	})
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdin := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	})
	want := "queued prompt\nwith bytes\tunchanged\n"
	if _, err := io.WriteString(writer, want); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runSupervisor(t.Context(), fixture.options); err != nil {
		t.Fatalf("runSupervisor: %v", err)
	}
	attach := fixture.recordsOfKind(t, "attach")
	if len(attach) != 1 || attach[0].Input != want {
		t.Fatalf("attach input = %#v, want %q exactly once", attach, want)
	}
	assertFakeChildrenReaped(t, fixture)
}

func TestSupervisorSessionEvents(t *testing.T) {
	directory := filepath.Clean(t.TempDir())
	events := []eventEnvelope{
		fakeEvent("session.created", map[string]any{"sessionID": "ses_created123", "info": map[string]any{"id": "ses_created123", "directory": directory}}),
		fakeEvent("session.created", map[string]any{"sessionID": "ses_child123", "info": map[string]any{"id": "ses_child123", "parentID": "ses_created123", "directory": directory}}),
		fakeEvent("session.created", map[string]any{"sessionID": "ses_wrong123", "info": map[string]any{"id": "ses_wrong123", "directory": directory + "-other"}}),
		{Type: "session.created", Properties: json.RawMessage(`"malformed"`)},
		fakeEvent("session.created", map[string]any{"sessionID": "ses_new456", "info": map[string]any{"id": "ses_new456", "directory": directory}}),
		fakeEvent("session.created", map[string]any{"sessionID": "ses_new456", "info": map[string]any{"id": "ses_new456", "directory": directory}}),
	}
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
		Directory:         directory,
		CreatedID:         "ses_created123",
		Events:            events,
		AttachDelayMillis: 200,
	})
	foreignName := "uam-opencode-feedface"
	if err := session.WriteProviderIdentity(fixture.options.RuntimeDir, foreignName, "ses_foreign123"); err != nil {
		t.Fatal(err)
	}
	if err := runSupervisor(t.Context(), fixture.options); err != nil {
		t.Fatalf("runSupervisor: %v", err)
	}
	assertProviderIdentity(t, fixture.options, "ses_new456")
	foreignID, err := session.ReadProviderIdentity(fixture.options.RuntimeDir, foreignName)
	if err != nil || foreignID != "ses_foreign123" {
		t.Fatalf("foreign identity = (%q, %v), want unchanged", foreignID, err)
	}
	assertFakeChildrenReaped(t, fixture)
}

func TestSupervisorPermissionModes(t *testing.T) {
	for _, yolo := range []bool{false, true} {
		name := "safe"
		if yolo {
			name = "yolo"
		}
		t.Run(name, func(t *testing.T) {
			directory := filepath.Clean(t.TempDir())
			events := []eventEnvelope{
				fakeEvent("session.created", map[string]any{"sessionID": "ses_child123", "info": map[string]any{"id": "ses_child123", "parentID": "ses_created123", "directory": directory}}),
				fakeEvent("permission.asked", map[string]any{"id": "per_valid123", "sessionID": "ses_child123"}),
				fakeEvent("permission.asked", map[string]any{"id": "per_valid123", "sessionID": "ses_child123"}),
				fakeEvent("permission.asked", map[string]any{"id": "per_foreign123", "sessionID": "ses_foreign123"}),
				fakeEvent("permission.asked", map[string]any{"id": "bad/id", "sessionID": "ses_child123"}),
			}
			fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
				Directory:         directory,
				CreatedID:         "ses_created123",
				Events:            events,
				AttachDelayMillis: 200,
			})
			fixture.options.Yolo = yolo
			if err := runSupervisor(t.Context(), fixture.options); err != nil {
				t.Fatalf("runSupervisor: %v", err)
			}
			replies := fixture.recordsOfKind(t, "permission")
			if !yolo {
				if len(replies) != 0 {
					t.Fatalf("safe mode permission replies = %#v, want none", replies)
				}
			} else if len(replies) != 1 || replies[0].ID != "per_valid123" || replies[0].Body != `{"reply":"once"}` {
				t.Fatalf("yolo permission replies = %#v, want one once reply", replies)
			}
			assertFakeChildrenReaped(t, fixture)
		})
	}
}

func TestSupervisorPermissionActiveRootTree(t *testing.T) {
	directory := filepath.Clean(t.TempDir())
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
		Directory:         directory,
		CreatedID:         "ses_created123",
		AttachDelayMillis: 200,
		Events: []eventEnvelope{
			fakeEvent("session.created", map[string]any{"sessionID": "ses_oldchild123", "info": map[string]any{"id": "ses_oldchild123", "parentID": "ses_created123", "directory": directory}}),
			fakeEvent("session.created", map[string]any{"sessionID": "ses_newroot123", "info": map[string]any{"id": "ses_newroot123", "directory": directory}}),
			fakeEvent("permission.asked", map[string]any{"id": "per_stale123", "sessionID": "ses_oldchild123"}),
			fakeEvent("session.created", map[string]any{"sessionID": "ses_newchild123", "info": map[string]any{"id": "ses_newchild123", "parentID": "ses_newroot123", "directory": directory}}),
			fakeEvent("permission.asked", map[string]any{"id": "per_current123", "sessionID": "ses_newchild123"}),
		},
	})
	fixture.options.Yolo = true
	if err := runSupervisor(t.Context(), fixture.options); err != nil {
		t.Fatalf("runSupervisor: %v", err)
	}
	replies := fixture.recordsOfKind(t, "permission")
	if len(replies) != 1 || replies[0].ID != "per_current123" {
		t.Fatalf("active-root permission replies = %#v, want current tree only", replies)
	}
	assertProviderIdentity(t, fixture.options, "ses_newroot123")
	assertLifecycleClean(t, fixture, nil)
}

func TestSupervisorEventReconnect(t *testing.T) {
	directory := filepath.Clean(t.TempDir())
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
		Directory:         directory,
		CreatedID:         "ses_created123",
		DisconnectSSE:     true,
		AttachDelayMillis: 260,
		Events: []eventEnvelope{
			fakeEvent("session.created", map[string]any{"sessionID": "ses_new456", "info": map[string]any{"id": "ses_new456", "directory": directory}}),
			fakeEvent("permission.asked", map[string]any{"id": "per_valid123", "sessionID": "ses_new456"}),
		},
	})
	fixture.options.Yolo = true
	if err := runSupervisor(t.Context(), fixture.options); err != nil {
		t.Fatalf("runSupervisor: %v", err)
	}
	if got := len(fixture.recordsOfKind(t, "event")); got < 3 {
		t.Fatalf("event subscriptions = %d, want at least 3 reconnects", got)
	}
	if got := len(fixture.recordsOfKind(t, "permission")); got != 1 {
		t.Fatalf("permission replies after replay = %d, want 1", got)
	}
	assertProviderIdentity(t, fixture.options, "ses_new456")
	assertFakeChildrenReaped(t, fixture)
}

func TestSupervisorReconnectBackoff(t *testing.T) {
	want := []time.Duration{25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 400 * time.Millisecond, 400 * time.Millisecond}
	for attempt, duration := range want {
		if got := reconnectBackoff(attempt); got != duration {
			t.Fatalf("reconnectBackoff(%d) = %s, want %s", attempt, got, duration)
		}
	}
}

func TestSupervisorLifecycleStartup(t *testing.T) {
	t.Run("readiness timeout", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{HealthNeverReady: true})
		ctx, cancel := context.WithTimeout(t.Context(), 175*time.Millisecond)
		defer cancel()
		err := runSupervisor(ctx, fixture.options)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "readiness timed out") {
			t.Fatalf("readiness error = %v", err)
		}
		assertLifecycleClean(t, fixture, err)
	})

	t.Run("three pre-readiness process failures use three attempts", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{FailServeAttempts: 2})
		if err := runSupervisor(t.Context(), fixture.options); err != nil {
			t.Fatalf("runSupervisor: %v", err)
		}
		if got := len(fixture.recordsOfKind(t, "serve_attempt")); got != 2 {
			t.Fatalf("failed serve attempts = %d, want 2", got)
		}
		if got := len(fixture.recordsOfKind(t, "serve")); got != 1 {
			t.Fatalf("successful serve attempts = %d, want 1", got)
		}
		assertLifecycleClean(t, fixture, nil)
	})

	t.Run("server exits before attach", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
			ServerExitMillis:  100,
			CreateDelayMillis: 500,
		})
		err := runSupervisor(t.Context(), fixture.options)
		if err == nil {
			t.Fatal("runSupervisor succeeded after server exited before attach")
		}
		if got := fixture.recordsOfKind(t, "attach_start"); len(got) != 0 {
			t.Fatalf("attach started after server failure: %#v", got)
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("server failure returned attach ExitError: %v", err)
		}
		assertLifecycleClean(t, fixture, err)
	})
}

func TestSupervisorLifecycleAttachAndServerExit(t *testing.T) {
	t.Run("server dies while attach is active", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
			AttachDelayMillis: 2000,
			ServerExitMillis:  300,
		})
		err := runSupervisor(t.Context(), fixture.options)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "server exited") {
			t.Fatalf("server death error = %v", err)
		}
		if got := len(fixture.recordsOfKind(t, "attach_start")); got != 1 {
			t.Fatalf("attach start records = %#v, want one", fixture.recordsOfKind(t, "attach_start"))
		}
		assertLifecycleClean(t, fixture, err)
	})

	for _, tt := range []struct {
		name      string
		exit      int
		signal    bool
		wantCode  int
		wantError bool
	}{
		{name: "attach exit zero", exit: 0},
		{name: "attach exit 23", exit: 23, wantCode: 23, wantError: true},
		{name: "signaled attach maps to one", signal: true, wantCode: 1, wantError: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newSupervisorFixture(t, fakeOpenCodeConfig{AttachExit: tt.exit, AttachSignal: tt.signal})
			err := runSupervisor(t.Context(), fixture.options)
			if !tt.wantError {
				if err != nil {
					t.Fatalf("runSupervisor: %v", err)
				}
			} else {
				var exitErr *ExitError
				if !errors.As(err, &exitErr) || exitErr.Code != tt.wantCode {
					t.Fatalf("attach exit error = %#v, want code %d", err, tt.wantCode)
				}
			}
			assertLifecycleClean(t, fixture, err)
		})
	}
}

func TestSupervisorLifecycleCancellation(t *testing.T) {
	t.Run("context cancellation", func(t *testing.T) {
		fixture := newSupervisorFixture(t, fakeOpenCodeConfig{AttachDelayMillis: 2000})
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() { result <- runSupervisor(ctx, fixture.options) }()
		awaitFakeRecord(t, fixture, "attach_start", 2*time.Second)
		cancel()
		err := awaitSupervisorResult(t, result, 3*time.Second)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error = %v, want context.Canceled", err)
		}
		assertLifecycleClean(t, fixture, err)
	})

	t.Run("simultaneous attach exit and cancellation", func(t *testing.T) {
		for attempt := 0; attempt < 8; attempt++ {
			fixture := newSupervisorFixture(t, fakeOpenCodeConfig{AttachDelayMillis: 60, AttachExit: 23})
			ctx, cancel := context.WithCancel(t.Context())
			result := make(chan error, 1)
			go func() { result <- runSupervisor(ctx, fixture.options) }()
			awaitFakeRecord(t, fixture, "attach_start", 2*time.Second)
			time.AfterFunc(60*time.Millisecond, cancel)
			err := awaitSupervisorResult(t, result, 3*time.Second)
			var exitErr *ExitError
			if !errors.Is(err, context.Canceled) && (!errors.As(err, &exitErr) || exitErr.Code != 23) {
				t.Fatalf("attempt %d simultaneous result = %v", attempt, err)
			}
			assertLifecycleClean(t, fixture, err)
		}
	})
}

func TestSupervisorLifecycleSignals(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			fixture := newSupervisorFixture(t, fakeOpenCodeConfig{AttachDelayMillis: 2000})
			command := exec.Command(os.Args[0], append([]string{"__supervisor_test"}, fixture.commandArgs()...)...)
			command.Env = os.Environ()
			var output strings.Builder
			command.Stdout = &output
			command.Stderr = &output
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			awaitFakeRecord(t, fixture, "attach_start", 2*time.Second)
			if err := command.Process.Signal(sig); err != nil {
				t.Fatal(err)
			}
			if err := waitCommand(t, command, 4*time.Second); err != nil {
				t.Fatalf("supervisor signal exit: %v; output=%q", err, output.String())
			}
			if strings.Contains(output.String(), testSupervisorPassword) {
				t.Fatalf("signal output leaked password: %q", output.String())
			}
			assertLifecycleClean(t, fixture, nil)
		})
	}
}

func TestSupervisorLifecycleStuckChildrenEscalate(t *testing.T) {
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
		AttachDelayMillis: 5000,
		IgnoreTerminate:   true,
	})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- runSupervisor(ctx, fixture.options) }()
	awaitFakeRecord(t, fixture, "attach_start", 2*time.Second)
	started := time.Now()
	cancel()
	err := awaitSupervisorResult(t, result, 4*time.Second)
	elapsed := time.Since(started)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stuck child cancellation error = %v", err)
	}
	if elapsed < childCleanupTimeout-150*time.Millisecond || elapsed > childCleanupTimeout+1500*time.Millisecond {
		t.Fatalf("stuck child cleanup took %s, want bounded escalation near %s", elapsed, childCleanupTimeout)
	}
	assertLifecycleClean(t, fixture, err)
}

func TestSupervisorLifecycleBoundedServerLog(t *testing.T) {
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{
		AttachDelayMillis: 2000,
		ServerExitMillis:  300,
		NoisyServerLog:    true,
	})
	err := runSupervisor(t.Context(), fixture.options)
	if err == nil {
		t.Fatal("runSupervisor succeeded after noisy server exited")
	}
	if strings.Contains(err.Error(), testSupervisorPassword) || !strings.Contains(err.Error(), "<redacted>") {
		t.Fatalf("server error was not sanitized: %q", err)
	}
	if got := len([]rune(err.Error())); got > 700 {
		t.Fatalf("server error has %d runes, want bounded diagnostic", got)
	}

	ring := newByteRing(8)
	_, _ = ring.Write([]byte("012345"))
	_, _ = ring.Write([]byte("abcdef"))
	if got := string(ring.Bytes()); got != "45abcdef" {
		t.Fatalf("ring contents = %q, want newest 8 bytes", got)
	}
	assertLifecycleClean(t, fixture, err)
}

func TestSupervisorLifecycleCredentialRedaction(t *testing.T) {
	fixture := newSupervisorFixture(t, fakeOpenCodeConfig{HealthLeaksSecret: true})
	err := runSupervisor(t.Context(), fixture.options)
	if err == nil {
		t.Fatal("runSupervisor accepted secret as a server version")
	}
	if !strings.Contains(err.Error(), "<redacted>") || regexp.MustCompile(`[0-9a-f]{64}`).MatchString(err.Error()) {
		t.Fatalf("server-controlled version leaked generated credential: %q", err)
	}
	assertLifecycleClean(t, fixture, err)
}

func fakeEvent(eventType string, properties any) eventEnvelope {
	data, err := json.Marshal(properties)
	if err != nil {
		panic(err)
	}
	return eventEnvelope{Type: eventType, Properties: data}
}

func assertProviderIdentity(t *testing.T, options supervisorOptions, want string) {
	t.Helper()
	got, err := session.ReadProviderIdentity(options.RuntimeDir, options.SessionName)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("provider identity = %q, want %q", got, want)
	}
}

func assertServeAndAttachRecords(t *testing.T, fixture supervisorFixture, wantID string) {
	t.Helper()
	serves := fixture.recordsOfKind(t, "serve")
	if len(serves) != 1 || !reflect.DeepEqual(serves[0].Args[:3], []string{"--hostname", "127.0.0.1", "--port"}) || serves[0].Port < 1 {
		t.Fatalf("serve records = %#v", serves)
	}
	attaches := fixture.recordsOfKind(t, "attach")
	if len(attaches) != 1 {
		t.Fatalf("attach records = %#v", attaches)
	}
	wantArgs := []string{"http://127.0.0.1:" + strconv.Itoa(serves[0].Port), "--dir", fixture.options.Directory, "--session", wantID}
	if !reflect.DeepEqual(attaches[0].Args, wantArgs) {
		t.Fatalf("attach argv = %#v, want %#v", attaches[0].Args, wantArgs)
	}
	for _, arg := range attaches[0].Args {
		if arg == "-c" || arg == "--continue" {
			t.Fatalf("attach used fallback flag in %#v", attaches[0].Args)
		}
	}
}

func assertNoSupervisorSecret(t *testing.T, fixture supervisorFixture) {
	t.Helper()
	for _, record := range fixture.records(t) {
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), testSupervisorPassword) {
			t.Fatalf("record leaked fixed password: %s", data)
		}
		if (strings.HasPrefix(record.Kind, "serve") || strings.HasPrefix(record.Kind, "attach")) && (!record.PasswordSet || !record.PasswordReplaced) {
			t.Fatalf("child did not receive replaced credentials: %#v", record)
		}
	}
	entries, err := os.ReadDir(fixture.options.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(fixture.options.RuntimeDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), testSupervisorPassword) {
			t.Fatalf("runtime file %s leaked fixed password", entry.Name())
		}
	}
}

func assertLifecycleClean(t *testing.T, fixture supervisorFixture, returned error) {
	t.Helper()
	if returned != nil && strings.Contains(returned.Error(), testSupervisorPassword) {
		t.Fatalf("supervisor error leaked fixed password: %q", returned)
	}
	assertNoSupervisorSecret(t, fixture)
	assertFakeChildrenReaped(t, fixture)
	for _, record := range fixture.records(t) {
		if record.Port < 1 {
			continue
		}
		connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(record.Port)), 75*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			t.Fatalf("fake server listener on port %d remains open", record.Port)
		}
	}
	for _, path := range []string{fixture.configPath, fixture.recordPath} {
		data, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(data), testSupervisorPassword) {
			t.Fatalf("fixture file %s leaked fixed password", path)
		}
	}
}

func awaitFakeRecord(t *testing.T, fixture supervisorFixture, kind string, timeout time.Duration) fakeOpenCodeRecord {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		records := fixture.recordsOfKind(t, kind)
		if len(records) > 0 {
			return records[len(records)-1]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for fake record %q; records=%#v", timeout, kind, fixture.records(t))
	return fakeOpenCodeRecord{}
}

func awaitSupervisorResult(t *testing.T, result <-chan error, timeout time.Duration) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for supervisor", timeout)
		return nil
	}
}

func waitCommand(t *testing.T, command *exec.Cmd, timeout time.Duration) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		_ = command.Process.Kill()
		<-done
		t.Fatalf("timed out after %s waiting for command", timeout)
		return nil
	}
}

func assertFakeChildrenReaped(t *testing.T, fixture supervisorFixture) {
	t.Helper()
	for _, record := range fixture.records(t) {
		if !strings.HasPrefix(record.Kind, "serve") && !strings.HasPrefix(record.Kind, "attach") || record.PID <= 0 {
			continue
		}
		if err := syscall.Kill(record.PID, 0); !errors.Is(err, syscall.ESRCH) {
			t.Fatalf("fake child pid %d was not reaped: %v", record.PID, err)
		}
	}
}

var fakeRecordMu sync.Mutex

func fakeOpenCodeConfigFromEnv() (fakeOpenCodeConfig, error) {
	data, err := os.ReadFile(os.Getenv(fakeConfigEnv))
	if err != nil {
		return fakeOpenCodeConfig{}, err
	}
	var config fakeOpenCodeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fakeOpenCodeConfig{}, err
	}
	return config, nil
}

func writeFakeRecord(record fakeOpenCodeRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	fakeRecordMu.Lock()
	defer fakeRecordMu.Unlock()
	file, err := os.OpenFile(os.Getenv(fakeRecordEnv), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	_, err = file.Write(append(data, '\n'))
	return err
}

func fakeChildCredentialRecord(kind string, args []string) fakeOpenCodeRecord {
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	return fakeOpenCodeRecord{
		Kind:             kind,
		PID:              os.Getpid(),
		Args:             append([]string(nil), args...),
		PasswordSet:      password != "",
		PasswordReplaced: password != testSupervisorPassword && len(password) == 64,
	}
}

func fakeOpenCodeServe(args []string) int {
	config, err := fakeOpenCodeConfigFromEnv()
	if err != nil {
		return 90
	}
	fs := flag.NewFlagSet("fake serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	hostname := fs.String("hostname", "", "")
	port := fs.Int("port", 0, "")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 || *hostname != "127.0.0.1" || *port < 1 {
		return 91
	}
	if config.IgnoreTerminate {
		signal.Ignore(syscall.SIGHUP, syscall.SIGTERM)
	}
	if config.FailServeAttempts > 0 {
		attemptPath := os.Getenv(fakeConfigEnv) + ".attempts"
		data, _ := os.ReadFile(attemptPath)
		if len(data) < config.FailServeAttempts {
			_ = os.WriteFile(attemptPath, append(data, 'x'), 0o600)
			record := fakeChildCredentialRecord("serve_attempt", args)
			record.Port = *port
			_ = writeFakeRecord(record)
			return 92
		}
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(*hostname, strconv.Itoa(*port)))
	if err != nil {
		return 93
	}
	defer func() { _ = listener.Close() }()
	record := fakeChildCredentialRecord("serve", args)
	record.Port = *port
	if err := writeFakeRecord(record); err != nil {
		return 94
	}
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	username := os.Getenv("OPENCODE_SERVER_USERNAME")
	if config.NoisyServerLog {
		_, _ = fmt.Fprint(os.Stderr, strings.Repeat("x", (64<<10)+8192), password)
	}
	if config.ServerExitMillis > 0 {
		go func() {
			time.Sleep(time.Duration(config.ServerExitMillis) * time.Millisecond)
			os.Exit(44)
		}()
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		gotUsername, gotPassword, ok := request.BasicAuth()
		if !ok || gotUsername != username || gotPassword != password {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if request.Header.Get("X-OpenCode-Directory") != config.Directory {
			http.Error(w, "wrong directory", http.StatusBadRequest)
			return
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/global/health":
			_ = writeFakeRecord(fakeOpenCodeRecord{Kind: "health"})
			w.Header().Set("Content-Type", "application/json")
			if config.HealthNeverReady {
				_, _ = io.WriteString(w, `{"healthy":false,"version":"1.18.1"}`)
				return
			}
			if config.HealthLeaksSecret {
				_, _ = fmt.Fprintf(w, `{"healthy":true,"version":%q}`, password)
				return
			}
			_, _ = io.WriteString(w, `{"healthy":true,"version":"1.18.1"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/event":
			_ = writeFakeRecord(fakeOpenCodeRecord{Kind: "event"})
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			for _, event := range config.Events {
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
			}
			if config.DisconnectSSE {
				return
			}
			<-request.Context().Done()
		case request.Method == http.MethodPost && request.URL.Path == "/session":
			body, _ := io.ReadAll(request.Body)
			_ = writeFakeRecord(fakeOpenCodeRecord{Kind: "create", Body: string(body)})
			if config.CreateDelayMillis > 0 {
				time.Sleep(time.Duration(config.CreateDelayMillis) * time.Millisecond)
			}
			w.Header().Set("Content-Type", "application/json")
			responseID := config.ResponseID
			if responseID == "" {
				responseID = config.CreatedID
			}
			responseDirectory := config.ResponseDirectory
			if responseDirectory == "" {
				responseDirectory = config.Directory
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"parentID":%q,"directory":%q,"title":"UAM"}`, responseID, config.ResponseParentID, responseDirectory)
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/session/"):
			id := strings.TrimPrefix(request.URL.Path, "/session/")
			_ = writeFakeRecord(fakeOpenCodeRecord{Kind: "get", ID: id})
			if config.ResumeMissing || id != config.ExistingID {
				http.Error(w, "missing", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			responseID := config.ResponseID
			if responseID == "" {
				responseID = id
			}
			responseDirectory := config.ResponseDirectory
			if responseDirectory == "" {
				responseDirectory = config.Directory
			}
			_, _ = fmt.Fprintf(w, `{"id":%q,"parentID":%q,"directory":%q,"title":"existing"}`, responseID, config.ResponseParentID, responseDirectory)
		case request.Method == http.MethodPost && strings.HasPrefix(request.URL.Path, "/permission/") && strings.HasSuffix(request.URL.Path, "/reply"):
			body, _ := io.ReadAll(request.Body)
			id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, "/permission/"), "/reply")
			_ = writeFakeRecord(fakeOpenCodeRecord{Kind: "permission", ID: id, Body: string(body)})
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, request)
		}
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return 95
	}
	return 0
}

func fakeOpenCodeAttach(args []string) int {
	config, err := fakeOpenCodeConfigFromEnv()
	if err != nil {
		return 96
	}
	if config.IgnoreTerminate {
		signal.Ignore(syscall.SIGHUP, syscall.SIGTERM)
	}
	start := fakeChildCredentialRecord("attach_start", args)
	if err := writeFakeRecord(start); err != nil {
		return 97
	}
	if config.AttachDelayMillis > 0 {
		time.Sleep(time.Duration(config.AttachDelayMillis) * time.Millisecond)
	}
	var input []byte
	if config.ReadStdin {
		input, err = io.ReadAll(os.Stdin)
		if err != nil {
			return 98
		}
	}
	record := fakeChildCredentialRecord("attach", args)
	record.Input = string(input)
	if err := writeFakeRecord(record); err != nil {
		return 99
	}
	if config.AttachSignal {
		_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {}
	}
	return config.AttachExit
}
