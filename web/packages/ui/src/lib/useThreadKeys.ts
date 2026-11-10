import { useEffect, useEffectEvent, useMemo } from 'react';
import { readOnly, type Project, type SessionSummary } from '../api';
import type { FederationView, Machine, MachineFilter } from '../FederationContext';
import type { Auth } from '../state';
import { decodeEntity, encodeEntity } from './instanceIdentity';
import { keysTaken } from './overlays';
import { byActivity, cycleTask, needsYouNow, sidebarTasks, type newsReader } from './tasks';
import { HASH_PREFIX } from './useHashRoute';

/** The Thread shortcuts that work from anywhere: Alt+N for a new Thread, Alt+J and Alt+K through the Threads that need the owner. */
export function useThreadKeys({ auth, openNewTask, federation, machines, machineFilter, here, projects, sessions, filter, hasNews, selectedId, select }: {
  auth: Auth;
  openNewTask: () => void;
  federation: FederationView | null;
  machines: Machine[] | undefined;
  machineFilter: MachineFilter | null | undefined;
  /** This machine's id among the connected instances ('' for the hosting one). */
  here: string;
  projects: Project[];
  sessions: SessionSummary[];
  filter: string | null;
  hasNews: ReturnType<typeof newsReader>;
  selectedId: string | null;
  select: (id: string | null) => void;
}) {
  // Alt+N opens it from anywhere but a menu, a dialog or the terminal (Ctrl+N is the browser's); the pen's own tooltip, which names the shortcut, does not stand in the way.
  // The terminal's keys are the shell's: on macOS, Option+N is a dead key xterm.js lets through.
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.code !== 'KeyN' || e.defaultPrevented || keysTaken()) return;
      if (e.target instanceof Element && e.target.closest('.xterm')) return;
      e.preventDefault();
      openNewTask();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth, openNewTask]);

  // Alt+J / Alt+K open the next / previous Task among the sidebar's Tasks that need the user, in list order, wrapping. With connected instances that list
  // is every machine's (Sidebar.tsx rows: each row keyed by its machine and Task, newest first), and another machine's Task opens on that machine.
  // Not in the terminal (its keys are the shell's), a menu or a dialog, nor in a text field where the key types a character (macOS Option+J is "∆").
  const needsYouIds = useMemo(() => {
    if (!machines) return sidebarTasks(projects, sessions, filter).filter((s) => !readOnly(s) && needsYouNow(s, hasNews)).map((s) => s.id);
    // The machine filter applies while its Project still exists, as in the sidebar.
    const chosen = machineFilter && machines.some((m) => m.id === machineFilter.machine && (m.active ? projects : m.state.projects).some((p) => p.id === machineFilter.project)) ? machineFilter : null;
    return machines.flatMap((m) => {
      if (chosen && m.id !== chosen.machine) return [];
      const own = m.active ? { projects: projects, sessions: sessions } : m.state;
      const unread = m.active ? hasNews : m.hasNews;
      return sidebarTasks(own.projects, own.sessions, chosen ? chosen.project : null).filter((s) => !readOnly(s) && needsYouNow(s, unread)).map((s) => ({ key: encodeEntity(m.id || null, s.id), s }));
    }).sort((a, b) => byActivity(a.s, b.s)).map((entry) => entry.key);
  }, [machines, machineFilter, projects, sessions, filter, hasNews]);
  // Read at the key press, so a press right after the rows commit (before passive effects run) sees them.
  const cycleNeedsYou = useEffectEvent((e: KeyboardEvent) => {
    const current = selectedId && machines ? encodeEntity(here || null, selectedId) : selectedId;
    const next = cycleTask(needsYouIds, current, e.code === 'KeyJ' ? 1 : -1);
    if (!next) return;
    e.preventDefault();
    if (next === current) return;
    const target = machines ? decodeEntity(next) : null;
    if (target && (target.connectionId ?? '') !== here) federation?.go?.(target.connectionId ?? '', `${HASH_PREFIX}${encodeURIComponent(target.entityId)}`);
    else select(target ? target.entityId : next);
  });
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || (e.code !== 'KeyJ' && e.code !== 'KeyK') || e.defaultPrevented || keysTaken()) return;
      const target = e.target instanceof Element ? e.target : null;
      if (target?.closest('.xterm')) return;
      const field = target instanceof HTMLElement && (target.isContentEditable || !!target.closest('input, textarea, select'));
      if (field && e.key.toLowerCase() !== (e.code === 'KeyJ' ? 'j' : 'k')) return;
      cycleNeedsYou(e);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth]);
}
