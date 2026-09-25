import { Bot, Check, ChevronRight, Copy, Ellipsis, MessageCircleQuestion, Minus, Shield, ShieldCheck, ShieldX, Terminal, X } from 'lucide-react';
import { memo, useCallback, useEffect, useState, type ReactNode } from 'react';
import { modelName, type Interaction, type Item, type Subagent, type SubagentStatus, type ToolStatus, type TurnTiming } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { approvalMark, askedOn, duration, elapsedSince, foregroundItems, foregroundStart, completedDuration, isWork, segmentActivity, summarizeActivity, subagentSummary, timingForTurn, showTurnEnd, summarizeTools, linkInteractions, mergeByTime, questionOf, toolLabel, type AskedQuestion, type Entry } from '../lib/transcript';
import { turnVerb } from '../lib/verbs';
import type { AgentTranscript } from '../state';
import { ImageThumbs, ItemAttachments } from './Attachments';
import { CodeBlock, Markdown, SessionContext, Spinner, SubagentIdleIcon, WorkdirContext, WorkingMark, useApp } from './common';
import { DecidedRow } from './Interactions';
import { Button } from './ui/button';
import { Chip } from './ui/chip';
import { Collapse, usePresence } from './ui/collapse';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

interface Props {
  /** The Task, for the attachment routes. */
  sessionId: string;
  items: Item[];
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
  onOpenAgent: (agentId: string, opener: HTMLElement) => void;
}

/** Rows that arrive after mount rise in; rows present at mount appear at once. Stable, so memoised rows hold. */
function useArrivals(ids: string[]) {
  const [initial] = useState(() => new Set(ids));
  return useCallback((id: string) => (initial.has(id) ? '' : 'animate-rise'), [initial]);
}

/**
 * Main transcript. User items are bubbles on the right; everything between two user items
 * is one flat assistant turn: thinking inline, one row per tool call with its approval on
 * it, a question block for each `ask_user` call once it no longer waits, a compact row for
 * each `task` call that spawned a subagent (its output lives in the panel, never here), and
 * the prose. A decided request without a tool row joins the turn at its time.
 */
export function Transcript({ sessionId, items, turnTimings = [], interactions, subagents, agents = {}, agentSteps = {}, live, working, provider, workdir, onOpenAgent }: Props) {
  const arrival = useArrivals([...items.map((i) => i.id), ...interactions.map((i) => i.id)]);
  const byParent = new Map<string, Subagent>();
  for (const s of subagents) if (s.parent_tool_call_id) byParent.set(s.parent_tool_call_id, s);
  const { linked, loose, questions } = linkInteractions(items, interactions);
  const ctx: RenderContext = { sessionId, live, streamingId: working ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items), arrival, approvals: linked };

  const foreground = new Set(foregroundItems(items).map((item) => item.id));
  const out: ReactNode[] = [];
  let group: Entry[] = [];
  let showedWorking = false;
  let userItemId: string | undefined;
  // A turn is keyed by the user message before it, so it keeps its rows when its first entry changes.
  let after = 'start';
  const flush = (last = false, boundary = true) => {
    const timing = timingForTurn(turnTimings, userItemId);
    const showEnd = showTurnEnd(timing, { hasContent: group.length > 0, boundary, last, live });
    if (!group.length) {
      if (showEnd) out.push(<TurnStatus key={`end-${timing!.id}`} timing={timing} />);
      return;
    }
    const groupLive = live && group.some((entry) => entry.item && foreground.has(entry.item.id));
    const nodes = renderEntries(group, { ...ctx, live: groupLive }, (item) => {
      const agent = byParent.get(item.id);
      return agent ? <SubagentRow key={item.id} item={item} subagent={agent} agentItems={agents[agent.id]?.items ?? (agentSteps[agent.id] ? [agentSteps[agent.id]] : undefined)} provider={provider} onOpen={(el) => onOpenAgent(agent.id, el)} /> : null;
    });
    if (last && working) showedWorking = true;
    // One status row heads the turn: "Busy for 12s" becomes "Took 12s" in the same slot, and
    // streamed content lands below it, so nothing on screen moves at either moment.
    out.push(
      <div key={`turn-${after}`} className="flex flex-col gap-3">
        {last && working ? <TurnStatus working start={foregroundStart(turnTimings)} /> : showEnd && <TurnStatus timing={timing} />}
        {nodes}
      </div>,
    );
    group = [];
  };
  mergeByTime(items, [...loose, ...questions]).forEach((entry) => {
    if (entry.item?.kind !== 'user') {
      group.push(entry);
      return;
    }
    flush(false, !entry.item.delivery);
    after = entry.item.id;
    if (!entry.item.delivery) userItemId = entry.item.id;
    out.push(<UserBubble key={entry.item.id} item={entry.item} sessionId={sessionId} className={arrival(entry.item.id)} />);
  });
  flush(true);
  return (
    <SessionContext.Provider value={sessionId}>
      <WorkdirContext.Provider value={workdir}>
        {out}
        {working && !showedWorking && <TurnStatus working start={foregroundStart(turnTimings)} />}
        <WorkingTail working={working && !liveAtFoot(items, byParent)} turnId={userItemId ?? 'start'} />
      </WorkdirContext.Provider>
    </SessionContext.Provider>
  );
}

