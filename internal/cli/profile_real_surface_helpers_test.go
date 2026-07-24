package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

func todo7RepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func runTodo7Binary(binary string, env []string, args ...string) (string, string, int) {
	var stdout, stderr bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.String(), stderr.String(), exitErr.ExitCode()
	}
	return stdout.String(), stderr.String() + err.Error(), -1
}

func runTodo7ProfilePTY(t *testing.T, binary string, env []string) ([]byte, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	cmd.Env = env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 32, Cols: 110})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close() }()
	var capture bytes.Buffer
	readUntil := func(marker string) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		if err := ptmx.SetReadDeadline(deadline); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 4096)
		for !strings.Contains(ansi.Strip(capture.String()), marker) {
			n, readErr := ptmx.Read(buf)
			if n > 0 {
				capture.Write(buf[:n])
			}
			if readErr != nil {
				t.Fatalf("PTY waiting for %q: %v\n%s", marker, readErr, ansi.Strip(capture.String()))
			}
		}
	}
	readUntil("seeded")
	if _, err := io.WriteString(ptmx, "e"); err != nil {
		t.Fatal(err)
	}
	readUntil("NEW SESSION")
	if _, err := io.WriteString(ptmx, "\x1b[Z"); err != nil {
		t.Fatal(err)
	}
	readUntil("profile focused")
	if _, err := io.WriteString(ptmx, "\x1b"); err != nil {
		t.Fatal(err)
	}
	readUntil("effective: focused")
	if _, err := io.WriteString(ptmx, "\x1b"); err != nil {
		t.Fatal(err)
	}
	_ = ptmx.Close()
	_ = cmd.Wait()
	return capture.Bytes(), []byte(ansi.Strip(capture.String()))
}

func writeTodo7Artifact(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}
