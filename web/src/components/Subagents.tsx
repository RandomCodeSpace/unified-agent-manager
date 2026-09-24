import { ArrowLeft, Bot, Copy, Crosshair, Ellipsis, Square, X } from 'lucide-react';
import { useEffect, useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { LIVE, api, describeError, isStatus, modelName, newRequestId, readOnly, type Interaction, type Item, type Meta, type SessionDetail, type Subagent, type SubagentStatus, type Submission } from '../api';
import { useCopied } from '../lib/clipboard';
import { cn } from '../lib/cn';
import { useResizable } from '../lib/useResizable';
import type { AgentTranscript } from '../state';
import { Markdown, Note, useApp } from './common';
import { AgentChip, AgentItems, duration } from './Transcript';
import { Button } from './ui/button';
import { ContextMenu, Menu, type ActionItem } from './ui/menu';
import { Tip } from './ui/tooltip';

/** "<model> · <effort>" from whatever the provider reported; empty when it reported neither. */
function setupOf(meta: Meta | null, provider: string, s: Subagent): string {
  return [s.model ? modelName(meta, provider, s.model) : '', s.effort ?? ''].filter(Boolean).join(' · ');
}

export type PanelView = { view: 'list' } | { view: 'agent'; id: string };

const GROUPS: { status: SubagentStatus; label: string }[] = [
  { status: 'running', label: 'Running' },
  { status: 'idle', label: 'Idle' },
  { status: 'failed', label: 'Failed' },
  { status: 'completed', label: 'Completed' },
  { status: 'cancelled', label: 'Cancelled' },
];

/** A stop request for one subagent; the provider's SSE still owns its terminal status. */
interface StopState {
  busy: boolean;
  requested: boolean;
  error: string | null;
}
const NOT_STOPPING: StopState = { busy: false, requested: false, error: null };

/** One record of stop requests per panel, so the list row and the transcript header agree. */
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

/**
 * A side panel: inline beside the column at ≥1280px with a draggable inner edge (width
 * remembered per panel), an overlay from the right at 960–1279, a full-screen sheet below.
 */
export function SidePanel({ id, inline, label, children, className, defaultWidth = 440 }: { id: string; inline: boolean; label: string; children: ReactNode; className?: string; defaultWidth?: number }) {
  const { narrow } = useApp();
  const { panelRef, handleProps } = useResizable(id, defaultWidth);
  if (!inline) {
    return (
      <aside role="dialog" aria-modal="true" aria-label={label} className={cn('fixed inset-y-0 right-0 z-40 flex w-full flex-col bg-canvas shadow-modal animate-slide-in', !narrow && 'w-[min(var(--spacing-panel),100vw)] border-l border-hairline', className)}>
        {children}
      </aside>
    );
  }
  return (
    <aside ref={panelRef} aria-label={label} className={cn('relative flex w-(--panel-w) shrink-0 flex-col border-l border-hairline bg-canvas animate-fade-in', className)}>
      <div
        {...handleProps}
        className="group/handle absolute inset-y-0 -left-1 z-10 flex w-2 cursor-col-resize items-center justify-center outline-hidden focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-focus"
        title="Drag to resize · double-click to reset"
      >
        <span aria-hidden="true" className="h-full w-px bg-hairline transition-[background-color,width] duration-100 group-hover/handle:w-0.5 group-hover/handle:bg-accent group-focus-visible/handle:w-0.5 group-focus-visible/handle:bg-accent group-active/handle:bg-accent" />
      </div>
      {children}
    </aside>
  );
}

export function PanelHeader({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn('flex h-header shrink-0 items-center gap-1.5 border-b border-hairline pr-2 pl-3', className)}>{children}</div>;
}

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
  const [stops, stop] = useStops(session.id);
  const current = view.view === 'agent' ? session.subagents.find((s) => s.id === view.id) : undefined;

  // Focus the leading control whenever the view changes (open, back, open transcript).
  useEffect(() => {
    lead.current?.focus();
  }, [view]);

  if (view.view === 'agent' && current) {
    const setup = setupOf(meta, session.provider, current);
    return (
      <SidePanel id="subagents" inline={inline} label={`Subagent ${current.name}`}>
        <PanelHeader>
          <Button ref={lead} size="icon-md" aria-label="Back to the subagent list" className="-ml-1 text-muted" onClick={() => onView({ view: 'list' })}>
            <ArrowLeft />
          </Button>
          <div className="flex min-w-0 flex-1 flex-col">
            <span className="truncate text-title text-ink" title={current.name}>
              {current.name}
            </span>
            {setup && <span className="truncate font-mono text-meta text-muted" title={setup}>{setup}</span>}
          </div>
          <AgentChip status={current.status} />
          <StopSubagent session={session} subagent={current} stopping={stops[current.id] ?? NOT_STOPPING} onStop={() => stop(current.id)} />
          <Button size="icon-md" aria-label="Close subagents" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        </PanelHeader>
        <AgentTranscriptView
          key={current.id}
          sessionId={session.id}
          subagent={current}
          interactions={session.interactions}
          transcript={agents[current.id]}
          snapshotSeq={snapshotSeq}
          result={current.status === 'completed' || current.status === 'idle' ? session.items.find((i) => i.id === current.parent_tool_call_id)?.tool?.output : undefined}
        />
        <SubagentComposer key={`composer-${current.id}`} session={session} subagent={current} />
      </SidePanel>
    );
  }

  const running = session.subagents.filter((s) => s.status === 'running').length;
  return (
    <SidePanel id="subagents" inline={inline} label="Subagents">
      <PanelHeader>
        {narrow && (
          <Button ref={lead} size="icon-md" aria-label="Back to the task" className="-ml-1 text-muted" onClick={onClose}>
            <ArrowLeft />
          </Button>
        )}
        <Bot aria-hidden="true" className="size-4 text-muted" />
        <span className="text-title text-ink">Subagents</span>
        <span className="text-caption tabular-nums text-muted">
          {session.subagents.length}
          {running > 0 && ` · ${running} running`}
        </span>
        <span className="flex-1" />
        {!narrow && (
          <Button ref={lead} size="icon-md" aria-label="Close subagents" className="text-muted" onClick={onClose}>
            <X />
          </Button>
        )}
      </PanelHeader>
      <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-2 py-2">
        {GROUPS.map(({ status, label }) => {
          const rows = session.subagents.filter((s) => s.status === status);
          if (!rows.length) return null;
          return (
            <section key={status} aria-label={label} className="mb-3">
              <div className="flex h-7 items-center gap-2 px-2 text-caption text-muted">
                <span>{label}</span>
                <span className="tabular-nums text-faint">{rows.length}</span>
                <span className="h-px flex-1 bg-hairline" aria-hidden="true" />
              </div>
              <ul className="flex flex-col gap-px">
                {rows.map((s) => (
                  <SubagentRow key={s.id} session={session} subagent={s} setup={setupOf(meta, session.provider, s)} onOpen={() => onView({ view: 'agent', id: s.id })} onLocate={onLocate} stopping={stops[s.id] ?? NOT_STOPPING} onStop={() => stop(s.id)} />
                ))}
              </ul>
            </section>
          );
        })}
      </div>
    </SidePanel>
  );
}

