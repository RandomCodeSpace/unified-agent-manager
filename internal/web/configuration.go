package web

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"
)

const maxConfigurationBytes = 256 << 10
const maxConfigurationFiles = 128
const disabledConfigurationSuffix = ".uam-disabled"

var configurationName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)

// ConfigurationFile is an exact native Copilot file. Keeping its contents
// intact preserves provider options that the guided form does not expose.
type ConfigurationFile struct {
	Name           string `json:"name"`
	Path           string `json:"path"`
	Content        string `json:"content"`
	Revision       string `json:"revision"`
	Editable       bool   `json:"editable"`
	Disabled       bool   `json:"disabled,omitempty"`
	Error          string `json:"error,omitempty"`
	ReadOnlyReason string `json:"read_only_reason,omitempty"`
}

type Configuration struct {
	Scope            string                        `json:"scope"`
	ProjectID        string                        `json:"project_id,omitempty"`
	TerminalAllowed  bool                          `json:"terminal_allowed"`
	Agents           []ConfigurationFile           `json:"agents"`
	Skills           []ConfigurationFile           `json:"skills"`
	Hooks            []ConfigurationFile           `json:"hooks"`
	Instructions     ConfigurationFile             `json:"instructions"`
	InstructionFiles []ConfigurationFile           `json:"instruction_files"`
	Conflicts        []ConfigurationConflict       `json:"conflicts,omitempty"`
	ConflictWarnings []string                      `json:"conflict_warnings,omitempty"`
	ConflictDetails  []ConfigurationConflictDetail `json:"conflict_details,omitempty"`
}

// ConfigurationConflict identifies distinct files with the same filename or
// skill directory name. It does not interpret frontmatter or select a winner.
type ConfigurationConflict struct {
	Kind  string   `json:"kind"`
	Name  string   `json:"name"`
	Paths []string `json:"paths"`
}

// ConfigurationConflictDetail identifies a source that duplicate checks could not read.
type ConfigurationConflictDetail struct {
	Kind    string `json:"kind"`
	Path    string `json:"path,omitempty"`
	Message string `json:"message"`
}

func (c *Configuration) conflictDetail(kind, path, message string) {
	detail := ConfigurationConflictDetail{Kind: kind, Path: path, Message: message}
	if slices.Contains(c.ConflictDetails, detail) {
		return
	}
	c.ConflictDetails = append(c.ConflictDetails, detail)
	if path != "" {
		message = path + ": " + message
	}
	c.ConflictWarnings = append(c.ConflictWarnings, message)
}

type configurationScope struct {
	base      string
	projectID string
	global    bool
}

func (m *Manager) configurationScope(projectID string) (configurationScope, error) {
	if projectID != "" {
		m.mu.Lock()
		defer m.mu.Unlock()
		p := m.projects[projectID]
		if p == nil {
			return configurationScope{}, newError(http.StatusNotFound, "project not found")
		}
		return configurationScope{base: p.Dir, projectID: projectID}, nil
	}
	base := os.Getenv("COPILOT_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return configurationScope{}, newError(http.StatusInternalServerError, "service home is unavailable")
		}
		base = filepath.Join(home, ".copilot")
	}
	if !filepath.IsAbs(base) {
		return configurationScope{}, newError(http.StatusConflict, "COPILOT_HOME must be an absolute directory")
	}
	return configurationScope{base: filepath.Clean(base), global: true}, nil
}

func (s configurationScope) root(kind string) string {
	if s.global {
		return filepath.Join(s.base, kind)
	}
	return filepath.Join(s.base, ".github", kind)
}

