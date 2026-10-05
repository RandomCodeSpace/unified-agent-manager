import { flushSync } from 'react-dom';
import { HistoryAnchor } from './HistoryAnchor';
import { isSubagentCall, subagentSummary, duration, toolLabel, windowInteractions } from '../lib/transcript';
import { BodyNotice, useDetailAgent, useDisclosure, useItemBody } from './Details';
import { Popover as BasePopover } from '@base-ui/react/popover';
import { Dialog as BaseDialog } from '@base-ui/react/dialog';
import { ArrowLeft, Bot, Check, ChevronDown, ChevronRight, CornerDownRight, Minus, Square, X } from 'lucide-react';
import { createContext, Fragment, memo, useContext, useEffect, useEffectEvent, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type KeyboardEvent as ReactKeyboardEvent, type ReactNode, type RefObject } from 'react';
import { api, describeError, modelName, readOnly, type Interaction, type Item, type OutlineItem, type SessionDetail, type Subagent, type SubagentStatus } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { useDensity } from '../lib/density';
import { historyPage } from '../lib/historyArchive';
import { compactTokens } from '../lib/cost';
import { EXIT_MS } from './ui/collapse';
import { FILTER_OVER, IDENTITY_LIMIT, PAGE_ROWS, countParts, countSubagents, families, inFilter, indexGroups, liveSet, matches, mergeOutline, ms, parentMap, replyIndex, runCount, runLines, subagentNoun, totalTokens, type CountPart, type Family, type IdentityTone, type IndexFilter, type IndexGroup, type LiveSubagent, type Replies, type SubagentCounts } from '../lib/subagents';
import { useResizable } from '../lib/useResizable';
import type { AgentTranscript } from '../state';
import { useFileHintItems } from './FileReferences';
import { Markdown, Note, Skeleton, Spinner, SubagentIdleIcon, WorkingMark, clockTime, useApp, useMedia } from './common';
import { AgentItems } from './Transcript';
import { Button } from './ui/button';
import { AlertDialog, Sheet, backdropClass, useConfirm } from './ui/dialog';
import { Input } from './ui/input';
import { popupClass } from './ui/menu';
import { Popover } from './ui/popover';
import { Tip } from './ui/tooltip';

/** A stop request for one subagent; the provider's SSE still owns its terminal status. */
interface StopState {
  busy: boolean;
  requested: boolean;
  error: string | null;
}
const NOT_STOPPING: StopState = { busy: false, requested: false, error: null };

/** One record of stop requests per Task, so every row of a subagent agrees. */
function useStops(sessionId: string, subagents: readonly Subagent[]): [Record<string, StopState>, (agentId: string) => void] {
  const [stops, setStops] = useState<Record<string, StopState>>({});
  // A request (or its failure) belongs to the run it was for: once the subagent no longer runs it
  // goes, so a later run can be stopped again.
  const over = Object.keys(stops).filter((agentId) => !stops[agentId].busy && subagents.find((x) => x.id === agentId)?.status !== 'running');
  if (over.length) setStops((all) => Object.fromEntries(Object.entries(all).filter(([agentId]) => !over.includes(agentId))));
  const set = (agentId: string, v: StopState) => setStops((all) => ({ ...all, [agentId]: v }));
  async function stop(agentId: string) {
    const current = stops[agentId];
    if (current?.busy || current?.requested) return;
    set(agentId, { busy: true, requested: false, error: null });
    try {
      await api.cancelSubagent(sessionId, agentId);
      set(agentId, { busy: false, requested: true, error: null });
    } catch (e) {
      set(agentId, { busy: false, requested: false, error: describeError(e) });
    }
  }
  return [stops, (agentId) => void stop(agentId)];
}

/** HH:MM on the local clock; empty for a missing or unreadable time. */
const clock = (iso?: string) => {
  const at = iso ? new Date(iso) : null;
  return at && !Number.isNaN(at.getTime()) ? clockTime(at) : '';
};

/** Distance from the bottom, in px, under which the view counts as "at the bottom". */
const BOTTOM_SLACK = 32;
const NO_ITEMS: Item[] = [];

/** Whether the view is within reach (two screens, 200px to 1000px) of the window's `direction` edge. */
function nearEdge(el: HTMLElement, direction: 'older' | 'newer'): boolean {
  const rect = el.querySelector('[data-history-window]')?.getBoundingClientRect();
  const edge = el.getBoundingClientRect();
  let distance: number;
  if (direction === 'older') distance = rect ? edge.top - rect.top : el.scrollTop;
  else distance = rect ? rect.bottom - edge.bottom : el.scrollHeight - el.scrollTop - el.clientHeight;
  return distance < Math.min(1000, Math.max(200, el.clientHeight * 2));
}

/**
 * A side panel: inline beside the column at ≥1280px with a draggable inner edge (width
 * remembered per panel), an overlay sheet from the right at 960–1279, a full-screen sheet
 * below. The overlay is a Base UI dialog (focus trap, Esc, backdrop) that slides in and
 * out. Inline, the column commits its width at once and the panel slides over the space it
 * left; closing slides it out, then `onClosed` lets the owner unmount it.
 */
export function SidePanel({ id, inline, open, onClose, onClosed, label, children, className, defaultWidth = 440, preferWidth = 0 }: Readonly<{ id: string; inline: boolean; open: boolean; onClose: () => void; onClosed: () => void; label: string; children: ReactNode; className?: string; defaultWidth?: number; /** Widens the inline panel to at least this much while set, as far as the row allows (a wide view inside it). */ preferWidth?: number }>) {
  const { narrow } = useApp();
  const { panelRef, handleProps } = useResizable(id, defaultWidth, undefined, undefined, preferWidth);
  // The slide starts one frame after mount, so the first paint is off-screen.
  const [shown, setShown] = useState(false);
  useEffect(() => {
    if (!inline) return;
    const frame = requestAnimationFrame(() => setShown(open));
    return () => cancelAnimationFrame(frame);
  }, [inline, open]);
  const closed = useEffectEvent(onClosed);
  useEffect(() => {
    if (!inline || open) return;
    const timer = window.setTimeout(closed, EXIT_MS);
    return () => window.clearTimeout(timer);
  }, [inline, open]);
  if (!inline) {
    return (
      <Sheet open={open} onOpenChange={(o) => !o && onClose()} onClosed={onClosed} side="right" label={label} className={cn('w-full max-w-none bg-canvas', !narrow && 'w-[min(var(--spacing-panel),100vw)]', className)}>
        {children}
      </Sheet>
    );
  }
  return (
    <aside ref={panelRef} aria-label={label} className={cn('relative flex w-(--panel-w) shrink-0 flex-col bg-canvas', className)}>
      <div className={cn('flex min-h-0 flex-1 flex-col transition-transform duration-240 ease-app', shown && open ? 'translate-x-0' : 'translate-x-full')} inert={!open}>
        <div
          {...handleProps}
          className="group/handle absolute inset-y-0 -left-1 z-10 flex w-2 cursor-col-resize items-center justify-center outline-hidden focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-focus"
          title="Drag to resize · double-click to reset"
        >
          <span aria-hidden="true" className="fade-rule-y h-full w-px flex-none transition-[background-color,width] duration-100 group-hover/handle:w-0.5 group-hover/handle:bg-accent group-hover/handle:bg-none group-focus-visible/handle:w-0.5 group-focus-visible/handle:bg-accent group-focus-visible/handle:bg-none group-active/handle:bg-accent group-active/handle:bg-none" />
        </div>
        {children}
      </div>
    </aside>
  );
}

export function PanelHeader({ children, className }: Readonly<{ children: ReactNode; className?: string }>) {
  return <div className={cn('pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3', className)}>{children}</div>;
}

/**
 * A request from `locate` to show the row of the subagent `toolCallId` spawned: its reply's list
 * opens, and the page holding the row; with `expand` its transcript opens too. `n` counts the
 * requests, so asking twice for the same row still opens it.
 */
export interface Reveal {
  toolCallId: string;
  expand: boolean;
  n: number;
}

/**
 * The subagent whose transcript is open (one per Task): the row it was opened from, the ones it
 * was opened through ("← parent"), and on a phone whether its sheet has grown from the peek to the
 * transcript. `open` turns false while it closes.
 */
interface Pinned {
  id: string;
  anchor: Element | null;
  back: string[];
  full: boolean;
  open: boolean;
}

/** A run picked in the transcript's run strip: the transcript shows the run's first item. `n` counts the picks. */
interface Seek {
  time: string;
  /** The first run: the transcript's start. */
  first: boolean;
  /** Still running: with nothing of it recorded yet, the transcript's end. */
  running: boolean;
  n: number;
}

/** What of the Task the transcript reads; kept while those fields are unchanged, so streaming leaves it alone. */
type TaskInfo = Pick<SessionDetail, 'id' | 'provider' | 'workdir' | 'interactions' | 'stage' | 'state'>;

/** What a subagent's one line is read from: its transcript or latest step, and its `task` call. */
interface Summaries {
  agents: Record<string, AgentTranscript>;
  agentSteps: Record<string, Item>;
  /** The `task` calls held (the window and the live tail), by id. */
  calls: ReadonlyMap<string, Item>;
}

/**
 * Everything a subagent row may read, swapped whole when it changes; rows select only what they
 * show. Nothing in it changes while the main agent streams, so a token renders none of them.
 */
interface ScopeData {
  task: TaskInfo;
  subagents: Subagent[];
  subagentsBefore?: string;
  agents: Record<string, AgentTranscript>;
  snapshotSeq: number;
  summaries: Summaries;
  /** The user messages and `task` calls of the outline (lib/subagents `mergeOutline`), loaded or not. */
  outline: readonly OutlineItem[];
  /** The user messages held, for the index's quotes. */
  messages: readonly Item[];
  /** When each `task` call ran, from the outline. */
  callTimes: ReadonlyMap<string, string>;
  replies: Replies;
  /** The live set; empty while nothing is at work. */
  live: LiveSubagent[];
  liveIds: ReadonlySet<string>;
  stops: Record<string, StopState>;
  /** The subagent whose transcript is open, or "". */
  pinned: string;
  /** Below 640px: a row opens a sheet instead of the peek and the panel. */
  phone: boolean;
  reveal: Reveal | null;
}

