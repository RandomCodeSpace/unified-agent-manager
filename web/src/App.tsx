import { useCallback, useEffect, useMemo, useReducer, useRef, useState } from 'react';
import { UPDATE_EVENTS, api, onUnauthorized, type Meta, type Project, type SessionSummary } from './api';
import { initialState, reducer } from './state';
import { AppContext, useMedia } from './components/common';
import { Deck } from './components/Deck';
import { Login } from './components/Login';
import { NewTask } from './components/NewTask';
import { AddProjectDialog, RemoveProjectDialog, RenameProjectDialog } from './components/Projects';
import { CONNECTION_TEXT, Rail, tasksOf, type WorkspaceActions } from './components/Rail';
import { Task } from './components/Task';

type Auth = 'checking' | 'in' | 'out';
type ProjectDialog = { kind: 'add' } | { kind: 'rename'; project: Project } | { kind: 'remove'; project: Project } | null;

/** Below this the rail is a drawer (DESIGN.md breakpoints). */
const NARROW = '(max-width: 959px)';
/** From this width the Changes sheet sits beside the column instead of over it. */
const SHEET_INLINE = '(min-width: 1280px)';
const HIDDEN_KEY = 'uam.hiddenProjects';
const VIEWED_KEY = 'uam.viewed';
const HASH_PREFIX = '#task=';

