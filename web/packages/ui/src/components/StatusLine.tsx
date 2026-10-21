// The status strip (DESIGN.md Status line): one quiet line over the composer. While the Thread
// works it gives the todo list as stage blocks (✓ done, ◉ in progress, ⊘ blocked, ○ to do), then
// the live line, what happens now ("Thinking · 12s", "Running go test … · 41s", "Needs you · allow
// …"), the one place that says so; with nothing live, "Now:" and the row the work is at (without a
// list, what the agent says it is doing, `assistant.intent`), for how long, a model call being retried and automatic compaction running
// beside the turn ("Compacting in background", words only); after a stop it says why, until the
// next turn. Its right end holds the Thread's subagents as blocks (✗ failed, ● running, ✓ done,
// ○ stopped), there whenever the Thread has any. Pressing it opens one fixed sheet off the strip's
// right end with a Todo and a Subagents tab (a bottom sheet on a phone), the one home of subagents.
// It sits in the dock's overlap, outside the scroller, so coming and going never moves the composer
// or the transcript. The live line's mark is the pane's one animated element; everything else on
// the strip is still. A subagent's retry stands under its row instead
// (`SubagentRetry`).

import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Tabs } from '@base-ui/react/tabs';
import { Ban, Check, Circle, CircleDot, CircleSmall, CircleStop, Clock as ClockIcon, ListChecks, Minus, Pause, RotateCw, ShieldQuestionMark, Workflow, X, type LucideProps } from 'lucide-react';
import { forwardRef, useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from 'react';
import { ATTENTION, LIVE, readOnly, type Retry, type SessionDetail, type Subagent, type TodoView } from '../api';
import { cn } from '../lib/cn';
import { retryWords, stopWords } from '../lib/stop';
import { countSubagents } from '../lib/subagents';
import { todoLine, todoSentence, type TodoLine, type TodoTally } from '../lib/todos';
import { elapsedSince, isSubagentCall, toolKind } from '../lib/transcript';
import { BreathingDot, WorkingMark } from './common';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow, SubagentSheetBody, useSheetRequest, useStopConfirming, useSubagentIndex, type SheetTo } from './Subagents';
import { PHONE, TodoTab } from './Todos';
import { Blocks, type Block } from './ui/blocks';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead } from './ui/panel';
import { HelpTip } from './ui/tooltip';

/** What the line says. */
export type Line =
  | { kind: 'working'; lead: string; compacting: boolean; retry?: Retry; todo?: TodoLine; plan?: boolean; /** Automatic compaction runs beside the turn. */ background?: boolean; /** What happens now, in place of the slot's "Now:" (`liveLine`). */ live?: Live | null }
  | { kind: 'stopped'; short: string; long: string; notYours: boolean }
  | { kind: 'paused'; text: string };

/**
 * The line for a Thread, or null when it has nothing to say. `working`: a turn runs, or subagents
 * outlived it; `lead` is what the agent says it is doing, or "" (never a made-up verb).
 * `connected`: the event stream is up, so a timing marked unknown is the service's (a turn a
 * restart or close cut off), not the running one this page lost track of while it reconnects.
 */
