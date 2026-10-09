import { BodyNotice, DetailVisibility, useDetailVisibility, useBodyCopy, useDisclosure, useItemBody, useEditDiff, useWholeText, type WholeText } from './Details';
import { Check, ChevronRight, ChevronUp, Copy, Ellipsis, FileDiff, GitBranch, MessageCircleQuestion, Minus, Pencil, RotateCcw, Terminal, X } from 'lucide-react';
import { Fragment, Suspense, lazy, memo, useCallback, useId, useLayoutEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode, type RefObject, type SyntheticEvent } from 'react';
import { flushSync } from 'react-dom';
import type { Interaction, Item, NativeFileEdit, Subagent, ToolStatus, TurnTiming } from '../api';
import { isChartCall } from '../lib/chart';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import type { Density } from '../lib/density';
import { compactTokens } from '../lib/cost';
import { approvalMark, askedOn, callProduct, changedFiles, scratchPlan, currentStep, duration, foregroundItems, itemTook, completedDuration, isSubagentCall, isWork, promoted, segmentActivity, summarizeActivity, summarizeTurn, timingForTurn, showTurnEnd, summarizeTools, linkInteractions, mergeByTime, questionOf, readableInput, toolKind, toolLabel, type AskedQuestion, type Entry, type Step, type TurnSummary } from '../lib/transcript';
import { groupIdentities } from '../lib/historyState';
import { GROUP_OVER, parentMap, replyIndex, subagentNoun, type IdentityTone, type Replies } from '../lib/subagents';
import { turnVerb } from '../lib/verbs';
import { receipts, type Stamp } from '../lib/receipts';
import { Receipts } from './Receipts';
import { ImageThumbs, ItemAttachments } from './Attachments';
import { CodeBlock, Markdown, SessionContext, Skeleton, Spinner, WorkdirContext, WorkingMark, clockTime, dateTime } from './common';
import { APPROVAL_ICONS, DecidedRow } from './Interactions';
import { LiveOutput } from './LiveOutput';
import { LiveSubagents, SubagentChip, SubagentList, SubagentRow, useLiveSubagentIds, useSubagentDisclosure, useSubagentReplies } from './Subagents';
import { TurnTodo } from './Todos';
import { PlanNotice } from './Plan';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Collapse, usePresence } from './ui/collapse';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** A chart card loads with the first chart shown, never with the app; meanwhile it stands as the card does while its rows load. */
const TurnChangesReader = lazy(() => import('./TurnChanges').then(m => ({ default: m.TurnChangesReader })));

const ChartCard = lazy(() => import('./Chart').then((m) => ({ default: m.ChartCard })));
const InlinePatch = lazy(() => import('./Changes').then((m) => ({ default: m.InlinePatch })));


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
  planVersion?: number;
  planAvailable?: boolean;
  /** The provider's scratch plan file; its edits are not the turn's changed files. */
  planPath?: string;
  /** The Task's requests; the decided ones join the turns, the pending ones stay cards. */
  interactions: Interaction[];
  /** The Task's subagents; the main transcript draws them (a `SubagentScope` holds the rest), a subagent's own passes none. */
  subagents: Subagent[];
  /** The provider still holds the turn, so pending tools may still report. */
  live: boolean;
  /** A turn is running (not merely waiting for the user): the last item is still streaming. */
  working: boolean;
  /** The event stream is up: a turn timing marked unknown is then one the service cut off, not one this page lost track of. */
  connected?: boolean;
  /** Provider id used to resolve subagent model names. */
  provider: string;
  /** The Task's directory, for links to its files. */
  workdir: string;
  /** The window ends at the live tail: the live subagent card sits at its foot. */
  liveCard?: boolean;
  /** Compact folds a turn's work into its head row (DESIGN.md turn line); detailed draws one activity row per run. */
  density?: Density;
  /** Open the Changes sheet on a turn's edits, from its "Changed n files" line; `latest` when it is the Task's latest turn. */
  onOpenChanges?: (turn: { files: string[]; latest: boolean }) => void;
  /** Keep a compact turn's "Changed n files" line; off where the Task's folder has no Changes view (no Git). */
  changedLine?: boolean;
  /** The full current workspace Changes view, separate from historical native facts. */
  onOpenAllChanges?: () => void;
  readersActive?: boolean;
  readerGeneration?: string;
  /** Name the turn's verb on the foot line between steps; the main pane's floating `WorkingLabel` carries it instead, and its foot line names only the current step (Compact). */
  footVerb?: boolean;
  /** The conversation is being compacted: the foot says so in place of the current step. */
  compacting?: boolean;
  /** Branch the exact owner message at the final reply's stable foot. */
  onBranch?: BranchReply;
  /** Rewind to before the exact owner message of a finished reply. */
  onRewind?: BranchReply;
  /** Edit that owner message in the composer, to rewind and send the edit. */
  onEdit?: BranchReply;
}

type BranchReply = (userItemId: string, anchor: HTMLElement | null) => void;

const NO_SUBAGENTS: Subagent[] = [];
const NO_TONES: ReadonlyMap<string, IdentityTone> = new Map();
const NO_ROWS: ReadonlyMap<string, ReactNode> = new Map();

/** Rows that arrive after mount rise in; rows present at mount appear at once. Stable, so memoised rows hold. */
function useArrivals(ids: string[], historyItemSeq?: Record<string, number>) {
  const [initial] = useState(() => new Set(ids));
  // The map is new with every frame; read through a ref it no longer changes the callback. A row's own
  // entry changes with its item (a frame or a history page replaces it), which re-renders the row anyway.
  const seq = useRef(historyItemSeq);
  // eslint-disable-next-line react-hooks/refs -- written before any row calls it in this render, so every call reads the frame being rendered.
  seq.current = historyItemSeq;
  return useCallback((id: string) => (initial.has(id) || seq.current?.[id] !== undefined ? '' : 'animate-rise'), [initial]);
}

/**
 * Main transcript. User items are bubbles on the right; everything between two user items
 * is one flat assistant turn: thinking inline, one row per tool call with its approval on
 * it, a question block for each `ask_user` call once it no longer waits, and the prose. A
 * decided request without a tool row joins the turn at its time. The subagents a reply spawned
 * are a chip on its turn line (Compact) or rows in its activity (Detailed), and the live card
 * at the foot while one runs; each opens in place onto its own transcript.
 */