// Older servers omit `required`; treat absent as true.
const loggedIn = (r: { authenticated: boolean; required?: boolean }) => r.authenticated || r.required === false;

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
  const [newTaskIn, setNewTaskIn] = useState<string | null>(null);
  const [sheetOpen, setSheetOpen] = useState(false);
  const [drawerOpen, setDrawerOpen] = useState(false);
  const [dialog, setDialog] = useState<ProjectDialog>(null);
  const [highlightId, setHighlightId] = useState<string | null>(null);
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set(readJSON<string[]>(HIDDEN_KEY, [])));
  const [viewed, setViewed] = useState<Record<string, string>>(() => readJSON(VIEWED_KEY, {}));
  const loadedAt = useRef(new Date().toISOString());
  const drawerButton = useRef<HTMLButtonElement>(null);

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

  // Esc closes the sheet, then the drawer; native dialogs handle their own Esc.
  useEffect(() => {
    if (!sheetOpen && !drawerOpen) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape' || document.querySelector('dialog[open]')) return;
      if (sheetOpen) {
        setSheetOpen(false);
        document.getElementById('changes-link')?.focus();
      } else {
        setDrawerOpen(false);
        drawerButton.current?.focus();
      }
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [sheetOpen, drawerOpen]);

  // Move focus into the drawer when it opens.
  useEffect(() => {
    if (drawerOpen) document.querySelector<HTMLElement>('.rail-drawer button')?.focus();
  }, [drawerOpen]);

  // The selected task counts as viewed as long as it is on screen.
  const detailUpdated = state.detail?.updated_at;
  useEffect(() => {
    const id = state.selectedId;
    if (!id || !detailUpdated) return;
    setViewed((v) => {
      if (v[id] === detailUpdated) return v;
      const next = { ...v, [id]: detailUpdated };
      localStorage.setItem(VIEWED_KEY, JSON.stringify(next));
      return next;
    });
  }, [state.selectedId, detailUpdated]);

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
    es.addEventListener('snapshot', (e) => dispatch({ type: 'snapshot', data: JSON.parse((e as MessageEvent).data) }));
    for (const name of UPDATE_EVENTS) {
      es.addEventListener(name, (e) => dispatch({ type: 'update', data: { name, ...JSON.parse((e as MessageEvent).data) } }));
    }
    return () => {
      es.close();
      window.clearTimeout(retry);
    };
  }, [auth, state.selectedId, streamKey]);

  const hasNews = useCallback(
    (s: SessionSummary) => {
      if (s.id === state.selectedId) return false;
      const seen = viewed[s.id] ?? loadedAt.current;
      return s.updated_at > seen;
    },
    [viewed, state.selectedId],
  );

  const ctx = useMemo(() => ({ meta, dispatch, narrow, hasNews }), [meta, narrow, hasNews]);

  const actions: WorkspaceActions = useMemo(
    () => ({
      onSelect: (id) => {
        dispatch({ type: 'select', id });
        setNewTaskIn(null);
        setSheetOpen(false);
        setDrawerOpen(false);
        setHighlightId(null);
      },
      onHome: () => {
        dispatch({ type: 'select', id: null });
        setNewTaskIn(null);
        setSheetOpen(false);
        setDrawerOpen(false);
      },
      onNewTask: (projectId) => {
        dispatch({ type: 'select', id: null });
        setNewTaskIn(projectId);
        setSheetOpen(false);
        setDrawerOpen(false);
      },
      onAddProject: () => setDialog({ kind: 'add' }),
      onRenameProject: (project) => setDialog({ kind: 'rename', project }),
      onRemoveProject: (project) => setDialog({ kind: 'remove', project }),
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
    [hidden],
  );

  if (auth === 'checking') return <main className="login caption">Loading…</main>;
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />;

  const selected = state.sessions.find((s) => s.id === state.selectedId) ?? null;
  const project = selected ? state.projects.find((p) => p.id === selected.project_id) : undefined;
  const newTaskProject = newTaskIn ? state.projects.find((p) => p.id === newTaskIn) : undefined;
  const showRail = narrow ? drawerOpen : true;

  function upsertSession(s: SessionSummary) {
    dispatch({ type: 'upsert_session', session: s });
  }

  function showProject(id: string) {
    setHidden((h) => {
      if (!h.has(id)) return h;
      const next = new Set(h);
      next.delete(id);
      localStorage.setItem(HIDDEN_KEY, JSON.stringify([...next]));
      return next;
    });
    actions.onHome();
    setHighlightId(id);
  }

  async function logout() {
    try {
      await api.logout();
    } finally {
      setAuth('out');
    }
  }

  // On narrow screens the header carries the drawer button; the Task pane renders its own header.
  const menuButton = narrow ? (
    <button
      ref={drawerButton}
      type="button"
      className="btn btn-icon"
      aria-label="Projects"
      aria-expanded={drawerOpen}
      onClick={() => setDrawerOpen((o) => !o)}
    >
      <span aria-hidden="true">☰</span>
    </button>
  ) : null;

  // The task pane manages its own scrolling; every other view scrolls inside `.page`.
  let page: React.ReactNode;
  let pane: React.ReactNode = null;
  if (newTaskProject) {
    page = (
      <NewTask
        project={newTaskProject}
        onCreated={(s) => {
          upsertSession(s);
          actions.onSelect(s.id);
        }}
        onCancel={actions.onHome}
      />
    );
  } else if (state.detail && selected) {
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
        onSessionUpdate={upsertSession}
        onDeleted={(id) => dispatch({ type: 'remove_session', id })}
        onInteractionUpdate={(sessionId, interaction) => dispatch({ type: 'upsert_interaction', sessionId, interaction })}
        leading={menuButton}
      />
    );
  } else if (state.selectedId && state.snapshotSeq >= 0 && !selected) {
    page = (
      <div className="page-column empty">
        <h1 className="display-md">This task no longer exists.</h1>
        <button type="button" className="btn btn-secondary" onClick={actions.onHome}>
          Back to projects
        </button>
      </div>
    );
  } else if (state.selectedId) {
    page = (
      <div className="page-column empty">
        <p className="caption">Loading conversation…</p>
      </div>
    );
  } else {
    page = <Deck projects={state.projects} sessions={state.sessions} actions={actions} highlightId={highlightId} />;
  }

  return (
    <AppContext.Provider value={ctx}>
      <div className={narrow ? 'shell narrow' : 'shell'}>
        {showRail && (
          <div className={narrow ? 'rail-drawer' : 'rail-col'}>
            <Rail
              projects={state.projects}
              sessions={state.sessions}
              selectedId={state.selectedId}
              actions={actions}
              authRequired={authRequired}
              onLogout={() => void logout()}
              connection={state.connection}
            />
          </div>
        )}
        <main className="main">
          {narrow && !pane && (
            <header className="main-header">
              {menuButton}
              <h1 className="task-title">{newTaskProject ? 'New task' : 'uam'}</h1>
              <span className="spacer" />
              {state.connection !== 'connected' && (
                <span className={`conn conn-${state.connection}`} role="status" title={CONNECTION_TEXT[state.connection]}>
                  <span className="mark-dot" aria-hidden="true" />
                  <span className="sr-only">{CONNECTION_TEXT[state.connection]}</span>
                </span>
              )}
            </header>
          )}
          {pane ?? <div className="page">{page}</div>}
        </main>

        {((sheetOpen && !sheetInline) || (narrow && drawerOpen)) && (
          <button
            type="button"
            className="scrim"
            aria-label="Close"
            onClick={() => {
              setSheetOpen(false);
              setDrawerOpen(false);
            }}
          />
        )}

        {dialog?.kind === 'add' && (
          <AddProjectDialog
            onAdded={(p) => {
              dispatch({ type: 'upsert_project', project: p });
              showProject(p.id);
            }}
            onExisting={showProject}
            onClose={() => setDialog(null)}
          />
        )}
        {dialog?.kind === 'rename' && (
          <RenameProjectDialog
            project={dialog.project}
            onRenamed={(p) => dispatch({ type: 'upsert_project', project: p })}
            onClose={() => setDialog(null)}
          />
        )}
        {dialog?.kind === 'remove' && (
          <RemoveProjectDialog
            project={dialog.project}
            tasks={tasksOf(state.sessions, dialog.project.id)}
            onRemoved={(id) => {
              dispatch({ type: 'remove_project', id });
              if (newTaskIn === id) setNewTaskIn(null);
            }}
            onClose={() => setDialog(null)}
          />
        )}
      </div>
    </AppContext.Provider>
  );
}
