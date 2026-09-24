import { memo, useEffect, useState, type ReactNode } from 'react';
import { api, describeError, type Item, type Subagent, type SubagentStatus, type ToolStatus } from '../api';
import type { AgentTranscript } from '../state';
import { Markdown, Sep, useApp } from './common';

interface Props {
  sessionId: string;
  items: Item[];
  subagents: Subagent[];
  agents: Record<string, AgentTranscript>;
  /** Changes on every fresh snapshot; expanded subagent blocks reload then. */
  snapshotSeq: number;
  /** The provider still holds the turn, so pending tools may still report. */
  live: boolean;
}

/**
 * Main transcript. Consecutive tool calls fold into one ledger line; a tool call that
 * started a subagent stands on its own with the subagent block under it, and consecutive
 * ones (parallel subagents) stack.
 */
export function Transcript({ sessionId, items, subagents, agents, snapshotSeq, live }: Props) {
  const byParent = new Map<string, Subagent>();
  for (const s of subagents) if (s.parent_tool_call_id) byParent.set(s.parent_tool_call_id, s);

  const out: ReactNode[] = [];
  let run: Item[] = [];
  const flush = () => {
    if (!run.length) return;
    out.push(<Ledger key={`ledger-${run[0].id}`} items={run} live={live} />);
    run = [];
  };
  for (const item of items) {
    if (item.kind === 'tool') {
      const agent = byParent.get(item.id);
      if (!agent) {
        run.push(item);
        continue;
      }
      flush();
      out.push(
        <SubagentBlock
          key={item.id}
          sessionId={sessionId}
          item={item}
          subagent={agent}
          transcript={agents[agent.id]}
          snapshotSeq={snapshotSeq}
        />,
      );
      continue;
    }
    flush();
    out.push(<Turn key={item.id} item={item} />);
  }
  flush();
  return <div className="transcript">{out}</div>;
}

