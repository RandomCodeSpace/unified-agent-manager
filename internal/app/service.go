package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/log"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/pr"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/store"
)

type Service struct {
	Store    *store.Store
	Registry *adapter.Registry
	// prInFlight guards the PR-check pass of a refresh so overlapping refreshes
	// (stacked 2s ticks) do not launch concurrent `gh` subprocesses. A refresh
	// that fails to acquire it skips the network check and keeps the persisted
	// PR record; the holder clears it on completion, success or error (F02).
	prInFlight atomic.Bool
}

const agentUnavailableFormat = "agent %q unavailable"

// lastSeenRefresh is how stale a live session's persisted LastSeenAt may grow
// before a refresh re-stamps it. Bumping every tick would write the store on
// every 2s refresh; gating on staleness keeps LastSeenAt fresh enough that
// PruneOld never deletes a still-running session while avoiding a write-storm
// (F20 Stage 1, paired with the idempotent-refresh contract).
const lastSeenRefresh = time.Minute

// pruneMaxAge is the age past which a dead-pane record is eligible for startup
// pruning so sessions.json does not grow unbounded. It is deliberately long: a
// record is only removed when its pane is gone AND it has not been seen for this
// long (F20 Stage 2).
const pruneMaxAge = 30 * 24 * time.Hour

// prCheckTimeout bounds a single `gh pr view` call so a hung gh cannot wedge the
// 2s refresh. It is comfortably under the 60s PR re-check interval, so a
// timed-out check just defers to the next tick (F02).
const prCheckTimeout = 4 * time.Second

func NewService(st *store.Store, reg *adapter.Registry) *Service {
	return &Service{Store: st, Registry: reg}
}

func (s *Service) LoadSessions(ctx context.Context) ([]adapter.Session, store.Config, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, cfg, err
	}
	live := s.liveSessions(ctx)
	s.mergeStoredSessions(live, cfg, time.Now())
	// refreshSessionRecords runs pr.Check OUTSIDE any store lock and returns the
	// records this refresh owns. persistRefresh re-reads inside the flock and
	// writes only those keys, so a concurrent TogglePin/Rename on a different
	// session is never clobbered by a stale whole-config Save (F01).
	updates := s.refreshSessionRecords(ctx, live, &cfg)
	s.persistRefresh(updates)
	out := sessionsFromMap(live)
	SortSessions(out)
	return out, cfg, nil
}

// PruneStartup removes long-stale, dead-pane records from the store so
// sessions.json does not grow unbounded. It is server-down-safe: a tmux server
// that is down looks exactly like an empty one (zero live sessions), so with no
// live session to prove the server is up it skips pruning entirely rather than
// risk wiping every record during a transient outage. When at least one live
// session is visible the server is up, and PruneOld then drops records whose
// pane is gone and that have not been seen within pruneMaxAge (F20 Stage 2).
func (s *Service) PruneStartup(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	live := s.liveSessions(ctx)
	if len(live) == 0 {
		// Unknown server state — don't prune.
		return nil
	}
	liveTmux := make(map[string]struct{}, len(live))
	for _, sess := range live {
		if sess.TmuxSession != "" {
			liveTmux[sess.TmuxSession] = struct{}{}
		}
	}
	exists := func(tmuxName string) bool {
		_, ok := liveTmux[tmuxName]
		return ok
	}
	return s.Store.Update(func(cfg *store.Config) error {
		store.PruneOld(cfg, pruneMaxAge, exists)
		return nil
	})
}

// persistRefresh writes the records owned by a refresh back to the store via an
// atomic read-modify-write. It re-reads the on-disk config inside the lock and
// overwrites only the supplied keys, leaving every other record (and any
// concurrent mutation to it) intact.
func (s *Service) persistRefresh(updates map[string]store.SessionRecord) {
	if len(updates) == 0 || s.Store == nil {
		return
	}
	_ = s.Store.Update(func(cfg *store.Config) error {
		for key, rec := range updates {
			cfg.Sessions[key] = rec
		}
		return nil
	})
}

func (s *Service) loadConfig() (store.Config, error) {
	cfg := store.DefaultConfig()
	if s.Store == nil {
		return cfg, nil
	}
	return s.Store.Load()
}

