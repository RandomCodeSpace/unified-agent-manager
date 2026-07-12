package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/providerstate"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
	"golang.org/x/sys/unix"
)

// OpenCode's --auto support is version-dependent, so it is appended by the
// preparation hook only after probing the resolved executable. Static yolo
// args stay empty to keep older versions safe.
var yoloArgs []string

// sessionArgs appends opencode's `-c` (continue) flag on resume.
// sessionArgs picks opencode's resume flags. opencode supports exact resume
// (`--session ses_...`). The event plugin learns that id after launch;
// records without a learned identity retain the guarded `-c` fallback.
func sessionArgs(req adapter.ResumeRequest, activity string) []string {
	if activity == "resumed" {
		if req.ProviderSessionID != "" {
			return []string{"--session", req.ProviderSessionID}
		}
		return []string{"-c"}
	}
	return nil
}

func New(backend adapter.Backend) adapter.AgentAdapter {
	a := adapter.NewAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, yoloArgs, backend)
	a.SessionArgs = sessionArgs
	a.PrepareLaunch = prepareLaunch
	a.LiveProviderSessionID = liveProviderSessionID
	a.SkipPromptOnResume = true
	return a
}

const maxIdentityBytes = 1024

var providerIDRE = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{3,60}$`)
var autoTokenRE = regexp.MustCompile(`(^|[[:space:],\[])--auto([=[:space:],\]]|$)`)

type executableIdentity struct {
	path          string
	size, mtime   int64
	device, inode uint64
}

var autoCache = struct {
	sync.Mutex
	values map[executableIdentity]bool
}{values: map[executableIdentity]bool{}}

func resetAutoCacheForTest() {
	autoCache.Lock()
	defer autoCache.Unlock()
	autoCache.values = map[executableIdentity]bool{}
}

func prepareLaunch(ctx adapter.Context, req adapter.ResumeRequest, _, sessionName, _ string) (adapter.LaunchPreparation, error) {
	pluginPath, err := ensureProviderFiles()
	if err != nil {
		return adapter.LaunchPreparation{}, err
	}
	handoff, err := identityPath(sessionName)
	if err != nil {
		return adapter.LaunchPreparation{}, err
	}
	config, err := mergeConfigContent(os.Getenv("OPENCODE_CONFIG_CONTENT"), (&url.URL{Scheme: "file", Path: pluginPath}).String())
	if err != nil {
		return adapter.LaunchPreparation{}, fmt.Errorf("merge OPENCODE_CONFIG_CONTENT: %w", err)
	}
	prep := adapter.LaunchPreparation{Env: map[string]string{
		"OPENCODE_CONFIG_CONTENT":       config,
		"UAM_OPENCODE_IDENTITY_FILE":    handoff,
		session.ProviderIdentityFileEnv: handoff,
	}}
	if req.Mode != "safe" {
		if req.ExecutablePath != "" && supportsAuto(ctx, req.ExecutablePath) {
			prep.ExtraArgs = []string{"--auto"}
		}
	}
	return prep, nil
}

func supportsAuto(ctx context.Context, path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	key := executableIdentity{path: path, size: info.Size(), mtime: info.ModTime().UnixNano()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		key.device, key.inode = uint64(st.Dev), st.Ino
	}
	autoCache.Lock()
	defer autoCache.Unlock()
	if value, ok := autoCache.values[key]; ok {
		return value
	}
	probeCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, path, "--help").CombinedOutput() // #nosec G204 -- LookPath-resolved executable, invoked without a shell.
	supported := err == nil && autoTokenRE.Match(out)
	autoCache.values[key] = supported
	return supported
}

func stateRoot() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "uam", "providers", "opencode"), nil
}

func verifyOwner(path string, wantDir bool, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (wantDir && !info.IsDir()) || (!wantDir && !info.Mode().IsRegular()) {
		return fmt.Errorf("provider state %s has unsafe file type", path)
	}
	if info.Mode().Perm() != mode {
		return fmt.Errorf("provider state %s has unsafe mode %04o; want %04o", path, info.Mode().Perm(), mode)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("provider state %s is not owned by the current user", path)
	}
	return nil
}

func ensureProviderFiles() (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	base := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	if err := providerstate.EnsureTrustedBase(base); err != nil {
		return "", err
	}
	if err := ensureDirChain(base, "uam", "providers", "opencode"); err != nil {
		return "", err
	}
	identityDir := filepath.Join(root, "identity")
	if err := os.Mkdir(identityDir, 0o700); err != nil && !os.IsExist(err) {
		return "", err
	}
	if err := verifyOwner(identityDir, true, 0o700); err != nil {
		return "", err
	}
	pluginPath := filepath.Join(root, "uam-identity-plugin.mjs")
	if _, err := os.Lstat(pluginPath); err == nil {
		if err := verifyOwner(pluginPath, false, 0o600); err != nil {
			return "", err
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmpFile, err := os.CreateTemp(root, ".uam-identity-plugin-*")
	if err != nil {
		return "", err
	}
	tmp := tmpFile.Name()
	defer func() { _ = os.Remove(tmp) }()
	if err := tmpFile.Chmod(0o600); err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	if _, err := tmpFile.WriteString(pluginSource); err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, pluginPath); err != nil {
		return "", err
	}
	if err := verifyOwner(pluginPath, false, 0o600); err != nil {
		return "", err
	}
	return pluginPath, nil
}

func ensureDirChain(base string, components ...string) error {
	path := base
	for _, component := range components {
		path = filepath.Join(path, component)
		if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
			return err
		}
		if err := verifyOwner(path, true, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func identityPath(sessionName string) (string, error) {
	if err := session.ValidateName(sessionName); err != nil {
		return "", err
	}
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "identity", sessionName+".json"), nil
}

func liveProviderSessionID(sessionName string) (string, error) {
	root, err := stateRoot()
	if err != nil {
		return "", err
	}
	base := filepath.Dir(filepath.Dir(filepath.Dir(root)))
	if err := providerstate.VerifyTrustedBase(base); err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	path := base
	for _, component := range []string{"uam", "providers", "opencode", "identity"} {
		path = filepath.Join(path, component)
		if err := verifyOwner(path, true, 0o700); err != nil {
			if os.IsNotExist(err) {
				return "", nil
			}
			return "", err
		}
	}
	path, err = identityPath(sessionName)
	if err != nil {
		return "", err
	}
	id, err := readIdentity(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return id, err
}

func readIdentity(path string) (string, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0) // #nosec G304 -- canonical-session-derived path, opened no-follow.
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return "", err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG || os.FileMode(st.Mode).Perm() != 0o600 || int(st.Uid) != os.Getuid() {
		return "", fmt.Errorf("identity handoff has unsafe owner, mode, or type")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxIdentityBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxIdentityBytes {
		return "", fmt.Errorf("identity handoff exceeds %d bytes", maxIdentityBytes)
	}
	var payload struct {
		ProviderSessionID string `json:"provider_session_id"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return "", fmt.Errorf("parse identity handoff: %w", err)
	}
	if !providerIDRE.MatchString(payload.ProviderSessionID) || !store.ValidProviderSessionID(payload.ProviderSessionID) {
		return "", fmt.Errorf("invalid OpenCode provider session id")
	}
	return payload.ProviderSessionID, nil
}

