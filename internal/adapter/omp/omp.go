package omp

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/providerstate"
)

// omp (Oh My Pi, github.com/can1357/oh-my-pi) launches bare: a plain `omp`
// with no subcommand opens its TUI, the default surface (the other modes are
// explicit — `omp -p`, `omp --mode rpc`, `omp acp`). Unlike hermes/opencode,
// omp does expose a real auto-approve flag (`--auto-approve`, per `omp
// --help`), so it is wired as the yolo arg and appended in non-safe mode so
// dispatched sessions skip tool-call approval prompts, matching
// claude/codex/copilot. Model auth is an in-TUI `/login` OAuth flow, not an
// env var.
var yoloArgs = []string{"--auto-approve"}

// sessionArgs appends omp's `-c`/`--continue` flag on resume. New managed
// sessions also use a deterministic per-UAM-ID session directory, making that
// continue exact; legacy records without an existing derived directory keep
// the historical bare `-c` behavior.
func sessionArgs(_ adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		return []string{"-c"}
	}
	return nil
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("omp", "Oh My Pi", []adapter.CommandCandidate{{Display: "omp", Args: []string{"omp"}}}, yoloArgs, backend)
	a.SessionArgs = sessionArgs
	a.PrepareLaunch = prepareLaunch
	a.ResumeKindFor = resumeKind
	a.SkipPromptOnResume = true
	return a
}

var safeID = regexp.MustCompile(`^[0-9a-f][0-9a-f-]{7,63}$`)

func stateRoot() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "uam", "providers", "omp"), nil
}

func verifyOwnerOnlyDir(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("provider state %s is not a real directory", path)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("provider state %s has unsafe mode %04o", path, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("provider state %s is not owner-controlled", path)
	}
	return nil
}

func sessionDir(id string) (string, error) {
	if !safeID.MatchString(id) {
		return "", fmt.Errorf("unsafe UAM session id %q", id)
	}
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, id), nil
}

func ensureSessionDir(id string) (string, error) {
	dir, err := sessionDir(id)
	if err != nil {
		return "", err
	}
	root := filepath.Dir(dir)
	base := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	if err := providerstate.EnsureTrustedBase(base); err != nil {
		return "", err
	}
	if err := ensureDirChain(base, "uam", "providers", "omp"); err != nil {
		return "", err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if err := verifyOwnerOnlyDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func ensureDirChain(base string, components ...string) error {
	path := base
	for _, component := range components {
		path = filepath.Join(path, component)
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		if err := verifyOwnerOnlyDir(path); err != nil {
			return err
		}
	}
	return nil
}

func verifyDirChain(base string, components ...string) error {
	path := base
	for _, component := range components {
		path = filepath.Join(path, component)
		if err := verifyOwnerOnlyDir(path); err != nil {
			return err
		}
	}
	return nil
}

func inspectSessionDir(id string) (string, bool, error) {
	dir, err := sessionDir(id)
	if err != nil {
		return "", false, err
	}
	root := filepath.Dir(dir)
	base := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	if err := providerstate.VerifyTrustedBase(base); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if err := verifyDirChain(base, "uam", "providers", "omp"); err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	_, err = os.Lstat(dir)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if err := verifyOwnerOnlyDir(dir); err != nil {
		return "", false, err
	}
	return dir, true, nil
}

func existingSessionDir(id string) (string, bool) {
	dir, exists, err := inspectSessionDir(id)
	if err != nil {
		return "", false
	}
	return dir, exists
}

func prepareLaunch(_ adapter.Context, req adapter.ResumeRequest, activity, _, _ string) (adapter.LaunchPreparation, error) {
	if activity == "dispatched" {
		dir, err := ensureSessionDir(req.ID)
		if err != nil {
			return adapter.LaunchPreparation{}, err
		}
		return adapter.LaunchPreparation{ExtraArgs: []string{"--session-dir", dir}}, nil
	}
	dir, exists, err := inspectSessionDir(req.ID)
	if err != nil {
		return adapter.LaunchPreparation{}, err
	}
	if exists {
		return adapter.LaunchPreparation{ExtraArgs: []string{"--session-dir", dir}}, nil
	}
	return adapter.LaunchPreparation{}, nil
}

func resumeKind(req adapter.ResumeRequest) adapter.ResumeKind {
	if _, ok := existingSessionDir(req.ID); ok {
		return adapter.ResumeExact
	}
	return adapter.ResumeHeuristic
}
