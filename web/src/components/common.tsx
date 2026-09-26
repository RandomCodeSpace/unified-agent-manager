import { Check, CircleDashed, Copy, CornerDownLeft, ExternalLink, File, FileArchive, FileBraces, FileCode, FileImage, FileMusic, FileSpreadsheet, FileText, FileType, FileVideoCamera, ImageOff, Minus, Pause, X } from 'lucide-react';
import { createContext, memo, useCallback, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import ReactMarkdown, { defaultUrlTransform, type Components, type ExtraProps } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { LIVE, api, taskName, type AccountUsage, type Badge, type BadgeColor, type FileDeclaration, type Meta, type SessionState, type SessionSummary, type Settings } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { DEFAULT_SETTINGS, type Action } from '../state';
import { fenceClosed } from '../lib/diagram';
import { codeFile, localPath, splitBlocks, taskFile } from '../lib/markdown';
import type { HighlightTree } from '../lib/highlight';
import { fileLabel, previewClick, tempFile, type FileFormat } from '../lib/preview';
import { hintPath } from '../lib/fileReferences';
import { TempRootContext, usePreview } from '../lib/previewContext';
import { Lightbox } from './Attachments';
import { DiagramCard } from './Diagram';
import { useFileDemand, useFileReference } from './FileReferences';
import { Button, buttonVariants } from './ui/button';
import { Chip, chipVariants } from './ui/chip';
import { Input } from './ui/input';
import { ContextMenu, type ActionItem } from './ui/menu';

export const STATE_LABELS: Record<SessionState, string> = {
  idle: 'Idle',
  starting: 'Starting',
  working: 'Working',
  awaiting_permission: 'Needs permission',
  awaiting_answer: 'Needs answer',
  completed: 'Completed',
  cancelled: 'Cancelled',
  failed: 'Failed',
  interrupted: 'Interrupted',
  closed: 'Closed',
};

export type Tone = 'accent' | 'attention' | 'success' | 'error' | 'warning' | 'muted' | 'faint';

/** Colour is reserved for act-now (attention), in-motion (accent) and broken (error/warning). */
export const STATE_TONE: Record<SessionState, Tone> = {
  idle: 'faint',
  starting: 'accent',
  working: 'accent',
  awaiting_permission: 'attention',
  awaiting_answer: 'attention',
  completed: 'success',
  cancelled: 'muted',
  failed: 'error',
  interrupted: 'warning',
  closed: 'faint',
};

export const TONE_TEXT: Record<Tone, string> = {
  accent: 'text-accent',
  attention: 'text-attention',
  success: 'text-success',
  error: 'text-error',
  warning: 'text-warning',
  muted: 'text-muted',
  // Faint text is muted text: the faint colour is for glyphs only (it is under AA for words).
  faint: 'text-muted',
};

const TONE_BG: Record<Tone, string> = {
  accent: 'bg-accent',
  attention: 'bg-attention',
  success: 'bg-success',
  error: 'bg-error',
  warning: 'bg-warning',
  muted: 'bg-muted',
  faint: 'bg-faint',
};

export const INTERRUPTED_TEXT = 'UAM stopped while this turn was running; it was not resumed or resent.';

/** Values shared by most of the tree; avoids threading meta/dispatch through every layer. */
export interface AppContextValue {
  /** The catalogs; null until GET /api/meta answers. `metaError` says why it did not, once it failed. */
  meta: Meta | null;
  metaError: string | null;
  /** Whether the first snapshot has arrived: before that, nothing the service holds is known (skeletons, never empty states). */
  loaded: boolean;
  dispatch: (a: Action) => void;
  narrow: boolean;
  /** Tasks with activity the user has not looked at yet (UI-local). */
  hasNews: (s: SessionSummary) => boolean;
  /** The service's settings (the send default the composer follows). */
  settings: Settings;
  usage: AccountUsage | null;
  /** Reads GET /api/meta again: the catalogs and each provider's cheapest_model. */
  refreshMeta: () => void;
}

export const AppContext = createContext<AppContextValue>({
  meta: null,
  metaError: null,
  loaded: false,
  dispatch: () => {},
  narrow: false,
  hasNews: () => false,
  settings: DEFAULT_SETTINGS,
  usage: null,
  refreshMeta: () => {},
});

export const useApp = () => useContext(AppContext);

/** True while the media query matches; follows changes. */
export function useMedia(query: string): boolean {
  const [matches, setMatches] = useState(() => window.matchMedia(query).matches);
  useEffect(() => {
    const m = window.matchMedia(query);
    const on = () => setMatches(m.matches);
    on();
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, [query]);
  return matches;
}

/**
 * State mark (DESIGN.md): a dot for in-motion and act-now states, a drawn glyph for the
 * finished ones, always with the word for assistive tech. `label` renders the word too,
 * as a chip; only the attention chip has a fill.
 */
export function StateMark({ state, label = false, title, className }: { state: SessionState; label?: boolean; title?: string; className?: string }) {
  const text = STATE_LABELS[state] ?? state;
  const tone = STATE_TONE[state];
  const attention = tone === 'attention';
  // Keyed on the state, so a change fades the new glyph in (`base`) in the same 16px slot; the chip's colour transitions with it.
  const glyph = (
    <span key={state} className="flex animate-fade-in">
      <StateGlyph state={state} />
    </span>
  );
  if (!label) {
    return (
      <span className={cn('inline-flex size-4 shrink-0 items-center justify-center', className)} title={title}>
        {glyph}
        <span className="sr-only">{text}</span>
      </span>
    );
  }
  return (
    <Chip tone={attention ? 'attention' : tone === 'faint' ? 'muted' : tone} className={className} title={title}>
      {glyph}
      {text}
    </Chip>
  );
}

/**
 * The working mark (DESIGN.md): an `accent` core that breathes while a satellite circles
 * it on a faint ring; under reduced motion the same glyph, still. 14px, crisp from 12 to 16.
 * One component, so the sidebar rows, the Task header, the subagent chips and the
 * transcript's working row all move alike.
 */
export function WorkingMark({ className }: { className?: string }) {
  return (
    <svg aria-hidden="true" viewBox="0 0 16 16" className={cn('size-3.5 shrink-0 text-accent', className)}>
      <circle cx="8" cy="8" r="5.5" fill="none" stroke="currentColor" strokeWidth="1.25" className="opacity-25" />
      <circle cx="8" cy="8" r="2" fill="currentColor" className="animate-breathe motion-reduce:animate-none" />
      <g className="origin-center animate-orbit motion-reduce:animate-none">
        <circle cx="8" cy="2.5" r="1.5" fill="currentColor" />
      </g>
    </svg>
  );
}

function StateGlyph({ state }: { state: SessionState }) {
  const tone = STATE_TONE[state];
  switch (state) {
    case 'working':
    case 'starting':
      return <WorkingMark />;
    case 'awaiting_permission':
    case 'awaiting_answer':
      return <span aria-hidden="true" className="size-2 rounded-full bg-attention" />;
    case 'completed':
      return <Check aria-hidden="true" className={cn('size-3.5', TONE_TEXT[tone])} strokeWidth={2.5} />;
    case 'failed':
      return <X aria-hidden="true" className={cn('size-3.5', TONE_TEXT[tone])} strokeWidth={2.5} />;
    case 'cancelled':
      return <Minus aria-hidden="true" className={cn('size-3.5', TONE_TEXT[tone])} strokeWidth={2.5} />;
    case 'interrupted':
      return <Pause aria-hidden="true" className={cn('size-3', TONE_TEXT[tone])} strokeWidth={2.5} fill="currentColor" />;
    case 'idle':
      return <CircleDashed aria-hidden="true" className="size-3.5 text-faint" strokeWidth={2} />;
    default:
      return <span aria-hidden="true" className={cn('size-2 rounded-full border-[1.5px]', tone === 'faint' ? 'border-faint' : `border-current ${TONE_TEXT[tone]}`)} />;
  }
}

// Full class names so Tailwind emits every tone (DESIGN.md Badges).
const BADGE_BG: Record<BadgeColor, string> = {
  red: 'bg-badge-red',
  orange: 'bg-badge-orange',
  amber: 'bg-badge-amber',
  lime: 'bg-badge-lime',
  green: 'bg-badge-green',
  teal: 'bg-badge-teal',
  cyan: 'bg-badge-cyan',
  blue: 'bg-badge-blue',
  violet: 'bg-badge-violet',
  pink: 'bg-badge-pink',
};

/** A Project's badge: a 16px rounded square in its tone with the two characters. Decorative; the name beside it carries the meaning. */
export function ProjectBadge({ badge, className }: { badge: Badge; className?: string }) {
  return (
    <span aria-hidden="true" className={cn('inline-flex size-4 shrink-0 items-center justify-center rounded-xs text-badge font-bold text-on-primary select-none', BADGE_BG[badge.color], className)}>
      {badge.text}
    </span>
  );
}

/** A tiny dot in a tone; used for connection status and unread marks. */
export function Dot({ tone, className, pulse = false }: { tone: Tone; className?: string; pulse?: boolean }) {
  return <span aria-hidden="true" className={cn('inline-block size-2 shrink-0 rounded-full', TONE_BG[tone], pulse && 'animate-pulse-dot', className)} />;
}

import { Spinner } from './ui/spinner';
export { Spinner };

export function SubagentIdleIcon({ className }: { className?: string }) {
  return <CornerDownLeft aria-hidden="true" className={cn('size-3.5', className)} />;
}

/** Display name: name, else provider title, else a placeholder that says whether a title is on its way. */
export function TaskTitle({ session, className }: { session: SessionSummary; className?: string }) {
  const name = taskName(session);
  if (name) return <span className={className}>{name}</span>;
  const waiting = LIVE.includes(session.state);
  return <span className={cn('text-muted', className)}>{waiting ? 'Waiting for a title…' : 'New task'}</span>;
}

/** Visually hidden separator so adjacent labels do not run together in accessible names. */
export function Sep() {
  return <span className="sr-only">, </span>;
}

export function relTime(iso: string, now = Date.now()): string {
  const ms = now - new Date(iso).getTime();
  const m = Math.round(ms / 60000);
  if (m < 1) return 'now';
  if (m < 60) return `${m}m`;
  const h = Math.round(m / 60);
  if (h < 24) return `${h}h`;
  return `${Math.round(h / 24)}d`;
}

/**
 * Whether a scroll container has been scrolled away from its top, for the pane header's fade
 * (`data-scrolled`). Watches a sentinel placed first inside the container through an
 * IntersectionObserver, so nothing runs per scroll event; the returned ref goes on the sentinel.
 */
export function useScrolled(): [boolean, (el: HTMLElement | null) => void] {
  const [scrolled, setScrolled] = useState(false);
  const ref = useCallback((el: HTMLElement | null) => {
    if (!el?.parentElement) return;
    const observer = new IntersectionObserver(([entry]) => setScrolled(!entry.isIntersecting), { root: el.parentElement });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [scrolled, ref];
}

/** The sentinel `useScrolled` watches: the first child of the scroll container, taking no room. */
export function ScrollSentinel({ sentinelRef }: { sentinelRef: (el: HTMLElement | null) => void }) {
  return <div ref={sentinelRef} aria-hidden="true" className="-mb-px h-px" />;
}

/**
 * A skeleton (DESIGN.md Placeholder and loading states): `rows` bars of `sunken` in the shape
 * of what is coming, one highlight sweeping over the group, while a list or a transcript first
 * loads. It is a live status for screen readers and hidden decoration otherwise; the caller
 * marks the region `aria-busy`.
 */
export function Skeleton({ label, rows = 3, className, rowClassName, children, ...props }: { label: string; rows?: number; className?: string; rowClassName?: string; children?: ReactNode; 'aria-hidden'?: boolean | 'true' }) {
  const hidden = !!props['aria-hidden'];
  return (
    <div role={hidden ? undefined : 'status'} aria-hidden={hidden || undefined} className={cn('skeleton flex flex-col gap-3 motion-reduce:[&::after]:hidden', className)}>
      {!hidden && <span className="sr-only">{label}</span>}
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} aria-hidden="true" className={cn('h-4 rounded-sm bg-sunken', i % 3 === 0 ? 'w-3/4' : i % 3 === 1 ? 'w-full' : 'w-1/2', rowClassName)} />
      ))}
      {children}
    </div>
  );
}

