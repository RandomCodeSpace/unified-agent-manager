import { flushSync } from 'react-dom';
import { HistoryAnchor } from './HistoryAnchor';
import { isSubagentCall, subagentSummary, duration, windowInteractions } from '../lib/transcript';
import { BodyNotice, DetailVisibility, useDetailAgent, useDisclosure, useItemBody } from './Details';
import { Bot, Check, ChevronDown, ChevronRight, Copy, Crosshair, Ellipsis, Minus, Square, X } from 'lucide-react';
import { createContext, memo, useContext, useEffect, useEffectEvent, useId, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore, type KeyboardEvent, type ReactNode, type RefObject } from 'react';
import { LIVE, api, describeError, isStatus, modelName, newRequestId, readOnly, type Interaction, type Item, type SessionDetail, type Subagent, type SubagentStatus, type Submission } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { useDensity } from '../lib/density';
import { historyPage } from '../lib/historyArchive';
import { FILTER_OVER, GROUP_OVER, IDENTITY_LIMIT, LIVE_ROWS, PAGE_ROWS, countParts, countSubagents, earlierTag, inFilter, indexGroups, liveRows, liveSet, matches, ranAgain, replyIndex, runLines, statusGroups, subagentNoun, type CountPart, type IdentityTone, type IndexFilter, type IndexGroup, type LiveSubagent, type Replies, type StatusGroup, type SubagentCounts } from '../lib/subagents';
import { useResizable } from '../lib/useResizable';
import type { AgentTranscript } from '../state';
import { useFileHintItems } from './FileReferences';
import { Markdown, Note, Skeleton, Spinner, SubagentIdleIcon, WorkingMark, useApp } from './common';
import { AgentItems } from './Transcript';
import { Button } from './ui/button';
import { Collapse, EXIT_MS, usePresence } from './ui/collapse';
import { AlertDialog, Sheet, useConfirm } from './ui/dialog';
import { Input } from './ui/input';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
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
function useStops(sessionId: string): [Record<string, StopState>, (agentId: string) => void] {
  const [stops, setStops] = useState<Record<string, StopState>>({});
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

const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

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

/** Why a follow-up cannot be sent now; null when it can. */
function followUpBlocked(session: SessionDetail): string | null {
  if (readOnly(session)) return session.stage === 'settled' ? 'Settled. Reopen this task to continue the same conversation.' : 'Archived. This task is read-only.';
  if (LIVE.includes(session.state)) return 'Unavailable while the task is running a turn.';
  return null;
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
          <span aria-hidden="true" className="fade-rule-y h-full w-px flex-none transition-[background-color,width] duration-100 group-hover/handle:w-0.5 group-hover/handle:bg-accent group-focus-visible/handle:w-0.5 group-focus-visible/handle:bg-accent group-active/handle:bg-accent" />
        </div>
        {children}
      </div>
    </aside>
  );
}

export function PanelHeader({ children, className }: Readonly<{ children: ReactNode; className?: string }>) {
  return <div className={cn('pane-header flex h-header shrink-0 items-center gap-1.5 pr-2 pl-3', className)}>{children}</div>;
}

/** Where a row is drawn: a reply's list (in Detailed, its activity run), the live card, or the header index (subagents not placed in the conversation). A Task expands one row at a time. */
type Place = 'list' | 'live' | 'index';

/**
 * A request from `locate` to show the row of the subagent `toolCallId` spawned: its reply's list
 * opens, and the group and page holding the row; with `expand` the row opens too. `n` counts the
 * requests, so asking twice for the same row still opens it.
 */
export interface Reveal {
  toolCallId: string;
  expand: boolean;
  n: number;
}

interface Expanded {
  id: string;
  place: Place;
}

/** Everything a subagent row may read, swapped whole on every change of the Task; rows select only what they show. */
interface ScopeData {
  session: SessionDetail;
  agents: Record<string, AgentTranscript>;
  agentSteps: Record<string, Item>;
  snapshotSeq: number;
  /** The window's and the live tail's items by id, for a subagent's `task` call. */
  items: ReadonlyMap<string, Item>;
  /** The identity index (it outlives history pages), for when a call ran. */
  index: readonly Item[];
  replies: Replies;
  /** The live card's set; empty while there is no card. */
  live: LiveSubagent[];
  liveIds: ReadonlySet<string>;
  stops: Record<string, StopState>;
  expanded: Expanded | null;
  reveal: Reveal | null;
}

