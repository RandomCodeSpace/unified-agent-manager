import { BodyNotice, DetailVisibility, useDetailVisibility, useBodyCopy, useDisclosure, useItemBody, useEditDiff, useWholeText, type WholeText } from './Details';
import { BookOpen, Brain, Check, ChevronDown, ChevronRight, ChevronUp, CircleDashed, Copy, Ellipsis, FileDiff, FilePen, FilePlus, Files, FileText, FolderSearch, GitBranch, Globe, Image as ImageIcon, MessageCircleQuestion, Minus, Pencil, Plug, RotateCcw, Search, ShieldQuestionMark, Signpost, Terminal, Workflow, X, type LucideIcon } from 'lucide-react';
import { Fragment, Suspense, lazy, memo, useCallback, useContext, useEffect, useId, useMemo, useRef, useState, type HTMLAttributes, type ReactNode, type Ref, type RefObject } from 'react';
import type { Interaction, Item, NativeFileEdit, Subagent, TodoCounts, ToolStatus, TurnTiming } from '../api';
import { useApi } from '../ApiContext';
import { todoTally } from '../lib/todos';
import { isChartCall } from '../lib/chart';
import { localHints } from '../lib/fileReferences';
import { usePreview } from '../lib/previewContext';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { compactTokens } from '../lib/cost';
import { approvalMark, askedOn, callProduct, callWords, changedFiles, editCounts, firstLine, foldLabel, scratchPlan, currentStep, duration, foregroundItems, formatMs, itemTook, completedDuration, localToolMs, promoted, stepResult, summarizeTurn, timelineRows, timingForTurn, turnElapsed, showTurnEnd, linkInteractions, mergeByTime, questionOf, readableInput, toolKind, toolLabel, type AskedQuestion, type Entry, type StepIcon, type StepWords, type TimelineRow, type TurnSummary, type WordTone } from '../lib/transcript';
import { groupIdentities } from '../lib/historyState';
import { parentMap, replyIndex, type IdentityTone } from '../lib/subagents';
import { receipts, type Stamp } from '../lib/receipts';
import { gitLabCandidates, type GitLabOrigin } from '../lib/gitlabRefs';
import { GitLabItemsContext, TurnStrip } from './GitLabStrip';
import { Receipts } from './Receipts';
import { AsideChip, useTurnHasAsides } from './AskAside';
import { ImageThumbs, ItemAttachments } from './Attachments';
import { CodeBlock, ItemContext, Markdown, PlainText, SessionContext, Skeleton, StepDots, WorkdirContext, clockTime, dateTime } from './common';
import { APPROVAL_ICONS } from './Interactions';
import { LiveOutput } from './LiveOutput';
import { LiveSubagents, SubagentChip, useLiveSubagentIds, useSubagentReplies } from './Subagents';
import { todoBlocks } from './StatusLine';
import { TurnTodo } from './Todos';
import { TurnWaybill } from './TurnWaybill';
import { PlanNotice } from './Plan';
import { GitLabReviewRequest, gitLabReviewRequest } from './GitLabReviewRequest';
import { Blocks, type Block } from './ui/blocks';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Collapse, usePresence } from './ui/collapse';
import { ContextMenu, Menu, useLazyContextMenu, type ActionItem } from './ui/menu';
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
  /** The window ends at the live tail: the live subagent rows sit at its foot. */
  liveCard?: boolean;
  /** Open the Changes sheet on a turn's edits, from its "Changed n files" line; `latest` when it is the Task's latest turn. */
  onOpenChanges?: (turn: { files: string[]; latest: boolean }) => void;
  /** Keep a compact turn's "Changed n files" line; off where the Task's folder has no Changes view (no Git). */
  changedLine?: boolean;
  /** The full current workspace Changes view, separate from historical native facts. */
  onOpenThreadChanges?: () => void;
  readersActive?: boolean;
  readerGeneration?: string;
  /** The conversation is being compacted: the foot shows no step (the strip says so). */
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
 * is one flat assistant turn: a head row opens the timeline of thinking and tool calls,
 * while prose and answered questions stay in the answer. A decided request without a
 * tool row joins the timeline at its time. The subagents a reply spawned
 * are a chip on its turn line, and plain rows at the foot while one runs;
 * the chip and each row open the strip's sheet.
 */
export const Transcript = memo(function Transcript({ sessionId, planVersion, planAvailable, planPath, agentId, items, identityItems = items, liveItems = items, historyItemSeq, turnTimings = [], interactions, subagents, live, working, connected = true, workdir, onOpenChanges, onOpenThreadChanges, readersActive = true, readerGeneration = '', changedLine = true, compacting = false, liveCard = false, onBranch, onRewind, onEdit }: Readonly<Props>) {
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
  const { linked, loose, questions } = linkInteractions(items, interactions, agentId);
  const groupItems = useIdentityEntries(identityItems, loose, questions);
  const turnIds = useGroupIdentities(groupItems);
  const replyActions = useMemo(() => ({ threadChanges: onOpenThreadChanges, branch: agentId ? undefined : onBranch, rewind: agentId ? undefined : onRewind, edit: agentId ? undefined : onEdit, active: readersActive, generation: readerGeneration }), [onOpenThreadChanges, agentId, onBranch, onRewind, onEdit, readersActive, readerGeneration]);
  const stamps = new Map<string, Stamp[]>();
  const gitlab = useContext(GitLabItemsContext);
  const ctx: RenderContext = { replyActions, sessionId, planVersion, planAvailable, live, streamingId: working ? liveItems.at(-1)?.id : undefined, thoughtEnd: thoughtEnds(items), replyEnd: replyEnds(identityItems, turnTimings, working), receipts: stamps, arrival, approvals: linked, subagentOf: (item) => (inline ? byParent.get(item.id) : undefined) };
  const scratch = scratchPlan(planPath, workdir);
  // What a call produced for the person stands in the answer while the call folds like any
  // other: a chart or the images its result returned.
  const product = (item: Item) => {
    const kind = callProduct(item);
    return kind ? <CallProduct key={`${item.agent_id ?? 'main'}:${item.id}`} item={item} sessionId={sessionId} className={arrival(item.id)} /> : null;
  };
  const own = (item: Item) => callProduct(item) === 'chart';

  const foreground = new Set(foregroundItems(items).map((item) => item.id));
  // A turn's asides chip sits in its reply's foot once it ended with one, else in its message's foot.
  const replied = new Set(ctx.replyEnd ? [...ctx.replyEnd.values()].map((end) => end.turn) : []);
  const out: ReactNode[] = [];
  let group: Entry[] = [];
  let showedWorking = false;
  const firstIndex = identityItems.findIndex(item => item.id === items[0]?.id);
  let userItemId: string | undefined = identityItems.slice(0, Math.max(0, firstIndex)).reverse().find(item => item.kind === 'user' && !item.delivery)?.id;
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
    let refs: Item[] | undefined;
    // The turn's last reply, once the turn is over, gets its receipts from the turn's calls (the main agent's, held here).
    if (!agentId && !open) {
      const reply = group.map((e) => e.item).filter((i): i is Item => !!i && !i.agent_id && i.kind === 'assistant').at(-1);
      if (reply?.text && ctx.replyEnd?.has(reply.id)) stamps.set(reply.id, stampsFor(reply, group.flatMap((e) => (e.item ? [e.item] : []))));
      // And the GitLab items the turn touched, when the Thread's project is a signed-in GitLab one.
      if (reply && gitlab && ctx.replyEnd?.has(reply.id)) refs = gitLabCallsFor(reply, group.flatMap((e) => (e.item ? [e.item] : [])), gitlab.origin);
    }
    const groupLive = live && group.some((entry) => entry.item && foreground.has(entry.item.id) && begunSince(entry));
    const gctx = { ...ctx, live: groupLive, streamingId: groupLive ? ctx.streamingId : undefined };
    if (last) footLive = groupLive;
    const runs = last && working && open;
    if (runs) showedWorking = true;
    // One status row heads the turn and keeps its slot while it runs, so "Took 12s" lands there
    // at the end and streamed content lands below it: nothing on screen moves at either moment.
    // Compact: the head row carries the turn's counts and opens its timeline; only promoted
    // entries stand in the answer, and a steer bubble sits in the turn at its place.
    const summary = summarizeTurn(group, { live: groupLive, streamingId: gctx.streamingId, approvals: linked, scratch });
    const changed = changedFiles(group, scratch);
    // What the turn left sits in its card's head: the reply that ends it brings copy, the turn's
    // menu and its outcome chips; a turn without one (still working, or ended without a reply) has its edits' chip alone.
    const lastReply = group.map((e) => e.item).filter((i): i is Item => !!i && !i.agent_id && i.kind === 'assistant').at(-1);
    const replyEnd = lastReply && ctx.replyEnd?.get(lastReply.id);
    const edits = changedLine && changed.length > 0 ? turnChangeFor(lastReply ?? group[0].item, changed, last && liveCard, onOpenChanges, editCounts(group, scratch)) : undefined;
    const first = (group[0].item ?? group[0].interaction).id;
    const id = (first && turnIds.get(first)) ?? userItemId ?? 'start';
    const reply = inline ? replies?.byKey.get(userItemId ?? 'start') : undefined;
    const spawned = reply?.subagents ?? NO_SUBAGENTS;
    // The live rows show every one of them under their own chip: its chip waits until they leave. Every call has spawned one
    // they show (a nested subagent's call may or may not be among the reply's: it is in its parent's transcript).
    const covered = !!(liveCard && liveIds && reply && spawned.every((s) => liveIds.has(s.id)) && reply.calls.every((call) => spawned.some((s) => s.parent_tool_call_id === call)));
    out.push(
      <div key={`turn-${id}`} data-reply={userItemId ?? 'start'} className="flex flex-col gap-3">
        {runs || showEnd || summary.count > 0 || spawned.length > 0 || replyEnd || edits ? <TurnHead id={id} agentId={agentId} working={runs} timing={timing} at={timing?.ended_at ?? replyEnd?.at} summary={summary} entries={group} ctx={gctx} subagents={spawned} calls={reply?.calls.length} chip={!covered} replyKey={userItemId ?? 'start'} tones={tones} reply={replyEnd ? lastReply : undefined} end={replyEnd} edits={edits} gitlab={refs} /> : null}
        {renderCompact(group, gctx, product, own)}
      </div>,
    );
    group = [];
  };
  mergeByTime(items, [...loose, ...questions]).forEach((entry) => {
    if (entry.item?.kind !== 'user' || entry.item.delivery) {
      group.push(entry);
      return;
    }
    flush();
    userItemId = entry.item.id;
    ownMessage = true;
    out.push(<UserBubble key={entry.item.id} item={entry.item} sessionId={sessionId} className={arrival(entry.item.id)} asideTurn={!agentId && !replied.has(entry.item.id) ? entry.item.id : undefined} />);
  });
  flush(true);
  // Compact draws no live activity row, so the foot shows the current step itself, unfolded.
  const step = working && !compacting ? currentStep(items, { live: footLive, streamingId: footLive ? ctx.streamingId : undefined, approvals: linked }, own) : null;
  // A streaming thought is the strip's "Thinking" and its peek: the foot shows only a running call.
  const current = step?.item?.kind === 'tool' && <LiveStep key={step.item.id} item={step.item} live={live} sessionId={sessionId} approvals={linked.get(step.item.id)} />;
  return (
    <SessionContext.Provider value={sessionId}>
      <WorkdirContext.Provider value={workdir}>
        {out}
        {working && !showedWorking && <TurnStatus working timing={turnTimings.at(-1)?.state === 'working' ? turnTimings.at(-1) : undefined} />}
        {inline && liveCard && <LiveSubagents />}
        <WorkingTail working={working} turnId={userItemId ?? 'start'} current={current} />
      </WorkdirContext.Provider>
    </SessionContext.Provider>
  );
});

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
      out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} planVersion={ctx.planVersion} planAvailable={ctx.planAvailable} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} stamps={ctx.receipts?.get(item.id)} className={ctx.arrival(item.id)} />);
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