func configurationRelative(scope configurationScope, kind, name string) (string, error) {
	if !configurationName.MatchString(name) || strings.Contains(name, "..") {
		return "", newError(http.StatusBadRequest, "use a name of 1 to 128 letters, digits, dots, underscores or hyphens")
	}
	var path string
	switch kind {
	case "agents":
		path = filepath.Join("agents", name+".agent.md")
	case "skills":
		path = filepath.Join("skills", name, "SKILL.md")
	case "hooks":
		path = filepath.Join("hooks", name+".json")
	case "instructions":
		switch name {
		case "copilot-instructions":
			path = "copilot-instructions.md"
		case "agents":
			if scope.global {
				return "", newError(http.StatusBadRequest, "AGENTS.md is a project instruction file; use global copilot-instructions.md for instructions across projects")
			}
			return "AGENTS.md", nil
		default:
			return "", newError(http.StatusBadRequest, "unknown instructions file")
		}
	default:
		return "", newError(http.StatusBadRequest, "unknown configuration kind")
	}
	if !scope.global {
		path = filepath.Join(".github", path)
	}
	return path, nil
}

func openConfigurationRoot(scope configurationScope, create bool) (*os.Root, error) {
	if create && scope.global {
		if err := os.MkdirAll(scope.base, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(scope.base)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("configuration directory must not be a symbolic link")
	}
	return os.OpenRoot(scope.base)
}

// Every component is checked through the anchored root. Opening with
// O_NONBLOCK avoids hanging on a file replaced by a FIFO after Lstat.
func configurationPath(root *os.Root, path string, create bool) error {
	parts := strings.Split(filepath.Clean(path), string(filepath.Separator))
	for i := range parts {
		part := filepath.Join(parts[:i+1]...)
		info, err := root.Lstat(part)
		if errors.Is(err, os.ErrNotExist) && create && i < len(parts)-1 {
			if err = root.Mkdir(part, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			info, err = root.Lstat(part)
		}
		if errors.Is(err, os.ErrNotExist) && i == len(parts)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("symbolic links cannot be managed here")
		}
		if i < len(parts)-1 && !info.IsDir() {
			return errors.New("configuration parent is not a directory")
		}
		if i == len(parts)-1 && !info.Mode().IsRegular() {
			return errors.New("configuration must be a regular file")
		}
	}
	return nil
}

func readConfiguration(root *os.Root, path string) (string, string, error) {
	if err := configurationPath(root, path, false); err != nil {
		return "", "", err
	}
	f, err := root.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("configuration must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, maxConfigurationBytes+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > maxConfigurationBytes {
		return "", "", errors.New("file exceeds the 256 KiB editing limit")
	}
	if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return "", "", errors.New("configuration must be UTF-8 text")
	}
	sum := sha256.Sum256(data)
	return string(data), hex.EncodeToString(sum[:]), nil
}

func configurationFile(scope configurationScope, kind, name string, editable bool) ConfigurationFile {
	path, err := configurationRelative(scope, kind, name)
	entry := ConfigurationFile{Name: name, Path: filepath.Join(scope.base, path), Editable: editable}
	if err != nil {
		entry.Error = err.Error()
		entry.Editable = false
		return entry
	}
	return configurationFileAtPath(scope, kind, name, path, editable)
}

func configurationFileAtPath(scope configurationScope, kind, name, path string, editable bool) ConfigurationFile {
	entry := ConfigurationFile{Name: name, Path: filepath.Join(scope.base, path), Editable: editable, Disabled: strings.HasSuffix(path, disabledConfigurationSuffix)}
	if _, err := configurationRelative(scope, kind, name); err != nil {
		entry.Error = err.Error()
		entry.Editable = false
		return entry
	}
	root, err := openConfigurationRoot(scope, false)
	if err == nil {
		defer root.Close()
		entry.Content, entry.Revision, err = readConfiguration(root, path)
	}
	if err != nil && !(kind == "instructions" && errors.Is(err, os.ErrNotExist)) {
		entry.Error = "File cannot be edited: " + shortError(err)
		entry.Editable = false
	}
	return entry
}