func (s *Service) liveSessions(ctx context.Context) map[string]adapter.Session {
	live := map[string]adapter.Session{}
	if s.Registry == nil {
		return live
	}
	for _, a := range s.Registry.Enabled() {
		sessions, err := a.List(ctx)
		if err != nil {
			// One adapter's List failure must not blank the dashboard for the
			// others, but it shouldn't vanish silently either (F12).
			log.Warn("listing sessions for adapter failed", "agent", a.Name(), "error", err)
			continue
		}
		for _, sess := range sessions {
			live[store.Key(sess.AgentType, sess.ID)] = sess
		}
	}
	return live
}

func (s *Service) mergeStoredSessions(live map[string]adapter.Session, cfg store.Config, now time.Time) {
	for key, rec := range cfg.Sessions {
		if sess, ok := live[key]; ok {
			live[key] = mergeStoredMetadata(sess, rec)
			continue
		}
		live[key] = deadSessionFromRecord(rec, now)
	}
}

func mergeStoredMetadata(sess adapter.Session, rec store.SessionRecord) adapter.Session {
	sess.DisplayName = firstNonEmpty(rec.Name, sess.DisplayName)
	sess.Prompt = firstNonEmpty(rec.Prompt, sess.Prompt)
	sess.Pinned = rec.Pinned
	sess.Group = rec.Group
	sess.SortIndex = rec.SortIndex
	// Closed implies the pane has exited: a user-closed record whose pane is
	// still alive renders under ACTIVE (the refresh reconciles its persisted
	// status back to Active). This keeps a live session from showing under
	// CLOSED with a live glyph (F18).
	sess.Closed = rec.Status == store.StatusClosedByUser && sess.ProcAlive == adapter.Exited
	if rec.PR != nil && sess.PR == nil {
		// The store schema does not persist Owner/Repo, but the URL is lossless:
		// re-derive them via the same GitHub PR regex the adapter uses so a
		// store round-trip never strips them (C2-7). Keep the persisted
		// Number/Status as the source of truth.
		ref := &adapter.PRRef{URL: rec.PR.URL, Number: rec.PR.Number, Status: adapter.PRStatus(rec.PR.LastStatus)}
		if derived := adapter.ExtractPR(rec.PR.URL); derived != nil {
			ref.Owner = derived.Owner
			ref.Repo = derived.Repo
		}
		sess.PR = ref
	}
	return sess
}

func deadSessionFromRecord(rec store.SessionRecord, now time.Time) adapter.Session {
	return adapter.Session{ID: rec.ID, AgentType: rec.Agent, DisplayName: rec.Name, Prompt: rec.Prompt, Cwd: rec.Workdir, TmuxSession: rec.TmuxSession, State: adapter.Failed, ProcAlive: adapter.Exited, CreatedAt: rec.CreatedAt, LastChange: now, Pinned: rec.Pinned, Group: rec.Group, SortIndex: rec.SortIndex, Closed: rec.Status == store.StatusClosedByUser}
}

// refreshSessionRecords reconciles live sessions against the loaded config and
// returns the records this refresh wants to persist (keyed by store key). It
// also updates the in-memory cfg and live maps so the returned snapshot is
// consistent. pr.Check runs here, OUTSIDE any store lock; persistence happens
// later via persistRefresh's atomic re-read.
func (s *Service) refreshSessionRecords(ctx context.Context, live map[string]adapter.Session, cfg *store.Config) map[string]store.SessionRecord {
	updates := map[string]store.SessionRecord{}
	now := time.Now()
	// Acquire the PR-check guard for the duration of this refresh's network pass.
	// If another refresh already holds it, skip the gh calls (the persisted PR
	// record is kept) so stacked ticks never launch overlapping gh subprocesses.
	// Released unconditionally on completion so a single pass never wedges the
	// guard (F02).
	doPR := s.prInFlight.CompareAndSwap(false, true)
	if doPR {
		defer s.prInFlight.Store(false)
	}
	for key, sess := range live {
		rec, ok := cfg.Sessions[key]
		changed := false
		if !ok || rec.ID == "" {
			// Backfill a record for a live session we have no (valid) record
			// for. Guard against an empty session ID so we never persist an
			// un-killable phantom (C1-1 fix-trap).
			if sess.ID == "" {
				continue
			}
			rec = RecordFromSession(sess, store.ModeYolo)
			cfg.PutSession(key, rec)
			changed = true
		} else if rec.Status == store.StatusClosedByUser && sess.ProcAlive == adapter.Alive {
			// Anti-flap: the user closed this session but its pane is still
			// alive, so it belongs in the Active group. Reset the persisted
			// status to Active, otherwise the next merge re-flags it Closed
			// and it flaps (F18).
			rec.Status = store.StatusActive
			cfg.Sessions[key] = rec
			changed = true
		}
		// Keep LastSeenAt fresh for a still-running pane so staleness-based
		// pruning never deletes a live session. Gated on the refresh interval so
		// a long-lived session isn't re-saved on every tick (F20 Stage 1).
		if sess.ProcAlive == adapter.Alive && now.Sub(rec.LastSeenAt) >= lastSeenRefresh {
			rec.LastSeenAt = now
			cfg.Sessions[key] = rec
			changed = true
		}
		if doPR && updatePRRecord(ctx, key, &sess, &rec, live, now) {
			cfg.Sessions[key] = rec
			changed = true
		}
		if changed {
			updates[key] = rec
		}
	}
	return updates
}