/** The conversation's skeleton: a user bubble at the right, then assistant lines, in the transcript's gutters. */
export function TranscriptSkeleton({ label = 'Loading the conversation…' }: { label?: string }) {
  return (
    <div className="flex flex-col gap-6 px-3 pt-6 sm:px-4 md:px-6">
      <Skeleton label={label} rows={0} className="items-end">
        <div aria-hidden="true" className="h-11 w-[min(60%,480px)] rounded-lg bg-sunken" />
      </Skeleton>
      <Skeleton label="" rows={4} aria-hidden="true" />
    </div>
  );
}

/** True once `active` has held for `ms`; false again as soon as it ends. Keeps brief waits (a Task switch, a reconnect) from flashing. */
export function useLate(active: boolean, ms: number): boolean {
  const [late, setLate] = useState(false);
  useEffect(() => {
    const id = window.setTimeout(() => setLate(active), active ? ms : 0);
    return () => window.clearTimeout(id);
  }, [active, ms]);
  return active && late;
}

/** Re-renders once a minute so relative times stay honest. */
export function useMinuteTick(): number {
  const [tick, setTick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), 60_000);
    return () => window.clearInterval(id);
  }, []);
  return tick;
}

/**
 * In-place rename (T3 Code): an input that takes the row's place, selects its text,
 * saves on Enter or blur and cancels on Esc. IME composition is left alone. An empty
 * value is allowed: it clears the name so the provider's title shows again.
 */
