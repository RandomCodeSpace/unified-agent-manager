package supervisor

import (
	"testing"
)

func TestSpawnHostRejectsEmptyName(t *testing.T) {
	s := supForHandlers(t)
	if _, err := s.SpawnHost(SpawnSpec{Argv: []string{"/bin/echo"}}); err == nil {
		t.Fatalf("expected empty-name error")
	}
}

// TestHostConfigForBuildsConsistentPaths verifies hostConfigFor pins the
// host's journal and socket paths to the supervisor's runtime dir.
func TestHostConfigForBuildsConsistentPaths(t *testing.T) {
	s := supForHandlers(t)
	cfg := s.hostConfigFor("foo", SpawnSpec{
		SessionName: "foo",
		Argv:        []string{"/bin/x"},
		Cols:        80,
		Rows:        24,
	})
	if cfg.SessionID != "foo" {
		t.Fatalf("SessionID: %s", cfg.SessionID)
	}
	if cfg.Cols != 80 || cfg.Rows != 24 {
		t.Fatalf("dimensions not propagated: %+v", cfg)
	}
	if cfg.JournalPath != s.hostJournalPath("foo") {
		t.Fatalf("JournalPath: %s", cfg.JournalPath)
	}
	if cfg.SocketPath != s.hostSocketPath("foo") {
		t.Fatalf("SocketPath: %s", cfg.SocketPath)
	}
}

func TestPidSelfMatchesGetpid(t *testing.T) {
	if pidSelf() != getpid() {
		t.Fatalf("pidSelf must match getpid()")
	}
}