/** Stable for the life of the scope, so rows never render for a new callback. */
interface ScopeActions {
  /** Scroll to the row of the subagent a call spawned, opening what holds it; `expand` opens its transcript. */
  locate: (toolCallId: string, expand?: boolean) => void;
  /** Scroll to the reply a user message started and flash its turn line. */
  jumpToReply: (key: string) => void;
  /** Asks to confirm, then stops. */
  stop: (s: Subagent) => void;
  /** Opens a subagent's transcript next to `anchor` (its row, or what stands for it); on a phone, its sheet at the peek. */
  open: (id: string, anchor: Element | null) => void;
}

class ScopeStore {
  data: ScopeData;
  private listeners = new Set<() => void>();
  constructor(data: ScopeData) {
    this.data = data;
  }
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  set(data: ScopeData) {
    if (data === this.data) return;
    this.data = data;
    this.listeners.forEach((listener) => listener());
  }
}

const SubagentContext = createContext<{ store: ScopeStore; actions: ScopeActions; peek: BasePopover.Handle<string> } | null>(null);
const NO_SUBSCRIBE = () => () => {};

/** One value from the Task's subagents; a component renders again only when it changes, so `select` returns primitives or kept references. */
function useScope<T>(select: (d: ScopeData) => T): T | undefined {
  const scope = useContext(SubagentContext);
  return useSyncExternalStore(scope?.store.subscribe ?? NO_SUBSCRIBE, () => (scope ? select(scope.store.data) : undefined));
}
const useActions = () => useContext(SubagentContext)?.actions;

/** `next` itself when any element differs from `kept` (by reference), else `kept`: a list rebuilt with the same members stays the same list. */
function useKept<T>(next: readonly T[]): readonly T[] {
  const [kept, setKept] = useState(next);
  if (kept === next || (kept.length === next.length && kept.every((x, i) => x === next[i]))) return kept;
  setKept(next);
  return next;
}

/** The replies of the Task and the subagents each spawned (lib/subagents `replyIndex`); undefined outside a Task. */
export const useSubagentReplies = () => useScope((d) => d.replies);
/** The subagents the live set shows, while it shows. */
export const useLiveSubagentIds = () => useScope((d) => d.liveIds);

/**
 * The Task's subagents for everything that draws them (the reply chips and lists, the live set,
 * the header index): the session, the transcripts held, the one peek (DESIGN.md subagent peek),
 * the one open transcript, stop requests and their one confirmation, and `locate`.
 */
export function SubagentScope({ session, agents, agentSteps, snapshotSeq, reveal, onLocate, onJumpToReply, children }: Readonly<{ session: SessionDetail; agents: Record<string, AgentTranscript>; agentSteps: Record<string, Item>; snapshotSeq: number; reveal: Reveal | null; onLocate: (toolCallId: string, expand?: boolean) => void; onJumpToReply: (key: string) => void; children: ReactNode }>) {
  const [stops, stopNow] = useStops(session.id, session.subagents);
  // Stopping a subagent ends its work for good, so it is confirmed first (DESIGN.md Confirmations).
  const stopConfirm = useConfirm<Subagent>();
  const phone = useMedia('(max-width: 639px)');
  const [pinned, setPinned] = useState<Pinned | null>(null);
  // The items and the index change with every streamed token; what is read from them (the user
  // messages and the `task` calls) only with a message or a call, so those lists are kept until then.
  const byParent = parentMap(session.subagents);
  const held = [...session.items, ...(session.recent_items ?? [])];
  const calls = useKept(held.filter((item) => isSubagentCall(item) || byParent.has(item.id)));
  const messages = useKept(held.filter((item) => item.kind === 'user'));
  const outline = useKept(mergeOutline(session.outline, (session.history_index ?? session.items).filter((item) => (item.kind === 'user' && !item.delivery) || isSubagentCall(item) || byParent.has(item.id))));
  const callMap = useMemo(() => new Map(calls.map((item) => [item.id, item])), [calls]);
  const callTimes = useMemo(() => new Map(outline.flatMap((item) => (item.kind === 'tool' && item.time ? [[item.id, item.time] as const] : []))), [outline]);
  const replies = useMemo(() => replyIndex(outline, session.subagents), [outline, session.subagents]);
  const summaries = useMemo<Summaries>(() => ({ agents, agentSteps, calls: callMap }), [agents, agentSteps, callMap]);
  const { id, provider, workdir, interactions, stage, state } = session;
  const task = useMemo<TaskInfo>(() => ({ id, provider, workdir, interactions, stage, state }), [id, provider, workdir, interactions, stage, state]);
  // The open transcript keeps its subagent in the live set, so its row stays.
  const keep = pinned?.id;
  const live = useMemo(() => liveSet(replies.list.at(-1), session.subagents, keep), [replies, session.subagents, keep]);
  const liveIds = useMemo(() => new Set(live.map((x) => x.subagent.id)), [live]);
  // A transcript whose subagent is gone closes.
  if (pinned?.open && !session.subagents.some((x) => x.id === pinned.id)) setPinned({ ...pinned, open: false });
  const data = useMemo<ScopeData>(
    () => ({ task, subagents: session.subagents, subagentsBefore: session.subagents_before, agents, snapshotSeq, summaries, outline, messages, callTimes, replies, live, liveIds, stops, pinned: pinned?.open ? pinned.id : '', phone, reveal }),
    [task, session.subagents, session.subagents_before, agents, snapshotSeq, summaries, outline, messages, callTimes, replies, live, liveIds, stops, pinned, phone, reveal],
  );
  const [store] = useState(() => new ScopeStore(data));
  useLayoutEffect(() => store.set(data), [store, data]);
  const handlers = useRef({ onLocate, onJumpToReply, ask: stopConfirm.ask });
  useLayoutEffect(() => {
    handlers.current = { onLocate, onJumpToReply, ask: stopConfirm.ask };
  });
  const [value] = useState(() => {
    const peek = BasePopover.createHandle<string>();
    return {
      store,
      peek,
      actions: {
        // What it lands on is in the conversation: the peek and an open transcript make way.
        locate: (to: string, expand?: boolean) => {
          peek.close();
          setPinned((p) => (p?.open ? { ...p, open: false } : p));
          handlers.current.onLocate(to, expand);
        },
        jumpToReply: (key: string) => handlers.current.onJumpToReply(key),
        stop: (s: Subagent) => handlers.current.ask(s),
        open: (to: string, anchor: Element | null) => {
          peek.close();
          setPinned({ id: to, anchor, back: [], full: false, open: true });
        },
      } satisfies ScopeActions,
    };
  });
  const stopName = stopConfirm.target ? `“${stopConfirm.target.name}”` : '';
  // Its stop confirmation sits outside it: a press there is not a press away from the transcript.
  const close = () => {
    if (!stopConfirm.target) setPinned((p) => (p ? { ...p, open: false } : p));
  };
  const closed = () => setPinned((p) => (p?.open ? p : null));
  // A subagent it spawned opens in its place, with the way back.
  const child = (to: string) => setPinned((p) => (p ? { ...p, id: to, back: [...p.back, p.id], full: true } : p));
  const back = () => setPinned((p) => (p?.back.length ? { ...p, id: p.back.at(-1)!, back: p.back.slice(0, -1) } : p));
  // One opened directly goes up to the one that spawned it.
  const up = (to: string) => setPinned((p) => (p ? { ...p, id: to, back: [], full: true } : p));
  return (
    <SubagentContext.Provider value={value}>
      {children}
      {!phone && <SubagentPeek handle={value.peek} />}
      {pinned && (phone
        ? <SubagentSheet pinned={pinned} onFull={() => setPinned((p) => (p ? { ...p, full: true } : p))} onClose={close} onClosed={closed} onChild={child} onBack={back} onUp={up} />
        : <SubagentPanel pinned={pinned} onClose={close} onClosed={closed} onChild={child} onBack={back} onUp={up} />)}
      <AlertDialog
        {...stopConfirm.props}
        title={`Stop subagent ${stopName}?`}
        description="It stops where it is. What it has done so far stays in its transcript; the main agent gets no result from it."
        confirmLabel="Stop subagent"
        onConfirm={() => {
          const target = stopConfirm.target?.id;
          stopConfirm.close();
          if (target) stopNow(target);
        }}
      />
    </SubagentContext.Provider>
  );
}

/** A reply's list disclosure, remembered like the turn line's; a `locate` for one of its subagents opens it. */
export function useSubagentDisclosure(key: string, subagents: readonly Subagent[]) {
  const [open, setOpen] = useDisclosure(key);
  const reveal = useScope((d) => d.reveal);
  const [seen, setSeen] = useState(reveal?.n);
  if (reveal && reveal.n !== seen) {
    setSeen(reveal.n);
    if (!open && subagents.some((s) => s.parent_tool_call_id === reveal.toolCallId)) setOpen(true);
  }
  return [open, setOpen] as const;
}

// Identity tones as whole class names, so Tailwind sees them.
const DOT: Record<IdentityTone, string> = { violet: 'bg-badge-violet', pink: 'bg-badge-pink', cyan: 'bg-badge-cyan', amber: 'bg-badge-amber', teal: 'bg-badge-teal' };
const PART_TONE: Record<CountPart['tone'], string> = { muted: 'text-muted', success: 'text-success', error: 'text-error', accent: 'text-accent' };
const STATUS_WORD: Record<SubagentStatus, string> = { running: 'running', idle: 'idle', completed: 'completed', failed: 'failed', cancelled: 'stopped' };
const STATUS_LABEL: Record<SubagentStatus, string> = { running: 'Running', idle: 'Idle', completed: 'Done', failed: 'Failed', cancelled: 'Stopped' };
const STATUS_TONE: Record<SubagentStatus, string> = { running: 'text-accent', idle: 'text-muted', completed: 'text-success', failed: 'text-error', cancelled: 'text-muted' };

