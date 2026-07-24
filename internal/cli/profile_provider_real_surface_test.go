package cli

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"
)

func runTodo7NewProviderPromptPTY(t *testing.T, binary string, env []string, profile, provider string) ([]byte, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "new", "--profile", profile)
	cmd.Env = env
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 20, Cols: 110})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ptmx.Close() }()

	marker := "provider [" + provider + "]:"
	var capture bytes.Buffer
	buf := make([]byte, 1024)
	if err := ptmx.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(ansi.Strip(capture.String()), marker) {
		n, readErr := ptmx.Read(buf)
		if n > 0 {
			capture.Write(buf[:n])
		}
		if readErr != nil {
			t.Fatalf("PTY waiting for %q: %v\n%s", marker, readErr, ansi.Strip(capture.String()))
		}
	}
	_ = ptmx.Close()
	cancel()
	_ = cmd.Wait()
	return capture.Bytes(), []byte(ansi.Strip(capture.String()))
}