func listConfiguration(scope configurationScope, kind string, editable bool) ([]ConfigurationFile, error) {
	if kind == "skills" {
		return listConfigurationSkills(scope.root(kind), scope, editable)
	}
	out := []ConfigurationFile{}
	root, err := openConfigurationRoot(scope, false)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()
	path, err := filepath.Rel(scope.base, scope.root(kind))
	if err != nil {
		return nil, err
	}
	// Probe a child to check the directory itself and all its ancestors.
	if err = configurationPath(root, filepath.Join(path, ".probe"), false); errors.Is(err, os.ErrNotExist) {
		return out, nil
	} else if err != nil {
		return nil, err
	}
	dir, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxConfigurationFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxConfigurationFiles {
		return nil, errors.New("configuration directory exceeds 128 entries")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	bytes := 0
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), disabledConfigurationSuffix)
		switch kind {
		case "agents":
			if !strings.HasSuffix(name, ".agent.md") {
				continue
			}
			name = strings.TrimSuffix(name, ".agent.md")
		case "hooks":
			if !strings.HasSuffix(name, ".json") {
				continue
			}
			name = strings.TrimSuffix(name, ".json")
		}
		file := configurationFileAtPath(scope, kind, name, filepath.Join(path, entry.Name()), editable)
		bytes += len(file.Content)
		if bytes > 4<<20 {
			return nil, errors.New("configuration contents exceed the 4 MiB listing limit")
		}
		out = append(out, file)
	}
	return out, nil
}

// Discovered skills may use directory or SKILL.md symlinks. Path identifies
// the resolved file for deduplication; the catalogue applies writable-root
// checks after combining native, shared and built-in discovery sources.
func listConfigurationSkills(dir string, scope configurationScope, editable bool) ([]ConfigurationFile, error) {
	out := []ConfigurationFile{}
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if _, statErr := os.Lstat(dir); errors.Is(statErr, os.ErrNotExist) {
				return out, nil
			}
		}
		return []ConfigurationFile{{Name: filepath.Base(dir), Path: dir, Error: "Skill directory cannot be read: " + shortError(err)}}, nil
	}
	defer f.Close()
	entries, err := f.ReadDir(maxConfigurationFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return []ConfigurationFile{{Name: filepath.Base(dir), Path: dir, Error: "Skill directory cannot be read: " + shortError(err)}}, nil
	}
	if len(entries) > maxConfigurationFiles {
		return nil, errors.New("configuration directory exceeds 128 entries")
	}
	slices.SortFunc(entries, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	bytes := 0
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			continue
		}
		path := filepath.Join(dir, entry.Name(), "SKILL.md")
		actual, activeErr := filepath.EvalSymlinks(path)
		disabled, disabledErr := disabledConfigurationSkillPath(path)
		_, activeStatErr := os.Lstat(path)
		_, disabledStatErr := os.Lstat(path + disabledConfigurationSuffix)
		type skillCandidate struct {
			path, actual string
			err          error
			disabled     bool
		}
		candidates := []skillCandidate{}
		if activeErr == nil || disabledErr != nil && (!errors.Is(activeStatErr, os.ErrNotExist) || entry.Type()&os.ModeSymlink != 0) {
			candidates = append(candidates, skillCandidate{path, actual, activeErr, false})
		}
		if disabledErr == nil || !errors.Is(disabledStatErr, os.ErrNotExist) {
			candidates = append(candidates, skillCandidate{path + disabledConfigurationSuffix, disabled, disabledErr, true})
		}
		for _, candidate := range candidates {
			file := ConfigurationFile{Name: entry.Name(), Path: candidate.path, Disabled: candidate.disabled}
			err := candidate.err
			if err == nil {
				file.Path, err = filepath.Abs(candidate.actual)
			}
			if err == nil {
				var root *os.Root
				root, err = os.OpenRoot(filepath.Dir(file.Path))
				if err == nil {
					file.Content, file.Revision, err = readConfiguration(root, filepath.Base(file.Path))
					_ = root.Close()
				}
			}
			if err != nil {
				file.Error = "Skill cannot be read: " + shortError(err)
			} else if editable {
				if relative, pathErr := configurationRelative(scope, "skills", entry.Name()); pathErr == nil {
					if candidate.disabled {
						relative += disabledConfigurationSuffix
					}
					if root, openErr := openConfigurationRoot(scope, false); openErr == nil {
						file.Editable = configurationPath(root, relative, false) == nil
						_ = root.Close()
					}
				}
			}
			bytes += len(file.Content)
			if bytes > 4<<20 {
				return nil, errors.New("configuration contents exceed the 4 MiB listing limit")
			}
			out = append(out, file)
		}
	}
	return out, nil
}

