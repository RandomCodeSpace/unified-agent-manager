import { useEffect, useLayoutEffect, useRef, type ReactNode } from 'react';
import { api, describeError, modelName, type Item, type Meta, type SessionDetail, type Subagent, type SubagentStatus } from '../api';
import type { AgentTranscript } from '../state';
import { Markdown, Spinner, useApp } from './common';
import { AgentChip, AgentItems, duration } from './Transcript';

/** "<model> · <effort>" from whatever the provider reported; empty when it reported neither. */
function setupOf(meta: Meta | null, provider: string, s: Subagent): string {
  return [s.model ? modelName(meta, provider, s.model) : '', s.effort ?? ''].filter(Boolean).join(' · ');
}

export type PanelView = { view: 'list' } | { view: 'agent'; id: string };

const GROUPS: { status: SubagentStatus; label: string }[] = [
  { status: 'running', label: 'Running' },
  { status: 'failed', label: 'Failed' },
  { status: 'completed', label: 'Completed' },
  { status: 'cancelled', label: 'Cancelled' },
];

const clock = (iso: string) => new Date(iso).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

/** Distance from the bottom, in px, under which the view counts as "at the bottom". */
const BOTTOM_SLACK = 32;

/**
 * The subagent panel beside (or over) the conversation: a grouped list of the task's
 * subagents, or one subagent's transcript. Subagent output never appears in the main
 * column, so the two streams cannot be confused.
 */
export function SubagentPanel({
  session,
  agents,
  snapshotSeq,
  view,
  inline,
  onView,
  onClose,
  onLocate,
}: {
  session: SessionDetail;
  agents: Record<string, AgentTranscript>;
  snapshotSeq: number;
  view: PanelView;
  inline: boolean;
  onView: (v: PanelView) => void;
  onClose: () => void;
  /** Scroll the main transcript to the `task` tool row that spawned a subagent. */
  onLocate: (toolCallId: string) => void;
}) {
  const { meta, narrow } = useApp();
  const lead = useRef<HTMLButtonElement>(null);
  const current = view.view === 'agent' ? session.subagents.find((s) => s.id === view.id) : undefined;

  // Focus the leading control whenever the view changes (open, back, open transcript).
  useEffect(() => {
    lead.current?.focus();
  }, [view]);

  const cls = inline ? 'panel' : 'panel panel-overlay';
  if (view.view === 'agent' && current) {
    return (
      <aside className={cls} role={inline ? undefined : 'dialog'} aria-modal={inline ? undefined : true} aria-label={`Subagent ${current.name}`}>
        <div className="sheet-head">
          <button ref={lead} type="button" className="btn btn-icon" aria-label="Back to the subagent list" onClick={() => onView({ view: 'list' })}>
            <span aria-hidden="true">←</span>
          </button>
          <span className="sheet-title panel-title" title={current.name}>
            <span className="muted">Subagent · </span>
            {current.name}
          </span>
          <AgentChip status={current.status} />
          {setupOf(meta, session.provider, current) && <span className="caption mono panel-setup">{setupOf(meta, session.provider, current)}</span>}
          {/* Room for a Stop action once the providers can cancel one subagent. */}
          <button type="button" className="btn btn-icon" aria-label="Close subagents" onClick={onClose}>
            <span aria-hidden="true">×</span>
          </button>
        </div>
        <AgentTranscriptView
          key={current.id}
          sessionId={session.id}
          subagent={current}
          transcript={agents[current.id]}
          snapshotSeq={snapshotSeq}
          result={current.status === 'completed' ? session.items.find((i) => i.id === current.parent_tool_call_id)?.tool?.output : undefined}
        />
      </aside>
    );
  }

  const running = session.subagents.filter((s) => s.status === 'running').length;
  return (
    <aside className={cls} role={inline ? undefined : 'dialog'} aria-modal={inline ? undefined : true} aria-label="Subagents">
      <div className="sheet-head">
        {narrow ? (
          <button ref={lead} type="button" className="btn btn-icon" aria-label="Back to the task" onClick={onClose}>
            <span aria-hidden="true">←</span>
          </button>
        ) : null}
        <span className="sheet-title">Subagents</span>
        <span className="sheet-counts num">
          {session.subagents.length}
          {running > 0 && ` · ${running} running`}
        </span>
        <span className="spacer" />
        {!narrow && (
          <button ref={lead} type="button" className="btn btn-icon" aria-label="Close subagents" onClick={onClose}>
            <span aria-hidden="true">×</span>
          </button>
        )}
      </div>
      <div className="panel-body">
        {GROUPS.map(({ status, label }) => {
          const rows = session.subagents.filter((s) => s.status === status);
          if (!rows.length) return null;
          return (
            <section key={status} className="agent-group" aria-label={label}>
              <div className="eyebrow agent-group-label">
                {label} <span className="count num">· {rows.length}</span>
              </div>
              {rows.map((s) => (
                <SubagentRow
                  key={s.id}
                  subagent={s}
                  setup={setupOf(meta, session.provider, s)}
                  onOpen={() => onView({ view: 'agent', id: s.id })}
                  onLocate={onLocate}
                />
              ))}
            </section>
          );
        })}
      </div>
    </aside>
  );
}