export function InlineName({
  initial,
  onSave,
  onCancel,
  className,
  label = 'Name',
}: {
  initial: string;
  onSave: (value: string) => void;
  onCancel: () => void;
  className?: string;
  label?: string;
}) {
  const [value, setValue] = useState(initial);
  const ref = useRef<HTMLInputElement>(null);
  const done = useRef(false);
  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
  }, []);
  const finish = (save: boolean) => {
    if (done.current) return;
    done.current = true;
    if (save) onSave(value.trim());
    else onCancel();
  };
  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (e.key === 'Enter') {
      e.preventDefault();
      finish(true);
    } else if (e.key === 'Escape') {
      e.preventDefault();
      e.stopPropagation();
      finish(false);
    }
  };
  return (
    <Input
      ref={ref}
      size="sm"
      aria-label={label}
      className={className}
      value={value}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={onKeyDown}
      onBlur={() => finish(true)}
      onClick={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
    />
  );
}

const remarkPlugins = [remarkGfm];

/**
 * A code block (DESIGN.md `code-block`): the `code-bg` well with its inset ring, 10px radius,
 * a 24px header with the language when known and a copy button; right-click offers Copy code. The text
 * is read from the DOM, so it is exactly what is shown, unless `text` names what to copy.
 * The slots are for the diagram card: `head` is the header's middle (default: a spacer),
 * `body` replaces the `<pre>`, `foot` is a line under it.
 */
