import { ArrowUp, ChevronDown, Cpu, File, FileDiff, Folder, Gauge, GitBranch, ListEnd, ListPlus, Paperclip, Shield, ShieldOff, Square, X, Zap } from 'lucide-react';
import { useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, api, describeError, isStatus, modelCatalog, modelName, newRequestId, readOnly, type Command, type CommandResult, type FileEntry, type Model, type Project, type PromptMode, type SessionDetail, type SessionSummary, type Submission } from '../api';
import { LIMITS, acceptFor, checkUpload, fileKind, mediaNote, type Kind } from '../lib/attachments';
import { cn } from '../lib/cn';
import { compactTokens, estimateTurnCost, formatCredits, modelCostLine } from '../lib/cost';
import { visibleModels } from '../lib/models';
import { ComposerUsage } from './ComposerUsage';
import { applyPick, argumentTrigger, commandPending, commandReason, enterActions, filterCommands, parseCommand, pruneFiles, removeToken, triggerAt } from '../lib/composer';
import { DropOverlay, FileRefChip, QueuedExtras, UploadChip, type Pending } from './Attachments';
import { Markdown, Note, ProjectBadge, Spinner, useApp } from './common';
import { ExecutionStatus } from './ExecutionStatus';
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
      {c.aliases?.length ? <span className="truncate font-mono text-code-sm text-faint">{c.aliases.map((name) => `/${name}`).join(', ')}</span> : null}
      {c.input_hint && <span className="min-w-0 truncate font-mono text-code-sm text-faint">{c.input_hint}</span>}
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

