package copilot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/agentapi"
	sdk "github.com/github/copilot-sdk/go"
)

func TestUsageTelemetryPreservesExplicitConfiguration(t *testing.T) {
	for _, entry := range []string{"COPILOT_OTEL_ENABLED=false", "COPILOT_OTEL_ENABLED=0", "COPILOT_OTEL_ENABLED=", "OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318", "COPILOT_OTEL_FILE_EXPORTER_PATH=/owner/export.jsonl", "OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT=true", "OTEL_SERVICE_NAME=owner", "COPILOT_OTEL_EXPORTER_TYPE=otlp-http"} {
		t.Run(entry, func(t *testing.T) {
			telemetry, err := usageTelemetry([]string{"PATH=/bin", entry}, func() (string, error) {
				t.Fatal("explicit configuration must not touch the usage directory")
				return "", nil
			})
			if err != nil || telemetry != nil {
				t.Fatalf("telemetry=%+v error=%v", telemetry, err)
			}
		})
	}
}

func TestUsageTelemetryCreatesPrivateExport(t *testing.T) {
	home := t.TempDir()
	config, err := usageTelemetry([]string{"PATH=/bin"}, func() (string, error) { return home, nil })
	if err != nil {
		t.Fatal(err)
	}
	if config.ExporterType != "file" || config.SourceName != "uam" || config.CaptureContent == nil || *config.CaptureContent {
		t.Fatalf("config=%+v", config)
	}
	if filepath.Dir(config.FilePath) != filepath.Join(home, ".copilot", "otel") || !strings.HasSuffix(config.FilePath, ".jsonl") {
		t.Fatalf("export path=%q", config.FilePath)
	}
	info, err := os.Stat(config.FilePath)
	if err != nil || info.Mode().Perm() != 0600 || info.Size() != 0 {
		t.Fatalf("private file: %v, %v", info, err)
	}
	next, err := usageTelemetry(nil, func() (string, error) { return home, nil })
	if err != nil || next.FilePath == config.FilePath {
		t.Fatalf("export reused: %+v %v", next, err)
	}
}