export function statusLine(session: SessionDetail, working: boolean, compacting: boolean, connected = true): Line | null {
  // A turn waiting on you still runs: the live line says "Needs you" where the work was.
  if (working || (ATTENTION.includes(session.state) && session.turn_activity?.phase?.kind === 'waiting')) {
    if (compacting) return { kind: 'working', lead: 'Compacting the conversation', compacting, live: liveLine(session, compacting) };
    const turn = session.turn_timings?.at(-1);
    // The Thread runs a turn whose timing has not come yet: all there is belongs to the last turn, which ended or was cut off.
    if ((turn?.ended_at || (connected && turn?.state === 'unknown')) && LIVE.includes(session.state)) return null;
    const activity = session.turn_activity;
    const todo = todoLine(activity?.todos, turn?.started_at);
    return { kind: 'working', lead: activity?.intent ?? '', compacting, retry: activity?.retry, ...(activity?.plan && todo?.kind === 'list' ? { plan: true } : {}), ...(session.compacting_background ? { background: true } : {}), todo, live: liveLine(session, compacting) };
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

/**
 * The live line's mark: model work breathes (`dot`), local work turns (`ring`), the model's first token still on its
 * way breathes until WAIT_STILL_MS, then is a still clock (`wait`); you (`you`) and subagents (`subagents`) are still.
 */
export type LiveMark = 'dot' | 'ring' | 'wait' | 'you' | 'subagents';

/** What the turn does now, as the strip's one live line: [mark] phase · detail · elapsed (since `since`), then a running command's newest output line (`tail`). */
export interface Live { mark: LiveMark; phase: string; detail?: string; since?: string; tail?: string }

/** A model call silent this long reads as a wait, not work: its mark stops. */
export const WAIT_STILL_MS = 60_000;

/** How long the last live line stays once nothing is live while the turn still works. */
export const LIVE_HOLD_MS = 1500;

const READS = new Set(['view', 'read', 'list']);
const plural = (n: number, word: string) => `${n} ${word}${n === 1 ? '' : 's'}`;

/**
 * The live line from what the service says the turn waits on (`turn_activity.phase`) and the main agent's newest
 * items: compaction, you, subagents with the main agent idle, a local tool, the model thinking, writing or not
 * answering yet. Null when none holds, so the slot's "Now:" (or the intent) comes back.
 */
export function liveLine(session: SessionDetail, compacting: boolean): Live | null {
  const phase = session.turn_activity?.phase;
  if (compacting || phase?.by === 'compaction') return { mark: 'dot', phase: 'Compacting the conversation', since: phase?.by === 'compaction' ? phase.since : undefined };
  if (phase?.kind === 'waiting') {
    const ask = session.interactions.filter((i) => i.state === 'pending' && !i.auto).sort((a, b) => Date.parse(a.time) - Date.parse(b.time))[0];
    const detail = ask?.kind === 'permission' && ask.detail ? `allow ${ask.detail.split('\n')[0]}` : phase.detail;
    return { mark: 'you', phase: 'Needs you', detail, since: phase.since };
  }
  const running = session.subagents.filter((s) => s.status === 'running').length;
  if (running && phase && (phase.by === 'subagent' || (!!phase.calls && phase.calls === phase.subagents))) return { mark: 'subagents', phase: `Waiting on ${plural(running, 'subagent')}` };
  if (!phase) return null;
  const items = (session.recent_items ?? session.items).filter((i) => !i.agent_id);
  if (phase.kind === 'local') {
    const tool = phase.tool ?? '';
    const calls = items.filter((i) => i.kind === 'tool' && (i.tool?.status === 'running' || i.tool?.status === 'pending') && !isSubagentCall(i));
    // A plain local phase may be a subagent's tool mid-turn: with no call of the main agent's running, it waits on them.
    if (running && !calls.length) return { mark: 'subagents', phase: `Waiting on ${plural(running, 'subagent')}` };
    const reads = calls.filter((i) => READS.has(i.tool?.name.toLowerCase() ?? '')).length;
    const kind = toolKind(tool);
    const arg = phase.detail || tool;
    const words = READS.has(tool.toLowerCase()) ? (reads > 1 ? `Reading ${plural(reads, 'file')}` : `Reading ${arg}`)
      : kind === 'file' ? `Editing ${arg}`
      : kind === 'search' ? `Searching ${arg}`
      : kind === 'command' ? `Running ${arg}`
      : `Running ${[tool, phase.detail].filter(Boolean).join(' ')}`;
    // The phase names the oldest running tool, so the output line is the oldest running command's.
    const tail = kind === 'command' ? calls.find((i) => toolKind(i.tool?.name ?? '') === 'command')?.tool?.tail?.filter((l) => l.text.trim()).at(-1)?.text.trim() : undefined;
    return { mark: 'ring', phase: words, since: phase.since, ...(tail ? { tail } : {}) };
  }
  // The model: what its newest item since the call began is says whether it thinks or writes; nothing yet is a wait.
  const last = items.at(-1);
  const fresh = last && !(Date.parse(last.time) < Date.parse(phase.since)) ? last.kind : undefined;
  if (fresh === 'reasoning') return { mark: 'dot', phase: 'Thinking', since: phase.since };
  if (fresh === 'assistant') {
    const t = session.turn_timings?.at(-1);
    const rate = t?.output_tokens && t.generation_ms ? Math.round(t.output_tokens / (t.generation_ms / 1000)) : 0;
    return { mark: 'dot', phase: 'Writing the reply', ...(rate > 0 ? { detail: `${rate} tok/s` } : {}) };
  }
  return { mark: 'wait', phase: 'Waiting for the model', since: phase.since };
}

/** "41s", "1m 04s", "1h 02m": the live line's elapsed time, to the second under an hour. */
export function liveClock(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, '0')}s`;
  return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, '0')}m`;
}