function SubagentRow({
  subagent: s,
  setup,
  onOpen,
  onLocate,
}: {
  subagent: Subagent;
  /** "<model> · <effort>", or empty. */
  setup: string;
  onOpen: () => void;
  onLocate: (toolCallId: string) => void;
}) {
  const meta: ReactNode[] = [];
  if (s.started_at) meta.push(<span key="start">Started {clock(s.started_at)}</span>);
  const took = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  if (took) meta.push(<span key="dur">{took}</span>);
  return (
    <div className="agent-item">
      <button type="button" className="agent-item-main" onClick={onOpen} title={s.description || undefined}>
        <span className="agent-item-name">{s.name || 'Subagent'}</span>
        {s.description && <span className="caption agent-item-desc">{s.description}</span>}
        {setup && <span className="caption mono agent-item-setup">{setup}</span>}
        {meta.length > 0 && (
          <span className="caption agent-item-meta num">
            {meta.map((m, k) => (
              <span key={k}>
                {k > 0 && ' · '}
                {m}
              </span>
            ))}
          </span>
        )}
      </button>
      <div className="agent-item-side">
        <AgentChip status={s.status} />
        {s.parent_tool_call_id && (
          <button type="button" className="btn btn-ghost btn-sm" onClick={() => onLocate(s.parent_tool_call_id!)}>
            Spawned by
          </button>
        )}
      </div>
    </div>
  );
}

/**
 * One subagent's transcript. Mounting loads it from the subagent route, after which live
 * frames tagged with this agent_id keep it current (frames during the fetch are buffered
 * and replayed, see state.ts). Reloads on every fresh snapshot to close any gap.
 */
function AgentTranscriptView({
  sessionId,
  subagent,
  transcript,
  snapshotSeq,
  result,
}: {
  sessionId: string;
  subagent: Subagent;
  transcript: AgentTranscript | undefined;
  snapshotSeq: number;
  /** The `task` tool's output on the parent item, which is the subagent's result. */
  result?: string;
}) {
  const { dispatch } = useApp();
  const scroller = useRef<HTMLDivElement>(null);
  const atBottom = useRef(true);
  const live = subagent.status === 'running';

  useEffect(() => {
    let cancelled = false;
    dispatch({ type: 'agent_loading', agentId: subagent.id });
    api
      .subagent(sessionId, subagent.id)
      .then((d) => !cancelled && dispatch({ type: 'agent_loaded', agentId: subagent.id, subagent: d.subagent, items: d.items }))
      .catch((e: unknown) => !cancelled && dispatch({ type: 'agent_failed', agentId: subagent.id, error: describeError(e) }));
    return () => {
      cancelled = true;
    };
  }, [sessionId, subagent.id, snapshotSeq, dispatch]);

  // Follow new output only while the reader is at the bottom.
  const items: Item[] = transcript?.items ?? [];
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [items]);

  function onScroll() {
    const el = scroller.current;
    if (el) atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
  }

  return (
    <div className="panel-body" ref={scroller} onScroll={onScroll} role="log">
      {subagent.description && <p className="caption agent-desc">{subagent.description}</p>}
      {!transcript || transcript.loading ? (
        <p className="caption">
          <Spinner /> Loading transcript…
        </p>
      ) : transcript.error ? (
        <p className="error" role="alert">
          {transcript.error}
        </p>
      ) : (
        <>
          <AgentItems items={items} live={live} />
          {items.length === 0 && <p className="caption">Nothing recorded yet.</p>}
        </>
      )}
      {subagent.status === 'failed' && <p className="error">Failed{subagent.error ? `: ${subagent.error}` : '.'}</p>}
      {subagent.status === 'cancelled' && <p className="caption">Stopped before it finished.</p>}
      {result && (
        <div className="agent-result">
          <div className="label">Result</div>
          <Markdown text={result} />
        </div>
      )}
    </div>
  );
}
