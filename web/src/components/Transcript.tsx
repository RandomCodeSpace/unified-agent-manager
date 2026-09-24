import { memo, useState, type ReactNode } from 'react';
import { modelName, type Item, type Subagent, type SubagentStatus, type ToolStatus } from '../api';
import { Markdown, Sep, Spinner, useApp } from './common';

interface Props {
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

/**
 * Main transcript. User items are bubbles on the right; everything between two user items
 * is one flat assistant turn: thinking rows, a ledger of folded tool calls, a compact row
 * for each `task` call that spawned a subagent (its output lives in the panel, never
 * here), and the prose.
 */
export function Transcript({ items, subagents, live, working, provider, model, onOpenAgent }: Props) {
  const byParent = new Map<string, Subagent>();
  for (const s of subagents) if (s.parent_tool_call_id) byParent.set(s.parent_tool_call_id, s);
  const ctx: RenderContext = { live, streamingId: working ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items) };

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
      <div key={`turn-${group[0].id}`} className="turn turn-assistant">
        {first && (
          <div className="provider-line">
            {provider}
            {model && (
              <>
                {' · '}
                <code>{model}</code>
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
    out.push(
      <div key={item.id} className="turn turn-user">
        <div className="bubble-user">
          <span className="sr-only">You: </span>
          {item.delivery === 'steer' && <span className="caption">Steer</span>}
          <Markdown text={item.text ?? ''} />
        </div>
      </div>,
    );
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
  live: boolean;
  /** The item still receiving deltas, if any. */
  streamingId: string | undefined;
  /** For each reasoning item that was followed by another item: when that next item started. */
  thoughtEnd: Map<string, string>;
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
    if (run.length) out.push(<Ledger key={`ledger-${run[0].id}`} items={run} live={ctx.live} />);
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
    out.push(<Turn key={item.id} item={item} streaming={item.id === ctx.streamingId} endedAt={ctx.thoughtEnd.get(item.id)} />);
  }
  flush();
  return out;
}

function WorkingIndicator() {
  return (
    <div className="working" role="status">
      <span className="mark-dot" aria-hidden="true" />
      Working…
    </div>
  );
}

function Ledger({ items, live }: { items: Item[]; live: boolean }) {
  const running = items.filter((i) => isActive(i.tool?.status)).length;
  const failed = items.filter((i) => i.tool?.status === 'failed').length;
  const active = running > 0 && live;
  const summary = [`${items.length} tool call${items.length === 1 ? '' : 's'}`, active ? `${running} running` : '', failed ? `${failed} failed` : '']
    .filter(Boolean)
    .join(' · ');
  return (
    <details className="ledger" open={active}>
      <summary aria-live="polite">
        <span className="chev" aria-hidden="true" />
        <span className="num">{summary}</span>
        {active && <Spinner />}
      </summary>
      <div className="ledger-body">
        {items.map((i) => (
          <ToolRow key={i.id} item={i} live={live} />
        ))}
      </div>
    </details>
  );
}

const isActive = (s?: ToolStatus) => s === 'pending' || s === 'running';

const TOOL_GLYPH: Record<string, string> = { completed: '✓', failed: '✕', ended: '–' };

/** One ledger row: glyph, tool name (the first word, weight 500), argument; expands to input and output. */
export const ToolRow = memo(function ToolRow({ item, live }: { item: Item; live: boolean }) {
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
  return (
    <details className={`tool tool-${tone}`} id={`item-${item.id}`}>
      <summary title={ended ? 'The turn ended before this tool reported a result' : undefined}>
        {tone === 'running' || tone === 'pending' ? (
          <Spinner />
        ) : (
          <span className="tool-mark" aria-hidden="true">
            {TOOL_GLYPH[tone]}
          </span>
        )}
        <span className="tool-name">{head}</span>
        {rest && <span className="tool-title">{rest}</span>}
        <span className="sr-only">, {word}</span>
      </summary>
      <div className="tool-body">
        {item.text && <Markdown text={item.text} />}
        {t?.input && (
          <>
            <div className="label">Input</div>
            <pre className="code" translate="no">
              {t.input}
            </pre>
          </>
        )}
        {t?.output && (
          <>
            <div className="label">Output</div>
            <pre className="code" translate="no">
              {t.output}
            </pre>
          </>
        )}
        {!item.text && !t?.input && !t?.output && <p className="caption">No details yet.</p>}
      </div>
    </details>
  );
});

/** One non-user item. Everything from the provider is markdown, rendered without raw HTML, also while it streams. */
export const Turn = memo(function Turn({ item, streaming, endedAt }: { item: Item; streaming: boolean; endedAt?: string }) {
  switch (item.kind) {
    case 'user':
      return (
        <div className="bubble-user">
          <span className="sr-only">You: </span>
          {item.delivery === 'steer' && <span className="caption">Steer</span>}
          <Markdown text={item.text ?? ''} />
        </div>
      );
    case 'assistant':
      return (
        <div className="message-assistant">
          <Markdown text={item.text ?? ''} />
        </div>
      );
    case 'reasoning':
      return <Thinking item={item} streaming={streaming} endedAt={endedAt} />;
    case 'notice':
      return (
        <div className="notice-line">
          <Markdown text={item.text ?? ''} />
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
  const lines = text.split('\n').map((l) => l.trim()).filter(Boolean);
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
export function Thinking({ item, streaming, endedAt }: { item: Item; streaming: boolean; endedAt?: string }) {
  const key = THINKING_KEY + item.id;
  const [open, setOpen] = useState(() => sessionStorage.getItem(key) === '1');
  const text = item.text ?? '';
  const took = !streaming && endedAt ? duration(item.time, endedAt) : null;
  const label = streaming ? 'Thinking…' : took ? `Thought for ${took}` : 'Thought';
  const preview = streaming && !open ? lastLine(text) : '';
  return (
    <details
      className={streaming ? 'thinking thinking-live' : 'thinking'}
      open={open}
      onToggle={(e) => {
        const next = e.currentTarget.open;
        if (next === open) return;
        setOpen(next);
        sessionStorage.setItem(key, next ? '1' : '0');
      }}
    >
      <summary>
        <span className="chev" aria-hidden="true" />
        <span className="thinking-label num">{label}</span>
        {preview && (
          <>
            <Sep />
            <span className="thinking-preview">{preview}</span>
          </>
        )}
      </summary>
      <div className="thinking-body">
        <Markdown text={text} />
      </div>
    </details>
  );
}

/** Subagent state as a chip: glyph plus the word; only "running" animates. */
export function AgentChip({ status }: { status: SubagentStatus }) {
  switch (status) {
    case 'running':
      return (
        <span className="chip">
          <Spinner />
          Running
        </span>
      );
    case 'idle':
      return (
        <span className="chip">
          <span aria-hidden="true">↩</span>Idle
        </span>
      );
    case 'completed':
      return (
        <span className="chip chip-success">
          <span aria-hidden="true">✓</span>Completed
        </span>
      );
    case 'failed':
      return (
        <span className="chip chip-error">
          <span aria-hidden="true">✕</span>Failed
        </span>
      );
    default:
      return (
        <span className="chip">
          <span aria-hidden="true">–</span>Stopped
        </span>
      );
  }
}

/**
 * The `task` tool call that spawned a subagent, as one compact row: name, state, duration
 * once ended, and "Open", which shows the transcript in the side panel. Nothing of the
 * subagent's output renders in the main column.
 */
function SubagentRow({
  item,
  subagent,
  provider,
  onOpen,
}: {
  item: Item;
  subagent: Subagent;
  provider: string;
  onOpen: (opener: HTMLElement) => void;
}) {
  const { meta } = useApp();
  const name = subagent.name || item.tool?.title || item.tool?.name || 'Subagent';
  const took = subagent.started_at && subagent.ended_at ? duration(subagent.started_at, subagent.ended_at) : null;
  return (
    <div className="agent-chip" id={`item-${item.id}`}>
      <span className="eyebrow">Subagent</span>
      <span className="agent-chip-name" title={subagent.description || undefined}>
        {name}
      </span>
      <AgentChip status={subagent.status} />
      {subagent.model && <span className="caption mono">{modelName(meta, provider, subagent.model)}</span>}
      {took && <span className="caption num">{took}</span>}
      <button type="button" className="btn btn-ghost btn-sm" onClick={(e) => onOpen(e.currentTarget)}>
        Open
      </button>
    </div>
  );
}

/** A subagent's own transcript at 13px: the same rows and bubbles; tool calls fold like the main one. */
export function AgentItems({ items, live }: { items: Item[]; live: boolean }) {
  const ctx: RenderContext = { live, streamingId: live ? items[items.length - 1]?.id : undefined, thoughtEnd: thoughtEnds(items) };
  return (
    <div className="transcript transcript-agent turn">
      {renderItems(items, ctx)}
      {live && <WorkingIndicator />}
    </div>
  );
}