/** A running subagent's block glyph: a small filled dot, still (the ring stays at the running step). */
const RunningDot = forwardRef<SVGSVGElement, Omit<LucideProps, 'ref'>>(function RunningDot(props, ref) {
  return <CircleSmall ref={ref} {...props} fill="currentColor" />;
});

/** The todo list's stages as blocks, those with rows only: ✓ done, ◉ in progress, ⊘ blocked, ○ to do (and "3 open" for counts kept before the split). */
export function todoBlocks(tally: TodoTally): Block[] {
  const n = (status: string) => tally.stages.find((s) => s.status === status)?.n ?? 0;
  const blocks: Block[] = [
    { key: 'done', tone: 'success', Icon: Check, label: tally.done, title: `${tally.done} done` },
    { key: 'in_progress', tone: 'accent', Icon: CircleDot, label: n('in_progress'), title: `${n('in_progress')} in progress` },
    { key: 'blocked', tone: 'warning', Icon: Ban, label: n('blocked'), title: `${n('blocked')} blocked` },
    { key: 'pending', tone: 'none', Icon: Circle, label: n('pending'), title: `${n('pending')} to do` },
    { key: 'open', tone: 'none', label: `${tally.open} open`, title: `${tally.open} open` },
  ];
  return blocks.filter((b) => (b.key === 'open' ? tally.open : b.label));
}

/** The subagents' states as blocks, those with any only: ✗ failed, ● running, ✓ done, ○ stopped. */
export function subagentBlocks(subagents: readonly Subagent[]): Block[] {
  const c = countSubagents(subagents);
  const blocks: Block[] = [
    { key: 'failed', tone: 'error', Icon: X, label: c.failed, title: `${c.failed} failed` },
    { key: 'running', tone: 'accent', Icon: RunningDot, label: c.running, title: `${c.running} running` },
    { key: 'done', tone: 'success', Icon: Check, label: c.done, title: `${c.done} done` },
    { key: 'stopped', tone: 'none', Icon: Circle, label: c.stopped, title: `${c.stopped} stopped` },
  ];
  return blocks.filter((b) => b.label);
}

/** "Subagents: 5, 1 failed, 3 running, 1 done" for the strip's name; "or more" while older ones wait in the record. */
function subagentSentence(subagents: readonly Subagent[], before?: string): string {
  return `Subagents: ${subagents.length}${before ? ' or more' : ''}${subagentBlocks(subagents).map((b) => `, ${b.title}`).join('')}`;
}

const Sep = () => <span className="shrink-0 text-faint">·</span>;

type Tab = 'todo' | 'subagents';

/**
 * The strip as two buttons on one row: the line, and the subagents at its end. Each one's name is a
 * sentence (sr-only), the words on screen are hidden from assistive tech, and a polite live region
 * beside them speaks only what `useCue` lets through. The line opens the sheet on its Todo tab, or
 * without a list shows the end of the conversation, where the work is; the subagents open it on
 * their tab, and so does a subagent row or chip in the conversation (`useSheetRequest`), on that
 * subagent or that reply's list. Each lights and lifts alone, so the subagents never light the line.
 * `hidden` gives its place to another strip (the sheet then opens from the bottom). `error`: why the
 * last jump to a subagent did not land, said in the Subagents tab.
 */
