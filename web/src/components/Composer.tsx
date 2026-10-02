import { ArrowUp, ChevronDown, Cpu, Ellipsis, File, Folder, Gauge, ListEnd, Paperclip, RotateCcw, ShieldAlert, ShieldCheck, ShieldHalf, ShieldOff, Square, X } from 'lucide-react';
import { memo, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, SIGNED_OUT, api, describeError, errorCode, isStatus, modelCatalog, modelName, newRequestId, readOnly, type Command, type CommandResult, type FileEntry, type Interaction, type Model, type PromptMode, type PromptSettings, type Question, type QueuedPrompt, type SessionDetail, type SessionSummary, type Submission, type TaskDefaults } from '../api';
import { answerFromComposer, answerPlaceholder, canAnswer, recommendedChoice } from '../lib/answer';
import { LIMITS, acceptFor, checkUpload, fileKind, kindOf, mediaNote, type Kind } from '../lib/attachments';
import { cn } from '../lib/cn';
import { compactTokens, estimateTurnCost, formatCredits, modelCostLine } from '../lib/cost';
import { visibleModels } from '../lib/models';
import { foldToFit } from '../lib/toolbarFold';
import { BackgroundTasks } from './BackgroundTasks';
import { SUGGESTION_ID, SuggestionGhost, useSuggestion } from './Assist';
import { isPanelOutput, panelOutput, type CommandOutput } from './CommandOutput';
import { ComposerUsage } from './ComposerUsage';
import { applyPick, argumentTrigger, commandPending, commandReason, enterActions, enterInPicker, entersRiskiest, filterCommands, parseCommand, pruneFiles, removeToken, triggerAt } from '../lib/composer';
import { changeSettings, draftKey, newTaskKey, parseDraft, serializeDraft, type Draft } from '../lib/drafts';
import { historyEntries, historyKey, lastPrompt, type Browsing } from '../lib/history';
import { DropOverlay, FileRefChip, QueuedExtras, UploadChip, type Pending } from './Attachments';
import { Markdown, Note, Skeleton, Spinner, useApp } from './common';
import { ExecutionItems } from './ExecutionStatus';
import { InlinePicker, type PickerItem } from './InlinePicker';
import { ComposerQuestion } from './Interactions';
import { Appear } from './ui/appear';
import { Button } from './ui/button';
import { AlertDialog, useConfirm } from './ui/dialog';
import { Collapse, usePresence } from './ui/collapse';
import { Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** How freely the agent acts, from permissions and execution together (DESIGN.md Permissions and execution): safest to riskiest. */
const RISKS = [
  { Icon: ShieldCheck, tone: 'text-success', text: 'Safest: asks before risky actions and waits for each message.' },
  { Icon: ShieldHalf, tone: 'text-warning', text: 'Keeps working between turns by itself, but still asks before risky actions.' },
  { Icon: ShieldOff, tone: 'text-attention', text: 'Unsafe: allows permission requests without asking.' },
  { Icon: ShieldAlert, tone: 'text-error', text: 'Highly risky: allows every permission request and keeps working without you.' },
] as const;

export const MODE_TEXT = {
  safe: 'Asks before allowing permission requests.',
  yolo: 'Allows permission requests automatically. Questions and managed-policy requests still need you.',
} as const;

export function sizeLabel(id: string): string {
  if (id === 'long_context') return 'Long context';
  if (id === 'default') return 'Default';
  return id;
}

/** Why effort or context size cannot be chosen for this model and provider; empty when they can. */
export function effortReason(model?: Model): string {
  return (model?.efforts?.length ?? 0) === 0 ? 'This model uses its default effort.' : '';
}
export function contextReason(model: Model | undefined, supported: boolean): string {
  if (!supported) return 'Context size selection is unavailable for this provider.';
  return model?.context_sizes?.some((s) => s.id !== 'default') ? '' : 'This model uses its default context size.';
}

/** A queued prompt's name: its text, or for one without text what it carries. */
const queuedLabel = (q: QueuedPrompt): string => q.text || [...(q.attachments ?? []).map((a) => a.name), ...(q.files ?? [])].join(', ');

const MAX_FILE_REFS = 20;
const LIST_ID = 'composer-picker';
const LIMITS_TEXT = 'Images up to 3 MiB, PDF up to 10 MiB, text up to 256 KiB · 5 per message';
/** Typing pauses this long before the draft is written. */
const DRAFT_DELAY = 250;
/** A touch screen: focusing the composer would raise the keyboard over the conversation (as App's select). */
const COARSE = '(pointer: coarse)';
/** What the turn-time actions are called, on the Send button and in its menu. */
const CHOICE_LABEL = { steer: 'Send now', queue: 'After this turn' } as const;
/** The Send button's glyph while a turn runs: up and away now, or onto the end of the waiting list (its strip's glyph). */
const CHOICE_ICON = { steer: ArrowUp, queue: ListEnd } as const;
/** The key that does the action Enter does not while a turn runs. */
const MODIFIED_KEY = navigator.platform.startsWith('Mac') ? '⌘+Enter' : 'Ctrl+Enter';

// Storage may be unavailable (private mode, quota): the composer works without a draft then.
function readDraft(key: string): Draft | null {
  try { return parseDraft(localStorage.getItem(key)); }
  catch { return null; }
}
function writeDraft(key: string, draft: Draft) {
  const raw = serializeDraft(draft);
  try { if (raw) localStorage.setItem(key, raw); else localStorage.removeItem(key); }
  catch { /* The draft lives in state until the next write. */ }
}

const sentence = (s: string) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

/** A stored upload back as a done chip (a draft's, or a past prompt's); an image shows its stored copy. */
const storedUpload = (sessionId: string, a: { id: string; name: string; size: number; kind: Kind }): Pending => ({
  key: a.id, name: a.name, size: a.size, kind: a.kind, progress: 1, status: 'done', id: a.id, ...(a.kind === 'image' ? { preview: api.attachmentUrl(sessionId, a.id) } : {}),
});

interface Choice {
  value: string;
  label: ReactNode;
  description?: ReactNode;
  /** Choices with a group are listed after the others under its label, e.g. a custom model's provider. */
  group?: string;
}

/** The picker gives up width only once the lower-priority controls have folded away (on a phone they always have). */
const SHRINK = 'max-sm:min-w-0 max-sm:shrink in-data-[fold~=more]:min-w-0 in-data-[fold~=more]:shrink';

/**
 * One compact toolbar picker: the current value on a small ghost trigger, a radio menu to
 * change it. When the value cannot be changed the trigger stays visible, disabled, and
 * says why on hover and focus, so the rule is visible instead of a missing control.
 * The value truncates no shorter than SQUEEZE_MIN and leaves at the `model` fold (lib/toolbarFold);
 * the glyph, the accessible name, the tooltip and the menu still carry it.
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
  hint,
  className,
  onChange,
}: Readonly<{
  id: string;
  icon: ReactNode;
  label: string;
  value: string;
  display: string;
  choices: Choice[];
  disabled: boolean;
  reason?: string;
  /** A second tooltip line (the model the latest turn was routed to). */
  hint?: string;
  className?: string;
  onChange: (value: string) => void;
}>) {
  const face = (
    <>
      {icon}
      <span data-squeeze="" className="max-w-36 truncate max-sm:max-w-16 in-data-[fold~=model]:hidden">{display}</span>
      <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
    </>
  );
  const item = (c: Choice) => (
    <Menu.RadioItem key={c.value} value={c.value} description={c.description}>
      {c.label}
    </Menu.RadioItem>
  );
  const line = (text?: string) => text && <span className="block text-on-primary/70">{text}</span>;
  // The value leads, since the trigger can be a bare glyph.
  const tip = (why?: string) => <>{`${label}: ${display}`}{line(why)}{line(hint)}</>;
  if (disabled) {
    const hintSuffix = hint ? ` ${hint}.` : '';
    return (
      <Tip label={tip(reason ?? `${label} cannot change now`)}>
        <Button id={id} size="sm" variant="subtle" aria-disabled="true" aria-label={`${label}: ${display}. ${reason ?? ''}${hintSuffix}`} className={cn(SHRINK, 'text-muted', className)}>
          {face}
        </Button>
      </Tip>
    );
  }
  return (
    <Menu.Root modal={false}>
      <Tip label={tip()}>
        <Menu.Trigger render={<Button id={id} size="sm" variant="subtle" aria-label={`${label}: ${display}`} className={cn(SHRINK, 'text-body', className)} />}>{face}</Menu.Trigger>
      </Tip>
      <Menu.Content side="top" align="start" sideOffset={6} className="min-w-52">
        <Menu.RadioGroup value={value} onValueChange={(v) => onChange(v as string)}>
          <Menu.Label>{label}</Menu.Label>
          {choices.filter((c) => !c.group).map(item)}
          {[...new Set(choices.flatMap((c) => (c.group ? [c.group] : [])))].map((group) => [
            <Menu.Separator key={`separator-${group}`} />,
            <Menu.Group key={group}>
              <Menu.Label>{group}</Menu.Label>
              {choices.filter((c) => c.group === group).map(item)}
            </Menu.Group>,
          ])}
        </Menu.RadioGroup>
      </Menu.Content>
    </Menu.Root>
  );
}