/** "3 subagents · 2 done · 1 failed", with each count in its tone. */
function Counts({ c, lead }: Readonly<{ c: SubagentCounts; lead?: ReactNode }>) {
  return (
    <>
      {lead}
      {countParts(c).map((p, k) => (
        <span key={p.text} className={PART_TONE[p.tone]}>
          {(lead || k > 0) && ' · '}
          {p.text}
        </span>
      ))}
    </>
  );
}

/** A subagent's state as a glyph (AgentChip's, without the word): the working mark, a check, a cross, the idle glyph, a dash. */
function SubagentMark({ status }: Readonly<{ status: SubagentStatus }>) {
  switch (status) {
    case 'running':
      return <WorkingMark />;
    case 'completed':
      return <Check aria-hidden="true" className="size-3.5 shrink-0 text-success" strokeWidth={2.5} />;
    case 'failed':
      return <X aria-hidden="true" className="size-3.5 shrink-0 text-error" strokeWidth={2.5} />;
    case 'idle':
      return <SubagentIdleIcon className="shrink-0 text-muted" />;
    default:
      return <Minus aria-hidden="true" className="size-3.5 shrink-0 text-faint" strokeWidth={2.5} />;
  }
}

/** What a subagent is doing or reported (`subagentSummary`): its line in the index and the peek. */
function rowSummary(d: Summaries, s: Subagent): string {
  const steps = d.agents[s.id]?.items ?? (d.agentSteps[s.id] ? [d.agentSteps[s.id]] : undefined);
  return subagentSummary(s, steps, d.calls.get(s.parent_tool_call_id ?? '')?.tool);
}

/** The time now, every second while `on`. */
function useNow(on: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!on) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [on]);
  return now;
}

/** How long it took, or, while it runs, how long it has been running (in `accent`). */
function Took({ subagent: s, className }: Readonly<{ subagent: Subagent; className?: string }>) {
  const running = s.status === 'running';
  const now = useNow(running && !!s.started_at);
  let text = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  if (running && s.started_at) text = duration(s.started_at, new Date(Math.max(now, ms(s.started_at))).toISOString());
  return <span className={cn('shrink-0 text-meta tabular-nums', running ? 'text-accent' : 'text-muted', className)}>{text ?? ''}</span>;
}

/** "2m 40s · 174K tokens · 19 tool calls", from what is known. */
function usage(s: Subagent): string {
  const took = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  return [took, s.tokens ? `${compactTokens(s.tokens)} tokens` : '', s.tool_calls ? `${s.tool_calls} tool ${s.tool_calls === 1 ? 'call' : 'calls'}` : ''].filter(Boolean).join(' · ');
}

/**
 * A reply's subagents on its turn line (DESIGN.md subagent chip): identity dots (the `Bot` glyph
 * past five calls), then "3 subagents · 1.2M tokens · 2 done · 1 failed", and "2 of 3 loaded" while
 * some of the reply's calls have no subagent here yet. It opens the reply's list under the line.
 */
export function SubagentChip({ subagents, calls, tones, open, controls, onToggle }: Readonly<{ subagents: Subagent[]; /** The reply's `task` calls, loaded or not. */ calls: number; tones: ReadonlyMap<string, IdentityTone>; open: boolean; controls?: string; onToggle: () => void }>) {
  const c = countSubagents(subagents);
  const total = Math.max(calls, subagents.length);
  const tokens = totalTokens(subagents);
  const lead = [subagentNoun(total), tokens ? `${compactTokens(tokens)} tokens` : ''].filter(Boolean).join(' · ');
  const partial = subagents.length < total ? `${subagents.length} of ${total} loaded` : '';
  return (
    <button
      type="button"
      data-subagent-items={JSON.stringify(subagents.flatMap((s) => (s.parent_tool_call_id ? [s.parent_tool_call_id] : [])))}
      aria-expanded={open}
      aria-controls={controls}
      title={[lead, partial, ...countParts(c).map((p) => p.text)].filter(Boolean).join(' · ')}
      className={cn('-mx-1.5 flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-left text-caption transition-colors duration-100 hover:bg-tint-well hover:text-body pointer-coarse:min-h-11', open ? 'text-body' : 'text-muted')}
      onClick={onToggle}
    >
      {total <= IDENTITY_LIMIT ? (
        <span aria-hidden="true" className="flex shrink-0 items-center gap-1">
          {subagents.map((s) => {
            const tone = tones.get(s.id);
            return <span key={s.id} className={cn('size-1.5 rounded-full', tone ? DOT[tone] : 'bg-faint')} />;
          })}
        </span>
      ) : (
        <Bot aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
      )}
      <span className="min-w-0 truncate tabular-nums">
        <Counts c={c} lead={partial ? <>{lead} · <span className="text-muted">{partial}</span></> : lead} />
      </span>
      <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
    </button>
  );
}

/** Rows a list draws past a filter's reach, and the columns they flow into. */
const COLUMNS = 'columns-[20rem] gap-x-6';

/**
 * Subagents as one-line rows flowing into columns as wide as the conversation allows (one on a
 * phone): a family (a subagent and the ones it spawned) at a time, never split across columns,
 * failed families first, then running ones (lib/subagents `families`). `limit` families render
 * until "Show 50 more".
 */
function Families({ list, tones, anchor, limit, onMore }: Readonly<{ list: Family[]; tones: ReadonlyMap<string, IdentityTone>; anchor: boolean; limit: number; onMore: () => void }>) {
  return (
    <>
      {/* No more columns than families, so a few rows take the width (up to 36rem) instead of leaving empty columns. */}
      <ul className={COLUMNS} style={{ columnCount: Math.min(list.length, limit) }}>
        {list.slice(0, limit).map((f) => (
          <li key={f.head.id} className="max-w-xl break-inside-avoid">
            {f.rows.map(({ subagent, depth }) => (
              <SubagentRow key={subagent.id} subagent={subagent} tone={tones.get(subagent.id)} depth={depth} anchor={anchor} />
            ))}
          </li>
        ))}
      </ul>
      {list.length > limit && (
        <button type="button" className="flex h-7 w-fit items-center px-2 text-left text-caption text-accent hover:underline pointer-coarse:min-h-11" onClick={onMore}>
          Show {Math.min(PAGE_ROWS, list.length - limit)} more
        </button>
      )}
    </>
  );
}

/**
 * A reply's subagents (DESIGN.md subagent list): their rows in columns, failed families first;
 * past twelve a filter by name or line. A `locate` for one of them clears the filter and pages to
 * it. Reused by Detailed for a reply past eight.
 */
export function SubagentList({ id, subagents, calls = subagents.length, tones }: Readonly<{ id: string; subagents: Subagent[]; calls?: number; tones: ReadonlyMap<string, IdentityTone> }>) {
  const [query, setQuery] = useState('');
  const [limit, setLimit] = useState(PAGE_ROWS);
  const reveal = useScope((d) => d.reveal);
  // Only while a filter is set does the list read every row's line.
  const filtering = useScope((d) => (query ? d.summaries : null));
  const all = useMemo(() => families(subagents), [subagents]);
  // A reveal from before this list mounted is not replayed (`locate` asks again once it has opened what holds the list).
  const [seen, setSeen] = useState(reveal?.n);
  if (reveal && reveal.n !== seen) {
    setSeen(reveal.n);
    const at = all.findIndex((f) => f.rows.some((r) => r.subagent.parent_tool_call_id === reveal.toolCallId));
    if (at >= 0) {
      setQuery('');
      setLimit((l) => Math.max(l, at + 1));
    }
  }
  const shown = filtering ? all.filter((f) => f.rows.some((r) => matches(r.subagent, rowSummary(filtering, r.subagent), query))) : all;
  const total = Math.max(calls, subagents.length);
  return (
    <section id={id} aria-label={subagentNoun(total)} className="flex flex-col">
      {subagents.length > FILTER_OVER && (
        <Input size="sm" type="search" value={query} onChange={(e) => setQuery(e.target.value)} aria-label={`Filter ${subagentNoun(subagents.length)} by name or result`} placeholder={`Filter ${subagents.length} by name or result`} className="mb-1 max-w-80 text-caption" />
      )}
      <Families list={shown} tones={tones} anchor limit={limit} onMore={() => setLimit(limit + PAGE_ROWS)} />
      {query && !shown.length && <p className="px-2 py-1 text-caption text-muted">No subagent matches “{query}”.</p>}
      {subagents.length < total && <p className="px-2 py-1 text-caption text-muted">{total - subagents.length} more not loaded here: the header’s Subagents list reads older ones from the record.</p>}
    </section>
  );
}

/**
 * One subagent (DESIGN.md subagent row): one line of its state glyph, identity dot (while its
 * reply has at most five), name, duration and tokens; a failed one adds its error under it. One
 * spawned by another is indented under it. Hovering it peeks (after a moment); a click or a tap
 * opens its transcript. In a reply's list (`anchor`) it carries its `task` call's id, so
 * `locate` lands on it. Memoised: it renders again only when what it shows changes.
 */
