package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const CurrentSchemaVersion = 2

const configFileName = "sessions.json"

type Mode string

const (
	ModeYolo Mode = "yolo"
	ModeSafe Mode = "safe"
)

// Status distinguishes records that should keep behaving as live sessions
// (StatusActive — recoverable on attach) from records the user deliberately
// retired (StatusClosedByUser).
type Status string

const (
	StatusActive       Status = "active"
	StatusClosedByUser Status = "closed_by_user"
)

type Config struct {
	SchemaVersion int                      `json:"schema_version"`
	DefaultAgent  string                   `json:"default_agent"`
	Sessions      map[string]SessionRecord `json:"sessions"`
	UI            UISettings               `json:"ui"`
}

type UISettings struct {
	GroupByDir bool   `json:"group_by_dir"`
	Sort       string `json:"sort"`
	PeekWidth  int    `json:"peek_width"`
}

type SessionRecord struct {
	ID          string    `json:"id"`
	Agent       string    `json:"agent"`
	Name        string    `json:"name"`
	Prompt      string    `json:"prompt,omitempty"`
	Mode        Mode      `json:"mode"`
	Workdir     string    `json:"workdir"`
	TmuxSession string    `json:"tmux_session"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Pinned      bool      `json:"pinned"`
	Group       string    `json:"group"`
	SortIndex   int       `json:"sort_index"`
	Status      Status    `json:"status,omitempty"`
	PR          *PRRecord `json:"pr,omitempty"`
}

type PRRecord struct {
	URL         string    `json:"url"`
	Number      int       `json:"number"`
	LastStatus  string    `json:"last_status"`
	LastChecked time.Time `json:"last_checked"`
}

type Store struct{ path string }

func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

func (s *Store) Path() string { return s.path }

func DefaultPath() string {
	if v := os.Getenv("UAM_CONFIG_DIR"); v != "" {
		return filepath.Join(v, configFileName)
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "uam", configFileName)
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "uam", configFileName)
	}
	return filepath.Join(".uam", configFileName)
}

func DefaultConfig() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		DefaultAgent:  "claude",
		Sessions:      map[string]SessionRecord{},
		UI:            UISettings{Sort: "state", PeekWidth: 60},
	}
}

func Key(agent, id string) string {
	return strings.ToLower(agent) + ":" + ShortID(id)
}

func ShortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func (s *Store) Load() (Config, error) {
	unlock, err := s.lock()
	if err != nil {
		return Config{}, err
	}
	defer unlock()
	return s.loadNoLock()
}

func (s *Store) Save(cfg Config) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	return s.saveNoLock(normalize(cfg))
}

func (s *Store) Update(fn func(*Config) error) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := s.loadNoLock()
	if err != nil {
		return err
	}
	if err := fn(&cfg); err != nil {
		return err
	}
	return s.saveNoLock(normalize(cfg))
}

func (s *Store) loadNoLock() (Config, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultConfig(), nil
	}
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		_ = s.moveAside()
		return DefaultConfig(), nil
	}
	if cfg.SchemaVersion < CurrentSchemaVersion {
		if err := s.copyBackup(); err != nil {
			return Config{}, err
		}
		cfg = migrate(normalize(cfg))
		if err := s.saveNoLock(cfg); err != nil {
			return Config{}, err
		}
	}
	return normalize(cfg), nil
}

func normalize(cfg Config) Config {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = CurrentSchemaVersion
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = "claude"
	}
	if cfg.Sessions == nil {
		cfg.Sessions = map[string]SessionRecord{}
	}
	if cfg.UI.Sort == "" {
		cfg.UI.Sort = "state"
	}
	if cfg.UI.PeekWidth == 0 {
		cfg.UI.PeekWidth = 60
	}
	for k, rec := range cfg.Sessions {
		if rec.Status == "" {
			rec.Status = StatusActive
			cfg.Sessions[k] = rec
		}
	}
	return cfg
}

func migrate(cfg Config) Config {
	// v1 → v2 backfills Status. Pre-v2 records had no notion of user-closed,
	// so every existing row defaults to Active. Soft-closed records from
	// the legacy `uam stop` path are indistinguishable here and will surface
	// as Active until the user attaches or deletes them.
	if cfg.SchemaVersion < 2 {
		for k, rec := range cfg.Sessions {
			if rec.Status == "" {
				rec.Status = StatusActive
				cfg.Sessions[k] = rec
			}
		}
	}
	cfg.SchemaVersion = CurrentSchemaVersion
	return normalize(cfg)
}

func (s *Store) saveNoLock(cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", s.path, os.Getpid())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600) // #nosec G304 -- UAM intentionally writes its own config file path.
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *Store) lock() (func(), error) {
	lockPath := s.path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- UAM intentionally writes its own config lock file path.
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

func (s *Store) backupPath() string { return fmt.Sprintf("%s.bak.%d", s.path, time.Now().UnixNano()) }

func (s *Store) copyBackup() error {
	in, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(s.backupPath(), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 -- UAM intentionally writes migration backups next to its config.
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func (s *Store) moveAside() error { return os.Rename(s.path, s.backupPath()) }

func PruneOld(cfg *Config, maxAge time.Duration, exists func(string) bool) {
	cutoff := time.Now().Add(-maxAge)
	for key, rec := range cfg.Sessions {
		if rec.LastSeenAt.Before(cutoff) && !exists(rec.TmuxSession) {
			delete(cfg.Sessions, key)
		}
	}
}
