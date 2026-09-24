import { ArrowUp, ChevronDown, Cpu, File, Folder, Gauge, Layers, ListEnd, ListPlus, Paperclip, Shield, ShieldOff, Square, X, Zap } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, api, describeError, isStatus, modelCatalog, modelName, newRequestId, readOnly, type Command, type FileEntry, type Model, type PromptMode, type SessionDetail, type SessionSummary, type Submission } from '../api';
import { LIMITS, acceptFor, checkUpload, fileKind, mediaNote, type Kind } from '../lib/attachments';
import { cn } from '../lib/cn';
import { applyPick, filterCommands, parseCommand, pruneFiles, removeToken, triggerAt } from '../lib/composer';
import { DropOverlay, FileRefChip, QueuedExtras, UploadChip, type Pending } from './Attachments';
import { Note, Spinner, useApp } from './common';
import { InlinePicker, type PickerItem } from './InlinePicker';
import { Button } from './ui/button';
import { Menu } from './ui/menu';
import { Tip } from './ui/tooltip';

export const MODE_TEXT = {
  safe: 'Asks before allowing permission requests.',
  yolo: 'Allows permission requests automatically. Questions and managed-policy requests still need you.',
} as const;

export const sizeLabel = (id: string) => (id === 'long_context' ? 'Long context' : id === 'default' ? 'Default' : id);

/** Why effort or context size cannot be chosen for this model and provider; empty when they can. */
export function effortReason(model?: Model): string {
  return (model?.efforts?.length ?? 0) === 0 ? 'This model uses its default effort.' : '';
}
export function contextReason(model: Model | undefined, supported: boolean): string {
  if (!supported) return 'Context size selection is unavailable for this provider.';
  return model?.context_sizes?.some((s) => s.id !== 'default') ? '' : 'This model uses its default context size.';
}

const MAX_FILE_REFS = 20;
const LIST_ID = 'composer-picker';
const LIMITS_TEXT = 'Images up to 3 MiB, PDF up to 10 MiB, text up to 256 KiB · 5 per message';
const COMMANDS_CLOSED = 'Commands are listed once the conversation is open. Send a message first.';

const sentence = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

interface Choice {
  value: string;
  label: ReactNode;
  description?: ReactNode;
}

/**
 * One compact toolbar picker: the current value on a small ghost trigger, a radio menu to
 * change it. When the value cannot be changed the trigger stays visible, disabled, and
 * says why on hover and focus, so the rule is visible instead of a missing control.
 * `compact` drops the value text on a phone, where the toolbar must stay one row; the
 * glyph, the accessible name and the menu still carry it.
 */
function Picker({
  id,
  icon,
  label,
  value,
  display,
  choices,
  disabled,
  reason,
  mono = false,
  compact = false,
  onChange,
}: {
  id: string;
  icon: ReactNode;
  label: string;
  value: string;
  display: string;
  choices: Choice[];
  disabled: boolean;
  reason?: string;
  mono?: boolean;
  compact?: boolean;
  onChange: (value: string) => void;
}) {
  const face = (
    <>
      {icon}
      <span className={cn('max-w-36 truncate max-sm:max-w-16', mono && 'font-mono text-code-sm', compact && 'max-sm:hidden')}>{display}</span>
      <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
    </>
  );
  if (disabled) {
    return (
      <Tip label={reason ?? `${label} cannot change now`}>
        <Button id={id} size="sm" variant="subtle" aria-disabled="true" aria-label={`${label}: ${display}. ${reason ?? ''}`} className="cursor-not-allowed text-muted opacity-60 hover:bg-transparent hover:text-muted">
          {face}
        </Button>
      </Tip>
    );
  }
  return (
    <Menu.Root modal={false}>
      <Tip label={compact ? `${label}: ${display}` : label}>
        <Menu.Trigger render={<Button id={id} size="sm" variant="subtle" aria-label={`${label}: ${display}`} className="text-body" />}>{face}</Menu.Trigger>
      </Tip>
      <Menu.Content side="top" align="start" sideOffset={6} className="min-w-52">
        <Menu.RadioGroup value={value} onValueChange={(v) => onChange(v as string)}>
          <Menu.Label>{label}</Menu.Label>
          {choices.map((c) => (
            <Menu.RadioItem key={c.value} value={c.value} description={c.description} className={mono ? 'font-mono text-code-sm [&_.text-caption]:font-sans' : undefined}>
              {c.label}
            </Menu.RadioItem>
          ))}
        </Menu.RadioGroup>
      </Menu.Content>
    </Menu.Root>
  );
}