// A SKILL.md link remains in place when its canonical file is disabled. Follow
// its target to the disabled sibling so aliases still deduplicate and restore.
func disabledConfigurationSkillPath(path string) (string, error) {
	for range 40 {
		actual, err := filepath.EvalSymlinks(path + disabledConfigurationSuffix)
		if err == nil {
			return actual, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		target, linkErr := os.Readlink(path)
		if linkErr != nil {
			return "", err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(path), target)
		}
		path = target
	}
	return "", errors.New("too many skill symbolic links")
}

func (m *Manager) Configuration(projectID string) (Configuration, error) {
	scope, err := m.configurationScope(projectID)
	if err != nil {
		return Configuration{}, err
	}
	m.mu.Lock()
	terminal := m.settings.Terminal
	m.mu.Unlock()
	out := Configuration{Scope: "project", ProjectID: projectID, TerminalAllowed: terminal}
	if scope.global {
		out.Scope = "global"
	}
	for kind, dest := range map[string]*[]ConfigurationFile{"agents": &out.Agents, "hooks": &out.Hooks} {
		*dest, err = listConfiguration(scope, kind, true)
		if err != nil {
			return Configuration{}, newError(http.StatusConflict, "cannot list %s: %s", kind, shortError(err))
		}
	}
	out.Skills, err = m.configurationSkillsForScope(scope)
	if err != nil {
		return Configuration{}, newError(http.StatusConflict, "cannot list skills: %s", shortError(err))
	}
	out.Instructions = configurationFile(scope, "instructions", "copilot-instructions", true)
	out.InstructionFiles = []ConfigurationFile{out.Instructions}
	if !scope.global {
		out.InstructionFiles = append(out.InstructionFiles, configurationFile(scope, "instructions", "agents", true))
	}
	m.configurationConflicts(&out, scope)
	// Failed discoveries are diagnostics, not installed skills.
	out.Skills = slices.DeleteFunc(out.Skills, func(file ConfigurationFile) bool { return file.Error != "" })
	return out, nil
}

func (m *Manager) configurationConflicts(out *Configuration, scope configurationScope) {
	files := map[string][]ConfigurationFile{"agents": slices.Clone(out.Agents), "skills": slices.Clone(out.Skills)}
	if !scope.global {
		global, err := m.configurationScope("")
		if err != nil {
			for _, kind := range []string{"agents", "skills"} {
				out.conflictDetail(kind, "", "Global "+kind+" duplicates could not be checked: "+shortError(err))
			}
		} else {
			for _, kind := range []string{"agents", "skills"} {
				var discovered []ConfigurationFile
				if kind == "skills" {
					discovered, err = m.configurationSkillsForScope(global)
				} else {
					discovered, err = listConfiguration(global, kind, false)
				}
				if err != nil {
					out.conflictDetail(kind, global.root(kind), "Global "+kind+" duplicates could not be checked: "+shortError(err))
					continue
				}
				files[kind] = append(files[kind], discovered...)
			}
		}
	}
	for _, kind := range []string{"agents", "skills"} {
		for _, file := range files[kind] {
			if file.Error != "" {
				out.conflictDetail(kind, file.Path, file.Error)
			}
		}
		out.Conflicts = append(out.Conflicts, configurationDuplicateDefinitions(kind, files[kind])...)
	}
}

