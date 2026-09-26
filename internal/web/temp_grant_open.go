package web

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	maxGrantPathBytes = 4096
	maxGrantDepth     = 64
	maxGrantFileBytes = 20 << 20
)

var errGrantUnavailable = newError(http.StatusNotFound, "temporary file is unavailable")

type grantIdentity struct{ dev, ino uint64 }

func statIdentity(st *unix.Stat_t) grantIdentity {
	return grantIdentity{uint64(st.Dev), uint64(st.Ino)}
}

func descriptorStat(f *os.File) (unix.Stat_t, error) {
	var st unix.Stat_t
	err := unix.Fstat(int(f.Fd()), &st)
	return st, err
}

// grantRoots retains both configuration roots. Only their configured spellings
// may be aliases; every component below the temp root must be a real directory.
type grantRoots struct {
	temp, runtime *os.Root
	canonical     string
	aliases       []string
	tempID        grantIdentity
	runtimeID     grantIdentity
	runtimePath   string
	components    []string
	forbidden     []string
}

func openGrantRoot(raw string) (*os.Root, string, grantIdentity, error) {
	if !filepath.IsAbs(raw) {
		return nil, "", grantIdentity{}, errGrantUnavailable
	}
	canonical, err := filepath.EvalSymlinks(raw)
	if err != nil {
		return nil, "", grantIdentity{}, err
	}
	r, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, "", grantIdentity{}, err
	}
	f, err := r.Open(".")
	if err != nil {
		_ = r.Close()
		return nil, "", grantIdentity{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	current, pathErr := os.Lstat(canonical)
	st, statErr := descriptorStat(f)
	if err != nil || pathErr != nil || statErr != nil || !info.IsDir() || !os.SameFile(info, current) {
		_ = r.Close()
		return nil, "", grantIdentity{}, errGrantUnavailable
	}
	return r, filepath.Clean(canonical), statIdentity(&st), nil
}

func openGrantRoots(tempPath, runtimePath string) (*grantRoots, error) {
	temp, canonical, tempID, err := openGrantRoot(tempPath)
	if err != nil {
		return nil, err
	}
	runtime, runtimePathResolved, runtimeID, err := openGrantRoot(runtimePath)
	if err != nil {
		_ = temp.Close()
		return nil, err
	}
	r := &grantRoots{temp: temp, runtime: runtime, canonical: canonical, tempID: tempID,
		runtimeID: runtimeID, runtimePath: runtimePathResolved}
	if canonical != string(filepath.Separator) {
		r.components = strings.Split(strings.TrimPrefix(canonical, string(filepath.Separator)), string(filepath.Separator))
	}
	insideRuntime, relErr := filepath.Rel(runtimePathResolved, canonical)
	if tempID == runtimeID || relErr != nil || filepath.IsLocal(insideRuntime) || len(r.components) > maxGrantDepth {
		r.close()
		return nil, errGrantUnavailable
	}
	if raw := filepath.Clean(tempPath); raw != canonical {
		r.aliases = []string{raw}
	}
	// Exclude both the configured name (even after replacement) and the
	// retained runtime object's identity (even after a rename).
	for _, candidate := range []string{runtimePath, runtimePathResolved} {
		if rel, err := r.relative(candidate); err == nil {
			r.forbidden = append(r.forbidden, rel)
		}
	}
	if err := r.validateLocation(context.Background()); err != nil {
		r.close()
		return nil, err
	}
	return r, nil
}

func (r *grantRoots) close() {
	_ = r.temp.Close()
	_ = r.runtime.Close()
}

func (r *grantRoots) relative(candidate string) (string, error) {
	if len(candidate) == 0 || len(candidate) > maxGrantPathBytes || !utf8.ValidString(candidate) ||
		strings.ContainsRune(candidate, 0) || !filepath.IsAbs(candidate) {
		return "", newError(http.StatusBadRequest, "invalid temporary file path")
	}
	for _, part := range strings.Split(candidate, string(filepath.Separator)) {
		if part == "." || part == ".." {
			return "", newError(http.StatusBadRequest, "invalid temporary file path")
		}
	}
	for _, root := range append([]string{r.canonical}, r.aliases...) {
		rel, err := filepath.Rel(root, candidate)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			continue
		}
		if len(strings.Split(rel, string(filepath.Separator))) > maxGrantDepth {
			return "", newError(http.StatusBadRequest, "temporary file path is too deep")
		}
		for _, denied := range r.forbidden {
			if rel == denied || strings.HasPrefix(rel, denied+string(filepath.Separator)) {
				return "", errGrantUnavailable
			}
		}
		return rel, nil
	}
	return "", errGrantUnavailable
}

