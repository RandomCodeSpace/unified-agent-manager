import { Check, CircleDashed, Copy, CornerDownLeft, Minus, Pause, X } from 'lucide-react';
import { createContext, useContext, useEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { LIVE, taskName, type AccountUsage, type Badge, type BadgeColor, type Meta, type SessionState, type SessionSummary, type Settings } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { DEFAULT_SETTINGS, type Action } from '../state';
import { fenceClosed } from '../lib/diagram';
import type { HighlightTree } from '../lib/highlight';
import { DiagramCard } from './Diagram';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
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
  meta: Meta | null;
  dispatch: (a: Action) => void;
  narrow: boolean;
  /** Tasks with activity the user has not looked at yet (UI-local). */
  hasNews: (s: SessionSummary) => boolean;
  /** The service's settings (the send default the composer follows). */
  settings: Settings;
  usage: AccountUsage | null;
}

export const AppContext = createContext<AppContextValue>({
  meta: null,
  dispatch: () => {},
  narrow: false,
  hasNews: () => false,
  settings: DEFAULT_SETTINGS,
  usage: null,
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
  if (!label) {
    return (
      <span className={cn('inline-flex size-4 shrink-0 items-center justify-center', className)} title={title}>
        <StateGlyph state={state} />
        <span className="sr-only">{text}</span>
      </span>
    );
  }
  return (
    <Chip tone={attention ? 'attention' : tone === 'faint' ? 'muted' : tone} className={className} title={title}>
      <StateGlyph state={state} />
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
    <span aria-hidden="true" className={cn('inline-flex size-4 shrink-0 items-center justify-center rounded-xs font-mono text-badge font-semibold text-on-primary select-none', BADGE_BG[badge.color], className)}>
      {badge.text}
    </span>
  );
}

/** A tiny dot in a tone; used for connection status and unread marks. */
export function Dot({ tone, className, pulse = false }: { tone: Tone; className?: string; pulse?: boolean }) {
  return <span aria-hidden="true" className={cn('inline-block size-2 shrink-0 rounded-full', TONE_BG[tone], pulse && 'animate-pulse-dot', className)} />;
}

export { Spinner } from './ui/spinner';

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
 * A code block (DESIGN.md `code-block`): sunken well, hairline, 10px radius, a 24px header
 * with the language when known and a copy button; right-click offers Copy code. The text
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
      <ContextMenu.Trigger render={<div className={cn('group/code relative my-2.5 overflow-hidden rounded-md border border-hairline bg-code-bg', className)} />}>
        <div className="flex h-6 items-center gap-2 border-b border-hairline px-3 text-code-sm text-muted">
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
          <pre ref={pre} translate="no" className="!my-0 !rounded-none !border-0 max-h-[480px] overflow-auto px-3 py-2.5 font-mono text-code text-ink">
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

const mdComponents: Components = {
  a({ href, children }) {
    const safe = typeof href === 'string' && /^(https?:|mailto:)/i.test(href);
    return safe ? (
      <a href={href} rel="noopener noreferrer" target="_blank">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    );
  },
  code({ className, children }) {
    const language = languageOf(className);
    const code = typeof children === 'string' ? children : Array.isArray(children) && children.every((c) => typeof c === 'string') ? children.join('') : null;
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

/** Markdown for untrusted provider text: no raw HTML, no images, safe links only. */
export function Markdown({ text, streaming = false, className }: { text: string; streaming?: boolean; className?: string }) {
  const source = useMemo(() => ({ text, streaming }), [text, streaming]);
  return (
    <MdContext.Provider value={source}>
      <div className={cn('md', className)}>
        <ReactMarkdown remarkPlugins={remarkPlugins} components={mdComponents} disallowedElements={['img']} unwrapDisallowed>
          {text}
        </ReactMarkdown>
      </div>
    </MdContext.Provider>
  );
}

/** A `caption` line for feedback: `error` and `warn` are the only coloured ones. */
export function Note({ tone = 'muted', className, children, role, id }: { tone?: 'muted' | 'error' | 'warn' | 'info'; className?: string; children: ReactNode; role?: 'alert' | 'status'; id?: string }) {
  return (
    <p id={id} role={role} className={cn('text-caption', tone === 'error' && 'text-error', tone === 'warn' && 'text-warning', tone === 'info' && 'text-info', tone === 'muted' && 'text-muted', className)}>
      {children}
    </p>
  );
}