func updatePRRecord(ctx context.Context, key string, sess *adapter.Session, rec *store.SessionRecord, live map[string]adapter.Session, now time.Time) bool {
	if sess.PR == nil || (rec.PR != nil && rec.PR.URL == sess.PR.URL && time.Since(rec.PR.LastChecked) <= 60*time.Second) {
		return false
	}
	status := string(sess.PR.Status)
	// Bound the gh call so a hung process cannot wedge the refresh. On timeout
	// (or any error) the status is left at the last-known value and LastChecked is
	// stamped below, arming the 60s guard so the next tick does not immediately
	// re-launch gh (F02).
	checkCtx, cancel := context.WithTimeout(ctx, prCheckTimeout)
	defer cancel()
	if checked, err := pr.Check(checkCtx, sess.PR.URL); err == nil && checked != pr.None {
		status = string(checked)
		sess.PR.Status = adapter.PRStatus(status)
		live[key] = *sess
	}
	rec.PR = &store.PRRecord{URL: sess.PR.URL, Number: sess.PR.Number, LastStatus: status, LastChecked: now}
	return true
}

func sessionsFromMap(live map[string]adapter.Session) []adapter.Session {
	out := make([]adapter.Session, 0, len(live))
	for _, sess := range live {
		out = append(out, sess)
	}
	return out
}

func SortSessions(sessions []adapter.Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		// Closed sessions sort below everything else so the renderer's
		// Active group stays packed at the top and Closed forms a tail.
		if sessions[i].Closed != sessions[j].Closed {
			return !sessions[i].Closed
		}
		if sessions[i].Pinned != sessions[j].Pinned {
			return sessions[i].Pinned
		}
		if sessions[i].SortIndex != sessions[j].SortIndex {
			return sessions[i].SortIndex < sessions[j].SortIndex
		}
		if sessions[i].ProcAlive != sessions[j].ProcAlive {
			return sessions[i].ProcAlive == adapter.Alive
		}
		if !sessions[i].CreatedAt.Equal(sessions[j].CreatedAt) {
			return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
		}
		// Total-order tiebreaker on the unique agent+id. Without it, rows that
		// tie on every field above keep their input order, and that input is a
		// Go map iteration (sessionsFromMap) — randomized per call — so equal
		// rows reshuffle on every refresh tick. tmux reports creation time only
		// to the second, which makes same-second ties common.
		if sessions[i].AgentType != sessions[j].AgentType {
			return sessions[i].AgentType < sessions[j].AgentType
		}
		return sessions[i].ID < sessions[j].ID
	})
}

func (s *Service) Dispatch(ctx context.Context, agentName, prompt, cwd, mode string) (adapter.Session, error) {
	return s.DispatchNamed(ctx, agentName, "", prompt, cwd, mode)
}

func (s *Service) DispatchNamed(ctx context.Context, agentName, name, prompt, cwd, mode string) (adapter.Session, error) {
	if s.Registry == nil {
		return adapter.Session{}, errors.New("no registry configured")
	}
	a, ok := s.Registry.Get(agentName)
	if !ok {
		return adapter.Session{}, fmt.Errorf("agent %q is unavailable", agentName)
	}
	if mode == "" {
		mode = string(store.ModeYolo)
	}
	sess, err := a.Dispatch(ctx, adapter.DispatchRequest{Name: name, Prompt: prompt, Cwd: cwd, Mode: mode})
	if err != nil {
		return adapter.Session{}, err
	}
	if s.Store != nil {
		rec := RecordFromSession(sess, store.Mode(mode))
		if err := s.Store.Update(func(cfg *store.Config) error {
			cfg.PutSession(store.Key(sess.AgentType, sess.ID), rec)
			return nil
		}); err != nil {
			// Advisory only: the session is live. Killing it because the store
			// hiccupped would discard the user's work; instead surface the live
			// session plus a wrapped warning so the caller can still attach.
			return sess, fmt.Errorf("dispatched %s but failed to persist its record: %w", sess.ID, err)
		}
	}
	return sess, nil
}