export function CodeBlock({ language, className, text, head, body, foot, children }: { language?: string; className?: string; text?: string; head?: ReactNode; body?: ReactNode; foot?: ReactNode; children: ReactNode }) {
  const pre = useRef<HTMLPreElement>(null);
  const [copied, copy] = useCopied();
  const read = () => text ?? pre.current?.textContent ?? '';
  const items: ActionItem[] = [{ key: 'copy', label: 'Copy code', icon: <Copy />, onSelect: () => copy(read()) }];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/code relative my-2.5 overflow-hidden rounded-md bg-code-bg shadow-well', className)} />}>
        <div className="flex h-6 items-center gap-2 px-3 text-code-sm text-muted">
          <span className="font-mono">{language ?? 'code'}</span>
          {head ?? <span className="flex-1" />}
          <Button
            size="icon-sm"
            variant="subtle"
            aria-label={copied ? 'Copied' : 'Copy code'}
            className={cn('text-muted opacity-0 transition-opacity group-hover/code:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100', copied && 'opacity-100 text-success')}
            onClick={() => copy(read())}
          >
            {copied ? <Check /> : <Copy />}
          </Button>
        </div>
        {body ?? (
          <pre ref={pre} translate="no" className="!my-0 !rounded-none !shadow-none max-h-[480px] overflow-auto px-3 pt-0.5 pb-2.5 font-mono text-code text-ink">
            {children}
          </pre>
        )}
        {foot}
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}

type Highlighter = typeof import('../lib/highlight');
let highlighter: Highlighter | null = null;
let highlighterLoad: Promise<Highlighter> | null = null;