func mergeConfigContent(inline, pluginURL string) (string, error) {
	cfg := map[string]any{}
	if strings.TrimSpace(inline) != "" {
		if err := json.Unmarshal([]byte(inline), &cfg); err != nil {
			return "", err
		}
		if cfg == nil {
			return "", fmt.Errorf("inline config must be a JSON object")
		}
	}
	plugins := []any{}
	if existing, ok := cfg["plugin"]; ok {
		var compatible bool
		plugins, compatible = existing.([]any)
		if !compatible {
			return "", fmt.Errorf("plugin must be an array")
		}
	}
	for _, existing := range plugins {
		if value, ok := existing.(string); ok && value == pluginURL {
			encoded, err := json.Marshal(cfg)
			return string(encoded), err
		}
	}
	cfg["plugin"] = append(plugins, pluginURL)
	encoded, err := json.Marshal(cfg)
	return string(encoded), err
}

const pluginSource = `import { promises as fs } from "node:fs";
import { randomUUID } from "node:crypto";
let rootID = "";
const target = process.env.UAM_OPENCODE_IDENTITY_FILE;
async function record(id) {
  if (!target || typeof id !== "string" || !/^ses_[A-Za-z0-9_-]{3,60}$/.test(id)) return;
  const tmp = target + ".tmp-" + process.pid + "-" + randomUUID();
  try {
    await fs.writeFile(tmp, JSON.stringify({provider_session_id:id}), {mode:0o600});
    await fs.chmod(tmp, 0o600);
    await fs.rename(tmp, target);
  } catch (_) {
    // Identity discovery is best-effort and must never reject provider events.
  } finally {
    await fs.rm(tmp, {force:true}).catch(() => {});
  }
}
export const UAMIdentityPlugin = async () => ({
  event: async ({event}) => {
    const type = event?.type || "";
    const info = event?.properties?.info || event?.properties?.session || event?.info || {};
    const id = info.id || "";
	const activityID = event?.properties?.sessionID || event?.properties?.sessionId || info.sessionID || info.sessionId || "";
    const parent = info.parentID || info.parentId || info.parent_id || "";
    if ((type === "session.created" || type === "session.updated") && id && !parent) {
      rootID = id;
      await record(rootID);
      return;
    }
    const activity = type === "session.idle" || type === "session.status" || type === "message.updated" || type === "command.executed";
    if (activity && activityID && activityID === rootID) await record(rootID);
  }
});
`