func (s *Service) Stop(ctx context.Context, id string, remove bool) error {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	if s.Registry != nil {
		if a, ok := s.Registry.Get(sess.AgentType); ok && sess.TmuxSession != "" {
			if killErr := a.Stop(ctx, sess.ID); killErr != nil {
				// The kill failed. If the pane is still alive, deleting/flagging
				// the record would orphan a running session whose only handle is
				// that record — so abort the mutation and surface the error.
				// If the pane is already gone (probe says not-alive, or the
				// adapter can't probe), proceed: this preserves `uam rm` cleanup
				// of already-dead sessions.
				if hs, ok := a.(adapter.HasSessionAdapter); ok && hs.HasSession(ctx, sess.ID) {
					return fmt.Errorf("kill session %s: %w", sess.ID, killErr)
				}
			}
		}
	}
	if s.Store == nil {
		return nil
	}
	if remove {
		return s.Store.Update(func(cfg *store.Config) error {
			delete(cfg.Sessions, store.Key(sess.AgentType, sess.ID))
			return nil
		})
	}
	// Soft-close: keep the record so the user can resume later, but flag it
	// as user-closed so the UI sorts it into the Closed group and Reconcile
	// never treats it as a reboot-recovery candidate.
	return s.Store.Update(func(cfg *store.Config) error {
		key := store.Key(sess.AgentType, sess.ID)
		rec, ok := cfg.Sessions[key]
		if !ok {
			return nil
		}
		rec.Status = store.StatusClosedByUser
		cfg.Sessions[key] = rec
		return nil
	})
}

// NotifyClosed flags the record whose tmux name matches tmuxSession as
// user-closed. It is the entry point for the `uam notify-closed` CLI
// subcommand wired into tmux's session-closed hook: when the user types
// `exit` in a session (or someone runs `tmux -L uam kill-session`), tmux
// destroys the session and fires this so the status survives the close.
//
// Idempotent: a no-op if the record is already StatusClosedByUser or if
// no record matches (e.g., uam already deleted it via Ctrl+X / `uam rm`).
func (s *Service) NotifyClosed(tmuxSession string) error {
	if s.Store == nil || tmuxSession == "" {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		for key, rec := range cfg.Sessions {
			if rec.TmuxSession != tmuxSession {
				continue
			}
			if rec.Status == store.StatusClosedByUser {
				return nil
			}
			rec.Status = store.StatusClosedByUser
			cfg.Sessions[key] = rec
			return nil
		}
		return nil
	})
}

func (s *Service) Rename(ctx context.Context, id, name string) error {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	return s.applyRecordMutation(sess, func(rec *store.SessionRecord) { rec.Name = name })
}

func (s *Service) TogglePin(ctx context.Context, id string) error {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	return s.applyRecordMutation(sess, func(rec *store.SessionRecord) { rec.Pinned = !rec.Pinned })
}

// applyRecordMutation applies mut to the store record for sess inside an atomic
// read-modify-write. If the record is missing (or a zero-value phantom with no
// ID) it is backfilled from the live session first, so Rename/TogglePin never
// persist an empty-ID record that would be un-killable once the pane dies
// (C1-2). It mirrors ResumeBackground's RecordFromSession backfill.
func (s *Service) applyRecordMutation(sess adapter.Session, mut func(*store.SessionRecord)) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		key := store.Key(sess.AgentType, sess.ID)
		rec := cfg.Sessions[key]
		if rec.ID == "" {
			rec = RecordFromSession(sess, store.ModeYolo)
		}
		mut(&rec)
		cfg.Sessions[key] = rec
		return nil
	})
}

func (s *Service) SetUI(mut func(*store.UISettings)) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error { mut(&cfg.UI); return nil })
}