function CommandRow({ c }: { c: Command }) {
  return (
    <>
      <span className="shrink-0 font-mono text-code-sm font-medium text-ink">/{c.name}</span>
      {c.input_hint && <span className="shrink-0 font-mono text-code-sm text-faint">{c.input_hint}</span>}
      {c.description && <span className="min-w-0 flex-1 truncate text-caption text-muted">{c.description}</span>}
    </>
  );
}

function FileRow({ f }: { f: FileEntry }) {
  const cut = f.path.lastIndexOf('/');
  const dir = cut >= 0 ? f.path.slice(0, cut + 1) : '';
  const base = f.path.slice(cut + 1);
  return (
    <>
      {f.type === 'directory' ? <Folder aria-hidden="true" className="text-faint" /> : <File aria-hidden="true" className="text-faint" />}
      <span className="flex min-w-0 font-mono text-code-sm">
        {dir && <span className="min-w-0 truncate text-muted">{dir}</span>}
        <span className="shrink-0 text-ink">
          {base}
          {f.type === 'directory' && '/'}
        </span>
      </span>
    </>
  );
}

const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files');

export function Composer({ session, onSessionUpdate }: { session: SessionDetail; onSessionUpdate: (s: SessionSummary) => void }) {
  const { meta } = useApp();
  const [text, setText] = useState('');
  const [caret, setCaret] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Submission | null>(null);
  const pending = useRef<{ key: string; id: string } | null>(null);
  const refocus = useRef(false);
  const live = LIVE.includes(session.state);
  const locked = readOnly(session);
  const last = outcome && (!session.last_submission || outcome.time >= session.last_submission.time) ? outcome : session.last_submission;
  const catalog = modelCatalog(meta, session.provider);
  const models = catalog.some((m) => m.id === session.model) ? catalog : [{ id: session.model, name: session.model || 'Default model' }, ...catalog];
  const selectedModel = catalog.find((m) => m.id === session.model);
  const modelLabel = modelName(meta, session.provider, session.model);
  const routed = session.last_model && session.last_model !== session.model ? modelName(meta, session.provider, session.last_model) : null;
  const queue = session.queue ?? [];
  const effort = session.effort ?? '';
  const contextSize = session.context_size || 'default';
  const sizes = selectedModel?.context_sizes ?? [];
  const noEffort = effortReason(selectedModel);
  const noContext = contextReason(selectedModel, !!session.capabilities.context_size);
  const settingsLocked = live || !!busy || locked;
  const mode = session.mode ?? 'safe';

  /* ---------- `/` and `@` pickers ---------- */

  const textarea = useRef<HTMLTextAreaElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const pendingCaret = useRef<number | null>(null);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [highlight, setHighlight] = useState(0);
  const [files, setFiles] = useState<string[]>([]);
  const [commands, setCommands] = useState<Command[] | null>(null);
  const [commandsError, setCommandsError] = useState<string | null>(null);
  const fetchingCommands = useRef(false);
  const [fileList, setFileList] = useState<{ q: string; files: FileEntry[]; reason: string } | null>(null);
  const fileSeq = useRef(0);

  const rawTrigger = useMemo(() => triggerAt(text, caret), [text, caret]);
  const triggerKey = rawTrigger ? `${rawTrigger.kind}${rawTrigger.start}` : null;
  const trigger = rawTrigger && dismissed !== triggerKey && !locked && !busy ? rawTrigger : null;
  const wantCommands = trigger?.kind === '/' && session.open;

  // The command list is fetched when the picker first opens and kept; a failure is retried on the next open.
  useEffect(() => {
    if (!wantCommands || commands || commandsError || fetchingCommands.current) return;
    fetchingCommands.current = true;
    api
      .commands(session.id)
      .then((list) => setCommands(list))
      .catch((e) => setCommandsError(isStatus(e, 409) ? COMMANDS_CLOSED : sentence(describeError(e))))
      .finally(() => {
        fetchingCommands.current = false;
      });
  }, [wantCommands, commands, commandsError, session.id]);

  // Files: debounced search; a stale answer never overwrites a newer one.
  const fileQuery = trigger?.kind === '@' ? trigger.query : null;
  useEffect(() => {
    if (fileQuery === null) return;
    const seq = ++fileSeq.current;
    const timer = window.setTimeout(
      () => {
        api
          .files(session.id, fileQuery)
          .then((r) => seq === fileSeq.current && setFileList({ q: fileQuery, files: r.files, reason: r.reason }))
          .catch((e) => seq === fileSeq.current && setFileList({ q: fileQuery, files: [], reason: describeError(e) }));
      },
      fileQuery === '' ? 0 : 120,
    );
    return () => window.clearTimeout(timer);
  }, [fileQuery, session.id]);

  const items = useMemo<PickerItem[]>(() => {
    if (!trigger) return [];
    if (trigger.kind === '/') {
      const matched = filterCommands(commands ?? [], trigger.query);
      return [...matched.filter((c) => c.kind !== 'skill'), ...matched.filter((c) => c.kind === 'skill')].map((c) => ({
        key: c.name,
        group: c.kind === 'skill' ? 'Skills' : 'Commands',
        label: `/${c.name}${c.description ? `. ${c.description}` : ''}`,
        render: <CommandRow c={c} />,
      }));
    }
    return (fileList?.files ?? []).map((f) => ({ key: f.path, label: `${f.type === 'directory' ? 'Directory' : 'File'} ${f.path}`, render: <FileRow f={f} /> }));
  }, [trigger, commands, fileList]);
  const hi = Math.min(highlight, Math.max(0, items.length - 1));
  const filesLoading = fileQuery !== null && fileList?.q !== fileQuery;
  const commandsLoading = !!wantCommands && !commands && !commandsError;

  // The textarea is disabled while a prompt is in flight, which drops focus; a send gives it back.
  useEffect(() => {
    if (busy || !refocus.current) return;
    refocus.current = false;
    textarea.current?.focus();
  }, [busy]);

  // Applies a caret position chosen by a pick once the new text is in the DOM.
  useLayoutEffect(() => {
    const c = pendingCaret.current;
    if (c === null) return;
    pendingCaret.current = null;
    textarea.current?.setSelectionRange(c, c);
  }, [text]);

  function updateText(next: string, nextCaret: number) {
    setText(next);
    setCaret(nextCaret);
    setHighlight(0);
    setFiles((f) => pruneFiles(next, f));
    if (!triggerAt(next, nextCaret)) setDismissed(null);
  }

  function pick(item: PickerItem) {
    if (!trigger) return;
    if (trigger.kind === '@' && !files.includes(item.key) && files.length >= MAX_FILE_REFS) {
      setError(`A message references at most ${MAX_FILE_REFS} files.`);
      return;
    }
    const next = applyPick(text, trigger, `${trigger.kind}${item.key}`);
    if (trigger.kind === '@') setFiles((f) => (f.includes(item.key) ? f : [...f, item.key]));
    pendingCaret.current = next.caret;
    setText(next.text);
    setCaret(next.caret);
    setHighlight(0);
    textarea.current?.focus();
  }

  function removeFile(path: string) {
    const next = removeToken(text, path);
    setFiles((f) => f.filter((x) => x !== path));
    updateText(next, Math.min(caret, next.length));
  }

  /* ---------- Attachments ---------- */

  const fileInput = useRef<HTMLInputElement>(null);
  const [uploads, setUploads] = useState<Pending[]>([]);
  const [dragging, setDragging] = useState(0);
  const media = selectedModel?.media;
  const gateNote = mediaNote(media, modelLabel);
  const activeKinds = uploads.filter((u) => u.status !== 'error').map((u) => u.kind);
  const patch = (key: string, p: Partial<Pending>) => setUploads((u) => u.map((x) => (x.key === key ? { ...x, ...p } : x)));

  function addFiles(list: File[]) {
    if (locked || !list.length) return;
    const kinds: Kind[] = [...activeKinds];
    const next: Pending[] = [];
    for (const file of list) {
      const key = newRequestId();
      const sniffed = fileKind(file);
      const kind: Kind = sniffed === 'image' || sniffed === 'pdf' ? sniffed : 'text';
      const reason = checkUpload(file, media, kinds, modelLabel);
      if (reason) {
        next.push({ key, name: file.name, size: file.size, kind, progress: 0, status: 'error', error: reason });
        continue;
      }
      kinds.push(kind);
      const up = api.upload(session.id, file, (p) => patch(key, { progress: p }));
      next.push({ key, name: file.name, size: file.size, kind, progress: 0, status: 'uploading', abort: up.abort });
      if (kind === 'image') {
        const reader = new FileReader();
        reader.onload = () => patch(key, { preview: String(reader.result) });
        reader.readAsDataURL(file);
      }
      up.done
        .then((a) => patch(key, { status: 'done', id: a.id, progress: 1, name: a.name, size: a.size ?? file.size, abort: undefined }))
        .catch((e) => {
          if (isStatus(e, 0) && e.message === 'Upload cancelled') return;
          patch(key, { status: 'error', error: sentence(describeError(e)), abort: undefined });
        });
    }
    setUploads((u) => [...u, ...next]);
  }

  function removeUpload(key: string) {
    uploads.find((u) => u.key === key)?.abort?.();
    setUploads((u) => u.filter((x) => x.key !== key));
  }

  const uploadsFull = activeKinds.length >= LIMITS.count;
  const attachReason = locked ? 'This task is read-only.' : uploadsFull ? `A message carries at most ${LIMITS.count} attachments.` : '';
  const uploading = uploads.some((u) => u.status === 'uploading');
  const refused = uploads.some((u) => u.status === 'error');
  const attachmentIds = uploads.flatMap((u) => (u.status === 'done' && u.id ? [u.id] : []));
  const hasExtras = files.length > 0 || attachmentIds.length > 0;
  const extras = { ...(files.length ? { files } : {}), ...(attachmentIds.length ? { attachments: attachmentIds } : {}) };

  /* ---------- Sending ---------- */

  const cmd = commands ? parseCommand(text, commands) : null;
  const blocked = uploading ? 'Wait for the upload to finish' : refused ? 'Remove the attachment that was refused' : live && cmd ? `/${cmd.name} runs between turns; wait for this turn to finish` : '';
  const steerBlocked = hasExtras ? 'A steer takes text only; queue the message instead' : '';
  const cannotSubmit = !!busy || locked || session.state === 'starting' || !text.trim() || !!blocked;

  async function send(promptMode: PromptMode) {
    const t = text.trim();
    if (cannotSubmit || (promptMode === 'send' && live)) return;
    if (promptMode === 'steer' && steerBlocked) {
      setError(`${steerBlocked}.`);
      return;
    }
    const key = JSON.stringify([t, files, attachmentIds, cmd?.name ?? '']);
    if (!pending.current || pending.current.key !== key) pending.current = { key, id: newRequestId() };
    const id = pending.current.id;
    refocus.current = true;
    setBusy(promptMode);
    setError(null);
    try {
      const sub = cmd && promptMode === 'send' ? await api.command(session.id, cmd.name, cmd.args, id, extras) : await api.prompt(session.id, t, id, promptMode, extras);
      setOutcome(sub);
      pending.current = null;
      if (sub.status === 'accepted' || sub.status === 'queued') {
        setText('');
        setCaret(0);
        setFiles([]);
        setUploads([]);
        setDismissed(null);
      }
    } catch (e) {
      setError(`${describeError(e)}. Nothing will be retried automatically. Repeating this action with unchanged text uses the same request (${id.slice(0, 8)}).`);
    } finally {
      setBusy(null);
    }
  }

  async function action(label: string, op: () => Promise<unknown>) {
    if (busy) return;
    setBusy(label);
    setError(null);
    try {
      await op();
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }
  const settings = (body: Parameters<typeof api.settings>[1]) => action('settings', async () => onSessionUpdate(await api.settings(session.id, body)));

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.nativeEvent.isComposing) return;
    if (trigger) {
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        setDismissed(triggerKey);
        return;
      }
      if (items.length > 0) {
        if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
          e.preventDefault();
          setHighlight(e.key === 'ArrowDown' ? (hi + 1) % items.length : (hi - 1 + items.length) % items.length);
          return;
        }
        if ((e.key === 'Enter' && !e.shiftKey) || e.key === 'Tab') {
          e.preventDefault();
          pick(items[hi]);
          return;
        }
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      void send(e.ctrlKey || e.metaKey ? 'steer' : live ? 'queue' : 'send');
    }
  }

  const sendLabel = busy === 'send' || busy === 'queue' ? 'Submitting…' : live ? 'Queue' : cmd ? `Run /${cmd.name}` : 'Send';
  const pickerNote =
    trigger?.kind === '/' ? (!session.open ? COMMANDS_CLOSED : commandsError) : trigger?.kind === '@' && fileList?.q === trigger.query && fileList.reason ? sentence(fileList.reason) : null;
  const pickerEmpty =
    trigger?.kind === '/'
      ? commands && commands.length === 0
        ? 'This task has no commands.'
        : trigger.query
          ? `No command matches “/${trigger.query}”. Enter sends it as text.`
          : null
      : trigger?.kind === '@'
        ? trigger.query
          ? `No file matches “${trigger.query}”.`
          : 'No files to reference.'
        : null;
  const combo = trigger
    ? {
        role: 'combobox' as const,
        'aria-expanded': true,
        'aria-haspopup': 'listbox' as const,
        'aria-autocomplete': 'list' as const,
        'aria-controls': LIST_ID,
        'aria-activedescendant': items[hi] ? `${LIST_ID}-${items[hi].key}` : undefined,
      }
    : {};

  return (
    <form
      className={cn(
        'relative flex flex-col rounded-md border border-hairline bg-raised shadow-[0_1px_2px_rgba(28,27,24,0.05)] transition-[border-color,box-shadow] duration-160 focus-within:border-hairline-strong focus-within:shadow-[0_2px_8px_rgba(28,27,24,0.08)]',
        locked && 'bg-surface',
        dragging > 0 && 'border-accent',
      )}
      onSubmit={(e) => {
        e.preventDefault();
        void send(live ? 'queue' : 'send');
      }}
      onDragEnter={(e) => {
        if (locked || !hasFiles(e)) return;
        e.preventDefault();
        setDragging((d) => d + 1);
      }}
      onDragOver={(e) => {
        if (locked || !hasFiles(e)) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
      }}
      onDragLeave={(e) => {
        if (locked || !hasFiles(e)) return;
        setDragging((d) => Math.max(0, d - 1));
      }}
      onDrop={(e) => {
        if (locked || !hasFiles(e)) return;
        e.preventDefault();
        setDragging(0);
        addFiles(Array.from(e.dataTransfer.files));
      }}
    >
      {dragging > 0 && <DropOverlay note={gateNote || LIMITS_TEXT} />}
      {trigger && (
        <InlinePicker
          id={LIST_ID}
          title={trigger.kind === '/' ? 'Commands' : 'Files'}
          items={items}
          highlighted={hi}
          loading={trigger.kind === '/' ? commandsLoading : filesLoading && !fileList}
          empty={pickerEmpty}
          note={pickerNote}
          onHighlight={setHighlight}
          onPick={pick}
          popupRef={popup}
        />
      )}
      {(locked || last?.status === 'uncertain' || last?.status === 'rejected' || error || (live && cmd) || (live && hasExtras)) && (
        <div className="flex flex-col gap-1 border-b border-hairline px-3.5 py-2">
          {locked && <Note>{session.stage === 'settled' ? 'Settled. Reopen this task to continue the same conversation.' : 'Archived. This task is read-only.'}</Note>}
          {last?.status === 'uncertain' && (
            <Note tone="warn" role="alert">
              The provider may or may not have received your last prompt. Check the conversation before sending again; it will not be resent automatically.
            </Note>
          )}
          {last?.status === 'rejected' && (
            <Note tone="error" role="alert">
              Last prompt rejected{last.error ? `: ${last.error}` : '.'}
            </Note>
          )}
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
          {live && cmd && (
            <Note role="status">
              <span className="font-mono text-code-sm text-ink">/{cmd.name}</span> runs between turns. Wait for this turn to finish; commands are not queued.
            </Note>
          )}
          {live && hasExtras && !cmd && <Note>Files and attachments go with a queued message; Steer takes text only.</Note>}
        </div>
      )}
      {queue.length > 0 && (
        <details className="group/queue border-b border-hairline px-3.5 py-1.5" open>
          <summary className="flex h-6 list-none items-center gap-2 text-caption text-muted select-none [&::-webkit-details-marker]:hidden">
            <ListEnd aria-hidden="true" className="size-3.5" />
            <span className="tabular-nums">{queue.length} queued</span>
            <span aria-hidden="true">·</span>
            <span>{session.queue_paused ? 'Paused' : 'Waiting for the current turn'}</span>
            <span className="flex-1" />
            {session.queue_paused && (
              <Button size="sm" variant="secondary" className="h-6" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'resume'))}>
                Resume
              </Button>
            )}
            <Button size="sm" variant="subtle" className="h-6 text-muted" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'clear'))}>
              Clear
            </Button>
          </summary>
          <ol className="flex flex-col gap-0.5 pb-1">
            {queue.map((q, i) => (
              <li key={q.request_id} className="flex items-start gap-2 text-ui text-body">
                <span className="mt-0.5 w-4 shrink-0 text-right text-caption tabular-nums text-faint">{i + 1}</span>
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  <span className="truncate">{q.text}</span>
                  <QueuedExtras files={q.files} attachments={q.attachments} />
                </span>
                <Button size="icon" variant="subtle" className="size-6 text-muted" aria-label={`Cancel queued prompt: ${q.text}`} disabled={!!busy || locked} onClick={() => void action('queue', () => api.cancelQueued(session.id, q.request_id))}>
                  <X />
                </Button>
              </li>
            ))}
          </ol>
        </details>
      )}

      {(uploads.length > 0 || files.length > 0) && (
        <div className="flex flex-wrap items-center gap-1.5 px-3.5 pt-3">
          {uploads.map((u) => (
            <UploadChip key={u.key} item={u} onRemove={() => removeUpload(u.key)} />
          ))}
          {files.map((f) => (
            <FileRefChip key={f} path={f} onRemove={() => removeFile(f)} />
          ))}
        </div>
      )}

      <label className="sr-only" htmlFor="composer-text">
        Message
      </label>
      <textarea
        ref={textarea}
        id="composer-text"
        rows={2}
        value={text}
        placeholder={locked ? '' : live ? 'Queue a follow-up, or steer this turn…' : 'Message the agent… (/ commands, @ files)'}
        onChange={(e) => updateText(e.target.value, e.target.selectionStart ?? e.target.value.length)}
        onSelect={(e) => setCaret(e.currentTarget.selectionStart ?? 0)}
        onKeyDown={onKeyDown}
        onBlur={(e) => {
          if (popup.current?.contains(e.relatedTarget as Node | null)) return;
          if (triggerKey) setDismissed(triggerKey);
        }}
        onPaste={(e) => {
          const pasted = Array.from(e.clipboardData.files);
          if (locked || !pasted.length) return;
          e.preventDefault();
          addFiles(pasted);
        }}
        disabled={!!busy || locked}
        {...combo}
        className={cn('max-h-[40dvh] min-h-14 w-full resize-none bg-transparent px-3.5 pt-3 pb-1 text-chat text-ink outline-hidden [field-sizing:content] disabled:text-muted max-sm:text-chat-lg', locked && 'min-h-0 h-2 pt-0')}
      />

      <div className="flex flex-wrap items-center gap-0.5 px-2 pt-1 pb-2">
        {!locked && (
          <>
            <input
              ref={fileInput}
              type="file"
              multiple
              hidden
              accept={acceptFor(media)}
              onChange={(e) => {
                addFiles(Array.from(e.target.files ?? []));
                e.target.value = '';
              }}
            />
            <Tip
              label={
                attachReason || (
                  <>
                    Attach files
                    {gateNote && <span className="block text-on-primary/70">{gateNote}</span>}
                    <span className="block text-on-primary/70">{LIMITS_TEXT}. Paste or drop works too.</span>
                  </>
                )
              }
            >
              <Button
                size="icon"
                variant="subtle"
                aria-label={`Attach files. ${attachReason || gateNote}`.trim()}
                aria-disabled={attachReason ? 'true' : undefined}
                className={cn('text-muted', attachReason && 'cursor-not-allowed opacity-60 hover:bg-transparent hover:text-muted')}
                onClick={() => !attachReason && fileInput.current?.click()}
              >
                <Paperclip />
              </Button>
            </Tip>
          </>
        )}
        <Picker
          id="composer-model"
          icon={<Cpu aria-hidden="true" className="text-faint" />}
          label="Model"
          value={session.model}
          display={modelLabel}
          mono
          choices={models.map((m) => ({ value: m.id, label: m.name }))}
          disabled={settingsLocked}
          reason={locked ? 'This task is read-only.' : live ? 'The model changes between turns.' : undefined}
          onChange={(v) => void settings({ model: v })}
        />
        <Picker
          id="composer-effort"
          icon={<Gauge aria-hidden="true" className="text-faint" />}
          label="Effort"
          value={effort}
          display={effort || 'Default'}
          compact
          choices={[{ value: '', label: 'Default', description: 'Leaves the choice to the provider' }, ...(selectedModel?.efforts ?? []).map((e) => ({ value: e, label: e }))]}
          disabled={settingsLocked || !!noEffort}
          reason={locked ? 'This task is read-only.' : live ? 'Effort changes between turns.' : noEffort || undefined}
          onChange={(v) => void settings({ effort: v })}
        />
        <Picker
          id="composer-context-size"
          icon={<Layers aria-hidden="true" className="text-faint" />}
          label="Context size"
          value={contextSize}
          display={sizeLabel(contextSize)}
          compact
          choices={[
            ...(sizes.some((s) => s.id === 'default') ? [] : [{ value: 'default', label: 'Default' }]),
            ...sizes.map((s) => ({ value: s.id, label: sizeLabel(s.id), description: `${s.tokens.toLocaleString()} tokens${s.id === 'long_context' ? ' · may cost more' : ''}` })),
          ]}
          disabled={settingsLocked || !!noContext}
          reason={locked ? 'This task is read-only.' : live ? 'Context size changes between turns.' : noContext || undefined}
          onChange={(v) => void settings({ context_size: v })}
        />
        <Picker
          id="composer-mode"
          icon={mode === 'yolo' ? <ShieldOff aria-hidden="true" className="text-attention" /> : <Shield aria-hidden="true" className="text-faint" />}
          label="Mode"
          value={mode}
          display={mode === 'yolo' ? 'Yolo' : 'Safe'}
          compact
          choices={[
            { value: 'safe', label: 'Safe', description: MODE_TEXT.safe },
            { value: 'yolo', label: 'Yolo', description: MODE_TEXT.yolo },
          ]}
          disabled={!!busy || locked}
          reason={locked ? 'This task is read-only.' : undefined}
          onChange={(v) => void settings({ mode: v as 'safe' | 'yolo' })}
        />
        {/* The actions wrap onto the next row as one right-aligned group when the toolbar is too narrow. */}
        <span className="ml-auto flex items-center gap-0.5">
        {busy === 'settings' && <Spinner className="mr-1" />}
        {live && (
          <Tip label={!session.capabilities.cancel ? 'This provider cannot cancel a turn' : 'Stop the turn and pause queued follow-ups'}>
            <Button size="icon-md" variant="secondary" aria-label="Stop turn" className="animate-rise" disabled={!!busy || locked || !session.capabilities.cancel} onClick={() => void action('stop', async () => onSessionUpdate(await api.cancel(session.id)))}>
              {busy === 'stop' ? <Spinner /> : <Square className="!size-3.5" fill="currentColor" />}
            </Button>
          </Tip>
        )}
        {live && (
          <Tip label={steerBlocked || 'Steer this turn (Ctrl+Enter)'}>
            <Button size="md" variant="secondary" className="animate-rise" disabled={cannotSubmit || !!steerBlocked} onClick={() => void send('steer')}>
              {busy === 'steer' ? <Spinner /> : <Zap />}
              Steer
            </Button>
          </Tip>
        )}
        {!locked && (
          <Tip
            label={
              blocked ? (
                blocked
              ) : live ? (
                <>
                  Queue for the next turn (Enter)
                  <span className="block text-on-primary/70">Ctrl+Enter steers · Shift+Enter adds a line</span>
                </>
              ) : (
                <>
                  {cmd ? `Run /${cmd.name} (Enter)` : 'Send (Enter)'}
                  <span className="block text-on-primary/70">Shift+Enter adds a line</span>
                </>
              )
            }
          >
            <Button type="submit" size="icon-md" variant="primary" aria-label={blocked ? `${sendLabel}. ${blocked}` : sendLabel} className="ml-1 transition-transform duration-100 active:scale-95" disabled={cannotSubmit}>
              {busy === 'send' || busy === 'queue' ? <Spinner className="border-on-primary border-r-transparent" /> : live ? <ListPlus /> : <ArrowUp strokeWidth={2.25} />}
            </Button>
          </Tip>
        )}
        </span>
      </div>
      {routed && (
        <p className="border-t border-hairline/60 px-3.5 py-1 text-caption text-muted">
          Latest turn ran on <span className="font-mono">{routed}</span>
        </p>
      )}
      {busy && busy !== 'settings' && (
        <span className="sr-only" role="status">
          Updating {busy}…
        </span>
      )}
    </form>
  );
}