/** The turn line (DESIGN.md turn card): its head on top, its steps under the head while open; flat on the page, no panel. */
const CARD = 'flex flex-col';
/** The card's head row: the turn's facts, then its counts at the end; it wraps on a narrow screen. */
const HEAD = 'flex min-h-9 flex-wrap items-center gap-x-2 gap-y-0.5 py-1.5 text-ui tabular-nums text-muted';

/** The tip on a turn's duration slot: whether the time shown was recorded. */
const durationTitle = (elapsed: string | null) => (elapsed ? 'Recorded foreground turn duration' : 'Turn duration was not recorded.');

/** `nodes` without the empty ones, a faint dot between each. */
function dotted(nodes: ReactNode[]): ReactNode[] {
  return nodes.filter(Boolean).flatMap((node, i) => [...(i ? [<span key={`dot-${i}`} aria-hidden="true" className="text-faint">·</span>] : []), <Fragment key={i}>{node}</Fragment>]);
}

const SPLIT_TITLE = "Model: the model calls' own time, subagents' included. Local: the turn's own tool calls running, overlaps counted once. Waiting on you: approvals and answers, which the turn's time leaves out.";

/** The ended turn's time split, "model 41s · local 3s · waiting on you 5m", each part only when recorded. */
function timeSplit(timing: TurnTiming, entries: Entry[]): string {
  const paused = timing.paused_ms ?? 0;
  const local = localToolMs(entries, paused);
  return [
    timing.generation_ms ? `model ${formatMs(timing.generation_ms)}` : '',
    local > 0 ? `local ${formatMs(local)}` : '',
    paused > 0 ? `waiting on you ${formatMs(paused)}` : '',
  ].filter(Boolean).join(' · ');
}

/** The running turn's foreground time, waits for the user left out; only these digits re-render each second. */
function LiveClock({ timing }: Readonly<{ timing?: TurnTiming }>) {
  const [now, setNow] = useState(Date.now);
  const ticking = timing?.state === 'working' && !timing.paused_at;
  useEffect(() => {
    if (!ticking) return;
    const tick = () => setNow(Date.now());
    const first = window.setTimeout(tick, 0);
    const timer = window.setInterval(tick, 1000);
    return () => {
      window.clearTimeout(first);
      window.clearInterval(timer);
    };
  }, [ticking]);
  const elapsed = timing ? turnElapsed(timing, now) : null;
  return elapsed ? <span className="font-medium text-ink">{elapsed}</span> : null;
}

/**
 * The head's facts before its counts. Ended: when it ended, "Took 28m" with the time split in
 * `faint` parentheses, then the turn's tokens and generation speed, a button to its waybill.
 * Running: "Working 8m" and the tokens and speed so far, which stay live.
 */
function turnFacts({ working, timing, at, entries }: Readonly<{ working: boolean; timing?: TurnTiming; at?: string; entries: Entry[] }>): ReactNode[] {
  const tokens = timing?.output_tokens ? <TurnTokens timing={timing} live={working} /> : null;
  if (working) return [<span key="working" className="whitespace-nowrap"><span className="font-medium text-ink">Working</span> <LiveClock timing={timing} /></span>, tokens];
  const elapsed = completedDuration(timing);
  const split = elapsed && timing ? timeSplit(timing, entries) : '';
  return [
    at && <time dateTime={at} title={dateTime(at)} className="font-medium whitespace-nowrap text-ink">{clockTime(at)}</time>,
    elapsed && (
      <span title={durationTitle(elapsed)}>
        <span className="whitespace-nowrap">Took <span className="font-medium text-ink">{elapsed}</span></span>
        {split && <span className="text-faint" title={SPLIT_TITLE}> ({split})</span>}
      </span>
    ),
    tokens,
  ];
}

/**
 * The head of a turn's card (DESIGN.md turn card), in the turn status row's slot: the turn's facts
 * (`turnFacts`), its failures in `error`, then at its end the counts, "1 thought · 2 commands ·
 * 1 question answered ›", updating in place as items append. With anything counted the counts are
 * a button that opens the turn's steps in the card under the head, mounted on the first open only;
 * they turn `attention` while a call waits for the user. The running step is the live step at the
 * transcript's foot: the head never names it.
 */