function Ledger({ items, live }: { items: Item[]; live: boolean }) {
  const running = items.filter((i) => isActive(i.tool?.status)).length;
  const failed = items.filter((i) => i.tool?.status === 'failed').length;
  const summary = [
    `${items.length} tool call${items.length === 1 ? '' : 's'}`,
    running && live ? `${running} running` : '',
    failed ? `${failed} failed` : '',
  ]
    .filter(Boolean)
    .join(' · ');
  const tone = running && live ? 'running' : failed ? 'failed' : 'completed';
  return (
    <details className="ledger" open={running > 0 && live}>
      <summary>
        <span className={`tool-dot tool-dot-${tone}`} aria-hidden="true" />
        {summary}
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

export const ToolRow = memo(function ToolRow({ item, live }: { item: Item; live: boolean }) {
  const t = item.tool;
  const status = t?.status ?? 'pending';
  // Display only: a tool still pending/running after the turn ended never reported a result.
  const ended = !live && isActive(status);
  const shown = ended ? 'ended' : status === 'completed' ? 'done' : status;
  return (
    <details className={`tool tool-${ended ? 'ended' : status}`}>
      <summary>
        <span className={`tool-dot tool-dot-${ended ? 'ended' : status}`} aria-hidden="true" />
        <span className="tool-title">{t?.title || t?.name || 'Tool'}</span>
        <Sep />
        <span className="tool-status" title={ended ? 'The turn ended before this tool reported a result' : undefined}>
          {shown}
        </span>
      </summary>
      <div className="tool-body">
        {item.text && <div className="plain">{item.text}</div>}
        {t?.input && (
          <>
            <div className="label">Input</div>
            <pre className="code">{t.input}</pre>
          </>
        )}
        {t?.output && (
          <>
            <div className="label">Output</div>
            <pre className="code">{t.output}</pre>
          </>
        )}
        {!item.text && !t?.input && !t?.output && <p className="muted small">No details yet.</p>}
      </div>
    </details>
  );
});

export const Turn = memo(function Turn({ item }: { item: Item }) {
  switch (item.kind) {
    case 'user':
      return (
        <div className="you">
          <div className="label">You</div>
          <div className="plain">{item.text}</div>
        </div>
      );
    case 'assistant':
      return (
        <div className="turn turn-assistant">
          <Markdown text={item.text ?? ''} />
        </div>
      );
    case 'reasoning':
      return (
        <details className="reasoning">
          <summary>Reasoning</summary>
          <div className="plain muted">{item.text}</div>
        </details>
      );
    case 'notice':
      return <p className="notice">{item.text}</p>;
    case 'tool':
      return <ToolRow item={item} live={false} />;
    default:
      return null;
  }
});

const AGENT_STATUS: Record<SubagentStatus, string> = {
  running: 'running',
  completed: 'done',
  failed: 'failed',
  cancelled: 'stopped',
};

/**
 * A subagent under the `task` tool call that started it. Collapsed by default; expanding
 * loads the transcript from the subagent route, after which live frames tagged with this
 * agent_id keep it current (see state.ts).
 */
function SubagentBlock({
  sessionId,
  item,
  subagent,
  transcript,
  snapshotSeq,
}: {
  sessionId: string;
  item: Item;
  subagent: Subagent;
  transcript: AgentTranscript | undefined;
  snapshotSeq: number;
}) {
  const { dispatch } = useApp();
  const [open, setOpen] = useState(false);
  const live = subagent.status === 'running';

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    dispatch({ type: 'agent_loading', agentId: subagent.id });
    api
      .subagent(sessionId, subagent.id)
      .then((d) => !cancelled && dispatch({ type: 'agent_loaded', agentId: subagent.id, subagent: d.subagent, items: d.items }))
      .catch((e: unknown) => !cancelled && dispatch({ type: 'agent_failed', agentId: subagent.id, error: describeError(e) }));
    return () => {
      cancelled = true;
    };
    // snapshotSeq: a reconnect replaced the stream, so reload to close any gap.
  }, [open, sessionId, subagent.id, snapshotSeq, dispatch]);

  const name = subagent.name || item.tool?.title || item.tool?.name || 'Subagent';
  return (
    <details className={`agent agent-${subagent.status}`} open={open} onToggle={(e) => setOpen(e.currentTarget.open)}>
      <summary>
        <span className={`tool-dot tool-dot-${subagent.status}`} aria-hidden="true" />
        <span className="agent-name">{name}</span>
        <span className="badge-pill">subagent</span>
        <Sep />
        <span className="tool-status">{AGENT_STATUS[subagent.status] ?? subagent.status}</span>
        {subagent.description && (
          <>
            <Sep />
            <span className="agent-desc">{subagent.description}</span>
          </>
        )}
      </summary>
      <div className="agent-body">
        {!transcript || transcript.loading ? (
          <p className="muted small">Loading transcript…</p>
        ) : transcript.error ? (
          <p className="error" role="alert">
            {transcript.error}
          </p>
        ) : (
          <>
            <AgentItems items={transcript.items} live={live} />
            {transcript.items.length === 0 && <p className="muted small">Nothing recorded yet.</p>}
          </>
        )}
        {subagent.status === 'completed' && item.tool?.output && (
          <div className="agent-result">
            <div className="label">Result</div>
            <Markdown text={item.tool.output} />
          </div>
        )}
        {subagent.status === 'failed' && (
          <p className="error">Failed{subagent.error ? `: ${subagent.error}` : '.'}</p>
        )}
        {subagent.status === 'cancelled' && <p className="muted small">Stopped before it finished.</p>}
        {live && <p className="muted small">Running…</p>}
      </div>
    </details>
  );
}

/** A subagent's own transcript: tool calls fold like the main one; nested `task` calls stay plain rows. */
function AgentItems({ items, live }: { items: Item[]; live: boolean }) {
  const out: ReactNode[] = [];
  let run: Item[] = [];
  const flush = () => {
    if (run.length) out.push(<Ledger key={`ledger-${run[0].id}`} items={run} live={live} />);
    run = [];
  };
  for (const item of items) {
    if (item.kind === 'tool') {
      run.push(item);
      continue;
    }
    flush();
    out.push(<Turn key={item.id} item={item} />);
  }
  flush();
  return <div className="transcript transcript-agent">{out}</div>;
}