export const SubagentRow = memo(function SubagentRow({ subagent: s, tone, depth = 0, anchor = false }: { subagent: Subagent; tone?: IdentityTone; depth?: number; anchor?: boolean }) {
  const scope = useContext(SubagentContext);
  const phone = useScope((d) => d.phone) ?? false;
  const open = useScope((d) => d.pinned === s.id) ?? false;
  const stopError = useScope((d) => d.stops[s.id]?.error) ?? null;
  // Keyboard focus peeks too, after the same moment; the peek never takes the focus.
  const triggerId = useId();
  const focusTimer = useRef(0);
  useEffect(() => () => window.clearTimeout(focusTimer.current), []);
  if (!scope) return null;
  const name = s.name || 'Subagent';
  const parentId = s.parent_tool_call_id;
  const failure = s.status === 'failed' ? s.error : '';
  const props = {
    type: 'button' as const,
    'data-subagent-toggle': '',
    'aria-haspopup': 'dialog' as const,
    'aria-label': `${name}, ${STATUS_WORD[s.status]}${failure ? `: ${failure}` : ''}`,
    className: cn('flex min-h-7 w-full items-center gap-2 rounded-sm py-0.5 pr-1.5 text-left text-caption text-body transition-colors duration-100 hover:bg-tint-well pointer-coarse:min-h-11', open && 'bg-tint-selected hover:bg-tint-selected'),
    style: { paddingLeft: `${8 + depth * 18}px` },
  };
  const line = (
    <>
      {depth > 0 && <CornerDownRight aria-hidden="true" className="-ml-1 size-3 shrink-0 text-faint" />}
      <span className="flex size-4 shrink-0 items-center justify-center">
        <SubagentMark status={s.status} />
      </span>
      {tone && <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', DOT[tone])} />}
      <span className="min-w-0 flex-1 truncate">{name}</span>
      <Took subagent={s} className="w-14 text-right" />
      <span className="w-10 shrink-0 text-right text-meta tabular-nums text-muted">{s.tokens ? compactTokens(s.tokens) : ''}</span>
    </>
  );
  return (
    <div id={anchor && parentId ? `item-${parentId}` : undefined} data-subagent-row="" className="flex flex-col">
      {phone ? (
        <button {...props} onClick={(e) => scope.actions.open(s.id, e.currentTarget)}>
          {line}
        </button>
      ) : (
        <BasePopover.Trigger
          {...props}
          id={triggerId}
          handle={scope.peek}
          payload={s.id}
          openOnHover
          delay={400}
          closeDelay={120}
          onFocus={(e) => {
            if (!e.currentTarget.matches(':focus-visible')) return;
            window.clearTimeout(focusTimer.current);
            focusTimer.current = window.setTimeout(() => scope.peek.open(triggerId), 400);
          }}
          onBlur={() => {
            window.clearTimeout(focusTimer.current);
            // Tab may go on into the peek (its actions, through its focus guards); anywhere else closes it.
            focusTimer.current = window.setTimeout(() => {
              if (document.activeElement?.closest('[data-subagent-peek]')) return;
              if (scope.peek.isOpen && document.getElementById(triggerId)?.hasAttribute('data-popup-open')) scope.peek.close();
            });
          }}
          onClick={(e) => {
            e.preventBaseUIHandler();
            scope.actions.open(s.id, e.currentTarget);
          }}
        >
          {line}
        </BasePopover.Trigger>
      )}
      {failure && (
        <p className="truncate pr-2 pb-1 text-caption text-error" style={{ paddingLeft: `${32 + depth * 18}px` }} title={failure}>
          {failure}
        </p>
      )}
      {stopError && (
        <p role="alert" className="pr-2 pb-1 text-caption text-error" style={{ paddingLeft: `${32 + depth * 18}px` }}>
          Could not stop it: {stopError}
        </p>
      )}
    </div>
  );
});

/** Who spawned it, and when: "Spawned 06:40 by the main agent". */
function spawnedLine(s: Subagent, subagents: readonly Subagent[]): string {
  const by = s.parent_agent_id ? subagents.find((x) => x.id === s.parent_agent_id)?.name || 'another subagent' : 'the main agent';
  const at = clock(s.runs?.[0]?.started_at || s.started_at);
  return `Spawned ${at ? `${at} ` : ''}by ${by}`;
}

/** A quiet text action: the peek's and the transcript's links. */
const LINK = 'rounded-xs text-caption text-accent hover:underline pointer-coarse:min-h-11';

/** A side action of the peek and the transcript (Show where it was spawned, Copy agent ID): a small filled button, 24px tall. */
function SideAction({ onClick, children }: Readonly<{ onClick: () => void; children: ReactNode }>) {
  return (
    <Button size="sm" variant="secondary" className="h-6 font-normal text-caption text-body" onClick={onClick}>
      {children}
    </Button>
  );
}

/** The last few steps of a running subagent: its transcript's newest items, one line each. */
function LatestSteps({ subagent: s }: Readonly<{ subagent: Subagent }>) {
  const detail = useDetailAgent(s.id, true);
  const items = (detail.agent?.items ?? []).filter((item) => item.kind === 'tool' || item.kind === 'reasoning' || (item.kind === 'assistant' && item.text?.trim())).slice(-4);
  const step = useScope((d) => d.summaries.agentSteps[s.id]);
  const lines = items.length ? items : step ? [step] : [];
  if (!lines.length) return <p className="text-caption text-muted">Nothing recorded yet.</p>;
  return (
    <div className="flex flex-col gap-0.5 rounded-md bg-surface px-2.5 py-2 font-mono text-code-sm text-body shadow-well">
      {lines.map((item) => {
        let text: string;
        if (item.kind === 'tool') {
          const { name, arg } = toolLabel(item.tool);
          text = arg ? `${name} ${arg}` : name;
        } else if (item.kind === 'reasoning') text = item.ended_at ? 'Thought' : 'Thinking…';
        else text = (item.text ?? '').trim().split('\n')[0];
        return <span key={item.id} className="truncate">{text}</span>;
      })}
    </div>
  );
}

/**
 * The peek (DESIGN.md subagent peek): name and state, how long, tokens and tool calls, model and
 * runs; while it runs its last steps, once done its line, when failed its error; who spawned it
 * and when; Stop while it runs, "Full transcript" and Copy agent ID. On a phone it is the sheet's
 * first state, without Copy agent ID and "Show where it was spawned".
 */
function PeekBody({ id, onFull, phone = false }: Readonly<{ id: string; onFull: () => void; phone?: boolean }>) {
  const actions = useActions();
  const { meta } = useApp();
  const [copied, copy] = useCopied();
  const s = useScope((d) => d.subagents.find((x) => x.id === id));
  const subagents = useScope((d) => d.subagents);
  const provider = useScope((d) => d.task.provider) ?? '';
  const summary = useScope((d) => (s ? rowSummary(d.summaries, s) : '')) ?? '';
  const stopping = useScope((d) => d.stops[id]) ?? NOT_STOPPING;
  const locked = useScope((d) => readOnly(d.task)) ?? true;
  if (!s || !actions || !subagents) return null;
  const runs = s.runs?.length ?? 0;
  const setup = [s.model ? modelName(meta, provider, s.model) : '', runs > 1 ? runCount(s) : ''].filter(Boolean).join(' · ');
  const parentId = s.parent_tool_call_id;
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-start gap-2">
        <span className="flex size-5 shrink-0 items-center justify-center">
          <SubagentMark status={s.status} />
        </span>
        {/* The state word sits by the name, so the facts below take the whole width: one line, model included. */}
        <div className="min-w-0 flex-1">
          <div className="flex items-baseline gap-2">
            <p className="min-w-0 flex-1 truncate text-ui font-medium text-ink">{s.name || 'Subagent'}</p>
            <span className={cn('shrink-0 text-caption', STATUS_TONE[s.status])}>{STATUS_LABEL[s.status]}</span>
          </div>
          <p className="text-caption tabular-nums text-muted">
            {s.status === 'running' ? <Took subagent={s} /> : null}
            {[usage(s), setup].filter(Boolean).map((part, i) => (
              <Fragment key={i}>{(i > 0 || s.status === 'running') && ' · '}<span className="whitespace-nowrap">{part}</span></Fragment>
            ))}
          </p>
        </div>
      </div>
      {s.status === 'failed' && <p className="rounded-md bg-error-wash px-2.5 py-1.5 font-mono text-code-sm text-error">{s.error || 'Failed.'}</p>}
      {s.status === 'running' && <LatestSteps subagent={s} />}
      {s.status !== 'running' && s.status !== 'failed' && summary && <p className="line-clamp-4 text-caption text-body">{summary}</p>}
      <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-caption text-muted">
        <span>{spawnedLine(s, subagents)}</span>
        {!phone && parentId && <SideAction onClick={() => actions.locate(parentId)}>Show where it was spawned</SideAction>}
      </div>
      <div className="flex flex-wrap items-center gap-3 border-t border-hairline pt-2">
        {s.status === 'running' && (
          <Button size="sm" variant="danger" aria-label={`Stop subagent ${s.name}`} loading={stopping.busy} disabled={locked || stopping.requested} onClick={() => actions.stop(s)}>
            <Square className="!size-3" fill="currentColor" />
            {stopping.requested ? 'Stop requested' : 'Stop'}
          </Button>
        )}
        <button type="button" className={LINK} onClick={onFull}>
          Full transcript ›
        </button>
        <span className="flex-1" />
        {!phone && <SideAction onClick={() => copy(s.id)}>{copied ? 'Copied' : 'Copy agent ID'}</SideAction>}
      </div>
    </div>
  );
}

/** The one peek of the Task: opened by hovering any row (a detached trigger), it shows that row's subagent. */
function SubagentPeek({ handle }: Readonly<{ handle: BasePopover.Handle<string> }>) {
  const scope = useContext(SubagentContext);
  return (
    <BasePopover.Root handle={handle}>
      {({ payload }) =>
        payload ? (
          <BasePopover.Portal>
            {/* Beside the row (below it only without room at either side), so the rows under it stay in reach of the pointer. */}
            <BasePopover.Positioner side="right" align="start" sideOffset={8} collisionPadding={8} className="z-50 outline-hidden">
              <BasePopover.Popup data-popup="" data-subagent-peek="" aria-label="Subagent" initialFocus={false} className={cn(popupClass, 'w-96 max-w-(--available-width) px-3 py-2.5 text-body')}>
                <PeekBody
                  id={payload}
                  onFull={() => {
                    const row = document.querySelector(`[data-subagent-row] [aria-expanded][data-popup-open], [data-subagent-row] [data-popup-open]`);
                    scope?.actions.open(payload, row);
                  }}
                />
              </BasePopover.Popup>
            </BasePopover.Positioner>
          </BasePopover.Portal>
        ) : null
      }
    </BasePopover.Root>
  );
}

/**
 * An open transcript's head and body, in the desktop panel and the phone sheet: the way up (to
 * the subagent it was opened through, else the one that spawned it), state glyph, name, Stop while
 * it runs and close; what it was asked (two lines); how long, tokens, tool calls and model; on
 * desktop "Show where it was spawned" and Copy agent ID; a strip of its runs (each shows its start)
 * and of the subagents it spawned (each opens in its place); then its transcript, which scrolls on its own.
 */
