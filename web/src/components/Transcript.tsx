import { BodyNotice, DetailVisibility, useBodyCopy, useDisclosure, useItemBody, useWholeText, type WholeText } from './Details';
import { Bot, Check, ChevronRight, ChevronUp, Copy, Ellipsis, FileDiff, MessageCircleQuestion, Minus, Terminal, X } from 'lucide-react';
import { memo, useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode, type RefObject, type SyntheticEvent } from 'react';
import { flushSync } from 'react-dom';
import { modelName, type Interaction, type Item, type Subagent, type SubagentStatus, type ToolBoardCard, type ToolStatus, type TurnTiming } from '../api';
import { isChartCall } from '../lib/chart';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import type { Density } from '../lib/density';
import { approvalMark, askedOn, callProduct, changedFiles, currentStep, duration, elapsedSince, foregroundItems, turnElapsed, itemTook, completedDuration, isWork, newestFileDeclarations, promoted, segmentActivity, summarizeActivity, summarizeTurn, subagentSummary, timingForTurn, showTurnEnd, summarizeTools, linkInteractions, mergeByTime, questionOf, toolLabel, type AskedQuestion, type Entry, type Step, type TurnSummary } from '../lib/transcript';
import { groupIdentities } from '../lib/historyState';
import { turnVerb } from '../lib/verbs';
import type { AgentTranscript } from '../state';
import { ImageThumbs, ItemAttachments } from './Attachments';
import { ChartCard } from './Chart';
import { CodeBlock, DeclaredFileCard, Markdown, SessionContext, Spinner, SubagentIdleIcon, WorkdirContext, WorkingMark, useApp } from './common';
import { APPROVAL_ICONS, DecidedRow } from './Interactions';
import { usePlannerOpenCard } from './planner/context';
import { Button } from './ui/button';
import { Chip, chipVariants } from './ui/chip';
import { Collapse, usePresence } from './ui/collapse';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Appear } from './ui/appear';
import { Tip } from './ui/tooltip';

interface Props {
  /** The Task, for the attachment routes. */
  sessionId: string;
  /** Child transcripts retain their own interaction ownership. */
  agentId?: string;
  items: Item[];
  identityItems?: Item[];
  liveItems?: Item[];
  historyItemSeq?: Record<string, number>;
  turnTimings?: TurnTiming[];
  /** The Task's requests; the decided ones join the turns, the pending ones stay cards. */
  interactions: Interaction[];
  subagents: Subagent[];
  /** Subagent transcripts the browser holds (fetched once their panel opened); a running row's step comes from them. */
  agents?: Record<string, AgentTranscript>;
  /** Each subagent's latest step from live frames; stands in for its items until its transcript is open. */
  agentSteps?: Record<string, Item>;
  /** The provider still holds the turn, so pending tools may still report. */
  live: boolean;
  /** A turn is running (not merely waiting for the user): the last item is still streaming. */
  working: boolean;
  /** Provider id used to resolve subagent model names. */
  provider: string;
  /** The Task's directory, for links to its files. */
  workdir: string;
  /** Open a subagent's transcript in the side panel; `opener` gets focus back when it closes. */
  onOpenAgent?: (agentId: string, opener: HTMLElement) => void;
  /** Compact folds a turn's work into its head row (DESIGN.md turn line); detailed draws one activity row per run. */
  density?: Density;
  /** Open the Changes sheet from a turn's "Changed n files" line. */
  onOpenChanges?: () => void;
  /** Keep a compact turn's "Changed n files" line; off where the Task's folder has no Changes view (no Git). */
  changedLine?: boolean;
  /** Name the turn's verb on the foot line between steps; the main pane's floating `WorkingLabel` carries it instead, and its foot line names only the current step (Compact). */
  footVerb?: boolean;
  /** The conversation is being compacted: the foot says so in place of the current step. */
  compacting?: boolean;
}

/** Rows that arrive after mount rise in; rows present at mount appear at once. Stable, so memoised rows hold. */
function useArrivals(ids: string[], historyItemSeq?: Record<string, number>) {
  const [initial] = useState(() => new Set(ids));
  return useCallback((id: string) => (initial.has(id) || historyItemSeq?.[id] !== undefined ? '' : 'animate-rise'), [initial, historyItemSeq]);
}

/**
 * Main transcript. User items are bubbles on the right; everything between two user items
 * is one flat assistant turn: thinking inline, one row per tool call with its approval on
 * it, a question block for each `ask_user` call once it no longer waits, a compact row for
 * each `task` call that spawned a subagent (its output lives in the panel, never here), and
 * the prose. A decided request without a tool row joins the turn at its time.
 */
export function Transcript({ sessionId, agentId, items, identityItems = items, liveItems = items, historyItemSeq, turnTimings = [], interactions, subagents, agents = {}, agentSteps = {}, live, working, provider, workdir, onOpenAgent, density = 'detailed', onOpenChanges, changedLine = true, footVerb = true, compacting = false }: Readonly<Props>) {
  const arrival = useArrivals([...items.map((i) => i.id), ...interactions.map((i) => i.id)], historyItemSeq);
  const byParent = useMemo(() => {
    const map = new Map<string, Subagent>();
    for (const s of subagents) if (s.parent_tool_call_id) map.set(s.parent_tool_call_id, s);
    return map;
  }, [subagents]);
  const { linked, loose, questions } = linkInteractions(items, interactions, agentId);
  const declarations = useMemo(() => newestFileDeclarations(items), [items]);
  const groupItems = useIdentityEntries(identityItems, loose, questions);
  const turnIds = useGroupIdentities(groupItems, 'turn');
  const groupIds = useGroupIdentities(groupItems, false, item => byParent.has(item.id));
  const toolGroupIds = useGroupIdentities(identityItems, true, item => byParent.has(item.id) || !!endedQuestion(item, linked, live), [...loose, ...questions].filter(interaction => interaction.state !== 'pending').map(interaction => interaction.time));
  // A subagent's row in any state: it stands in the answer while it runs and folds into the
  // turn's collapsed activity once it is idle, completed, failed or cancelled.
  const subagentRow = (item: Item) => {
    const agent = byParent.get(item.id);
    return agent && onOpenAgent ? <SubagentRow key={item.id} item={item} subagent={agent} agentItems={agents[agent.id]?.items ?? (agentSteps[agent.id] ? [agentSteps[agent.id]] : undefined)} provider={provider} onOpen={(el) => onOpenAgent(agent.id, el)} /> : null;
  };
  const running = (item: Item) => byParent.get(item.id)?.status === 'running';
  const ctx: RenderContext = { sessionId, live, streamingId: working ? liveItems.at(-1)?.id : undefined, thoughtEnd: thoughtEnds(items), arrival, approvals: linked, groupIds, toolGroupIds, subagentOf: (item) => byParent.get(item.id), foldedSubagentRow: (item) => (running(item) ? null : subagentRow(item)) };
  const compact = density === 'compact';
  const special = (item: Item) => (running(item) ? subagentRow(item) : null);
  // What a call produced for the person stands in the answer while the call folds like any
  // other: a declared file's card in both densities, the images its result returned in Compact.
  const product = (item: Item) => {
    const kind = callProduct(item, declarations);
    return kind === 'card' || kind === 'chart' || (compact && kind) ? <CallProduct key={`${item.agent_id ?? 'main'}:${item.id}`} item={item} sessionId={sessionId} card={kind === 'card'} className={arrival(item.id)} /> : null;
  };
  const own = (item: Item) => running(item) || ['card', 'chart'].includes(callProduct(item, declarations) ?? '');

  const foreground = new Set(foregroundItems(items).map((item) => item.id));
  const out: ReactNode[] = [];
  let group: Entry[] = [];
  let showedWorking = false;
  const firstIndex = identityItems.findIndex(item => item.id === items[0]?.id);
  let userItemId: string | undefined = identityItems.slice(0, Math.max(0, firstIndex)).reverse().find(item => item.kind === 'user' && !item.delivery)?.id;
  // A turn is keyed by the user message before it, so it keeps its rows when its first entry changes.
  let after = userItemId ?? 'start';
  const flush = (last = false, boundary = true) => {
    const timing = timingForTurn(turnTimings, userItemId);
    const showEnd = showTurnEnd(timing, { hasContent: group.length > 0, boundary, last, live });
    if (!group.length) {
      if (showEnd) out.push(<TurnStatus key={`end-${timing!.id}`} timing={timing} />);
      return;
    }
    const groupLive = live && group.some((entry) => entry.item && foreground.has(entry.item.id));
    const gctx = { ...ctx, live: groupLive };
    if (last && working) showedWorking = true;
    // One status row heads the turn and keeps its slot while it runs, so "Took 12s" lands there
    // at the end and streamed content lands below it: nothing on screen moves at either moment.
    if (compact) {
      // Compact: the head row carries the turn's counts and opens its timeline; only promoted
      // entries stand in the answer, and a steer bubble sits in the turn at its place.
      const summary = summarizeTurn(group, { live: groupLive, streamingId: gctx.streamingId, approvals: linked });
      const changed = changedFiles(group);
      const first = (group[0].item ?? group[0].interaction).id;
      const id = (first && turnIds.get(first)) ?? userItemId ?? 'start';
      out.push(
        <div key={`turn-${id}`} className="flex flex-col gap-3">
          {(last && working) || showEnd || summary.count > 0 ? <TurnHead id={id} agentId={agentId} working={last && working} timing={timing} summary={summary} entries={group} ctx={gctx} /> : null}
          {renderCompact(group, gctx, (item) => special(item) ?? product(item), own)}
          {changedLine && changed.length > 0 && <ChangedLine files={changed} onOpen={onOpenChanges} />}
        </div>,
      );
    } else {
      const nodes = renderEntries(group, gctx, special, product);
      out.push(
        <div key={`turn-${after}`} className="flex flex-col gap-3">
          {last && working ? <TurnStatus working /> : showEnd && <TurnStatus timing={timing} />}
          {nodes}
        </div>,
      );
    }
    group = [];
  };
  mergeByTime(items, [...loose, ...questions]).forEach((entry) => {
    if (entry.item?.kind !== 'user' || (compact && entry.item.delivery)) {
      group.push(entry);
      return;
    }
    flush(false, !entry.item.delivery);
    after = entry.item.id;
    if (!entry.item.delivery) userItemId = entry.item.id;
    out.push(<UserBubble key={entry.item.id} item={entry.item} sessionId={sessionId} className={arrival(entry.item.id)} />);
  });
  flush(true);
  // Compact draws no live activity row, so the foot shows the current step itself, unfolded.
  const step = compact && working && !compacting ? currentStep(items, { live, streamingId: ctx.streamingId, approvals: linked }, own) : null;
  const current = step?.item && <LiveStep key={step.item.id} item={step.item} live={live} sessionId={sessionId} approvals={linked.get(step.item.id)} />;
  return (
    <SessionContext.Provider value={sessionId}>
      <WorkdirContext.Provider value={workdir}>
        {out}
        {working && !showedWorking && <TurnStatus working />}
        <WorkingTail working={working && (compacting || compact || (footVerb && !liveAtFoot(items, byParent)))} turnId={userItemId ?? 'start'} step={step} verb={footVerb} current={current} compacting={compacting} />
      </WorkdirContext.Provider>
    </SessionContext.Provider>
  );
}

