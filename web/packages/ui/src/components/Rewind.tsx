import { useCallback, useEffect, useRef, useState } from 'react';
import { useApi } from '../ApiContext';
import { describeError, errorCode, type RewindMode, type RewindPreview, type RewindReceipt, type RewindResult, type SessionSummary } from '../api';
import { Note } from './common';
import { Button } from './ui/button';
import { Popover } from './ui/popover';
import { Segmented } from './ui/segmented';

const OUTCOME: Record<string, string> = {
  success: 'Rewound.',
  'checkpoint-cleanup-failed': 'Rewound. Copilot could not clean up its checkpoints.',
  'snapshot-prune-failed': 'Rewound. Copilot could not remove its old file snapshots.',
  'truncation-failed': 'Files were restored, but the conversation was not rewound.',
  'rollback-incomplete': 'Restoring files failed and could not be fully undone. Check the files below.',
  'files-rolled-back': 'Nothing changed: restoring files failed and was undone.',
  'session-busy': 'Nothing changed: the conversation was busy.',
  'file-change-tracking-disabled': 'Nothing changed: this conversation does not track file changes.',
  'unsupported-remote-session': 'Nothing changed: this conversation cannot rewind here.',
};
const SKIP: Record<string, string> = {
  'user-modified': 'changed after Copilot’s last write; left as it is',
  'skipped-capture': 'no faithful copy was captured; left as it is',
};
const UNAVAILABLE: Record<string, string> = {
  'file-change-tracking-disabled': 'This conversation does not track file changes.',
  'unsupported-remote-session': 'File restore is not supported for this conversation.',
};
export const UNCERTAIN_REWIND = 'The rewind result was lost. The conversation and files may or may not have changed, and uam will not try again. Check the files, then reread the conversation.';

/** Every native outcome in words. */
export function rewindOutcome(receipt: RewindReceipt): string {
  if (receipt.state === 'uncertain' || receipt.state === 'pending') return UNCERTAIN_REWIND;
  return OUTCOME[receipt.result?.outcome ?? ''] ?? `Copilot reported ${receipt.result?.outcome ?? 'no outcome'}.`;
}

/** Truncation landed: the discarded turns are gone. */
export const rewound = (receipt: RewindReceipt) => ['success', 'checkpoint-cleanup-failed', 'snapshot-prune-failed'].includes(receipt.result?.outcome ?? '');

/** The per-file outcome rows of a result. */
export function RewindFiles({ result }: Readonly<{ result: RewindResult }>) {
  if (!result.restored_files.length && !result.skipped_files.length && !result.restored_omitted && !result.skipped_omitted) return null;
  return (
    <ul className="flex flex-col gap-1 text-caption">
      {result.restored_files.map(path => <li key={`r${path}`} className="flex gap-2"><span className="text-success">restored</span><span className="min-w-0 break-all font-mono text-body">{path}</span></li>)}
      {!!result.restored_omitted && <li className="text-muted">{result.restored_omitted} more restored</li>}
      {result.skipped_files.map(f => <li key={`s${f.path}`} className="flex flex-wrap gap-x-2"><span className="text-warning">kept</span><span className="min-w-0 break-all font-mono text-body">{f.path}</span><span className="text-muted">{SKIP[f.reason] ?? f.reason}</span></li>)}
      {!!result.skipped_omitted && <li className="text-muted">{result.skipped_omitted} more kept as they are</li>}
    </ul>
  );
}

function inside(path: string, workdir: string) {
  const root = workdir.endsWith('/') ? workdir : `${workdir}/`;
  return !!workdir && path.startsWith(root);
}

/** What the rewind does to one file: the inverse of the discarded turns' changes. */
function effect(kind: string, additions = 0, deletions = 0) {
  if (kind === 'deleted') return <><span className="text-success">+{deletions}</span> restored</>;
  if (kind === 'created') return <><span className="text-error">−{additions}</span> removed</>;
  return <><span className="text-success">+{deletions}</span> <span className="text-error">−{additions}</span></>;
}