const TurnHead = memo(function TurnHead({ id, agentId, working, timing, at, summary, entries, ctx, subagents = NO_SUBAGENTS, calls = subagents.length, chip = true, replyKey, tones = NO_TONES, reply, end, edits, gitlab }: Readonly<{ /** The reply that ends the turn: the head copies it. */ reply?: Item; /** The turn's end, once a reply ends it: its outcome and menu. */ end?: ReplyEnd; /** The files the turn's edit calls named. */ edits?: TurnChange; /** The turn's items that can name GitLab items. */ gitlab?: Item[]; id: string; agentId?: string; working: boolean; timing?: TurnTiming; /** When the turn ended: its recorded end, else its last reply's time. */ at?: string; summary: TurnSummary; entries: Entry[]; ctx: RenderContext; /** The subagents the reply spawned: a chip beside the counts that opens their list in the strip's sheet. */ subagents?: Subagent[]; /** The reply's `task` calls, loaded or not. */ calls?: number; /** False while the live rows at the foot show the same subagents under their own chip. */ chip?: boolean; /** The reply's key (its message's id, "start" before any): the chip opens the sheet's list filtered to it. */ replyKey?: string; tones?: ReadonlyMap<string, IdentityTone> }>) {
  // Item IDs are local to their agent; main-turn identities stay unchanged.
  const scope = agentId ? 'agent-turn' : 'turn';
  const scopedId = agentId ? encodeURIComponent(JSON.stringify([agentId, id])) : id;
  const [open, setOpen] = useDisclosure(`${scope}:${scopedId}`);
  const [opened, setOpened] = useState(open);
  const card = useRef<HTMLDivElement>(null);
  const outcome = useTurnOutcome({ reply, end, edits, gitlab, replyActions: ctx.replyActions, anchor: card });
  const failures = summary.parts.filter((p) => p.tone === 'error');
  // The change chip states the files changed, with their lines: the counts leave them out then, unless nothing else would open the steps.
  const others = summary.parts.filter((p) => p.tone !== 'error' && !(outcome.changed && p.kind === 'changed'));
  const counts = others.length ? others : summary.parts.filter((p) => p.tone !== 'error');
  const text = summary.parts.filter((p) => counts.includes(p) || p.tone === 'error').map((p) => p.text).join(' · ');
  const domId = `${scope}-${scopedId}`;
  const timelineId = `${domId}-timeline`;
  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const headButton = useRef<HTMLButtonElement>(null);
  // The foot of open steps folds them too: the head comes back into view, so a long list
  // never has to be scrolled back up to close, and focus returns to the counts.
  const collapse = () => {
    headButton.current?.scrollIntoView({ block: 'nearest' });
    headButton.current?.focus({ preventScroll: true });
    setOpen(false);
  };
  const facts = [...turnFacts({ working, timing, at, entries }), ...failures.map((p) => <span className="whitespace-nowrap text-error">{p.text}</span>)].filter(Boolean);
  return (
    <div ref={card} id={domId} className={CARD}>
      <div data-history-anchor={`turn-head-${id}`} data-history-items={JSON.stringify(entries.flatMap(entry => entry.item ? [entry.item.id] : []))} className={HEAD} title={working || summary.count ? undefined : durationTitle(completedDuration(timing))}>
        {dotted(facts)}
        {facts.length > 0 && counts.length + failures.length > 0 && <span aria-hidden="true" className="text-faint">·</span>}
        {counts.length + failures.length > 0 && (
          <button
            ref={headButton}
            type="button"
            aria-expanded={open}
            aria-controls={opened ? timelineId : undefined}
            title={text}
            className={cn("relative -mx-1.5 flex h-6 min-w-0 max-w-full items-center gap-1.5 rounded-sm px-1.5 text-left transition-colors duration-100 after:absolute after:inset-x-0 after:-inset-y-1 after:content-[''] hover:bg-tint-well hover:text-body pointer-coarse:after:-inset-y-2.5", summary.tone === 'attention' && 'text-attention')}
            onClick={toggle}
          >
            <span className="min-w-0 truncate">
              {counts.map((p, k) => (
                <Fragment key={k}>
                  {k > 0 && <span aria-hidden="true" className="text-faint"> · </span>}
                  {p.text}
                </Fragment>
              ))}
            </span>
            <span className="sr-only">{failures.map((p) => ` · ${p.text}`).join('')} activity of this turn</span>
            <ChevronRight aria-hidden="true" className={cn('size-3.5 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-z-90')} />
          </button>
        )}
        {subagents.length > 0 && chip && <SubagentChip subagents={subagents} calls={calls} tones={tones} reply={replyKey} />}
        {outcome.actions && <span className="ml-auto">{outcome.actions}</span>}
      </div>
      <div data-outcome="" className="-mt-0.5 flex flex-wrap items-center gap-x-3 gap-y-2 pb-2 empty:hidden">{outcome.chips}</div>
      {outcome.reader}
      {opened && (
        <Collapse open={open} appear>
          <div aria-hidden="true" className="fade-rule" />
          <DetailVisibility open={open}><Timeline id={timelineId} entries={entries} ctx={ctx} agentId={agentId} /></DetailVisibility>
          <button type="button" aria-controls={timelineId} className="mb-1.5 ml-1.5 flex h-6 w-fit items-center gap-1 rounded-sm px-1.5 text-caption text-muted transition-colors duration-100 hover:bg-tint-well hover:text-body pointer-coarse:min-h-11" onClick={collapse}>
            <ChevronUp aria-hidden="true" className="size-3" />
            Collapse
          </button>
        </Collapse>
      )}
    </div>
  );
}, (a, b) => {
  const same = (x: Entry, y: Entry) => (x.item ?? x.interaction) === (y.item ?? y.interaction);
  // Its counts and its steps follow from its entries and these context fields alone; `summary` is rebuilt every render.
  return a.id === b.id && a.agentId === b.agentId && a.working === b.working && a.timing === b.timing && a.at === b.at && a.subagents === b.subagents && a.calls === b.calls && a.chip === b.chip && a.replyKey === b.replyKey && a.tones === b.tones
    && a.ctx.live === b.ctx.live && a.ctx.sessionId === b.ctx.sessionId && a.ctx.replyActions === b.ctx.replyActions && a.ctx.approvals === b.ctx.approvals
    && streamingIn(a.entries, a.ctx.streamingId) === streamingIn(b.entries, b.ctx.streamingId) && a.entries.length === b.entries.length && a.entries.every((e, i) => same(e, b.entries[i]))
    && a.entries.every((e) => !e.item || a.ctx.subagentOf?.(e.item) === b.ctx.subagentOf?.(e.item))
    && a.reply === b.reply && a.end === b.end && a.edits === b.edits && a.gitlab === b.gitlab;
});

/** Each step kind's glyph (Lucide, the app's 1.5 stroke). */
const STEP_ICONS: Record<StepIcon, LucideIcon> = { thought: Brain, skill: BookOpen, read: FileText, image: ImageIcon, search: Search, find: FolderSearch, fetch: Globe, tool: Plug, edit: FilePen, create: FilePlus, files: Files, intent: Signpost, command: Terminal, question: MessageCircleQuestion, subagent: Workflow };
const FOLD_ICONS: Record<NonNullable<StepWords['fold']>, LucideIcon> = { read: Files, search: Search, edit: FilePen, command: Terminal };
const WORD_TONE: Record<WordTone, string> = { muted: 'text-muted', error: 'text-error', attention: 'text-attention' };
const ICON_TONE: Record<WordTone, string> = { muted: 'text-muted', error: 'text-error', attention: 'text-attention' };

/** A step's grid (DESIGN.md steps): its clock, the rail's icon column, the step, its duration; what it opens spans the last two. A phone's clock drops the seconds. */
const STEP_GRID = 'grid grid-cols-[76px_14px_minmax(0,1fr)_auto] gap-x-2 max-sm:grid-cols-[56px_14px_minmax(0,1fr)_auto]';
/** One 24px line of a step (44px under a coarse pointer). */
const STEP_LINE = 'flex min-h-6 items-center pointer-coarse:min-h-11';

interface Edge { first: boolean; last: boolean }
/** The four edges as constants: a new object per row would defeat the steps' memo. */
const EDGES: Edge[] = [{ first: false, last: false }, { first: true, last: false }, { first: false, last: true }, { first: true, last: true }];
const edgeOf = (first: boolean, last: boolean) => EDGES[Number(first) + 2 * Number(last)];

/** The rail down the icon column: a 1px hairline from the first step's glyph to the last one's, dashed across a gap; the glyph sits on it. */
function Rail({ icon, edge, dashed = false, className }: Readonly<{ icon?: ReactNode; edge: Edge; dashed?: boolean; className?: string }>) {
  const line = dashed ? 'w-0 border-l border-dashed border-hairline-strong' : 'w-px bg-hairline-strong';
  return (
    <span aria-hidden="true" className="row-span-2 flex flex-col items-center">
      {icon ? (
        <>
          <span className={cn('mb-0.5 h-[3px] shrink-0 pointer-coarse:h-[13px]', !edge.first && line)} />
          <span className={cn('flex size-3.5 shrink-0 items-center justify-center [&_svg]:size-3.5', className)}>{icon}</span>
          <span className={cn('mt-0.5 flex-1', !edge.last && line)} />
        </>
      ) : (
        <span className={cn('flex-1', line)} />
      )}
    </span>
  );
}

/** One step on the rail: its clock (blank for an intent), glyph, label slot and duration, and under them what it opens. */
function StepFrame({ at, icon, iconClass, edge, dur, under, children, ref, ...rest }: Readonly<{ at?: string; icon: ReactNode; iconClass?: string; edge: Edge; dur?: string | null; under?: ReactNode; children: ReactNode; ref?: Ref<HTMLDivElement> } & Omit<HTMLAttributes<HTMLDivElement>, 'children'>>) {
  return (
    <div ref={ref} role="listitem" className={STEP_GRID} {...rest}>
      <span className={cn(STEP_LINE, 'text-caption whitespace-nowrap tabular-nums text-faint')} title={at ? dateTime(at) : undefined}>
        {at && <><span className="max-sm:hidden">{clockTime(at, true)}</span><span className="sm:hidden">{clockTime(at)}</span></>}
      </span>
      <Rail icon={icon} edge={edge} className={iconClass} />
      <div className="min-w-0">{children}</div>
      <span className={cn(STEP_LINE, 'justify-end text-caption whitespace-nowrap tabular-nums text-muted')}>{dur}</span>
      {under && <div className="col-span-2 col-start-3 min-w-0">{under}</div>}
    </div>
  );
}

