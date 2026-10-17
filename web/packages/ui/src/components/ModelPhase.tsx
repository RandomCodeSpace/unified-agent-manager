import { Hand, Sparkle, Terminal, type LucideIcon } from 'lucide-react';
import { createContext, useContext, useEffect, useState } from 'react';
import { modelName, type ModelCalls, type Phase } from '../api';
import { cn } from '../lib/cn';
import { useApp } from './common';
import { Chip } from './ui/chip';
import { Tip } from './ui/tooltip';

/**
 * The service's model calls in flight across its Threads (`model_calls`); null until a snapshot carries them, as from
 * an older service. Its own context, so the counts changing re-render the AI chip alone.
 */
export const ModelCallsContext = createContext<ModelCalls | null>(null);

/** An elapsed time as a clock: "0:42", "12:05", "1:02:09". */
export function phaseClock(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = String(total % 60).padStart(2, '0');
  return h ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}

/** Date.now(), again each second while `on`. */
function useSecondTick(on: boolean): number {
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!on) return;
    // The time now at once, then each second: the clock may have stood still while off.
    const tick = () => setNow(Date.now());
    const first = window.setTimeout(tick, 0);
    const timer = window.setInterval(tick, 1000);
    return () => {
      window.clearTimeout(first);
      window.clearInterval(timer);
    };
  }, [on]);
  return now;
}

const FACES: Record<Phase['kind'], { label: string; Icon: LucideIcon; tone: 'accent' | 'success' | 'attention'; wash: string }> = {
  model: { label: 'Model', Icon: Sparkle, tone: 'accent', wash: 'bg-accent-wash' },
  local: { label: 'Local · no model', Icon: Terminal, tone: 'success', wash: 'bg-success-wash' },
  waiting: { label: 'Waiting on you', Icon: Hand, tone: 'attention', wash: '' },
};

/** The phase's second words: the model called, by its catalog name, the tool and its argument, or what the owner is asked. */
function phaseDetail(phase: Phase, name: (id: string) => string): string {
  if (phase.kind === 'model') return phase.model ? name(phase.model) : '';
  if (phase.kind === 'local') return [phase.tool, phase.detail].filter(Boolean).join(' ');
  return phase.detail ?? '';
}

/**
 * Under the composer, at the right: what the open Thread's turn waits on now, the model, a tool running locally or
 * you, with the detail and a clock since it started. Words carry the state; nothing spins. Nothing while none holds.
 * The detail gives way on a phone.
 */
export function PhasePill({ phase, provider }: Readonly<{ phase: Phase | undefined; provider: string }>) {
  const { meta } = useApp();
  const now = useSecondTick(!!phase);
  if (!phase) return null;
  const face = FACES[phase.kind];
  const detail = phaseDetail(phase, (id) => modelName(meta, provider, id));
  const since = Date.parse(phase.since);
  return (
    <span data-phase={phase.kind} className="ml-auto flex min-w-0 shrink items-center gap-2 text-caption">
      <Chip tone={face.tone} className={cn('rounded-full px-2 font-medium', face.wash)}>
        <face.Icon aria-hidden="true" className="size-3.5" />
        {face.label}
      </Chip>
      {detail && <span className="min-w-0 max-w-80 truncate text-muted max-sm:hidden">{detail}</span>}
      {Number.isFinite(since) && <span className="shrink-0 text-muted tabular-nums">{phaseClock(now - since)}</span>}
      <span aria-hidden="true" className="mx-1 h-4 w-px shrink-0 bg-hairline-strong" />
    </span>
  );
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

/** The AI chip's tip, line by line: whether a model works now, for what, and what runs locally. */
export function modelCallsLines(calls: ModelCalls): string[] {
  const count = calls.threads + calls.subagents + calls.background;
  const lines = [count ? 'Model working now' : 'No model calls now'];
  if (count) {
    const parts = [calls.threads && plural(calls.threads, 'Thread'), calls.subagents && plural(calls.subagents, 'subagent')].filter(Boolean);
    parts.push(calls.background ? `Background AI: ${plural(calls.background, 'call')}` : 'Background AI idle');
    lines.push(parts.join(' · '));
  }
  if (calls.local) lines.push(`Local: ${plural(calls.local, 'Thread')} running a tool`);
  return lines;
}

/**
 * The sidebar foot's AI chip, beside Usage: the model calls in flight across this service's Threads, their
 * subagents and Background AI, the glyph in the accent while any is; muted with no count while none is. `rail` is
 * the collapsed rail's glyph over the count. Pressing it shows the tip, which says what calls and what runs locally.
 */
export function ModelCallsChip({ side = 'top', variant = 'chip', className }: Readonly<{ side?: 'top' | 'right'; variant?: 'chip' | 'rail'; className?: string }>) {
  const calls = useContext(ModelCallsContext);
  if (!calls) return null;
  const count = calls.threads + calls.subagents + calls.background;
  const lines = modelCallsLines(calls);
  const label = <>{lines.map((line, i) => <span key={line} className={cn('block', i > 0 && 'text-on-primary/70')}>{line}</span>)}</>;
  const glyph = <Sparkle aria-hidden="true" className={cn('size-3.5 shrink-0', count ? 'text-accent' : 'text-faint')} />;
  return (
    <Tip openOnClick side={side} label={label}>
      {variant === 'chip' ? (
        <button type="button" data-model-calls={count} aria-label={`AI. ${lines.join('. ')}`} className={cn('lift flex h-7 min-w-0 shrink-0 cursor-default items-center gap-1.5 rounded-full bg-raised px-2.5 text-caption shadow-raised', className)}>
          {glyph}
          <span className={cn('font-medium', count ? 'text-ink' : 'text-muted')}>AI</span>
          {count > 0 && <span className="font-medium text-accent tabular-nums">{count}</span>}
        </button>
      ) : (
        <button type="button" data-model-calls={count} aria-label={`AI. ${lines.join('. ')}`} className={cn('flex h-auto w-10 cursor-default flex-col items-center gap-1 rounded-sm py-1.5 text-caption leading-none', className)}>
          {glyph}
          <span className={cn('font-medium tabular-nums', count ? 'text-accent' : 'text-muted')}>{count || 'AI'}</span>
        </button>
      )}
    </Tip>
  );
}