export function Composer({ session, project, fileCount, onChanges, onRename, onSessionUpdate }: { session: SessionDetail; project?: Project; fileCount: number | null; onChanges: () => void; onRename: () => void; onSessionUpdate: (s: SessionSummary) => void }) {
  const { meta, settings: appSettings, dispatch } = useApp();
  const [text, setText] = useState('');
  const [caret, setCaret] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [steerUnavailable, setSteerUnavailable] = useState('');
  const [outcome, setOutcome] = useState<Submission | null>(null);
  const resultStorageKey = `uam:command-result-dismissed:${session.id}`;
  const [dismissedResultIds, setDismissedResultIds] = useState<string[]>(() => {
    try {
      const saved: unknown = JSON.parse(sessionStorage.getItem(resultStorageKey) ?? '[]');
      return Array.isArray(saved) ? saved.filter((id): id is string => typeof id === 'string').slice(-64) : [];
    } catch { return []; }
  });
  const [commandAction, setCommandAction] = useState<Extract<CommandResult, { kind: 'action' }>['action'] | null>(null);
  const pending = useRef<{ key: string; id: string } | null>(null);
  const refocus = useRef(false);
  const live = LIVE.includes(session.state);
  const locked = readOnly(session);
  const last = outcome && (!session.last_submission || outcome.time >= session.last_submission.time) ? outcome : session.last_submission;
  const commandResult = last?.status === 'accepted' && !dismissedResultIds.includes(last.request_id) ? last.command_result : null;
  const dismissResult = (id = last?.request_id ?? null) => {
    if (!id) return;
    const ids = [...dismissedResultIds.filter((previous) => previous !== id), id].slice(-64);
    setDismissedResultIds(ids);
    try { sessionStorage.setItem(resultStorageKey, JSON.stringify(ids)); }
    catch { /* Dismissal still works when browser storage is unavailable. */ }
  };
  const catalog = modelCatalog(meta, session.provider);
  const models = visibleModels(catalog, appSettings.hidden_models?.[session.provider]);
  const hiddenModel = appSettings.hidden_models?.[session.provider]?.includes(session.model);
  const selectedModel = catalog.find((m) => m.id === session.model);
  const modelLabel = modelName(meta, session.provider, session.model);
  const routed = session.last_model && session.last_model !== session.model ? modelName(meta, session.provider, session.last_model) : null;
  const queue = session.queue ?? [];
  const effort = session.effort ?? '';
  const contextSize = session.context_size || 'default';
  const sizes = selectedModel?.context_sizes ?? [];
  const selectedSize = sizes.find((s) => s.id === contextSize);
  const contextLabel = selectedSize?.tokens ? compactTokens(selectedSize.tokens) : sizeLabel(contextSize);
  const noEffort = effortReason(selectedModel);
  const noContext = contextReason(selectedModel, !!session.capabilities.context_size);
  const settingsLocked = live || !!busy || locked;
  const autopilot = session.execution?.mode === 'autopilot' || session.execution?.objective?.status === 'active';
  const mode = session.mode ?? 'safe';

  /* ---------- `/` and `@` pickers ---------- */

  const textarea = useRef<HTMLTextAreaElement>(null);
  const popup = useRef<HTMLDivElement>(null);
  const pendingCaret = useRef<number | null>(null);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [highlight, setHighlight] = useState(0);
  const [files, setFiles] = useState<string[]>([]);
  const [commandVersion, setCommandVersion] = useState(0);
  const [executionOpen, setExecutionOpen] = useState(false);
  const pendingExecution = useRef<{ mode: 'interactive' | 'autopilot'; id: string } | null>(null);
  const commandKey = `${session.id}:${session.open}:${live}:${session.mode}:${session.execution?.mode}:${commandVersion}`;
  const [commandList, setCommandList] = useState<{ key: string; commands: Command[] | null; error: string | null } | null>(null);
  const commands = commandList?.key === commandKey ? commandList.commands : null;
  const commandsError = commandList?.key === commandKey ? commandList.error : null;
  const [fileList, setFileList] = useState<{ q: string; files: FileEntry[]; reason: string } | null>(null);
  const fileSeq = useRef(0);

  const argument = useMemo(() => triggerAt(text, caret) ? null : argumentTrigger(text, caret, commands ?? []), [text, caret, commands]);
  const rawTrigger = useMemo(() => triggerAt(text, caret) ?? argument?.trigger ?? null, [text, caret, argument]);
  const triggerKey = rawTrigger ? `${rawTrigger.kind}${rawTrigger.start}` : null;
  const trigger = rawTrigger && dismissed !== triggerKey && !locked && !busy ? rawTrigger : null;
  const shapedCommand = /^[/$]\S/.test(text.trim());
  const pendingCommand = commandPending(text, commands, commandsError);
  const wantCommands = !locked && (executionOpen || trigger?.kind === '/' || trigger?.kind === '$' || pendingCommand);
  const autopilotCommand = commands?.find((c) => c.kind === 'command' && (c.name === 'autopilot' || c.aliases?.includes('autopilot')));
  const executionReason = locked ? 'This task is read-only.' : session.state === 'starting' ? 'Wait for this task to start.' : commandsError || (!commands ? 'Loading execution controls…' : !autopilotCommand ? 'Autopilot is unavailable for this provider.' : commandReason(autopilotCommand, LIVE.includes(session.state)));

  // Fetching can reopen an exact closed conversation, but never submits a prompt.
  // Catalogue failures hold slash-shaped input instead of falling through to a prompt.
  useEffect(() => {
    if (!wantCommands || commands || commandsError) return;
    let current = true;
    api.commands(session.id)
      .then((list) => current && setCommandList({ key: commandKey, commands: list, error: null }))
      .catch((e) => current && setCommandList({ key: commandKey, commands: null, error: sentence(describeError(e)) }));
    return () => { current = false; };
  }, [wantCommands, commands, commandsError, commandKey, session.id]);

  // Open the same controls used by the toolbar after submission unlocks them.
  useEffect(() => {
    if (busy || !commandAction) return;
    const action = commandAction;
    const timer = window.setTimeout(() => {
      setCommandAction(null);
      if (action === 'rename') { onRename(); return; }
      const target = document.getElementById({ model: 'composer-model', permissions: 'composer-mode', context: 'composer-context-usage', usage: 'composer-usage' }[action]);
      if (!target || target.getAttribute('aria-disabled') === 'true' || target.hasAttribute('disabled')) {
        setError(`The ${action} control is unavailable now.`);
        return;
      }
      target.click();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [busy, commandAction, onRename]);

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
    if (argument) {
      return (argument.command.input_choices ?? []).filter((c) => c.name.toLowerCase().includes(trigger.query.toLowerCase())).map((c) => ({
        key: c.name, label: `${c.name}. ${c.description}`, render: <><span className="font-mono text-code-sm">{c.name}</span><span className="min-w-0 text-caption text-muted">{c.description}</span></>,
      }));
    }
    if (trigger.kind === '/' || trigger.kind === '$') {
      const matched = filterCommands((commands ?? []).filter((c) => trigger.kind !== '$' || c.kind === 'skill'), trigger.query);
      return [...matched.filter((c) => c.kind !== 'skill'), ...matched.filter((c) => c.kind === 'skill')].map((c) => ({
        key: c.name,
        group: c.kind === 'skill' ? 'Skills' : 'Commands',
        label: `/${c.name}${c.aliases?.length ? `, aliases ${c.aliases.map((name) => `/${name}`).join(', ')}` : ''}. ${commandReason(c, live) || c.description}`,
        disabled: !!commandReason(c, live),
        render: <CommandRow c={c} />,
      }));
    }
    return (fileList?.files ?? []).map((f) => ({ key: f.path, label: `${f.type === 'directory' ? 'Directory' : 'File'} ${f.path}`, render: <FileRow f={f} /> }));
  }, [trigger, argument, commands, fileList, live]);
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
    if (item.disabled) {
      setError(commandReason(commands?.find((c) => c.name === item.key), live));
      return;
    }
    if (trigger.kind === '@' && !files.includes(item.key) && files.length >= MAX_FILE_REFS) {
      setError(`A message references at most ${MAX_FILE_REFS} files.`);
      return;
    }
    const next = applyPick(text, trigger, argument ? item.key : `${trigger.kind}${item.key}`);
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
  const attachReason = uploadsFull ? `A message carries at most ${LIMITS.count} attachments.` : '';
  const uploading = uploads.some((u) => u.status === 'uploading');
  const refused = uploads.some((u) => u.status === 'error');
  const attachmentIds = uploads.flatMap((u) => (u.status === 'done' && u.id ? [u.id] : []));
  const hasExtras = files.length > 0 || attachmentIds.length > 0;
  const extras = { ...(files.length ? { files } : {}), ...(attachmentIds.length ? { attachments: attachmentIds } : {}) };

  /* ---------- Sending ---------- */

  const cmd = commands ? parseCommand(text, commands) : null;
  const descriptor = commands?.find((c) => c.name === cmd?.name);
  const commandBlocked = commandReason(descriptor, live) || (descriptor?.input_required && !cmd?.args ? `/${descriptor.name} needs ${descriptor.input_hint || 'an argument'}.` : '');
  const blocked = uploading
    ? 'Wait for the upload to finish'
    : refused
      ? 'Remove the attachment that was refused'
      : pendingCommand
        ? 'Wait for the command list to load'
        : shapedCommand && commandsError
          ? 'Commands could not be loaded. Retry the command list.'
          : commandBlocked;
  const steerBlocked = hasExtras ? 'A steer takes text only; queue the message instead' : steerUnavailable;
  const cannotSubmit = !!busy || locked || session.state === 'starting' || !text.trim() || !!blocked;
  // Enter does the setting's action, Ctrl/Cmd+Enter the other (issue #183). The primary button is Enter's;
  // the secondary is the other's, and stays Steer, disabled with its reason, while a steer is impossible.
  const steerDefault = appSettings.send_default === 'steer';
  const { enter, modified } = enterActions(live, appSettings.send_default, !!steerBlocked);
  const other: PromptMode = steerBlocked ? 'steer' : modified;

  async function send(promptMode: PromptMode) {
    const t = text.trim();
    if (cannotSubmit || (!cmd && promptMode === 'send' && live)) return;
    if (!cmd && promptMode === 'steer' && steerBlocked) {
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
      const sub = cmd ? await api.command(session.id, cmd.name, cmd.args, id, extras) : await api.prompt(session.id, t, id, promptMode, extras);
      setOutcome(sub);
      if (sub.status === 'accepted' || sub.status === 'queued') {
        pending.current = null;
        const result = sub.command_result;
        const prefill = result?.kind === 'text' ? result.prefill_input ?? '' : '';
        setText(prefill);
        setCaret(prefill.length);
        if (cmd) {
          if (result?.kind === 'action') setCommandAction(result.action);
          setCommandVersion((v) => v + 1);
        }
        setFiles([]);
        setUploads([]);
        setDismissed(null);
      }
    } catch (e) {
      if (!cmd && promptMode === 'steer' && isStatus(e, 409) && e.message.includes('cannot steer a running turn')) {
        setSteerUnavailable('This provider cannot steer a running turn');
        setError('This provider cannot steer a running turn. Your message is still here; Enter will queue it.');
        return;
      }
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

  async function changeExecution(next: 'interactive' | 'autopilot') {
    if (busy || executionReason || !autopilotCommand || (session.execution?.known && session.execution.mode === next)) return;
    if (pendingExecution.current?.mode !== next) pendingExecution.current = { mode: next, id: newRequestId() };
    const { id } = pendingExecution.current;
    // A toolbar setting has no success banner, including after SSE or a page reload.
    // Errors and uncertain outcomes still use the existing submission feedback.
    dismissResult(id);
    await action('execution', async () => {
      const sub = await api.command(session.id, autopilotCommand.name, next === 'autopilot' ? 'on' : 'off', id);
      setOutcome(sub);
      if (sub.status === 'accepted') {
        pendingExecution.current = null;
        setCommandVersion((v) => v + 1);
        dispatch({ type: 'detail_loaded', detail: await api.session(session.id) });
      }
    });
  }

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
      void send(e.ctrlKey || e.metaKey ? modified : enter);
    }
  }

  const sendLabel = busy === enter ? 'Submitting…' : cmd ? `Run /${cmd.name}` : live ? (enter === 'steer' ? 'Steer' : 'Queue') : 'Send';
  const pickerNote =
    (trigger?.kind === '/' || trigger?.kind === '$') ? (commandsError || (items[hi]?.disabled ? commandReason(commands?.find((c) => c.name === items[hi].key), live) : null)) : trigger?.kind === '@' && fileList?.q === trigger.query && fileList.reason ? sentence(fileList.reason) : null;
  const pickerEmpty =
    (trigger?.kind === '/' || trigger?.kind === '$')
      ? argument
        ? 'Type arguments, then press Enter to run.'
        : commands && commands.length === 0
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
        'relative flex flex-col rounded-md border border-hairline bg-raised shadow-raised transition-[border-color,box-shadow] duration-160 focus-within:border-hairline-strong focus-within:shadow-float',
        locked && 'bg-surface',
        dragging > 0 && 'border-accent',
      )}
      onSubmit={(e) => {
        e.preventDefault();
        void send(enter);
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
          title={argument ? `/${argument.command.name} arguments` : trigger.kind === '$' ? 'Skills' : trigger.kind === '/' ? 'Commands' : 'Files'}
          items={items}
          highlighted={hi}
          loading={trigger.kind !== '@' ? commandsLoading : filesLoading && !fileList}
          empty={pickerEmpty}
          note={pickerNote}
          onHighlight={setHighlight}
          onPick={pick}
          popupRef={popup}
        />
      )}
      {(locked || last?.status === 'uncertain' || last?.status === 'rejected' || error || commandBlocked || (shapedCommand && commandsError) || (live && steerBlocked)) && (
        <div className="flex flex-col gap-1 border-b border-hairline px-3.5 py-2">
          {locked && <Note>{session.stage === 'settled' ? 'Settled. Reopen this task to continue the same conversation.' : 'Archived. This task is read-only.'}</Note>}
          {last?.status === 'uncertain' && (
            <Note tone="warn" role="alert">
              Your last submission may have changed the provider state. Check the conversation and settings before retrying; it will not be resent automatically.
            </Note>
          )}
          {last?.status === 'rejected' && (
            <Note tone="error" role="alert">
              Last submission rejected{last.error ? `: ${last.error}` : '.'}
            </Note>
          )}
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
          {commandBlocked && <Note role="status">{commandBlocked}</Note>}
          {shapedCommand && commandsError && <Note tone="error" role="alert">{commandsError} <Button size="sm" variant="subtle" onClick={() => { setCommandVersion((v) => v + 1); setDismissed(null); textarea.current?.focus(); }}>Retry commands</Button></Note>}
          {live && hasExtras && !cmd && (
            <Note>{steerDefault ? 'Enter queues this message: a steer takes text only, so files and attachments go with the next turn.' : 'Files and attachments go with a queued message; Steer takes text only.'}</Note>
          )}
          {live && steerUnavailable && !hasExtras && !cmd && <Note>{steerUnavailable}. Enter queues the message for the next turn.</Note>}
        </div>
      )}
      {commandResult && commandResult.kind !== 'action' && (
        <div className="border-b border-hairline px-3.5 py-2 text-ui text-body">
          <div className="flex items-center gap-2 pb-1"><span className="text-caption text-muted">Command result</span><span className="flex-1" /><Button size="icon" variant="subtle" aria-label="Dismiss command result" className="size-6" onClick={() => dismissResult()}><X /></Button></div>
          {commandResult.kind === 'select' ? <>
            <p className="text-caption text-muted">{commandResult.title}</p>
            <div className="max-h-40 overflow-y-auto">
              {commandResult.options.map((choice) => <Button key={choice.name} size="sm" variant="subtle" disabled={!!busy || locked} className="h-auto min-h-8 w-full justify-start whitespace-normal text-left pointer-coarse:min-h-11" onClick={() => {
                const next = `/${commandResult.command} ${choice.name} `;
                updateText(next, next.length); pendingCaret.current = next.length; dismissResult(); textarea.current?.focus();
              }}><span className="font-mono">{choice.name}</span><span className="text-caption text-muted">{choice.description}</span></Button>)}
            </div>
          </> : commandResult.kind === 'text' && commandResult.markdown ? <Markdown text={commandResult.text} /> : <p className="whitespace-pre-wrap" role="status">{commandResult.text || 'Command completed.'}</p>}
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
        placeholder={locked ? '' : live ? (steerDefault ? 'Steer this turn, or queue a follow-up…' : 'Queue a follow-up, or steer this turn…') : 'Ask anything, @ files, $ skills, / commands'}
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
        <Picker
          id="composer-model"
          icon={<Cpu aria-hidden="true" className="text-faint" />}
          label="Model"
          value={session.model}
          display={modelLabel}
          mono
          choices={models.map((m) => {
            const estimate = estimateTurnCost(m, session.context, contextSize);
            const prices = session.capabilities.usage ? modelCostLine(m) : '';
            return { value: m.id, label: m.name, description: [prices, session.capabilities.usage && estimate !== null ? `≈ ${formatCredits(estimate)} credits / turn, input only` : ''].filter(Boolean).join(' · ') };
          })}
          disabled={settingsLocked}
          reason={locked ? 'This task is read-only.' : live ? 'The model changes between turns.' : undefined}
          onChange={(v) => void settings({ model: v })}
        />
        {hiddenModel && <span className="text-caption text-muted">Hidden in Settings</span>}
        <ComposerUsage session={session} model={selectedModel} />
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-hairline-strong" />
        <Menu.Root modal={false}>
          <Tip label={settingsLocked ? (locked ? 'This task is read-only.' : 'Effort and context change between turns.') : 'Effort and context size'}>
            <Menu.Trigger disabled={settingsLocked} render={<Button id="composer-effort-context" size="sm" variant="subtle" aria-label={`Effort and context size: ${effort || 'Default'} · ${contextLabel}`} />}>
              <Gauge aria-hidden="true" className="text-faint" />
              <span>{effort || 'Default'} · {contextLabel}</span>
              <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
            </Menu.Trigger>
          </Tip>
          <Menu.Content side="top" align="start">
            <Menu.Group>
              <Menu.Label>Effort</Menu.Label>
              {noEffort ? <p className="max-w-64 px-2 py-1 text-caption text-muted">{noEffort}</p> : <Menu.RadioGroup value={effort} onValueChange={(v) => void settings({ effort: v as string })}>
                <Menu.RadioItem value="">Default</Menu.RadioItem>
                {(selectedModel?.efforts ?? []).map((e) => <Menu.RadioItem key={e} value={e}>{e}</Menu.RadioItem>)}
              </Menu.RadioGroup>}
            </Menu.Group>
            <Menu.Separator />
            <Menu.Group>
              <Menu.Label>Context size</Menu.Label>
              {noContext ? <p className="max-w-64 px-2 py-1 text-caption text-muted">{noContext}</p> : <Menu.RadioGroup value={contextSize} onValueChange={(v) => void settings({ context_size: v as string })}>
                {!sizes.some((s) => s.id === 'default') && <Menu.RadioItem value="default">Default</Menu.RadioItem>}
                {sizes.map((s) => <Menu.RadioItem key={s.id} value={s.id} description={s.id === 'long_context' ? 'May cost more' : undefined}>{sizeLabel(s.id)} · {compactTokens(s.tokens)}</Menu.RadioItem>)}
              </Menu.RadioGroup>}
            </Menu.Group>
          </Menu.Content>
        </Menu.Root>
        <span aria-hidden="true" className="mx-1 h-4 w-px bg-hairline-strong" />
        <Picker
          id="composer-mode"
          icon={mode === 'yolo' ? <ShieldOff aria-hidden="true" className="text-attention" /> : <Shield aria-hidden="true" className="text-faint" />}
          label="Permissions"
          value={mode}
          display={mode === 'yolo' ? 'Yolo' : 'Safe'}
          choices={[
            { value: 'safe', label: 'Safe', description: MODE_TEXT.safe },
            { value: 'yolo', label: 'Yolo', description: MODE_TEXT.yolo },
          ]}
          disabled={!!busy || locked}
          reason={locked ? 'This task is read-only.' : undefined}
          onChange={(v) => void settings({ mode: v as 'safe' | 'yolo' })}
        />
        <ExecutionStatus execution={session.execution} supported={!!session.capabilities.execution_modes}
          reason={executionReason} busy={!!busy} onOpenChange={setExecutionOpen}
          onChange={(next) => void changeExecution(next)}
          onRetry={commandsError && !locked ? () => setCommandVersion((v) => v + 1) : undefined} />
        {/* The actions wrap onto the next row as one right-aligned group when the toolbar is too narrow. */}
        <span className="ml-auto flex items-center gap-0.5">
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

        {busy === 'settings' && <Spinner className="mr-1" />}
        {(live || autopilot) && (
          <Tip label={!session.capabilities.cancel ? 'This provider cannot cancel a turn' : 'Stop execution and pause queued follow-ups'}>
            <Button size="icon-md" variant="danger" aria-label={autopilot ? "Stop autopilot" : "Stop turn"} className="animate-rise rounded-full bg-error text-on-primary hover:bg-error/90" disabled={!!busy || locked || !session.capabilities.cancel} onClick={() => void action('stop', async () => onSessionUpdate(await api.cancel(session.id)))}>
              {busy === 'stop' ? <Spinner /> : <Square className="!size-3.5" fill="currentColor" />}
            </Button>
          </Tip>
        )}
        {live && !cmd && other === 'steer' && (
          <Tip label={steerBlocked || 'Steer this turn (Ctrl+Enter)'}>
            <Button size="md" variant="secondary" className="animate-rise" disabled={cannotSubmit || !!steerBlocked} onClick={() => void send('steer')}>
              {busy === 'steer' ? <Spinner /> : <Zap />}
              Steer
            </Button>
          </Tip>
        )}
        {live && !cmd && other === 'queue' && (
          <Tip label="Queue for the next turn (Ctrl+Enter)">
            <Button size="md" variant="secondary" className="animate-rise" disabled={cannotSubmit} onClick={() => void send('queue')}>
              {busy === 'queue' ? <Spinner /> : <ListPlus />}
              Queue
            </Button>
          </Tip>
        )}
        {!locked && (
          <Tip
            label={
              blocked ? (
                blocked
              ) : live && !cmd ? (
                <>
                  {enter === 'steer' ? 'Steer this turn (Enter)' : 'Queue for the next turn (Enter)'}
                  <span className="block text-on-primary/70">{steerBlocked || (enter === 'steer' ? 'Ctrl+Enter queues' : 'Ctrl+Enter steers')} · Shift+Enter adds a line</span>
                </>
              ) : (
                <>
                  {cmd ? `Run /${cmd.name} (Enter)` : 'Send (Enter)'}
                  <span className="block text-on-primary/70">Shift+Enter adds a line</span>
                </>
              )
            }
          >
            <Button type="submit" size="icon-md" variant="primary" aria-label={blocked ? `${sendLabel}. ${blocked}` : sendLabel} className="ml-1 rounded-full transition-transform duration-100 active:scale-95" disabled={cannotSubmit}>
              {busy === enter ? <Spinner className="border-on-primary border-r-transparent" /> : !live ? <ArrowUp strokeWidth={2.25} /> : enter === 'steer' ? <Zap /> : <ListPlus />}
            </Button>
          </Tip>
        )}
        </span>
      </div>
      <div className="flex min-h-8 items-center gap-2 rounded-b-md border-t border-hairline bg-sunken px-3 text-caption text-muted">
        <span className="flex min-w-0 items-center gap-2 max-sm:hidden">
          {project && <ProjectBadge badge={project.badge} />}
          <span className="max-w-52 truncate">{project?.name ?? 'Project'}</span>
          <span aria-hidden="true">·</span><span title={session.workdir}>Project folder</span>
        </span>
        <span className="flex-1" />
        <Button id="changes-link" size="sm" variant="subtle" aria-label={`Open changes${fileCount !== null ? `, ${fileCount} files` : ''}`} className="px-1 text-caption text-muted" onClick={onChanges}>
          <FileDiff aria-hidden="true" />{fileCount === null ? 'Changes' : `${fileCount} changed`}
        </Button>
        {project?.branch && <span className="flex min-w-0 max-w-[50%] items-center gap-1 font-mono" title={project.branch}><GitBranch aria-hidden="true" className="size-3 shrink-0" /><span className="truncate">{project.branch}</span></span>}
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
