package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

const skillsPackage = "skills@1.7.0"
const skillsSetupTimeout = 2 * time.Minute
const skillsSetupOutputLimit = 128 << 10
const skillsSetupFileLimit = 1000
const skillsSetupByteLimit = 25 << 20

var errSkillsTerminalOff = newError(http.StatusForbidden, "skills setup requires Settings → General → Shell access → Terminal on")

var skillSetupName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,99}$`)
var skillSetupRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+$`)

type skillSetupInput struct {
	Source string   `json:"source"`
	Skills []string `json:"skills,omitempty"`
}

type skillSetupResult struct {
	Output    string   `json:"output"`
	Installed []string `json:"installed,omitempty"`
}

type skillSetupRunner func(context.Context, string, []string) (string, error)

func (s *Server) handleListSkillSource(w http.ResponseWriter, r *http.Request) {
	s.handleSkillSetup(w, r, false)
}

func (s *Server) handleInstallSkills(w http.ResponseWriter, r *http.Request) {
	s.handleSkillSetup(w, r, true)
}

func (s *Server) handleSkillSetup(w http.ResponseWriter, r *http.Request, install bool) {
	var input skillSetupInput
	if !decodeBody(w, r, &input) {
		return
	}
	result, err := s.m.setupSkills(r.Context(), r.URL.Query().Get("project_id"), input, install, runSkillsSetup)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// setupSkills always lets the CLI work in a disposable project. Publishing is a
// separate create-only operation, since skills add replaces existing folders.
func (m *Manager) setupSkills(ctx context.Context, projectID string, input skillSetupInput, install bool, run skillSetupRunner) (skillSetupResult, error) {
	var result skillSetupResult
	if err := validateSkillSetup(input, install); err != nil {
		return result, err
	}
	if !m.Settings().Terminal {
		return result, errSkillsTerminalOff
	}
	scope, err := m.configurationScope(projectID)
	if err != nil {
		return result, err
	}
	stage, err := os.MkdirTemp("", "uam-skills-setup-*")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	args := []string{"--yes", skillsPackage, "add", input.Source, "--agent", "github-copilot", "--yes"}
	if install {
		args = append(args, "--copy", "--json", "--skill")
		args = append(args, input.Skills...)
	} else {
		args = append(args, "--list")
	}
	result.Output, err = run(ctx, stage, args)
	if err != nil {
		return result, newError(http.StatusBadGateway, "skills setup: %s", err)
	}
	if !install {
		return result, nil
	}
	if err := validateSkillSetupResult(result.Output, input.Skills); err != nil {
		return result, err
	}
	if err := validateStagedSkills(stage, input.Skills); err != nil {
		return result, err
	}
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	if !m.Settings().Terminal {
		return result, errSkillsTerminalOff
	}
	// Resolve again: a project may have been removed while npm was running.
	scope, err = m.configurationScope(projectID)
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	root, err := openConfigurationRoot(scope, true)
	if err != nil {
		return result, err
	}
	defer root.Close()
	rel := "skills"
	if !scope.global {
		rel = filepath.Join(".github", "skills")
	}
	if err := publishStagedSkills(root, rel, stage, input.Skills); err != nil {
		return result, err
	}
	result.Installed = slices.Clone(input.Skills)
	result.Output = fmt.Sprintf("Installed %d skill(s). Open a new Task or reopen a closed Task to load them.", len(result.Installed))
	return result, nil
}

func validateSkillSetup(input skillSetupInput, install bool) error {
	source := input.Source
	if source == "" || len(source) > 2048 || strings.TrimSpace(source) != source || strings.ContainsAny(source, "\x00\r\n") {
		return newError(http.StatusBadRequest, "source must be a GitHub owner/repository or an HTTPS repository URL")
	}
	if skillSetupRepo.MatchString(source) {
		_, repo, _ := strings.Cut(source, "/")
		if repo == "." || repo == ".." {
			return newError(http.StatusBadRequest, "source must name a repository")
		}
	} else {
		u, err := url.Parse(source)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return newError(http.StatusBadRequest, "source must be a GitHub owner/repository or an HTTPS repository URL without credentials")
		}
	}
	if !install {
		return nil
	}
	if len(input.Skills) == 0 || len(input.Skills) > 20 {
		return newError(http.StatusBadRequest, "select between 1 and 20 skill names")
	}
	seen := make(map[string]bool)
	for _, name := range input.Skills {
		if !skillSetupName.MatchString(name) || seen[name] {
			return newError(http.StatusBadRequest, "skill names must be unique lowercase names containing letters, digits, hyphens or underscores")
		}
		seen[name] = true
	}
	return nil
}