/**
 * Whether the last row is already live: a thought streaming or a call running folds into an
 * activity row that carries the working mark and says so ("Thinking…", "Running: …"). A subagent's
 * call is its own row, not an activity row.
 */
function liveAtFoot(items: Item[], byParent: Map<string, Subagent>): boolean {
  const last = items[items.length - 1];
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
function WorkingTail({ working, turnId }: { working: boolean; turnId: string }) {
  const { mounted, onClosed } = usePresence(working);
  if (!mounted) return null;
  return (
    <Collapse open={working} appear onClosed={onClosed} className="-mt-6" inner="pt-3">
      <div aria-hidden="true" className="flex h-6 items-center gap-2 text-caption text-muted">
        <WorkingMark />
        <span className="animate-shimmer motion-reduce:animate-none">{turnVerb(turnId)}…</span>
      </div>
    </Collapse>
  );
}

interface RenderContext {
  sessionId?: string;
  live: boolean;
  /** The item still receiving deltas, if any. */
  streamingId: string | undefined;
  /** For each reasoning item that was followed by another item: when that next item started. */
  thoughtEnd: Map<string, string>;
  arrival: (id: string) => string;
  /** Requests by the tool item they sit on, oldest first. */
  approvals: Map<string, Interaction[]>;
}

/** A reasoning item ends when the next item begins; both are server timestamps. */
function thoughtEnds(items: Item[]): Map<string, string> {
  const m = new Map<string, string>();
  items.forEach((item, k) => {
    const next = items[k + 1];
    if (item.kind === 'reasoning' && next) m.set(item.id, next.time);
  });
  return m;
}

/**
 * Entries in order. Each contiguous run of work between two messages (thinking, tool calls,
 * decided requests, questions that no longer wait) folds into one activity row; prose and
 * the rows `special` takes over (subagents, whose controls must stay in view) stand on
 * their own between them.
 */
function renderEntries(entries: Entry[], ctx: RenderContext, special?: (item: Item) => ReactNode | null): ReactNode[] {
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
    out.push(<ActivityRun key={segment.key} entries={segment.entries} ctx={ctx} endedAt={endedAt} className={ctx.arrival(segment.key)} />);
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
    if (run.length) out.push(<ToolRun key={`run-${runs++}`} items={run} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals} arrival={ctx.arrival} />);
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
    if (item.kind === 'reasoning' && !item.text?.trim()) continue;
    flush();
    out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} className={ctx.arrival(item.id)} />);
  }
  flush();
  return out;
}