export function Transcript({ sessionId, planVersion, planAvailable, planPath, agentId, items, identityItems = items, liveItems = items, historyItemSeq, turnTimings = [], interactions, subagents, live, working, connected = true, workdir, density = 'detailed', onOpenChanges, onOpenAllChanges, readersActive = true, readerGeneration = '', changedLine = true, footVerb = true, compacting = false, liveCard = false, onBranch, onRewind, onEdit }: Readonly<Props>) {
  const arrival = useArrivals([...items.map((i) => i.id), ...interactions.map((i) => i.id)], historyItemSeq);
  const byParent = parentMap(subagents);
  // Only the main transcript draws subagents; a subagent's own has none.
  const inline = !agentId && subagents.length > 0;
  // Which reply spawned which subagent comes from the identity index (the Task's `SubagentScope`), never from the window.
  const scoped = useSubagentReplies();
  const unscoped = useMemo(() => (scoped || !inline ? undefined : replyIndex(identityItems, subagents)), [scoped, inline, identityItems, subagents]);
  const replies = scoped ?? unscoped;
  const tones = replies?.tones ?? NO_TONES;
  const liveIds = useLiveSubagentIds();
  // Detailed: a reply past eight shows its subagents as one list at its first call in the window.
  const hosts = useMemo(() => {
    const map = new Map<string, string>();
    if (!replies || density === 'compact') return map;
    const shown = new Set(items.map((item) => item.id));
    for (const r of replies.list) {
      const first = r.subagents.length > GROUP_OVER ? r.calls.find((call) => shown.has(call)) : undefined;
      if (first) map.set(first, r.key);
    }
    return map;
  }, [replies, items, density]);
  const { linked, loose, questions } = linkInteractions(items, interactions, agentId);
  const groupItems = useIdentityEntries(identityItems, loose, questions);
  const turnIds = useGroupIdentities(groupItems, 'turn');
  const groupIds = useGroupIdentities(groupItems, false);
  const toolGroupIds = useGroupIdentities(identityItems, true, item => byParent.has(item.id) || !!endedQuestion(item, linked, live), [...loose, ...questions].filter(interaction => interaction.state !== 'pending').map(interaction => interaction.time));
  // Detailed: a subagent's row, in any state, takes its `task` call's place in the turn's activity.
  const subagentRow = (item: Item) => {
    const reply = inline ? replies?.ofCall.get(item.id) : undefined;
    if (reply && reply.subagents.length > GROUP_OVER) {
      const key = hosts.get(item.id);
      return key ? <HostedList key={item.id} replyKey={key} unscoped={unscoped} /> : <Fragment key={item.id} />;
    }
    const agent = inline ? byParent.get(item.id) : undefined;
    return agent ? (
      <div key={item.id} className="max-w-2xl overflow-hidden rounded-md bg-raised shadow-raised">
        <SubagentRow subagent={agent} tone={tones.get(agent.id)} anchor />
      </div>
    ) : null;
  };
  const replyActions = useMemo(() => ({ allChanges: onOpenAllChanges, branch: agentId ? undefined : onBranch, rewind: agentId ? undefined : onRewind, edit: agentId ? undefined : onEdit, active: readersActive, generation: readerGeneration }), [onOpenAllChanges, agentId, onBranch, onRewind, onEdit, readersActive, readerGeneration]);
  const stamps = new Map<string, Stamp[]>();
  const ctx: RenderContext = { replyActions, sessionId, planVersion, planAvailable, live, streamingId: working ? liveItems.at(-1)?.id : undefined, thoughtEnd: thoughtEnds(items), replyEnd: replyEnds(identityItems, turnTimings, working), receipts: stamps, arrival, approvals: linked, groupIds, toolGroupIds, subagentOf: (item) => (inline ? byParent.get(item.id) : undefined), foldedSubagentRow: subagentRow, tones, hostedBy: (item) => replies?.byKey.get(hosts.get(item.id) ?? '')?.calls };
  const scratch = scratchPlan(planPath, workdir);
  const compact = density === 'compact';
  // What a call produced for the person stands in the answer while the call folds like any
  // other: a chart in both densities, the images its result returned in Compact.
  const product = (item: Item) => {
    const kind = callProduct(item);
    return kind === 'chart' || (compact && kind) ? <CallProduct key={`${item.agent_id ?? 'main'}:${item.id}`} item={item} sessionId={sessionId} className={arrival(item.id)} /> : null;
  };
  const own = (item: Item) => callProduct(item) === 'chart';

  const foreground = new Set(foregroundItems(items).map((item) => item.id));
  const out: ReactNode[] = [];
  let group: Entry[] = [];
  let showedWorking = false;
  const firstIndex = identityItems.findIndex(item => item.id === items[0]?.id);
  let userItemId: string | undefined = identityItems.slice(0, Math.max(0, firstIndex)).reverse().find(item => item.kind === 'user' && !item.delivery)?.id;
  // A turn is keyed by the user message before it, so it keeps its rows when its first entry changes.
  let after = userItemId ?? 'start';
  // The turn's message is in this window, so all of its reply would be too (the last one only when the window reaches the live tail).
  let ownMessage = false;
  let footLive = live;
  const flush = (last = false, boundary = true) => {
    const timing = timingForTurn(turnTimings, userItemId);
    // A turn that ended is over, also while the next one starts before its message lands: it keeps its
    // end, and a call it left running is not the running step. A row begun after its end is a turn's that came without one.
    // So is a turn the service cut off (unknown without an end) while the stream is up, up to the next turn's start.
    const cut = connected && timing?.state === 'unknown' && !timing.ended_at;
    const end = cut ? turnTimings[turnTimings.indexOf(timing) + 1]?.started_at : timing?.ended_at;
    const ended = end ? Date.parse(end) : cut ? Infinity : Number.NaN;
    const begunSince = (entry: Entry) => !!entry.item && !(Date.parse(entry.item.time) < ended);
    const open = !(timing?.ended_at || cut) || group.some(begunSince);
    const showEnd = showTurnEnd(timing, { hasContent: group.length > 0, boundary, last, live: live && open });
    if (!group.length) {
      if (showEnd) out.push(<TurnStatus key={`end-${timing!.id}`} timing={timing} empty={ownMessage && (!last || liveCard)} />);
      return;
    }
    // The turn's last reply, once the turn is over, gets its receipts from the turn's calls (the main agent's, held here).
    if (!agentId && !open) {
      const reply = group.map((e) => e.item).filter((i): i is Item => !!i && !i.agent_id && i.kind === 'assistant').at(-1);
      if (reply?.text && ctx.replyEnd?.has(reply.id)) stamps.set(reply.id, stampsFor(reply, group.flatMap((e) => (e.item ? [e.item] : []))));
    }
    const groupLive = live && group.some((entry) => entry.item && foreground.has(entry.item.id) && begunSince(entry));
    const gctx = { ...ctx, live: groupLive, streamingId: groupLive ? ctx.streamingId : undefined };
    if (last) footLive = groupLive;
    const runs = last && working && open;
    if (runs) showedWorking = true;
    // One status row heads the turn and keeps its slot while it runs, so "Took 12s" lands there
    // at the end and streamed content lands below it: nothing on screen moves at either moment.
    if (compact) {
      // Compact: the head row carries the turn's counts and opens its timeline; only promoted
      // entries stand in the answer, and a steer bubble sits in the turn at its place.
      const summary = summarizeTurn(group, { live: groupLive, streamingId: gctx.streamingId, approvals: linked, scratch });
      const changed = changedFiles(group, scratch);
      const first = (group[0].item ?? group[0].interaction).id;
      const id = (first && turnIds.get(first)) ?? userItemId ?? 'start';
      const reply = inline ? replies?.byKey.get(userItemId ?? 'start') : undefined;
      const spawned = reply?.subagents ?? NO_SUBAGENTS;
      // The live card shows every one of them: its chip waits until the card leaves. Every call has spawned one
      // the card shows (a nested subagent's call may or may not be among the reply's: it is in its parent's transcript).
      const covered = !!(liveCard && liveIds && reply && spawned.every((s) => liveIds.has(s.id)) && reply.calls.every((call) => spawned.some((s) => s.parent_tool_call_id === call)));
      out.push(
        <div key={`turn-${id}`} data-reply={userItemId ?? 'start'} className="flex flex-col gap-3">
          {runs || showEnd || summary.count > 0 || spawned.length > 0 ? <TurnHead id={id} agentId={agentId} working={runs} timing={timing} summary={summary} entries={group} ctx={gctx} subagents={spawned} calls={reply?.calls.length} chip={!covered} tones={tones} /> : null}
          {renderCompact(group, gctx, product, own)}
          {changedLine && changed.length > 0 && <ChangedLine files={changed} latest={last && liveCard} onOpen={onOpenChanges} />}
        </div>,
      );
    } else {
      const nodes = renderEntries(group, gctx, product);
      out.push(
        <div key={`turn-${after}`} data-reply={userItemId ?? 'start'} className="flex flex-col gap-3">
          {runs ? <TurnStatus working /> : showEnd && <TurnStatus timing={timing} />}
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
    if (!entry.item.delivery) {
      userItemId = entry.item.id;
      ownMessage = true;
    }
    out.push(<UserBubble key={entry.item.id} item={entry.item} sessionId={sessionId} className={arrival(entry.item.id)} />);
  });
  flush(true);
  // Compact draws no live activity row, so the foot shows the current step itself, unfolded.
  const step = compact && working && !compacting ? currentStep(items, { live: footLive, streamingId: footLive ? ctx.streamingId : undefined, approvals: linked }, own) : null;
  const current = step?.item && <LiveStep key={step.item.id} item={step.item} live={live} sessionId={sessionId} approvals={linked.get(step.item.id)} />;
  return (
    <SessionContext.Provider value={sessionId}>
      <WorkdirContext.Provider value={workdir}>
        {out}
        {working && !showedWorking && <TurnStatus working />}
        {inline && liveCard && <LiveSubagents />}
        <WorkingTail working={working && (compacting || compact || (footVerb && !liveAtFoot(items)))} turnId={userItemId ?? 'start'} step={step} verb={footVerb} current={current} compacting={compacting} />
      </WorkdirContext.Provider>
    </SessionContext.Provider>
  );
}

/**
 * Compact (DESIGN.md turn line): the entries that stand in the answer, in order. Prose,
 * notices and steer bubbles; the promoted work, that is a question that no longer waits and
 * what a call produced (the images its result returned, a chart) without the
 * call's row. Every other call, failed ones included, is in the turn line and its timeline; a
 * subagent's call is on the reply's subagent chip.
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
      out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} planVersion={ctx.planVersion} planAvailable={ctx.planAvailable} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} end={ctx.replyEnd?.get(item.id)} stamps={ctx.receipts?.get(item.id)} replyActions={ctx.replyActions} className={ctx.arrival(item.id)} />);
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

const TurnHead = memo(function TurnHead({ id, agentId, working, timing, summary, entries, ctx, subagents = NO_SUBAGENTS, calls = subagents.length, chip = true, tones = NO_TONES }: Readonly<{ id: string; agentId?: string; working: boolean; timing?: TurnTiming; summary: TurnSummary; entries: Entry[]; ctx: RenderContext; /** The subagents the reply spawned: a chip beside the counts that opens their list under the line. */ subagents?: Subagent[]; /** The reply's `task` calls, loaded or not. */ calls?: number; /** False while the live card shows the same subagents (an open list keeps it). */ chip?: boolean; tones?: ReadonlyMap<string, IdentityTone> }>) {
  // Item IDs are local to their agent; main-turn identities stay unchanged.
  const scope = agentId ? 'agent-turn' : 'turn';
  const scopedId = agentId ? encodeURIComponent(JSON.stringify([agentId, id])) : id;
  const [open, setOpen] = useDisclosure(`${scope}:${scopedId}`);
  const [opened, setOpened] = useState(open);
  // The reply's subagent list, mounted on its first open (a `locate` may be what opens it).
  const [listOpen, setListOpen] = useSubagentDisclosure(`subagents:${scopedId}`, subagents);
  const [listOpened, setListOpened] = useState(listOpen);
  if (listOpen && !listOpened) setListOpened(true);
  const elapsed = working ? null : completedDuration(timing);
  const head = elapsed ? `Took ${elapsed}` : '';
  const text = [head, ...summary.parts.map((p) => p.text)].filter(Boolean).join(' · ');
  const domId = `${scope}-${scopedId}`;
  const timelineId = `${domId}-timeline`;
  const listId = `${domId}-subagents`;
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
      <div data-history-anchor={`turn-head-${id}`} data-history-items={JSON.stringify(entries.flatMap(entry => entry.item ? [entry.item.id] : []))} className="flex min-h-[34px] flex-wrap items-center gap-x-4 py-2 text-caption tabular-nums text-muted" title={working || summary.count ? undefined : durationTitle(elapsed)}>
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
        {subagents.length > 0 && (chip || listOpen) && <SubagentChip subagents={subagents} calls={calls} tones={tones} open={listOpen} controls={listOpened ? listId : undefined} onToggle={() => setListOpen((o) => !o)} />}
      </div>
      {listOpened && subagents.length > 0 && (
        <Collapse open={listOpen} appear inner="pb-2">
          <DetailVisibility open={listOpen}><SubagentList id={listId} subagents={subagents} calls={calls} tones={tones} /></DetailVisibility>
        </Collapse>
      )}
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
}, (a, b) => {
  const same = (x: Entry, y: Entry) => (x.item ?? x.interaction) === (y.item ?? y.interaction);
  // Its counts and its timeline follow from its entries and these context fields alone, as an `ActivityRun`'s do; `summary` is rebuilt every render.
  return a.id === b.id && a.agentId === b.agentId && a.working === b.working && a.timing === b.timing && a.subagents === b.subagents && a.calls === b.calls && a.chip === b.chip && a.tones === b.tones
    && a.ctx.live === b.ctx.live && a.ctx.sessionId === b.ctx.sessionId && a.ctx.replyActions === b.ctx.replyActions && a.ctx.approvals === b.ctx.approvals
    && streamingIn(a.entries, a.ctx.streamingId) === streamingIn(b.entries, b.ctx.streamingId) && a.entries.length === b.entries.length && a.entries.every((e, i) => same(e, b.entries[i]))
    && a.entries.every((e) => !e.item || a.ctx.subagentOf?.(e.item) === b.ctx.subagentOf?.(e.item));
});

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
    // A subagent's call is its row in the reply's subagent list, not a step here.
    if (ctx.subagentOf?.(item)) continue;
    const asked = askedOn(item, ctx.approvals, ctx.live);
    const question = asked && asked.outcome !== 'pending' ? ctx.approvals.get(item.id)?.find((ix) => ix.kind === 'question') : undefined;
    row(item.id, item.time, took, question ? <DecidedRow interaction={question} /> : <ToolRow item={item} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals.get(item.id)} />);
  }
  return (
    <div id={id} className="relative mb-2 ml-1.5 flex flex-col gap-1 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-px before:fade-rule-y before:content-['']">
      {rows}
    </div>
  );
}