/** The highlighter module once it is here; the first caller starts the download. */
function useHighlighter(): Highlighter | null {
  const [mod, setMod] = useState(highlighter);
  useEffect(() => {
    if (mod) return;
    let on = true;
    (highlighterLoad ??= import('../lib/highlight').then((m) => (highlighter = m))).then(
      (m) => on && setMod(m),
      () => {},
    );
    return () => {
      on = false;
    };
  }, [mod]);
  return mod;
}

/** Beyond this many characters a block stays plain: highlighting runs again on every streamed delta. */
const MAX_HIGHLIGHT = 100_000;

function hastToReact(nodes: HighlightTree['children'], prefix = ''): ReactNode[] {
  return nodes.map((n, i) => {
    if (n.type === 'text') return n.value;
    if (n.type !== 'element') return null;
    const cls = n.properties.className;
    return (
      <span key={`${prefix}${i}`} className={Array.isArray(cls) ? cls.join(' ') : undefined}>
        {hastToReact(n.children, `${prefix}${i}.`)}
      </span>
    );
  });
}

/** The code with `hljs-*` spans once the highlighter has loaded and knows the language; plain until then. */
function Highlighted({ language, code }: { language: string; code: string }) {
  const h = useHighlighter();
  const tree = useMemo(() => (h && code.length <= MAX_HIGHLIGHT ? h.highlight(language, code) : null), [h, language, code]);
  return tree ? <>{hastToReact(tree.children)}</> : <>{code}</>;
}

interface MdSource {
  text: string;
  /** The text is still arriving: a fence that has not closed yet is not a diagram. */
  streaming: boolean;
}

const MdContext = createContext<MdSource>({ text: '', streaming: false });

/** The Task whose transcript is rendering: markdown images and links by file path are served from its directory. */
export const SessionContext = createContext<string | undefined>(undefined);

/** That Task's directory, so a link by absolute path inside it opens and one outside stays text. */
export const WorkdirContext = createContext<string | undefined>(undefined);

/** Natural sizes of local images that loaded, by URL, so a block parsed again reserves the same box before the bytes arrive. */
const imageSizes = new Map<string, { width: number; height: number }>();

/** A compact stand-in where an image cannot show: the alt text and the path, never a broken-image glyph. */
function ImageNote({ alt, detail, href }: { alt: string; detail: string; href?: string }) {
  const body = (
    <>
      <ImageOff className="size-3.5 shrink-0" aria-hidden="true" />
      <span>{alt || 'Image'}</span>
      <span className="truncate text-muted">{detail}</span>
    </>
  );
  const className = 'inline-flex max-w-full items-center gap-1.5 rounded-sm bg-sunken px-2 py-1 align-middle text-caption text-body';
  return href ? (
    <a href={href} rel="noopener noreferrer" target="_blank" className={cn(className, 'hover:underline')}>
      {body}
    </a>
  ) : (
    <span className={className}>{body}</span>
  );
}

/** A temporary path is an explicit owner choice, never an image or discovery request. */
function TempFileAction({ file, children }: { file: { path: string; hash: string }; children?: ReactNode }) {
  const preview = usePreview();
  return (
    <span className="inline-flex max-w-full flex-wrap items-baseline gap-x-1.5 gap-y-1">
      {children}
      <button type="button" className="inline-flex max-w-full flex-wrap items-baseline gap-x-1.5 rounded-xs text-left text-accent underline decoration-accent/40 underline-offset-2 hover:decoration-current" onClick={event => {
        event.preventDefault();
        event.stopPropagation();
        preview?.({ tempPath: file.path, hash: file.hash, name: file.path.split('/').pop() || file.path, description: file.path }, event.currentTarget);
      }}>
        <span>Open temp file</span><code className="break-all">{file.path}</code>
      </button>
    </span>
  );
}

const FILE_ICONS = { image: FileImage, pdf: FileType, code: FileCode, data: FileBraces, archive: FileArchive, audio: FileMusic, video: FileVideoCamera, sheet: FileSpreadsheet, text: FileText, file: File } satisfies Record<FileFormat, typeof File>;

/** A compact resolved file label. The path remains the link's identity and accessible name. */
function FileLink({ path, url }: { path: string; url: string }) {
  const preview = usePreview();
  const label = fileLabel(path);
  const Icon = FILE_ICONS[label.format];
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" title={label.path} aria-label={`Open ${label.path}`} className={cn(chipVariants({ fill: 'well', tone: 'accent' }), 'max-w-full min-w-0 align-middle hover:bg-sunken')} onClick={event => {
      if (preview && previewClick(event)) {
        event.preventDefault();
        preview({ url, name: label.name, description: label.path, frameable: true }, event.currentTarget);
      }
    }}>
      <Icon className="size-3.5 shrink-0" aria-hidden="true" />
      <span className="min-w-0 truncate">{label.name}</span>
    </a>
  );
}

