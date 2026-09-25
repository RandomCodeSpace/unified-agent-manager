import { X } from 'lucide-react';
import { ViewTransition, addTransitionType, startTransition, useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { UPDATE_EVENTS, api, describeError, newRequestId, onUnauthorized, provider, readOnly, resolveTaskDefaults, taskName, type Interaction, type Meta, type Project, type SessionSummary, type SnapshotData, type TaskDefaults, type UpdateData } from './api';
import { initialState, reducer } from './state';
import { AppContext, Dot, Loading, useLate, useMedia } from './components/common';
import { Login } from './components/Login';
import { AddProjectDialog, EditProjectDialog } from './components/Projects';
import { NewTaskPalette } from './components/ProjectPicker';
import { SettingsView } from './components/Settings';
import { Brand, CONNECTION_TEXT, Sidebar, SidebarToggle, type WorkspaceActions } from './components/Sidebar';
import { cn } from './lib/cn';
import { createRequest, draftKey, serializeDraft, staleDraftKeys, type DraftAttachment } from './lib/drafts';
import { needsYouCount, pageTitle, tasksOf } from './lib/tasks';
import { checkDue, decideUpdate } from './lib/update';
import { NewTaskPane, Task } from './components/Task';
import type { FirstMessage } from './components/Composer';
import { TaskActionsContext, type Renaming, type TaskActions } from './components/taskActions';
import { Button } from './components/ui/button';
import { AlertDialog, Sheet } from './components/ui/dialog';
import { TooltipProvider } from './components/ui/tooltip';

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
const SIDEBAR_KEY = 'uam.sidebar';
const FILTER_KEY = 'uam.projectFilter';
const HASH_PREFIX = '#task=';
/** A wait shorter than this shows nothing new: no "Connecting…", no loading placeholder. */
const QUIET_MS = 600;
const SETTINGS_HASH = '#settings';
/** The shell fills the viewport and keeps clear of the notch, rounded corners and home indicator of an installed app (`viewport-fit=cover`). */
/** Typing anywhere (Settings forms, a subagent follow-up) counts as unsent work, like a composer draft. */
function editing(): boolean {
  const el = document.activeElement;
  return el instanceof HTMLElement && (el.isContentEditable || el.matches('textarea, input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=submit])'));
}

const SHELL = 'grid h-dvh overflow-x-clip pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)]';

// Older servers omit `required`; treat absent as true.
const loggedIn = (r: { authenticated: boolean; required?: boolean }) => r.authenticated || r.required === false;

/** True while a Base UI popup (menu, dialog, tooltip) is open; app-level Esc handlers stand back. */
export const popupOpen = () => !!document.querySelector('[data-popup]');

function readJSON<T>(key: string, fallback: T): T {
  try {
    const raw = localStorage.getItem(key);
    return raw ? (JSON.parse(raw) as T) : fallback;
  } catch {
    return fallback;
  }
}

function hashSelection(): string | null {
  const h = window.location.hash;
  return h.startsWith(HASH_PREFIX) ? decodeURIComponent(h.slice(HASH_PREFIX.length)) || null : null;
}

export default function App() {
  const [auth, setAuth] = useState<Auth>('checking');
  const [authRequired, setAuthRequired] = useState(true);
  const [state, dispatch] = useReducer(reducer, initialState, (s) => ({ ...s, selectedId: hashSelection() }));
  const [meta, setMeta] = useState<Meta | null>(null);
  const [streamKey, setStreamKey] = useState(0);
  const narrow = useMedia(NARROW);
  const sheetInline = useMedia(SHEET_INLINE);
  const [notice, setNotice] = useState<string | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
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
  // New task opens a draft for a Project on its defaults; nothing exists on the service until its first Send.
  // `tick` refocuses its composer when New task is chosen again.
  const [newTask, setNewTask] = useState<{ projectId: string; defaults: TaskDefaults; tick: number } | null>(null);
  const newTaskTick = useRef(0);
  const aside = useRef<HTMLElement>(null);
  const loadedAt = useRef(new Date().toISOString());
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
  }, []);

  // Custom models are part of the model lists, so a change to them reloads the catalogs.
  const customModels = JSON.stringify(state.settings.custom_models ?? []);
  useEffect(() => {
    if (auth !== 'in') return;
    lastCheck.current = Date.now();
    api.meta().then(setMeta).catch(() => setMeta(null));
  }, [auth, customModels]);

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

  // Keep the view in the URL fragment so a reload lands on it: `#settings`, else the selected task.
  useEffect(() => {
    const id = state.selectedId;
    const next = settingsOpen ? SETTINGS_HASH : id ? `${HASH_PREFIX}${encodeURIComponent(id)}` : '';
    if (window.location.hash !== next) history.replaceState(null, '', `${window.location.pathname}${window.location.search}${next}`);
  }, [state.selectedId, settingsOpen]);

  /**
   * Hides or shows the sidebar (wide layout), remembered per browser. Focus follows the
   * toggle the user was on, so a keyboard user is never left on an inert element; focus
   * anywhere else (the composer) is left alone.
   */
  const toggleSidebar = useCallback(() => {
    const next = !sidebarOpen;
    setSidebarOpen(next);
    localStorage.setItem(SIDEBAR_KEY, JSON.stringify(next));
    const active = document.activeElement;
    const onToggle = next ? active?.id === 'sidebar-show' : !!aside.current?.contains(active);
    if (onToggle) requestAnimationFrame(() => document.getElementById(next ? 'sidebar-hide' : 'sidebar-show')?.focus());
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

  // The selected task counts as viewed as long as it is on screen: every frame that carries
  // its updated_at marks it, so nothing it does while open shows as unread later.
  const markViewed = useCallback((id: string, at: string) => {
    setViewed((v) => {
      if (v[id] === at) return v;
      const next = { ...v, [id]: at };
      localStorage.setItem(VIEWED_KEY, JSON.stringify(next));
      return next;
    });
  }, []);

  // One EventSource at a time, scoped to the selected session.
  useEffect(() => {
    if (auth !== 'in') return;
    const es = new EventSource(api.eventsUrl(state.selectedId));
    let retry: number | undefined;
    dispatch({ type: 'connection', status: 'connecting' });
    es.onopen = () => {
      dispatch({ type: 'connection', status: 'connected' });
      if (wasDown.current) {
        wasDown.current = false;
        checkVersion(true);
      }
    };
    es.onerror = () => {
      wasDown.current = true;
      if (es.readyState !== EventSource.CLOSED) {
        dispatch({ type: 'connection', status: 'reconnecting' });
        return;
      }
      // The browser gave up (non-200 response). Re-check auth, then reopen.
      dispatch({ type: 'connection', status: 'offline' });
      const later = () => {
        retry = window.setTimeout(() => setStreamKey((k) => k + 1), 5000);
      };
      api
        .auth()
        .then((r) => (loggedIn(r) ? later() : setAuth('out')))
        .catch(later);
    };
    const selected = state.selectedId;
    // Stream deltas wait for the next animation frame and land in one dispatch; any other
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
      const data = JSON.parse((e as MessageEvent).data) as SnapshotData;
      flush();
      // A snapshot lands the opened Task: the pane cross-fades to it (ViewTransition, type "switch").
      startTransition(() => {
        addTransitionType('switch');
        dispatch({ type: 'snapshot', data });
      });
      setFilter((current) => {
        if (!current || data.projects.some((p) => p.id === current)) return current;
        localStorage.removeItem(FILTER_KEY);
        return null;
      });
      if (data.session && data.session.id === selected) markViewed(selected, data.session.updated_at);
      // Composer drafts of Tasks that no longer exist go with them.
      try {
        for (const key of staleDraftKeys(Object.keys(localStorage), data.sessions.map((s) => s.id), data.projects.map((p) => p.id))) localStorage.removeItem(key);
      } catch {
        // Storage unavailable: nothing to sweep.
      }
    });
    for (const name of UPDATE_EVENTS) {
      es.addEventListener(name, (e) => {
        const data = { name, ...JSON.parse((e as MessageEvent).data) } as UpdateData;
        // A hidden tab gets no animation frames: apply at once there.
        if (data.name === 'delta' && !document.hidden) {
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
      es.close();
      window.clearTimeout(retry);
      // Deltas still queued belong to this stream; the next one starts with a snapshot.
      cancelAnimationFrame(frame);
    };
  }, [auth, state.selectedId, streamKey, markViewed, checkVersion]);

  const hasNews = useCallback(
    (s: SessionSummary) => {
      if (s.id === state.selectedId) return false;
      const seen = viewed[s.id] ?? loadedAt.current;
      return s.updated_at > seen;
    },
    [viewed, state.selectedId],
  );

  // Opening a Task reopens the stream; only a disconnect that lasts is shown as one.
  const late = useLate(state.connection !== 'connected', QUIET_MS);
  const connection = late ? state.connection : 'connected';
  const lateLoad = useLate(!!state.selectedId && !state.detail && !settingsOpen, QUIET_MS);

  const refreshMeta = useCallback(() => checkVersion(true), [checkVersion]);
  const ctx = useMemo(() => ({ meta, dispatch, narrow, hasNews, settings: state.settings, usage: state.usage, refreshMeta }), [meta, narrow, hasNews, state.settings, state.usage, refreshMeta]);

  // Focus the composer of a Task that was just created or chosen, once its detail is on screen; a read-only Task has nothing to type into.
  const detailId = state.detail?.id;
  const detailLocked = !!state.detail && readOnly(state.detail);
  useEffect(() => {
    if (!detailId || detailId !== focusTask.current) return;
    focusTask.current = null;
    if (!detailLocked) document.getElementById('composer-text')?.focus();
  }, [detailId, detailLocked, selectTick]);

  const logout = useCallback(() => {
    void api.logout().finally(() => setAuth('out'));
  }, []);
  const onSheet = useCallback((open: boolean) => {
    setSheetOpen(open);
    if (!open) document.getElementById('changes-link')?.focus();
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
    startTransition(() => {
      addTransitionType('switch');
      dispatch({ type: 'select', id });
    });
    setNotice(null);
    setSheetOpen(false);
    setDrawerOpen(false);
    setSettingsOpen(false);
    setNewTask(null);
  }, []);
  // A `#task=` fragment the user navigates to (back/forward, a pasted URL) selects that Task; the
  // write above uses replaceState, which fires no hashchange.
  useEffect(() => {
    const onHash = () => {
      const id = hashSelection();
      if (id) select(id);
    };
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, [select]);

  /** New task: a draft for the Project on its defaults, with its composer focused; no request until its first Send. */
  const startTask = useCallback(
    (projectId: string) => {
      const project = state.projects.find((p) => p.id === projectId);
      const defaults = project && resolveTaskDefaults(meta, project.defaults, state.settings.hidden_models);
      if (!defaults) {
        setNotice(`Could not start a task: ${meta ? 'No provider is available.' : 'The provider list has not loaded yet.'}`);
        return;
      }
      select(null);
      setNewTask({ projectId, defaults, tick: ++newTaskTick.current });
    },
    [state.projects, state.settings.hidden_models, meta, select],
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
  // Alt+N opens it from anywhere but a menu or dialog (Ctrl+N is the browser's); the pen's own tooltip, which names the shortcut, does not stand in the way.
  useEffect(() => {
    if (auth !== 'in') return;
    const onKey = (e: KeyboardEvent) => {
      if (!e.altKey || e.ctrlKey || e.metaKey || e.shiftKey || e.code !== 'KeyN' || e.defaultPrevented || document.querySelector('[data-popup]:not([role="tooltip"])')) return;
      e.preventDefault();
      openNewTask();
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [auth, openNewTask]);

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
        if (info && !info.available) throw new Error(`${info.display_name} is unavailable: ${info.reason || 'not installed'}`);
        s = await api.createSession(createRequest(projectId, first.settings, entry.id));
      } catch (e) {
        creating.current.set(projectId, { ...entry, busy: false });
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
    [meta, select],
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
      settle: (id) => void runTask(id, () => api.stage(id, 'settle'), 'settle the task').catch(() => {}),
      reopen: (id) => void runTask(id, () => api.stage(id, 'reopen'), 'reopen the task').catch(() => {}),
      archive: (id) => openTaskDialog({ kind: 'archive', id }),
      remove: (id) => openTaskDialog({ kind: 'delete', id }),
      close: (id) => openTaskDialog({ kind: 'close', id }),
      busy: busyTasks,
    }),
    [renaming, busyTasks, select, runTask, state.sessions, openTaskDialog],
  );

  const actions: WorkspaceActions = useMemo(
    () => ({
      onNewTask: openNewTask,
      onAddProject: () => openDialog({ kind: 'add' }),
      onEditProject: (project) => openDialog({ kind: 'edit', project }),
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
        setDrawerOpen(false);
        setNewTask(null);
      },
    }),
    [filter, narrow, drawerOpen, sidebarOpen, settingsOpen, openNewTask, openDialog, toggleSidebar],
  );

  const selected = state.sessions.find((s) => s.id === state.selectedId) ?? null;

  // The tab title and the installed app's badge carry how many Tasks wait for the user; the title names the open Task.
  const attention = useMemo(() => needsYouCount(state.sessions), [state.sessions]);
  // A new Task shows as "New task"; only real Tasks count as needing you.
  const openName = selected ? taskName(selected) : newTask ? '' : null;
  useEffect(() => {
    document.title = pageTitle(attention, openName);
    if (attention > 0) navigator.setAppBadge?.(attention).catch(() => {});
    else navigator.clearAppBadge?.().catch(() => {});
  }, [attention, openName]);

  if (auth === 'checking') {
    return (
      <main className="grid h-dvh place-items-center text-caption text-muted" aria-busy="true">
        <Brand className="animate-pulse-dot" />
      </main>
    );
  }
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />;

  // The Task on screen: the selected one, or the one before it (inert) until the new detail arrives or the wait gets long.
  const shown = state.detail && selected ? state.detail : state.selectedId && selected && !lateLoad ? state.previous : null;
  const stale = !!shown && shown !== state.detail;
  const project = shown ? state.projects.find((p) => p.id === shown.project_id) : undefined;
  const dialogTask = taskDialog ? state.sessions.find((s) => s.id === taskDialog.id) : undefined;
  const newTaskProject = newTask ? state.projects.find((p) => p.id === newTask.projectId) : undefined;

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

  // The main pane's header starts with the sidebar toggle when the sidebar is hidden, or the drawer toggle on a narrow screen.
  const leading = narrow ? (
    <SidebarToggle id="sidebar-show" size="icon-md" open={drawerOpen} onToggle={() => setDrawerOpen((o) => !o)} className="-ml-1" />
  ) : !sidebarOpen ? (
    <SidebarToggle id="sidebar-show" size="icon-md" open={false} onToggle={toggleSidebar} className="-ml-1" />
  ) : null;

  let pane: React.ReactNode;
  if (settingsOpen) {
    pane = <SettingsView leading={leading} onClose={() => setSettingsOpen(false)} />;
  } else if (newTask && newTaskProject) {
    pane = <NewTaskPane key={newTask.projectId} project={newTaskProject} defaults={newTask.defaults} onSend={createTask} leading={leading} />;
  } else if (shown) {
    pane = (
      <Task
        key={shown.id}
        session={shown}
        project={project}
        agents={state.agents}
        agentSteps={state.agentSteps}
        snapshotSeq={state.snapshotSeq}
        sheetOpen={sheetOpen}
        sidePanelInline={sheetInline}
        onSheet={onSheet}
        onSessionUpdate={onSessionUpdate}
        onInteractionUpdate={onInteractionUpdate}
        leading={leading}
      />
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
  } else if (state.selectedId) {
    pane = <LoadingPane leading={leading} placeholder={lateLoad} />;
  } else {
    // A quiet placeholder (issue #185): New task and Add project live in the sidebar.
    pane = (
      <EmptyPane leading={leading} connection={connection}>
        <Brand markOnly className="[&_svg]:size-9 opacity-80" />
        <p className="text-ui text-muted">{state.projects.length > 0 ? 'Open a task from the sidebar, or start a new one there.' : 'Add a project in the sidebar to begin.'}</p>
      </EmptyPane>
    );
  }

  return (
    <AppContext.Provider value={ctx}>
      <TaskActionsContext.Provider value={taskActions}>
        <TooltipProvider delay={400} closeDelay={0}>
          <div
            className={cn(
              SHELL,
              narrow
                ? 'grid-cols-1'
                : cn('transition-[grid-template-columns] duration-240 ease-app', sidebarOpen ? 'grid-cols-[264px_minmax(0,1fr)]' : 'grid-cols-[0px_minmax(0,1fr)]'),
            )}
          >
            {/* The column animates to 0; the sidebar keeps its width inside so nothing reflows on the way, and is inert once hidden. */}
            {!narrow && (
              <aside ref={aside} className="min-h-0 overflow-hidden" inert={!sidebarOpen} aria-hidden={!sidebarOpen}>
                <div className="h-full w-rail">{sidebar}</div>
              </aside>
            )}
            {narrow && (
              <Sheet open={drawerOpen} onOpenChange={setDrawerOpen} side="left" label="Projects">
                {sidebar}
              </Sheet>
            )}
            <main className="relative flex min-h-0 min-w-0 flex-col bg-canvas">
              {connection !== 'connected' && (
                <p role="status" className={cn('flex items-center gap-2 border-b border-hairline px-4 py-1.5 text-caption animate-fade-in', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
                  <Dot tone={connection === 'offline' ? 'error' : 'warning'} pulse />
                  {CONNECTION_TEXT[connection]}
                </p>
              )}
              {updated && (
                <p role="status" className="flex items-center gap-2 border-b border-hairline bg-surface px-4 py-1 text-caption text-body animate-fade-in">
                  <Dot tone="accent" />
                  <span className="flex-1">UAM was updated.</span>
                  <Button size="sm" variant="secondary" onClick={() => window.location.reload()}>
                    Reload
                  </Button>
                </p>
              )}
              {notice && (
                <p className="flex items-center gap-2 border-b border-hairline bg-error-wash px-4 py-1.5 text-caption text-error animate-fade-in" role="alert">
                  <span className="flex-1">{notice}</span>
                  <Button size="icon" variant="ghost" aria-label="Dismiss" className="text-error hover:bg-error-wash hover:text-error" onClick={() => setNotice(null)}>
                    <X />
                  </Button>
                </p>
              )}
              {/* One wrapper for every pane, so the Task on screen stays mounted while it turns stale; a switch cross-fades it. */}
              <ViewTransition name="pane" default="none" update={{ switch: 'vt-pane', default: 'none' }}>
                <div className="flex min-h-0 flex-1 flex-col" inert={stale}>
                  {pane}
                </div>
              </ViewTransition>
            </main>

            <NewTaskPalette open={paletteOpen} onOpenChange={setPaletteOpen} projects={state.projects} sessions={state.sessions} selectedId={state.selectedId} filter={filter} onPick={startTask} />
            {dialog?.kind === 'add' && (
              <AddProjectDialog
                open={dialogOpen}
                onClose={() => setDialogOpen(false)}
                onClosed={() => setDialog(null)}
                onAdded={(p) => {
                  dispatch({ type: 'upsert_project', project: p });
                  showProject(p.id);
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
                onUpdated={(p) => dispatch({ type: 'upsert_project', project: p })}
                onRemoved={(id) => dispatch({ type: 'remove_project', id })}
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
              title={`Delete ${dialogTask ? `“${dialogTask.name || dialogTask.title || 'this task'}”` : 'this task'}?`}
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
          </div>
        </TooltipProvider>
      </TaskActionsContext.Provider>
    </AppContext.Provider>
  );
}

/** The header of the non-Task views while the sidebar is away (narrow, or hidden): its toggle, the brand, the connection. */
function PaneHeader({ leading, connection }: { leading: React.ReactNode; connection?: keyof typeof CONNECTION_TEXT }) {
  return (
    <header className="flex h-header shrink-0 items-center gap-2 border-b border-hairline px-3">
      {leading}
      <span className="text-title font-semibold text-ink">uam</span>
      <span className="flex-1" />
      {connection && connection !== 'connected' && (
        <span role="status" className="flex items-center gap-1.5 text-caption text-warning" title={CONNECTION_TEXT[connection]}>
          <Dot tone={connection === 'offline' ? 'error' : 'warning'} pulse />
          <span className="sr-only">{CONNECTION_TEXT[connection]}</span>
        </span>
      )}
    </header>
  );
}

function EmptyPane({ leading, connection, children }: { leading: React.ReactNode; connection: keyof typeof CONNECTION_TEXT; children: React.ReactNode }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {leading && <PaneHeader leading={leading} connection={connection} />}
      <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 pb-16 text-center animate-rise">{children}</div>
    </div>
  );
}

/** Calm placeholder while the selected Task's detail is on its way and nothing was on screen before: the header holds its height, a quiet indicator once the wait is long. */
function LoadingPane({ leading, placeholder }: { leading: React.ReactNode; placeholder: boolean }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-busy="true" aria-label="Loading conversation">
      <header className="flex h-header shrink-0 items-center gap-2 border-b border-hairline px-3">{leading}</header>
      {placeholder && <Loading label="Loading the conversation…" delay={0} className="flex-1 justify-center" />}
    </div>
  );
}
