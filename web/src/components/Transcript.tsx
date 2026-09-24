import { Bot, Check, ChevronRight, Copy, Ellipsis, Minus, X } from 'lucide-react';
import { memo, useState, type ReactNode } from 'react';
import { modelName, type Item, type Subagent, type SubagentStatus, type ToolStatus } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { markFileRefs } from '../lib/composer';
import { ItemAttachments } from './Attachments';
import { CodeBlock, Markdown, Sep, Spinner, SubagentIdleIcon, useApp } from './common';
import { Button } from './ui/button';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

interface Props {
  /** The Task, for the attachment routes. */
  sessionId: string;
  items: Item[];
  subagents: Subagent[];
  /** The provider still holds the turn, so pending tools may still report. */
  live: boolean;
  /** A turn is running (not merely waiting for the user): the last item is still streaming. */
  working: boolean;
  /** Provider id and the model of the latest turn, for the provider line above the first reply. */
  provider: string;
  model: string;
  /** Open a subagent's transcript in the side panel; `opener` gets focus back when it closes. */
  onOpenAgent: (agentId: string, opener: HTMLElement) => void;
}

/** Items that arrive after mount rise in; items present at mount appear at once. */
function useArrivals(items: Item[]) {
  const [initial] = useState(() => new Set(items.map((i) => i.id)));
  return (id: string) => (initial.has(id) ? '' : 'animate-rise');
}

/**
 * Main transcript. User items are bubbles on the right; everything between two user items
 * is one flat assistant turn: thinking rows, a ledger of folded tool calls, a compact row
 * for each `task` call that spawned a subagent (its output lives in the panel, never
 * here), and the prose.
 */
export function Transcript({ sessionId, items, subagents, live, working, provider, model, onOpenAgent }: Props) {
  const { meta } = useApp();
  const arrival = useArrivals(items);
  const byParent = new Map<string, Subagent>();
  for (const s of subagents) if (s.parent_tool_call_id) byParent.set(s.parent_tool_call_id, s);
  const ctx: RenderContext = { sessionId, live, streamingId: working ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items), arrival };

  const out: ReactNode[] = [];
  let group: Item[] = [];
  let first = true;
  const flush = () => {
    if (!group.length) return;
    const nodes = renderItems(group, ctx, (item) => {
      const agent = byParent.get(item.id);
      return agent ? <SubagentRow key={item.id} item={item} subagent={agent} provider={provider} onOpen={(el) => onOpenAgent(agent.id, el)} /> : null;
    });
    out.push(
      <div key={`turn-${group[0].id}`} className="flex flex-col gap-3">
        {first && (
          <div className="text-caption text-muted">
            {provider}
            {model && (
              <>
                {' · '}
                <span className="font-mono">{modelName(meta, provider, model)}</span>
              </>
            )}
          </div>
        )}
        {nodes}
      </div>,
    );
    first = false;
    group = [];
  };
  items.forEach((item) => {
    if (item.kind !== 'user') {
      group.push(item);
      return;
    }
    flush();
    out.push(<UserBubble key={item.id} item={item} sessionId={sessionId} className={arrival(item.id)} />);
  });
  flush();
  return (
    <>
      {out}
      {working && <WorkingIndicator />}
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

/** Items in order; consecutive tool calls fold into one ledger, `special` may take an item over. */
function renderItems(items: Item[], ctx: RenderContext, special?: (item: Item) => ReactNode | null): ReactNode[] {
  const out: ReactNode[] = [];
  let run: Item[] = [];
  const flush = () => {
    if (run.length) out.push(<Ledger key={`ledger-${run[0].id}`} items={run} live={ctx.live} className={ctx.arrival(run[0].id)} />);
    run = [];
  };
  for (const item of items) {
    if (item.kind === 'tool') {
      const node = special?.(item);
      if (!node) {
        run.push(item);
        continue;
      }
      flush();
      out.push(node);
      continue;
    }
    // A reasoning item the provider closed without any text has nothing to disclose.
    if (item.kind === 'reasoning' && !item.text?.trim() && item.id !== ctx.streamingId) continue;
    flush();
    out.push(<Turn key={item.id} item={item} sessionId={ctx.sessionId} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} className={ctx.arrival(item.id)} />);
  }
  flush();
  return out;
}

