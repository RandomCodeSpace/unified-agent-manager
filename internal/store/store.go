package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
)

const CurrentSchemaVersion = 3

const configFileName = "sessions.json"

// UI defaults and bounds. normalize clamps/coerces out-of-range or unknown
// on-disk values so a hand-edited or corrupt config can never feed an invalid
// value downstream (F44).
const (
	DefaultAgentName = "opencode"
	defaultSort      = "state"
	defaultPeekWidth = 60
	minPeekWidth     = 20
	maxPeekWidth     = 200
)

// knownSorts is the set of sort modes the UI understands; anything else is
// reset to defaultSort.
var knownSorts = map[string]struct{}{defaultSort: {}}

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

	// unknown captures any top-level JSON fields written by a newer binary so
	// they round-trip untouched instead of being silently dropped (F33). It is
	// never persisted directly — MarshalJSON merges it back into the object.
	unknown map[string]json.RawMessage
	// ReadOnly is set when the on-disk file declares a SchemaVersion newer than
	// this binary understands. The app must not write such a config (doing so
	// would drop fields it does not model), so Save/Update refuse it (F33). It
	// is in-memory only and never serialized.
	ReadOnly bool
}

// ErrReadOnly is returned by Save/Update when asked to write a config loaded
// from a newer on-disk schema. Refusing the write prevents an older binary
// from clobbering fields it does not understand (F33).
var ErrReadOnly = errors.New("store: config loaded from a newer schema is read-only")

// configAlias mirrors Config without the custom marshaller (and without the
// in-memory-only fields) so (Un)MarshalJSON can delegate to the stdlib encoder
// without recursing.
type configAlias struct {
	SchemaVersion int                      `json:"schema_version"`
	DefaultAgent  string                   `json:"default_agent"`
	Sessions      map[string]SessionRecord `json:"sessions"`
	UI            UISettings               `json:"ui"`
}

// knownConfigFields lists the JSON keys Config models directly; everything else
// is preserved verbatim via the unknown overflow.
var knownConfigFields = map[string]struct{}{
	"schema_version": {},
	"default_agent":  {},
	"sessions":       {},
	"ui":             {},
}

func (c Config) MarshalJSON() ([]byte, error) {
	base, err := json.Marshal(configAlias{
		SchemaVersion: c.SchemaVersion,
		DefaultAgent:  c.DefaultAgent,
		Sessions:      c.Sessions,
		UI:            c.UI,
	})
	if err != nil {
		return nil, err
	}
	if len(c.unknown) == 0 {
		return base, nil
	}
	var merged map[string]json.RawMessage
	if err := json.Unmarshal(base, &merged); err != nil {
		return nil, err
	}
	for k, v := range c.unknown {
		if _, known := knownConfigFields[k]; known {
			continue
		}
		merged[k] = v
	}
	return json.Marshal(merged)
}

func (c *Config) UnmarshalJSON(data []byte) error {
	var alias configAlias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	c.SchemaVersion = alias.SchemaVersion
	c.DefaultAgent = alias.DefaultAgent
	c.Sessions = alias.Sessions
	c.UI = alias.UI

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k := range raw {
		if _, known := knownConfigFields[k]; known {
			delete(raw, k)
		}
	}
	if len(raw) == 0 {
		c.unknown = nil
	} else {
		c.unknown = raw
	}
	return nil
}

type UISettings struct {
	GroupByDir bool `json:"group_by_dir"`
	// Sort and PeekWidth are retained schema-v3 compatibility fields. They are
	// normalized and round-tripped even when the TUI exposes no direct control.
	Sort      string `json:"sort"`
	PeekWidth int    `json:"peek_width"`
}

