package execpath

import (
	"fmt"
	"os"
	"path/filepath"
)

var fixedDirs = []string{
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
	"/opt/homebrew/bin",
	"/opt/local/bin",
}

// Resolve finds an executable by name in fixed system directories only.
// It intentionally does not read PATH, avoiding writable-directory hijacks.
func Resolve(name string) (string, error) {
	return ResolveInDirs(name, fixedDirs)
}

func ResolveInDirs(name string, dirs []string) (string, error) {
	if name == "" || filepath.Base(name) != name {
		return "", fmt.Errorf("executable name %q must not contain path separators", name)
	}
	for _, dir := range dirs {
		candidate := filepath.Join(dir, name)
		if isExecutable(candidate) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found in fixed executable paths", name)
}

// ValidateAbsoluteExecutable requires an explicit absolute path to a regular
// executable file. It is used for opt-in overrides where PATH lookup would
// reintroduce executable-hijacking risk.
func ValidateAbsoluteExecutable(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("executable path must be absolute: %s", path)
	}
	if !isExecutable(path) {
		return fmt.Errorf("executable path is not a regular executable file: %s", path)
	}
	return nil
}

func isExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}