const NO_OWN = new Map<string, ReactNode>();

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
const ActivityRun = memo(function ActivityRun({ entries, ctx, endedAt, className }: { entries: Entry[]; ctx: RenderContext; endedAt?: string; className?: string }) {
  const [open, setOpen] = useState(false);
  const { label, tone, active } = summarizeActivity(entries, { live: ctx.live, streamingId: ctx.streamingId, approvals: ctx.approvals, endedAt });
  if (!label) return null;
  return (
    <div data-activity="" className={cn('flex flex-col', className)}>
      <button type="button" aria-expanded={open} title={label} className={cn('flex h-6 w-fit max-w-full items-center gap-2 rounded-full bg-tint-well pr-3 pl-2 text-left text-caption text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11', tone === 'error' && 'text-error', tone === 'attention' && 'text-attention')} onClick={() => setOpen((o) => !o)}>
        <span className="flex size-3.5 shrink-0 items-center justify-center">
          {active ? <WorkingMark /> : <ChevronRight aria-hidden="true" className={cn('size-3 text-faint transition-transform duration-160 ease-app', open && 'rotate-90')} />}
        </span>
        <span className="min-w-0 truncate tabular-nums">{label}</span>
      </button>
      <Collapse open={open}>
        <div className="mt-1 flex flex-col gap-1 pl-5.5">{renderRows(entries, ctx, NO_OWN)}</div>
      </Collapse>
    </div>
  );
}, (a, b) => {
  const same = (x: Entry, y: Entry) => (x.item ?? x.interaction) === (y.item ?? y.interaction);
  // `thoughtEnd` is rebuilt every render; what it says about these entries changes only with them or with `endedAt`.
  return a.endedAt === b.endedAt && a.className === b.className && a.ctx.live === b.ctx.live && a.ctx.sessionId === b.ctx.sessionId && a.ctx.approvals === b.ctx.approvals && a.ctx.arrival === b.ctx.arrival
    && streamingIn(a.entries, a.ctx.streamingId) === streamingIn(b.entries, b.ctx.streamingId) && a.entries.length === b.entries.length && a.entries.every((e, i) => same(e, b.entries[i]));
});

/**
 * The row that heads a turn (DESIGN.md turn status), in one slot for both states: the
 * working mark and a live "Busy for 12s" while the turn runs, then "Took 12s" once it
 * ended. Without a recorded duration the row keeps its slot, with no label and no rule.
 */
function TurnStatus({ working = false, start, timing }: { working?: boolean; start?: string; timing?: TurnTiming }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!working) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [working]);
  const elapsed = working ? elapsedSince(start, now) : completedDuration(timing);
  return (
    <div className="flex min-h-[34px] items-center gap-2 py-2 text-caption tabular-nums text-muted" title={working ? undefined : elapsed ? 'Recorded foreground turn duration' : 'Turn duration was not recorded.'}>
      {working && <WorkingMark />}
      {working && <span role="status" className="sr-only">Busy</span>}
      {working ? <span role="timer" aria-live="off">{elapsed ? `Busy for ${elapsed}` : 'Busy'}</span> : elapsed && <span className="animate-fade-in">Took {elapsed}</span>}
    </div>
  );
}

/** A hover copy button plus a right-click menu around any block of provider or user text. */
function Copyable({ text, label, className, side = 'right', children, extra = [] }: { text: string; label: string; className?: string; /** Where the button sits: over the block's top-right corner, or outside it to the left (the user bubble, so it never covers the text). */ side?: 'right' | 'left'; children: ReactNode; extra?: ActionItem[] }) {
  const [copied, copy] = useCopied();
  const items: ActionItem[] = [{ key: 'copy', label, icon: <Copy />, onSelect: () => copy(text) }, ...extra];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/copy relative', className)} />}>
        {children}
        <Tip label={copied ? 'Copied' : label}>
          <Button
            size="icon-sm"
            variant="ghost"
            aria-label={copied ? 'Copied' : label}
            className={cn('absolute top-0 text-muted opacity-0 transition-opacity duration-100 group-hover/copy:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100', side === 'right' ? '-right-1' : '-left-7', copied && 'opacity-100 text-success')}
            onClick={() => copy(text)}
          >
            {copied ? <Check /> : <Copy />}
          </Button>
        </Tip>
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}

/** The user's turn: a bubble with the text as typed, then its uploads. The item carries no list of its `@path` references, so those stay plain text. */
const UserBubble = memo(function UserBubble({ item, sessionId, className }: { item: Item; sessionId?: string; className?: string }) {
  const attachments = item.attachments ?? [];
  return (
    <div className={cn('flex justify-end', className)}>
      <Copyable text={item.text ?? ''} label="Copy message" side="left" className="max-w-[min(88%,720px)] max-sm:max-w-[88%]">
        <div className="flex flex-col gap-2 rounded-lg bg-bubble px-3.5 py-2.5 text-chat text-ink shadow-raised">
          <span className="sr-only">You: </span>
          {item.delivery === 'autopilot' && <span className="block text-caption text-accent">Autopilot</span>}
          {item.text && <Markdown text={item.text} />}
          {attachments.length > 0 && sessionId && <ItemAttachments sessionId={sessionId} attachments={attachments} />}
        </div>
      </Copyable>
    </div>
  );
});