/** A timeline step's start (to the second, 24-hour) and its recorded duration, blank while it runs or when unrecorded. A phone keeps the duration alone, so the step's details keep the width; the start stays in the tooltip. */
function StepTime({ time, took }: Readonly<{ time: string; took: string | null }>) {
  const at = new Date(time);
  return (
    <span className="flex h-6 shrink-0 items-center gap-2 text-caption tabular-nums text-faint pointer-coarse:h-11" title={dateTime(at)}>
      <span className="max-sm:hidden">{clockTime(at, true)}</span>
      <span className="w-10 text-right text-muted">{took}</span>
    </span>
  );
}

/** The one line a compact turn keeps for its edits: "Changed 2 files", with the paths in its tooltip, and the way to the Changes sheet. */
function ChangedLine({ files, latest, onOpen }: Readonly<{ files: string[]; latest: boolean; onOpen?: (turn: { files: string[]; latest: boolean }) => void }>) {
  return (
    <div className="flex h-6 items-center gap-2 text-caption text-muted" title={files.join('\n')}>
      <FileDiff aria-hidden="true" className="size-3.5 shrink-0 text-faint" />
      <span>
        Changed {files.length} {files.length === 1 ? 'file' : 'files'}
      </span>
      {onOpen && (
        // Focused first (Safari does not focus a clicked button), so closing Changes brings focus back here.
        <button type="button" className="rounded-xs text-accent hover:underline" onClick={(e) => { e.currentTarget.focus(); onOpen({ files, latest }); }}>
          View changes
        </button>
      )}
    </div>
  );
}