/**
 * Compact (DESIGN.md turn line): the entries that stand in the answer, in order. Prose,
 * notices and steer bubbles; the promoted work, that is a question that no longer waits, a
 * running subagent's row and what a call produced (the images its result returned, a declared
 * file's card) without the call's row. Every call, failed ones included, is in the turn line
 * and its timeline.
 */
function renderCompact(entries: Entry[], ctx: RenderContext, special: (item: Item) => ReactNode | null, own: (item: Item) => boolean): ReactNode[] {
  const out: ReactNode[] = [];
  for (const entry of entries) {
    // A pending request is the action card under the transcript; it is not drawn twice.
    if (entry.interaction?.state === 'pending' || !promoted(entry, { live: ctx.live, approvals: ctx.approvals }, own)) continue;
    if (entry.interaction) {
      const ix = entry.interaction;
      out.push(<QuestionBlock key={ix.id} id={ix.id} asked={questionOf(undefined, ix, ctx.live)!} className={ctx.arrival(ix.id)} />);
      continue;
    }
    const item = entry.item;
    if (item.kind === 'user') {
      out.push(<UserBubble key={item.id} item={item} sessionId={ctx.sessionId} className={ctx.arrival(item.id)} />);
      continue;
    }
    if (item.kind !== 'tool') {
      out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} className={ctx.arrival(item.id)} />);
      continue;
    }
    const node = special(item);
    if (node) {
      out.push(node);
      continue;
    }
    const asked = askedOn(item, ctx.approvals, ctx.live);
    if (asked && asked.outcome !== 'pending') out.push(<QuestionBlock key={item.id} id={item.id} asked={asked} className={ctx.arrival(item.id)} />);
    else out.push(<ToolRow key={item.id} item={item} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals.get(item.id)} className={ctx.arrival(item.id)} />);
  }
  return out;
}

/**
 * The row that heads a compact turn (DESIGN.md turn line), in the turn status row's slot:
 * "Took 12s" once it ended (while it runs the working label says so), then the turn's counts,
 * "5 thoughts (42s) · 3 commands · 2 files read", updating in place as items append. With
 * anything counted it is a button that opens the turn's whole timeline in place, mounted on
 * the first open only; the counts turn `error` after a failure and `attention`
 * while a call waits for the user. Before the first count (the first step still runs, or the
 * only call waits for the user) the row stays blank in its slot: the live step and the foot's
 * label say what is happening, and the counts land without moving anything.
 */
/** The tip on a turn's duration slot: whether the time shown was recorded. */
const durationTitle = (elapsed: string | null) => (elapsed ? 'Recorded foreground turn duration' : 'Turn duration was not recorded.');

function TurnHead({ id, agentId, working, timing, summary, entries, ctx }: Readonly<{ id: string; agentId?: string; working: boolean; timing?: TurnTiming; summary: TurnSummary; entries: Entry[]; ctx: RenderContext }>) {
  // Item IDs are local to their agent; main-turn identities stay unchanged.
  const scope = agentId ? 'agent-turn' : 'turn';
  const scopedId = agentId ? encodeURIComponent(JSON.stringify([agentId, id])) : id;
  const [open, setOpen] = useDisclosure(`${scope}:${scopedId}`);
  const [opened, setOpened] = useState(open);
  const elapsed = working ? null : completedDuration(timing);
  const head = elapsed ? `Took ${elapsed}` : '';
  const text = [head, ...summary.parts.map((p) => p.text)].filter(Boolean).join(' · ');
  const domId = `${scope}-${scopedId}`;
  const timelineId = `${domId}-timeline`;
  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const headButton = useRef<HTMLButtonElement>(null);
  // The foot of an open timeline folds it too: the turn line comes back into view, so a long timeline
  // never has to be scrolled back up to close, and focus returns to the line.
  const collapse = () => {
    headButton.current?.scrollIntoView({ block: 'nearest' });
    headButton.current?.focus({ preventScroll: true });
    setOpen(false);
  };
  return (
    <div id={domId} className="flex flex-col rounded-sm">
      <div data-history-anchor={`turn-head-${id}`} data-history-items={JSON.stringify(entries.flatMap(entry => entry.item ? [entry.item.id] : []))} className="flex min-h-[34px] items-center gap-2 py-2 text-caption tabular-nums text-muted" title={working || summary.count ? undefined : durationTitle(elapsed)}>
        {summary.parts.length > 0 ? (
          <button
            ref={headButton}
            type="button"
            aria-expanded={open}
            aria-controls={opened ? timelineId : undefined}
            title={text}
            className={cn('-mx-1.5 flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-left transition-colors duration-100 hover:bg-tint-well hover:text-body pointer-coarse:min-h-11', summary.tone === 'attention' && 'text-attention')}
            onClick={toggle}
          >
            <span className="min-w-0 truncate">
              {head}
              {summary.parts.map((p, k) => (
                <span key={k} className={cn(p.tone === 'error' && 'text-error')}>
                  {(head || k > 0) && ' · '}
                  {p.text}
                </span>
              ))}
            </span>
            <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
            <span className="sr-only">, activity of this turn</span>
          </button>
        ) : (
          head && <span className="animate-fade-in">{head}</span>
        )}
      </div>
      {opened && (
        <Collapse open={open} appear>
          <DetailVisibility open={open}><Timeline id={timelineId} entries={entries} ctx={ctx} /></DetailVisibility>
          <button type="button" aria-controls={timelineId} className="mt-1 ml-1.5 flex h-6 w-fit items-center gap-1 rounded-sm px-1.5 text-caption text-muted transition-colors duration-100 hover:bg-tint-well hover:text-body pointer-coarse:min-h-11" onClick={collapse}>
            <ChevronUp aria-hidden="true" className="size-3" />
            Collapse
          </button>
        </Collapse>
      )}
    </div>
  );
}

/**
 * A turn's whole timeline, in order, under its head row: each thought (its text `muted` on
 * its own click), each tool call as its row with its approval, details and images, each
 * question row and each decided request.
 */