/** The row's accessible name: the command, its aliases, and why it cannot run or what it does. */
function commandLabel(c: Command, live: boolean): string {
  const aliases = c.aliases?.length ? `, aliases ${c.aliases.map((name) => '/' + name).join(', ')}` : '';
  return `/${c.name}${aliases}. ${commandReason(c, live) || c.description}`;
}

function CommandRow({ c }: Readonly<{ c: Command }>) {
  return (
    <>
      <span className="shrink-0 text-caption font-medium text-ink">/{c.name}</span>
      {c.aliases?.length ? <span className="truncate text-caption text-muted" title={c.aliases.map((name) => `/${name}`).join(', ')}>{c.aliases.map((name) => `/${name}`).join(', ')}</span> : null}
      {c.input_hint && <span className="min-w-0 truncate text-caption text-muted" title={c.input_hint}>{c.input_hint}</span>}
      {c.description && <span className="min-w-0 flex-1 truncate text-caption text-muted" title={c.description}>{c.description}</span>}
    </>
  );
}

function FileRow({ f }: Readonly<{ f: FileEntry }>) {
  const cut = f.path.lastIndexOf('/');
  const dir = cut >= 0 ? f.path.slice(0, cut + 1) : '';
  const base = f.path.slice(cut + 1);
  return (
    <>
      {f.type === 'directory' ? <Folder aria-hidden="true" className="text-faint" /> : <File aria-hidden="true" className="text-faint" />}
      <span className="flex min-w-0 text-caption">
        {dir && <span className="min-w-0 truncate text-muted" title={f.path}>{dir}</span>}
        <span className="shrink-0 text-ink">
          {base}
          {f.type === 'directory' && '/'}
        </span>
      </span>
    </>
  );
}

const hasFiles = (e: DragEvent) => Array.from(e.dataTransfer?.types ?? []).includes('Files');

/** A new Task's first message with the settings chosen for it; attachments are the files themselves, uploaded once the Task exists. */
export interface FirstMessage {
  settings: TaskDefaults;
  text: string;
  files: string[];
  uploads: { file: File; kind: Kind }[];
}

/** A composer for a Task that does not exist yet: `send` creates it and delivers the message, and throws while nothing was created. */
export interface NewTask {
  projectId: string;
  send: (first: FirstMessage) => Promise<void>;
}

/** A new Task has no provider conversation yet, so no commands or skills; its execution mode shows once it exists. */
const NO_COMMANDS: Command[] = [];

/**
 * A pending question with one question, answered from the composer (DESIGN.md Composer, answer
 * mode): the question it shows, and where the answered or declined interaction goes.
 */
export interface Answering {
  interaction: Interaction;
  question: Question;
  onAnswered: (i: Interaction) => void;
}

/** No option chosen: one array, so nothing derived from it changes between renders. */
const NO_CHOICES: string[] = [];
/** Why files cannot be added while a question waits: an answer is an option or typed text, nothing else. */
const ANSWER_FIRST = 'Answer the question first.';

interface ComposerProps {
  session: SessionDetail;
  onRename: () => void;
  onSessionUpdate: (s: SessionSummary) => void;
  /** Set for a new Task: its settings are `session`'s, changed locally through `onSessionUpdate`. */
  newTask?: NewTask;
  /** Set while a question is answered from here. */
  answering?: Answering | null;
  /** Shows a command's longer output in the Task's side panel; without it, the output stays under the composer. */
  onCommandOutput?: (output: CommandOutput) => void;
}

/** The composer reads everything on the Task but its transcript, so a streamed delta does not re-render it. */
function sameComposerProps(a: ComposerProps, b: ComposerProps): boolean {
  if (a.onRename !== b.onRename || a.onSessionUpdate !== b.onSessionUpdate || a.newTask !== b.newTask || a.answering !== b.answering || a.onCommandOutput !== b.onCommandOutput) return false;
  if (a.session === b.session) return true;
  const keys = new Set([...Object.keys(a.session), ...Object.keys(b.session)] as (keyof SessionDetail)[]);
  keys.delete('items');
  for (const k of keys) if (a.session[k] !== b.session[k]) return false;
  // Streamed text changes items in place and is ignored; a new item (a sent or steered prompt)
  // re-renders, so Up-arrow history sees it.
  return a.session.items?.length === b.session.items?.length;
}

export const Composer = memo(ComposerView, sameComposerProps);