/** Display intent only. The resolver or exact temp-file grant checks the current file. */
export function DeclaredFileCard({ declaration }: { declaration: FileDeclaration }) {
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const tempRoots = useContext(TempRootContext);
  const preview = usePreview();
  const path = declaration.path;
  const href = path.split('/').map(encodeURIComponent).join('/');
  const workFile = taskFile(href, workdir);
  const temporary = tempFile(href, workdir, tempRoots);
  const { exists } = useFileReference(workFile?.path);
  const demand = useFileDemand(path);
  const label = fileLabel(path);
  const Icon = FILE_ICONS[label.format];
  return (
    <div ref={demand} className="rounded-md bg-raised px-3.5 py-3 shadow-raised" role="group" aria-label={`Declared file ${path}`}>
      <div className="flex min-w-0 items-start gap-2.5">
        <Icon aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted" />
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
            <span className="break-all font-medium text-body">{label.name}</span>
            <span className="text-caption text-muted">{label.format}</span>
          </div>
          {declaration.title && <p className="break-words text-caption text-body">{declaration.title}</p>}
          {declaration.type_hint && <p className="break-words text-caption text-muted">Type: {declaration.type_hint}</p>}
          <code className="block break-all text-code-sm text-muted">{path}</code>
        </div>
      </div>
      <div className="mt-2 pl-6" data-file-reference={workFile?.path}>
        {sessionId && workFile && exists ? <FileLink path={path} url={api.viewFileUrl(sessionId, workFile.path)} />
          : sessionId && temporary && preview ? <TempFileAction file={temporary} />
            : <span className="text-caption text-muted">File availability unknown or unavailable.</span>}
      </div>
    </div>
  );
}

function containsImage(node: ExtraProps['node']): boolean {
  return !!node && (node.tagName === 'img' || node.children.some(child => child.type === 'element' && containsImage(child)));
}

/**
 * An image the agent named by path, from the raw file route. Lazy, never wider than the
 * column; one that fails (outside the Task's folder, missing, not an image) becomes a note.
 * Clicking it opens the lightbox with the file's name.
 */
function LocalImage({ sessionId, path, alt }: { sessionId: string; path: string; alt: string }) {
  const preview = usePreview();
  const url = api.rawFileUrl(sessionId, path);
  const [failed, setFailed] = useState(false);
  const [size, setSize] = useState(() => imageSizes.get(url));
  const [open, setOpen] = useState(false);
  if (failed) return <ImageNote alt={alt} detail={path} />;
  const name = path.split('/').pop() || path;
  return (
    <>
      <button type="button" className="lift block max-w-full rounded-sm text-left" aria-label={`Open ${alt || name}`} onClick={event => preview ? preview({ url, name: alt || name, description: path, image: true }, event.currentTarget) : setOpen(true)}>
        <img
          src={url}
          alt={alt}
          title={path}
          width={size?.width}
          height={size?.height}
          loading="lazy"
          decoding="async"
          className="block h-auto max-h-[480px] w-auto max-w-full rounded-sm bg-sunken object-contain"
          onError={() => setFailed(true)}
          onLoad={(e) => {
            const { naturalWidth: width, naturalHeight: height } = e.currentTarget;
            if (width && height && !size) {
              imageSizes.set(url, { width, height });
              setSize({ width, height });
            }
          }}
        />
      </button>
      {!preview && <Lightbox
        open={open}
        onOpenChange={setOpen}
        title={alt || name}
        description={path}
        src={url}
        alt={alt || name}
        footer={
          <a href={url} target="_blank" rel="noopener noreferrer" className={buttonVariants({ variant: 'secondary', size: 'md' })}>
            <ExternalLink />
            Open original
          </a>
        }
      />}
    </>
  );
}

