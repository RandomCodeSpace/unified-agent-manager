// The status line (DESIGN.md Status line): one quiet caption line over the composer. While the
// Task works it says what the agent says it is doing (`assistant.intent`, else the turn's verb)
// and for how long, and a model call being retried; after a stop it says why, until the next
// turn. With a todo list the agents keep, it adds how far it got, the row in progress and the
// blocked rows, and opens the list (`TodoReader`). It sits in the dock's overlap, outside the
// scroller, so coming and going never moves the composer or the transcript. It is still: the ring
// stays at the Task's state glyph and the running step. A subagent's retry stands under its row
// instead (`SubagentRetry`).

import { Ban, CircleStop, ListChecks, Minus, Pause, RotateCw } from 'lucide-react';
import { useEffect, useId, useRef, useState } from 'react';
import { LIVE, readOnly, type Item, type Retry, type SessionDetail, type TurnTiming } from '../api';
import { cn } from '../lib/cn';
import { retryWords, stopWords } from '../lib/stop';
import { todoLine, todoSentence, type TodoLine } from '../lib/todos';
import { elapsedSince, turnElapsed } from '../lib/transcript';
import { turnVerb } from '../lib/verbs';
import { Dot } from './common';
import { PHONE, TodoMeter, TodoReader } from './Todos';

/** What the line says. */
export type Line =
  | { kind: 'working'; lead: string; compacting: boolean; retry?: Retry; todo?: TodoLine }
  | { kind: 'stopped'; short: string; long: string; notYours: boolean }
  | { kind: 'paused'; text: string };

/**
 * The line for a Task, or null when it has nothing to say. `working`: a turn runs, or subagents
 * outlived it; the turn's verb comes from the user message its timing links, so a turn starting
 * (the Task works before its message lands) has none until then, never the last turn's.
 */
export function statusLine(session: SessionDetail, working: boolean, compacting: boolean, items: Item[], identityItems?: Item[]): Line | null {
  if (working) {
    const lastUser = (list: Item[] = []) => [...list].reverse().find((item) => item.kind === 'user' && !item.delivery)?.id;
    if (compacting) return { kind: 'working', lead: 'Compacting the conversation', compacting };
    const turn = session.turn_timings?.at(-1);
    // The Task runs a turn whose timing has not come yet: all there is belongs to the last turn.
    if (turn?.ended_at && LIVE.includes(session.state)) return null;
    const activity = session.turn_activity;
    const key = turn ? turn.user_item_id : lastUser(items) ?? lastUser(identityItems) ?? 'start';
    const lead = activity?.intent || (key ? turnVerb(key) : '');
    const todo = todoLine(activity?.todos, turn?.started_at);
    // Copilot derives the intent from the row in progress: "Now:" goes when it would say the lead again.
    return { kind: 'working', lead, compacting, retry: activity?.retry, todo: todo?.kind === 'list' && todo.now === lead ? { ...todo, now: undefined, more: 0 } : todo };
  }
  if (readOnly(session)) return null;
  if (session.state === 'cancelled' && session.stop_reason) {
    const { short, long, notYours } = stopWords(session);
    return { kind: 'stopped', short, long, notYours };
  }
  const objective = session.execution?.objective;
  if (session.execution?.mode === 'autopilot' && objective?.status === 'paused' && objective.pause_reason) return { kind: 'paused', text: `Autopilot paused: ${objective.pause_reason}` };
  return null;
}

const Sep = () => <span className="shrink-0 text-faint">·</span>;

/**
 * The line as one button. Its name is one sentence (sr-only), the words on screen are hidden from
 * assistive tech, and a polite live region beside it speaks only what `useCue` lets through.
 * Pressing it opens the todo list when the line shows one, else shows the end of the
 * conversation, where the work is. `hidden` gives its place to another strip.
 */