func configurationDuplicateDefinitions(kind string, files []ConfigurationFile) []ConfigurationConflict {
	byName := map[string][]string{}
	for _, file := range files {
		if file.Disabled || file.Error != "" || file.Revision == "" {
			continue
		}
		actual, err := filepath.EvalSymlinks(file.Path)
		if err != nil {
			continue
		}
		actual, err = filepath.Abs(actual)
		if err != nil || slices.Contains(byName[file.Name], actual) {
			continue
		}
		byName[file.Name] = append(byName[file.Name], actual)
	}
	names := []string{}
	for name, paths := range byName {
		if len(paths) > 1 {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var conflicts []ConfigurationConflict
	for _, name := range names {
		paths := byName[name]
		slices.Sort(paths)
		conflicts = append(conflicts, ConfigurationConflict{Kind: kind, Name: name, Paths: paths})
	}
	return conflicts
}

func (m *Manager) configurationSkillsForScope(scope configurationScope) ([]ConfigurationFile, error) {
	m.mu.Lock()
	builtins := slices.Clone(m.skillDirs)
	m.mu.Unlock()
	files, err := listConfigurationSkills(scope.root("skills"), scope, true)
	if err != nil {
		return nil, err
	}
	// Shared ecosystem directories and UAM's own skills are visible without
	// turning this editor into a general filesystem writer.
	var extra []string
	if scope.global {
		if home, e := os.UserHomeDir(); e == nil {
			extra = append(extra, filepath.Join(home, ".agents", "skills"), filepath.Join(home, ".claude", "skills"))
		}
		extra = append(extra, builtins...)
	} else {
		extra = append(extra, filepath.Join(scope.base, ".agents", "skills"), filepath.Join(scope.base, ".claude", "skills"))
	}
	seen := map[string]bool{scope.root("skills"): true}
	for _, dir := range extra {
		if seen[dir] {
			continue
		}
		seen[dir] = true
		discovered, e := listConfigurationSkills(dir, configurationScope{}, false)
		if e != nil {
			files = append(files, ConfigurationFile{Name: filepath.Base(dir), Path: dir, Error: "Skill directory cannot be read: " + shortError(e)})
			continue
		}
		files = append(files, discovered...)
	}
	// All successful reads report the resolved SKILL.md path. Keep a managed
	// direct file when an alias or another discovery source reaches it too.
	seenSkills := map[string]int{}
	unique := files[:0]
	for _, file := range files {
		if i, ok := seenSkills[file.Path]; ok {
			if file.Editable && !unique[i].Editable || file.Name == filepath.Base(filepath.Dir(file.Path)) && unique[i].Name != file.Name {
				unique[i] = file
			}
			continue
		}
		seenSkills[file.Path] = len(unique)
		unique = append(unique, file)
	}
	for i := range unique {
		file := &unique[i]
		file.Editable = false
		if file.Error != "" {
			file.ReadOnlyReason = file.Error
			continue
		}
		protected := false
		for _, dir := range builtins {
			actual, err := filepath.EvalSymlinks(dir)
			if err == nil {
				relative, err := filepath.Rel(actual, file.Path)
				if err == nil && filepath.IsLocal(relative) {
					protected = true
					break
				}
			}
		}
		if protected {
			file.ReadOnlyReason = "Built-in skills are managed by UAM."
			continue
		}
		root, relative, err := openConfigurationSkillRoot(scope, file.Path)
		if err == nil {
			err = configurationPath(root, relative, false)
			_ = root.Close()
		}
		if err != nil {
			file.ReadOnlyReason = "Skill path cannot be managed: " + shortError(err)
			continue
		}
		file.Editable = configurationName.MatchString(file.Name) && !strings.Contains(file.Name, "..")
		if !file.Editable {
			file.ReadOnlyReason = "The skill directory name is not supported by this editor."
		}
	}
	return unique, nil
}

// Only canonical skill files inside known directories for the selected scope
// can be managed. Walk from the scope into a known skills root without
// following replacement links, then confine mutations to that opened root.
func openConfigurationSkillRoot(scope configurationScope, path string) (*os.Root, string, error) {
	sharedBase := scope.base
	if scope.global {
		sharedBase, _ = os.UserHomeDir()
	}
	roots := []struct{ base, dir string }{{scope.base, scope.root("skills")}}
	if sharedBase != "" {
		for _, kind := range []string{".agents", ".claude"} {
			roots = append(roots, struct{ base, dir string }{sharedBase, filepath.Join(sharedBase, kind, "skills")})
		}
	}
	for _, candidate := range roots {
		relative, err := filepath.Rel(candidate.dir, path)
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		parts := strings.Split(relative, string(filepath.Separator))
		if len(parts) != 2 || strings.TrimSuffix(parts[1], disabledConfigurationSuffix) != "SKILL.md" {
			continue
		}
		root, err := openConfigurationRoot(configurationScope{base: candidate.base}, false)
		if err != nil {
			return nil, "", err
		}
		directories, err := filepath.Rel(candidate.base, candidate.dir)
		if err != nil || !filepath.IsLocal(directories) {
			_ = root.Close()
			return nil, "", errors.New("skill directory is outside its configuration root")
		}
		for _, directory := range strings.Split(directories, string(filepath.Separator)) {
			info, err := root.Lstat(directory)
			if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				_ = root.Close()
				return nil, "", errors.New("skill directory is unavailable or is a symbolic link")
			}
			next, err := root.OpenRoot(directory)
			_ = root.Close()
			if err != nil {
				return nil, "", err
			}
			opened, err := next.Stat(".")
			if err != nil || !os.SameFile(info, opened) {
				_ = next.Close()
				return nil, "", errors.New("skill directory changed while opening it")
			}
			root = next
		}
		return root, relative, nil
	}
	return nil, "", errors.New("this skill resolves outside this scope's managed skill directories")
}

func validateConfiguration(kind, content string) error {
	if len(content) > maxConfigurationBytes {
		return newError(http.StatusRequestEntityTooLarge, "configuration exceeds 256 KiB")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return newError(http.StatusBadRequest, "configuration must be UTF-8 text")
	}
	if kind == "agents" || kind == "skills" {
		// Native YAML is deliberately not translated or reserialized. Copilot
		// owns its schema, including fields introduced by newer CLI versions.
		normal := strings.ReplaceAll(content, "\r\n", "\n")
		if !strings.HasPrefix(normal, "---\n") || !strings.Contains(normal[4:], "\n---") {
			return newError(http.StatusBadRequest, "include YAML frontmatter between --- lines and the Markdown instructions")
		}
	}
	if kind == "hooks" {
		var cfg struct {
			Version int                          `json:"version"`
			Hooks   map[string][]json.RawMessage `json:"hooks"`
		}
		if json.Unmarshal([]byte(content), &cfg) != nil || cfg.Version != 1 || cfg.Hooks == nil {
			return newError(http.StatusBadRequest, "hooks need valid JSON with version 1 and a hooks object containing event arrays")
		}
		for _, hooks := range cfg.Hooks {
			if hooks == nil {
				return newError(http.StatusBadRequest, "each hook event must contain an array")
			}
			for _, hook := range hooks {
				var fields map[string]json.RawMessage
				if json.Unmarshal(hook, &fields) != nil || fields == nil {
					return newError(http.StatusBadRequest, "each hook must be a JSON object")
				}
			}
		}
	}
	return nil
}

type configurationInput struct {
	Content  string `json:"content"`
	Revision string `json:"revision"`
	Path     string `json:"path,omitempty"`
	Disabled *bool  `json:"disabled,omitempty"`
}

func (m *Manager) SaveConfiguration(projectID, kind, name string, in configurationInput, remove bool) (ConfigurationFile, error) {
	m.settingsMu.Lock()
	defer m.settingsMu.Unlock()
	scope, err := m.configurationScope(projectID)
	if err != nil {
		return ConfigurationFile{}, err
	}
	path, err := configurationRelative(scope, kind, name)
	if err != nil {
		return ConfigurationFile{}, err
	}
	m.mu.Lock()
	terminal := m.settings.Terminal
	m.mu.Unlock()
	if kind != "instructions" && !terminal {
		return ConfigurationFile{}, newError(http.StatusForbidden, "editing agents, skills or hooks requires Settings → Shell access → Terminal on because they can run commands or grant tool permissions")
	}
	if in.Disabled != nil && (kind == "instructions" || remove || in.Path == "" || in.Revision == "" || in.Content != "") {
		return ConfigurationFile{}, newError(http.StatusBadRequest, "disabling or enabling requires an agent, skill or hook path and revision without content")
	}
	if !remove && in.Disabled == nil {
		if err = validateConfiguration(kind, in.Content); err != nil {
			return ConfigurationFile{}, err
		}
	}
	var root *os.Root
	var disabled bool
	if in.Path != "" {
		if in.Revision == "" {
			return ConfigurationFile{}, newError(http.StatusConflict, "an existing file revision is required; reload before saving or removing it")
		}
		var files []ConfigurationFile
		if kind == "instructions" {
			files = []ConfigurationFile{configurationFile(scope, kind, name, true)}
		} else if kind == "skills" {
			files, err = m.configurationSkillsForScope(scope)
		} else {
			files, err = listConfiguration(scope, kind, true)
		}
		if err != nil {
			return ConfigurationFile{}, newError(http.StatusConflict, "configuration listing is unavailable: %s", shortError(err))
		}
		i := slices.IndexFunc(files, func(file ConfigurationFile) bool { return file.Name == name && file.Path == in.Path })
		if i < 0 || !files[i].Editable || files[i].Error != "" || files[i].Revision == "" {
			return ConfigurationFile{}, newError(http.StatusConflict, "configuration file is unavailable or read-only; reload before saving or removing it")
		}
		disabled = files[i].Disabled
		if disabled && !remove && in.Disabled == nil {
			return ConfigurationFile{}, newError(http.StatusConflict, "enable the configuration before editing it")
		}
		if disabled {
			path += disabledConfigurationSuffix
		}
		if kind == "skills" {
			root, path, err = openConfigurationSkillRoot(scope, files[i].Path)
			if err != nil {
				return ConfigurationFile{}, newError(http.StatusConflict, "configuration path is unavailable: %s", shortError(err))
			}
		}
	}
	if root == nil {
		root, err = openConfigurationRoot(scope, !remove && in.Path == "")
	}
	if err != nil {
		return ConfigurationFile{}, newError(http.StatusConflict, "configuration directory is unavailable: %s", shortError(err))
	}
	defer root.Close()
	if err = configurationPath(root, path, !remove && in.Path == ""); err != nil {
		return ConfigurationFile{}, newError(http.StatusConflict, "configuration path is unavailable: %s", shortError(err))
	}
	_, revision, err := readConfiguration(root, path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return ConfigurationFile{}, newError(http.StatusConflict, "configuration file is unavailable: %s", shortError(err))
	}
	if revision != in.Revision || remove && revision == "" {
		return ConfigurationFile{}, newError(http.StatusConflict, "configuration changed; reload before saving or removing it")
	}
	if in.Disabled != nil {
		destination := strings.TrimSuffix(path, disabledConfigurationSuffix)
		if *in.Disabled {
			destination += disabledConfigurationSuffix
		}
		if destination != path {
			err = moveConfiguration(root, path, destination, revision)
		}
		if err == nil {
			path = destination
			disabled = *in.Disabled
			in.Path = strings.TrimSuffix(in.Path, disabledConfigurationSuffix)
			if disabled {
				in.Path += disabledConfigurationSuffix
			}
		}
	} else if remove {
		err = root.Remove(path)
		if err == nil && kind == "skills" {
			// Remove succeeds only for an empty directory. Supporting files
			// belong to the owner and remain after deactivating the skill.
			_ = root.Remove(filepath.Dir(path))
		}
	} else {
		mode := os.FileMode(0600)
		if in.Path != "" {
			info, err := root.Lstat(path)
			if err != nil || !info.Mode().IsRegular() {
				return ConfigurationFile{}, newError(http.StatusConflict, "configuration changed; reload before saving")
			}
			mode = info.Mode().Perm()
		}
		tmp := filepath.Join(filepath.Dir(path), ".uam-"+rand.Text())
		f, e := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return ConfigurationFile{}, newError(http.StatusInternalServerError, "cannot create configuration file: %s", shortError(e))
		}
		defer root.Remove(tmp)
		err = nil
		if in.Path != "" {
			err = f.Chmod(mode)
		}
		if err == nil {
			_, err = io.WriteString(f, in.Content)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = publishConfiguration(root, tmp, path, revision)
		}
	}
	if err != nil {
		var webErr *Error
		if errors.As(err, &webErr) {
			return ConfigurationFile{}, err
		}
		return ConfigurationFile{}, newError(http.StatusInternalServerError, "cannot save configuration: %s", shortError(err))
	}
	if remove {
		return ConfigurationFile{}, nil
	}
	if in.Path != "" {
		content, revision, err := readConfiguration(root, path)
		if err != nil {
			return ConfigurationFile{}, newError(http.StatusConflict, "saved configuration is unavailable: %s", shortError(err))
		}
		return ConfigurationFile{Name: name, Path: in.Path, Content: content, Revision: revision, Editable: true, Disabled: disabled}, nil
	}
	return configurationFile(scope, kind, name, true), nil
}