function TranscriptBody({ pinned, phone, onChild, onBack, onUp, close }: Readonly<{ pinned: Pinned; phone: boolean; onChild: (id: string) => void; onBack: () => void; /** Opens the subagent that spawned this one in its place. */ onUp: (id: string) => void; close: ReactNode }>) {
  const actions = useActions();
  const { meta } = useApp();
  const [copied, copy] = useCopied();
  const task = useScope((d) => d.task);
  const subagents = useScope((d) => d.subagents);
  const s = subagents?.find((x) => x.id === pinned.id);
  const transcript = useScope((d) => d.agents[pinned.id]);
  const snapshotSeq = useScope((d) => d.snapshotSeq) ?? 0;
  const parent = useScope((d) => d.summaries.calls.get(s?.parent_tool_call_id ?? ''));
  const stopping = useScope((d) => d.stops[pinned.id]) ?? NOT_STOPPING;
  const [seek, setSeek] = useState<Seek | null>(null);
  if (!s || !task || !actions || !subagents) return null;
  const name = s.name || 'Subagent';
  const from = pinned.back.length ? subagents.find((x) => x.id === pinned.back.at(-1)) : undefined;
  // The way up: the one it was opened through, else (opened directly) the one that spawned it.
  const spawner = !pinned.back.length && s.parent_agent_id ? subagents.find((x) => x.id === s.parent_agent_id) : undefined;
  const up = from ?? spawner;
  const spawned = subagents.filter((x) => x.parent_agent_id === s.id);
  const runs = runLines(s);
  const facts = [usage(s), s.model ? modelName(meta, task.provider, s.model) : ''].filter(Boolean).join(' · ');
  const parentId = s.parent_tool_call_id;
  return (
    <>
      <div className="flex shrink-0 flex-col gap-1.5 px-4 pt-3 pb-2">
        {up && (
          <button type="button" className={cn(LINK, 'flex w-fit items-center gap-1')} title={spawner ? `Spawned by ${spawner.name || 'Subagent'}` : undefined} onClick={from ? onBack : () => onUp(up.id)}>
            <ArrowLeft aria-hidden="true" className="size-3" />
            {up.name || 'Subagent'}
          </button>
        )}
        <div className="flex items-center gap-2">
          <span className="flex size-5 shrink-0 items-center justify-center">
            <SubagentMark status={s.status} />
          </span>
          <h2 className="min-w-0 flex-1 truncate text-ui font-semibold text-ink">{name}</h2>
          <StopSubagent session={task} subagent={s} stopping={stopping} onStop={() => actions.stop(s)} />
          {close}
        </div>
        {s.description && <p className="line-clamp-2 pl-7 text-caption text-body" title={s.description}>{s.description}</p>}
        <p className="pl-7 text-caption tabular-nums text-muted">
          <span className={STATUS_TONE[s.status]}>{STATUS_LABEL[s.status]}</span>
          {s.status === 'running' && <> · <Took subagent={s} /></>}
          {facts && ` · ${facts}`}
        </p>
        {!phone && (
          <div className="flex flex-wrap items-center gap-1.5 pl-7">
            {parentId && <SideAction onClick={() => actions.locate(parentId)}>Show where it was spawned</SideAction>}
            <SideAction onClick={() => copy(s.id)}>{copied ? 'Copied' : 'Copy agent ID'}</SideAction>
          </div>
        )}
        {(runs.length > 0 || spawned.length > 0) && (
          <div className="flex flex-wrap items-center gap-1.5 pl-7">
            {runs.map((r, i) => {
              const at = clock(r.started_at);
              return (
                <Fragment key={i}>
                  {r.gapBefore && <span className="text-meta text-faint">Earlier runs not kept · latest runs</span>}
                  <button
                    type="button"
                    disabled={!r.started_at}
                    title={r.started_at ? `${r.trigger} · ${at}` : 'When this run started was not recorded, so it cannot be found in the transcript.'}
                    className="h-6 rounded-full bg-tint-well px-2 text-meta tabular-nums text-body transition-colors duration-100 hover:bg-tint-hover disabled:hover:bg-tint-well pointer-coarse:min-h-11"
                    onClick={() => setSeek((x) => ({ time: r.started_at!, first: i === 0, running: r.running, n: (x?.n ?? 0) + 1 }))}
                  >
                    {r.n === null ? 'Run' : `Run ${r.n}`} · <span className={PART_TONE[r.tone]}>{r.outcome}</span>
                  </button>
                </Fragment>
              );
            })}
            {spawned.map((x) => (
              <button key={x.id} type="button" className="flex h-6 items-center gap-1.5 rounded-full bg-tint-well px-2 text-meta text-body transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11" onClick={() => onChild(x.id)}>
                <Bot aria-hidden="true" className="size-3 text-muted" />
                {x.name || 'Subagent'}
                <span className="text-accent">Open</span>
              </button>
            ))}
          </div>
        )}
      </div>
      <div className="flex min-h-0 flex-1 flex-col border-t border-hairline bg-surface shadow-well">
        <AgentTranscriptView
          key={s.id}
          name={name}
          provider={task.provider}
          sessionId={task.id}
          workdir={task.workdir}
          subagent={s}
          interactions={task.interactions}
          transcript={transcript}
          snapshotSeq={snapshotSeq}
          open
          parent={parent}
          result={(s.status === 'completed' || s.status === 'idle') && !s.background ? parent?.tool?.output : undefined}
          seek={seek}
        />
      </div>
    </>
  );
}

/** Whether two messages say the same once spacing is ignored. */
function sameText(a: string, b: string): boolean {
  const flat = (t: string) => t.replace(/\s+/g, ' ').trim();
  return flat(a) === flat(b);
}

/**
 * The open transcript on desktop (DESIGN.md subagent transcript): a 640px panel beside the row it
 * was opened from (right of it, else wherever it fits), at most 70vh tall, over a dimmed page; Esc,
 * the close button or a click outside closes it.
 */
function SubagentPanel({ pinned, onClose, onClosed, onChild, onBack, onUp }: Readonly<{ pinned: Pinned; onClose: () => void; onClosed: () => void; onChild: (id: string) => void; onBack: () => void; onUp: (id: string) => void }>) {
  const anchor = pinned.anchor?.isConnected ? pinned.anchor : document.getElementById('subagents-link');
  return (
    <BasePopover.Root open={pinned.open} modal onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BasePopover.Portal>
        <BasePopover.Backdrop className={backdropClass} />
        <BasePopover.Positioner anchor={anchor} side="right" align="start" sideOffset={8} collisionPadding={12} className="z-50 outline-hidden">
          {/* Below 1024px no row leaves 640px beside it: the panel takes the width of the page. */}
          <BasePopover.Popup data-popup="" aria-label="Subagent transcript" aria-modal="true" className="flex max-h-[min(70vh,var(--available-height))] w-[640px] max-w-(--available-width) max-lg:w-[calc(100vw-24px)] flex-col overflow-hidden rounded-lg bg-raised text-body shadow-modal outline-hidden transition-[opacity,scale] duration-160 ease-app data-starting-style:scale-[0.98] data-starting-style:opacity-0 data-ending-style:scale-[0.98] data-ending-style:opacity-0">
            <TranscriptBody
              pinned={pinned}
              phone={false}
              onChild={onChild}
              onBack={onBack}
              onUp={onUp}
              close={
                <BasePopover.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
                  <X />
                </BasePopover.Close>
              }
            />
          </BasePopover.Popup>
        </BasePopover.Positioner>
      </BasePopover.Portal>
    </BasePopover.Root>
  );
}

/**
 * A subagent on a phone: a sheet from the bottom over a dimmed page, first its peek, then, after
 * "Full transcript", 85% of the screen with its transcript. Dragging its handle down or the close
 * button closes it.
 */
function SubagentSheet({ pinned, onFull, onClose, onClosed, onChild, onBack, onUp }: Readonly<{ pinned: Pinned; onFull: () => void; onClose: () => void; onClosed: () => void; onChild: (id: string) => void; onBack: () => void; onUp: (id: string) => void }>) {
  const popup = useRef<HTMLDivElement>(null);
  const drag = useRef<{ y: number; dy: number } | null>(null);
  const follow = (dy: number) => {
    if (popup.current) popup.current.style.transform = dy > 0 ? `translateY(${dy}px)` : '';
  };
  const close = (
    <BaseDialog.Close render={<Button size="icon-sm" aria-label="Close" className="text-muted" />}>
      <X />
    </BaseDialog.Close>
  );
  return (
    <BaseDialog.Root open={pinned.open} onOpenChange={(o) => !o && onClose()} onOpenChangeComplete={(o) => !o && onClosed()}>
      <BaseDialog.Portal>
        <BaseDialog.Backdrop className={backdropClass} />
        <BaseDialog.Popup
          ref={popup}
          data-popup=""
          aria-label="Subagent"
          className={cn('fixed inset-x-0 bottom-0 z-50 flex max-h-[85dvh] flex-col overflow-hidden rounded-t-lg bg-raised pb-[env(safe-area-inset-bottom)] text-body shadow-modal outline-hidden transition-transform duration-240 ease-app data-starting-style:translate-y-full data-ending-style:translate-y-full', pinned.full && 'h-[85dvh]')}
        >
          <div
            aria-hidden="true"
            className="flex h-6 shrink-0 touch-none items-center justify-center"
            onPointerDown={(e) => {
              drag.current = { y: e.clientY, dy: 0 };
              e.currentTarget.setPointerCapture(e.pointerId);
            }}
            onPointerMove={(e) => {
              if (!drag.current) return;
              drag.current.dy = e.clientY - drag.current.y;
              follow(drag.current.dy);
            }}
            onPointerUp={() => {
              const dy = drag.current?.dy ?? 0;
              drag.current = null;
              follow(0);
              if (dy > 80) onClose();
            }}
            onPointerCancel={() => {
              drag.current = null;
              follow(0);
            }}
          >
            <span className="h-1 w-9 rounded-full bg-hairline-strong" />
          </div>
          {pinned.full ? (
            <TranscriptBody pinned={pinned} phone onChild={onChild} onBack={onBack} onUp={onUp} close={close} />
          ) : (
            <div className="flex flex-col gap-2 overflow-y-auto px-4 pb-4">
              <div className="flex justify-end">{close}</div>
              <PeekBody id={pinned.id} phone onFull={onFull} />
            </div>
          )}
        </BaseDialog.Popup>
      </BaseDialog.Portal>
    </BaseDialog.Root>
  );
}