export function StatusLine({ line, session, since, hidden, onJump }: Readonly<{
  line: Line | null;
  session: SessionDetail;
  /** When the subagents that outlived the turn started, which the clock then counts from. */
  since?: string;
  hidden: boolean;
  onJump: () => void;
}>) {
  const shown = hidden ? null : line;
  const timing = session.turn_timings?.at(-1);
  const cue = useCue(shown, timing?.id ?? '');
  const list = shown?.kind === 'working' ? shown.todo : undefined;
  const view = session.turn_activity?.todos;
  const readerId = useId();
  const [button, setButton] = useState<HTMLButtonElement | null>(null);
  // Phone or not is chosen as it opens, so a resize does not remount it.
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  // With the list gone from the line (the turn ended, the conversation closed), the reader closes too.
  if (reader?.open && !list) setReader({ ...reader, open: false });
  return (
    <>
      <div role="status" aria-live="polite" aria-atomic="true" className="sr-only">
        {shown ? cue : ''}
      </div>
      {shown && (
        <button
          ref={setButton}
          type="button"
          onClick={list ? () => setReader({ open: true, phone: window.matchMedia(PHONE).matches }) : onJump}
          aria-haspopup={list ? 'dialog' : undefined}
          aria-expanded={list ? !!reader?.open : undefined}
          aria-controls={list && reader?.open ? readerId : undefined}
          className="flex h-8 max-w-full min-w-0 items-center rounded-sm bg-canvas px-2 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover pointer-coarse:h-11"
        >
          <span key={shown.kind} aria-hidden="true" className="flex min-w-0 animate-fade-in items-center gap-1.5 sm:gap-2">
            {shown.kind === 'working' ? <Working line={shown} since={since} timing={timing} /> : shown.kind === 'stopped' ? <Stopped line={shown} /> : <Paused text={shown.text} />}
          </span>
          <span className="sr-only">
            {shown.kind === 'working' ? (
              <>
                {shown.lead || 'Working'}
                <ClockWords since={since} timing={timing} />
                {shown.todo && todoSentence(shown.todo)}
                {shown.retry && retrySentence(shown.retry)}
              </>
            ) : shown.kind === 'stopped' ? (
              shown.long.replaceAll(' · ', ', ')
            ) : (
              shown.text
            )}
            {list ? '. Show the list' : '. Jump to bottom'}
          </span>
        </button>
      )}
      {reader && view && (
        <TodoReader id={readerId} open={reader.open} phone={reader.phone} anchor={button} view={view} subagents={session.subagents} onClose={() => setReader((r) => r && { ...r, open: false })} onClosed={() => setReader(null)} />
      )}
    </>
  );
}

function retrySentence(retry: Retry): string {
  const { lead, parts } = retryWords(retry);
  return `. ${lead}${parts.length ? `: ${parts.join(', ')}` : ''}`;
}

function Working({ line, since, timing }: Readonly<{ line: Extract<Line, { kind: 'working' }>; since?: string; timing?: TurnTiming }>) {
  const retry = line.retry && retryWords(line.retry);
  return (
    <>
      {/* Still: this line says what, the ring is elsewhere. */}
      <Dot tone="accent" className={cn('size-1.5', line.compacting && 'bg-badge-violet')} />
      {line.lead && <span className={cn('min-w-0 truncate text-body', line.compacting && 'text-badge-violet')}>{line.lead}…</span>}
      <Clock since={since} timing={timing} />
      {line.todo && <TodoSegment todo={line.todo} />}
      {retry && (
        <>
          <Sep />
          <RotateCw className="size-3 shrink-0 text-muted" />
          <span className="shrink-0 text-body">{retry.lead}</span>
          {retry.parts.length > 0 && <span className="min-w-0 shrink-[2] truncate max-sm:hidden">· {retry.parts.join(' · ')}</span>}
        </>
      )}
    </>
  );
}

/** "Todo 2/7", the meter, "Now: …" with "+2" when more rows are in progress, and "⊘ 1 blocked"; or the last turn's open rows. A phone keeps the counts and the blocked rows. */
function TodoSegment({ todo }: Readonly<{ todo: TodoLine }>) {
  if (todo.kind === 'carried') {
    return (
      <>
        <Sep />
        <ListChecks className="size-3.5 shrink-0 text-muted" />
        <span className="shrink-0 text-body max-sm:hidden">
          Todo <span className="text-faint">·</span>
        </span>
        <span className="min-w-0 truncate text-body">{todo.open} open from the last turn</span>
      </>
    );
  }
  return (
    <>
      <Sep />
      <ListChecks className="size-3.5 shrink-0 text-muted" />
      <span className="shrink-0 tabular-nums text-body">
        <span className="max-sm:hidden">Todo </span>
        {todo.done}/{todo.total}
      </span>
      {todo.meter && <TodoMeter statuses={todo.meter} className="max-sm:hidden" />}
      {todo.now && (
        // The row in progress gives way before the lead does.
        <span className="min-w-0 shrink-[4] truncate text-body max-sm:hidden">
          <span className="text-muted">Now:</span> {todo.now}
        </span>
      )}
      {todo.now && todo.more > 0 && <span className="shrink-0 tabular-nums text-muted max-sm:hidden">+{todo.more}</span>}
      {todo.blocked > 0 && (
        <>
          <Sep />
          <span className="flex shrink-0 items-center gap-1 text-warning">
            <Ban className="size-3" />
            {todo.blocked} blocked
          </span>
        </>
      )}
    </>
  );
}

function Stopped({ line }: Readonly<{ line: Extract<Line, { kind: 'stopped' }> }>) {
  // A credit limit's numbers follow its words in the quieter tone.
  const [head, ...rest] = line.long.split(' · ');
  const tone = line.notYours ? 'font-medium text-warning' : 'text-body';
  return (
    <>
      {line.notYours ? <CircleStop className="size-3.5 shrink-0 text-warning" /> : <Minus className="size-3.5 shrink-0 text-muted" strokeWidth={2.5} />}
      <span className={cn('min-w-0 truncate sm:hidden', tone)}>{line.short}</span>
      <span className="min-w-0 truncate max-sm:hidden">
        <span className={tone}>{head}</span>
        {rest.length > 0 && ` · ${rest.join(' · ')}`}
      </span>
    </>
  );
}