/** A step's words as its button: the step, then its result words (`muted`, failures `error`, a person's decision `attention`); a phone puts the result under the step. */
function StepButton({ label, result, open, onClick, fold = false, intent = false, ink = false, lead, title, buttonRef, className }: Readonly<{ label: string; result: StepWords['result']; open: boolean; onClick: () => void; fold?: boolean; intent?: boolean; ink?: boolean; lead?: ReactNode; title?: string; buttonRef?: Ref<HTMLButtonElement>; className?: string }>) {
  return (
    <button ref={buttonRef} type="button" aria-expanded={open} title={title} onClick={onClick} className={cn("-mx-1.5 flex min-h-6 w-[calc(100%+0.75rem)] min-w-0 items-center gap-x-1 rounded-sm px-1.5 text-left text-caption text-body transition-colors duration-100 hover:bg-tint-well max-sm:flex-col max-sm:items-start max-sm:justify-center max-sm:py-0.5 pointer-coarse:min-h-11", className)}>
      <span className="flex max-w-full min-w-0 shrink-0 items-center gap-1.5">
        {lead}
        <span className={cn('min-w-0 truncate', intent && 'text-muted italic', ink && 'text-ink')}>{label}</span>
        {fold && <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-z-90')} />}
      </span>
      {result.length > 0 && (
        <span className="max-w-full min-w-0 truncate max-sm:text-meta">
          {result.map((r, i) => (
            <span key={i} className={WORD_TONE[r.tone]}>
              <span aria-hidden="true" className={cn('text-faint', i === 0 && 'max-sm:hidden')}>{i ? ' · ' : '· '}</span>
              <span className="sr-only">, </span>
              {r.text}
            </span>
          ))}
        </span>
      )}
    </button>
  );
}

/** A long text in a call's raw view: three lines, then "Show all N lines". */
function RawLines({ text: raw, className }: Readonly<{ text: string; className?: string }>) {
  const [all, setAll] = useState(false);
  const text = raw.replace(/^\n+|\s+$/g, '');
  const lines = text.split('\n').length;
  return (
    <>
      <pre translate="no" className={cn('m-0 font-mono text-code-sm leading-[18px] whitespace-pre-wrap text-body [overflow-wrap:anywhere]', !all && 'max-h-[54px] overflow-hidden', className)}>{text}</pre>
      {lines > 3 && (
        <button type="button" aria-expanded={all} className="flex h-[22px] w-fit items-center gap-1 rounded-xs text-caption text-accent hover:underline" onClick={() => setAll((a) => !a)}>
          <ChevronDown aria-hidden="true" className={cn('size-3', all && 'rotate-180')} />
          {all ? 'Show less' : `Show all ${lines} lines`}
        </button>
      )}
    </>
  );
}

/** A call's raw view: its command (or its input) and its output, each three lines until asked, with its exit code and Copy. */
function StepRaw({ item, onCopy }: Readonly<{ item: Item; onCopy?: () => void }>) {
  const t = item.tool;
  const input = t?.input ? readableInput(t.name, t.input) : null;
  const command = input?.kind === 'command';
  return (
    <div className="mt-0.5 mb-1.5 flex flex-col gap-0.5 rounded-md bg-sunken px-2 py-1.5">
      <div className="flex min-w-0 items-center gap-2 text-meta text-muted">
        <span className="min-w-0 flex-1 truncate font-mono" title={command ? input.text : undefined}>{command ? `$ ${input.text}` : t?.name}</span>
        {t?.exit_code !== undefined && <span className={cn(t.exit_code !== 0 && 'text-error')}>exit {t.exit_code}</span>}
        {onCopy && (t?.output || t?.input) && (
          <button type="button" className="flex items-center gap-1 rounded-xs hover:text-body" onClick={onCopy}>
            <Copy aria-hidden="true" className="size-3" />
            Copy
          </button>
        )}
      </div>
      {item.text && <Markdown text={item.text} className="text-caption" />}
      {input && !command && <RawLines text={input.text} className="text-muted" />}
      {t?.output && <RawLines text={t.output} />}
      {!item.text && !t?.input && !t?.output && <p className="text-caption text-muted">No details yet.</p>}
    </div>
  );
}

/** A call's row menu (right-click): expand, open its files in the preview, copy its command or input and its output. */
function useToolActions(item: Item, open: boolean, toggle: () => void, sessionId?: string) {
  const [, copy] = useCopied();
  const api = useApi();
  const preview = usePreview();
  const workdir = useContext(WorkdirContext);
  const anchor = useRef<HTMLButtonElement>(null);
  const t = item.tool;
  // The input copies as its details read it: the command, or the input decoded.
  const inputCopy = useBodyCopy(item, (input) => copy(readableInput(t?.name ?? '', input).text));
  const outputCopy = useBodyCopy(item, copy);
  const { name, arg } = toolLabel(t);
  // The files a local file tool read or wrote open in the preview, as their links in a reply do.
  const files = sessionId && preview && api.supports('files-v1') ? localHints(item, workdir ?? '').slice(0, 5) : [];
  const items: ActionItem[] = [
    { key: 'toggle', label: open ? 'Collapse' : 'Expand', icon: <ChevronRight />, onSelect: toggle },
    ...files.map((path, i): ActionItem => ({ key: `file:${path}`, label: `Open ${path.split('/').pop() || path}`, icon: <FileText />, separator: i === 0, onSelect: () => {
      const url = api.viewFileUrl(sessionId!, path);
      if (anchor.current) preview!({ url, name: path.split('/').pop() || path, description: path, path, frameable: true }, anchor.current);
    } })),
    { key: 'cmd', label: toolKind(name) === 'command' ? 'Copy command' : 'Copy input', icon: <Copy />, disabled: !t?.input && !t?.has_input, onSelect: () => inputCopy.copyBody('input'), separator: true },
    { key: 'out', label: 'Copy output', icon: <Copy />, disabled: !t?.output && !t?.has_output, onSelect: () => outputCopy.copyBody('output') },
  ];
  const copyRaw = () => (t?.output || t?.has_output ? outputCopy.copyBody('output') : inputCopy.copyBody('input'));
  return { items, anchor, label: arg ? `${name} ${arg}` : name, copyError: inputCopy.copyError || outputCopy.copyError, copyRaw };
}

/**
 * One call as a step (DESIGN.md steps): its glyph on the rail, "Ran go test ./..." and its result
 * words, its duration; while it runs the spinner and its newest output line. It opens onto its
 * raw command or input and output, its per-file edits and its images. `nested`: a call inside an
 * unfolded run, indented under the run's label, without a clock.
 */
const StepCall = memo(function StepCall({ item, live, sessionId, approvals, edge, nested = false }: { item: Item; live: boolean; sessionId?: string; approvals?: Interaction[]; edge: Edge; nested?: boolean }) {
  const [open, setOpen] = useDisclosure(`tool:${item.agent_id ?? ''}:${item.id}`);
  // What it opens is mounted on the first open only.
  const [opened, setOpened] = useState(open);
  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const { item: full, body, attach, retry } = useItemBody(item, open);
  const actions = useToolActions(item, open, toggle, sessionId);
  const context = useLazyContextMenu();
  const t = item.tool;
  const running = live && isActive(t?.status);
  const words = callWords(full ?? item, live, approvals);
  const Icon = STEP_ICONS[words.icon];
  const glyph = running ? <ToolMark tone="running" /> : <Icon aria-hidden="true" />;
  const took = running ? null : itemTook(item);
  const ended = !live && isActive(t?.status);
  const title = ended ? 'The turn ended before this tool reported a result' : undefined;
  const under = (
    <>
      {running && <LiveOutput lines={t?.tail?.slice(-1)} className="mb-1" />}
      {live && toolProgress(t)}
      {actions.copyError && <p role="alert" className="text-caption text-error">{actions.copyError}</p>}
      {opened && (
        <Collapse open={open} appear>
          <BodyNotice body={body} retry={retry} />
          {t?.edit_event_id && <NativeEdits item={item} />}
          {(item.images?.length || item.images_note) && <ToolImages item={item} sessionId={sessionId} className="mb-1.5" />}
          {full && <StepRaw item={full} onCopy={actions.copyRaw} />}
        </Collapse>
      )}
    </>
  );
  const button = <StepButton buttonRef={actions.anchor} className="pr-7 pointer-coarse:pr-11" label={words.label} result={words.result} open={open} onClick={toggle} intent={words.intent} ink={running} title={title} lead={nested ? <Icon aria-hidden="true" className={cn('size-3 shrink-0', words.tone === 'error' ? 'text-error' : 'text-faint')} /> : undefined} />;
  const menu = (
    <>
      <Menu.Root modal={false}>
        <Menu.Trigger render={<Button size="icon-sm" className="absolute top-0 right-0 text-muted opacity-0 transition-opacity group-hover/tool:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" aria-label={`Actions for ${actions.label}`} />}>
          <Ellipsis />
        </Menu.Trigger>
        <Menu.Content align="end" side="bottom">
          <Menu.Actions items={actions.items} />
        </Menu.Content>
      </Menu.Root>
      {context.menu(<ContextMenu.Actions items={actions.items} />)}
    </>
  );
  if (nested) {
    return (
      <div ref={attach} id={`item-${item.id}`} className="flex flex-col pl-3" {...context.shell}>
        <div className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-2">
          <div className="group/tool relative min-w-0">{button}{menu}</div>
          <span className={cn(STEP_LINE, 'justify-end text-caption whitespace-nowrap tabular-nums text-muted')}>{took}</span>
        </div>
        {under}
      </div>
    );
  }
  return (
    <StepFrame ref={attach} id={`item-${item.id}`} at={words.intent ? undefined : item.time} icon={glyph} iconClass={words.intent ? 'text-faint' : ICON_TONE[words.tone]} edge={edge} dur={took} under={under} {...context.shell}>
      <div className="group/tool relative">{button}{menu}</div>
    </StepFrame>
  );
});

/** A run of same-kind calls as one step, "Read 3 files ›", from its first call's start to its last one's end; it unfolds to the calls, indented, without a clock. */
function StepFold({ row, live, sessionId, approvals, edge, agentId }: Readonly<{ row: Extract<TimelineRow, { type: 'fold' }>; live: boolean; sessionId?: string; approvals: Map<string, Interaction[]>; edge: Edge; agentId?: string }>) {
  const [open, setOpen] = useDisclosure(`fold:${agentId ?? ''}:${row.key}`);
  const [opened, setOpened] = useState(open);
  const Icon = FOLD_ICONS[row.fold];
  const under = opened && (
    <Collapse open={open} appear>
      <div role="list" className="flex flex-col pb-0.5">
        {row.items.map((item) => <div key={item.id} role="listitem"><StepCall item={item} live={live} sessionId={sessionId} approvals={approvals.get(item.id)} edge={edge} nested /></div>)}
      </div>
    </Collapse>
  );
  return (
    <StepFrame at={row.items[0].time} icon={<Icon aria-hidden="true" />} iconClass="text-muted" edge={edge} dur={formatMs(row.end - row.start)} under={under}>
      <StepButton label={foldLabel(row.fold, row.items.length)} result={[]} open={open} fold onClick={() => { setOpened(true); setOpen((o) => !o); }} />
    </StepFrame>
  );
}

/** A thought as a step, "Thought · its first line", its duration; it opens onto its text (`muted`, behind a fading rule) with Copy thinking. */
function StepThought({ item, streaming, edge }: Readonly<{ item: Item; streaming: boolean; edge: Edge }>) {
  const [open, setOpen] = useDisclosure(`thought:${item.agent_id ?? ''}:${item.id}`);
  const [opened, setOpened] = useState(open);
  const { item: full, body, attach, retry } = useItemBody(item, open);
  const text = full?.text ?? '';
  const first = firstLine(item.text);
  const under = opened && (
    <Collapse open={open} appear>
      <BodyNotice body={body} retry={retry} />
      {full && <Copyable text={text} label="Copy thinking" className="mt-0.5 mb-1.5 pr-6 pl-3 text-caption text-muted before:absolute before:inset-y-0 before:left-0 before:w-px before:bg-hairline-strong before:content-['']">
        <Markdown text={text} className="md-quiet" streaming={streaming} />
      </Copyable>}
    </Collapse>
  );
  return (
    <StepFrame ref={attach} at={item.time} icon={<Brain aria-hidden="true" />} iconClass="text-muted" edge={edge} dur={streaming ? null : itemTook(item)} under={under}>
      <StepButton label={streaming ? 'Thinking…' : 'Thought'} result={first ? [{ text: first, tone: 'muted' }] : []} open={open} ink={streaming} onClick={() => { setOpened(true); setOpen((o) => !o); }} />
    </StepFrame>
  );
}

/** A decided request or settled question as a step: its title, its first line and the outcome word in `attention`, the full resolution in its tooltip. */
function StepDecided({ interaction, at = interaction.time, dur, edge }: Readonly<{ interaction: Interaction; at?: string; dur?: string | null; edge: Edge }>) {
  const { word, full, tone } = approvalMark(interaction);
  const Icon = interaction.kind === 'question' ? MessageCircleQuestion : tone === 'ok' ? ShieldQuestionMark : APPROVAL_ICONS[tone];
  const detail = interaction.detail?.split('\n').map((l) => l.trim()).find(Boolean);
  return (
    <StepFrame at={at} icon={<Icon aria-hidden="true" />} iconClass="text-attention" edge={edge} dur={dur} title={full}>
      <div className="flex min-h-6 min-w-0 items-center text-caption max-sm:flex-col max-sm:items-start pointer-coarse:min-h-11">
        <span className="min-w-0 truncate text-body">{interaction.title}</span>
        <span className="min-w-0 truncate pl-[0.3em] max-sm:pl-0">
          {detail && <span className="font-mono text-code-sm text-muted"><span className="text-faint">· </span>{detail} </span>}
          <span className="text-attention"><span className="text-faint">· </span>{word}</span>
        </span>
        <span className="sr-only">: {full}</span>
      </div>
    </StepFrame>
  );
}

/** The time between two steps on the rail, dashed: "· 41s model", or "· 1m 12s waiting on you" in `attention`; the cap's "N earlier steps · Show all N calls" in the same place. */
function StepGap({ children, you = false }: Readonly<{ children: ReactNode; you?: boolean }>) {
  return (
    <div role="listitem" className={STEP_GRID}>
      <span />
      <Rail edge={EDGES[0]} dashed />
      <span className={cn('col-span-2 flex min-h-[22px] items-center text-meta tabular-nums', you ? 'text-attention' : 'text-faint')}>{children}</span>
    </div>
  );
}

/**
 * A turn's steps, in order, in its card under the head (DESIGN.md steps): one clock-stamped
 * timeline, the same in the main thread and in a subagent's work. Each thought, call and
 * decided request is a row on the rail ([clock] [glyph] [step · result] [duration]); runs of one
 * kind fold; the time between steps over 30s shows on the rail; over 20 steps the first, "N earlier
 * steps · Show all N calls" and the last 12, every step one press away.
 */
function Timeline({ id, entries, ctx, agentId }: Readonly<{ id: string; entries: Entry[]; ctx: RenderContext; agentId?: string }>) {
  const [all, setAll] = useState(false);
  const list = useRef<HTMLDivElement>(null);
  const reveal = useRef(-1);
  useEffect(() => {
    if (!all || reveal.current < 0) return;
    list.current?.children[reveal.current]?.querySelector<HTMLElement>('button')?.focus();
    reveal.current = -1;
  }, [all]);
  const showAll = () => {
    // The first step the cap hid: the row after the first step and any gap after it.
    reveal.current = timelineRows(entries, { live: ctx.live, approvals: ctx.approvals, skip: (item) => !!ctx.subagentOf?.(item), all: true }).findIndex((r, i) => i > 0 && r.type !== 'gap');
    setAll(true);
  };
  const rows = timelineRows(entries, { live: ctx.live, approvals: ctx.approvals, skip: (item) => !!ctx.subagentOf?.(item), all });
  const out = rows.map((row, i) => {
    const edge = edgeOf(i === 0, i === rows.length - 1);
    switch (row.type) {
      case 'gap':
        return <StepGap key={row.key} you={row.you}>· {formatMs(row.ms)} {row.you ? 'waiting on you' : 'model'}</StepGap>;
      case 'more':
        return (
          <StepGap key={row.key}>
            <button type="button" className="-mx-1.5 rounded-sm px-1.5 text-left transition-colors duration-100 hover:bg-tint-well pointer-coarse:min-h-11" onClick={showAll}>
              {row.hidden} earlier steps · <span className="text-ink">Show all {row.calls} calls</span>
            </button>
          </StepGap>
        );
      case 'fold':
        return <StepFold key={row.key} row={row} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals} edge={edge} agentId={agentId} />;
    }
    const { entry } = row;
    if (entry.interaction) return <StepDecided key={row.key} interaction={entry.interaction} edge={edge} />;
    const item = entry.item;
    if (item.kind === 'reasoning') return <StepThought key={row.key} item={item} streaming={item.id === ctx.streamingId} edge={edge} />;
    // A settled question stands in the answer as its card; the step marks its place.
    const asked = askedOn(item, ctx.approvals, ctx.live);
    const question = asked && asked.outcome !== 'pending' ? ctx.approvals.get(item.id)?.find((ix) => ix.kind === 'question') : undefined;
    if (question) return <StepDecided key={row.key} interaction={question} at={item.time} dur={itemTook(item)} edge={edge} />;
    return <StepCall key={row.key} item={item} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals.get(item.id)} edge={edge} />;
  });
  return (
    <div ref={list} id={id} role="list" aria-label="Steps" className="flex max-w-[720px] flex-col pt-2 pb-1">
      {out}
    </div>
  );
}