function SubagentRow({
  session,
  subagent: s,
  setup,
  onOpen,
  onLocate,
  stopping,
  onStop,
}: {
  session: SessionDetail;
  subagent: Subagent;
  /** "<model> · <effort>", or empty. */
  setup: string;
  onOpen: () => void;
  onLocate: (toolCallId: string) => void;
  stopping: StopState;
  onStop: () => void;
}) {
  const [, copy] = useCopied();
  const meta: string[] = [];
  if (s.started_at) meta.push(`Started ${clock(s.started_at)}`);
  const took = s.started_at && s.ended_at ? duration(s.started_at, s.ended_at) : null;
  if (took) meta.push(took);
  const canStop = s.status === 'running' && !readOnly(session);

  const items: ActionItem[] = [
    { key: 'open', label: 'Open', icon: <Bot />, onSelect: onOpen },
    ...(s.status === 'running' ? [{ key: 'stop', label: stopping.requested ? 'Stop requested' : 'Stop', icon: <Square />, disabled: !canStop || stopping.busy || stopping.requested, onSelect: onStop }] : []),
    ...(s.parent_tool_call_id ? [{ key: 'locate', label: 'Show where it was spawned', icon: <Crosshair />, onSelect: () => onLocate(s.parent_tool_call_id!) }] : []),
    { key: 'copy', label: 'Copy agent ID', icon: <Copy />, onSelect: () => copy(s.id), separator: true },
  ];

  return (
    <li>
      <ContextMenu.Root>
        <ContextMenu.Trigger render={<div className="group/agent relative rounded-sm transition-colors hover:bg-tint-hover" />}>
          <button type="button" className="flex w-full flex-col items-start gap-0.5 rounded-sm py-2 pr-20 pl-3 text-left focus-visible:-outline-offset-2" onClick={onOpen} title={s.description || undefined}>
            <span className="flex w-full items-center gap-2">
              <span className="min-w-0 flex-1 truncate text-ui font-medium text-ink" title={s.name || undefined}>{s.name || 'Subagent'}</span>
            </span>
            {s.description && <span className="line-clamp-2 text-caption text-muted">{s.description}</span>}
            <span className="flex flex-wrap items-center gap-x-2 text-caption text-muted">
              {setup && <span className="font-mono text-meta">{setup}</span>}
              {meta.length > 0 && <span className="tabular-nums">{meta.join(' · ')}</span>}
            </span>
            {stopping.error && <span className="text-caption text-error">{stopping.error}</span>}
          </button>
          <span className="absolute top-2 right-1.5 flex items-center gap-0.5">
            <AgentChip status={s.status} />
            <Menu.Root modal={false}>
              <Menu.Trigger render={<Button size="icon" aria-label={`Actions for subagent ${s.name}`} className="text-muted opacity-0 transition-opacity group-hover/agent:opacity-100 focus-visible:opacity-100 data-open:opacity-100 pointer-coarse:opacity-100" />}>
                <Ellipsis />
              </Menu.Trigger>
              <Menu.Content align="end">
                <Menu.Actions items={items} />
              </Menu.Content>
            </Menu.Root>
          </span>
        </ContextMenu.Trigger>
        <ContextMenu.Content>
          <ContextMenu.Actions items={items} />
        </ContextMenu.Content>
      </ContextMenu.Root>
    </li>
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
  interactions,
  transcript,
  snapshotSeq,
  result,
}: {
  sessionId: string;
  subagent: Subagent;
  /** The Task's requests; this subagent's are those with its agent_id. */
  interactions: Interaction[];
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
      .then((d) => !cancelled && dispatch({ type: 'agent_loaded', agentId: subagent.id, ...d }))
      .catch((e: unknown) => !cancelled && dispatch({ type: 'agent_failed', agentId: subagent.id, error: describeError(e) }));
    return () => {
      cancelled = true;
    };
  }, [sessionId, subagent.id, snapshotSeq, dispatch]);

  // Follow new output only while the reader is at the bottom.
  const items: Item[] = transcript?.items ?? NO_ITEMS;
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && atBottom.current) el.scrollTop = el.scrollHeight;
  }, [items]);

  function onScroll() {
    const el = scroller.current;
    if (el) atBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < BOTTOM_SLACK;
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto overscroll-contain px-4 py-4" ref={scroller} onScroll={onScroll} role="log">
      {subagent.description && <Note>{subagent.description}</Note>}
      {!transcript || transcript.loading ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          <span className="h-3 w-3/5 rounded-xs bg-sunken animate-pulse-dot" />
          <span className="h-3 w-4/5 rounded-xs bg-sunken animate-pulse-dot" />
          <span className="h-3 w-2/5 rounded-xs bg-sunken animate-pulse-dot" />
          <span className="sr-only">Loading transcript…</span>
        </div>
      ) : transcript.error ? (
        <Note tone="error" role="alert">
          {transcript.error}
        </Note>
      ) : (
        <>
          <AgentItems sessionId={sessionId} agentId={subagent.id} items={items} interactions={interactions} live={live} />
          {items.length === 0 && <Note>Nothing recorded yet.</Note>}
        </>
      )}
      {subagent.status === 'failed' && <Note tone="error">Failed{subagent.error ? `: ${subagent.error}` : '.'}</Note>}
      {subagent.status === 'cancelled' && <Note>Stopped before it finished.</Note>}
      {result && (
        <div className="rounded-md bg-tint-well px-3 py-2 text-ui">
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
function SubagentComposer({ session, subagent }: { session: SessionDetail; subagent: Subagent }) {
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [outcome, setOutcome] = useState<Submission | null>(null);
  const pending = useRef<{ text: string; id: string } | null>(null);
  // An uncertain follow-up keeps the subagent running until the provider settles it; say so meanwhile.
  if (subagent.status !== 'idle') {
    return outcome?.status === 'uncertain' ? (
      <div className="border-t border-hairline px-4 py-2">
        <Note tone="warn" role="alert">
          {UNCERTAIN_FOLLOW_UP}
        </Note>
      </div>
    ) : null;
  }
  const blocked = readOnly(session)
    ? session.stage === 'settled'
      ? 'Settled. Reopen this task to continue the same conversation.'
      : 'Archived. This task is read-only.'
    : LIVE.includes(session.state)
      ? 'Unavailable while the task is running a turn.'
      : null;
  const cannotSubmit = busy || !!blocked || !text.trim();

  async function send() {
    const t = text.trim();
    if (cannotSubmit) return;
    if (!pending.current || pending.current.text !== t) pending.current = { text: t, id: newRequestId() };
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
    <div className="shrink-0 border-t border-hairline p-3">
      <form
        className="flex flex-col rounded-md border border-hairline bg-raised transition-[border-color] focus-within:border-hairline-strong has-[textarea:focus-visible]:outline-2 has-[textarea:focus-visible]:-outline-offset-1 has-[textarea:focus-visible]:outline-focus"
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
function StopSubagent({ session, subagent, stopping, onStop }: { session: SessionDetail; subagent: Subagent; stopping: StopState; onStop: () => void }) {
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
