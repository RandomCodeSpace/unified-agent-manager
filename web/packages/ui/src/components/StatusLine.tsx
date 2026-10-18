// The status strip (DESIGN.md Status line): one quiet line over the composer. While the Thread
// works it gives the todo list as stage blocks (✓ done, ◉ in progress, ⊘ blocked, ○ to do), then
// "Now:" and the row the work is at (without a list, what the agent says it is doing,
// `assistant.intent`), for how long, a model call being retried and automatic compaction running
// beside the turn ("Compacting in background", words only); after a stop it says why, until the
// next turn. Its right end holds the Thread's subagents as blocks (✗ failed, ● running, ✓ done,
// ○ stopped), there whenever the Thread has any. Pressing it opens one anchored reader with a Todo
// and a Subagents tab (a sheet on a phone). It sits in the dock's overlap, outside the scroller, so
// coming and going never moves the composer or the transcript. It is still: the ring stays at the
// Thread's state glyph and the running step. A subagent's retry stands under its row instead
// (`SubagentRetry`).

import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Tabs } from '@base-ui/react/tabs';
import { Ban, Check, Circle, CircleDot, CircleSmall, CircleStop, ListChecks, Minus, Network, Pause, RotateCw, X, type LucideProps } from 'lucide-react';
import { forwardRef, useEffect, useId, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react';
import { LIVE, readOnly, type Retry, type SessionDetail, type Subagent, type TodoView, type TurnTiming } from '../api';
import { cn } from '../lib/cn';
import { retryWords, stopWords } from '../lib/stop';
import { countSubagents } from '../lib/subagents';
import { todoLine, todoSentence, type TodoLine, type TodoTally } from '../lib/todos';
import { elapsedSince, turnElapsed } from '../lib/transcript';
import { Key } from './InlinePicker';
import { BottomSheet, LiftedRow, SubagentIndexBody, useSubagentIndex, type IndexPick } from './Subagents';
import { PHONE, TodoTab } from './Todos';
import { Blocks, type Block } from './ui/blocks';
import { Button } from './ui/button';
import { backdropClass } from './ui/dialog';
import { PanelFoot, PanelHead } from './ui/panel';
import { HelpTip } from './ui/tooltip';

/** What the line says. */
export type Line =
  | { kind: 'working'; lead: string; compacting: boolean; retry?: Retry; todo?: TodoLine; plan?: boolean; /** Automatic compaction runs beside the turn. */ background?: boolean }
  | { kind: 'stopped'; short: string; long: string; notYours: boolean }
  | { kind: 'paused'; text: string };

/**
 * The line for a Thread, or null when it has nothing to say. `working`: a turn runs, or subagents
 * outlived it; `lead` is what the agent says it is doing, or "" (never a made-up verb).
 * `connected`: the event stream is up, so a timing marked unknown is the service's (a turn a
 * restart or close cut off), not the running one this page lost track of while it reconnects.
 */
export function statusLine(session: SessionDetail, working: boolean, compacting: boolean, connected = true): Line | null {
  if (working) {
    if (compacting) return { kind: 'working', lead: 'Compacting the conversation', compacting };
    const turn = session.turn_timings?.at(-1);
    // The Thread runs a turn whose timing has not come yet: all there is belongs to the last turn, which ended or was cut off.
    if ((turn?.ended_at || (connected && turn?.state === 'unknown')) && LIVE.includes(session.state)) return null;
    const activity = session.turn_activity;
    const todo = todoLine(activity?.todos, turn?.started_at);
    return { kind: 'working', lead: activity?.intent ?? '', compacting, retry: activity?.retry, ...(activity?.plan && todo?.kind === 'list' ? { plan: true } : {}), ...(session.compacting_background ? { background: true } : {}), todo };
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
 * The strip as one button. Its name is one sentence (sr-only), the words on screen are hidden from
 * assistive tech, and a polite live region beside it speaks only what `useCue` lets through.
 * Pressing it opens the reader on its Todo tab (on its Subagents tab when pressed on the subagent
 * blocks, or with no list), else shows the end of the conversation, where the work is. `hidden`
 * gives its place to another strip. `error`: why the last jump to a subagent did not land, said in
 * the Subagents tab.
 */
export function StatusLine({ line, session, since, hidden, error, onJump }: Readonly<{
  line: Line | null;
  session: SessionDetail;
  /** When the subagents that outlived the turn started, which the clock then counts from. */
  since?: string;
  hidden: boolean;
  error?: string;
  onJump: () => void;
}>) {
  const shownLine = hidden ? null : line;
  const index = useSubagentIndex();
  const subagents = hidden || !index?.subagents.length ? undefined : index.subagents;
  const timing = session.turn_timings?.at(-1);
  const cue = useCue(shownLine, timing?.id ?? '');
  const list = shownLine?.kind === 'working' ? shownLine.todo : undefined;
  const view = list ? session.turn_activity?.todos : undefined;
  const tabs: Tab[] = [...(view ? ['todo' as const] : []), ...(subagents ? ['subagents' as const] : [])];
  const readerId = useId();
  const [button, setButton] = useState<HTMLButtonElement | null>(null);
  // Phone or not is chosen as it opens, so a resize does not remount it.
  const [reader, setReader] = useState<{ open: boolean; phone: boolean; tab: Tab } | null>(null);
  // Where a pick in the Subagents tab goes once the reader has closed.
  const picked = useRef<IndexPick | null>(null);
  // With nothing left to show (the turn ended, the conversation closed), the reader goes too.
  if (reader && !tabs.length) setReader(null);
  const tab = reader && tabs.includes(reader.tab) ? reader.tab : tabs[0];
  const show = (on: Tab) => {
    picked.current = null;
    setReader({ open: true, phone: window.matchMedia(PHONE).matches, tab: on });
  };
  const close = () => setReader((r) => r && { ...r, open: false });
  const closed = () => {
    setReader(null);
    const to = picked.current;
    picked.current = null;
    if (!to || !index) return;
    if ('agent' in to) index.actions.open(to.agent, document.getElementById('subagents-link'));
    else index.actions.locate(to.call, to.expand);
  };
  const pick = (to: IndexPick) => {
    picked.current = to;
    close();
  };
  const plan = shownLine?.kind === 'working' && shownLine.plan;
  const action = tabs.length === 2 ? 'Show the list and the subagents' : tabs[0] === 'todo' ? 'Show the list' : tabs[0] === 'subagents' ? 'Show the subagents' : 'Jump to bottom';
  return (
    <>
      <div role="status" aria-live="polite" aria-atomic="true" className="sr-only">
        {shownLine ? cue : ''}
      </div>
      {(shownLine || subagents) && (
        <button
          ref={setButton}
          type="button"
          onClick={tabs.length ? (e) => show((e.target as Element).closest('[data-strip-subagents]') && subagents ? 'subagents' : tabs[0]) : onJump}
          aria-haspopup={tabs.length ? 'dialog' : undefined}
          aria-expanded={tabs.length ? !!reader?.open : undefined}
          aria-controls={tabs.length && reader?.open ? readerId : undefined}
          className="flex h-8 min-w-0 flex-1 items-center gap-2.5 rounded-sm bg-canvas px-2 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover pointer-coarse:h-11"
        >
          {shownLine && (
            <span key={shownLine.kind} aria-hidden="true" className="flex min-w-0 animate-fade-in items-center gap-2 sm:gap-2.5">
              {shownLine.kind === 'working' ? <Working line={shownLine} since={since} timing={timing} /> : shownLine.kind === 'stopped' ? <Stopped line={shownLine} /> : <Paused text={shownLine.text} />}
            </span>
          )}
          <span className="flex-1" />
          {subagents && (
            <span id="subagents-link" data-strip-subagents="" aria-hidden="true" className="flex shrink-0 items-center gap-2">
              <Network className="size-3.5 shrink-0 text-muted" />
              <Blocks items={subagentBlocks(subagents)} />
            </span>
          )}
          <span className="sr-only">
            {shownLine?.kind === 'working' ? (
              <>
                {slotWords(shownLine)}
                <ClockWords since={since} timing={timing} />
                {shownLine.todo && todoWords(shownLine.todo, shownLine.plan)}
                {shownLine.retry && retrySentence(shownLine.retry)}
                {shownLine.background && '. Compacting in background'}
                {'. '}
              </>
            ) : shownLine?.kind === 'stopped' ? (
              `${shownLine.long.replaceAll(' · ', ', ')}. `
            ) : shownLine ? (
              `${shownLine.text}. `
            ) : null}
            {subagents && `${subagentSentence(subagents, index?.before)}. `}
            {action}
          </span>
        </button>
      )}
      {reader && tab && (
        <StripReader id={readerId} open={reader.open} phone={reader.phone} anchor={button} tabs={tabs} tab={tab} onTab={(t) => setReader((r) => r && { ...r, tab: t })} todoLabel={plan ? 'Plan' : 'Todo'} todo={view && list ? { view, blocks: todoBlocks(list) } : undefined} subagents={session.subagents} error={error} onPick={pick} picked={picked} onClose={close} onClosed={closed} />
      )}
    </>
  );
}

const TAB_ICON: Record<Tab, typeof ListChecks> = { todo: ListChecks, subagents: Network };

/**
 * The strip's reader (DESIGN.md Status line, reader): an anchored reader above the strip (the strip
 * lifted above a 160ms dim), or on a phone (`phone`, chosen as it opened) a sheet from the bottom,
 * with a tab per thing the strip counts: Todo (or Plan), the list in plan order, and Subagents, the
 * subagent index. It opens with focus on the chosen tab; ← and → switch tabs from anywhere but a
 * text field; Esc, the close button or a press outside close it and give focus back to the strip,
 * unless a pick in the index takes it on.
 */
function StripReader({ id, open, phone, anchor, tabs, tab, onTab, todoLabel, todo, subagents, error, onPick, picked, onClose, onClosed }: Readonly<{
  id: string;
  open: boolean;
  phone: boolean;
  anchor: HTMLElement | null;
  tabs: Tab[];
  tab: Tab;
  onTab: (tab: Tab) => void;
  todoLabel: 'Todo' | 'Plan';
  todo?: { view: TodoView; blocks: Block[] };
  subagents: readonly Subagent[];
  error?: string;
  onPick: (to: IndexPick) => void;
  picked: { readonly current: IndexPick | null };
  onClose: () => void;
  onClosed: () => void;
}>) {
  const activeTab = useRef<HTMLButtonElement>(null);
  const search = useRef<HTMLInputElement>(null);
  const sheet = phone || !anchor;
  const names: Record<Tab, string> = { todo: todoLabel, subagents: 'Subagents' };
  const label = tabs.map((t, i) => (i ? names[t].toLowerCase() : names[t])).join(' and ');
  const back = () => (picked.current ? false : anchor ?? false);
  const onArrows = (e: ReactKeyboardEvent) => {
    if ((e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') || tabs.length < 2 || e.altKey || e.ctrlKey || e.metaKey) return;
    const target = e.target as HTMLElement;
    // The tabs move themselves, and a text field keeps its caret.
    if (target.closest('[role="tablist"], input, textarea, [contenteditable="true"]')) return;
    e.preventDefault();
    const next = tabs[(tabs.indexOf(tab) + (e.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length];
    onTab(next);
    requestAnimationFrame(() => activeTab.current?.focus());
  };
  const body = (closeButton: ReactNode) => (
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
        <Tabs.Panel value="todo" className="flex min-h-0 flex-col gap-2 overflow-x-hidden overflow-y-auto overscroll-contain px-4 pt-1 pb-3 outline-hidden">
          {tab === 'todo' && <TodoTab view={todo.view} subagents={subagents} blocks={sheet ? <Blocks items={todo.blocks} className="self-start" /> : undefined} />}
        </Tabs.Panel>
      )}
      {tabs.includes('subagents') && (
        <Tabs.Panel value="subagents" className="flex min-h-0 flex-col gap-1.5 px-3 pt-0.5 pb-2 outline-hidden">
          {tab === 'subagents' && <SubagentIndexBody input={search} error={error} onPick={onPick} />}
        </Tabs.Panel>
      )}
      {!sheet && (
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
          {tab === 'subagents' ? (
            <span className="flex items-center gap-1.5">
              <Key>↵</Key>open
            </span>
          ) : (
            <>
              <span className="flex-1" />
              <span className="flex items-center gap-0.5">
                Read only
                <HelpTip label="Read only">To change it, ask in the chat</HelpTip>
              </span>
            </>
          )}
        </PanelFoot>
      )}
    </Tabs.Root>
  );
  if (sheet) {
    return (
      <BottomSheet id={id} open={open} onClose={onClose} onClosed={onClosed} label={label} className="duration-160" backdropClassName="duration-160" initialFocus={activeTab} finalFocus={back}>
        {body(
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
        {open && anchor.isConnected && <LiftedRow row={anchor} />}
        <BasePopover.Positioner anchor={anchor} side="top" align="start" sideOffset={10} collisionPadding={12} className="z-50 outline-hidden">
          <BasePopover.Popup
            id={id}
            data-popup=""
            aria-label={label}
            aria-modal="true"
            initialFocus={activeTab}
            finalFocus={back}
            onKeyDown={onArrows}
            className="flex max-h-[min(80vh,var(--available-height))] w-[460px] max-w-(--available-width) origin-(--transform-origin) flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0"
          >
            {body(
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
 * what the agent says it is doing ("Working" when it says nothing); then the clock, a retry and
 * compaction in the background.
 */
/** The working line's one slot in words, as the strip's name starts. */
function slotWords(line: Extract<Line, { kind: 'working' }>): string {
  const now = line.todo?.kind === 'list' ? line.todo.now : undefined;
  if (line.compacting) return line.lead;
  if (line.todo?.kind !== 'carried' && now) return `Now: ${now}`;
  return line.lead || 'Working';
}

function Working({ line, since, timing }: Readonly<{ line: Extract<Line, { kind: 'working' }>; since?: string; timing?: TurnTiming }>) {
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
      {/* The slot gives way before the clock does. */}
      {line.compacting ? (
        <span className="min-w-0 truncate text-ui text-badge-violet">{line.lead}…</span>
      ) : todo?.kind === 'carried' ? (
        <span className="min-w-0 truncate text-ui text-muted">From the last turn</span>
      ) : now ? (
        <span className="min-w-0 truncate text-ui text-body">
          <span className="text-muted">Now:</span> {now}
        </span>
      ) : (
        <span className="min-w-0 truncate text-ui text-body">{line.lead || 'Working'}</span>
      )}
      <Clock since={since} timing={timing} />
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
  return elapsed && <span className="min-w-[3ch] shrink-0 text-right tabular-nums text-muted">{elapsed}</span>;
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