/** A turn's edits for its Changed chip: the files its edit calls named, their line counts when every call recorded them, whether it is the Thread's latest turn, and the way to the Changes sheet. */
interface TurnChange { files: string[]; counts: { additions: number; deletions: number } | null; latest: boolean; onOpen?: (turn: { files: string[]; latest: boolean }) => void }

/** An unchanged turn's edits keep their identity, so its memoised reply holds while other rows stream. */
const turnChangeOf = new WeakMap<Item, TurnChange>();
function turnChangeFor(owner: Item | undefined, files: string[], latest: boolean, onOpen: TurnChange['onOpen'], counts: TurnChange['counts']): TurnChange {
  const held = owner && turnChangeOf.get(owner);
  if (held && held.latest === latest && held.onOpen === onOpen && held.files.join('\n') === files.join('\n') && held.counts?.additions === counts?.additions && held.counts?.deletions === counts?.deletions) return held;
  const change = { files, counts, latest, onOpen };
  if (owner) turnChangeOf.set(owner, change);
  return change;
}

/** A turn's todo counts as the strip's blocks; once the turn ended nothing works on the list, so in progress keeps its glyph without the accent. */
function turnTodoBlocks(counts: TodoCounts, stopped: boolean): Block[] {
  return todoBlocks(todoTally(counts)).map((block) => (stopped && block.key === 'in_progress' ? { ...block, tone: 'none' } : block));
}

/** The turn's one total change chip: "2 files" (with "+52 −3" when the counts are known), never a file's name. */
function ChangedChip({ files, additions, deletions, expanded, onClick }: Readonly<{ files: number; additions?: number; deletions?: number; expanded?: boolean; onClick?: (button: HTMLButtonElement) => void }>) {
  const label = `Changed ${files} ${files === 1 ? 'file' : 'files'}${additions !== undefined ? ` +${additions} −${deletions ?? 0}` : ''}`;
  const body = <>
    <FileDiff aria-hidden="true" className="size-3 shrink-0 text-muted" />
    {files} {files === 1 ? 'file' : 'files'}{additions !== undefined && <> <span className="text-success">+{additions}</span> <span className="text-error">−{deletions ?? 0}</span></>}
  </>;
  const chip = 'relative inline-flex h-6 items-center gap-1.5 rounded-sm bg-surface px-2 text-caption whitespace-nowrap tabular-nums text-ink';
  // Focused first (Safari does not focus a clicked button), so closing what it opens brings focus back here.
  return onClick ? (
    <button type="button" data-turn-changes="" aria-haspopup={expanded === undefined ? undefined : 'dialog'} aria-expanded={expanded} aria-label={label} title={label} className={cn(chip, "transition-colors duration-100 after:absolute after:inset-x-0 after:-inset-y-1 after:content-[''] hover:bg-tint-hover aria-expanded:bg-tint-selected pointer-coarse:after:-inset-y-2.5")} onClick={(e) => { e.currentTarget.focus(); onClick(e.currentTarget); }}>
      {body}
    </button>
  ) : (
    <span className={chip} title={label}><span className="sr-only">Changed </span>{body}</span>
  );
}

/** Copies a turn's reply from its card's head; a clipped reply is read whole first. */
function ReplyCopy({ reply }: Readonly<{ reply: Item }>) {
  const [copied, copy] = useCopied();
  const whole = useWholeText(reply);
  const run = () => { if (reply.clipped) void whole.read().then(copy, () => {}); else copy(reply.text ?? ''); };
  return (
    <Tip label={copied ? 'Copied' : 'Copy message'}>
      <Button size="icon-sm" variant="ghost" aria-label={copied ? 'Copied' : 'Copy message'} className={cn('text-faint transition-colors duration-100 hover:text-ink focus-visible:text-ink', copied && 'text-success')} onClick={run}>
        {copied ? <Check /> : <Copy />}
      </Button>
    </Tip>
  );
}