export function StatusLine({ line: given, session, since, hidden, error, onJump }: Readonly<{
  line: Line | null;
  session: SessionDetail;
  /** When the subagents that outlived the turn started, which the clock then counts from. */
  since?: string;
  hidden: boolean;
  error?: string;
  onJump: () => void;
}>) {
  // A working line with nothing of its own (no list, no stated intent, no compaction, no retry) would only
  // repeat the turn card's head, "Working 18s": the strip says nothing then. Subagents that outlived the
  // turn (`since`) are another matter: the head says "Took …", so the strip counts their time.
  // Between a tool's end and the next model call nothing is live for a moment: the last live line holds for LIVE_HOLD_MS, so the strip does not blink.
  const held = useHeld(given?.kind === 'working' ? given.live : null, given?.kind === 'working', LIVE_HOLD_MS);
  const line = given?.kind === 'working' && !given.live && held ? { ...given, live: held } : given;
  const bare = line?.kind === 'working' && !since && !line.todo && !line.lead && !line.compacting && !line.retry && !line.background && !line.live;
  const shownLine = hidden || bare ? null : line;
  const index = useSubagentIndex();
  const any = !!index?.subagents.length;
  const subagents = hidden || !any ? undefined : index!.subagents;
  const timing = session.turn_timings?.at(-1);
  // The live region still says a turn started when the strip draws nothing for it.
  const spoken = hidden ? null : line;
  const cue = useCue(spoken, timing?.id ?? '');
  const list = shownLine?.kind === 'working' ? shownLine.todo : undefined;
  const view = list ? session.turn_activity?.todos : undefined;
  const tabs: Tab[] = [...(view ? ['todo' as const] : []), ...(any ? ['subagents' as const] : [])];
  const readerId = useId();
  const row = useRef<HTMLDivElement>(null);
  // Phone or not is chosen as it opens, so a resize does not remount it. `opener` takes the focus back,
  // `lift` is the strip button drawn above the dim, `to` where the Subagents tab opens; `n` counts the
  // openings, so a new one starts the tab afresh; `leaving`: it closes for a jump into the conversation.
  const [reader, setReader] = useState<{ open: boolean; phone: boolean; tab: Tab; opener: HTMLElement | null; lift: HTMLElement | null; to: SheetTo | null; n: number; leaving: boolean } | null>(null);
  const confirming = useStopConfirming();
  // With nothing left to show (the turn ended, the conversation closed), the reader goes too.
  if (reader && !tabs.length) setReader(null);
  const tab = reader && tabs.includes(reader.tab) ? reader.tab : tabs[0];
  const sheetMode = () => window.matchMedia(PHONE).matches || hidden;
  const show = (on: Tab, opener: HTMLElement) => setReader({ open: true, phone: sheetMode(), tab: on, opener, lift: opener, to: null, n: (reader?.n ?? 0) + 1, leaving: false });
  // A subagent row or chip in the conversation opens the same sheet, on the subagent or the reply's list; a jump closes it.
  const request = useSheetRequest();
  const [seen, setSeen] = useState(request?.n);
  if (request && request.n !== seen) {
    setSeen(request.n);
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    if (request.to) setReader({ open: true, phone: sheetMode(), tab: 'subagents', opener, lift: document.getElementById('subagents-link'), to: request.to, n: (reader?.n ?? 0) + 1, leaving: false });
    else if (reader?.open) setReader({ ...reader, open: false, leaving: true });
  }
  // A stop's confirmation sits outside the sheet: a press there is not a press away from it.
  const close = () => {
    if (!confirming) setReader((r) => r && { ...r, open: false });
  };
  const closed = () => setReader(null);
  const plan = shownLine?.kind === 'working' && shownLine.plan;
  const listed = tabs.includes('todo');
  const strip = 'flex h-8 items-center rounded-sm px-2 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover aria-expanded:bg-tint-hover pointer-coarse:h-11';
  return (
    <>
      <div role="status" aria-live="polite" aria-atomic="true" className="sr-only">
        {spoken ? cue : ''}
      </div>
      {(shownLine || subagents) && (
        // Two buttons on one row: the line (its todo list, or the jump to the end) and the subagents, each lit and lifted on its own.
        <div ref={row} className="flex h-8 min-w-0 flex-1 items-center gap-1 bg-canvas pointer-coarse:h-11">
          {shownLine ? (
            <button
              type="button"
              onClick={(e) => (listed ? show('todo', e.currentTarget) : onJump())}
              aria-haspopup={listed ? 'dialog' : undefined}
              aria-expanded={listed ? reader?.open === true && reader.tab === 'todo' : undefined}
              aria-controls={listed && reader?.open && reader.tab === 'todo' ? readerId : undefined}
              className={cn(strip, 'min-w-0 flex-1 gap-2.5')}
            >
              <span key={shownLine.kind} aria-hidden="true" className="flex min-w-0 animate-fade-in items-center gap-2 sm:gap-2.5">
                {shownLine.kind === 'working' ? <Working line={shownLine} since={since} /> : shownLine.kind === 'stopped' ? <Stopped line={shownLine} /> : <Paused text={shownLine.text} />}
              </span>
              <span className="sr-only">
                {shownLine.kind === 'working' ? (
                  <>
                    {slotWords(shownLine, !!since)}
                    <ClockWords since={shownLine.live?.since ?? since} />
                    {shownLine.todo && todoWords(shownLine.todo, shownLine.plan)}
                    {shownLine.retry && retrySentence(shownLine.retry)}
                    {shownLine.background && '. Compacting in background'}
                    {'. '}
                  </>
                ) : shownLine.kind === 'stopped' ? `${shownLine.long.replaceAll(' · ', ', ')}. ` : `${shownLine.text}. `}
                {listed ? 'Show the list' : 'Jump to bottom'}
              </span>
            </button>
          ) : (
            <span className="flex-1" />
          )}
          {subagents && (
            <button
              id="subagents-link"
              type="button"
              onClick={(e) => show('subagents', e.currentTarget)}
              aria-haspopup="dialog"
              aria-expanded={reader?.open === true && reader.tab === 'subagents'}
              aria-controls={reader?.open && reader.tab === 'subagents' ? readerId : undefined}
              className={cn(strip, 'shrink-0 gap-2')}
            >
              <Workflow aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
              <Blocks aria-hidden="true" items={subagentBlocks(subagents)} />
              <span className="sr-only">{`${subagentSentence(subagents, index?.before)}. Show the subagents`}</span>
            </button>
          )}
        </div>
      )}
      {reader && tab && (
        <StripReader id={readerId} open={reader.open} phone={reader.phone} row={row} lift={reader.lift} back={reader.leaving ? null : reader.opener} tabs={tabs} tab={tab} onTab={(t) => setReader((r) => r && { ...r, tab: t })} todoLabel={plan ? 'Plan' : 'Todo'} todo={view && list ? { view, blocks: todoBlocks(list) } : undefined} subagents={session.subagents} body={<SubagentSheetBody key={reader.n} to={reader.to} error={error} phone={reader.phone} />} onClose={close} onClosed={closed} />
      )}
    </>
  );
}

