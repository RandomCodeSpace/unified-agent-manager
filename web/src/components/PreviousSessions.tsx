import { useEffect, useRef, useState } from 'react';
import { api, describeError, providerLabel, type Meta, type PreviousSession, type Project, type SessionDetail } from '../api';
import { Note, Spinner, useApp } from './common';
import { useTaskActions } from './taskActions';
import { Button } from './ui/button';
import { Dialog } from './ui/dialog';

/** An available provider can import its recorded conversations; Edit project offers Previous sessions only then. */
export const canImport = (meta: Meta | null): boolean => !!meta?.providers.some((p) => p.available && p.capabilities.import);

export function PreviousSessionsDialog({ project, onClose }: { project: Project; onClose: () => void }) {
  const { meta, dispatch } = useApp();
  const actions = useTaskActions();
  const [sessions, setSessions] = useState<PreviousSession[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [refresh, setRefresh] = useState(0);
  /** "Import all" progress: how many are done of how many, while it runs. */
  const [progress, setProgress] = useState<{ done: number; total: number } | null>(null);
  const [stopping, setStopping] = useState(false);
  const stop = useRef(false);
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

  // The rows Import is enabled for, one after another (the host checks each conversation's holder); a failure
  // is noted and the rest go on, Cancel stops after the current one. Nothing opens; the list reloads at the end.
  const importable = (sessions ?? []).filter((s) => !s.in_use);
  async function importAll() {
    if (pending.current || !importable.length) return;
    pending.current = true;
    stop.current = false;
    setStopping(false);
    setBusy('all');
    setError("");
    const failed: string[] = [];
    let done = 0;
    for (const s of importable) {
      if (stop.current) break;
      setProgress({ done, total: importable.length });
      try {
        dispatch({ type: "upsert_session", session: await api.importPrevious(project.id, s.conversation_id) });
      } catch (e) {
        failed.push(`${s.title || 'Untitled session'}: ${describeError(e)}`);
      }
      done++;
    }
    pending.current = false;
    if (!alive.current) return;
    setProgress(null);
    setBusy(null);
    setError(failed.length ? `${failed.length} of ${done} could not be imported. ${failed.join(' · ')}` : "");
    setSessions(null);
    setRefresh((n) => n + 1);
  }

  return (
    <Dialog open onOpenChange={(open) => { if (!open) onClose(); }} title={`Previous sessions in ${project.name}`} description="Import a recorded conversation as a task. Importing reads its history without sending a message."
      footer={<>
        {progress && <Note role="status" className="mr-auto">Importing {progress.done + 1} of {progress.total}…</Note>}
        {progress && <Button variant="secondary" disabled={stopping} onClick={() => { stop.current = true; setStopping(true); }}>Cancel</Button>}
        {(progress || importable.length > 1) && (
          <Button variant="secondary" loading={!!progress} disabled={!!busy} onClick={() => void importAll()}>Import all ({progress?.total ?? importable.length})</Button>
        )}
        <Button variant="secondary" disabled={!!busy || (sessions === null && !error)} onClick={() => { setSessions(null); setError(""); setRefresh((n) => n + 1); }}>Refresh</Button>
      </>}>
      {error && <Note role="alert" tone="error" className="mb-3">{error}</Note>}
      {sessions === null && !error && <Note role="status"><Spinner /> Loading previous sessions…</Note>}
      {sessions?.length === 0 && <Note>No previous sessions are available to import in this project.</Note>}
      {sessions && sessions.length > 0 && (
        <ul className="flex max-h-[60dvh] flex-col gap-1 overflow-y-auto">
          {sessions.map((s) => (
            <li key={`${s.provider}:${s.conversation_id}`} className="flex items-center gap-3 rounded-md bg-tint-well px-3 py-2.5">
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
      <Button size="sm" loading={busy} disabled={!ready} onClick={() => void reload()}>Retry history</Button>
    </div>
  );
}
