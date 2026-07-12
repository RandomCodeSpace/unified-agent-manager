package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RandomCodeSpace/unified-agent-manager/internal/adapter"
	"github.com/RandomCodeSpace/unified-agent-manager/internal/displaytext"
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
	checkPR    func(context.Context, string) (pr.Status, error)
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

const (
	prCheckTimeout   = 4 * time.Second
	prRefreshTimeout = 5 * time.Second
	prRefreshAge     = 60 * time.Second
	prRefreshWorkers = 4
)

func NewService(st *store.Store, reg *adapter.Registry) *Service {
	return &Service{Store: st, Registry: reg, checkPR: pr.Check}
}

func (s *Service) LoadSessions(ctx context.Context) ([]adapter.Session, store.Config, error) {
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, cfg, err
	}
	live := s.liveSessions(ctx)
	s.mergeStoredSessions(live, cfg, time.Now())
	// Discovery and reconciliation are local-only. Network PR enrichment runs on
	// the independent RefreshPRStatuses cadence and never delays this path.
	updates := s.refreshSessionRecords(ctx, live, &cfg)
	if err := s.persistRefresh(updates); err != nil {
		log.Warn("persist refreshed session metadata failed", "records", len(updates), "error", err)
	}
	out := sessionsFromMap(live)
	SortSessions(out)
	return out, cfg, nil
}

// PruneStartup removes long-stale, dead records from the store so
// sessions.json does not grow unbounded. It is outage-safe: pruning only runs
// when at least one live session is visible, so a transient failure to scan
// the session runtime directory (which looks exactly like "no sessions") can
// never wipe every record. PruneOld then drops records whose process is gone
// and that have not been seen within pruneMaxAge (F20 Stage 2).
func (s *Service) PruneStartup(ctx context.Context) error {
	if s.Store == nil {
		return nil
	}
	live := s.liveSessions(ctx)
	if len(live) == 0 {
		// No live session visible — could be a scan failure; don't prune.
		return nil
	}
	liveNames := make(map[string]struct{}, len(live))
	for _, sess := range live {
		if sess.SessionName != "" {
			liveNames[sess.SessionName] = struct{}{}
		}
	}
	exists := func(sessionName string) bool {
		_, ok := liveNames[sessionName]
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
type refreshPatch struct {
	create            *store.SessionRecord
	status            *store.Status
	lastSeenAt        *time.Time
	pr                *store.PRRecord
	clearPR           bool
	providerSessionID *string
}

func (s *Service) persistRefresh(updates map[string]refreshPatch) error {
	if len(updates) == 0 || s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		for key, patch := range updates {
			applyRefreshPatch(cfg, key, patch)
		}
		return nil
	})
}