const TAB_ICON: Record<Tab, typeof ListChecks> = { todo: ListChecks, subagents: Workflow };

/** Where the sheet hangs: the strip's own right end (inside its padding), not the subagents button, which "Jump to bottom" pushes aside. */
function stripEnd(row: RefObject<HTMLDivElement | null>) {
  return () => {
    const strip = row.current?.parentElement;
    if (!strip) return document.getElementById('subagents-link');
    return {
      getBoundingClientRect: () => {
        const r = strip.getBoundingClientRect();
        return DOMRect.fromRect({ x: r.x, y: r.y, width: r.width - Number.parseFloat(getComputedStyle(strip).paddingRight), height: r.height });
      },
    };
  };
}

/**
 * The strip's sheet (DESIGN.md Status line, sheet): one box of fixed place and size hung off the
 * strip's right end, its right edge flush with it, 10px above it and 560px wide (the strip button
 * that opened it lifted above a 160ms dim), or on a phone (`phone`, chosen as it opened) a bottom
 * sheet of 70% of the screen, with a tab per thing the strip counts: Todo (or Plan), the list in
 * plan order, and Subagents (`body`, list and detail in turn). It opens with focus on the chosen tab
 * (on a subagent's detail, its way back to the list); ← and → switch tabs from anywhere but a text
 * field; Esc, the close button or a press outside close it and give focus back to `back`.
 */