interface ToolRunProps {
  items: Item[];
  live: boolean;
  sessionId?: string;
  approvals: Map<string, Interaction[]>;
  arrival: (id: string) => string;
}

/** Consecutive tools share a compact disclosure; prose and questions stay in time order. Memoised on its calls, which a streamed delta elsewhere leaves alone. */
const ToolRun = memo(function ToolRun({ items, live, sessionId, approvals, arrival }: ToolRunProps) {
  const [open, setOpen] = useState(false);
  const failed = items.some((item) => item.tool?.status === 'failed');
  const active = live && items.some((item) => isActive(item.tool?.status));
  return (
    <div data-tool-run="">
      <button type="button" aria-expanded={open} className={cn('flex min-h-7 w-fit max-w-full items-center gap-2 rounded-full bg-tint-well pr-3 pl-2 text-left text-ui text-muted transition-colors duration-100 hover:bg-tint-hover hover:text-body pointer-coarse:min-h-11', failed && 'text-error')} onClick={() => setOpen((o) => !o)}>
        {active ? <WorkingMark /> : <Terminal aria-hidden="true" className="size-4 shrink-0" />}
        <span>{summarizeTools(items, live)}</span>
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 transition-transform duration-160 ease-app', open && 'rotate-90')} />
      </button>
      <Collapse open={open}>
        <div className="relative mt-1 flex flex-col gap-1 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-px before:fade-rule-y before:content-['']">
          {items.map((item) => <ToolRow key={item.id} item={item} live={live} sessionId={sessionId} approvals={approvals.get(item.id)} className={arrival(item.id)} />)}
        </div>
      </Collapse>
    </div>
  );
}, (a, b) => a.live === b.live && a.sessionId === b.sessionId && a.approvals === b.approvals && a.arrival === b.arrival && a.items.length === b.items.length && a.items.every((item, i) => item === b.items[i]));

const isActive = (s?: ToolStatus) => s === 'pending' || s === 'running';

function ToolMark({ tone }: { tone: string }) {
  if (tone === 'running' || tone === 'pending') return <Spinner />;
  if (tone === 'completed') return <Check aria-hidden="true" className="size-3.5 text-success" strokeWidth={2.5} />;
  if (tone === 'failed') return <X aria-hidden="true" className="size-3.5 text-error" strokeWidth={2.5} />;
  return <Minus aria-hidden="true" className="size-3.5 text-faint" strokeWidth={2.5} />;
}

/**
 * The decided requests that sit on a tool row, as one mark: a shield and the latest
 * request's word ("auto" for yolo, "allowed" or "denied" for a person), never a line of
 * its own. The tooltip and the accessible name carry every request's full resolution.
 */
