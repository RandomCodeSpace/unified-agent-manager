import { useApi } from './ApiContext';
import { scopedMachine, useFederation } from './FederationContext';
import { recentProjection } from './lib/historyState';
import { DetailsProvider } from './components/Details';
import { X } from 'lucide-react';
import { Suspense, lazy, startTransition, useCallback, useEffect, useEffectEvent, useLayoutEffect, useMemo, useReducer, useRef, useState } from 'react';
import { describeError, newRequestId, onUnauthorized, readOnly, taskName, type Interaction, type Project, type SessionDetail, type SessionSummary } from './api';
import { initialState, loggedIn, reducer, type Auth } from './state';
import { AppContext, Dot, useMedia } from './components/common';
import { ModelCallsContext } from './components/ModelPhase';
import { MessageOutcomesContext, MessagePairsContext } from './components/Messages';
import { Login } from './components/Login';
import { Home } from './components/Home';
import { AddProjectDialog, EditProjectDialog } from './components/Projects';
import { NewTaskPalette } from './components/ProjectPicker';
import { Brand, CONNECTION_TEXT, Sidebar, SidebarRail, SidebarToggle, type WorkspaceActions } from './components/Sidebar';
import { cn } from './lib/cn';
import { newTaskKey, parseDraft, serializeDraft } from './lib/drafts';
import { needsYouCount, newTaskProject as paletteStart, pageTitle, putAwayWork, tasksOf } from './lib/tasks';
import { setViewing, startNotifications } from './lib/notify';
import { clearArchive, forgetArchive } from './lib/historyArchive';
import { RecentTasks } from './lib/recentTasks';
import { NewTaskPane, Task } from './components/Task';
import { TaskActionsContext, TaskList, type Renaming, type TaskActions } from './components/taskActions';
import { TryModelDialog } from './components/Assist';
import { saveBlob } from './lib/assist';
import { Button } from './components/ui/button';
import { AlertDialog, Sheet } from './components/ui/dialog';
import { CoveredContext } from './components/ui/reader';
import { SettleGuard, useUnsavedGuard, type UnsavedTarget } from './components/SettleGuard';
import { TooltipProvider } from './components/ui/tooltip';
import { readJSON, writeJSON } from './lib/storage';
import { useChangesSheet } from './lib/useChangesSheet';
import { useEventStream, type SweptMembership } from './lib/useEventStream';
import { useMachineActions } from './lib/useMachineActions';
import { useMeta } from './lib/useMeta';
import { COARSE, useNewTask, type NewTaskDraft } from './lib/useNewTask';
import { useReadMarks } from './lib/useReadMarks';
import { ALL_ROUTINES, HASH_PREFIX, SETTINGS_HASH, hashAgent, hashRoutines, hashSelection, useHashRoute } from './lib/useHashRoute';
import { useDrawer, useSidebar } from './lib/useSidebar';
import { useThreadKeys } from './lib/useThreadKeys';
import { useTypeToCompose } from './lib/useTypeToCompose';
import { conversation, useViewFocus } from './lib/useViewFocus';
import { Contained } from './components/VisualBoundary';
import { editing, keysTaken, sessionsUpdate } from './lib/overlays';
import { EmptyPane, LoadingPane, LoadingVeil, PaneHeader, TerminalDock, ViewLoading, usePaneLoad, useTerminalDock } from './components/Panes';

export { keysTaken, popupOpen, sessionsUpdate, threadCovered } from './lib/overlays';

/** Settings and Routines are off the first paint: each loads with its first open and stays. */
const SettingsView = lazy(() => import('./components/Settings').then((m) => ({ default: m.SettingsView })));
const RoutinesView = lazy(() => import('./components/Routines').then((m) => ({ default: m.RoutinesView })));

type ProjectDialog = { kind: 'add' } | { kind: 'edit'; project: Project } | null;
/** `owner`: another machine's Task (connected instances), confirmed here and run against that machine. */
type TaskDialog = { kind: 'delete' | 'close'; id: string; owner?: string } | null;

/** Below this the sidebar is a drawer (DESIGN.md breakpoints). */
const NARROW = '(max-width: 959px)';
/** From this width the Changes sheet sits beside the column instead of over it. */
const SHEET_INLINE = '(min-width: 1280px)';

/** When the owner last had each Task on screen, by this browser's clock: "Since you left" counts from it. */

/** When this browser first ran the Task list's read tracking: a Task never opened here and changed since is unread. */

/** The shell fills the viewport and keeps clear of the notch, rounded corners and home indicator of an installed app (`viewport-fit=cover`). */

// With the on-screen keyboard open the shell covers only what is visible above it (lib/viewport).
// iOS paints the status bar in the colour of a full-width fixed element at the page's top edge, so
// the drawer's dim (grey there) starts at the drawer's edge, which covers the rest; the bar keeps the page's colour.
const DRAWER_BACKDROP = 'supports-[-webkit-touch-callout:none]:left-[min(360px,calc(100vw-44px))]';
const SHELL = 'grid h-[var(--app-height,100dvh)] mt-[var(--app-top,0px)] overflow-x-clip pt-[env(safe-area-inset-top)] pr-[env(safe-area-inset-right)] pb-[env(safe-area-inset-bottom)] pl-[env(safe-area-inset-left)] in-data-keyboard:pb-0';





