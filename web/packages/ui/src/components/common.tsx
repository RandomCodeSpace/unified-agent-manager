import { useApi } from '../ApiContext';
import { Ban, Circle, CircleAlert, CircleDot, CircleHelp, Check, CircleDashed, Copy, CornerDownLeft, ExternalLink, ImageOff, Info, Lightbulb, MessageCircleQuestion, Minimize2, Minus, OctagonAlert, Pause, SkipForward, TriangleAlert, X, type LucideIcon } from 'lucide-react';
import { Children, createContext, isValidElement, memo, startTransition, useCallback, useContext, useEffect, useMemo, useRef, useState, type ComponentProps, type KeyboardEvent, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react';
import ReactMarkdown, { defaultUrlTransform, type Components, type ExtraProps, type Options } from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import remarkGfm from 'remark-gfm';
import { taskName, type AccountUsage, type Badge, type BadgeColor, type Meta, type SessionState, type SessionSummary, type Settings } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { DEFAULT_SETTINGS, type Action } from '../state';
import { fenceClosed, mermaidFences } from '../lib/diagram';
import { codeFile, localPath, looksLikePath, splitBlocks, taskFile } from '../lib/markdown';
import { SEVERITY_ORDER, STATUS_ORDER, commandsOf, outlineOf, remarkRich, type CalloutKind, type Command, type Outline } from '../lib/richMarkdown';
import type { TodoStatus } from '../api';
import type { HighlightTree } from '../lib/highlight';
import { fileLabel, previewClick, tempFile } from '../lib/preview';
import { FileIcon } from './FileIcon';
import { hintPath } from '../lib/fileReferences';
import { gitLabWords, parseGitLabUrl, remarkGitLabRefs } from '../lib/gitlabRefs';
import { GitLabItemsContext, plainClick } from './GitLabStrip';
import { TempRootContext, usePreview } from '../lib/previewContext';
import { Lightbox } from './Attachments';
import { DiagramCard } from './Diagram';
import { MarkdownTable } from './DataTable';
import { useFileDemand, useFileReference } from './FileReferences';
import { Button, buttonVariants } from './ui/button';
import { Chip, chipVariants } from './ui/chip';
import { Input } from './ui/input';
import { ContextMenu, useLazyContextMenu, type ActionItem } from './ui/menu';

export const STATE_LABELS: Record<SessionState, string> = {
  idle: 'Idle',
  starting: 'Starting',
  working: 'Working',
  awaiting_permission: 'Input',
  awaiting_answer: 'Input',
  completed: 'Finished',
  cancelled: 'Stopped',
  failed: 'Error',
  interrupted: 'Interrupted',
  closed: 'Idle',
};

export type Tone = 'accent' | 'attention' | 'success' | 'error' | 'warning' | 'muted' | 'faint';

/** Colour is reserved for act-now (attention), in-motion (accent) and broken (error/warning). */
export const STATE_TONE: Record<SessionState, Tone> = {
  idle: 'faint',
  starting: 'accent',
  working: 'accent',
  awaiting_permission: 'warning',
  awaiting_answer: 'warning',
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

/**
 * The top bars (DESIGN.md Thread header, Sidebar): the 44px row keeps its box, and inside it a solid lifted card
 * (no blur) 4px from its top and bottom, so the sidebar's and the Thread's top rows line up.
 */
export const BAR_ROW = 'relative z-10 flex h-header shrink-0 items-center px-1.5 sm:px-2';
const BAR_CARD = 'flex h-9 min-w-0 flex-1 items-center gap-1.5 rounded-md bg-raised pr-1 pl-1.5 shadow-raised';
/** A state's still edge on the bar card, a 3px stripe at its left in the tone: in motion, needs you, broken, done; any other tone stays neutral. */
const BAR_EDGE: Partial<Record<Tone, string>> = { accent: 'before:bg-accent', warning: 'before:bg-warning', error: 'before:bg-error', success: 'before:bg-success' };
export const barCard = (tone?: Tone, className?: string) => cn(BAR_CARD, tone && BAR_EDGE[tone] && cn("relative overflow-hidden before:absolute before:inset-y-0 before:left-0 before:w-[3px] before:content-['']", BAR_EDGE[tone]), className);

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
  /** The service's settings (the send default the composer follows). */
  settings: Settings;
  usage: AccountUsage | null;
  /** Opens the shared account Usage popover, revealing the sidebar on phones. */
  openUsage?: () => void;
  /** Reads GET /api/meta again: the catalogs and each provider's cheapest_model. */
  refreshMeta: () => void;
}

export const AppContext = createContext<AppContextValue>({
  meta: null,
  metaError: null,
  loaded: false,
  dispatch: () => {},
  narrow: false,
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
export function StateMark({ state, label = false, compacting = false, title, className, text = STATE_LABELS[state] ?? state, icon, tone = STATE_TONE[state] }: Readonly<{ state: SessionState; label?: boolean; compacting?: boolean; title?: string; className?: string; /** The word in place of the state's ("Compacting…", "Stopped: credit limit"). */ text?: string; /** The glyph in place of the state's. */ icon?: ReactNode; /** The chip's tone in place of the state's, with `icon` and `text` saying the same. */ tone?: Tone }>) {
  // Keyed on the state, so a change fades the new glyph in (`base`) in the same 16px slot; the chip's colour transitions with it.
  const glyph = (
    <span key={state} className="flex animate-fade-in">
      {compacting ? <Minimize2 aria-hidden="true" className="size-3.5 text-badge-violet" /> : icon ?? <StateGlyph state={state} />}
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
    <Chip tone={tone === 'faint' ? 'muted' : tone} className={cn(compacting && 'text-badge-violet', className)} title={title}>
      {glyph}
      {text}
    </Chip>
  );
}

/**
 * The same quiet ring used by requests and buttons, at 14px for task activity.
 * Its one transform animation stops under the app's reduced-motion setting.
 */
export function WorkingMark({ className }: Readonly<{ className?: string }>) {
  return <Spinner className={cn('size-3.5 border-current border-r-transparent text-accent', className)} />;
}

/** Model work on the strip's live line: a 7px ink dot that breathes (opacity and scale only); still under reduced motion. */
export function BreathingDot() {
  return <span aria-hidden="true" className="inline-block size-[7px] shrink-0 animate-breathe rounded-full bg-ink motion-reduce:animate-none" />;
}

function StateGlyph({ state }: Readonly<{ state: SessionState }>) {
  const tone = STATE_TONE[state];
  switch (state) {
    case 'working':
    case 'starting':
      return <WorkingMark />;
    case 'awaiting_permission':
    case 'awaiting_answer':
      return <MessageCircleQuestion aria-hidden="true" className="size-3.5 text-warning" />;
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

/** A badge tone as text: a Project's name on its Thread row, a subagent's name in its identity tone. */
export const BADGE_TEXT: Record<BadgeColor, string> = {
  red: 'text-badge-red',
  orange: 'text-badge-orange',
  amber: 'text-badge-amber',
  lime: 'text-badge-lime',
  green: 'text-badge-green',
  teal: 'text-badge-teal',
  cyan: 'text-badge-cyan',
  blue: 'text-badge-blue',
  violet: 'text-badge-violet',
  pink: 'text-badge-pink',
};

/** A Project's badge: a 16px rounded square in its tone with the two characters. Decorative; the name beside it carries the meaning. */
export function ProjectBadge({ badge, className }: Readonly<{ badge: Badge; className?: string }>) {
  return (
    <span aria-hidden="true" className={cn('inline-flex size-4 shrink-0 items-center justify-center rounded-xs text-badge font-bold text-on-primary select-none', BADGE_BG[badge.color], className)}>
      {badge.text}
    </span>
  );
}

/** A tiny dot in a tone; used for connection status and unread marks. */
export function Dot({ tone, className }: Readonly<{ tone: Tone; className?: string }>) {
  return <span aria-hidden="true" className={cn('inline-block size-2 shrink-0 rounded-full', TONE_BG[tone], className)} />;
}

import { Spinner, StepDots } from './ui/spinner';
export { Spinner, StepDots };

export function SubagentIdleIcon({ className }: Readonly<{ className?: string }>) {
  return <CornerDownLeft aria-hidden="true" className={cn('size-3.5', className)} />;
}

/** Display name: name, else title, else a placeholder that says whether the server is making a title. */
export function TaskTitle({ session, className }: Readonly<{ session: SessionSummary; className?: string }>) {
  const name = taskName(session);
  if (name) return <span className={className}>{name}</span>;
  return <span className={cn('text-muted', className)}>{session.title_pending ? 'Waiting for a title…' : 'New thread'}</span>;
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
 * One formatter per format, made on first use: the Date methods build one per call, about an eighth of the CPU time
 * of history scrolling on a phone. They keep the locale and time zone the page loaded with.
 */
const formats = new Map<string, Intl.DateTimeFormat>();
function formatted(at: Date | string, name: string, options: Intl.DateTimeFormatOptions): string {
  const date = new Date(at);
  // As the Date methods: an unreadable time reads "Invalid Date" (a formatter would throw).
  if (Number.isNaN(date.getTime())) return 'Invalid Date';
  let format = formats.get(name);
  if (!format) formats.set(name, (format = new Intl.DateTimeFormat(undefined, options)));
  return format.format(date);
}

/** A clock time in the browser's locale ("2:05 PM" or "14:05"); `seconds` adds them for step timelines. */
export function clockTime(at: Date | string, seconds = false): string {
  return formatted(at, seconds ? 'clock-seconds' : 'clock', { hour: 'numeric', minute: '2-digit', ...(seconds && { second: '2-digit' }) });
}

/** A full date and time in the browser's locale ("Oct 5, 2026, 2:05 PM"): tooltips and records. */
export function dateTime(at: Date | string): string {
  return formatted(at, 'date-time', { dateStyle: 'medium', timeStyle: 'short' });
}

/** A past time in words: "just now", else "5m ago". */
export function timeAgo(iso: string, now = Date.now()): string {
  const t = relTime(iso, now);
  return t === 'now' ? 'just now' : `${t} ago`;
}

/**
 * Whether a scroll container has been scrolled away from its top, for the pane header's fade
 * (`data-scrolled`). Watches a sentinel placed first inside the container through an
 * IntersectionObserver, so nothing runs per scroll event; the returned ref goes on the sentinel.
 * The fade is a transition: reaching the top must not restart a history page's render under way.
 */
export function useScrolled(): [boolean, (el: HTMLElement | null) => void] {
  const [scrolled, setScrolled] = useState(false);
  const ref = useCallback((el: HTMLElement | null) => {
    if (!el?.parentElement) return;
    const observer = new IntersectionObserver(([entry]) => {
      // A pane skipped under Settings (content-visibility: hidden) reads as scrolled away; keep its last state, or the fade flashes on the way back.
      if (el.checkVisibility?.() === false) return;
      startTransition(() => setScrolled(!entry.isIntersecting));
    }, { root: el.parentElement });
    observer.observe(el);
    return () => observer.disconnect();
  }, []);
  return [scrolled, ref];
}

/** The sentinel `useScrolled` watches: the first child of the scroll container, taking no room. */
export function ScrollSentinel({ sentinelRef }: Readonly<{ sentinelRef: (el: HTMLElement | null) => void }>) {
  return <div ref={sentinelRef} aria-hidden="true" className="-mb-px h-px" />;
}

/**
 * A skeleton (DESIGN.md Placeholder and loading states): `rows` bars of `sunken` in the shape
 * of what is coming, with the delayed quiet ring and label while a list or transcript first
 * loads. Decorative companion skeletons stay still; the caller marks the region `aria-busy`.
 */
const SKELETON_WIDTHS = ['w-3/4', 'w-full', 'w-1/2'];

export function Skeleton({ label, rows = 3, className, rowClassName, children, ...props }: Readonly<{ label: string; rows?: number; className?: string; rowClassName?: string; children?: ReactNode; 'aria-hidden'?: boolean | 'true' }>) {
  const hidden = !!props['aria-hidden'];
  return (
    <div aria-hidden={hidden || undefined} className={cn('flex flex-col gap-3', className)}>
      {!hidden && <Loading label={label} />}
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} aria-hidden="true" className={cn('h-4 rounded-sm bg-sunken', SKELETON_WIDTHS[i % 3], rowClassName)} />
      ))}
      {children}
    </div>
  );
}

/** The conversation's skeleton: a user bubble at the right, then assistant lines, in the transcript's gutters. */
export function TranscriptSkeleton({ label = 'Loading the conversation…' }: Readonly<{ label?: string }>) {
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
}: Readonly<{
  initial: string;
  onSave: (value: string) => void;
  onCancel: () => void;
  className?: string;
  label?: string;
}>) {
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

interface MdNode { type: string; value?: string; children?: MdNode[] }

/** HTML comments are notes to the writer, never shown: a comment block or inline comment is dropped; other raw HTML stays as text. */
function remarkDropComments() {
  const strip = (node: MdNode) => {
    if (!node.children) return;
    node.children = node.children.flatMap((child) => {
      if (child.type !== 'html' || !child.value?.includes('<!--')) { strip(child); return [child]; }
      const value = child.value.replace(/<!--[\s\S]*?(?:-->|$)/g, '');
      return value.trim() ? [{ ...child, value }] : [];
    });
  };
  return (tree: MdNode) => strip(tree);
}

const remarkPlugins = [remarkGfm, remarkDropComments];
/** For the owner's own text: a single newline is a line break, as typed with Shift+Enter. */
const remarkBreakPlugins = [remarkGfm, remarkBreaks, remarkDropComments];

/**
 * A code block (DESIGN.md `code-block`): the `code-bg` well with its inset ring, 10px radius,
 * a 24px header with the language when known and a copy button; right-click offers Copy code. The text
 * is read from the DOM, so it is exactly what is shown, unless `text` names what to copy.
 * The slots are for the diagram card: `head` is the header's middle (default: a spacer),
 * `body` replaces the `<pre>`, `foot` is a line under it.
 */
export function CodeBlock({ language, className, text, head, body, foot, children }: Readonly<{ language?: string; className?: string; text?: string; head?: ReactNode; body?: ReactNode; foot?: ReactNode; children: ReactNode }>) {
  const pre = useRef<HTMLPreElement>(null);
  const [copied, copy] = useCopied();
  const read = () => text ?? pre.current?.textContent ?? '';
  const items: ActionItem[] = [{ key: 'copy', label: 'Copy code', icon: <Copy />, onSelect: () => copy(read()) }];
  const context = useLazyContextMenu();
  return (
    <div className={cn('group/code relative my-2.5 overflow-hidden rounded-md bg-code-bg shadow-well', className)} {...context.shell}>
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
        <pre ref={pre} translate="no" className="!my-0 !rounded-none !shadow-none max-h-[480px] overflow-x-hidden overflow-y-auto px-3 pt-0.5 pb-2.5 font-mono text-code text-ink whitespace-pre-wrap [overflow-wrap:anywhere]">
          {children}
        </pre>
      )}
      {foot}
      {context.menu(<ContextMenu.Actions items={items} />)}
    </div>
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
    highlighterLoad ??= import('../lib/highlight').then((m) => {
      highlighter = m;
      return m;
    });
    highlighterLoad.then(
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
export function Highlighted({ language, code }: { language: string; code: string }) {
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

/** The reply an assistant message's markdown belongs to (its id and whole text), so a mermaid fence knows which drawing is its own. */
export const ItemContext = createContext<{ id: string; text: string } | null>(null);

/** What a reply's rich blocks reach in their Thread: `plan`, the live todo list's states by title key (`planKey`), which a reply's task list or steps tick off. */
export interface ReplyTools { plan?: ReadonlyMap<string, TodoStatus> }
export const ReplyContext = createContext<ReplyTools>({});

/** The Task whose transcript is rendering: markdown images and links by file path are served from its directory. */
export const SessionContext = createContext<string | undefined>(undefined);

/** That Task's directory, so a link by absolute path inside it opens and one outside stays text. */
export const WorkdirContext = createContext<string | undefined>(undefined);

/** The folder, in the Task's directory, that relative links and images resolve from: "" for replies, a Markdown file's own folder when it renders. */
export const MdBaseContext = createContext('');

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
function TempFileAction({ file, children }: Readonly<{ file: { path: string; hash: string }; children?: ReactNode }>) {
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


/**
 * A compact resolved file label. The path remains the link's identity and accessible name; a
 * cited line (`#L12`) shows after the name, and a Markdown link's own words (`label`) replace
 * the name. An image the view route serves opens larger at once.
 */
function FileLink({ path, url, label: words }: Readonly<{ path: string; url: string; label?: string }>) {
  const preview = usePreview();
  const label = fileLabel(path);
  const line = /#L(\d+)$/.exec(url)?.[1];
  const image = /\.(png|jpe?g|gif|webp|svg)$/i.test(path);
  return (
    <a href={url} target="_blank" rel="noopener noreferrer" title={line ? `${label.path}:${line}` : label.path} aria-label={`Open ${label.path}${line ? ` at line ${line}` : ''}`} className={cn(chipVariants({ fill: 'well' }), 'max-w-full min-w-0 align-middle text-file-link hover:bg-sunken')} onClick={event => {
      if (preview && previewClick(event)) {
        event.preventDefault();
        preview({ url, name: label.name, description: label.path, path: label.path, frameable: true, image }, event.currentTarget);
      }
    }}>
      <FileIcon path={label.path} className="size-3.5" />
      <span className="min-w-0 truncate">{words ?? <>{label.name}{line && <span className="text-muted">:{line}</span>}</>}</span>
    </a>
  );
}

/** A PDF, audio or video file shown in place, with the browser's own viewer or player; null for any other kind. */
export function FileMedia({ kind, url, name }: Readonly<{ kind: string; url: string; name: string }>) {
  if (kind === 'pdf') return <iframe src={url} title={`Preview ${name}`} referrerPolicy="no-referrer" className="min-h-96 w-full flex-1 rounded-sm bg-raised" />;
  // A file the agent made carries no caption track to offer.
  // eslint-disable-next-line jsx-a11y/media-has-caption
  if (kind === 'video') return <video src={url} controls preload="metadata" className="max-h-[70vh] w-full rounded-sm bg-sunken" aria-label={name} />;
  // eslint-disable-next-line jsx-a11y/media-has-caption
  if (kind === 'audio') return <audio src={url} controls preload="metadata" className="w-full" aria-label={name} />;
  return null;
}

/** The plain text of a hast element, as a link's words. */
function nodeText(node: ExtraProps['node']): string {
  return node?.children.map(child => child.type === 'text' ? child.value : child.type === 'element' ? nodeText(child) : '').join('') ?? '';
}

/** A link's words when they say more than its path does: not the path, its name, or the target as written. */
function linkWords(node: ExtraProps['node'], href: string | undefined, path: string): string | undefined {
  const words = nodeText(node).trim();
  const name = path.split('/').pop() ?? path;
  if (!words || [href, path, name, decodeURI(href ?? '')].includes(words) || words.replace(/:\d+(?::\d+)?$/, '').endsWith(path) || words.replace(/:\d+(?::\d+)?$/, '') === name) return undefined;
  return words;
}

function containsImage(node: ExtraProps['node']): boolean {
  return !!node && (node.tagName === 'img' || node.children.some(child => child.type === 'element' && containsImage(child)));
}

/**
 * An image the agent named by path, from the raw file route. Lazy, never wider than the
 * column; one that fails (outside the Task's folder, missing, not an image) becomes a note.
 * Clicking it opens the lightbox with the file's name.
 */
function LocalImage({ sessionId, path, alt }: Readonly<{ sessionId: string; path: string; alt: string }>) {
  const api = useApi();
  const preview = usePreview();
  const url = api.rawFileUrl(sessionId, path);
  // Keyed to the URL: a new file or connection generation gets a fresh attempt.
  const [failedUrl, setFailed] = useState<string | null>(null);
  const [size, setSize] = useState(() => imageSizes.get(url));
  const [open, setOpen] = useState(false);
  if (!api.supports('files-v1')) return <ImageNote alt={alt} detail="Images are unavailable on this instance." />;
  if (failedUrl === url) return <ImageNote alt={alt} detail={path} />;
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
          onError={() => setFailed(url)}
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

/** A web image (`img-src https:`): loaded from its host with no referrer, and a link to it; one that fails to load is the link card. */
function RemoteImage({ src, alt }: Readonly<{ src: string; alt: string }>) {
  const [failed, setFailed] = useState(false);
  if (failed) return <ImageNote alt={alt} detail={src} href={src} />;
  return (
    <a href={src} rel="noopener noreferrer" target="_blank" className="inline-block max-w-full">
      <img src={src} alt={alt} referrerPolicy="no-referrer" loading="lazy" decoding="async" onError={() => setFailed(true)} className="block h-auto max-h-[480px] w-auto max-w-full rounded-sm" />
    </a>
  );
}

/** A markdown image: by path from the Task's directory; `data:` inline; an https address loads from its host; anything else is a link card. */
function MdImage({ src, alt }: Readonly<{ src?: string; alt?: string }>) {
  const preview = usePreview();
  const tempRoots = useContext(TempRootContext);
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const base = useContext(MdBaseContext);
  const { streaming } = useContext(MdContext);
  const text = alt ?? '';
  const path = localPath(src);
  const file = taskFile(src, workdir, base);
  const temporary = !streaming && sessionId && preview ? tempFile(src, workdir, tempRoots) : null;
  if (temporary) return <TempFileAction file={temporary}>{text && <span>{text}</span>}</TempFileAction>;
  if (path) return sessionId && file ? <LocalImage sessionId={sessionId} path={file.path} alt={text} /> : <ImageNote alt={text} detail={path} />;
  if (src && /^(data|blob):/i.test(src)) return <img src={src} alt={text} loading="lazy" decoding="async" className="block h-auto max-h-[480px] w-auto max-w-full rounded-sm" />;
  if (src && /^https:/i.test(src)) return <RemoteImage key={src} src={src} alt={text} />;
  return <ImageNote alt={text} detail={src ?? ''} href={src && /^https?:/i.test(src) ? src : undefined} />;
}

/** Where a link goes: a Task file through the view route once it is known to exist, a web or mail address as it is, nothing otherwise. */
function linkTarget(api: ReturnType<typeof useApi>, href: string | undefined, file: ReturnType<typeof taskFile>, sessionId: string | undefined, exists: boolean): string | undefined {
  if (localPath(href)) return api.supports('files-v1') && sessionId && file && exists ? api.viewFileUrl(sessionId, file.path) + file.hash : undefined;
  return typeof href === 'string' && /^(https?:|mailto:)/i.test(href) ? href : undefined;
}

/** GitLab's mark (Tabler `brand-gitlab`, MIT), drawn on Lucide's grid and stroke. */
export function GitLabMark({ className }: Readonly<{ className?: string }>) {
  return <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" className={cn('inline size-3.5 shrink-0 align-[-2px]', className)}><path d="M21 14l-9 7l-9 -7l3 -11l3 7h6l3 -7l3 11" /></svg>;
}

/** GitHub's mark (Tabler `brand-github`, MIT), drawn like GitLabMark. */
export function GitHubMark({ className }: Readonly<{ className?: string }>) {
  return <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinecap="round" strokeLinejoin="round" className={cn('inline size-3.5 shrink-0 align-[-2px]', className)}><path d="M9 19c-4.3 1.4 -4.3 -2.5 -6 -3m12 5v-3.5c0 -1 .1 -1.4 -.5 -2c2.8 -.3 5.5 -1.4 5.5 -6a4.6 4.6 0 0 0 -1.3 -3.2a4.2 4.2 0 0 0 -.1 -3.2s-1.1 -.3 -3.5 1.3a12.3 12.3 0 0 0 -6.2 0c-2.4 -1.6 -3.5 -1.3 -3.5 -1.3a4.2 4.2 0 0 0 -.1 3.2a4.6 4.6 0 0 0 -1.3 3.2c0 4.6 2.7 5.7 5.5 6c-.6 .6 -.6 1.2 -.5 2v3.5" /></svg>;
}

/**
 * A link to an item of the Thread's GitLab project: GitLab's mark, then the reply's own words, or
 * "MR !72" for a bare address. A plain click opens it in the GitLab side panel; a modified one, GitLab.
 */
function GitLabLink({ href, words }: Readonly<{ href: string; words: string }>) {
  const gitlab = useContext(GitLabItemsContext);
  const ref = gitlab ? parseGitLabUrl(href, gitlab.origin) : null;
  const label = !words || /^https?:\/\//i.test(words) ? (ref ? gitLabWords(ref) : words) : words;
  return (
    <a href={href} target="_blank" rel="noopener noreferrer" data-gitlab-link="" title={href} onClick={(e) => {
      if (!gitlab || !ref || !plainClick(e)) return;
      e.preventDefault();
      gitlab.open([ref], ref);
    }}>
      <GitLabMark className="mr-1" />
      {label}
    </a>
  );
}

/** Local available files preview on an ordinary click; modified clicks and web links stay native. */
function MdLink({ href, children, node }: Readonly<{ href?: string; children?: ReactNode } & ExtraProps>) {
  const api = useApi();
  const preview = usePreview();
  const tempRoots = useContext(TempRootContext);
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const base = useContext(MdBaseContext);
  const { streaming } = useContext(MdContext);
  const gitlab = useContext(GitLabItemsContext);
  const named = !streaming ? taskFile(href, workdir, base) : null;
  const { eligible, exists, file: resolved } = useFileReference(named?.path);
  // A bare name opens the one file it means.
  const file = named && resolved ? { ...named, path: resolved } : named;
  const temporary = !streaming && sessionId && preview ? tempFile(href, workdir, tempRoots) : null;
  if (temporary) return <TempFileAction file={temporary}>{children}</TempFileAction>;
  if (href && gitlab && parseGitLabUrl(href, gitlab.origin)) return <GitLabLink href={href} words={nodeText(node).trim()} />;
  const target = linkTarget(api, href, file, sessionId, exists);
  let link: ReactNode;
  if (target && file && localPath(href) && !containsImage(node)) link = <FileLink path={file.path} url={target} label={linkWords(node, href, file.path)} />;
  else if (target) link = <a href={target} rel="noopener noreferrer" target="_blank" onClick={event => {
    if (preview && file && localPath(href) && previewClick(event)) {
      event.preventDefault();
      preview({ url: target, name: file.path.split('/').pop() || file.path, description: file.path, path: file.path, frameable: true }, event.currentTarget);
    }
  }}>{children}</a>;
  else link = children;
  return localPath(href) ? <span key={named?.path} data-file-reference={eligible ? named?.path : undefined}>{link}</span> : <>{link}</>;
}

/**
 * Inline code that names a file of the Task's folder (`ai-news.html`, `out/report.html`)
 * opens it through the view route once the file is known to exist; until then, and when it
 * does not, it is the same plain code. Nothing is asked while its block is still streaming.
 */
function InlineCode({ text, className }: Readonly<{ text: string; className?: string }>) {
  const api = useApi();
  const preview = usePreview();
  const tempRoots = useContext(TempRootContext);
  const sessionId = useContext(SessionContext);
  const workdir = useContext(WorkdirContext);
  const { streaming } = useContext(MdContext);
  const hintedPath = sessionId && !streaming && !text.includes('/') ? hintPath(text, workdir ?? '') : undefined;
  let file: ReturnType<typeof codeFile> = null;
  if (sessionId && !streaming) file = codeFile(text, workdir) ?? (hintedPath ? { path: hintedPath, hash: '' } : null);
  const { eligible, exists, file: resolved } = useFileReference(file?.path, text);
  // A bare name opens the one file it means.
  const target = resolved ?? file?.path;
  const url = api.supports('files-v1') && sessionId && target ? api.viewFileUrl(sessionId, target) : undefined;
  const temporary = !streaming && sessionId && preview && text.startsWith('/') ? tempFile(text.split('/').map(encodeURIComponent).join('/'), workdir, tempRoots) : null;
  if (temporary) return <TempFileAction file={temporary} />;
  const code = <code className={className}>{text}</code>;
  if (!file) return code;
  return (
    <span key={file?.path} data-file-reference={eligible ? file?.path : undefined}>
      {exists && url && file && target ? <FileLink path={target} url={url + file.hash} /> : code}
    </span>
  );
}

interface Span {
  start: { offset?: number };
  end: { offset?: number };
}

function MermaidBlock({ source, position, children }: Readonly<{ source: string; position?: Span; children: ReactNode }>) {
  const { text, streaming } = useContext(MdContext);
  const item = useContext(ItemContext);
  const ready = !streaming || !position || fenceClosed(text, position.start.offset ?? 0, position.end.offset ?? text.length);
  // The fence's index among the reply's mermaid fences (its `fence_index`); two fences of one source share the first's.
  const index = item ? mermaidFences(item.text).indexOf(source.trim()) : -1;
  return (
    <DiagramCard source={source} ready={ready} fence={item && index >= 0 ? { itemId: item.id, index } : undefined}>
      {children}
    </DiagramCard>
  );
}

/** A hast `className` (a list or one string) as one string. */
function classText(className: unknown): string {
  if (Array.isArray(className)) return className.join(' ');
  return typeof className === 'string' ? className : '';
}

const languageOf = (className: unknown): string | undefined => /language-([\w+-]+)/.exec(classText(className))?.[1];

/** The children as one string when they are text (a fenced block's, an inline code's); null when they hold elements. */
function textOf(children: ReactNode): string | null {
  if (typeof children === 'string') return children;
  return Array.isArray(children) && children.every((c) => typeof c === 'string') ? children.join('') : null;
}

/** react-markdown drops `file:` URLs as unsafe; here they are paths for the file routes, which MdImage and MdLink decide on. */
const mdUrl = (url: string): string => (/^file:\/\//i.test(url) ? url : defaultUrlTransform(url));

function MdTable({ children }: Readonly<{ children?: ReactNode }>) {
  const { streaming } = useContext(MdContext);
  return <MarkdownTable streaming={streaming}>{children}</MarkdownTable>;
}

const CALLOUT: Record<CalloutKind, { title: string; Icon: LucideIcon }> = {
  note: { title: 'Note', Icon: Info },
  tip: { title: 'Tip', Icon: Lightbulb },
  important: { title: 'Important', Icon: CircleAlert },
  warning: { title: 'Warning', Icon: TriangleAlert },
  caution: { title: 'Caution', Icon: OctagonAlert },
  question: { title: 'Question', Icon: CircleHelp },
  blocker: { title: 'Blocker', Icon: Ban },
};

/** A GitHub alert (`> [!WARNING]`, marked by remarkRich) as a toned callout with its title; any other quote as written. */
function MdQuote({ node, children }: Readonly<{ children?: ReactNode } & ExtraProps>) {
  const kind = node?.properties.dataCallout as CalloutKind | undefined;
  const callout = kind && CALLOUT[kind];
  if (!callout) return <blockquote>{children}</blockquote>;
  return (
    <div role="note" className="md-callout" data-callout={kind}>
      <p className="md-callout-title"><callout.Icon aria-hidden="true" className="size-3.5 shrink-0" />{callout.title}</p>
      {children}
    </div>
  );
}

/** A plan item's state: its glyph, colour and words. */
const PLAN_MARK: Record<TodoStatus, { Icon: LucideIcon; tone: string; words: string }> = {
  done: { Icon: Check, tone: 'text-success', words: 'done' },
  in_progress: { Icon: CircleDot, tone: 'text-accent', words: 'in progress' },
  blocked: { Icon: Ban, tone: 'text-warning', words: 'blocked' },
  pending: { Icon: Circle, tone: 'text-faint', words: 'to do' },
};

type HastNode = NonNullable<ExtraProps['node']>;
const checkedItem = (li: HastNode) => li.children.some((c) => c.type === 'element' && c.tagName === 'input' && !!c.properties.checked);

/** An item's state: the todo list's row of the same title when there is one, else a task item's own box. */
function itemState(li: HastNode, plan: ReplyTools['plan']): { state?: TodoStatus; live: boolean } {
  const key = li.properties.dataPlan;
  const live = typeof key === 'string' && key ? plan?.get(key) : undefined;
  if (live) return { state: live, live: true };
  const task = String(li.properties.className ?? '').includes('task-list-item');
  return { state: task ? (checkedItem(li) ? 'done' : 'pending') : undefined, live: false };
}

/** "2 of 4 done", with a bar, and "from the todo list" while some items follow it. */
function PlanProgress({ done, total, live }: Readonly<{ done: number; total: number; live: boolean }>) {
  return (
    <p className="md-progress">
      <progress value={done} max={total} aria-label={`${done} of ${total} done`} />
      <span className="tabular-nums">{done} of {total} done</span>
      {live && <span>· from the todo list</span>}
    </p>
  );
}

/** A list's items' states, counted: done of total, and whether any follows the todo list. */
function planTally(list: HastNode | undefined, plan: ReplyTools['plan']) {
  let done = 0, total = 0, live = false;
  for (const li of list?.children ?? []) {
    if (li.type !== 'element' || li.tagName !== 'li') continue;
    const item = itemState(li, plan);
    total++;
    live ||= item.live;
    if (item.state === 'done') done++;
  }
  return { done, total, live };
}

/** A status list item's state (remarkRich `statusList`): its glyph, colour and words. */
const STATUS_MARK: Record<(typeof STATUS_ORDER)[number], { Icon: LucideIcon; tone: string; words: string; count: string }> = {
  fail: { Icon: X, tone: 'text-error', words: 'failed', count: 'failed' },
  warn: { Icon: TriangleAlert, tone: 'text-warning', words: 'warning', count: 'warning' },
  skip: { Icon: SkipForward, tone: 'text-muted', words: 'skipped', count: 'skipped' },
  ok: { Icon: Check, tone: 'text-success', words: 'passed', count: 'passed' },
};
const SEVERITY_TONE: Record<(typeof SEVERITY_ORDER)[number], string> = { critical: 'text-error', high: 'text-error', medium: 'text-warning', low: 'text-muted' };

/**
 * A status or severity list's count line: each state with rows, in the list's order, the
 * failures and warnings (critical, high, medium) in their tone. Null for any other list.
 */
function ListCounts({ node }: Readonly<{ node?: HastNode }>) {
  const kind = String(node?.properties.className ?? '');
  const key = kind.includes('md-status') ? 'dataStatus' : kind.includes('md-severity') ? 'dataSeverity' : null;
  if (!key || !node) return null;
  const order: readonly string[] = key === 'dataStatus' ? STATUS_ORDER : SEVERITY_ORDER;
  const counts = new Map<string, number>();
  for (const li of node.children) if (li.type === 'element') counts.set(String(li.properties[key]), (counts.get(String(li.properties[key])) ?? 0) + 1);
  const parts = order.filter((k) => counts.get(k)).map((k) => {
    const n = counts.get(k)!;
    if (key === 'dataStatus') {
      const mark = STATUS_MARK[k as keyof typeof STATUS_MARK];
      return { k, words: `${n} ${mark.count}`, tone: k === 'fail' || k === 'warn' ? mark.tone : '' };
    }
    const tone = SEVERITY_TONE[k as keyof typeof SEVERITY_TONE];
    return { k, words: `${n} ${k}`, tone: k === 'low' ? '' : tone };
  });
  return (
    <p className="md-counts">
      {parts.map((p, i) => (
        <span key={p.k}>
          {i > 0 && ' · '}
          <span className={cn(p.tone && `font-semibold ${p.tone}`)}>{p.words}</span>
        </span>
      ))}
    </p>
  );
}

/** A file row (remarkRich `fileList`): the file's icon, the item as written (its path a file link), then its line counts with a bar. */
function FileItem({ node, className, children }: Readonly<{ node: HastNode; className?: string; children?: ReactNode }>) {
  const path = String(node.properties.dataPath);
  const add = node.properties.dataAdd == null ? null : Number(node.properties.dataAdd);
  const del = Number(node.properties.dataDel ?? 0);
  return (
    <li className={className}>
      <FileIcon path={path} className="md-file-icon" />
      <span className="min-w-0 flex-1">{children}</span>
      {add != null && (
        <span className="md-file-counts">
          <span className="text-success">+{add}</span>
          <span className="text-error">−{del}</span>
          <span aria-hidden="true" className="md-file-bar"><i style={{ width: `${add + del ? (add / (add + del)) * 100 : 0}%` }} /></span>
        </span>
      )}
    </li>
  );
}

/** A task list or steps item: its state's glyph (a task item's box gives way to it), live from the todo list when a row has its title. Status, severity and file items draw their own rows. */
function MdItem({ node, className, children }: Readonly<{ className?: string; children?: ReactNode } & ExtraProps>) {
  const status = node?.properties.dataStatus as keyof typeof STATUS_MARK | undefined;
  if (status && STATUS_MARK[status]) {
    const mark = STATUS_MARK[status];
    return (
      <li className={className} data-status={status}>
        <mark.Icon aria-hidden="true" className={cn('md-mark', mark.tone)} strokeWidth={2.25} />
        {children}
        <span className="sr-only">, {mark.words}</span>
      </li>
    );
  }
  const severity = node?.properties.dataSeverity as keyof typeof SEVERITY_TONE | undefined;
  if (severity && SEVERITY_TONE[severity]) {
    return (
      <li className={className} data-severity={severity}>
        <span className={cn('md-level', SEVERITY_TONE[severity])}>{String(node?.properties.dataLabel ?? severity)}<span className="sr-only">:</span></span>
        <span className="min-w-0">{children}</span>
      </li>
    );
  }
  if (node?.properties.dataPath) return <FileItem node={node} className={className}>{children}</FileItem>;
  // A choice (remarkRich `choices`) shows its letter in the gutter and is never tied to the todo list.
  if (node?.properties.dataOption) return <li className={className} data-option={String(node.properties.dataOption)}>{children}</li>;
  if (node?.properties.dataWhen) {
    return (
      <li className={className} data-gap={node.properties.dataGap ? true : undefined}>
        <time>{String(node.properties.dataWhen)}</time>
        <span aria-hidden="true" className="md-timeline-dot" />
        <span className="min-w-0">{children}</span>
      </li>
    );
  }
  return <PlanItem node={node} className={className}>{children}</PlanItem>;
}

function PlanItem({ node, className, children }: Readonly<{ node?: HastNode; className?: string; children?: ReactNode }>) {
  const { plan } = useContext(ReplyContext);
  const item = node ? itemState(node, plan) : { live: false };
  if (!item.state) return <li className={className}>{children}</li>;
  const mark = PLAN_MARK[item.state];
  const rest = Children.toArray(children).filter((c) => !(isValidElement(c) && c.type === 'input'));
  return (
    <li className={className} data-state={item.state} data-live={item.live || undefined}>
      <mark.Icon aria-hidden="true" className={cn('md-mark', mark.tone)} strokeWidth={item.state === 'done' ? 2.5 : undefined} />
      {rest}
      <span className="sr-only">, {mark.words}{item.live && ' in the todo list'}</span>
    </li>
  );
}

/** Steps with the progress line above them once any follows the todo list. */
function MdOrdered({ node, children, ...props }: Readonly<{ children?: ReactNode } & ExtraProps & Omit<ComponentProps<'ol'>, 'children'>>) {
  const { plan } = useContext(ReplyContext);
  const list = <ol {...props}>{children}</ol>;
  if (/md-(status|severity)/.test(String(props.className ?? ''))) return <><ListCounts node={node} />{list}</>;
  const tally = String(props.className ?? '').includes('md-steps') ? planTally(node, plan) : null;
  if (!tally?.live) return list;
  return (
    <>
      <PlanProgress {...tally} />
      {list}
    </>
  );
}

/** A task list with how many of its items are done above it, each item live from the todo list when a row has its title. */
function MdList({ node, className, children }: Readonly<{ className?: string; children?: ReactNode } & ExtraProps>) {
  const { plan } = useContext(ReplyContext);
  const list = <ul className={className}>{children}</ul>;
  if (/md-(status|severity)/.test(className ?? '')) return <><ListCounts node={node} />{list}</>;
  if (!Number(node?.properties.dataTotal)) return list;
  return (
    <>
      <PlanProgress {...planTally(node, plan)} />
      {list}
    </>
  );
}

/**
 * Echo trace (r24): the mouse over inline code underlines every identical inline code span in the same
 * message, through a CSS Custom Highlight (no DOM change, nothing opens); nothing on touch or without the API.
 */
function echoOver(e: ReactPointerEvent<HTMLDivElement>) {
  if (typeof CSS === 'undefined' || !('highlights' in CSS)) return;
  const code = e.pointerType === 'mouse' ? (e.target as Element).closest?.('code') : null;
  const text = code && !code.closest('pre') ? code.textContent?.trim() ?? '' : '';
  const same = text.length >= 3 ? [...e.currentTarget.querySelectorAll('code')].filter((c) => !c.closest('pre') && c.textContent?.trim() === text) : [];
  if (same.length < 2) {
    CSS.highlights.delete('echo');
    return;
  }
  CSS.highlights.set('echo', new Highlight(...same.map((c) => {
    const range = new Range();
    range.selectNodeContents(c);
    return range;
  })));
}

function echoLeave() {
  if (typeof CSS !== 'undefined' && 'highlights' in CSS) CSS.highlights.delete('echo');
}

/** The reply's outline (`outlineOf`), when it has one: its title heading carries the section index. */
const OutlineContext = createContext<Outline | null>(null);

const hastText = (node: HastNode | HastNode['children'][number]): string => node.type === 'text' ? node.value : 'children' in node ? node.children.map(hastText).join('') : '';

/** A long reply's title with an index of its sections under it; a click brings the section into view. Any other heading as written. */
function MdTitle({ node, children, ...props }: Readonly<{ children?: ReactNode } & ExtraProps & Omit<ComponentProps<'h1'>, 'children'>>) {
  const outline = useContext(OutlineContext);
  if (!outline || !node || hastText(node).trim() !== outline.title) return <h1 {...props}>{children}</h1>;
  const show = (target: HTMLElement, section: string) => {
    const heading = [...(target.closest('.md')?.querySelectorAll('h2') ?? [])].find((h) => h.textContent?.trim() === section);
    heading?.scrollIntoView({ block: 'start' });
  };
  return (
    <div className="md-outline">
      <h1 {...props}>{children}</h1>
      <ol aria-label="Sections">
        {outline.sections.map((section, i) => (
          <li key={i}>
            <button type="button" onClick={(e) => show(e.currentTarget, section)}>
              <span aria-hidden="true" className="md-outline-n">{String(i + 1).padStart(2, '0')}</span>
              {section}
            </button>
          </li>
        ))}
      </ol>
    </div>
  );
}

/**
 * A list's head (remarkRich `heads`), its glyph in its tone when it says what was or was not verified; a
 * paragraph with a test tally gets the count line above it, failures in their tone. Any other paragraph as written.
 */
function MdParagraph({ node, children, ...props }: Readonly<{ children?: ReactNode } & ExtraProps & Omit<ComponentProps<'p'>, 'children'>>) {
  const tone = node?.properties.dataTone;
  const tally = node?.properties.dataTally;
  if (tone === 'ok' || tone === 'warn') {
    const Icon = tone === 'ok' ? Check : TriangleAlert;
    return <p {...props} data-tone={tone}><Icon aria-hidden="true" className="size-3.5 shrink-0" />{children}</p>;
  }
  if (typeof tally !== 'string') return <p {...props}>{children}</p>;
  const parts = tally.split(',');
  return (
    <>
      <p className="md-counts" aria-hidden="true">
        {parts.map((part, i) => (
          <span key={part}>
            {i > 0 && ' · '}
            <span className={cn(/^[1-9][\d]* (failed|errors)$/.test(part) && 'font-semibold text-error')}>{part}</span>
          </span>
        ))}
      </p>
      <p {...props}>{children}</p>
    </>
  );
}

const SHELL = new Set(['sh', 'bash', 'shell', 'console', 'zsh']);

/** One command row: `$`, the command, its own copy button, and the output lines under it, muted. */
function CommandRow({ command }: Readonly<{ command: Command }>) {
  const [copied, copy] = useCopied();
  return (
    <li className="md-cmd">
      <div className="flex items-start gap-2">
        <code className="min-w-0 flex-1 whitespace-pre-wrap [overflow-wrap:anywhere]"><span aria-hidden="true" className="mr-2 text-faint select-none">$</span>{command.command}</code>
        <Button size="icon-sm" variant="subtle" aria-label={copied ? 'Copied' : `Copy ${command.command}`} className={cn('text-muted', copied && 'text-success')} onClick={() => copy(command.command)}>
          {copied ? <Check /> : <Copy />}
        </Button>
      </div>
      {command.output.length > 0 && <pre className="md-cmd-out">{command.output.join('\n')}</pre>}
    </li>
  );
}

/** A shell session fence (`commandsOf`): a row per command, so each copies alone. */
function CommandRows({ commands }: Readonly<{ commands: Command[] }>) {
  return <ul translate="no" className="md-cmds">{commands.map((c, i) => <CommandRow key={i} command={c} />)}</ul>;
}

const mdComponents: Components = {
  a: MdLink,
  img: MdImage,
  table: MdTable,
  blockquote: MdQuote,
  ul: MdList,
  ol: MdOrdered,
  li: MdItem,
  p: MdParagraph,
  h1: MdTitle,
  code({ className, children }) {
    const language = languageOf(className);
    const code = textOf(children);
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
    const first = code?.type === 'element' ? code.children[0] : undefined;
    const commands = language && SHELL.has(language) && first?.type === 'text' ? commandsOf(first.value) : null;
    if (commands) return <CodeBlock language={language} text={commands.map((c) => c.command).join('\n')} body={<CommandRows commands={commands} />}>{children}</CodeBlock>;
    // GitLab's ```suggestion:-0+0 fence: the lines a reviewer proposes.
    return <CodeBlock language={language === 'suggestion' ? 'Suggested change' : language}>{children}</CodeBlock>;
  },
};

/**
 * Markdown for untrusted provider text: no raw HTML by default, safe links only, images by path served
 * from the Task's directory (`SessionContext`) and no image from another origin. Completed
 * messages mount as one document. Streaming text uses top-level blocks so only the last block
 * is parsed again per delta; it keeps those boundaries after completion to retain DOM state.
 * `breaks` keeps single newlines as line breaks, for text the owner typed; provider text stays CommonMark.
 */
export function Markdown({ text, streaming = false, breaks = false, rehypePlugins, className }: Readonly<{ text: string; streaming?: boolean; breaks?: boolean; rehypePlugins?: Options['rehypePlugins']; className?: string }>) {
  const [hasStreamed, setHasStreamed] = useState(streaming);
  if (streaming && !hasStreamed) setHasStreamed(true);
  const blocks = useMemo(() => hasStreamed ? splitBlocks(text) : [text], [text, hasStreamed]);
  const fileDemand = useFileDemand(text);
  // The outline needs the whole reply (a streamed one renders in blocks), and waits for it to finish like other rich blocks.
  const outline = useMemo(() => streaming ? null : outlineOf(text), [text, streaming]);
  return (
    <OutlineContext.Provider value={outline}>
      <div ref={fileDemand} className={cn('md', className)} onPointerOver={echoOver} onPointerLeave={echoLeave}>
        {blocks.map((block, i) => (
          <MarkdownBlock key={i} text={block} streaming={streaming && i === blocks.length - 1} first={i === 0} breaks={breaks} rehypePlugins={rehypePlugins} />
        ))}
      </div>
    </OutlineContext.Provider>
  );
}

/** A path outside Markdown (a review note's `path:line`) that links as inline code would once the file is known to exist. */
export function FileRef({ text, className }: Readonly<{ text: string; className?: string }>) {
  const fileDemand = useFileDemand(text);
  return <span ref={fileDemand} className="min-w-0"><InlineCode text={text} className={className} /></span>;
}

const FILE_MENTION = /`([^`\n]{1,300})`|\[([^\]\n]{1,300})\]\(([^)\s]{1,2048})\)/g;

/**
 * Text too large to parse as Markdown (a whole message of megabytes), shown as written: only
 * its inline code and links that name files become file links, as they would in Markdown.
 */
export function PlainText({ text, className }: Readonly<{ text: string; className?: string }>) {
  const fileDemand = useFileDemand(text);
  const parts = useMemo(() => {
    const out: ReactNode[] = [];
    let at = 0;
    for (const m of text.matchAll(FILE_MENTION)) {
      const file = m[1] !== undefined ? looksLikePath(m[1]) : !!localPath(m[3]);
      if (!file) continue;
      out.push(text.slice(at, m.index));
      out.push(m[1] !== undefined ? <InlineCode key={m.index} text={m[1]} /> : <MdLink key={m.index} href={m[3]}>{m[2]}</MdLink>);
      at = m.index + m[0].length;
    }
    out.push(text.slice(at));
    return out;
  }, [text]);
  return <p ref={fileDemand} className={cn('break-words whitespace-pre-wrap', className)}>{parts}</p>;
}

const MarkdownBlock = memo(function MarkdownBlock({ text, streaming, first, breaks, rehypePlugins }: { text: string; streaming: boolean; first: boolean; breaks: boolean; rehypePlugins?: Options['rehypePlugins'] }) {
  const source = useMemo(() => ({ text, streaming }), [text, streaming]);
  // With the Thread's GitLab project known, "MR !72" and "pipeline #123" in the text link to it.
  const origin = useContext(GitLabItemsContext)?.origin;
  const plugins = useMemo(() => {
    const base = breaks ? remarkBreakPlugins : remarkPlugins;
    // Rich blocks wait for the block to finish, so a half-written list never flips shape mid-stream.
    const rich: NonNullable<Options['remarkPlugins']> = streaming ? base : [...base, [remarkRich, { first }]];
    return origin ? [...rich, remarkGitLabRefs(origin)] : rich;
  }, [breaks, origin, streaming, first]);
  return (
    <MdContext.Provider value={source}>
      <ReactMarkdown remarkPlugins={plugins} rehypePlugins={rehypePlugins} components={mdComponents} urlTransform={mdUrl}>
        {text}
      </ReactMarkdown>
    </MdContext.Provider>
  );
});

/**
 * The quiet loading indicator: nothing for `delay` ms (a wait that short shows nothing new),
 * then a spinner and a word. Replaces skeleton placeholders; what was on screen stays.
 */
export function Loading({ label = 'Loading…', delay = 300, className }: Readonly<{ label?: string; delay?: number; className?: string }>) {
  const late = useLate(true, delay);
  return (
    <output aria-busy="true" className={cn('flex min-h-8 items-center gap-2 text-caption text-muted', className)}>
      {late && (
        <span className="flex items-center gap-2 animate-fade-in">
          <Spinner className="border-muted" />
          {label}
        </span>
      )}
      {!late && <span className="sr-only">{label}</span>}
    </output>
  );
}

/**
 * A file's path on one line that gives way from the middle of its folder: the file name stays whole, then its own
 * folder, and the folders above it shorten at their end ("build-now…/fail/fail_test.go"). Assistive tech reads the
 * path as one word: the parts are laid out as blocks, which a name from content would read apart.
 */
export function FilePath({ path, className }: Readonly<{ path: string; className?: string }>) {
  const name = path.slice(path.lastIndexOf('/') + 1);
  const dir = path.slice(0, path.length - name.length);
  const own = dir.slice(dir.lastIndexOf('/', dir.length - 2) + 1);
  const above = dir.slice(0, dir.length - own.length);
  return (
    <span className={cn('flex min-w-0', className)}>
      <span className="sr-only">{path}</span>
      {above && <span aria-hidden="true" className="min-w-0 truncate">{above}</span>}
      <span aria-hidden="true" className="flex max-w-full shrink-0">
        {own && <span className="min-w-0 truncate">{own}</span>}
        <span className="max-w-full shrink-0 truncate">{name}</span>
      </span>
    </span>
  );
}

/** A `caption` line for feedback: `error` and `warn` are the only coloured ones. */
export function Note({ tone = 'muted', className, children, role, id }: Readonly<{ tone?: 'muted' | 'error' | 'warn' | 'info'; className?: string; children: ReactNode; role?: 'alert' | 'status'; id?: string }>) {
  return (
    <p id={id} role={role} className={cn('text-caption animate-fade-in', tone === 'error' && 'text-error', tone === 'warn' && 'text-warning', tone === 'info' && 'text-info', tone === 'muted' && 'text-muted', className)}>
      {children}
    </p>
  );
}

/** The connected instance a view belongs to, after its title in the header (DESIGN.md Connected instances); nothing on this instance. */
export function InstanceName() {
  const owner = useApi().owner;
  if (!owner) return null;
  return (
    <>
      <span aria-hidden="true" className="text-muted">·</span>
      <span className="max-w-40 shrink-0 truncate text-caption text-muted" title={owner.label}>{owner.label}</span>
    </>
  );
}