func TestUsageOwnershipBeforeTaskSend(t *testing.T) {
	for _, resume := range []bool{false, true} {
		t.Run(map[bool]string{false: "create", true: "resume"}[resume], func(t *testing.T) {
			p, fc := titleProvider(nil)
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			var recorded string
			var recorder agentapi.UsageSessionRecorder = p
			recorder.SetUsageSessionRecorder(func(id string, active bool) error {
				if !active {
					return nil
				}
				if len(fc.sessions) != 1 || fc.sessions[0].ID() != id || len(fc.sessions[0].sent) != 0 {
					t.Fatal("ownership was not recorded after creation and before inference")
				}
				recorded = id
				return nil
			})
			req := agentapi.OpenRequest{SessionID: "uam-task", Model: "test", Events: &recSink{}}
			if resume {
				req.ConversationID = "existing-provider-session"
			}
			conv, err := p.Open(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if recorded != conv.ID() {
				t.Fatalf("recorded=%q actual=%q", recorded, conv.ID())
			}
			if err := conv.Send(context.Background(), agentapi.Prompt{Text: "synthetic"}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUsageOwnershipFailureDisconnectsWithoutDeleting(t *testing.T) {
	for _, kind := range []string{"create", "resume", "utility"} {
		t.Run(kind, func(t *testing.T) {
			p, fc := titleProvider(func(context.Context, sdk.MessageOptions) (string, error) {
				t.Fatal("inference after ownership failure")
				return "", nil
			})
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			failed := errors.New("ownership persistence failed")
			p.SetUsageSessionRecorder(func(string, bool) error { return failed })
			var err error
			if kind == "utility" {
				_, err = p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "test", Purpose: "title", Prompt: "synthetic"})
			} else {
				req := agentapi.OpenRequest{SessionID: "uam-task", Model: "test", Events: &recSink{}}
				if kind == "resume" {
					req.ConversationID = "existing-provider-session"
				}
				_, err = p.Open(context.Background(), req)
			}
			if !errors.Is(err, failed) {
				t.Fatalf("error=%v", err)
			}
			if len(fc.sessions) != 1 || !fc.sessions[0].disconnected || len(fc.sessions[0].sent) != 0 || len(fc.deleted) != 0 {
				t.Fatalf("failure cleanup: sessions=%d deleted=%v", len(fc.sessions), fc.deleted)
			}
		})
	}
}

func TestUsageOwnershipBeforeUtilitySend(t *testing.T) {
	var recorded string
	p, fc := titleProvider(func(context.Context, sdk.MessageOptions) (string, error) {
		if recorded == "" {
			t.Fatal("utility inference before ownership")
		}
		return "synthetic", nil
	})
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	p.SetUsageSessionRecorder(func(id string, active bool) error {
		if !active {
			return nil
		}
		if len(fc.sessions) != 1 || id != fc.sessions[0].ID() {
			t.Fatal("wrong utility identity")
		}
		recorded = id
		return nil
	})
	if _, err := p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "test", Purpose: "title", Prompt: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if recorded == "" {
		t.Fatal("utility ownership missing")
	}
}

func TestUsageOwnershipReleasedAfterDisconnect(t *testing.T) {
	for _, kind := range []string{"task", "utility"} {
		t.Run(kind, func(t *testing.T) {
			p, fc := titleProvider(func(context.Context, sdk.MessageOptions) (string, error) { return "synthetic", nil })
			t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
			var states []bool
			p.SetUsageSessionRecorder(func(id string, active bool) error {
				if len(fc.sessions) != 1 || id != fc.sessions[0].ID() {
					t.Fatal("wrong session identity")
				}
				if !active && !fc.sessions[0].disconnected {
					t.Fatal("released before disconnect")
				}
				states = append(states, active)
				return nil
			})
			if kind == "utility" {
				if _, err := p.RunUtility(context.Background(), agentapi.UtilityRequest{Model: "test", Purpose: "title", Prompt: "synthetic"}); err != nil {
					t.Fatal(err)
				}
			} else {
				conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "task", Model: "test", Events: &recSink{}})
				if err != nil {
					t.Fatal(err)
				}
				if err = conv.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err = conv.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			if !slices.Equal(states, []bool{true, false}) {
				t.Fatalf("ownership=%v", states)
			}
		})
	}
}

func TestUsageOwnershipReleaseErrorSurfaced(t *testing.T) {
	p, _ := titleProvider(nil)
	t.Cleanup(func() { _ = p.Shutdown(context.Background()) })
	failed := errors.New("release persistence failed")
	p.SetUsageSessionRecorder(func(_ string, active bool) error {
		if !active {
			return failed
		}
		return nil
	})
	conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "task", Model: "test", Events: &recSink{}})
	if err != nil {
		t.Fatal(err)
	}
	if err = conv.Close(context.Background()); err == nil || !strings.Contains(err.Error(), failed.Error()) {
		t.Fatalf("close hid release error: %v", err)
	}
}

func TestSDKUsageTelemetryFailureDoesNotBlockClient(t *testing.T) {
	// Remove inherited telemetry for this test and restore it automatically.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "OTEL_") || strings.HasPrefix(key, "COPILOT_OTEL_") {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "copilot"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".copilot"), []byte("blocked directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("HOME", dir)
	client, err := newSDKClient()
	if err != nil || client == nil {
		t.Fatalf("optional telemetry blocked client: %v", err)
	}
}

func TestUsageOwnershipReleasedWhenClientStops(t *testing.T) {
	for _, kind := range []string{"watchdog", "shutdown-disconnect-failure"} {
		t.Run(kind, func(t *testing.T) {
			p, fc := titleProvider(nil)
			states := map[string][]bool{}
			p.SetUsageSessionRecorder(func(id string, active bool) error {
				if !active && fc.forced == 0 && fc.stopped == 0 {
					t.Error("ownership released before runtime stopped")
				}
				states[id] = append(states[id], active)
				return nil
			})
			conv, err := p.Open(context.Background(), agentapi.OpenRequest{SessionID: "task", Model: "test", Events: &recSink{}})
			if err != nil {
				t.Fatal(err)
			}
			// Utility sessions do not appear in p.convs but still need stop cleanup.
			if err := p.recordUsageSession(fc, "utility", true); err != nil {
				t.Fatal(err)
			}
			if kind == "watchdog" {
				p.fail(fc, "synthetic runtime exit")
			} else {
				fc.sessions[0].disconnectHook = func() error { return errors.New("disconnect failed") }
				if err := p.Shutdown(context.Background()); err == nil {
					t.Fatal("disconnect error hidden")
				}
			}
			if err := conv.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"task", "utility"} {
				if !slices.Equal(states[id], []bool{true, false}) {
					t.Fatalf("%s ownership=%v", id, states[id])
				}
			}
			_ = p.Shutdown(context.Background())
		})
	}
}

func TestUsageOwnershipStoppedClientCannotReleaseReplacement(t *testing.T) {
	p, old := titleProvider(nil)
	states := map[string][]bool{}
	p.SetUsageSessionRecorder(func(id string, active bool) error { states[id] = append(states[id], active); return nil })
	if _, err := p.ensureStarted(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.recordUsageSession(old, "same-session", true); err != nil {
		t.Fatal(err)
	}
	replacement := &fakeClient{}
	p.mu.Lock()
	p.client = replacement
	p.mu.Unlock()
	if err := p.recordUsageSession(replacement, "same-session", true); err != nil {
		t.Fatal(err)
	}
	old.ForceStop()
	if err := p.releaseClientUsage(old); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(states["same-session"], []bool{true, true}) {
		t.Fatalf("old runtime released replacement: %v", states)
	}
	if err := p.recordUsageSession(old, "late-old-session", true); !errors.Is(err, agentapi.ErrClosed) {
		t.Fatalf("detached client registration=%v", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(states["same-session"], []bool{true, true, false}) {
		t.Fatalf("replacement release=%v", states)
	}
}
