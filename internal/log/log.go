package log

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

const (
	maxLogSize = int64(5 << 20)
	maxBackups = 3
)

var current *slog.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

type rotatingFile struct {
	mu   sync.Mutex
	path string
	file *os.File
}

func Init() (io.Closer, error) {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "uam.log")
	w, err := openRotatingFile(path)
	if err != nil {
		return nil, err
	}
	current = newLogger(w)
	current.Info("logger initialized", "path", path)
	return w, nil
}

func openRotatingFile(path string) (*rotatingFile, error) {
	unlock, err := lockLog(path)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := rotateIfNeeded(path); err != nil {
		return nil, err
	}
	f, err := openLogFile(path)
	if err != nil {
		return nil, err
	}
	if err := enforcePrivateLogModes(path); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotatingFile{path: path, file: f}, nil
}

// The lock file must retain its inode while the active log and backups rotate.
func lockLog(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- UAM intentionally writes its own cache log lock path.
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func openLogFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- UAM intentionally writes its own cache log path.
}

func (w *rotatingFile) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, os.ErrClosed
	}
	unlock, err := lockLog(w.path)
	if err != nil {
		return 0, err
	}
	defer unlock()
	info, err := w.file.Stat()
	if err != nil {
		return 0, err
	}
	active, err := os.Stat(w.path)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if os.IsNotExist(err) || !os.SameFile(info, active) {
		f, err := openLogFile(w.path)
		if err != nil {
			return 0, err
		}
		_ = w.file.Close()
		w.file = f
		info, err = f.Stat()
		if err != nil {
			return 0, err
		}
	}
	if info.Size() > 0 && info.Size()+int64(len(p)) > maxLogSize {
		if err := w.file.Close(); err != nil {
			return 0, err
		}
		w.file = nil
		if err := rotateLogs(w.path); err != nil {
			return 0, err
		}
		f, err := openLogFile(w.path)
		if err != nil {
			return 0, err
		}
		w.file = f
		if err := enforcePrivateLogModes(w.path); err != nil {
			_ = w.file.Close()
			w.file = nil
			return 0, err
		}
	}
	return w.file.Write(p)
}

func (w *rotatingFile) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// UseStderr installs the non-file fallback used when Init cannot create or
// open the cache log. The caller remains responsible for printing the single
// initialization warning that explains why the fallback was selected.
func UseStderr(w io.Writer) {
	if w == nil {
		w = os.Stderr
	}
	current = newLogger(w)
}

func newLogger(w io.Writer) *slog.Logger {
	level := slog.LevelInfo
	if os.Getenv("UAM_DEBUG") != "" {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

func rotateIfNeeded(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return enforcePrivateLogModes(path)
	}
	if err != nil {
		return err
	}
	if info.Size() < maxLogSize {
		return enforcePrivateLogModes(path)
	}
	return rotateLogs(path)
}

func rotateLogs(path string) error {
	if err := os.Remove(path + ".3"); err != nil && !os.IsNotExist(err) {
		return err
	}
	for i := maxBackups - 1; i >= 1; i-- {
		oldPath := path + "." + string(rune('0'+i))
		newPath := path + "." + string(rune('1'+i))
		if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.Rename(path, path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	return enforcePrivateLogModes(path)
}

func enforcePrivateLogModes(path string) error {
	for i := 0; i <= maxBackups; i++ {
		candidate := path
		if i > 0 {
			candidate += "." + string(rune('0'+i))
		}
		if err := os.Chmod(candidate, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func L() *slog.Logger { return current }

// SetLogger swaps the package logger and returns the previous one, so callers
// (chiefly tests) can install a capturing handler and restore it afterwards.
func SetLogger(l *slog.Logger) *slog.Logger {
	prev := current
	if l != nil {
		current = l
	}
	return prev
}

func Debug(msg string, args ...any) { current.Debug(msg, args...) }
func Info(msg string, args ...any)  { current.Info(msg, args...) }
func Warn(msg string, args ...any)  { current.Warn(msg, args...) }
func Error(msg string, args ...any) { current.Error(msg, args...) }

func cacheDir() string {
	if v := os.Getenv("UAM_CACHE_DIR"); v != "" {
		return v
	}
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "uam")
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "uam")
	}
	return filepath.Join(".uam", "cache")
}
