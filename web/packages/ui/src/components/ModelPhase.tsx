import { Cpu, Hand, Terminal, type LucideIcon } from 'lucide-react';
import { createContext, useEffect, useState } from 'react';
import type { ModelCalls, Phase } from '../api';
import { Blocks, type BlockTone } from './ui/blocks';

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

/** Each phase's block: its tone, glyph and word, and its words in full for the screen reader and the hover title. */
const FACES: Record<Phase['kind'], { word: string; full: string; Icon: LucideIcon; tone: BlockTone }> = {
  model: { word: 'Model', full: 'Model', Icon: Cpu, tone: 'accent' },
  local: { word: 'Local', full: 'Local · no model', Icon: Terminal, tone: 'success' },
  waiting: { word: 'You', full: 'Waiting on you', Icon: Hand, tone: 'attention' },
};

/** Whose work it is when not the turn's main agent's: a subagent's between turns, or compaction. */
function phaseBy(phase: Phase): string {
  if (phase.kind === 'waiting') return '';
  if (phase.by === 'compaction' && phase.kind === 'model') return 'compacting';
  return phase.by === 'subagent' ? 'subagent' : '';
}

/**
 * The phase's detail beside its block: what the subagent calling the model does, the tool and its argument, or what
 * the owner is asked. Never the model's name: the composer's Model picker shows it.
 */
function phaseDetail(phase: Phase): string {
  if (phase.kind === 'model') return phase.by === 'subagent' ? phase.detail ?? '' : '';
  if (phase.kind === 'local') return [phase.tool, phase.detail].filter(Boolean).join(' ');
  return phase.detail ?? '';
}

/**
 * Under the composer, at the right: what the open Thread's turn waits on now, the model, a tool running locally or
 * you, as one block (glyph, word, a clock since it started) with its detail at its left; between turns, a subagent
 * still running or compaction. Words carry the state; nothing spins. Nothing while none holds.
 * The wrapper is `display: contents`, so the detail is an item of the footer's own row: it truncates first (its
 * shrink outweighs the branch's), and the branch gives way only once the detail is gone. A phone hides the detail and
 * whose work it is, so the branch keeps its room. A hairline parts the block from the time split that may follow it
 * (TimeSplitSummary), only while one does; a phone shows the block alone (Task).
 */
export function PhaseBlock({ phase }: Readonly<{ phase: Phase | undefined }>) {
  const now = useSecondTick(!!phase);
  if (!phase) return null;
  const face = FACES[phase.kind];
  const by = phaseBy(phase);
  const detail = phaseDetail(phase);
  const since = Date.parse(phase.since);
  const clock = Number.isFinite(since) ? phaseClock(now - since) : '';
  const words = by ? `${face.word} · ${by}` : face.full;
  return (
    <span data-phase={phase.kind} className="contents [&:has(+*)_[data-phase-rule]]:block">
      {detail && <span data-phase-detail="" title={detail} className="min-w-0 max-w-90 shrink-1000 truncate text-caption text-muted max-sm:hidden">{detail}</span>}
      <Blocks items={[{
        key: phase.kind,
        tone: face.tone,
        Icon: face.Icon,
        label: <>{face.word}{by && <span className="max-sm:hidden"> · {by}</span>}{clock && ` ${clock}`}</>,
        title: clock ? `${words}, ${clock}` : words,
      }]} />
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
