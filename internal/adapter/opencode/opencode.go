package opencode

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/session"
)

var yoloArgs []string

var providerIDRE = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{3,60}$`)

func New(backend adapter.Backend) adapter.AgentAdapter {
	agent := adapter.NewAgent("opencode", "OpenCode", []adapter.CommandCandidate{{Display: "opencode", Args: []string{"opencode"}}}, yoloArgs, backend)
	agent.Terminal = adapter.ProviderTerminalPolicy{Identity: adapter.ProviderOpenCode, OuterScreen: adapter.OuterScreenUAM, KeyProtocol: adapter.KeyProtocolNative}
	agent.PrepareLaunch = prepareLaunch
	agent.LiveProviderSessionID = liveProviderSessionID
	agent.ResumeKindFor = resumeKind
	agent.SkipPromptOnResume = true
	return agent
}

func prepareLaunch(ctx adapter.Context, req adapter.ResumeRequest, _, sessionName, cwd string) (adapter.LaunchPreparation, error) {
	providerCommand, err := providerCommandFor(req)
	if err != nil {
		return adapter.LaunchPreparation{}, err
	}
	if err := requireMinimumVersion(ctx, providerCommand); err != nil {
		return adapter.LaunchPreparation{}, err
	}
	if req.ProviderSessionID != "" && !validOpenCodeSessionID(req.ProviderSessionID) {
		return adapter.LaunchPreparation{}, fmt.Errorf("invalid OpenCode provider session ID")
	}

	uamExecutable, err := os.Executable()
	if err != nil {
		return adapter.LaunchPreparation{}, fmt.Errorf("resolve uam executable: %w", err)
	}
	uamExecutable, err = filepath.Abs(uamExecutable)
	if err != nil {
		return adapter.LaunchPreparation{}, fmt.Errorf("make uam executable absolute: %w", err)
	}

	runtimeDir, err := openCodeRuntimeDir()
	if err != nil {
		return adapter.LaunchPreparation{}, err
	}
	identityPath, err := session.ProviderIdentityPath(runtimeDir, sessionName)
	if err != nil {
		return adapter.LaunchPreparation{}, err
	}

	internalArgv := []string{uamExecutable, "__opencode"}
	if providerCommand.path != "" {
		internalArgv = append(internalArgv, "--path", providerCommand.path)
	} else {
		internalArgv = append(internalArgv, "--shell", providerCommand.shell, "--alias", providerCommand.alias)
	}
	mode := "yolo"
	if req.Mode == "safe" {
		mode = "safe"
	}
	internalArgv = append(internalArgv,
		"--dir", cwd,
		"--name", sessionName,
		"--runtime-dir", runtimeDir,
		"--mode", mode,
	)
	if req.ProviderSessionID != "" {
		internalArgv = append(internalArgv, "--session", req.ProviderSessionID)
	}

	return adapter.LaunchPreparation{
		Command: internalArgv,
		Env: map[string]string{
			session.ProviderIdentityFileEnv: identityPath,
		},
		ProviderSessionID: req.ProviderSessionID,
	}, nil
}

func liveProviderSessionID(sessionName string) (string, error) {
	runtimeDir, err := openCodeRuntimeDir()
	if err != nil {
		return "", err
	}
	return session.ReadProviderIdentity(runtimeDir, sessionName)
}

func openCodeRuntimeDir() (string, error) {
	dir, err := filepath.Abs(session.DefaultDir())
	if err != nil {
		return "", fmt.Errorf("make OpenCode runtime directory absolute: %w", err)
	}
	return filepath.Clean(dir), nil
}

func resumeKind(req adapter.ResumeRequest) adapter.ResumeKind {
	if validOpenCodeSessionID(req.ProviderSessionID) {
		return adapter.ResumeExact
	}
	return adapter.ResumeUnsupported
}
