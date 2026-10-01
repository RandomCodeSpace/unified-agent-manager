// PROTOTYPE (throwaway): small visual atoms the redesign variants share. Layout is never shared.

import { Fragment, type ReactNode } from 'react';
import { CheckCircle2, FileSearch, FilePen, Search, SquareTerminal, Sparkles, LoaderCircle } from 'lucide-react';
import type { Badge, Item, SessionState } from '../../api';
import { stateTone } from './data';
import { cn } from '../../lib/cn';

const BADGE: Record<string, string> = {
  red: 'bg-badge-red', orange: 'bg-badge-orange', amber: 'bg-badge-amber', lime: 'bg-badge-lime', green: 'bg-badge-green',
  teal: 'bg-badge-teal', cyan: 'bg-badge-cyan', blue: 'bg-badge-blue', violet: 'bg-badge-violet', pink: 'bg-badge-pink',
};

export function ProjectBadge({ badge, size = 18 }: { badge: Badge; size?: number }) {
  return (
    <span
      className={cn('inline-grid shrink-0 place-items-center rounded-xs font-semibold text-on-primary', BADGE[badge.color] ?? 'bg-badge-blue')}
      style={{ width: size, height: size, fontSize: Math.round(size * 0.42) }}
    >
      {badge.text}
    </span>
  );
}

export function StateDot({ state, className }: { state: SessionState; className?: string }) {
  const tone = stateTone(state);
  if (tone === 'live') return <LoaderCircle aria-hidden className={cn('size-3.5 animate-spin text-accent motion-reduce:animate-none', className)} />;
  return (
    <span
      aria-hidden
      className={cn(
        'inline-block size-2 shrink-0 rounded-full',
        tone === 'attention' && 'bg-attention',
        tone === 'ok' && 'bg-success',
        tone === 'bad' && 'bg-error',
        tone === 'quiet' && 'bg-faint',
        className,
      )}
    />
  );
}

export const toneText = (state: SessionState) =>
  ({ live: 'text-accent', attention: 'text-attention', ok: 'text-success', bad: 'text-error', quiet: 'text-muted' })[stateTone(state)];

/** Backtick code spans and paragraphs only; enough to judge layout. */
export function Prose({ text, className }: { text: string; className?: string }) {
  const paras = text.split(/\n{2,}/).filter((p) => !p.startsWith('!['));
  return (
    <div className={cn('space-y-3', className)}>
      {paras.map((p, n) => (
        <p key={n}>
          {p.split(/(`[^`]+`)/).map((part, k) =>
            part.startsWith('`') ? (
              <code key={k} className="rounded-xs bg-code-bg px-1 py-px font-mono text-[0.88em] text-ink">
                {part.slice(1, -1)}
              </code>
            ) : (
              <Fragment key={k}>{part.replace(/\[([^\]]+)\]\([^)]+\)/g, '$1')}</Fragment>
            ),
          )}
        </p>
      ))}
    </div>
  );
}

const TOOL_ICON: Record<string, typeof Search> = { grep: Search, view: FileSearch, edit: FilePen, bash: SquareTerminal };

export function ToolLine({ item, className }: { item: Item; className?: string }) {
  const t = item.tool!;
  const Icon = TOOL_ICON[t.name] ?? Sparkles;
  const running = t.status === 'running';
  return (
    <div className={cn('flex items-center gap-2 text-caption text-muted', className)}>
      <Icon aria-hidden className="size-3.5 text-faint" />
      <span className="truncate">{t.title ?? t.output ?? t.name}</span>
      {running ? <LoaderCircle aria-hidden className="size-3 animate-spin text-accent motion-reduce:animate-none" /> : <CheckCircle2 aria-hidden className="size-3 text-success" />}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="rounded-xs border border-hairline bg-raised px-1.5 py-0.5 font-sans text-keycap text-muted">{children}</kbd>;
}