/** Done, failed and running over the whole set, from the left, on the well; the counts beside it say the same in words. */
function Progress({ c }: Readonly<{ c: SubagentCounts }>) {
  const scale = (n: number) => ({ transform: `scaleX(${n / c.total})` });
  const bar = 'absolute inset-0 origin-left transition-transform duration-240 ease-app';
  return (
    <div aria-hidden="true" className="relative h-1.5 w-40 overflow-hidden rounded-full bg-tint-well">
      <div className={cn(bar, 'bg-accent opacity-45')} style={scale(c.done + c.failed + c.running)} />
      <div className={cn(bar, 'bg-error')} style={scale(c.done + c.failed)} />
      <div className={cn(bar, 'bg-success')} style={scale(c.done)} />
    </div>
  );
}

const NO_LIVE: LiveSubagent[] = [];

/**
 * The live set (DESIGN.md live subagents), at the conversation's foot while one of its subagents
 * runs or its transcript is open: the latest reply's subagents and any earlier one still or again
 * running, counted in a head line with their tokens (a progress bar past five), then their rows in
 * columns like a reply's list, failed and running families first. A subagent failing is said
 * once, politely.
 */
export function LiveSubagents() {
  const set = useScope((d) => d.live) ?? NO_LIVE;
  const tones = useScope((d) => d.replies.tones);
  const [limit, setLimit] = useState(PAGE_ROWS);
  const list = useMemo(() => families(set.map((x) => x.subagent)), [set]);
  // Failures said aloud: the ones seen failed already, and the last sentence.
  const failedNow = set.filter((x) => x.subagent.status === 'failed').map((x) => x.subagent.id).join(',');
  const [failedSeen, setFailedSeen] = useState(failedNow);
  const [announced, setAnnounced] = useState('');
  if (failedNow !== failedSeen) {
    const fresh = set.find((x) => x.subagent.status === 'failed' && !failedSeen.split(',').includes(x.subagent.id));
    setFailedSeen(failedNow);
    if (fresh) setAnnounced(`Subagent ${fresh.subagent.name || 'Subagent'} failed.`);
  }
  // The set left: it comes back at its first page.
  if (!set.length && limit !== PAGE_ROWS) setLimit(PAGE_ROWS);
  const status = (
    <p role="status" className="sr-only">
      {announced}
    </p>
  );
  if (!set.length || !tones) return status;
  const subagents = set.map((x) => x.subagent);
  const c = countSubagents(subagents);
  const tokens = totalTokens(subagents);
  const lead = [c.running === c.total ? `${subagentNoun(c.total)} running` : subagentNoun(c.total), tokens ? `${compactTokens(tokens)} tokens` : ''].filter(Boolean).join(' · ');
  return (
    <>
      {status}
      <section aria-label="Subagents at work" className="animate-rise flex flex-col gap-1">
        <header className="flex min-h-7 flex-wrap items-center gap-x-3 gap-y-1 px-2 text-caption tabular-nums">
          <Bot aria-hidden="true" className="size-4 shrink-0 text-muted" />
          <span>
            <span className="font-semibold text-ink">{lead}</span>
            {c.running < c.total && (
              <>
                {' · '}
                <Counts c={c} />
              </>
            )}
          </span>
          {c.total > IDENTITY_LIMIT && <Progress c={c} />}
        </header>
        <Families list={list} tones={tones} anchor={false} limit={limit} onMore={() => setLimit(limit + PAGE_ROWS)} />
      </section>
    </>
  );
}

const FILTERS: { key: IndexFilter; label: string; tone: string }[] = [
  { key: 'all', label: 'All', tone: 'text-body' },
  { key: 'running', label: 'Running', tone: 'text-accent' },
  { key: 'failed', label: 'Failed', tone: 'text-error' },
  { key: 'done', label: 'Done', tone: 'text-body' },
];
/** Rows the header index renders before "Show 100 more". */
const INDEX_ROWS = 100;

/**
 * The Task header's subagents (DESIGN.md subagent index): the count, the working mark while one
 * runs, and a popover to find any of them: search, status filters, the subagents grouped by the
 * message that started their reply (newest first, each with Jump), the ones not placed in the
 * conversation and the older ones from the record. Picking a placed one scrolls to its row and
 * opens its transcript; picking one not placed opens its transcript by the header.
 */
export function SubagentIndex({ labels, error }: Readonly<{ labels: boolean; /** Why the last jump did not land, said where the reader is. */ error?: string }>) {
  const actions = useActions();
  const subagents = useScope((d) => d.subagents);
  const before = useScope((d) => d.subagentsBefore);
  const [open, setOpen] = useState(false);
  // Where to go once the popover has closed; a pick keeps focus off the trigger (`finalFocus`), what it opens takes it.
  const picked = useRef<{ call: string; expand: boolean } | { agent: string } | null>(null);
  const input = useRef<HTMLInputElement>(null);
  if (!subagents || !actions) return null;
  const running = subagents.filter((s) => s.status === 'running').length;
  const label = `Subagents, ${subagents.length}${before ? ' or more' : ''}${running ? `, ${running} running` : ''}`;
  const pick = (to: NonNullable<typeof picked.current>) => {
    picked.current = to;
    setOpen(false);
  };
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        if (o) picked.current = null;
        setOpen(o);
      }}
      onOpenChangeComplete={(o) => {
        const to = picked.current;
        if (o || !to) return;
        if ('agent' in to) actions.open(to.agent, document.getElementById('subagents-link'));
        else actions.locate(to.call, to.expand);
      }}
    >
      <Tip label="Subagents">
        <Popover.Trigger render={<Button id="subagents-link" size="md" aria-label={label} className="px-2 text-muted" />}>
          <Bot />
          {labels && <span>Subagents</span>}
          <span className="tabular-nums text-ink">
            {subagents.length}
            {before && '+'}
          </span>
          {running > 0 && <WorkingMark />}
          <ChevronDown className="text-muted" />
        </Popover.Trigger>
      </Tip>
      <Popover.Content side="bottom" align="end" sideOffset={4} initialFocus={input} finalFocus={() => !picked.current} aria-label="Subagents" className="w-[440px] max-w-(--available-width) gap-1.5 p-1.5">
        <IndexBody input={input} error={error} onPick={pick} />
      </Popover.Content>
    </Popover.Root>
  );
}

