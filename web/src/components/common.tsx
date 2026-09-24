import { Check, CircleDashed, Copy, CornerDownLeft, Minus, Pause, X } from 'lucide-react';
import { createContext, useContext, useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { LIVE, taskName, type Meta, type SessionState, type SessionSummary } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import type { Action } from '../state';
import { Button } from './ui/button';
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
  faint: 'text-faint',
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
}

export const AppContext = createContext<AppContextValue>({
  meta: null,
  dispatch: () => {},
  narrow: false,
  hasNews: () => false,
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
    <span
      className={cn(
        'inline-flex h-5 shrink-0 items-center gap-1.5 rounded-xs px-1.5 text-caption whitespace-nowrap',
        attention ? 'bg-attention-wash text-attention' : TONE_TEXT[tone],
        tone === 'faint' && 'text-muted',
        className,
      )}
      title={title}
    >
      <StateGlyph state={state} />
      {text}
    </span>
  );
}

function StateGlyph({ state }: { state: SessionState }) {
  const tone = STATE_TONE[state];
  switch (state) {
    case 'working':
    case 'starting':
      return <span aria-hidden="true" className="size-2 rounded-full bg-accent animate-pulse-dot motion-reduce:bg-transparent motion-reduce:shadow-[inset_0_0_0_2px_var(--color-accent)]" />;
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

/** A tiny dot in a tone; used for connection status and unread marks. */
export function Dot({ tone, className, pulse = false }: { tone: Tone; className?: string; pulse?: boolean }) {
  return <span aria-hidden="true" className={cn('inline-block size-2 shrink-0 rounded-full', TONE_BG[tone], pulse && 'animate-pulse-dot', className)} />;
}

export function Spinner({ className }: { className?: string }) {
  return (
    <span
      aria-hidden="true"
      className={cn('inline-block size-3 shrink-0 animate-spin rounded-full border-[1.5px] border-accent border-r-transparent motion-reduce:animate-none', className)}
    />
  );
}

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
    <input
      ref={ref}
      aria-label={label}
      className={cn('h-7 w-full min-w-0 rounded-xs border border-hairline-strong bg-raised px-1.5 text-ink outline-hidden focus:border-accent', className)}
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
 * is read from the DOM, so it is exactly what is shown.
 */
export function CodeBlock({ language, className, children }: { language?: string; className?: string; children: ReactNode }) {
  const pre = useRef<HTMLPreElement>(null);
  const [copied, copy] = useCopied();
  const text = () => pre.current?.textContent ?? '';
  const items: ActionItem[] = [{ key: 'copy', label: 'Copy code', icon: <Copy />, onSelect: () => copy(text()) }];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/code relative my-2.5 overflow-hidden rounded-md border border-hairline bg-code-bg', className)} />}>
        <div className="flex h-6 items-center gap-2 border-b border-hairline/70 px-3 text-code-sm text-muted">
          <span className="font-mono">{language ?? 'code'}</span>
          <span className="flex-1" />
          <Button
            size="icon"
            variant="subtle"
            aria-label={copied ? 'Copied' : 'Copy code'}
            className={cn('size-6 text-muted opacity-0 transition-opacity group-hover/code:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100', copied && 'opacity-100 text-success')}
            onClick={() => copy(text())}
          >
            {copied ? <Check /> : <Copy />}
          </Button>
        </div>
        <pre ref={pre} translate="no" className="!my-0 !rounded-none !border-0 max-h-[480px] overflow-auto px-3 py-2.5 font-mono text-code text-ink">
          {children}
        </pre>
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}

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
  pre({ children }) {
    const child = Array.isArray(children) ? children[0] : children;
    const cls = child && typeof child === 'object' && 'props' in child ? String((child.props as { className?: string }).className ?? '') : '';
    const language = /language-([\w+-]+)/.exec(cls)?.[1];
    return <CodeBlock language={language}>{children}</CodeBlock>;
  },
};

/** Markdown for untrusted provider text: no raw HTML, no images, safe links only. */
export function Markdown({ text, className }: { text: string; className?: string }) {
  return (
    <div className={cn('md', className)}>
      <ReactMarkdown remarkPlugins={remarkPlugins} components={mdComponents} disallowedElements={['img']} unwrapDisallowed>
        {text}
      </ReactMarkdown>
    </div>
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