func validateSkillSetupResult(output string, names []string) error {
	var results []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(output), &results); err != nil {
		return newError(http.StatusBadGateway, "skills setup returned invalid installation results")
	}
	if len(results) != len(names) {
		return newError(http.StatusBadGateway, "skills setup did not install all selected skills")
	}
	seen := make(map[string]bool)
	for _, result := range results {
		if result.Status != "installed" || !slices.Contains(names, result.Name) || seen[result.Name] {
			return newError(http.StatusBadGateway, "skills setup did not install all selected skills")
		}
		seen[result.Name] = true
	}
	return nil
}

func validateStagedSkills(stage string, names []string) error {
	var size int64
	files := 0
	for _, name := range names {
		dir := filepath.Join(stage, ".agents", "skills", name)
		if info, err := os.Lstat(filepath.Join(dir, "SKILL.md")); err != nil || !info.Mode().IsRegular() {
			return newError(http.StatusBadGateway, "skill %q has no regular SKILL.md file", name)
		}
		content, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		if err != nil {
			return err
		}
		if err := validateConfiguration("skills", string(content)); err != nil {
			return err
		}
		err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("skill %q contains a symbolic link or special file", name)
			}
			size += info.Size()
			files++
			if size > skillsSetupByteLimit || files > skillsSetupFileLimit {
				return errors.New("selected skills exceed 25 MiB or 1000 files")
			}
			return nil
		})
		if err != nil {
			return newError(http.StatusBadGateway, "invalid skill files: %s", err)
		}
	}
	return nil
}

// The root confines writes; exclusive directory creation prevents replacing an
// existing skill. Only directories created by this call are rolled back.
func publishStagedSkills(root *os.Root, rel, stage string, names []string) (err error) {
	if err := configurationPath(root, filepath.Join(rel, "SKILL.md"), true); err != nil {
		return err
	}
	for _, name := range names {
		if _, err := root.Lstat(filepath.Join(rel, name)); !errors.Is(err, fs.ErrNotExist) {
			return newError(http.StatusConflict, "skill %q already exists; no skills were installed", name)
		}
	}
	var created []string
	defer func() {
		if err != nil {
			for _, path := range created {
				_ = root.RemoveAll(path)
			}
		}
	}()
	for _, name := range names {
		target := filepath.Join(rel, name)
		if err = root.Mkdir(target, 0o700); err != nil {
			return err
		}
		created = append(created, target)
		source := filepath.Join(stage, ".agents", "skills", name)
		err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			child, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			dest := filepath.Join(target, child)
			if entry.IsDir() {
				return root.MkdirAll(dest, 0o700)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("skill contains a symbolic link or special file")
			}
			in, err := os.Open(path)
			if err != nil {
				return err
			}
			defer in.Close()
			out, err := root.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()&0o700)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, in)
			closeErr := out.Close()
			return errors.Join(copyErr, closeErr)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func runSkillsSetup(ctx context.Context, dir string, args []string) (string, error) {
	npx, err := exec.LookPath("npx")
	if err != nil {
		return "", errors.New("install Node.js 22.20 or newer, including npm and npx, on the server first")
	}
	runCtx, cancel := context.WithTimeout(ctx, skillsSetupTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, npx, args...) // #nosec G204 G702 -- fixed pinned package and options, validated source/names, no shell.
	cmd.Dir = dir
	cmd.Env = slices.DeleteFunc(chartEnv(), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		return slices.Contains([]string{"XDG_STATE_HOME", "npm_config_cache", "DISABLE_TELEMETRY", "DO_NOT_TRACK", "CI", "npm_config_ignore_scripts", "npm_config_engine_strict"}, key)
	})
	cmd.Env = append(cmd.Env, "XDG_STATE_HOME="+filepath.Join(dir, "state"), "npm_config_cache="+filepath.Join(dir, "npm-cache"), "DISABLE_TELEMETRY=1", "DO_NOT_TRACK=1", "CI=1", "npm_config_ignore_scripts=true", "npm_config_engine_strict=true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	stdout := &capWriter{limit: skillsSetupOutputLimit, cancel: cancel}
	stderr := &capWriter{limit: skillsSetupOutputLimit, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("npx did not start: %w", err)
	}
	waitErr := cmd.Wait()
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	switch {
	case ctx.Err() != nil:
		return "", context.Cause(ctx)
	case stdout.over || stderr.over:
		return "", errors.New("npx output exceeded 128 KiB")
	case runCtx.Err() != nil:
		return "", errors.New("npx skills did not finish within two minutes")
	case waitErr != nil:
		return "", fmt.Errorf("npx skills failed: %s", strings.TrimSpace(stderr.buf.String()))
	}
	return stdout.buf.String(), nil
}