export default function App() {
  const api = useApi();
  const FILTER_KEY = api.storageKey('uam.projectFilter');
  const LOOKED_KEY = api.storageKey('uam.looked');
  const VIEWED_KEY = api.storageKey('uam.viewed');
  const federation = useFederation();
  const onAuth = federation?.onAuth;
  const onRoute = federation?.onRoute;
  const forwardEvent = federation?.onEvent;
  const onTerminal = federation?.onTerminal;
  const initialPath = federation?.initialPath;
  const federated = !!federation;
  // With connected instances, every machine; switching the one on screen starts its view from what is known of it, without a blank page.
  const machines = federation?.machines;
  // The instance the sidebar's switcher scoped the Task list to: Home lists its Tasks alone.
  const scope = scopedMachine(federation);
  const here = api.owner?.id ?? '';
  const [auth, setAuth] = useState<Auth>(() => (federation?.authenticated ? 'in' : 'checking'));
  const [authRequired, setAuthRequired] = useState(true);
  const [state, dispatch] = useReducer(reducer, initialState, (s) => {
    const seed = federation?.seed;
    const known = seed?.loaded ? { loaded: true, projects: seed.projects, sessions: seed.sessions, settings: seed.settings, usage: seed.usage } : {};
    return { ...s, ...known, selectedId: hashSelection(initialPath) };
  });
  const latestSessions = useRef(state.sessions);
  useLayoutEffect(() => { latestSessions.current = state.sessions; }, [state.sessions]);
  const [recentTasks] = useState(() => new RecentTasks());
  // Selection restarts the stream; only a changed membership needs another full storage sweep.
  const sweptMembership = useRef<SweptMembership>(null);
  const confirmedDetail = useRef<SessionDetail | null>(null);
  useLayoutEffect(() => {
    confirmedDetail.current = auth === 'in' && state.connection === 'connected' ? state.detail && recentProjection(state.detail) : null;
  }, [auth, state.connection, state.detail]);
  const metaCache = federation?.metaCache;
  const narrow = useMedia(NARROW);
  const sheetInline = useMedia(SHEET_INLINE);
  const [notice, setNotice] = useState<string | null>(null);
  const { drawerOpen, setDrawerOpen, drawerKept, drawer } = useDrawer(narrow, state.loaded);
  const [dialog, setDialog] = useState<ProjectDialog>(null);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [taskDialog, setTaskDialog] = useState<TaskDialog>(null);
  const [taskDialogOpen, setTaskDialogOpen] = useState(false);
  // `owner`: a rename on another machine's row (connected instances).
  const [renaming, setRenaming] = useState<(Renaming & { owner?: string }) | null>(null);
  // The New task palette (the pen, or Alt+N); it chooses the draft's Project.
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [busyTasks, setBusyTasks] = useState<Readonly<Record<string, boolean>>>({});
  const emptyWorkspace = state.loaded && state.sessions.length === 0;
  const [ownFilter, setFilter] = useState<string | null>(() => readJSON<string | null>(FILTER_KEY, null));
  // Under federation the filter is a Project on a machine (or a machine alone), kept by the owner; here it is that Project while its machine is on screen.
  const machineFilter = machines ? federation?.filter ?? null : undefined;
  const filter = machineFilter === undefined ? ownFilter : machineFilter?.machine === here ? machineFilter.project || null : null;
  const onMachineFilter = federation?.onFilter;
  const applyFilter = useCallback((id: string | null) => {
    if (machines && onMachineFilter) return onMachineFilter(id === null ? null : { machine: here, project: id });
    setFilter(id);
    writeJSON(FILTER_KEY, id);
  }, [machines, onMachineFilter, here, FILTER_KEY]);
  const [settingsOpen, setSettingsOpen] = useState(() => (initialPath ?? window.location.hash) === SETTINGS_HASH);
  // The subagent open in the selected Thread's Conversation pane; it belongs to that Thread, so selecting another leaves it.
  const [agentView, setAgentView] = useState(() => hashAgent(initialPath));
  const shownAgent = agentView?.task === state.selectedId ? agentView.agent : null;
  const [tokenPricesRequest, setTokenPricesRequest] = useState(0);
  const [connectionsRequest, setConnectionsRequest] = useState(0);
  // The provider whose account Settings opens on (the blocked app's Open Settings), or null.
  const [accountFocus, setAccountFocus] = useState<string | null>(null);
  const [routinesFor, setRoutinesFor] = useState<string | null>(() => hashRoutines(initialPath));
  // New task opens a draft for a Project on its defaults; nothing exists on the service until its first Send.
  // `tick` refocuses its composer when New task is chosen again.
  const [newTask, setNewTask] = useState<NewTaskDraft | null>(null);
  // Task whose composer takes focus once its detail arrives (a Task just created, or one chosen
  // from the list on a fine pointer); `selectTick` re-runs the focus when the open Task is chosen again.
  const focusTask = useRef<string | null>(null);
  const [selectTick, setSelectTick] = useState(0);

  useEffect(() => { if (auth !== 'checking') onAuth?.(auth === 'in'); }, [auth, onAuth]);
  const { terminalId, setTerminalId, terminalProject, terminalOpens, docked, terminalDock, terminalPanel, toggleTerminal, closeTerminal } = useTerminalDock({ api, state, onTerminal });

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
  }, [api, recentTasks]);

  useEffect(() => { if (auth !== 'in') recentTasks.clear(); }, [auth, recentTasks]);
  // Signed out for any reason (sign-out, a 401, sign-in required): no transcript stays in this browser.
  useEffect(() => {
    if (auth === 'out') {
      sweptMembership.current = null;
      clearArchive();
    }
  }, [auth]);

  const { meta, metaError, checkVersion, refreshMeta, updated } = useMeta({ api, auth, federation, customModels: state.loaded ? JSON.stringify(state.settings.custom_models ?? []) : null });

  const { sidebarOpen, toggleSidebar, aside } = useSidebar({ auth, narrow, emptyWorkspace, setDrawerOpen });

  useTypeToCompose(auth);

  const { sheetOpen, setSheetOpen, onSheet } = useChangesSheet();

  // Settings or Routines over the selected Task: the owner is not looking at it, as if the page were hidden.
  const viewOpen = settingsOpen || !!routinesFor;
  const viewShown = useRef(false);
  const { setViewed, markViewed, hasNews, opened } = useReadMarks({ api, selectedId: state.selectedId, sessions: state.sessions, viewOpen, viewShown, LOOKED_KEY, VIEWED_KEY });
  const setStreamKey = useEventStream({ api, auth, selectedId: state.selectedId, dispatch, forwardEvent, recentTasks, sweptMembership, confirmedDetail, checkVersion, markViewed, setAuth, setNotice, setFilter, setViewed, FILTER_KEY, LOOKED_KEY, VIEWED_KEY });

  const { loading, connection, mountedId, detailId, held, lateLoad } = usePaneLoad({ state, settingsOpen, routinesFor });

  const openUsage = useCallback(() => {
    if (narrow) setDrawerOpen(true);
    // The drawer's portal mounts before the next frame.
    requestAnimationFrame(() => {
      const buttons = document.querySelectorAll<HTMLElement>('[data-account-usage]');
      Array.from(buttons).find((button) => !button.closest('[inert], [aria-hidden="true"]'))?.click();
    });
  }, [narrow, setDrawerOpen]);
  const ownerSettings = useMemo(() => api.owner ? { ...state.settings, terminal: state.settings.terminal && api.supports('terminal-v1') } : state.settings, [api, state.settings]);
  const ctx = useMemo(
    () => ({ meta, metaError, loaded: state.loaded, dispatch, narrow, settings: ownerSettings, usage: state.usage, refreshMeta, openUsage }),
    [meta, metaError, state.loaded, narrow, ownerSettings, state.usage, refreshMeta, openUsage],
  );

  // Focus the composer of a Task that was just created or chosen, once its detail is on screen; a read-only Task has nothing to type into,
  // and a rename the choosing double-click started keeps its input.
  const detailLocked = !!state.detail && readOnly(state.detail);
  useEffect(() => {
    if (!detailId || detailId !== focusTask.current) return;
    focusTask.current = null;
    if (!detailLocked && !renaming) document.getElementById('composer-text')?.focus();
  }, [detailId, detailLocked, selectTick, renaming]);

  const logout = useCallback(() => {
    recentTasks.clear();
    confirmedDetail.current = null;
    // Log out is in Settings: closing it leaves the sign-in screen the plain title and no fragment, as a fresh one has.
    void api.logout().finally(() => { dispatch({ type: 'reset' }); setSettingsOpen(false); setAuth('out'); });
  }, [api, recentTasks]);
  const onSessionUpdate = useCallback((s: SessionSummary) => dispatch({ type: 'upsert_session', session: s }), []);
  const onInteractionUpdate = useCallback((sessionId: string, interaction: Interaction) => dispatch({ type: 'upsert_interaction', sessionId, interaction }), []);

  // The drawer closes first: a dialog's backdrop sits under the drawer's own layer.
  const openDialog = useCallback((d: Exclude<ProjectDialog, null>) => {
    setDrawerOpen(false);
    setDialog(d);
    setDialogOpen(true);
  }, [setDrawerOpen]);
  const openTaskDialog = useCallback((d: Exclude<TaskDialog, null>) => {
    setTaskDialog(d);
    setTaskDialogOpen(true);
  }, []);

  // A move between views (a Task, Settings, Routines) adds a history entry, so Back and
  // Forward retrace it; a correction the app makes on its own (a stale or unknown fragment) replaces the entry.
  const pushView = useRef(false);
  useLayoutEffect(() => {
    viewShown.current = viewOpen;
  });

  const select = useCallback((id: string | null) => {
    pushView.current = true;
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
    setRoutinesFor(null);
    setNewTask(null);
  }, [recentTasks, setSheetOpen, setDrawerOpen]);

  const openTask = useCallback((id: string) => select(id), [select]);
  /** Opens one of the selected Thread's subagents in its Conversation pane, or (null) goes back to the Thread: a move between views. */
  const showAgent = useCallback((agent: string | null) => {
    pushView.current = true;
    setAgentView(agent && state.selectedId ? { task: state.selectedId, agent } : null);
  }, [state.selectedId]);
  // Esc goes back from a subagent to its Thread, as ← does; not while typing, in the terminal, in a side panel or with a popup open.
  useEffect(() => {
    if (!shownAgent || viewOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || e.defaultPrevented || keysTaken() || editing()) return;
      if (e.target instanceof Element && e.target.closest('.xterm, aside')) return;
      showAgent(null);
    };
    // On the window, so a side panel's own Esc (on the document) goes first.
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [shownAgent, viewOpen, showAgent]);
  const { noteOpener, closeView } = useViewFocus({ viewOpen, viewShown, pushView, setSettingsOpen, setRoutinesFor });
  const taskList = useMemo(() => ({ sessions: state.sessions, projects: state.projects, openTask }), [state.sessions, state.projects, openTask]);
  /** Settings in the main pane, from the signed-out banner (like the sidebar's gear, it keeps the selected Task behind it). */
  const showSettings = useCallback(() => {
    noteOpener();
    setDrawerOpen(false);
    // In a transition, like a Thread switch: what is on screen stays until the view (and its code, on the first open) is ready.
    startTransition(() => {
      setTokenPricesRequest(0);
      setAccountFocus(null);
      setSettingsOpen(true);
      setRoutinesFor(null);
      setNewTask(null);
    });
  }, [noteOpener, setDrawerOpen]);
  // Providers whose runtime is signed out: a banner over the pane says so until one signs in.
  const signedOut = useMemo(() => (meta?.providers ?? []).filter((p) => p.signed_out), [meta]);
  // A provider signed in as another account than the one this server is linked to blocks the app: only Settings stays open.
  // Under federation, a connection linked to another Copilot account than the hosting instance is blocked the same way.
  const fleetMismatch = federation?.accountMismatch;
  const mismatched = useMemo(() => (meta?.providers ?? []).find((p) => p.account_mismatch) ?? (fleetMismatch ? { name: 'copilot', reason: fleetMismatch } : null), [meta, fleetMismatch]);
  /** Settings on the provider's account section, its title focused. */
  const openAccount = useCallback((name: string) => {
    showSettings();
    setAccountFocus(name);
  }, [showSettings]);
  /** Routines in the main pane, one Project's or `all` (like Settings, it keeps the selected Task behind it). */
  const showRoutines = useCallback((projectId: string) => {
    noteOpener();
    setDrawerOpen(false);
    startTransition(() => {
      setRoutinesFor(projectId);
      setSettingsOpen(false);
      setNewTask(null);
    });
  }, [noteOpener, setDrawerOpen]);

  useEffect(() => {
    if (auth === 'in' && !federated) return startNotifications();
  }, [auth, federated]);
  // The Task on screen is not announced while this page is visible; Settings and Routines cover it.
  useEffect(() => {
    if (auth === 'in') setViewing(settingsOpen || routinesFor || mismatched ? null : state.selectedId);
  }, [auth, state.selectedId, settingsOpen, routinesFor, mismatched]);

  useHashRoute({ api, federated, onRoute, pushView, selectedId: state.selectedId, shownAgent, settingsOpen, routinesFor, select, showSettings, showRoutines, setAgentView });

  const { startTask, latestStartTask, createTask } = useNewTask({ api, meta, projects: state.projects, settings: state.settings, select, setNotice, newTask, setNewTask, refreshMeta, dispatch });

  /** With connected instances: a Project on another machine opens that machine, and its draft there once it has loaded. */
  const requestMachine = federation?.request;
  const pickProject = useCallback((projectId: string, machine = here) => {
    if (machine === here) startTask(projectId);
    else requestMachine?.({ machine, kind: 'new-task', projectId });
  }, [here, startTask, requestMachine]);
  const intent = federation?.intent;
  const consumeIntent = federation?.consumeIntent;
  const intentReady = !!intent && intent.machine === here && state.loaded && (intent.kind !== 'new-task' || !!meta || !!metaError);
  const runIntent = useEffectEvent(() => {
    if (!intent) return;
    consumeIntent?.();
    if (intent.kind === 'new-task') startTask(intent.projectId);
    else if (intent.kind === 'account') openAccount('copilot');
    else {
      const project = state.projects.find((p) => p.id === intent.projectId);
      if (project) openDialog({ kind: 'edit', project });
    }
  });
  // After the view has mounted, as if the owner had picked it here.
  useEffect(() => {
    if (!intentReady) return;
    const timer = window.setTimeout(runIntent);
    return () => window.clearTimeout(timer);
  }, [intentReady, intent]);

  /** New task: the palette chooses the Project, unless there is only one (on any machine, with connected instances). */
  const everyProject = useMemo(() => machines ? machines.flatMap((m) => (m.active ? state.projects : m.state.projects).map((p) => ({ machine: m.id, id: p.id }))) : state.projects.map((p) => ({ machine: here, id: p.id })), [machines, state.projects, here]);
  const projectCount = everyProject.length;
  const onlyProject = projectCount === 1 ? everyProject[0] : null;
  const openNewTask = useCallback(() => {
    // Blocked: no draft, no palette; leaving Settings shows the block.
    if (mismatched) select(null);
    else if (onlyProject) pickProject(onlyProject.id, onlyProject.machine);
    else if (projectCount > 0) setPaletteOpen(true);
  }, [mismatched, select, onlyProject, projectCount, pickProject]);
  useThreadKeys({ auth, openNewTask, federation, machines, machineFilter, here, projects: state.projects, sessions: state.sessions, filter, hasNews, selectedId: state.selectedId, select });


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

  const { guard, askUnsaved, askArchive, changesRequest, setChangesRequest, setUnsavedOpen } = useUnsavedGuard();
  /** This machine's Thread in the confirmation: its forced request, and Open Changes. */
  const localTarget = useCallback((id: string, kind: UnsavedTarget['kind']) => (refused: Pick<UnsavedTarget, 'pending' | 'paths' | 'anchor'>): UnsavedTarget => ({
    kind,
    ...refused,
    force: () => runTask(id, () => api.stage(id, kind, { force: true }), `${kind} the thread`),
    openChanges: () => {
      setUnsavedOpen(false);
      select(id);
      setChangesRequest({ id, at: Date.now() });
    },
  }), [runTask, api, select, setUnsavedOpen, setChangesRequest]);
  const putAway = useCallback((id: string, kind: UnsavedTarget['kind']) => runTask(id, () => askUnsaved(putAwayWork(latestSessions.current.find((s) => s.id === id)), () => api.stage(id, kind), localTarget(id, kind), () => api.changes(id, 'workspace')), `${kind} the thread`), [runTask, askUnsaved, api, localTarget]);

  /** Run again and Try with another model: the new Task opens; a refusal becomes the notice line. */
  const rerun = useCallback((id: string, model?: string) => runTask(id, async () => {
    const s = await api.rerun(id, { model, request_id: newRequestId() });
    dispatch({ type: 'upsert_session', session: s });
    select(s.id);
  }, 'run the thread again'), [api, runTask, select]);
  const [tryModel, setTryModel] = useState<string | null>(null);

  const taskActions: TaskActions = useMemo(
    () => ({
      select: (id) => select(id),
      startRename: (id, place) => setRenaming({ id, place }),
      renaming: renaming && !renaming.owner ? renaming : null,
      cancelRename: () => setRenaming(null),
      rename: async (id, name) => {
        setRenaming(null);
        const current = latestSessions.current.find((s) => s.id === id);
        if (!current || current.name === name) return;
        await runTask(id, () => api.rename(id, name), 'rename the thread').catch(() => {});
      },
      settle: (id) => void putAway(id, 'settle').catch(() => {}),
      reopen: (id) => void runTask(id, () => api.stage(id, 'reopen'), 'reopen the thread').catch(() => {}),
      unarchive: meta?.capabilities?.includes('unarchive-v1') ? (id) => void runTask(id, async () => {
        const s = await api.stage(id, 'unarchive');
        markViewed(id, s.updated_at);
        select(id);
        return s;
      }, 'unarchive the thread').catch(() => {}) : undefined,
      // Known unsaved work goes straight to its confirmation; otherwise the dry run decides which one opens.
      archive: (id) => {
        const s = latestSessions.current.find((x) => x.id === id);
        if (s && putAwayWork(s)) void putAway(id, 'archive').catch(() => {});
        else askArchive(() => api.archiveCheck(id), localTarget(id, 'archive'), () => putAway(id, 'archive'));
      },
      remove: (id) => openTaskDialog({ kind: 'delete', id }),
      close: (id) => openTaskDialog({ kind: 'close', id }),
      busy: busyTasks,
      runAgain: (id) => void rerun(id).catch(() => {}),
      tryModel: setTryModel,
      exportMarkdown: (id) => void runTask(id, async () => { const f = await api.exportMarkdown(id); saveBlob(f.blob, f.name); }, 'export the thread').catch(() => {}),
      // A handoff's Continue: the Project's new-Thread draft gets the handoff (after any text already in it), then opens.
      continueFrom: (projectId, text) => {
        const key = api.storageKey(newTaskKey(projectId));
        try {
          const old = parseDraft(localStorage.getItem(key));
          const raw = serializeDraft({ ...old, text: old?.text.trim() ? `${old.text}\n\n${text}` : text, files: old?.files ?? [], attachments: old?.attachments ?? [] });
          if (raw) localStorage.setItem(key, raw);
        } catch { /* Without storage the draft opens empty. */ }
        latestStartTask.current(projectId);
      },
    }),
    [renaming, busyTasks, select, runTask, api, openTaskDialog, rerun, meta, markViewed, putAway, askArchive, localTarget, latestStartTask],
  );

  const go = federation?.go;
  const { machineActions, runRemote, remoteBusy } = useMachineActions({ machines, metaCache, go, setNotice, renaming, setRenaming, openTaskDialog, askUnsaved, askArchive });

  const actions: WorkspaceActions = useMemo(
    () => ({
      onNewTask: openNewTask,
      onAddProject: () => openDialog({ kind: 'add' }),
      onEditProject: (project) => openDialog({ kind: 'edit', project }),
      onRoutines: (project) => showRoutines(project.id),
      routinesOpen: !!routinesFor && !settingsOpen,
      onAllRoutines: () => {
        pushView.current = true;
        if (routinesFor && !settingsOpen) startTransition(() => setRoutinesFor(null));
        else showRoutines(ALL_ROUTINES);
      },
      filter,
      onFilter: applyFilter,
      sidebarOpen: narrow ? drawerOpen : sidebarOpen,
      onToggleSidebar: () => (narrow ? setDrawerOpen((o) => !o) : toggleSidebar()),
      settingsOpen,
      onSettings: (target) => {
        noteOpener();
        setDrawerOpen(false);
        startTransition(() => {
          setTokenPricesRequest((request) => target === 'token-prices' ? request + 1 : 0);
          setConnectionsRequest((request) => target === 'connections' ? request + 1 : 0);
          setAccountFocus(null);
          setSettingsOpen((o) => target ? true : !o);
          setRoutinesFor(null);
          setNewTask(null);
        });
      },
    }),
    [filter, narrow, drawerOpen, sidebarOpen, settingsOpen, openNewTask, openDialog, toggleSidebar, showRoutines, routinesFor, noteOpener, applyFilter, setDrawerOpen],
  );


  const selected = state.sessions.find((s) => s.id === state.selectedId) ?? null;

  // The tab title and the installed app's badge carry how many Tasks wait for the user; the title names the open Task.
  const localNeedsYouTasks = useMemo(() => needsYouCount(state.sessions, hasNews), [state.sessions, hasNews]);
  const needsYouTasks = localNeedsYouTasks + (federation?.otherAttention ?? 0);
  // On a narrow screen the main pane's header starts with the drawer toggle; a collapsed wide sidebar keeps its toggle on the rail.
  // Held across renders, so the memoised Task header holds while the Task streams.
  const leading = useMemo(() => (narrow ? <SidebarToggle id="sidebar-show" size="icon-md" open={drawerOpen} count={needsYouTasks} onToggle={() => setDrawerOpen((o) => !o)} className="-ml-1 pointer-coarse:-ml-2.5" /> : null), [narrow, drawerOpen, needsYouTasks, setDrawerOpen]);
  const attention = needsYouTasks;
  // The title names what the pane shows, in the pane's own order; a new or untitled Task shows as "New task". Only real Tasks count as needing you.
  let shownName: string | null = null;
  if (settingsOpen) shownName = 'Settings';
  else if (mismatched) shownName = null;
  else if (routinesFor) shownName = 'Routines';
  else if (newTask) shownName = '';
  else if (selected) shownName = taskName(selected);
  // The sign-in screen has the plain title, also when the session ended under an open view.
  const loggedOut = auth === 'out';
  useEffect(() => {
    document.title = loggedOut ? pageTitle(0, null) : pageTitle(attention, shownName);
    // The badge is this page's count while it is visible; otherwise the service worker sets the service's count with each push (public/sw.js).
    const badge = () => {
      if (document.visibilityState !== 'visible') return;
      if (attention > 0) navigator.setAppBadge?.(attention).catch(() => {});
      else navigator.clearAppBadge?.().catch(() => {});
    };
    badge();
    document.addEventListener('visibilitychange', badge);
    return () => document.removeEventListener('visibilitychange', badge);
  }, [attention, shownName, loggedOut]);


  if (auth === 'checking') {
    return (
      <main className="grid h-dvh place-items-center text-caption text-muted" aria-busy="true">
        <Brand />
      </main>
    );
  }
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />;

  // A cached selected task stays visible through a slow refresh. An unrelated previous task yields to the skeleton after the quiet period.
  let shown: SessionDetail | null = null;
  if (state.detail && selected && state.detail.id === mountedId) shown = state.detail;
  else if (state.selectedId && selected && (state.previousCached || !lateLoad)) shown = state.previous ?? held.detail;
  const stale = !settingsOpen && !mismatched && !routinesFor && !newTask && !!shown && shown !== state.detail;
  const project = shown ? state.projects.find((p) => p.id === shown.project_id) : undefined;
  const spawner = shown?.spawned_by ? state.sessions.find((s) => s.id === shown.spawned_by) : undefined;
  if (terminalId && !terminalProject) setTerminalId(null);
  const dialogMachine = taskDialog?.owner !== undefined ? machines?.find((m) => m.id === taskDialog.owner) : undefined;
  const dialogTask = taskDialog ? (dialogMachine ? dialogMachine.state.sessions : state.sessions).find((s) => s.id === taskDialog.id) : undefined;
  const dialogBusy = !!taskDialog && !!(dialogMachine ? remoteBusy[dialogMachine.id]?.[taskDialog.id] : busyTasks[taskDialog.id]);
  const dialogTaskName = dialogTask ? `“${dialogTask.name || dialogTask.title || 'this thread'}”` : 'this thread';
  const newTaskProject = newTask ? state.projects.find((p) => p.id === newTask.projectId) : undefined;
  // A removed Project, or a `#routines=` link to none, lands on the usual view.
  const routinesKnown = routinesFor === ALL_ROUTINES || state.projects.some((p) => p.id === routinesFor);
  const routinesShown = !!routinesFor && !settingsOpen && state.loaded && routinesKnown;
  if (routinesFor && state.loaded && !routinesKnown) setRoutinesFor(null);

  function showProject(id: string) {
    applyFilter(id);
    select(null);
  }

  async function confirmTaskDialog() {
    if (!taskDialog) return;
    const { kind, id } = taskDialog;
    if (dialogMachine) {
      const c = dialogMachine.client;
      try {
        if (kind === 'close') await runRemote(dialogMachine, id, () => c.close(id), 'close the conversation');
        else {
          await runRemote(dialogMachine, id, () => c.deleteSession(id), 'delete the thread');
          forgetArchive(c.cacheKey(id));
        }
      } catch {
        // Reported on the notice line.
      }
      setTaskDialogOpen(false);
      return;
    }
    try {
      if (kind === 'close') await runTask(id, () => api.close(id), 'close the conversation');
      else {
        await runTask(id, () => api.deleteSession(id), 'delete the thread');
        sweptMembership.current = null;
        recentTasks.remove(id);
        forgetArchive(api.cacheKey(id));
        if (confirmedDetail.current?.id === id) confirmedDetail.current = null;
        sessionsUpdate(() => dispatch({ type: 'remove_session', id }));
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
      hasNews={hasNews}
      actions={actions}
      connection={connection}
      version={meta?.version}
      machineActions={machines ? machineActions : undefined}
      count={needsYouTasks}
      progressive={narrow}
    />
  );

  // The sidebar's column steps between its width and the rail's; the track never animates.
  const columns = sidebarOpen ? 'grid-cols-[var(--spacing-rail)_minmax(0,1fr)]' : 'grid-cols-[var(--spacing-rail-collapsed)_minmax(0,1fr)]';

  // Settings and Routines are a layer over the pane: the Thread beneath stays mounted (scroll, open rows, side panels) and comes back as it was.
  let view: React.ReactNode = null;
  if (settingsOpen) {
    view = <SettingsView leading={leading} onClose={closeView} onLogout={authRequired && !api.owner ? logout : undefined} tokenPricesRequest={tokenPricesRequest} connectionsRequest={connectionsRequest} connections={api.owner ? undefined : federation?.connectionsSettings} focusAccount={accountFocus ?? undefined} threadProject={selected?.project_id} />;
  } else if (routinesShown && !mismatched) {
    view = <RoutinesView leading={leading} projects={state.projects} scope={routinesFor === ALL_ROUTINES ? null : routinesFor} onScope={(id) => setRoutinesFor(id ?? ALL_ROUTINES)} sessions={state.sessions} onOpenTask={openTask} onClose={closeView} />;
  }
  let pane: React.ReactNode;
  // The terminal dock shows under a Task only; elsewhere (and under a view) it is hidden, its shell kept.
  let taskPane = false;
  let skeletonPane = false;
  if (mismatched) {
    // Every view but Settings lands here until the account is fixed there; the next meta read lifts it.
    pane = (
      <EmptyPane leading={leading}>
        <h1 className="text-display-md">Copilot is signed in to another account</h1>
        <p className="text-ui text-muted [overflow-wrap:anywhere]">{mismatched.reason}</p>
        <Button variant="primary" onClick={() => openAccount(mismatched.name)}>
          Open Settings
        </Button>
      </EmptyPane>
    );
  } else if (newTask && newTaskProject) {
    pane = <NewTaskPane key={newTask.projectId} project={newTaskProject} defaults={newTask.defaults} onSend={createTask} leading={leading} />;
  } else if (shown) {
    taskPane = !view;
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
        connected={state.connection === 'connected'}
        historyRequest={state.historyRequest}
        historyItemSeq={state.historyItemSeq}
        itemSeq={state.itemSeq}
        onHistoryReset={() => { recentTasks.remove(shown.id); confirmedDetail.current = null; setStreamKey(k => k + 1); }}
        sheetOpen={sheetOpen}
        sidePanelInline={sheetInline}
        onSheet={onSheet}
        changesRequest={changesRequest}
        terminalOpen={!!terminalProject}
        onTerminal={toggleTerminal}
        onSessionUpdate={onSessionUpdate}
        onInteractionUpdate={onInteractionUpdate}
        leading={leading}
        spawnedBy={spawner && { id: spawner.id, name: taskName(spawner), badge: state.projects.find((p) => p.id === spawner.project_id)?.badge }}
        since={opened.id === shown.id ? opened.mark : undefined}
        rerunOf={taskName(state.sessions.find((s) => s.id === shown.rerun_of) ?? { name: '', title: '' })}
        subagent={shown.id === state.selectedId ? shownAgent : null}
        onSubagent={showAgent}
      />
      </DetailsProvider>
    );
  } else if (state.selectedId && state.snapshotSeq >= 0 && !selected) {
    pane = (
      <EmptyPane leading={leading}>
        <h1 className="text-display-md">This thread no longer exists.</h1>
        <p className="text-ui text-muted">It was deleted, or its project was removed.</p>
        <Button variant="secondary" onClick={() => select(null)}>
          Back to projects
        </Button>
      </EmptyPane>
    );
  } else if (state.selectedId || !state.loaded) {
    // The Task's detail, or the first snapshot, is on its way: a skeleton, never the placeholder that says there is nothing.
    skeletonPane = true;
    pane = <LoadingPane leading={leading} />;
  } else {
    pane = (
      <div className="flex min-h-0 flex-1 flex-col">
        {leading && <PaneHeader leading={leading} />}
        <Home
          projects={state.projects}
          sessions={state.sessions}
          hasNews={hasNews}
          newTaskReady={meta !== null}
          onNewTask={openNewTask}
          onAddProject={actions.onAddProject}
          onSelect={select}
          machines={(scope ? [scope] : machines)?.map((m) => (m.active ? { ...m, state: { ...m.state, projects: state.projects, sessions: state.sessions }, hasNews } : m))}
          scope={scope?.label}
          onOpen={(machine, id) => (machine === here ? select(id) : go?.(machine, `${HASH_PREFIX}${encodeURIComponent(id)}`))}
        />
      </div>
    );
  }

  return (
    <AppContext.Provider value={ctx}>
      <ModelCallsContext.Provider value={state.modelCalls}>
      <MessagePairsContext.Provider value={state.messagePairs}>
      <MessageOutcomesContext.Provider value={state.messageOutcomes}>
      <TaskList.Provider value={taskList}>
      <TaskActionsContext.Provider value={taskActions}>
        <TooltipProvider delay={400} closeDelay={0}>
          {/* The first stop for the keyboard: past the sidebar to the conversation, else the main pane (a button, not a link: the fragment holds the view). */}
          <button type="button" className="sr-only z-60 rounded-sm bg-raised px-3 py-2 text-ui text-ink shadow-float focus-visible:not-sr-only focus-visible:fixed focus-visible:top-2 focus-visible:left-2" onClick={() => conversation()?.focus()}>
            Skip to main content
          </button>
          <div
            className={cn(
              SHELL,
              narrow ? 'grid-cols-1' : columns,
            )}
          >
            {/* The column steps to the collapsed rail's width at once (its width never animates); the sidebar keeps its width inside, fades in when it opens, and is inert once collapsed, under the rail that fades in. */}
            {!narrow && (
              <aside ref={aside} className="rail-edge relative min-h-0 overflow-clip">
                <div className={cn('h-full w-rail transition-opacity duration-240 ease-app', !sidebarOpen && 'opacity-0')} inert={!sidebarOpen} aria-hidden={!sidebarOpen}>{sidebar}</div>
                {!sidebarOpen && (
                  <div className="absolute inset-y-0 left-0 animate-fade-in">
                    <SidebarRail projects={state.projects} actions={actions} connection={connection} count={needsYouTasks} live={{ sessions: state.sessions, hasNews }} />
                  </div>
                )}
              </aside>
            )}
            {narrow && (
              <Sheet open={drawerOpen} onOpenChange={setDrawerOpen} side="left" label="Projects" className="w-rail" backdropClassName={DRAWER_BACKDROP} keepMounted ref={drawer} initialFocus={drawer}>
                {drawerKept && sidebar}
              </Sheet>
            )}
            <main tabIndex={-1} className="relative flex min-h-0 min-w-0 flex-col bg-canvas outline-hidden">
              {federation?.pendingRoute && <p role="status" className="px-4 py-2 text-caption">Opening the connected instance…</p>}
              {connection !== 'connected' && (
                <output className={cn('flex items-center gap-2 px-4 py-1.5 text-caption animate-fade-in', connection === 'offline' ? 'bg-error-wash text-error' : 'bg-warning-wash text-warning')}>
                  <Dot tone={connection === 'offline' ? 'error' : 'warning'} />
                  {CONNECTION_TEXT[connection]}
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
              {state.saveError && (
                <output className="flex items-center gap-2 bg-error-wash px-4 py-1 text-caption text-error animate-fade-in">
                  <Dot tone="error" />
                  <span className="flex-1">Threads could not be saved: {state.saveError}. UAM tries again with the next change.</span>
                </output>
              )}
              {notice && (
                <p className="flex items-center gap-2 bg-error-wash px-4 py-1.5 text-caption text-error animate-fade-in" role="alert">
                  <span className="flex-1">{notice}</span>
                  <Button size="icon" variant="ghost" aria-label="Dismiss" className="text-error hover:bg-error-wash hover:text-error" onClick={() => setNotice(null)}>
                    <X />
                  </Button>
                </p>
              )}
              <div className="relative flex min-h-0 flex-1 flex-col">
                {/* Notices float over the pane under the header bar, so nothing above them moves. */}
                <div className="pointer-events-none absolute top-14 left-1/2 z-30 flex max-w-[calc(100%-2rem)] -translate-x-1/2 flex-col items-center gap-2 *:pointer-events-auto">
                  {updated && (
                    <output className="flex items-center gap-3 rounded-full bg-raised py-1 pr-1 pl-3 text-caption text-body shadow-float animate-fade-in">
                      <Dot tone="accent" />
                      UAM was updated.
                      <Button size="sm" variant="primary" className="rounded-full" onClick={() => window.location.reload()}>
                        Reload
                      </Button>
                    </output>
                  )}
                  {stale && state.previousCached && (
                    <output className="flex items-center gap-2 rounded-full bg-raised px-3 py-1 text-caption text-muted shadow-float animate-fade-in">
                      <Dot tone="accent" /> Refreshing thread…
                    </output>
                  )}
                </div>
                {/* A cached page is presentation only; actions and typing wait for confirmation. Under a view the pane is skipped, not unmounted: content-visibility keeps its scroll, display:none would not. */}
                <div className={cn('flex min-h-0 flex-1 flex-col', !!view && '[content-visibility:hidden]')} inert={stale || !!view} aria-hidden={!!view || undefined} aria-busy={stale || undefined}>
                  <CoveredContext.Provider value={!!view}>
                    <Contained what="This view" of={shown?.id ?? newTask?.projectId ?? mismatched}><Suspense fallback={<ViewLoading leading={leading} />}>{pane}</Suspense></Contained>
                  </CoveredContext.Provider>
                </div>
                {/* Always mounted, so a view that opens in a transition waits here for its code instead of showing the fallback. */}
                <Suspense fallback={<div className="absolute inset-0 flex flex-col bg-canvas"><ViewLoading leading={leading} /></div>}>
                  {view && <div data-view="" className="absolute inset-0 flex flex-col bg-canvas"><Contained what="This view" of={settingsOpen ? 'settings' : routinesFor}>{view}</Contained></div>}
                </Suspense>
                {/* Settings, routines and a new Task do not wait on the stream; the skeleton's own loading line is its one ring. */}
                <LoadingVeil show={loading && !skeletonPane && !settingsOpen && !mismatched && !routinesShown && !newTask} />
              </div>
              {terminalDock.mounted && docked && <TerminalDock key={`${terminalOpens}:${docked.project.id}:${docked.task ?? ''}`} project={docked.project} thread={docked.task ? { id: docked.task, dir: state.sessions.find((s) => s.id === docked.task)?.workdir ?? docked.project.dir } : undefined} open={!!terminalProject} hidden={!taskPane} panel={terminalPanel} onClose={closeTerminal} onClosed={terminalDock.onClosed} />}
            </main>

            <NewTaskPalette
              open={paletteOpen && !mismatched}
              onOpenChange={setPaletteOpen}
              projects={state.projects}
              sessions={state.sessions}
              selectedId={state.selectedId}
              filter={filter}
              onPick={pickProject}
              onAddProject={actions.onAddProject}
              groups={machines?.map((m) => ({ key: m.id, label: m.label, projects: m.active ? state.projects : m.state.projects }))}
              start={machines && (federation?.filter && federation.filter.machine !== here ? { group: federation.filter.machine, project: federation.filter.project || undefined } : { group: here, project: paletteStart(state.projects, state.sessions, filter, state.selectedId)?.id })}
            />
            {dialog?.kind === 'add' && (
              <AddProjectDialog
                open={dialogOpen}
                onClose={() => setDialogOpen(false)}
                onClosed={() => setDialog(null)}
                machines={machines}
                choices={federation?.choices}
                onAdded={(p, machine = here) => {
                  // Another machine's stream brings its new Project to the lists.
                  if (machine !== here) return;
                  dispatch({ type: 'upsert_project', project: p });
                  select(null);
                }}
                onExisting={(id, machine = here) => (machine === here ? showProject(id) : onMachineFilter?.({ machine, project: id }))}
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
                  sweptMembership.current = null;
                  recentTasks.removeProject(id);
                  if (confirmedDetail.current?.project_id === id) confirmedDetail.current = null;
                  dispatch({ type: 'remove_project', id });
                }}
              />
            )}

            <AlertDialog
              open={taskDialogOpen && taskDialog?.kind === 'delete'}
              onOpenChange={(o) => !o && setTaskDialogOpen(false)}
              onClosed={() => setTaskDialog(null)}
              title={`Delete ${dialogTaskName}?`}
              description="This removes the thread from UAM and cannot be undone; the provider conversation on the host is untouched."
              confirmLabel="Delete thread"
              busy={dialogBusy}
              onConfirm={() => void confirmTaskDialog()}
            />
            <AlertDialog
              open={taskDialogOpen && taskDialog?.kind === 'close'}
              onOpenChange={(o) => !o && setTaskDialogOpen(false)}
              onClosed={() => setTaskDialog(null)}
              title="Close this conversation?"
              description="Closing disconnects the provider conversation. The thread and its conversation ID are kept, so nothing is deleted; sending another prompt reopens the same conversation. To interrupt the current turn without closing, use Stop instead."
              confirmLabel="Close conversation"
              danger={false}
              busy={dialogBusy}
              onConfirm={() => void confirmTaskDialog()}
            />
            <SettleGuard {...guard} />
            <TryModelDialog session={state.sessions.find((s) => s.id === tryModel) ?? null} onClose={() => setTryModel(null)} onRun={(model) => rerun(tryModel ?? '', model)} />
          </div>
        </TooltipProvider>
      </TaskActionsContext.Provider>
      </TaskList.Provider>
      </MessageOutcomesContext.Provider>
      </MessagePairsContext.Provider>
      </ModelCallsContext.Provider>
    </AppContext.Provider>
  );
}
