import { FolderPlus, Menu as MenuIcon, SquarePen, X } from 'lucide-react';
import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { UPDATE_EVENTS, api, describeError, newRequestId, onUnauthorized, provider, resolveTaskDefaults, type Meta, type Project, type SessionSummary, type SnapshotData, type UpdateData } from './api';
import { initialState, reducer } from './state';
import { AppContext, Dot, useMedia } from './components/common';
import { Login } from './components/Login';
import { AddProjectDialog, EditProjectDialog, RemoveProjectDialog } from './components/Projects';
import { Brand, CONNECTION_TEXT, Sidebar, type WorkspaceActions } from './components/Sidebar';
import { mostRecentProject, tasksOf } from './lib/tasks';
import { Task } from './components/Task';
import { TaskActionsContext, type Renaming, type TaskActions } from './components/taskActions';
import { Button } from './components/ui/button';
import { AlertDialog, Sheet } from './components/ui/dialog';
import { TooltipProvider } from './components/ui/tooltip';

type Auth = 'checking' | 'in' | 'out';
type ProjectDialog = { kind: 'add' } | { kind: 'edit'; project: Project } | { kind: 'remove'; project: Project } | null;
type TaskDialog = { kind: 'archive' | 'delete' | 'close'; id: string } | null;

/** Below this the sidebar is a drawer (DESIGN.md breakpoints). */
const NARROW = '(max-width: 959px)';
/** From this width the Changes sheet sits beside the column instead of over it. */
const SHEET_INLINE = '(min-width: 1280px)';
const HIDDEN_KEY = 'uam.hiddenProjects';
const VIEWED_KEY = 'uam.viewed';
const HASH_PREFIX = '#task=';

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

function initialSelection(): string | null {
  const h = window.location.hash;
  return h.startsWith(HASH_PREFIX) ? decodeURIComponent(h.slice(HASH_PREFIX.length)) || null : null;
}