function IndexBody({ input, error, onPick }: Readonly<{ input: RefObject<HTMLInputElement | null>; error?: string; onPick: (to: { call: string; expand: boolean } | { agent: string }) => void }>) {
  const taskId = useScope((d) => d.task.id);
  const subagents = useScope((d) => d.subagents);
  const before = useScope((d) => d.subagentsBefore);
  const outline = useScope((d) => d.outline);
  const messages = useScope((d) => d.messages);
  const summaries = useScope((d) => d.summaries);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<IndexFilter>('all');
  const [limit, setLimit] = useState(INDEX_ROWS);
  const list = useRef<HTMLDivElement>(null);
  const groups = useMemo(() => (outline && messages && subagents ? indexGroups(outline, messages, subagents) : []), [outline, messages, subagents]);
  if (!taskId || !subagents || !summaries) return null;
  // The chips and the groups count what the search finds.
  const found = query ? subagents.filter((s) => matches(s, rowSummary(summaries, s), query)) : subagents;
  const all = countSubagents(found);
  const counts: Record<IndexFilter, number> = { all: all.total, running: all.running, failed: all.failed, done: all.done };
  // At most `limit` rows across the groups; the rest wait behind "Show more".
  let budget = limit;
  let hidden = 0;
  const shown: (IndexGroup & { rows: Family['rows']; matched: Subagent[] })[] = [];
  for (const g of groups) {
    const matched = g.subagents.filter((s) => inFilter(s, filter) && found.includes(s));
    // Each under the one that spawned it, as in the conversation.
    const rows = families(matched, false).flatMap((f) => f.rows);
    const take = rows.slice(0, Math.max(0, budget));
    hidden += rows.length - take.length;
    budget -= take.length;
    if (take.length) shown.push({ ...g, rows: take, matched });
  }
  // ArrowDown from the search box enters the rows; the arrows move between them, ArrowUp from the first goes back.
  const rowButtons = () => [...(list.current?.querySelectorAll<HTMLElement>('[data-index-row]') ?? [])];
  const onArrow = (e: ReactKeyboardEvent) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    const rows = rowButtons();
    const at = rows.indexOf(e.target as HTMLElement);
    if (e.target === input.current) {
      if (e.key !== 'ArrowDown' || !rows.length) return;
      rows[0].focus();
    } else if (at < 0) return;
    else if (e.key === 'ArrowDown') rows[Math.min(at + 1, rows.length - 1)].focus();
    else if (at === 0) input.current?.focus();
    else rows[at - 1].focus();
    e.preventDefault();
  };
  const reset = () => setLimit(INDEX_ROWS);
  return (
    <>
      {error && (
        <Note tone="warn" role="alert">
          {error}
        </Note>
      )}
      <Input ref={input} size="md" type="search" value={query} onChange={(e) => { setQuery(e.target.value); reset(); }} onKeyDown={onArrow} aria-label="Search subagents" placeholder={`Search ${subagents.length} subagents`} />
      <div role="group" aria-label="Show subagents by status" className="flex flex-wrap gap-1">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            aria-pressed={filter === f.key}
            className={cn('h-6 rounded-full px-2 text-caption tabular-nums transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11', filter === f.key ? 'bg-tint-selected text-ink' : counts[f.key] ? f.tone : 'text-muted')}
            onClick={() => { setFilter(f.key); reset(); }}
          >
            {f.label} {counts[f.key]}
          </button>
        ))}
      </div>
      {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- Arrow keys move between the row buttons inside. */}
      <div ref={list} onKeyDown={onArrow} className="-mx-1.5 max-h-[min(60vh,480px)] overflow-y-auto overscroll-contain px-1.5">
        {shown.map((g) => {
          const title = indexTitle(g.key, g.text);
          const first = g.subagents.find((s) => s.parent_tool_call_id)?.parent_tool_call_id;
          return (
            <section key={g.key} aria-label={title} className="flex flex-col py-0.5">
              <div className="flex h-8 items-center gap-2 px-2">
                <span className="min-w-0 truncate text-ui text-ink" title={g.text || undefined}>{title}</span>
                {g.time && <span className="shrink-0 text-meta tabular-nums text-muted">{clock(g.time)}</span>}
                <span className="flex-1" />
                {/* "48 · 2 failed · 15 running": what still needs a look, so the message keeps the room. */}
                <span className="shrink-0 text-meta whitespace-nowrap tabular-nums text-muted">
                  <Counts c={{ ...countSubagents(g.matched), done: 0, stopped: 0 }} lead={`${g.matched.length}`} />
                </span>
                {g.key && first && (
                  <button type="button" aria-label={`Jump to the subagents of ${title}`} className="shrink-0 rounded-xs text-caption text-accent hover:underline pointer-coarse:min-h-11" onClick={() => onPick({ call: first, expand: false })}>
                    Jump
                  </button>
                )}
              </div>
              <ul className="flex flex-col">
                {g.rows.map(({ subagent: s, depth }) => {
                  const summary = rowSummary(summaries, s);
                  return (
                    <li key={s.id}>
                      <button
                        type="button"
                        data-index-row=""
                        title={[s.name || 'Subagent', summary].filter(Boolean).join('\n')}
                        className="flex min-h-7 w-full items-center gap-2 rounded-sm py-0.5 pr-2 text-left text-caption transition-colors duration-100 hover:bg-tint-well pointer-coarse:min-h-11"
                        style={{ paddingLeft: `${8 + depth * 18}px` }}
                        // Not placed in the conversation (no call, or one past the history held): it opens by the header.
                        onClick={() => onPick(g.key && s.parent_tool_call_id ? { call: s.parent_tool_call_id, expand: true } : { agent: s.id })}
                      >
                        {depth > 0 && <CornerDownRight aria-hidden="true" className="-ml-1 size-3 shrink-0 text-faint" />}
                        <span className="flex size-4 shrink-0 items-center justify-center">
                          <SubagentMark status={s.status} />
                        </span>
                        <span className="max-w-[60%] min-w-0 shrink-0 truncate font-medium text-body">{s.name || 'Subagent'}</span>
                        <span className="sr-only">, {STATUS_WORD[s.status]}</span>
                        <span className={cn('min-w-0 flex-1 truncate', s.status === 'failed' ? 'text-error' : 'text-muted')}>{summary}</span>
                        <Took subagent={s} />
                        {s.tokens ? <span className="shrink-0 text-meta tabular-nums text-muted">{compactTokens(s.tokens)}</span> : null}
                      </button>
                    </li>
                  );
                })}
              </ul>
            </section>
          );
        })}
        {!shown.length && <p className="px-2 py-2 text-caption text-muted">{query ? `No subagent matches “${query}”.` : 'No subagents in this state.'}</p>}
        {hidden > 0 && (
          <button type="button" className="flex h-8 w-full items-center px-2 text-left text-caption text-accent hover:underline pointer-coarse:min-h-11" onClick={() => setLimit(limit + INDEX_ROWS)}>
            Show {Math.min(INDEX_ROWS, hidden)} more
          </button>
        )}
      </div>
      {before && <OlderSubagents key={`${taskId}:${before}`} sessionId={taskId} before={before} />}
    </>
  );
}

/** A group's head in the index: the message quoted, never a turn number. */
function indexTitle(key: string, text: string): string {
  if (!key) return 'Earlier in the conversation';
  if (key === 'start') return 'Before the first message';
  return text ? `“${text}”` : 'An earlier message';
}