function Paused({ text }: Readonly<{ text: string }>) {
  return (
    <>
      <Pause className="size-3 shrink-0 text-warning" strokeWidth={2.5} fill="currentColor" />
      <span className="min-w-0 truncate text-body">{text}</span>
    </>
  );
}

/** A running subagent's model call being retried, under its row: "Retrying, attempt 2 · HTTP 429 · rate limited". It leaves once the call goes through. */
export function SubagentRetry({ retry, indent }: Readonly<{ retry: Retry; /** Where the row's name starts, so the line sits under it. */ indent: number }>) {
  const { lead, parts } = retryWords(retry);
  const text = [lead, ...parts].join(' · ');
  return (
    <p className="flex min-w-0 items-center gap-1.5 pr-2 pb-1 text-caption text-muted" style={{ paddingLeft: `${indent}px` }}>
      <RotateCw aria-hidden="true" className="size-3 shrink-0" />
      <span className="min-w-0 truncate" title={text}>
        {text}
      </span>
    </p>
  );
}

/**
 * The turn's foreground time, waits for approvals and answers excluded; `since` times work that
 * outlived the turn. Ticks each second while it counts.
 */
function useElapsed(since: string | undefined, timing: TurnTiming | undefined): string | null {
  const [now, setNow] = useState(() => Date.now());
  const ticking = !!since || !timing?.paused_at;
  const [wasTicking, setWasTicking] = useState(ticking);
  if (ticking !== wasTicking) {
    setWasTicking(ticking);
    if (ticking) setNow(() => Date.now());
  }
  useEffect(() => {
    if (!ticking) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [ticking]);
  return since ? elapsedSince(since, now) : timing?.state === 'working' ? turnElapsed(timing, now) : null;
}

/** The clock's digits: only they re-render each second. Under an hour they are at most three characters wide, and the slot holds them all, so the line keeps its width. */
function Clock({ since, timing }: Readonly<{ since?: string; timing?: TurnTiming }>) {
  const elapsed = useElapsed(since, timing);
  return elapsed && <span className="min-w-[3ch] shrink-0 text-right tabular-nums text-faint">{elapsed}</span>;
}

/** The clock as the button's name says it, by the minute. */
function ClockWords({ since, timing }: Readonly<{ since?: string; timing?: TurnTiming }>) {
  const elapsed = useElapsed(since, timing);
  return elapsed && `, ${elapsedWords(elapsed)}`;
}

/** "5s", "3m" or "1h 5m" as a screen reader hears it, by the minute: "under a minute", "3 minutes". */
function elapsedWords(elapsed: string): string {
  const unit = (n: string | undefined, word: string) => (n && n !== '0' ? `${n} ${word}${n === '1' ? '' : 's'}` : '');
  const words = [unit(/(\d+)h/.exec(elapsed)?.[1], 'hour'), unit(/(\d+)m/.exec(elapsed)?.[1], 'minute')].filter(Boolean).join(' ');
  return words || 'under a minute';
}

/** At most one announcement this often; the latest waits for the slot. */
const CUE_MS = 5000;

function cueOf(line: Line | null, turnId: string): [key: string, words: string] {
  if (line?.kind === 'working') {
    if (line.compacting) return [`compacting ${turnId}`, 'Compacting the conversation'];
    if (line.retry) return [`retry ${line.retry.count} ${line.retry.at}`, retryWords(line.retry).cue];
    if (line.todo?.kind === 'list' && line.todo.cue) return [`${line.todo.cue.key} ${turnId}`, line.todo.cue.words];
    return [`turn ${turnId}`, 'Working'];
  }
  if (line?.kind === 'stopped') return [`stop ${turnId}`, line.short];
  return ['', ''];
}

/**
 * What the live region says: a turn starting, compaction, a retry of the main agent's call, a todo
 * row done ("3 of 7 done") or blocked ("Blocked: …"), and a stop, each once, at most one every 5s.
 * Never the clock or the intent, and nothing for what was already on screen when the line mounted.
 */
function useCue(line: Line | null, turnId: string): string {
  const [key, words] = cueOf(line, turnId);
  const spoken = useRef<Set<string> | null>(null);
  if (spoken.current === null) spoken.current = new Set([key]);
  const last = useRef(0);
  const [said, setSaid] = useState({ key, text: '' });
  useEffect(() => {
    const seen = spoken.current!;
    if (!key) {
      seen.clear();
      return;
    }
    if (seen.has(key)) return;
    const timer = window.setTimeout(() => {
      seen.add(key);
      last.current = Date.now();
      setSaid({ key, text: words });
    }, Math.max(0, last.current + CUE_MS - Date.now()));
    return () => window.clearTimeout(timer);
  }, [key, words]);
  // Once the line moved on the region empties, unspoken, so the next words are a change.
  return said.key === key ? said.text : '';
}
