import { useApi } from '../ApiContext';
import { flushSync } from 'react-dom';
import { HistoryAnchor } from './HistoryAnchor';
import { isSubagentCall, subagentSummary, duration, windowInteractions } from '../lib/transcript';
import { BodyNotice, useDetailAgent, useItemBody } from './Details';
import { Check, ChevronDown, ChevronRight, Copy, CornerDownRight, CornerUpLeft, Minus, Square, Workflow, X } from 'lucide-react';
import { createContext, memo, useContext, useEffect, useEffectEvent, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react';
import { describeError, readOnly, type Interaction, type Item, type OutlineItem, type SessionDetail, type Subagent, type SubagentStatus } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { historyPage } from '../lib/historyArchive';
import { compactTokens } from '../lib/cost';
import { EXIT_MS } from './ui/collapse';
import { IDENTITY_LIMIT, PAGE_ROWS, countParts, countSubagents, families, inFilter, indexGroups, liveSet, matches, mergeOutline, ms, parentMap, replyIndex, subagentNoun, totalTokens, type CountPart, type IdentityTone, type IndexFilter, type LiveSubagent, type Replies, type SubagentCounts } from '../lib/subagents';
import { useResizable } from '../lib/useResizable';
import type { AgentTranscript } from '../state';
import { useFileHintItems } from './FileReferences';
import { BADGE_TEXT, Markdown, Note, Skeleton, Spinner, StepDots, SubagentIdleIcon, clockTime, useApp } from './common';
import { AgentItems } from './Transcript';
import { SubagentRetry } from './StatusLine';
import { Button } from './ui/button';
import { AlertDialog, Sheet, useConfirm } from './ui/dialog';
import { Input } from './ui/input';
import { PanelFoot, PanelSection } from './ui/panel';
import { Tip } from './ui/tooltip';
import { Key } from './InlinePicker';

/** A stop request for one subagent; the provider's SSE still owns its terminal status. */
interface StopState {
  busy: boolean;
  requested: boolean;
  error: string | null;
}
export const NOT_STOPPING: StopState = { busy: false, requested: false, error: null };

/** One record of stop requests per Task, so every row of a subagent agrees. */
function useStops(sessionId: string, subagents: readonly Subagent[]): [Record<string, StopState>, (agentId: string) => void] {
  const api = useApi();
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
          <span aria-hidden="true" className="fade-rule-y h-full w-px flex-none transition-[background-color,scale] duration-100 group-hover/handle:scale-x-200 group-hover/handle:bg-accent group-hover/handle:bg-none group-focus-visible/handle:scale-x-200 group-focus-visible/handle:bg-accent group-focus-visible/handle:bg-none group-active/handle:bg-accent group-active/handle:bg-none" />
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
 * Where the strip's sheet opens (DESIGN.md subagent sheet): the list, filtered to one reply's
 * subagents with `reply` (its message's id, "start" before any). A subagent opens in the
 * Conversation pane instead (`show`).
 */
export type SheetTo = { reply?: string };

/** The last request to open the strip's sheet (`to`) or close it (null); `n` counts them, so asking twice still opens it. */
export interface SheetRequest {
  to: SheetTo | null;
  n: number;
}

/** A run picked in the subagent's Tools: its transcript shows the run's first item. `n` counts the picks. */
export interface Seek {
  agent: string;
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
  /** The user messages held, for the list's quotes. */
  messages: readonly Item[];
  replies: Replies;
  /** The live set; empty while nothing is at work. */
  live: LiveSubagent[];
  liveIds: ReadonlySet<string>;
  stops: Record<string, StopState>;
  sheet: SheetRequest | null;
  /** A stop waits for its confirmation: a press there is not a press away from the sheet. */
  confirming: boolean;
  /** The last run picked to scroll its subagent's transcript to. */
  seek: Seek | null;
}

/** Stable for the life of the scope, so rows never render for a new callback. */
interface ScopeActions {
  /** Scroll to the row of the subagent a call spawned (else the chip holding it); the sheet makes way. */
  locate: (toolCallId: string) => void;
  /** Scroll to the reply a user message started and flash its turn line; the sheet makes way. */
  jumpToReply: (key: string) => void;
  /** Asks to confirm, then stops. */
  stop: (s: Subagent) => void;
  /** Opens the strip's sheet on the list. */
  open: (to: SheetTo) => void;
  /** Shows a subagent in the Conversation pane, in the Thread's place; the sheet makes way. */
  show: (agentId: string) => void;
  /** Scrolls the open subagent's transcript to a run's first item. */
  seek: (run: Omit<Seek, 'n'>) => void;
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

const SubagentContext = createContext<{ store: ScopeStore; actions: ScopeActions } | null>(null);
const NO_SUBSCRIBE = () => () => {};

/** One value from the Task's subagents; a component renders again only when it changes, so `select` returns primitives or kept references. */
export function useScope<T>(select: (d: ScopeData) => T): T | undefined {
  const scope = useContext(SubagentContext);
  return useSyncExternalStore(scope?.store.subscribe ?? NO_SUBSCRIBE, () => (scope ? select(scope.store.data) : undefined));
}
export const useActions = () => useContext(SubagentContext)?.actions;
/** Scrolls the conversation to a tool call's row, opening what folds it; undefined outside a Task. */
export const useLocateItem = () => useContext(SubagentContext)?.actions.locate;

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
/** The last request to open or close the strip's sheet; undefined outside a Thread. */
export const useSheetRequest = () => useScope((d) => d.sheet);
/** A stop confirmation is open over the sheet. */
export const useStopConfirming = () => useScope((d) => d.confirming) ?? false;

/**
 * The Task's subagents for everything that draws them (the reply chips, the live rows, the strip's
 * sheet): the session, the transcripts held, the requests to open the sheet, stop requests and their
 * one confirmation, and `locate`.
 */
export function SubagentScope({ session, agents, agentSteps, snapshotSeq, onLocate, onJumpToReply, onShow, children }: Readonly<{ session: SessionDetail; agents: Record<string, AgentTranscript>; agentSteps: Record<string, Item>; snapshotSeq: number; onLocate: (toolCallId: string) => void; onJumpToReply: (key: string) => void; onShow: (agentId: string) => void; children: ReactNode }>) {
  const [stops, stopNow] = useStops(session.id, session.subagents);
  // Stopping a subagent ends its work for good, so it is confirmed first (DESIGN.md Confirmations).
  const stopConfirm = useConfirm<Subagent>();
  const [sheet, setSheet] = useState<SheetRequest | null>(null);
  const [seek, setSeek] = useState<Seek | null>(null);
  // The items and the index change with every streamed token; what is read from them (the user
  // messages and the `task` calls) only with a message or a call, so those lists are kept until then.
  const byParent = parentMap(session.subagents);
  const held = [...session.items, ...(session.recent_items ?? [])];
  const calls = useKept(held.filter((item) => isSubagentCall(item) || byParent.has(item.id)));
  const messages = useKept(held.filter((item) => item.kind === 'user'));
  const outline = useKept(mergeOutline(session.outline, (session.history_index ?? session.items).filter((item) => (item.kind === 'user' && !item.delivery) || isSubagentCall(item) || byParent.has(item.id))));
  const callMap = useMemo(() => new Map(calls.map((item) => [item.id, item])), [calls]);
  const replies = useMemo(() => replyIndex(outline, session.subagents), [outline, session.subagents]);
  const summaries = useMemo<Summaries>(() => ({ agents, agentSteps, calls: callMap }), [agents, agentSteps, callMap]);
  const { id, provider, workdir, interactions, stage, state } = session;
  const task = useMemo<TaskInfo>(() => ({ id, provider, workdir, interactions, stage, state }), [id, provider, workdir, interactions, stage, state]);
  const live = useMemo(() => liveSet(replies.list.at(-1), session.subagents), [replies, session.subagents]);
  const liveIds = useMemo(() => new Set(live.map((x) => x.subagent.id)), [live]);
  const confirming = stopConfirm.target !== null;
  const data = useMemo<ScopeData>(
    () => ({ task, subagents: session.subagents, subagentsBefore: session.subagents_before, agents, snapshotSeq, summaries, outline, messages, replies, live, liveIds, stops, sheet, confirming, seek }),
    [task, session.subagents, session.subagents_before, agents, snapshotSeq, summaries, outline, messages, replies, live, liveIds, stops, sheet, confirming, seek],
  );
  const [store] = useState(() => new ScopeStore(data));
  useLayoutEffect(() => store.set(data), [store, data]);
  const handlers = useRef({ onLocate, onJumpToReply, onShow, ask: stopConfirm.ask });
  useLayoutEffect(() => {
    handlers.current = { onLocate, onJumpToReply, onShow, ask: stopConfirm.ask };
  });
  const [value] = useState(() => {
    const request = (to: SheetTo | null) => setSheet((r) => ({ to, n: (r?.n ?? 0) + 1 }));
    return {
      store,
      actions: {
        // What it lands on is in the conversation: the sheet makes way.
        locate: (to: string) => {
          request(null);
          handlers.current.onLocate(to);
        },
        jumpToReply: (key: string) => {
          request(null);
          handlers.current.onJumpToReply(key);
        },
        stop: (s: Subagent) => handlers.current.ask(s),
        open: request,
        show: (agentId: string) => {
          request(null);
          handlers.current.onShow(agentId);
        },
        seek: (run: Omit<Seek, 'n'>) => setSeek((x) => ({ ...run, n: (x?.n ?? 0) + 1 })),
      } satisfies ScopeActions,
    };
  });
  const stopName = stopConfirm.target ? `“${stopConfirm.target.name}”` : '';
  return (
    <SubagentContext.Provider value={value}>
      {children}
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

// Identity tones as whole class names, so Tailwind sees them.
export const DOT: Record<IdentityTone, string> = { violet: 'bg-badge-violet', pink: 'bg-badge-pink', cyan: 'bg-badge-cyan', amber: 'bg-badge-amber', teal: 'bg-badge-teal' };
export const PART_TONE: Record<CountPart['tone'], string> = { muted: 'text-muted', success: 'text-success', error: 'text-error', accent: 'text-accent' };
const STATUS_WORD: Record<SubagentStatus, string> = { running: 'running', idle: 'idle', completed: 'completed', failed: 'failed', cancelled: 'stopped' };
export const STATUS_LABEL: Record<SubagentStatus, string> = { running: 'Running', idle: 'Idle', completed: 'Done', failed: 'Failed', cancelled: 'Stopped' };

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

/**
 * A subagent's state as a glyph (AgentChip's, without the word): the step dots while it runs
 * (`StepDots`, the running tool step's mark), a check, a cross, the idle glyph, a dash.
 */
export function SubagentMark({ status }: Readonly<{ status: SubagentStatus }>) {
  switch (status) {
    case 'running':
      return <StepDots />;
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

/** What a subagent is doing or reported (`subagentSummary`): its row's caption. */
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
export function Took({ subagent: s, className }: Readonly<{ subagent: Subagent; className?: string }>) {
  const running = s.status === 'running';
  const now = useNow(running && !!s.started_at);
  let text = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  if (running && s.started_at) text = duration(s.started_at, new Date(Math.max(now, ms(s.started_at))).toISOString());
  return <span className={cn('shrink-0 text-meta tabular-nums', running ? 'text-accent' : 'text-muted', className)}>{text ?? ''}</span>;
}

/**
 * A reply's subagents (DESIGN.md subagent chip): identity dots (the `Workflow` glyph past five
 * calls), then "3 subagents · 1.2M tokens · 2 done · 1 failed", and "2 of 3 loaded" while some of
 * the reply's calls have no subagent here yet. It opens the strip's sheet on their list, filtered
 * to the reply (`reply`; the whole list without one).
 */
export function SubagentChip({ subagents, calls, tones, reply }: Readonly<{ subagents: Subagent[]; /** The reply's `task` calls, loaded or not. */ calls: number; tones: ReadonlyMap<string, IdentityTone>; reply?: string }>) {
  const actions = useActions();
  const c = countSubagents(subagents);
  const total = Math.max(calls, subagents.length);
  const tokens = totalTokens(subagents);
  const lead = [subagentNoun(total), tokens ? `${compactTokens(tokens)} tokens` : ''].filter(Boolean).join(' · ');
  const partial = subagents.length < total ? `${subagents.length} of ${total} loaded` : '';
  return (
    <button
      type="button"
      data-subagent-items={JSON.stringify(subagents.flatMap((s) => (s.parent_tool_call_id ? [s.parent_tool_call_id] : [])))}
      aria-haspopup="dialog"
      title={[lead, partial, ...countParts(c).map((p) => p.text)].filter(Boolean).join(' · ')}
      className="-mx-1.5 flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-well hover:text-body pointer-coarse:min-h-11"
      onClick={() => actions?.open({ reply })}
    >
      {total <= IDENTITY_LIMIT ? (
        <span aria-hidden="true" className="flex shrink-0 items-center gap-1">
          {subagents.map((s) => {
            const tone = tones.get(s.id);
            return <span key={s.id} className={cn('size-1.5 rounded-full', tone ? DOT[tone] : 'bg-faint')} />;
          })}
        </span>
      ) : (
        <Workflow aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
      )}
      <span className="min-w-0 truncate tabular-nums">
        <Counts c={c} lead={partial ? <>{lead} · <span className="text-muted">{partial}</span></> : lead} />
      </span>
      <ChevronRight aria-hidden="true" className="size-3 shrink-0 text-faint" />
    </button>
  );
}

/** A note under a subagent row (a failed stop, a retry) starts where its name does: 32px in, 18px more a level (`--depth` on the row). */
const UNDER_NAME = 'pl-[calc(var(--spacing)*(8+var(--depth)*4.5))]';

/**
 * One subagent (DESIGN.md subagent row), the same in the transcript and the sheet's list: its state
 * glyph, identity dot (while its reply has at most five), name, caption (what it is doing now, the
 * first line of its result, or its error, in `error`), duration and tokens; on a phone the caption
 * takes a second line. One spawned by another is indented under it. A press opens it in the
 * Conversation pane. With `anchor` it carries its `task` call's id, so `locate` lands on it.
 * Memoised: it renders again only when what it shows changes.
 */
export const SubagentRow = memo(function SubagentRow({ subagent: s, tone, depth = 0, anchor = false }: { subagent: Subagent; tone?: IdentityTone; depth?: number; anchor?: boolean }) {
  const actions = useActions();
  const stopError = useScope((d) => d.stops[s.id]?.error) ?? null;
  const caption = useScope((d) => rowSummary(d.summaries, s)) ?? '';
  if (!actions) return null;
  const name = s.name || 'Subagent';
  const text = caption || (s.status === 'cancelled' ? 'Stopped' : '');
  const parentId = s.parent_tool_call_id;
  return (
    // `--depth` indents the row 18px a level; its notes start under its name, 24px further in.
    <div id={anchor && parentId ? `item-${parentId}` : undefined} data-subagent-row="" className="flex flex-col" style={{ '--depth': depth } as CSSProperties}>
      <button
        type="button"
        data-subagent-toggle=""
        data-index-row=""
        aria-label={`${name}, ${STATUS_WORD[s.status]}${text ? `: ${text}` : ''}`}
        className="flex min-h-7 w-full items-center gap-2 rounded-sm py-0.5 pr-1.5 text-left text-caption text-body transition-colors duration-100 hover:bg-tint-well max-sm:min-h-11 max-sm:flex-wrap max-sm:gap-y-0 pointer-coarse:min-h-11 pl-[calc(var(--spacing)*(2+var(--depth)*4.5))]"
        onClick={() => actions.show(s.id)}
      >
        {depth > 0 && <CornerDownRight aria-hidden="true" className="-ml-1 size-3 shrink-0 text-faint" />}
        <span className="flex size-4 shrink-0 items-center justify-center">
          <SubagentMark status={s.status} />
        </span>
        {tone && <span aria-hidden="true" className={cn('size-1.5 shrink-0 rounded-full', DOT[tone])} />}
        <span className={cn('min-w-0 truncate', tone && BADGE_TEXT[tone], text ? 'max-w-[45%] shrink-0 max-sm:max-w-none max-sm:flex-1' : 'flex-1')}>{name}</span>
        {text && (
          <span className={cn('min-w-0 flex-1 truncate max-sm:order-last max-sm:basis-full max-sm:pl-6 max-sm:text-meta', s.status === 'failed' ? 'text-error' : 'text-muted')} title={text}>
            {text}
          </span>
        )}
        <Took subagent={s} className="w-14 text-right" />
        <span className="w-10 shrink-0 text-right text-meta tabular-nums text-muted">{s.tokens ? compactTokens(s.tokens) : ''}</span>
      </button>
      {stopError && (
        <p role="alert" className={cn('pr-2 pb-1 text-caption text-error', UNDER_NAME)}>
          Could not stop it: {stopError}
        </p>
      )}
      {s.status === 'running' && s.retry && <SubagentRetry retry={s.retry} className={UNDER_NAME} />}
    </div>
  );
});

/** Whether two messages say the same once spacing is ignored. */
function sameText(a: string, b: string): boolean {
  const flat = (t: string) => t.replace(/\s+/g, ' ').trim();
  return flat(a) === flat(b);
}

const NO_LIVE: LiveSubagent[] = [];

/**
 * The subagents at work (DESIGN.md subagent rows), at the conversation's foot while one of them
 * runs: the latest reply's subagents and any earlier one still or again running, under their chip,
 * as plain rows, running first. No card of their own: they read as the reply's list. A subagent
 * failing is said once, politely.
 */
export function LiveSubagents() {
  const set = useScope((d) => d.live) ?? NO_LIVE;
  const replies = useScope((d) => d.replies);
  const [limit, setLimit] = useState(PAGE_ROWS);
  const rows = useMemo(() => families(set.map((x) => x.subagent)).flatMap((f) => f.rows), [set]);
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
  if (!set.length || !replies) return status;
  const subagents = set.map((x) => x.subagent);
  // Only the latest reply's: the chip opens its list; with earlier ones running too, the whole list.
  const latest = set.some((x) => x.earlier) ? undefined : replies.list.at(-1);
  return (
    <>
      {status}
      <section aria-label="Subagents at work" className="flex flex-col gap-1">
        <div className="flex px-2">
          <SubagentChip subagents={subagents} calls={latest?.calls.length ?? subagents.length} tones={replies.tones} reply={latest?.key} />
        </div>
        <ul className="flex max-w-2xl flex-col">
          {rows.slice(0, limit).map(({ subagent, depth }) => (
            <li key={subagent.id}>
              <SubagentRow subagent={subagent} tone={replies.tones.get(subagent.id)} depth={depth} anchor />
            </li>
          ))}
        </ul>
        {rows.length > limit && (
          <Button variant="link" className="h-7 w-fit px-2" onClick={() => setLimit(limit + PAGE_ROWS)}>
            Show {Math.min(PAGE_ROWS, rows.length - limit)} more
          </Button>
        )}
      </section>
    </>
  );
}

const FILTERS: { key: IndexFilter; label: string; tone: string }[] = [
  { key: 'all', label: 'All', tone: 'text-body' },
  { key: 'running', label: 'Running', tone: 'text-accent' },
  { key: 'failed', label: 'Failed', tone: 'text-error' },
  { key: 'done', label: 'Done', tone: 'text-body' },
  { key: 'stopped', label: 'Stopped', tone: 'text-muted' },
];
/** An older message's group shows this many rows before "N more · Show all"; one with a row more shows them all. */
const GROUP_ROWS = 3;
const CHIP = 'h-6 rounded-full px-2 text-caption tabular-nums transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11';

/** The Thread's subagents for the status strip: the list, whether older ones wait in the record; undefined outside a Thread. */
export function useSubagentIndex() {
  const subagents = useScope((d) => d.subagents);
  const before = useScope((d) => d.subagentsBefore);
  return subagents ? { subagents, before } : undefined;
}

/**
 * The strip sheet's Subagents tab (DESIGN.md subagent sheet): the list, the index of the Thread's
 * subagents, in the one fixed box. `to` is where it opened. The list: search, status chips, and the
 * subagents grouped by the message that started their reply, newest first, each with its clock and
 * Jump; older messages show three rows, then "N more · Show all"; then the ones not placed and the
 * older ones from the record. Opened from a reply's chip it shows that reply's group alone, until
 * "From <time> ×" lifts the filter. A row opens the subagent in the Conversation pane, closing the sheet.
 */
export function SubagentSheetBody({ to, error, phone }: Readonly<{ to: SheetTo | null; error?: string; phone: boolean }>) {
  const actions = useActions();
  const input = useRef<HTMLInputElement>(null);
  const taskId = useScope((d) => d.task.id);
  const subagents = useScope((d) => d.subagents);
  const before = useScope((d) => d.subagentsBefore);
  const outline = useScope((d) => d.outline);
  const messages = useScope((d) => d.messages);
  const summaries = useScope((d) => d.summaries);
  const tones = useScope((d) => d.replies.tones);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<IndexFilter>('all');
  const [reply, setReply] = useState(to?.reply ?? null);
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const list = useRef<HTMLDivElement>(null);
  const groups = useMemo(() => (outline && messages && subagents ? indexGroups(outline, messages, subagents) : []), [outline, messages, subagents]);
  if (!taskId || !subagents || !summaries || !tones || !actions) return null;
  const pool = reply === null ? groups : groups.filter((g) => g.key === reply);
  const all = pool.flatMap((g) => g.subagents);
  // The chips and the groups count what the search finds.
  const found = new Set(query ? all.filter((s) => matches(s, rowSummary(summaries, s), query)) : all);
  const c = countSubagents([...found]);
  const counts: Record<IndexFilter, number> = { all: c.total, running: c.running, failed: c.failed, done: c.done, stopped: c.stopped };
  const shown = pool
    .flatMap((g) => {
      const matched = g.subagents.filter((s) => inFilter(s, filter) && found.has(s));
      // Each under the one that spawned it, running first, as in the conversation.
      const rows = families(matched).flatMap((f) => f.rows);
      return rows.length ? [{ ...g, matched, rows }] : [];
    })
    // Caps defer: the newest message shown (after the search and filters) shows all its rows, older ones three until "Show all".
    .map((g, at) => ({ ...g, cap: reply !== null || at === 0 || g.rows.length <= GROUP_ROWS + 1 || expanded.has(g.key) ? g.rows.length : GROUP_ROWS }));
  const from = reply === null ? undefined : groups.find((g) => g.key === reply);
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
  return (
    <>
      <div className="flex min-h-0 flex-1 animate-fade-in flex-col gap-1.5 px-3 pt-0.5">
        {error && (
          <Note tone="warn" role="alert">
            {error}
          </Note>
        )}
        <Input ref={input} size="md" type="search" value={query} onChange={(e) => setQuery(e.target.value)} onKeyDown={onArrow} aria-label="Search subagents" placeholder={`Search ${all.length} subagents`} />
        <div role="group" aria-label="Show subagents by status" className="flex flex-wrap items-center gap-1">
          {FILTERS.filter((f) => f.key === 'all' || f.key === filter || counts[f.key]).map((f) => (
            <button key={f.key} type="button" aria-pressed={filter === f.key} className={cn(CHIP, filter === f.key ? 'bg-tint-selected text-ink' : counts[f.key] ? f.tone : 'text-muted')} onClick={() => setFilter(f.key)}>
              {f.label} {counts[f.key]}
            </button>
          ))}
          {reply !== null && (
            <>
              <span className="flex-1" />
              <button type="button" title="Show every message's subagents" className={cn(CHIP, 'flex items-center gap-1 bg-tint-well text-body')} onClick={() => setReply(null)}>
                From {from?.time ? clock(from.time) : 'this reply'}
                <X aria-hidden="true" className="size-3 text-muted" />
                <span className="sr-only">, show every message's subagents</span>
              </button>
            </>
          )}
        </div>
        {/* eslint-disable-next-line jsx-a11y/no-static-element-interactions -- Arrow keys move between the row buttons inside. */}
        <div ref={list} onKeyDown={onArrow} className="-mx-1.5 min-h-0 flex-1 overflow-y-auto overflow-x-hidden overscroll-contain px-1.5 pb-2">
          {shown.map((g) => {
            const title = indexTitle(g.key, g.text);
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
                  {g.key && (
                    <Button variant="link" aria-label={`Jump to the subagents of ${title}`} onClick={() => actions.jumpToReply(g.key)}>
                      Jump
                    </Button>
                  )}
                </div>
                <ul className="flex flex-col">
                  {g.rows.slice(0, g.cap).map(({ subagent: s, depth }) => (
                    <li key={s.id}>
                      <SubagentRow subagent={s} tone={tones.get(s.id)} depth={depth} />
                    </li>
                  ))}
                  {g.cap < g.rows.length && (
                    <li>
                      <button type="button" className="flex h-7 w-full items-center gap-1.5 rounded-sm pl-8 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-well pointer-coarse:min-h-11" onClick={() => setExpanded((e) => new Set([...e, g.key]))}>
                        <ChevronDown aria-hidden="true" className="size-3.5 shrink-0" />
                        {g.rows.length - g.cap} more · <span className="text-accent">Show all {g.rows.length}</span>
                      </button>
                    </li>
                  )}
                </ul>
              </section>
            );
          })}
          {!shown.length && <p className="px-2 py-2 text-caption text-muted">{query ? `No subagent matches “${query}”.` : 'No subagents in this state.'}</p>}
          {/* Older pages join no reply here, so a reply's list would drop them: only the whole list reads them. */}
          {before && reply === null && <OlderSubagents key={`${taskId}:${before}`} sessionId={taskId} before={before} />}
        </div>
      </div>
      {!phone && (
        <PanelFoot>
          <span className="flex items-center gap-1.5"><Key>Esc</Key>close</span>
          <span className="flex items-center gap-1.5"><Key>↑</Key><Key>↓</Key>move</span>
          <span className="flex items-center gap-1.5"><Key>↵</Key>open</span>
        </PanelFoot>
      )}
    </>
  );
}

/** A group's head in the list: the message quoted, never a turn number. */
function indexTitle(key: string, text: string): string {
  if (!key) return 'Earlier in the conversation';
  if (key === 'start') return 'Before the first message';
  return text ? `“${text}”` : 'An earlier message';
}

/** Subagents only Copilot's record still lists: read a page at a time, only when asked, and merged into the groups above. */
function OlderSubagents({ sessionId, before }: Readonly<{ sessionId: string; before: string }>) {
  const api = useApi();
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
 * One subagent's transcript as a conversation, in the Conversation pane (DESIGN.md subagent view):
 * what it was asked (by whom, when), its work, then its Result (or Error) as returned to its
 * spawner; a running one follows its end. A scroller filling the pane that keeps its scrolling to
 * itself. Mounting loads it from the subagent route, after which live frames tagged with this
 * agent_id keep it current (frames during the fetch are buffered and replayed, see state.ts).
 * Reloads on every fresh snapshot to close any gap.
 */
export function AgentTranscriptView({
  sessionId,
  provider,
  workdir,
  subagent,
  interactions,
  transcript: legacyTranscript,
  snapshotSeq,
  result: legacyResult,
  parent,
  name,
  spawner,
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
  parent?: Item;
  name: string;
  /** Who spawned it: the Thread's name or a subagent's, for "asked by" and "returned to". */
  spawner: string;
  /** A run picked in its Tools: show the first item at or after its start, paging to it if need be. */
  seek?: Seek | null;
}>) {
  // Always on screen: it mounts with the view and leaves with it.
  const open = true;
  const api = useApi();
  const { dispatch } = useApp();
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
  const readsParent = open && !!subagent.parent_tool_call_id && finished && !subagent.background;
  const { item: parentFullItem, body: parentBody, attach: parentAttach, retry: parentRetry } = useItemBody(parentItem, readsParent);
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
  }, [sessionId, subagent.id, snapshotSeq, attempt, dispatch, detail.compact, open, api]);

  // Follow new output only while the reader is at the bottom.
  const items: Item[] = transcript?.items ?? NO_ITEMS;
  // The result: the `task` call's output; for a background launch, its own last message (once the window reaches its end).
  let result = detail.compact ? parentFullItem?.tool?.output : legacyResult;
  if (subagent.background) result = finished && !detail.agent?.after ? [...items].reverse().find((item) => item.kind === 'assistant' && item.text?.trim())?.text : undefined;
  // Read as a conversation: what it was asked (its first message, once the window reaches the start),
  // its work, then the result. Its last message is the result when it says the same (always, for a
  // background launch), so it is drawn once, under Result.
  const atStart = !detail.agent?.before;
  const prompt = atStart && items[0]?.kind === 'user' ? items[0] : undefined;
  const last = finished && !detail.agent?.after ? [...items].reverse().find((item) => item.kind === 'assistant' && item.text?.trim()) : undefined;
  const lastIsResult = !!last && !!result && (subagent.background || sameText(result, last.text ?? ''));
  const work = useMemo(() => items.filter((item) => !(lastIsResult && item === last)), [items, lastIsResult, last]);
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
    void historyPage(api.cacheKey(sessionId), subagent.id, before, direction, store.value.epoch, () => api.subagentHistory(sessionId, subagent.id, before, controller.signal, direction)).then(page => {
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
        <HistoryAnchor scroller={scroller} firstItem={items[0]?.id ?? ''} lastItem={items.at(-1)?.id} itemIds={detail.compact ? items.map(item => item.id) : undefined} knownIds={detail.agent?.index?.map(item => item.id)} resetKey={`${detail.store?.value.epoch}:${windowReset}`} className="flex flex-col gap-6">
          <AgentItems sessionId={sessionId} provider={provider} workdir={workdir} agentId={subagent.id} items={work} identityItems={detail.agent?.index} historyItemSeq={detail.agent?.itemSeq} interactions={visibleInteractions} live={live && !detail.agent?.after} />
        </HistoryAnchor>
        {items.length === 0 && <Note>Nothing recorded yet.</Note>}
      </>
    );
  }

  // Result: what went back, or how it ended without one; a failure's error is said here once, under Error.
  let outcome: ReactNode = null;
  if (subagent.status === 'failed') outcome = <p className="text-ui text-error">{subagent.error || 'Failed.'}</p>;
  else if (subagent.status === 'cancelled') outcome = <Note>Stopped before it finished.</Note>;
  else if (result) outcome = <div className="text-ui text-body"><Markdown text={result} /></div>;
  // The notice belongs to a body on its way: a call never read (a background launch, a run not finished) has none.
  const parentNotice = detail.compact && readsParent && parentBody && <div ref={parentAttach}><BodyNotice body={parentBody} retry={parentRetry} /></div>;
  const ended = clock(subagent.ended_at);

  return (
    // Sideways it never scrolls: wide content scrolls in its own box; a touch hit area at the edge must not widen it.
    // Its foot clears the dock's 40px overlap and the switcher floating above it.
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The transcript scroll region accepts keyboard paging at both boundaries.
    <div className="flex min-h-0 flex-1 flex-col gap-6 overflow-x-hidden overflow-y-auto overscroll-contain px-3 pt-6 pb-28 [overflow-wrap:anywhere] sm:px-4 md:px-6 pointer-coarse:pb-32" ref={scroller} onScroll={onScroll} role="region" aria-label={`Transcript of ${name}`} tabIndex={0} aria-busy={(!transcript || transcript.loading) && items.length === 0 ? true : undefined} onWheelCapture={event => {
      if (event.ctrlKey) return;
      userAt.current = performance.now();
      if (event.deltaY < 0 && nearEdge(event.currentTarget, 'older')) loadOlder();
      if (event.deltaY > 0 && nearEdge(event.currentTarget, 'newer')) loadOlder('newer');
    }}>
      {detail.agent?.page && <Note role="status"><Spinner /> Loading {detail.agent.page.direction === 'newer' ? 'newer' : 'earlier'} messages…</Note>}
      {detail.agent?.after && <Button className="sticky top-0 z-10 self-center" size="sm" variant="secondary" onClick={latest}>Jump to latest</Button>}
      {detail.agent?.pageError && <Note tone="error">{detail.agent.pageError}</Note>}
      {lost && <Note role="status">That run is not in the retained transcript.</Note>}
      {prompt && <p className="-mb-4 self-end text-caption text-muted">Asked by {spawner}{clock(prompt.time) && ` · ${clock(prompt.time)}`}</p>}
      {body}
      {live && !detail.agent?.after && <p className="text-caption text-accent">Working · follows its end</p>}
      {(outcome || parentNotice) && (
        <PanelSection label={subagent.status === 'failed' ? 'Error' : 'Result'} icon={<CornerUpLeft aria-hidden="true" className="size-3.5 text-muted" />} meta={subagent.status === 'cancelled' ? ended : [`${subagent.status === 'failed' ? 'to' : 'returned to'} ${spawner}`, ended].filter(Boolean).join(' · ')} action={result && <CopyText text={result} label="Copy result" />} className="rounded-md bg-tint-well px-4 pt-2 pb-3">
          {outcome}
          {parentNotice}
        </PanelSection>
      )}
    </div>
  );
}

/** An icon button that copies `text`, saying so in its tip once done. */
function CopyText({ text, label }: Readonly<{ text: string; label: string }>) {
  const [copied, copy] = useCopied();
  return (
    <Tip label={copied ? 'Copied' : label}>
      <Button size="icon-sm" aria-label={label} className="text-muted" onClick={() => copy(text)}>
        {copied ? <Check /> : <Copy />}
      </Button>
    </Tip>
  );
}

/** Cancellation requests never invent a terminal status; the provider's SSE owns it. */
export function StopSubagent({ session, subagent, stopping, onStop }: Readonly<{ session: TaskInfo; subagent: Subagent; stopping: StopState; onStop: () => void }>) {
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