// Link refuses to replace an existing destination. Remove the old name only
// after verifying both names still refer to the revision selected by the user.
func moveConfiguration(root *os.Root, path, destination, revision string) error {
	if err := configurationPath(root, destination, false); err != nil {
		return newError(http.StatusConflict, "configuration destination is unavailable: %s", shortError(err))
	}
	original, err := root.Lstat(path)
	if err != nil || !original.Mode().IsRegular() {
		return newError(http.StatusConflict, "configuration changed; reload before disabling or enabling it")
	}
	if err := root.Link(path, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return newError(http.StatusConflict, "configuration destination already exists; resolve it before disabling or enabling this file")
		}
		return err
	}
	complete := false
	defer func() {
		if !complete {
			// Never remove a replacement written by someone else.
			if current, err := root.Lstat(destination); err == nil && os.SameFile(original, current) {
				_ = root.Remove(destination)
			}
		}
	}()
	for _, name := range []string{path, destination} {
		current, err := root.Lstat(name)
		if err != nil || !os.SameFile(original, current) {
			return newError(http.StatusConflict, "configuration changed; reload before disabling or enabling it")
		}
		_, currentRevision, err := readConfiguration(root, name)
		if err != nil || currentRevision != revision {
			return newError(http.StatusConflict, "configuration changed; reload before disabling or enabling it")
		}
	}
	if err := root.Remove(path); err != nil {
		return err
	}
	complete = true
	return nil
}