/**
 * Whether the last row is already live: a thought streaming or a call running folds into an
 * activity row that carries the working mark and says so ("Thinking…", "Running: …").
 */
function liveAtFoot(items: Item[]): boolean {
  const last = items.at(-1);
  if (!last) return false;
  if (last.kind === 'reasoning') return true;
  return last.kind === 'tool' && (last.tool?.status === 'pending' || last.tool?.status === 'running');
}

/**
 * The live line at the foot of a running turn, where new output lands: a steady gerund
 * ("Untangling…", words only: the working mark comes with a step or compacting) while prose
 * streams or the agent is between steps. The verb
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
            {text && (compacting || step) && (step?.tone === 'attention' && !compacting ? <MessageCircleQuestion aria-hidden="true" className="size-3.5 text-warning" /> : <WorkingMark className={compacting ? 'text-badge-violet' : undefined} />)}
            <span className={cn('min-w-0 truncate', compacting ? 'text-badge-violet' : step?.tone === 'attention' && 'text-warning')} title={step?.label}>
              {text}
            </span>
          </div>
        )}
      </div>
    </Collapse>
  );
}

/**
 * Compact (DESIGN.md live step): the step in progress at the turn's foot, unfolded. A thought
 * streaming is "Thinking…" over its text as it arrives, in a box at most 80px tall that shows its
 * newest lines, the older ones clipped above, so it never grows into a block and nothing scrolls;
 * a call running is its tool row (mark, name, argument) over its newest output lines, which its
 * row carries (`LiveOutput`). It rises in; once the step ends it is gone from here and counted on the turn line.
 */
function LiveStep({ item, live, sessionId, approvals }: Readonly<{ item: Item; live: boolean; sessionId: string; approvals?: Interaction[] }>) {
  const thought = item.kind === 'reasoning';
  const { item: full, attach } = useItemBody(item, thought);
  const text = thought ? full?.text ?? '' : '';
  return (
    <div ref={attach} className="flex animate-rise flex-col gap-1">
      {thought ? (
        <div aria-hidden="true" className="flex h-6 items-center gap-2 text-caption text-muted">
          <WorkingMark />
          <span>Thinking…</span>
        </div>
      ) : (
        <ToolRow item={item} live={live} sessionId={sessionId} approvals={approvals} tail={false} />
      )}
      {text.trim() && (
        <div className="relative flex max-h-20 flex-col justify-end overflow-hidden pl-3 before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:fade-rule-y before:content-['']">
          <Markdown text={text} className="md-quiet shrink-0 text-ui text-muted" streaming />
        </div>
      )}
      {!thought && <LiveOutput lines={item.tool?.tail} className="ml-6" />}
    </div>
  );
}

interface RenderContext {
  planVersion?: number;
  planAvailable?: boolean;
  sessionId?: string;
  replyActions?: ReplyActions;
  live: boolean;
  /** The item still receiving deltas, if any. */
  streamingId: string | undefined;
  /** For each reasoning item with a recorded end: when it ended. */
  thoughtEnd: Map<string, string>;
  /** For the last reply of each ended turn: when the turn ended and its timing, so that reply alone carries the foot with the time and the turn's tokens. */
  replyEnd?: ReadonlyMap<string, ReplyEnd>;
  /** For the last reply of each ended turn: its claims stamped against the turn's record (lib/receipts). */
  receipts?: ReadonlyMap<string, Stamp[]>;
  arrival: (id: string) => string;
  /** Requests by the tool item they sit on, oldest first. */
  approvals: Map<string, Interaction[]>;
  groupIds?: Map<string, string>;
  toolGroupIds?: Map<string, string>;
  /** The subagent a `task` call spawned (main transcript only), and its row, which takes the call's place in its turn's activity (Detailed). */
  subagentOf?: (item: Item) => Subagent | undefined;
  foldedSubagentRow?: (item: Item) => ReactNode | null;
  /** The subagents' identity tones, which their rows (in `foldedSubagentRow`) draw. */
  tones?: ReadonlyMap<string, IdentityTone>;
  /** The `task` calls of the reply whose list a call hosts (Detailed, past eight), so `locate` finds the run that holds it. */
  hostedBy?: (item: Item) => string[] | undefined;
}


/** Each reasoning item's recorded end. */
function thoughtEnds(items: Item[]): Map<string, string> {
  const m = new Map<string, string>();
  for (const item of items) if (item.kind === 'reasoning' && item.ended_at) m.set(item.id, item.ended_at);
  return m;
}

/**
 * Entries in order. Each contiguous run of work between two messages (thinking, tool calls,
 * subagents' calls, decided requests, questions that no longer wait) folds into one activity
 * row; prose stands on its own between them, and what `product` draws for a call in a run (a
 * chart) stands right after that run.
 */
function renderEntries(entries: Entry[], ctx: RenderContext, product?: (item: Item) => ReactNode | null): ReactNode[] {
  // A pending request is the action card under the transcript; it is not drawn twice.
  const drawn = entries.filter((entry) => entry.interaction?.state !== 'pending');
  const out: ReactNode[] = [];
  let pos = 0;
  for (const segment of segmentActivity(drawn, isWork)) {
    pos += segment.entries.length;
    if (!segment.work) {
      out.push(...renderRows(segment.entries, ctx, NO_ROWS));
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
function renderRows(entries: Entry[], ctx: RenderContext, own: ReadonlyMap<string, ReactNode>): ReactNode[] {
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
    out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} planVersion={ctx.planVersion} planAvailable={ctx.planAvailable} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} end={ctx.replyEnd?.get(item.id)} stamps={ctx.receipts?.get(item.id)} replyActions={ctx.replyActions} className={ctx.arrival(item.id)} />);
  }
  flush();
  return out;
}

/** The streaming item's id when it is in `entries`; the row only cares about its own. */
const streamingIn = (entries: Entry[], id: string | undefined) => (id && entries.some((e) => e.item?.id === id) ? id : undefined);

/**
 * One run of work as a 24px `caption` row (DESIGN.md activity row): a chevron, or the
 * working mark while a call runs or thinking streams and the run is closed (open, the chevron
 * turns `accent` and the running row inside has the mark), and the summary, which updates in
 * place as the run grows and truncates rather than wraps, so streaming never moves the
 * page. Failures turn it `error`, a call waiting for permission `attention`. It opens
 * through the shared height collapse onto the rows themselves, indented, each with its own
 * disclosure. Memoised on its entries: text streaming into another item leaves it alone.
 */