/** The anchored rewind preview and confirmation. Opening or cancelling changes nothing. */
export function RewindPanel({ session, userItemId, anchor, onClose, onRewound }: Readonly<{
  session: SessionSummary;
  userItemId: string;
  anchor: HTMLElement | null;
  onClose: () => void;
  onRewound?: (receipt: RewindReceipt) => void;
}>) {
  const api = useApi();
  const [preview, setPreview] = useState<RewindPreview | null>(null);
  const [loadError, setLoadError] = useState('');
  const [mode, setMode] = useState<RewindMode>('conversation-and-files');
  const [requestId, setRequestId] = useState(() => crypto.randomUUID());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [receipt, setReceipt] = useState<RewindReceipt | null>(null);
  const [reads, setReads] = useState(0);
  const alive = useRef(false);
  useEffect(() => {
    alive.current = true;
    return () => { alive.current = false; };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    api.rewindPreview(session.id, userItemId, controller.signal).then(p => {
      if (controller.signal.aborted) return;
      setPreview(p);
      setRequestId(crypto.randomUUID());
      if (!p.files_available) setMode('conversation');
    }, (e: unknown) => { if (!controller.signal.aborted) setLoadError(Array.from(describeError(e).slice(0, 512)).join('')); });
    return () => controller.abort();
  }, [api, session.id, userItemId, reads]);
  const confirm = useCallback(() => {
    if (!preview || busy || receipt) return;
    setBusy(true);
    setError('');
    api.rewind(session.id, { user_item_id: userItemId, mode, token: preview.token, request_id: requestId }).then(
      result => {
        if (!alive.current) return;
        setReceipt(result);
        onRewound?.(result);
      },
      (e: unknown) => {
        if (!alive.current) return;
        setError(describeError(e));
        if (errorCode(e) === 'rewind_stale') {
          setPreview(null);
          setReads(n => n + 1);
        }
      },
    ).finally(() => { if (alive.current) setBusy(false); });
  }, [api, busy, mode, onRewound, preview, receipt, requestId, session.id, userItemId]);
  const later = (preview?.turns ?? 1) - 1;
  const files = preview?.files.entries ?? [];
  const title = later === 0 ? 'Undo this turn?' : 'Rewind to before this prompt?';
  return (
    <Popover.Root open onOpenChange={open => { if (!open && !busy) onClose(); }} modal={false}>
      <Popover.Content anchor={anchor} side="bottom" className="w-96 max-w-[calc(100vw-16px)] max-h-(--available-height) gap-3 overflow-y-auto" finalFocus={() => anchor?.isConnected ? anchor : false}>
        <Popover.Title>{receipt ? 'Rewind' : title}</Popover.Title>
        {receipt ? <>
          <Note tone={receipt.state === 'uncertain' ? 'warn' : receipt.state === 'done' && !rewound(receipt) ? 'muted' : 'info'} role="status">{rewindOutcome(receipt)}</Note>
          {receipt.result?.error && <p className="text-caption break-words text-muted">{receipt.result.error}</p>}
          {receipt.result && <RewindFiles result={receipt.result} />}
        </> : !preview ? (
          loadError ? <Note tone="error" role="alert">{loadError}</Note> : <p role="status" className="text-caption text-muted">Reading what the rewind would change…</p>
        ) : <>
          <Popover.Description>
            Removes this prompt{later > 0 ? `, the ${later} after it` : ''} and every reply from the conversation. Copilot’s background work that is not part of these turns stays.
          </Popover.Description>
          {preview.files_available
            ? <Segmented size="sm" aria-label="What to rewind" className="w-full" value={mode} onValueChange={v => setMode(v as RewindMode)} disabled={busy} items={[{ value: 'conversation-and-files', label: 'Conversation and files' }, { value: 'conversation', label: 'Conversation only' }]} />
            : <Note>Files stay as they are. {UNAVAILABLE[preview.files_reason ?? ''] ?? 'File restore is unavailable for this conversation.'}</Note>}
          {mode === 'conversation-and-files' ? <section aria-label="Files" className="flex flex-col gap-1">
            <p className="text-caption text-muted tabular-nums">{preview.files.files ? `${preview.files.files} ${preview.files.files === 1 ? 'file' : 'files'} restored` : 'No captured file changes to restore.'}</p>
            {files.length > 0 && <ul className="flex flex-col gap-1">{files.map(f => <li key={f.path} className="flex flex-wrap items-baseline gap-x-2 text-caption">
              <span className="min-w-0 flex-1 break-all font-mono text-body">{f.path}</span>
              {!inside(f.path, session.workdir) && <span className="text-warning">outside the project</span>}
              <span className="whitespace-nowrap tabular-nums">{effect(f.kind, f.additions, f.deletions)}</span>
            </li>)}</ul>}
            {!!preview.files.omitted && <p className="text-caption text-muted">{preview.files.omitted} more files not shown.</p>}
            <p className="text-caption text-muted">Files changed after Copilot’s last write are protected and stay as they are.</p>
          </section> : !!preview.files.files && <p className="text-caption text-muted">The {preview.files.files} edited {preview.files.files === 1 ? 'file stays' : 'files stay'} on disk as {preview.files.files === 1 ? 'it is' : 'they are'} now.</p>}
        </>}
        {error && <Note tone="error" role="alert">{error}</Note>}
        <div className="flex justify-end gap-2">
          <Button variant="ghost" onClick={onClose} disabled={busy}>{receipt ? 'Close' : 'Cancel'}</Button>
          {!receipt && <Button variant="danger" onClick={confirm} loading={busy} disabled={!preview}>{later === 0 ? 'Undo turn' : 'Rewind'}</Button>}
        </div>
      </Popover.Content>
    </Popover.Root>
  );
}

/** A Task held by a rewind until the conversation is read again. Reconciling never rewinds again. */
export function RewindHold({ session }: Readonly<{ session: SessionSummary }>) {
  const api = useApi();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const hold = session.rewind;
  if (!hold) return null;
  const reconcile = () => {
    setBusy(true);
    setError('');
    api.reconcileRewind(session.id, hold.request_id).catch((e: unknown) => setError(describeError(e))).finally(() => setBusy(false));
  };
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Note tone={hold.state === 'uncertain' ? 'warn' : 'info'} role="status" className="min-w-0 flex-1">
        {hold.state === 'uncertain' ? UNCERTAIN_REWIND : 'Rereading the conversation after the rewind. Sending waits until it is read.'}
      </Note>
      <Button size="sm" variant="secondary" onClick={reconcile} loading={busy}>Reread conversation</Button>
      {error && <Note tone="error" role="alert" className="basis-full">{error}</Note>}
    </div>
  );
}