func (s *Service) SetDefaultAgent(agent string) error {
	if s.Store == nil || agent == "" {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error { cfg.DefaultAgent = agent; return nil })
}

func (s *Service) Find(ctx context.Context, id string) (adapter.Session, store.Config, error) {
	sessions, cfg, err := s.LoadSessions(ctx)
	if err != nil {
		return adapter.Session{}, cfg, err
	}
	for _, sess := range sessions {
		if sess.ID == id || strings.HasPrefix(sess.ID, id) || sess.TmuxSession == id || strings.HasPrefix(sess.TmuxSession, id) {
			return sess, cfg, nil
		}
	}
	return adapter.Session{}, cfg, fmt.Errorf("session %q not found", id)
}

func (s *Service) Peek(ctx context.Context, id string) (adapter.PeekResult, error) {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return adapter.PeekResult{}, err
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return adapter.PeekResult{}, fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	return a.Peek(ctx, sess.ID)
}

func (s *Service) Reply(ctx context.Context, id, text string) error {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	return a.Reply(ctx, sess.ID, text)
}

func (s *Service) AttachSpec(ctx context.Context, id string) (adapter.AttachSpec, error) {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return adapter.AttachSpec{}, err
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return adapter.AttachSpec{}, fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	if sess.ProcAlive == adapter.Exited {
		if err := s.ResumeBackground(ctx, sess.ID); err != nil {
			return adapter.AttachSpec{}, err
		}
	}
	return a.Attach(sess.ID)
}

// ResumeBackground restarts a stopped session's tmux session without attaching
// to it. It is a no-op when the session is already running.
func (s *Service) ResumeBackground(ctx context.Context, id string) error {
	sess, cfg, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	if sess.ProcAlive == adapter.Alive {
		return nil
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		return fmt.Errorf("session %q is not running and agent %q cannot resume it", sess.ID, sess.AgentType)
	}
	rec := cfg.Sessions[store.Key(sess.AgentType, sess.ID)]
	if rec.ID == "" {
		rec = RecordFromSession(sess, store.ModeYolo)
	}
	resumed, err := resumable.Resume(ctx, adapter.ResumeRequest{ID: rec.ID, Name: rec.Name, Prompt: rec.Prompt, Cwd: rec.Workdir, Mode: string(rec.Mode), TmuxSession: rec.TmuxSession, CreatedAt: rec.CreatedAt})
	if err != nil {
		return err
	}
	if s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		key := store.Key(resumed.AgentType, resumed.ID)
		rec := cfg.Sessions[key]
		if rec.ID == "" {
			rec = RecordFromSession(resumed, store.ModeYolo)
		}
		rec.TmuxSession = resumed.TmuxSession
		rec.Workdir = resumed.Cwd
		rec.LastSeenAt = time.Now()
		// Resuming a closed_by_user session reactivates it. The tmux hook
		// will flip Status back to closed_by_user on the next exit.
		rec.Status = store.StatusActive
		cfg.Sessions[key] = rec
		return nil
	})
}

func (s *Service) PrintList(ctx context.Context, asJSON bool) error {
	sessions, _, err := s.LoadSessions(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(sessions)
	}
	for _, sess := range sessions {
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", sess.ID, sess.AgentType, sess.State, sess.DisplayName, sess.Cwd)
	}
	return nil
}

func RecordFromSession(sess adapter.Session, mode store.Mode) store.SessionRecord {
	if mode == "" {
		mode = store.ModeYolo
	}
	name := firstNonEmpty(sess.DisplayName, sess.ID)
	status := store.StatusActive
	if sess.Closed {
		status = store.StatusClosedByUser
	}
	return store.SessionRecord{ID: sess.ID, Agent: sess.AgentType, Name: name, Prompt: sess.Prompt, Mode: mode, Workdir: sess.Cwd, TmuxSession: sess.TmuxSession, CreatedAt: sess.CreatedAt, LastSeenAt: time.Now(), Pinned: sess.Pinned, Group: sess.Group, SortIndex: sess.SortIndex, Status: status}
}

func (s *Service) UpdateSortOrder(sessions []adapter.Session) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		for i, sess := range sessions {
			key := store.Key(sess.AgentType, sess.ID)
			rec := cfg.Sessions[key]
			if rec.ID == "" {
				rec = RecordFromSession(sess, store.ModeYolo)
			}
			rec.SortIndex = i
			cfg.Sessions[key] = rec
		}
		return nil
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