func applyRefreshPatch(cfg *store.Config, key string, patch refreshPatch) {
	rec, exists := cfg.Sessions[key]
	if (!exists || rec.ID == "") && patch.create != nil {
		rec = *patch.create
	}
	if patch.status != nil {
		rec.Status = *patch.status
	}
	if patch.lastSeenAt != nil {
		rec.LastSeenAt = *patch.lastSeenAt
	}
	if patch.clearPR {
		rec.PR = nil
	} else if patch.pr != nil {
		pr := *patch.pr
		rec.PR = &pr
	}
	if patch.providerSessionID != nil {
		rec.ProviderSessionID = *patch.providerSessionID
	}
	cfg.Sessions[key] = rec
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
	sessions, err := s.Registry.ListAll(ctx)
	if err != nil {
		// Partial custom-adapter results are retained; production's shared backend
		// failure yields an empty snapshot and one actionable warning.
		log.Warn("listing managed sessions failed", "error", err)
	}
	for _, sess := range sessions {
		live[store.Key(sess.AgentType, sess.ID)] = sess
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
	// A live session only knows the 8-char id embedded in its session name;
	// the record carries the full UUID. Restore it so Find can match the full
	// id the dispatch command printed — without this, peek/stop/attach by full
	// id fail exactly while the session is alive (they worked once it died,
	// because dead rows are built from the record).
	if rec.ID != "" && strings.HasPrefix(rec.ID, sess.ID) {
		sess.ID = rec.ID
	}
	sess.DisplayName = firstNonEmpty(rec.Name, sess.DisplayName)
	sess.CommandAlias = firstNonEmpty(rec.CommandAlias, sess.CommandAlias)
	sess.Prompt = firstNonEmpty(rec.Prompt, sess.Prompt)
	// A non-empty live identity is authoritative; an empty discovery result is
	// a transient absence and must not erase a known stored identity.
	sess.ProviderSessionID = firstNonEmpty(sess.ProviderSessionID, rec.ProviderSessionID)
	sess.Pinned = rec.Pinned
	sess.Group = rec.Group
	sess.SortIndex = rec.SortIndex
	// Closed is retained as explicit-stop reason metadata. A live process is
	// always RUNNING, so refresh reconciles a stale user-closed status to Active.
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
	state := adapter.Completed
	if rec.LastExitCode != nil && *rec.LastExitCode != 0 {
		state = adapter.Failed
	}
	return adapter.Session{ExitCode: rec.LastExitCode, ProviderSessionID: rec.ProviderSessionID, ID: rec.ID, AgentType: rec.Agent, CommandAlias: rec.CommandAlias, DisplayName: rec.Name, Prompt: rec.Prompt, Cwd: rec.Workdir, SessionName: rec.SessionName, State: state, ProcAlive: adapter.Exited, CreatedAt: rec.CreatedAt, LastChange: now, Pinned: rec.Pinned, Group: rec.Group, SortIndex: rec.SortIndex, Closed: rec.Status == store.StatusClosedByUser}
}

// refreshSessionRecords reconciles live sessions against the loaded config and
// returns the records this refresh wants to persist (keyed by store key). It
// also updates the in-memory cfg and live maps so the returned snapshot is
// consistent. It only records locally discovered PR metadata; status checks run
// independently in RefreshPRStatuses.
func (s *Service) refreshSessionRecords(_ context.Context, live map[string]adapter.Session, cfg *store.Config) map[string]refreshPatch {
	updates := map[string]refreshPatch{}
	now := time.Now()
	for key, sess := range live {
		before, existed := cfg.Sessions[key]
		rec, changed, keep := reconcileRefreshRecord(cfg, key, sess, now)
		if !keep {
			continue
		}
		if syncDiscoveredPRRecord(sess, &rec) {
			cfg.Sessions[key] = rec
			changed = true
		}
		if changed {
			updates[key] = makeRefreshPatch(before, rec, existed && before.ID != "")
		}
	}
	return updates
}

func makeRefreshPatch(before, after store.SessionRecord, existed bool) refreshPatch {
	patch := refreshPatch{}
	if !existed {
		created := after
		patch.create = &created
	}
	if !existed || before.Status != after.Status {
		status := after.Status
		patch.status = &status
	}
	if !existed || !before.LastSeenAt.Equal(after.LastSeenAt) {
		lastSeen := after.LastSeenAt
		patch.lastSeenAt = &lastSeen
	}
	if !prRecordEqual(before.PR, after.PR) {
		if after.PR == nil {
			patch.clearPR = true
		} else {
			pr := *after.PR
			patch.pr = &pr
		}
	}
	if after.ProviderSessionID != "" && before.ProviderSessionID != after.ProviderSessionID {
		providerID := after.ProviderSessionID
		patch.providerSessionID = &providerID
	}
	return patch
}

func prRecordEqual(a, b *store.PRRecord) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func reconcileRefreshRecord(cfg *store.Config, key string, sess adapter.Session, now time.Time) (store.SessionRecord, bool, bool) {
	rec, ok := cfg.Sessions[key]
	changed := false
	if !ok || rec.ID == "" {
		// Backfill a record for a live session we have no (valid) record for.
		// Guard against an empty session ID so we never persist an un-killable
		// phantom (C1-1 fix-trap).
		if sess.ID == "" {
			return rec, false, false
		}
		rec = RecordFromSession(sess, store.ModeYolo)
		cfg.PutSession(key, rec)
		changed = true
	} else if rec.Status == store.StatusClosedByUser && sess.ProcAlive == adapter.Alive {
		// Anti-flap: the user closed this session but its pane is still alive, so
		// it belongs in RUNNING. Reset the persisted status to Active,
		// otherwise the next merge re-flags it Closed and it flaps (F18).
		rec.Status = store.StatusActive
		cfg.Sessions[key] = rec
		changed = true
	}
	// Keep LastSeenAt fresh for a still-running pane so staleness-based pruning
	// never deletes a live session. Gated on the refresh interval so a long-lived
	// session isn't re-saved on every tick (F20 Stage 1).
	if sess.ProcAlive == adapter.Alive && now.Sub(rec.LastSeenAt) >= lastSeenRefresh {
		rec.LastSeenAt = now
		cfg.Sessions[key] = rec
		changed = true
	}
	if sess.ProviderSessionID != "" && sess.ProviderSessionID != rec.ProviderSessionID {
		rec.ProviderSessionID = sess.ProviderSessionID
		cfg.Sessions[key] = rec
		changed = true
	}
	return rec, changed, true
}

func syncDiscoveredPRRecord(sess adapter.Session, rec *store.SessionRecord) bool {
	if sess.PR == nil || (rec.PR != nil && rec.PR.URL == sess.PR.URL) {
		return false
	}
	rec.PR = &store.PRRecord{URL: sess.PR.URL, Number: sess.PR.Number, LastStatus: string(sess.PR.Status)}
	return true
}

type prRefreshJob struct {
	key string
	ref adapter.PRRef
}

type prRefreshResult struct {
	key    string
	record store.PRRecord
}

type prCheckResult struct {
	status pr.Status
	err    error
}

// checkPRWithContext enforces the caller's deadline even when a custom checker
// fails to observe context cancellation. The result channel is buffered so a
// late checker can finish without retaining the refresh worker.
func checkPRWithContext(ctx context.Context, checker func(context.Context, string) (pr.Status, error), url string) (pr.Status, error) {
	resultCh := make(chan prCheckResult, 1)
	go func() {
		status, err := checker(ctx, url)
		resultCh <- prCheckResult{status: status, err: err}
	}()
	select {
	case result := <-resultCh:
		return result.status, result.err
	case <-ctx.Done():
		return pr.None, ctx.Err()
	}
}

// RefreshPRStatuses enriches stale discovered PRs outside the session-discovery
// path. Only PR-owned fields are persisted, and overlapping passes coalesce.
func (s *Service) RefreshPRStatuses(ctx context.Context) error {
	if s.Store == nil || !s.prInFlight.CompareAndSwap(false, true) {
		return nil
	}
	defer s.prInFlight.Store(false)
	ctx, cancel := context.WithTimeout(ctx, prRefreshTimeout)
	defer cancel()
	sessions, cfg, err := s.LoadSessions(ctx)
	if err != nil {
		return err
	}
	jobs := stalePRRefreshJobs(sessions, cfg, time.Now())
	if len(jobs) == 0 {
		return nil
	}
	results := runPRRefreshJobs(ctx, jobs, firstPRChecker(s.checkPR))
	if err := s.persistPRRefreshResults(results); err != nil {
		return err
	}
	return ctx.Err()
}

func stalePRRefreshJobs(sessions []adapter.Session, cfg store.Config, now time.Time) []prRefreshJob {
	jobs := make([]prRefreshJob, 0, len(sessions))
	for _, sess := range sessions {
		if sess.PR == nil {
			continue
		}
		key := store.Key(sess.AgentType, sess.ID)
		rec := cfg.Sessions[key]
		if rec.PR != nil && rec.PR.URL == sess.PR.URL && now.Sub(rec.PR.LastChecked) < prRefreshAge {
			continue
		}
		jobs = append(jobs, prRefreshJob{key: key, ref: *sess.PR})
	}
	return jobs
}

func firstPRChecker(checker func(context.Context, string) (pr.Status, error)) func(context.Context, string) (pr.Status, error) {
	if checker != nil {
		return checker
	}
	return pr.Check
}

func runPRRefreshJobs(ctx context.Context, jobs []prRefreshJob, checker func(context.Context, string) (pr.Status, error)) []prRefreshResult {
	jobCh := make(chan prRefreshJob)
	resultCh := make(chan prRefreshResult, len(jobs))
	workers := min(prRefreshWorkers, len(jobs))
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				resultCh <- runPRRefreshJob(ctx, job, checker)
			}
		}()
	}
	go func() {
		defer close(jobCh)
		for _, job := range jobs {
			select {
			case jobCh <- job:
			case <-ctx.Done():
				return
			}
		}
	}()
	wg.Wait()
	close(resultCh)
	results := make([]prRefreshResult, 0, len(jobs))
	for result := range resultCh {
		results = append(results, result)
	}
	return results
}

