import { Bot, Check, ChevronRight, Copy, Ellipsis, MessageCircleQuestion, Minus, Shield, ShieldCheck, ShieldX, Terminal, X } from 'lucide-react';
import { memo, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from 'react';
import { modelName, type Interaction, type Item, type Subagent, type SubagentStatus, type ToolStatus } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { approvalMark, elapsedSince, foregroundItems, foregroundStart, summarizeTools, linkInteractions, mergeByTime, questionOf, toolLabel, type AskedQuestion, type Entry } from '../lib/transcript';
import { ImageThumbs, ItemAttachments } from './Attachments';
import { CodeBlock, Markdown, Spinner, SubagentIdleIcon, WorkingMark, useApp } from './common';
import { DecidedRow } from './Interactions';
import { Button } from './ui/button';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

interface Props {
  /** The Task, for the attachment routes. */
  sessionId: string;
  items: Item[];
  /** The Task's requests; the decided ones join the turns, the pending ones stay cards. */
  interactions: Interaction[];
  subagents: Subagent[];
  /** The provider still holds the turn, so pending tools may still report. */
  live: boolean;
  /** A turn is running (not merely waiting for the user): the last item is still streaming. */
  working: boolean;
  /** Provider id used to resolve subagent model names. */
  provider: string;
  /** Open a subagent's transcript in the side panel; `opener` gets focus back when it closes. */
  onOpenAgent: (agentId: string, opener: HTMLElement) => void;
}

/** Rows that arrive after mount rise in; rows present at mount appear at once. */
function useArrivals(ids: string[]) {
  const [initial] = useState(() => new Set(ids));
  return (id: string) => (initial.has(id) ? '' : 'animate-rise');
}

/**
 * Main transcript. User items are bubbles on the right; everything between two user items
 * is one flat assistant turn: thinking inline, one row per tool call with its approval on
 * it, a question block for each `ask_user` call, a compact row for each `task` call that
 * spawned a subagent (its output lives in the panel, never here), and the prose. A decided
 * request without a tool row joins the turn at its time.
 */
export function Transcript({ sessionId, items, interactions, subagents, live, working, provider, onOpenAgent }: Props) {
  const arrival = useArrivals([...items.map((i) => i.id), ...interactions.map((i) => i.id)]);
  const byParent = new Map<string, Subagent>();
  for (const s of subagents) if (s.parent_tool_call_id) byParent.set(s.parent_tool_call_id, s);
  const { linked, loose, questions } = linkInteractions(items, interactions);
  const ctx: RenderContext = { sessionId, live, streamingId: working ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items), arrival, approvals: linked };

  const foreground = new Set(foregroundItems(items).map((item) => item.id));
  const out: ReactNode[] = [];
  let group: Entry[] = [];
  let showedWorking = false;
  const flush = (last = false, boundary = true) => {
    if (!group.length) return;
    const groupLive = live && group.some((entry) => entry.item && foreground.has(entry.item.id));
    const nodes = renderEntries(group, { ...ctx, live: groupLive }, (item) => {
      const agent = byParent.get(item.id);
      return agent ? <SubagentRow key={item.id} item={item} subagent={agent} provider={provider} onOpen={(el) => onOpenAgent(agent.id, el)} /> : null;
    });
    const key = (group[0].item ?? group[0].interaction)!.id;
    if (last && working) showedWorking = true;
    out.push(
      <div key={`turn-${key}`} className="flex flex-col gap-3">
        {last && working && <WorkingIndicator start={foregroundStart(items)} />}
        {nodes}
        {boundary && (!last || !live) && <div className="border-b border-hairline py-2 text-caption text-muted" title="The provider does not supply a turn completion timestamp.">Worked</div>}
      </div>,
    );
    group = [];
  };
  mergeByTime(items, [...loose, ...questions]).forEach((entry) => {
    if (entry.item?.kind !== 'user') {
      group.push(entry);
      return;
    }
    flush(false, entry.item.delivery !== 'steer');
    out.push(<UserBubble key={entry.item.id} item={entry.item} sessionId={sessionId} className={arrival(entry.item.id)} />);
  });
  flush(true);
  return (
    <>
      {out}
      {working && !showedWorking && <WorkingIndicator start={foregroundStart(items)} />}
    </>
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

/** Entries in order; consecutive tool calls form one run of rows, `special` may take an item over. */
function renderEntries(entries: Entry[], ctx: RenderContext, special?: (item: Item) => ReactNode | null): ReactNode[] {
  const out: ReactNode[] = [];
  let run: Item[] = [];
  const flush = () => {
    if (run.length) out.push(<ToolRun key={`run-${run[0].id}`} items={run} ctx={ctx} />);
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
      const linked = ctx.approvals.get(item.id);
      const asked = questionOf(item.tool, linked?.filter((ix) => ix.kind === 'question').at(-1), ctx.live);
      const node = special?.(item) ?? (asked ? <QuestionBlock key={item.id} id={item.id} asked={asked} className={ctx.arrival(item.id)} /> : null);
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

function WorkingIndicator({ start }: { start?: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, []);
  const elapsed = elapsedSince(start, now);
  return (
    <div className="flex items-center gap-2 border-b border-hairline py-2 text-caption text-muted">
      <WorkingMark />
      <span role="status" className="sr-only">Working</span>
      <span role="timer" aria-live="off">{elapsed ? `Working for ${elapsed}` : 'Working'}</span>
    </div>
  );
}

/** A hover copy button plus a right-click menu around any block of provider or user text. */
function Copyable({ text, label, className, children, extra = [] }: { text: string; label: string; className?: string; children: ReactNode; extra?: ActionItem[] }) {
  const [copied, copy] = useCopied();
  const items: ActionItem[] = [{ key: 'copy', label, icon: <Copy />, onSelect: () => copy(text) }, ...extra];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className={cn('group/copy relative', className)} />}>
        {children}
        <Tip label={copied ? 'Copied' : label}>
          <Button
            size="icon"
            variant="ghost"
            aria-label={copied ? 'Copied' : label}
            className={cn('absolute top-0 -right-1 size-6 text-muted opacity-0 transition-opacity duration-100 group-hover/copy:opacity-100 focus-visible:opacity-100 pointer-coarse:opacity-100', copied && 'opacity-100 text-success')}
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
function UserBubble({ item, sessionId, className }: { item: Item; sessionId?: string; className?: string }) {
  const attachments = item.attachments ?? [];
  return (
    <div className={cn('flex justify-end', className)}>
      <Copyable text={item.text ?? ''} label="Copy message" className="max-w-[min(88%,720px)] max-sm:max-w-[88%]">
        <div className="flex flex-col gap-2 rounded-lg bg-bubble px-3.5 py-2.5 text-chat text-ink">
          <span className="sr-only">You: </span>
          {item.delivery === 'steer' && <span className="block text-caption text-accent">Steer</span>}
          {item.text && <Markdown text={item.text} />}
          {attachments.length > 0 && sessionId && <ItemAttachments sessionId={sessionId} attachments={attachments} />}
        </div>
      </Copyable>
    </div>
  );
}

/** Consecutive tools share a compact disclosure; prose and questions stay in time order. */
function ToolRun({ items, ctx }: { items: Item[]; ctx: RenderContext }) {
  const failed = items.some((item) => item.tool?.status === 'failed');
  const active = ctx.live && items.some((item) => isActive(item.tool?.status));
  return (
    <details className="group/run" data-tool-run="">
      <summary className={cn('flex min-h-7 cursor-pointer list-none items-center gap-2 rounded-sm text-ui text-muted hover:text-body pointer-coarse:min-h-11 [&::-webkit-details-marker]:hidden', failed && 'text-error')}>
        {active ? <WorkingMark /> : <Terminal aria-hidden="true" className="size-4 shrink-0" />}
        <span>{summarizeTools(items, ctx.live)}</span>
        <ChevronRight aria-hidden="true" className="size-3 shrink-0 transition-transform group-open/run:rotate-90" />
      </summary>
      <div className="mt-1 flex flex-col gap-1 border-l border-hairline pl-3">
        {items.map((item) => <ToolRow key={item.id} item={item} live={ctx.live} sessionId={ctx.sessionId} approvals={ctx.approvals.get(item.id)} className={ctx.arrival(item.id)} />)}
      </div>
    </details>
  );
}

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
      <span className="ml-auto inline-flex h-5 shrink-0 items-center gap-1 rounded-xs px-1 font-sans text-caption text-muted transition-colors duration-100 group-hover/tool:text-body">
        <Icon aria-hidden="true" className="size-3 text-faint" strokeWidth={2} />
        {latest.word}
        {earlier.length > 0 && <span className="text-faint tabular-nums">+{earlier.length}</span>}
        <span className="sr-only">: {marks.map((m) => m.full).join('; earlier: ')}</span>
      </span>
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
        <details id={`item-${item.id}`} className={cn('rounded-sm', tone === 'failed' && 'text-error')} open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
          <summary
            className={cn('flex h-6 list-none items-center gap-2 rounded-sm pr-8 pl-1 font-mono text-code-sm text-muted select-none transition-colors hover:bg-canvas pointer-coarse:min-h-11 pointer-coarse:pr-11 [&::-webkit-details-marker]:hidden', tone === 'running' && 'text-body', tone === 'failed' && 'text-error')}
            title={ended ? 'The turn ended before this tool reported a result' : undefined}
          >
            <span className="flex size-4 shrink-0 items-center justify-center">
              <ToolMark tone={tone} />
            </span>
            <span className={cn('shrink-0 font-medium', tone !== 'failed' && 'text-body')}>{name}</span>
            {arg && <span className="min-w-0 truncate">{arg}</span>}
            <span className="sr-only">, {word}</span>
            {approvals && approvals.filter((ix) => ix.state !== 'pending').length > 0 && <ApprovalMark interactions={approvals.filter((ix) => ix.state !== 'pending')} />}
          </summary>
          <div className="my-1 ml-6 flex flex-col gap-1 text-ui">
            {item.text && <Markdown text={item.text} />}
            {t?.input && <CodeBlock language="input">{t.input}</CodeBlock>}
            {t?.output && <CodeBlock language="output">{t.output}</CodeBlock>}
            {!item.text && !t?.input && !t?.output && <p className="text-caption text-muted">No details yet.</p>}
          </div>
        </details>
        {(images.length > 0 || item.images_note) && (
          <div className="mt-1 mb-1.5 ml-7 flex flex-col gap-1">
            {sessionId && <ImageThumbs sessionId={sessionId} images={images} />}
            {item.images_note && <p className="text-caption text-muted">{item.images_note}</p>}
          </div>
        )}
        <Menu.Root modal={false}>
          <Menu.Trigger render={<Button size="icon" className="absolute top-0 right-0 size-6 text-muted opacity-0 transition-opacity group-hover/tool:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" aria-label={`Actions for ${label}`} />}>
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
function QuestionBlock({ id, asked, className }: { id: string; asked: AskedQuestion; className?: string }) {
  const [, copy] = useCopied();
  const text = asked.questions.map((q) => q.text).join('\n') || 'The agent asked a question.';
  const items: ActionItem[] = [
    { key: 'q', label: 'Copy question', icon: <Copy />, onSelect: () => copy(text) },
    { key: 'a', label: 'Copy answer', icon: <Copy />, disabled: !asked.answer, onSelect: () => copy(asked.answer ?? '') },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<section id={`item-${id}`} aria-label="Question" className={cn('flex flex-col gap-1.5 rounded-md bg-sunken/60 px-3 py-2.5 text-ui', className)} />}>
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
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5 border-t border-hairline pt-1.5">
            <span className="text-caption text-muted">You answered</span>
            <span className="min-w-0 text-ink">{asked.answer}</span>
          </div>
        )}
        {asked.outcome === 'declined' && <p className="border-t border-hairline pt-1.5 text-caption text-muted">You declined to answer.</p>}
        {asked.outcome === 'none' && <p className="border-t border-hairline pt-1.5 text-caption text-muted">No answer.</p>}
        {asked.outcome === 'failed' && <p className="border-t border-hairline pt-1.5 text-caption text-error">Failed{asked.error ? `: ${asked.error}` : '.'}</p>}
      </ContextMenu.Trigger>
      <ContextMenu.Content>
        <ContextMenu.Actions items={items} />
      </ContextMenu.Content>
    </ContextMenu.Root>
  );
}

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
const CLAMP_LINES = 3;

/** The text's non-empty lines with leading markdown marks stripped. */
function plainLines(text: string): string[] {
  return text
    .split('\n')
    .map((l) => l.trim().replace(/^[#>*\-\s`]+/, '').replace(/`/g, ''))
    .filter(Boolean);
}

/** "12s", "1m 4s" or "<1s" between two ISO timestamps; null when they are not in order. */
export function duration(from: string, to: string): string | null {
  const ms = new Date(to).getTime() - new Date(from).getTime();
  if (!Number.isFinite(ms) || ms < 0) return null;
  if (ms < 1000) return '<1s';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  return `${Math.floor(s / 60)}m ${s % 60}s`;
}

/**
 * A reasoning item: its text inline in `muted` behind a hairline rule, clamped to three
 * lines with Show more / Show less. While it streams the latest lines show under a
 * shimmering "Thinking…"; done, "Thought for 12s" when the next item's timestamp is
 * known. The choice is remembered per item for the browser session.
 */
export function Thinking({ item, streaming, endedAt, className }: { item: Item; streaming: boolean; endedAt?: string; className?: string }) {
  const key = THINKING_KEY + item.id;
  const [expanded, setExpanded] = useState(() => sessionStorage.getItem(key) === '1');
  const [clamped, setClamped] = useState(false);
  const body = useRef<HTMLDivElement>(null);
  const text = item.text ?? '';
  const took = !streaming && endedAt ? duration(item.time, endedAt) : null;
  const lines = plainLines(text);
  // The clamp is measured, so a long paragraph counts as much as many short lines.
  useLayoutEffect(() => {
    const el = body.current;
    if (el && !expanded) setClamped(el.scrollHeight > el.clientHeight + 1);
  }, [text, expanded, streaming]);
  const more = expanded || clamped || (streaming && lines.length > CLAMP_LINES);
  const toggle = () => {
    const next = !expanded;
    setExpanded(next);
    sessionStorage.setItem(key, next ? '1' : '0');
  };
  return (
    <div className={cn('flex flex-col gap-1 border-l-2 border-hairline pl-3 text-ui text-muted', className)}>
      {streaming && <span className="text-caption animate-shimmer motion-reduce:animate-none">Thinking…</span>}
      {expanded ? (
        <Copyable text={text} label="Copy thinking" className="pr-6">
          <Markdown text={text} className="md-quiet" streaming={streaming} />
        </Copyable>
      ) : streaming ? (
        <div ref={body} className="line-clamp-3 whitespace-pre-line">
          {lines.slice(-CLAMP_LINES).join('\n')}
        </div>
      ) : (
        <div ref={body} className="line-clamp-3">
          <Markdown text={text} className="md-quiet" streaming={streaming} />
        </div>
      )}
      {(more || took) && (
        <div className="flex items-center gap-2 text-caption">
          {more && (
            <button type="button" aria-expanded={expanded} className="rounded-xs text-muted transition-colors duration-100 hover:text-body pointer-coarse:min-h-11 pointer-coarse:min-w-11" onClick={toggle}>
              {expanded ? 'Show less' : 'Show more'}
            </button>
          )}
          {took && <span className="text-faint tabular-nums">Thought for {took}</span>}
        </div>
      )}
    </div>
  );
}

/** Subagent state as a chip: glyph plus the word; only "running" moves. */
export function AgentChip({ status }: { status: SubagentStatus }) {
  const base = 'inline-flex h-5 shrink-0 items-center gap-1.5 rounded-xs px-1.5 text-caption whitespace-nowrap';
  switch (status) {
    case 'running':
      return (
        <span className={cn(base, 'text-accent')}>
          <WorkingMark />
          Running
        </span>
      );
    case 'idle':
      return (
        <span className={cn(base, 'text-muted')}>
          <SubagentIdleIcon />
          Idle
        </span>
      );
    case 'completed':
      return (
        <span className={cn(base, 'text-success')}>
          <Check aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
          Completed
        </span>
      );
    case 'failed':
      return (
        <span className={cn(base, 'text-error')}>
          <X aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
          Failed
        </span>
      );
    default:
      return (
        <span className={cn(base, 'text-muted')}>
          <Minus aria-hidden="true" className="size-3.5" strokeWidth={2.5} />
          Stopped
        </span>
      );
  }
}

/**
 * The `task` tool call that spawned a subagent, as one compact row: name, state, duration
 * once ended, and "Open", which shows the transcript in the side panel. Nothing of the
 * subagent's output renders in the main column.
 */
function SubagentRow({ item, subagent, provider, onOpen }: { item: Item; subagent: Subagent; provider: string; onOpen: (opener: HTMLElement) => void }) {
  const { meta } = useApp();
  const [, copy] = useCopied();
  const name = subagent.name || item.tool?.title || item.tool?.name || 'Subagent';
  const took = subagent.started_at && subagent.ended_at ? duration(subagent.started_at, subagent.ended_at) : null;
  const items: ActionItem[] = [
    { key: 'open', label: 'Open subagent', icon: <Bot />, onSelect: () => onOpen(document.getElementById(`item-${item.id}`) ?? document.body) },
    { key: 'copy', label: 'Copy agent ID', icon: <Copy />, onSelect: () => copy(subagent.id), separator: true },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger
        render={<div id={`item-${item.id}`} className="flex min-h-9 flex-wrap items-center gap-x-3 gap-y-1 rounded-sm bg-sunken/60 py-1.5 pr-1.5 pl-3 text-ui transition-colors" />}
      >
        <Bot aria-hidden="true" className="size-4 shrink-0 text-muted" />
        <span className="min-w-0 flex-1 truncate font-medium text-ink" title={subagent.description || undefined}>
          {name}
        </span>
        <AgentChip status={subagent.status} />
        {subagent.model && <span className="font-mono text-code-sm text-muted">{modelName(meta, provider, subagent.model)}</span>}
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
export function AgentItems({ sessionId, agentId, items, interactions, live }: { sessionId: string; agentId: string; items: Item[]; interactions: Interaction[]; live: boolean }) {
  const arrival = useArrivals([...items.map((i) => i.id), ...interactions.map((i) => i.id)]);
  const { linked, loose, questions } = linkInteractions(items, interactions, agentId);
  const ctx: RenderContext = { sessionId, live, streamingId: live ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items), arrival, approvals: linked };
  return (
    <div className="flex flex-col gap-3 text-ui [&_.text-chat]:text-ui [&_.text-chat-lg]:text-ui">
      {renderEntries(mergeByTime(items, [...loose, ...questions]), ctx)}
      {live && <WorkingIndicator />}
    </div>
  );
}
