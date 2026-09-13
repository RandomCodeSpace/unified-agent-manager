package opencode

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

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

func prepareLaunch(ctx adapter.Context, req adapter.ResumeRequest, activity, sessionName, cwd string) (adapter.LaunchPreparation, error) {
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
	var prompt *os.File
	if activity != "resumed" && strings.TrimSpace(req.Prompt) != "" {
		prompt, err = initialPromptFile(runtimeDir, req.Prompt)
		if err != nil {
			return adapter.LaunchPreparation{}, err
		}
		internalArgv = append(internalArgv, "--prompt-fd", "3")
	}

	return adapter.LaunchPreparation{
		Command: internalArgv,
		Env: map[string]string{
			session.ProviderIdentityFileEnv: identityPath,
		},
		ProviderSessionID: req.ProviderSessionID,
		InitialPrompt:     prompt,
	}, nil
}

func initialPromptFile(dir, prompt string) (*os.File, error) {
	if err := session.EnsureDir(dir); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, "initial-prompt-")
	if err != nil {
		return nil, fmt.Errorf("create OpenCode initial prompt: %w", err)
	}
	// Unlink before writing so crashes cannot leave prompt material behind.
	if err := os.Remove(file.Name()); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("unlink OpenCode initial prompt: %w", err)
	}
	if _, err := io.WriteString(file, prompt); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("write OpenCode initial prompt: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("rewind OpenCode initial prompt: %w", err)
	}
	return file, nil
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
