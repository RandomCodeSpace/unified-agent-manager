package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
)

func TestProviderCommandConstruction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	writeVersionExecutable(t, path, "1.18.1\n", 0, false)

	direct, err := providerCommandFromFlags(path, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := direct.argv("serve"), []string{path, "serve"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("direct argv = %#v, want %#v", got, want)
	}

	// PATH-resolved aliases carry both fields on ResumeRequest. Preserve the
	// already-resolved executable identity instead of resolving the alias again.
	resolved, err := providerCommandFor(adapter.ResumeRequest{
		ExecutablePath: path,
		CommandAlias:   "custom-opencode",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := resolved.argv("serve"), []string{path, "serve"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved alias argv = %#v, want %#v", got, want)
	}

	shell := "/bin/sh"
	alias := "custom-opencode"
	shellCommand, err := providerCommandFromFlags("", shell, alias)
	if err != nil {
		t.Fatal(err)
	}
	wantShell := []string{shell, "-ic", "exec " + adapter.ShellJoin([]string{alias, "serve"})}
	if got := shellCommand.argv("serve"); !reflect.DeepEqual(got, wantShell) {
		t.Fatalf("alias argv = %#v, want %#v", got, wantShell)
	}

	t.Setenv("SHELL", shell)
	fromRequest, err := providerCommandFor(adapter.ResumeRequest{CommandAlias: alias})
	if err != nil {
		t.Fatal(err)
	}
	if got := fromRequest.argv("serve"); !reflect.DeepEqual(got, wantShell) {
		t.Fatalf("request alias argv = %#v, want %#v", got, wantShell)
	}
}

func TestProviderCommandRejectsInvalidForms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode")
	writeVersionExecutable(t, path, "1.18.1\n", 0, false)

	tests := []struct {
		name        string
		path, shell string
		alias       string
	}{
		{name: "relative direct path", path: "opencode"},
		{name: "non-regular direct path", path: dir},
		{name: "empty path and alias"},
		{name: "relative shell", shell: "bin/sh", alias: "custom-opencode"},
		{name: "direct path and alias", path: path, shell: "/bin/sh", alias: "custom-opencode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := providerCommandFromFlags(tt.path, tt.shell, tt.alias); err == nil {
				t.Fatalf("providerCommandFromFlags(%q, %q, %q) succeeded", tt.path, tt.shell, tt.alias)
			}
		})
	}

	for _, alias := range []string{"bad alias", "a;b", "$(boom)", "-flag", "a/b"} {
		t.Run("unsafe alias "+alias, func(t *testing.T) {
			if _, err := providerCommandFromFlags("", "/bin/sh", alias); err == nil {
				t.Fatalf("unsafe alias %q accepted", alias)
			}
		})
	}
}

func TestMinimumVersionValidation(t *testing.T) {
	const secret = "environment-value-must-not-leak"
	t.Setenv("UAM_VERSION_TEST_SECRET", secret)

	tests := []struct {
		name     string
		output   string
		exitCode int
		delay    bool
		wantErr  bool
		detected string
	}{
		{name: "below floor", output: "1.18.0\n", wantErr: true, detected: "1.18.0"},
		{name: "at floor", output: "1.18.1\n"},
		{name: "newer minor", output: "1.19.0\n"},
		{name: "newer major", output: "2.0.0\n"},
		{name: "v prefix", output: "v1.18.1\n"},
		{name: "stable build metadata", output: "1.18.1+linux.amd64\n"},
		{name: "floor prerelease", output: "1.18.1-beta.1\n", wantErr: true, detected: "1.18.1-beta.1"},
		{name: "malformed", output: "not-\x1b[31mvalid\x1b[0m\n", wantErr: true, detected: "not-valid"},
		{name: "multiple tokens", output: "opencode 1.18.1\n", wantErr: true, detected: "opencode 1.18.1"},
		{name: "nonzero exit", output: "1.18.1\n", exitCode: 23, wantErr: true, detected: "1.18.1"},
		{name: "timeout", output: "waiting\x1b[31m\n", delay: true, wantErr: true, detected: "waiting"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "opencode")
			writeVersionExecutable(t, path, tt.output, tt.exitCode, tt.delay)
			command, err := providerCommandFromFlags(path, "", "")
			if err != nil {
				t.Fatal(err)
			}

			started := time.Now()
			err = requireMinimumVersion(context.Background(), command)
			if tt.delay && time.Since(started) > 1500*time.Millisecond {
				t.Fatalf("version timeout took %s, want at most 1.5s", time.Since(started))
			}
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("requireMinimumVersion() error = %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("requireMinimumVersion() succeeded")
			}
			message := err.Error()
			for _, want := range []string{tt.detected, minimumVersion, "opencode upgrade 1.18.1"} {
				if !strings.Contains(message, want) {
					t.Errorf("error %q missing %q", message, want)
				}
			}
			if strings.Contains(message, "\x1b") {
				t.Errorf("error contains terminal control: %q", message)
			}
			if strings.Contains(message, secret) {
				t.Errorf("error leaked environment value: %q", message)
			}
		})
	}
}

func TestMinimumVersionDirectCacheUsesStatIdentity(t *testing.T) {
	for _, tt := range []struct {
		name, version string
		wantErr       bool
	}{
		{name: "success", version: "1.18.1\n"},
		{name: "failure", version: "1.18.0\n", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "opencode")
			count := filepath.Join(dir, "count")
			writeCountingVersionExecutable(t, path, count, tt.version)
			command, err := providerCommandFromFlags(path, "", "")
			if err != nil {
				t.Fatal(err)
			}

			for range 2 {
				err := requireMinimumVersion(context.Background(), command)
				if (err != nil) != tt.wantErr {
					t.Fatalf("cached probe error = %v, wantErr %v", err, tt.wantErr)
				}
			}
			assertProbeCount(t, count, "x")

			replacement := path + ".replacement"
			writeCountingVersionExecutable(t, replacement, count, tt.version)
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
			err = requireMinimumVersion(context.Background(), command)
			if (err != nil) != tt.wantErr {
				t.Fatalf("replacement probe error = %v, wantErr %v", err, tt.wantErr)
			}
			assertProbeCount(t, count, "xx")
		})
	}
}

func TestMinimumVersionDoesNotCacheShellAliases(t *testing.T) {
	dir := t.TempDir()
	shell := filepath.Join(dir, "shell")
	count := filepath.Join(dir, "count")
	writeCountingVersionExecutable(t, shell, count, "1.18.1\n")
	command, err := providerCommandFromFlags("", shell, "custom-opencode")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := requireMinimumVersion(context.Background(), command); err != nil {
			t.Fatal(err)
		}
	}
	assertProbeCount(t, count, "xx")
}

func writeVersionExecutable(t *testing.T, path, output string, exitCode int, delay bool) {
	t.Helper()
	script := "#!/bin/sh\n"
	if output != "" {
		script += "printf %s " + adapter.ShellJoin([]string{output}) + "\n"
	}
	if delay {
		script += "exec sleep 2\n"
	} else {
		script += fmt.Sprintf("exit %d\n", exitCode)
	}
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func writeCountingVersionExecutable(t *testing.T, path, count, version string) {
	t.Helper()
	script := "#!/bin/sh\n" +
		"printf x >> " + adapter.ShellJoin([]string{count}) + "\n" +
		"printf %s " + adapter.ShellJoin([]string{version}) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
}

func assertProbeCount(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != want {
		t.Fatalf("probe count = %q, want %q", got, want)
	}
}