/** Subagents only Copilot's record still lists: read a page at a time, only when asked, and merged into the groups above. */
function OlderSubagents({ sessionId, before }: Readonly<{ sessionId: string; before: string }>) {
  const { dispatch } = useApp();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function load() {
    setBusy(true);
    setError(null);
    try {
      const page = await api.olderSubagents(sessionId, before);
      dispatch({ type: 'subagents_older', sessionId, before, page });
    } catch (e) {
      setError(describeError(e));
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="flex flex-col items-start gap-1 px-2 pt-1">
      <Button size="sm" loading={busy} onClick={() => void load()}>Show older subagents</Button>
      {error && <Note tone="error" role="alert">Could not load older subagents: {error}</Note>}
    </div>
  );
}

/**
 * One subagent's transcript, in its open panel or sheet: a scroller filling it that keeps its
 * scrolling to itself. Mounting loads it from the subagent route, after which live frames
 * tagged with this agent_id keep it current (frames during the fetch are buffered and
 * replayed, see state.ts). Reloads on every fresh snapshot to close any gap.
 */
function AgentTranscriptView({
  sessionId,
  provider,
  workdir,
  subagent,
  interactions,
  transcript: legacyTranscript,
  snapshotSeq,
  result: legacyResult,
  open,
  parent,
  name,
  seek,
}: Readonly<{
  sessionId: string;
  provider: string;
  workdir: string;
  subagent: Subagent;
  /** The Task's requests; this subagent's are those with its agent_id. */
  interactions: Interaction[];
  transcript: AgentTranscript | undefined;
  snapshotSeq: number;
  /** The `task` tool's output on the parent item, which is the subagent's result. */
  result?: string;
  open: boolean;
  parent?: Item;
  name: string;
  /** A run picked in the row: show the first item at or after its start, paging to it if need be. */
  seek?: Seek | null;
}>) {
  const { dispatch } = useApp();
  const density = useDensity();
  const scroller = useRef<HTMLDivElement>(null);
  const live = subagent.status === 'running';
  // A running one follows its output; a finished one opens at the top of what is held.
  const atBottom = useRef(live);
  const [attempt, setAttempt] = useState(0);
  const detail = useDetailAgent(subagent.id, open);
  const transcript = detail.compact ? detail.agent : legacyTranscript;
  const finished = subagent.status === 'completed' || subagent.status === 'idle';
  // One another subagent spawned has its call in that subagent's transcript.
  const parentItem: Item = parent ?? { id: subagent.parent_tool_call_id ?? '', agent_id: subagent.parent_agent_id, kind: 'tool', time: '', compact: { has_text: false, has_reasoning: false } };
  // A background launch's call only acknowledges the launch, so its body is not read.
  const { item: parentFullItem, body: parentBody, attach: parentAttach, retry: parentRetry } = useItemBody(parentItem, open && !!subagent.parent_tool_call_id && finished && !subagent.background);
  const pageRead = useRef<{ token: number; controller: AbortController } | null>(null);
  const pageToken = useRef(0);
  const retryAfter = useRef(0);
  const [windowReset, setWindowReset] = useState(0);
  useEffect(() => () => { pageRead.current?.controller.abort(); pageRead.current = null; }, [sessionId, subagent.id, open, detail.agent?.seq]);

  useEffect(() => {
    if (detail.compact || !open) return;
    let cancelled = false;
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 10000);
    dispatch({ type: 'agent_loading', sessionId, agentId: subagent.id });
    api
      .subagent(sessionId, subagent.id, controller.signal)
      .then((d) => !cancelled && dispatch({ type: 'agent_loaded', sessionId, agentId: subagent.id, ...d }))
      .catch((e: unknown) => !cancelled && dispatch({ type: 'agent_failed', sessionId, agentId: subagent.id, error: describeError(e) }));
    return () => {
      cancelled = true;
      controller.abort();
      window.clearTimeout(timer);
      dispatch({ type: 'agent_unloaded', sessionId, agentId: subagent.id });
    };
  }, [sessionId, subagent.id, snapshotSeq, attempt, dispatch, detail.compact, open]);

  // Follow new output only while the reader is at the bottom.
  const items: Item[] = transcript?.items ?? NO_ITEMS;
  // The result: the `task` call's output; for a background launch, its own last message (once the window reaches its end).
  let result = detail.compact ? parentFullItem?.tool?.output : legacyResult;
  if (subagent.background) result = finished && !detail.agent?.after ? [...items].reverse().find((item) => item.kind === 'assistant' && item.text?.trim())?.text : undefined;
  // A result that repeats the last message above it is not drawn twice: a line under that message says where it went.
  const lastText = detail.agent?.after ? undefined : [...items].reverse().find((item) => item.kind === 'assistant' && item.text?.trim())?.text;
  const resultRepeats = !!result && !!lastText && sameText(result, lastText);
  useFileHintItems(subagent.id, items, open);
  useLayoutEffect(() => {
    const el = scroller.current;
    if (!el) return;
    if (atBottom.current && !detail.agent?.after) el.scrollTop = el.scrollHeight;
  }, [items, detail.agent?.after]);

  function loadOlder(direction: 'older' | 'newer' = 'older') {
    const agent = detail.agent, store = detail.store;
    const before = direction === 'older' ? agent?.before : agent?.after;
    if (Date.now() < retryAfter.current || !detail.compact || !open || !agent || !before || agent.loading || agent.page || pageRead.current || !store) return;
    const controller = new AbortController(), token = ++pageToken.current;
    pageRead.current = { token, controller };
    store.action({ type: 'page_start', agentId: subagent.id, before, token, direction });
    const timer = window.setTimeout(() => {
      if (pageRead.current?.token !== token) return;
      retryAfter.current = Date.now() + 1000;
      store.action({ type: 'page_failed', agentId: subagent.id, token, error: 'Loading earlier messages timed out. Scroll up to retry.' });
      controller.abort();
    }, 10000);
    void historyPage(sessionId, subagent.id, before, direction, store.value.epoch, () => api.subagentHistory(sessionId, subagent.id, before, controller.signal, direction)).then(page => {
      if (controller.signal.aborted || pageRead.current?.token !== token || store.value.agent?.page?.token !== token) return;
      atBottom.current = false;
      store.action({ type: 'page_done', agentId: subagent.id, before, token, page });
    }).catch(error => {
      if (pageRead.current?.token === token && !controller.signal.aborted) { retryAfter.current = Date.now() + 1000; store.action({ type: 'page_failed', agentId: subagent.id, token, error: describeError(error) }); }
    }).finally(() => { window.clearTimeout(timer); if (pageRead.current?.token === token) pageRead.current = null; });
  }
  // When the reader last moved the transcript themselves (wheel, touch, keys), and whether a finger is down.
  const userAt = useRef(0);
  const touching = useRef(false);
  const loadOnDemand = useEffectEvent(loadOlder);
  useEffect(() => {
    const el = scroller.current;
    if (!el || !open) return;
    let touchY = 0;
    const key = (event: globalThis.KeyboardEvent) => {
      userAt.current = performance.now();
      if (['Home', 'PageUp', 'ArrowUp'].includes(event.key) && nearEdge(el, 'older')) loadOnDemand();
      if (['End', 'PageDown', 'ArrowDown'].includes(event.key) && nearEdge(el, 'newer')) loadOnDemand('newer');
    };
    const start = (event: TouchEvent) => { touchY = event.touches[0]?.clientY ?? 0; touching.current = true; userAt.current = performance.now(); };
    const end = (event: TouchEvent) => { touching.current = event.touches.length > 0; };
    const move = (event: TouchEvent) => {
      userAt.current = performance.now();
      const y = event.touches[0]?.clientY ?? touchY;
      if (y > touchY && nearEdge(el, 'older')) loadOnDemand();
      if (y < touchY && nearEdge(el, 'newer')) loadOnDemand('newer');
      touchY = y;
    };
    el.addEventListener('keydown', key); el.addEventListener('touchstart', start, { passive: true }); el.addEventListener('touchmove', move, { passive: true }); el.addEventListener('touchend', end); el.addEventListener('touchcancel', end);
    return () => { el.removeEventListener('keydown', key); el.removeEventListener('touchstart', start); el.removeEventListener('touchmove', move); el.removeEventListener('touchend', end); el.removeEventListener('touchcancel', end); };
  }, [open]);
  const scrollTop = useRef(0);
  function onScroll() {
    const el = scroller.current;
    if (!el) return;
    atBottom.current = !detail.agent?.after && el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
    if (el.scrollTop < scrollTop.current && nearEdge(el, 'older')) loadOlder();
    else if (el.scrollTop > scrollTop.current && nearEdge(el, 'newer')) loadOlder('newer');
    scrollTop.current = el.scrollTop;
  }

  // Seeking a run: page towards its start until the first item at or after it is here, then show
  // it; the first run is the transcript's start, a running run with nothing recorded yet its end.
  // The reader scrolling or touching meanwhile takes over: nothing is written under them.
  const [sought, setSought] = useState(seek?.n);
  const [seeking, setSeeking] = useState<Seek | null>(null);
  const [lost, setLost] = useState(false);
  if (seek && seek.n !== sought) {
    setSought(seek.n);
    setSeeking(seek);
    setLost(false);
  }
  const seekStart = useRef(0);
  useEffect(() => {
    if (seeking) seekStart.current = performance.now();
  }, [seeking]);
  const seekNow = useEffectEvent(() => {
    const el = scroller.current;
    const run = seeking;
    if (!run || !el || !transcript || transcript.loading || detail.agent?.page) return;
    // A failed read says so itself; the run is not called gone for it.
    if (transcript.error || detail.agent?.pageError || touching.current || userAt.current > seekStart.current) return setSeeking(null);
    const more = detail.compact && open;
    const before = more && detail.agent?.before, after = more && detail.agent?.after;
    if (run.first) {
      if (before) return loadOlder();
      setSeeking(null);
      atBottom.current = false;
      el.scrollTop = 0;
      return;
    }
    // Compared as instants: an item's and a run's times may differ in zone or fraction.
    const start = ms(run.time);
    const at = items.findIndex((item) => ms(item.time) >= start);
    if ((at === 0 || (at < 0 && !items.length)) && before) return loadOlder();
    if (at < 0 && after) return loadOlder('newer');
    setSeeking(null);
    if (at < 0 && run.running) {
      el.scrollTop = el.scrollHeight;
      return;
    }
    const id = items[at]?.id;
    const row = id ? (el.querySelector<HTMLElement>(`[data-history-anchor="${CSS.escape(id)}"], #${CSS.escape(`item-${id}`)}`) ?? [...el.querySelectorAll<HTMLElement>('[data-history-items]')].find((node) => (JSON.parse(node.dataset.historyItems ?? '[]') as string[]).includes(id))) : undefined;
    if (!row) return setLost(true);
    atBottom.current = false;
    el.scrollTop += row.getBoundingClientRect().top - el.getBoundingClientRect().top - 12;
  });
  useEffect(() => {
    if (seeking === null) return;
    const frame = requestAnimationFrame(seekNow);
    return () => cancelAnimationFrame(frame);
  }, [seeking, items, transcript?.loading, transcript?.error, detail.agent?.page, detail.agent?.pageError]);

  const visibleInteractions = detail.compact ? windowInteractions(items, interactions, 0, !!detail.agent?.before, !!detail.agent?.after) : interactions;
  function latest() {
    pageRead.current?.controller.abort(); pageRead.current = null;
    flushSync(() => { detail.store?.action({ type: 'latest', agentId: subagent.id }); setWindowReset(value => value + 1); });
    atBottom.current = true;
    if (scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }

  // The transcript: a skeleton until it is here, the failure with Retry, or the rows.
  let body: ReactNode;
  if ((!transcript || transcript.loading) && items.length === 0) {
    body = <Skeleton label="Loading the transcript…" rows={5} />;
  } else if (transcript?.error) {
    body = (
      <Note tone="error" role="alert" className="flex flex-wrap items-center gap-2">
        <span className="min-w-0 flex-1">Could not load the transcript: {transcript.error}</span>
        <Button size="sm" variant="secondary" onClick={() => { if (detail.compact) detail.retry?.(); else setAttempt((n) => n + 1); }}>
          Retry
        </Button>
      </Note>
    );
  } else {
    body = (
      <>
        <HistoryAnchor scroller={scroller} firstItem={items[0]?.id ?? ''} lastItem={items.at(-1)?.id} itemIds={detail.compact ? items.map(item => item.id) : undefined} knownIds={detail.agent?.index?.map(item => item.id)} resetKey={`${detail.store?.value.epoch}:${windowReset}`} className="flex flex-col gap-3">
          <AgentItems sessionId={sessionId} provider={provider} workdir={workdir} agentId={subagent.id} items={items} identityItems={detail.agent?.index} historyItemSeq={detail.agent?.itemSeq} interactions={visibleInteractions} live={live && !detail.agent?.after} density={density} />
        </HistoryAnchor>
        {items.length === 0 && <Note>Nothing recorded yet.</Note>}
      </>
    );
  }

  return (
    // Sideways it never scrolls: wide content scrolls in its own box; a touch hit area at the edge must not widen it.
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The transcript scroll region accepts keyboard paging at both boundaries.
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-x-hidden overflow-y-auto overscroll-contain px-3 py-3 [overflow-wrap:anywhere]" ref={scroller} onScroll={onScroll} role="region" aria-label={`Transcript of ${name}`} tabIndex={0} aria-busy={(!transcript || transcript.loading) && items.length === 0 ? true : undefined} onWheelCapture={event => {
      if (event.ctrlKey) return;
      userAt.current = performance.now();
      if (event.deltaY < 0 && nearEdge(event.currentTarget, 'older')) loadOlder();
      if (event.deltaY > 0 && nearEdge(event.currentTarget, 'newer')) loadOlder('newer');
    }}>
      {detail.agent?.page && <Note role="status"><Spinner /> Loading {detail.agent.page.direction === 'newer' ? 'newer' : 'earlier'} messages…</Note>}
      {detail.agent?.after && <Button className="sticky top-0 z-10 self-center" size="sm" variant="secondary" onClick={latest}>Jump to latest</Button>}
      {detail.agent?.pageError && <Note tone="error">{detail.agent.pageError}</Note>}
      {lost && <Note role="status">That run is not in the retained transcript.</Note>}
      {body}
      {subagent.status === 'failed' && <Note tone="error">Failed{subagent.error ? `: ${subagent.error}` : '.'}</Note>}
      {subagent.status === 'cancelled' && <Note>Stopped before it finished.</Note>}
      {detail.compact && parentBody && <div ref={parentAttach}><BodyNotice body={parentBody} retry={parentRetry} /></div>}
      {resultRepeats && <p className="text-caption text-muted">↑ Sent to the main agent as the result.</p>}
      {result && !resultRepeats && (
        <div className="rounded-md bg-raised px-3.5 py-2.5 text-ui shadow-raised">
          <div className="mb-1 text-caption text-muted">Result sent to the main agent</div>
          <Markdown text={result} />
        </div>
      )}
    </div>
  );
}

/** Cancellation requests never invent a terminal status; the provider's SSE owns it. */
function StopSubagent({ session, subagent, stopping, onStop }: Readonly<{ session: TaskInfo; subagent: Subagent; stopping: StopState; onStop: () => void }>) {
  const { busy, requested, error } = stopping;
  if (subagent.status !== 'running') return null;
  return (
    <Tip label={error ?? (requested ? 'Stop requested' : 'Stop this subagent')}>
      <Button size="sm" variant="secondary" aria-label={`Stop subagent ${subagent.name}`} loading={busy} disabled={requested || readOnly(session)} onClick={onStop}>
        <Square className="!size-3" fill="currentColor" />
        {requested ? 'Requested' : 'Stop'}
      </Button>
    </Tip>
  );
}
