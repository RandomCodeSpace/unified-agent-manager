package log

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

var current *slog.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))

func Init() (io.Closer, error) {
	dir := cacheDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "uam.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) // #nosec G304 -- UAM intentionally writes its own cache log path.
	if err != nil {
		return nil, err
	}
	level := slog.LevelInfo
	if os.Getenv("UAM_DEBUG") != "" {
		level = slog.LevelDebug
	}
	current = slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level}))
	current.Info("logger initialized", "path", f.Name())
	return f, nil
}

func L() *slog.Logger { return current }

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

// Fatal prints to stderr and exits with code 1.
func Fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "uam: "+format+"\n", args...)
	os.Exit(1)
}
