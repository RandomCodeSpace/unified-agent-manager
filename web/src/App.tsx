import { useEffect, useReducer, useState } from 'react';
import { api, onUnauthorized, UPDATE_EVENTS, type Meta, type SessionSummary } from './api';
import { initialState, reducer, type Connection } from './state';
import { Changes } from './components/Changes';
import { Conversation } from './components/Conversation';
import { Login } from './components/Login';
import { Sidebar } from './components/Sidebar';

type Auth = 'checking' | 'in' | 'out';
type Drawer = 'sidebar' | 'changes' | null;

const NARROW = '(max-width: 900px)';
const CHANGES_PREF = 'uam.changesOpen';

const CONNECTION_TEXT: Record<Connection, string> = {
  connecting: 'Connecting…',
  connected: 'Connected',
  reconnecting: 'Connection lost — reconnecting. Closing this page does not stop work on the server',
  offline: 'Offline — retrying. Closing this page does not stop work on the server',
};

// Older servers omit `required`; treat absent as true.
const loggedIn = (r: { authenticated: boolean; required?: boolean }) => r.authenticated || r.required === false;

export default function App() {
  const [auth, setAuth] = useState<Auth>('checking');
  const [authRequired, setAuthRequired] = useState(true);
  const [state, dispatch] = useReducer(reducer, initialState);
  const [meta, setMeta] = useState<Meta | null>(null);
  const [streamKey, setStreamKey] = useState(0);
  const [narrow, setNarrow] = useState(() => window.matchMedia(NARROW).matches);
  const [drawer, setDrawer] = useState<Drawer>(null);
  const [changesOpen, setChangesOpen] = useState(() => localStorage.getItem(CHANGES_PREF) !== '0');

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

  useEffect(() => {
    const m = window.matchMedia(NARROW);
    const onChange = () => setNarrow(m.matches);
    m.addEventListener('change', onChange);
    return () => m.removeEventListener('change', onChange);
  }, []);

  // Esc closes an open drawer (dialogs handle their own Esc).
  useEffect(() => {
    if (!drawer) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !document.querySelector('dialog[open]')) setDrawer(null);
    };
    document.addEventListener('keydown', onKey);
    return () => document.removeEventListener('keydown', onKey);
  }, [drawer]);

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
      es.addEventListener(name, (e) =>
        dispatch({ type: 'update', data: { name, ...JSON.parse((e as MessageEvent).data) } }),
      );
    }
    return () => {
      es.close();
      window.clearTimeout(retry);
    };
  }, [auth, state.selectedId, streamKey]);

  if (auth === 'checking') return <main className="login muted">Loading…</main>;
  if (auth === 'out') return <Login onLoggedIn={() => setAuth('in')} />;

  const showSidebar = narrow ? drawer === 'sidebar' : true;
  const showChanges = narrow ? drawer === 'changes' : changesOpen;
  const selected = state.sessions.find((s) => s.id === state.selectedId) ?? null;

  function toggleChanges() {
    if (narrow) {
      setDrawer(drawer === 'changes' ? null : 'changes');
      return;
    }
    const next = !changesOpen;
    setChangesOpen(next);
    localStorage.setItem(CHANGES_PREF, next ? '1' : '0');
  }

  function select(id: string) {
    dispatch({ type: 'select', id });
    setDrawer(null);
  }

  function upsertSession(s: SessionSummary) {
    dispatch({ type: 'upsert_session', session: s });
  }

  async function logout() {
    try {
      await api.logout();
    } finally {
      setAuth('out');
    }
  }

  return (
    <div className={narrow ? 'app narrow' : 'app'}>
      <header className="topbar">
        {narrow && (
          <button
            type="button"
            className="btn small"
            aria-expanded={drawer === 'sidebar'}
            onClick={() => setDrawer(drawer === 'sidebar' ? null : 'sidebar')}
          >
            Sessions
          </button>
        )}
        <h1>UAM</h1>
        <span className={`conn conn-${state.connection}`} role="status">
          <span className="dot" aria-hidden="true" />
          {CONNECTION_TEXT[state.connection]}
        </span>
        <span className="spacer" />
        {selected && (
          <button type="button" className="btn small" aria-pressed={showChanges} onClick={toggleChanges}>
            Changes
          </button>
        )}
        {authRequired && (
          <button type="button" className="btn small" onClick={() => void logout()}>
            Log out
          </button>
        )}
      </header>

      {showSidebar && (
        <aside className="sidebar" aria-label="Sessions">
          <Sidebar
            sessions={state.sessions}
            selectedId={state.selectedId}
            meta={meta}
            onSelect={select}
            onCreated={(s) => {
              upsertSession(s);
              select(s.id);
            }}
          />
        </aside>
      )}

      <main className="main">
        {state.detail ? (
          <Conversation
            session={state.detail}
            meta={meta}
            onSessionUpdate={upsertSession}
            onInteractionUpdate={(sessionId, interaction) =>
              dispatch({ type: 'upsert_interaction', sessionId, interaction })
            }
          />
        ) : (
          <div className="empty">
            {!state.selectedId ? (
              <>
                <p>Select a session or create a new one.</p>
                <p className="muted">Work continues on the server when you close this page.</p>
              </>
            ) : state.snapshotSeq >= 0 && !selected ? (
              <>
                <p>This session no longer exists.</p>
                <button type="button" className="btn" onClick={() => dispatch({ type: 'select', id: null })}>
                  Back to sessions
                </button>
              </>
            ) : (
              <p className="muted">Loading conversation…</p>
            )}
          </div>
        )}
      </main>

      {showChanges && selected && (
        <aside className="changes" aria-label="Changes">
          <Changes key={selected.id} session={selected} />
        </aside>
      )}

      {narrow && drawer && <button type="button" className="backdrop" aria-label="Close panel" onClick={() => setDrawer(null)} />}
    </div>
  );
}
