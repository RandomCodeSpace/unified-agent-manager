import { Hand, Sparkle, Terminal, type LucideIcon } from 'lucide-react';
import { createContext, useEffect, useState } from 'react';
import { modelName, type ModelCalls, type Phase } from '../api';
import { cn } from '../lib/cn';
import { useApp } from './common';
import { Chip } from './ui/chip';

/**
 * The service's model calls in flight across its Threads (`model_calls`); null until a snapshot carries them, as from
 * an older service. Its own context, so the counts changing re-render the sidebar's Usage and AI button alone.
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

/** Each phase's pill: its words, and the short word a phone shows. */
const FACES: Record<Phase['kind'], { label: string; short: string; Icon: LucideIcon; tone: 'accent' | 'success' | 'attention'; wash: string }> = {
  model: { label: 'Model', short: 'Model', Icon: Sparkle, tone: 'accent', wash: 'bg-accent-wash' },
  local: { label: 'Local · no model', short: 'Local', Icon: Terminal, tone: 'success', wash: 'bg-success-wash' },
  waiting: { label: 'Waiting on you', short: 'You', Icon: Hand, tone: 'attention', wash: '' },
};

/** The phase's words: whose work it is when not the turn's main agent's, a subagent's between turns or compaction. */
function phaseLabel(phase: Phase): string {
  const face = FACES[phase.kind];
  if (phase.kind === 'waiting') return face.label;
  if (phase.by === 'compaction' && phase.kind === 'model') return 'Model · compacting';
  if (phase.by === 'subagent') return `${face.short} · subagent`;
  return face.label;
}

/**
 * The phase's second words: the model called, by its catalog name, or the subagent calling it, the tool and its
 * argument, or what the owner is asked.
 */
function phaseDetail(phase: Phase, name: (id: string) => string): string {
  if (phase.kind === 'model') return (phase.by === 'subagent' && phase.detail) || (phase.model ? name(phase.model) : '');
  if (phase.kind === 'local') return [phase.tool, phase.detail].filter(Boolean).join(' ');
  return phase.detail ?? '';
}

/**
 * Under the composer, at the right: what the open Thread's turn waits on now, the model, a tool running locally or
 * you, with the detail and a clock since it started; between turns, a subagent still running or compaction. Words carry the state; nothing spins. Nothing while none holds.
 * A phone shows the glyph, a short word and the clock, so the branch keeps its room. A hairline parts it from the
 * time split that may follow it (TimeSplitSummary), only while one does; a phone shows the pill alone (Task).
 */
export function PhasePill({ phase, provider }: Readonly<{ phase: Phase | undefined; provider: string }>) {
  const { meta } = useApp();
  const now = useSecondTick(!!phase);
  if (!phase) return null;
  const face = FACES[phase.kind];
  const detail = phaseDetail(phase, (id) => modelName(meta, provider, id));
  const since = Date.parse(phase.since);
  return (
    <span data-phase={phase.kind} className="flex min-w-0 shrink items-center gap-2 text-caption [&:has(+*)_[data-phase-rule]]:block">
      <Chip tone={face.tone} className={cn('rounded-full px-2 font-medium max-sm:gap-1 max-sm:px-1.5', face.wash)}>
        <face.Icon aria-hidden="true" className="size-3.5" />
        <span className="max-sm:sr-only">{phaseLabel(phase)}</span>
        <span aria-hidden="true" className="sm:hidden">{face.short}</span>
      </Chip>
      {detail && <span className="min-w-0 max-w-80 truncate text-muted max-sm:hidden">{detail}</span>}
      {Number.isFinite(since) && <span className="shrink-0 text-muted tabular-nums">{phaseClock(now - since)}</span>}
      <span aria-hidden="true" data-phase-rule="" className="mx-1 hidden h-4 w-px shrink-0 bg-hairline-strong max-sm:hidden!" />
    </span>
  );
}

const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

/** The Usage and AI button's words for the work in flight, line by line: whether a model works now, for what, and what runs locally. */
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