func runPRRefreshJob(ctx context.Context, job prRefreshJob, checker func(context.Context, string) (pr.Status, error)) prRefreshResult {
	status := job.ref.Status
	checkCtx, checkCancel := context.WithTimeout(ctx, prCheckTimeout)
	checked, checkErr := checkPRWithContext(checkCtx, checker, job.ref.URL)
	checkCancel()
	if checkErr == nil && checked != pr.None {
		status = adapterStatus(checked)
	}
	return prRefreshResult{key: job.key, record: store.PRRecord{URL: job.ref.URL, Number: job.ref.Number, LastStatus: string(status), LastChecked: time.Now()}}
}

func (s *Service) persistPRRefreshResults(results []prRefreshResult) error {
	return s.Store.Update(func(cfg *store.Config) error {
		for _, result := range results {
			rec, ok := cfg.Sessions[result.key]
			if !ok || rec.PR == nil || rec.PR.URL != result.record.URL {
				continue
			}
			record := result.record
			rec.PR = &record
			cfg.Sessions[result.key] = rec
		}
		return nil
	})
}

func adapterStatus(status pr.Status) adapter.PRStatus {
	switch status {
	case pr.Open:
		return adapter.PROpen
	case pr.Merged:
		return adapter.PRMerged
	case pr.Closed:
		return adapter.PRClosed
	case pr.Draft:
		return adapter.PRDraft
	default:
		return adapter.PRNone
	}
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
		// Process liveness is the dashboard lifecycle source of truth. Persisted
		// Closed is reason/compatibility metadata and must not split stopped rows.
		if sessions[i].ProcAlive != sessions[j].ProcAlive {
			return sessions[i].ProcAlive == adapter.Alive
		}
		if sessions[i].Pinned != sessions[j].Pinned {
			return sessions[i].Pinned
		}
		if sessions[i].SortIndex != sessions[j].SortIndex {
			return sessions[i].SortIndex < sessions[j].SortIndex
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

func (s *Service) DispatchNamed(ctx context.Context, agentName, name, prompt, cwd, mode string) (adapter.Session, error) {
	return s.DispatchNamedWithAlias(ctx, agentName, "", name, prompt, cwd, mode)
}

func (s *Service) DispatchNamedWithAlias(ctx context.Context, agentName, commandAlias, name, prompt, cwd, mode string) (adapter.Session, error) {
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
	sess, err := a.Dispatch(ctx, adapter.DispatchRequest{Name: name, CommandAlias: commandAlias, Prompt: prompt, Cwd: cwd, Mode: mode})
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
	return s.stopFound(ctx, sess, remove)
}

// StopExact is the provider-aware TUI variant of Stop. The ID-only method is
// retained for CLI prefix lookup compatibility.
func (s *Service) StopExact(ctx context.Context, agentName, id string, remove bool) error {
	sess, _, err := s.FindExact(ctx, agentName, id)
	if err != nil {
		return err
	}
	return s.stopFound(ctx, sess, remove)
}

func (s *Service) stopFound(ctx context.Context, sess adapter.Session, remove bool) error {
	if err := s.stopAdapterSession(ctx, sess); err != nil {
		return err
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
	// Soft-close: keep the record so the user can resume later, while preserving
	// user-closed as reason metadata. Process liveness places it in STOPPED.
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

func (s *Service) stopAdapterSession(ctx context.Context, sess adapter.Session) error {
	if s.Registry == nil || sess.SessionName == "" {
		return nil
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return nil
	}
	if killErr := a.Stop(ctx, sess.ID); killErr != nil {
		// The kill failed. If the pane is still alive, deleting/flagging the
		// record would orphan a running session whose only handle is that record —
		// so abort the mutation and surface the error. If the pane is already gone
		// (probe says not-alive, or the adapter can't probe), proceed: this
		// preserves `uam rm` cleanup of already-dead sessions.
		if hs, ok := a.(adapter.HasSessionAdapter); ok && hs.HasSession(ctx, sess.ID) {
			return fmt.Errorf("kill session %s: %w", sess.ID, killErr)
		}
	}
	return nil
}

// NotifyClosed flags the record whose backend session name matches as
// user-closed. It backs the `uam notify-closed` CLI subcommand. Session hosts
// normally mark records closed in-process when their agent exits
// (store.MarkSessionClosed); this stays as the scriptable entry point.
//
// Idempotent: a no-op if the record is already StatusClosedByUser or if
// no record matches (e.g., uam already deleted it via Ctrl+X / `uam rm`).
func (s *Service) NotifyClosed(sessionName string) error {
	if s.Store == nil || sessionName == "" {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		for key, rec := range cfg.Sessions {
			if rec.SessionName != sessionName {
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

func (s *Service) RenameExact(ctx context.Context, agentName, id, name string) error {
	sess, _, err := s.FindExact(ctx, agentName, id)
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

func (s *Service) TogglePinExact(ctx context.Context, agentName, id string) error {
	sess, _, err := s.FindExact(ctx, agentName, id)
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
		if sess.ID == id || sess.SessionName == id {
			return sess, cfg, nil
		}
	}
	var match adapter.Session
	for _, sess := range sessions {
		if strings.HasPrefix(sess.ID, id) || strings.HasPrefix(sess.SessionName, id) {
			if match.ID != "" {
				return adapter.Session{}, cfg, fmt.Errorf("session %q is ambiguous; matches multiple sessions", id)
			}
			match = sess
		}
	}
	if match.ID != "" {
		return match, cfg, nil
	}
	return adapter.Session{}, cfg, fmt.Errorf("session %q not found", id)
}

// FindExact resolves the unique provider/session pair used by the TUI. Unlike
// Find it deliberately performs no prefix or cross-provider matching.
func (s *Service) FindExact(ctx context.Context, agentName, id string) (adapter.Session, store.Config, error) {
	sessions, cfg, err := s.LoadSessions(ctx)
	if err != nil {
		return adapter.Session{}, cfg, err
	}
	for _, sess := range sessions {
		if sess.AgentType == agentName && sess.ID == id {
			return sess, cfg, nil
		}
	}
	return adapter.Session{}, cfg, fmt.Errorf("session %q for provider %q not found", id, agentName)
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

func (s *Service) PeekExact(ctx context.Context, agentName, id string) (adapter.PeekResult, error) {
	sess, _, err := s.FindExact(ctx, agentName, id)
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

func (s *Service) ReplyExact(ctx context.Context, agentName, id, text string) error {
	sess, _, err := s.FindExact(ctx, agentName, id)
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
	return s.AttachSpecWithOptions(ctx, id, ResumeOptions{})
}

func (s *Service) AttachSpecExact(ctx context.Context, agentName, id string) (adapter.AttachSpec, error) {
	return s.AttachSpecExactWithOptions(ctx, agentName, id, ResumeOptions{})
}

func (s *Service) AttachSpecExactWithOptions(ctx context.Context, agentName, id string, opts ResumeOptions) (adapter.AttachSpec, error) {
	sess, _, err := s.FindExact(ctx, agentName, id)
	if err != nil {
		return adapter.AttachSpec{}, err
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return adapter.AttachSpec{}, fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	if sess.ProcAlive == adapter.Exited {
		if err := s.ResumeBackgroundExactWithOptions(ctx, agentName, id, opts); err != nil {
			return adapter.AttachSpec{}, err
		}
	}
	return a.Attach(sess.ID)
}

func (s *Service) AttachSpecWithOptions(ctx context.Context, id string, opts ResumeOptions) (adapter.AttachSpec, error) {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return adapter.AttachSpec{}, err
	}
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return adapter.AttachSpec{}, fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	if sess.ProcAlive == adapter.Exited {
		if err := s.ResumeBackgroundWithOptions(ctx, sess.ID, opts); err != nil {
			return adapter.AttachSpec{}, err
		}
	}
	return a.Attach(sess.ID)
}

// Restart replaces a session's agent process while keeping its identity: a
// running backend session is stopped (soft close, record kept), then resumed
// under the same name with the provider's resume args so the agent picks its
// conversation back up. A session that is already stopped simply resumes.
func (s *Service) Restart(ctx context.Context, id string) error {
	return s.RestartWithOptions(ctx, id, ResumeOptions{})
}

func (s *Service) RestartExact(ctx context.Context, agentName, id string) error {
	return s.RestartExactWithOptions(ctx, agentName, id, ResumeOptions{})
}

func (s *Service) RestartExactWithOptions(ctx context.Context, agentName, id string, opts ResumeOptions) error {
	sess, cfg, err := s.FindExact(ctx, agentName, id)
	if err != nil {
		return err
	}
	resumable, rec, req, err := s.prepareResume(sess, cfg, opts)
	if err != nil {
		return err
	}
	if sess.ProcAlive == adapter.Alive {
		if err := s.StopExact(ctx, agentName, id, false); err != nil {
			return err
		}
	}
	return s.resumePrepared(ctx, resumable, rec, req)
}

func (s *Service) RestartWithOptions(ctx context.Context, id string, opts ResumeOptions) error {
	sess, cfg, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	resumable, rec, req, err := s.prepareResume(sess, cfg, opts)
	if err != nil {
		return err
	}
	if sess.ProcAlive == adapter.Alive {
		if err := s.Stop(ctx, id, false); err != nil {
			return err
		}
	}
	return s.resumePrepared(ctx, resumable, rec, req)
}

// ResumeBackground restarts a stopped session's backend session without
// attaching to it. It is a no-op when the session is already running.
func (s *Service) ResumeBackground(ctx context.Context, id string) error {
	return s.ResumeBackgroundWithOptions(ctx, id, ResumeOptions{})
}

func (s *Service) ResumeBackgroundExact(ctx context.Context, agentName, id string) error {
	return s.ResumeBackgroundExactWithOptions(ctx, agentName, id, ResumeOptions{})
}

func (s *Service) ResumeBackgroundExactWithOptions(ctx context.Context, agentName, id string, opts ResumeOptions) error {
	sess, cfg, err := s.FindExact(ctx, agentName, id)
	if err != nil {
		return err
	}
	return s.resumeBackgroundFound(ctx, sess, cfg, opts)
}

type ResumeKind = adapter.ResumeKind

const (
	ResumeExact       = adapter.ResumeExact
	ResumeHeuristic   = adapter.ResumeHeuristic
	ResumeUnsupported = adapter.ResumeUnsupported
)

type ResumeOptions struct {
	AllowLatest bool
}

var ErrAmbiguousResume = errors.New("ambiguous heuristic resume")

func (s *Service) ResumeBackgroundWithOptions(ctx context.Context, id string, opts ResumeOptions) error {
	sess, cfg, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	return s.resumeBackgroundFound(ctx, sess, cfg, opts)
}

func (s *Service) resumeBackgroundFound(ctx context.Context, sess adapter.Session, cfg store.Config, opts ResumeOptions) error {
	if sess.ProcAlive == adapter.Alive {
		return nil
	}
	resumable, rec, req, err := s.prepareResume(sess, cfg, opts)
	if err != nil {
		return err
	}
	return s.resumePrepared(ctx, resumable, rec, req)
}

// resumePrepared carries one validated resume decision through launch. Restart
// deliberately calls this after stopping the old process so a newly persisted
// sibling cannot trigger a second ambiguity rejection after the destructive
// step. The initial prepareResume call remains the sole decision point.
func (s *Service) resumePrepared(ctx context.Context, resumable adapter.ResumableAdapter, rec store.SessionRecord, req adapter.ResumeRequest) error {
	var lifecycle resumeLifecycle
	var err error
	if s.Store != nil {
		lifecycle, err = s.beginResumeLifecycle(rec)
		if err != nil {
			return err
		}
	}
	resumed, err := resumable.Resume(ctx, req)
	if err != nil {
		if s.Store != nil {
			if rollbackErr := s.rollbackResumeLifecycle(lifecycle); rollbackErr != nil {
				log.Warn("restore failed resume lifecycle", "session", rec.SessionName, "error", rollbackErr)
			}
		}
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
		rec.SessionName = resumed.SessionName
		rec.Workdir = resumed.Cwd
		rec.CommandAlias = firstNonEmpty(rec.CommandAlias, resumed.CommandAlias)
		rec.ProviderSessionID = firstNonEmpty(resumed.ProviderSessionID, rec.ProviderSessionID)
		rec.LastSeenAt = nextResumeTimestamp(lifecycle.attemptAt)
		cfg.Sessions[key] = rec
		return nil
	})
}

type resumeLifecycle struct {
	key        string
	status     store.Status
	exitCode   *int
	lastSeenAt time.Time
	attemptAt  time.Time
}

// beginResumeLifecycle clears the previous process result before launch. The
// replacement host can then record an immediate exit without a later success
// write erasing that newer result.
func (s *Service) beginResumeLifecycle(fallback store.SessionRecord) (resumeLifecycle, error) {
	key := store.Key(fallback.Agent, fallback.ID)
	state := resumeLifecycle{key: key}
	err := s.Store.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[key]
		if rec.ID == "" {
			rec = fallback
		}
		state.status = rec.Status
		if rec.LastExitCode != nil {
			code := *rec.LastExitCode
			state.exitCode = &code
		}
		state.lastSeenAt = rec.LastSeenAt
		state.attemptAt = nextResumeTimestamp(rec.LastSeenAt)
		rec.Status = store.StatusActive
		rec.LastExitCode = nil
		rec.LastSeenAt = state.attemptAt
		cfg.Sessions[key] = rec
		return nil
	})
	return state, err
}

// rollbackResumeLifecycle restores the prior result only while the lifecycle
// fields still have the pre-launch values. A concurrent host exit or explicit
// stop wins and is never overwritten by rollback.
func (s *Service) rollbackResumeLifecycle(state resumeLifecycle) error {
	return s.Store.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[state.key]
		if rec.ID == "" || rec.Status != store.StatusActive || rec.LastExitCode != nil || !rec.LastSeenAt.Equal(state.attemptAt) {
			return nil
		}
		rec.Status = state.status
		rec.LastSeenAt = state.lastSeenAt
		if state.exitCode == nil {
			rec.LastExitCode = nil
		} else {
			code := *state.exitCode
			rec.LastExitCode = &code
		}
		cfg.Sessions[state.key] = rec
		return nil
	})
}

func nextResumeTimestamp(after time.Time) time.Time {
	now := time.Now().UTC()
	if !now.After(after) {
		return after.Add(time.Nanosecond)
	}
	return now
}

func (s *Service) prepareResume(sess adapter.Session, cfg store.Config, opts ResumeOptions) (adapter.ResumableAdapter, store.SessionRecord, adapter.ResumeRequest, error) {
	a, ok := s.Registry.Get(sess.AgentType)
	if !ok {
		return nil, store.SessionRecord{}, adapter.ResumeRequest{}, fmt.Errorf(agentUnavailableFormat, sess.AgentType)
	}
	resumable, ok := a.(adapter.ResumableAdapter)
	if !ok {
		return nil, store.SessionRecord{}, adapter.ResumeRequest{}, fmt.Errorf("session %q is not running and agent %q cannot resume it", sess.ID, sess.AgentType)
	}
	rec := cfg.Sessions[store.Key(sess.AgentType, sess.ID)]
	if rec.ID == "" {
		rec = RecordFromSession(sess, store.ModeYolo)
	}
	req := adapter.ResumeRequest{ID: rec.ID, Name: rec.Name, CommandAlias: rec.CommandAlias, Prompt: rec.Prompt, Cwd: rec.Workdir, Mode: string(rec.Mode), SessionName: rec.SessionName, ProviderSessionID: rec.ProviderSessionID, CreatedAt: rec.CreatedAt}
	kind := adapter.ResumeHeuristic
	if classifier, ok := a.(adapter.ResumeKindAdapter); ok {
		kind = classifier.ResumeKind(req)
	}
	if kind == adapter.ResumeUnsupported {
		return nil, store.SessionRecord{}, adapter.ResumeRequest{}, fmt.Errorf("session %q is not running and agent %q cannot resume it", sess.ID, sess.AgentType)
	}
	if kind == adapter.ResumeHeuristic && !opts.AllowLatest && retainedSessionCount(cfg, rec.Agent, rec.Workdir) > 1 {
		return nil, store.SessionRecord{}, adapter.ResumeRequest{}, fmt.Errorf("%w: provider %q has multiple retained sessions in %q; retry with --allow-latest", ErrAmbiguousResume, rec.Agent, rec.Workdir)
	}
	return resumable, rec, req, nil
}

func retainedSessionCount(cfg store.Config, agentName, workdir string) int {
	want := normalizedWorkspace(workdir)
	count := 0
	for _, candidate := range cfg.Sessions {
		if strings.EqualFold(candidate.Agent, agentName) && normalizedWorkspace(candidate.Workdir) == want {
			count++
		}
	}
	return count
}

func normalizedWorkspace(workdir string) string {
	if abs, err := filepath.Abs(workdir); err == nil {
		workdir = abs
	}
	return filepath.Clean(workdir)
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
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n",
			displaytext.Sanitize(sess.ID),
			displaytext.Sanitize(sess.AgentType),
			displaytext.Sanitize(string(sess.State)),
			displaytext.Sanitize(sess.DisplayName),
			displaytext.Sanitize(sess.Cwd),
		)
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
	return store.SessionRecord{ID: sess.ID, Agent: sess.AgentType, CommandAlias: sess.CommandAlias, Name: name, Prompt: sess.Prompt, Mode: mode, Workdir: sess.Cwd, SessionName: sess.SessionName, ProviderSessionID: sess.ProviderSessionID, CreatedAt: sess.CreatedAt, LastSeenAt: time.Now(), Pinned: sess.Pinned, Group: sess.Group, SortIndex: sess.SortIndex, Status: status}
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

// UpdateSortIndices persists the explicit canonical positions carried by each
// session. Grouped presentation order is not itself a global sort order: only
// the pair moved inside a workspace has its positions exchanged.
func (s *Service) UpdateSortIndices(sessions []adapter.Session) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error {
		for _, sess := range sessions {
			key := store.Key(sess.AgentType, sess.ID)
			rec := cfg.Sessions[key]
			if rec.ID == "" {
				rec = RecordFromSession(sess, store.ModeYolo)
			}
			rec.SortIndex = sess.SortIndex
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
