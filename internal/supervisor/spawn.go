package supervisor

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

// SpawnSpec is the supervisor-side view of a new-session request.
// Mirrors mux.SpawnSpec without importing it (avoids a cycle).
type SpawnSpec struct {
	SessionName string   `json:"session_name"`
	Argv        []string `json:"argv"`
	Env         []string `json:"env"`
	Cwd         string   `json:"cwd"`
	Cols        uint16   `json:"cols"`
	Rows        uint16   `json:"rows"`
}

// SpawnHost fork+exec's `<ownExe> internal-host --config <json>` and
// records the resulting session. The host claims its own UDS at
// hostsDir/<id>.sock; the supervisor waits up to dialTimeout for the
// socket to appear before returning.
func (s *Supervisor) SpawnHost(spec SpawnSpec) (SessionRecord, error) {
	id := spec.SessionName
	if id == "" {
		return SessionRecord{}, fmt.Errorf("supervisor: empty SessionName")
	}
	cfg := s.hostConfigFor(id, spec)
	js, err := json.Marshal(cfg)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("supervisor: marshal cfg: %w", err)
	}
	// #nosec G204 -- s.ownExe is the supervisor's own resolved binary
	// path, taken from os.Executable() at New time.
	cmd := exec.Command(s.ownExe, "internal-host", "--config", string(js))
	if err := cmd.Start(); err != nil {
		return SessionRecord{}, fmt.Errorf("supervisor spawn host: %w", err)
	}
	// Wait briefly for the host's socket to appear so the caller's next
	// RPC succeeds.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if probeHostAlive(cfg.SocketPath) {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	rec := SessionRecord{
		ID:         id,
		SocketPath: cfg.SocketPath,
		Pid:        cmd.Process.Pid,
		CreatedAt:  time.Now().Unix(),
	}
	s.mu.Lock()
	s.sessions[id] = rec
	s.mu.Unlock()
	return rec, nil
}