func grantFileInfo(f *os.File) (os.FileInfo, error) {
	// Validate the same snapshot that supplies ServeContent's size. A second
	// stat after validation could return a later, unchecked file size.
	info, err := f.Stat()
	if err != nil {
		return nil, errGrantUnavailable
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || int64(st.Uid) != int64(os.Getuid()) ||
		st.Nlink != 1 || info.Mode().Perm()&0400 == 0 {
		return nil, errGrantUnavailable
	}
	if info.Size() > maxGrantFileBytes {
		return nil, newError(http.StatusRequestEntityTooLarge, "temporary file exceeds 20 MiB")
	}
	return info, nil
}

func openGrantAt(parent *os.File, name string, directory bool) (*os.File, error) {
	flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
	if directory {
		flags |= unix.O_DIRECTORY
	}
	fd, err := unix.Openat(int(parent.Fd()), name, flags, 0)
	if err != nil {
		return nil, errGrantUnavailable
	}
	return os.NewFile(uintptr(fd), name), nil
}

func grantChildMatches(parent, child *os.File, name string) bool {
	var named unix.Stat_t
	opened, err := descriptorStat(child)
	return err == nil && unix.Fstatat(int(parent.Fd()), name, &named, unix.AT_SYMLINK_NOFOLLOW) == nil &&
		statIdentity(&named) == statIdentity(&opened)
}

func openGrantDirectory(parent *os.File, name string, runtimeID grantIdentity) (*os.File, grantIdentity, error) {
	child, err := openGrantAt(parent, name, true)
	if err != nil {
		return nil, grantIdentity{}, err
	}
	st, statErr := descriptorStat(child)
	parentStat, parentErr := descriptorStat(parent)
	up, upErr := openGrantAt(child, "..", true)
	matches := false
	if upErr == nil {
		upStat, e := descriptorStat(up)
		matches = e == nil && parentErr == nil && statIdentity(&upStat) == statIdentity(&parentStat)
		_ = up.Close()
	}
	if statErr != nil || statIdentity(&st) == runtimeID || !matches || !grantChildMatches(parent, child, name) {
		_ = child.Close()
		return nil, grantIdentity{}, errGrantUnavailable
	}
	return child, statIdentity(&st), nil
}

// A retained os.Root follows renames. Rewalk its original canonical location
// from / without symlinks, before and after each file check. This rejects a
// moved temp root, symlink-replaced ancestors, and either the retained runtime
// directory or a replacement at its configured location. Only current/next
// directory descriptors are retained while traversing the bounded components.
func (r *grantRoots) validateLocation(ctx context.Context) error {
	parent, err := os.Open(string(filepath.Separator))
	if err != nil {
		return errGrantUnavailable
	}
	defer func() { _ = parent.Close() }()
	currentPath := string(filepath.Separator)
	for _, component := range r.components {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		currentPath = filepath.Join(currentPath, component)
		if currentPath == r.runtimePath {
			return errGrantUnavailable
		}
		child, _, err := openGrantDirectory(parent, component, r.runtimeID)
		if err != nil {
			return err
		}
		_ = parent.Close()
		parent = child
	}
	st, err := descriptorStat(parent)
	if err != nil || statIdentity(&st) != r.tempID || ctx.Err() != nil {
		return errGrantUnavailable
	}
	return nil
}

// walk holds only the current directory, next child, and a short parent check.
// The chain is bounded identity metadata, not a chain of retained descriptors.
func (r *grantRoots) walk(ctx context.Context, rel string, beforeLeaf func()) (*os.File, []grantIdentity, error) {
	parent, err := r.temp.Open(".")
	if err != nil {
		return nil, nil, errGrantUnavailable
	}
	defer func() { _ = parent.Close() }()
	st, err := descriptorStat(parent)
	if err != nil {
		return nil, nil, errGrantUnavailable
	}
	chain := []grantIdentity{statIdentity(&st)}
	parts := strings.Split(rel, string(filepath.Separator))
	for _, component := range parts[:len(parts)-1] {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		child, identity, err := openGrantDirectory(parent, component, r.runtimeID)
		if err != nil {
			return nil, nil, err
		}
		_ = parent.Close()
		parent = child
		chain = append(chain, identity)
	}
	if beforeLeaf != nil {
		beforeLeaf()
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	f, err := openGrantAt(parent, parts[len(parts)-1], false)
	if err != nil {
		return nil, nil, err
	}
	if _, err = grantFileInfo(f); err != nil || !grantChildMatches(parent, f, parts[len(parts)-1]) {
		_ = f.Close()
		if err == nil {
			err = errGrantUnavailable
		}
		return nil, nil, err
	}
	return f, chain, nil
}

// checkedOpen refuses a changed ancestor or object; it never falls back to A
// when the final rewalk fails. This is checked-object authorization, not an
// immutable snapshot or continuous filesystem isolation after the last check.
func (r *grantRoots) checkedOpen(ctx context.Context, rel string, beforeLeaf, beforeRewalk func()) (*os.File, error) {
	if err := r.validateLocation(ctx); err != nil {
		return nil, err
	}
	a, chain, err := r.walk(ctx, rel, beforeLeaf)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	if beforeRewalk != nil {
		beforeRewalk()
	}
	b, rewalk, err := r.walk(ctx, rel, nil)
	if err != nil {
		return nil, err
	}
	ai, ae := a.Stat()
	bi, be := grantFileInfo(b)
	matches := len(chain) == len(rewalk)
	for i := range chain {
		if !matches || chain[i] != rewalk[i] {
			matches = false
			break
		}
	}
	if ae != nil || be != nil || !matches || !os.SameFile(ai, bi) || ctx.Err() != nil || r.validateLocation(ctx) != nil {
		_ = b.Close()
		return nil, errors.Join(errGrantUnavailable, ctx.Err())
	}
	return b, nil
}