export default function App() {
  const [auth, setAuth] = useState<Auth>('checking');
  const [authRequired, setAuthRequired] = useState(true);
  const [state, dispatch] = useReducer(reducer, initialState, (s) => ({ ...s, selectedId: initialSelection() }));
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
  const [busyTask, setBusyTask] = useState<string | null>(null);
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set(readJSON<string[]>(HIDDEN_KEY, [])));
  const [viewed, setViewed] = useState<Record<string, string>>(() => readJSON(VIEWED_KEY, {}));
  const loadedAt = useRef(new Date().toISOString());
  // One create per project at a time; the request ID survives a failure so a retry is idempotent.
  const creating = useRef(new Map<string, { id: string; busy: boolean }>());
  // Task whose composer takes focus once its detail arrives (a Task just created).
  const focusTask = useRef<string | null>(null);

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

  useEffect(() => {
    if (auth !== 'in') return;
    api.meta().then(setMeta).catch(() => setMeta(null));
  }, [auth]);

  // Keep the selected task in the URL fragment so a reload lands on it.
  useEffect(() => {
    const id = state.selectedId;
    const next = id ? `${HASH_PREFIX}${encodeURIComponent(id)}` : '';
    if (window.location.hash !== next) history.replaceState(null, '', `${window.location.pathname}${window.location.search}${next}`);
  }, [state.selectedId]);

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
    es.onopen = () => dispatch({ type: 'connection', status: 'connected' });
    es.onerror = () => {
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
    es.addEventListener('snapshot', (e) => {
      const data = JSON.parse((e as MessageEvent).data) as SnapshotData;
      dispatch({ type: 'snapshot', data });
      if (data.session && data.session.id === selected) markViewed(selected, data.session.updated_at);
    });
    for (const name of UPDATE_EVENTS) {
      es.addEventListener(name, (e) => {
        const data = { name, ...JSON.parse((e as MessageEvent).data) } as UpdateData;
        dispatch({ type: 'update', data });
        if (data.name === 'session' && data.session.id === selected) markViewed(selected, data.session.updated_at);
      });
    }
    return () => {
      es.close();
      window.clearTimeout(retry);
    };
  }, [auth, state.selectedId, streamKey, markViewed]);

  const hasNews = useCallback(
    (s: SessionSummary) => {
      if (s.id === state.selectedId) return false;
      const seen = viewed[s.id] ?? loadedAt.current;
      return s.updated_at > seen;
    },
    [viewed, state.selectedId],
  );

  const ctx = useMemo(() => ({ meta, dispatch, narrow, hasNews }), [meta, narrow, hasNews]);

  // Focus the composer of a Task that was just created, once its detail is on screen.
  const detailId = state.detail?.id;
  useEffect(() => {
    if (!detailId || detailId !== focusTask.current) return;
    focusTask.current = null;
    document.getElementById('composer-text')?.focus();
  }, [detailId]);

  const openDialog = useCallback((d: Exclude<ProjectDialog, null>) => {
    setDialog(d);
    setDialogOpen(true);
  }, []);
  const openTaskDialog = useCallback((d: Exclude<TaskDialog, null>) => {
    setTaskDialog(d);
    setTaskDialogOpen(true);
  }, []);

  const select = useCallback((id: string | null) => {
    dispatch({ type: 'select', id });
    setNotice(null);
    setSheetOpen(false);
    setDrawerOpen(false);
  }, []);

  /** New task: create it at once with the Project's defaults, no prompt or name, and open its chat. */
  const startTask = useCallback(
    async (projectId: string) => {
      const entry = creating.current.get(projectId) ?? { id: newRequestId(), busy: false };
      if (entry.busy) return;
      creating.current.set(projectId, { ...entry, busy: true });
      setNotice(null);
      try {
        const project = state.projects.find((p) => p.id === projectId);
        const settings = project && resolveTaskDefaults(meta, project.defaults);
        if (!project || !settings) throw new Error(meta ? 'No provider is available.' : 'The provider list has not loaded yet.');
        const info = provider(meta, settings.provider);
        if (info && !info.available) throw new Error(`${info.display_name} is unavailable: ${info.reason || 'not installed'}`);
        const s = await api.createSession({ project_id: projectId, ...settings, model: settings.model || undefined, request_id: entry.id });
        creating.current.delete(projectId);
        focusTask.current = s.id;
        dispatch({ type: 'upsert_session', session: s });
        select(s.id);
      } catch (e) {
        creating.current.set(projectId, { ...entry, busy: false });
        setNotice(`Could not start a task: ${describeError(e)}`);
      }
    },
    [state.projects, meta, select],
  );

  /** Runs one lifecycle request; the result is dispatched, a failure becomes the notice line. */
  const runTask = useCallback(async (id: string, op: () => Promise<SessionSummary | void>, verb: string) => {
    setBusyTask(id);
    setNotice(null);
    try {
      const s = await op();
      if (s) dispatch({ type: 'upsert_session', session: s });
    } catch (e) {
      setNotice(`Could not ${verb}: ${describeError(e)}`);
      throw e;
    } finally {
      setBusyTask(null);
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
      busy: busyTask,
    }),
    [renaming, busyTask, select, runTask, state.sessions, openTaskDialog],
  );

  const actions: WorkspaceActions = useMemo(
    () => ({
      onHome: () => select(null),
      onNewTask: (projectId) => void startTask(projectId),
      onAddProject: () => openDialog({ kind: 'add' }),
      onEditProject: (project) => openDialog({ kind: 'edit', project }),
      onRemoveProject: (project) => openDialog({ kind: 'remove', project }),
      collapsed: hidden,
      onToggleProject: (id) =>
        setHidden((h) => {
          const next = new Set(h);
          if (next.has(id)) next.delete(id);
          else next.add(id);
          localStorage.setItem(HIDDEN_KEY, JSON.stringify([...next]));
          return next;
        }),
    }),
    [hidden, startTask, select, openDialog],
  );

  if (auth === 'checking') {
    return (
      <main className="grid h-dvh place-items-center text-caption text-muted" aria-busy="true">
        <Brand className="animate-pulse-dot" />
      </main>
    );
  }
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />;

  const selected = state.sessions.find((s) => s.id === state.selectedId) ?? null;
  const project = selected ? state.projects.find((p) => p.id === selected.project_id) : undefined;
  const dialogTask = taskDialog ? state.sessions.find((s) => s.id === taskDialog.id) : undefined;
  const recent = mostRecentProject(state.projects, state.sessions, state.selectedId);

  function showProject(id: string) {
    setHidden((h) => {
      if (!h.has(id)) return h;
      const next = new Set(h);
      next.delete(id);
      localStorage.setItem(HIDDEN_KEY, JSON.stringify([...next]));
      return next;
    });
    select(null);
  }

  async function logout() {
    try {
      await api.logout();
    } finally {
      setAuth('out');
    }
  }

  async function confirmTaskDialog() {
    if (!taskDialog) return;
    const { kind, id } = taskDialog;
    try {
      if (kind === 'archive') await runTask(id, () => api.stage(id, 'archive'), 'archive the task');
      else if (kind === 'close') await runTask(id, () => api.close(id), 'close the conversation');
      else {
        await runTask(id, () => api.deleteSession(id), 'delete the task');
        dispatch({ type: 'remove_session', id });
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
      onLogout={() => void logout()}
      connection={state.connection}
      version={meta?.version}
    />
  );

  // On narrow screens the header carries the drawer button; the Task pane renders its own header.
  const menuButton = narrow ? (
    <Button size="icon-md" aria-label="Projects" aria-expanded={drawerOpen} className="-ml-1 text-muted" onClick={() => setDrawerOpen((o) => !o)}>
      <MenuIcon />
    </Button>
  ) : null;

  let pane: React.ReactNode;
  if (state.detail && selected) {
    pane = (
      <Task
        key={state.detail.id}
        session={state.detail}
        project={project}
        agents={state.agents}
        snapshotSeq={state.snapshotSeq}
        sheetOpen={sheetOpen}
        sidePanelInline={sheetInline}
        onSheet={(open) => {
          setSheetOpen(open);
          if (!open) document.getElementById('changes-link')?.focus();
        }}
        onSessionUpdate={(s) => dispatch({ type: 'upsert_session', session: s })}
        onInteractionUpdate={(sessionId, interaction) => dispatch({ type: 'upsert_interaction', sessionId, interaction })}
        leading={menuButton}
      />
    );
  } else if (state.selectedId && state.snapshotSeq >= 0 && !selected) {
    pane = (
      <EmptyPane leading={menuButton} connection={state.connection} narrow={narrow}>
        <h1 className="text-display-md">This task no longer exists.</h1>
        <p className="text-ui text-muted">It was deleted, or its project was removed.</p>
        <Button variant="secondary" onClick={() => select(null)}>
          Back to projects
        </Button>
      </EmptyPane>
    );
  } else if (state.selectedId) {
    pane = <LoadingPane leading={menuButton} />;
  } else {
    pane = (
      <EmptyPane leading={menuButton} connection={state.connection} narrow={narrow}>
        <Brand markOnly className="[&_svg]:size-9 opacity-80" />
        {recent ? (
          <>
            <h1 className="text-display-md">Ready when you are.</h1>
            <p className="max-w-sm text-ui text-muted">Open a task from the sidebar, or start a new one. Settled and archived tasks keep their full conversation there too.</p>
            <div className="mt-2 flex flex-wrap items-center justify-center gap-2">
              <Button variant="primary" onClick={() => void startTask(recent.id)}>
                <SquarePen />
                New task in {recent.name}
              </Button>
              <Button onClick={() => openDialog({ kind: 'add' })}>
                <FolderPlus />
                Add project
              </Button>
            </div>
          </>
        ) : (
          <>
            <h1 className="text-display-md">Add a project to begin.</h1>
            <p className="max-w-sm text-ui text-muted">A project is a directory on this host. Tasks run inside it with the defaults you choose.</p>
            <Button variant="primary" className="mt-2" onClick={() => openDialog({ kind: 'add' })}>
              <FolderPlus />
              Add project
            </Button>
          </>
        )}
      </EmptyPane>
    );
  }

  return (
    <AppContext.Provider value={ctx}>
      <TaskActionsContext.Provider value={taskActions}>
        <TooltipProvider delay={400} closeDelay={0}>
          <div className={narrow ? 'grid h-dvh grid-cols-1' : 'grid h-dvh grid-cols-[264px_minmax(0,1fr)]'}>
            {!narrow && <aside className="min-h-0">{sidebar}</aside>}
            {narrow && (
              <Sheet open={drawerOpen} onOpenChange={setDrawerOpen} side="left" label="Projects">
                {sidebar}
              </Sheet>
            )}
            <main className="relative flex min-h-0 min-w-0 flex-col bg-canvas">
              {notice && (
                <p className="flex items-center gap-2 border-b border-hairline bg-error-wash px-4 py-1.5 text-caption text-error" role="alert">
                  <span className="flex-1">{notice}</span>
                  <Button size="icon" variant="ghost" aria-label="Dismiss" className="text-error hover:bg-error-wash hover:text-error" onClick={() => setNotice(null)}>
                    <X />
                  </Button>
                </p>
              )}
              {pane}
            </main>

            {sheetOpen && !sheetInline && (
              <button
                type="button"
                className="fixed inset-0 z-30 bg-backdrop animate-fade-in"
                aria-label="Close"
                onClick={() => setSheetOpen(false)}
              />
            )}

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
              <EditProjectDialog open={dialogOpen} onClose={() => setDialogOpen(false)} onClosed={() => setDialog(null)} project={dialog.project} onUpdated={(p) => dispatch({ type: 'upsert_project', project: p })} />
            )}
            {dialog?.kind === 'remove' && (
              <RemoveProjectDialog
                open={dialogOpen}
                onClose={() => setDialogOpen(false)}
                onClosed={() => setDialog(null)}
                project={dialog.project}
                tasks={tasksOf(state.sessions, dialog.project.id)}
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
              busy={!!busyTask}
              onConfirm={() => void confirmTaskDialog()}
            />
            <AlertDialog
              open={taskDialogOpen && taskDialog?.kind === 'delete'}
              onOpenChange={(o) => !o && setTaskDialogOpen(false)}
              onClosed={() => setTaskDialog(null)}
              title={`Delete ${dialogTask ? `“${dialogTask.name || dialogTask.title || 'this task'}”` : 'this task'}?`}
              description="This removes the task record from UAM. The provider conversation on the host is untouched."
              confirmLabel="Delete task"
              busy={!!busyTask}
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
              busy={!!busyTask}
              onConfirm={() => void confirmTaskDialog()}
            />
          </div>
        </TooltipProvider>
      </TaskActionsContext.Provider>
    </AppContext.Provider>
  );
}

/** The narrow layout's header for the non-Task views: drawer button, brand, connection. */
function PaneHeader({ leading, connection }: { leading: React.ReactNode; connection?: keyof typeof CONNECTION_TEXT }) {
  return (
    <header className="flex h-header shrink-0 items-center gap-2 border-b border-hairline px-3">
      {leading}
      <Brand />
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

function EmptyPane({ leading, connection, narrow, children }: { leading: React.ReactNode; connection: keyof typeof CONNECTION_TEXT; narrow: boolean; children: React.ReactNode }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {narrow && <PaneHeader leading={leading} connection={connection} />}
      <div className="flex flex-1 flex-col items-center justify-center gap-3 px-6 pb-16 text-center animate-rise">{children}</div>
    </div>
  );
}

/** Calm placeholder while the selected Task's detail is on its way: the header holds its height, three quiet lines below. */
function LoadingPane({ leading }: { leading: React.ReactNode }) {
  return (
    <div className="flex min-h-0 flex-1 flex-col" aria-busy="true" aria-label="Loading conversation">
      <header className="flex h-header shrink-0 items-center gap-2 border-b border-hairline px-3">
        {leading}
        <span className="h-3.5 w-40 rounded-xs bg-sunken animate-pulse-dot" />
      </header>
      <div className="flex flex-col gap-3 px-6 pt-8">
        <span className="ml-auto h-9 w-2/5 rounded-lg bg-raised animate-pulse-dot" />
        <span className="mt-4 h-3 w-3/5 rounded-xs bg-sunken animate-pulse-dot" />
        <span className="h-3 w-4/5 rounded-xs bg-sunken animate-pulse-dot" />
        <span className="h-3 w-1/2 rounded-xs bg-sunken animate-pulse-dot" />
      </div>
    </div>
  );
}