func publishConfiguration(root *os.Root, tmp, path, revision string) error {
	if revision == "" {
		if err := root.Link(tmp, path); errors.Is(err, os.ErrExist) {
			return newError(http.StatusConflict, "configuration was created elsewhere; reload before saving")
		} else {
			return err
		}
	}
	_, current, err := readConfiguration(root, path)
	if err != nil || current != revision {
		return newError(http.StatusConflict, "configuration changed; reload before saving")
	}
	return root.Rename(tmp, path)
}

func (s *Server) handleConfiguration(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.m.Configuration(r.URL.Query().Get("project_id"))
	if err != nil {
		writeFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleSaveConfiguration(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content  *string `json:"content"`
		Revision *string `json:"revision"`
		Path     string  `json:"path,omitempty"`
		Disabled *bool   `json:"disabled,omitempty"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	remove := r.Method == http.MethodDelete
	if body.Revision == nil || !remove && body.Content == nil && body.Disabled == nil {
		writeError(w, http.StatusBadRequest, "revision is required, and saving also requires content")
		return
	}
	if body.Disabled != nil && (remove || body.Content != nil) {
		writeError(w, http.StatusBadRequest, "disabling or enabling cannot be combined with content or removal")
		return
	}
	in := configurationInput{Revision: *body.Revision, Path: body.Path, Disabled: body.Disabled}
	if body.Content != nil {
		in.Content = *body.Content
	}
	file, err := s.m.SaveConfiguration(r.URL.Query().Get("project_id"), r.PathValue("kind"), r.PathValue("name"), in, remove)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if remove {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, file)
}