/** Stable for the life of the scope, so rows never render for a new callback. */
interface ScopeActions {
  /** Scroll to the row of the subagent a call spawned, opening what holds it; `expand` opens the row. */
  locate: (toolCallId: string, expand?: boolean) => void;
  /** Scroll to the reply a user message started and flash its turn line. */
  jumpToReply: (key: string) => void;
  /** Asks to confirm, then stops. */
  stop: (s: Subagent) => void;
  /** Opens one row (closing any other) or none; `row`, the opened row, stays in view. */
  expand: (next: Expanded | null, row?: HTMLElement | null) => void;
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
function useScope<T>(select: (d: ScopeData) => T): T | undefined {
  const scope = useContext(SubagentContext);
  return useSyncExternalStore(scope?.store.subscribe ?? NO_SUBSCRIBE, () => (scope ? select(scope.store.data) : undefined));
}
const useActions = () => useContext(SubagentContext)?.actions;

/** The replies of the Task and the subagents each spawned (lib/subagents `replyIndex`); undefined outside a Task. */
export const useSubagentReplies = () => useScope((d) => d.replies);
/** The subagents the live card shows, while it shows. */
export const useLiveSubagentIds = () => useScope((d) => d.liveIds);

/**
 * The Task's subagents for everything that draws them (the reply chips and lists, the live card,
 * the header index): the session, the transcripts held, the one expanded row, stop requests and
 * their one confirmation, and `locate`.
 */
export function SubagentScope({ session, agents, agentSteps, snapshotSeq, reveal, onLocate, onJumpToReply, onExpand, children }: Readonly<{ session: SessionDetail; agents: Record<string, AgentTranscript>; agentSteps: Record<string, Item>; snapshotSeq: number; reveal: Reveal | null; onLocate: (toolCallId: string, expand?: boolean) => void; onJumpToReply: (key: string) => void; /** A row opened in the conversation: keep it in view. */ onExpand: (row: HTMLElement) => void; children: ReactNode }>) {
  const [stops, stopNow] = useStops(session.id);
  // Stopping a subagent ends its work for good, so it is confirmed first (DESIGN.md Confirmations).
  const stopConfirm = useConfirm<Subagent>();
  const [expanded, setExpanded] = useState<Expanded | null>(null);
  const [seen, setSeen] = useState(reveal?.n);
  if (reveal && reveal.n !== seen) {
    setSeen(reveal.n);
    const s = reveal.expand ? session.subagents.find((x) => x.parent_tool_call_id === reveal.toolCallId) : undefined;
    if (s) setExpanded({ id: s.id, place: 'list' });
  }
  const items = useMemo(() => new Map([...session.items, ...(session.recent_items ?? [])].map((item) => [item.id, item])), [session.items, session.recent_items]);
  const index = session.history_index ?? session.items;
  // The index changes with every streamed token; the replies only with a message or a `task` call, so they are kept until then.
  const shape = useMemo(() => index.flatMap((item) => ((item.kind === 'user' && !item.delivery) || isSubagentCall(item) ? [`${item.id}@${item.time}`] : [])).join(','), [index]);
  const [held, setHeld] = useState(() => ({ shape, subagents: session.subagents, replies: replyIndex(index, session.subagents) }));
  let replies = held.replies;
  if (held.shape !== shape || held.subagents !== session.subagents) {
    replies = replyIndex(index, session.subagents);
    setHeld({ shape, subagents: session.subagents, replies });
  }
  const keep = expanded?.place === 'live' ? expanded.id : undefined;
  const live = useMemo(() => liveSet(replies.list.at(-1), session.subagents, keep), [replies, session.subagents, keep]);
  const liveIds = useMemo(() => new Set(live.map((x) => x.subagent.id)), [live]);
  // An open row that nothing can draw any more (its subagent gone, its reply's list out of the window) closes.
  if (expanded) {
    const s = session.subagents.find((x) => x.id === expanded.id);
    const parent = s?.parent_tool_call_id ?? '';
    if (!s || (expanded.place === 'list' && !items.has(parent) && !items.has(replies.ofCall.get(parent)?.key ?? ''))) setExpanded(null);
  }
  const data = useMemo<ScopeData>(() => ({ session, agents, agentSteps, snapshotSeq, items, index, replies, live, liveIds, stops, expanded, reveal }), [session, agents, agentSteps, snapshotSeq, items, index, replies, live, liveIds, stops, expanded, reveal]);
  const [store] = useState(() => new ScopeStore(data));
  useLayoutEffect(() => store.set(data), [store, data]);
  const handlers = useRef({ onLocate, onJumpToReply, onExpand, ask: stopConfirm.ask });
  useLayoutEffect(() => {
    handlers.current = { onLocate, onJumpToReply, onExpand, ask: stopConfirm.ask };
  });
  const [value] = useState(() => ({
    store,
    actions: {
      locate: (id: string, expand?: boolean) => handlers.current.onLocate(id, expand),
      jumpToReply: (key: string) => handlers.current.onJumpToReply(key),
      stop: (s: Subagent) => handlers.current.ask(s),
      expand: (next: Expanded | null, row?: HTMLElement | null) => {
        setExpanded(next);
        if (next && row) handlers.current.onExpand(row);
      },
    } satisfies ScopeActions,
  }));
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
          const id = stopConfirm.target?.id;
          stopConfirm.close();
          if (id) stopNow(id);
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
const STRIPE: Record<IdentityTone, string> = {
  violet: 'shadow-[inset_3px_0_0_var(--color-badge-violet)]',
  pink: 'shadow-[inset_3px_0_0_var(--color-badge-pink)]',
  cyan: 'shadow-[inset_3px_0_0_var(--color-badge-cyan)]',
  amber: 'shadow-[inset_3px_0_0_var(--color-badge-amber)]',
  teal: 'shadow-[inset_3px_0_0_var(--color-badge-teal)]',
};
const PART_TONE: Record<CountPart['tone'], string> = { muted: 'text-muted', success: 'text-success', error: 'text-error', accent: 'text-accent' };
const STATUS_WORD: Record<SubagentStatus, string> = { running: 'running', idle: 'idle', completed: 'completed', failed: 'failed', cancelled: 'stopped' };

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

/** The line under a subagent's name: what it is doing or what it reported (`subagentSummary`). */
function rowSummary(d: ScopeData, s: Subagent): string {
  const steps = d.agents[s.id]?.items ?? (d.agentSteps[s.id] ? [d.agentSteps[s.id]] : undefined);
  return subagentSummary(s, steps, d.items.get(s.parent_tool_call_id ?? '')?.tool);
}

/** When a call ran, from the items held or the identity index. */
const callTime = (d: ScopeData, id: string) => d.items.get(id)?.time ?? d.index.find((item) => item.id === id)?.time;

/**
 * A reply's subagents on its turn line (DESIGN.md subagent chip): identity dots (the `Bot` glyph
 * past five calls), then "3 subagents · 2 done · 1 failed", and "2 of 3 loaded" while some of the
 * reply's calls have no subagent here yet. It opens the reply's list under the line.
 */
export function SubagentChip({ subagents, calls, tones, open, controls, onToggle }: Readonly<{ subagents: Subagent[]; /** The reply's `task` calls, loaded or not. */ calls: number; tones: ReadonlyMap<string, IdentityTone>; open: boolean; controls?: string; onToggle: () => void }>) {
  const c = countSubagents(subagents);
  const total = Math.max(calls, subagents.length);
  const lead = subagentNoun(total);
  const partial = subagents.length < total ? `${subagents.length} of ${total} loaded` : '';
  return (
    <button
      type="button"
      data-subagent-items={JSON.stringify(subagents.flatMap((s) => (s.parent_tool_call_id ? [s.parent_tool_call_id] : [])))}
      aria-expanded={open}
      aria-controls={controls}
      title={[lead, partial, ...countParts(c).map((p) => p.text)].filter(Boolean).join(' · ')}
      className={cn('flex h-6 min-w-0 shrink items-center gap-1.5 rounded-full px-2 text-caption shadow-well transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11', open ? 'bg-tint-selected text-ink' : 'bg-tint-well text-body')}
      onClick={onToggle}
    >
      {total <= IDENTITY_LIMIT ? (
        <span aria-hidden="true" className="flex shrink-0 items-center gap-1">
          {subagents.map((s) => {
            const tone = tones.get(s.id);
            return <span key={s.id} className={cn('size-2 rounded-full', tone ? DOT[tone] : 'bg-faint')} />;
          })}
        </span>
      ) : (
        <Bot aria-hidden="true" className="size-3.5 shrink-0 text-muted" />
      )}
      <span className="min-w-0 truncate tabular-nums">
        <Counts c={c} lead={partial ? <>{lead} · <span className="text-muted">{partial}</span></> : lead} />
      </span>
      <ChevronDown aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-180')} />
    </button>
  );
}

/**
 * A reply's subagents: a row each in spawn order. More than eight are grouped by status (Failed
 * and Running open, Done and Stopped folded; the group of the open row always open), more than
 * twelve get a filter by name or result, and a group renders 50 rows at a time. A `locate` for
 * one of them opens its group and page. Reused by Detailed for a reply past eight.
 */
export function SubagentList({ id, subagents, calls = subagents.length, tones }: Readonly<{ id: string; subagents: Subagent[]; calls?: number; tones: ReadonlyMap<string, IdentityTone> }>) {
  const grouped = subagents.length > GROUP_OVER;
  const [query, setQuery] = useState('');
  const [folds, setFolds] = useState<Partial<Record<StatusGroup['key'], boolean>>>({});
  const [limits, setLimits] = useState<Partial<Record<StatusGroup['key'], number>>>({});
  const reveal = useScope((d) => d.reveal);
  const open = useScope((d) => (d.expanded?.place === 'list' ? d.expanded.id : ''));
  // Only while a filter is set does the list read every row's line.
  const filtering = useScope((d) => (query ? d : null));
  // A reveal from before this list mounted is not replayed (`locate` asks again once it has opened what holds the list).
  const [seen, setSeen] = useState(reveal?.n);
  if (reveal && reveal.n !== seen) {
    setSeen(reveal.n);
    const target = subagents.find((s) => s.parent_tool_call_id === reveal.toolCallId);
    const group = target && grouped ? statusGroups(subagents).find((g) => g.subagents.includes(target)) : undefined;
    if (target && group) {
      setQuery('');
      setFolds((f) => ({ ...f, [group.key]: true }));
      const at = group.subagents.indexOf(target) + 1;
      setLimits((l) => ({ ...l, [group.key]: Math.max(l[group.key] ?? PAGE_ROWS, at) }));
    }
  }
  const shown = filtering ? subagents.filter((s) => matches(s, rowSummary(filtering, s), query)) : subagents;
  const row = (s: Subagent) => (
    <li key={s.id}>
      <SubagentRow subagent={s} tone={tones.get(s.id)} place="list" anchor />
    </li>
  );
  const total = Math.max(calls, subagents.length);
  const lead = subagentNoun(total);
  return (
    <section id={id} aria-label={lead} className="overflow-hidden rounded-md bg-raised shadow-raised">
      <header className="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1 bg-surface px-3.5 py-1.5">
        <span className="flex items-center gap-2">
          <Bot aria-hidden="true" className="size-4 shrink-0 text-muted" />
          <span className="text-ui font-semibold text-ink">{lead}</span>
        </span>
        {subagents.length > FILTER_OVER && (
          <Input size="sm" type="search" value={query} onChange={(e) => setQuery(e.target.value)} aria-label={`Filter ${subagentNoun(subagents.length)} by name or result`} placeholder={`Filter ${subagents.length} by name or result`} className="max-w-80 min-w-40 flex-1 text-caption" />
        )}
      </header>
      {grouped ? (
        statusGroups(shown).map((g) => {
          // A filter shows every match, and the open row's group stays open.
          const holdsOpen = !!open && g.subagents.some((s) => s.id === open);
          const opened = query || holdsOpen ? true : folds[g.key] ?? g.open;
          const limit = Math.max(limits[g.key] ?? PAGE_ROWS, holdsOpen ? g.subagents.findIndex((s) => s.id === open) + 1 : 0);
          const listId = `${id}-${g.key}`;
          return (
            <div key={g.key}>
              <button
                type="button"
                aria-expanded={opened}
                aria-controls={opened ? listId : undefined}
                disabled={!!query || holdsOpen}
                className="flex h-8 w-full items-center gap-2 bg-surface px-3.5 text-left text-caption text-muted transition-colors duration-100 hover:text-body focus-visible:-outline-offset-2 pointer-coarse:min-h-11"
                onClick={() => setFolds((f) => ({ ...f, [g.key]: !opened }))}
              >
                <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', opened && 'rotate-90')} />
                <span className={cn('font-semibold', g.key === 'failed' && 'text-error', g.key === 'running' && 'text-accent')}>{g.label}</span>
                <span className="tabular-nums">{g.subagents.length}</span>
              </button>
              {opened && <ul id={listId} className="divide-y divide-hairline">{g.subagents.slice(0, limit).map(row)}</ul>}
              {opened && g.subagents.length > limit && (
                <button type="button" className="flex h-8 w-full items-center px-3.5 text-left text-caption text-accent hover:underline focus-visible:-outline-offset-2 pointer-coarse:min-h-11" onClick={() => setLimits((l) => ({ ...l, [g.key]: limit + PAGE_ROWS }))}>
                  Show {Math.min(PAGE_ROWS, g.subagents.length - limit)} more
                </button>
              )}
            </div>
          );
        })
      ) : (
        <ul className="divide-y divide-hairline">{shown.map(row)}</ul>
      )}
      {query && !shown.length && <p className="px-3.5 py-2 text-caption text-muted">No subagent matches “{query}”.</p>}
      {subagents.length < total && <p className="bg-surface px-3.5 py-2 text-caption text-muted">{total - subagents.length} more not loaded here: the header’s Subagents list reads older ones from the record.</p>}
    </section>
  );
}

/**
 * One subagent (DESIGN.md subagent row): its status mark, its identity (a 3px stripe and a dot,
 * while its reply has at most five), its name, one line of what it is doing or reported, model,
 * duration and runs. It expands in place onto its description, its runs, its own transcript in a
 * bounded scroller, Stop while it runs and a follow-up while it is idle; Esc inside folds it back.
 * In a reply's list (`anchor`) it carries its `task` call's id, so "Show where it was spawned"
 * lands on it. Memoised: it renders again only when what it shows changes.
 */
export const SubagentRow = memo(function SubagentRow({ subagent: s, tone, place, anchor = false, earlier = false }: { subagent: Subagent; tone?: IdentityTone; place: Place; anchor?: boolean; /** Spawned by an earlier reply (the live card): a tag says how it came back and leads there. */ earlier?: boolean }) {
  const actions = useActions();
  const { meta } = useApp();
  const [, copy] = useCopied();
  const toggleRef = useRef<HTMLButtonElement>(null);
  const panelId = useId();
  const expanded = useScope((d) => d.expanded?.id === s.id && d.expanded.place === place) ?? false;
  const summary = useScope((d) => rowSummary(d, s)) ?? '';
  const provider = useScope((d) => d.session.provider) ?? '';
  const stopping = useScope((d) => d.stops[s.id]) ?? NOT_STOPPING;
  const locked = useScope((d) => readOnly(d.session)) ?? true;
  // "ran again 17:26 ↓", in an earlier reply's list: "<start>|<reply>".
  const again = useScope((d) => (place === 'list' ? ranAgainKey(s, d.replies) : '')) ?? '';
  const presence = usePresence(expanded);
  if (!actions) return null;
  const name = s.name || 'Subagent';
  const parentId = s.parent_tool_call_id;
  const took = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  const when = took ?? (s.status === 'running' && s.started_at ? `since ${clock(s.started_at)}` : '');
  const runs = s.runs?.length ?? 0;
  const info = [s.model ? modelName(meta, provider, s.model) : '', when, runs > 1 ? `${runs} runs` : ''].filter(Boolean).join(' · ');
  const [againAt, againReply] = again.split('|');
  const collapse = () => {
    actions.expand(null);
    toggleRef.current?.focus({ preventScroll: true });
  };
  const items: ActionItem[] = [
    { key: 'copy', label: 'Copy agent ID', icon: <Copy />, onSelect: () => copy(s.id) },
    ...(parentId ? [{ key: 'locate', label: 'Show where it was spawned', icon: <Crosshair />, onSelect: () => actions.locate(parentId) }] : []),
    ...(s.status === 'running' ? [{ key: 'stop', label: stopping.requested ? 'Stop requested' : 'Stop subagent', icon: <Square />, danger: true, disabled: locked || stopping.busy || stopping.requested, onSelect: () => actions.stop(s), separator: true }] : []),
  ];
  return (
    <div id={anchor && parentId ? `item-${parentId}` : undefined} data-subagent-row="" className={cn('flex flex-col', tone && STRIPE[tone])}>
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div className="group/agent relative flex min-h-10 items-center gap-2.5 py-1.5 pr-1.5 pl-3.5 transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11" />}>
          {/* The whole line toggles; the tags and the actions sit above it. */}
          <button
            ref={toggleRef}
            type="button"
            data-subagent-toggle=""
            aria-expanded={expanded}
            aria-controls={expanded ? panelId : undefined}
            aria-label={`${name}, ${STATUS_WORD[s.status]}${summary ? `: ${summary}` : ''}`}
            title={[name, summary].filter(Boolean).join('\n')}
            className="absolute inset-0 focus-visible:-outline-offset-2"
            onClick={(e) => actions.expand(expanded ? null : { id: s.id, place }, expanded || place === 'index' ? null : e.currentTarget.closest<HTMLElement>('[data-subagent-row]'))}
          />
          <span className="flex size-4 shrink-0 items-center justify-center">
            <SubagentMark status={s.status} />
          </span>
          {tone && <span aria-hidden="true" className={cn('size-2 shrink-0 rounded-full', DOT[tone])} />}
          <span className="flex min-w-0 flex-1 flex-col sm:flex-row sm:items-center sm:gap-3">
            <span className="min-w-0 truncate text-ui font-medium text-ink sm:max-w-[45%] sm:shrink-0">{name}</span>
            {summary && <span className={cn('min-w-0 truncate text-caption', s.status === 'failed' ? 'text-error' : 'text-body')}>{summary}</span>}
          </span>
          {earlier && <EarlierTag subagent={s} />}
          {againAt && (
            <button type="button" className={TAG} title="Show the reply it ran in again" onClick={() => actions.jumpToReply(againReply)}>
              ran again {clock(againAt)} ↓
            </button>
          )}
          {info && <span className="shrink-0 text-meta tabular-nums text-muted max-md:hidden">{info}</span>}
          <ChevronRight aria-hidden="true" className={cn('size-3.5 shrink-0 text-faint transition-transform duration-160 ease-app', expanded && 'rotate-90')} />
          <Menu.Root modal={false}>
            <Menu.Trigger render={<Button size="icon-sm" aria-label={`Actions for subagent ${name}`} className="relative text-muted opacity-0 transition-opacity group-hover/agent:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" />}>
              <Ellipsis />
            </Menu.Trigger>
            <Menu.Content align="end">
              <Menu.Actions items={items} />
            </Menu.Content>
          </Menu.Root>
        </ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>
      {stopping.error && (
        <p role="alert" className="pr-2 pb-1.5 pl-10 text-caption text-error">
          Could not stop it: {stopping.error}
        </p>
      )}
      {presence.mounted && (
        <Collapse open={expanded} appear onClosed={presence.onClosed}>
          <SubagentDetail id={panelId} open={expanded} subagent={s} name={name} onCollapse={collapse} />
        </Collapse>
      )}
    </div>
  );
});

const TAG = 'relative max-w-[40%] min-w-0 truncate rounded-full bg-tint-well px-2 py-0.5 text-meta whitespace-nowrap text-muted shadow-well transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11';

/** The "ran again" target as one string, so the row's selection stays a primitive. */
function ranAgainKey(s: Subagent, replies: Replies): string {
  const again = ranAgain(s, replies);
  return again ? `${again.at}|${again.reply}` : '';
}

/**
 * On a live row an earlier reply spawned: "resumed by the agent · first ran 17:05 ↑" or "your
 * follow-up · first ran 17:05 ↑" from its runs, else "from an earlier reply ↑". It leads to that reply's row.
 */
function EarlierTag({ subagent }: Readonly<{ subagent: Subagent }>) {
  const actions = useActions();
  const parentId = subagent.parent_tool_call_id;
  const parentTime = useScope((d) => (parentId ? callTime(d, parentId) : undefined));
  const tag = earlierTag(subagent, parentTime);
  const text = tag.first ? `${tag.text} · first ran ${clock(tag.first)}` : tag.text;
  if (!parentId || !actions) return <span className={cn(TAG, 'hover:bg-tint-well hover:text-muted')}>{text}</span>;
  return (
    <button type="button" className={TAG} title="Show where it was first spawned" onClick={() => actions.locate(parentId)}>
      {text} ↑
    </button>
  );
}

/** Whether a popup other than the one holding `target` (a menu, a dialog) is open and owns the keys. */
const otherPopupOpen = (target: EventTarget) => [...document.querySelectorAll('[data-popup]:not([data-popup="tooltip"])')].some((popup) => !popup.contains(target as Node));

/** An expanded row: what the subagent was asked, its runs, its transcript (fetched while open), Stop while it runs, the follow-up while it is idle. */
function SubagentDetail({ id, open, subagent: s, name, onCollapse }: Readonly<{ id: string; open: boolean; subagent: Subagent; name: string; onCollapse: () => void }>) {
  const { meta } = useApp();
  const actions = useActions();
  // The open row follows the session as a whole (its transcript and composer need it).
  const d = useScope((x) => x);
  const [seek, setSeek] = useState<{ time: string; n: number } | null>(null);
  if (!d || !actions) return null;
  const { session } = d;
  const setup = [s.model ? modelName(meta, session.provider, s.model) : '', s.effort ?? '', s.started_at ? `started ${clock(s.started_at)}` : ''].filter(Boolean).join(' · ');
  const parent = d.items.get(s.parent_tool_call_id ?? '');
  const stopping = d.stops[s.id] ?? NOT_STOPPING;
  const runs = runLines(s);
  return (
    // eslint-disable-next-line jsx-a11y/no-noninteractive-element-interactions -- Esc anywhere inside folds the row back.
    <div
      id={id}
      role="region"
      aria-label={`Subagent ${name}`}
      className="flex flex-col gap-2 pt-1 pr-2 pb-3 pl-3.5"
      onKeyDown={(e) => {
        if (e.key !== 'Escape' || e.defaultPrevented || otherPopupOpen(e.target)) return;
        e.preventDefault();
        e.stopPropagation();
        onCollapse();
      }}
    >
      {(s.description || setup) && (
        <div className="flex flex-col gap-0.5 pl-6.5">
          {s.description && <p className="line-clamp-2 text-caption text-body" title={s.description}>{s.description}</p>}
          {setup && <p className="text-meta text-muted">{setup}</p>}
        </div>
      )}
      {runs.length > 0 && (
        <ol aria-label="Runs" className="flex flex-col pl-5">
          {runs.map((r) => (
            <li key={r.n}>
              <button type="button" className="block h-6 max-w-full truncate rounded-sm px-1.5 text-left leading-6 text-caption text-muted tabular-nums transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11" title="Show this run in the transcript" onClick={() => setSeek((x) => ({ time: r.started_at, n: (x?.n ?? 0) + 1 }))}>
                <span className="text-body">Run {r.n}</span> · {clock(r.started_at)} · {r.trigger} · <span className={PART_TONE[r.tone]}>{r.outcome}</span>
              </button>
            </li>
          ))}
        </ol>
      )}
      <DetailVisibility open={open}>
        <div className="flex flex-col overflow-hidden rounded-md bg-surface shadow-well">
          <AgentTranscriptView
            key={s.id}
            name={name}
            provider={session.provider}
            sessionId={session.id}
            workdir={session.workdir}
            subagent={s}
            interactions={session.interactions}
            transcript={d.agents[s.id]}
            snapshotSeq={d.snapshotSeq}
            open={open}
            parent={parent}
            result={s.status === 'completed' || s.status === 'idle' ? parent?.tool?.output : undefined}
            seek={seek}
          />
        </div>
      </DetailVisibility>
      {s.status === 'running' && (
        <div className="flex flex-wrap items-center gap-2">
          <StopSubagent session={session} subagent={s} stopping={stopping} onStop={() => actions.stop(s)} />
        </div>
      )}
      <SubagentComposer session={session} subagent={s} />
    </div>
  );
}

/** Done, failed and running over the whole set, from the left, on the well; the counts beside it say the same in words. */
function Progress({ c }: Readonly<{ c: SubagentCounts }>) {
  const scale = (n: number) => ({ transform: `scaleX(${n / c.total})` });
  const bar = 'absolute inset-0 origin-left transition-transform duration-240 ease-app';
  return (
    <div aria-hidden="true" className="relative h-1.5 overflow-hidden rounded-full bg-tint-well">
      <div className={cn(bar, 'bg-accent opacity-45')} style={scale(c.done + c.failed + c.running)} />
      <div className={cn(bar, 'bg-error')} style={scale(c.done + c.failed)} />
      <div className={cn(bar, 'bg-success')} style={scale(c.done)} />
    </div>
  );
}

const NO_LIVE: LiveSubagent[] = [];

/**
 * The live subagent card (DESIGN.md), at the conversation's foot while one of its subagents runs
 * or one of its rows is open: the latest reply's subagents and any earlier one still or again
 * running, counted in its head (a progress bar past five), the failed and running ones as rows,
 * five at most until "Show all"; the open row stays whatever becomes of it. A subagent failing is
 * said once, politely.
 */
export function LiveSubagents() {
  const set = useScope((d) => d.live) ?? NO_LIVE;
  const keep = useScope((d) => (d.expanded?.place === 'live' ? d.expanded.id : ''));
  const tones = useScope((d) => d.replies.tones);
  const [all, setAll] = useState(false);
  const [limit, setLimit] = useState(PAGE_ROWS);
  const listId = useId();
  // Failures said aloud: the ones seen failed already, and the last sentence.
  const failedNow = set.filter((x) => x.subagent.status === 'failed').map((x) => x.subagent.id).join(',');
  const [failedSeen, setFailedSeen] = useState(failedNow);
  const [announced, setAnnounced] = useState('');
  if (failedNow !== failedSeen) {
    const fresh = set.find((x) => x.subagent.status === 'failed' && !failedSeen.split(',').includes(x.subagent.id));
    setFailedSeen(failedNow);
    if (fresh) setAnnounced(`Subagent ${fresh.subagent.name || 'Subagent'} failed.`);
  }
  // The card left: it comes back folded.
  if (!set.length && (all || limit !== PAGE_ROWS)) {
    setAll(false);
    setLimit(PAGE_ROWS);
  }
  const status = (
    <p role="status" className="sr-only">
      {announced}
    </p>
  );
  if (!set.length || !tones) return status;
  const c = countSubagents(set.map((x) => x.subagent));
  const { rows, rest } = liveRows(set, all, keep || undefined);
  return (
    <>
      {status}
      <section aria-label="Subagents at work" className="animate-rise overflow-hidden rounded-md bg-raised shadow-float">
        <header className="flex flex-col gap-2 bg-surface px-3.5 py-2">
          <div className="flex min-h-5 flex-wrap items-center gap-x-2.5 gap-y-0.5">
            <Bot aria-hidden="true" className="size-4 shrink-0 text-muted" />
            <span className="text-ui font-semibold text-ink">{c.running === c.total ? `${subagentNoun(c.total)} running` : subagentNoun(c.total)}</span>
            {c.running < c.total && (
              <span className="text-caption tabular-nums">
                <Counts c={c} />
              </span>
            )}
          </div>
          {c.total > LIVE_ROWS && <Progress c={c} />}
        </header>
        <ul id={listId} className="divide-y divide-hairline">
          {rows.slice(0, limit).map(({ subagent, earlier }) => (
            <li key={subagent.id}>
              <SubagentRow subagent={subagent} tone={tones.get(subagent.id)} place="live" earlier={earlier} />
            </li>
          ))}
        </ul>
        {rows.length > limit && (
          <button type="button" className="flex h-8 w-full items-center px-3.5 text-left text-caption text-accent hover:underline focus-visible:-outline-offset-2 pointer-coarse:min-h-11" onClick={() => setLimit(limit + PAGE_ROWS)}>
            Show {Math.min(PAGE_ROWS, rows.length - limit)} more
          </button>
        )}
        {(rest || all) && (
          <button
            type="button"
            aria-expanded={all}
            aria-controls={listId}
            className="flex h-8 w-full items-center gap-2 bg-surface px-3.5 text-left text-caption text-muted transition-colors duration-100 hover:text-body focus-visible:-outline-offset-2 pointer-coarse:min-h-11"
            onClick={() => {
              setAll(!all);
              setLimit(PAGE_ROWS);
            }}
          >
            <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', all && 'rotate-90')} />
            <span className="min-w-0 flex-1 truncate tabular-nums">{all ? `All ${c.total}` : rest}</span>
            <span className="shrink-0 text-accent">{all ? 'Show fewer' : `Show all ${c.total}`}</span>
          </button>
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
];
/** Rows the header index renders before "Show 100 more". */
const INDEX_ROWS = 100;

/**
 * The Task header's subagents (DESIGN.md subagent index): the count, the working mark while one
 * runs, and a popover to find any of them: search, status filters, the subagents grouped by the
 * message that started their reply (newest first, each with Jump), the ones not placed in the
 * conversation (they open right there) and the older ones from the record. Picking a placed one
 * scrolls to its row and expands it.
 */
export function SubagentIndex({ labels, error }: Readonly<{ labels: boolean; /** Why the last jump did not land, said where the reader is. */ error?: string }>) {
  const actions = useActions();
  const session = useScope((d) => d.session);
  const [open, setOpen] = useState(false);
  // Where to go once the popover has closed; a pick keeps focus off the trigger (`finalFocus`), the row it lands on takes it.
  const picked = useRef<{ id: string; expand: boolean } | null>(null);
  const input = useRef<HTMLInputElement>(null);
  if (!session || !actions) return null;
  const running = session.subagents.filter((s) => s.status === 'running').length;
  const label = `Subagents, ${session.subagents.length}${session.subagents_before ? ' or more' : ''}${running ? `, ${running} running` : ''}`;
  return (
    <Popover.Root
      open={open}
      onOpenChange={(o) => {
        if (o) picked.current = null;
        // A row opened in here goes with it.
        else actions.expand(null);
        setOpen(o);
      }}
      onOpenChangeComplete={(o) => {
        if (!o && picked.current) actions.locate(picked.current.id, picked.current.expand);
      }}
    >
      <Tip label="Subagents">
        <Popover.Trigger render={<Button id="subagents-link" size="md" aria-label={label} className="px-2 text-muted" />}>
          <Bot />
          {labels && <span>Subagents</span>}
          <span className="tabular-nums text-ink">
            {session.subagents.length}
            {session.subagents_before && '+'}
          </span>
          {running > 0 && <WorkingMark />}
          <ChevronDown className="text-muted" />
        </Popover.Trigger>
      </Tip>
      <Popover.Content side="bottom" align="end" sideOffset={4} initialFocus={input} finalFocus={() => !picked.current} aria-label="Subagents" className="w-[440px] max-w-(--available-width) gap-1.5 p-1.5">
        <IndexBody
          input={input}
          error={error}
          onPick={(id, expand) => {
            picked.current = { id, expand };
            setOpen(false);
          }}
        />
      </Popover.Content>
    </Popover.Root>
  );
}

function IndexBody({ input, error, onPick }: Readonly<{ input: RefObject<HTMLInputElement | null>; error?: string; onPick: (toolCallId: string, expand: boolean) => void }>) {
  // The index is open only while someone reads it: it may follow the session as a whole.
  const d = useScope((x) => x);
  const [query, setQuery] = useState('');
  const [filter, setFilter] = useState<IndexFilter>('all');
  const [limit, setLimit] = useState(INDEX_ROWS);
  const session = d?.session;
  const groups = useMemo(() => (session && d ? indexGroups(d.index, [...session.items, ...(session.recent_items ?? [])], session.subagents) : []), [d, session]);
  if (!d || !session) return null;
  const all = countSubagents(session.subagents);
  const counts: Record<IndexFilter, number> = { all: all.total, running: all.running, failed: all.failed, done: all.done };
  // At most `limit` rows across the groups; the rest wait behind "Show more".
  let budget = limit;
  let hidden = 0;
  const shown: (IndexGroup & { rows: Subagent[] })[] = [];
  for (const g of groups) {
    const rows = g.subagents.filter((s) => inFilter(s, filter) && matches(s, rowSummary(d, s), query));
    const take = rows.slice(0, Math.max(0, budget));
    hidden += rows.length - take.length;
    budget -= take.length;
    if (take.length) shown.push({ ...g, rows: take });
  }
  const reset = () => setLimit(INDEX_ROWS);
  return (
    <>
      {error && (
        <Note tone="warn" role="alert">
          {error}
        </Note>
      )}
      <Input ref={input} size="md" type="search" value={query} onChange={(e) => { setQuery(e.target.value); reset(); }} aria-label="Search subagents" placeholder={`Search ${all.total} subagents`} />
      <div role="group" aria-label="Show subagents by status" className="flex flex-wrap gap-1">
        {FILTERS.map((f) => (
          <button
            key={f.key}
            type="button"
            aria-pressed={filter === f.key}
            className={cn('h-6 rounded-full px-2 text-caption tabular-nums transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11', filter === f.key ? 'bg-tint-selected text-ink' : f.tone)}
            onClick={() => { setFilter(f.key); reset(); }}
          >
            {f.label} {counts[f.key]}
          </button>
        ))}
      </div>
      <div className="-mx-1.5 max-h-[min(60vh,480px)] overflow-y-auto overscroll-contain px-1.5">
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
                  <Counts c={{ ...countSubagents(g.subagents), done: 0, stopped: 0 }} lead={`${g.subagents.length}`} />
                </span>
                {g.key && first && (
                  <button type="button" aria-label={`Jump to the subagents of ${title}`} className="shrink-0 rounded-xs text-caption text-accent hover:underline pointer-coarse:min-h-11" onClick={() => onPick(first, false)}>
                    Jump
                  </button>
                )}
              </div>
              {g.key ? (
                <ul className="flex flex-col">
                  {g.rows.map((s) => {
                    const summary = rowSummary(d, s);
                    return (
                      <li key={s.id}>
                        <button
                          type="button"
                          title={summary || undefined}
                          className="flex min-h-[30px] w-full items-center gap-2 rounded-sm py-1 pr-2 pl-5 text-left text-ui text-body transition-colors duration-100 hover:bg-tint-hover hover:text-ink pointer-coarse:min-h-11"
                          onClick={() => onPick(s.parent_tool_call_id!, true)}
                        >
                          <span className="flex size-4 shrink-0 items-center justify-center">
                            <SubagentMark status={s.status} />
                          </span>
                          <span className="min-w-0 flex-1 truncate">{s.name || 'Subagent'}</span>
                          <span className="sr-only">, {STATUS_WORD[s.status]}</span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              ) : (
                // Not placed in the conversation (no call, or one past the history held): they open right here.
                <ul className="flex flex-col overflow-hidden rounded-md bg-raised shadow-raised">
                  {g.rows.map((s) => (
                    <li key={s.id}>
                      <SubagentRow subagent={s} place="index" />
                    </li>
                  ))}
                </ul>
              )}
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
      {session.subagents_before && <OlderSubagents key={`${session.id}:${session.subagents_before}`} sessionId={session.id} before={session.subagents_before} />}
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
 * One subagent's transcript, in its expanded row: a scroller at most 60vh tall that keeps its
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
  seek?: { time: string; n: number } | null;
}>) {
  const { dispatch } = useApp();
  const density = useDensity();
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const live = subagent.status === 'running';
  const [attempt, setAttempt] = useState(0);
  const detail = useDetailAgent(subagent.id, open);
  const transcript = detail.compact ? detail.agent : legacyTranscript;
  const parentItem: Item = parent ?? { id: subagent.parent_tool_call_id ?? '', kind: 'tool', time: '', compact: { has_text: false, has_reasoning: false } };
  const { item: parentFullItem, body: parentBody, attach: parentAttach, retry: parentRetry } = useItemBody(parentItem, open && !!subagent.parent_tool_call_id && (subagent.status === 'completed' || subagent.status === 'idle'));
  const result = detail.compact ? parentFullItem?.tool?.output : legacyResult;
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
  const loadOnDemand = useEffectEvent(loadOlder);
  useEffect(() => {
    const el = scroller.current;
    if (!el || !open) return;
    let touchY = 0;
    const key = (event: globalThis.KeyboardEvent) => {
      if (['Home', 'PageUp', 'ArrowUp'].includes(event.key) && nearEdge(el, 'older')) loadOnDemand();
      if (['End', 'PageDown', 'ArrowDown'].includes(event.key) && nearEdge(el, 'newer')) loadOnDemand('newer');
    };
    const start = (event: TouchEvent) => { touchY = event.touches[0]?.clientY ?? 0; };
    const move = (event: TouchEvent) => {
      const y = event.touches[0]?.clientY ?? touchY;
      if (y > touchY && nearEdge(el, 'older')) loadOnDemand();
      if (y < touchY && nearEdge(el, 'newer')) loadOnDemand('newer');
      touchY = y;
    };
    el.addEventListener('keydown', key); el.addEventListener('touchstart', start, { passive: true }); el.addEventListener('touchmove', move, { passive: true });
    return () => { el.removeEventListener('keydown', key); el.removeEventListener('touchstart', start); el.removeEventListener('touchmove', move); };
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

  // Seeking a run: page towards its start until the first item at or after it is here, then show it; else say it is gone.
  const [sought, setSought] = useState(seek?.n);
  const [seeking, setSeeking] = useState<string | null>(null);
  const [lost, setLost] = useState(false);
  if (seek && seek.n !== sought) {
    setSought(seek.n);
    setSeeking(seek.time);
    setLost(false);
  }
  const seekNow = useEffectEvent(() => {
    const el = scroller.current;
    if (seeking === null || !el || !transcript || transcript.loading || detail.agent?.page) return;
    if (detail.agent?.pageError) return setSeeking(null);
    const at = items.findIndex((item) => item.time && item.time >= seeking);
    const more = detail.compact && open;
    if ((at === 0 || (at < 0 && !items.length)) && more && detail.agent?.before) return loadOlder();
    if (at < 0 && more && detail.agent?.after) return loadOlder('newer');
    setSeeking(null);
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
  }, [seeking, items, transcript?.loading, detail.agent?.page, detail.agent?.pageError]);

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
    // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The transcript scroll region accepts keyboard paging at both boundaries.
    <div className="flex max-h-[60vh] min-h-0 flex-col gap-3 overflow-y-auto overscroll-contain px-3 py-3 [overflow-wrap:anywhere]" ref={scroller} onScroll={onScroll} role="region" aria-label={`Transcript of ${name}`} tabIndex={0} aria-busy={(!transcript || transcript.loading) && items.length === 0 ? true : undefined} onWheel={event => {
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
      {result && (
        <div className="rounded-md bg-raised px-3.5 py-2.5 text-ui shadow-raised">
          <div className="mb-1 text-caption text-muted">Result sent to the main agent</div>
          <Markdown text={result} />
        </div>
      )}
    </div>
  );
}

const UNCERTAIN_FOLLOW_UP = 'The subagent may or may not have received your message. Check its transcript before sending again; it will not be resent automatically.';

/**
 * A follow-up to one idle subagent, which the main agent never sees. Shown only while the
 * subagent is idle; enabled only while the task itself is active and between turns. The
 * status change and the user item both arrive over SSE, so nothing is added optimistically.
 */
function SubagentComposer({ session, subagent }: Readonly<{ session: SessionDetail; subagent: Subagent }>) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Submission | null>(null);
  const pending = useRef<{ text: string; id: string } | null>(null);
  // An uncertain follow-up keeps the subagent running until the provider settles it; say so meanwhile.
  if (subagent.status !== 'idle') {
    return outcome?.status === 'uncertain' ? (
      <Note tone="warn" role="alert">
        {UNCERTAIN_FOLLOW_UP}
      </Note>
    ) : null;
  }
  const blocked = followUpBlocked(session);
  const cannotSubmit = busy || !!blocked || !text.trim();

  async function send() {
    const t = text.trim();
    if (cannotSubmit) return;
    if (pending.current?.text !== t) pending.current = { text: t, id: newRequestId() };
    const id = pending.current.id;
    setBusy(true);
    setError(null);
    try {
      const sub = await api.promptSubagent(session.id, subagent.id, t, id);
      setOutcome(sub);
      pending.current = null;
      if (sub.status === 'accepted') setText('');
    } catch (e) {
      // Only a transport failure leaves the outcome unknown; a server answer stands on its own.
      setError(isStatus(e, 0) ? `${describeError(e)}. Nothing will be retried automatically. Repeating this action with unchanged text uses the same request (${id.slice(0, 8)}).` : describeError(e));
    } finally {
      setBusy(false);
    }
  }
  function onKeyDown(e: KeyboardEvent<HTMLTextAreaElement>) {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    }
  }

  const textId = `subagent-text-${subagent.id}`;
  return (
    <div className="shrink-0">
      <form
        className="relative isolate flex flex-col rounded-lg bg-raised shadow-float before:pointer-events-none before:absolute before:inset-0 before:-z-10 before:rounded-[inherit] before:opacity-0 before:shadow-focus-float before:transition-opacity before:duration-160 before:content-[''] focus-within:before:opacity-100"
        aria-label={`Follow up with subagent ${subagent.name}`}
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <div className="flex flex-col gap-1 px-3 pt-2">
          <Note>Follow up with this subagent only. The main agent does not see this conversation.</Note>
          {blocked && <Note role="status">{blocked}</Note>}
          {outcome?.status === 'uncertain' && (
            <Note tone="warn" role="alert">
              {UNCERTAIN_FOLLOW_UP}
            </Note>
          )}
          {outcome?.status === 'rejected' && (
            <Note tone="error" role="alert">
              Rejected{outcome.error ? `: ${outcome.error}` : '.'}
            </Note>
          )}
          {error && (
            <Note tone="error" role="alert">
              {error}
            </Note>
          )}
        </div>
        <label className="sr-only" htmlFor={textId}>
          Follow-up for subagent {subagent.name}
        </label>
        <textarea
          id={textId}
          rows={2}
          value={text}
          placeholder="Follow up with this subagent…"
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          disabled={busy || !!blocked}
          className="max-h-40 min-h-12 w-full resize-none bg-transparent px-3 py-2 text-ui text-ink outline-hidden [field-sizing:content] max-sm:text-chat-lg"
        />
        <div className="flex justify-end px-2 pb-2">
          <Button type="submit" size="sm" variant="primary" loading={busy} disabled={cannotSubmit}>
            Send
          </Button>
        </div>
      </form>
    </div>
  );
}

/** Cancellation requests never invent a terminal status; the provider's SSE owns it. */
function StopSubagent({ session, subagent, stopping, onStop }: Readonly<{ session: SessionDetail; subagent: Subagent; stopping: StopState; onStop: () => void }>) {
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