/** A markdown image: by path from the Task's directory; `data:` inline; a web address stays a link, as the page loads nothing cross-origin. */
function MdImage({ src, alt }: { src?: string; alt?: string }) {
  const preview = usePreview();
  const tempRoots = useContext(TempRootContext);
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const { streaming } = useContext(MdContext);
  const text = alt ?? '';
  const path = localPath(src);
  const file = taskFile(src, workdir);
  const temporary = !streaming && sessionId && preview ? tempFile(src, workdir, tempRoots) : null;
  if (temporary) return <TempFileAction file={temporary}>{text && <span>{text}</span>}</TempFileAction>;
  if (path) return sessionId && file ? <LocalImage sessionId={sessionId} path={file.path} alt={text} /> : <ImageNote alt={text} detail={path} />;
  if (src && /^(data|blob):/i.test(src)) return <img src={src} alt={text} loading="lazy" decoding="async" className="block h-auto max-h-[480px] w-auto max-w-full rounded-sm" />;
  return <ImageNote alt={text} detail={src ?? ''} href={src && /^https?:/i.test(src) ? src : undefined} />;
}

/** Local available files preview on an ordinary click; modified clicks and web links stay native. */
function MdLink({ href, children, node }: { href?: string; children?: ReactNode } & ExtraProps) {
  const preview = usePreview();
  const tempRoots = useContext(TempRootContext);
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const { streaming } = useContext(MdContext);
  const file = !streaming ? taskFile(href, workdir) : null;
  const { eligible, exists } = useFileReference(file?.path);
  const temporary = !streaming && sessionId && preview ? tempFile(href, workdir, tempRoots) : null;
  if (temporary) return <TempFileAction file={temporary}>{children}</TempFileAction>;
  const target = localPath(href) ? (sessionId && file && exists ? api.viewFileUrl(sessionId, file.path) + file.hash : undefined) : typeof href === 'string' && /^(https?:|mailto:)/i.test(href) ? href : undefined;
  const link = target && file && localPath(href) && !containsImage(node) ? <FileLink path={file.path} url={target} /> : target ? <a href={target} rel="noopener noreferrer" target="_blank" onClick={event => {
    if (preview && file && localPath(href) && previewClick(event)) {
      event.preventDefault();
      preview({ url: target, name: file.path.split('/').pop() || file.path, description: file.path, frameable: true }, event.currentTarget);
    }
  }}>{children}</a> : children;
  return localPath(href) ? <span key={file?.path} data-file-reference={eligible ? file?.path : undefined}>{link}</span> : <>{link}</>;
}

/**
 * Inline code that names a file of the Task's folder (`ai-news.html`, `out/report.html`)
 * opens it through the view route once the file is known to exist; until then, and when it
 * does not, it is the same plain code. Nothing is asked while its block is still streaming.
 */
function InlineCode({ text, className }: { text: string; className?: string }) {
  const preview = usePreview();
  const tempRoots = useContext(TempRootContext);
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const { streaming } = useContext(MdContext);
  const hintedPath = sessionId && !streaming && !text.includes('/') ? hintPath(text, workdir ?? '') : undefined;
  const file = sessionId && !streaming ? codeFile(text, workdir) ?? (hintedPath ? { path: hintedPath, hash: '' } : null) : null;
  const url = sessionId && file ? api.viewFileUrl(sessionId, file.path) : undefined;
  const { eligible, exists } = useFileReference(file?.path, text);
  const temporary = !streaming && sessionId && preview && text.startsWith('/') ? tempFile(text.split('/').map(encodeURIComponent).join('/'), workdir, tempRoots) : null;
  if (temporary) return <TempFileAction file={temporary} />;
  const code = <code className={className}>{text}</code>;
  if (!file) return code;
  return (
    <span key={file?.path} data-file-reference={eligible ? file?.path : undefined}>
      {exists && url && file ? <FileLink path={file.path} url={url + file.hash} /> : code}
    </span>
  );
}

interface Span {
  start: { offset?: number };
  end: { offset?: number };
}

function MermaidBlock({ source, position, children }: { source: string; position?: Span; children: ReactNode }) {
  const { text, streaming } = useContext(MdContext);
  const ready = !streaming || !position || fenceClosed(text, position.start.offset ?? 0, position.end.offset ?? text.length);
  return (
    <DiagramCard source={source} ready={ready}>
      {children}
    </DiagramCard>
  );
}

const languageOf = (className: unknown): string | undefined => /language-([\w+-]+)/.exec(Array.isArray(className) ? className.join(' ') : String(className ?? ''))?.[1];