function WorkingIndicator() {
  return (
    <div className="flex items-center gap-2 text-caption text-muted animate-rise" role="status">
      <span aria-hidden="true" className="size-2 rounded-full bg-accent animate-pulse-dot motion-reduce:bg-transparent motion-reduce:shadow-[inset_0_0_0_2px_var(--color-accent)]" />
      Working…
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
            className={cn('absolute top-0 -right-1 size-6 text-muted opacity-0 transition-opacity duration-100 group-hover/copy:opacity-100 focus-visible:opacity-100', copied && 'opacity-100 text-success')}
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

/** The user's turn: a bubble with the text (`@path` references read as chips), then its uploads. */
function UserBubble({ item, sessionId, className }: { item: Item; sessionId?: string; className?: string }) {
  const attachments = item.attachments ?? [];
  return (
    <div className={cn('flex justify-end', className)}>
      <Copyable text={item.text ?? ''} label="Copy message" className="max-w-[min(78%,560px)] max-sm:max-w-[88%]">
        <div className="flex flex-col gap-2 rounded-lg bg-bubble px-3.5 py-2.5 text-chat text-ink shadow-[0_1px_2px_rgba(28,27,24,0.05)] max-sm:text-chat-lg">
          <span className="sr-only">You: </span>
          {item.delivery === 'steer' && <span className="block text-caption text-accent">Steer</span>}
          {item.text && <Markdown text={markFileRefs(item.text)} />}
          {attachments.length > 0 && sessionId && <ItemAttachments sessionId={sessionId} attachments={attachments} />}
        </div>
      </Copyable>
    </div>
  );
}

function Ledger({ items, live, className }: { items: Item[]; live: boolean; className?: string }) {
  const running = items.filter((i) => isActive(i.tool?.status)).length;
  const failed = items.filter((i) => i.tool?.status === 'failed').length;
  const active = running > 0 && live;
  const summary = [`${items.length} tool call${items.length === 1 ? '' : 's'}`, active ? `${running} running` : '', failed ? `${failed} failed` : ''].filter(Boolean).join(' · ');
  return (
    <details className={cn('group/ledger', className)} open={active}>
      <summary aria-live="polite" className="flex h-6 list-none items-center gap-1.5 rounded-sm text-caption text-muted select-none hover:text-body [&::-webkit-details-marker]:hidden">
        <ChevronRight aria-hidden="true" className="size-3.5 text-faint transition-transform duration-160 ease-app group-open/ledger:rotate-90" />
        <span className="tabular-nums">{summary}</span>
        {active && <Spinner />}
      </summary>
      <div className="mt-1 flex flex-col gap-px pl-1">
        {items.map((i) => (
          <ToolRow key={i.id} item={i} live={live} />
        ))}
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

/** One ledger row: glyph, tool name (the first word, weight 500), argument; expands to input and output. */
export const ToolRow = memo(function ToolRow({ item, live }: { item: Item; live: boolean }) {
  const [open, setOpen] = useState(false);
  const [, copy] = useCopied();
  const t = item.tool;
  const status = t?.status ?? 'pending';
  // Display only: a tool still pending/running after the turn ended never reported a result.
  const ended = !live && isActive(status);
  const tone = ended ? 'ended' : status;
  const label = t?.title || t?.name || 'Tool';
  const space = label.indexOf(' ');
  const head = space > 0 ? label.slice(0, space) : label;
  const rest = space > 0 ? label.slice(space + 1) : '';
  const word = ended ? 'no result' : status === 'completed' ? 'done' : status;
  const items: ActionItem[] = [
    { key: 'toggle', label: open ? 'Collapse' : 'Expand', icon: <ChevronRight />, onSelect: () => setOpen((o) => !o) },
    { key: 'cmd', label: 'Copy command', icon: <Copy />, disabled: !t?.input, onSelect: () => copy(t?.input ?? ''), separator: true },
    { key: 'out', label: 'Copy output', icon: <Copy />, disabled: !t?.output, onSelect: () => copy(t?.output ?? '') },
  ];
  return (
    <ContextMenu.Root>
      <ContextMenu.Trigger render={<div className="group/tool relative" />}>
        <details id={`item-${item.id}`} className={cn('rounded-sm', tone === 'failed' && 'text-error')} open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
          <summary
            className={cn('flex h-6 list-none items-center gap-2 rounded-sm pr-8 pl-1 font-mono text-code-sm text-muted select-none transition-colors hover:bg-canvas [&::-webkit-details-marker]:hidden', tone === 'running' && 'text-body', tone === 'failed' && 'text-error')}
            title={ended ? 'The turn ended before this tool reported a result' : undefined}
          >
            <span className="flex size-4 shrink-0 items-center justify-center">
              <ToolMark tone={tone} />
            </span>
            <span className={cn('shrink-0 font-medium', tone !== 'failed' && 'text-body')}>{head}</span>
            {rest && <span className="min-w-0 truncate">{rest}</span>}
            <span className="sr-only">, {word}</span>
          </summary>
          <div className="my-1 ml-6 flex flex-col gap-1 text-ui">
            {item.text && <Markdown text={item.text} />}
            {t?.input && <CodeBlock language="input">{t.input}</CodeBlock>}
            {t?.output && <CodeBlock language="output">{t.output}</CodeBlock>}
            {!item.text && !t?.input && !t?.output && <p className="text-caption text-muted">No details yet.</p>}
          </div>
        </details>
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

/** One non-user item. Everything from the provider is markdown, rendered without raw HTML, also while it streams. */
export const Turn = memo(function Turn({ item, sessionId, streaming, endedAt, className }: { item: Item; sessionId?: string; streaming: boolean; endedAt?: string; className?: string }) {
  switch (item.kind) {
    case 'user':
      return <UserBubble item={item} sessionId={sessionId} className={className} />;
    case 'assistant':
      return (
        <Copyable text={item.text ?? ''} label="Copy message" className={cn('pr-6', className)}>
          <div className="text-chat text-body max-sm:text-chat-lg">
            <Markdown text={item.text ?? ''} />
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
      return <ToolRow item={item} live={false} />;
    default:
      return null;
  }
});

const THINKING_KEY = 'uam.thinking:';

/** Last non-empty line of the text, with leading markdown marks stripped, for the live preview. */
function lastLine(text: string): string {
  const lines = text
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
  const line = lines[lines.length - 1] ?? '';
  return line.replace(/^[#>*\-\s`]+/, '').replace(/`/g, '');
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
 * A reasoning item: a "Thinking" disclosure, collapsed by default. While it streams the row
 * reads "Thinking…" with the latest line; done, it reads "Thought for 12s" when the next item's
 * timestamp is known. The choice is remembered per item for the browser session.
 */
export function Thinking({ item, streaming, endedAt, className }: { item: Item; streaming: boolean; endedAt?: string; className?: string }) {
  const key = THINKING_KEY + item.id;
  const [open, setOpen] = useState(() => sessionStorage.getItem(key) === '1');
  const text = item.text ?? '';
  const took = !streaming && endedAt ? duration(item.time, endedAt) : null;
  const label = streaming ? 'Thinking…' : took ? `Thought for ${took}` : 'Thought';
  const preview = streaming && !open ? lastLine(text) : '';
  return (
    <details
      className={cn('group/think', className)}
      open={open}
      onToggle={(e) => {
        const next = e.currentTarget.open;
        if (next === open) return;
        setOpen(next);
        sessionStorage.setItem(key, next ? '1' : '0');
      }}
    >
      <summary className="flex h-6 list-none items-center gap-1.5 rounded-sm text-ui text-muted select-none hover:text-body [&::-webkit-details-marker]:hidden">
        <ChevronRight aria-hidden="true" className="size-3.5 shrink-0 text-faint transition-transform duration-160 ease-app group-open/think:rotate-90" />
        <span className={cn('shrink-0 tabular-nums', streaming && 'animate-shimmer motion-reduce:animate-none')}>{label}</span>
        {preview && (
          <>
            <Sep />
            <span className="min-w-0 truncate text-caption text-faint">{preview}</span>
          </>
        )}
      </summary>
      <Copyable text={text} label="Copy thinking" className="mt-1 ml-1">
        <div className="max-h-80 overflow-y-auto rounded-sm bg-sunken px-3 py-2 text-ui text-muted">
          <Markdown text={text} />
        </div>
      </Copyable>
    </details>
  );
}

/** Subagent state as a chip: glyph plus the word; only "running" animates. */
export function AgentChip({ status }: { status: SubagentStatus }) {
  const base = 'inline-flex h-5 shrink-0 items-center gap-1.5 rounded-xs px-1.5 text-caption whitespace-nowrap';
  switch (status) {
    case 'running':
      return (
        <span className={cn(base, 'text-accent')}>
          <Spinner />
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

/** A subagent's own transcript at 13px: the same rows and bubbles; tool calls fold like the main one. */
export function AgentItems({ items, live }: { items: Item[]; live: boolean }) {
  const arrival = useArrivals(items);
  const ctx: RenderContext = { live, streamingId: live ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items), arrival };
  return (
    <div className="flex flex-col gap-3 text-ui [&_.text-chat]:text-ui [&_.text-chat-lg]:text-ui">
      {renderItems(items, ctx)}
      {live && <WorkingIndicator />}
    </div>
  );
}