/**
 * What an ended turn left, in its card's head (DESIGN.md turn card): `actions` (copy the reply, the
 * turn's menu) at the head row's end, `chips` (the turn's total change, its todo list, the GitLab
 * items it touched, its asides) in a row under it. Nothing the turn did not produce shows: no
 * change, no chip. A turn still running or without a reply has only its change chip.
 */
function useTurnOutcome({ reply, end, edits, gitlab, replyActions, anchor }: Readonly<{ reply?: Item; end?: ReplyEnd; edits?: TurnChange; gitlab?: Item[]; replyActions?: ReplyActions; anchor: RefObject<HTMLDivElement | null> }>) {
  const task = useContext(SessionContext) ?? '';
  const asides = useTurnHasAsides(task, end?.turn);
  const visible = useDetailVisibility();
  const [changesReader, setChangesReader] = useState<{ generation: string; phone: boolean; open: boolean; anchor: HTMLElement | null } | null>(null);
  if (changesReader && (!visible || replyActions?.active === false || changesReader.generation !== (replyActions?.generation ?? ''))) setChangesReader(null);
  const timing = end?.timing;
  // Only captured changes with something in them make a chip; "unavailable" or none says nothing.
  const changes = timing?.changes && !(timing.changes.status === 'available' && !timing.changes.files && !timing.changes.omitted) ? timing.changes : undefined;
  const showChanges = () => setChangesReader({ generation: replyActions?.generation ?? '', phone: window.matchMedia('(max-width: 639px)').matches, open: true, anchor: anchor.current?.querySelector<HTMLButtonElement>('[data-turn-changes]') ?? anchor.current });
  const openEdits = edits?.onOpen && (() => edits.onOpen!({ files: edits.files, latest: edits.latest }));
  const at = (pick?: (userItemId: string, anchor: HTMLElement | null) => void) => (end?.userItemId && pick ? () => pick(end.userItemId, anchor.current?.querySelector<HTMLElement>('[data-fork-anchor]') ?? anchor.current) : undefined);
  const branch = at(replyActions?.branch);
  const rewind = at(replyActions?.rewind);
  const edit = end?.userItemId && replyActions?.edit ? () => replyActions.edit!(end.userItemId, anchor.current) : undefined;
  const menu: ActionItem[] = end ? [
    ...(changes ? [{ key: 'turn-changes', label: "This turn's changes", icon: <FileDiff />, onSelect: showChanges, takesFocus: true }] : []),
    // The chip opens the captured changes; the files the turn's calls named stay one step away in Changes.
    ...(changes && openEdits ? [{ key: 'turn-files', label: "This turn's files in Changes", icon: <FileDiff />, onSelect: openEdits }] : []),
    ...(edit ? [{ key: 'edit', label: 'Edit and resend…', icon: <Pencil />, takesFocus: true, onSelect: edit }] : []),
    ...(branch ? [{ key: 'branch', label: 'Branch from here', icon: <GitBranch />, takesFocus: true, onSelect: branch }] : []),
    ...(rewind ? [{ key: 'rewind', label: 'Rewind to before this prompt…', icon: <RotateCcw />, takesFocus: true, onSelect: rewind }] : []),
  ] : [];
  // The captured counts open the turn's changes; without them, the files its calls named (with their calls' own line counts when all recorded them) open Changes.
  const changeChip = changes?.status === 'available'
    ? <ChangedChip files={changes.files ?? 0} additions={changes.additions ?? 0} deletions={changes.deletions ?? 0} expanded={!!changesReader?.open} onClick={showChanges} />
    : edits ? <ChangedChip files={edits.files.length} additions={edits.counts?.additions} deletions={edits.counts?.deletions} onClick={openEdits} /> : null;
  // Its row shows only when something is in it; the GitLab chips may still be loading, so an empty row hides itself.
  const actions = end && (
    <span className="-mr-1.5 flex shrink-0 items-center gap-0.5">
      {reply?.text && <ReplyCopy reply={reply} />}
      {menu.length > 0 && <Menu.Root>
        <Menu.Trigger render={<Button data-fork-anchor="" variant="ghost" size="icon-sm" aria-label="Turn actions" className="text-faint hover:text-ink" />}><Ellipsis /></Menu.Trigger>
        <Menu.Content><Menu.Actions items={menu} /></Menu.Content>
      </Menu.Root>}
    </span>
  );
  const chips = (
    <>
      {changeChip}
      {timing?.todo?.total ? <TurnTodo timing={timing} blocks={turnTodoBlocks(timing.todo, timing.state !== 'working')} /> : null}
      {end && gitlab && <TurnStrip items={gitlab} />}
      {asides && <AsideChip task={task} turn={end!.turn} sep={false} />}
    </>
  );
  const reader = changesReader && timing && <Suspense fallback={null}><TurnChangesReader timing={timing} anchor={changesReader.anchor} phone={changesReader.phone} open={changesReader.open} onClose={() => setChangesReader(r => r && { ...r, open: false })} onClosed={() => setChangesReader(null)} onThreadChanges={replyActions?.threadChanges} /></Suspense>;
  return { actions, chips, reader, changed: !!changeChip };
}

/**
 * The foot of a running turn, where new output lands: the step in progress unfolded (`LiveStep`), and nothing else.
 * What happens now is said once, by the strip's live line above the composer (StatusLine), never here: no label,
 * no verb, no mark. It grows in when the turn starts and folds away when it ends.
 */
function WorkingTail({ working, turnId, current }: Readonly<{ working: boolean; turnId: string; /** Compact: the step in progress, unfolded (`LiveStep`). */ current?: ReactNode }>) {
  const { mounted, onClosed } = usePresence(working);
  // Once a turn showed a step unfolded, the foot keeps that room (a step row's height) until the turn ends:
  // a step folding into the turn line never pulls the transcript up, and the next one lands in the same room.
  const [roomFor, setRoomFor] = useState<string | null>(null);
  if (current && roomFor !== turnId) setRoomFor(turnId);
  if (!mounted) return null;
  return (
    <Collapse open={working} appear onClosed={onClosed} className="-mt-6" inner="pt-3">
      <div className={cn(roomFor === turnId && 'min-h-6 pointer-coarse:min-h-11')}>{current}</div>
    </Collapse>
  );
}

/**
 * Compact (DESIGN.md live step): the call in progress at the turn's foot, its tool row (name,
 * argument) alone; its output, and a streaming thought's text, are the strip's peek. Its mark is the
 * step dots (`StepDots`), not the ring, which only the strip's live line turns. It rises in; once the step ends it is gone from here and counted on the turn line.
 */