function StripReader({ id, open, phone, row, lift, back, tabs, tab, onTab, todoLabel, todo, subagents, body, onClose, onClosed }: Readonly<{
  id: string;
  open: boolean;
  phone: boolean;
  /** The strip's row: the sheet hangs off the strip's right end, wherever the subagents button sits. */
  row: RefObject<HTMLDivElement | null>;
  lift: HTMLElement | null;
  back: HTMLElement | null;
  tabs: Tab[];
  tab: Tab;
  onTab: (tab: Tab) => void;
  todoLabel: 'Todo' | 'Plan';
  todo?: { view: TodoView; blocks: Block[] };
  subagents: readonly Subagent[];
  body: ReactNode;
  onClose: () => void;
  onClosed: () => void;
}>) {
  const activeTab = useRef<HTMLButtonElement>(null);
  const names: Record<Tab, string> = { todo: todoLabel, subagents: 'Subagents' };
  const label = tabs.map((t, i) => (i ? names[t].toLowerCase() : names[t])).join(' and ');
  const finalFocus = () => (back?.isConnected ? back : false);
  const initialFocus = () => document.getElementById(id)?.querySelector<HTMLElement>('[data-sheet-focus]') ?? activeTab.current;
  const onArrows = (e: ReactKeyboardEvent) => {
    if (e.defaultPrevented || (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') || tabs.length < 2 || e.altKey || e.ctrlKey || e.metaKey) return;
    const target = e.target as HTMLElement;
    // The tabs move themselves, and a text field keeps its caret.
    if (target.closest('[role="tablist"], input, textarea, [contenteditable="true"]')) return;
    e.preventDefault();
    const next = tabs[(tabs.indexOf(tab) + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length];
    onTab(next);
    requestAnimationFrame(() => activeTab.current?.focus());
  };
  const content = (closeButton: ReactNode) => (
    <Tabs.Root value={tab} onValueChange={(v) => onTab(v as Tab)} className="flex min-h-0 flex-1 flex-col">
      <PanelHead className="pt-2 pr-2 pb-1.5 pl-2.5">
        <div className="flex min-h-7 min-w-0 items-center gap-1">
          <Tabs.List activateOnFocus loopFocus aria-label="Show" className="flex min-w-0 flex-1 gap-1">
            {tabs.map((t) => {
              const Icon = TAB_ICON[t];
              return (
                <Tabs.Tab key={t} value={t} ref={t === tab ? activeTab : undefined} className="flex h-7 items-center gap-1.5 rounded-sm px-2.5 text-ui font-medium whitespace-nowrap text-muted outline-hidden hover:text-ink focus-visible:shadow-focus data-active:bg-tint-selected data-active:text-ink pointer-coarse:h-9">
                  <Icon aria-hidden="true" className="size-3.5 shrink-0" />
                  {names[t]}
                </Tabs.Tab>
              );
            })}
          </Tabs.List>
          {closeButton}
        </div>
      </PanelHead>
      {todo && (
        <Tabs.Panel value="todo" className="flex min-h-0 flex-1 flex-col gap-2 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-3 outline-hidden">
          {tab === 'todo' && <TodoTab view={todo.view} subagents={subagents} blocks={phone ? <Blocks items={todo.blocks} className="self-start" /> : undefined} />}
        </Tabs.Panel>
      )}
      {tabs.includes('subagents') && (
        <Tabs.Panel value="subagents" className="flex min-h-0 flex-1 flex-col outline-hidden">
          {tab === 'subagents' && body}
        </Tabs.Panel>
      )}
      {/* The Subagents tab keeps its own foot: the list's keys, or the detail's with Stop. */}
      {!phone && tab === 'todo' && (
        <PanelFoot>
          <span className="flex items-center gap-1.5">
            <Key>Esc</Key>close
          </span>
          {tabs.length > 1 && (
            <span className="flex items-center gap-1.5">
              <Key>←</Key>
              <Key>→</Key>switch
            </span>
          )}
          <span className="flex-1" />
          <span className="flex items-center gap-0.5">
            Read only
            <HelpTip label="Read only">To change it, ask in the chat</HelpTip>
          </span>
        </PanelFoot>
      )}
    </Tabs.Root>
  );
  if (phone) {
    return (
      <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label={label} className="h-[70dvh] duration-160" backdropClassName="duration-160" initialFocus={initialFocus} finalFocus={finalFocus}>
        {content(
          <BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
            <X />
          </BaseDialog.Close>,
        )}
      </BottomSheet>
    );
  }
  return (
    <BasePopover.Root open={open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={cn(backdropClass, 'duration-160')} />
        {open && lift?.isConnected && <LiftedRow row={lift} />}
        {/* The same place and size whoever opened it: never beside a row in the conversation. */}
        <BasePopover.Positioner anchor={stripEnd(row)} side="top" align="end" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup
            id={id}
            data-popup=""
            aria-label={label}
            aria-modal="true"
            initialFocus={initialFocus}
            finalFocus={finalFocus}
            onKeyDown={onArrows}
            className="flex h-[min(70vh,var(--available-height))] w-[560px] max-w-(--available-width) origin-bottom-right flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,translate] duration-160 ease-app data-starting-style:translate-y-2 data-starting-style:opacity-0 data-ending-style:translate-y-2 data-ending-style:opacity-0"
          >
            {content(
              <BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
                <X />
              </BasePopover.Close>,
            )}
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

/** The list's counts in the strip's name; the row the work is at is already its slot. */
function todoWords(todo: TodoLine, plan?: boolean): string {
  const words = todoSentence(todo.kind === 'list' ? { ...todo, now: undefined } : todo);
  return plan ? words.replace('. Todo ', '. Plan ') : words;
}

function retrySentence(retry: Retry): string {
  const { lead, parts } = retryWords(retry);
  return `. ${lead}${parts.length ? `: ${parts.join(', ')}` : ''}`;
}

/**
 * The working line: the todo list's stage blocks (dimmed while it is the last turn's), then one
 * slot: "Compacting the conversation", "From the last turn", "Now:" and the row the work is at, or
 * what the agent says it is doing (nothing when it says nothing); then a retry and compaction in
 * the background. How long the turn has worked is the turn card's head's, never repeated here.
 */
/** The working line's one slot in words, as the strip's name starts. */
function slotWords(line: Extract<Line, { kind: 'working' }>, outlived: boolean): string {
  const now = line.todo?.kind === 'list' ? line.todo.now : undefined;
  if (line.live) return [line.live.phase, line.live.detail].filter(Boolean).join(', ');
  if (line.compacting) return line.lead;
  if (line.todo?.kind !== 'carried' && now) return `Now: ${now}`;
  return line.lead || (outlived ? 'Working' : '');
}

function Working({ line, since }: Readonly<{ line: Extract<Line, { kind: 'working' }>; since?: string }>) {
  const retry = line.retry && retryWords(line.retry);
  const todo = line.todo;
  const now = todo?.kind === 'list' ? todo.now : undefined;
  return (
    <>
      {todo && (
        <span className={cn('flex shrink-0 items-center gap-2', todo.kind === 'carried' && 'opacity-55')}>
          <ListChecks className="size-3.5 shrink-0 text-muted" />
          <Blocks items={todoBlocks(todo)} />
        </span>
      )}
      {/* The slot gives way before the clock does. Live, the line says what happens now; "Now:" stays in the reader. */}
      {line.live ? (
        <LiveSlot live={since && !line.live.since ? { ...line.live, since } : line.live} />
      ) : line.compacting ? (
        <span className="min-w-0 truncate text-ui text-badge-violet">{line.lead}…</span>
      ) : todo?.kind === 'carried' ? (
        <span className="min-w-0 truncate text-ui text-muted">From the last turn</span>
      ) : now ? (
        <span className="min-w-0 truncate text-ui text-body">
          <span className="text-muted">Now:</span> {now}
        </span>
      ) : line.lead || since ? (
        <span className="min-w-0 truncate text-ui text-body">{line.lead || 'Working'}</span>
      ) : null}
      {since && !line.live && <Clock since={since} />}
      {retry && (
        <>
          <Sep />
          <RotateCw className="size-3 shrink-0 text-muted" />
          <span className="shrink-0 text-body">{retry.lead}</span>
          {retry.parts.length > 0 && <span className="min-w-0 shrink-[2] truncate max-sm:hidden">· {retry.parts.join(' · ')}</span>}
        </>
      )}
      {/* Words only: the turn goes on, so the line keeps saying what it does. */}
      {line.background && (
        <>
          <Sep />
          <span className="min-w-0 shrink-[3] truncate">Compacting in background</span>
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

/** Date.now(), again each second while `on`. */
function useNow(on: unknown): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
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

/** Time since `since`, the work that outlived the turn; ticks each second while it counts. */
function useElapsed(since: string | undefined): string | null {
  const now = useNow(since);
  return since ? elapsedSince(since, now) : null;
}

/** `value`, or for `ms` after it went away while `on`, the last one it was; nothing once `on` ends. */
function useHeld<T>(value: T | null | undefined, on: boolean, ms: number): T | null {
  const [held, setHeld] = useState<T | null>(null);
  if (value && value !== held) setHeld(value);
  if (!on && held) setHeld(null);
  useEffect(() => {
    if (value || !on) return;
    const timer = window.setTimeout(() => setHeld(null), ms);
    return () => window.clearTimeout(timer);
  }, [value, on, ms]);
  return on ? value ?? held : null;
}

/** True while `el` is off screen (the dock under a phone's keyboard), so its animation pauses. */
function useOffScreen(el: HTMLElement | null): boolean {
  const [off, setOff] = useState(false);
  useEffect(() => {
    if (!el || typeof IntersectionObserver === 'undefined') return;
    const watch = new IntersectionObserver(([entry]) => setOff(!entry.isIntersecting));
    watch.observe(el);
    return () => watch.disconnect();
  }, [el]);
  return off;
}

/**
 * The live line (DESIGN.md Status line, live line): [mark] phase · detail · elapsed, then a running command's newest
 * output line, quieter, which gives way first (hidden on a phone). Its mark is the one animated element in the pane
 * besides the sidebar's glyph: model work breathes, local work turns, a wait past WAIT_STILL_MS is a still clock in
 * muted words, "Needs you" is a still amber shield, subagents a still glyph. Paused off screen; still under reduced motion.
 */
function LiveSlot({ live }: Readonly<{ live: Live }>) {
  const [el, setEl] = useState<HTMLSpanElement | null>(null);
  const off = useOffScreen(el);
  const now = useNow(live.since);
  const ms = live.since ? now - Date.parse(live.since) : Number.NaN;
  const elapsed = Number.isFinite(ms) ? liveClock(ms) : '';
  const waited = live.mark === 'wait' && ms >= WAIT_STILL_MS;
  const you = live.mark === 'you';
  const mark = you ? <ShieldQuestionMark className="size-3.5" />
    : live.mark === 'subagents' ? <Workflow className="size-3.5" />
    : waited ? <ClockIcon className="size-3.5" />
    : live.mark === 'ring' ? <WorkingMark />
    : <BreathingDot />;
  return (
    <span ref={setEl} className={cn('flex min-w-0 items-center gap-2 overflow-hidden text-ui whitespace-nowrap', you ? 'text-attention' : 'text-body', off && '[&_*]:[animation-play-state:paused]')}>
      <span className={cn('grid size-3.5 shrink-0 place-items-center', !you && 'text-muted')}>{mark}</span>
      <span className={cn('min-w-0 truncate', live.detail && 'shrink-0', you ? 'font-semibold' : waited ? 'text-muted' : 'text-ink')}>{live.phase}</span>
      {live.detail && (
        <>
          <Sep />
          <span className={cn('min-w-0 shrink-1000 truncate', waited && 'text-muted')}>{live.detail}</span>
        </>
      )}
      {/* The output line gives way first, then the detail, then the phase; the clock stays whole (the line clips past it). */}
      {elapsed && (
        <span className={cn('shrink-0 tabular-nums', !you && 'text-muted')}>
          <span className="pr-2 text-faint">·</span>
          {elapsed}
        </span>
      )}
      {live.tail && <span className="min-w-0 shrink-1000 truncate pl-1.5 font-mono text-code-sm text-faint max-sm:hidden">{live.tail}</span>}
    </span>
  );
}

/** The clock's digits: only they re-render each second. Under an hour they are at most three characters wide, and the slot holds them all, so the line keeps its width. */
function Clock({ since }: Readonly<{ since: string }>) {
  const elapsed = useElapsed(since);
  return elapsed && <span className="min-w-[3ch] shrink-0 text-right tabular-nums text-muted">{elapsed}</span>;
}

/** The clock as the button's name says it, by the minute. */
function ClockWords({ since }: Readonly<{ since?: string }>) {
  const elapsed = useElapsed(since);
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