function Timeline({ id, entries, ctx }: Readonly<{ id: string; entries: Entry[]; ctx: RenderContext }>) {
  const rows: ReactNode[] = [];
  const row = (key: string, time: string, took: string | null, node: ReactNode) =>
    rows.push(
      <div key={key} className="flex items-start gap-3">
        <StepTime time={time} took={took} />
        <div className="min-w-0 flex-1">{node}</div>
      </div>,
    );
  for (const entry of entries) {
    if (!isWork(entry)) continue;
    // A settled question stands in the answer as its card; the timeline only marks its place.
    if (entry.interaction) {
      row(entry.interaction.id, entry.interaction.time, null, <DecidedRow interaction={entry.interaction} />);
      continue;
    }
    const item = entry.item;
    const took = item.id === ctx.streamingId ? null : itemTook(item);
    if (item.kind === 'reasoning') {
      if (item.text?.trim() || item.compact?.has_reasoning) row(item.id, item.time, took, <Thinking item={item} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} />);
      continue;
    }
    const asked = askedOn(item, ctx.approvals, ctx.live);
    const question = asked && asked.outcome !== 'pending' ? ctx.approvals.get(item.id)?.find((ix) => ix.kind === 'question') : undefined;
    row(item.id, item.time, took, ctx.foldedSubagentRow?.(item) ?? (question ? <DecidedRow interaction={question} /> : <ToolRow item={item} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals.get(item.id)} />));
  }
  return (
    <div id={id} className="relative mb-2 ml-1.5 flex flex-col gap-1 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-px before:fade-rule-y before:content-['']">
      {rows}
    </div>
  );
}

/** A timeline step's start (to the second, 24-hour) and its recorded duration, blank while it runs or when unrecorded. */
function StepTime({ time, took }: Readonly<{ time: string; took: string | null }>) {
  const at = new Date(time);
  return (
    <span className="flex h-6 shrink-0 items-center gap-2 text-caption tabular-nums text-faint pointer-coarse:h-11" title={at.toLocaleString()}>
      <span>{at.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' })}</span>
      <span className="w-10 text-right text-muted">{took}</span>
    </span>
  );
}

/** The one line a compact turn keeps for its edits: "Changed 2 files", with the paths in its tooltip, and the way to the Changes sheet. */
function ChangedLine({ files, onOpen }: Readonly<{ files: string[]; onOpen?: () => void }>) {
  return (
    <div className="flex h-6 items-center gap-2 text-caption text-muted" title={files.join('\n')}>
      <FileDiff aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
      <span>
        Changed {files.length} {files.length === 1 ? 'file' : 'files'}
      </span>
      {onOpen && (
        <button type="button" className="rounded-xs text-accent hover:underline" onClick={onOpen}>
          View changes
        </button>
      )}
    </div>
  );
}

/**
 * Whether the last row is already live: a thought streaming or a call running folds into an
 * activity row that carries the working mark and says so ("Thinking…", "Running: …"). A subagent's
 * call is its own row, not an activity row.
 */
function liveAtFoot(items: Item[], byParent: Map<string, Subagent>): boolean {
  const last = items.at(-1);
  if (!last) return false;
  if (last.kind === 'reasoning') return true;
  return last.kind === 'tool' && (last.tool?.status === 'pending' || last.tool?.status === 'running') && !byParent.has(last.id);
}

/**
 * The live line at the foot of a running turn, where new output lands: the working mark and a
 * shimmering gerund ("Untangling…") while prose streams or the agent is between steps. The verb
 * is picked from the id of the user message that began the turn, so it holds for the whole turn
 * and comes back the same after a reload. It steps aside while the last row is already live,
 * grows in when the turn starts and folds away when it ends or waits for the user; the turn's
 * status row already announces the state, so this one is not read out again.
 */
function WorkingTail({ working, turnId, step, verb = true, current, compacting = false }: Readonly<{ /** The conversation is being compacted: say so in place of the step or verb. */ compacting?: boolean; working: boolean; turnId: string; /** Compact: the current step ("Running: …", "Thinking…") in place of the verb. */ step?: Step | null; /** Between steps, the verb; without it the line stays blank, so it does not fold and grow back at every step. */ verb?: boolean; /** Compact: the step in progress, unfolded (`LiveStep`), in place of its one-line label. */ current?: ReactNode }>) {
  const { mounted, onClosed } = usePresence(working);
  // Once a turn showed a step unfolded, the foot keeps that room until the turn ends: a step
  // folding into the turn line never pulls the transcript up, and the next one lands in the same room.
  const [roomFor, setRoomFor] = useState<string | null>(null);
  if (current && roomFor !== turnId) setRoomFor(turnId);
  if (!mounted) return null;
  let text = '';
  if (compacting) text = 'Compacting the conversation…';
  else if (step) text = step.label;
  else if (verb) text = `${turnVerb(turnId)}…`;
  return (
    <Collapse open={working} appear onClosed={onClosed} className="-mt-6" inner="pt-3">
      {working && verb && <output className="sr-only">Busy</output>}
      <div className={cn(roomFor === turnId && 'min-h-[108px] pointer-coarse:min-h-[128px]')}>
        {current || (
          <div aria-hidden="true" className="flex h-6 items-center gap-2 text-caption text-muted">
            {text && <WorkingMark />}
            <span className={cn('min-w-0 truncate', (!step || step.shimmer) && 'animate-shimmer motion-reduce:animate-none', step?.tone === 'attention' && 'text-attention')} title={step?.label}>
              {text}
            </span>
          </div>
        )}
      </div>
    </Collapse>
  );
}

/** The most of a running call's output the live step renders: its tail box shows only the last lines anyway. */
const LIVE_OUTPUT_TAIL = 4000;

/**
 * Compact (DESIGN.md live step): the step in progress at the turn's foot, unfolded. A thought
 * streaming is "Thinking…" over its text as it arrives; a call running is its tool row (mark,
 * name, argument) over the tail of its output. The text sits in a box at most 80px tall that
 * shows its newest lines, the older ones clipped above, so it never grows into a block and
 * nothing scrolls. It rises in; once the step ends it is gone from here and counted on the turn line.
 */
function LiveStep({ item, live, sessionId, approvals }: Readonly<{ item: Item; live: boolean; sessionId: string; approvals?: Interaction[] }>) {
  const { item: full, attach } = useItemBody(item, true);
  const thought = item.kind === 'reasoning';
  const text = (thought ? full?.text : full?.tool?.output) ?? '';
  return (
    <div ref={attach} className="flex animate-rise flex-col gap-1">
      {thought ? (
        <div aria-hidden="true" className="flex h-6 items-center gap-2 text-caption text-muted">
          <WorkingMark />
          <span className="animate-shimmer motion-reduce:animate-none">Thinking…</span>
        </div>
      ) : (
        <ToolRow item={item} live={live} sessionId={sessionId} approvals={approvals} />
      )}
      {text.trim() && (
        <div className={cn('flex max-h-20 flex-col justify-end overflow-hidden', thought ? 'relative pl-3 before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:fade-rule-y before:content-[\'\']' : 'ml-6 rounded-sm bg-code-bg px-2 py-1')}>
          {thought ? (
            <Markdown text={text} className="md-quiet shrink-0 text-ui text-muted" streaming />
          ) : (
            <pre translate="no" className="shrink-0 font-mono text-code-sm whitespace-pre-wrap break-words text-muted">{text.slice(-LIVE_OUTPUT_TAIL)}</pre>
          )}
        </div>
      )}
    </div>
  );
}

/**
 * The main pane's working label (DESIGN.md working label), floating over the composer: while a
 * turn runs, the working mark, the turn's verb and how long the turn has been busy; what the
 * agent is doing stays in the transcript. It sits outside the transcript, so it stays in view at
 * any scroll position and whichever history page is loaded; `items` is the live tail. Its width
 * holds while the time counts up, so it never shifts. While the conversation compacts it says so in place of the verb.
 */
export function WorkingLabel({ working, compacting = false, since, items, identityItems = items, turnTimings = [] }: Readonly<{ working: boolean; compacting?: boolean; since?: string; items: Item[]; identityItems?: Item[]; turnTimings?: TurnTiming[] }>) {
  const [now, setNow] = useState(() => Date.now());
  const timing = turnTimings.at(-1);
  const ticking = working && (!!since || !timing?.paused_at);
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
  const lastUser = (list: Item[]) => [...list].reverse().find((item) => item.kind === 'user' && !item.delivery)?.id;
  const turnId = lastUser(items) ?? lastUser(identityItems) ?? 'start';
  // `since` times work that outlived the turn (subagents still running); otherwise the running turn's clock.
  const elapsed = working ? (since ? elapsedSince(since, now) : timing?.state === 'working' ? turnElapsed(timing, now) : null) : null;
  return (
    <Appear show={working}>
      {working && <output className="sr-only">{compacting ? 'Compacting the conversation' : 'Busy'}</output>}
      <span aria-hidden="true" className="flex h-7 items-center gap-2 rounded-sm bg-raised px-2.5 text-caption text-muted shadow-float">
        <WorkingMark />
        <span className="animate-shimmer whitespace-nowrap motion-reduce:animate-none">{compacting ? 'Compacting the conversation' : turnVerb(turnId)}…</span>
        {/* Under an hour the time is at most three characters wide: the slot holds them all. */}
        {elapsed && <span className="min-w-[3ch] text-right tabular-nums text-faint">{elapsed}</span>}
      </span>
    </Appear>
  );
}

interface RenderContext {
  sessionId?: string;
  live: boolean;
  /** The item still receiving deltas, if any. */
  streamingId: string | undefined;
  /** For each reasoning item with a recorded end: when it ended. */
  thoughtEnd: Map<string, string>;
  arrival: (id: string) => string;
  /** Requests by the tool item they sit on, oldest first. */
  approvals: Map<string, Interaction[]>;
  groupIds?: Map<string, string>;
  toolGroupIds?: Map<string, string>;
  /** The subagent a `task` call spawned, and the row of one no longer running, which it keeps inside its turn's activity. */
  subagentOf?: (item: Item) => Subagent | undefined;
  foldedSubagentRow?: (item: Item) => ReactNode | null;
}


/** Each reasoning item's recorded end. */
function thoughtEnds(items: Item[]): Map<string, string> {
  const m = new Map<string, string>();
  for (const item of items) if (item.kind === 'reasoning' && item.ended_at) m.set(item.id, item.ended_at);
  return m;
}

/**
 * Entries in order. Each contiguous run of work between two messages (thinking, tool calls,
 * decided requests, questions that no longer wait) folds into one activity row; prose and
 * the rows `special` takes over (subagents, whose controls must stay in view) stand on
 * their own between them, and what `product` draws for a call in a run (a declared file's
 * card) stands right after that run.
 */
function renderEntries(entries: Entry[], ctx: RenderContext, special?: (item: Item) => ReactNode | null, product?: (item: Item) => ReactNode | null): ReactNode[] {
  // A pending request is the action card under the transcript; it is not drawn twice.
  const drawn = entries.filter((entry) => entry.interaction?.state !== 'pending');
  // Tool calls that are a block of their own: a subagent row.
  const own = new Map<string, ReactNode>();
  for (const { item } of drawn) {
    if (item?.kind !== 'tool') continue;
    const node = special?.(item);
    if (node) own.set(item.id, node);
  }
  const out: ReactNode[] = [];
  let pos = 0;
  for (const segment of segmentActivity(drawn, (entry) => isWork(entry) && !(entry.item && own.has(entry.item.id)))) {
    pos += segment.entries.length;
    if (!segment.work) {
      out.push(...renderRows(segment.entries, ctx, own));
      continue;
    }
    // The run ended when the next item began; the same clock as a thought's.
    let endedAt: string | undefined;
    for (let k = pos; k < drawn.length && !endedAt; k++) endedAt = drawn[k].item?.time;
    out.push(<ActivityRun key={ctx.groupIds?.get(segment.key) ?? segment.key} identity={ctx.groupIds?.get(segment.key) ?? segment.key} entries={segment.entries} ctx={ctx} endedAt={endedAt} className={ctx.arrival(segment.key)} />);
    for (const { item } of segment.entries) {
      const node = item && product?.(item);
      if (node) out.push(node);
    }
  }
  return out;
}

/**
 * The rows themselves: consecutive tool calls form one run of rows, `own` holds the blocks
 * that take a tool call over, and a question that no longer waits is its question block. A
 * question still waiting is the action card; its tool row stays until it is answered.
 */
function renderRows(entries: Entry[], ctx: RenderContext, own: Map<string, ReactNode>): ReactNode[] {
  const out: ReactNode[] = [];
  let run: Item[] = [];
  // Runs are keyed by their order: a call taken out of a run (a subagent row) must not remount the rest.
  let runs = 0;
  const flush = () => {
    if (run.length) out.push(<ToolRun key={ctx.toolGroupIds?.get(run[0].id) ?? `run-${runs++}`} identity={ctx.toolGroupIds?.get(run[0].id) ?? run[0].id} items={run} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals} arrival={ctx.arrival} />);
    run = [];
  };
  for (const entry of entries) {
    if (entry.interaction) {
      flush();
      const ix = entry.interaction;
      const asked = questionOf(undefined, ix, ctx.live);
      out.push(asked ? <QuestionBlock key={ix.id} id={ix.id} asked={asked} className={ctx.arrival(ix.id)} /> : <DecidedRow key={ix.id} interaction={ix} className={ctx.arrival(ix.id)} />);
      continue;
    }
    const item = entry.item;
    if (item.kind === 'tool') {
      const asked = own.has(item.id) ? null : askedOn(item, ctx.approvals, ctx.live);
      const node = own.get(item.id) ?? (asked && asked.outcome !== 'pending' ? <QuestionBlock key={item.id} id={item.id} asked={asked} className={ctx.arrival(item.id)} /> : null);
      if (!node) {
        run.push(item);
        continue;
      }
      flush();
      out.push(node);
      continue;
    }
    // A reasoning item the provider closed without any text has nothing to show.
    if (item.kind === 'reasoning' && !item.text?.trim() && !item.compact?.has_reasoning) continue;
    flush();
    out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} className={ctx.arrival(item.id)} />);
  }
  flush();
  return out;
}