function LiveStep({ item, live, sessionId, approvals }: Readonly<{ item: Item; live: boolean; sessionId: string; approvals?: Interaction[] }>) {
  return (
    <div className="flex animate-rise flex-col gap-1">
      <ToolRow item={item} live={live} sessionId={sessionId} approvals={approvals} tail={false} step />
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
  /** The subagent a `task` call spawned, shown in the reply's list instead of its timeline. */
  subagentOf?: (item: Item) => Subagent | undefined;

}


/** Each reasoning item's recorded end. */
function thoughtEnds(items: Item[]): Map<string, string> {
  const m = new Map<string, string>();
  for (const item of items) if (item.kind === 'reasoning' && item.ended_at) m.set(item.id, item.ended_at);
  return m;
}

/** The streaming item's id when it is in `entries`; the row only cares about its own. */
const streamingIn = (entries: Entry[], id: string | undefined) => (id && entries.some((e) => e.item?.id === id) ? id : undefined);

/**
 * The head of a turn with nothing in it yet (DESIGN.md turn status), the card the turn head then
 * takes over in the same slot: "Working 3s" and its tokens while it starts, "Took 12s" once it
 * ended, "Took 3s · No reply" when it ended with no reply, thought or call. Without a recorded
 * duration an ended turn keeps its slot, blank, with no card.
 */
function TurnStatus({ working = false, timing, empty = false }: Readonly<{ working?: boolean; timing?: TurnTiming; /** The turn ended without a reply, a thought or a call: say so after its duration. */ empty?: boolean }>) {
  const elapsed = working ? null : completedDuration(timing);
  if (!working && !elapsed) return <div className="min-h-9" title={durationTitle(null)} />;
  return (
    <div className={CARD}>
      <div className={HEAD} title={working ? undefined : durationTitle(elapsed)}>
        {working ? dotted(turnFacts({ working, timing, entries: [] })) : <span className="animate-fade-in">Took {elapsed}{empty && ' · No reply'}</span>}
      </div>
    </div>
  );
}

interface ReplyActions { threadChanges?: () => void; branch?: BranchReply; rewind?: BranchReply; edit?: BranchReply; active: boolean; generation: string }

/** A hover copy button plus a right-click menu around any block of provider or user text; a reply that ends its turn has its copy in the turn card's head (DESIGN.md turn card). */
function Copyable({ text, read, label, className, side = 'right', at, foot = true, children, extra = [], asideTurn }: Readonly<{ /** The turn whose asides chip this foot carries (AskAside). */ asideTurn?: string; text: string; /** Reads the text to copy when `text` is only part of it. */ read?: () => Promise<string>; label: string; className?: string; /** Where the button sits: over the block's top-right corner, or outside it to the left (the user bubble, so it never covers the text). */ side?: 'right' | 'left'; /** When the message began: the user's clock time in the foot under the block, the full date in its tooltip. */ at?: string; /** Whether the block has a foot at all; without one, copying is in the right-click menu alone. */ foot?: boolean; children: ReactNode; extra?: ActionItem[] }>) {
  const [copied, copy] = useCopied();
  const task = useContext(SessionContext) ?? '';
  const asides = useTurnHasAsides(task, foot ? asideTurn : undefined);
  const context = useLazyContextMenu();
  const run = () => { if (read) void read().then(copy, () => {}); else copy(text); };
  const items: ActionItem[] = [{ key: 'copy', label, icon: <Copy />, onSelect: run }, ...extra];
  return (
    <div data-foot={foot ? '' : undefined} className={cn('group/copy relative flex flex-col', side === 'left' && 'items-end', foot && 'not-last:mb-3', className)} {...context.shell}>
      {children}
      {/* A message's foot: the copy glyph and the time under the block, at its start (the agent) or its end (the user). It keeps its row and fades in while the block is hovered or focused; a coarse pointer has no hover, so there it stays. */}
      {foot && (
        <div className={cn('absolute top-full z-[1] flex h-6 items-center gap-1 text-stamp tabular-nums text-muted opacity-0 transition-opacity duration-100 group-hover/copy:opacity-100 group-focus-within/copy:opacity-100 pointer-coarse:opacity-100', side === 'left' ? 'right-0 -mr-1 flex-row-reverse' : 'left-0 -ml-1', (copied || asides) && 'opacity-100')}>
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
          {asides && <AsideChip task={task} turn={asideTurn!} />}
        </div>
      )}
      {context.menu(<ContextMenu.Actions items={items} />)}
    </div>
  );
}

/** The turn head's tokens out and, when the calls reported their duration, tokens per second of generation; the tooltip holds the whole count. Ended, a press opens the turn's waybill; `live`, they count on while it runs. */
function TurnTokens({ timing, live = false }: Readonly<{ timing: TurnTiming; live?: boolean }>) {
  const [button, setButton] = useState<HTMLButtonElement | null>(null);
  const [reader, setReader] = useState<{ open: boolean; phone: boolean } | null>(null);
  const out = timing.output_tokens ?? 0;
  if (!out) return null;
  const rate = timing.generation_ms ? Math.round(out / (timing.generation_ms / 1000)) : 0;
  const title = `${(timing.input_tokens ?? 0).toLocaleString()} tokens in · ${out.toLocaleString()} out${timing.generation_ms ? ` · ${Math.round(timing.generation_ms / 1000)}s generating` : ''}`;
  const words = <>
    {compactTokens(out)} tokens
    {rate > 0 && (
      <>
        <span aria-hidden="true" className="text-faint"> · </span>
        {rate} tok/s
      </>
    )}
  </>;
  if (live) return <span title={title} className="whitespace-nowrap">{words}</span>;
  return (
    <>
      <button ref={setButton} data-turn-waybill="" type="button" aria-haspopup="dialog" aria-expanded={!!reader?.open} title={title} className="relative -mx-1 h-6 rounded-sm px-1 whitespace-nowrap tabular-nums transition-colors duration-100 after:absolute after:inset-x-0 after:-inset-y-1 after:content-[''] hover:bg-tint-hover hover:text-ink aria-expanded:text-ink pointer-coarse:after:-inset-y-2.5" onClick={() => setReader({ open: true, phone: window.matchMedia('(max-width: 639px)').matches })}>
        {words}
      </button>
      {reader && <TurnWaybill timing={timing} anchor={button} phone={reader.phone} open={reader.open} onClose={() => setReader(r => r && { ...r, open: false })} onClosed={() => setReader(null)} />}
    </>
  );
}

/** What the last reply of a turn shows in its foot: when the turn ended and, when known, its timing. */
interface ReplyEnd { at: string; timing?: TurnTiming; userItemId: string; /** The turn's key: its user message's ID, whatever its length. */ turn: string }

/** A reply's stamps, kept while the reply and the calls they read are unchanged, so a memoised turn holds. */
const stampsOf = new WeakMap<Item, { key: string; stamps: Stamp[] }>();
function stampsFor(reply: Item, items: Item[]): Stamp[] {
  // A call's input and output land after its row (the detail stream sends bodies later): they are part of what was read.
  const key = items.map((i) => (i.kind === 'tool' ? `${i.id}:${i.tool?.status}:${i.tool?.exit_code ?? ''}:${i.tool?.file_edits?.length ?? 0}:${i.tool?.input?.length ?? 0}:${i.tool?.output?.length ?? 0}:${i.tool?.display_arg ?? ''}` : '')).join('|');
  const held = stampsOf.get(reply);
  if (held?.key === key) return held.stamps;
  const stamps = receipts(reply.text ?? '', items);
  stampsOf.set(reply, { key, stamps });
  return stamps;
}

/** A turn's items that can name GitLab items, kept while they are unchanged, so a memoised turn holds; none is undefined. */
const gitLabCallsOf = new WeakMap<Item, { key: string; items?: Item[] }>();
function gitLabCallsFor(reply: Item, items: Item[], origin: GitLabOrigin): Item[] | undefined {
  const found = gitLabCandidates(items, origin);
  const key = `${origin.host}/${origin.path}|${found.map((i) => `${i.id}:${i.tool?.status ?? ''}:${i.tool?.input?.length ?? 0}:${i.tool?.output?.length ?? 0}:${i.text?.length ?? 0}`).join('|')}`;
  const held = gitLabCallsOf.get(reply);
  if (held?.key === key) return held.items;
  const kept = found.length ? found : undefined;
  gitLabCallsOf.set(reply, { key, items: kept });
  return kept;
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
    if (end?.at !== at || end.timing !== timing || end.userItemId !== branchUser || end.turn !== userItemId) {
      end = { at, timing, userItemId: branchUser, turn: userItemId };
      replyEndOf.set(reply, end);
    }
    out.set(reply.id, end);
  }
  return out;
}

/** The user's turn: a bubble with the text as typed, then its uploads. The item carries no list of its `@path` references, so those stay plain text. */
const UserBubble = memo(function UserBubble({ item, sessionId, className, asideTurn }: { item: Item; sessionId?: string; className?: string; /** Set while the message's turn has no reply foot: its asides chip goes in this one. */ asideTurn?: string }) {
  return item.clipped ? <ClippedMessage item={item} sessionId={sessionId} className={className} asideTurn={asideTurn} /> : userBubble({ item, text: item.text ?? '', sessionId, className, asideTurn });
});

/** A message row's parts: `text` is the item's, or a clipped item's shown part, with `whole` for its note and copy. Plain functions, so a row costs no extra component. */
interface MessageParts { item: Item; text: string; sessionId?: string; streaming?: boolean; className?: string; whole?: WholeText; /** The reply's claims against its turn's record, when it ends the turn. */ stamps?: Stamp[]; asideTurn?: string }

function userBubble({ item, text, sessionId, className, whole, asideTurn }: MessageParts) {
  const reviewRequest = gitLabReviewRequest(text);
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
      {text && (reviewRequest ? <GitLabReviewRequest request={reviewRequest} text={text} /> : whole?.status === 'whole' ? plainText(text) : <Markdown text={text} breaks />)}
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
        <Copyable text={text} read={whole?.status === 'whole' ? undefined : whole?.read} label="Copy message" side="left" at={item.time} className={width} asideTurn={asideTurn}>
          {bubble}
        </Copyable>
      ) : (
        <div className={width}>{bubble}</div>
      )}
    </div>
  );
}

function assistantMessage({ item, text, streaming = false, className, whole, stamps }: MessageParts) {
  // A reply has no foot: the turn card's head copies the reply that ends the turn, and any reply copies from its right-click menu.
  return (
    <Copyable text={text} read={whole?.status === 'whole' ? undefined : whole?.read} label="Copy message" foot={false} className={className}>
      <div data-history-anchor={item.id} className="text-chat text-body">
        {whole?.status === 'whole' ? plainText(text) : text.includes('mermaid') ? <ItemContext.Provider value={itemContext(item, text)}><Markdown text={text} streaming={streaming} /></ItemContext.Provider> : <Markdown text={text} streaming={streaming} />}
        {whole && <WholeNote whole={whole} />}
        {stamps && <Receipts stamps={stamps} />}
      </div>
    </Copyable>
  );
}

/** A reply's context for its mermaid fences (only a reply that mentions mermaid gets one), one object per item and text, so the provider's value holds still across renders. */
const itemContexts = new WeakMap<Item, { id: string; text: string }>();
function itemContext(item: Item, text: string) {
  let ctx = itemContexts.get(item);
  if (!ctx || ctx.text !== text) {
    ctx = { id: item.id, text };
    itemContexts.set(item, ctx);
  }
  return ctx;
}

function noticeRow({ text, className, whole }: MessageParts) {
  return (
    <div className={cn('flex min-h-8 items-center gap-2 text-caption text-muted', whole && 'flex-wrap', className)}>
      {whole?.status === 'whole' ? plainText(text) : <Markdown text={text} className="[&_p]:m-0" />}
      {whole && <WholeNote whole={whole} />}
    </div>
  );
}

/** A clipped message's whole text, plain: as Markdown, megabytes of it would hold the page for seconds. Its file references still link. */
const plainText = (text: string) => <PlainText text={text} />;

