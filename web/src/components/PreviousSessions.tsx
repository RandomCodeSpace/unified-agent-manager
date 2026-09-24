import { useEffect, useRef, useState } from 'react';
import { api, describeError, providerLabel, type PreviousSession, type Project, type SessionDetail, type SessionSummary } from '../api';
import { Note, Spinner, useApp } from './common';
import { useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Dialog } from './ui/dialog';

/** One discovery request for the rail, independent of live task output and project count. */
export function usePreviousCounts(projects: Project[], sessions: SessionSummary[]) {
  const { meta } = useApp();
  const enabled = !!meta?.providers.some((p) => p.available && p.capabilities.import);
  const projectKey = projects.map((p) => `${p.id}:${p.dir}`).sort().join("\n");
  const linkedKey = sessions.map((s) => `${s.provider}:${s.conversation_id}`).sort().join("\n");
  const [counts, setCounts] = useState<Record<string, number>>({});
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    api.previousCounts().then((next) => { if (alive) setCounts(next); }).catch(() => { if (alive) setCounts({}); });
    return () => { alive = false; };
  }, [enabled, projectKey, linkedKey]);
  return counts;
}

export function PreviousSessionsEntry({ project, count }: { project: Project; count?: number }) {
  const { meta } = useApp();
  const [open, setOpen] = useState(false);
  const enabled = !!meta?.providers.some((p) => p.available && p.capabilities.import);
  if (!enabled) return null;
  return (
    <>
      <button type="button" data-nav="" className="flex min-h-8 w-full items-center rounded-sm px-2 text-left text-caption text-muted hover:bg-canvas hover:text-body focus-visible:-outline-offset-2 pointer-coarse:min-h-11" onClick={() => setOpen(true)}>
        Previous sessions{count === undefined ? "" : ` (${count})`}
      </button>
      {open && <PreviousSessionsDialog project={project} onClose={() => setOpen(false)} />}
    </>
  );
}

function PreviousSessionsDialog({ project, onClose }: { project: Project; onClose: () => void }) {
  const { meta, dispatch } = useApp();
  const actions = useTaskActions();
  const [sessions, setSessions] = useState<PreviousSession[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [refresh, setRefresh] = useState(0);
  const pending = useRef(false);
  const alive = useRef(true);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);
  useEffect(() => {
    let current = true;
    api.previous(project.id).then((list) => { if (current) setSessions(list); }).catch((e: unknown) => { if (current) setError(describeError(e)); });
    return () => { current = false; };
  }, [project.id, refresh]);

  async function importSession(s: PreviousSession) {
    if (pending.current) return;
    pending.current = true;
    setBusy(s.conversation_id);
    setError("");
    try {
      const task = await api.importPrevious(project.id, s.conversation_id);
      dispatch({ type: "upsert_session", session: task });
      if (alive.current) {
        onClose();
        actions.select(task.id);
      }
    } catch (e) {
      if (alive.current) setError(describeError(e));
    } finally {
      pending.current = false;
      if (alive.current) setBusy(null);
    }
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }} title={`Previous sessions in ${project.name}`} description="Import a recorded conversation as a task. Importing reads its history without sending a message."
      footer={<Button variant="secondary" disabled={!!busy || (sessions === null && !error)} onClick={() => { setSessions(null); setError(""); setRefresh((n) => n + 1); }}>Refresh</Button>}>
      {error && <Note role="alert" tone="error" className="mb-3">{error}</Note>}
      {sessions === null && !error && <Note role="status"><Spinner /> Loading previous sessions…</Note>}
      {sessions?.length === 0 && <Note>No previous sessions are available to import in this project.</Note>}
      {sessions && sessions.length > 0 && (
        <ul className="max-h-[60dvh] divide-y divide-hairline overflow-y-auto">
          {sessions.map((s) => (
            <li key={`${s.provider}:${s.conversation_id}`} className="flex items-center gap-3 py-3">
              <div className="min-w-0 flex-1">
                <p className="break-words text-ui text-ink">{s.title || 'Untitled session'}</p>
                <p className="mt-1 text-caption text-muted">{providerLabel(meta, s.provider)} · <time dateTime={s.updated_at}>{new Date(s.updated_at).toLocaleString()}</time></p>
                {s.in_use && <Note>In use by another client. Close it there, then refresh.</Note>}
              </div>
              <Button size="sm" variant="secondary" disabled={s.in_use || !!busy} aria-label={`Import ${s.title || 'untitled session'}`} onClick={() => void importSession(s)}>
                {busy === s.conversation_id ? 'Importing…' : 'Import'}
              </Button>
            </li>
          ))}
        </ul>
      )}
    </Dialog>
  );
}

/** Reading or retrying history never reopens a Task or changes its settings. */
export function HistoryStatus({ session }: { session: SessionDetail }) {
  const { dispatch } = useApp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [ready, setReady] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (session.history !== 'unavailable') return;
    const timer = window.setTimeout(() => setReady(true), 60_000);
    return () => window.clearTimeout(timer);
  }, [session.history, session.history_reason, attempt]);
  async function reload() {
    setBusy(true);
    setError('');
    setReady(false);
    setAttempt((n) => n + 1);
    try { dispatch({ type: 'detail_loaded', detail: await api.session(session.id) }); }
    catch (e) { setError(describeError(e)); }
    finally { setBusy(false); }
  }
  if (session.history === 'loading') return <Note role="status"><Spinner /> Loading recorded history…</Note>;
  if (session.history !== 'unavailable') return null;
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
      <Note role="status">{error || session.history_reason || 'Recorded history is unavailable.'}{!ready && !busy && ' You can retry in a minute.'}</Note>
      <Button size="sm" disabled={busy || !ready} onClick={() => void reload()}>{busy ? 'Retrying…' : 'Retry history'}</Button>
    </div>
  );
}