const ActivityRun = memo(function ActivityRun({ identity, entries, ctx, endedAt, className }: { identity: string; entries: Entry[]; ctx: RenderContext; endedAt?: string; className?: string }) {
  const [open, setOpen] = useDisclosure(`activity:${entries[0]?.item?.agent_id ?? ''}:${identity}`);
  // The rows are mounted on the first open only: a closed run costs one button.
  const [opened, setOpened] = useState(open);
  const summary = summarizeActivity(entries, { live: ctx.live, streamingId: ctx.streamingId, approvals: ctx.approvals, endedAt });
  const { tone, active } = summary;
  // Subagents are not counted in the label; a run of nothing else is named by them.
  const only = entries.length > 0 && entries.every((e) => isSubagentCall(e.item));
  const label = only ? [subagentNoun(entries.length), summary.label].filter(Boolean).join(' · ') : summary.label;
  const hosted = entries.flatMap((e) => (e.item && ctx.hostedBy?.(e.item)) || []);
  if (!label) return null;
  return (
    <div data-activity="" className={cn('flex flex-col', className)}>
      <button data-history-anchor={`activity-${identity}`} data-history-items={JSON.stringify(entries.flatMap(entry => entry.item ? [entry.item.id] : []))} data-subagent-items={hosted.length ? JSON.stringify(hosted) : undefined} type="button" aria-expanded={open} title={label} className={cn('flex h-6 w-fit max-w-full items-center gap-2 rounded-full bg-tint-well pr-3 pl-2 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11', tone === 'error' && 'text-error', tone === 'attention' && 'text-attention')} onClick={() => { setOpened(true); setOpen((o) => !o); }}>
        <span className="flex size-3.5 shrink-0 items-center justify-center">
          {active && !open ? <WorkingMark /> : <ChevronRight aria-hidden="true" className={cn('size-3 text-faint transition-transform duration-160 ease-app', open && 'rotate-90', active && 'text-accent')} />}
        </span>
        <span className="min-w-0 truncate tabular-nums">{label}</span>
      </button>
      {!open && ctx.live && entries.map(({ item }) => item?.tool?.progress && <div key={item.id}>{toolProgress(item.tool, true)}</div>)}
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
  return a.endedAt === b.endedAt && a.className === b.className && a.ctx.live === b.ctx.live && a.ctx.sessionId === b.ctx.sessionId && a.ctx.replyActions === b.ctx.replyActions && a.ctx.approvals === b.ctx.approvals && a.ctx.arrival === b.ctx.arrival && a.ctx.tones === b.ctx.tones
    && streamingIn(a.entries, a.ctx.streamingId) === streamingIn(b.entries, b.ctx.streamingId) && a.entries.length === b.entries.length && a.entries.every((e, i) => same(e, b.entries[i]))
    && a.entries.every((e) => !e.item || (a.ctx.subagentOf?.(e.item) === b.ctx.subagentOf?.(e.item) && a.ctx.hostedBy?.(e.item) === b.ctx.hostedBy?.(e.item)));
});

/** A long reply's subagents as one list in its activity (Detailed): the reply comes from the scope, so the list follows all of them. */
function HostedList({ replyKey, unscoped }: Readonly<{ replyKey: string; /** The replies, outside a Task scope. */ unscoped?: Replies }>) {
  const id = useId();
  const replies = useSubagentReplies() ?? unscoped;
  const reply = replies?.byKey.get(replyKey);
  if (!replies || !reply) return null;
  return <SubagentList id={id} subagents={reply.subagents} calls={reply.calls.length} tones={replies.tones} />;
}

/** The subagents' rows among `entries`, which take their `task` calls over. */
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
function TurnStatus({ working = false, timing, empty = false }: Readonly<{ working?: boolean; timing?: TurnTiming; /** The turn ended without a reply, a thought or a call: say so after its duration. */ empty?: boolean }>) {
  const elapsed = working ? null : completedDuration(timing);
  return (
    <div className="flex min-h-[34px] items-center gap-2 py-2 text-caption tabular-nums text-muted" title={working ? undefined : durationTitle(elapsed)}>
      {elapsed && <span className="animate-fade-in">Took {elapsed}{empty && ' · No reply'}</span>}
    </div>
  );
}

interface ReplyActions { allChanges?: () => void; branch?: BranchReply; rewind?: BranchReply; edit?: BranchReply; active: boolean; generation: string }

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
function Copyable({ text, read, label, className, side = 'right', at, timing, replyActions, branch, rewind, edit, foot = true, children, extra = [] }: Readonly<{ text: string; /** Reads the text to copy when `text` is only part of it. */ read?: () => Promise<string>; label: string; className?: string; /** Where the button sits: over the block's top-right corner, or outside it to the left (the user bubble, so it never covers the text). */ side?: 'right' | 'left'; /** When the message began: its clock time in the foot under the block, the full date in its tooltip. */ at?: string; /** The turn this message ends: its tokens out and generation speed follow the time, then its todo list, which keeps the foot in view. */ timing?: TurnTiming; replyActions?: ReplyActions; branch?: (anchor: HTMLElement | null) => void; rewind?: (anchor: HTMLElement | null) => void; edit?: (anchor: HTMLElement | null) => void; /** Whether the block has a foot at all; without one, copying is in the right-click menu alone. */ foot?: boolean; children: ReactNode; extra?: ActionItem[] }>) {
  const [copied, copy] = useCopied();
  const [menuReady, setMenuReady] = useState(false);
  const anchor = useRef<HTMLDivElement>(null);
  const visible = useDetailVisibility();
  const [changesReader, setChangesReader] = useState<{ generation: string; phone: boolean; open: boolean; anchor: HTMLElement | null } | null>(null);
  if (changesReader && (!visible || replyActions?.active === false || changesReader.generation !== (replyActions?.generation ?? ''))) setChangesReader(null);
  // A turn that changed nothing says nothing: no "0 files" in its foot or its menu.
  const changes = timing?.changes && !(timing.changes.status === 'available' && !timing.changes.files && !timing.changes.omitted) ? timing.changes : undefined;
  const showChanges = () => setChangesReader({ generation: replyActions?.generation ?? '', phone: window.matchMedia('(max-width: 639px)').matches, open: true, anchor: anchor.current?.querySelector<HTMLButtonElement>('[data-turn-changes]') ?? anchor.current });
  const menuHandlers = useRef<CopyableMenuEvents | null>(null);
  const run = () => { if (read) void read().then(copy, () => {}); else copy(text); };
  const turnItems: ActionItem[] = [
    ...(changes ? [{ key: 'turn-changes', label: "This turn's changes", icon: <FileDiff />, onSelect: showChanges, takesFocus: true }] : []),
    ...(timing && replyActions?.allChanges ? [{ key: 'all-changes', label: 'All changes', icon: <FileDiff />, onSelect: replyActions.allChanges }] : []),
    ...(edit ? [{ key: 'edit', label: 'Edit and resend…', icon: <Pencil />, takesFocus: true, onSelect: () => edit(anchor.current) }] : []),
    ...(branch ? [{ key: 'branch', label: 'Branch from here', icon: <GitBranch />, takesFocus: true, onSelect: () => branch(anchor.current?.querySelector<HTMLElement>('[data-fork-anchor]') ?? anchor.current) }] : []),
    ...(rewind ? [{ key: 'rewind', label: 'Rewind to before this prompt…', icon: <RotateCcw />, takesFocus: true, onSelect: () => rewind(anchor.current?.querySelector<HTMLElement>('[data-fork-anchor]') ?? anchor.current) }] : []),
  ];
  const items: ActionItem[] = [{ key: 'copy', label, icon: <Copy />, onSelect: run }, ...turnItems, ...extra];

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
      ref={anchor}
      className={cn('group/copy relative flex flex-col', side === 'left' && 'items-end', foot && 'not-last:mb-3', className)}
      style={{ WebkitTouchCallout: 'none' }}
      onContextMenu={event => menuEvents(event)?.onContextMenu?.(event)}
      onTouchStart={event => menuEvents(event)?.onTouchStart?.(event)}
      onTouchMove={menuReady ? event => menuEvents(event)?.onTouchMove?.(event) : undefined}
      onTouchEnd={menuReady ? event => menuEvents(event)?.onTouchEnd?.(event) : undefined}
      onTouchCancel={menuReady ? event => menuEvents(event)?.onTouchCancel?.(event) : undefined}
    >
      {children}
      {/* The foot: the copy glyph and the time under the block, at its start (the agent) or its end (the user). It keeps its row and fades in while the block is hovered or focused; a coarse pointer has no hover, so there it stays. */}
      {foot && (
        <div className={cn('absolute top-full z-[1] flex h-6 items-center gap-1 text-stamp tabular-nums text-faint opacity-0 transition-opacity duration-100 group-hover/copy:opacity-100 group-focus-within/copy:opacity-100 pointer-coarse:opacity-100', side === 'left' ? 'right-0 -mr-1 flex-row-reverse' : 'left-0 -ml-1', (copied || timing?.todo?.total || changes) && 'opacity-100')}>
          <Tip label={copied ? 'Copied' : label}>
            <Button size="icon-sm" variant="ghost" aria-label={copied ? 'Copied' : label} className={cn('text-faint transition-colors duration-100 hover:text-ink focus-visible:text-ink', copied && 'text-success')} onClick={run}>
              {copied ? <Check /> : <Copy />}
            </Button>
          </Tip>
          {at && (
            <time dateTime={at} title={dateTime(at)} className="whitespace-nowrap">
              {clockTime(at)}
            </time>
          )}
          {timing && <TurnTokens timing={timing} />}
          {changes && <>
            <span aria-hidden="true"> · </span>
            <button data-turn-changes="" type="button" aria-haspopup="dialog" aria-expanded={!!changesReader?.open} className="rounded-sm px-1 whitespace-nowrap hover:text-ink" onClick={showChanges}>
              {changes.status === 'available' ? <>{changes.files ?? 0} {(changes.files ?? 0) === 1 ? 'file' : 'files'} <span className="text-success">+{changes.additions ?? 0}</span> <span className="text-error">−{changes.deletions ?? 0}</span></> : 'Changes unavailable'}
            </button>
          </>}
          {timing && <TurnTodo timing={timing} />}
          {(branch || rewind || edit || (timing && (changes || replyActions?.allChanges))) && <Menu.Root>
            <Menu.Trigger render={<Button data-fork-anchor="" variant="ghost" size="icon-sm" aria-label="Turn actions" className="text-faint" />}><Ellipsis /></Menu.Trigger>
            <Menu.Content><Menu.Actions items={turnItems} /></Menu.Content>
          </Menu.Root>}
        </div>
      )}
      {changesReader && timing && <Suspense fallback={null}><TurnChangesReader timing={timing} anchor={changesReader.anchor} phone={changesReader.phone} open={changesReader.open} onClose={() => setChangesReader(r => r && { ...r, open: false })} onClosed={() => setChangesReader(null)} onAllChanges={replyActions?.allChanges} /></Suspense>}
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

/** The reply's foot: the turn's tokens out and, when the calls reported their duration, tokens per second of generation; the tooltip holds the whole count. */
function TurnTokens({ timing }: Readonly<{ timing: TurnTiming }>) {
  const out = timing.output_tokens ?? 0;
  if (!out) return null;
  const rate = timing.generation_ms ? Math.round(out / (timing.generation_ms / 1000)) : 0;
  const title = `${(timing.input_tokens ?? 0).toLocaleString()} tokens in · ${out.toLocaleString()} out${timing.generation_ms ? ` · ${Math.round(timing.generation_ms / 1000)}s generating` : ''}`;
  return (
    <span className="whitespace-nowrap" title={title}>
      <span aria-hidden="true"> · </span>
      {compactTokens(out)} tokens
      {rate > 0 && (
        <>
          <span aria-hidden="true"> · </span>
          {rate} tok/s
        </>
      )}
    </span>
  );
}

/** What the last reply of a turn shows in its foot: when the turn ended and, when known, its timing. */
interface ReplyEnd { at: string; timing?: TurnTiming; userItemId: string }

/** A reply's stamps, kept while the reply and the calls they read are unchanged, so a memoised turn holds. */
const stampsOf = new WeakMap<Item, { key: string; stamps: Stamp[] }>();
function stampsFor(reply: Item, items: Item[]): Stamp[] {
  const key = items.map((i) => (i.kind === 'tool' ? `${i.id}:${i.tool?.status}:${i.tool?.exit_code ?? ''}:${i.tool?.file_edits?.length ?? 0}` : '')).join('|');
  const held = stampsOf.get(reply);
  if (held?.key === key) return held.stamps;
  const stamps = receipts(reply.text ?? '', items);
  stampsOf.set(reply, { key, stamps });
  return stamps;
}

/** Each reply's last foot: an unchanged one keeps its identity, so its memoised `Turn` holds while other rows stream. */
const replyEndOf = new WeakMap<Item, ReplyEnd>();

/** The end of each turn, keyed by the turn's last assistant message, so that reply alone carries the foot: the time the turn ended and the whole turn's tokens, from the user's message to the agent stopping. A turn still working has no end yet, so its replies show nothing. */
export function replyEnds(items: Item[], timings: TurnTiming[], working: boolean): ReadonlyMap<string, ReplyEnd> | undefined {
  const lastReply = new Map<string, Item>();
  let turn: string | undefined;
  for (const item of items) {
    if (item.agent_id) continue;
    if (item.kind === 'user' && !item.delivery) turn = item.id;
    else if (item.kind === 'assistant' && turn) lastReply.set(turn, item);
  }
  if (!lastReply.size) return undefined;
  const timingOf = new Map(timings.filter((t) => t.user_item_id).map((t) => [t.user_item_id!, t]));
  const out = new Map<string, ReplyEnd>();
  for (const [userItemId, reply] of lastReply) {
    const timing = timingOf.get(userItemId);
    // Without a recorded timing, only the live last turn can still be working.
    const ended = timing ? timing.state !== 'working' : !(working && userItemId === turn);
    if (!ended) continue;
    const at = timing?.ended_at ?? reply.time;
    const branchUser = userItemId.length <= 256 ? userItemId : '';
    let end = replyEndOf.get(reply);
    if (end?.at !== at || end.timing !== timing || end.userItemId !== branchUser) {
      end = { at, timing, userItemId: branchUser };
      replyEndOf.set(reply, end);
    }
    out.set(reply.id, end);
  }
  return out;
}

/** The user's turn: a bubble with the text as typed, then its uploads. The item carries no list of its `@path` references, so those stay plain text. */
const UserBubble = memo(function UserBubble({ item, sessionId, className }: { item: Item; sessionId?: string; className?: string }) {
  return item.clipped ? <ClippedMessage item={item} sessionId={sessionId} className={className} /> : userBubble({ item, text: item.text ?? '', sessionId, className });
});

/** A message row's parts: `text` is the item's, or a clipped item's shown part, with `whole` for its note and copy. Plain functions, so a row costs no extra component. */
interface MessageParts { item: Item; text: string; sessionId?: string; streaming?: boolean; className?: string; whole?: WholeText; /** Set when this reply ends its turn: the foot shows then. */ end?: ReplyEnd; replyActions?: ReplyActions; /** The reply's claims against its turn's record, when it ends the turn. */ stamps?: Stamp[] }

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
        <Copyable text={text} read={whole?.status === 'whole' ? undefined : whole?.read} label="Copy message" side="left" at={item.time} className={width}>
          {bubble}
        </Copyable>
      ) : (
        <div className={width}>{bubble}</div>
      )}
    </div>
  );
}