/** A message the service holds shortened (`clipped`, a text over its memory bound): the part held, then the way to the whole text, read only on request. */
function ClippedMessage({ item, sessionId, streaming, className, asideTurn }: Readonly<{ item: Item; sessionId?: string; streaming?: boolean; className?: string; asideTurn?: string }>) {
  const whole = useWholeText(item);
  const parts = { item, text: whole.text, sessionId, streaming, className, whole, asideTurn };
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

const isActive = (s?: ToolStatus) => s === 'pending' || s === 'running';

function toolProgress(tool: Item['tool'], named = false) {
  if (!tool?.progress || !isActive(tool.status)) return null;
  return <p role="status" aria-label="Tool progress" className="ml-6 break-words whitespace-pre-wrap text-caption text-muted">{named && `${toolLabel(tool).name}: `}{tool.progress}</p>;
}

/** A call's state as a glyph: three dots that light in turn while it runs, a still dashed circle while it waits to start, a check, a cross, or a dash for a call that never reported. */
export function ToolMark({ tone }: Readonly<{ tone: string }>) {
  if (tone === 'running') return <StepDots />;
  if (tone === 'pending') return <CircleDashed aria-hidden="true" className="size-3.5 text-muted" strokeWidth={2.5} />;
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
 * A finished step's result at the end of its row (DESIGN.md steps), from what the call recorded:
 * its test counts ("✓ 12 passed", "✗ 14 passed · 1 failed"), its exit code ("✓ exit 0", "✗ exit 2")
 * or that it failed, each a block; its edits' line counts as coloured numbers ("+12 −3") when it has
 * no file rows under it to carry them. The block's
 * words carry it all, never the colour alone; a phone keeps a test run's failures alone.
 */
function StepOutcome({ tool }: Readonly<{ tool: Item['tool'] }>) {
  const result = stepResult(tool);
  if (!result) return null;
  if (result.kind === 'diff') {
    // A step with file rows of its own leaves the counts to them: one count per file, never twice.
    if (tool?.edit_event_id) return null;
    return (
      <span className="shrink-0 font-sans text-caption tabular-nums">
        <span className="text-success">+{result.additions}</span> <span className="text-error">−{result.deletions}</span>
      </span>
    );
  }
  if (result.kind === 'tests') {
    const failed = result.failed > 0;
    const words = !failed ? `${result.passed} passed` : result.passed > 0 ? `${result.passed} passed · ${result.failed} failed` : `${result.failed} failed`;
    const label = failed && result.passed > 0 ? <><span className="max-sm:hidden">{result.passed} passed · </span>{result.failed} failed</> : words;
    return <Blocks className="font-sans" items={[{ key: 'tests', tone: failed ? 'error' : 'success', Icon: failed ? X : Check, label, title: words }]} />;
  }
  const ok = result.kind === 'exit' && result.code === 0;
  const words = result.kind === 'exit' ? `exit ${result.code}` : 'failed';
  return <Blocks className="font-sans" items={[{ key: 'result', tone: ok ? 'success' : 'error', Icon: ok ? Check : X, label: words, title: words }]} />;
}

/**
 * One tool call: mark, tool name (weight 500), its main argument in `code-sm` on one line,
 * and its approval when a request named it; expands to the full input and output, and while
 * it runs its newest output lines (`tail`; the live step shows them under the row instead). The
 * images its result returned sit under the row, visible without expanding it.
 */
export const ToolRow = memo(function ToolRow({ item, live, sessionId, approvals, className, tail = true, step = false }: { item: Item; live: boolean; sessionId?: string; approvals?: Interaction[]; className?: string; tail?: boolean; /** The step in progress (the live foot): its mark turns even before the call reports running. */ step?: boolean }) {
  const [open, setOpen] = useDisclosure(`tool:${item.agent_id ?? ''}:${item.id}`);
  // The details (code blocks) are mounted on the first open only.
  const [opened, setOpened] = useState(open);
  const toggle = () => {
    setOpened(true);
    setOpen((o) => !o);
  };
  const { item: fullItem, body, attach, retry } = useItemBody(item, open);
  const t = item.tool;
  const { items, anchor: toggleButton, label, copyError } = useToolActions(item, open, toggle, sessionId);
  const status = t?.status ?? 'pending';
  // Display only: a tool still pending/running after the turn ended never reported a result.
  const ended = !live && isActive(status);
  const tone = ended ? 'ended' : status;
  const { name, arg } = toolLabel(t);
  const word = statusWord(status, ended);
  const images = item.images ?? [];
  const decided = approvals?.filter((ix) => ix.state !== 'pending') ?? [];
  const context = useLazyContextMenu();
  return (
    <div className={cn('group/tool relative', className)} {...context.shell}>
      <div ref={attach} id={`item-${item.id}`} className={cn('rounded-sm', tone === 'failed' && 'text-error')}>
        <div className="flex items-center gap-1">
          <button
            ref={toggleButton}
            type="button"
            aria-expanded={open}
            className={cn('flex h-6 min-w-0 flex-1 items-center gap-2 rounded-full pl-1.5 text-left font-mono text-code-sm text-muted transition-colors hover:bg-tint-well pointer-coarse:min-h-11 pr-8 pointer-coarse:pr-11', tone === 'running' && 'text-body', tone === 'failed' && 'text-error')}
            title={ended ? 'The turn ended before this tool reported a result' : undefined}
            onClick={toggle}
          >
            <span className="flex size-4 shrink-0 items-center justify-center">
              <ToolMark tone={step && tone === 'pending' ? 'running' : tone} />
            </span>
            <span className={cn('shrink-0 font-medium', tone !== 'failed' && 'text-body')}>{name}</span>
            {arg && <span className="min-w-0 truncate" title={arg}>{arg}</span>}
            <span className="sr-only">, {word}</span>
            <span className="min-w-2 flex-1" />
            {decided.length > 0 && <ApprovalMark interactions={decided} />}
            <StepOutcome tool={t} />
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
      {context.menu(<ContextMenu.Actions items={items} />)}
    </div>
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

/** The images a tool's result returned, as thumbnails that open the viewer, and its note on any left out. A lone image without a name takes the image file the call named (a view of `shot.png`). */
function ToolImages({ item, sessionId, className }: Readonly<{ item: Item; sessionId?: string; className?: string }>) {
  const arg = toolLabel(item.tool).arg;
  const file = /\.(?:png|jpe?g|gif|webp)$/i.test(arg) ? arg.slice(Math.max(arg.lastIndexOf('/'), arg.lastIndexOf('\\')) + 1) : '';
  const images = item.images?.length === 1 && !item.images[0].name && file ? [{ ...item.images[0], name: file }] : item.images ?? [];
  return (
    <div className={cn('flex flex-col gap-1', className)}>
      {sessionId && <ImageThumbs sessionId={sessionId} images={images} />}
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
  const context = useLazyContextMenu();
  return (
    <section id={`item-${id}`} aria-label="Question" className={cn('rounded-md text-ui shadow-raised', pending ? 'flex flex-col gap-1.5 bg-raised px-3.5 py-3' : 'bg-tint-well', className)} {...context.shell}>
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
      {context.menu(<ContextMenu.Actions items={items} />)}
    </section>
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
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', open && 'rotate-z-90')} />
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
export const Turn = memo(function Turn({ item, sessionId, planVersion, planAvailable, streaming, endedAt, className, stamps }: { item: Item; sessionId?: string; planVersion?: number; planAvailable?: boolean; streaming: boolean; endedAt?: string; /** The reply's receipts, when it ends its turn. */ stamps?: Stamp[]; className?: string }) {
  switch (item.kind) {
    case 'user':
      return <UserBubble item={item} sessionId={sessionId} className={className} />;
    case 'assistant':
    case 'notice':
      if (item.kind === 'notice' && item.plan) return <PlanNotice item={item} sessionId={sessionId} planVersion={planVersion} draftAvailable={planAvailable} className={className} />;
      if (item.clipped) return <ClippedMessage item={item} streaming={streaming} className={className} />;
      return item.kind === 'assistant' ? assistantMessage({ item, text: item.text ?? '', streaming, className, stamps }) : noticeRow({ item, text: item.text ?? '', className });
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
function thinkingLabel(streaming: boolean, took: string | null | undefined): string {
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
        {streaming ? <span aria-hidden="true" className="mx-[3px] size-1.5 shrink-0 rounded-full bg-accent" /> : <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', expanded && 'rotate-z-90')} />}
        <span className="tabular-nums">{thinkingLabel(streaming, took)}</span>
      </button>
      <Collapse open={expanded}>
        {/* A closed thought is not read, so it has no body on its way to tell of. */}
        {expanded && <BodyNotice body={body} retry={retry} />}
        {fullItem && <Copyable text={text} label="Copy thinking" className="mt-1 pr-6 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:fade-rule-y before:content-['']">
          <Markdown text={text} className="md-quiet" streaming={streaming} />
        </Copyable>}
      </Collapse>
    </div>
  );
}

/** A subagent's own transcript at 13px: the same rows, blocks and bubbles as the main one, with its own requests. */
export function AgentItems({ sessionId, workdir, provider, agentId, items, identityItems = items, historyItemSeq, interactions, live }: Readonly<{ sessionId: string; workdir: string; provider: string; agentId: string; items: Item[]; identityItems?: Item[]; historyItemSeq?: Record<string, number>; interactions: Interaction[]; live: boolean }>) {
  return (
    <div className="flex flex-col gap-3 text-ui [&_.text-chat]:text-ui [&_.text-chat-lg]:text-ui">
      <Transcript sessionId={sessionId} agentId={agentId} provider={provider} workdir={workdir} items={items} identityItems={identityItems} historyItemSeq={historyItemSeq} interactions={interactions} subagents={[]} live={live} working={live} />
    </div>
  );
}

/** Keep group IDs across backward extension without retaining any row content. */
function useGroupIdentities(items: Item[]) {
  const [saved, setSaved] = useState(() => ({ items, ids: groupIdentities(items) }));
  if (saved.items !== items) {
    const next = { items, ids: groupIdentities(items, saved.ids) };
    setSaved(next);
    return next.ids;
  }
  return saved.ids;
}

/** Standalone decisions are group members too; keep only their identity and time. */
function useIdentityEntries(items: Item[], loose: Interaction[], questions: Interaction[]) {
  return useMemo(() => mergeByTime(items, [...loose, ...questions].filter(interaction => interaction.state !== 'pending')).map(entry => entry.item ?? ({ id: entry.interaction.id, kind: 'tool' as const, time: entry.interaction.time, agent_id: entry.interaction.agent_id })), [items, loose, questions]);
}