/** The streaming item's id when it is in `entries`; the row only cares about its own. */
const streamingIn = (entries: Entry[], id: string | undefined) => (id && entries.some((e) => e.item?.id === id) ? id : undefined);

/**
 * One run of work as a 24px `caption` row (DESIGN.md activity row): a chevron, or the
 * working mark while a call runs or thinking streams, and the summary, which updates in
 * place as the run grows and truncates rather than wraps, so streaming never moves the
 * page. Failures turn it `error`, a call waiting for permission `attention`. It opens
 * through the shared height collapse onto the rows themselves, indented, each with its own
 * disclosure. Memoised on its entries: text streaming into another item leaves it alone.
 */
const ActivityRun = memo(function ActivityRun({ identity, entries, ctx, endedAt, className }: { identity: string; entries: Entry[]; ctx: RenderContext; endedAt?: string; className?: string }) {
  const [open, setOpen] = useDisclosure(`activity:${entries[0]?.item?.agent_id ?? ''}:${identity}`);
  // The rows are mounted on the first open only: a closed run costs one button.
  const [opened, setOpened] = useState(open);
  const { label, tone, active } = summarizeActivity(entries, { live: ctx.live, streamingId: ctx.streamingId, approvals: ctx.approvals, endedAt });
  if (!label) return null;
  return (
    <div data-activity="" className={cn('flex flex-col', className)}>
      <button data-history-anchor={`activity-${identity}`} data-history-items={JSON.stringify(entries.flatMap(entry => entry.item ? [entry.item.id] : []))} type="button" aria-expanded={open} title={label} className={cn('flex h-6 w-fit max-w-full items-center gap-2 rounded-full bg-tint-well pr-3 pl-2 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11', tone === 'error' && 'text-error', tone === 'attention' && 'text-attention')} onClick={() => { setOpened(true); setOpen((o) => !o); }}>
        <span className="flex size-3.5 shrink-0 items-center justify-center">
          {active ? <WorkingMark /> : <ChevronRight aria-hidden="true" className={cn('size-3 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />}
        </span>
        <span className="min-w-0 truncate tabular-nums">{label}</span>
      </button>
      {opened && (
        <Collapse open={open} appear>
          <DetailVisibility open={open}><div className="mt-1 flex flex-col gap-1 pl-5.5">{renderRows(entries, ctx, subagentRows(entries, ctx))}</div></DetailVisibility>
        </Collapse>
      )}
    </div>
  );
}, (a, b) => {
  const same = (x: Entry, y: Entry) => (x.item ?? x.interaction) === (y.item ?? y.interaction);
  // `thoughtEnd` is rebuilt every render; what it says about these entries changes only with them or with `endedAt`.
  return a.endedAt === b.endedAt && a.className === b.className && a.ctx.live === b.ctx.live && a.ctx.sessionId === b.ctx.sessionId && a.ctx.approvals === b.ctx.approvals && a.ctx.arrival === b.ctx.arrival
    && streamingIn(a.entries, a.ctx.streamingId) === streamingIn(b.entries, b.ctx.streamingId) && a.entries.length === b.entries.length && a.entries.every((e, i) => same(e, b.entries[i]))
    && a.entries.every((e) => !e.item || a.ctx.subagentOf?.(e.item) === b.ctx.subagentOf?.(e.item));
});

/** The rows of the folded subagents among `entries`, which take their `task` calls over. */
function subagentRows(entries: Entry[], ctx: RenderContext): Map<string, ReactNode> {
  const rows = new Map<string, ReactNode>();
  for (const { item } of entries) {
    const row = item?.kind === 'tool' ? ctx.foldedSubagentRow?.(item) : null;
    if (row) rows.set(item!.id, row);
  }
  return rows;
}

/**
 * The row that heads a turn (DESIGN.md turn status), in one slot for both states: empty while
 * the turn runs (the working label says so), then "Took 12s" once it ended. Without a recorded
 * duration the row keeps its slot, with no label and no rule.
 */
function TurnStatus({ working = false, timing }: Readonly<{ working?: boolean; timing?: TurnTiming }>) {
  const elapsed = working ? null : completedDuration(timing);
  return (
    <div className="flex min-h-[34px] items-center gap-2 py-2 text-caption tabular-nums text-muted" title={working ? undefined : durationTitle(elapsed)}>
      {elapsed && <span className="animate-fade-in">Took {elapsed}</span>}
    </div>
  );
}

type CopyableMenuEvents = Pick<ComponentProps<'div'>, 'onContextMenu' | 'onTouchStart' | 'onTouchMove' | 'onTouchEnd' | 'onTouchCancel'>;

/** Attach Base UI's handlers to the stable message shell without replaying DOM events. */
function CopyableMenuTarget({ handlersRef, ...props }: ComponentProps<'div'> & { handlersRef: RefObject<CopyableMenuEvents | null> }) {
  useLayoutEffect(() => {
    handlersRef.current = props;
    return () => { handlersRef.current = null; };
  }, [handlersRef, props]);
  return <div {...props} />;
}

/** A hover copy button plus a right-click menu around any block of provider or user text. */
function Copyable({ text, read, label, className, side = 'right', children, extra = [] }: Readonly<{ text: string; /** Reads the text to copy when `text` is only part of it. */ read?: () => Promise<string>; label: string; className?: string; /** Where the button sits: over the block's top-right corner, or outside it to the left (the user bubble, so it never covers the text). */ side?: 'right' | 'left'; children: ReactNode; extra?: ActionItem[] }>) {
  const [copied, copy] = useCopied();
  const [menuReady, setMenuReady] = useState(false);
  const menuHandlers = useRef<CopyableMenuEvents | null>(null);
  const run = () => { if (read) void read().then(copy, () => {}); else copy(text); };
  const items: ActionItem[] = [{ key: 'copy', label, icon: <Copy />, onSelect: run }, ...extra];

  function menuEvents(event: SyntheticEvent<HTMLDivElement>) {
    // Portalled menu interactions do not originate in the message.
    if (!event.currentTarget.contains(event.target as Node)) return null;
    // Keep message/button parents stable. Base UI receives the original event once,
    // including its native target, coordinates, modifiers and touch cancellation.
    if (!menuReady) flushSync(() => setMenuReady(true));
    return menuHandlers.current;
  }

  return (
    <div
      className={cn('group/copy relative', className)}
      style={{ WebkitTouchCallout: 'none' }}
      onContextMenu={event => menuEvents(event)?.onContextMenu?.(event)}
      onTouchStart={event => menuEvents(event)?.onTouchStart?.(event)}
      onTouchMove={menuReady ? event => menuEvents(event)?.onTouchMove?.(event) : undefined}
      onTouchEnd={menuReady ? event => menuEvents(event)?.onTouchEnd?.(event) : undefined}
      onTouchCancel={menuReady ? event => menuEvents(event)?.onTouchCancel?.(event) : undefined}
    >
      {children}
      <Tip label={copied ? 'Copied' : label}>
        <Button
          size="icon-sm"
          variant="ghost"
          aria-label={copied ? 'Copied' : label}
          className={cn('absolute top-0 text-muted opacity-0 transition-opacity duration-100 group-hover/copy:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100', side === 'right' ? '-right-1' : '-left-7', copied && 'opacity-100 text-success')}
          onClick={run}
        >
          {copied ? <Check /> : <Copy />}
        </Button>
      </Tip>
      {menuReady && (
        <ContextMenu.Root>
          <ContextMenu.Trigger render={<CopyableMenuTarget handlersRef={menuHandlers} />} className="contents" />
          <ContextMenu.Content>
            <ContextMenu.Actions items={items} />
          </ContextMenu.Content>
        </ContextMenu.Root>
      )}
    </div>
  );
}

/** The user's turn: a bubble with the text as typed, then its uploads. The item carries no list of its `@path` references, so those stay plain text. */
const UserBubble = memo(function UserBubble({ item, sessionId, className }: { item: Item; sessionId?: string; className?: string }) {
  return item.clipped ? <ClippedMessage item={item} sessionId={sessionId} className={className} /> : userBubble({ item, text: item.text ?? '', sessionId, className });
});

/** A message row's parts: `text` is the item's, or a clipped item's shown part, with `whole` for its note and copy. Plain functions, so a row costs no extra component. */
interface MessageParts { item: Item; text: string; sessionId?: string; streaming?: boolean; className?: string; whole?: WholeText }

function userBubble({ item, text, sessionId, className, whole }: MessageParts) {
  const attachments = item.attachments ?? [];
  const chips = attachments.length > 0 && !!sessionId;
  const accepted = item.steer_status === 'accepted';
  const width = 'max-w-[min(88%,720px)] max-sm:max-w-[88%]';
  const bubble = (
    // A steer the provider accepted but has not recorded yet reads bold italic; delivered, it settles to normal text.
    <div className={cn('flex flex-col gap-2 rounded-lg bg-bubble px-3.5 py-2.5 text-chat text-ink shadow-raised', accepted && 'font-semibold italic')} title={accepted ? 'Accepted: sent to the agent, delivery not confirmed yet' : undefined}>
      <span className="sr-only">{accepted ? 'You (accepted, not delivered yet): ' : 'You: '}</span>
      {item.delivery === 'autopilot' && <span className="block text-caption text-accent">Autopilot</span>}
      {item.steer_status === 'not_delivered' && <span className="block text-caption text-error">Not delivered</span>}
      {text && (whole?.status === 'whole' ? plainText(text) : <Markdown text={text} breaks />)}
      {whole && <WholeNote whole={whole} />}
      {chips && <ItemAttachments sessionId={sessionId} attachments={attachments} />}
      {/* Nothing to show (a message of only file references, which the item does not list): say so rather than draw an empty bubble. */}
      {!text && !whole && !chips && <span className="text-caption text-muted">No text</span>}
    </div>
  );
  return (
    <div data-history-anchor={item.id} className={cn('flex justify-end', className)}>
      {/* A message without text has nothing to copy: its chips alone. */}
      {text ? (
        <Copyable text={text} read={whole?.status === 'whole' ? undefined : whole?.read} label="Copy message" side="left" className={width}>
          {bubble}
        </Copyable>
      ) : (
        <div className={width}>{bubble}</div>
      )}
    </div>
  );
}

function assistantMessage({ item, text, streaming = false, className, whole }: MessageParts) {
  return (
    <Copyable text={text} read={whole?.status === 'whole' ? undefined : whole?.read} label="Copy message" className={cn('pr-6', className)}>
      <div data-history-anchor={item.id} className="text-chat text-body">
        {whole?.status === 'whole' ? plainText(text) : <Markdown text={text} streaming={streaming} />}
        {whole && <WholeNote whole={whole} />}
      </div>
    </Copyable>
  );
}

function noticeRow({ text, className, whole }: MessageParts) {
  return (
    <div className={cn('flex min-h-8 items-center gap-2 text-caption text-muted', whole && 'flex-wrap', className)}>
      {whole?.status === 'whole' ? plainText(text) : <Markdown text={text} className="[&_p]:m-0" />}
      {whole && <WholeNote whole={whole} />}
    </div>
  );
}

/** A clipped message's whole text, plain: as Markdown, megabytes of it would hold the page for seconds. */
const plainText = (text: string) => <p className="break-words whitespace-pre-wrap">{text}</p>;

/** A message the service holds shortened (`clipped`, a text over its memory bound): the part held, then the way to the whole text, read only on request. */
function ClippedMessage({ item, sessionId, streaming, className }: Readonly<{ item: Item; sessionId?: string; streaming?: boolean; className?: string }>) {
  const whole = useWholeText(item);
  const parts = { item, text: whole.text, sessionId, streaming, className, whole };
  if (item.kind === 'user') return userBubble(parts);
  if (item.kind === 'notice') return noticeRow(parts);
  return assistantMessage(parts);
}

function WholeNote({ whole }: Readonly<{ whole: WholeText }>) {
  if (whole.status === 'whole') return null;
  if (whole.status === 'loading') return <p role="status" className="text-caption text-muted">Loading the full message…</p>;
  if (whole.status === 'error') return <p role="alert" className="text-caption text-error">{whole.error} <button type="button" onClick={whole.show} className="rounded-xs underline">Retry</button></p>;
  return (
    <p className="text-caption text-muted">
      Shortened here to save memory.{' '}
      <button type="button" onClick={whole.show} className="rounded-xs text-accent hover:underline">Show full message</button>
    </p>
  );
}

interface ToolRunProps {
  identity: string;
  items: Item[];
  live: boolean;
  sessionId?: string;
  approvals: Map<string, Interaction[]>;
  arrival: (id: string) => string;
}

/** Consecutive tools share a compact disclosure; prose and questions stay in time order. Memoised on its calls, which a streamed delta elsewhere leaves alone. */
const ToolRun = memo(function ToolRun({ identity, items, live, sessionId, approvals, arrival }: ToolRunProps) {
  const [open, setOpen] = useDisclosure(`tools:${items[0]?.agent_id ?? ''}:${identity}`);
  const [opened, setOpened] = useState(open);
  const failed = items.some((item) => item.tool?.status === 'failed');
  const active = live && items.some((item) => isActive(item.tool?.status));
  return (
    <div data-tool-run="">
      <button data-history-anchor={`tools-${identity}`} data-history-items={JSON.stringify(items.map(item => item.id))} type="button" aria-expanded={open} className={cn('flex min-h-7 w-fit max-w-full items-center gap-2 rounded-full bg-tint-well pr-3 pl-2 text-left text-ui text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11', failed && 'text-error')} onClick={() => { setOpened(true); setOpen((o) => !o); }}>
        {active ? <WorkingMark /> : <Terminal aria-hidden="true" className="size-4 shrink-0" />}
        <span>{summarizeTools(items, live)}</span>
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      {opened && (
        <Collapse open={open} appear>
          <div className="relative mt-1 flex flex-col gap-1 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-px before:fade-rule-y before:content-['']">
            <DetailVisibility open={open}>{items.map((item) => <ToolRow key={item.id} item={item} live={live} sessionId={sessionId} approvals={approvals.get(item.id)} className={arrival(item.id)} />)}</DetailVisibility>
          </div>
        </Collapse>
      )}
    </div>
  );
}, (a, b) => a.live === b.live && a.sessionId === b.sessionId && a.approvals === b.approvals && a.arrival === b.arrival && a.items.length === b.items.length && a.items.every((item, i) => item === b.items[i]));

const isActive = (s?: ToolStatus) => s === 'pending' || s === 'running';

/** A call's state as a glyph: the spinner while it is open, a check, a cross, or a dash for a call that never reported. */
export function ToolMark({ tone }: Readonly<{ tone: string }>) {
  if (tone === 'running' || tone === 'pending') return <Spinner />;
  if (tone === 'completed' || tone === 'decided') return <Check aria-hidden="true" className="size-3.5 text-success" strokeWidth={2.5} />;
  if (tone === 'failed') return <X aria-hidden="true" className="size-3.5 text-error" strokeWidth={2.5} />;
  return <Minus aria-hidden="true" className="size-3.5 text-faint" strokeWidth={2.5} />;
}

/** A tool call's details: its text, then the input and output as code blocks; "No details yet." with none of them. */
export function ToolDetails({ item, className }: Readonly<{ item: Item; className?: string }>) {
  const t = item.tool;
  return (
    <div className={cn('my-1 flex flex-col gap-1 text-ui', className)}>
      {item.text && <Markdown text={item.text} />}
      {t?.input && <CodeBlock language="input">{t.input}</CodeBlock>}
      {t?.output && <CodeBlock language="output">{t.output}</CodeBlock>}
      {!item.text && !t?.input && !t?.output && <p className="text-caption text-muted">No details yet.</p>}
    </div>
  );
}

/**
 * The decided requests that sit on a tool row, as one mark: a shield and the latest
 * request's word ("auto" for yolo, "allowed" or "denied" for a person), never a line of
 * its own. The tooltip and the accessible name carry every request's full resolution.
 */
/** The state as a word for assistive tech: "no result" for a call the turn ended on, "done" for a completed one, else the status. */
function statusWord(status: ToolStatus, ended: boolean): string {
  if (ended) return 'no result';
  return status === 'completed' ? 'done' : status;
}

function ApprovalMark({ interactions }: Readonly<{ interactions: Interaction[] }>) {
  const marks = interactions.map(approvalMark).reverse();
  const [latest, ...earlier] = marks;
  const Icon = APPROVAL_ICONS[latest.tone];
  const label = earlier.length ? (
    <>
      {latest.full}
      {earlier.map((m, i) => (
        <span key={i} className="block text-on-primary/70">
          earlier: {m.full}
        </span>
      ))}
    </>
  ) : (
    latest.full
  );
  return (
    <Tip label={label}>
      <Chip className="ml-auto gap-1 px-1 font-sans transition-colors duration-100 group-hover/tool:text-body">
        <Icon aria-hidden="true" className="size-3 text-faint" strokeWidth={2} />
        {latest.word}
        {earlier.length > 0 && <span className="tabular-nums">+{earlier.length}</span>}
        <span className="sr-only">: {marks.map((m) => m.full).join('; earlier: ')}</span>
      </Chip>
    </Tip>
  );
}

/**
 * One tool call: mark, tool name (weight 500), its main argument in `code-sm` on one line,
 * and its approval when a request named it; expands to the full input and output. The
 * images its result returned sit under the row, visible without expanding it.
 */
export const ToolRow = memo(function ToolRow({ item, live, sessionId, approvals, className }: { item: Item; live: boolean; sessionId?: string; approvals?: Interaction[]; className?: string }) {
  const [open, setOpen] = useDisclosure(`tool:${item.agent_id ?? ''}:${item.id}`);
  // The details (code blocks) are mounted on the first open only.
  const [opened, setOpened] = useState(open);
  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const [, copy] = useCopied();
  const { item: fullItem, body, attach, retry } = useItemBody(item, open);
  const { copyBody, copyError } = useBodyCopy(item, copy);
  const t = item.tool;
  const status = t?.status ?? 'pending';
  // Display only: a tool still pending/running after the turn ended never reported a result.
  const ended = !live && isActive(status);
  const tone = ended ? 'ended' : status;
  const { name, arg } = toolLabel(t);
  const label = arg ? `${name} ${arg}` : name;
  const word = statusWord(status, ended);
  const images = item.images ?? [];
  const decided = approvals?.filter((ix) => ix.state !== 'pending') ?? [];
  const items: ActionItem[] = [
    { key: 'toggle', label: open ? 'Collapse' : 'Expand', icon: <ChevronRight />, onSelect: toggle },
    { key: 'cmd', label: 'Copy command', icon: <Copy />, disabled: !t?.input && !t?.has_input, onSelect: () => copyBody('input'), separator: true },
    { key: 'out', label: 'Copy output', icon: <Copy />, disabled: !t?.output && !t?.has_output, onSelect: () => copyBody('output') },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/tool relative', className)} />}>
        <div ref={attach} id={`item-${item.id}`} className={cn('rounded-sm', tone === 'failed' && 'text-error')}>
          <div className={cn('flex items-center gap-1', t?.board_card && 'pr-8 pointer-coarse:pr-11')}>
            <button
              type="button"
              aria-expanded={open}
              className={cn('flex h-6 min-w-0 flex-1 items-center gap-2 rounded-full pl-1.5 text-left font-mono text-code-sm text-muted transition-colors hover:bg-tint-well pointer-coarse:min-h-11', t?.board_card ? 'pr-1.5' : 'pr-8 pointer-coarse:pr-11', tone === 'running' && 'text-body', tone === 'failed' && 'text-error')}
              title={ended ? 'The turn ended before this tool reported a result' : undefined}
              onClick={toggle}
            >
              <span className="flex size-4 shrink-0 items-center justify-center">
                <ToolMark tone={tone} />
              </span>
              <span className={cn('shrink-0 font-medium', tone !== 'failed' && 'text-body')}>{name}</span>
              {arg && <span className="min-w-0 truncate" title={arg}>{arg}</span>}
              <span className="sr-only">, {word}</span>
              {decided.length > 0 && <ApprovalMark interactions={decided} />}
            </button>
            {t?.board_card && <BoardCardChip card={t.board_card} />}
          </div>
          {opened && (
            <Collapse open={open} appear>
              <div className="ml-6"><BodyNotice body={body} retry={retry} />{fullItem && <ToolDetails item={fullItem} />}</div>
            </Collapse>
          )}
        </div>
        {copyError && <p role="alert" className="text-caption text-error">{copyError}</p>}
        {(images.length > 0 || item.images_note) && <ToolImages item={item} sessionId={sessionId} className="mt-1 mb-1.5 ml-7" />}
        <Menu.Root modal={false}>
          <Menu.Trigger render={<Button size="icon-sm" className="absolute top-0 right-0 text-muted opacity-0 transition-opacity group-hover/tool:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" aria-label={`Actions for ${label}`} />}>
            <Ellipsis />
          </Menu.Trigger>
          <Menu.Content align="end" side="bottom">
            <Menu.Actions items={items} />
          </Menu.Content>
        </Menu.Root>
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
});

/**
 * The planner card a planner tool call was about, as "#12 Title" beside its row. It opens the
 * card in the planner, and is plain text while the planner is off.
 */
function BoardCardChip({ card }: Readonly<{ card: ToolBoardCard }>) {
  const openCard = usePlannerOpenCard();
  const name = `#${card.seq} ${card.title}`;
  const label = (
    <>
      <span className="tabular-nums">#{card.seq}</span>{' '}
      <span className="truncate">{card.title}</span>
    </>
  );
  if (!openCard) {
    return <Chip fill="well" className="max-w-48 font-sans" title={name}>{label}</Chip>;
  }
  return (
    <button type="button" className={cn(chipVariants({ fill: 'well' }), 'max-w-48 font-sans hover:text-body')} title={`Open ${name} in the planner`} onClick={() => openCard(card.id)}>
      {label}
    </button>
  );
}

/** The images a tool's result returned, as thumbnails that open the viewer, and its note on any left out. */
function ToolImages({ item, sessionId, className }: Readonly<{ item: Item; sessionId?: string; className?: string }>) {
  return (
    <div className={cn('flex flex-col gap-1', className)}>
      {sessionId && <ImageThumbs sessionId={sessionId} images={item.images ?? []} />}
      {item.images_note && <p className="text-caption text-muted">{item.images_note}</p>}
    </div>
  );
}

/**
 * What a folded call produced for the person (DESIGN.md promotion), standing in the answer
 * without the call's row, which is in the turn's activity: a declared file's card, or the
 * images its result returned. Memoised on the call, which a streamed delta elsewhere leaves alone.
 */
const CallProduct = memo(function CallProduct({ item, sessionId, card, className }: { item: Item; sessionId?: string; card: boolean; className?: string }) {
  return (
    <div data-history-anchor={item.id} className={className}>
      {isChartCall(item) && sessionId ? <ChartCard sessionId={sessionId} callId={item.id} /> : card && item.tool?.declaration ? <DeclaredFileCard declaration={item.tool.declaration} /> : <ToolImages item={item} sessionId={sessionId} />}
    </div>
  );
});

/**
 * A question the agent asked, from its interaction (any provider) or Copilot's `ask_user`
 * call (input and output, so it reads the same after a reload and a restart). While it
 * waits (the action card below the transcript takes the answer) it shows the text and
 * choices in full. Once settled it is one compact card of at most two lines, the question
 * and what it got ("You chose", "You wrote", "Declined", "Not answered"), that opens onto
 * the full question, every choice with the chosen ones marked and the whole answer.
 */
// `asked` is rebuilt on every transcript render; equal content means nothing to redraw.
export const QuestionBlock = memo(function QuestionBlock({ id, asked, className }: { id: string; asked: AskedQuestion; className?: string }) {
  const [, copy] = useCopied();
  const text = asked.questions.map((q) => q.text).join('\n') || 'The agent asked a question.';
  const items: ActionItem[] = [
    { key: 'q', label: 'Copy question', icon: <Copy />, onSelect: () => copy(text) },
    { key: 'a', label: 'Copy answer', icon: <Copy />, disabled: !asked.answer, onSelect: () => copy(asked.answer ?? '') },
  ];
  const pending = asked.outcome === 'pending';
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<section id={`item-${id}`} aria-label="Question" className={cn('rounded-md bg-raised text-ui shadow-raised', pending && 'flex flex-col gap-1.5 px-3.5 py-3', className)} />}>
        {pending ? (
          <>
            <div className="flex items-center gap-1.5 text-caption text-muted">
              <MessageCircleQuestion aria-hidden="true" className="size-3.5 text-faint" />
              <span>Question</span>
              <span aria-hidden="true">·</span>
              <span className="text-attention">Waiting for your answer</span>
            </div>
            <QuestionDetail asked={asked} />
          </>
        ) : (
          <SettledQuestion id={id} asked={asked} />
        )}
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}, (a, b) => a.id === b.id && a.className === b.className && JSON.stringify(a.asked) === JSON.stringify(b.asked));

/** What a settled question got, in plain words: a marker ("You chose", "You wrote") and the answer, or the outcome alone. */
function settledAnswer(asked: AskedQuestion): { marker?: string; answer: string; tone: string } {
  const answer = asked.answer ?? '';
  switch (asked.outcome) {
    case 'declined':
      return { answer: 'Declined', tone: 'text-muted' };
    case 'failed':
      return { answer: asked.error ? `Failed: ${asked.error}` : 'Failed', tone: 'text-error' };
    case 'answered':
      break;
    default:
      return { answer: 'Not answered', tone: 'text-muted' };
  }
  if (!answer) return { answer: 'Answered', tone: 'text-muted' };
  if (asked.questions.length > 1) return { marker: 'You answered', answer, tone: 'text-ink' };
  // Picked: the answer is chosen labels and nothing else; anything more was typed.
  const choices = asked.questions[0]?.choices ?? [];
  const picked = asked.chosen.length > 0 && (asked.chosen.includes(answer) || answer.split(/;\s*|,\s*/).every((p) => choices.includes(p.trim())));
  return picked ? { marker: 'You chose', answer: asked.chosen.join(', '), tone: 'text-ink' } : { marker: 'You wrote', answer, tone: 'text-ink' };
}

/**
 * A settled question as one compact card (DESIGN.md question block): the question on the
 * first line and the answer on the second, side by side where the card is wide, each
 * truncated with the whole text in its tooltip and in the button's accessible name. The
 * button opens the full question below; the disclosure outlives re-renders and history pages.
 */
function SettledQuestion({ id, asked }: Readonly<{ id: string; asked: AskedQuestion }>) {
  const [open, setOpen] = useDisclosure(`question:${id}`);
  const [opened, setOpened] = useState(open);
  const bodyId = useId();
  const many = asked.questions.length > 1;
  const flat = (s: string) => s.replaceAll(/\s+/g, ' ').trim();
  const question = (many ? asked.questions.map((q) => flat(q.text)).join(' · ') : flat(asked.questions[0]?.text ?? '')) || 'The agent asked a question.';
  const { marker, answer, tone } = settledAnswer(asked);
  const lead = many ? `${asked.questions.length} questions` : '';
  const name = `${lead || 'Question'}: ${question} ${marker ? `${marker}: ` : ''}${answer}`;
  return (
    <div className="@container">
      <button
        type="button"
        aria-expanded={open}
        aria-controls={opened ? bodyId : undefined}
        aria-label={name}
        className="flex w-full items-center gap-2 rounded-md px-3 py-1.5 text-left transition-colors duration-100 hover:bg-tint-hover pointer-coarse:min-h-11"
        onClick={() => {
          setOpened(true);
          setOpen((o) => !o);
        }}
      >
        <MessageCircleQuestion aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
        {open ? (
          <span className="min-w-0 flex-1 text-caption text-muted">{lead || 'Question'}</span>
        ) : (
          <span className="flex min-w-0 flex-1 flex-col @2xl:flex-row @2xl:items-baseline @2xl:gap-3">
            <span className="min-w-0 truncate text-ink" title={question}>
              {lead && <span className="mr-1.5 text-muted">{lead}</span>}
              {question}
            </span>
            <span className="flex min-w-0 items-baseline gap-1.5 @2xl:max-w-1/2 @2xl:shrink-0">
              {marker && <span className="shrink-0 text-caption text-muted">{marker}</span>}
              <span className={cn('min-w-0 truncate', tone)} title={answer}>
                {answer}
              </span>
            </span>
          </span>
        )}
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      {opened && (
        <Collapse open={open} appear>
          <div id={bodyId} className="flex flex-col gap-1.5 pt-0.5 pr-3.5 pb-3 pl-[2.125rem]">
            <QuestionDetail asked={asked} />
          </div>
        </Collapse>
      )}
    </div>
  );
}

/** Every question in full with its choices (a check on the chosen ones), then what it got once settled. */
function QuestionDetail({ asked }: Readonly<{ asked: AskedQuestion }>) {
  const settled = asked.outcome === 'pending' ? null : settledAnswer(asked);
  return (
    <>
      {asked.questions.length === 0 && <p className="text-body">The agent asked a question.</p>}
      {asked.questions.map((q, k) => (
        <div key={k} className="flex flex-col gap-1">
          {q.header && <span className="text-caption text-muted">{q.header}</span>}
          <Markdown text={q.text} className="text-body" />
          {q.choices.length > 0 && (
            <ul className="flex flex-col gap-0.5">
              {q.choices.map((c) => {
                const chosen = asked.chosen.includes(c);
                return (
                  <li key={c} className={cn('flex items-start gap-2', chosen ? 'text-ink' : 'text-muted')}>
                    <span className="mt-[3px] flex size-3.5 shrink-0 items-center justify-center">
                      {chosen ? <Check aria-hidden="true" className="size-3.5 text-success" strokeWidth={2.5} /> : <span aria-hidden="true" className="size-2 rounded-full border-[1.5px] border-current opacity-60" />}
                    </span>
                    <span className={cn(chosen && 'font-medium')}>{c}</span>
                    {chosen && <span className="sr-only">(chosen)</span>}
                  </li>
                );
              })}
            </ul>
          )}
        </div>
      ))}
      {settled && <div className="fade-rule mt-0.5" aria-hidden="true" />}
      {settled?.marker && (
        <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
          <span className="text-caption text-muted">{settled.marker}</span>
          <span className="min-w-0 whitespace-pre-wrap break-words text-ink">{settled.answer}</span>
        </div>
      )}
      {settled && !settled.marker && <p className={cn('text-caption', settled.tone)}>{settled.answer}</p>}
    </>
  );
}

/** One non-user item. Everything from the provider is markdown, rendered without raw HTML, also while it streams. */
export const Turn = memo(function Turn({ item, sessionId, streaming, endedAt, className }: { item: Item; sessionId?: string; streaming: boolean; endedAt?: string; className?: string }) {
  switch (item.kind) {
    case 'user':
      return <UserBubble item={item} sessionId={sessionId} className={className} />;
    case 'assistant':
    case 'notice':
      if (item.clipped) return <ClippedMessage item={item} streaming={streaming} className={className} />;
      return item.kind === 'assistant' ? assistantMessage({ item, text: item.text ?? '', streaming, className }) : noticeRow({ item, text: item.text ?? '', className });
    case 'reasoning':
      return <Thinking item={item} streaming={streaming} endedAt={endedAt} className={className} />;
    case 'tool':
      return <ToolRow item={item} live={false} sessionId={sessionId} />;
    default:
      return null;
  }
});

const THINKING_KEY = 'uam.thinking:';

export { duration };

/**
 * A reasoning item: one 24px row, "Thinking…" shimmering while it streams and "Thought for
 * 12s" once the next item's timestamp is known, which opens the text (`muted`, behind a
 * hairline rule) on click with a height collapse. Nothing of the text shows while it is
 * closed, so streaming never resizes the row. The choice is remembered per item for the
 * browser session.
 */
/** The row's word: "Thinking…" while it streams, the time it took once known, else just that it thought. */
function thinkingLabel(streaming: boolean, took: string | null): string {
  if (streaming) return 'Thinking…';
  return took ? `Thought for ${took}` : 'Thought';
}

export function Thinking({ item, streaming, endedAt, className }: Readonly<{ item: Item; streaming: boolean; endedAt?: string; className?: string }>) {
  const key = THINKING_KEY + (item.agent_id ? `${item.agent_id}:` : '') + item.id;
  const [expanded, setExpanded] = useState(() => sessionStorage.getItem(key) === '1');
  const { item: fullItem, body, attach, retry } = useItemBody(item, expanded);
  const text = fullItem?.text ?? '';
  const took = !streaming && endedAt ? duration(item.time, endedAt) : null;
  const toggle = () => {
    const next = !expanded;
    setExpanded(next);
    sessionStorage.setItem(key, next ? '1' : '0');
  };
  return (
    <div ref={attach} className={cn('flex flex-col text-ui text-muted', className)}>
      <button type="button" aria-expanded={expanded} className="flex h-6 w-fit items-center gap-1.5 rounded-sm pr-1 text-left transition-colors duration-100 hover:text-body pointer-coarse:min-h-11" onClick={toggle}>
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', expanded && 'rotate-90')} />
        <span className={cn('tabular-nums', streaming && 'animate-shimmer motion-reduce:animate-none')}>{thinkingLabel(streaming, took)}</span>
      </button>
      <Collapse open={expanded}>
        <BodyNotice body={body} retry={retry} />
        {fullItem && <Copyable text={text} label="Copy thinking" className="mt-1 pr-6 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:fade-rule-y before:content-['']">
          <Markdown text={text} className="md-quiet" streaming={streaming} />
        </Copyable>}
      </Collapse>
    </div>
  );
}

/** Subagent state as a chip: glyph plus the word; only "running" moves. */
export function AgentChip({ status }: Readonly<{ status: SubagentStatus }>) {
  switch (status) {
    case 'running':
      return (
        <Chip tone="accent">
          <WorkingMark />
          Running
        </Chip>
      );
    case 'idle':
      return (
        <Chip>
          <SubagentIdleIcon />
          Idle
        </Chip>
      );
    case 'completed':
      return (
        <Chip tone="success">
          <Check aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
          Completed
        </Chip>
      );
    case 'failed':
      return (
        <Chip tone="error">
          <X aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
          Failed
        </Chip>
      );
    default:
      return (
        <Chip>
          <Minus aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
          Stopped
        </Chip>
      );
  }
}

/**
 * The `task` tool call that spawned a subagent, as one compact row: name, state, duration
 * once ended, and "Open", which shows the transcript in the side panel. Under the name, one
 * caption line says what it is doing (its latest step, once its transcript is held) or what
 * it reported (the first line of the `task` result, or its error); it grows in through the
 * height collapse and keeps its last text while it folds. Nothing else of the subagent's
 * output renders in the main column.
 */
function SubagentRow({ item, subagent, agentItems, provider, onOpen }: Readonly<{ item: Item; subagent: Subagent; agentItems?: Item[]; provider: string; onOpen: (opener: HTMLElement) => void }>) {
  const { meta } = useApp();
  const [, copy] = useCopied();
  const name = subagent.name || item.tool?.title || item.tool?.name || 'Subagent';
  const took = subagent.started_at && subagent.ended_at ? duration(subagent.started_at, subagent.ended_at) : null;
  const summary = subagentSummary(subagent, agentItems, item.tool);
  // The last text stays while the line folds, as usePresence keeps a row through its exit.
  const [shown, setShown] = useState(summary);
  if (summary && summary !== shown) setShown(summary);
  const items: ActionItem[] = [
    { key: 'open', label: 'Open subagent', icon: <Bot />, onSelect: () => onOpen(document.getElementById(`item-${item.id}`) ?? document.body) },
    { key: 'copy', label: 'Copy agent ID', icon: <Copy />, onSelect: () => copy(subagent.id), separator: true },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger
        render={<div id={`item-${item.id}`} className="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1 rounded-md bg-raised py-2 pr-2 pl-3.5 text-ui shadow-raised transition-colors" />}
      >
        <Bot aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <div className="flex min-w-0 flex-1 flex-col max-sm:basis-[calc(100%-28px)]">
          <span className="truncate font-medium text-ink" title={subagent.description || name}>
            {name}
          </span>
          <Collapse open={!!summary}>
            <span className="block truncate text-caption text-muted" title={shown}>
              {shown}
            </span>
          </Collapse>
        </div>
        <AgentChip status={subagent.status} />
        {subagent.model && <span className="min-w-0 truncate text-caption text-muted" title={modelName(meta, provider, subagent.model)}>{modelName(meta, provider, subagent.model)}</span>}
        {took && <span className="text-caption tabular-nums text-muted">{took}</span>}
        <Button size="sm" variant="secondary" className="h-7" onClick={(e) => onOpen(e.currentTarget)}>
          Open
        </Button>
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}

/** A subagent's own transcript at 13px: the same rows, blocks and bubbles as the main one, with its own requests. */
export function AgentItems({ sessionId, workdir, provider, agentId, items, identityItems = items, historyItemSeq, interactions, live, density }: Readonly<{ sessionId: string; workdir: string; provider: string; agentId: string; items: Item[]; identityItems?: Item[]; historyItemSeq?: Record<string, number>; interactions: Interaction[]; live: boolean; density: Density }>) {
  return (
    <div className="flex flex-col gap-3 text-ui [&_.text-chat]:text-ui [&_.text-chat-lg]:text-ui">
      <Transcript sessionId={sessionId} agentId={agentId} provider={provider} workdir={workdir} items={items} identityItems={identityItems} historyItemSeq={historyItemSeq} interactions={interactions} subagents={[]} live={live} working={live} density={density} />
    </div>
  );
}


function endedQuestion(item: Item, approvals: Map<string, Interaction[]>, live: boolean) {
  const question = askedOn(item, approvals, live);
  return question && question.outcome !== 'pending';
}

/** Keep group IDs across backward extension without retaining any row content. */
function useGroupIdentities(items: Item[], toolsOnly: boolean | 'turn' = false, special: (item: Item) => boolean = () => false, interruptions: string[] = []) {
  const key = JSON.stringify([items.filter(special).map(item => item.id), interruptions]);
  const [saved, setSaved] = useState(() => ({ items, key, ids: groupIdentities(items, toolsOnly, special, interruptions) }));
  if (saved.items !== items || saved.key !== key) {
    const next = { items, key, ids: groupIdentities(items, toolsOnly, special, interruptions, saved.ids) };
    setSaved(next);
    return next.ids;
  }
  return saved.ids;
}

/** Standalone decisions are group members too; keep only their identity and time. */
function useIdentityEntries(items: Item[], loose: Interaction[], questions: Interaction[]) {
  return useMemo(() => mergeByTime(items, [...loose, ...questions].filter(interaction => interaction.state !== 'pending')).map(entry => entry.item ?? ({ id: entry.interaction.id, kind: 'tool' as const, time: entry.interaction.time, agent_id: entry.interaction.agent_id })), [items, loose, questions]);
}
