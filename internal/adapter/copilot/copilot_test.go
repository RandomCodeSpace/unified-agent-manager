package copilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/tmux"
)

func TestNew(t *testing.T) {
	a := New(nil)
	if a == nil || a.Name() != "copilot" || a.DisplayName() == "" {
		t.Fatalf("bad adapter: %#v", a)
	}
}

func TestYoloModeUsesAutopilot(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "copilot"), "#!/bin/sh\nexit 0\n")
	tmuxPath := filepath.Join(dir, "tmux")
	writeExecutable(t, tmuxPath, `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_LOG"
exit 0
`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logPath := filepath.Join(dir, "tmux.log")
	t.Setenv("TMUX_LOG", logPath)

	client := tmux.New("uam")
	client.Executable = tmuxPath
	a := New(client)
	_, err := a.Dispatch(context.Background(), adapter.DispatchRequest{Cwd: "/tmp", Mode: "yolo"})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logData)
	if !strings.Contains(logText, "copilot --autopilot") {
		t.Fatalf("copilot yolo mode should use --autopilot: %s", logText)
	}
	if strings.Contains(logText, "--allow-all-tools") {
		t.Fatalf("copilot yolo mode should not use --allow-all-tools: %s", logText)
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