function ApprovalMark({ interactions }: { interactions: Interaction[] }) {
  const marks = interactions.map(approvalMark).reverse();
  const [latest, ...earlier] = marks;
  const Icon = latest.tone === 'denied' ? ShieldX : latest.tone === 'gone' ? Shield : ShieldCheck;
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
  const [open, setOpen] = useState(false);
  const [, copy] = useCopied();
  const t = item.tool;
  const status = t?.status ?? 'pending';
  // Display only: a tool still pending/running after the turn ended never reported a result.
  const ended = !live && isActive(status);
  const tone = ended ? 'ended' : status;
  const { name, arg } = toolLabel(t);
  const label = arg ? `${name} ${arg}` : name;
  const word = ended ? 'no result' : status === 'completed' ? 'done' : status;
  const images = item.images ?? [];
  const items: ActionItem[] = [
    { key: 'toggle', label: open ? 'Collapse' : 'Expand', icon: <ChevronRight />, onSelect: () => setOpen((o) => !o) },
    { key: 'cmd', label: 'Copy command', icon: <Copy />, disabled: !t?.input, onSelect: () => copy(t?.input ?? ''), separator: true },
    { key: 'out', label: 'Copy output', icon: <Copy />, disabled: !t?.output, onSelect: () => copy(t?.output ?? '') },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/tool relative', className)} />}>
        <div id={`item-${item.id}`} className={cn('rounded-sm', tone === 'failed' && 'text-error')}>
          <button
            type="button"
            aria-expanded={open}
            className={cn('flex h-6 w-full items-center gap-2 rounded-full pr-8 pl-1.5 text-left font-mono text-code-sm text-muted transition-colors hover:bg-tint-well pointer-coarse:min-h-11 pointer-coarse:pr-11', tone === 'running' && 'text-body', tone === 'failed' && 'text-error')}
            title={ended ? 'The turn ended before this tool reported a result' : undefined}
            onClick={() => setOpen((o) => !o)}
          >
            <span className="flex size-4 shrink-0 items-center justify-center">
              <ToolMark tone={tone} />
            </span>
            <span className={cn('shrink-0 font-medium', tone !== 'failed' && 'text-body')}>{name}</span>
            {arg && <span className="min-w-0 truncate" title={arg}>{arg}</span>}
            <span className="sr-only">, {word}</span>
            {approvals && approvals.filter((ix) => ix.state !== 'pending').length > 0 && <ApprovalMark interactions={approvals.filter((ix) => ix.state !== 'pending')} />}
          </button>
          <Collapse open={open}>
            <div className="my-1 ml-6 flex flex-col gap-1 text-ui">
              {item.text && <Markdown text={item.text} />}
              {t?.input && <CodeBlock language="input">{t.input}</CodeBlock>}
              {t?.output && <CodeBlock language="output">{t.output}</CodeBlock>}
              {!item.text && !t?.input && !t?.output && <p className="text-caption text-muted">No details yet.</p>}
            </div>
          </Collapse>
        </div>
        {(images.length > 0 || item.images_note) && (
          <div className="mt-1 mb-1.5 ml-7 flex flex-col gap-1">
            {sessionId && <ImageThumbs sessionId={sessionId} images={images} />}
            {item.images_note && <p className="text-caption text-muted">{item.images_note}</p>}
          </div>
        )}
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
 * A question the agent asked, from its interaction (any provider) or Copilot's `ask_user`
 * call (input and output, so it reads the same after a reload and a restart): the text and
 * choices with the chosen ones marked, then the answer under "You answered". While it
 * waits, the action card below the transcript takes the answer; a call left open by a
 * restart or a stopped turn reads "No answer".
 */
// `asked` is rebuilt on every transcript render; equal content means nothing to redraw.
const QuestionBlock = memo(function QuestionBlock({ id, asked, className }: { id: string; asked: AskedQuestion; className?: string }) {
  const [, copy] = useCopied();
  const text = asked.questions.map((q) => q.text).join('\n') || 'The agent asked a question.';
  const items: ActionItem[] = [
    { key: 'q', label: 'Copy question', icon: <Copy />, onSelect: () => copy(text) },
    { key: 'a', label: 'Copy answer', icon: <Copy />, disabled: !asked.answer, onSelect: () => copy(asked.answer ?? '') },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<section id={`item-${id}`} aria-label="Question" className={cn('flex flex-col gap-1.5 rounded-md bg-raised px-3.5 py-3 text-ui shadow-raised', className)} />}>
        <div className="flex items-center gap-1.5 text-caption text-muted">
          <MessageCircleQuestion aria-hidden="true" className="size-3.5 text-faint" />
          <span>Question</span>
          {asked.outcome === 'pending' && (
            <>
              <span aria-hidden="true">·</span>
              <span className="text-attention">Waiting for your answer</span>
            </>
          )}
        </div>
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
        {asked.outcome === 'answered' && (
          <>
            <div className="fade-rule mt-0.5" aria-hidden="true" />
            <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
              <span className="text-caption text-muted">You answered</span>
              <span className="min-w-0 text-ink">{asked.answer}</span>
            </div>
          </>
        )}
        {asked.outcome !== 'answered' && asked.outcome !== 'pending' && <div className="fade-rule mt-0.5" aria-hidden="true" />}
        {asked.outcome === 'declined' && <p className="text-caption text-muted">You declined to answer.</p>}
        {asked.outcome === 'none' && <p className="text-caption text-muted">No answer.</p>}
        {asked.outcome === 'failed' && <p className="text-caption text-error">Failed{asked.error ? `: ${asked.error}` : '.'}</p>}
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}, (a, b) => a.id === b.id && a.className === b.className && JSON.stringify(a.asked) === JSON.stringify(b.asked));

/** One non-user item. Everything from the provider is markdown, rendered without raw HTML, also while it streams. */
export const Turn = memo(function Turn({ item, sessionId, streaming, endedAt, className }: { item: Item; sessionId?: string; streaming: boolean; endedAt?: string; className?: string }) {
  switch (item.kind) {
    case 'user':
      return <UserBubble item={item} sessionId={sessionId} className={className} />;
    case 'assistant':
      return (
        <Copyable text={item.text ?? ''} label="Copy message" className={cn('pr-6', className)}>
          <div className="text-chat text-body">
            <Markdown text={item.text ?? ''} streaming={streaming} />
          </div>
        </Copyable>
      );
    case 'reasoning':
      return <Thinking item={item} streaming={streaming} endedAt={endedAt} className={className} />;
    case 'notice':
      return (
        <div className={cn('flex min-h-8 items-center gap-2 text-caption text-muted', className)}>
          <Markdown text={item.text ?? ''} className="[&_p]:m-0" />
        </div>
      );
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
export function Thinking({ item, streaming, endedAt, className }: { item: Item; streaming: boolean; endedAt?: string; className?: string }) {
  const key = THINKING_KEY + item.id;
  const [expanded, setExpanded] = useState(() => sessionStorage.getItem(key) === '1');
  const text = item.text ?? '';
  const took = !streaming && endedAt ? duration(item.time, endedAt) : null;
  const toggle = () => {
    const next = !expanded;
    setExpanded(next);
    sessionStorage.setItem(key, next ? '1' : '0');
  };
  return (
    <div className={cn('flex flex-col text-ui text-muted', className)}>
      <button type="button" aria-expanded={expanded} className="flex h-6 w-fit items-center gap-1.5 rounded-sm pr-1 text-left transition-colors duration-100 hover:text-body pointer-coarse:min-h-11" onClick={toggle}>
        <ChevronRight aria-hidden="true" className={cn('size-3 shrink-0 text-faint transition-transform duration-160 ease-app', expanded && 'rotate-90')} />
        <span className={cn('tabular-nums', streaming && 'animate-shimmer motion-reduce:animate-none')}>{streaming ? 'Thinking…' : took ? `Thought for ${took}` : 'Thought'}</span>
      </button>
      <Collapse open={expanded}>
        <Copyable text={text} label="Copy thinking" className="mt-1 pr-6 pl-3 before:absolute before:inset-y-0 before:left-0 before:w-0.5 before:fade-rule-y before:content-['']">
          <Markdown text={text} className="md-quiet" streaming={streaming} />
        </Copyable>
      </Collapse>
    </div>
  );
}

/** Subagent state as a chip: glyph plus the word; only "running" moves. */
export function AgentChip({ status }: { status: SubagentStatus }) {
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
function SubagentRow({ item, subagent, agentItems, provider, onOpen }: { item: Item; subagent: Subagent; agentItems?: Item[]; provider: string; onOpen: (opener: HTMLElement) => void }) {
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
export function AgentItems({ sessionId, workdir, agentId, items, interactions, live }: { sessionId: string; workdir: string; agentId: string; items: Item[]; interactions: Interaction[]; live: boolean }) {
  const arrival = useArrivals([...items.map((i) => i.id), ...interactions.map((i) => i.id)]);
  const { linked, loose, questions } = linkInteractions(items, interactions, agentId);
  const ctx: RenderContext = { sessionId, live, streamingId: live ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items), arrival, approvals: linked };
  return (
    <SessionContext.Provider value={sessionId}>
      <WorkdirContext.Provider value={workdir}>
        <div className="flex flex-col gap-3 text-ui [&_.text-chat]:text-ui [&_.text-chat-lg]:text-ui">
          {renderEntries(mergeByTime(items, [...loose, ...questions]), ctx)}
          {live && <TurnStatus working />}
        </div>
      </WorkdirContext.Provider>
    </SessionContext.Provider>
  );
}