function ComposerView({ session, onRename, onSessionUpdate, newTask, answering = null, onCommandOutput }: Readonly<ComposerProps>) {
  const { meta, metaError, settings: appSettings, dispatch, refreshMeta } = useApp();
  // The catalogs are still on their way: the pickers' slot holds a skeleton, since their values would be a guess.
  const catalogPending = !meta && !metaError;
  // The draft this Task left behind (text, `@` files, finished uploads); read once, on mount.
  const storageKey = newTask ? newTaskKey(newTask.projectId) : draftKey(session.id);
  const [draft] = useState(() => readDraft(storageKey));
  const [promptSettings, setPromptSettings] = useState<PromptSettings | null>(() => draft?.settings ?? null);
  const [text, setText] = useState(draft?.text ?? '');
  const [caret, setCaret] = useState(draft?.text.length ?? 0);
  /** Prompt history browsing (Up/Down/Escape); null until Up recalls an entry. */
  const [browsing, setBrowsing] = useState<Browsing | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  /** A warning, not a failure: the question was answered elsewhere or withdrawn after the note went. */
  const [notice, setNotice] = useState<string | null>(null);
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
  /** Says where a command's output went: the panel does not take focus, so nothing else would. */
  const [announced, setAnnounced] = useState('');
  const pending = useRef<{ key: string; id: string } | null>(null);
  const refocus = useRef(false);
  const live = LIVE.includes(session.state);
  const locked = readOnly(session);
  const last = outcome && (!session.last_submission || outcome.time >= session.last_submission.time) ? outcome : session.last_submission;
  // A longer output goes to the side panel when it arrives; the strip keeps confirmations and choices.
  const stripResult = last?.status === 'accepted' && !dismissedResultIds.includes(last.request_id) ? last.command_result : null;
  const commandResult = onCommandOutput && isPanelOutput(stripResult) ? null : stripResult;
  const dismissResult = (id = last?.request_id ?? null) => {
    if (!id) return;
    const ids = [...dismissedResultIds.filter((previous) => previous !== id), id].slice(-64);
    setDismissedResultIds(ids);
    try { sessionStorage.setItem(resultStorageKey, JSON.stringify(ids)); }
    catch { /* Dismissal still works when browser storage is unavailable. */ }
  };
  const selection: PromptSettings = promptSettings ?? { model: session.model, effort: session.effort ?? '', context_size: session.context_size || 'default' };
  const selectionChanged = selection.model !== session.model || selection.effort !== (session.effort ?? '') || selection.context_size !== (session.context_size || 'default');
  const catalog = modelCatalog(meta, session.provider);
  const models = visibleModels(catalog, appSettings.hidden_models?.[session.provider]);
  const hiddenModel = appSettings.hidden_models?.[session.provider]?.includes(selection.model);
  const selectedModel = catalog.find((m) => m.id === selection.model);
  const modelLabel = modelName(meta, session.provider, selection.model);
  const routed = session.last_model && session.last_model !== session.model ? modelName(meta, session.provider, session.last_model) : null;
  const queue = session.queue ?? [];
  // Cancelling a queued prompt or clearing the queue loses its text, so each is confirmed first (DESIGN.md Confirmations).
  const riskConfirm = useConfirm<{ run: () => void }>();
  const discard = useConfirm<{ kind: 'one'; id: string; text: string; label: string } | { kind: 'all'; count: number }>();
  function confirmDiscard() {
    const d = discard.target;
    if (!d) return;
    discard.close();
    void action('queue', () => (d.kind === 'one' ? api.cancelQueued(session.id, d.id) : api.queueAction(session.id, 'clear')));
  }
  // The queue strip opens and closes through the shared height collapse: it stays mounted through its
  // exit, showing the last queue, and a strip present when the composer mounts (a Task switch) does not grow in.
  const queueStrip = usePresence(queue.length > 0);
  const [shownQueue, setShownQueue] = useState(queue);
  if (queue.length > 0 && queue !== shownQueue) setShownQueue(queue);
  const [queueAtMount] = useState(queue.length > 0);
  const effort = selection.effort;
  const contextSize = selection.context_size;
  const sizes = selectedModel?.context_sizes ?? [];
  const selectedSize = sizes.find((s) => s.id === contextSize);
  const contextLabel = selectedSize?.tokens ? compactTokens(selectedSize.tokens) : sizeLabel(contextSize);
  const noEffort = effortReason(selectedModel);
  const noContext = contextReason(selectedModel, !!session.capabilities.context_size);
  // Neither can change (the Auto model): the picker shows one word and says why, instead of "Default · Default".
  const fixedTuning = !!noEffort && !!noContext;
  const tuningLabel = fixedTuning ? 'Default' : [noEffort ? '' : effort || 'Default', noContext ? '' : contextLabel].filter(Boolean).join(' · ');
  const settingsLocked = !!busy || locked;
  const autopilot = session.execution?.mode === 'autopilot' || session.execution?.objective?.status === 'active';
  const mode = session.mode ?? 'safe';

  /* ---------- `/` and `@` pickers ---------- */

  const textarea = useRef<HTMLTextAreaElement>(null);
  // The control row folds to fit its width (lib/toolbarFold) whenever that or its contents change. Folding writes one
  // attribute on the row, never state, so a resize does not render the composer.
  const toolbar = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const row = toolbar.current;
    if (!row) return;
    const fit = () => void foldToFit(row);
    fit();
    void document.fonts?.ready?.then(fit);
    if (typeof ResizeObserver === 'undefined') return;
    const resized = new ResizeObserver(fit);
    resized.observe(row);
    const changed = new MutationObserver(fit);
    changed.observe(row, { childList: true, characterData: true, subtree: true });
    return () => {
      resized.disconnect();
      changed.disconnect();
    };
  }, []);
  const popup = useRef<HTMLDivElement>(null);
  const pendingCaret = useRef<number | null>(null);
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [highlight, setHighlight] = useState(0);
  const [files, setFiles] = useState<string[]>(draft?.files ?? []);
  const [commandVersion, setCommandVersion] = useState(0);
  const [executionOpen, setExecutionOpen] = useState(false);
  const pendingExecution = useRef<{ mode: 'interactive' | 'autopilot'; id: string } | null>(null);
  const commandKey = `${session.id}:${session.open}:${live}:${session.mode}:${session.execution?.mode}:${commandVersion}`;
  const [commandList, setCommandList] = useState<{ key: string; commands: Command[] | null; error: string | null } | null>(null);
  let commands: Command[] | null = null;
  if (newTask) commands = NO_COMMANDS;
  else if (commandList?.key === commandKey) commands = commandList.commands;
  const commandsError = commandList?.key === commandKey ? commandList.error : null;
  const [fileList, setFileList] = useState<{ q: string; files: FileEntry[]; reason: string } | null>(null);
  const fileSeq = useRef(0);

  const argument = useMemo(() => triggerAt(text, caret) ? null : argumentTrigger(text, caret, commands ?? []), [text, caret, commands]);
  const rawTrigger = useMemo(() => triggerAt(text, caret) ?? argument?.trigger ?? null, [text, caret, argument]);
  const triggerKey = rawTrigger ? `${rawTrigger.kind}${rawTrigger.start}` : null;
  // An answer is plain text, never a command or a file: while answering no picker opens and `/`, `$` and `@` are text.
  const trigger = rawTrigger && dismissed !== triggerKey && !locked && !busy && !answering ? rawTrigger : null;
  const shapedCommand = !answering && /^[/$]\S/.test(text.trim());
  const pendingCommand = !answering && commandPending(text, commands, commandsError);
  const wantCommands = !locked && (executionOpen || trigger?.kind === '/' || trigger?.kind === '$' || pendingCommand);
  const autopilotCommand = commands?.find((c) => c.kind === 'command' && (c.name === 'autopilot' || c.aliases?.includes('autopilot')));
  /** Why the execution mode cannot change now; empty when it can. */
  function describeExecutionReason(): string {
    if (locked) return 'This task is read-only.';
    if (session.state === 'starting') return 'Wait for this task to start.';
    if (commandsError) return commandsError;
    if (!commands) return 'Loading execution controls…';
    if (!autopilotCommand) return 'Autopilot is unavailable for this provider.';
    return commandReason(autopilotCommand, LIVE.includes(session.state));
  }
  const executionReason = describeExecutionReason();

  // Fetching can reopen an exact closed conversation, but never submits a prompt.
  // Catalogue failures hold slash-shaped input instead of falling through to a prompt.
  useEffect(() => {
    if (newTask || !wantCommands || commands || commandsError) return;
    let current = true;
    api.commands(session.id)
      .then((list) => current && setCommandList({ key: commandKey, commands: list, error: null }))
      .catch((e) => current && setCommandList({ key: commandKey, commands: null, error: sentence(describeError(e)) }));
    return () => { current = false; };
  }, [newTask, wantCommands, commands, commandsError, commandKey, session.id]);

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
  const projectId = newTask?.projectId;
  useEffect(() => {
    if (fileQuery === null) return;
    const seq = ++fileSeq.current;
    const timer = window.setTimeout(
      () => {
        (projectId ? api.projectFiles(projectId, fileQuery) : api.files(session.id, fileQuery))
          .then((r) => seq === fileSeq.current && setFileList({ q: fileQuery, files: r.files, reason: r.reason }))
          .catch((e) => seq === fileSeq.current && setFileList({ q: fileQuery, files: [], reason: describeError(e) }));
      },
      fileQuery === '' ? 0 : 120,
    );
    return () => window.clearTimeout(timer);
  }, [fileQuery, session.id, projectId]);

  const items = useMemo<PickerItem[]>(() => {
    if (!trigger) return [];
    if (argument) {
      return (argument.command.input_choices ?? []).filter((c) => c.name.toLowerCase().includes(trigger.query.toLowerCase())).map((c) => ({
        key: c.name, label: `${c.name}. ${c.description}`, render: <><span className="text-caption">{c.name}</span><span className="min-w-0 text-caption text-muted">{c.description}</span></>,
      }));
    }
    if (trigger.kind === '/' || trigger.kind === '$') {
      const matched = filterCommands((commands ?? []).filter((c) => trigger.kind !== '$' || c.kind === 'skill'), trigger.query);
      return [...matched.filter((c) => c.kind !== 'skill'), ...matched.filter((c) => c.kind === 'skill')].map((c) => ({
        key: c.name,
        group: c.kind === 'skill' ? 'Skills' : 'Commands',
        label: commandLabel(c, live),
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
    // Typing an answer replaces the staged option(s): the answer is one or the other.
    if (answering?.question.custom && staged.length && next.trim()) setChosen({ id: answering.interaction.id, choices: NO_CHOICES });
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
  // Finished uploads come back from the draft as done chips; an image shows its stored copy.
  const [uploads, setUploads] = useState<Pending[]>(() => (draft?.attachments ?? []).map((a) => storedUpload(session.id, a)));
  const [dragging, setDragging] = useState(0);
  const media = selectedModel?.media;
  const gateNote = mediaNote(media, modelLabel);
  const activeKinds = uploads.filter((u) => u.status !== 'error').map((u) => u.kind);
  const patch = (key: string, p: Partial<Pending>) => setUploads((u) => u.map((x) => (x.key === key ? { ...x, ...p } : x)));
  // Uploads still in flight; leaving the Task (this composer unmounts) cancels them.
  const inflight = useRef(new Set<() => void>());
  // A new Task's attachments wait here, by chip key, until its first Send uploads them; leaving it drops them.
  const held = useRef(new Map<string, File>());
  useEffect(() => {
    const running = inflight.current;
    return () => {
      for (const abort of running) abort();
    };
  }, []);

  function addFiles(list: File[]) {
    if (locked || !list.length) return;
    // An answer carries no files: a drop or paste while answering is refused with the Attach button's reason.
    if (answering) {
      setNotice(ANSWER_FIRST);
      return;
    }
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
      if (kind === 'image') {
        const reader = new FileReader();
        reader.onload = () => { if (typeof reader.result === 'string') patch(key, { preview: reader.result }); };
        reader.readAsDataURL(file);
      }
      if (newTask) {
        held.current.set(key, file);
        next.push({ key, name: file.name, size: file.size, kind, progress: 1, status: 'done' });
        continue;
      }
      const up = api.upload(session.id, file, (p) => patch(key, { progress: p }), selection.model);
      next.push({ key, name: file.name, size: file.size, kind, progress: 0, status: 'uploading', abort: up.abort });
      inflight.current.add(up.abort);
      up.done
        .finally(() => inflight.current.delete(up.abort))
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
    held.current.delete(key);
    setUploads((u) => u.filter((x) => x.key !== key));
  }

  const uploadsFull = activeKinds.length >= LIMITS.count;
  const fullReason = uploadsFull ? `A message carries at most ${LIMITS.count} attachments.` : '';
  const attachReason = answering ? ANSWER_FIRST : fullReason;
  const uploading = uploads.some((u) => u.status === 'uploading');
  const refused = uploads.some((u) => u.status === 'error');
  const attachmentIds = uploads.flatMap((u) => (u.status === 'done' && u.id ? [u.id] : []));
  const extras = { ...(files.length ? { files } : {}), ...(attachmentIds.length ? { attachments: attachmentIds } : {}) };

  /* ---------- Answer mode ---------- */

  // A question is answered from its own buffer: the Task's draft (text, `@` files, uploads) is parked
  // when the question arrives and comes back when it resolves, however it resolves. The parked draft
  // is what the storage keeps meanwhile. An answer left unsent stays only where the draft was empty.
  const answeringId = answering?.interaction.id ?? null;
  // The options chosen on the question, its own: another question starts with its recommended option
  // staged, else none. Once per question, so a pick the owner changes or clears stays that way: the
  // tab's storage keeps it per Task (one question at a time), so leaving the Task or reloading restores it.
  const choicesKey = `uam:question-choices:${session.id}`;
  const [chosen, setChosen] = useState<{ id: string; choices: string[] }>(() => {
    try {
      const saved: unknown = JSON.parse(sessionStorage.getItem(choicesKey) ?? 'null');
      if (saved && typeof saved === 'object' && 'id' in saved && 'choices' in saved && typeof saved.id === 'string' && Array.isArray(saved.choices)) {
        return { id: saved.id, choices: saved.choices.filter((c): c is string => typeof c === 'string') };
      }
    } catch { /* Nothing kept: the question stages as it arrives. */ }
    return { id: '', choices: NO_CHOICES };
  });
  useEffect(() => {
    if (!chosen.id) return;
    try { sessionStorage.setItem(choicesKey, JSON.stringify(chosen)); }
    catch { /* The pick holds until the composer leaves. */ }
  }, [chosen, choicesKey]);
  // Resolved here, the question's pick goes with it.
  const askedId = useRef(answeringId);
  useEffect(() => {
    if (askedId.current && !answeringId) {
      try { sessionStorage.removeItem(choicesKey); }
      catch { /* Overwritten by the next question. */ }
    }
    askedId.current = answeringId;
  }, [answeringId, choicesKey]);
  if (answering && chosen.id !== answering.interaction.id) {
    const recommended = recommendedChoice(answering.question.choices);
    setChosen({ id: answering.interaction.id, choices: recommended ? [recommended] : NO_CHOICES });
  }
  const staged = answeringId && chosen.id === answeringId ? chosen.choices : NO_CHOICES;
  const [parked, setParked] = useState<{ id: string; text: string; files: string[]; uploads: Pending[] } | null>(null);
  if (answeringId && parked?.id !== answeringId) {
    setParked(parked ? { ...parked, id: answeringId } : { id: answeringId, text, files, uploads });
    setText('');
    setCaret(0);
    setFiles([]);
    setUploads([]);
    setBrowsing(null);
    setDismissed(null);
  } else if (!answeringId && parked) {
    setParked(null);
    if (parked.text || parked.files.length || parked.uploads.length || !(text.trim() || files.length || uploads.length)) {
      setText(parked.text);
      setCaret(parked.text.length);
      setFiles(parked.files);
      setUploads(parked.uploads);
    }
  }
  // A question arriving on a desktop is an invitation to answer: the composer takes focus when nothing
  // else holds it. Never on a touch screen, where the keyboard would rise over the conversation.
  useEffect(() => {
    if (!answeringId || window.matchMedia(COARSE).matches) return;
    const active = document.activeElement;
    if (!active || active === document.body) textarea.current?.focus();
  }, [answeringId]);

  // The draft follows the text, the picked files and the finished uploads once typing pauses;
  // leaving the Task writes it at once. An accepted send clears it (see `send`).
  const buffer = useMemo(() => ({ text, files, uploads }), [text, files, uploads]);
  const kept = parked ?? buffer;
  const draftNow = useMemo<Draft>(() => ({ text: kept.text, files: kept.files, ...(promptSettings ? { settings: promptSettings } : {}), attachments: kept.uploads.flatMap((u) => (u.status === 'done' && u.id ? [{ id: u.id, name: u.name, size: u.size, kind: u.kind }] : [])) }), [kept, promptSettings]);
  const latestDraft = useRef(draftNow);
  useEffect(() => {
    latestDraft.current = draftNow;
    const timer = window.setTimeout(() => writeDraft(storageKey, draftNow), DRAFT_DELAY);
    return () => window.clearTimeout(timer);
  }, [draftNow, storageKey]);
  useEffect(() => () => writeDraft(storageKey, latestDraft.current), [storageKey]);

  /* ---------- Sending ---------- */

  // After a turn that failed, was stopped or was interrupted, the last prompt can come back into an empty
  // composer: its text and its stored uploads (file references are not recorded on the item). Never sent by itself.
  const failedTurn = session.state === 'failed' || session.state === 'interrupted' || session.state === 'cancelled';
  const lastSent = failedTurn && !locked && !text.trim() && !files.length && !uploads.length ? lastPrompt(session.items) : null;
  // Offered only when there is something to put back: its text or an upload with a stored copy. A prompt of
  // only file references, or of uploads without an ID, offers nothing rather than an older prompt.
  const resendable = lastSent?.text?.trim() || lastSent?.attachments?.some((a) => a.id) ? lastSent : null;
  function resend() {
    if (!resendable) return;
    const t = resendable.text ?? '';
    pendingCaret.current = t.length;
    updateText(t, t.length);
    setUploads((resendable.attachments ?? []).flatMap((a) => (a.id ? [storedUpload(session.id, { id: a.id, name: a.name, size: a.size ?? 0, kind: kindOf(a.mime) })] : [])));
    textarea.current?.focus();
  }

  const cmd = commands && !answering ? parseCommand(text, commands) : null;
  const descriptor = commands?.find((c) => c.name === cmd?.name);
  const commandBlocked = commandReason(descriptor, live) || (descriptor?.input_required && !cmd?.args ? `/${descriptor.name} needs ${descriptor.input_hint || 'an argument'}.` : '');
  /** Why nothing can be sent now; empty when it can. */
  function describeBlocked(): string {
    if (uploading) return 'Wait for the upload to finish';
    if (refused) return 'Remove the attachment that was refused';
    if (pendingCommand) return 'Wait for the command list to load';
    if (shapedCommand && commandsError) return 'Commands could not be loaded. Retry the command list.';
    return commandBlocked;
  }
  const blocked = describeBlocked();
  const settingsSteerReason = live && selectionChanged ? 'Model, effort and context changes apply to the next turn; Send now keeps the current turn’s settings.' : '';
  const steerBlocked = steerUnavailable || settingsSteerReason;
  // A message needs text, a file reference or a finished upload; blank text alongside them goes as none.
  const empty = answering ? !canAnswer(answering.question, staged, text) : !text.trim() && !files.length && !uploads.some((u) => u.status === 'done');
  const cannotSubmit = !!busy || locked || session.state === 'starting' || empty || !!blocked;
  // Enter does the setting's action, Ctrl/Cmd+Enter the other (issue #183); when a steer is impossible both queue.
  const { enter, modified } = enterActions(live, appSettings.send_default, !!steerBlocked);
  // While a turn runs a message has one Send for Enter's action, named and drawn for it, and a menu beside it with the other.
  const twoChoices = live && !cmd && !answering && !locked && !newTask;
  const primary = enter === 'steer' ? 'steer' : 'queue';
  const other = primary === 'steer' ? 'queue' : 'steer';
  const PrimaryIcon = CHOICE_ICON[primary];

  /** A new Task's first Send: the text stays here, with the reason, unless the Task was created. */
  async function sendFirst(t: string) {
    if (!newTask) return;
    const heldUploads = uploads.flatMap((u) => {
      const file = held.current.get(u.key);
      return u.status === 'done' && file ? [{ file, kind: u.kind }] : [];
    });
    refocus.current = true;
    setBusy('send');
    setError(null);
    try {
      await newTask.send({ settings: { provider: session.provider, model: session.model, effort, context_size: contextSize, mode }, text: t, files, uploads: heldUploads });
      // The Task exists and holds the message now (or its composer does); this draft is done.
      latestDraft.current = { text: '', files: [], attachments: [] };
      writeDraft(storageKey, latestDraft.current);
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  /** What an accepted send leaves behind: nothing (a command's prefill aside). */
  function clearBuffer(prefill = '') {
    setText(prefill);
    setCaret(prefill.length);
    setFiles([]);
    setUploads([]);
    setDismissed(null);
  }

  /** Answer mode's send: the staged options or the typed text, and nothing else. */
  async function sendAnswer() {
    if (!answering || cannotSubmit) return;
    const answers = answerFromComposer(answering.question, text, staged);
    if (!answers) return;
    refocus.current = true;
    setBusy('answer');
    setError(null);
    setNotice(null);
    try {
      const answered = await api.respond(session.id, answering.interaction.id, { answers });
      clearBuffer();
      answering.onAnswered(answered);
    } catch (e) {
      // Answered from another tab or withdrawn: the card's wording, as a note.
      if (isStatus(e, 409)) setNotice('This request was already answered elsewhere.');
      else if (isStatus(e, 410)) setNotice('This request expired before it was answered.');
      else setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  /** Declines the question from the action row; the card's rules (a 409 or 410 is a note, not a failure). */
  async function decline() {
    if (!answering || busy) return;
    setBusy('decline');
    setError(null);
    setNotice(null);
    try {
      answering.onAnswered(await api.respond(session.id, answering.interaction.id, { reject: true }));
    } catch (e) {
      if (isStatus(e, 409)) setNotice('This request was already answered elsewhere.');
      else if (isStatus(e, 410)) setNotice('This request expired before it was answered.');
      else setError(describeError(e));
    } finally {
      setBusy(null);
    }
  }

  async function send(promptMode: PromptMode, confirmed = false) {
    if (answering) return sendAnswer();
    const t = text.trim();
    if (cannotSubmit || (!cmd && promptMode === 'send' && live)) return;
    if (newTask) return sendFirst(t);
    if (cmd && !confirmed && entersRiskiest(cmd.name, cmd.args, { yolo: mode === 'yolo', autopilot })) {
      riskConfirm.ask({ run: () => void send(promptMode, true) });
      return;
    }
    if (!cmd && promptMode === 'steer' && steerBlocked) {
      setError(steerBlocked);
      return;
    }
    const key = JSON.stringify([t, files, attachmentIds, cmd?.name ?? '', cmd ? null : selection]);
    if (pending.current?.key !== key) pending.current = { key, id: newRequestId() };
    const id = pending.current.id;
    refocus.current = true;
    setBusy(promptMode);
    setError(null);
    try {
      const sub = cmd ? await api.command(session.id, cmd.name, cmd.args, id, extras) : await api.prompt(session.id, t, id, promptMode, { ...extras, settings: selection });
      setOutcome(sub);
      if (sub.status === 'accepted' || sub.status === 'queued') {
        pending.current = null;
        const result = sub.command_result;
        const prefill = result?.kind === 'text' ? result.prefill_input ?? '' : '';
        clearBuffer(prefill);
        if (cmd) {
          if (result?.kind === 'action') setCommandAction(result.action);
          if (onCommandOutput && isPanelOutput(result)) {
            onCommandOutput(panelOutput(sub.request_id, cmd.name, result));
            setAnnounced(`Output of /${cmd.name} is in the side panel.`);
          }
          setCommandVersion((v) => v + 1);
        }
        setPromptSettings(null);
        // Gone at once, not after the debounce: a reload right after sending must not bring the prompt back.
        writeDraft(storageKey, { text: prefill, files: [], attachments: [] });
      }
    } catch (e) {
      if (!cmd && promptMode === 'steer' && isStatus(e, 409) && e.message.includes('cannot steer a running turn')) {
        setSteerUnavailable('This agent cannot take a message during a turn');
        setError('This agent cannot take a message during a turn. Your message is still here; Enter sends it after this turn.');
        return;
      }
      if (errorCode(e) === SIGNED_OUT) {
        // The plain reason, and the banner above the pane says it too.
        setError(`${describeError(e)} Your message is still here.`);
        refreshMeta();
        return;
      }
      setError(`${describeError(e)}. Nothing will be retried automatically. Repeating this action with unchanged message and settings uses the same request (${id.slice(0, 8)}).`);
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
  const settings = (body: Parameters<typeof api.settings>[1]) => {
    const changed = changeSettings({ provider: session.provider, ...selection, mode }, body, catalog.find((m) => m.id === body.model));
    if (newTask) return onSessionUpdate({ ...session, ...changed });
    if (body.mode === undefined && (live || promptSettings)) {
      setPromptSettings({ model: changed.model, effort: changed.effort, context_size: changed.context_size });
      return;
    }
    return action('settings', async () => onSessionUpdate(await api.settings(session.id, body)));
  };

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
      if (sub.status === 'accepted' || sub.status === 'rejected') pendingExecution.current = null;
      if (sub.status === 'accepted') {
        setCommandVersion((v) => v + 1);
        dispatch({ type: 'detail_loaded', detail: await api.session(session.id) });
      }
    });
  }

  // The reply the owner would likely send next, as ghost text in the empty composer (Assist.tsx).
  const suggestion = useSuggestion(session, !!newTask || !!answering || locked || !!busy || text !== '');
  /** Puts the suggestion in the composer, the caret at its end; nothing is sent. */
  function takeSuggestion() {
    updateText(suggestion, suggestion.length);
    pendingCaret.current = suggestion.length;
    textarea.current?.focus();
  }

  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.nativeEvent.isComposing) return;
    if (suggestion && (e.key === 'ArrowRight' || e.key === 'End') && !e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault();
      takeSuggestion();
      return;
    }
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
      // An empty list (no match, a reason, still loading): Enter closes it rather than sending a half-typed token.
      if (e.key === 'Enter' && !e.shiftKey && enterInPicker(items.length, !!argument) === 'close') {
        e.preventDefault();
        setDismissed(triggerKey);
        return;
      }
    }
    // Terminal-style history: Up from the first line recalls earlier prompts, Down from the last
    // line comes back, Escape restores the draft. Only the text changes; chips and uploads stay.
    if (!e.shiftKey && !e.altKey && !e.ctrlKey && !e.metaKey) {
      const step = historyKey(browsing, historyEntries(session.items, queue), text, caret, e.key);
      if (step) {
        e.preventDefault();
        setBrowsing(step.browsing);
        pendingCaret.current = step.text.length;
        setText(step.text);
        setCaret(step.text.length);
        return;
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      void send(e.ctrlKey || e.metaKey ? modified : enter);
    }
  }

  /** The send button's name: what Enter does now. */
  function describeSend(): string {
    if (answering) return busy === 'answer' ? 'Submitting…' : 'Answer';
    if (busy === enter) return 'Submitting…';
    if (cmd) return `Run /${cmd.name}`;
    if (!live) return 'Send';
    return enter === 'steer' ? 'Steer' : 'Queue';
  }
  const sendLabel = describeSend();
  /** The line under the picker's rows: a reason, an error; none when there is nothing to say. */
  function describePickerNote(): string | null {
    if (trigger?.kind === '/' || trigger?.kind === '$') {
      if (newTask && !argument) return `${trigger.kind === '$' ? 'Skills are' : 'Commands are'} available after the first message.`;
      if (commandsError) return commandsError;
      return items[hi]?.disabled ? commandReason(commands?.find((c) => c.name === items[hi].key), live) : null;
    }
    if (trigger?.kind === '@' && fileList?.q === trigger.query && fileList.reason) return sentence(fileList.reason);
    return null;
  }
  const pickerNote = describePickerNote();
  /** What the picker says instead of rows when there are none. */
  function describePickerEmpty(): string | null {
    if (trigger?.kind === '/' || trigger?.kind === '$') {
      if (argument) return 'Type arguments, then press Enter to run.';
      if (newTask) return null;
      if (commands?.length === 0) return 'This task has no commands.';
      return trigger.query ? `No command matches “/${trigger.query}”. Enter closes the list; Enter again sends it as text.` : null;
    }
    if (trigger?.kind === '@') return trigger.query ? `No file matches “${trigger.query}”.` : 'No files to reference.';
    return null;
  }
  const pickerEmpty = describePickerEmpty();
  /** The picker's heading: the command's arguments, skills, commands or files. */
  function describePickerTitle(): string {
    if (argument) return `/${argument.command.name} arguments`;
    if (trigger?.kind === '$') return 'Skills';
    if (trigger?.kind === '/') return 'Commands';
    return 'Files';
  }
  const modeChoices: Choice[] = [
    { value: 'safe', label: 'Safe', description: MODE_TEXT.safe },
    { value: 'yolo', label: 'Yolo', description: MODE_TEXT.yolo },
  ];
  // Yolo with autopilot is the riskiest pair: every way into it is confirmed first.
  const chooseMode = (next: 'safe' | 'yolo') => {
    if (next === 'yolo' && mode !== 'yolo' && autopilot) riskConfirm.ask({ run: () => void settings({ mode: next }) });
    else void settings({ mode: next });
  };
  const chooseExecution = (next: 'interactive' | 'autopilot') => {
    if (next === 'autopilot' && mode === 'yolo' && !autopilot) riskConfirm.ask({ run: () => void changeExecution(next) });
    else void changeExecution(next);
  };
  // The permission group, shared by the toolbar's permissions and execution menu and the phone's More menu.
  const permissionItems = (
    <Menu.RadioGroup value={mode} onValueChange={(v) => chooseMode(v as 'safe' | 'yolo')}>
      <Menu.Label>Permissions</Menu.Label>
      {modeChoices.map((c) => <Menu.RadioItem key={c.value} value={c.value} description={c.description} disabled={!!busy}>{c.label}</Menu.RadioItem>)}
    </Menu.RadioGroup>
  );
  const executionSupported = !newTask && !!session.capabilities.execution_modes;
  const executionKnown = executionSupported && session.execution?.known === true && !!session.execution.mode;
  const executionMode = executionKnown ? session.execution!.mode! : '';
  const permission = mode === 'yolo' ? 'Yolo' : 'Safe';
  const execution = executionMode.charAt(0).toUpperCase() + executionMode.slice(1);
  const runLabel = [permission, execution].filter(Boolean).join(' · ');
  // The toolbar's folds shorten it to the permission, then to the glyph alone.
  const runFace = (
    <span className="max-w-40 truncate in-data-[fold~=permissions]:hidden">
      {permission}
      {execution && <span className="in-data-[fold~=execution]:hidden"> · {execution}</span>}
    </span>
  );
  const risk = RISKS[(mode === 'yolo' ? 2 : 0) + (autopilot ? 1 : 0)];
  const runIcon = <risk.Icon aria-hidden="true" className={risk.tone} />;
  const riskLine = (
    <p className={cn('flex max-w-72 items-start gap-2 px-2 py-1 text-caption', risk.tone)}>
      <risk.Icon aria-hidden="true" className="mt-0.5 size-3.5 shrink-0" />
      {risk.text}
    </p>
  );
  // The effort and context groups, shared by the toolbar picker and the phone's More menu.
  const tuningItems = (
    <>
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
    </>
  );
  /** Why effort and context cannot change now, or that this model has nothing to change. */
  function describeTuningReason(): string {
    if (locked) return 'This task is read-only.';
    if (busy) return 'Wait for the current action to finish.';
    return 'This model uses its default effort and context size.';
  }
  const tuningReason = describeTuningReason();
  const objectiveSuffix = executionKnown && session.execution?.objective ? ` · ${session.execution.objective.status}` : '';
  /** The textarea's placeholder: what Enter does while a turn runs, else the invitation. */
  function describePlaceholder(): string {
    if (locked) return '';
    if (answering) return answerPlaceholder(answering.question, staged.length > 0);
    if (!live) return 'Ask anything, @ files, $ skills, / commands';
    return enter === 'steer' ? 'Send now to guide this turn, or after it…' : 'Send after this turn, or now to guide it…';
  }
  /** The send button's tip: why it is blocked, or what Enter and Ctrl+Enter do. */
  function describeSendTip(): ReactNode {
    if (blocked) return blocked;
    if (answering) {
      return (
        <>
          Answer (Enter)
          <span className="block text-on-primary/70">Shift+Enter adds a line</span>
        </>
      );
    }
    return (
      <>
        {cmd ? `Run /${cmd.name} (Enter)` : 'Send (Enter)'}
        <span className="block text-on-primary/70">Shift+Enter adds a line</span>
      </>
    );
  }
  /** A turn-time action's tip: what it does and its key, or why Send now cannot. */
  function describeChoiceTip(mode: 'queue' | 'steer'): ReactNode {
    if (blocked) return blocked;
    if (mode === 'steer' && steerBlocked) return steerBlocked;
    const shortcut = mode === enter ? 'Enter' : modified === mode ? MODIFIED_KEY : '';
    return (
      <>
        {mode === 'steer' ? 'Send now: the agent reads it before its next step' : 'After this turn: sent when the current turn ends'}
        {shortcut && <span className="block text-on-primary/70">{shortcut} · Shift+Enter adds a line</span>}
      </>
    );
  }
  // The menu's one item: the action Enter does not, dimmed with its reason when a steer is impossible.
  const otherBlocked = other === 'steer' ? steerBlocked : '';
  const otherAction: ActionItem = {
    key: other,
    label: CHOICE_LABEL[other],
    hint: otherBlocked ? undefined : MODIFIED_KEY,
    disabled: !!otherBlocked,
    reason: otherBlocked,
    takesFocus: true,
    onSelect: () => void send(other),
  };
  /** The confirmation for a discard: the one prompt, or the whole queue. */
  function describeDiscard(): string {
    if (discard.target?.kind !== 'all') return 'Cancel this waiting message?';
    return discard.target.count === 1 ? 'Clear the waiting message?' : `Clear ${discard.target.count} waiting messages?`;
  }
  /** What cancelling loses: the text, or for a prompt of only attachments or files, just its sending. */
  function describeDiscardLoss(): string {
    if (discard.target?.kind === 'all') return 'Their text is not kept; nothing else changes.';
    return discard.target?.text ? 'Its text is not kept; nothing else changes.' : 'It will not be sent; nothing else changes.';
  }
  // A command's result under the composer: choices to pick from, Markdown, or plain text.
  let resultBody: ReactNode = null;
  if (commandResult?.kind === 'select') {
    resultBody = (
      <>
        <p className="text-caption text-muted">{commandResult.title}</p>
        <div className="max-h-40 overflow-y-auto">
          {commandResult.options.map((choice) => <Button key={choice.name} size="sm" variant="subtle" disabled={!!busy || locked} className="h-auto min-h-8 w-full justify-start whitespace-normal text-left pointer-coarse:min-h-11" onClick={() => {
            const next = `/${commandResult.command} ${choice.name} `;
            updateText(next, next.length); pendingCaret.current = next.length; dismissResult(); textarea.current?.focus();
          }}><span>{choice.name}</span><span className="text-caption text-muted">{choice.description}</span></Button>)}
        </div>
      </>
    );
  } else if (commandResult?.kind === 'text' && commandResult.markdown) {
    resultBody = <Markdown text={commandResult.text} />;
  } else if (commandResult && commandResult.kind !== 'action') {
    resultBody = <p className="whitespace-pre-wrap" role="status">{commandResult.text || 'Command completed.'}</p>;
  }
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
      // `data-draft` marks unsent work (text, picked files, uploads, a parked draft); an update waits while it is set.
      data-draft={text.trim() || files.length || uploads.length || parked?.text.trim() || parked?.files.length || parked?.uploads.length ? '' : undefined}
      className={cn(
        // The floating control plane (DESIGN.md Composer): `lg` corners on the float shadow; focus-within fades in
        // (opacity only) a pseudo-element carrying a deeper neutral shadow (no glow), so no shadow is ever animated.
        "relative isolate flex flex-col rounded-lg bg-raised shadow-float before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:rounded-[inherit] before:opacity-0 before:shadow-focus-float before:transition-opacity before:duration-160 before:content-[''] focus-within:before:opacity-100",
        locked && 'bg-surface',
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
      {dragging > 0 && !answering && <DropOverlay note={gateNote || LIMITS_TEXT} />}
      {trigger && (
        <InlinePicker
          id={LIST_ID}
          title={describePickerTitle()}
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
      {answering && (
        // Answer mode (DESIGN.md Composer): the question is the composer's extension, above what answers it.
        // Choosing an option replaces a typed answer, as typing replaces the option.
        <ComposerQuestion
          interactionId={answering.interaction.id}
          question={answering.question}
          chosen={staged}
          disabled={!!busy || locked}
          onChoose={(choices) => {
            setChosen({ id: answering.interaction.id, choices });
            if (choices.length && answering.question.custom && text) updateText('', 0);
          }}
        />
      )}
      {(locked || resendable || last?.status === 'uncertain' || last?.status === 'rejected' || error || notice || commandBlocked || (shapedCommand && commandsError) || (live && steerBlocked && !answering) || selectionChanged) && (
        <div className="flex flex-col gap-1 px-3.5 pt-2 pb-1">
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
          {notice && (
            <Note tone="warn" role="alert">
              {notice}
            </Note>
          )}
          {commandBlocked && <Note role="status">{commandBlocked}</Note>}
          {shapedCommand && commandsError && <Note tone="error" role="alert">{commandsError} <Button size="sm" variant="subtle" onClick={() => { setCommandVersion((v) => v + 1); setDismissed(null); textarea.current?.focus(); }}>Retry commands</Button></Note>}
          {live && steerUnavailable && !cmd && !answering && <Note>{steerUnavailable}. Enter sends your message after this turn.</Note>}
          {selectionChanged && <Note>Current model: {modelName(meta, session.provider, session.model)} · {session.effort || 'Default'} effort · {sizeLabel(session.context_size || 'default')} context. Draft settings apply when its next turn starts.</Note>}
          {settingsSteerReason && !cmd && !answering && <Note>{settingsSteerReason}</Note>}
          {resendable && (
            <Tip label="Puts the last prompt back here to edit or send again. Nothing is sent until you do.">
              <Button size="sm" variant="secondary" className="self-start animate-rise" onClick={resend}>
                <RotateCcw />
                Resend last prompt
              </Button>
            </Tip>
          )}
        </div>
      )}
      {onCommandOutput && <p role="status" className="sr-only">{announced}</p>}
      {commandResult && commandResult.kind !== 'action' && (
        <div className="px-3.5 py-2 text-ui text-body">
          <div className="flex items-center gap-2 pb-1"><span className="text-caption text-muted">Command result</span><span className="flex-1" /><Button size="icon-sm" variant="subtle" aria-label="Dismiss command result" onClick={() => dismissResult()}><X /></Button></div>
          {resultBody}
        </div>
      )}
      {queueStrip.mounted && (
        <Collapse open={queue.length > 0} appear={!queueAtMount} onClosed={queueStrip.onClosed}>
        <details className="group/queue px-3.5 py-1.5" open>
          <summary className="flex h-6 list-none items-center gap-2 text-caption text-muted select-none [&::-webkit-details-marker]:hidden">
            <ListEnd aria-hidden="true" className="size-3.5" />
            <span className="tabular-nums">{shownQueue.length} waiting</span>
            <span aria-hidden="true">·</span>
            <span>{session.queue_paused ? 'Paused' : 'Sent after this turn'}</span>
            <span className="flex-1" />
            {session.queue_paused && (
              <Button size="sm" variant="secondary" className="h-6" disabled={!!busy || locked} onClick={() => void action('queue', () => api.queueAction(session.id, 'resume'))}>
                Resume
              </Button>
            )}
            <Button size="sm" variant="subtle" className="h-6 text-muted" disabled={!!busy || locked} onClick={() => discard.ask({ kind: 'all', count: shownQueue.length })}>
              Clear
            </Button>
          </summary>
          <ol className="flex flex-col gap-0.5 pb-1">
            {shownQueue.map((q, i) => (
              <li key={q.request_id} className="flex items-start gap-2 text-ui text-body">
                <span className="mt-0.5 w-4 shrink-0 text-right text-caption tabular-nums text-muted">{i + 1}</span>
                <span className="flex min-w-0 flex-1 flex-col gap-1">
                  {q.text && <span className="truncate" title={q.text}>{q.text}</span>}
                  {q.settings && <span className="text-caption text-muted">{modelName(meta, session.provider, q.settings.model)} · {q.settings.effort || 'Default'} effort · {sizeLabel(q.settings.context_size)} context</span>}
                  <QueuedExtras files={q.files} attachments={q.attachments} />
                </span>
                <Button size="icon-sm" variant="subtle" className="text-muted" aria-label={`Cancel waiting message: ${queuedLabel(q)}`} disabled={!!busy || locked} onClick={() => discard.ask({ kind: 'one', id: q.request_id, text: q.text, label: queuedLabel(q) })}>
                  <X />
                </Button>
              </li>
            ))}
          </ol>
        </details>
        </Collapse>
      )}
      {(riskConfirm.target || riskConfirm.props.open) && (
        <AlertDialog
          {...riskConfirm.props}
          title="Switch to Yolo with Autopilot?"
          description="The agent will allow every permission request itself and keep working between turns without waiting for you."
          confirmLabel="Switch"
          onConfirm={() => {
            const run = riskConfirm.target?.run;
            riskConfirm.close();
            run?.();
          }}
        />
      )}
      {(discard.target || discard.props.open) && (
        <AlertDialog
          {...discard.props}
          title={describeDiscard()}
          description={describeDiscardLoss()}
          confirmLabel={discard.target?.kind === 'all' ? 'Clear all' : 'Cancel message'}
          cancelLabel="Keep"
          onConfirm={confirmDiscard}
        >
          {discard.target?.kind === 'one' && (
            <p className="mt-3 line-clamp-3 rounded-sm bg-sunken px-3 py-2 text-ui text-ink" title={discard.target.label}>
              {discard.target.label}
            </p>
          )}
        </AlertDialog>
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
        {answering ? 'Your answer' : 'Message'}
      </label>
      <div className="relative">
      <textarea
        ref={textarea}
        id="composer-text"
        rows={2}
        value={text}
        placeholder={suggestion ? '' : describePlaceholder()}
        aria-describedby={suggestion ? SUGGESTION_ID : undefined}
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
      {suggestion && <SuggestionGhost text={suggestion} onUse={takeSuggestion} />}
      </div>

      {/* One control row (DESIGN.md D3): Attach and the pickers at left, the actions at right. On a phone the effort,
          context, permissions and execution pickers fold into a More menu; a narrow row folds its labels in priority order
          (lib/toolbarFold), the model's last. The actions are glyphs, named for screen readers and in their tooltips;
          while a turn runs they stay on the row and wrap under it only when the pickers cannot keep their touch targets
          beside them. */}
      <div ref={toolbar} className={cn('flex items-center gap-0.5 px-2 pt-1 pb-2 data-[fold~=wrap]:flex-wrap data-[fold~=wrap]:gap-y-1', twoChoices && 'max-sm:flex-wrap max-sm:gap-y-1')}>
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
                    {newTask && <span className="block text-on-primary/70">Kept until you leave this new task.</span>}
                  </>
                )
              }
            >
              <Button
                size="icon"
                variant="subtle"
                aria-label={`Attach files. ${attachReason || gateNote}`.trim()}
                aria-disabled={attachReason ? 'true' : undefined}
                className="text-muted"
                onClick={() => !attachReason && fileInput.current?.click()}
              >
                <Paperclip />
              </Button>
            </Tip>
          </>
        )}
        {catalogPending ? (
          <Skeleton label="Loading the model catalog…" rows={1} className="w-48" rowClassName="h-5 w-full" />
        ) : (
          <>
        <Picker
          id="composer-model"
          icon={<Cpu aria-hidden="true" className="text-faint" />}
          label="Model"
          value={selection.model}
          display={modelLabel}
          choices={models.map((m) => {
            const estimate = estimateTurnCost(m, session.context, contextSize);
            const prices = session.capabilities.usage ? modelCostLine(m) : '';
            const group = appSettings.custom_models?.find((c) => `${c.name}/${c.model_id}` === m.id)?.name;
            return { value: m.id, label: m.name, group, description: [prices, session.capabilities.usage && estimate !== null ? `≈ ${formatCredits(estimate)} credits / turn, input only` : ''].filter(Boolean).join(' · ') };
          })}
          disabled={settingsLocked}
          // On a phone it wraps the row by its 44px target, not its label, and grows back to the label where there is room.
          className="pointer-coarse:min-w-11 max-sm:max-w-max max-sm:grow max-sm:basis-11"
          reason={locked ? 'This task is read-only.' : undefined}
          hint={routed ? `Latest turn ran on ${routed}` : undefined}
          onChange={(v) => void settings({ model: v })}
        />
        {hiddenModel && <span className="text-caption text-muted max-sm:hidden">Hidden in Settings</span>}
        <ComposerUsage session={session} model={catalog.find((m) => m.id === session.model)} />
        <span aria-hidden="true" className={cn('mx-1 h-4 w-px bg-hairline-strong max-sm:hidden', !locked && 'in-data-[fold~=more]:hidden')} />
        {settingsLocked || fixedTuning ? (
          <Tip label={<>{`Effort and context size: ${tuningLabel}`}<span className="block text-on-primary/70">{tuningReason}</span></>}>
            <Button id="composer-effort-context" size="sm" variant="subtle" aria-disabled="true" aria-label={`Effort and context size: ${tuningLabel}. ${tuningReason}`} className={cn('text-muted max-sm:hidden', !locked && 'in-data-[fold~=more]:hidden')}>
              <Gauge aria-hidden="true" className="text-faint" />
              <span className="in-data-[fold~=tuning]:hidden">{tuningLabel}</span>
              <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
            </Button>
          </Tip>
        ) : (
          <Menu.Root modal={false}>
            <Tip label={`Effort and context size: ${tuningLabel}`}>
              <Menu.Trigger render={<Button id="composer-effort-context" size="sm" variant="subtle" aria-label={`Effort and context size: ${tuningLabel}`} className="max-sm:hidden in-data-[fold~=more]:hidden" />}>
                <Gauge aria-hidden="true" className="text-faint" />
                <span className="in-data-[fold~=tuning]:hidden">{tuningLabel}</span>
                <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
              </Menu.Trigger>
            </Tip>
            <Menu.Content side="top" align="start">{tuningItems}</Menu.Content>
          </Menu.Root>
        )}
        <span aria-hidden="true" className={cn('mx-1 h-4 w-px bg-hairline-strong max-sm:hidden', !locked && 'in-data-[fold~=more]:hidden')} />
        {/* Permissions and execution: one menu, since both say how freely the agent acts; its glyph follows both. */}
        {locked ? (
          <Tip label={<>{`Permissions and execution: ${runLabel}`}<span className="block text-on-primary/70">This task is read-only.</span></>}>
            <Button id="composer-mode" size="sm" variant="subtle" aria-disabled="true" aria-label={`Permissions and execution: ${runLabel}. This task is read-only.`} className="text-muted max-sm:hidden">
              {runIcon}
              {runFace}
              <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
            </Button>
          </Tip>
        ) : (
          <Menu.Root modal={false} onOpenChange={setExecutionOpen}>
            <Tip label={<>{`Permissions and execution: ${runLabel}${objectiveSuffix}`}<span className="block text-on-primary/70">{risk.text}</span></>}>
              <Menu.Trigger render={<Button id="composer-mode" size="sm" variant="subtle" aria-label={`Permissions and execution: ${runLabel}`} aria-busy={!!busy} className="max-sm:hidden in-data-[fold~=more]:hidden" />}>
                {runIcon}
                {runFace}
                <ChevronDown aria-hidden="true" className="!size-3 text-faint" />
              </Menu.Trigger>
            </Tip>
            <Menu.Content side="top" align="start" className="max-w-80">
              {riskLine}
              <Menu.Separator />
              {permissionItems}
              {executionSupported && (
                <>
                  <Menu.Separator />
                  <ExecutionItems execution={session.execution} reason={executionReason} busy={!!busy} onChange={chooseExecution} onRetry={commandsError ? () => setCommandVersion((v) => v + 1) : undefined} />
                </>
              )}
            </Menu.Content>
          </Menu.Root>
        )}
        {!newTask && <BackgroundTasks key={session.id} sessionId={session.id} snapshot={session.background_tasks} locked={locked} />}
        {!locked && (
          <Menu.Root modal={false} onOpenChange={setExecutionOpen}>
            <Tip label="Effort, context, permissions and execution">
              <Menu.Trigger render={<Button id="composer-more" size="icon" variant="subtle" aria-label="More settings: effort, context, permissions and execution" className="text-muted sm:hidden sm:in-data-[fold~=more]:inline-flex" />}>
                <Ellipsis />
              </Menu.Trigger>
            </Tip>
            <Menu.Content side="top" align="start" className="max-w-80">
              {settingsLocked ? <p className="max-w-64 px-2 py-1 text-caption text-muted">Effort and context cannot change now.</p> : tuningItems}
              <Menu.Separator />
              {riskLine}
              {permissionItems}
              {executionSupported && (
                <>
                  <Menu.Separator />
                  <ExecutionItems execution={session.execution} reason={executionReason} busy={!!busy} onChange={chooseExecution} onRetry={commandsError ? () => setCommandVersion((v) => v + 1) : undefined} />
                </>
              )}
            </Menu.Content>
          </Menu.Root>
        )}
          </>
        )}
        {/* The actions: the send button keeps the far right, so Stop and Decline rise in beside it and nothing else moves. */}
        <span className="ml-auto flex shrink-0 items-center gap-0.5">
        {busy === 'settings' && <Spinner className="mr-1" />}
        <Appear show={live || session.execution?.objective?.status === 'active'}>
          <Tip label={!session.capabilities.cancel ? 'This provider cannot cancel a turn' : 'Stop the turn and hold the waiting messages'}>
            <Button size="icon-md" variant="primary" aria-label={autopilot ? "Stop autopilot" : "Stop turn"} className="rounded-full" loading={busy === 'stop'} disabled={!!busy || locked || !session.capabilities.cancel} onClick={() => void action('stop', async () => onSessionUpdate(await api.cancel(session.id)))}>
              <Square className="!size-3" fill="currentColor" />
            </Button>
          </Tip>
        </Appear>
        <Appear show={!!answering}>
          <Tip label="Decline to answer this question">
            <Button size="icon-md" variant="danger" aria-label="Decline" className="ml-1 rounded-full" loading={busy === 'decline'} disabled={!!busy || locked} onClick={() => void decline()}>
              <X aria-hidden="true" strokeWidth={2.25} />
            </Button>
          </Tip>
        </Appear>
        {/* While a turn runs, one Send named and drawn for Enter's action, and a menu beside it holding the other. */}
        {twoChoices ? (
          <span className="ml-1 flex items-center max-sm:ml-0.5">
            <Tip label={describeChoiceTip(primary)}>
              <Button size="icon-md" variant="primary" aria-label={blocked ? `${CHOICE_LABEL[primary]}. ${blocked}` : CHOICE_LABEL[primary]} className="rounded-l-full rounded-r-none" loading={busy === primary} disabled={cannotSubmit} onClick={() => void send(primary)}>
                <PrimaryIcon aria-hidden="true" strokeWidth={2.25} />
              </Button>
            </Tip>
            <Menu.Root modal={false}>
              <Tip label="More send options">
                <Menu.Trigger render={<Button size="icon-md" variant="primary" aria-label="More send options" className="rounded-l-none rounded-r-full border-l border-on-primary/25" loading={busy === other} disabled={cannotSubmit} />}>
                  <ChevronDown aria-hidden="true" strokeWidth={2.25} />
                </Menu.Trigger>
              </Tip>
              <Menu.Content side="top" align="end">
                <Menu.Actions items={[otherAction]} />
              </Menu.Content>
            </Menu.Root>
          </span>
        ) : !locked && (
          <Tip label={describeSendTip()}>
            <Button type="submit" size="icon-md" variant="primary" aria-label={blocked ? `${sendLabel}. ${blocked}` : sendLabel} className="ml-1 rounded-full" loading={busy === (answering ? 'answer' : enter)} disabled={cannotSubmit || (!answering && !cmd && enter === 'steer' && !!settingsSteerReason)}>
              <ArrowUp aria-hidden="true" strokeWidth={2.25} />
            </Button>
          </Tip>
        )}
        </span>
      </div>
      {busy && busy !== 'settings' && (
        <output className="sr-only">
          Updating {busy}…
        </output>
      )}
    </form>
  );
}