type SessionRecord struct {
	ID           string `json:"id"`
	Agent        string `json:"agent"`
	CommandAlias string `json:"command_alias,omitempty"`
	Name         string `json:"name"`
	Prompt       string `json:"prompt,omitempty"`
	Mode         Mode   `json:"mode"`
	Workdir      string `json:"workdir"`
	// SessionName is the backend session name ("uam-<agent>-<id>"). The JSON
	// key keeps its historical "tmux_session" spelling so configs written by
	// tmux-backed releases load unchanged.
	SessionName string    `json:"tmux_session"`
	CreatedAt   time.Time `json:"created_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	Pinned      bool      `json:"pinned"`
	Group       string    `json:"group"`
	SortIndex   int       `json:"sort_index"`
	Status      Status    `json:"status,omitempty"`
	// ProviderSessionID is the agent CLI's own session id, recorded when the
	// provider lets uam seed it at dispatch (e.g. claude --session-id). A
	// resume can then target the exact provider session instead of the
	// provider's "most recent" heuristic.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// LastExitCode records the agent process's exit status from the most
	// recent close (-1 when it died on a signal). Pointer so records from
	// older schemas stay distinguishable from a real exit 0.
	LastExitCode *int      `json:"last_exit_code,omitempty"`
	PR           *PRRecord `json:"pr,omitempty"`
}

// SessionExit describes how a provider process left its native session host.
// UAMInitiated is true only for an explicit UAM stop/restart request; terminal
// provider exits and externally delivered signals remain natural exits.
type SessionExit struct {
	SessionName       string
	ProviderSessionID string
	ExitCode          int
	UAMInitiated      bool
}

type PRRecord struct {
	URL         string    `json:"url"`
	Number      int       `json:"number"`
	LastStatus  string    `json:"last_status"`
	LastChecked time.Time `json:"last_checked"`
}

type Store struct {
	path string
	// sessionExists, when set, reports whether a backend session name is
	// still live. It is injected by the caller (the store stays backend-free)
	// and is used only to reclassify Statusless v1 records during migration
	// (F07).
	sessionExists func(string) bool
}

func Open(path string) (*Store, error) {
	if path == "" {
		path = DefaultPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Store{path: path}, nil
}

// SetSessionProbe injects a callback that reports whether a backend session
// name is still live. Migration uses it to tell a reboot-survivor (live ->
// stays Active) apart from a user-stopped session (dead -> closed-by-user).
// When unset, migration conservatively keeps the legacy Active behavior (F07).
func (s *Store) SetSessionProbe(exists func(string) bool) { s.sessionExists = exists }

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
		DefaultAgent:  DefaultAgentName,
		Sessions:      map[string]SessionRecord{},
		UI:            UISettings{Sort: "state", PeekWidth: 60},
	}
}

// PutSession inserts or updates rec under key with a guard against the 8-char
// ShortID map key collapsing two distinct full IDs into one slot (F22). The
// short key carries only 32 bits of entropy, so two same-agent sessions can
// collide; without this guard the second write would silently clobber the
// first, orphaning a live session whose only handle is that record. It returns
// true on a successful write (no record, or the same full ID) and false (with a
// log) when an existing record under key carries a different non-empty full ID.
func (c *Config) PutSession(key string, rec SessionRecord) bool {
	if c.Sessions == nil {
		c.Sessions = map[string]SessionRecord{}
	}
	if existing, ok := c.Sessions[key]; ok && existing.ID != "" && rec.ID != "" && existing.ID != rec.ID {
		log.Warn("refusing short-key collision overwrite", "key", key, "existing_id", existing.ID, "incoming_id", rec.ID)
		return false
	}
	c.Sessions[key] = rec
	return true
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
	if cfg.ReadOnly {
		return ErrReadOnly
	}
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
	if cfg.ReadOnly {
		return ErrReadOnly
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
		// Quarantine the corrupt file before starting fresh. If the move-aside
		// fails the original is still on disk, so returning DefaultConfig here
		// would let the next Save overwrite it with defaults and lose the only
		// copy. Propagate the error instead so the app refuses to clobber it
		// (F43). normalize guarantees a non-nil Sessions map on the success path.
		if mvErr := s.moveAside(); mvErr != nil {
			return Config{}, fmt.Errorf("quarantine corrupt config %s: %w", s.path, mvErr)
		}
		return normalize(DefaultConfig()), nil
	}
	// A file written by a newer binary carries fields this version does not
	// model. Surface it read-only (preserving the unknown overflow) instead of
	// erroring or clobbering it on the next save (F33).
	if cfg.SchemaVersion > CurrentSchemaVersion {
		cfg.ReadOnly = true
		return cfg, nil
	}
	dropInvalidRecords(&cfg)
	if cfg.SchemaVersion < CurrentSchemaVersion {
		if err := s.copyBackup(); err != nil {
			return Config{}, err
		}
		// Reclassify Statusless v1 records BEFORE normalize backfills them to
		// Active: a dead-pane record was a user-stopped session and must not
		// auto-resume on attach (F07). normalize stays unchanged.
		reclassifyV1Closed(&cfg, s.sessionExists)
		cfg = migrate(normalize(cfg))
		if err := s.saveNoLock(cfg); err != nil {
			return Config{}, err
		}
	}
	return normalize(cfg), nil
}

// reclassifyV1Closed runs on the RAW pre-normalize config during a v1->v2
// migration. For each Statusless record it asks the injected probe whether the
// session is still alive: a live one is a reboot survivor (left Statusless so
// normalize/migrate backfill it to Active), while a dead pane was a deliberately
// stopped session and is marked closed-by-user so it does not auto-resume. With
// no probe it is a no-op, preserving the legacy all-Active behavior.
func reclassifyV1Closed(cfg *Config, exists func(string) bool) {
	if exists == nil || cfg.SchemaVersion >= 2 {
		return
	}
	for k, rec := range cfg.Sessions {
		if rec.Status == "" && !exists(rec.SessionName) {
			rec.Status = StatusClosedByUser
			cfg.Sessions[k] = rec
		}
	}
}

// unsafeArgvChars are the shell metacharacters that must never appear in an ID
// or backend session name. The native backend execs argv directly (no shell)
// and session names are further allow-listed by session.ValidateName, but
// these fields are untrusted on disk, so classic shell metacharacters stay
// rejected as defense in depth. Whitespace and control runes are rejected
// separately. Note `:` is intentionally allowed: it is never a shell hazard,
// and real Key-derived values can carry it.
const unsafeArgvChars = "'\"`;&|$<>(){}[]*?!#\\~/ \t\n\r"

// prURLRE mirrors the GitHub PR URL shape recognized by the adapter's
// ExtractPR (internal/adapter/detect.go), anchored end-to-end so a record
// cannot smuggle in an unrelated or malformed URL.
var prURLRE = regexp.MustCompile(`^https://github\.com/[^/\s]+/[^/\s]+/pull/\d+$`)

// dropInvalidRecords removes any session record whose untrusted on-disk fields
// fail validation, logging each drop. It runs on load ONLY (not on the write
// path via normalize) so a single corrupt or hostile record cannot brick the
// whole store — the bad record is discarded and the rest survive.
func dropInvalidRecords(cfg *Config) {
	for key, rec := range cfg.Sessions {
		if reason := validateRecord(rec); reason != "" {
			log.Warn("dropping invalid session record", "key", key, "reason", reason)
			delete(cfg.Sessions, key)
		}
	}
}

// validateRecord returns a non-empty reason if the record must be dropped, or
// "" if it is safe to keep. It rejects only the values that carry real risk —
// shell metacharacters or control runes in the argv-bound ID/SessionName
// fields, a non-absolute or control-char Workdir, and a PR URL that does not
// match the GitHub PR shape. Empty optional fields are allowed.
//
// The on-disk JSON key for SessionName remains "tmux_session" for backward
// compatibility, which is why the drop reasons below keep that spelling.
func validateRecord(rec SessionRecord) string {
	if isUnsafeArgv(rec.ID) {
		return "unsafe id"
	}
	if isUnsafeArgv(rec.SessionName) {
		return "unsafe tmux_session"
	}
	if rec.Workdir != "" {
		if !filepath.IsAbs(rec.Workdir) {
			return "non-absolute workdir"
		}
		if hasControlChar(rec.Workdir) {
			return "control char in workdir"
		}
	}
	if rec.PR != nil && !prURLRE.MatchString(rec.PR.URL) {
		return "invalid pr url"
	}
	if rec.CommandAlias != "" && !isSafeCommandAlias(rec.CommandAlias) {
		return "unsafe command_alias"
	}
	// The provider session id is passed as a resume argv value; constrain it
	// so a hand-edited record cannot smuggle a flag or shell hazard into the
	// agent's command line.
	if rec.ProviderSessionID != "" && !providerSessionIDRE.MatchString(rec.ProviderSessionID) {
		return "unsafe provider_session_id"
	}
	return ""
}

// providerSessionIDRE constrains persisted provider session ids to the id
// alphabets the supported providers use — claude/codex UUIDs and opencode
// "ses_..." ids — with no shell metacharacters and no leading dash (a value
// starting with '-' could be parsed as a flag by the agent CLI).
var providerSessionIDRE = regexp.MustCompile(`^[0-9A-Za-z_][0-9A-Za-z_-]{0,63}$`)

func isSafeCommandAlias(alias string) bool {
	if alias == "" || strings.HasPrefix(alias, "-") {
		return false
	}
	for _, r := range alias {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// isUnsafeArgv reports whether s contains a shell metacharacter or a control
// rune that has no place in a persisted ID or session name.
func isUnsafeArgv(s string) bool {
	if strings.ContainsAny(s, unsafeArgvChars) {
		return true
	}
	return hasControlChar(s)
}

func hasControlChar(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func normalize(cfg Config) Config {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = CurrentSchemaVersion
	}
	if cfg.DefaultAgent == "" {
		cfg.DefaultAgent = DefaultAgentName
	}
	if cfg.Sessions == nil {
		cfg.Sessions = map[string]SessionRecord{}
	}
	if _, ok := knownSorts[cfg.UI.Sort]; !ok {
		cfg.UI.Sort = defaultSort
	}
	cfg.UI.PeekWidth = clampPeekWidth(cfg.UI.PeekWidth)
	for k, rec := range cfg.Sessions {
		if changed := coerceRecord(&rec); changed {
			cfg.Sessions[k] = rec
		}
	}
	return cfg
}

// clampPeekWidth coerces an out-of-range peek width back into bounds. A
// non-positive value (unset or corrupt) falls back to the default; otherwise it
// is clamped into [minPeekWidth, maxPeekWidth].
func clampPeekWidth(w int) int {
	switch {
	case w <= 0:
		return defaultPeekWidth
	case w < minPeekWidth:
		return minPeekWidth
	case w > maxPeekWidth:
		return maxPeekWidth
	default:
		return w
	}
}

// coerceRecord normalizes a record's enum fields to valid values, reporting
// whether it changed anything. An empty or unknown Status becomes Active and an
// unknown Mode becomes yolo — unknown values are NEVER coerced to ClosedByUser
// or dropped, so a hostile/corrupt status can never silently retire a session.
func coerceRecord(rec *SessionRecord) bool {
	changed := false
	if rec.Status != StatusActive && rec.Status != StatusClosedByUser {
		rec.Status = StatusActive
		changed = true
	}
	if rec.Mode != ModeYolo && rec.Mode != ModeSafe {
		rec.Mode = ModeYolo
		changed = true
	}
	return changed
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
	// CreateTemp picks a randomly-suffixed name with O_EXCL in the SAME
	// directory as the target (filepath.Dir) — same dir is required because a
	// cross-device rename fails with EXDEV. The random suffix means a stale
	// orphan from a previously-killed run is never silently reused (F45).
	f, err := os.CreateTemp(filepath.Dir(s.path), filepath.Base(s.path)+".tmp.*")
	if err != nil {
		return err
	}
	tmp := f.Name()
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
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	return syncDir(filepath.Dir(s.path))
}

func syncDir(dir string) error {
	// dir is the containing directory of Store.path; opening that configured
	// path is the intended durability operation, not user-controlled inclusion.
	f, err := os.Open(dir) // #nosec G304 -- Store intentionally fsyncs its configured parent directory.
	if err != nil {
		return fmt.Errorf("open store directory for sync: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := f.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return fmt.Errorf("sync store directory: %w", err)
	}
	return nil
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

// MarkSessionClosed flags the record whose backend session name matches as
// user-closed and records the agent's exit code. It is what a session host
// calls when its agent exits — the native replacement for the tmux
// session-closed hook driving `uam notify-closed`. Idempotent and a no-op
// when no record matches (e.g. uam already deleted it via `uam rm`).
func (s *Store) MarkSessionClosed(sessionName string, exitCode int) error {
	_, err := s.TryMarkSessionClosed(sessionName, exitCode)
	return err
}

// TryMarkSessionClosed is the compatibility entry point for older callers. It
// records an explicit UAM stop and returns whether a durable record matched.
func (s *Store) TryMarkSessionClosed(sessionName string, exitCode int) (bool, error) {
	return s.TryRecordSessionExit(SessionExit{SessionName: sessionName, ExitCode: exitCode, UAMInitiated: true})
}

// TryRecordSessionExit records the provider's latest exit while preserving
// resumability for natural exits. Only an explicit UAM stop/restart retires
// the record into the closed-by-user group.
func (s *Store) TryRecordSessionExit(exit SessionExit) (bool, error) {
	if exit.SessionName == "" {
		return false, nil
	}
	matched := false
	err := s.Update(func(cfg *Config) error {
		for key, rec := range cfg.Sessions {
			if rec.SessionName != exit.SessionName {
				continue
			}
			if exit.UAMInitiated {
				rec.Status = StatusClosedByUser
			} else {
				rec.Status = StatusActive
			}
			code := exit.ExitCode
			rec.LastExitCode = &code
			cfg.Sessions[key] = rec
			matched = true
			return nil
		}
		return nil
	})
	return matched, err
}

func PruneOld(cfg *Config, maxAge time.Duration, exists func(string) bool) {
	cutoff := time.Now().Add(-maxAge)
	for key, rec := range cfg.Sessions {
		if rec.LastSeenAt.Before(cutoff) && !exists(rec.SessionName) {
			delete(cfg.Sessions, key)
		}
	}
}