/** react-markdown drops `file:` URLs as unsafe; here they are paths for the file routes, which MdImage and MdLink decide on. */
const mdUrl = (url: string): string => (/^file:\/\//i.test(url) ? url : defaultUrlTransform(url));

const mdComponents: Components = {
  a: MdLink,
  img: MdImage,
  table({ children }) {
    return (
      // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The labelled table scroll region accepts keyboard scrolling.
      <div role="region" aria-label="Table" tabIndex={0} className="my-2.5 max-w-full overflow-x-auto [overflow-wrap:normal]">
        <table className="!my-0">{children}</table>
      </div>
    );
  },
  code({ className, children }) {
    const language = languageOf(className);
    const code = typeof children === 'string' ? children : Array.isArray(children) && children.every((c) => typeof c === 'string') ? children.join('') : null;
    // Fenced code ends in a newline and so never looks like a path: only inline code links.
    if (!language && code !== null) return <InlineCode text={code} className={className} />;
    if (!language || code === null) return <code className={className}>{children}</code>;
    return (
      <code className={className}>
        <Highlighted language={language} code={code} />
      </code>
    );
  },
  pre({ node, children }) {
    const code = node?.children[0];
    const language = code?.type === 'element' ? languageOf(code.properties.className) : undefined;
    if (language === 'mermaid' && code?.type === 'element') {
      const first = code.children[0];
      return (
        <MermaidBlock source={first?.type === 'text' ? first.value : ''} position={node?.position}>
          {children}
        </MermaidBlock>
      );
    }
    return <CodeBlock language={language}>{children}</CodeBlock>;
  },
};

/**
 * Markdown for untrusted provider text: no raw HTML, safe links only, images by path served
 * from the Task's directory (`SessionContext`) and no image from another origin. Completed
 * messages mount as one document. Streaming text uses top-level blocks so only the last block
 * is parsed again per delta; it keeps those boundaries after completion to retain DOM state.
 */
export function Markdown({ text, streaming = false, className }: { text: string; streaming?: boolean; className?: string }) {
  const [hasStreamed, setHasStreamed] = useState(streaming);
  if (streaming && !hasStreamed) setHasStreamed(true);
  const blocks = useMemo(() => hasStreamed ? splitBlocks(text) : [text], [text, hasStreamed]);
  const fileDemand = useFileDemand(text);
  return (
    <div ref={fileDemand} className={cn('md', className)}>
      {blocks.map((block, i) => (
        <MarkdownBlock key={i} text={block} streaming={streaming && i === blocks.length - 1} />
      ))}
    </div>
  );
}

const MarkdownBlock = memo(function MarkdownBlock({ text, streaming }: { text: string; streaming: boolean }) {
  const source = useMemo(() => ({ text, streaming }), [text, streaming]);
  return (
    <MdContext.Provider value={source}>
      <ReactMarkdown remarkPlugins={remarkPlugins} components={mdComponents} urlTransform={mdUrl}>
        {text}
      </ReactMarkdown>
    </MdContext.Provider>
  );
});

/**
 * The quiet loading indicator: nothing for `delay` ms (a wait that short shows nothing new),
 * then a spinner and a word. Replaces skeleton placeholders; what was on screen stays.
 */
export function Loading({ label = 'Loading…', delay = 300, className }: { label?: string; delay?: number; className?: string }) {
  const late = useLate(true, delay);
  return (
    <div role="status" aria-busy="true" className={cn('flex min-h-8 items-center gap-2 text-caption text-muted', className)}>
      {late && (
        <span className="flex items-center gap-2 animate-fade-in">
          <Spinner className="border-muted" />
          {label}
        </span>
      )}
      {!late && <span className="sr-only">{label}</span>}
    </div>
  );
}

/** A `caption` line for feedback: `error` and `warn` are the only coloured ones. */
export function Note({ tone = 'muted', className, children, role, id }: { tone?: 'muted' | 'error' | 'warn' | 'info'; className?: string; children: ReactNode; role?: 'alert' | 'status'; id?: string }) {
  return (
    <p id={id} role={role} className={cn('text-caption animate-fade-in', tone === 'error' && 'text-error', tone === 'warn' && 'text-warning', tone === 'info' && 'text-info', tone === 'muted' && 'text-muted', className)}>
      {children}
    </p>
  );
}