function assistantMessage({ item, text, streaming = false, className, whole, end, replyActions, stamps }: MessageParts) {
  const onBranch = replyActions?.branch;
  const onRewind = replyActions?.rewind;
  const onEdit = replyActions?.edit;
  return (
    <Copyable text={text} read={whole?.status === 'whole' ? undefined : whole?.read} label="Copy message" at={end?.at} timing={end?.timing} replyActions={replyActions} branch={end?.userItemId && onBranch ? anchor => onBranch(end.userItemId, anchor) : undefined} rewind={end?.userItemId && onRewind ? anchor => onRewind(end.userItemId, anchor) : undefined} edit={end?.userItemId && onEdit ? anchor => onEdit(end.userItemId, anchor) : undefined} foot={!!end} className={className}>
      <div data-history-anchor={item.id} className="text-chat text-body">
        {whole?.status === 'whole' ? plainText(text) : <Markdown text={text} streaming={streaming} />}
        {whole && <WholeNote whole={whole} />}
        {stamps && <Receipts stamps={stamps} />}
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
function ClippedMessage({ item, sessionId, streaming, className, end, replyActions }: Readonly<{ item: Item; sessionId?: string; streaming?: boolean; className?: string; end?: ReplyEnd; replyActions?: ReplyActions }>) {
  const whole = useWholeText(item);
  const parts = { item, text: whole.text, sessionId, streaming, className, whole, end, replyActions };
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

/** Consecutive tools share a compact disclosure; prose and questions stay in time order. While a call runs the closed run has the working mark; open, its glyph turns `accent` and the call's row has it. Memoised on its calls, which a streamed delta elsewhere leaves alone. */
const ToolRun = memo(function ToolRun({ identity, items, live, sessionId, approvals, arrival }: ToolRunProps) {
  const [open, setOpen] = useDisclosure(`tools:${items[0]?.agent_id ?? ''}:${identity}`);
  const [opened, setOpened] = useState(open);
  const failed = items.some((item) => item.tool?.status === 'failed');
  const active = live && items.some((item) => isActive(item.tool?.status));
  return (
    <div data-tool-run="">
      <button data-history-anchor={`tools-${identity}`} data-history-items={JSON.stringify(items.map(item => item.id))} type="button" aria-expanded={open} className={cn('flex min-h-7 w-fit max-w-full items-center gap-2 rounded-full bg-tint-well pr-3 pl-2 text-left text-ui text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11', failed && 'text-error')} onClick={() => { setOpened(true); setOpen((o) => !o); }}>
        {active && !open ? <WorkingMark /> : <Terminal aria-hidden="true" className={cn('size-4 shrink-0', active && 'text-accent')} />}
        <span>{summarizeTools(items, live)}</span>
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      {!open && live && items.map((item) => item.tool?.progress && <div key={item.id}>{toolProgress(item.tool, true)}</div>)}
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

function toolProgress(tool: Item['tool'], named = false) {
  if (!tool?.progress || !isActive(tool.status)) return null;
  return <p role="status" aria-label="Tool progress" className="ml-6 break-words whitespace-pre-wrap text-caption text-muted">{named && `${toolLabel(tool).name}: `}{tool.progress}</p>;
}

/** A call's state as a glyph: the spinner while it is open, a check, a cross, or a dash for a call that never reported. */
export function ToolMark({ tone }: Readonly<{ tone: string }>) {
  if (tone === 'running' || tone === 'pending') return <Spinner />;
  if (tone === 'completed' || tone === 'decided') return <Check aria-hidden="true" className="size-3.5 text-success" strokeWidth={2.5} />;
  if (tone === 'failed') return <X aria-hidden="true" className="size-3.5 text-error" strokeWidth={2.5} />;
  return <Minus aria-hidden="true" className="size-3.5 text-faint" strokeWidth={2.5} />;
}

/**
 * A tool call's details: its text, then the input and output as code blocks; "No details yet."
 * with none of them. The input reads as its command, or decoded; "Raw" shows it as recorded.
 */
export function ToolDetails({ item, className }: Readonly<{ item: Item; className?: string }>) {
  const t = item.tool;
  const [raw, setRaw] = useState(false);
  const input = t?.input ? readableInput(t.name, t.input) : null;
  const rawToggle = input && input.text !== t?.input && (
    <>
      <span className="flex-1" />
      <button type="button" aria-pressed={raw} className={cn('rounded-xs font-mono transition-colors duration-100 hover:text-body', raw && 'text-body')} onClick={() => setRaw((r) => !r)}>
        Raw
      </button>
    </>
  );
  return (
    <div className={cn('my-1 flex flex-col gap-1 text-ui', className)}>
      {item.text && <Markdown text={item.text} />}
      {t?.input && input && <CodeBlock language={raw ? 'input' : input.kind} head={rawToggle || undefined}>{raw ? t.input : input.text}</CodeBlock>}
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
 * and its approval when a request named it; expands to the full input and output, and while
 * it runs its newest output lines (`tail`; the live step shows them under the row instead). The
 * images its result returned sit under the row, visible without expanding it.
 */
export const ToolRow = memo(function ToolRow({ item, live, sessionId, approvals, className, tail = true }: { item: Item; live: boolean; sessionId?: string; approvals?: Interaction[]; className?: string; tail?: boolean }) {
  const [open, setOpen] = useDisclosure(`tool:${item.agent_id ?? ''}:${item.id}`);
  // The details (code blocks) are mounted on the first open only.
  const [opened, setOpened] = useState(open);
  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const [, copy] = useCopied();
  const { item: fullItem, body, attach, retry } = useItemBody(item, open);
  const t = item.tool;
  // The input copies as its details read it: the command, or the input decoded.
  const inputCopy = useBodyCopy(item, (input) => copy(readableInput(t?.name ?? '', input).text));
  const outputCopy = useBodyCopy(item, copy);
  const copyError = inputCopy.copyError || outputCopy.copyError;
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
    { key: 'cmd', label: toolKind(name) === 'command' ? 'Copy command' : 'Copy input', icon: <Copy />, disabled: !t?.input && !t?.has_input, onSelect: () => inputCopy.copyBody('input'), separator: true },
    { key: 'out', label: 'Copy output', icon: <Copy />, disabled: !t?.output && !t?.has_output, onSelect: () => outputCopy.copyBody('output') },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/tool relative', className)} />}>
        <div ref={attach} id={`item-${item.id}`} className={cn('rounded-sm', tone === 'failed' && 'text-error')}>
          <div className="flex items-center gap-1">
            <button
              type="button"
              aria-expanded={open}
              className={cn('flex h-6 min-w-0 flex-1 items-center gap-2 rounded-full pl-1.5 text-left font-mono text-code-sm text-muted transition-colors hover:bg-tint-well pointer-coarse:min-h-11 pr-8 pointer-coarse:pr-11', tone === 'running' && 'text-body', tone === 'failed' && 'text-error')}
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
          </div>
          {t?.edit_event_id && <NativeEdits item={item} />}
          {live && toolProgress(t)}
          {opened && (
            <Collapse open={open} appear>
              <div className="ml-6"><BodyNotice body={body} retry={retry} />{fullItem && <ToolDetails item={fullItem} />}{tail && live && isActive(status) && <LiveOutput lines={t?.tail} className="mb-1" />}</div>
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

const EDIT_UNAVAILABLE: Record<string, string> = {
  missing: 'This edit has no recorded native patch.',
  binary: 'A textual diff is unavailable for this binary edit.',
  too_large: 'This recorded diff exceeds the display limit.',
  unsupported: 'The native detail is not a complete supported textual diff.',
};
function NativeEdits({ item }: Readonly<{ item: Item }>) {
  return <div className="ml-6">
    {item.tool!.file_edits?.map((edit, index) => <NativeEdit key={`${item.tool!.edit_event_id}:${index}:${edit.path}`} item={item} edit={edit} />)}
    {item.tool!.file_edits_truncated && <p className="text-caption text-muted">Some committed file edits exceed the metadata limit.</p>}
  </div>;
}
function NativeEdit({ item, edit }: Readonly<{ item: Item; edit: NativeFileEdit }>) {
  const event = item.tool!.edit_event_id!;
  const [open, setOpen] = useDisclosure(JSON.stringify(['edit', item.agent_id ?? '', item.id, event, edit.path]));
  const known = edit.diff_status === 'available';
  const { state, visible, retry } = useEditDiff(item, event, edit.path, open && known);
  const label = `${edit.path} ${known ? `+${edit.additions ?? 0} −${edit.deletions ?? 0}` : 'counts unavailable'}`;
  return <div className="mb-1">
    <button type="button" aria-expanded={open} onClick={() => setOpen(value => !value)} className="flex min-h-6 w-full items-center gap-2 rounded-sm px-1 text-left font-mono text-code-sm text-muted hover:bg-tint-well pointer-coarse:min-h-11" title={edit.path}>
      <ChevronRight className={cn('size-3 shrink-0', open && 'rotate-90')} />
      <span className="min-w-0 flex-1 truncate">{edit.path}</span>
      {known ? <span className="shrink-0"><span className="text-success">+{edit.additions ?? 0}</span> <span className="text-error">−{edit.deletions ?? 0}</span></span> : <span className="shrink-0 font-sans text-meta">counts unavailable</span>}
      <span className="sr-only">{edit.kind} diff</span>
    </button>
    {open && !known && <p role="status" className="p-2 text-caption text-muted">{EDIT_UNAVAILABLE[edit.diff_status] ?? 'This edit has no supported recorded diff.'}</p>}
    {visible && <div role="region" aria-label={`Diff for ${label}`} className="overflow-x-auto rounded-sm">
      {state?.status === 'loaded' ? state.data.status === 'available' && state.data.patch ? <Suspense fallback={<p role="status" className="p-2 text-caption text-muted">Loading the diff viewer…</p>}><InlinePatch path={edit.path} text={state.data.patch} /></Suspense> : <p role="status" className="p-2 text-caption text-muted">{EDIT_UNAVAILABLE[state.data.status] ?? 'This edit has no supported recorded diff.'}</p> : state?.status === 'loading' ? <p role="status" className="p-2 text-caption text-muted">Loading the recorded diff…</p> : <p role={state?.status === 'error' ? 'alert' : 'status'} className="p-2 text-caption text-muted">{state?.status === 'error' ? state.error : 'The diff is no longer cached.'} <button type="button" onClick={retry} className="underline">Retry</button></p>}
    </div>}
  </div>;
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
 * without the call's row, which is in the turn's activity: a chart, or the
 * images its result returned. Memoised on the call, which a streamed delta elsewhere leaves alone.
 */
const CallProduct = memo(function CallProduct({ item, sessionId, className }: { item: Item; sessionId?: string; className?: string }) {
  return (
    <div data-history-anchor={item.id} className={className}>
      {isChartCall(item) && sessionId ? <Suspense fallback={<Skeleton label="Loading the chart…" rows={3} className="rounded-lg bg-raised p-4 shadow-raised" />}><ChartCard sessionId={sessionId} callId={item.id} /></Suspense> : <ToolImages item={item} sessionId={sessionId} />}
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
      <ContextMenu.Trigger render={<section id={`item-${id}`} aria-label="Question" className={cn('rounded-md text-ui shadow-raised', pending ? 'flex flex-col gap-1.5 bg-raised px-3.5 py-3' : 'bg-tint-well', className)} />}>
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
    case 'cancelled':
      return { answer: 'Cancelled', tone: 'text-muted' };
    case 'failed':
      return { answer: asked.error ? `Failed: ${asked.error}` : 'Failed', tone: 'text-error' };
    case 'answered':
      break;
    default:
      return { answer: 'Not answered', tone: 'text-muted' };
  }
  if (asked.link) return { answer: 'Done', tone: 'text-muted' };
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
export const Turn = memo(function Turn({ item, sessionId, planVersion, planAvailable, streaming, endedAt, end, replyActions, className, stamps }: { item: Item; sessionId?: string; planVersion?: number; planAvailable?: boolean; streaming: boolean; endedAt?: string; /** Set when this reply ends its turn: it gets the foot with the end time and the turn's tokens. */ end?: ReplyEnd; /** The reply's receipts, when it ends its turn. */ stamps?: Stamp[]; replyActions?: ReplyActions; className?: string }) {
  switch (item.kind) {
    case 'user':
      return <UserBubble item={item} sessionId={sessionId} className={className} />;
    case 'assistant':
    case 'notice':
      if (item.kind === 'notice' && item.plan) return <PlanNotice item={item} sessionId={sessionId} planVersion={planVersion} draftAvailable={planAvailable} className={className} />;
      if (item.clipped) return <ClippedMessage item={item} streaming={streaming} className={className} end={end} replyActions={replyActions} />;
      return item.kind === 'assistant' ? assistantMessage({ item, text: item.text ?? '', streaming, end, replyActions, className, stamps }) : noticeRow({ item, text: item.text ?? '', className });
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
 * A reasoning item: one 24px row, a quiet ring and "Thinking…" while it streams and "Thought for
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
        {streaming ? <WorkingMark className="size-3" /> : <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', expanded && 'rotate-90')} />}
        <span className="tabular-nums">{thinkingLabel(streaming, took)}</span>
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
