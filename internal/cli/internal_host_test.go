package cli

import (
	"encoding/json"
	"testing"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/host"
)

// TestHostConfigSerializesIntoJSON exercises the marshal/unmarshal path
// the supervisor uses to ship a host.Config to a forked uam process.
func TestHostConfigSerializesIntoJSON(t *testing.T) {
	cfg := host.Config{
		SessionID:   "uam-test-12345678",
		Argv:        []string{"/bin/echo", "ok"},
		Cwd:         "/tmp",
		Env:         []string{"K=V"},
		Cols:        80,
		Rows:        24,
		JournalPath: "/tmp/j.log",
		SocketPath:  "/tmp/s.sock",
	}
	js, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got host.Config
	if err := json.Unmarshal(js, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.SessionID != cfg.SessionID || len(got.Argv) != 2 || got.Cols != 80 {
		t.Fatalf("roundtrip mismatch: %+v", got)
	}
}
