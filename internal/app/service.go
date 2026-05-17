package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/randomcodespace/unified-agent-manager/internal/adapter"
	"github.com/randomcodespace/unified-agent-manager/internal/pr"
	"github.com/randomcodespace/unified-agent-manager/internal/store"
)

type Service struct {
	Store    *store.Store
	Registry *adapter.Registry
}

func NewService(st *store.Store, reg *adapter.Registry) *Service {
	return &Service{Store: st, Registry: reg}
}

func (s *Service) LoadSessions(ctx context.Context) ([]adapter.Session, store.Config, error) {
	cfg := store.DefaultConfig()
	var err error
	if s.Store != nil {
		cfg, err = s.Store.Load()
		if err != nil {
			return nil, cfg, err
		}
	}
	live := map[string]adapter.Session{}
	if s.Registry != nil {
		for _, a := range s.Registry.Enabled() {
			sessions, err := a.List(ctx)
			if err != nil {
				continue
			}
			for _, sess := range sessions {
				live[store.Key(sess.AgentType, sess.ID)] = sess
			}
		}
	}
	now := time.Now()
	mutated := false
	for key, rec := range cfg.Sessions {
		if sess, ok := live[key]; ok {
			sess.DisplayName = firstNonEmpty(rec.Name, sess.DisplayName)
			sess.Pinned = rec.Pinned
			sess.Group = rec.Group
			sess.SortIndex = rec.SortIndex
			if rec.PR != nil && sess.PR == nil {
				sess.PR = &adapter.PRRef{URL: rec.PR.URL, Number: rec.PR.Number, Status: adapter.PRStatus(rec.PR.LastStatus)}
			}
			live[key] = sess
			continue
		}
		// Keep recent dead records visible so users can clean them up.
		live[key] = adapter.Session{ID: rec.ID, AgentType: rec.Agent, DisplayName: rec.Name, Cwd: rec.Workdir, TmuxSession: rec.TmuxSession, State: adapter.Failed, ProcAlive: adapter.Exited, Activity: "session not running", CreatedAt: rec.CreatedAt, LastChange: now, Pinned: rec.Pinned, Group: rec.Group, SortIndex: rec.SortIndex}
	}
	for key, sess := range live {
		rec, ok := cfg.Sessions[key]
		if !ok || rec.ID == "" {
			rec = RecordFromSession(sess, store.ModeYolo)
			mutated = true
		}
		if sess.PR != nil {
			status := string(sess.PR.Status)
			if rec.PR == nil || rec.PR.URL != sess.PR.URL || time.Since(rec.PR.LastChecked) > 60*time.Second {
				if checked, err := pr.Check(ctx, sess.PR.URL); err == nil && checked != pr.None {
					status = string(checked)
					sess.PR.Status = adapter.PRStatus(status)
					live[key] = sess
				}
				rec.PR = &store.PRRecord{URL: sess.PR.URL, Number: sess.PR.Number, LastStatus: status, LastChecked: now}
				cfg.Sessions[key] = rec
				mutated = true
			}
		}
	}
	if mutated && s.Store != nil {
		_ = s.Store.Save(cfg)
	}
	out := make([]adapter.Session, 0, len(live))
	for _, sess := range live {
		out = append(out, sess)
	}
	SortSessions(out)
	return out, cfg, nil
}

func SortSessions(sessions []adapter.Session) {
	order := map[adapter.State]int{adapter.NeedsInput: 0, adapter.Working: 1, adapter.ReadyForReview: 2, adapter.Failed: 3, adapter.Completed: 4, adapter.Idle: 5}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].Pinned != sessions[j].Pinned {
			return sessions[i].Pinned
		}
		if sessions[i].SortIndex != sessions[j].SortIndex {
			return sessions[i].SortIndex < sessions[j].SortIndex
		}
		if order[sessions[i].State] != order[sessions[j].State] {
			return order[sessions[i].State] < order[sessions[j].State]
		}
		return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
	})
}

func (s *Service) Dispatch(ctx context.Context, agentName, prompt, cwd, mode string) (adapter.Session, error) {
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
	sess, err := a.Dispatch(ctx, adapter.DispatchRequest{Prompt: prompt, Cwd: cwd, Mode: mode})
	if err != nil {
		return adapter.Session{}, err
	}
	if s.Store != nil {
		rec := RecordFromSession(sess, store.Mode(mode))
		_ = s.Store.Update(func(cfg *store.Config) error { cfg.Sessions[store.Key(sess.AgentType, sess.ID)] = rec; return nil })
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
			_ = a.Stop(ctx, sess.ID)
		}
	}
	if s.Store != nil && remove {
		return s.Store.Update(func(cfg *store.Config) error { delete(cfg.Sessions, store.Key(sess.AgentType, sess.ID)); return nil })
	}
	return nil
}

func (s *Service) Rename(ctx context.Context, id, name string) error {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	return s.Store.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[store.Key(sess.AgentType, sess.ID)]
		rec.Name = name
		cfg.Sessions[store.Key(sess.AgentType, sess.ID)] = rec
		return nil
	})
}

func (s *Service) TogglePin(ctx context.Context, id string) error {
	sess, _, err := s.Find(ctx, id)
	if err != nil {
		return err
	}
	return s.Store.Update(func(cfg *store.Config) error {
		rec := cfg.Sessions[store.Key(sess.AgentType, sess.ID)]
		rec.Pinned = !rec.Pinned
		cfg.Sessions[store.Key(sess.AgentType, sess.ID)] = rec
		return nil
	})
}

func (s *Service) SetUI(mut func(*store.UISettings)) error {
	if s.Store == nil {
		return nil
	}
	return s.Store.Update(func(cfg *store.Config) error { mut(&cfg.UI); return nil })
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
		return adapter.PeekResult{}, fmt.Errorf("agent %q unavailable", sess.AgentType)
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
		return fmt.Errorf("agent %q unavailable", sess.AgentType)
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
		return adapter.AttachSpec{}, fmt.Errorf("agent %q unavailable", sess.AgentType)
	}
	return a.Attach(sess.ID)
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
	return store.SessionRecord{ID: sess.ID, Agent: sess.AgentType, Name: name, Mode: mode, Workdir: sess.Cwd, TmuxSession: sess.TmuxSession, CreatedAt: sess.CreatedAt, LastSeenAt: time.Now(), Pinned: sess.Pinned, Group: sess.Group, SortIndex: sess.SortIndex}
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
