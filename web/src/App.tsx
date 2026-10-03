import { recentProjection } from './lib/historyState';
import { DetailsProvider } from './components/Details';
import { X } from 'lucide-react';
import { Suspense, addTransitionType, lazy, startTransition, useCallback, useEffect, useEffectEvent, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react';
import { SIGNED_OUT, UPDATE_EVENTS, api, describeError, errorCode, isStatus, newRequestId, onUnauthorized, provider, readOnly, resolveTaskDefaults, taskName, undecidedHolds, type Card, type Interaction, type Meta, type Project, type SessionDetail, type SessionSummary, type SnapshotData, type TaskDefaults, type UpdateData } from './api';
import { initialState, reducer } from './state';
import { AppContext, Dot, Spinner, TranscriptSkeleton, useLate, useMedia } from './components/common';
import { Login } from './components/Login';
import { AddProjectDialog, EditProjectDialog } from './components/Projects';
import { NewTaskPalette } from './components/ProjectPicker';
import { SettingsView } from './components/Settings';
import { RoutinesView } from './components/Routines';
import { Brand, CONNECTION_TEXT, Sidebar, SidebarRail, SidebarToggle, type WorkspaceActions } from './components/Sidebar';
import { cn } from './lib/cn';
import { staleReviewKeys } from './lib/review';
import { createRequest, draftKey, serializeDraft, staleDraftKeys, type DraftAttachment } from './lib/drafts';
import { commandGroups, cycleTask, mostRecentProject, needsYouCount, newsReader, pageTitle, sidebarTasks, tasksOf } from './lib/tasks';
import { handleNotice, setViewing, startNotifications, streamOpened, type Notice } from './lib/notify';
import { pendingRequests } from './lib/board';
import { PlannerContext, PlannerView, usePlannerController } from './components/planner/Planner';
import { PlannerTasks } from './components/planner/context';
import { SettleDialog, type SettleAsk } from './components/planner/SettleDialog';
import { clearArchive, forgetArchive, retainArchive } from './lib/historyArchive';
import { RecentTasks } from './lib/recentTasks';
import { useResizable } from './lib/useResizable';
import { checkDue, decideUpdate } from './lib/update';
import { NewTaskPane, Task } from './components/Task';
import type { FirstMessage } from './components/Composer';
import { TaskActionsContext, type Renaming, type TaskActions } from './components/taskActions';
import { TryModelDialog } from './components/Assist';
import { saveBlob } from './lib/assist';
import { Button } from './components/ui/button';
import { Appear } from './components/ui/appear';
import { AlertDialog, Sheet } from './components/ui/dialog';
import { TooltipProvider } from './components/ui/tooltip';

/** The terminal's content brings xterm.js, so it loads with the first terminal opened. */
const TerminalPanel = lazy(() => import('./components/Terminal'));

type Auth = 'checking' | 'in' | 'out';
type ProjectDialog = { kind: 'add' } | { kind: 'edit'; project: Project } | null;
type TaskDialog = { kind: 'archive' | 'delete' | 'close'; id: string } | null;

/** Below this the sidebar is a drawer (DESIGN.md breakpoints). */
const NARROW = '(max-width: 959px)';
/** From this width the Changes sheet sits beside the column instead of over it. */
const SHEET_INLINE = '(min-width: 1280px)';
/** A touch screen: focusing the composer would raise the keyboard over the conversation just opened. */
const COARSE = '(pointer: coarse)';
const VIEWED_KEY = 'uam.viewed';
/** When the owner last had each Task on screen, by this browser's clock: "Since you left" counts from it. */
const LOOKED_KEY = 'uam.looked';
/** When this browser first ran the Task list's read tracking: a Task never opened here and changed since is unread. */
const VIEWED_SINCE_KEY = 'uam.viewedSince';
const SIDEBAR_KEY = 'uam.sidebar';
const FILTER_KEY = 'uam.projectFilter';
const HASH_PREFIX = '#task=';
/** A wait shorter than this shows nothing new: no loading veil or placeholder, no connection banner. */
const QUIET_MS = 600;
const SETTINGS_HASH = '#settings';
/** The Planner view: `#planner=<project id or unassigned>`. */
const PLANNER_PREFIX = '#planner=';
/** Every Project's routines: `#routines` (or `#routines=all`); one Project's: `#routines=<project id>`. */
const ROUTINES_HASH = '#routines';
const ROUTINES_PREFIX = '#routines=';
const ALL_ROUTINES = 'all';
/** The shell fills the viewport and keeps clear of the notch, rounded corners and home indicator of an installed app (`viewport-fit=cover`). */
/** Typing anywhere (Settings forms, a subagent follow-up) counts as unsent work, like a composer draft. */
function editing(): boolean {
  const el = document.activeElement;
  return el instanceof HTMLElement && (el.isContentEditable || el.matches('textarea, input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit])'));
}

// With the on-screen keyboard open the shell covers only what is visible above it (lib/viewport).
const SHELL = 'grid h-[var(--app-height,100dvh)] mt-[var(--app-top,0px)] overflow-x-clip pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] in-data-keyboard:pb-0';

// Older servers omit `required`; treat absent as true.
const loggedIn = (r: { authenticated: boolean; required?: boolean }) => r.authenticated || r.required === false;

/** Hoisted: the stream's retry timer sits five functions deep, and its updater would be a sixth. */
const increment = (n: number) => n + 1;

/** True while a Base UI popup (menu, dialog, tooltip) is open; app-level Esc handlers stand back. */
export const popupOpen = () => !!document.querySelector('[data-popup]');
/** A menu, dialog or other popup owns the keyboard; an open tooltip (Base UI marks it `data-popup="tooltip"`, with no role) does not. */
const keysTaken = () => !!document.querySelector('[data-popup]:not([data-popup="tooltip"])');

function readJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function hashPlanner(): string | null {
  const h = window.location.hash;
  return h.startsWith(PLANNER_PREFIX) ? decodeURIComponent(h.slice(PLANNER_PREFIX.length)) || null : null;
}

/** The routines the fragment shows: a Project id, `all`, or null for another view. */
function hashRoutines(): string | null {
  const h = window.location.hash;
  if (h === ROUTINES_HASH) return ALL_ROUTINES;
  return h.startsWith(ROUTINES_PREFIX) ? decodeURIComponent(h.slice(ROUTINES_PREFIX.length)) || ALL_ROUTINES : null;
}

function hashSelection(): string | null {
  const h = window.location.hash;
  return h.startsWith(HASH_PREFIX) ? decodeURIComponent(h.slice(HASH_PREFIX.length)) || null : null;
}

export default function App() {
  const [auth, setAuth] = useState<Auth>('checking');
  const [authRequired, setAuthRequired] = useState(true);
  const [state, dispatch] = useReducer(reducer, initialState, (s) => ({ ...s, selectedId: hashSelection() }));
  const [recentTasks] = useState(() => new RecentTasks());
  const confirmedDetail = useRef<SessionDetail | null>(null);
  useLayoutEffect(() => {
    confirmedDetail.current = auth === 'in' && state.connection === 'connected' ? state.detail && recentProjection(state.detail) : null;
  }, [auth, state.connection, state.detail]);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [metaError, setMetaError] = useState<string | null>(null);
  const [streamKey, setStreamKey] = useState(0);
  const narrow = useMedia(NARROW);
  const sheetInline = useMedia(SHEET_INLINE);
  const [notice, setNotice] = useState<string | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  // The terminal docked under the Task view (TerminalDock): the Project its shell started in, or null.
  const [terminalId, setTerminalId] = useState<string | null>(null);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [dialog, setDialog] = useState<ProjectDialog>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [taskDialog, setTaskDialog] = useState<TaskDialog>(null);
  const [taskDialogOpen, setTaskDialogOpen] = useState(false);
  const [renaming, setRenaming] = useState<Renaming | null>(null);
  // The New task palette (the pen, or Alt+N); it chooses the draft's Project.
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [busyTasks, setBusyTasks] = useState<Readonly<Record<string, boolean>>>({});
  const [viewed, setViewed] = useState<Record<string, string>>(() => readJSON(VIEWED_KEY, {}));
  const [sidebarOpen, setSidebarOpen] = useState(() => readJSON<boolean>(SIDEBAR_KEY, true));
  const [filter, setFilter] = useState<string | null>(() => readJSON<string | null>(FILTER_KEY, null));
  const [settingsOpen, setSettingsOpen] = useState(() => window.location.hash === SETTINGS_HASH);
  const [plannerOpen, setPlannerOpen] = useState(() => window.location.hash.startsWith(PLANNER_PREFIX));
  const [routinesFor, setRoutinesFor] = useState<string | null>(hashRoutines);
  // Settle found subtasks the Task holds: the dialog decides each (ADR 0005 §5).
  const [settleAsk, setSettleAsk] = useState<SettleAsk | null>(null);
  // New task opens a draft for a Project on its defaults; nothing exists on the service until its first Send.
  // `tick` refocuses its composer when New task is chosen again.
  const [newTask, setNewTask] = useState<{ projectId: string; defaults: TaskDefaults; tick: number } | null>(null);
  const newTaskTick = useRef(0);
  const aside = useRef<HTMLElement>(null);
  // A Task never opened in this browser counts as read up to the first visit, so a reload (the phone drops pages often) never forgets what finished meanwhile.
  const [loadedAt] = useState(() => {
    const since = readJSON<string | null>(VIEWED_SINCE_KEY, null);
    if (since) return since;
    const now = new Date().toISOString();
    localStorage.setItem(VIEWED_SINCE_KEY, JSON.stringify(now));
    return now;
  });
  // One create per project at a time; the request ID survives a failure so a retry is idempotent.
  const creating = useRef(new Map<string, { id: string; busy: boolean }>());
  // Task whose composer takes focus once its detail arrives (a Task just created, or one chosen
  // from the list on a fine pointer); `selectTick` re-runs the focus when the open Task is chosen again.
  const focusTask = useRef<string | null>(null);
  const [selectTick, setSelectTick] = useState(0);
  // The service version this page loaded with, the time of the last version check, whether the
  // stream has been down since it last opened, and whether a newer version waits for a Reload.
  const loadedVersion = useRef<string | null>(null);
  const lastCheck = useRef(0);
  const wasDown = useRef(false);
  const [updated, setUpdated] = useState(false);

  useEffect(() => {
    onUnauthorized(() => {
      recentTasks.clear();
      confirmedDetail.current = null;
      dispatch({ type: 'reset' });
      setAuthRequired(true);
      setAuth('out');
    });
    api
      .auth()
      .then((r) => {
        setAuthRequired(r.required !== false);
        setAuth(loggedIn(r) ? 'in' : 'out');
      })
      .catch(() => setAuth('out'));
  }, [recentTasks]);

  useEffect(() => { if (auth !== 'in') recentTasks.clear(); }, [auth, recentTasks]);
  // Signed out for any reason (sign-out, a 401, sign-in required): no transcript stays in this browser.
  useEffect(() => { if (auth === 'out') clearArchive(); }, [auth]);

  // Custom models are part of the model lists, so a change to them reloads the catalogs; `metaAttempt` is a Retry after a failure.
  const customModels = JSON.stringify(state.settings.custom_models ?? []);
  const [metaAttempt, setMetaAttempt] = useState(0);
  useEffect(() => {
    if (auth !== 'in') return;
    lastCheck.current = Date.now();
    api
      .meta()
      .then((m) => {
        setMeta(m);
        setMetaError(null);
      })
      .catch((e: unknown) => {
        setMeta(null);
        setMetaError(describeError(e));
      });
  }, [auth, customModels, metaAttempt]);

  /**
   * A redeploy shows as the stream reconnecting, so the version is read again once the stream
   * is back; a page coming back into view (a phone that slept through it) reads it too, at most
   * once a minute. A failed read keeps the providers on screen.
   */
  const checkVersion = useCallback((force: boolean) => {
    const now = Date.now();
    if (!force && !checkDue(lastCheck.current, now)) return;
    lastCheck.current = now;
    api.meta().then(setMeta).catch(() => {});
  }, []);
  useEffect(() => {
    if (auth !== 'in') return;
    const onVisible = () => document.visibilityState === 'visible' && checkVersion(false);
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [auth, checkVersion]);
  // A new version applies itself when nothing would be lost (no draft, no popup); otherwise the
  // strip above the pane offers Reload and the next check tries again.
  useEffect(() => {
    if (!meta) return;
    loadedVersion.current ??= meta.version;
    const decision = decideUpdate(loadedVersion.current, meta.version, { draft: !!document.querySelector('[data-draft]') || editing(), popup: popupOpen() });
    if (decision === 'reload') window.location.reload();
    else if (decision === 'offer') setUpdated(true);
  }, [meta]);

  /**
   * Collapses the sidebar to its icon rail or expands it (wide layout), remembered per browser.
   * Focus on the sidebar or the rail moves to the other's toggle, so a keyboard user is never
   * left on an inert or removed element; focus anywhere else (the composer) is left alone.
   */
  const toggleSidebar = useCallback(() => {
    const next = !sidebarOpen;
    setSidebarOpen(next);
    localStorage.setItem(SIDEBAR_KEY, JSON.stringify(next));
    if (aside.current?.contains(document.activeElement)) requestAnimationFrame(() => document.getElementById(next ? 'sidebar-hide' : 'sidebar-show')?.focus());
  }, [sidebarOpen]);

  // Ctrl/Cmd+B toggles the sidebar, or the drawer on a narrow screen.
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      const modifier = navigator.platform.startsWith('Mac') ? e.metaKey : e.ctrlKey;
      if (!modifier || e.altKey || e.shiftKey || e.key.toLowerCase() !== 'b' || e.defaultPrevented || popupOpen()) return;
      e.preventDefault();
      if (narrow) setDrawerOpen((o) => !o);
      else toggleSidebar();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth, narrow, toggleSidebar]);

  // Start typing in the viewed Task without first clicking its message box.
  // Focus synchronously so the browser inserts the first character normally.
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      if (e.defaultPrevented || e.isComposing || e.metaKey || ((e.ctrlKey || e.altKey) && !e.getModifierState('AltGraph'))) return;
      if (e.key !== 'Dead' && ([...e.key].length !== 1 || !e.key.trim())) return;
      const target = e.target;
      if (!(target instanceof HTMLElement) || target.isContentEditable || target.closest('input, textarea, select, [role="textbox"], [role="combobox"], [role="listbox"], [role="menu"], [role="tree"], [role="grid"], [role="tablist"], [role="slider"], [role="spinbutton"]')) return;
      if (keysTaken() || window.getSelection()?.isCollapsed === false) return;
      const composer = document.getElementById('composer-text');
      if (!(composer instanceof HTMLTextAreaElement) || composer.disabled || composer.readOnly || composer.closest('[inert]') || !composer.getClientRects().length) return;
      composer.focus({ preventScroll: true });
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth]);

  // Esc closes the Changes sheet when no popup owns the key (popups and the drawer handle their own).
  useEffect(() => {
    if (!sheetOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || popupOpen()) return;
      setSheetOpen(false);
      document.getElementById('changes-link')?.focus();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [sheetOpen]);

  // When the owner last looked at each Task: while it is on screen, and as they leave it.
  const markLooked = useCallback((id: string) => {
    try {
      localStorage.setItem(LOOKED_KEY, JSON.stringify({ ...readJSON<Record<string, string>>(LOOKED_KEY, {}), [id]: new Date().toISOString() }));
    } catch {
      // Storage unavailable: no mark, so no "Since you left".
    }
  }, []);

  // The selected task counts as viewed as long as it is on screen: every frame that carries
  // its updated_at marks it, so nothing it does while open shows as unread later. A hidden
  // page shows nothing: what the Task does meanwhile stays unread until the page is back.
  const markViewed = useCallback((id: string, at: string) => {
    if (document.visibilityState !== 'visible') return;
    markLooked(id);
    setViewed((v) => {
      if (v[id] === at) return v;
      const next = { ...v, [id]: at };
      localStorage.setItem(VIEWED_KEY, JSON.stringify(next));
      return next;
    });
  }, [markLooked]);
  // Leaving a Task, hiding the page or closing it ends the look at the Task on screen.
  useEffect(() => {
    const id = state.selectedId;
    if (!id) return;
    const onHide = () => markLooked(id);
    const onVisibility = () => { if (document.hidden) markLooked(id); };
    document.addEventListener('visibilitychange', onVisibility);
    window.addEventListener('pagehide', onHide);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      window.removeEventListener('pagehide', onHide);
      // A hidden page recorded the leave when it hid; it shows nothing since.
      if (!document.hidden) markLooked(id);
    };
  }, [state.selectedId, markLooked]);
  const markShown = useEffectEvent(() => {
    const shown = state.sessions.find((s) => s.id === state.selectedId);
    if (shown) markViewed(shown.id, shown.updated_at);
  });
  useEffect(() => {
    document.addEventListener('visibilitychange', markShown);
    return () => document.removeEventListener('visibilitychange', markShown);
  }, []);

  // One EventSource at a time, scoped to the selected session.
  useEffect(() => {
    if (auth !== 'in') return;
    const es = new EventSource(api.eventsUrl(state.selectedId));
    let alive = true;
    let retry: number | undefined;
    // Opening a stream is a load; a retry after a failure keeps that failure on screen until the snapshot.
    if (!wasDown.current) dispatch({ type: 'connection', status: 'connecting' });
    es.onopen = () => {
      if (!alive) return;
      // The snapshot confirms this connection; opening TCP alone does not.
      if (wasDown.current) {
        wasDown.current = false;
        checkVersion(true);
      }
    };
    es.onerror = () => {
      if (!alive) return;
      wasDown.current = true;
      if (es.readyState !== EventSource.CLOSED) {
        dispatch({ type: 'connection', status: 'reconnecting' });
        return;
      }
      // The browser gave up (non-200 response). A selected Task the service does not know (a stale
      // link, one deleted elsewhere) is dropped with a notice; otherwise re-check auth, then reopen.
      const later = () => {
        if (!alive) return;
        retry = window.setTimeout(() => { if (alive) setStreamKey(increment); }, 5000);
      };
      const reconnect = () => {
        if (!alive) return;
        dispatch({ type: 'connection', status: 'offline' });
        api
          .auth()
          .then((r) => { if (alive) { if (loggedIn(r)) later(); else { recentTasks.clear(); confirmedDetail.current = null; dispatch({ type: 'reset' }); setAuth('out'); } } })
          .catch(later);
      };
      const opened = state.selectedId;
      if (!opened) return reconnect();
      api.session(opened).then(reconnect, (err: unknown) => {
        if (!alive) return;
        if (!isStatus(err, 404)) return reconnect();
        recentTasks.remove(opened);
        forgetArchive(opened);
        dispatch({ type: 'select', id: null });
        setNotice('That task no longer exists.');
      });
    };
    const selected = state.selectedId;
    // Transcript updates wait for the next animation frame and land in one dispatch; any other
    // frame (and a snapshot) flushes them first, so the order on the wire is kept.
    let queue: UpdateData[] = [];
    let frame = 0;
    const flush = () => {
      cancelAnimationFrame(frame);
      frame = 0;
      if (!queue.length) return;
      const data = queue;
      queue = [];
      dispatch({ type: 'updates', data });
    };
    es.addEventListener('snapshot', (e) => {
      if (!alive) return;
      const data = JSON.parse((e as MessageEvent).data) as SnapshotData;
      flush();
      confirmedDetail.current = null;
      recentTasks.confirm(data);
      // Open the Task as soon as its snapshot arrives; a view transition delays readiness.
      dispatch({ type: 'snapshot', data });
      setFilter((current) => {
        if (!current || data.projects.some((p) => p.id === current)) return current;
        localStorage.removeItem(FILTER_KEY);
        return null;
      });
      if (data.session?.id === selected) markViewed(selected, data.session.updated_at);
      // This stream is new to the service: it learns again which Task this tab shows.
      streamOpened();
      // Composer drafts of Tasks that no longer exist go with them.
      try {
        for (const key of staleDraftKeys(Object.keys(localStorage), data.sessions.map((s) => s.id), data.projects.map((p) => p.id))) localStorage.removeItem(key);
        for (const key of staleReviewKeys(Object.keys(localStorage), data.sessions.map((s) => s.id))) localStorage.removeItem(key);
      } catch {
        // Storage unavailable: nothing to sweep.
      }
      // So are their read and last-looked marks.
      const live = new Set(data.sessions.map((s) => s.id));
      try {
        const looked = readJSON<Record<string, string>>(LOOKED_KEY, {});
        if (!Object.keys(looked).every((id) => live.has(id))) localStorage.setItem(LOOKED_KEY, JSON.stringify(Object.fromEntries(Object.entries(looked).filter(([id]) => live.has(id)))));
      } catch {
        // Storage unavailable: nothing to prune.
      }
      setViewed((v) => {
        if (Object.keys(v).every((id) => live.has(id))) return v;
        const next = Object.fromEntries(Object.entries(v).filter(([id]) => live.has(id)));
        try {
          localStorage.setItem(VIEWED_KEY, JSON.stringify(next));
        } catch {
          // Storage unavailable: the marks shrink for this page only.
        }
        return next;
      });
      retainArchive(data.sessions.map((s) => s.id));
    });
    // A Task needs you or finished: a notification, when this browser asked for them (lib/notify.ts).
    es.addEventListener('notify', (e) => {
      if (alive) void handleNotice(JSON.parse((e as MessageEvent).data) as Notice);
    });
    for (const name of UPDATE_EVENTS) {
      es.addEventListener(name, (e) => {
        if (!alive) return;
        const data = { name, ...JSON.parse((e as MessageEvent).data) } as UpdateData;
        recentTasks.invalidate(data);
        if (data.name === 'session_removed') forgetArchive(data.session_id);
        // Invalidations precede React's commit. A click in between must not
        // put the old confirmed reference straight back into the cache.
        const current = confirmedDetail.current;
        if (current && (((data.name === 'history' || data.name === 'items_trimmed' || data.name === 'session_removed') && current.id === data.session_id)
          || (data.name === 'project_removed' && current.project_id === data.project_id))) confirmedDetail.current = null;
        // A hidden tab gets no animation frames: apply at once there.
        if ((data.name === 'delta' || data.name === 'tool_output' || data.name === 'item') && !document.hidden) {
          queue.push(data);
          frame ||= requestAnimationFrame(flush);
          return;
        }
        flush();
        if (data.name === 'session' || data.name === 'session_removed') {
          // Task rows enter, leave and reorder with a view transition (type "sessions"); everything else commits at once.
          startTransition(() => {
            addTransitionType('sessions');
            dispatch({ type: 'update', data });
          });
        } else dispatch({ type: 'update', data });
        if (data.name === 'project_removed') {
          setFilter((current) => {
            if (current !== data.project_id) return current;
            localStorage.removeItem(FILTER_KEY);
            return null;
          });
        }
        if (data.name === 'session' && data.session.id === selected) markViewed(selected, data.session.updated_at);
      });
    }
    return () => {
      alive = false;
      es.close();
      window.clearTimeout(retry);
      // Deltas still queued belong to this stream; the next one starts with a snapshot.
      cancelAnimationFrame(frame);
    };
  }, [auth, state.selectedId, streamKey, markViewed, checkVersion, recentTasks]);

  const hasNews = useMemo(() => newsReader(state.selectedId, viewed, loadedAt), [state.selectedId, viewed, loadedAt]);
  // The owner's last look at the Task just opened, read before this visit marks it: "Since you left" starts there.
  const [opened, setOpened] = useState<{ id: string | null; mark?: string }>({ id: null });
  if (opened.id !== state.selectedId) setOpened({ id: state.selectedId, mark: state.selectedId ? readJSON<Record<string, string>>(LOOKED_KEY, {})[state.selectedId] ?? viewed[state.selectedId] : undefined });

  // Opening a Task reopens the stream: a load that lasts veils the pane with a spinner; only a disconnect that lasts is shown as one.
  const late = useLate(state.connection !== 'connected', QUIET_MS);
  const loading = late && state.connection === 'connecting';
  const connection = late && !loading ? state.connection : 'connected';
  const lateLoad = useLate(!!state.selectedId && !state.detail && !settingsOpen && !plannerOpen && !routinesFor, QUIET_MS);

  // A refresh with the catalogs on screen keeps them on a failure (checkVersion); without them it is a retry of the first read.
  const refreshMeta = useCallback(() => (meta ? checkVersion(true) : setMetaAttempt((n) => n + 1)), [meta, checkVersion]);
  const ctx = useMemo(
    () => ({ meta, metaError, loaded: state.loaded, dispatch, narrow, hasNews, settings: state.settings, usage: state.usage, refreshMeta }),
    [meta, metaError, state.loaded, narrow, hasNews, state.settings, state.usage, refreshMeta],
  );

  // Focus the composer of a Task that was just created or chosen, once its detail is on screen; a read-only Task has nothing to type into.
  const detailId = state.detail?.id;
  const detailLocked = !!state.detail && readOnly(state.detail);
  useEffect(() => {
    if (!detailId || detailId !== focusTask.current) return;
    focusTask.current = null;
    if (!detailLocked) document.getElementById('composer-text')?.focus();
  }, [detailId, detailLocked, selectTick]);

  const logout = useCallback(() => {
    recentTasks.clear();
    confirmedDetail.current = null;
    void api.logout().finally(() => { dispatch({ type: 'reset' }); setAuth('out'); });
  }, [recentTasks]);
  const toggleTerminal = useCallback((projectId: string) => setTerminalId((id) => (id ? null : projectId)), []);
  const closeTerminal = useCallback(() => {
    setTerminalId(null);
    document.getElementById('terminal-link')?.focus();
  }, []);
  const onSheet = useCallback((open: boolean, restoreFocus = true) => {
    setSheetOpen(open);
    if (!open && restoreFocus) document.getElementById('changes-link')?.focus();
  }, []);
  const onSessionUpdate = useCallback((s: SessionSummary) => dispatch({ type: 'upsert_session', session: s }), []);
  const onInteractionUpdate = useCallback((sessionId: string, interaction: Interaction) => dispatch({ type: 'upsert_interaction', sessionId, interaction }), []);

  const openDialog = useCallback((d: Exclude<ProjectDialog, null>) => {
    setDialog(d);
    setDialogOpen(true);
  }, []);
  const openTaskDialog = useCallback((d: Exclude<TaskDialog, null>) => {
    setTaskDialog(d);
    setTaskDialogOpen(true);
  }, []);

  const select = useCallback((id: string | null) => {
    // Choosing a Task is an invitation to type: its composer takes focus, except on a touch screen, where a keyboard would rise over the conversation.
    if (id && !window.matchMedia(COARSE).matches) {
      focusTask.current = id;
      setSelectTick((t) => t + 1);
    }
    // Capture only on leaving a confirmed task, not on every streamed token.
    const current = confirmedDetail.current;
    if (current && current.id !== id) recentTasks.remember(current);
    dispatch({ type: 'select', id, cached: id ? recentTasks.get(id) : undefined });
    setNotice(null);
    setSheetOpen(false);
    setDrawerOpen(false);
    setSettingsOpen(false);
    setPlannerOpen(false);
    setRoutinesFor(null);
    setNewTask(null);
  }, [recentTasks]);

  /** The Planner view in the main pane (like Settings, it keeps the selected Task behind it). */
  const showPlanner = useCallback(() => {
    setPlannerOpen(true);
    setSettingsOpen(false);
    setRoutinesFor(null);
    setNewTask(null);
    setDrawerOpen(false);
    setSheetOpen(false);
  }, []);
  const openTask = useCallback((id: string) => select(id), [select]);
  /** Settings in the main pane, from the signed-out banner (like the sidebar's gear, it keeps the selected Task behind it). */
  const showSettings = useCallback(() => {
    setSettingsOpen(true);
    setPlannerOpen(false);
    setRoutinesFor(null);
    setNewTask(null);
    setDrawerOpen(false);
    setSheetOpen(false);
  }, []);
  // Providers whose runtime is signed out: a banner over the pane says so until one signs in.
  const signedOut = useMemo(() => (meta?.providers ?? []).filter((p) => p.signed_out), [meta]);
  /** Routines in the main pane, one Project's or `all` (like Settings, it keeps the selected Task behind it). */
  const showRoutines = useCallback((projectId: string) => {
    setRoutinesFor(projectId);
    setSettingsOpen(false);
    setPlannerOpen(false);
    setNewTask(null);
    setDrawerOpen(false);
    setSheetOpen(false);
  }, []);
  // Only `true` shows the planner: `false` leaves its Settings switch, and a service that does not know the setting (undefined) shows none of it.
  const plannerOn = state.settings.planner === true && state.loaded;
  // A `#planner=` link on a service without the planner on lands on the usual view.
  if (state.loaded && !plannerOn && plannerOpen) setPlannerOpen(false);
  const plannerShown = plannerOpen && plannerOn;
  const planner = usePlannerController({
    enabled: plannerOn,
    boards: state.boards,
    jobs: state.boardJobs,
    projects: state.projects,
    dispatch,
    onShowPlanner: showPlanner,
    onOpenTask: openTask,
    initialProject: hashPlanner(),
  });
  const plannerTasks = useMemo(() => ({ sessions: state.sessions, openTask }), [state.sessions, openTask]);
  const plannerProject = planner.value.ui.project;
  const setPlannerUi = planner.value.setUi;

  useEffect(() => {
    if (auth === 'in') return startNotifications();
  }, [auth]);
  // The Task on screen is not announced while this page is visible; Settings, the planner and Routines cover it.
  useEffect(() => {
    if (auth === 'in') setViewing(settingsOpen || plannerOpen || routinesFor ? null : state.selectedId);
  }, [auth, state.selectedId, settingsOpen, plannerOpen, routinesFor]);

  // Keep the view in the URL fragment so a reload lands on it: `#settings`, `#planner=…`, else the selected task.
  useEffect(() => {
    const id = state.selectedId;
    let next = '';
    if (settingsOpen) next = SETTINGS_HASH;
    else if (plannerOpen) next = `${PLANNER_PREFIX}${encodeURIComponent(plannerProject ?? '')}`;
    else if (routinesFor) next = routinesFor === ALL_ROUTINES ? ROUTINES_HASH : `${ROUTINES_PREFIX}${encodeURIComponent(routinesFor)}`;
    else if (id) next = `${HASH_PREFIX}${encodeURIComponent(id)}`;
    if (window.location.hash !== next) history.replaceState(null, '', `${window.location.pathname}${window.location.search}${next}`);
  }, [state.selectedId, settingsOpen, plannerOpen, plannerProject, routinesFor]);
  // A `#task=` or `#routines` fragment the user navigates to (back/forward, a pasted URL) shows that
  // Task or those routines; the write above uses replaceState, which fires no hashchange.
  useEffect(() => {
    const onHash = () => {
      const routines = hashRoutines();
      const id = hashSelection();
      if (routines) showRoutines(routines);
      else if (id) select(id);
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, [select, showRoutines]);

  /** New task: a draft for the Project on the Task defaults of Settings, with its composer focused; no request until its first Send. */
  const startTask = useCallback(
    (projectId: string) => {
      const project = state.projects.find((p) => p.id === projectId);
      const defaults = project && resolveTaskDefaults(meta, state.settings.task_defaults, state.settings.hidden_models);
      if (!defaults) {
        setNotice(`Could not start a task: ${meta ? 'No provider is available.' : 'The provider list has not loaded yet.'}`);
        return;
      }
      select(null);
      setNewTask({ projectId, defaults, tick: ++newTaskTick.current });
    },
    [state.projects, state.settings.task_defaults, state.settings.hidden_models, meta, select],
  );
  const draftTick = newTask?.tick;
  useEffect(() => {
    if (draftTick !== undefined && !window.matchMedia(COARSE).matches) document.getElementById('composer-text')?.focus();
  }, [draftTick]);

  /** New task: the palette chooses the Project, unless there is only one. */
  const projectCount = state.projects.length;
  const onlyProject = projectCount === 1 ? state.projects[0].id : null;
  const openNewTask = useCallback(() => {
    if (onlyProject) startTask(onlyProject);
    else if (projectCount > 0) setPaletteOpen(true);
  }, [onlyProject, projectCount, startTask]);
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

  // Alt+J / Alt+K open the next / previous Task in the sidebar's Needs you group, wrapping. Not in the terminal (its keys are the
  // shell's), a menu or a dialog, nor in a text field where the key types a character (macOS Option+J is "∆").
  const needsYouIds = useMemo(
    () => commandGroups(sidebarTasks(state.projects, state.sessions, filter).filter((s) => !readOnly(s)), hasNews).you.map((s) => s.id),
    [state.projects, state.sessions, filter, hasNews],
  );
  const selectedId = state.selectedId;
  // Read at the key press, so a press right after the rows commit (before passive effects run) sees them.
  const cycleNeedsYou = useEffectEvent((e: KeyboardEvent) => {
    const next = cycleTask(needsYouIds, selectedId, e.code === 'KeyJ' ? 1 : -1);
    if (!next) return;
    e.preventDefault();
    if (next !== selectedId) select(next);
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

  /**
   * A new Task's first Send: create the Task with the chosen settings, upload the held
   * attachments to it, send the message, then open it. A failed create throws, so the draft
   * keeps the text and says why; once the Task exists it opens whatever happens next, and a
   * message that was not accepted waits in its composer with the reason on the notice line.
   */
  const createTask = useCallback(
    async (projectId: string, first: FirstMessage) => {
      const entry = creating.current.get(projectId) ?? { id: newRequestId(), busy: false };
      if (entry.busy) throw new Error('This task is already being created.');
      creating.current.set(projectId, { ...entry, busy: true });
      let s: SessionSummary;
      try {
        const info = provider(meta, first.settings.provider);
        if (info && !info.available) throw new Error(info.signed_out && info.reason ? info.reason : `${info.display_name} is unavailable: ${info.reason || 'not installed'}`);
        s = await api.createSession(createRequest(projectId, first.settings, entry.id));
      } catch (e) {
        creating.current.set(projectId, { ...entry, busy: false });
        if (errorCode(e) === SIGNED_OUT) refreshMeta();
        throw new Error(`Could not start the task: ${describeError(e)}`, { cause: e });
      }
      creating.current.delete(projectId);
      startTransition(() => {
        addTransitionType('sessions');
        dispatch({ type: 'upsert_session', session: s });
      });
      const sent: DraftAttachment[] = [];
      let failed = '';
      try {
        for (const u of first.uploads) {
          const a = await api.upload(s.id, u.file, () => {}).done;
          sent.push({ id: a.id, name: a.name, size: a.size ?? u.file.size, kind: u.kind });
        }
        const extras = { ...(first.files.length ? { files: first.files } : {}), ...(sent.length ? { attachments: sent.map((a) => a.id) } : {}) };
        const sub = await api.prompt(s.id, first.text, newRequestId(), 'send', extras);
        if (sub.status !== 'accepted' && sub.status !== 'queued') failed = sub.error || `the message was ${sub.status}`;
      } catch (e) {
        failed = describeError(e);
      }
      if (failed) {
        try {
          const raw = serializeDraft({ text: first.text, files: first.files, attachments: sent });
          if (raw) localStorage.setItem(draftKey(s.id), raw);
        } catch {
          // Storage unavailable: the notice still says what happened.
        }
      }
      select(s.id);
      if (failed) setNotice(`The task was created, but its first message was not sent: ${failed}. The message is in its composer.`);
    },
    [meta, select, refreshMeta],
  );

  /** Runs one lifecycle request; the result is dispatched, a failure becomes the notice line. */
  const runTask = useCallback(async (id: string, op: () => Promise<SessionSummary | void>, verb: string) => {
    setBusyTasks((b) => ({ ...b, [id]: true }));
    setNotice(null);
    try {
      const s = await op();
      if (s) dispatch({ type: 'upsert_session', session: s });
    } catch (e) {
      setNotice(`Could not ${verb}: ${describeError(e)}`);
      throw e;
    } finally {
      setBusyTasks(({ [id]: _, ...rest }) => rest);
    }
  }, []);

  /** Run again and Try with another model: the new Task opens; a refusal becomes the notice line. */
  const rerun = useCallback((id: string, model?: string) => runTask(id, async () => {
    const s = await api.rerun(id, { model, request_id: newRequestId() });
    dispatch({ type: 'upsert_session', session: s });
    select(s.id);
  }, 'run the task again'), [runTask, select]);
  const [tryModel, setTryModel] = useState<string | null>(null);

  const taskActions: TaskActions = useMemo(
    () => ({
      select: (id) => select(id),
      startRename: (id, place) => setRenaming({ id, place }),
      renaming,
      cancelRename: () => setRenaming(null),
      rename: async (id, name) => {
        setRenaming(null);
        const current = state.sessions.find((s) => s.id === id);
        if (!current || current.name === name) return;
        await runTask(id, () => api.rename(id, name), 'rename the task').catch(() => {});
      },
      // With the planner on, a Task holding subtasks answers 409 holds_undecided: the Settle dialog decides each, then settles.
      settle: (id) =>
        void runTask(id, async () => {
          if (!plannerOn) return api.stage(id, 'settle');
          try {
            return await api.settle(id);
          } catch (e) {
            const held = undecidedHolds(e);
            if (!held) throw e;
            const task = state.sessions.find((s) => s.id === id);
            setSettleAsk({
              taskName: task ? `“${taskName(task) || 'New task'}”` : 'this task',
              cards: held as Card[],
              settle: async (holds) => {
                const s = await api.settle(id, holds);
                dispatch({ type: 'upsert_session', session: s });
              },
            });
          }
        }, 'settle the task').catch(() => {}),
      reopen: (id) => void runTask(id, () => api.stage(id, 'reopen'), 'reopen the task').catch(() => {}),
      archive: (id) => openTaskDialog({ kind: 'archive', id }),
      remove: (id) => openTaskDialog({ kind: 'delete', id }),
      close: (id) => openTaskDialog({ kind: 'close', id }),
      busy: busyTasks,
      runAgain: (id) => void rerun(id).catch(() => {}),
      tryModel: setTryModel,
      exportMarkdown: (id) => void runTask(id, async () => { const f = await api.exportMarkdown(id); saveBlob(f.blob, f.name); }, 'export the task').catch(() => {}),
    }),
    [renaming, busyTasks, select, runTask, state.sessions, openTaskDialog, plannerOn, rerun],
  );

  const actions: WorkspaceActions = useMemo(
    () => ({
      onNewTask: openNewTask,
      onAddProject: () => openDialog({ kind: 'add' }),
      onEditProject: (project) => openDialog({ kind: 'edit', project }),
      onRoutines: (project) => showRoutines(project.id),
      routinesOpen: !!routinesFor && !settingsOpen,
      onAllRoutines: () => (routinesFor && !settingsOpen ? setRoutinesFor(null) : showRoutines(ALL_ROUTINES)),
      filter,
      onFilter: (id) => {
        setFilter(id);
        localStorage.setItem(FILTER_KEY, JSON.stringify(id));
      },
      sidebarOpen: narrow ? drawerOpen : sidebarOpen,
      onToggleSidebar: () => (narrow ? setDrawerOpen((o) => !o) : toggleSidebar()),
      settingsOpen,
      onSettings: () => {
        setSettingsOpen((o) => !o);
        setPlannerOpen(false);
        setRoutinesFor(null);
        setDrawerOpen(false);
        setNewTask(null);
      },
      planner: plannerOn
        ? {
            open: plannerOpen && !settingsOpen,
            onOpen: (projectId) => {
              if (projectId) setPlannerUi({ project: projectId, selected: null, epic: null, panel: null });
              if (plannerOpen && !settingsOpen && !projectId) setPlannerOpen(false);
              else showPlanner();
            },
          }
        : undefined,
    }),
    [filter, narrow, drawerOpen, sidebarOpen, settingsOpen, openNewTask, openDialog, toggleSidebar, plannerOn, plannerOpen, setPlannerUi, showPlanner, showRoutines, routinesFor],
  );

  const selected = state.sessions.find((s) => s.id === state.selectedId) ?? null;

  // The tab title and the installed app's badge carry how many Tasks wait for the user; the title names the open Task.
  // Pending planner requests need the owner too (ADR 0005 §10): they fold into the same count.
  const needsYouTasks = useMemo(() => needsYouCount(state.sessions, hasNews), [state.sessions, hasNews]);
  const attention = needsYouTasks + (plannerOn ? pendingRequests(state.boards) : 0);
  // The title names what the pane shows, in the pane's own order; a new or untitled Task shows as "New task". Only real Tasks count as needing you.
  let shownName: string | null = null;
  if (settingsOpen) shownName = 'Settings';
  else if (plannerShown) shownName = 'Planner';
  else if (routinesFor) shownName = 'Routines';
  else if (newTask) shownName = '';
  else if (selected) shownName = taskName(selected);
  useEffect(() => {
    document.title = pageTitle(attention, shownName);
    // The badge is this page's count while it is visible; otherwise the service worker sets the service's count with each push (public/sw.js).
    const badge = () => {
      if (document.visibilityState !== 'visible') return;
      if (attention > 0) navigator.setAppBadge?.(attention).catch(() => {});
      else navigator.clearAppBadge?.().catch(() => {});
    };
    badge();
    document.addEventListener('visibilitychange', badge);
    return () => document.removeEventListener('visibilitychange', badge);
  }, [attention, shownName]);

  if (auth === 'checking') {
    return (
      <main className="grid h-dvh place-items-center text-caption text-muted" aria-busy="true">
        <Brand className="animate-pulse-dot" />
      </main>
    );
  }
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />;

  // A cached selected task stays visible through a slow refresh. An unrelated previous task yields to the skeleton after the quiet period.
  let shown: SessionDetail | null = null;
  if (state.detail && selected) shown = state.detail;
  else if (state.selectedId && selected && (state.previousCached || !lateLoad)) shown = state.previous;
  const stale = !settingsOpen && !plannerShown && !routinesFor && !newTask && !!shown && shown !== state.detail;
  const project = shown ? state.projects.find((p) => p.id === shown.project_id) : undefined;
  // Turning Settings → Terminal off ends every shell on the service, and a removed Project takes its shell: the dock leaves.
  const terminalProject = terminalId && state.settings.terminal ? state.projects.find((p) => p.id === terminalId) : undefined;
  if (terminalId && !terminalProject) setTerminalId(null);
  const dialogTask = taskDialog ? state.sessions.find((s) => s.id === taskDialog.id) : undefined;
  const dialogTaskName = dialogTask ? `“${dialogTask.name || dialogTask.title || 'this task'}”` : 'this task';
  const newTaskProject = newTask ? state.projects.find((p) => p.id === newTask.projectId) : undefined;
  // A removed Project, or a `#routines=` link to none, lands on the usual view.
  const routinesKnown = routinesFor === ALL_ROUTINES || state.projects.some((p) => p.id === routinesFor);
  const routinesShown = !!routinesFor && !settingsOpen && !plannerShown && state.loaded && routinesKnown;
  if (routinesFor && state.loaded && !routinesKnown) setRoutinesFor(null);

  function showProject(id: string) {
    setFilter(id);
    localStorage.setItem(FILTER_KEY, JSON.stringify(id));
    select(null);
  }

  async function confirmTaskDialog() {
    if (!taskDialog) return;
    const { kind, id } = taskDialog;
    try {
      if (kind === 'archive') await runTask(id, () => api.stage(id, 'archive'), 'archive the task');
      else if (kind === 'close') await runTask(id, () => api.close(id), 'close the conversation');
      else {
        await runTask(id, () => api.deleteSession(id), 'delete the task');
        recentTasks.remove(id);
        forgetArchive(id);
        if (confirmedDetail.current?.id === id) confirmedDetail.current = null;
        startTransition(() => {
          addTransitionType('sessions');
          dispatch({ type: 'remove_session', id });
        });
      }
    } catch {
      // Reported on the notice line.
    }
    setTaskDialogOpen(false);
  }

  const sidebar = (
    <Sidebar
      loaded={state.loaded}
      projects={state.projects}
      sessions={state.sessions}
      selectedId={state.selectedId}
      actions={actions}
      authRequired={authRequired}
      onLogout={logout}
      connection={connection}
      version={meta?.version}
    />
  );

  // On a narrow screen the main pane's header starts with the drawer toggle; a collapsed wide sidebar keeps its toggle on the rail.
  const leading = narrow ? <SidebarToggle id="sidebar-show" size="icon-md" open={drawerOpen} count={needsYouTasks} onToggle={() => setDrawerOpen((o) => !o)} className="-ml-1" /> : null;
  // The sidebar's column, animated between its width and the rail's.
  const columns = sidebarOpen ? 'grid-cols-[var(--spacing-rail)_minmax(0,1fr)]' : 'grid-cols-[var(--spacing-rail-collapsed)_minmax(0,1fr)]';

  let pane: React.ReactNode;
  // The Board the planner opens on: the filtered Project when it has git, else the most recently active git Project.
  const gitProjects = state.projects.filter((p) => !p.no_git);
  const defaultBoard = (filter && gitProjects.some((p) => p.id === filter) ? filter : mostRecentProject(gitProjects, state.sessions, state.selectedId)?.id) ?? null;
  if (settingsOpen) {
    pane = <SettingsView leading={leading} onClose={() => setSettingsOpen(false)} />;
  } else if (plannerShown) {
    pane = <PlannerView leading={leading} inline={sheetInline} defaultProject={defaultBoard} onClose={() => setPlannerOpen(false)} />;
  } else if (routinesShown) {
    pane = <RoutinesView leading={leading} projects={state.projects} scope={routinesFor === ALL_ROUTINES ? null : routinesFor} onScope={(id) => setRoutinesFor(id ?? ALL_ROUTINES)} sessions={state.sessions} onOpenTask={openTask} onClose={() => setRoutinesFor(null)} />;
  } else if (newTask && newTaskProject) {
    pane = <NewTaskPane key={newTask.projectId} project={newTaskProject} defaults={newTask.defaults} onSend={createTask} leading={leading} />;
  } else if (shown) {
    pane = (
      <DetailsProvider key={shown.id} session={shown} active={!stale && state.connection === 'connected'} generation={state.detailGeneration} versions={state.bodyVersions} onAuthLost={() => { recentTasks.clear(); confirmedDetail.current = null; dispatch({ type: 'reset' }); setAuth('out'); }}>
      <Task
        key={shown.id}
        session={shown}
        project={project}
        agents={state.agents}
        agentSteps={state.agentSteps}
        snapshotSeq={state.snapshotSeq}
        historyGeneration={state.detailGeneration}
        active={!stale}
        historyRequest={state.historyRequest}
        historyItemSeq={state.historyItemSeq}
        onHistoryReset={() => { recentTasks.remove(shown.id); confirmedDetail.current = null; setStreamKey(k => k + 1); }}
        sheetOpen={sheetOpen}
        sidePanelInline={sheetInline}
        onSheet={onSheet}
        terminalOpen={!!terminalProject}
        onTerminal={toggleTerminal}
        onSessionUpdate={onSessionUpdate}
        onInteractionUpdate={onInteractionUpdate}
        leading={leading}
        spawnedBy={taskName(state.sessions.find((s) => s.id === shown.spawned_by) ?? { name: '', title: '' })}
        since={opened.id === shown.id ? opened.mark : undefined}
        rerunOf={taskName(state.sessions.find((s) => s.id === shown.rerun_of) ?? { name: '', title: '' })}
      />
      </DetailsProvider>
    );
  } else if (state.selectedId && state.snapshotSeq >= 0 && !selected) {
    pane = (
      <EmptyPane leading={leading} connection={connection}>
        <h1 className="text-display-md">This task no longer exists.</h1>
        <p className="text-ui text-muted">It was deleted, or its project was removed.</p>
        <Button variant="secondary" onClick={() => select(null)}>
          Back to projects
        </Button>
      </EmptyPane>
    );
  } else if (state.selectedId || !state.loaded) {
    // The Task's detail, or the first snapshot, is on its way: a skeleton, never the placeholder that says there is nothing.
    pane = <LoadingPane leading={leading} />;
  } else {
    // A quiet placeholder (issue #185): New task and Add project live in the sidebar.
    pane = (
      <EmptyPane leading={leading} connection={connection}>
        <Brand className="[&_svg]:size-9 [&>span]:text-display-md opacity-80" />
        <p className="text-ui text-muted">{state.projects.length > 0 ? 'Open a task from the sidebar, or start a new one there.' : 'Add a project in the sidebar to begin.'}</p>
      </EmptyPane>
    );
  }

  return (
    <AppContext.Provider value={ctx}>
      <PlannerContext.Provider value={planner.value}>
      <PlannerTasks.Provider value={plannerTasks}>
      <TaskActionsContext.Provider value={taskActions}>
        <TooltipProvider delay={400} closeDelay={0}>
          <div
            className={cn(
              SHELL,
              narrow ? 'grid-cols-1' : cn('transition-[grid-template-columns] duration-240 ease-app', columns),
            )}
          >
            {/* The column animates to the collapsed rail's width; the sidebar keeps its width inside so nothing reflows on the way, and is inert once collapsed, under the rail. */}
            {!narrow && (
              <aside ref={aside} className="rail-edge relative min-h-0 overflow-clip">
                <div className="h-full w-rail" inert={!sidebarOpen} aria-hidden={!sidebarOpen}>{sidebar}</div>
                {!sidebarOpen && (
                  <div className="absolute inset-y-0 left-0 animate-fade-in">
                    <SidebarRail projects={state.projects} actions={actions} connection={connection} count={needsYouTasks} />
                  </div>
                )}
              </aside>
            )}
            {narrow && (
              <Sheet open={drawerOpen} onOpenChange={setDrawerOpen} side="left" label="Projects" className="w-[360px]">
                {sidebar}
              </Sheet>
            )}
            <main className="relative flex min-h-0 min-w-0 flex-col bg-canvas">
              {connection !== 'connected' && (
                <output className={cn('flex items-center gap-2 px-4 py-1.5 text-caption animate-fade-in', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
                  <Dot tone={connection === 'offline' ? 'error' : 'warning'} pulse />
                  {CONNECTION_TEXT[connection]}
                </output>
              )}
              {updated && (
                <output className="flex items-center gap-2 bg-surface px-4 py-1 text-caption text-body animate-fade-in">
                  <Dot tone="accent" />
                  <span className="flex-1">UAM was updated.</span>
                  <Button size="sm" variant="secondary" onClick={() => window.location.reload()}>
                    Reload
                  </Button>
                </output>
              )}
              {signedOut.map((p) => (
                <output key={p.name} className="flex items-center gap-2 bg-warning-wash px-4 py-1 text-caption text-warning animate-fade-in">
                  <Dot tone="warning" />
                  <span className="flex-1">{p.reason || `${p.display_name} is signed out. Sign in in Settings.`}</span>
                  {!settingsOpen && (
                    <Button size="sm" variant="secondary" onClick={showSettings}>
                      Open Settings
                    </Button>
                  )}
                </output>
              ))}
              {notice && (
                <p className="flex items-center gap-2 bg-error-wash px-4 py-1.5 text-caption text-error animate-fade-in" role="alert">
                  <span className="flex-1">{notice}</span>
                  <Button size="icon" variant="ghost" aria-label="Dismiss" className="text-error hover:bg-error-wash hover:text-error" onClick={() => setNotice(null)}>
                    <X />
                  </Button>
                </p>
              )}
              {stale && state.previousCached && (
                <output className="flex items-center gap-2 bg-surface px-4 py-1 text-caption text-muted">
                  <Dot tone="accent" pulse /> Refreshing task…
                </output>
              )}
              <div className="relative flex min-h-0 flex-1 flex-col">
                {/* A cached page is presentation only; actions and typing wait for confirmation. */}
                <div className="flex min-h-0 flex-1 flex-col" inert={stale} aria-busy={stale || undefined}>
                  {pane}
                </div>
                {/* Settings, the planner, routines and a new Task do not wait on the stream. */}
                <LoadingVeil show={loading && !settingsOpen && !plannerShown && !routinesShown && !newTask} />
              </div>
              {terminalProject && <TerminalDock key={terminalProject.id} project={terminalProject} onClose={closeTerminal} />}
            </main>

            <NewTaskPalette open={paletteOpen} onOpenChange={setPaletteOpen} projects={state.projects} sessions={state.sessions} selectedId={state.selectedId} filter={filter} onPick={startTask} />
            {dialog?.kind === 'add' && (
              <AddProjectDialog
                open={dialogOpen}
                onClose={() => setDialogOpen(false)}
                onClosed={() => setDialog(null)}
                onAdded={(p) => {
                  dispatch({ type: 'upsert_project', project: p });
                  select(null);
                }}
                onExisting={showProject}
              />
            )}
            {dialog?.kind === 'edit' && (
              <EditProjectDialog
                open={dialogOpen}
                onClose={() => setDialogOpen(false)}
                onClosed={() => setDialog(null)}
                project={dialog.project}
                tasks={tasksOf(state.sessions, dialog.project.id)}
                onRoutines={() => showRoutines(dialog.project.id)}
                onUpdated={(p) => dispatch({ type: 'upsert_project', project: p })}
                onRemoved={(id) => {
                  recentTasks.removeProject(id);
                  if (confirmedDetail.current?.project_id === id) confirmedDetail.current = null;
                  dispatch({ type: 'remove_project', id });
                }}
              />
            )}

            <AlertDialog
              open={taskDialogOpen && taskDialog?.kind === 'archive'}
              onOpenChange={(o) => !o && setTaskDialogOpen(false)}
              onClosed={() => setTaskDialog(null)}
              title="Archive this task?"
              description="Archiving makes the task permanently read-only. It stays in the sidebar's Archived shelf with its full conversation. It cannot be reopened."
              confirmLabel="Archive task"
              danger={false}
              busy={!!taskDialog && !!busyTasks[taskDialog.id]}
              onConfirm={() => void confirmTaskDialog()}
            />
            <AlertDialog
              open={taskDialogOpen && taskDialog?.kind === 'delete'}
              onOpenChange={(o) => !o && setTaskDialogOpen(false)}
              onClosed={() => setTaskDialog(null)}
              title={`Delete ${dialogTaskName}?`}
              description="This removes the task record from UAM. The provider conversation on the host is untouched."
              confirmLabel="Delete task"
              busy={!!taskDialog && !!busyTasks[taskDialog.id]}
              onConfirm={() => void confirmTaskDialog()}
            />
            <AlertDialog
              open={taskDialogOpen && taskDialog?.kind === 'close'}
              onOpenChange={(o) => !o && setTaskDialogOpen(false)}
              onClosed={() => setTaskDialog(null)}
              title="Close this conversation?"
              description="Closing disconnects the provider conversation. The task and its conversation ID are kept, so nothing is deleted; sending another prompt reopens the same conversation. To interrupt the current turn without closing, use Stop instead."
              confirmLabel="Close conversation"
              danger={false}
              busy={!!taskDialog && !!busyTasks[taskDialog.id]}
              onConfirm={() => void confirmTaskDialog()}
            />
            <SettleDialog ask={settleAsk} onClose={() => setSettleAsk(null)} />
            <TryModelDialog session={state.sessions.find((s) => s.id === tryModel) ?? null} onClose={() => setTryModel(null)} onRun={(model) => rerun(tryModel ?? '', model)} />
          </div>
        </TooltipProvider>
      </TaskActionsContext.Provider>
      </PlannerTasks.Provider>
      </PlannerContext.Provider>
    </AppContext.Provider>
  );
}

/** The header of the non-Task views while the sidebar is a drawer (narrow): its toggle, the brand, the connection. */
function PaneHeader({ leading, connection }: Readonly<{ leading: React.ReactNode; connection?: keyof typeof CONNECTION_TEXT }>) {
  return (
    <header className="pane-header flex h-header shrink-0 items-center gap-2 px-3">
      {leading}
      <span className="text-title font-semibold text-ink">UAM</span>
      <span className="flex-1" />
      {connection && connection !== 'connected' && (
        <output className="flex items-center gap-1.5 text-caption text-warning" title={CONNECTION_TEXT[connection]}>
          <Dot tone={connection === 'offline' ? 'error' : 'warning'} pulse />
          <span className="sr-only">{CONNECTION_TEXT[connection]}</span>
        </output>
      )}
    </header>
  );
}

function EmptyPane({ leading, connection, children }: Readonly<{ leading: React.ReactNode; connection: keyof typeof CONNECTION_TEXT; children: React.ReactNode }>) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {leading && <PaneHeader leading={leading} connection={connection} />}
      <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 pb-16 text-center animate-rise">{children}</div>
    </div>
  );
}

/**
 * While the stream (re)opens past the quiet period: a `canvas` veil over the pane below its header
 * that takes the pointer, with a spinner at its centre. It fades both ways (Appear, without its scale); the header stays usable.
 */
/**
 * Settings → Terminal's shell, docked under the Task view as part of the layout (DESIGN.md Terminal):
 * the Task view gives it the height, nothing is covered. Its top edge drags that height. It stays
 * across Task switches and ends with Close, which ends the shell.
 */
function TerminalDock({ project, onClose }: Readonly<{ project: Project; onClose: () => void }>) {
  const { panelRef, handleProps } = useResizable('terminal-h', 320, 160, 'y');
  return (
    <section ref={panelRef} aria-label="Terminal" className="relative flex h-[var(--panel-h,320px)] shrink-0 flex-col bg-canvas">
      <div {...handleProps} className="absolute inset-x-0 -top-1 z-10 flex h-2 cursor-row-resize items-center outline-hidden focus-visible:outline-2 focus-visible:outline-focus" title="Drag to resize · double-click to reset">
        <div className="fade-rule w-full" />
      </div>
      <Suspense fallback={null}>
        <TerminalPanel project={project} onClose={onClose} />
      </Suspense>
    </section>
  );
}

function LoadingVeil({ show }: Readonly<{ show: boolean }>) {
  return (
    <Appear show={show} className="absolute inset-x-0 top-header bottom-0 z-20 scale-100 items-center justify-center bg-canvas/70">
      <output className="flex items-center gap-2 rounded-full bg-canvas px-3 py-1.5 text-caption text-muted">
        <Spinner />
        Loading…
      </output>
    </Appear>
  );
}

/** While the selected Task's detail (or the first snapshot) is on its way and nothing was on screen before: the header holds its height over a transcript-shaped skeleton. */
function LoadingPane({ leading }: Readonly<{ leading: React.ReactNode }>) {
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-busy="true" aria-label="Loading conversation">
      <header className="pane-header flex h-header shrink-0 items-center gap-2 px-3">{leading}</header>
      <TranscriptSkeleton />
    </div>
  );
}
